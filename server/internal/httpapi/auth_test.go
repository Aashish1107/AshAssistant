package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Aashish1107/AshAssistant/server/internal/auth"
)

func decodeBody[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("response isn't the expected JSON: %v\n%s", err, body)
	}
	return v
}

func wantError(t *testing.T, rec interface {
	Result() *http.Response
}, body []byte, status int, code string) {
	t.Helper()
	if got := rec.Result().StatusCode; got != status {
		t.Fatalf("status = %d, want %d\n%s", got, status, body)
	}
	if got := decodeBody[errorBody](t, body).Error.Code; got != code {
		t.Fatalf("error code = %q, want %q\n%s", got, code, body)
	}
}

func TestLoginSetsASecureSessionCookie(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	rec := send(t, router, request{method: "POST", path: "/api/v1/auth/login",
		body: `{"username": "aashish", "password": "` + adminPassword + `"}`})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}

	body := decodeBody[sessionJSON](t, rec.Body.Bytes())
	if body.User.Username != "aashish" || body.User.Role != "admin" {
		t.Errorf("body = %+v", body)
	}
	if until := time.Until(body.ExpiresAt); until < 29*24*time.Hour || until > 31*24*time.Hour {
		t.Errorf("expires_at = %v, want about 30 days from now", body.ExpiresAt)
	}

	cookie := sessionCookieFrom(t, rec)
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Errorf("cookie flags: HttpOnly=%v Secure=%v SameSite=%v Path=%q; want HttpOnly, Secure, Strict, /",
			cookie.HttpOnly, cookie.Secure, cookie.SameSite, cookie.Path)
	}
	if cookie.MaxAge < 29*24*3600 {
		t.Errorf("cookie Max-Age = %d, want about 30 days", cookie.MaxAge)
	}
	if strings.Contains(rec.Body.String(), cookie.Value) {
		t.Error("the session token is in the response body, where scripts could read it")
	}
}

func TestLoginFailures(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	for name, tc := range map[string]struct {
		req    request
		status int
		code   string
	}{
		"wrong password": {request{body: `{"username": "aashish", "password": "nope"}`}, 401, "invalid_credentials"},
		"unknown user":   {request{body: `{"username": "nobody", "password": "nope"}`}, 401, "invalid_credentials"},
		"not JSON":       {request{body: `username=aashish`}, 400, "bad_request"},
		"unknown field":  {request{body: `{"username": "a", "password": "b", "admin": true}`}, 400, "bad_request"},
		"form post":      {request{body: `{}`, headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}}, 415, "unsupported_media_type"},
		"too large":      {request{body: `{"username": "` + strings.Repeat("a", maxJSONBody) + `"}`}, 413, "too_large"},
	} {
		t.Run(name, func(t *testing.T) {
			tc.req.method, tc.req.path = "POST", "/api/v1/auth/login"
			rec := send(t, router, tc.req)
			wantError(t, rec, rec.Body.Bytes(), tc.status, tc.code)
			if len(rec.Result().Cookies()) != 0 {
				t.Error("a failed login set a cookie")
			}
		})
	}
}

func TestForgedRequestsAreRefused(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	loginBody := `{"username": "aashish", "password": "` + adminPassword + `"}`
	for name, tc := range map[string]struct {
		headers map[string]string
		code    string
	}{
		"from another website":       {map[string]string{"Sec-Fetch-Site": "cross-site"}, "cross_origin"},
		"other origin (old browser)": {map[string]string{"Sec-Fetch-Site": "", "Origin": "https://evil.example"}, "cross_origin"},
		"without the custom header":  {map[string]string{"X-Requested-With": ""}, "missing_header"},
		"with a wrong custom header": {map[string]string{"X-Requested-With": "XMLHttpRequest"}, "missing_header"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := send(t, router, request{method: "POST", path: "/api/v1/auth/login", body: loginBody, headers: tc.headers})
			wantError(t, rec, rec.Body.Bytes(), http.StatusForbidden, tc.code)
		})
	}
}

func TestNonAdminGetsRateLimited(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{MaxFailures: 2})
	wrong := request{method: "POST", path: "/api/v1/auth/login", body: `{"username": "guest", "password": "wrong"}`}
	send(t, router, wrong)
	send(t, router, wrong)

	rec := send(t, router, request{method: "POST", path: "/api/v1/auth/login",
		body: `{"username": "guest", "password": "` + guestPassword + `"}`})
	wantError(t, rec, rec.Body.Bytes(), http.StatusTooManyRequests, "too_many_attempts")
	if retry := rec.Header().Get("Retry-After"); retry == "" || retry == "0" {
		t.Errorf("Retry-After = %q, want seconds to wait", retry)
	}
}

func TestAdminIsNeverRateLimited(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{MaxFailures: 2})
	wrong := request{method: "POST", path: "/api/v1/auth/login", body: `{"username": "aashish", "password": "wrong"}`}
	for range 4 {
		rec := send(t, router, wrong)
		wantError(t, rec, rec.Body.Bytes(), http.StatusUnauthorized, "invalid_credentials")
	}
	login(t, router, "aashish", adminPassword)
}

func TestMe(t *testing.T) {
	router, logs := newTestRouter(t, auth.Options{})

	rec := send(t, router, request{method: "GET", path: "/api/v1/me"})
	wantError(t, rec, rec.Body.Bytes(), http.StatusUnauthorized, "not_logged_in")

	cookie := login(t, router, "aashish", adminPassword)
	rec = send(t, router, request{method: "GET", path: "/api/v1/me", cookie: cookie})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	if got := decodeBody[sessionJSON](t, rec.Body.Bytes()).User; got.Username != "aashish" || got.ID != "1" {
		t.Errorf("user = %+v", got)
	}
	if !strings.Contains(logs.String(), "path=/api/v1/me status=200") || !strings.Contains(logs.String(), "user_id=1") {
		t.Errorf("access log doesn't name the user:\n%s", logs)
	}
}

func TestBadSessionCookieIsCleared(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	rec := send(t, router, request{method: "GET", path: "/api/v1/me", cookie: &http.Cookie{Name: sessionCookie, Value: "forged"}})
	wantError(t, rec, rec.Body.Bytes(), http.StatusUnauthorized, "not_logged_in")
	if cookie := sessionCookieFrom(t, rec); cookie.MaxAge >= 0 {
		t.Errorf("cookie not cleared: %+v", cookie)
	}
}

func TestLogout(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	cookie := login(t, router, "aashish", adminPassword)

	rec := send(t, router, request{method: "POST", path: "/api/v1/auth/logout", cookie: cookie})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body)
	}
	if cleared := sessionCookieFrom(t, rec); cleared.MaxAge >= 0 {
		t.Errorf("cookie not cleared: %+v", cleared)
	}
	// The old cookie no longer works, even if a browser kept it.
	rec = send(t, router, request{method: "GET", path: "/api/v1/me", cookie: cookie})
	wantError(t, rec, rec.Body.Bytes(), http.StatusUnauthorized, "not_logged_in")

	// Logging out when not logged in is fine.
	if rec := send(t, router, request{method: "POST", path: "/api/v1/auth/logout"}); rec.Code != http.StatusNoContent {
		t.Errorf("logout without a session: %d", rec.Code)
	}
}

func TestLogoutNeedsTheCustomHeader(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	cookie := login(t, router, "aashish", adminPassword)
	rec := send(t, router, request{method: "POST", path: "/api/v1/auth/logout", cookie: cookie, headers: map[string]string{"X-Requested-With": ""}})
	wantError(t, rec, rec.Body.Bytes(), http.StatusForbidden, "missing_header")

	rec = send(t, router, request{method: "GET", path: "/api/v1/me", cookie: cookie})
	if rec.Code != http.StatusOK {
		t.Error("a refused logout still logged the user out")
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	router, _ := newTestRouter(t, auth.Options{})
	rec := send(t, router, request{method: "GET", path: "/api/v1/nothing-here"})
	wantError(t, rec, rec.Body.Bytes(), http.StatusNotFound, "not_found")
}
