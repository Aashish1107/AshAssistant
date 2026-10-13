// Package httpapi holds Ash Cloud's HTTP routes, middleware and server lifecycle.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/Aashish1107/AshAssistant/server/internal/auth"
	"github.com/Aashish1107/AshAssistant/server/internal/webui"
)

const (
	// readyTimeout bounds how long /readyz waits for all its checks.
	readyTimeout = 5 * time.Second
	// apiTimeout bounds every /api request. (File transfers, added later,
	// will get their own limits.)
	apiTimeout = 30 * time.Second
)

// Check is one thing /readyz verifies, such as "the database answers".
type Check struct {
	Name string
	Run  func(ctx context.Context) error
}

// Deps is what the HTTP layer needs from the rest of the server.
type Deps struct {
	Logger    *slog.Logger
	Readiness []Check // what /readyz checks
	Auth      *auth.Service
}

// NewRouter returns the server's HTTP handler: every route, wrapped in the
// middleware chain (outermost first):
//
//	request ID → access log → panic recovery → security headers →
//	cross-origin check → CSRF header check → route (→ login check, API timeout)
func NewRouter(deps Deps) http.Handler {
	a := &api{logger: deps.Logger, auth: deps.Auth}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.Handle("GET /readyz", handleReadyz(deps.Logger, deps.Readiness))

	web := webui.Handler()
	mux.Handle("GET /{$}", web)
	mux.Handle("GET /assets/", web)

	mux.Handle("POST /api/v1/auth/login", withTimeout(http.HandlerFunc(a.login)))
	mux.Handle("POST /api/v1/auth/logout", withTimeout(http.HandlerFunc(a.logout)))
	mux.Handle("GET /api/v1/me", withTimeout(a.requireLogin(http.HandlerFunc(a.me))))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "There's no API endpoint at "+r.URL.Path)
	})

	var handler http.Handler = mux
	handler = requireCSRFHeader(handler)
	handler = rejectCrossOrigin(handler)
	handler = securityHeaders(handler)
	handler = recoverPanics(deps.Logger, handler)
	handler = logRequests(deps.Logger, handler)
	handler = withRequestID(handler)
	return handler
}

// handleHealthz reports that the process is up. It checks nothing else, so it
// stays cheap and answers as long as the server is running.
func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte("ok\n"))
}

type readyResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// handleReadyz reports whether the server can do real work: every check must
// pass for a 200. Failures return 503 and only say "failed"; the reason goes
// to the log, so paths and errors are never shown to the browser.
func handleReadyz(logger *slog.Logger, checks []Check) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()

		response := readyResponse{Status: "ok", Checks: make(map[string]string, len(checks))}
		status := http.StatusOK
		for _, check := range checks {
			if err := check.Run(ctx); err != nil {
				logger.Warn("readiness check failed", "check", check.Name, "error", err)
				response.Checks[check.Name] = "failed"
				response.Status = "unavailable"
				status = http.StatusServiceUnavailable
				continue
			}
			response.Checks[check.Name] = "ok"
		}
		writeJSON(w, status, response)
	}
}
