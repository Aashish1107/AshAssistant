package httpapi

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"time"

	"github.com/Aashish1107/AshAssistant/server/internal/drive"
	"github.com/Aashish1107/AshAssistant/server/internal/files"
)

// fileSandbox is the Content-Security-Policy for users' files.
const fileSandbox = "sandbox allow-same-origin"

type entryJSON struct {
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Type        string    `json:"type"`           // "folder" or "file"
	Size        *int64    `json:"size,omitempty"` // files only
	Modified    time.Time `json:"modified"`
	ContentType string    `json:"content_type,omitempty"` // files only
}

type listingJSON struct {
	Path    string      `json:"path"`
	Entries []entryJSON `json:"entries"`
}

// listFiles: GET /api/v1/files?path=/Photos lists a folder in the user's files.
func (a *api) listFiles(w http.ResponseWriter, r *http.Request) {
	user := sessionFrom(r.Context()).User
	folder, entries, err := a.files.List(user.ID, r.URL.Query().Get("path"))
	if err != nil {
		a.fileError(w, r, err)
		return
	}
	listing := listingJSON{Path: folder, Entries: make([]entryJSON, 0, len(entries))}
	for _, entry := range entries {
		listing.Entries = append(listing.Entries, toEntryJSON(entry))
	}
	writeJSON(w, http.StatusOK, listing)
}

func toEntryJSON(entry files.Entry) entryJSON {
	item := entryJSON{Name: entry.Name, Path: entry.Path, Type: "folder", Modified: entry.Modified.UTC()}
	if !entry.IsFolder {
		size := entry.Size
		item.Type, item.Size, item.ContentType = "file", &size, entry.ContentType
	}
	return item
}

// fileContent: GET /api/v1/files/content?path=/Videos/trip.mov[&inline=1]
// sends a file. It supports Range requests, so a phone can play a video from
// any point without downloading the rest, and ETags, so the browser can
// reuse what it already has. There's deliberately no time limit: a large
// download takes as long as it takes.
//
// Without inline=1 the browser saves the file. With it, images, video, audio
// and PDFs open in the browser, and text files (including HTML) show as
// plain text.
func (a *api) fileContent(w http.ResponseWriter, r *http.Request) {
	user := sessionFrom(r.Context()).User
	file, err := a.files.Open(user.ID, r.URL.Query().Get("path"))
	if err != nil {
		a.fileError(w, r, err)
		return
	}
	defer file.Close()

	name := path.Base(file.Path)
	contentType := file.ContentType
	disposition := "attachment"
	if inline := r.URL.Query().Get("inline"); inline == "1" || inline == "true" {
		switch files.Viewing(contentType) {
		case "media":
			disposition = "inline"
		case "text":
			disposition, contentType = "inline", "text/plain; charset=utf-8"
		}
	}

	header := w.Header()
	header.Set("Content-Type", contentType)
	if value := mime.FormatMediaType(disposition, map[string]string{"filename": name}); value != "" {
		header.Set("Content-Disposition", value)
	} else {
		header.Set("Content-Disposition", disposition)
	}
	// Size and last-modified time identify this version of the file. If it
	// changes mid-video, the browser notices instead of mixing old and new.
	header.Set("ETag", fmt.Sprintf(`"%x-%x"`, file.Size, file.Modified.UnixNano()))
	header.Set("Cache-Control", "private, no-cache")
	// A file must never act as part of this site: an uploaded .html or .svg
	// could otherwise run scripts with your session. The sandbox disables all
	// scripts, forms and pop-ups. allow-same-origin is needed so that when the
	// browser shows a video or photo on its own page, the player's requests
	// still carry the (SameSite=Strict) session cookie; without scripts it
	// grants nothing else. PDFs are exempt because browsers' PDF viewers
	// refuse to run sandboxed, and they never run page scripts anyway.
	if mediaType, _, _ := mime.ParseMediaType(contentType); mediaType == "application/pdf" {
		header.Del("Content-Security-Policy")
	} else {
		header.Set("Content-Security-Policy", fileSandbox)
	}

	// ServeContent handles Range, If-Range, If-None-Match and HEAD.
	http.ServeContent(w, r, name, file.Modified, file.File)
}

type pathRequest struct {
	Path string `json:"path"`
}

// makeFolder: POST /api/v1/files/folder {"path": "/Photos/2026"}
func (a *api) makeFolder(w http.ResponseWriter, r *http.Request) {
	var request pathRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	entry, err := a.drive.MakeFolder(r.Context(), sessionFrom(r.Context()).User.ID, clientFrom(r), request.Path)
	if err != nil {
		a.fileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toEntryJSON(entry))
}

type moveRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// moveFile: POST /api/v1/files/move {"from": "/a.jpg", "to": "/Photos/b.jpg"}
// renames or moves a file or folder. It never overwrites anything.
func (a *api) moveFile(w http.ResponseWriter, r *http.Request) {
	var request moveRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	entry, err := a.drive.Move(r.Context(), sessionFrom(r.Context()).User.ID, clientFrom(r), request.From, request.To)
	if err != nil {
		a.fileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toEntryJSON(entry))
}

type deletedJSON struct {
	TrashID string `json:"trash_id"` // for POST /api/v1/trash/restore
}

// deleteFile: DELETE /api/v1/files?path=/Photos/old.jpg moves a file or
// folder to the trash, where it stays restorable for 30 days.
func (a *api) deleteFile(w http.ResponseWriter, r *http.Request) {
	trashID, err := a.drive.Delete(r.Context(), sessionFrom(r.Context()).User.ID, clientFrom(r), r.URL.Query().Get("path"))
	if err != nil {
		a.fileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, deletedJSON{TrashID: trashID})
}

type restoreRequest struct {
	ID string `json:"id"`
}

// restoreFile: POST /api/v1/trash/restore {"id": "<trash_id>"} puts a deleted
// item back where it was.
func (a *api) restoreFile(w http.ResponseWriter, r *http.Request) {
	var request restoreRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	entry, err := a.drive.Restore(r.Context(), sessionFrom(r.Context()).User.ID, clientFrom(r), request.ID)
	if errors.Is(err, drive.ErrTrashNotFound) {
		writeError(w, http.StatusNotFound, "not_in_trash", "That item isn't in the trash any more")
		return
	}
	if err != nil {
		a.fileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toEntryJSON(entry))
}

// fileError maps file errors to responses.
func (a *api) fileError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, files.ErrBadPath):
		writeError(w, http.StatusBadRequest, "bad_path", "That path isn't valid. Use / between folder names, with no . or .. parts.")
	case errors.Is(err, files.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "There's nothing at that path")
	case errors.Is(err, files.ErrNotAFolder):
		writeError(w, http.StatusBadRequest, "not_a_folder", "That's a file, not a folder")
	case errors.Is(err, files.ErrIsAFolder):
		writeError(w, http.StatusBadRequest, "is_a_folder", "That's a folder, not a file")
	case errors.Is(err, files.ErrNoAccess):
		writeError(w, http.StatusForbidden, "no_access", "Windows won't let the server use that")
	case errors.Is(err, files.ErrExists):
		writeError(w, http.StatusConflict, "exists", "Something with that name is already there")
	case errors.Is(err, files.ErrInUse):
		writeError(w, http.StatusConflict, "in_use", "That file is open in another program (or still playing) - try again in a moment")
	case errors.Is(err, files.ErrIntoItself):
		writeError(w, http.StatusBadRequest, "into_itself", "A folder can't be moved into itself")
	case errors.Is(err, files.ErrDiskFull):
		writeError(w, http.StatusInsufficientStorage, "disk_full", "The laptop's drive is too full for that")
	default:
		a.serverError(w, r, err)
	}
}
