// Package platform owns the things that differ per operating system and per machine: where
// CodeFlow keeps its data, the PATH a GUI app inherits on macOS, and which network failures are
// worth retrying.
//
// Everything here is part of the drop-in contract (MIGRATION-GO.md §5). CodeFlow 3.0 installs over
// 2.7.x and must find that install's database, logs, clones and workspace folders exactly where it
// left them. A path that moves does not fail loudly — it silently presents the user with an empty
// application and their real data still on disk.
package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Paths resolves every location CodeFlow reads or writes, from one base directory.
//
// It is a value, not a package of functions, for one reason: the base is a parameter. The 2.x C#
// resolved it from a static property, and the test suite consequently wrote its fixtures into the
// developer's real ~/CodeFlow — the incident that left 45 stray lines in a personal log file.
// Every consumer takes a Paths, so a test can hand it a temp directory.
type Paths struct{ base string }

// windowsBase is a literal, not %LOCALAPPDATA% (BOOT-003, DIVERGENCE-BOOT-a).
//
// It is wrong by every Windows convention and it is kept anyway: every 2.x install on Windows has
// its database here, the NSIS uninstaller hardcodes the same string a second time, and the 2.x
// Electron shell hardcoded it a third. Moving it in 3.0 would strand those users' data. Changing
// it is its own migration, not a side effect of a port.
const windowsBase = `C:\CodeFlow`

// DefaultPaths resolves the base directory the way 2.x did (BOOT-003).
//
// The fallback when the home directory cannot be resolved is a relative "CodeFlow", which is
// deliberate: an app that writes beside its working directory is recoverable, one that refuses to
// start is not.
func DefaultPaths() Paths {
	if runtime.GOOS == "windows" {
		return Paths{base: windowsBase}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return Paths{base: "CodeFlow"}
	}
	return Paths{base: filepath.Join(home, "CodeFlow")}
}

// NewPaths roots everything at an explicit directory. Tests use it; so does the Phase 2 check that
// opens a copy of a real 2.7.1 database.
func NewPaths(base string) Paths { return Paths{base: base} }

// Base is {base} itself: ~/CodeFlow, or C:\CodeFlow on Windows.
func (p Paths) Base() string { return p.base }

// Logs holds errors.log, startup.log and shell.log (BOOT-030/031).
func (p Paths) Logs() string { return filepath.Join(p.base, "logs") }

// Repos is the clone root (BOOT-004).
func (p Paths) Repos() string { return filepath.Join(p.base, "repos") }

// Database is the SQLite file, alongside its -wal and -shm siblings (BOOT-004).
func (p Paths) Database() string { return filepath.Join(p.base, "codeflow.db") }

// ResetMarker is the empty file that asks the next launch to wipe {base} (BOOT-001/006/017).
//
// The wipe happens at start-up, before anything opens the database, because deleting a file a live
// SQLite connection holds is not reliably possible on Windows.
func (p Paths) ResetMarker() string { return filepath.Join(p.base, ".reset-pending") }

// Workspace is one workspace's own directory.
func (p Paths) Workspace(id string) string { return filepath.Join(p.base, "workspaces", id) }

// WorkspaceSkills is where a workspace's installed skills live. Created by whoever writes into it,
// not at start-up (BOOT-004/005).
func (p Paths) WorkspaceSkills(id string) string { return filepath.Join(p.Workspace(id), "skills") }

// WorkspaceMCPConfig is a workspace's mcp.json, handed to the AI engines that accept one.
func (p Paths) WorkspaceMCPConfig(id string) string {
	return filepath.Join(p.Workspace(id), "mcp.json")
}

// Tickets is the default work-item mirror root, unless the tickets_root_dir setting overrides it.
func (p Paths) Tickets() string { return filepath.Join(p.base, "tickets") }

// PRLinkReviews holds PULL_REQUEST.md and changes.diff per reviewed link.
func (p Paths) PRLinkReviews() string { return filepath.Join(p.base, "pr-link-reviews") }

// StartupDirectories are the three directories start-up creates, in this order, and no others
// (BOOT-005). Everything else is created by the feature that first needs it.
func (p Paths) StartupDirectories() []string {
	return []string{p.Base(), p.Logs(), p.Repos()}
}

// EnsureStartupDirectories is the "directories" start-up stage.
func (p Paths) EnsureStartupDirectories() error {
	for _, dir := range p.StartupDirectories() {
		if err := os.MkdirAll(dir, DirPerm); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}

// DownloadsDirectory is where the updater puts a downloaded installer (BOOT-021). It is the user's
// own Downloads folder rather than anywhere under {base}, because the hand-off is the user
// double-clicking the file in Finder or Explorer.
func DownloadsDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "Downloads"
	}
	return filepath.Join(home, "Downloads")
}
