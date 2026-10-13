package httpapi

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Aashish1107/AshAssistant/server/internal/auth"
	"github.com/Aashish1107/AshAssistant/server/internal/db"
)

const (
	adminPassword = "admin password 123"
	guestPassword = "guest password 456"
	guestID       = "V1StGXR8_Z5jdHi6B-myT"
)

// Real argon2id hashes are slow to make, so make each one once per test run.
var (
	adminHash = sync.OnceValue(func() string { return auth.HashPassword(adminPassword) })
	guestHash = sync.OnceValue(func() string { return auth.HashPassword(guestPassword) })
)

// syncBuffer is a bytes.Buffer that's safe to write from server goroutines
// while a test reads it.
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

// newTestRouter returns the full router on a fresh database holding an admin
// "aashish" and a regular user "guest", plus everything it logged.
func newTestRouter(t *testing.T, opts auth.Options, checks ...Check) (http.Handler, *syncBuffer) {
	t.Helper()
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err := db.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	queries := db.New(database)
	queries.CreateUser(ctx, db.CreateUserParams{ID: auth.FirstAdminID, Username: "aashish", PasswordHash: adminHash(), Role: auth.RoleAdmin, CreatedAt: 1})
	queries.CreateUser(ctx, db.CreateUserParams{ID: guestID, Username: "guest", PasswordHash: guestHash(), Role: auth.RoleUser, CreatedAt: 1})

	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	opts.Logger = logger
	router := NewRouter(Deps{Logger: logger, Readiness: checks, Auth: auth.NewService(database, opts)})
	return router, logs
}

// request describes one call to the router, as a browser on this site would
// make it unless a test says otherwise.
type request struct {
	method  string
	path    string
	body    string
	cookie  *http.Cookie
	headers map[string]string
}

func send(t *testing.T, router http.Handler, req request) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if req.body != "" {
		body = strings.NewReader(req.body)
	}
	r := httptest.NewRequest(req.method, req.path, body)
	r.RemoteAddr = "127.0.0.1:50000" // as if forwarded by Tailscale
	r.Header.Set("X-Forwarded-For", "100.64.0.7")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if req.method != http.MethodGet && req.method != http.MethodHead {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Requested-With", "ashcloud")
	}
	for name, value := range req.headers {
		if value == "" {
			r.Header.Del(name)
		} else {
			r.Header.Set(name, value)
		}
	}
	if req.cookie != nil {
		r.AddCookie(req.cookie)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	return rec
}

// login logs in and returns the session cookie.
func login(t *testing.T, router http.Handler, username, password string) *http.Cookie {
	t.Helper()
	rec := send(t, router, request{method: "POST", path: "/api/v1/auth/login",
		body: `{"username": "` + username + `", "password": "` + password + `"}`})
	if rec.Code != http.StatusOK {
		t.Fatalf("login as %s: %d %s", username, rec.Code, rec.Body)
	}
	return sessionCookieFrom(t, rec)
}

func sessionCookieFrom(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == sessionCookie {
			return cookie
		}
	}
	t.Fatalf("no %s cookie in response; headers: %v", sessionCookie, rec.Header())
	return nil
}
