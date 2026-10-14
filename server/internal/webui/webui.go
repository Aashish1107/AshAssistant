// Package webui serves the phone app, built into the server binary.
//
// The app's source is in web/ at the top of the repository; `npm run build`
// there writes it into dist/, which is committed, so building the server
// needs only Go.
package webui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Aashish1107/AshAssistant/server/internal/files"
)

//go:embed dist
var dist embed.FS

// asset is one file of the app, ready to send.
type asset struct {
	body        []byte
	brotli      []byte // pre-compressed copies made by the build, if any
	gzip        []byte
	contentType string
	etag        string
	immutable   bool // its name contains a hash of its content
}

// Handler serves the app: "/" is the app itself, plus its files (/assets/,
// /icons/, /sw.js, /manifest.webmanifest).
//
// Files under /assets/ have a hash of their content in their name, so
// browsers keep them for a year without asking again. Everything else is
// checked on every visit, which is cheap: an unchanged file answers
// "304 Not Modified" by its ETag. Text files are sent compressed (brotli or
// gzip), using copies compressed once at build time.
func Handler() http.Handler {
	assets, err := load(dist)
	if err != nil {
		panic(err) // the embedded folder is fixed at build time
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path
		if name == "/" {
			name = "/index.html"
		}
		a, ok := assets[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		header := w.Header()
		header.Set("Content-Type", a.contentType)
		if a.immutable {
			header.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			header.Set("Cache-Control", "no-cache")
		}
		body, etag := a.body, a.etag
		if a.brotli != nil || a.gzip != nil {
			header.Add("Vary", "Accept-Encoding")
			switch accepts := r.Header.Get("Accept-Encoding"); {
			case a.brotli != nil && acceptsEncoding(accepts, "br"):
				header.Set("Content-Encoding", "br")
				body, etag = a.brotli, a.etag+"-br"
			case a.gzip != nil && acceptsEncoding(accepts, "gzip"):
				header.Set("Content-Encoding", "gzip")
				body, etag = a.gzip, a.etag+"-gz"
			}
		}
		header.Set("ETag", `"`+etag+`"`)
		// ServeContent handles If-None-Match (304) and HEAD.
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
	})
}

func load(dist fs.FS) (map[string]*asset, error) {
	assets := make(map[string]*asset)
	err := fs.WalkDir(dist, "dist", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || strings.HasSuffix(name, ".br") || strings.HasSuffix(name, ".gz") {
			return err
		}
		body, err := fs.ReadFile(dist, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		url := strings.TrimPrefix(name, "dist")
		a := &asset{
			body:        body,
			contentType: files.ContentType(path.Base(name)),
			etag:        hex.EncodeToString(sum[:8]),
			immutable:   strings.HasPrefix(url, "/assets/"),
		}
		a.brotli, _ = fs.ReadFile(dist, name+".br")
		a.gzip, _ = fs.ReadFile(dist, name+".gz")
		assets[url] = a
		return nil
	})
	return assets, err
}

// acceptsEncoding reports whether an Accept-Encoding header allows coding.
func acceptsEncoding(header, coding string) bool {
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), coding) {
			continue
		}
		// "br;q=0" means "not br".
		q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
		return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
	}
	return false
}
