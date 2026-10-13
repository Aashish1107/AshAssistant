package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// ---- request info ----------------------------------------------------------

// requestInfo travels with each request so the access log can say which
// request ID it had and who made it.
type requestInfo struct {
	id     string
	userID string // set once a login or session check identifies the user
}

type requestInfoKey struct{}

func infoFrom(ctx context.Context) *requestInfo {
	if info, ok := ctx.Value(requestInfoKey{}).(*requestInfo); ok {
		return info
	}
	return &requestInfo{}
}

// withRequestID gives every request a random ID, returned in X-Request-Id
// and included in every log line about it.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := make([]byte, 8)
		rand.Read(raw)
		info := &requestInfo{id: hex.EncodeToString(raw)}
		w.Header().Set("X-Request-Id", info.id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestInfoKey{}, info)))
	})
}

// clientIP is the address of the device making the request. The server only
// listens on 127.0.0.1, so every connection comes from Tailscale or a browser
// on the laptop itself; Tailscale adds the phone's address to the end of
// X-Forwarded-For. Other senders' X-Forwarded-For is ignored.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			parts := strings.Split(forwarded, ",")
			if last := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(last) != nil {
				return last
			}
		}
	}
	return host
}

// ---- access log --------------------------------------------------------------

// statusRecorder captures the status code and body size a handler writes.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the real writer (for flushing,
// deadlines, etc.).
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// logRequests writes one log line per request.
func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		info := infoFrom(r.Context())
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration", time.Since(start),
			"client", clientIP(r),
			"request_id", info.id,
		}
		if info.userID != "" {
			attrs = append(attrs, "user_id", info.userID)
		}
		logger.Info("request", attrs...)
	})
}

// ---- safety ------------------------------------------------------------------

// recoverPanics turns a crash in a handler into a 500 response and a logged
// stack trace, instead of a dropped connection.
func recoverPanics(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			value := recover()
			if value == nil {
				return
			}
			if value == http.ErrAbortHandler {
				panic(value) // the standard way to abort a response; let net/http handle it
			}
			logger.Error("handler panicked", "request_id", infoFrom(r.Context()).id, "panic", value, "stack", string(debug.Stack()))
			writeError(w, http.StatusInternalServerError, "internal", "Something went wrong on the server")
		}()
		next.ServeHTTP(w, r)
	})
}

var securityHeaderValues = map[string]string{
	// Only this server's own scripts, styles and images; no inline scripts,
	// no plugins, never inside another site's frame.
	"Content-Security-Policy":    "default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'",
	"Strict-Transport-Security":  "max-age=31536000", // browsers only honour it over HTTPS
	"X-Content-Type-Options":     "nosniff",
	"X-Frame-Options":            "DENY",
	"Referrer-Policy":            "no-referrer",
	"Cross-Origin-Opener-Policy": "same-origin",
}

// securityHeaders adds browser protections to every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, value := range securityHeaderValues {
			w.Header().Set(name, value)
		}
		next.ServeHTTP(w, r)
	})
}

// rejectCrossOrigin refuses requests that change something when they come
// from another website (cross-site request forgery). It uses Go's built-in
// protection, which reads the browser's Sec-Fetch-Site and Origin headers.
func rejectCrossOrigin(next http.Handler) http.Handler {
	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusForbidden, "cross_origin", "Requests from other websites aren't allowed")
	}))
	return protection.Handler(next)
}

// csrfHeader must be on every request that changes something. Browsers won't
// let another website add custom headers to a request without this server's
// permission, so it's a second, independent defence against forged requests.
const csrfHeader = "X-Requested-With"

func requireCSRFHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get(csrfHeader) != "ashcloud" {
				writeError(w, http.StatusForbidden, "missing_header", "Requests that change something must send the header "+csrfHeader+": ashcloud")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// withTimeout gives an API request apiTimeout to finish its work.
func withTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), apiTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
