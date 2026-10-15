package archify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/repomap"
)

// Deps is what the Archify commands need.
type Deps struct {
	// Runtime answers where node is and the PATH child processes get, looked up per call so a node
	// installed while the app runs is found.
	Runtime func() Runtime
	// ViewDir holds the rendered pages the viewer serves.
	ViewDir string
	// Opener shows a page in the browser. Nil refuses to.
	Opener FileOpener
	// Maps is the code map's file index, which class diagrams are drawn from and a model's run is
	// oriented by.
	Maps *repomap.Service
	// AI runs the diagram task. Nil refuses every diagram a model would draw.
	AI Invoker
	// MCPConfig writes a configuration that offers the code map to a run as tools, and answers how
	// to remove it; nil runs without them. The code map provides it, so this package needs nothing of
	// that one.
	MCPConfig func(root string) (path string, cleanup func(), err error)
}

// ErrInvalidDiagram is a document Archify would refuse to draw.
var ErrInvalidDiagram = errors.New("archify would not draw this diagram")

// ClassDiagramResult is a class diagram ready to be saved and shown.
type ClassDiagramResult struct {
	Document string `json:"document"`
	Title    string `json:"title"`
	Types    int    `json:"types"`
	Omitted  int    `json:"omitted"`
}

// classWords are what a class diagram says, per interface language.
var classWords = map[string]struct{ title, whole, partial string }{
	"en": {"%s — classes", "%d types with their fields, methods and relationships, read from the code", "The %d most used of %d types, read from the code"},
	"es": {"%s — clases", "%d tipos con sus campos, métodos y relaciones, leídos del código", "Los %d tipos más usados de %d, leídos del código"},
}

// maxDocumentBytes bounds a document read for rendering; an Archify document is a few kilobytes.
const maxDocumentBytes = 2 << 20

// ErrDocumentOutside is a document path that leaves the repository.
var ErrDocumentOutside = errors.New("the document is not inside the repository")

// Register adds the Archify commands (ARCHIFY-002 … ARCHIFY-009). None needs the database, and none
// writes into the repository: the renderer saves what they answer.
func Register(r *bridge.Registry, deps Deps) {
	r.Add("archify_status", func(_ context.Context, p bridge.Params) (any, error) {
		rootPath, err := bridge.Arg[string](p, "rootPath")
		if err != nil {
			return nil, err
		}
		return StatusFor(rootPath), nil
	})

	r.Add("archify_render", func(ctx context.Context, p bridge.Params) (any, error) {
		rootPath, err := bridge.Arg[string](p, "rootPath")
		if err != nil {
			return nil, err
		}
		path, err := bridge.Arg[string](p, "path")
		if err != nil {
			return nil, err
		}
		root, document, err := readDocument(rootPath, path)
		if err != nil {
			return nil, err
		}
		cli, err := Find(root)
		if err != nil {
			return nil, err
		}
		return cli.Render(ctx, deps.Runtime(), root, document, deps.ViewDir)
	})

	r.Add("archify_class_diagram", func(ctx context.Context, p bridge.Params) (any, error) {
		rootPath, err := bridge.Arg[string](p, "rootPath")
		if err != nil {
			return nil, err
		}
		scope, err := bridge.ArgOr(p, "scope", "")
		if err != nil {
			return nil, err
		}
		language, err := bridge.ArgOr(p, "language", "en")
		if err != nil {
			return nil, err
		}
		return classDiagram(ctx, deps, rootPath, scope, language)
	})

	r.Add("archify_generate", func(ctx context.Context, p bridge.Params) (any, error) {
		var request struct {
			RootPath string `json:"rootPath"`
			Kind     string `json:"kind"`
			Topic    string `json:"topic"`
			RunID    string `json:"runId"`
			Language string `json:"language"`
		}
		if err := bridge.Bind(p, &request); err != nil {
			return nil, err
		}
		if request.RootPath == "" {
			return nil, bridge.MissingParameterError("rootPath")
		}
		return Generate(ctx, deps, GenerateRequest(request))
	})

	r.Add("archify_refine", func(ctx context.Context, p bridge.Params) (any, error) {
		var request struct {
			RootPath    string `json:"rootPath"`
			Path        string `json:"path"`
			Instruction string `json:"instruction"`
			RunID       string `json:"runId"`
			Language    string `json:"language"`
		}
		if err := bridge.Bind(p, &request); err != nil {
			return nil, err
		}
		if request.RootPath == "" {
			return nil, bridge.MissingParameterError("rootPath")
		}
		if request.Path == "" {
			return nil, bridge.MissingParameterError("path")
		}
		return Refine(ctx, deps, RefineRequest(request))
	})

	r.Add("archify_suggestions", func(ctx context.Context, p bridge.Params) (any, error) {
		rootPath, err := bridge.Arg[string](p, "rootPath")
		if err != nil {
			return nil, err
		}
		kind, err := bridge.Arg[string](p, "kind")
		if err != nil {
			return nil, err
		}
		snap, _, err := deps.Maps.Snapshot(ctx, rootPath)
		if err != nil {
			return nil, err
		}
		return Suggestions(snap, rootPath, kind), nil
	})

	r.Add("archify_open_external", func(_ context.Context, p bridge.Params) (any, error) {
		htmlPath, err := bridge.Arg[string](p, "htmlPath")
		if err != nil {
			return nil, err
		}
		return struct{}{}, OpenExternal(deps.Opener, deps.ViewDir, htmlPath)
	})
}

// classDiagram draws the types under scope, checked by the user's Archify when one is installed —
// so a document that would not render is refused here, not after it was written into the repository.
func classDiagram(ctx context.Context, deps Deps, rootPath, scope, language string) (ClassDiagramResult, error) {
	snap, _, err := deps.Maps.Snapshot(ctx, rootPath)
	if err != nil {
		return ClassDiagramResult{}, err
	}
	root, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		return ClassDiagramResult{}, fmt.Errorf("resolving %s: %w", rootPath, err)
	}
	words, ok := classWords[language]
	if !ok {
		words, language = classWords["en"], "en"
	}
	name := filepath.Base(root)
	if scope = strings.Trim(scope, "/"); scope != "" {
		name = filepath.Base(filepath.FromSlash(scope))
	}
	title := fmt.Sprintf(words.title, name)
	subtitle := func(drawn, total int) string {
		if drawn < total {
			return fmt.Sprintf(words.partial, drawn, total)
		}
		return fmt.Sprintf(words.whole, drawn)
	}
	rt := deps.Runtime()
	built, err := BuildClassDiagram(snap, ReadRepo(ctx, rt, root), scope, title, subtitle, language)
	if err != nil {
		return ClassDiagramResult{}, err
	}
	if cli, err := Find(root); err == nil {
		// Routed along the grid's gaps, so no line runs through an unrelated box. An install that
		// cannot lay the diagram out keeps Archify's own routes, which its validation then judges.
		routed, report, err := cli.RouteClassDiagram(ctx, rt, root, built.Document)
		if errors.Is(err, ErrNoClassRenderer) {
			routed = built.Document
			report, err = cli.Validate(ctx, rt, root, built.Document)
		}
		if err != nil {
			return ClassDiagramResult{}, err
		}
		built.Document = routed
		if !report.OK {
			return ClassDiagramResult{}, fmt.Errorf("%w: %s", ErrInvalidDiagram, strings.Join(report.Problems(), "; "))
		}
	}
	return ClassDiagramResult{Document: string(built.Document), Title: title, Types: built.Types, Omitted: built.Omitted}, nil
}

// readDocument reads a repository-relative document, refusing a path that leaves the repository —
// through `..` or a symlink.
func readDocument(rootPath, path string) (string, []byte, error) {
	root, err := filepath.EvalSymlinks(strings.TrimSpace(rootPath))
	if err != nil {
		return "", nil, fmt.Errorf("resolving %s: %w", rootPath, err)
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return "", nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if !inside(root, full) {
		return "", nil, ErrDocumentOutside
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if info.Size() > maxDocumentBytes {
		return "", nil, fmt.Errorf("%w: %s is %d bytes", ErrNotADocument, path, info.Size())
	}
	document, err := os.ReadFile(full) //nolint:gosec // G304: confined to the repository above
	if err != nil {
		return "", nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return root, document, nil
}
