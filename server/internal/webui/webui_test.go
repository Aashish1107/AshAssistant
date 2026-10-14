package webui

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, path string, headers map[string]string) *http.Response {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, r)
	return w.Result()
}

// builtScript finds the app's script, whose name changes with every build.
func builtScript(t *testing.T) string {
	t.Helper()
	matches, _ := fs.Glob(dist, "dist/assets/index-*.js")
	if len(matches) != 1 {
		t.Fatalf("want one built script in dist/assets, found %v (run npm run build in web/)", matches)
	}
	return strings.TrimPrefix(matches[0], "dist")
}

func TestTheAppIsCheckedOnEveryVisit(t *testing.T) {
	resp := get(t, "/", nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("GET / = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(string(body), builtScript(t)) {
		t.Errorf("index.html doesn't load %s", builtScript(t))
	}
	if resp.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("Cache-Control = %q", resp.Header.Get("Cache-Control"))
	}
	again := get(t, "/", map[string]string{"If-None-Match": resp.Header.Get("ETag")})
	if again.StatusCode != http.StatusNotModified {
		t.Errorf("unchanged page = %d, want 304", again.StatusCode)
	}
}

func TestBuiltFilesAreKeptForAYearAndCompressed(t *testing.T) {
	script := builtScript(t)
	plain := get(t, script, nil)
	if plain.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", plain.Header.Get("Cache-Control"))
	}
	if plain.Header.Get("Content-Encoding") != "" || plain.Header.Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Errorf("plain = %q %q", plain.Header.Get("Content-Encoding"), plain.Header.Get("Content-Type"))
	}
	brotli := get(t, script, map[string]string{"Accept-Encoding": "gzip, deflate, br, zstd"})
	gzipped := get(t, script, map[string]string{"Accept-Encoding": "gzip, br;q=0"})
	if brotli.Header.Get("Content-Encoding") != "br" || gzipped.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("encodings = %q, %q", brotli.Header.Get("Content-Encoding"), gzipped.Header.Get("Content-Encoding"))
	}
	if brotli.Header.Get("ETag") == plain.Header.Get("ETag") || brotli.Header.Get("Vary") != "Accept-Encoding" {
		t.Errorf("each encoding needs its own ETag and Vary: %v", brotli.Header)
	}
	if brotli.ContentLength >= plain.ContentLength/2 {
		t.Errorf("brotli %d bytes vs %d plain", brotli.ContentLength, plain.ContentLength)
	}
}

func TestAppFiles(t *testing.T) {
	for path, contentType := range map[string]string{
		"/sw.js":                "text/javascript; charset=utf-8",
		"/manifest.webmanifest": "application/manifest+json",
		"/icons/icon-192.png":   "image/png",
		"/icons/icon.svg":       "image/svg+xml",
	} {
		resp := get(t, path, nil)
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != contentType || resp.Header.Get("Cache-Control") != "no-cache" {
			t.Errorf("%s = %d %q %q", path, resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Cache-Control"))
		}
	}
	for _, path := range []string{"/nope.js", "/assets/", "/sw.js.br", "/index.html.gz"} {
		if resp := get(t, path, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestAcceptsEncoding(t *testing.T) {
	for header, want := range map[string]bool{
		"br":             true,
		"gzip, br":       true,
		"gzip;q=1.0, BR": true,
		"br;q=0":         false,
		"br; q=0.000":    false,
		"gzip":           false,
		"":               false,
		"brotli":         false,
	} {
		if got := acceptsEncoding(header, "br"); got != want {
			t.Errorf("acceptsEncoding(%q, br) = %v", header, got)
		}
	}
}
