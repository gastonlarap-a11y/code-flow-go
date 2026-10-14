package codemap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Exporting a flow or the overview to Archify (CODEMAP-017).

// FileOpener opens a file with the operating system's default application — a browser, for HTML.
type FileOpener interface {
	OpenFile(path string) error
}

// ArchifyRequest names what to draw: a symbol's flow as a sequence, or with no symbol the
// repository's architecture.
type ArchifyRequest struct {
	RootPath string
	Symbol   string
	Language string
}

// ArchifyResult is the rendered page, and what Archify said about it.
type ArchifyResult struct {
	HTMLPath    string `json:"html_path"`
	DiagramType string `json:"diagram_type"`
	Report      string `json:"report"`
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// slug turns a name into a file name Archify accepts in meta.output.
func slug(name string) string {
	s := strings.Trim(unsafeName.ReplaceAllString(name, "-"), "-")
	if s == "" {
		return "diagram"
	}
	return clip(s, 80)
}

// Archify draws the request with the user's Archify install, writing the document and the page
// under outputDir — never into the repository — and opens the page when an opener is given.
func (s *Service) Archify(ctx context.Context, outputDir string, opener FileOpener, request ArchifyRequest) (ArchifyResult, error) {
	tools := s.tools()
	root, err := resolveRoot(request.RootPath)
	if err != nil {
		return ArchifyResult{}, err
	}
	cli, err := findArchify(root)
	if err != nil {
		return ArchifyResult{}, err
	}
	g, status, err := s.Graph(ctx, request.RootPath)
	if err != nil {
		return ArchifyResult{}, err
	}

	repo := readArchifyRepo(ctx, tools, root)
	dir := filepath.Join(outputDir, slug(filepath.Base(root)))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return ArchifyResult{}, fmt.Errorf("creating %s: %w", dir, err)
	}

	var (
		document    any
		diagramType string
		name        string
		pinned      *archifyRepository
	)
	if strings.TrimSpace(request.Symbol) == "" {
		name, diagramType = "architecture", ArchifyArchitecture
		architecture := archifyArchitectureOf(g, g.Overview(status), repo, name+".html", request.Language)
		document, pinned = architecture, architecture.Meta.Repository
	} else {
		n, err := single(g, request.Symbol)
		if err != nil {
			return ArchifyResult{}, err
		}
		name, diagramType = slug(shortSymbol(n.Name))+"-flow", ArchifySequence
		sequence, err := archifySequenceOf(g.Flow(n, FlowOptions{Depth: 8, Budget: 200, DataOnly: true}), repo, name+".html", request.Language)
		if err != nil {
			return ArchifyResult{}, err
		}
		document, pinned = sequence, sequence.Meta.Repository
	}

	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return ArchifyResult{}, fmt.Errorf("encoding the Archify document: %w", err)
	}
	documentPath := filepath.Join(dir, name+".json")
	if err := os.WriteFile(documentPath, raw, 0o600); err != nil {
		return ArchifyResult{}, fmt.Errorf("writing %s: %w", documentPath, err)
	}

	// Archify checks citations against the checkout only when the document pins one.
	repoRoot := ""
	if pinned != nil {
		repoRoot = root
	}
	report, err := cli.render(ctx, tools, diagramType, dir, name+".json", name+".html", repoRoot)
	if err != nil {
		return ArchifyResult{}, err
	}
	html := filepath.Join(dir, name+".html")
	if opener != nil {
		if err := opener.OpenFile(html); err != nil {
			return ArchifyResult{}, fmt.Errorf("opening %s: %w", html, err)
		}
	}
	return ArchifyResult{HTMLPath: html, DiagramType: diagramType, Report: report}, nil
}

// resolveRoot is a repository's real path, as the map is built from.
func resolveRoot(rootPath string) (string, error) {
	absolute, err := filepath.Abs(rootPath)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", rootPath, err)
	}
	if info, err := os.Stat(absolute); err != nil || !info.IsDir() {
		return "", ErrNotADirectory
	}
	real, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", rootPath, err)
	}
	return real, nil
}
