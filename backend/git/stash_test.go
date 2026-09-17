package git_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge/jsonwire"
	"github.com/gastonlarap-a11y/code-flow/backend/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stashes(t *testing.T, repo *testRepo) []git.Stash {
	t.Helper()
	list, err := git.ListStashes(ctx(t), repo.Path)
	require.NoError(t, err)
	return list
}

func TestStashSaveAndList(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("a.txt", "committed\n")
	repo.commit("initial")
	repo.write("a.txt", "work in progress\n")

	require.NoError(t, git.StashSave(ctx(t), repo.Path, ptr("my changes"), false))

	list := stashes(t, repo)
	require.Len(t, list, 1)
	assert.EqualValues(t, 0, list[0].Index)
	assert.Contains(t, list[0].Message, "my changes")
	assert.Len(t, list[0].OID, 40)
	require.NoError(t, jsonwire.AssertNoNilSlices(list))

	// The working tree went back to the committed state.
	content, err := os.ReadFile(filepath.Join(repo.Path, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "committed\n", string(content))
}

func ptr(s string) *string { return &s }

// A stash with a blank label is one the user cannot tell apart from the others in the list.
func TestAnEmptyStashMessageBecomesWIP(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	repo.write("README.md", "changed\n")

	require.NoError(t, git.StashSave(ctx(t), repo.Path, nil, false))

	assert.Contains(t, stashes(t, repo)[0].Message, "WIP")
}

func TestStashIncludingUntracked(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	repo.write("brand-new.txt", "loose\n")

	require.NoError(t, git.StashSave(ctx(t), repo.Path, ptr("with untracked"), true))

	assert.NoFileExists(t, filepath.Join(repo.Path, "brand-new.txt"))
	assert.Len(t, stashes(t, repo), 1)
}

// Index 0 is the newest, which is git's own ordering and what the UI shows at the top.
func TestIndexZeroIsTheNewest(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	repo.write("README.md", "first change\n")
	require.NoError(t, git.StashSave(ctx(t), repo.Path, ptr("older"), false))
	repo.write("README.md", "second change\n")
	require.NoError(t, git.StashSave(ctx(t), repo.Path, ptr("newer"), false))

	list := stashes(t, repo)
	require.Len(t, list, 2)
	assert.Contains(t, list[0].Message, "newer")
	assert.EqualValues(t, 0, list[0].Index)
	assert.EqualValues(t, 1, list[1].Index)
}

func TestApplyKeepsTheEntryAndPopRemovesIt(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	repo.write("README.md", "stashed work\n")
	require.NoError(t, git.StashSave(ctx(t), repo.Path, ptr("work"), false))

	outcome, err := git.StashApply(ctx(t), repo.Path, 0)
	require.NoError(t, err)
	assert.Equal(t, git.OutcomeApplied, outcome)
	assert.Len(t, stashes(t, repo), 1, "apply keeps the entry")

	content, err := os.ReadFile(filepath.Join(repo.Path, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "stashed work\n", string(content))

	// Reset so the pop has a clean tree to land in.
	require.NoError(t, git.DiscardAllChanges(ctx(t), repo.Path))

	outcome, err = git.StashPop(ctx(t), repo.Path, 0)
	require.NoError(t, err)
	assert.Equal(t, git.OutcomeApplied, outcome)
	assert.Empty(t, stashes(t, repo), "pop removes it")
}

// These are results, not errors: the renderer acts on each one differently, and a red banner over
// a conflicted apply would hide a state the user has to resolve.
func TestOutcomesAreClassifiedNotRaised(t *testing.T) {
	// Two different messages and two different exit codes, which is why the classification is on
	// the text rather than the code: an empty stash answers "is not a valid reference" with exit
	// 1, and an out-of-range index on a non-empty one answers "only has N entries" with 128.
	t.Run("not_found with no stashes at all", func(t *testing.T) {
		repo := newTestRepo(t)
		repo.seed()

		outcome, err := git.StashApply(ctx(t), repo.Path, 7)

		require.NoError(t, err, "a missing index is an outcome, not an error")
		assert.Equal(t, git.OutcomeNotFound, outcome)
	})

	t.Run("not_found past the end of a non-empty stash", func(t *testing.T) {
		repo := newTestRepo(t)
		repo.seed()
		repo.write("README.md", "change\n")
		require.NoError(t, git.StashSave(ctx(t), repo.Path, ptr("only one"), false))

		outcome, err := git.StashApply(ctx(t), repo.Path, 7)

		require.NoError(t, err)
		assert.Equal(t, git.OutcomeNotFound, outcome)
	})

	t.Run("conflicts", func(t *testing.T) {
		repo := newTestRepo(t)
		repo.write("shared.txt", "base\n")
		repo.commit("initial")

		repo.write("shared.txt", "stashed version\n")
		require.NoError(t, git.StashSave(ctx(t), repo.Path, ptr("mine"), false))

		// A conflicting commit on top of what the stash was taken from.
		repo.write("shared.txt", "committed version\n")
		repo.commit("conflicting change")

		outcome, err := git.StashApply(ctx(t), repo.Path, 0)

		require.NoError(t, err)
		assert.Equal(t, git.OutcomeConflicts, outcome)

		// A conflicted stash is not a merge: the index is marked, but there is no MERGE_HEAD.
		merging, err := git.IsMerging(ctx(t), repo.Path)
		require.NoError(t, err)
		assert.False(t, merging, "a stash conflict must not look like a merge in progress")
	})

	t.Run("uncommitted_changes", func(t *testing.T) {
		repo := newTestRepo(t)
		repo.write("shared.txt", "base\n")
		repo.commit("initial")

		repo.write("shared.txt", "stashed version\n")
		require.NoError(t, git.StashSave(ctx(t), repo.Path, ptr("mine"), false))

		// An uncommitted edit to the same file blocks the apply before any merging happens.
		repo.write("shared.txt", "local work I have not committed\n")

		outcome, err := git.StashApply(ctx(t), repo.Path, 0)

		require.NoError(t, err)
		assert.Equal(t, git.OutcomeUncommittedChanges, outcome,
			"blocked before merging is a different state from conflicted while merging")
	})
}

// git keeps the entry when a pop conflicts. A conflicted pop that also deleted the stash would
// leave the user with markers in their files and nothing to go back to.
func TestAConflictedPopKeepsTheEntry(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("shared.txt", "base\n")
	repo.commit("initial")
	repo.write("shared.txt", "stashed version\n")
	require.NoError(t, git.StashSave(ctx(t), repo.Path, ptr("mine"), false))
	repo.write("shared.txt", "committed version\n")
	repo.commit("conflicting change")

	outcome, err := git.StashPop(ctx(t), repo.Path, 0)

	require.NoError(t, err)
	assert.Equal(t, git.OutcomeConflicts, outcome)
	assert.Len(t, stashes(t, repo), 1, "the stash must survive so the user can retry")
}

func TestStashDrop(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	repo.write("README.md", "change\n")
	require.NoError(t, git.StashSave(ctx(t), repo.Path, ptr("throwaway"), false))

	require.NoError(t, git.StashDrop(ctx(t), repo.Path, 0))

	assert.Empty(t, stashes(t, repo))
}

// git has no rename: the entry is dropped and re-appended, so it moves to the top. That is
// preserved rather than corrected — renaming a stash is how a user marks the one they care about.
func TestRenamingAStashMovesItToTheTop(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	repo.write("README.md", "first\n")
	require.NoError(t, git.StashSave(ctx(t), repo.Path, ptr("the one I care about"), false))
	repo.write("README.md", "second\n")
	require.NoError(t, git.StashSave(ctx(t), repo.Path, ptr("newer"), false))

	// The older entry is at index 1.
	before := stashes(t, repo)
	require.Len(t, before, 2)
	oldOID := before[1].OID

	require.NoError(t, git.RenameStash(ctx(t), repo.Path, 1, "renamed and promoted"))

	after := stashes(t, repo)
	require.Len(t, after, 2, "a rename must not lose or duplicate an entry")
	assert.Contains(t, after[0].Message, "renamed and promoted")
	assert.Equal(t, oldOID, after[0].OID, "the same commit, re-labelled")
	assert.Contains(t, after[1].Message, "newer")
}

func TestAnEmptyStashListIsAnArray(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	list, err := git.ListStashes(ctx(t), repo.Path)

	require.NoError(t, err)
	assert.Empty(t, list)
	assert.NotNil(t, list)
}
