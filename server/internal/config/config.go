// Package config reads and writes ashcloud.toml, the server's settings file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// FileName is the name of the settings file inside the data folder.
const FileName = "ashcloud.toml"

// DefaultListen keeps the server off the network: only programs on this
// laptop (including Tailscale) can connect.
const DefaultListen = "127.0.0.1:8080"

// DefaultSessionTTL keeps a device logged in for 30 days after it was last used.
const DefaultSessionTTL = "720h"

// ErrNotFound means there is no settings file yet (`ashcloud init` creates it).
var ErrNotFound = errors.New("settings file not found")

// Config is the contents of ashcloud.toml.
type Config struct {
	Listen     string `toml:"listen" comment:"Address the server listens on. Keep it on 127.0.0.1 so only Tailscale can reach it."`
	DataDir    string `toml:"data_dir" comment:"Folder for the database and other server files."`
	StorageDir string `toml:"storage_dir" comment:"Folder that holds your files (one subfolder per user)."`
	LogLevel   string `toml:"log_level" comment:"How much to log: debug, info, warn or error."`
	SessionTTL string `toml:"session_ttl" comment:"How long a device stays logged in after it was last used (720h = 30 days)."`
	FFmpegDir  string `toml:"ffmpeg_dir" comment:"Folder containing ffmpeg and ffprobe, used to switch a video's audio track. Empty: find them on the PATH."`
}

// DefaultDataDir is where `ashcloud init` puts everything unless told otherwise:
// C:\AshCloud on Windows, ~/AshCloud elsewhere.
func DefaultDataDir() string {
	if runtime.GOOS == "windows" {
		drive := os.Getenv("SystemDrive")
		if drive == "" {
			drive = "C:"
		}
		return drive + `\AshCloud`
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "AshCloud"
	}
	return filepath.Join(home, "AshCloud")
}

// DefaultPath is where commands look for the settings file when --config isn't given.
func DefaultPath() string {
	return filepath.Join(DefaultDataDir(), FileName)
}

// Default returns the settings `ashcloud init` writes for dataDir.
func Default(dataDir string) Config {
	return Config{
		Listen:     DefaultListen,
		DataDir:    dataDir,
		StorageDir: filepath.Join(dataDir, "storage"),
		LogLevel:   "info",
		SessionTTL: DefaultSessionTTL,
	}
}

// CacheDir holds files the server can recreate, such as extracted audio tracks.
func (c Config) CacheDir() string {
	return filepath.Join(c.DataDir, "cache")
}

// DatabasePath is the SQLite file inside the data folder.
func (c Config) DatabasePath() string {
	return filepath.Join(c.DataDir, "ashcloud.db")
}

// SlogLevel converts log_level for the logger. Call Validate first.
func (c Config) SlogLevel() slog.Level {
	var level slog.Level
	_ = level.UnmarshalText([]byte(c.LogLevel))
	return level
}

// SessionDuration converts session_ttl. Call Validate first.
func (c Config) SessionDuration() time.Duration {
	d, _ := time.ParseDuration(c.SessionTTL)
	return d
}

// Validate reports every problem with the settings at once.
func (c Config) Validate() error {
	var problems []error
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		problems = append(problems, fmt.Errorf("listen = %q is not an address like %q", c.Listen, DefaultListen))
	}
	if !filepath.IsAbs(c.DataDir) {
		problems = append(problems, fmt.Errorf("data_dir = %q must be a full path", c.DataDir))
	}
	if !filepath.IsAbs(c.StorageDir) {
		problems = append(problems, fmt.Errorf("storage_dir = %q must be a full path", c.StorageDir))
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(c.LogLevel)); err != nil {
		problems = append(problems, fmt.Errorf("log_level = %q must be debug, info, warn or error", c.LogLevel))
	}
	if c.FFmpegDir != "" && !filepath.IsAbs(c.FFmpegDir) {
		problems = append(problems, fmt.Errorf("ffmpeg_dir = %q must be a full path, or empty", c.FFmpegDir))
	}
	if d, err := time.ParseDuration(c.SessionTTL); err != nil || d < time.Hour {
		problems = append(problems, fmt.Errorf("session_ttl = %q must be a duration of at least 1h, like %q", c.SessionTTL, DefaultSessionTTL))
	}
	return errors.Join(problems...)
}

// Load reads and validates the settings file at path. Settings missing from
// the file keep their defaults, so files written by older versions still load.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	if err != nil {
		return Config{}, err
	}

	c := Config{Listen: DefaultListen, LogLevel: "info", SessionTTL: DefaultSessionTTL}
	decoder := toml.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		var unknown *toml.StrictMissingError
		var syntax *toml.DecodeError
		switch {
		case errors.As(err, &unknown):
			return Config{}, fmt.Errorf("%s has a setting Ash Cloud doesn't know (typo?):\n%s", path, unknown.String())
		case errors.As(err, &syntax):
			return Config{}, fmt.Errorf("%s isn't valid TOML:\n%s", path, syntax.String())
		default:
			return Config{}, fmt.Errorf("read %s: %w", path, err)
		}
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s has invalid settings:\n%w", path, err)
	}
	return c, nil
}

// Write saves c as a new settings file at path. It never replaces an
// existing file.
func Write(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	body, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	header := "# Ash Cloud settings. Restart the server after changing this file.\n\n"
	_, err = file.Write(append([]byte(header), body...))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}
