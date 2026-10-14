package codemap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

// Finding and running the user's Archify (CODEMAP-017).

// ErrNoArchify means no supported Archify install was found; the diagram editor's Mermaid export
// does not need one.
var ErrNoArchify = errors.New("Archify is not installed: run `npx skills add tt-a1i/archify -g` once") //nolint:staticcheck // ST1005: shown to the user as a sentence

// The Archify releases whose schema_version 1 documents this package writes: 3.1 and later 3.x.
const (
	archifyMajor    = 3
	archifyMinMinor = 1
)

// archifyCLI is an installed Archify command line.
type archifyCLI struct {
	script, version string
}

// archifyCandidates are where `npx skills add` puts Archify — for the user, then for the project —
// after an explicit ARCHIFY_BIN.
func archifyCandidates(root string) []string {
	candidates := make([]string, 0, 7)
	if bin := os.Getenv("ARCHIFY_BIN"); bin != "" {
		candidates = append(candidates, bin)
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, dir := range []string{".claude", ".agents", ".codex"} {
			candidates = append(candidates, filepath.Join(home, dir, "skills", "archify", "bin", "archify.mjs"))
		}
	}
	for _, dir := range []string{".claude", ".agents"} {
		candidates = append(candidates, filepath.Join(root, dir, "skills", "archify", "bin", "archify.mjs"))
	}
	return candidates
}

// findArchify answers the first supported install, or ErrNoArchify.
func findArchify(root string) (archifyCLI, error) {
	for _, script := range archifyCandidates(root) {
		if _, err := os.Stat(script); err != nil {
			continue
		}
		version, err := archifyVersion(filepath.Join(filepath.Dir(script), "..", "package.json"))
		if err != nil || !supportedArchify(version) {
			continue
		}
		return archifyCLI{script: script, version: version}, nil
	}
	return archifyCLI{}, ErrNoArchify
}

func archifyVersion(manifest string) (string, error) {
	raw, err := os.ReadFile(manifest) //nolint:gosec // G304: Archify's own package.json, beside the script found
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", manifest, err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return "", fmt.Errorf("decoding %s: %w", manifest, err)
	}
	return pkg.Version, nil
}

func supportedArchify(version string) bool {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, errMajor := strconv.Atoi(parts[0])
	minor, errMinor := strconv.Atoi(parts[1])
	return errMajor == nil && errMinor == nil && major == archifyMajor && minor >= archifyMinMinor
}

// render validates and renders one document with `archify deliver`, in dir, checking its citations
// against the checkout at repoRoot — "" for a document that pins no repository. Answers Archify's
// own report.
func (c archifyCLI) render(ctx context.Context, tools Tools, diagramType, dir, document, html, repoRoot string) (string, error) {
	if tools.Node == "" {
		return "", errNoNode
	}
	args := []string{c.script, "deliver", diagramType, document, html, "--quality", "standard", "--json"}
	if repoRoot != "" {
		args = append(args, "--repo-root", repoRoot)
	}
	cmd := proc.Command(ctx, tools.Node, args...)
	cmd.Dir = dir
	cmd.Env = proc.Environment(tools.Path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	report := strings.TrimSpace(stdout.String() + "\n" + stderr.String())
	if err != nil {
		return report, fmt.Errorf("archify could not render the diagram: %s", firstLine(archifyProblem(report, err))) //nolint:err113 // Archify's own words are the message
	}
	return report, nil
}

// archifyProblem picks the first thing Archify said was wrong from its JSON report.
func archifyProblem(report string, err error) string {
	var parsed struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(report), &parsed) == nil {
		if len(parsed.Errors) > 0 {
			return parsed.Errors[0].Message
		}
		if parsed.Error != "" {
			return parsed.Error
		}
	}
	if report != "" {
		return report
	}
	return err.Error()
}

// archifyRepo is what Archify needs to check citations: the pinned commit, where it links, and
// which files differ from that commit — citing those would fail, because Archify reads committed
// blobs, not the working tree.
type archifyRepo struct {
	meta  *archifyRepository
	dirty map[string]bool
}

// readArchifyRepo inspects the git checkout at root. A folder that is not a checkout's top, or has
// no origin, answers a repo without metadata: the diagrams then carry no citations, which Archify
// accepts.
func readArchifyRepo(ctx context.Context, tools Tools, root string) archifyRepo {
	repo := archifyRepo{dirty: map[string]bool{}}
	top, topErr := runGit(ctx, tools, root, "rev-parse", "--show-toplevel")
	revision, revErr := runGit(ctx, tools, root, "rev-parse", "HEAD")
	origin, originErr := runGit(ctx, tools, root, "remote", "get-url", "origin")
	if topErr != nil || revErr != nil || originErr != nil || !sameDir(strings.TrimSpace(top), root) {
		return repo
	}
	status, err := runGit(ctx, tools, root, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return repo
	}
	entries := strings.Split(status, "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}
		repo.dirty[entry[3:]] = true
		if entry[0] == 'R' || entry[0] == 'C' {
			i++ // the next entry is the rename's or copy's source
		}
	}
	address, provider, ok := archifyRemote(strings.TrimSpace(origin))
	if !ok {
		return repo
	}
	linkMode := "web"
	if branches, err := runGit(ctx, tools, root, "branch", "-r", "--contains", strings.TrimSpace(revision)); err != nil ||
		strings.TrimSpace(branches) == "" || provider == "" {
		linkMode = "local-only" // links to a commit the host has never seen would 404
	}
	repo.meta = &archifyRepository{URL: address, Provider: provider, LinkMode: linkMode, Revision: strings.TrimSpace(revision)}
	return repo
}

var (
	scpRemote = regexp.MustCompile(`^[A-Za-z0-9_.-]+@([^:/]+):(.+)$`)
	providers = map[string]string{"github.com": "github", "gitee.com": "gitee", "gitlab.com": "gitlab"}
)

// archifyRemote is the origin as Archify pins it, following its repository-authoring rules: an
// HTTP(S) origin loses its userinfo and keeps everything else; a GitHub, GitLab or Gitee SSH origin
// becomes the HTTPS address it matches, the only kind Archify links to; any other SSH origin stays
// as it is. provider is "" for a host Archify cannot link. ok is false for an origin Archify
// rejects — a folder on disk.
func archifyRemote(origin string) (address, provider string, ok bool) {
	if m := scpRemote.FindStringSubmatch(origin); m != nil {
		host := m[1]
		if provider := providers[host]; provider != "" {
			return "https://" + host + "/" + strings.TrimSuffix(m[2], ".git"), provider, true
		}
		return origin, "", true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return "", "", false
	}
	switch parsed.Scheme {
	case "https":
		parsed.User = nil
		return parsed.String(), providers[parsed.Host], true
	case "http":
		parsed.User = nil
		return parsed.String(), "", true
	case "ssh":
		return origin, "", true
	}
	return "", "", false
}

// cite answers up to three citations, skipping files not committed as they are.
func (r archifyRepo) cite(symbols []SymbolRef) []archifySource {
	if r.meta == nil {
		return nil
	}
	out := make([]archifySource, 0, 3)
	seen := map[string]bool{}
	for _, s := range symbols {
		key := s.File + ":" + strconv.Itoa(s.Line)
		if s.File == "" || r.dirty[s.File] || seen[key] || len(out) == 3 {
			continue
		}
		seen[key] = true
		source := archifySource{Path: s.File, Line: s.Line}
		if s.EndLine > s.Line {
			source.EndLine = s.EndLine
		}
		out = append(out, source)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// pinned answers the repository only when a citation uses it: Archify rejects a pinned repository
// with no sources.
func (r archifyRepo) pinned(cited bool) *archifyRepository {
	if !cited {
		return nil
	}
	return r.meta
}

func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// runGit runs a local, read-only git command in dir.
func runGit(ctx context.Context, tools Tools, dir string, args ...string) (string, error) {
	cmd := proc.Command(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = proc.Environment(tools.Path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
