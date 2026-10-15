package archify

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// Showing a rendered page inside the app (ARCHIFY-004).
//
// The page is Archify's own self-contained HTML — inline script and style, its font embedded — so
// it cannot be loaded as `srcdoc`: a srcdoc frame takes the app's policy, which forbids inline
// script. The asset server serves it instead, from the app's data folder, under a policy of its
// own that lets its inline code run and reach nothing at all. The renderer frames it with
// `sandbox="allow-scripts"` and no `allow-same-origin`, so it runs in an origin of its own, and the
// app's `frame-src` names only this path, so the frame cannot be navigated anywhere else — the
// Wails runtime endpoint included.

// viewPrefix is the URL path the viewer's pages are served under.
const viewPrefix = "/archify-view/"

// ViewDir is where rendered pages are kept: derived from documents, never the documents
// themselves, so the app's data folder rather than the repository.
func ViewDir(base string) string {
	return filepath.Join(base, "codemap", "archify", "view")
}

// pagePath is the only shape the viewer serves: a content-addressed page name.
var pagePath = regexp.MustCompile(`^/archify-view/([0-9a-f]{64})\.html$`)

// pagePolicy lets the page's own inline script and style run, its embedded font and images load,
// and nothing else: no network, no form, no frame of its own, and only the app may frame it.
const pagePolicy = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; " +
	"img-src data: blob:; font-src data:; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-src 'none'"

// ViewerMiddleware serves rendered pages from viewDir and hands every other request to next. It is
// a plain net/http middleware, so this package needs nothing of Wails; main.go puts it in front of
// the asset server.
func ViewerMiddleware(viewDir string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			match := pagePath.FindStringSubmatch(r.URL.Path)
			if match == nil {
				next.ServeHTTP(w, r)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			page, err := os.ReadFile(filepath.Join(viewDir, match[1]+".html")) //nolint:gosec // G304: a 64-hex name inside the viewer's own folder
			if err != nil {
				http.NotFound(w, r)
				return
			}
			page = withPolicy(page)
			header := w.Header()
			header.Set("Content-Type", "text/html; charset=utf-8")
			header.Set("Content-Security-Policy", pagePolicy+"; frame-ancestors 'self'")
			header.Set("X-Content-Type-Options", "nosniff")
			header.Set("Referrer-Policy", "no-referrer")
			header.Set("Cache-Control", "no-cache")
			header.Set("Content-Length", strconv.Itoa(len(page)))
			if r.Method == http.MethodHead {
				return
			}
			// G705: the page is Archify's own rendering, served under the policy above into a sandboxed
			// frame that may run its scripts and reach nothing. A frame closed mid-write is not an error the
			// app can act on.
			_, _ = w.Write(page) //nolint:gosec // G705, as above
		})
	}
}

// withPolicy puts the page's policy first in its head as well, so it holds even where a response
// header on the app's own scheme were not honoured.
func withPolicy(page []byte) []byte {
	meta := []byte(`<meta http-equiv="Content-Security-Policy" content="` + pagePolicy + `">`)
	at := bytes.Index(page, []byte("<head>"))
	if at < 0 {
		return append(meta, page...)
	}
	at += len("<head>")
	out := make([]byte, 0, len(page)+len(meta))
	out = append(out, page[:at]...)
	out = append(out, meta...)
	return append(out, page[at:]...)
}

// ErrNotAPage is a path outside the viewer's folder, which nothing may open through it.
var ErrNotAPage = errors.New("only a rendered diagram can be opened this way")

// FileOpener opens a file with the system's default application — a browser, for HTML.
type FileOpener interface {
	OpenFile(path string) error
}

// OpenExternal opens a rendered page in the browser, where Archify's own export and sharing work.
func OpenExternal(opener FileOpener, viewDir, htmlPath string) error {
	if !inside(viewDir, htmlPath) || !pagePath.MatchString(viewPrefix+filepath.Base(htmlPath)) {
		return ErrNotAPage
	}
	if opener == nil {
		return fmt.Errorf("%w: no opener", ErrNotAPage)
	}
	if err := opener.OpenFile(htmlPath); err != nil {
		return fmt.Errorf("opening %s: %w", htmlPath, err)
	}
	return nil
}
