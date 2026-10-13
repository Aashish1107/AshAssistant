package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Aashish1107/AshAssistant/server/internal/auth"
	"github.com/Aashish1107/AshAssistant/server/internal/config"
	"github.com/Aashish1107/AshAssistant/server/internal/db"
	"github.com/Aashish1107/AshAssistant/server/internal/httpapi"
	"github.com/Aashish1107/AshAssistant/server/internal/storage"
)

func runServe(ctx context.Context, args []string, stderr io.Writer) error {
	flags := newFlagSet("serve", stderr)
	configPath := flags.String("config", config.DefaultPath(), "settings file")
	listen := flags.String("listen", "", "address to listen on, overriding the settings file (keep it on 127.0.0.1)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: cfg.SlogLevel()}))

	database, applied, err := openDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer database.Close()
	if len(applied) > 0 {
		logger.Info("database schema updated", "versions", applied)
	}

	// Problems the server can run with, but that you should know about.
	if users, err := db.New(database).CountUsers(ctx); err != nil {
		return err
	} else if users == 0 {
		logger.Warn("no users yet - create yours with: ashcloud user create --admin <your-name>")
	}
	if err := storage.CheckWritable(cfg.StorageDir); err != nil {
		logger.Warn("files folder isn't writable; /readyz will report it", "dir", cfg.StorageDir, "error", err)
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("can't listen on %s (is something else using that port?): %w", cfg.Listen, err)
	}

	readiness := []httpapi.Check{
		{Name: "database", Run: func(ctx context.Context) error { return db.Check(ctx, database) }},
		{Name: "storage", Run: func(context.Context) error { return storage.CheckWritable(cfg.StorageDir) }},
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("Ash Cloud started", "url", "http://"+ln.Addr().String(), "version", version, "settings", *configPath)
	router := httpapi.NewRouter(httpapi.Deps{
		Logger:    logger,
		Readiness: readiness,
		Auth:      auth.NewService(database, auth.Options{SessionTTL: cfg.SessionDuration(), Logger: logger}),
	})
	err = httpapi.Serve(ctx, ln, router, logger)
	logger.Info("Ash Cloud stopped")
	return err
}
