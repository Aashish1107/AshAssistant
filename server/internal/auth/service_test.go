package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Aashish1107/AshAssistant/server/internal/db"
)

const (
	adminPassword = "admin password 123"
	userPassword  = "user password 456"
)

var (
	phone  = Client{IP: "100.64.0.7", UserAgent: "iPhone Safari"}
	laptop = Client{IP: "100.64.0.8", UserAgent: "Edge"}
)

// cheapHash makes hashes that verify in microseconds; VerifyPassword reads the
// settings from the hash, so the code under test is unchanged.
func cheapHash(password string) string {
	return hashPasswordWith(password, 8, 1, 1)
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newTestService returns a Service on a fresh database with an admin
// "aashish" and a regular user "guest".
func newTestService(t *testing.T, opts Options) (*Service, *fakeClock, *sql.DB) {
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
	for _, u := range []struct{ name, password, role string }{
		{"aashish", adminPassword, RoleAdmin},
		{"guest", userPassword, RoleUser},
	} {
		if _, err := queries.CreateUser(ctx, db.CreateUserParams{Username: u.name, PasswordHash: cheapHash(u.password), Role: u.role, CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}

	service := NewService(database, opts)
	clock := &fakeClock{t: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
	service.now = clock.now
	dummy := cheapHash("dummy")
	service.dummyHash = func() string { return dummy }
	return service, clock, database
}

func auditActions(t *testing.T, database *sql.DB) []string {
	t.Helper()
	rows, err := database.Query("SELECT action FROM audit_log ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action string
		rows.Scan(&action)
		actions = append(actions, action)
	}
	return actions
}

// ---- login ---------------------------------------------------------------

func TestLoginSucceeds(t *testing.T) {
	service, clock, database := newTestService(t, Options{})
	result, err := service.Login(context.Background(), "  Aashish ", adminPassword, phone)
	if err != nil {
		t.Fatal(err)
	}
	if result.Token == "" || len(result.Token) < 40 {
		t.Errorf("token = %q, want a long random token", result.Token)
	}
	want := User{ID: 1, Username: "aashish", Role: RoleAdmin}
	if result.Session.User != want {
		t.Errorf("user = %+v, want %+v", result.Session.User, want)
	}
	if got, want := result.Session.ExpiresAt, clock.t.Add(30*24*time.Hour); !got.Equal(want) {
		t.Errorf("expires = %v, want %v", got, want)
	}

	var storedHash []byte
	var agent, ip string
	if err := database.QueryRow("SELECT token_hash, user_agent, ip FROM sessions").Scan(&storedHash, &agent, &ip); err != nil {
		t.Fatal(err)
	}
	if string(storedHash) == result.Token {
		t.Error("the raw token was stored instead of its hash")
	}
	if agent != phone.UserAgent || ip != phone.IP {
		t.Errorf("stored agent/ip = %q/%q", agent, ip)
	}
	if got := auditActions(t, database); len(got) != 1 || got[0] != "login" {
		t.Errorf("audit = %v, want [login]", got)
	}
}

func TestLoginTokensAreUnique(t *testing.T) {
	service, _, _ := newTestService(t, Options{})
	first, _ := service.Login(context.Background(), "aashish", adminPassword, phone)
	second, _ := service.Login(context.Background(), "aashish", adminPassword, laptop)
	if first.Token == second.Token {
		t.Fatal("two logins got the same token")
	}
}

func TestLoginWrongPasswordAndUnknownUserLookTheSame(t *testing.T) {
	service, _, database := newTestService(t, Options{})
	ctx := context.Background()

	_, wrongPassword := service.Login(ctx, "aashish", "not the password", phone)
	_, unknownUser := service.Login(ctx, "nobody", adminPassword, phone)
	for _, err := range []error{wrongPassword, unknownUser} {
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("got %v, want ErrInvalidCredentials", err)
		}
	}

	var withUser, withoutUser int
	database.QueryRow("SELECT count(*) FROM audit_log WHERE action = 'login_failed' AND user_id = 1").Scan(&withUser)
	database.QueryRow("SELECT count(*) FROM audit_log WHERE action = 'login_failed' AND user_id IS NULL AND detail = 'nobody'").Scan(&withoutUser)
	if withUser != 1 || withoutUser != 1 {
		t.Errorf("failed logins audited: %d for aashish, %d for nobody; want 1 each", withUser, withoutUser)
	}
}

func TestLoginRejectsOverlongPassword(t *testing.T) {
	service, _, _ := newTestService(t, Options{})
	long := string(make([]rune, MaxPasswordLength+1))
	if _, err := service.Login(context.Background(), "aashish", long, phone); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("got %v, want ErrInvalidCredentials", err)
	}
}

// ---- lockout ---------------------------------------------------------------

func TestNonAdminIsBlockedAfterTooManyFailures(t *testing.T) {
	service, clock, database := newTestService(t, Options{MaxFailures: 3, FailureWindow: 10 * time.Minute})
	ctx := context.Background()

	for i := range 3 {
		if _, err := service.Login(ctx, "guest", "wrong", phone); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
		clock.advance(time.Minute)
	}

	// Even the right password is refused while blocked.
	_, err := service.Login(ctx, "guest", userPassword, phone)
	var tooMany *TooManyAttemptsError
	if !errors.As(err, &tooMany) {
		t.Fatalf("got %v, want TooManyAttemptsError", err)
	}
	// The first failure was 3 minutes ago; it ages out after 10.
	if tooMany.RetryAfter != 7*time.Minute {
		t.Errorf("RetryAfter = %v, want 7m", tooMany.RetryAfter)
	}

	// Another device isn't affected.
	if _, err := service.Login(ctx, "guest", userPassword, laptop); err != nil {
		t.Fatalf("other device: %v", err)
	}

	// Once the window passes, the phone can log in again.
	clock.advance(7 * time.Minute)
	if _, err := service.Login(ctx, "guest", userPassword, phone); err != nil {
		t.Fatalf("after waiting: %v", err)
	}

	got := auditActions(t, database)
	if got[3] != "login_blocked" {
		t.Errorf("audit = %v, want the blocked attempt recorded", got)
	}
}

func TestUnknownUsernamesCountTowardsTheLimit(t *testing.T) {
	service, _, _ := newTestService(t, Options{MaxFailures: 2})
	ctx := context.Background()
	service.Login(ctx, "nobody", "x", phone)
	service.Login(ctx, "someone", "x", phone)

	var tooMany *TooManyAttemptsError
	if _, err := service.Login(ctx, "guest", userPassword, phone); !errors.As(err, &tooMany) {
		t.Fatalf("got %v, want TooManyAttemptsError", err)
	}
}

func TestAdminIsNeverLockedOut(t *testing.T) {
	service, _, database := newTestService(t, Options{MaxFailures: 3})
	ctx := context.Background()

	for i := range 20 {
		if _, err := service.Login(ctx, "aashish", "wrong guess", phone); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: got %v, want a plain wrong-password error", i+1, err)
		}
	}
	if _, err := service.Login(ctx, "aashish", adminPassword, phone); err != nil {
		t.Fatalf("admin was locked out: %v", err)
	}

	var failed int
	database.QueryRow("SELECT count(*) FROM audit_log WHERE action = 'login_failed' AND user_id = 1").Scan(&failed)
	if failed != 20 {
		t.Errorf("%d failed admin logins audited, want 20", failed)
	}
}

func TestAdminLoginWorksFromABlockedDevice(t *testing.T) {
	service, _, _ := newTestService(t, Options{MaxFailures: 2})
	ctx := context.Background()
	service.Login(ctx, "guest", "wrong", phone)
	service.Login(ctx, "guest", "wrong", phone)

	var tooMany *TooManyAttemptsError
	if _, err := service.Login(ctx, "guest", userPassword, phone); !errors.As(err, &tooMany) {
		t.Fatalf("guest: got %v, want blocked", err)
	}
	if _, err := service.Login(ctx, "aashish", adminPassword, phone); err != nil {
		t.Fatalf("admin from the same device: %v", err)
	}
}

func TestSuccessfulLoginClearsFailures(t *testing.T) {
	service, _, _ := newTestService(t, Options{MaxFailures: 3})
	ctx := context.Background()
	service.Login(ctx, "guest", "wrong", phone)
	service.Login(ctx, "guest", "wrong", phone)
	if _, err := service.Login(ctx, "guest", userPassword, phone); err != nil {
		t.Fatal(err)
	}
	service.Login(ctx, "guest", "wrong", phone)
	service.Login(ctx, "guest", "wrong", phone)
	if _, err := service.Login(ctx, "guest", userPassword, phone); err != nil {
		t.Fatalf("failures before the successful login still counted: %v", err)
	}
}

func TestPasswordChecksAreCapped(t *testing.T) {
	service, _, _ := newTestService(t, Options{})
	var running, peak atomic.Int32
	slow := func(ctx context.Context) {
		service.checkSlots <- struct{}{}
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		<-service.checkSlots
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() { slow(context.Background()) })
	}
	wg.Wait()
	if peak.Load() > maxConcurrentPasswordChecks {
		t.Fatalf("%d checks ran at once, want at most %d", peak.Load(), maxConcurrentPasswordChecks)
	}
}

func TestPasswordCheckGivesUpWhenRequestIsCancelled(t *testing.T) {
	service, _, _ := newTestService(t, Options{})
	for range maxConcurrentPasswordChecks {
		service.checkSlots <- struct{}{} // every slot busy
	}
	// The user lookup finishes in time; waiting for a free slot doesn't.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := service.Login(ctx, "aashish", adminPassword, phone); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want context.DeadlineExceeded", err)
	}
}

// ---- sessions --------------------------------------------------------------

func TestAuthenticate(t *testing.T) {
	service, _, _ := newTestService(t, Options{})
	ctx := context.Background()
	login, err := service.Login(ctx, "aashish", adminPassword, phone)
	if err != nil {
		t.Fatal(err)
	}

	session, err := service.Authenticate(ctx, login.Token, phone)
	if err != nil {
		t.Fatal(err)
	}
	if session.User.Username != "aashish" || session.ID != login.Session.ID || session.Refreshed {
		t.Errorf("session = %+v", session)
	}

	for _, bad := range []string{"", "garbage", login.Token + "x", string(make([]byte, 500))} {
		if _, err := service.Authenticate(ctx, bad, phone); !errors.Is(err, ErrNoSession) {
			t.Errorf("token %.10q: got %v, want ErrNoSession", bad, err)
		}
	}
}

func TestSessionsExpireWhenUnused(t *testing.T) {
	service, clock, _ := newTestService(t, Options{SessionTTL: 48 * time.Hour})
	ctx := context.Background()
	login, _ := service.Login(ctx, "aashish", adminPassword, phone)

	clock.advance(48*time.Hour - time.Second)
	if _, err := service.Authenticate(ctx, login.Token, phone); err != nil {
		t.Fatalf("just before expiry: %v", err)
	}
	// That use extended it; let it lapse completely.
	clock.advance(48 * time.Hour)
	if _, err := service.Authenticate(ctx, login.Token, phone); !errors.Is(err, ErrNoSession) {
		t.Fatalf("after expiry: got %v, want ErrNoSession", err)
	}
}

func TestUsingASessionExtendsItAtMostHourly(t *testing.T) {
	service, clock, database := newTestService(t, Options{SessionTTL: 48 * time.Hour})
	ctx := context.Background()
	login, _ := service.Login(ctx, "aashish", adminPassword, phone)

	clock.advance(30 * time.Minute)
	session, _ := service.Authenticate(ctx, login.Token, laptop)
	if session.Refreshed {
		t.Error("refreshed after 30 minutes; want at most once an hour")
	}

	clock.advance(31 * time.Minute)
	session, _ = service.Authenticate(ctx, login.Token, laptop)
	if !session.Refreshed || !session.ExpiresAt.Equal(clock.t.Add(48*time.Hour)) {
		t.Errorf("after 61 minutes: refreshed=%v expires=%v, want refreshed to %v", session.Refreshed, session.ExpiresAt, clock.t.Add(48*time.Hour))
	}
	var ip string
	database.QueryRow("SELECT ip FROM sessions").Scan(&ip)
	if ip != laptop.IP {
		t.Errorf("session ip = %q, want the latest address %q", ip, laptop.IP)
	}
}

func TestLoginCleansUpExpiredSessions(t *testing.T) {
	service, clock, database := newTestService(t, Options{SessionTTL: time.Hour})
	ctx := context.Background()
	service.Login(ctx, "aashish", adminPassword, phone)
	clock.advance(2 * time.Hour)
	service.Login(ctx, "aashish", adminPassword, phone)

	var count int
	database.QueryRow("SELECT count(*) FROM sessions").Scan(&count)
	if count != 1 {
		t.Errorf("%d sessions stored, want only the live one", count)
	}
}

func TestLogout(t *testing.T) {
	service, _, database := newTestService(t, Options{})
	ctx := context.Background()
	login, _ := service.Login(ctx, "aashish", adminPassword, phone)
	other, _ := service.Login(ctx, "aashish", adminPassword, laptop)

	if err := service.Logout(ctx, login.Token, phone); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, login.Token, phone); !errors.Is(err, ErrNoSession) {
		t.Errorf("session still valid after logout: %v", err)
	}
	if _, err := service.Authenticate(ctx, other.Token, laptop); err != nil {
		t.Errorf("logging out the phone logged out the laptop: %v", err)
	}
	// Logging out again, or with no session, is fine.
	if err := service.Logout(ctx, login.Token, phone); err != nil {
		t.Errorf("second logout: %v", err)
	}
	if err := service.Logout(ctx, "", phone); err != nil {
		t.Errorf("logout without a session: %v", err)
	}
	if got := auditActions(t, database); got[len(got)-1] != "logout" {
		t.Errorf("audit = %v, want logout last", got)
	}
}

func TestResetPassword(t *testing.T) {
	service, _, database := newTestService(t, Options{})
	ctx := context.Background()
	phoneLogin, _ := service.Login(ctx, "aashish", adminPassword, phone)
	service.Login(ctx, "aashish", adminPassword, laptop)
	guestLogin, _ := service.Login(ctx, "guest", userPassword, phone)

	ended, err := service.ResetPassword(ctx, "Aashish", "brand new password", Client{IP: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	if ended != 2 {
		t.Errorf("ended %d sessions, want 2", ended)
	}
	if _, err := service.Authenticate(ctx, phoneLogin.Token, phone); !errors.Is(err, ErrNoSession) {
		t.Error("old session survived the password reset")
	}
	if _, err := service.Authenticate(ctx, guestLogin.Token, phone); err != nil {
		t.Errorf("another user's session was ended: %v", err)
	}
	if _, err := service.Login(ctx, "aashish", adminPassword, phone); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("old password still works: %v", err)
	}
	if _, err := service.Login(ctx, "aashish", "brand new password", phone); err != nil {
		t.Errorf("new password doesn't work: %v", err)
	}
	if got := auditActions(t, database); !contains(got, "password_reset") {
		t.Errorf("audit = %v, want password_reset", got)
	}

	if _, err := service.ResetPassword(ctx, "aashish", "short", Client{}); err == nil {
		t.Error("short password accepted")
	}
	if _, err := service.ResetPassword(ctx, "nobody", "long enough password", Client{}); err == nil {
		t.Error("reset for a missing user succeeded")
	}
}

func TestTooManyAttemptsMessageRoundsUp(t *testing.T) {
	err := &TooManyAttemptsError{RetryAfter: 61 * time.Second}
	if got := err.Error(); got != "too many wrong passwords from this device - try again in 2 minute(s)" {
		t.Errorf("message = %q", got)
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
