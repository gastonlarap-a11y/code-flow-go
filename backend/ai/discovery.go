package ai

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Finding an AI CLI (AI-005 … AI-007).
//
// The problem this solves is not "where is the binary" but "where is it when the app cannot see
// it". A macOS app launched from Finder inherits launchd's minimal PATH, not the user's; a Windows
// app that was already running when a CLI was installed keeps the stale pre-install PATH. In both
// cases the CLI is there and the app reports it missing.

// installDirs are the directories the AI CLI installers are known to write into.
//
// Searched **before** the inherited PATH, so a working install is found even when the environment
// knows nothing about it. Each entry earned its place by an install that otherwise probed as
// missing — the Codex one especially: its desktop app ships the CLI and deliberately does not put
// it on PATH, so without that entry a perfectly good install reports "not found".
func installDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	if runtime.GOOS == "windows" {
		return existing([]string{
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".claude", "local"),
			filepath.Join(home, ".opencode", "bin"),
			filepath.Join(os.Getenv("APPDATA"), "npm"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "agy", "bin"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "OpenAI", "Codex", "bin"),
		})
	}

	return existing([]string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".claude", "local"),
		filepath.Join(home, ".opencode", "bin"),
		filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, "Library", "pnpm"),
		filepath.Join(home, ".npm-global", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
	})
}

// existing drops entries whose home or environment variable was empty, so a missing %APPDATA%
// does not produce a search of the filesystem root.
func existing(dirs []string) []string {
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if dir == "" || dir == string(os.PathSeparator) {
			continue
		}
		// Kept even when absent: the list is also the child's PATH, and a directory that appears
		// after an install should work without restarting the app.
		out = append(out, dir)
	}
	return out
}

// SearchDirs is where a binary is looked for: the install directories, then the inherited PATH.
//
// The same list becomes the child's own PATH, so a CLI's subprocesses — git, node — see the
// augmented search space too. Without that, `claude` is found and then fails because it cannot
// find `node`.
func SearchDirs() []string {
	dirs := installDirs()
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry != "" {
			dirs = append(dirs, entry)
		}
	}
	return dirs
}

// windowsExtensions are tried in this order within each directory.
//
// A real `.exe` beats a `.cmd` or `.bat` shim — but **only inside the same directory**. An earlier
// directory's shim still wins over a later directory's native executable, because directory order
// is the user's search path and overriding it would be this code second-guessing them.
var windowsExtensions = []string{".exe", ".cmd", ".bat"}

// ResolveBinary turns a configured command into what actually gets executed (AI-006, AI-007).
//
// A name that already carries a path separator or an extension is trusted exactly as given and
// never searched — that is the manual `{provider}_binary_path` escape hatch, and second-guessing
// an absolute path the user typed would defeat the point of the setting.
//
// Everywhere but Windows this is otherwise a no-op: the child's augmented PATH finds a bare name,
// and Unix has no executable-extension quirk. On Windows it matters a great deal, because
// CreateProcess only auto-appends `.exe` — so a Node CLI installed as a `.cmd` shim, which is how
// opencode and agy arrive through npm, is invisible to a bare spawn and could not be executed
// directly even if it were found.
func ResolveBinary(binary string, dirs []string) string {
	if binary == "" || strings.ContainsRune(binary, os.PathSeparator) ||
		strings.ContainsRune(binary, '/') || filepath.Ext(binary) != "" {
		return binary
	}
	if runtime.GOOS != "windows" {
		return binary
	}

	for _, dir := range dirs {
		for _, extension := range windowsExtensions {
			candidate := filepath.Join(dir, binary+extension)
			if isExecutableFile(candidate) {
				return candidate
			}
		}
	}
	return binary
}

// FindOnPath locates a binary, answering the absolute path or whether it is there at all.
//
// Used by the Settings availability badge and before every run. It answers an absolute path rather
// than a bare name on purpose: on Windows the resolved path is what routes a `.cmd` through its
// interpreter correctly, and everywhere it is what the badge shows the user.
func FindOnPath(binary string) (string, bool) {
	if binary == "" {
		return "", false
	}

	// An explicit path is checked where it points, not searched for.
	if strings.ContainsRune(binary, os.PathSeparator) || strings.ContainsRune(binary, '/') {
		return binary, isExecutableFile(binary)
	}

	dirs := SearchDirs()
	if runtime.GOOS == "windows" {
		resolved := ResolveBinary(binary, dirs)
		return resolved, resolved != binary
	}

	for _, dir := range dirs {
		candidate := filepath.Join(dir, binary)
		if isExecutableFile(candidate) {
			return candidate, true
		}
	}
	return binary, false
}

// isExecutableFile reports whether a path is a file this process could run.
//
// The executable bit is not checked on Windows, where it does not exist — a file in one of the
// three extensions is executable by definition there.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}
