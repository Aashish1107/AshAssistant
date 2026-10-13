package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Aashish1107/AshAssistant/server/internal/auth"
	"github.com/Aashish1107/AshAssistant/server/internal/config"
	"github.com/Aashish1107/AshAssistant/server/internal/db"
)

const testPassword = "correct horse battery"

// ashcloud runs the CLI in-process and returns its output.
func ashcloud(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err = run(context.Background(), args, strings.NewReader(stdin), &out, &errOut)
	return out.String(), errOut.String(), err
}

// initDataDir runs `ashcloud init` in a temp folder and returns the settings file path.
func initDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, _, err := ashcloud(t, "", "init", "--data-dir", dir); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, config.FileName)
}

func openTestDB(t *testing.T, configPath string) *sql.DB {
	t.Helper()
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(context.Background(), cfg.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// ---- general -----------------------------------------------------------

func TestVersion(t *testing.T) {
	stdout, _, err := ashcloud(t, "", "version")
	if err != nil || strings.TrimSpace(stdout) != version {
		t.Fatalf("got %q, %v; want %q", stdout, err, version)
	}
}

func TestHelpListsEveryCommand(t *testing.T) {
	stdout, _, err := ashcloud(t, "", "help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ashcloud init", "ashcloud user create", "ashcloud serve", "--config FILE"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help missing %q:\n%s", want, stdout)
		}
	}
}

func TestMissingAndUnknownCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"bogus"}} {
		_, stderr, err := ashcloud(t, "", args...)
		if err == nil || !strings.Contains(stderr, "Usage:") {
			t.Errorf("run(%q) = %v with stderr %q, want an error and usage", args, err, stderr)
		}
	}
}

func TestCommandHelpFlagIsNotAnError(t *testing.T) {
	for _, command := range []string{"init", "serve"} {
		_, stderr, err := ashcloud(t, "", command, "-h")
		if err != nil {
			t.Errorf("%s -h = %v, want nil", command, err)
		}
		if !strings.Contains(stderr, "Usage of ashcloud "+command) {
			t.Errorf("%s -h printed %q", command, stderr)
		}
	}
}

// ---- init --------------------------------------------------------------

func TestInitCreatesEverything(t *testing.T) {
	dir := t.TempDir()
	stdout, _, err := ashcloud(t, "", "init", "--data-dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"ashcloud.toml", "ashcloud.db", "storage"} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("%s not created: %v", path, err)
		}
	}
	for _, want := range []string{"(created)", "schema updated to version 1", "ashcloud user create --config"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestInitTwiceKeepsSettings(t *testing.T) {
	configPath := initDataDir(t)
	if err := os.WriteFile(configPath, []byte(strings.Replace(mustRead(t, configPath), "info", "debug", 1)), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := ashcloud(t, "", "init", "--data-dir", filepath.Dir(configPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "already existed, left unchanged") || !strings.Contains(stdout, "already up to date") {
		t.Errorf("second init output:\n%s", stdout)
	}
	if !strings.Contains(mustRead(t, configPath), "debug") {
		t.Error("second init overwrote the settings file")
	}
}

func TestInitStoresFullPaths(t *testing.T) {
	parent := t.TempDir()
	t.Chdir(parent)
	if _, _, err := ashcloud(t, "", "init", "--data-dir", "relative"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(parent, "relative", config.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != filepath.Join(parent, "relative") {
		t.Errorf("data_dir = %q, want a full path", cfg.DataDir)
	}
}

// ---- user create -------------------------------------------------------

func TestUserCreate(t *testing.T) {
	configPath := initDataDir(t)
	stdout, _, err := ashcloud(t, testPassword+"\n"+testPassword+"\n",
		"user", "create", "--config", configPath, "--admin", "Aashish")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `Created admin "aashish" (id 1)`) {
		t.Errorf("output:\n%s", stdout)
	}
	if strings.Contains(stdout, testPassword) {
		t.Error("the password was printed")
	}

	var username, hash, role string
	err = openTestDB(t, configPath).QueryRow("SELECT username, password_hash, role FROM users WHERE id = 1").Scan(&username, &hash, &role)
	if err != nil {
		t.Fatal(err)
	}
	if username != "aashish" || role != "admin" {
		t.Errorf("stored %q %q, want aashish admin", username, role)
	}
	if ok, err := auth.VerifyPassword(testPassword, hash); err != nil || !ok {
		t.Errorf("stored hash doesn't verify: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(configPath), "storage", "users", "1")); err != nil {
		t.Errorf("files folder not created: %v", err)
	}
}

func TestUserCreateFlagsAfterName(t *testing.T) {
	configPath := initDataDir(t)
	stdout, _, err := ashcloud(t, testPassword+"\n"+testPassword+"\n",
		"user", "create", "aashish", "--admin", "--config", configPath)
	if err != nil || !strings.Contains(stdout, "Created admin") {
		t.Fatalf("got %v:\n%s", err, stdout)
	}
}

func TestUserCreateWithoutAdminIsAUser(t *testing.T) {
	configPath := initDataDir(t)
	stdout, _, err := ashcloud(t, testPassword+"\n"+testPassword+"\n", "user", "create", "--config", configPath, "guest")
	if err != nil || !strings.Contains(stdout, `Created user "guest"`) {
		t.Fatalf("got %v:\n%s", err, stdout)
	}
}

func TestUserCreateRejections(t *testing.T) {
	configPath := initDataDir(t)
	if _, _, err := ashcloud(t, testPassword+"\n"+testPassword+"\n", "user", "create", "--config", configPath, "--admin", "aashish"); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		stdin   string
		args    []string
		wantErr string
	}{
		"duplicate name (asks no password)": {"", []string{"AASHISH"}, `a user named "aashish" already exists`},
		"short password":                    {"short\nshort\n", []string{"bob"}, "at least 12 characters"},
		"passwords differ":                  {testPassword + "\n" + testPassword + "!\n", []string{"bob"}, "didn't match"},
		"no password":                       {"", []string{"bob"}, "no password given"},
		"bad username":                      {"", []string{"bob smith"}, "use 1-32 letters"},
		"no name":                           {"", nil, "usage: ashcloud user create"},
		"two names":                         {"", []string{"bob", "alice"}, "usage: ashcloud user create"},
	} {
		args := append([]string{"user", "create", "--config", configPath}, tc.args...)
		_, _, err := ashcloud(t, tc.stdin, args...)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.wantErr)
		}
	}

	var users int
	if err := openTestDB(t, configPath).QueryRow("SELECT count(*) FROM users").Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 1 {
		t.Errorf("%d users exist after the rejected attempts, want 1", users)
	}
}

func TestUserNeedsASubcommand(t *testing.T) {
	_, _, err := ashcloud(t, "", "user", "delete", "bob")
	if err == nil || !strings.Contains(err.Error(), "usage: ashcloud user create") {
		t.Fatalf("got %v, want usage", err)
	}
}

func TestCommandsBeforeInitExplainWhatToDo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), config.FileName)
	for _, args := range [][]string{{"serve", "--config", missing}, {"user", "create", "--config", missing, "bob"}} {
		_, _, err := ashcloud(t, "", args...)
		if err == nil || !strings.Contains(err.Error(), "run 'ashcloud init' first") {
			t.Errorf("%v: got %v, want a hint to run init", args, err)
		}
	}
}

// ---- serve -------------------------------------------------------------

// syncBuffer is a bytes.Buffer safe to write from the server's goroutines
// while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

// startServer runs `ashcloud serve` until the test ends and returns its URL and logs.
func startServer(t *testing.T, configPath string) (string, *syncBuffer) {
	t.Helper()
	addr := freeAddr(t)
	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"serve", "--config", configPath, "--listen", addr}, nil, io.Discard, logs)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve returned %v after shutdown", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("serve didn't stop")
		}
	})

	url := "http://" + addr
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(url + "/healthz")
		if err == nil {
			resp.Body.Close()
			return url, logs
		}
		select {
		case err := <-done:
			t.Fatalf("serve exited early: %v\n%s", err, logs)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never came up:\n%s", logs)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type readyBody struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

func getReady(t *testing.T, url string) (int, readyBody) {
	t.Helper()
	resp, err := http.Get(url + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body readyBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

func TestServeEndToEnd(t *testing.T) {
	configPath := initDataDir(t)
	if _, _, err := ashcloud(t, testPassword+"\n"+testPassword+"\n", "user", "create", "--config", configPath, "--admin", "aashish"); err != nil {
		t.Fatal(err)
	}
	url, logs := startServer(t, configPath)

	status, body := getReady(t, url)
	if status != http.StatusOK || body.Status != "ok" || body.Checks["database"] != "ok" || body.Checks["storage"] != "ok" {
		t.Fatalf("/readyz = %d %+v, want 200 with database and storage ok", status, body)
	}
	if strings.Contains(logs.String(), "no users yet") {
		t.Error("warned about no users, but one exists")
	}

	// Take the files folder away: /readyz must notice.
	cfg, _ := config.Load(configPath)
	if err := os.RemoveAll(cfg.StorageDir); err != nil {
		t.Fatal(err)
	}
	status, body = getReady(t, url)
	if status != http.StatusServiceUnavailable || body.Checks["storage"] != "failed" || body.Checks["database"] != "ok" {
		t.Fatalf("/readyz without storage = %d %+v, want 503 with storage failed", status, body)
	}
	if !strings.Contains(logs.String(), "readiness check failed") {
		t.Errorf("failure not logged:\n%s", logs)
	}
}

func TestServeWarnsWhenThereAreNoUsers(t *testing.T) {
	_, logs := startServer(t, initDataDir(t))
	if !strings.Contains(logs.String(), "no users yet") {
		t.Errorf("no warning about missing users:\n%s", logs)
	}
}

func TestServeReportsBusyPort(t *testing.T) {
	configPath := initDataDir(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()

	_, _, err = ashcloud(t, "", "serve", "--config", configPath, "--listen", busy.Addr().String())
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("can't listen on %s", busy.Addr())) {
		t.Fatalf("got %v, want a can't-listen error", err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
