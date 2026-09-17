package files

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/git"
)

// MaxFiles bounds every walk: listing for the palette, searching, and replacing.
//
// 20 000, and silent when it is reached — the walk simply stops collecting. There is no truncation
// flag for this one, which is 2.x's behaviour: a repository large enough to hit it is one where the
// palette was never going to be usable anyway, and inventing a warning now would be new behaviour.
const MaxFiles = 20_000

// IgnoreChecker answers whether a repo-relative path is ignored.
//
// Declared at the consumer, as everything in this package is: the walk needs one question answered
// and knows nothing else about git.
type IgnoreChecker interface {
	IsIgnored(ctx context.Context, repo string, probes []string) (map[string]bool, error)
}

// gitIgnoreChecker is the default, backed by `git check-ignore`.
type gitIgnoreChecker struct{}

func (gitIgnoreChecker) IsIgnored(ctx context.Context, repo string, probes []string) (map[string]bool, error) {
	return git.CheckIgnore(ctx, repo, probes)
}

// walk collects repo-relative file paths, pruning ignored directories (FILE-007).
//
// **Pruning, not filtering** (DIVERGENCE-FILE-a). An ignored directory is never descended into, so
// `node_modules/` and `target/` are never read from disk at all. Walking everything and filtering
// afterwards produces the same list and takes long enough on a real project that the palette feels
// broken — do not reimplement it that way.
//
// The probe for a directory carries a trailing `/`, and that matters: a directory-only ignore rule
// (`build/`) only matches a path git also sees as a directory, so without the slash the walk
// descends into exactly the directories the user asked it not to.
//
// Entries are sorted before recursing, so the palette's order is stable across calls rather than
// whatever order the filesystem happened to return.
func walk(ctx context.Context, checker IgnoreChecker, repo string) ([]string, error) {
	base, err := canonical(repo)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, 256)
	err = walkInto(ctx, checker, base, "", &out)
	return out, err
}

func walkInto(ctx context.Context, checker IgnoreChecker, base, relDir string, out *[]string) error {
	if len(*out) >= MaxFiles {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	entries, err := os.ReadDir(filepath.Join(base, filepath.FromSlash(relDir)))
	if err != nil {
		// A directory that cannot be read is skipped rather than failing the walk: one unreadable
		// folder must not cost the user every other file in the repository.
		return nil //nolint:nilerr // deliberate, see above
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	// One `git check-ignore` for the whole directory rather than one per entry. A repository with
	// a thousand-entry folder would otherwise spawn a thousand git processes for one level.
	probes := make([]string, 0, len(entries))
	kept := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		// `.git` goes by literal name, not by an ignore rule — it is not ignored, it is the
		// repository's own machinery.
		if entry.Name() == ".git" {
			continue
		}
		kept = append(kept, entry)
		probes = append(probes, probeFor(relDir, entry))
	}
	if len(kept) == 0 {
		return nil
	}

	ignored, err := checker.IsIgnored(ctx, base, probes)
	if err != nil {
		return err
	}

	for i, entry := range kept {
		if ignored[probes[i]] {
			continue
		}
		child := path(relDir, entry.Name())

		if entry.IsDir() {
			if err := walkInto(ctx, checker, base, child, out); err != nil {
				return err
			}
			continue
		}
		if len(*out) >= MaxFiles {
			return nil
		}
		*out = append(*out, child)
	}
	return nil
}

func probeFor(relDir string, entry os.DirEntry) string {
	probe := path(relDir, entry.Name())
	if entry.IsDir() {
		probe += "/"
	}
	return probe
}

func path(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// ListRepoFiles is every non-ignored file in the repository, for the go-to-file palette.
func ListRepoFiles(ctx context.Context, repo string) ([]string, error) {
	return walk(ctx, gitIgnoreChecker{}, repo)
}

// maxSearchFileBytes is the largest file search and replace will read (1 MiB).
//
// Skipped entirely rather than partially read: a half-searched file reports hits at line numbers
// that do not mean anything past the cut.
const maxSearchFileBytes = 1 << 20

// readTextFile reads a candidate for search or replace, or reports that it is not one.
//
// Two gates, both silent: over the size cap, or binary. Neither is an error — a repository full of
// images and bundles would otherwise produce a page of warnings for every search.
func readTextFile(full string) (string, bool) {
	info, err := os.Stat(full)
	if err != nil || info.IsDir() || info.Size() > maxSearchFileBytes {
		return "", false
	}

	content, err := os.ReadFile(full) //nolint:gosec // G304: a path the walk produced inside the repo
	if err != nil {
		return "", false
	}
	if looksBinary(content) {
		return "", false
	}
	return string(content), true
}

// looksBinary is grep's own heuristic: a NUL byte in the first 8 KiB.
//
// Not a content-type sniff and not an extension list. It is wrong for a UTF-16 text file, which is
// exactly as wrong as grep is, and matching grep is what makes the results predictable.
func looksBinary(content []byte) bool {
	head := content
	if len(head) > 8192 {
		head = head[:8192]
	}
	return strings.IndexByte(string(head), 0) >= 0
}
