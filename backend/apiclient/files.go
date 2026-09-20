package apiclient

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// The two files the workbench reads: one to attach as a body, one to import.
//
// Both take an absolute path the **user picked in a native dialog**, which is the whole access
// check: the dialog is the consent, and nothing here is reachable with a path the renderer invented.

// ReadFileBase64 reads a file for a binary body or a file form-part.
//
// The MIME type is guessed from the extension and never from the content. A guess from the bytes
// would be better at naming what a file is and worse at doing this job: the value goes out as a
// `Content-Type` header, and what the server should be told is what the user thinks they are
// sending — which is what the extension says.
func ReadFileBase64(path string) (FileBase64, error) {
	content, err := os.ReadFile(path) //nolint:gosec // G304: the path came from a native file dialog
	if err != nil {
		return FileBase64{}, fmt.Errorf("read %s: %w", path, err)
	}

	return FileBase64{
		Base64: base64.StdEncoding.EncodeToString(content),
		Mime:   MimeOf(path),
		Size:   int64(len(content)),
	}, nil
}

// ReadTextFile reads a file as UTF-8, refusing anything that is not.
//
// Refusing is the point: the callers are a collection import and the runner's CSV/JSON data, and
// both would otherwise proceed with U+FFFD where a byte used to be — a parse that half-succeeds and
// produces a collection with mangled names.
func ReadTextFile(path string) (string, error) {
	content, err := os.ReadFile(path) //nolint:gosec // G304: the path came from a native file dialog
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if !utf8.Valid(content) {
		return "", fmt.Errorf("%s is not valid UTF-8 text", path) //nolint:err113 // names the file the user picked
	}
	return string(content), nil
}

// MimeOf guesses a media type from a path's extension.
//
// A small explicit table rather than the standard library's, and the reason is the same one that
// keeps the ticket slug's fold table explicit: `mime.TypeByExtension` reads the host's own registry,
// so the `Content-Type` a request carries would depend on which machine sent it. The types below are
// the ones an API workbench actually attaches.
//
// An unknown extension is `application/octet-stream`, which is the honest answer rather than a
// guess: a server reading it treats the body as opaque bytes, which it is.
func MimeOf(path string) string {
	if known, found := mimeTypes[strings.ToLower(filepath.Ext(path))]; found {
		return known
	}
	return "application/octet-stream"
}

var mimeTypes = map[string]string{
	// Text and data, which is most of what gets attached.
	".json":    "application/json",
	".xml":     "application/xml",
	".txt":     "text/plain",
	".csv":     "text/csv",
	".html":    "text/html",
	".htm":     "text/html",
	".css":     "text/css",
	".js":      "application/javascript",
	".md":      "text/markdown",
	".yaml":    "application/yaml",
	".yml":     "application/yaml",
	".graphql": "application/graphql",
	".proto":   "text/plain",
	".sql":     "application/sql",
	".ndjson":  "application/x-ndjson",
	".form":    "application/x-www-form-urlencoded",
	// Images.
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
	".bmp":  "image/bmp",
	".ico":  "image/x-icon",
	".avif": "image/avif",
	// Documents and archives.
	".pdf":  "application/pdf",
	".zip":  "application/zip",
	".gz":   "application/gzip",
	".tar":  "application/x-tar",
	".7z":   "application/x-7z-compressed",
	".rtf":  "application/rtf",
	".doc":  "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xls":  "application/vnd.ms-excel",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	// Audio and video.
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
	".ogg":  "audio/ogg",
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".mov":  "video/quicktime",
	// Certificates, which the TLS options take.
	".pem": "application/x-pem-file",
	".crt": "application/x-x509-ca-cert",
	".cer": "application/x-x509-ca-cert",
	".p12": "application/x-pkcs12",
	".pfx": "application/x-pkcs12",
}
