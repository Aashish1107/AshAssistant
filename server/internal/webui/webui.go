// Package webui serves the browser app, built into the server binary.
//
// For now static/ holds a small test page for logging in and browsing files
// (Phase 1, steps 3-4). Step 6 replaces it with the real app, built from web/.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"

	"github.com/Aashish1107/AshAssistant/server/internal/files"
)

//go:embed static
var static embed.FS

// Handler serves static/index.html at "/" and the files under /assets/.
func Handler() http.Handler {
	site, err := fs.Sub(static, "static")
	if err != nil {
		panic(err) // the embedded folder is fixed at build time
	}
	server := http.FileServerFS(site)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always check for a newer version; the files are tiny.
		w.Header().Set("Cache-Control", "no-cache")
		// Set the type ourselves: on Windows the file server would ask the
		// registry, which some programs change (and with nosniff, a wrong
		// type for app.js would stop the page from working).
		contentType := "text/html; charset=utf-8"
		if r.URL.Path != "/" {
			contentType = files.ContentType(path.Base(r.URL.Path))
		}
		w.Header().Set("Content-Type", contentType)
		server.ServeHTTP(w, r)
	})
}
