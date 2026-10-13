package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// ShutdownTimeout is how long in-flight requests get to finish when the
// server is asked to stop.
const ShutdownTimeout = 30 * time.Second

// Serve answers requests on ln until ctx is cancelled, then shuts down
// gracefully: it stops accepting new connections and waits up to
// ShutdownTimeout for in-flight requests to finish.
func Serve(ctx context.Context, ln net.Listener, handler http.Handler, logger *slog.Logger) error {
	srv := &http.Server{
		Handler: handler,
		// Guards against clients that open a connection and never finish
		// sending headers. There is deliberately no overall Read/WriteTimeout:
		// large uploads and downloads must be able to stream for as long as
		// they need.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
