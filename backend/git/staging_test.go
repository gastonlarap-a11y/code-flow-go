package git_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func statusOf(t *testing.T, repo *testRepo) git.RepoStatus {
	t.Helper()
	status, err := git.Status(ctx(t), repo.Path)
	require.NoError(t, err)
	return status
}

func TestStageAndUnstageOneFile(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	repo.write("a.txt", "new\n")

	require.NoError(t, git.StageFile(ctx(t), repo.Path, "a.txt"))
	assert.Equal(t, []string{"a.txt"}, paths(statusOf(t, repo).Staged))

	require.NoError(t, git.UnstageFile(ctx(t), repo.Path, "a.txt"))
	assert.Empty(t, statusOf(t, repo).Staged)
	assert.Equal(t, []string{"a.txt"}, paths(statusOf(t, repo).Untracked))
}

// `git add` on a file that is gone fails; ticking a deleted file in the Changes panel means
// "record the deletion", so it is staged as one.
func TestStagingAFileThatWasDeletedRecordsTheDeletion(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("gone.txt", "bye\n")
	repo.commit("initial")
	repo.remove("gone.txt")

	require.NoError(t, git.StageFile(ctx(t), repo.Path, "gone.txt"))

	staged := statusOf(t, repo).Staged
	require.Len(t, staged, 1)
	assert.Equal(t, "gone.txt", staged[0].Path)
	assert.Equal(t, "deleted", staged[0].Status)
}

func TestStageAllAndUnstageAll(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	repo.write("a.txt", "one\n")
	repo.write("b.txt", "two\n")

	require.NoError(t, git.StageAll(ctx(t), repo.Path))
	assert.Len(t, statusOf(t, repo).Staged, 2)

	require.NoError(t, git.UnstageAll(ctx(t), repo.Path))
	assert.Empty(t, statusOf(t, repo).Staged)
	assert.Len(t, statusOf(t, repo).Untracked, 2)
}

// Discard restores from the **index**, not from HEAD. A file staged and then modified again goes
// back to the staged version — restoring from HEAD would destroy work the user deliberately kept.
func TestDiscardRestoresFromTheIndexNotFromHead(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("a.txt", "committed\n")
	repo.commit("initial")

	repo.write("a.txt", "staged\n")
	repo.git("add", "a.txt")
	repo.write("a.txt", "and then edited again\n")

	require.NoError(t, git.DiscardFileChanges(ctx(t), repo.Path, "a.txt"))

	content, err := os.ReadFile(filepath.Join(repo.Path, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "staged\n", string(content), "back to the index, not to HEAD")
}

func TestDiscardAllRemovesUntrackedFilesAndTheirEmptyParents(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("tracked.txt", "original\n")
	repo.commit("initial")

	repo.write("tracked.txt", "modified\n")
	repo.write("pkg/sub/new.go", "package sub\n")

	require.NoError(t, git.DiscardAllChanges(ctx(t), repo.Path))

	content, err := os.ReadFile(filepath.Join(repo.Path, "tracked.txt"))
	require.NoError(t, err)
	assert.Equal(t, "original\n", string(content))

	assert.NoFileExists(t, filepath.Join(repo.Path, "pkg", "sub", "new.go"))
	// Without walking up, `pkg/sub/` is left behind: invisible to git, clutter in the editor.
	assert.NoDirExists(t, filepath.Join(repo.Path, "pkg", "sub"))
	assert.NoDirExists(t, filepath.Join(repo.Path, "pkg"))
	assert.DirExists(t, repo.Path, "the repository itself must survive")
}

// The user staged those on purpose; "discard my changes" does not mean "throw away what I staged".
func TestDiscardAllLeavesStagedChangesAlone(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("a.txt", "committed\n")
	repo.commit("initial")

	repo.write("a.txt", "staged\n")
	repo.git("add", "a.txt")

	require.NoError(t, git.DiscardAllChanges(ctx(t), repo.Path))

	assert.Equal(t, []string{"a.txt"}, paths(statusOf(t, repo).Staged))
	content, err := os.ReadFile(filepath.Join(repo.Path, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "staged\n", string(content))
}

// A hard reset would resolve a merge by discarding it, which is not what "discard my changes"
// means while one is in progress.
func TestDiscardAllSkipsConflictedPaths(t *testing.T) {
	repo := conflictedRepo(t)

	require.NoError(t, git.DiscardAllChanges(ctx(t), repo.Path))

	status := statusOf(t, repo)
	assert.Equal(t, []string{"shared.txt"}, paths(status.Conflicted),
		"the conflict must still be there to resolve")
	merging, err := git.IsMerging(ctx(t), repo.Path)
	require.NoError(t, err)
	assert.True(t, merging)
}

// conflictedRepo leaves a repository mid-merge with one conflicted file.
func conflictedRepo(t *testing.T) *testRepo {
	t.Helper()
	repo := newTestRepo(t)
	repo.write("shared.txt", "base\n")
	repo.commit("base")

	repo.git("checkout", "--quiet", "-b", "other")
	repo.write("shared.txt", "theirs\n")
	repo.commit("theirs")

	repo.git("checkout", "--quiet", "main")
	repo.write("shared.txt", "ours\n")
	repo.commit("ours")

	_ = repo.tryGit("merge", "--no-edit", "other")
	return repo
}

func TestCommitReturnsTheNewCommitId(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("a.txt", "one\n")
	repo.git("add", "-A")

	oid, err := git.CreateCommit(ctx(t), repo.Path, "the message", nil, nil)
	require.NoError(t, err)

	assert.Len(t, oid, 40)
	assert.Equal(t, oid, strings.TrimSpace(repo.git("rev-parse", "HEAD")))
	assert.Contains(t, repo.git("log", "-1", "--format=%s"), "the message")
}

// libgit2's CreateCommit took one signature for both, so a CodeFlow commit has matching author and
// committer. `--author` alone would leave the committer as whatever the machine's config says.
func TestAnExplicitAuthorIsAlsoTheCommitter(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("a.txt", "one\n")
	repo.git("add", "-A")
	name, email := "Gastón Lara P.", "gaston@example.com"

	_, err := git.CreateCommit(ctx(t), repo.Path, "authored", &name, &email)
	require.NoError(t, err)

	out := repo.git("log", "-1", "--format=%an|%ae|%cn|%ce")
	assert.Equal(t, "Gastón Lara P.|gaston@example.com|Gastón Lara P.|gaston@example.com",
		strings.TrimSpace(out))
}

// libgit2 never ran hooks, so a repository with a pre-commit hook committed fine from 2.x and
// must keep doing so.
func TestHooksNeverFire(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	hook := filepath.Join(repo.Path, ".git", "hooks", "pre-commit")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o700))

	repo.write("a.txt", "one\n")
	repo.git("add", "-A")

	_, err := git.CreateCommit(ctx(t), repo.Path, "past the hook", nil, nil)
	assert.NoError(t, err, "--no-verify keeps libgit2's 'hooks never fire' behaviour")
}

func TestResetToCommit(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("a.txt", "one\n")
	repo.commit("first")
	first := strings.TrimSpace(repo.git("rev-parse", "HEAD"))
	repo.write("a.txt", "two\n")
	repo.commit("second")

	require.NoError(t, git.ResetToCommit(ctx(t), repo.Path, first, "mixed"))

	assert.Equal(t, first, strings.TrimSpace(repo.git("rev-parse", "HEAD")))
	// Mixed keeps the working tree, so the second commit's content is now an unstaged change.
	assert.Equal(t, []string{"a.txt"}, paths(statusOf(t, repo).Unstaged))
}

// An unrecognised mode becomes mixed, which is git's own default. Refusing would turn a renderer
// typo into a dead button; defaulting to hard would destroy work.
func TestAnUnknownResetModeBecomesMixed(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("a.txt", "one\n")
	repo.commit("first")
	first := strings.TrimSpace(repo.git("rev-parse", "HEAD"))
	repo.write("a.txt", "two\n")
	repo.commit("second")

	require.NoError(t, git.ResetToCommit(ctx(t), repo.Path, first, "nonsense"))

	assert.Equal(t, first, strings.TrimSpace(repo.git("rev-parse", "HEAD")))
	assert.NotEmpty(t, statusOf(t, repo).Unstaged, "the working tree survived, so it was mixed")
}

// MERGE_HEAD, not "are there conflicts": a merge with everything resolved is still a merge.
func TestIsMerging(t *testing.T) {
	clean := newTestRepo(t)
	clean.seed()
	merging, err := git.IsMerging(ctx(t), clean.Path)
	require.NoError(t, err)
	assert.False(t, merging)

	repo := conflictedRepo(t)
	merging, err = git.IsMerging(ctx(t), repo.Path)
	require.NoError(t, err)
	assert.True(t, merging)

	repo.git("add", "shared.txt")
	merging, err = git.IsMerging(ctx(t), repo.Path)
	require.NoError(t, err)
	assert.True(t, merging, "resolved conflicts do not end the merge")
}

// `git remote -v` prints each remote twice, once for fetch and once for push.
func TestListRemotesReportsEachRemoteOnce(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	repo.git("remote", "add", "origin", "https://example.com/a.git")
	repo.git("remote", "add", "upstream", "https://example.com/b.git")

	remotes, err := git.ListRemotes(ctx(t), repo.Path)
	require.NoError(t, err)

	require.Len(t, remotes, 2)
	assert.Equal(t, "origin", remotes[0].Name)
	assert.Equal(t, "https://example.com/a.git", remotes[0].URL)
}

// git keeps the fetch and push URLs separately; changing only one pulls from the old host and
// pushes to the new, which nobody notices until a push goes somewhere wrong.
func TestSetRemoteURLChangesBothFetchAndPush(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	repo.git("remote", "add", "origin", "https://example.com/old.git")

	require.NoError(t, git.SetRemoteURL(ctx(t), repo.Path, "origin", "https://example.com/new.git"))

	out := repo.git("remote", "-v")
	assert.NotContains(t, out, "old.git")
	assert.Equal(t, 2, strings.Count(out, "new.git"), "fetch and push")
}

func TestEmptyRemoteListIsAnArray(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	remotes, err := git.ListRemotes(ctx(t), repo.Path)
	require.NoError(t, err)

	assert.Empty(t, remotes)
	assert.NotNil(t, remotes)
}
