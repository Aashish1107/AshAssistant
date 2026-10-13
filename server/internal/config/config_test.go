package config

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestWriteThenLoadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	want := Default(dir)

	if err := Write(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

func TestWrittenFileIsCommented(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := Write(path, Default(dir)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Ash Cloud settings", "Keep it on 127.0.0.1 so only Tailscale", "listen = '127.0.0.1:8080'"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("settings file missing %q:\n%s", want, data)
		}
	}
}

func TestWindowsPathsSurviveEncoding(t *testing.T) {
	// Backslashes (\n, \t, \a...) must not turn into escape sequences. This
	// checks the TOML encoding directly because Validate only accepts
	// Windows paths when running on Windows.
	want := Config{
		Listen:     DefaultListen,
		DataDir:    `C:\AshCloud`,
		StorageDir: `D:\new\table\it's mine`,
		LogLevel:   "info",
	}
	body, err := toml.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Config
	if err := toml.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("round trip = %+v, want %+v\nTOML:\n%s", got, want, body)
	}
}

func TestWriteRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := Write(path, Default(dir)); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, Default(dir)); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second Write = %v, want os.ErrExist", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), FileName))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load = %v, want ErrNotFound", err)
	}
}

func TestLoadFillsDefaultsForMissingSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	writeFile(t, path, "data_dir = '"+dir+"'\nstorage_dir = '"+dir+"'\n")

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Listen != DefaultListen || got.LogLevel != "info" {
		t.Errorf("defaults not applied: %+v", got)
	}
}

func TestLoadRejectsUnknownSetting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	writeFile(t, path, "data_dir = '"+dir+"'\nstorage_dir = '"+dir+"'\nlisten_adress = '1.2.3.4:5'\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "listen_adress") {
		t.Fatalf("Load = %v, want an error naming listen_adress", err)
	}
}

func TestLoadRejectsBadTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	writeFile(t, path, "listen = \n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "isn't valid TOML") {
		t.Fatalf("Load = %v, want a TOML syntax error", err)
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	err := Config{Listen: "8080", DataDir: "relative", StorageDir: "", LogLevel: "loud"}.Validate()
	if err == nil {
		t.Fatal("Validate = nil, want errors")
	}
	for _, want := range []string{"listen", "data_dir", "storage_dir", "log_level"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error doesn't mention %s: %v", want, err)
		}
	}
}

func TestSlogLevel(t *testing.T) {
	for text, want := range map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "WARN": slog.LevelWarn, "error": slog.LevelError} {
		if got := (Config{LogLevel: text}).SlogLevel(); got != want {
			t.Errorf("SlogLevel(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestDefaultDataDirIsAbsolute(t *testing.T) {
	if dir := DefaultDataDir(); !filepath.IsAbs(dir) {
		t.Errorf("DefaultDataDir() = %q, want a full path", dir)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
