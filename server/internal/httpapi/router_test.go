package httpapi

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Aashish1107/AshAssistant/server/internal/auth"
)

func TestHealthz(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	rec := send(t, router, request{method: "GET", path: "/healthz"})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); body != "ok\n" {
		t.Errorf("body = %q, want %q", body, "ok\n")
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestHealthzHeadAndOtherMethods(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	if rec := send(t, router, request{method: "HEAD", path: "/healthz"}); rec.Code != http.StatusOK {
		t.Errorf("HEAD status = %d, want 200", rec.Code)
	}
	if rec := send(t, router, request{method: "POST", path: "/healthz"}); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", rec.Code)
	}
}

func TestUnknownPathIsNotFound(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	if rec := send(t, router, request{method: "GET", path: "/nope"}); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	for _, path := range []string{"/", "/healthz", "/api/v1/me", "/nope"} {
		rec := send(t, router, request{method: "GET", path: path})
		for name, want := range securityHeaderValues {
			if got := rec.Header().Get(name); got != want {
				t.Errorf("%s: %s = %q, want %q", path, name, got, want)
			}
		}
		if id := rec.Header().Get("X-Request-Id"); len(id) != 16 {
			t.Errorf("%s: X-Request-Id = %q, want 16 hex characters", path, id)
		}
	}
}

func TestTestPageIsServed(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	rec := send(t, router, request{method: "GET", path: "/"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<script src="/assets/app.js" defer>`) {
		t.Fatalf("GET / = %d:\n%s", rec.Code, rec.Body)
	}
	for path, wantType := range map[string]string{"/assets/app.js": "text/javascript", "/assets/app.css": "text/css"} {
		rec := send(t, router, request{method: "GET", path: path})
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), wantType) {
			t.Errorf("GET %s = %d %q, want 200 %s", path, rec.Code, rec.Header().Get("Content-Type"), wantType)
		}
	}
}

func TestRequestsAreLogged(t *testing.T) {
	router, logs := newTestRouter(t, auth.Options{})
	rec := send(t, router, request{method: "GET", path: "/healthz"})

	for _, want := range []string{"method=GET", "path=/healthz", "status=200", "bytes=3", "client=100.64.0.7", "request_id=" + rec.Header().Get("X-Request-Id")} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log missing %q:\n%s", want, logs)
		}
	}
}

func TestClientIP(t *testing.T) {
	for name, tc := range map[string]struct {
		remote, forwarded, want string
	}{
		"via Tailscale":                    {"127.0.0.1:5000", "100.64.0.7", "100.64.0.7"},
		"via Tailscale, IPv6 loopback":     {"[::1]:5000", "100.64.0.7", "100.64.0.7"},
		"browser on the laptop":            {"127.0.0.1:5000", "", "127.0.0.1"},
		"spoofed entries before Tailscale": {"127.0.0.1:5000", "6.6.6.6, 100.64.0.7", "100.64.0.7"},
		"not from loopback: ignore header": {"192.168.1.20:5000", "100.64.0.7", "192.168.1.20"},
		"garbage header":                   {"127.0.0.1:5000", "not-an-ip", "127.0.0.1"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.remote
		if tc.forwarded != "" {
			r.Header.Set("X-Forwarded-For", tc.forwarded)
		}
		if got := clientIP(r); got != tc.want {
			t.Errorf("%s: clientIP = %q, want %q", name, got, tc.want)
		}
	}
}

func TestPanicsBecome500s(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	handler := withRequestID(recoverPanics(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something broke")
	})))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "something broke") {
		t.Error("panic message leaked to the browser")
	}
	if !strings.Contains(logs.String(), "something broke") || !strings.Contains(logs.String(), "stack=") {
		t.Errorf("panic not logged with a stack:\n%s", logs.String())
	}
}

func TestStatusRecorderDefaultsTo200(t *testing.T) {
	handler := logRequests(slog.New(slog.NewTextHandler(io.Discard, nil)),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
