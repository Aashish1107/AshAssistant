package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Aashish1107/AshAssistant/server/internal/auth"
)

func getReadyz(t *testing.T, checks []Check) (*httptest.ResponseRecorder, readyResponse, string) {
	t.Helper()
	router, logs := newTestRouter(t, auth.Options{}, checks...)
	rec := send(t, router, request{method: "GET", path: "/readyz"})

	var body readyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response isn't JSON: %v\n%s", err, rec.Body)
	}
	return rec, body, logs.String()
}

func passing(ctx context.Context) error { return nil }

func TestReadyzAllChecksPass(t *testing.T) {
	rec, body, _ := getReadyz(t, []Check{{"database", passing}, {"storage", passing}})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body.Status != "ok" || body.Checks["database"] != "ok" || body.Checks["storage"] != "ok" {
		t.Errorf("body = %+v, want everything ok", body)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
}

func TestReadyzOneCheckFails(t *testing.T) {
	failing := func(ctx context.Context) error { return errors.New(`open C:\AshCloud\storage: access denied`) }
	rec, body, logs := getReadyz(t, []Check{{"database", passing}, {"storage", failing}})

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if body.Status != "unavailable" || body.Checks["database"] != "ok" || body.Checks["storage"] != "failed" {
		t.Errorf("body = %+v, want database ok and storage failed", body)
	}
	if strings.Contains(rec.Body.String(), "access denied") {
		t.Error("the error detail leaked into the response")
	}
	if !strings.Contains(logs, "access denied") || !strings.Contains(logs, "check=storage") {
		t.Errorf("the error detail wasn't logged:\n%s", logs)
	}
}

func TestReadyzWithNoChecksIsReady(t *testing.T) {
	rec, body, _ := getReadyz(t, nil)
	if rec.Code != http.StatusOK || body.Status != "ok" {
		t.Fatalf("got %d %+v, want 200 ok", rec.Code, body)
	}
}

func TestReadyzChecksHaveADeadline(t *testing.T) {
	var deadline time.Time
	var hasDeadline bool
	check := func(ctx context.Context) error {
		deadline, hasDeadline = ctx.Deadline()
		return nil
	}
	getReadyz(t, []Check{{"slow", check}})

	if !hasDeadline || time.Until(deadline) > readyTimeout {
		t.Fatalf("check context deadline = %v (set: %v), want within %v", deadline, hasDeadline, readyTimeout)
	}
}
