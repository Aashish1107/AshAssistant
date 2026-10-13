package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Aashish1107/AshAssistant/server/internal/db"
)

// Roles a user can have.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

const (
	// Using a session pushes its expiry forward, but at most once per
	// interval, so most requests don't write to the database.
	sessionRefreshInterval = time.Hour

	// Each password check uses 64 MiB of memory for about 0.1 s. Capping how
	// many run at once keeps a burst of logins from exhausting the laptop's
	// memory; extra attempts wait their turn instead of failing.
	maxConcurrentPasswordChecks = 4

	tokenBytes      = 32 // 256 bits of randomness per session token
	maxTokenLength  = 128
	maxDetailLength = 64
	maxAgentLength  = 256
)

var (
	// ErrInvalidCredentials is deliberately vague: it doesn't say whether the
	// username exists.
	ErrInvalidCredentials = errors.New("wrong username or password")
	// ErrNoSession means the request has no valid, unexpired session.
	ErrNoSession = errors.New("not logged in")
)

// TooManyAttemptsError means this client must wait before trying again.
type TooManyAttemptsError struct {
	RetryAfter time.Duration
}

func (e *TooManyAttemptsError) Error() string {
	minutes := int(math.Ceil(e.RetryAfter.Minutes()))
	return fmt.Sprintf("too many wrong passwords from this device - try again in %d minute(s)", minutes)
}

// Options tunes the Service. Zero values get the defaults shown.
type Options struct {
	SessionTTL    time.Duration // how long an unused session lasts (30 days)
	MaxFailures   int           // wrong passwords per client before blocking (5)
	FailureWindow time.Duration // ...within this window (15 minutes)
	Logger        *slog.Logger  // where failed logins are reported (discarded)
}

// Client is who is making a request, for the audit log and rate limiting.
type Client struct {
	IP        string
	UserAgent string
}

// User is the logged-in user.
type User struct {
	ID       int64
	Username string
	Role     string
}

// Session is a valid login on one device.
type Session struct {
	ID        int64
	User      User
	ExpiresAt time.Time
	// Refreshed means the expiry was just pushed forward, so the browser's
	// cookie should be re-sent with the new expiry.
	Refreshed bool
}

// LoginResult is a new session and the token that identifies it. The token
// goes in the browser's cookie; only its hash is stored.
type LoginResult struct {
	Token   string
	Session Session
}

// Service logs users in and out and checks sessions.
type Service struct {
	database   *sql.DB
	queries    *db.Queries
	logger     *slog.Logger
	sessionTTL time.Duration
	limiter    *loginLimiter
	checkSlots chan struct{}
	now        func() time.Time
	// dummyHash is checked against when the username doesn't exist, so a
	// wrong username takes as long as a wrong password and doesn't reveal
	// which usernames exist.
	dummyHash func() string
}

// NewService returns a Service backed by database.
func NewService(database *sql.DB, opts Options) *Service {
	if opts.SessionTTL <= 0 {
		opts.SessionTTL = 30 * 24 * time.Hour
	}
	if opts.MaxFailures <= 0 {
		opts.MaxFailures = 5
	}
	if opts.FailureWindow <= 0 {
		opts.FailureWindow = 15 * time.Minute
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{
		database:   database,
		queries:    db.New(database),
		logger:     opts.Logger,
		sessionTTL: opts.SessionTTL,
		limiter:    newLoginLimiter(opts.MaxFailures, opts.FailureWindow),
		checkSlots: make(chan struct{}, maxConcurrentPasswordChecks),
		now:        time.Now,
		dummyHash:  sync.OnceValue(func() string { return HashPassword("not a real password") }),
	}
}

// Login checks a username and password and, if they match, starts a session.
//
// Wrong passwords are limited per client address (Options.MaxFailures per
// Options.FailureWindow), EXCEPT for admin accounts: by the owner's choice an
// admin is never locked out. Failed admin logins are still logged and
// recorded in the audit log.
func (s *Service) Login(ctx context.Context, username, password string, client Client) (LoginResult, error) {
	now := s.now()
	username = strings.ToLower(strings.TrimSpace(username))

	user, err := s.queries.GetUserByUsername(ctx, username)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return LoginResult{}, err
	}

	limited := !(found && user.Role == RoleAdmin)
	if limited {
		if wait := s.limiter.retryAfter(client.IP, now); wait > 0 {
			s.audit(ctx, sql.NullInt64{}, "login_blocked", username, client, now)
			return LoginResult{}, &TooManyAttemptsError{RetryAfter: wait}
		}
	}

	hash := s.dummyHash()
	if found {
		hash = user.PasswordHash
	}
	matched := false
	if utf8.RuneCountInString(password) <= MaxPasswordLength {
		if matched, err = s.checkPassword(ctx, password, hash); err != nil {
			return LoginResult{}, err
		}
	}

	if !found || !matched {
		if limited {
			s.limiter.recordFailure(client.IP, now)
		}
		var userID sql.NullInt64
		if found {
			userID = sql.NullInt64{Int64: user.ID, Valid: true}
		}
		s.audit(ctx, userID, "login_failed", username, client, now)
		s.logger.Warn("failed login", "username", truncate(username, maxDetailLength), "ip", client.IP, "lockout_applies", limited)
		return LoginResult{}, ErrInvalidCredentials
	}
	if limited {
		s.limiter.reset(client.IP)
	}

	token, tokenHash := newSessionToken()
	expires := now.Add(s.sessionTTL).Unix()
	var sessionID int64
	err = s.inTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteExpiredSessions(ctx, now.Unix()); err != nil {
			return err
		}
		id, err := q.CreateSession(ctx, db.CreateSessionParams{
			UserID:     user.ID,
			TokenHash:  tokenHash,
			UserAgent:  truncate(client.UserAgent, maxAgentLength),
			Ip:         client.IP,
			CreatedAt:  now.Unix(),
			LastSeenAt: now.Unix(),
			ExpiresAt:  expires,
		})
		sessionID = id
		if err != nil {
			return err
		}
		return q.AddAuditEntry(ctx, auditEntry(sql.NullInt64{Int64: user.ID, Valid: true}, "login", "", client, now))
	})
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{
		Token: token,
		Session: Session{
			ID:        sessionID,
			User:      User{ID: user.ID, Username: user.Username, Role: user.Role},
			ExpiresAt: time.Unix(expires, 0),
		},
	}, nil
}

// Authenticate returns the session a token belongs to, or ErrNoSession.
// Using a session pushes its expiry forward, so a device you keep using stays
// logged in.
func (s *Service) Authenticate(ctx context.Context, token string, client Client) (Session, error) {
	now := s.now()
	row, err := s.lookup(ctx, token, now)
	if err != nil {
		return Session{}, err
	}
	session := Session{
		ID:        row.ID,
		User:      User{ID: row.UserID, Username: row.Username, Role: row.Role},
		ExpiresAt: time.Unix(row.ExpiresAt, 0),
	}
	if now.Sub(time.Unix(row.LastSeenAt, 0)) >= sessionRefreshInterval {
		expires := now.Add(s.sessionTTL).Unix()
		err := s.queries.TouchSession(ctx, db.TouchSessionParams{
			LastSeenAt: now.Unix(),
			ExpiresAt:  expires,
			Ip:         client.IP,
			ID:         row.ID,
		})
		if err != nil {
			return Session{}, err
		}
		session.ExpiresAt = time.Unix(expires, 0)
		session.Refreshed = true
	}
	return session, nil
}

// Logout ends the session a token belongs to. Logging out without a valid
// session is not an error.
func (s *Service) Logout(ctx context.Context, token string, client Client) error {
	now := s.now()
	row, err := s.lookup(ctx, token, now)
	if errors.Is(err, ErrNoSession) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteSession(ctx, row.ID); err != nil {
			return err
		}
		return q.AddAuditEntry(ctx, auditEntry(sql.NullInt64{Int64: row.UserID, Valid: true}, "logout", "", client, now))
	})
}

// ResetPassword sets a new password for username and logs out every device
// that user was logged in on. It returns how many sessions were ended.
func (s *Service) ResetPassword(ctx context.Context, username, newPassword string, client Client) (int64, error) {
	if err := CheckPasswordRules(newPassword); err != nil {
		return 0, err
	}
	user, err := s.queries.GetUserByUsername(ctx, strings.ToLower(strings.TrimSpace(username)))
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("there is no user named %q", username)
	}
	if err != nil {
		return 0, err
	}
	hash := HashPassword(newPassword)
	var ended int64
	err = s.inTx(ctx, func(q *db.Queries) error {
		if err := q.SetPasswordHash(ctx, db.SetPasswordHashParams{PasswordHash: hash, ID: user.ID}); err != nil {
			return err
		}
		if ended, err = q.DeleteUserSessions(ctx, user.ID); err != nil {
			return err
		}
		return q.AddAuditEntry(ctx, auditEntry(sql.NullInt64{Int64: user.ID, Valid: true}, "password_reset", "", client, s.now()))
	})
	return ended, err
}

func (s *Service) lookup(ctx context.Context, token string, now time.Time) (db.GetSessionRow, error) {
	if token == "" || len(token) > maxTokenLength {
		return db.GetSessionRow{}, ErrNoSession
	}
	row, err := s.queries.GetSession(ctx, db.GetSessionParams{TokenHash: hashToken(token), ExpiresAt: now.Unix()})
	if errors.Is(err, sql.ErrNoRows) {
		return db.GetSessionRow{}, ErrNoSession
	}
	return row, err
}

// checkPassword runs VerifyPassword, waiting for a free slot first.
func (s *Service) checkPassword(ctx context.Context, password, hash string) (bool, error) {
	select {
	case s.checkSlots <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	defer func() { <-s.checkSlots }()
	return VerifyPassword(password, hash)
}

func (s *Service) inTx(ctx context.Context, fn func(*db.Queries) error) error {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(s.queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

// audit records an event outside a transaction. A failure to record it is
// logged rather than failing the request.
func (s *Service) audit(ctx context.Context, userID sql.NullInt64, action, detail string, client Client, now time.Time) {
	if err := s.queries.AddAuditEntry(ctx, auditEntry(userID, action, detail, client, now)); err != nil {
		s.logger.Error("couldn't write audit log", "action", action, "error", err)
	}
}

func auditEntry(userID sql.NullInt64, action, detail string, client Client, now time.Time) db.AddAuditEntryParams {
	return db.AddAuditEntryParams{
		UserID: userID,
		Action: action,
		Detail: truncate(detail, maxDetailLength),
		Ip:     client.IP,
		At:     now.Unix(),
	}
}

func newSessionToken() (token string, hash []byte) {
	raw := make([]byte, tokenBytes)
	rand.Read(raw) // never fails: since Go 1.24 it crashes the program instead
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, hashToken(token)
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// truncate shortens s to at most n characters.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
