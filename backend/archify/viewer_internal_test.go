package archify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const pageKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func viewer(t *testing.T) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, pageKey+".html"), []byte("<!DOCTYPE html><html><head><title>x</title></head><body><script>1</script></body></html>"), 0o600))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	return ViewerMiddleware(dir)(next), dir
}

func serve(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, nil))
	return rec
}

func TestTheViewerServesARenderedPageUnderItsOwnPolicy(t *testing.T) {
	h, _ := viewer(t)
	rec := serve(h, http.MethodGet, "/archify-view/"+pageKey+".html")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	policy := rec.Header().Get("Content-Security-Policy")
	assert.Contains(t, policy, "default-src 'none'")
	assert.Contains(t, policy, "connect-src 'none'")
	assert.Contains(t, policy, "frame-ancestors 'self'")
	body := rec.Body.String()
	assert.True(t, strings.HasPrefix(body, `<!DOCTYPE html><html><head><meta http-equiv="Content-Security-Policy"`), "the policy is the first thing in the head")
}

func TestTheViewerLeavesEveryOtherRequestToTheAssetServer(t *testing.T) {
	h, _ := viewer(t)
	for _, path := range []string{
		"/", "/index.html", "/wails/runtime", "/archify-view/", "/archify-view/abc.html",
		"/archify-view/" + pageKey + ".html.map", "/archify-view/../" + pageKey + ".html",
		"/archify-view/" + strings.ToUpper(pageKey) + ".html",
	} {
		t.Run(path, func(t *testing.T) {
			assert.Equal(t, http.StatusTeapot, serve(h, http.MethodGet, path).Code)
		})
	}
}

func TestTheViewerOnlyReads(t *testing.T) {
	h, _ := viewer(t)
	assert.Equal(t, http.StatusMethodNotAllowed, serve(h, http.MethodPost, "/archify-view/"+pageKey+".html").Code)
	head := serve(h, http.MethodHead, "/archify-view/"+pageKey+".html")
	assert.Equal(t, http.StatusOK, head.Code)
	assert.Empty(t, head.Body.String())
	missing := strings.Repeat("f", 64)
	assert.Equal(t, http.StatusNotFound, serve(h, http.MethodGet, "/archify-view/"+missing+".html").Code)
}

type recordingOpener struct{ opened []string }

func (o *recordingOpener) OpenFile(path string) error {
	o.opened = append(o.opened, path)
	return nil
}

func TestOnlyARenderedPageOpensInTheBrowser(t *testing.T) {
	_, dir := viewer(t)
	opener := &recordingOpener{}
	page := filepath.Join(dir, pageKey+".html")
	require.NoError(t, OpenExternal(opener, dir, page))
	assert.Equal(t, []string{page}, opener.opened)

	for _, path := range []string{filepath.Join(dir, "..", "secrets.html"), filepath.Join(dir, "notes.html"), "/etc/hosts"} {
		require.ErrorIs(t, OpenExternal(opener, dir, path), ErrNotAPage, path)
	}
}

func TestADocumentIsReadOnlyFromInsideTheRepository(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "codemap"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "codemap", "a.archify.json"), []byte(`{"diagram_type":"class"}`), 0o600))
	outside := filepath.Join(t.TempDir(), "x.json")
	require.NoError(t, os.WriteFile(outside, []byte("{}"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link.json")))

	_, document, err := readDocument(root, "codemap/a.archify.json")
	require.NoError(t, err)
	assert.JSONEq(t, `{"diagram_type":"class"}`, string(document))

	_, _, err = readDocument(root, "../"+filepath.Base(filepath.Dir(outside))+"/x.json")
	require.Error(t, err)
	_, _, err = readDocument(root, "link.json")
	require.ErrorIs(t, err, ErrDocumentOutside, "a symlink out of the repository")
}

func TestADocumentNamesADiagramTypeArchifyDraws(t *testing.T) {
	kind, err := DiagramType([]byte(`{"diagram_type":"lifecycle"}`))
	require.NoError(t, err)
	assert.Equal(t, "lifecycle", kind)
	for _, bad := range []string{`{"diagram_type":"gantt"}`, `{}`, `not json`} {
		_, err := DiagramType([]byte(bad))
		require.ErrorIs(t, err, ErrNotADocument, bad)
	}
	assert.True(t, pinsRepository([]byte(`{"meta":{"repository":{"url":"u","revision":"r"}}}`)))
	assert.False(t, pinsRepository([]byte(`{"meta":{}}`)))
}

func TestAReportsProblemsAreItsErrors(t *testing.T) {
	report := Report{Diagnostics: []Diagnostic{{Severity: "warning", Message: "w"}, {Severity: "error", Message: "e"}}}
	assert.Equal(t, []string{"e"}, report.Problems())
	assert.Equal(t, []string{"broken"}, Report{Error: "broken", Diagnostics: []Diagnostic{}}.Problems())
}

// With a real Archify (ARCHIFY_BIN) and node, a class diagram is validated and rendered once.
func TestARealArchifyValidatesAndRendersAClassDiagram(t *testing.T) {
	script := os.Getenv("ARCHIFY_BIN")
	node, err := exec.LookPath("node")
	if script == "" || err != nil {
		t.Skip("needs ARCHIFY_BIN and node")
	}
	t.Setenv("HOME", t.TempDir())
	cli, err := Find(t.TempDir())
	require.NoError(t, err)
	require.True(t, cli.has("class"))
	rt := Runtime{Node: node, Path: filepath.SplitList(os.Getenv("PATH"))}

	unplaced := []byte(`{"schema_version":1,"diagram_type":"class","meta":{"title":"Cart"},"types":[{"id":"Cart","label":"Cart","kind":"class"}]}`)
	report, err := cli.Validate(t.Context(), rt, t.TempDir(), unplaced)
	require.NoError(t, err)
	assert.False(t, report.OK)
	require.NotEmpty(t, report.Problems())
	assert.Contains(t, report.Problems()[0], "row/col")

	placed := []byte(`{"schema_version":1,"diagram_type":"class","meta":{"title":"Cart"},"layout":{"mode":"grid"},
		"types":[{"id":"Cart","label":"Cart","kind":"class","row":1,"col":0},{"id":"Shop","label":"Shop","kind":"interface","row":0,"col":0}],
		"relationships":[{"from":"Cart","to":"Shop","kind":"realization"}]}`)
	report, err = cli.Validate(t.Context(), rt, t.TempDir(), placed)
	require.NoError(t, err)
	assert.True(t, report.OK, report.Problems())

	views, root := t.TempDir(), t.TempDir()
	first, err := cli.Render(t.Context(), rt, root, placed, views)
	require.NoError(t, err)
	assert.FileExists(t, first.HTMLPath)
	assert.Regexp(t, `^/archify-view/[0-9a-f]{64}\.html$`, first.ViewURL)
	again, err := cli.Render(t.Context(), rt, root, placed, views)
	require.NoError(t, err)
	assert.Empty(t, again.Report, "the same document is drawn once")
	assert.Equal(t, first.HTMLPath, again.HTMLPath)
	_, err = cli.Render(t.Context(), rt, t.TempDir(), placed, views)
	require.NoError(t, err)
	entries, err := os.ReadDir(views)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "a page per repository it was drawn for, and no staging folder left behind")
}
