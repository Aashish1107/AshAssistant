package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Aashish1107/AshAssistant/server/internal/drive"
)

// uploadIdleTimeout is how long an upload's connection may send nothing
// before it's treated as dropped (the phone lost signal without the
// connection closing). The browser then continues from where it stopped.
// (A variable so tests can shorten it.)
var uploadIdleTimeout = 60 * time.Second

// offsetHeader carries the byte an upload request continues from.
const offsetHeader = "Upload-Offset"

var errUploadStopped = errors.New("upload stopped")

type uploadJSON struct {
	ID     string     `json:"id"`
	Path   string     `json:"path"`
	Size   int64      `json:"size"`
	Offset int64      `json:"offset"`         // bytes received so far
	File   *entryJSON `json:"file,omitempty"` // the finished file, once complete
}

func toUploadJSON(result drive.Result) uploadJSON {
	out := uploadJSON{ID: result.Upload.ID, Path: result.Upload.Path, Size: result.Upload.Size, Offset: result.Upload.Offset}
	if result.File != nil {
		entry := toEntryJSON(*result.File)
		out.File = &entry
	}
	return out
}

// openUpload: POST /api/v1/uploads/{id}?path=/Photos/IMG_1.jpg&size=123&modified=<unix ms>
// starts an upload, or says where an unfinished one stopped. It checks the
// folder exists and the file fits, so a big upload fails at once instead of
// after sending everything.
func (a *api) openUpload(w http.ResponseWriter, r *http.Request) {
	up, ok := uploadFrom(w, r)
	if !ok {
		return
	}
	result, err := a.drive.Open(r.Context(), sessionFrom(r.Context()).User.ID, up)
	if err != nil {
		a.uploadError(w, r, result, err)
		return
	}
	writeJSON(w, http.StatusOK, toUploadJSON(result))
}

// putUpload: PUT /api/v1/uploads/{id}?path=…&size=…&modified=… with header
// Upload-Offset: <n> sends a file's bytes from byte n on. One request can
// carry the whole file; if it's cut off, what arrived is kept. There's no
// time limit, only an idle timeout.
//
// Replies 201 with the finished file once every byte has arrived, or 200
// with the new offset if the body ended before that.
func (a *api) putUpload(w http.ResponseWriter, r *http.Request) {
	up, ok := uploadFrom(w, r)
	if !ok {
		return
	}
	if value := r.Header.Get(offsetHeader); value != "" {
		offset, err := strconv.ParseInt(value, 10, 64)
		if err != nil || offset < 0 {
			writeError(w, http.StatusBadRequest, "bad_offset", offsetHeader+" must be a byte count")
			return
		}
		up.Offset = offset
	}
	if r.ContentLength > up.Size-up.Offset {
		writeError(w, http.StatusBadRequest, "too_large", "The request has more bytes than the rest of the file")
		return
	}

	body := newUploadBody(w, r.Body)
	result, err := a.drive.Write(r.Context(), sessionFrom(r.Context()).User.ID, clientFrom(r), up, body, body.stop)
	if err != nil {
		a.uploadError(w, r, result, err)
		return
	}
	w.Header().Set(offsetHeader, strconv.FormatInt(result.Upload.Offset, 10))
	status := http.StatusOK
	if result.File != nil {
		status = http.StatusCreated
	}
	writeJSON(w, status, toUploadJSON(result))
}

// cancelUpload: DELETE /api/v1/uploads/{id} stops an upload and deletes
// what it received.
func (a *api) cancelUpload(w http.ResponseWriter, r *http.Request) {
	if err := a.drive.Cancel(r.Context(), sessionFrom(r.Context()).User.ID, r.PathValue("id")); err != nil {
		a.uploadError(w, r, drive.Result{}, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// uploadFrom reads the upload described by the URL. On failure it has
// already sent the error response.
func uploadFrom(w http.ResponseWriter, r *http.Request) (drive.Upload, bool) {
	query := r.URL.Query()
	up := drive.Upload{ID: r.PathValue("id"), Path: query.Get("path")}
	size, err := strconv.ParseInt(query.Get("size"), 10, 64)
	if err != nil || size < 0 {
		writeError(w, http.StatusBadRequest, "bad_size", "size must be the file's size in bytes")
		return up, false
	}
	up.Size = size
	if value := query.Get("modified"); value != "" {
		millis, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_modified", "modified must be a time in milliseconds since 1970")
			return up, false
		}
		if millis > 0 {
			up.Modified = time.UnixMilli(millis)
		}
	}
	return up, true
}

func (a *api) uploadError(w http.ResponseWriter, r *http.Request, result drive.Result, err error) {
	var wrongOffset *drive.OffsetError
	switch {
	case errors.As(err, &wrongOffset):
		w.Header().Set(offsetHeader, strconv.FormatInt(wrongOffset.Offset, 10))
		writeError(w, http.StatusConflict, "wrong_offset", "Continue the upload from byte "+strconv.FormatInt(wrongOffset.Offset, 10))
	case errors.Is(err, drive.ErrUploadNotFound):
		writeError(w, http.StatusNotFound, "upload_not_found", "There's no unfinished upload with that ID - start it from the beginning")
	case errors.Is(err, drive.ErrUploadMismatch):
		writeError(w, http.StatusConflict, "upload_mismatch", "That upload ID belongs to a different file")
	case errors.Is(err, drive.ErrBadSize):
		writeError(w, http.StatusBadRequest, "bad_size", "The size or offset isn't valid")
	case errors.Is(err, drive.ErrTakenOver):
		w.Header().Set(offsetHeader, strconv.FormatInt(result.Upload.Offset, 10))
		writeError(w, http.StatusConflict, "taken_over", "A newer request is continuing this upload")
	case errors.Is(err, drive.ErrInterrupted):
		// Usually nobody is listening any more; the browser will continue.
		a.logger.Info("upload interrupted", "request_id", infoFrom(r.Context()).id, "upload_id", result.Upload.ID,
			"received", result.Upload.Offset, "size", result.Upload.Size, "error", err)
		w.Header().Set(offsetHeader, strconv.FormatInt(result.Upload.Offset, 10))
		writeError(w, http.StatusBadRequest, "interrupted", "The upload was interrupted - continue from byte "+strconv.FormatInt(result.Upload.Offset, 10))
	default:
		a.fileError(w, r, err)
	}
}

// uploadBody is an upload request's body. A read that receives nothing for
// uploadIdleTimeout fails, so a connection that silently died is noticed,
// and stop makes reads fail at once.
type uploadBody struct {
	body       io.Reader
	controller *http.ResponseController

	mu       sync.Mutex
	stopped  bool
	extended time.Time // when the read deadline was last pushed back
}

func newUploadBody(w http.ResponseWriter, body io.Reader) *uploadBody {
	return &uploadBody{body: body, controller: http.NewResponseController(w)}
}

func (b *uploadBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return 0, errUploadStopped
	}
	// Pushing the deadline back is cheap, but not free; once a second is
	// plenty for a 60-second timeout.
	if now := time.Now(); now.Sub(b.extended) >= time.Second {
		b.controller.SetReadDeadline(now.Add(uploadIdleTimeout))
		b.extended = now
	}
	b.mu.Unlock()
	return b.body.Read(p)
}

func (b *uploadBody) stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = true
	b.controller.SetReadDeadline(time.Now()) // wakes a read that's waiting
}
