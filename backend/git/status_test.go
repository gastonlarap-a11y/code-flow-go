package git_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge/jsonwire"
	"github.com/gastonlarap-a11y/code-flow/backend/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func paths(entries []git.FileStatus) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Path)
	}
	return out
}

func TestIsRepo(t *testing.T) {
	repo := newTestRepo(t)

	assert.True(t, git.IsRepo(ctx(t), repo.Path))
	assert.False(t, git.IsRepo(ctx(t), t.TempDir()), "an ordinary directory is not a repository")
}

// A clean repository must produce four empty arrays, never nulls: the renderer maps over all four
// without guarding, so a nil slice is a blank Changes panel on exactly the state a new user sees
// first.
func TestACleanRepositoryReportsEmptyArrays(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	status, err := git.Status(ctx(t), repo.Path)
	require.NoError(t, err)

	assert.Empty(t, status.Staged)
	assert.Empty(t, status.Unstaged)
	assert.Empty(t, status.Untracked)
	assert.Empty(t, status.Conflicted)
	require.NoError(t, jsonwire.AssertNoNilSlices(status))

	encoded, err := json.Marshal(status)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"staged":[]`)
	assert.NotContains(t, string(encoded), `"staged":null`)
}

func TestBranchAndDetachedHead(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	status, err := git.Status(ctx(t), repo.Path)
	require.NoError(t, err)
	require.NotNil(t, status.CurrentBranch)
	assert.Equal(t, "main", *status.CurrentBranch)
	assert.False(t, status.IsDetached)

	repo.git("checkout", "--quiet", "--detach", "HEAD")

	status, err = git.Status(ctx(t), repo.Path)
	require.NoError(t, err)
	assert.True(t, status.IsDetached)
	// Not the literal "(detached)": the renderer shows a different header for a detached HEAD, and
	// reporting that string as a branch name would put it in the branch switcher.
	assert.Nil(t, status.CurrentBranch)
}

func TestBuckets(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	repo.write("staged.txt", "new\n")
	repo.git("add", "staged.txt")
	repo.write("README.md", "# Changed\n")
	repo.write("untracked.txt", "loose\n")

	status, err := git.Status(ctx(t), repo.Path)
	require.NoError(t, err)

	assert.Equal(t, []string{"staged.txt"}, paths(status.Staged))
	assert.Equal(t, []string{"README.md"}, paths(status.Unstaged))
	assert.Equal(t, []string{"untracked.txt"}, paths(status.Untracked))
	assert.Equal(t, "added", status.Staged[0].Status)
	assert.Equal(t, "modified", status.Unstaged[0].Status)
	assert.Equal(t, "untracked", status.Untracked[0].Status)
}

// The bucket rule that is not obvious: a path staged and then modified again is reported **once**,
// as staged. git prints it as a single `1 MM` record. Listing it in both buckets would let the
// user tick the same file twice and stage half of it.
func TestAFileBothStagedAndModifiedAppearsOnceAsStaged(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	repo.write("README.md", "# Staged\n")
	repo.git("add", "README.md")
	repo.write("README.md", "# And then modified again\n")

	status, err := git.Status(ctx(t), repo.Path)
	require.NoError(t, err)

	assert.Equal(t, []string{"README.md"}, paths(status.Staged))
	assert.Empty(t, status.Unstaged, "the same path must not appear in two buckets")
}

// A new directory is listed as its individual files, because those are what the user ticks — a
// single directory entry would stage everything inside it with no way to choose.
func TestUntrackedFilesInsideANewDirectoryAreListedIndividually(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	repo.write("pkg/a.go", "package pkg\n")
	repo.write("pkg/b.go", "package pkg\n")

	status, err := git.Status(ctx(t), repo.Path)
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"pkg/a.go", "pkg/b.go"}, paths(status.Untracked))
}

// A move is one entry showing both paths, not a delete plus an add.
func TestARenameIsOneEntryWithBothPaths(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("old.txt", "some content worth detecting as a rename\n")
	repo.commit("initial")

	repo.git("mv", "old.txt", "new.txt")

	status, err := git.Status(ctx(t), repo.Path)
	require.NoError(t, err)

	require.Len(t, status.Staged, 1)
	assert.Equal(t, "old.txt → new.txt", status.Staged[0].Path)
	assert.Equal(t, "renamed", status.Staged[0].Status)
	assert.Empty(t, status.Unstaged)
	assert.Empty(t, status.Untracked, "the old path must not resurface as its own record")
}

func TestDeletionsAreReported(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	repo.write("gone.txt", "bye\n")
	repo.commit("add a file to delete")

	repo.remove("gone.txt")

	status, err := git.Status(ctx(t), repo.Path)
	require.NoError(t, err)
	require.Len(t, status.Unstaged, 1)
	assert.Equal(t, "gone.txt", status.Unstaged[0].Path)
	assert.Equal(t, "deleted", status.Unstaged[0].Status)
}

// Conflicts take priority over every other bucket: a conflicted file is also modified, and
// offering it a "stage" button that resolves nothing is worse than not listing it twice.
func TestConflictsTakePriorityOverEveryOtherBucket(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("shared.txt", "base\n")
	repo.commit("base")

	repo.git("checkout", "--quiet", "-b", "other")
	repo.write("shared.txt", "theirs\n")
	repo.commit("theirs")

	repo.git("checkout", "--quiet", "main")
	repo.write("shared.txt", "ours\n")
	repo.commit("ours")

	// Expected to fail: that is the conflict being created.
	_ = repo.tryGit("merge", "--no-edit", "other")

	status, err := git.Status(ctx(t), repo.Path)
	require.NoError(t, err)

	assert.Equal(t, []string{"shared.txt"}, paths(status.Conflicted))
	assert.Equal(t, "conflicted", status.Conflicted[0].Status)
	assert.Empty(t, status.Staged)
	assert.Empty(t, status.Unstaged)
}

// core.quotepath=off: without it git escapes non-ASCII as octal, and every accented path reaches
// the renderer as mojibake.
func TestNonASCIIPathsSurviveIntact(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	repo.write("documentación/año.txt", "hola\n")

	status, err := git.Status(ctx(t), repo.Path)
	require.NoError(t, err)

	assert.Equal(t, []string{"documentación/año.txt"}, paths(status.Untracked))
}

// -z removes the quoting question entirely, which is the only way a path containing a space is
// unambiguous in porcelain output.
func TestPathsWithSpacesAreOneEntry(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()

	repo.write("a file with spaces.txt", "x\n")

	status, err := git.Status(ctx(t), repo.Path)
	require.NoError(t, err)

	assert.Equal(t, []string{"a file with spaces.txt"}, paths(status.Untracked))
}

func TestStatusOnSomethingThatIsNotARepositoryFails(t *testing.T) {
	_, err := git.Status(ctx(t), t.TempDir())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "git status failed")
}

// A repository directory that is gone — moved, renamed, on an unmounted volume — is the case
// `exec` reports as `fork/exec /usr/bin/git: no such file or directory`, because a failed chdir is
// attributed to the binary the child was about to run. Passed through, that tells a user whose
// folder moved that git is not installed, and prints the whole command line to say it.
//
// Found by the differential oracle (`tools/parity`) against the installed 2.7.1 core, which answers
// `Path '<path>' doesn't point at a valid Git repository or workdir.` for the same call.
func TestAMissingRepositoryDirectoryBlamesTheDirectoryAndNotGit(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "moved-away")

	_, err := git.Status(ctx(t), missing)

	require.Error(t, err)
	assert.Contains(t, err.Error(), missing, "the message names the directory the user has to find")
	assert.Contains(t, err.Error(), "moved, renamed or unmounted")
	assert.NotContains(t, err.Error(), "fork/exec", "Go's own wording blames the wrong thing")
	assert.NotContains(t, err.Error(), "--porcelain", "and does not print the command line into a toast")
}
