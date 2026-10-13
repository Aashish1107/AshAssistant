// Package httpapi holds Ash Cloud's HTTP routes, middleware and server lifecycle.
package httpapi

import (
	"log/slog"
	"net/http"
)

// NewRouter returns the server's HTTP handler with every route registered.
func NewRouter(logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	return logRequests(logger, mux)
}

// handleHealthz reports that the process is up. It checks nothing else, so it
// stays cheap and answers as long as the server is running.
func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte("ok\n"))
}
