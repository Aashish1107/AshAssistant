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
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Aashish1107/AshAssistant/server/internal/db"
	"github.com/Aashish1107/AshAssistant/server/internal/ids"
)

// Roles a user can have.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// FirstAdminID is the ID of the first admin created on an empty server.
// Every other user gets a random Nano ID, so user IDs can't be guessed or
// used to count accounts.
const FirstAdminID = "1"

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

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
	// ErrUsernameTaken means another user already has that name.
	ErrUsernameTaken = errors.New("that username is taken")
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

// User is a user account.
type User struct {
	ID       string // "1" for the first admin, otherwise a Nano ID
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
			s.audit(ctx, sql.NullString{}, "login_blocked", username, client, now)
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
		var userID sql.NullString
		if found {
			userID = sql.NullString{String: user.ID, Valid: true}
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
		return q.AddAuditEntry(ctx, auditEntry(validID(user.ID), "login", "", client, now))
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
		return q.AddAuditEntry(ctx, auditEntry(validID(row.UserID), "logout", "", client, now))
	})
}

// NormalizeUsername lower-cases and trims a username and checks it's allowed:
// 1-32 letters, digits, dots, dashes or underscores, starting with a letter
// or digit.
func NormalizeUsername(username string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(username))
	if !usernamePattern.MatchString(normalized) {
		return "", fmt.Errorf("username %q: use 1-32 letters, digits, dots, dashes or underscores, starting with a letter or digit", username)
	}
	return normalized, nil
}

// CreateUser adds a user. The first admin created on an empty server gets
// the ID "1"; everyone else gets a random Nano ID.
func (s *Service) CreateUser(ctx context.Context, username, password, role string, client Client) (User, error) {
	username, err := NormalizeUsername(username)
	if err != nil {
		return User{}, err
	}
	if role != RoleAdmin && role != RoleUser {
		return User{}, fmt.Errorf("role %q must be %q or %q", role, RoleAdmin, RoleUser)
	}
	if err := CheckPasswordRules(password); err != nil {
		return User{}, err
	}
	hash := HashPassword(password) // slow, so done before taking the write lock

	var user User
	err = s.inTx(ctx, func(q *db.Queries) error {
		existing, err := q.CountUsers(ctx)
		if err != nil {
			return err
		}
		id := ids.New()
		if existing == 0 && role == RoleAdmin {
			id = FirstAdminID
		}
		err = q.CreateUser(ctx, db.CreateUserParams{
			ID:           id,
			Username:     username,
			PasswordHash: hash,
			Role:         role,
			CreatedAt:    s.now().Unix(),
		})
		if db.IsUniqueViolation(err) {
			return ErrUsernameTaken
		}
		if err != nil {
			return err
		}
		user = User{ID: id, Username: username, Role: role}
		return q.AddAuditEntry(ctx, auditEntry(validID(id), "user_created", username, client, s.now()))
	})
	return user, err
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
		return q.AddAuditEntry(ctx, auditEntry(validID(user.ID), "password_reset", "", client, s.now()))
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
func (s *Service) audit(ctx context.Context, userID sql.NullString, action, detail string, client Client, now time.Time) {
	if err := s.queries.AddAuditEntry(ctx, auditEntry(userID, action, detail, client, now)); err != nil {
		s.logger.Error("couldn't write audit log", "action", action, "error", err)
	}
}

func validID(id string) sql.NullString {
	return sql.NullString{String: id, Valid: true}
}

func auditEntry(userID sql.NullString, action, detail string, client Client, now time.Time) db.AddAuditEntryParams {
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
