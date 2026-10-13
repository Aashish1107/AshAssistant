// Package webui serves the browser app, built into the server binary.
//
// For now static/ holds a small test page for logging in and out (Phase 1,
// step 3). Step 6 replaces it with the real app, built from web/.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var static embed.FS

// Handler serves static/index.html at "/" and the files under /assets/.
func Handler() http.Handler {
	files, err := fs.Sub(static, "static")
	if err != nil {
		panic(err) // the embedded folder is fixed at build time
	}
	server := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always check for a newer version; the files are tiny.
		w.Header().Set("Cache-Control", "no-cache")
		server.ServeHTTP(w, r)
	})
}
