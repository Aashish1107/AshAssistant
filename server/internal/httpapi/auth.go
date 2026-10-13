package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/Aashish1107/AshAssistant/server/internal/auth"
	"github.com/Aashish1107/AshAssistant/server/internal/drive"
	"github.com/Aashish1107/AshAssistant/server/internal/files"
	"github.com/Aashish1107/AshAssistant/server/internal/media"
)

// sessionCookie holds the session token. It is:
//   - HttpOnly: page scripts can't read it, so injected scripts can't steal it
//   - Secure: only sent over HTTPS (browsers make an exception for 127.0.0.1)
//   - SameSite=Strict: never sent on requests started by another website
const sessionCookie = "ashcloud_session"

// api holds the handlers that need the server's services.
type api struct {
	logger *slog.Logger
	auth   *auth.Service
	files  *files.Store
	drive  *drive.Service
	media  *media.Library
}

type sessionKey struct{}

// sessionFrom returns the session requireLogin attached to the request.
func sessionFrom(ctx context.Context) auth.Session {
	session, _ := ctx.Value(sessionKey{}).(auth.Session)
	return session
}

type userJSON struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type sessionJSON struct {
	User      userJSON  `json:"user"`
	ExpiresAt time.Time `json:"expires_at"`
}

func toSessionJSON(session auth.Session) sessionJSON {
	return sessionJSON{
		User:      userJSON{ID: session.User.ID, Username: session.User.Username, Role: session.User.Role},
		ExpiresAt: session.ExpiresAt.UTC(),
	}
}

func clientFrom(r *http.Request) auth.Client {
	return auth.Client{IP: clientIP(r), UserAgent: r.UserAgent()}
}

func setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   max(1, int(time.Until(expires).Seconds())),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// login: POST /api/v1/auth/login {"username": "...", "password": "..."}
func (a *api) login(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	result, err := a.auth.Login(r.Context(), request.Username, request.Password, clientFrom(r))
	var tooMany *auth.TooManyAttemptsError
	switch {
	case errors.As(err, &tooMany):
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(tooMany.RetryAfter.Seconds()))))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", tooMany.Error())
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Wrong username or password")
	case err != nil:
		a.serverError(w, r, err)
	default:
		infoFrom(r.Context()).userID = result.Session.User.ID
		setSessionCookie(w, result.Token, result.Session.ExpiresAt)
		writeJSON(w, http.StatusOK, toSessionJSON(result.Session))
	}
}

// logout: POST /api/v1/auth/logout. Ends this device's session.
func (a *api) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if err := a.auth.Logout(r.Context(), cookie.Value, clientFrom(r)); err != nil {
			a.serverError(w, r, err)
			return
		}
	}
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// me: GET /api/v1/me. Who is logged in on this device.
func (a *api) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, toSessionJSON(sessionFrom(r.Context())))
}

// requireLogin lets the request through only with a valid session, which it
// attaches to the request context. A session in use gets its expiry pushed
// forward, and the cookie is re-sent to match.
func (a *api) requireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not_logged_in", "Log in first")
			return
		}
		session, err := a.auth.Authenticate(r.Context(), cookie.Value, clientFrom(r))
		if errors.Is(err, auth.ErrNoSession) {
			clearSessionCookie(w) // expired or logged out elsewhere: tidy up the browser
			writeError(w, http.StatusUnauthorized, "not_logged_in", "Your session has ended - log in again")
			return
		}
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		if session.Refreshed {
			setSessionCookie(w, cookie.Value, session.ExpiresAt)
		}
		infoFrom(r.Context()).userID = session.User.ID
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, session)))
	})
}

// serverError logs the real error and tells the browser only that something
// went wrong, with the request ID to find it in the log.
func (a *api) serverError(w http.ResponseWriter, r *http.Request, err error) {
	id := infoFrom(r.Context()).id
	a.logger.Error("request failed", "request_id", id, "path", r.URL.Path, "error", err)
	writeError(w, http.StatusInternalServerError, "internal", "Something went wrong on the server (request "+id+")")
}
