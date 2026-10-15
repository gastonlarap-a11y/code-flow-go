package archify

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

// Repository is the commit a document pins, as Archify's `meta.repository` spells it.
type Repository struct {
	URL      string `json:"url"`
	Provider string `json:"provider,omitempty"`
	LinkMode string `json:"link_mode,omitempty"`
	Revision string `json:"revision"`
}

// Source is one citation of a diagram element: a committed file, and the lines that show it.
type Source struct {
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	EndLine int    `json:"end_line,omitempty"`
}

// Ref is a place in the repository a diagram element could cite.
type Ref struct {
	Path    string
	Line    int
	EndLine int
}

// Repo is what Archify needs to check citations: the pinned commit, where it links, and which files
// differ from that commit — citing those would fail, because Archify reads committed blobs, not
// the working tree.
type Repo struct {
	Meta  *Repository
	Dirty map[string]bool
}

// maxSources is how many citations Archify accepts on one element.
const maxSources = 3

// ReadRepo inspects the git checkout at root. A folder that is not a checkout's top, or has no
// origin, answers a Repo without metadata: the diagrams then carry no citations, which Archify
// accepts.
func ReadRepo(ctx context.Context, rt Runtime, root string) Repo {
	repo := Repo{Dirty: map[string]bool{}}
	top, topErr := git(ctx, rt, root, "rev-parse", "--show-toplevel")
	revision, revErr := git(ctx, rt, root, "rev-parse", "HEAD")
	origin, originErr := git(ctx, rt, root, "remote", "get-url", "origin")
	if topErr != nil || revErr != nil || originErr != nil || !sameDir(strings.TrimSpace(top), root) {
		return repo
	}
	status, err := git(ctx, rt, root, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return repo
	}
	entries := strings.Split(status, "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}
		repo.Dirty[entry[3:]] = true
		if entry[0] == 'R' || entry[0] == 'C' {
			i++ // the next entry is the rename's or copy's source
		}
	}
	address, provider, ok := Remote(strings.TrimSpace(origin))
	if !ok {
		return repo
	}
	linkMode := "web"
	if branches, err := git(ctx, rt, root, "branch", "-r", "--contains", strings.TrimSpace(revision)); err != nil ||
		strings.TrimSpace(branches) == "" || provider == "" {
		linkMode = "local-only" // links to a commit the host has never seen would 404
	}
	repo.Meta = &Repository{URL: address, Provider: provider, LinkMode: linkMode, Revision: strings.TrimSpace(revision)}
	return repo
}

var (
	scpRemote = regexp.MustCompile(`^[A-Za-z0-9_.-]+@([^:/]+):(.+)$`)
	providers = map[string]string{"github.com": "github", "gitee.com": "gitee", "gitlab.com": "gitlab"}
)

// Remote is the origin as Archify pins it, following its repository-authoring rules: an HTTP(S)
// origin loses its userinfo and keeps everything else; a GitHub, GitLab or Gitee SSH origin becomes
// the HTTPS address it matches, the only kind Archify links to; any other SSH origin stays as it is.
// provider is "" for a host Archify cannot link. ok is false for an origin Archify rejects — a
// folder on disk.
func Remote(origin string) (address, provider string, ok bool) {
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

// Cite answers up to three citations, skipping files not committed as they are; nil when the
// repository pins nothing.
func (r Repo) Cite(refs []Ref) []Source {
	if r.Meta == nil {
		return nil
	}
	out := make([]Source, 0, maxSources)
	seen := map[string]bool{}
	for _, ref := range refs {
		key := ref.Path + ":" + strconv.Itoa(ref.Line)
		if ref.Path == "" || r.Dirty[ref.Path] || seen[key] || len(out) == maxSources {
			continue
		}
		seen[key] = true
		source := Source{Path: ref.Path, Line: ref.Line}
		if ref.EndLine > ref.Line {
			source.EndLine = ref.EndLine
		}
		out = append(out, source)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Pinned answers the repository only when a citation uses it: Archify rejects a pinned repository
// with no sources.
func (r Repo) Pinned(cited bool) *Repository {
	if !cited {
		return nil
	}
	return r.Meta
}

func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// git runs a local, read-only git command in dir.
func git(ctx context.Context, rt Runtime, dir string, args ...string) (string, error) {
	cmd := proc.Command(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = proc.Environment(rt.Path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
