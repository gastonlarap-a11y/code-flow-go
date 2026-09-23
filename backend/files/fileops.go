package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

// FileEntry is one row of the file tree.
type FileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

// ListDir reads one directory level, the repository root when subPath is empty (FILE-002).
//
// Directories sort before files and then case-insensitively by name, which is the order the tree
// renders without re-sorting.
func ListDir(repo string, subPath *string) ([]FileEntry, error) {
	base, err := canonical(repo)
	if err != nil {
		return nil, fmt.Errorf("invalid repo path: %w", err)
	}

	dir := base
	if subPath != nil && *subPath != "" {
		if dir, err = resolveWithinRepo(repo, *subPath); err != nil {
			return nil, err
		}
	}

	read, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	entries := make([]FileEntry, 0, len(read))
	for _, entry := range read {
		// `.git` is hidden by literal name. Not by any ignore rule — it is not ignored, it is the
		// repository's own machinery and showing it in an editor's tree is noise at best.
		if entry.Name() == ".git" {
			continue
		}

		// The type comes from the entry the enumeration produced, never from a second question
		// about the name (FILE-017). Asking again can be **refused while the enumeration
		// succeeded** — a repository under a TCC-protected folder on macOS answers "not a
		// directory" for every folder, and the tree then shows a repository with no folders at all.
		isDir, err := entryIsDir(dir, entry)
		if err != nil {
			// Loud on purpose: the renderer keeps its last good listing rather than replacing it
			// with whatever survived.
			return nil, err
		}

		rel, err := repoRelative(base, filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		entries = append(entries, FileEntry{Name: entry.Name(), Path: rel, IsDir: isDir})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}

// entryIsDir classifies one directory entry, following a symlink to what it points at.
//
// A symlink to a directory reports as a directory, which is 2.x's behaviour and what the tree needs
// to offer expanding it. os.DirEntry.IsDir() alone says "symlink", so the link has to be followed —
// and a broken one is an error rather than a guess.
func entryIsDir(dir string, entry os.DirEntry) (bool, error) {
	if entry.Type()&os.ModeSymlink == 0 {
		return entry.IsDir(), nil
	}
	info, err := os.Stat(filepath.Join(dir, entry.Name()))
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

// ReadFileText reads a repo-relative file as text.
//
// A directory is reported in its own words rather than as the operating system's "is a directory",
// which arrives differently worded on each platform and reads like a bug.
func ReadFileText(repo, relPath string) (string, error) {
	full, err := resolveWithinRepo(repo, relPath)
	if err != nil {
		return "", err
	}

	if info, err := os.Stat(full); err == nil && info.IsDir() {
		// VERBATIM.
		return "", fmt.Errorf("%s is a folder, not a file", relPath)
	}

	// gosec G304: the path is the point. This command exists to read a file the user named, and it
	// has already been through resolveWithinRepo — which is the containment check G304 is asking
	// for, done properly against the canonicalised repository root.
	content, err := os.ReadFile(full) //nolint:gosec // G304: contained by resolveWithinRepo above
	if err != nil {
		return "", err
	}

	// Strict, not lossy: this text goes into the editor and back out through write_file_text, and
	// silently replacing undecodable bytes with U+FFFD would corrupt the file on the next save.
	// Process output is the opposite case and decodes lossily — see shared/proc.
	if !utf8.Valid(content) {
		return "", fmt.Errorf("%s is not valid UTF-8 text", relPath)
	}
	return string(content), nil
}

// WriteFileText overwrites a repo-relative file.
func WriteFileText(repo, relPath, content string) error {
	full, err := resolveWithinRepo(repo, relPath)
	if err != nil {
		return err
	}
	base, err := canonical(repo)
	if err != nil {
		return fmt.Errorf("invalid repo path: %w", err)
	}
	return inRepository(base, full, func(root *os.Root, rel string) error {
		return root.WriteFile(rel, []byte(content), userFilePerm)
	})
}

// WriteFileBytes writes raw bytes to an absolute path (FILE-005).
//
// **The one file operation that is not repo-scoped, by design** (DIVERGENCE-FILE-d). It exists for
// the code-snapshot export, where the destination came from the operating system's own save dialog
// — the dialog is the authorisation, and a repository containment check here would refuse the
// Desktop the user just picked. Do not add one.
func WriteFileBytes(path string, content []byte) error {
	if !filepath.IsAbs(path) {
		// VERBATIM.
		return fmt.Errorf("expected an absolute path, got: %s", path)
	}

	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		// VERBATIM.
		return fmt.Errorf("no such folder: %s", parent)
	}
	return writeUserFile(path, content)
}

// userFilePerm and userDirPerm are the modes of what CodeFlow creates in the user's tree.
//
// 0644/0755 rather than platform.FilePerm: this is their source tree, not CodeFlow's data
// directory, and an editor that silently tightened the permissions of everything it saved would be
// a surprise nobody asked for. An existing file keeps its own mode — a write only applies the mode
// when it creates the file.
const (
	userFilePerm os.FileMode = 0o644
	userDirPerm  os.FileMode = 0o755
)

// writeUserFile writes one of the user's own files at an absolute path.
func writeUserFile(path string, content []byte) error {
	return os.WriteFile(path, content, userFilePerm) //nolint:gosec // G306: the user's file, not ours
}

// MovePath moves or renames a file or folder within the repository (FILE-003).
//
// destDir is a repo-relative directory, or empty for the repository root. The guard order is what
// makes it safe, and each step exists for a case the tree's drag-and-drop can produce.
func MovePath(repo, fromRel, destDir string) (string, error) {
	source, err := resolveWithinRepo(repo, fromRel)
	if err != nil {
		return "", err
	}

	name := filepath.Base(source)
	if name == "" || name == "." || name == string(os.PathSeparator) {
		// VERBATIM.
		return "", fmt.Errorf("cannot move %s", fromRel)
	}

	base, err := canonical(repo)
	if err != nil {
		return "", fmt.Errorf("invalid repo path: %w", err)
	}

	dest := base
	if strings.TrimSpace(destDir) != "" {
		if dest, err = resolveWithinRepo(repo, destDir); err != nil {
			return "", err
		}
	}

	info, err := os.Stat(dest)
	if err != nil || !info.IsDir() {
		// VERBATIM.
		return "", fmt.Errorf("%s is not a folder", destDir)
	}

	// A folder cannot be dropped into itself or into anything inside it. Compared on the
	// canonical paths, so a symlinked route into the subtree is caught too — without that, the
	// move succeeds and takes the whole subtree with it into a directory that no longer exists.
	if sourceInfo, err := os.Stat(source); err == nil && sourceInfo.IsDir() && within(source, dest) {
		// VERBATIM.
		return "", errors.New("cannot move a folder into itself")
	}

	target := filepath.Join(dest, name)
	if target == source {
		// Dropped back where it already lives. A no-op success, not an error: the user let go over
		// the folder it came from, which is not a mistake worth a red banner.
		return fromRel, nil
	}

	if _, err := os.Lstat(target); err == nil {
		// Refused rather than overwritten. VERBATIM.
		return "", fmt.Errorf("%s already exists here", name)
	}

	sourceRel, err := filepath.Rel(base, source)
	if err != nil {
		return "", errEscapes
	}
	if err := inRepository(base, target, func(root *os.Root, targetRel string) error {
		return root.Rename(sourceRel, targetRel)
	}); err != nil {
		return "", err
	}
	return repoRelative(base, target)
}

// CreateDir creates a repo-relative directory and every missing parent.
func CreateDir(repo, relPath string) error {
	full, err := resolveNewPath(repo, relPath)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(full); err == nil {
		// VERBATIM, with the trimmed name — unlike "invalid path", which uses the original.
		return fmt.Errorf("%s already exists", strings.TrimSpace(relPath))
	}
	base, err := canonical(repo)
	if err != nil {
		return fmt.Errorf("invalid repo path: %w", err)
	}
	return inRepository(base, full, func(root *os.Root, rel string) error {
		return root.MkdirAll(rel, userDirPerm)
	})
}

// CreateFile creates a new empty file, **never truncating an existing one** (FILE-004).
//
// O_EXCL is the whole rule: "new file" arriving as an empty existing file is how a user loses work
// by mistyping a name that already exists.
func CreateFile(repo, relPath string) error {
	full, err := resolveNewPath(repo, relPath)
	if err != nil {
		return err
	}
	base, err := canonical(repo)
	if err != nil {
		return fmt.Errorf("invalid repo path: %w", err)
	}
	return inRepository(base, full, func(root *os.Root, rel string) error {
		if err := root.MkdirAll(filepath.Dir(rel), userDirPerm); err != nil {
			return err
		}
		file, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, userFilePerm)
		if errors.Is(err, os.ErrExist) {
			// VERBATIM.
			return fmt.Errorf("%s already exists", strings.TrimSpace(relPath))
		}
		if err != nil {
			return err
		}
		return file.Close()
	})
}

// Opener hands a path to the operating system.
//
// Declared here rather than imported so this package stays free of Wails: it needs "show this to
// the user" and nothing else about the window. backend/desktop implements it.
type Opener interface {
	// OpenFile opens a file with its default application.
	OpenFile(path string) error
	// RevealInFileManager opens a directory in Explorer or Finder.
	RevealInFileManager(path string) error
}

// OpenInDefaultApp hands a repo-relative file to the operating system.
func OpenInDefaultApp(opener Opener, repo, relPath string) error {
	full, err := resolveWithinRepo(repo, relPath)
	if err != nil {
		return err
	}
	if opener == nil {
		return errors.New("no desktop available")
	}
	return opener.OpenFile(full)
}

// RevealInFileManager opens an absolute directory in the file manager.
//
// Absolute and unscoped, like WriteFileBytes: the paths it is given are project roots the user
// chose themselves, which are by definition outside any repository this could check against.
func RevealInFileManager(opener Opener, path string) error {
	if opener == nil {
		return errors.New("no desktop available")
	}
	return opener.RevealInFileManager(path)
}

// OpenInVSCode launches `code` against a path (FILE-006).
//
// On Windows `code` is a `.cmd` shim, which does not launch when spawned directly — the same class
// of problem as `npx` in the skill installer. `cmd /C` is the shim's own interpreter.
func OpenInVSCode(ctx context.Context, path string) error {
	name, args := "code", []string{path}
	if runtime.GOOS == "windows" {
		name, args = "cmd", []string{"/C", "code", path}
	}

	cmd := proc.Command(ctx, name, args...)
	if err := cmd.Start(); err != nil {
		// VERBATIM, backticks included: the toast tells the user what to check.
		return fmt.Errorf("failed to launch VS Code (is `code` on PATH?): %w", err)
	}

	// Started, not waited for: VS Code outlives this call by design, and waiting would block the
	// command until the user closed their editor.
	return nil
}
