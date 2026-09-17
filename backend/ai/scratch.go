// Package ai will hold the engine routing, run registry and the six CLI engines (Phase 4).
//
// What exists now is the scratch-file lifecycle, because the start-up sequence sweeps it: the
// "scratch-sweep" stage runs before storage opens, on every launch.
package ai

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The naming contract for the files engines hand to their CLIs. Two engines need one: opencode
// takes its prompt through --file, and agy takes an oversized brief from a directory it is granted
// with --add-dir.
//
// One owner for creation, recognition and sweeping, as in 2.x. Splitting those three across files
// is how BUG-AI-a survived a whole release: each creation site assumed someone else would clean
// up, and nobody did.
const (
	openCodePrefix = "codeflow-opencode-"
	agyPrefix      = "codeflow-agy-"
)

// OrphanAge is how old a scratch entry must be before the start-up sweep may claim it.
//
// An age check rather than a lock or a pid file, because it needs no coordination: a development
// build and an installed CodeFlow can run at the same time, and a young file may be another
// process's live invocation. An orphan, by definition, has no process left to touch it.
const OrphanAge = time.Hour

// SweepOrphans deletes scratch entries in dir older than OrphanAge and reports how many it removed.
//
// It never returns an error. A temp directory that cannot be read, or an entry another process
// holds open, is not a reason to fail a launch — the next launch is the second chance, and the
// cost of a missed sweep is disk space.
func SweepOrphans(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	cutoff := time.Now().Add(-OrphanAge)
	removed := 0
	for _, entry := range entries {
		if !isScratchName(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		// Ignored: an entry another process holds open stays until the next sweep, which is the
		// documented behaviour rather than a failure.
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err == nil {
			removed++
		}
	}
	return removed
}

// IsScratchPath reports whether a path is one of this package's scratch entries.
//
// Recognition by name and location rather than by threading a list of paths through every engine:
// the arguments a built command carries already name every scratch file, and the prefix and temp
// root are this package's own contract.
func IsScratchPath(path string) bool {
	if path == "" {
		return false
	}
	cleaned := filepath.Clean(path)
	tempRoot := filepath.Clean(os.TempDir())

	rel, err := filepath.Rel(tempRoot, cleaned)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}

	// The first segment under the temp root is the entry the sweep would claim: the opencode file
	// itself, or agy's directory (whose brief.txt lives inside it).
	first, _, _ := strings.Cut(rel, string(os.PathSeparator))
	return isScratchName(first)
}

func isScratchName(name string) bool {
	return strings.HasPrefix(name, openCodePrefix) || strings.HasPrefix(name, agyPrefix)
}
