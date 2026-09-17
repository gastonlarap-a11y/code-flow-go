package git

import (
	"context"
	"fmt"
	"strings"
)

// FileStatus is one changed path. `status` is a short code the renderer maps to an icon and a
// colour; the vocabulary is 2.x's and the renderer switches on it exactly.
type FileStatus struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// RepoStatus is what `get_status` answers — the most-called command in the application, and the
// one whose shape the whole Changes panel is built on.
//
// Four buckets rather than one list with flags, because that is how the UI groups them and how
// 2.x reported them. The slices are always non-nil: the renderer maps over all four without
// guarding, so a nil one is a blank panel on a clean repository.
type RepoStatus struct {
	Staged        []FileStatus `json:"staged"`
	Unstaged      []FileStatus `json:"unstaged"`
	Untracked     []FileStatus `json:"untracked"`
	Conflicted    []FileStatus `json:"conflicted"`
	CurrentBranch *string      `json:"current_branch"`
	IsDetached    bool         `json:"is_detached"`
}

// IsRepo reports whether path is inside a git work tree.
//
// On the exit code, never on stderr's text: the message differs between git versions and
// translations, and `git rev-parse` is explicitly designed to be tested this way.
func IsRepo(ctx context.Context, repo string) bool {
	result, err := NewRunner(repo).Run(ctx, "rev-parse", "--is-inside-work-tree")
	return err == nil && !result.Failed()
}

// Status reads the working tree (GIT-001, GIT-011).
//
// `--porcelain=v2 -z` because v1 is ambiguous about renames and about paths containing spaces or
// newlines, and `-z` removes the quoting question entirely. `--find-renames` so a moved file is
// one entry rather than a delete plus an add, and `--untracked-files=all` so a new directory is
// listed as its individual files — which is what the user expects to tick.
func Status(ctx context.Context, repo string) (RepoStatus, error) {
	status := RepoStatus{
		Staged:     make([]FileStatus, 0, 8),
		Unstaged:   make([]FileStatus, 0, 8),
		Untracked:  make([]FileStatus, 0, 8),
		Conflicted: make([]FileStatus, 0, 2),
	}

	result, err := NewRunner(repo).Run(ctx, "status", "--porcelain=v2", "-z",
		"--untracked-files=all", "--find-renames", "--branch")
	if err != nil {
		return status, err
	}
	if result.Failed() {
		return status, fmt.Errorf("git status failed: %s", result.Detail())
	}

	parseStatus(result.Stdout, &status)
	return status, nil
}

// parseStatus walks porcelain v2's NUL-separated records.
//
// Kept separate from the invocation so the table-driven tests can feed it git's real output
// captured once, rather than building a repository per case.
func parseStatus(output string, status *RepoStatus) {
	// Lines() already stripped the trailing newline the runner adds per line, so records are
	// separated by NUL and the last one may be empty.
	fields := strings.Split(strings.TrimRight(output, "\n\x00"), "\x00")

	for i := 0; i < len(fields); i++ {
		record := fields[i]
		if record == "" {
			continue
		}

		switch record[0] {
		case '#':
			parseBranchHeader(record, status)
		case '1':
			parseOrdinary(record, status)
		case '2':
			// A rename record is followed by its original path as a separate NUL-terminated
			// field. Consuming it here is what keeps the old path from being read as a record of
			// its own on the next iteration — which would produce a phantom entry whose first
			// character happens to decide its bucket.
			var origin string
			if i+1 < len(fields) {
				origin = fields[i+1]
				i++
			}
			parseRename(record, origin, status)
		case 'u':
			parseUnmerged(record, status)
		case '?':
			status.Untracked = append(status.Untracked, FileStatus{
				Path:   strings.TrimSpace(record[1:]),
				Status: "untracked",
			})
		}
	}
}

// parseBranchHeader reads the `# branch.*` records.
func parseBranchHeader(record string, status *RepoStatus) {
	switch {
	case strings.HasPrefix(record, "# branch.head "):
		head := strings.TrimSpace(strings.TrimPrefix(record, "# branch.head "))
		if head == "(detached)" {
			// Both halves matter: a detached HEAD has no branch *name*, and the renderer shows a
			// different header for it. Reporting the literal "(detached)" as a branch name is the
			// mistake this guards against.
			status.IsDetached = true
			status.CurrentBranch = nil
			return
		}
		status.CurrentBranch = &head
	}
}

// parseOrdinary handles a `1 <XY> ...` record: a change that is not a rename or a conflict.
//
// XY is the staged status and the worktree status. The bucket rule is 2.x's and is not obvious:
// a path that is staged *and* then modified again is reported **once, as staged**. git prints it
// as a single `1 MM` record, and 2.x put it in one bucket — showing it in both would let the user
// tick the same file twice and stage half of it.
func parseOrdinary(record string, status *RepoStatus) {
	fields := strings.SplitN(record, " ", 9)
	if len(fields) < 9 {
		return
	}
	xy, path := fields[1], fields[8]
	if len(xy) < 2 {
		return
	}

	staged, worktree := xy[0], xy[1]
	if staged != '.' {
		status.Staged = append(status.Staged, FileStatus{Path: path, Status: statusCode(staged)})
		return
	}
	if worktree != '.' {
		status.Unstaged = append(status.Unstaged, FileStatus{Path: path, Status: statusCode(worktree)})
	}
}

// parseRename handles a `2 <XY> ... <score> <new>` record plus its original path.
func parseRename(record, origin string, status *RepoStatus) {
	fields := strings.SplitN(record, " ", 10)
	if len(fields) < 10 {
		return
	}
	xy, path := fields[1], fields[9]
	if len(xy) < 2 {
		return
	}

	entry := FileStatus{Path: path, Status: "renamed"}
	if origin != "" {
		// The renderer shows "old → new" for a rename, and the arrow is what makes a move
		// readable in a list of forty files.
		entry.Path = origin + " → " + path
	}

	if xy[0] != '.' {
		status.Staged = append(status.Staged, entry)
		return
	}
	status.Unstaged = append(status.Unstaged, entry)
}

// parseUnmerged handles a `u <XY> ...` record: a conflicted path.
//
// Conflicts take priority over every other bucket. A conflicted file is also technically modified,
// and listing it as unstaged too would offer the user a "stage" button that resolves nothing.
func parseUnmerged(record string, status *RepoStatus) {
	fields := strings.SplitN(record, " ", 11)
	if len(fields) < 11 {
		return
	}
	status.Conflicted = append(status.Conflicted, FileStatus{Path: fields[10], Status: "conflicted"})
}

// statusCode maps porcelain v2's single letter to the vocabulary the renderer switches on.
func statusCode(letter byte) string {
	switch letter {
	case 'M':
		return "modified"
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'T':
		return "typechange"
	default:
		return "modified"
	}
}
