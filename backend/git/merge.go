package git

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

// MergeOutcome is how a merge ended.
//
// Status is one of four values and **never an error** — conflicts included. A conflicted merge has
// done exactly what it was asked to: the three-way merge ran, the repository is mid-merge with
// MERGE_HEAD set, and the working tree holds the markers the conflict panel then works on. Raising
// instead would put a red banner over a state the user has to resolve.
type MergeOutcome struct {
	Status    string   `json:"status"`
	Conflicts []string `json:"conflicts"`
}

// The four outcomes, in the priority order GIT-016 decides them.
const (
	MergeUpToDate    = "up_to_date"
	MergeFastForward = "fast_forward"
	MergeMerged      = "merged"
	MergeConflicts   = "conflicts"
)

// ConflictFile is one path with unresolved stages in the index.
//
// A one-field struct because that is the shape the renderer is typed against (`ConflictFile[]`);
// flattening it to a string array here would be a wire change for no gain.
type ConflictFile struct {
	Path string `json:"path"`
}

// ConflictVersions is the three sides of one conflicted file, as text.
//
// Internal: it never crosses the bridge. The AI conflict resolver consumes it so the engine gets
// each side whole instead of reverse-engineering them out of the `<<<<<<<` markers in the working
// copy. An absent side — the file was added or deleted there — is the empty string, not an error.
type ConflictVersions struct {
	Base   string
	Ours   string
	Theirs string
}

// MergeBranch merges a branch into HEAD, resolving to one of four outcomes (GIT-016).
//
// The priority order is the whole rule, and each step answers a question the next one no longer
// has to: their commit is already an ancestor of ours (up to date), ours is an ancestor of theirs
// (fast-forward), or neither (a real three-way merge).
//
// A local branch always wins over an identically named remote one, because the remote lookup only
// happens when the local one finds nothing.
func MergeBranch(ctx context.Context, repo, branchName string, authorName, authorEmail *string) (MergeOutcome, error) {
	runner := NewRunner(repo)

	theirs, err := resolveMergeSource(ctx, runner, branchName)
	if err != nil {
		return MergeOutcome{}, err
	}
	head, err := headCommit(ctx, runner)
	if err != nil {
		return MergeOutcome{}, err
	}

	base, err := mergeBase(ctx, runner, head, theirs)
	if err != nil {
		return MergeOutcome{}, err
	}

	switch base {
	case theirs:
		return noConflicts(MergeUpToDate), nil

	case head:
		// A fast-forward moves the branch ref and forces the working tree to match: no merge commit,
		// which is what plain `git merge` does and what 2.x did through a hard reset.
		//
		// `git reset --hard` rather than `git merge --ff-only`, deliberately and against
		// MIGRATION-GO.md §8.6's suggestion: `--ff-only` refuses when the working tree holds changes
		// the fast-forward would overwrite, which would turn this outcome into an error for a user
		// who has uncommitted work. libgit2's Reset(Hard) overwrote them, so that is the behaviour
		// existing installs have — and changing it is a named decision, not a silent one.
		result, err := runner.RunWrite(ctx, "reset", "--hard", theirs)
		if err != nil {
			return MergeOutcome{}, err
		}
		if result.Failed() {
			return MergeOutcome{}, fmt.Errorf("git reset failed: %s", result.Detail())
		}
		return noConflicts(MergeFastForward), nil
	}

	// The message is written out rather than left to git: merging by OID would otherwise produce
	// "Merge commit '<sha>'", and a remote branch "Merge remote-tracking branch 'origin/x'". 2.x
	// wrote this one form for every case and the history of existing repositories reads that way.
	message := fmt.Sprintf("Merge branch '%s'", branchName)

	// The OID, not the name: the branch was already resolved with local-wins, and passing the name
	// again would let git re-resolve it to the remote one.
	result, err := runner.RunWithEnv(ctx, identityEnv(authorName, authorEmail),
		"merge", "--no-edit", "--no-verify", "-m", message, theirs)
	if err != nil {
		return MergeOutcome{}, err
	}

	conflicts, err := conflictPaths(ctx, runner)
	if err != nil {
		return MergeOutcome{}, err
	}
	if len(conflicts) > 0 {
		// Left mid-merge on purpose: no cleanup, nothing committed, MERGE_HEAD set.
		return MergeOutcome{Status: MergeConflicts, Conflicts: conflicts}, nil
	}
	if result.Failed() {
		// Failed without leaving conflicts behind: local changes in the way, unrelated histories.
		// libgit2 raised for both, and so does this.
		return MergeOutcome{}, fmt.Errorf("git merge failed: %s", result.Detail())
	}
	return noConflicts(MergeMerged), nil
}

// noConflicts builds an outcome whose conflict list is empty but never nil — the renderer maps over
// it unguarded, and a nil slice marshals as `null`.
func noConflicts(status string) MergeOutcome {
	return MergeOutcome{Status: status, Conflicts: make([]string, 0)}
}

// resolveMergeSource finds the branch to merge, local first.
func resolveMergeSource(ctx context.Context, runner Runner, branchName string) (string, error) {
	for _, ref := range []string{"refs/heads/" + branchName, "refs/remotes/" + branchName} {
		result, err := runner.Run(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
		if err != nil {
			return "", err
		}
		if !result.Failed() {
			return strings.TrimSpace(result.Stdout), nil
		}
	}
	// 2.x's exact wording, which the renderer shows as-is. It says "local" even when the remote
	// lookup also failed; that is what users have been reading for two years.
	return "", fmt.Errorf("cannot locate local branch '%s'", branchName)
}

// headCommit resolves HEAD, reporting an unborn one the way libgit2 did.
func headCommit(ctx context.Context, runner Runner) (string, error) {
	result, err := runner.Run(ctx, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	if result.Failed() {
		return "", errors.New("reference 'HEAD' not found")
	}
	return strings.TrimSpace(result.Stdout), nil
}

// mergeBase is the best common ancestor, or the empty string when the histories are unrelated.
//
// Unrelated histories are not an error here: the caller falls through to `git merge`, which refuses
// them with a message that says so far better than anything this function could invent.
func mergeBase(ctx context.Context, runner Runner, head, theirs string) (string, error) {
	result, err := runner.Run(ctx, "merge-base", head, theirs)
	if err != nil {
		return "", err
	}
	if result.Failed() {
		return "", nil
	}
	return strings.TrimSpace(result.Stdout), nil
}

// identityEnv is the resolved identity as git environment variables.
//
// Author **and** committer, both or neither — libgit2's CreateCommit took one signature for both,
// so a merge commit made from CodeFlow has them matching. `git merge` accepts no `--author`, which
// is why this goes through the environment rather than a flag.
func identityEnv(name, email *string) []string {
	if name == nil || *name == "" || email == nil || *email == "" {
		return nil
	}
	return []string{
		"GIT_AUTHOR_NAME=" + *name,
		"GIT_AUTHOR_EMAIL=" + *email,
		"GIT_COMMITTER_NAME=" + *name,
		"GIT_COMMITTER_EMAIL=" + *email,
	}
}

// ListConflicts is every path with unresolved stages, for the conflict panel (GIT-017).
func ListConflicts(ctx context.Context, repo string) ([]ConflictFile, error) {
	paths, err := conflictPaths(ctx, NewRunner(repo))
	if err != nil {
		return nil, err
	}

	files := make([]ConflictFile, 0, len(paths))
	for _, path := range paths {
		files = append(files, ConflictFile{Path: path})
	}
	return files, nil
}

// conflictPaths lists conflicted paths once each, in git's order.
//
// `git ls-files -u` prints one line per *stage*, so a file conflicting on content appears three
// times — once as the ancestor, once as ours, once as theirs. The panel shows one row per file.
func conflictPaths(ctx context.Context, runner Runner) ([]string, error) {
	stages, err := unmergedEntries(ctx, runner)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(stages))
	paths := make([]string, 0, len(stages))
	for _, entry := range stages {
		if seen[entry.path] {
			continue
		}
		seen[entry.path] = true
		paths = append(paths, entry.path)
	}
	return paths, nil
}

// unmergedEntry is one `git ls-files -u` line: a stage of one conflicted path.
type unmergedEntry struct {
	stage int
	path  string
}

// unmergedEntries reads the index's conflict stages, optionally for one path.
//
// The format is `<mode> <sha> <stage>\t<path>`, NUL-terminated per record so a filename containing
// a newline — legal, and the reason `-z` exists — cannot split one record into two.
func unmergedEntries(ctx context.Context, runner Runner, paths ...string) ([]unmergedEntry, error) {
	args := []string{"ls-files", "-u", "-z"}
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}

	result, err := runner.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	if result.Failed() {
		return nil, fmt.Errorf("git ls-files failed: %s", result.Detail())
	}

	entries := make([]unmergedEntry, 0, 8)
	for _, record := range splitNUL(result.Stdout) {
		meta, path, found := strings.Cut(record, "\t")
		if !found {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) < 3 {
			continue
		}
		stage, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		entries = append(entries, unmergedEntry{stage: stage, path: path})
	}
	return entries, nil
}

// Stage numbers as git and libgit2 both define them.
const (
	stageAncestor = 1
	stageOurs     = 2
	stageTheirs   = 3
)

// ConflictVersionsFor reads all three sides of a conflicted file (GIT-017).
//
// A side that does not exist reads as the empty string, which is the honest answer: "the file was
// deleted on that side" is information the resolver needs, not a failure.
func ConflictVersionsFor(ctx context.Context, repo, relPath string) (ConflictVersions, error) {
	runner := NewRunner(repo)

	entries, err := unmergedEntries(ctx, runner, relPath)
	if err != nil {
		return ConflictVersions{}, err
	}
	if len(entries) == 0 {
		return ConflictVersions{}, errors.New("no conflict for this path")
	}

	present := make(map[int]bool, 3)
	for _, entry := range entries {
		present[entry.stage] = true
	}

	versions := ConflictVersions{}
	for _, side := range []struct {
		stage int
		into  *string
	}{
		{stageAncestor, &versions.Base},
		{stageOurs, &versions.Ours},
		{stageTheirs, &versions.Theirs},
	} {
		if !present[side.stage] {
			continue
		}
		text, err := stageContent(ctx, runner, side.stage, relPath)
		if err != nil {
			return ConflictVersions{}, err
		}
		*side.into = text
	}
	return versions, nil
}

// stageContent reads one index stage as text, decoded the way every other process output is.
func stageContent(ctx context.Context, runner Runner, stage int, relPath string) (string, error) {
	result, err := runner.RunRaw(ctx, "show", fmt.Sprintf(":%d:%s", stage, relPath))
	if err != nil {
		return "", err
	}
	if result.Failed() {
		return "", fmt.Errorf("git show failed: %s", strings.TrimSpace(result.Stderr))
	}
	return proc.DecodeLossyUTF8(result.Stdout), nil
}

// ResolveConflictSide takes one whole side of a conflict, writes it to disk and stages it
// (GIT-017).
func ResolveConflictSide(ctx context.Context, repo, relPath, side string) error {
	if side != "ours" && side != "theirs" {
		// 2.x's exact wording.
		return errors.New("side must be 'ours' or 'theirs'")
	}
	runner := NewRunner(repo)

	entries, err := unmergedEntries(ctx, runner, relPath)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return errors.New("no conflict for this path")
	}

	wanted := stageOurs
	if side == "theirs" {
		wanted = stageTheirs
	}
	if !hasStage(entries, wanted) {
		// The side added or deleted the file, so there is nothing to take from it. Distinguished
		// from "no conflict" on purpose: the panel says which button cannot apply.
		return errors.New("that side has no content for this file (it was added/deleted)")
	}

	// `git checkout --ours|--theirs` writes that stage's bytes to the working tree. Done by git
	// rather than by reading the blob and writing it here, so the content lands byte for byte with
	// no line-ending or permission handling of our own to get wrong.
	checkout, err := runner.RunWrite(ctx, "checkout", "--"+side, "--", relPath)
	if err != nil {
		return err
	}
	if checkout.Failed() {
		return fmt.Errorf("git checkout failed: %s", checkout.Detail())
	}
	return stageResolved(ctx, runner, relPath)
}

// MarkConflictResolved stages whatever is on disk for a conflicted path, as-is (GIT-017).
//
// For the other half of the panel: the user hand-edited the file in the embedded editor instead of
// taking a whole side, and this is how that edit becomes the resolution.
func MarkConflictResolved(ctx context.Context, repo, relPath string) error {
	return stageResolved(ctx, NewRunner(repo), relPath)
}

// stageResolved clears a path's conflict stages by staging the working-tree content.
//
// One `git add` does all of it: on a still-conflicted entry it removes all three stages and re-adds
// the path as a normal staged entry, which is exactly what libgit2's Index.Add did.
func stageResolved(ctx context.Context, runner Runner, relPath string) error {
	result, err := runner.RunWrite(ctx, "add", "--", relPath)
	if err != nil {
		return err
	}
	if result.Failed() {
		return fmt.Errorf("git add failed: %s", result.Detail())
	}
	return nil
}

func hasStage(entries []unmergedEntry, stage int) bool {
	for _, entry := range entries {
		if entry.stage == stage {
			return true
		}
	}
	return false
}

// CompleteMerge commits the resolved index as a two-parent merge commit (GIT-018).
//
// MERGE_HEAD is read from the repository rather than remembered from the MergeBranch call that
// started this, so completing still works after the application was restarted mid-conflict.
func CompleteMerge(ctx context.Context, repo, message string, authorName, authorEmail *string) (string, error) {
	runner := NewRunner(repo)

	conflicts, err := conflictPaths(ctx, runner)
	if err != nil {
		return "", err
	}
	if len(conflicts) > 0 {
		// VERBATIM: 2.x's wording, shown to the user as-is. The capital is the message, not a
		// style slip — lowercasing it would change a string users have been reading for two years.
		return "", errors.New("There are still unresolved conflicts") //nolint:staticcheck // ST1005
	}

	mergeHead, err := runner.Run(ctx, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	if err != nil {
		return "", err
	}
	if mergeHead.Failed() {
		return "", errors.New("MERGE_HEAD has no target")
	}

	// git builds the two-parent commit from MERGE_HEAD itself and clears the merge state on the way
	// through — measured, including MERGE_MSG and MERGE_MODE. `--no-verify` keeps libgit2's "hooks
	// never fire", as everywhere else in this package.
	result, err := runner.RunWithEnv(ctx, identityEnv(authorName, authorEmail),
		"commit", "--no-verify", "-m", message)
	if err != nil {
		return "", err
	}
	if result.Failed() {
		return "", fmt.Errorf("git commit failed: %s", result.Detail())
	}

	head, err := runner.Run(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if head.Failed() {
		return "", fmt.Errorf("git rev-parse failed: %s", head.Detail())
	}
	return strings.TrimSpace(head.Stdout), nil
}

// AbortMerge throws the in-progress merge away, restoring the working tree to HEAD (GIT-018).
//
// It does **not** check that a merge is in progress, which is 2.x's behaviour and is load-bearing
// in a way worth stating: outside a merge this is a plain `reset --hard`, so it discards every
// uncommitted change. What keeps that safe is the renderer, which only offers the button while
// `is_merging` is true — and `is_merging` is MERGE_HEAD, not "are there conflicts", so a stash that
// applied with conflicts never reaches it.
//
// One command where libgit2 needed two: `git reset --hard HEAD` restores the tree and clears
// MERGE_HEAD, MERGE_MSG and MERGE_MODE, which is measured rather than assumed — libgit2 left them
// behind, and a repository still holding MERGE_HEAD reports itself as merging forever.
func AbortMerge(ctx context.Context, repo string) error {
	runner := NewRunner(repo)

	if _, err := headCommit(ctx, runner); err != nil {
		return err
	}

	result, err := runner.RunWrite(ctx, "reset", "--hard", "HEAD")
	if err != nil {
		return err
	}
	if result.Failed() {
		return fmt.Errorf("git reset failed: %s", result.Detail())
	}
	return nil
}
