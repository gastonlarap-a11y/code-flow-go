package archify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

// Validating and rendering any document the user's Archify accepts (ARCHIFY-002, ARCHIFY-003).

// Types are the diagram types Archify 3.1 draws; a document naming another is refused before the
// CLI is started.
var Types = []string{"architecture", "workflow", "sequence", "dataflow", "lifecycle", "erd", "class", "tree", "timeline", "waterfall"}

// ErrNotADocument is a file that is not an Archify document: not JSON, or no known diagram_type.
var ErrNotADocument = errors.New("this is not an Archify document")

// Status is what the app can do with the user's Archify.
type Status struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version"`
	// HasClass says the install draws class diagrams, which arrived with its 3.1 line.
	HasClass bool `json:"has_class"`
	// Install is the command that installs it, for the view to offer when it is missing.
	Install string `json:"install"`
}

// InstallCommand installs Archify for the user, everywhere `npx skills` reaches.
const InstallCommand = "npx skills add tt-a1i/archify -g"

// StatusFor answers what the repository at root can use.
func StatusFor(root string) Status {
	cli, err := Find(root)
	if err != nil {
		return Status{Install: InstallCommand}
	}
	return Status{Installed: true, Version: cli.Version, HasClass: cli.has("class"), Install: InstallCommand}
}

// has says whether the install ships the schema of a diagram type.
func (c CLI) has(diagramType string) bool {
	_, err := os.Stat(c.schemaPath(diagramType))
	return err == nil
}

func (c CLI) schemaPath(name string) string {
	return filepath.Join(filepath.Dir(c.Script), "..", "schemas", name+".schema.json")
}

// Schema answers the install's own JSON Schema for a diagram type and the one every type shares —
// what a prompt hands a model, so it always matches the version that will validate the answer.
func (c CLI) Schema(diagramType string) (typeSchema, common string, err error) {
	if !slices.Contains(Types, diagramType) {
		return "", "", fmt.Errorf("%w: %q is no diagram type", ErrNotADocument, diagramType)
	}
	own, err := os.ReadFile(c.schemaPath(diagramType)) //nolint:gosec // G304: a schema of the Archify install found
	if err != nil {
		return "", "", fmt.Errorf("reading Archify's %s schema: %w", diagramType, err)
	}
	shared, err := os.ReadFile(c.schemaPath("common")) //nolint:gosec // G304: a schema of the Archify install found
	if err != nil {
		return "", "", fmt.Errorf("reading Archify's common schema: %w", err)
	}
	return string(own), string(shared), nil
}

// Diagnostic is one thing Archify found wrong with a document.
type Diagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	// Subject names the element it is about; Evidence is what Archify measured, by code.
	Subject  DiagnosticSubject `json:"subject"`
	Evidence json.RawMessage   `json:"evidence,omitempty"`
}

// DiagnosticSubject is the element of a document a diagnostic is about.
type DiagnosticSubject struct {
	Collection string `json:"collection"`
	Index      *int   `json:"index"`
}

// Report is Archify's verdict on a document.
type Report struct {
	OK          bool         `json:"ok"`
	Error       string       `json:"error"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Problems are the report's errors, one line each, for a model asked to repair the document.
func (r Report) Problems() []string {
	out := make([]string, 0, len(r.Diagnostics)+1)
	for _, d := range r.Diagnostics {
		if d.Severity == "error" {
			out = append(out, d.Message)
		}
	}
	if len(out) == 0 && r.Error != "" {
		out = append(out, r.Error)
	}
	return out
}

// DiagramType reads a document's diagram_type, refusing anything Archify does not draw.
func DiagramType(document []byte) (string, error) {
	var head struct {
		DiagramType string `json:"diagram_type"`
	}
	if err := json.Unmarshal(document, &head); err != nil {
		return "", fmt.Errorf("%w: %w", ErrNotADocument, err)
	}
	if !slices.Contains(Types, head.DiagramType) {
		return "", fmt.Errorf("%w: no known diagram_type", ErrNotADocument)
	}
	return head.DiagramType, nil
}

// pinsRepository says whether a document cites a commit, which is when Archify checks its citations
// against the checkout.
func pinsRepository(document []byte) bool {
	var head struct {
		Meta struct {
			Repository json.RawMessage `json:"repository"`
		} `json:"meta"`
	}
	return json.Unmarshal(document, &head) == nil && len(head.Meta.Repository) > 0 && string(head.Meta.Repository) != "null"
}

// Validate asks Archify whether a document would render, in a scratch folder of its own. A document
// Archify rejects is a report with OK false, not an error; an error is Archify not answering at all.
func (c CLI) Validate(ctx context.Context, rt Runtime, root string, document []byte) (Report, error) {
	diagramType, err := DiagramType(document)
	if err != nil {
		return Report{}, err
	}
	dir, err := os.MkdirTemp("", "codeflow-archify-")
	if err != nil {
		return Report{}, fmt.Errorf("creating a scratch folder: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }() // scratch: losing it costs nothing
	if err := os.WriteFile(filepath.Join(dir, "doc.json"), document, platform.FilePerm); err != nil {
		return Report{}, fmt.Errorf("writing the document to validate: %w", err)
	}
	// The quality every render asks for: below it Archify skips the layout checks `deliver` then
	// refuses on, and a document it validated would not draw.
	args := []string{"validate", diagramType, "doc.json", "--json", "--quality", renderQuality}
	if pinsRepository(document) {
		args = append(args, "--repo-root", root)
	}
	out, _, err := c.run(ctx, rt, dir, args...)
	var report Report
	if json.Unmarshal(out, &report) != nil {
		if err != nil {
			return Report{}, fmt.Errorf("archify could not validate the document: %s", firstLine(problem(string(out), err))) //nolint:err113 // Archify's own words are the message
		}
		return Report{}, fmt.Errorf("archify answered something that is not a report: %s", firstLine(string(out))) //nolint:err113 // Archify's own words are the message
	}
	if report.Diagnostics == nil {
		report.Diagnostics = []Diagnostic{}
	}
	return report, nil
}

// run starts the CLI in dir, answering its stdout and stderr; a non-zero exit is the error, with the
// output still returned — Archify reports a rejected document on stdout and exits 1.
func (c CLI) run(ctx context.Context, rt Runtime, dir string, args ...string) (stdout []byte, stderr string, err error) {
	return c.runScript(ctx, rt, dir, nil, c.Script, args...)
}

// runScript is run for another script of the install, under the same environment plus Archify's
// own settings in settings — `ARCHIFY_REPO_ROOT`, which its CLI sets from `--repo-root` for the
// renderers it starts.
func (c CLI) runScript(ctx context.Context, rt Runtime, dir string, settings []string, script string, args ...string) (stdout []byte, stderr string, err error) {
	if rt.Node == "" {
		return nil, "", ErrNoNode
	}
	cmd := proc.Command(ctx, rt.Node, append([]string{script}, args...)...)
	cmd.Dir = dir
	// The update check is the CLI phoning home on every run; the app decides when to look for one.
	cmd.Env = append(append(proc.Environment(rt.Path), "ARCHIFY_UPDATE_CHECK_DISABLED=1"), settings...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	runErr := cmd.Run()
	if runErr != nil {
		return out.Bytes(), errOut.String(), fmt.Errorf("running archify %s: %w", args[0], runErr)
	}
	return out.Bytes(), errOut.String(), nil
}

// Rendered is a page drawn from a document, and where the app's viewer serves it.
type Rendered struct {
	ViewURL  string `json:"view_url"`
	HTMLPath string `json:"html_path"`
	Report   string `json:"report"`
}

// Render draws a document into the viewer's folder, under a name made of the document, the Archify
// version and the repository — so the same document is drawn once, and drawn again when either
// changes. Citations are checked against the checkout when the document pins a commit.
func (c CLI) Render(ctx context.Context, rt Runtime, root string, document []byte, viewDir string) (Rendered, error) {
	diagramType, err := DiagramType(document)
	if err != nil {
		return Rendered{}, err
	}
	key := viewKey(document, c.Version, root)
	html := filepath.Join(viewDir, key+".html")
	rendered := Rendered{ViewURL: viewPrefix + key + ".html", HTMLPath: html}
	if _, err := os.Stat(html); err == nil {
		return rendered, nil
	}

	if err := os.MkdirAll(viewDir, platform.DirPerm); err != nil {
		return Rendered{}, fmt.Errorf("creating %s: %w", viewDir, err)
	}
	stage, err := os.MkdirTemp(viewDir, "stage-")
	if err != nil {
		return Rendered{}, fmt.Errorf("creating a staging folder: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }() // staging only: the page is moved out before it goes
	staged, err := withOutput(document, "page.html")
	if err != nil {
		return Rendered{}, err
	}
	if err := os.WriteFile(filepath.Join(stage, "doc.json"), staged, platform.FilePerm); err != nil {
		return Rendered{}, fmt.Errorf("writing the document to render: %w", err)
	}
	repoRoot := ""
	if pinsRepository(document) {
		repoRoot = root
	}
	report, err := c.Deliver(ctx, rt, diagramType, stage, "doc.json", "page.html", repoRoot)
	rendered.Report = report
	if err != nil {
		return rendered, err
	}
	if err := os.Rename(filepath.Join(stage, "page.html"), html); err != nil {
		return rendered, fmt.Errorf("keeping the rendered page: %w", err)
	}
	return rendered, nil
}

// withOutput is the document with `meta.output` naming the page `deliver` writes — which it
// requires, and checks is a portable relative path, while a document kept in the repository need
// not carry one. Only the staged copy is changed.
func withOutput(document []byte, output string) ([]byte, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(document, &doc); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotADocument, err)
	}
	meta := map[string]json.RawMessage{}
	if raw, ok := doc["meta"]; ok {
		if err := json.Unmarshal(raw, &meta); err != nil {
			return nil, fmt.Errorf("%w: meta: %w", ErrNotADocument, err)
		}
	}
	name, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("naming the page: %w", err)
	}
	meta["output"] = name
	if doc["meta"], err = json.Marshal(meta); err != nil {
		return nil, fmt.Errorf("writing meta: %w", err)
	}
	staged, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("writing the staged document: %w", err)
	}
	return staged, nil
}

// viewKey names a rendered page by what drew it.
func viewKey(document []byte, version, root string) string {
	sum := sha256.New()
	for _, part := range [][]byte{document, []byte(version), []byte(root)} {
		sum.Write(part)
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// inside says whether path is within dir, after resolving both.
func inside(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}
