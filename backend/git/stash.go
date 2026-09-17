package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Stash is one saved entry. Index 0 is the newest, which is git's own ordering and what the UI
// shows at the top.
type Stash struct {
	Index   int64  `json:"index"`
	Message string `json:"message"`
	OID     string `json:"oid"`
}

// ApplyOutcome is the result of applying or popping a stash.
//
// These are **results, not errors**, which is the whole point of the type. A stash that conflicts
// has done something useful — the changes are in the working tree with conflict markers — and
// reporting it as a failure would have the renderer show a red banner over a state the user needs
// to act on. Only `unknown` means "something happened that this port does not understand".
type ApplyOutcome string

const (
	OutcomeApplied            ApplyOutcome = "applied"
	OutcomeConflicts          ApplyOutcome = "conflicts"
	OutcomeNotFound           ApplyOutcome = "not_found"
	OutcomeUncommittedChanges ApplyOutcome = "uncommitted_changes"
	OutcomeUnknown            ApplyOutcome = "unknown"
)

// ListStashes enumerates the stash (GIT-015).
func ListStashes(ctx context.Context, repo string) ([]Stash, error) {
	stashes := make([]Stash, 0, 8)

	result, err := NewRunner(repo).Run(ctx, "stash", "list", "-z", "--format=%gd%x1f%gs%x1f%H")
	if err != nil {
		return nil, err
	}
	if result.Failed() {
		// A repository with no stash ref at all answers nothing useful; an empty list is the
		// honest reading, not a failure.
		return stashes, nil
	}

	for _, record := range splitNUL(result.Stdout) {
		fields := strings.Split(record, "\x1f")
		if len(fields) < 3 {
			continue
		}
		stashes = append(stashes, Stash{
			Index:   indexFromSelector(fields[0]),
			Message: fields[1],
			OID:     fields[2],
		})
	}
	return stashes, nil
}

// indexFromSelector reads the number out of `stash@{3}`.
func indexFromSelector(selector string) int64 {
	open := strings.IndexByte(selector, '{')
	closeAt := strings.IndexByte(selector, '}')
	if open < 0 || closeAt <= open {
		return 0
	}
	value, err := strconv.ParseInt(selector[open+1:closeAt], 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func selector(index int64) string { return fmt.Sprintf("stash@{%d}", index) }

// StashSave stashes the working tree and the index together.
//
// An empty message becomes "WIP", matching what git itself would generate — a stash with a blank
// label is one the user cannot tell apart from the others in the list.
func StashSave(ctx context.Context, repo string, message *string, includeUntracked bool) error {
	label := "WIP"
	if message != nil && strings.TrimSpace(*message) != "" {
		label = *message
	}

	args := []string{"stash", "push"}
	if includeUntracked {
		args = append(args, "-u")
	}
	args = append(args, "-m", label)

	result, err := NewRunner(repo).RunWrite(ctx, args...)
	if err != nil {
		return err
	}
	if result.Failed() {
		return fmt.Errorf("git stash push failed: %s", result.Detail())
	}
	return nil
}

// StashApply restores a stash, keeping the entry.
func StashApply(ctx context.Context, repo string, index int64) (ApplyOutcome, error) {
	return applyOrPop(ctx, repo, "apply", index)
}

// StashPop restores a stash and drops the entry — except on conflict, where git keeps it.
//
// That exception is measured, not assumed, and it is the right behaviour: a conflicted pop that
// also deleted the stash would leave the user with markers in their files and nothing to go back to.
func StashPop(ctx context.Context, repo string, index int64) (ApplyOutcome, error) {
	return applyOrPop(ctx, repo, "pop", index)
}

// applyOrPop classifies the outcome from git's exit code and text (GIT-019).
//
// The classification is the measured one, and it is matched in English — which is why every parsed
// command runs under LC_ALL=C. Under a translated locale each of these tests would silently stop
// matching and every outcome would become `unknown`.
func applyOrPop(ctx context.Context, repo, verb string, index int64) (ApplyOutcome, error) {
	result, err := NewRunner(repo).RunWrite(ctx, "stash", verb, selector(index))
	if err != nil {
		return OutcomeUnknown, err
	}

	detail := result.Stdout + "\n" + result.Stderr

	switch {
	case !result.Failed():
		return OutcomeApplied, nil

	// The index does not exist — the user is working from a list another window already changed.
	//
	// Classified on the text alone, **not** on the exit code. MIGRATION-GO.md §8.6 groups both
	// messages under exit 128; measured, they differ: an index beyond the end of a non-empty stash
	// is `fatal: log for 'stash' only has N entries` with 128, while any index in a repository
	// with no stash at all is `error: stash@{N} is not a valid reference` with **exit 1**.
	// Requiring 128 sent the second case to `unknown`, which is the "nothing is stashed" state a
	// user hits most often.
	case strings.Contains(detail, "only has") || strings.Contains(detail, "is not a valid reference"):
		return OutcomeNotFound, nil

	// The order of these two matters: a conflicted apply also mentions the files, so "would be
	// overwritten" has to be tested before CONFLICT or an untouched working tree would be
	// reported as conflicted.
	case strings.Contains(detail, "would be overwritten by merge"):
		return OutcomeUncommittedChanges, nil

	case strings.Contains(detail, "CONFLICT"):
		// Note: the index is marked, but there is **no MERGE_HEAD** — a conflicted stash is not a
		// merge, and IsMerging correctly reports false here.
		return OutcomeConflicts, nil

	default:
		return OutcomeUnknown, nil
	}
}

// StashDrop removes one entry.
func StashDrop(ctx context.Context, repo string, index int64) error {
	result, err := NewRunner(repo).RunWrite(ctx, "stash", "drop", selector(index))
	if err != nil {
		return err
	}
	if result.Failed() {
		return fmt.Errorf("git stash drop failed: %s", result.Detail())
	}
	return nil
}

// RenameStash changes an entry's message (GIT-014).
//
// git has no rename: the entry is dropped and re-appended with the new message, which is exactly
// what 2.x did. The consequence is deliberate and preserved — **the renamed entry becomes
// stash@{0}**, moving to the top of the list. Renaming a stash is how a user marks the one they
// care about, so surfacing it is the useful behaviour rather than an accident to be corrected.
func RenameStash(ctx context.Context, repo string, index int64, newMessage string) error {
	runner := NewRunner(repo)

	resolved, err := runner.Run(ctx, "rev-parse", selector(index))
	if err != nil {
		return err
	}
	if resolved.Failed() {
		return fmt.Errorf("git rev-parse failed: %s", resolved.Detail())
	}
	oid := strings.TrimSpace(resolved.Stdout)

	// Drop before store: the other order would briefly have the same commit in the stash twice,
	// and a crash between them would leave a duplicate the user has to work out.
	dropped, err := runner.RunWrite(ctx, "stash", "drop", selector(index))
	if err != nil {
		return err
	}
	if dropped.Failed() {
		return fmt.Errorf("git stash drop failed: %s", dropped.Detail())
	}

	stored, err := runner.RunWrite(ctx, "stash", "store", "-m", newMessage, oid)
	if err != nil {
		return err
	}
	if stored.Failed() {
		return fmt.Errorf("git stash store failed: %s", stored.Detail())
	}
	return nil
}
