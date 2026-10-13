// Package httpapi holds Ash Cloud's HTTP routes, middleware and server lifecycle.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// readyTimeout bounds how long /readyz waits for all its checks.
const readyTimeout = 5 * time.Second

// Check is one thing /readyz verifies, such as "the database answers".
type Check struct {
	Name string
	Run  func(ctx context.Context) error
}

// NewRouter returns the server's HTTP handler with every route registered.
// readiness lists what /readyz checks.
func NewRouter(logger *slog.Logger, readiness []Check) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.Handle("GET /readyz", handleReadyz(logger, readiness))
	return logRequests(logger, mux)
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

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.Encode(body)
}
