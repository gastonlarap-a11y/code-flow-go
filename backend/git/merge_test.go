package git_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge/jsonwire"
	"github.com/gastonlarap-a11y/code-flow/backend/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// diverged builds the shape almost every merge test needs: `main` and `other` each with one commit
// on top of a shared base. When bothTouch is true they change the same file, which conflicts.
func diverged(t *testing.T, bothTouch bool) *testRepo {
	t.Helper()
	repo := newTestRepo(t)
	repo.write("shared.txt", "base\n")
	repo.commit("base")

	repo.git("checkout", "--quiet", "-b", "other")
	repo.write("shared.txt", "their version\n")
	repo.commit("theirs")

	repo.git("checkout", "--quiet", "main")
	if bothTouch {
		repo.write("shared.txt", "our version\n")
	} else {
		repo.write("ours-only.txt", "ours\n")
	}
	repo.commit("ours")

	return repo
}

func mergeOther(t *testing.T, repo *testRepo) git.MergeOutcome {
	t.Helper()
	outcome, err := git.MergeBranch(ctx(t), repo.Path, "other", nil, nil)
	require.NoError(t, err)
	return outcome
}

func TestMergeUpToDate(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	repo.git("branch", "other")

	outcome := mergeOther(t, repo)

	assert.Equal(t, git.MergeUpToDate, outcome.Status)
	assert.Empty(t, outcome.Conflicts)
	require.NoError(t, jsonwire.AssertNoNilSlices(outcome),
		"the renderer maps over conflicts without guarding")
}

// A fast-forward creates no merge commit, which is plain `git merge`'s behaviour rather than
// `--no-ff`. The check is on the parent count: a merge commit would have two.
func TestMergeFastForwardCreatesNoMergeCommit(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	repo.git("checkout", "--quiet", "-b", "other")
	repo.write("new.txt", "ahead\n")
	repo.commit("ahead")
	theirs := strings.TrimSpace(repo.git("rev-parse", "HEAD"))
	repo.git("checkout", "--quiet", "main")

	outcome := mergeOther(t, repo)

	assert.Equal(t, git.MergeFastForward, outcome.Status)
	assert.Equal(t, theirs, strings.TrimSpace(repo.git("rev-parse", "HEAD")),
		"the branch ref moved straight to their commit")
	assert.Len(t, strings.Fields(repo.git("log", "-1", "--format=%P")), 1,
		"a fast-forward leaves a single-parent commit, not a merge")
	assert.FileExists(t, filepath.Join(repo.Path, "new.txt"))
}

func TestMergeCreatesATwoParentCommit(t *testing.T) {
	repo := diverged(t, false)
	head := strings.TrimSpace(repo.git("rev-parse", "HEAD"))
	theirs := strings.TrimSpace(repo.git("rev-parse", "other"))

	outcome := mergeOther(t, repo)

	require.Equal(t, git.MergeMerged, outcome.Status)
	parents := strings.Fields(repo.git("log", "-1", "--format=%P"))
	assert.Equal(t, []string{head, theirs}, parents, "ours first, theirs second")
	assert.Contains(t, repo.git("log", "-1", "--format=%s"), "Merge branch 'other'")
}

// Merging by OID would make git write "Merge commit '<sha>'", and merging a remote branch by name
// "Merge remote-tracking branch 'origin/x'". 2.x wrote one form for every case.
func TestTheMergeMessageIsAlwaysTheSameForm(t *testing.T) {
	repo := diverged(t, false)
	repo.git("branch", "-m", "other", "feature/thing")

	_, err := git.MergeBranch(ctx(t), repo.Path, "feature/thing", nil, nil)
	require.NoError(t, err)

	assert.Equal(t, "Merge branch 'feature/thing'",
		strings.TrimSpace(repo.git("log", "-1", "--format=%s")))
}

// Conflicts are a success return, not an error: the repository is left mid-merge and the panel
// works on it from there.
func TestMergeConflictsAreAnOutcomeNotAnError(t *testing.T) {
	repo := diverged(t, true)

	outcome, err := git.MergeBranch(ctx(t), repo.Path, "other", nil, nil)

	require.NoError(t, err, "a conflicted merge is a state, not a failure")
	assert.Equal(t, git.MergeConflicts, outcome.Status)
	assert.Equal(t, []string{"shared.txt"}, outcome.Conflicts)

	merging, err := git.IsMerging(ctx(t), repo.Path)
	require.NoError(t, err)
	assert.True(t, merging, "nothing is cleaned up — the user has work to do")

	content, err := os.ReadFile(filepath.Join(repo.Path, "shared.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "<<<<<<<", "the markers are in the working tree")
}

// The conflict list is one row per file, not one per index stage: a content conflict has three.
func TestConflictsAreListedOncePerFile(t *testing.T) {
	repo := diverged(t, true)
	mergeOther(t, repo)

	assert.Len(t, strings.Fields(repo.git("ls-files", "-u")), 3*4, "three stages, four fields each")

	conflicts, err := git.ListConflicts(ctx(t), repo.Path)

	require.NoError(t, err)
	assert.Equal(t, []git.ConflictFile{{Path: "shared.txt"}}, conflicts)
}

func TestAnEmptyConflictListIsAnArray(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	conflicts, err := git.ListConflicts(ctx(t), repo.Path)

	require.NoError(t, err)
	assert.Empty(t, conflicts)
	assert.NotNil(t, conflicts)
}

// A local branch always wins over an identically named remote one.
func TestALocalBranchWinsOverASameNamedRemote(t *testing.T) {
	repo := diverged(t, false)

	// A remote-tracking ref called `other` with different content from the local `other`.
	repo.git("update-ref", "refs/remotes/other", "HEAD")

	outcome := mergeOther(t, repo)

	require.Equal(t, git.MergeMerged, outcome.Status)
	assert.Equal(t, strings.TrimSpace(repo.git("rev-parse", "refs/heads/other")),
		strings.TrimSpace(repo.git("rev-parse", "HEAD^2")),
		"the second parent is the local branch, not the remote ref")
}

func TestMergingARemoteBranchWhenNoLocalOneExists(t *testing.T) {
	repo := diverged(t, false)
	theirs := strings.TrimSpace(repo.git("rev-parse", "other"))
	repo.git("update-ref", "refs/remotes/origin/other", theirs)
	repo.git("branch", "-D", "other")

	outcome, err := git.MergeBranch(ctx(t), repo.Path, "origin/other", nil, nil)

	require.NoError(t, err)
	assert.Equal(t, git.MergeMerged, outcome.Status)
}

func TestMergingAnUnknownBranch(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	_, err := git.MergeBranch(ctx(t), repo.Path, "nope", nil, nil)

	// 2.x's wording, shown to the user verbatim.
	assert.EqualError(t, err, "cannot locate local branch 'nope'")
}

// Blocked before the merge could run is a failure, not a conflict: libgit2 raised for it too, and
// reporting it as `conflicts` would open a panel with nothing in it.
func TestLocalChangesInTheWayAreAnError(t *testing.T) {
	repo := diverged(t, true)
	repo.write("shared.txt", "work I have not committed\n")

	_, err := git.MergeBranch(ctx(t), repo.Path, "other", nil, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "git merge failed")
}

func TestTheResolvedIdentityIsAuthorAndCommitter(t *testing.T) {
	repo := diverged(t, false)
	name, email := "Gastón Lara P.", "gaston@example.com"

	_, err := git.MergeBranch(ctx(t), repo.Path, "other", &name, &email)
	require.NoError(t, err)

	assert.Equal(t, "Gastón Lara P.|gaston@example.com|Gastón Lara P.|gaston@example.com",
		strings.TrimSpace(repo.git("log", "-1", "--format=%an|%ae|%cn|%ce")))
}

func TestResolveConflictSide(t *testing.T) {
	for _, side := range []struct{ name, want string }{
		{"ours", "our version\n"},
		{"theirs", "their version\n"},
	} {
		t.Run(side.name, func(t *testing.T) {
			repo := diverged(t, true)
			mergeOther(t, repo)

			require.NoError(t, git.ResolveConflictSide(ctx(t), repo.Path, "shared.txt", side.name))

			content, err := os.ReadFile(filepath.Join(repo.Path, "shared.txt"))
			require.NoError(t, err)
			assert.Equal(t, side.want, string(content))

			// Staging a conflicted path clears all three of its stages.
			conflicts, err := git.ListConflicts(ctx(t), repo.Path)
			require.NoError(t, err)
			assert.Empty(t, conflicts)
		})
	}
}

func TestResolveConflictSideRejectsAnythingElse(t *testing.T) {
	repo := diverged(t, true)
	mergeOther(t, repo)

	err := git.ResolveConflictSide(ctx(t), repo.Path, "shared.txt", "mine")

	assert.EqualError(t, err, "side must be 'ours' or 'theirs'")
}

// A side that deleted the file has no content to take. Distinguished from "no conflict for this
// path" so the panel can say which button cannot apply.
func TestTakingASideThatDeletedTheFile(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("doomed.txt", "base\n")
	repo.commit("base")

	repo.git("checkout", "--quiet", "-b", "other")
	repo.remove("doomed.txt")
	repo.commit("deleted on their side")

	repo.git("checkout", "--quiet", "main")
	repo.write("doomed.txt", "edited on ours\n")
	repo.commit("ours")

	outcome := mergeOther(t, repo)
	require.Equal(t, git.MergeConflicts, outcome.Status)

	err := git.ResolveConflictSide(ctx(t), repo.Path, "doomed.txt", "theirs")

	assert.EqualError(t, err, "that side has no content for this file (it was added/deleted)")
}

func TestResolvingAPathThatIsNotConflicted(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	err := git.ResolveConflictSide(ctx(t), repo.Path, "README.md", "ours")

	assert.EqualError(t, err, "no conflict for this path")
}

// The other half of the panel: the user edited the file by hand and this is how that becomes the
// resolution.
func TestMarkConflictResolvedStagesWhateverIsOnDisk(t *testing.T) {
	repo := diverged(t, true)
	mergeOther(t, repo)
	repo.write("shared.txt", "a hand-written merge of both\n")

	require.NoError(t, git.MarkConflictResolved(ctx(t), repo.Path, "shared.txt"))

	conflicts, err := git.ListConflicts(ctx(t), repo.Path)
	require.NoError(t, err)
	assert.Empty(t, conflicts)
	assert.Equal(t, "a hand-written merge of both\n",
		repo.git("show", ":0:shared.txt"), "the staged content is what was on disk")
}

func TestCompleteMergeRefusesWhileConflictsRemain(t *testing.T) {
	repo := diverged(t, true)
	mergeOther(t, repo)

	_, err := git.CompleteMerge(ctx(t), repo.Path, "resolved", nil, nil)

	// Verbatim: 2.x's message, shown to the user as-is.
	assert.EqualError(t, err, "There are still unresolved conflicts")
}

func TestCompleteMerge(t *testing.T) {
	repo := diverged(t, true)
	head := strings.TrimSpace(repo.git("rev-parse", "HEAD"))
	theirs := strings.TrimSpace(repo.git("rev-parse", "other"))
	mergeOther(t, repo)
	require.NoError(t, git.ResolveConflictSide(ctx(t), repo.Path, "shared.txt", "theirs"))

	oid, err := git.CompleteMerge(ctx(t), repo.Path, "merged by hand", nil, nil)

	require.NoError(t, err)
	assert.Len(t, oid, 40)
	assert.Equal(t, oid, strings.TrimSpace(repo.git("rev-parse", "HEAD")))
	assert.Equal(t, []string{head, theirs}, strings.Fields(repo.git("log", "-1", "--format=%P")))
	assert.Equal(t, "merged by hand", strings.TrimSpace(repo.git("log", "-1", "--format=%s")))

	merging, err := git.IsMerging(ctx(t), repo.Path)
	require.NoError(t, err)
	assert.False(t, merging, "the merge state is cleared, or the conflict banner never goes away")
}

// MERGE_HEAD is read from the repository rather than remembered, so completing works after the app
// was restarted mid-conflict — which is the same thing as completing from a process that never saw
// the merge start.
func TestCompleteMergeOutsideAMerge(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	_, err := git.CompleteMerge(ctx(t), repo.Path, "nothing to complete", nil, nil)

	assert.EqualError(t, err, "MERGE_HEAD has no target")
}

func TestAbortMerge(t *testing.T) {
	repo := diverged(t, true)
	head := strings.TrimSpace(repo.git("rev-parse", "HEAD"))
	mergeOther(t, repo)

	require.NoError(t, git.AbortMerge(ctx(t), repo.Path))

	assert.Equal(t, head, strings.TrimSpace(repo.git("rev-parse", "HEAD")))
	assert.Empty(t, strings.TrimSpace(repo.git("status", "--porcelain")),
		"the working tree is back to HEAD's")

	content, err := os.ReadFile(filepath.Join(repo.Path, "shared.txt"))
	require.NoError(t, err)
	assert.Equal(t, "our version\n", string(content))

	merging, err := git.IsMerging(ctx(t), repo.Path)
	require.NoError(t, err)
	assert.False(t, merging)
}

// Aborting never asks whether a merge is in progress — 2.x's behaviour, and what the renderer's
// `is_merging` gate exists to keep safe. The test states it so nobody "fixes" it later.
func TestAbortingOutsideAMergeDiscardsEverything(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	repo.write("README.md", "uncommitted work\n")

	require.NoError(t, git.AbortMerge(ctx(t), repo.Path))

	content, err := os.ReadFile(filepath.Join(repo.Path, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Test\n", string(content),
		"a plain reset --hard: the button is gated on is_merging for exactly this reason")
}

func TestConflictVersionsReadsAllThreeStages(t *testing.T) {
	repo := diverged(t, true)
	mergeOther(t, repo)

	versions, err := git.ConflictVersionsFor(ctx(t), repo.Path, "shared.txt")

	require.NoError(t, err)
	assert.Equal(t, "base\n", versions.Base)
	assert.Equal(t, "our version\n", versions.Ours)
	assert.Equal(t, "their version\n", versions.Theirs)
}

// An absent side reads as the empty string. "The file was deleted there" is what the resolver
// needs to know, not an error to handle.
func TestAnAbsentSideReadsAsEmpty(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("doomed.txt", "base\n")
	repo.commit("base")
	repo.git("checkout", "--quiet", "-b", "other")
	repo.remove("doomed.txt")
	repo.commit("deleted on their side")
	repo.git("checkout", "--quiet", "main")
	repo.write("doomed.txt", "edited on ours\n")
	repo.commit("ours")
	mergeOther(t, repo)

	versions, err := git.ConflictVersionsFor(ctx(t), repo.Path, "doomed.txt")

	require.NoError(t, err)
	assert.Equal(t, "base\n", versions.Base)
	assert.Equal(t, "edited on ours\n", versions.Ours)
	assert.Empty(t, versions.Theirs)
}

// The raw runner exists for exactly this: a line-decoding read would strip the \r and append a
// newline, rewriting every line ending in a file the user never touched.
func TestConflictVersionsPreserveCRLFAndAMissingFinalNewline(t *testing.T) {
	repo := newTestRepo(t)
	repo.write(".gitattributes", "* -text\n") // no line-ending conversion, on any platform
	repo.writeBytes("crlf.txt", []byte("one\r\ntwo\r\n"))
	repo.commit("base")

	repo.git("checkout", "--quiet", "-b", "other")
	repo.writeBytes("crlf.txt", []byte("one\r\ntheirs"))
	repo.commit("theirs")

	repo.git("checkout", "--quiet", "main")
	repo.writeBytes("crlf.txt", []byte("one\r\nours"))
	repo.commit("ours")
	mergeOther(t, repo)

	versions, err := git.ConflictVersionsFor(ctx(t), repo.Path, "crlf.txt")

	require.NoError(t, err)
	assert.Equal(t, "one\r\nours", versions.Ours, "CRLF intact, no newline invented")
	assert.Equal(t, "one\r\ntheirs", versions.Theirs)
}
