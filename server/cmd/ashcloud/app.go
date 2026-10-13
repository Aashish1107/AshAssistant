package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Aashish1107/AshAssistant/server/internal/config"
	"github.com/Aashish1107/AshAssistant/server/internal/db"
)

// loadConfig reads the settings file, explaining what to do if there isn't one.
func loadConfig(path string) (config.Config, error) {
	cfg, err := config.Load(path)
	if errors.Is(err, config.ErrNotFound) {
		return config.Config{}, fmt.Errorf("no settings file at %s - run 'ashcloud init' first, or pass --config", path)
	}
	return cfg, err
}

// openDatabase opens the database and brings its schema up to date. It
// returns the schema versions it applied.
func openDatabase(ctx context.Context, cfg config.Config) (*sql.DB, []int64, error) {
	database, err := db.Open(ctx, cfg.DatabasePath())
	if err != nil {
		return nil, nil, err
	}
	applied, err := db.Migrate(ctx, database)
	if err != nil {
		database.Close()
		return nil, nil, err
	}
	return database, applied, nil
}
