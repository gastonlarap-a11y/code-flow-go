package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// emptyTree is git's hash for the empty tree object. It is a constant of the format, identical in
// every repository, and it is how a root commit — which has no parent to diff against — is
// compared: `git diff <emptyTree> <oid>`.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// fullFileArgs are appended to every diff.
//
// `-U1000000` is the port of the C# FullFile() options: the renderer shows a diff with complete
// context rather than islands around each change, and the number is simply larger than any file
// anyone edits. `-M` keeps rename detection, without which every move is a delete plus an add.
// `--no-ext-diff` stops a user's configured external diff tool from replacing the output with
// something unparseable, and `--no-color` is belt-and-braces over the runner's `color.ui=never`.
var fullFileArgs = []string{"--no-color", "--no-ext-diff", "-M", "-U1000000"}

// WorkingDiff is the unstaged changes (GIT-010).
//
// Untracked files are included, which git's own `diff` does not do: 2.x showed them because a user
// looking at "what have I changed" means the new files too. Each is diffed against /dev/null
// separately, since there is no single git command that mixes tracked and untracked content.
func WorkingDiff(ctx context.Context, repo string) ([]FileDiff, error) {
	runner := NewRunner(repo)

	result, err := runner.Run(ctx, append([]string{"diff"}, fullFileArgs...)...)
	if err != nil {
		return nil, err
	}
	if result.Failed() {
		return nil, fmt.Errorf("git diff failed: %s", result.Detail())
	}
	diffs := parseUnifiedDiff(result.Stdout)

	untracked, err := untrackedDiffs(ctx, runner)
	if err != nil {
		return nil, err
	}
	return append(diffs, untracked...), nil
}

// untrackedDiffs renders each untracked file as an addition.
func untrackedDiffs(ctx context.Context, runner Runner) ([]FileDiff, error) {
	listed, err := runner.Run(ctx, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	if listed.Failed() {
		return nil, fmt.Errorf("list untracked files: %s", listed.Detail())
	}

	diffs := make([]FileDiff, 0, 4)
	for _, path := range splitNUL(listed.Stdout) {
		// Exit code 1 means "there are differences", which for a file against /dev/null is always
		// true and is not a failure. Anything else — a binary file, an unreadable one — is skipped
		// rather than failing the whole panel.
		result, err := runner.Run(ctx, append(append([]string{"diff"}, fullFileArgs...),
			"--no-index", "--", os.DevNull, path)...)
		if err != nil || result.ExitCode > 1 {
			continue
		}

		for _, diff := range parseUnifiedDiff(result.Stdout) {
			diff.Status = "added"
			diff.OldPath = nil
			// `--no-index` prints the path as given, which is relative to the repository already.
			diff.NewPath = strPtr(filepath.ToSlash(path))
			diffs = append(diffs, diff)
		}
	}
	return diffs, nil
}

// StagedDiff is what would be committed.
func StagedDiff(ctx context.Context, repo string) ([]FileDiff, error) {
	result, err := NewRunner(repo).Run(ctx, append([]string{"diff", "--cached"}, fullFileArgs...)...)
	if err != nil {
		return nil, err
	}
	if result.Failed() {
		return nil, fmt.Errorf("git diff --cached failed: %s", result.Detail())
	}
	return parseUnifiedDiff(result.Stdout), nil
}

// CommitDiff is one commit against its first parent.
//
// A root commit has no parent, so it is compared against the empty tree instead — otherwise the
// very first commit in a repository shows nothing, which is the state a new user is most likely to
// be looking at.
func CommitDiff(ctx context.Context, repo, oid string) ([]FileDiff, error) {
	runner := NewRunner(repo)

	args := append([]string{"diff"}, fullFileArgs...)
	if isRootCommit(ctx, runner, oid) {
		args = append(args, emptyTree, oid)
	} else {
		args = append(args, oid+"^!")
	}

	result, err := runner.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	if result.Failed() {
		return nil, fmt.Errorf("git diff failed: %s", result.Detail())
	}
	return parseUnifiedDiff(result.Stdout), nil
}

// CommitFileDiff is one file's changes within a commit.
//
// oldPath is passed when the file was renamed: git needs both names to follow the rename across
// the commit, and without the old one a renamed file's diff comes back as an addition.
func CommitFileDiff(ctx context.Context, repo, oid, filePath string, oldPath *string) ([]FileDiff, error) {
	runner := NewRunner(repo)

	args := append([]string{"diff"}, fullFileArgs...)
	if isRootCommit(ctx, runner, oid) {
		args = append(args, emptyTree, oid)
	} else {
		args = append(args, oid+"^!")
	}
	args = append(args, "--", filePath)
	if oldPath != nil && *oldPath != "" && *oldPath != filePath {
		args = append(args, *oldPath)
	}

	result, err := runner.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	if result.Failed() {
		return nil, fmt.Errorf("git diff failed: %s", result.Detail())
	}
	return parseUnifiedDiff(result.Stdout), nil
}

// CommitFile is one entry of a commit's file list — no content, just what happened to it.
type CommitFile struct {
	OldPath *string `json:"old_path"`
	NewPath *string `json:"new_path"`
	Status  string  `json:"status"`
}

// ListCommitFiles names the files a commit touched (GIT-035).
//
// `diff-tree` rather than `diff` because the list needs no content, and on a commit touching
// hundreds of files the difference is the panel opening instantly rather than after a pause.
func ListCommitFiles(ctx context.Context, repo, oid string) ([]CommitFile, error) {
	result, err := NewRunner(repo).Run(ctx,
		"diff-tree", "-r", "-z", "-M", "--name-status", "--root", oid)
	if err != nil {
		return nil, err
	}
	if result.Failed() {
		return nil, fmt.Errorf("git diff-tree failed: %s", result.Detail())
	}

	files := make([]CommitFile, 0, 8)
	fields := splitNUL(result.Stdout)

	// The first field of a root listing is the commit id itself; every record after it is a status
	// followed by one path, or two for a rename.
	for i := 0; i < len(fields); i++ {
		code := fields[i]
		if code == "" || len(code) > 3 || !isStatusCode(code[0]) {
			continue
		}
		if i+1 >= len(fields) {
			break
		}

		first := fields[i+1]
		i++

		entry := CommitFile{Status: statusCode(code[0]), NewPath: strPtr(first)}
		switch code[0] {
		case 'D':
			entry.OldPath, entry.NewPath = strPtr(first), nil
		case 'R', 'C':
			if i+1 < len(fields) {
				entry.OldPath, entry.NewPath = strPtr(first), strPtr(fields[i+1])
				i++
			}
		case 'A':
			entry.OldPath = nil
		default:
			entry.OldPath = strPtr(first)
		}
		files = append(files, entry)
	}
	return files, nil
}

// isRootCommit reports whether a commit has no parents.
func isRootCommit(ctx context.Context, runner Runner, oid string) bool {
	result, err := runner.Run(ctx, "rev-list", "--parents", "-n", "1", oid)
	if err != nil || result.Failed() {
		return false
	}
	// The output is the commit's own id followed by its parents; one field means no parents.
	return len(strings.Fields(result.Stdout)) == 1
}

func isStatusCode(letter byte) bool {
	return strings.IndexByte("AMDRCTUXB", letter) >= 0
}

// splitNUL splits a -z output into fields, dropping the empties the trailing separator leaves.
//
// The runner rebuilds output line by line and appends a newline to each, so those are trimmed
// first — git's -z records have no newlines of their own.
func splitNUL(output string) []string {
	trimmed := strings.ReplaceAll(output, "\n", "")
	fields := strings.Split(trimmed, "\x00")

	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}
