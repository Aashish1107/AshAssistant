package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Aashish1107/AshAssistant/server/internal/config"
)

// runInit creates the data folder, settings file, files folder and database.
// Running it again is safe: it never overwrites the settings file, and only
// adds anything that's missing.
func runInit(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("init", stderr)
	dataDir := flags.String("data-dir", config.DefaultDataDir(), "folder for the settings file, database and your files")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}

	dir, err := filepath.Abs(*dataDir)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, config.FileName)

	cfg, err := config.Load(path)
	switch {
	case err == nil:
		fmt.Fprintf(stdout, "Settings:  %s (already existed, left unchanged)\n", path)
	case errors.Is(err, config.ErrNotFound):
		cfg = config.Default(dir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := config.Write(path, cfg); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Settings:  %s (created)\n", path)
	default:
		return err
	}

	if err := os.MkdirAll(cfg.StorageDir, 0o700); err != nil {
		return fmt.Errorf("create files folder: %w", err)
	}
	database, applied, err := openDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer database.Close()

	schema := "already up to date"
	if len(applied) > 0 {
		schema = fmt.Sprintf("schema updated to version %d", applied[len(applied)-1])
	}
	fmt.Fprintf(stdout, "Database:  %s (%s)\n", cfg.DatabasePath(), schema)
	fmt.Fprintf(stdout, "Files:     %s\n\n", cfg.StorageDir)

	next := "ashcloud user create --admin <your-name>"
	if path != config.DefaultPath() {
		// Plain quotes, not %q: %q would double every backslash in a Windows path.
		next = `ashcloud user create --config "` + path + `" --admin <your-name>`
	}
	fmt.Fprintf(stdout, "Next, create your login:\n  %s\n", next)
	return nil
}
