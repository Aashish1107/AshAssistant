package files

import (
	"mime"
	"path"
	"strings"
)

// contentTypes covers the files a phone and laptop usually hold. Our own
// table gives the same answer on every computer; on Windows, Go would
// otherwise ask the registry, which installed programs sometimes change.
var contentTypes = map[string]string{
	// video
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".mov":  "video/quicktime", // iPhone camera videos
	".webm": "video/webm",
	".mkv":  "video/x-matroska",
	".avi":  "video/x-msvideo",
	".3gp":  "video/3gpp",
	// audio
	".mp3":  "audio/mpeg",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".wav":  "audio/wav",
	".flac": "audio/flac",
	".ogg":  "audio/ogg",
	".opus": "audio/ogg",
	// images
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".heic": "image/heic", // iPhone camera photos
	".heif": "image/heif",
	".avif": "image/avif",
	".bmp":  "image/bmp",
	".svg":  "image/svg+xml",
	".ico":  "image/x-icon",
	// documents and text
	".pdf":         "application/pdf",
	".txt":         "text/plain; charset=utf-8",
	".log":         "text/plain; charset=utf-8",
	".md":          "text/markdown; charset=utf-8",
	".csv":         "text/csv; charset=utf-8",
	".json":        "application/json",
	".webmanifest": "application/manifest+json",
	".xml":         "application/xml",
	".html":        "text/html; charset=utf-8",
	".htm":         "text/html; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".py":          "text/x-python; charset=utf-8",
	".go":          "text/x-go; charset=utf-8",
	// archives and office files
	".zip":  "application/zip",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
}

// ContentType returns the media type for a file name, falling back to the
// system's list and then to "application/octet-stream".
func ContentType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if t, ok := contentTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

// Viewing reports how a browser should open a file of this type:
//   - "media": images, video, audio and PDFs play or display in the browser
//   - "text": shown as plain text (never run, even if it's HTML)
//   - "download": everything else is saved instead of opened
func Viewing(contentType string) string {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	switch {
	case strings.HasPrefix(mediaType, "video/"), strings.HasPrefix(mediaType, "audio/"),
		strings.HasPrefix(mediaType, "image/"), mediaType == "application/pdf":
		return "media"
	case strings.HasPrefix(mediaType, "text/"), mediaType == "application/json", mediaType == "application/xml":
		return "text"
	default:
		return "download"
	}
}
