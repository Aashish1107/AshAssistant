package httpapi

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Aashish1107/AshAssistant/server/internal/media"
)

type audioTrackJSON struct {
	Track    int    `json:"track"`
	Codec    string `json:"codec"`
	Channels int    `json:"channels"`
	Layout   string `json:"layout,omitempty"`
	Language string `json:"language,omitempty"` // ISO 639-2, e.g. "hin"
	Title    string `json:"title,omitempty"`
	Default  bool   `json:"default"`
	// Native is the track the browser plays by itself: the first one. Every
	// other track is played from an extracted copy.
	Native bool `json:"native"`
}

type mediaInfoJSON struct {
	Path            string           `json:"path"`
	DurationSeconds float64          `json:"duration_seconds"`
	AudioTracks     []audioTrackJSON `json:"audio_tracks"`
	CanSwitchAudio  bool             `json:"can_switch_audio"`
	Reason          string           `json:"reason,omitempty"` // why switching isn't possible
}

type audioStatusJSON struct {
	State    string  `json:"state"` // "ready", "preparing" or "failed"
	Progress float64 `json:"progress"`
	Error    string  `json:"error,omitempty"`
	URL      string  `json:"url,omitempty"` // where to play it, once ready
}

type prepareAudioRequest struct {
	Path  string `json:"path"`
	Track int    `json:"track"`
}

// mediaInfo: GET /api/v1/media/info?path=/Videos/movie.mp4 lists a video's
// audio tracks and says whether they can be switched.
func (a *api) mediaInfo(w http.ResponseWriter, r *http.Request) {
	src, ok := a.mediaSource(w, r, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	response := mediaInfoJSON{Path: src.Path, AudioTracks: []audioTrackJSON{}}
	if err := a.media.Available(); err != nil {
		response.Reason = err.Error()
		writeJSON(w, http.StatusOK, response)
		return
	}
	info, ok := a.probe(w, r, src)
	if !ok {
		return
	}
	response.DurationSeconds = info.Duration.Seconds()
	for _, track := range info.AudioTracks {
		response.AudioTracks = append(response.AudioTracks, audioTrackJSON{
			Track:    track.Track,
			Codec:    track.Codec,
			Channels: track.Channels,
			Layout:   track.Layout,
			Language: track.Language,
			Title:    track.Title,
			Default:  track.Default,
			Native:   track.Track == 0,
		})
	}
	response.CanSwitchAudio = len(info.AudioTracks) > 1
	if !response.CanSwitchAudio {
		response.Reason = "This video has only one audio track"
	}
	writeJSON(w, http.StatusOK, response)
}

// prepareAudio: POST /api/v1/media/audio {"path": "...", "track": 1} starts
// extracting a track (if it isn't ready already) and reports progress. The
// player calls it again every second until the state is "ready".
func (a *api) prepareAudio(w http.ResponseWriter, r *http.Request) {
	var request prepareAudioRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	src, info, track, ok := a.mediaTrack(w, r, request.Path, request.Track)
	if !ok {
		return
	}
	status := a.media.Prepare(src, info, track)
	response := audioStatusJSON{State: status.State, Progress: status.Progress, Error: status.Error}
	if status.State == media.StateReady {
		response.URL = "/api/v1/media/audio?path=" + url.QueryEscape(src.Path) + "&track=" + strconv.Itoa(track.Track)
	}
	writeJSON(w, http.StatusOK, response)
}

// audioTrack: GET /api/v1/media/audio?path=...&track=1 plays a prepared
// track, with Range support so the player can seek in it like in the video.
func (a *api) audioTrack(w http.ResponseWriter, r *http.Request) {
	track, err := strconv.Atoi(r.URL.Query().Get("track"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "no_such_track", "track must be a number")
		return
	}
	src, _, chosen, ok := a.mediaTrack(w, r, r.URL.Query().Get("path"), track)
	if !ok {
		return
	}
	cached, contentType, ready := a.media.Ready(src, chosen)
	if !ready {
		writeError(w, http.StatusNotFound, "not_ready", "That audio track isn't prepared yet")
		return
	}
	file, err := os.Open(cached)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	defer file.Close()

	header := w.Header()
	header.Set("Content-Type", contentType)
	header.Set("Cache-Control", "private, no-cache")
	header.Set("Content-Security-Policy", fileSandbox)
	// Tied to the video's version and the track, so it stays the same however
	// often the cached copy is reused.
	header.Set("ETag", fmt.Sprintf(`"%x-%x-a%d"`, src.Size, src.Modified.UnixNano(), chosen.Stream))
	http.ServeContent(w, r, "audio"+filepath.Ext(cached), src.Modified, file)
}

// mediaSource finds the video at path in the user's files.
func (a *api) mediaSource(w http.ResponseWriter, r *http.Request, path string) (media.Source, bool) {
	user := sessionFrom(r.Context()).User
	local, err := a.files.Local(user.ID, path)
	if err != nil {
		a.fileError(w, r, err)
		return media.Source{}, false
	}
	return media.Source{UserID: user.ID, Path: local.Path, Local: local.Local, Size: local.Size, Modified: local.Modified}, true
}

func (a *api) probe(w http.ResponseWriter, r *http.Request, src media.Source) (media.Info, bool) {
	info, err := a.media.Info(r.Context(), src)
	if err != nil {
		a.logger.Warn("couldn't read video", "request_id", infoFrom(r.Context()).id, "path", src.Path, "error", err)
		writeError(w, http.StatusUnprocessableEntity, "not_a_video", "That file isn't a video the server can read")
		return media.Info{}, false
	}
	return info, true
}

// mediaTrack finds the video at path and its audio track number track.
func (a *api) mediaTrack(w http.ResponseWriter, r *http.Request, path string, track int) (media.Source, media.Info, media.AudioTrack, bool) {
	src, ok := a.mediaSource(w, r, path)
	if !ok {
		return media.Source{}, media.Info{}, media.AudioTrack{}, false
	}
	if err := a.media.Available(); err != nil {
		writeError(w, http.StatusConflict, "no_ffmpeg", err.Error())
		return media.Source{}, media.Info{}, media.AudioTrack{}, false
	}
	info, ok := a.probe(w, r, src)
	if !ok {
		return media.Source{}, media.Info{}, media.AudioTrack{}, false
	}
	if track < 0 || track >= len(info.AudioTracks) {
		writeError(w, http.StatusBadRequest, "no_such_track", fmt.Sprintf("This video has %d audio track(s); there's no track %d", len(info.AudioTracks), track))
		return media.Source{}, media.Info{}, media.AudioTrack{}, false
	}
	return src, info, info.AudioTracks[track], true
}
