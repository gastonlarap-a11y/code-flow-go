// Package files is the working tree as the editor sees it: listing, reading, writing, moving and
// creating, plus search, replace and the watcher that tells the renderer something changed.
package files

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// There are **two** containment guards here and they are not interchangeable.
//
// One is for paths expected to already exist, the other for paths about to be created, and the
// difference is what each can trust. resolveWithinRepo trusts the filesystem to collapse `..` and
// symlinks by resolving them; resolveNewPath cannot, because there is nothing on disk yet to
// resolve, so it inspects the components instead and rejects anything that is not a plain name.
//
// A reimplementation using one guard for both purposes gets it wrong in one of two ways: requiring
// prior existence rejects every legitimate create, and using the existence guard's fallback on a
// path that is not there admits a `..` that escapes.

// errEscapes is the refusal both reads and moves give. VERBATIM — the renderer shows it as-is.
var errEscapes = errors.New("path escapes the repository root")

// resolveWithinRepo turns a repo-relative path into an absolute one, refusing anything outside the
// repository (FILE-001).
//
// A candidate that does not exist is resolved rather than rejected: the renderer's file tree
// resolves paths mid-drag and against stale references, and erroring there would break ordinary
// use. Join cleans `..` away textually, which is what closed BUG-FILE-a; resolving the deepest
// ancestor that does exist is what closes BUG-FILE-c, a symlinked folder the name passes through.
func resolveWithinRepo(repo, relPath string) (string, error) {
	base, err := canonical(repo)
	if err != nil {
		return "", fmt.Errorf("invalid repo path: %w", err)
	}

	resolved := canonicalThroughExisting(filepath.Join(base, filepath.FromSlash(relPath)))
	if !within(base, resolved) {
		return "", errEscapes
	}
	return resolved, nil
}

// resolveNewPath turns the name of something about to be created into an absolute path (FILE-001).
//
// Used only by create_dir and create_file. Every component has to be a plain name, which is a
// stricter rule than the other guard's and the only one available: a path that does not exist
// cannot be canonicalised, so there is nothing to resolve a `..` against.
func resolveNewPath(repo, relPath string) (string, error) {
	trimmed := strings.TrimSpace(relPath)
	if trimmed == "" {
		// VERBATIM. Checked before the component rule, so an all-whitespace name reports this
		// rather than "invalid path".
		return "", errors.New("name cannot be empty")
	}

	if !isPlainRelativePath(trimmed) {
		// VERBATIM, and it interpolates the **original** argument, untrimmed — 2.x did, and the
		// message is the one users have been reading.
		return "", fmt.Errorf("invalid path: %s", relPath)
	}

	base, err := canonical(repo)
	if err != nil {
		return "", fmt.Errorf("invalid repo path: %w", err)
	}
	// Plain names cannot climb out of base by spelling, but they can by walking through a symlinked
	// folder that already exists (BUG-FILE-c), so the containment check still runs on what they
	// resolve to.
	resolved := canonicalThroughExisting(filepath.Join(base, filepath.FromSlash(trimmed)))
	if !within(base, resolved) {
		return "", errEscapes
	}
	return resolved, nil
}

// isPlainRelativePath reports whether every component is an ordinary name.
//
// Rejects absolute paths, drive letters and UNC prefixes, `.` and `..`, in either separator — the
// renderer sends `/`-separated paths and Windows accepts both, so a check that only looked at the
// platform separator would let `..\escaped` through on Windows.
func isPlainRelativePath(path string) bool {
	if filepath.IsAbs(path) || filepath.VolumeName(path) != "" || strings.HasPrefix(path, "/") {
		return false
	}

	components := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
	if len(components) == 0 {
		return false
	}
	for _, component := range components {
		if component == "." || component == ".." || strings.TrimSpace(component) == "" {
			return false
		}
	}
	// A trailing or repeated separator would have been dropped by FieldsFunc; rebuilding and
	// comparing catches the shapes that are not a plain join of those names.
	return true
}

// canonical is the absolute, symlink-resolved form of a path.
//
// Both steps matter. Without EvalSymlinks a repository reached through a symlink compares unequal
// to its own contents, and on macOS every t.TempDir() lives under /var, which is itself a symlink
// to /private/var — so the containment check would reject the repository's own files.
func canonical(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

// canonicalThroughExisting is canonical for a path that may not exist yet: the deepest ancestor that
// does exist is resolved, and the missing components are joined back on unchanged.
//
// The missing components need no resolving of their own — nothing is on disk to be a symlink — with
// one exception this cannot see: a dangling symlink, which exists as a name but not as a target.
// That case is left to os.Root, which refuses to follow a link out of the repository (see
// inRepository).
func canonicalThroughExisting(path string) string {
	var missing []string
	for current := path; ; {
		if resolved, err := canonical(current); err == nil {
			slices.Reverse(missing)
			return filepath.Join(append([]string{resolved}, missing...)...)
		}
		parent := filepath.Dir(current)
		if parent == current {
			// Nothing along the path exists, not even the volume root. The containment check then
			// judges the path as written, which is what it did before this existed.
			return path
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

// inRepository runs op against the repository through an os.Root, handing it the repo-relative form
// of an absolute path the guards above already accepted.
//
// The guards decide, with the messages the renderer shows; os.Root enforces, at the moment of the
// write, what a check made a moment earlier cannot: a dangling symlink as the final component, and
// a link swapped in between the check and the use. Only writes go through it. os.Root also refuses
// every absolute symlink, even one pointing inside the repository, and the guards hand it the
// canonical path precisely so that intermediate links never reach it.
func inRepository(base, path string, op func(root *os.Root, rel string) error) error {
	rel, err := filepath.Rel(base, path)
	if err != nil || !filepath.IsLocal(rel) {
		return errEscapes
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return fmt.Errorf("invalid repo path: %w", err)
	}
	// A directory handle opened only to be read through; failing to close it changes nothing the
	// caller could act on, and op's result is the one that matters.
	defer func() { _ = root.Close() }()
	return op(root, rel)
}

// within reports whether path is base or lies underneath it, comparing whole components.
//
// Component-wise, not a string prefix: `/repo-backup` starts with `/repo` as text and is a
// different directory.
func within(base, path string) bool {
	if path == base {
		return true
	}
	return strings.HasPrefix(path, base+string(os.PathSeparator))
}

// repoRelative turns an absolute path back into the `/`-separated form the renderer uses.
func repoRelative(base, path string) (string, error) {
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		// VERBATIM. Unreachable given the guards above, which is why it says what it says: if it
		// ever fires, something moved the file out of the repository between check and use.
		return "", errors.New("moved outside the repository")
	}
	return filepath.ToSlash(rel), nil
}
