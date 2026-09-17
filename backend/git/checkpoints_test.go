package git_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge/jsonwire"
	"github.com/gastonlarap-a11y/code-flow/backend/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkpointed is the fixture the three `git_checkpoint` vectors share: one committed file.
func checkpointed(t *testing.T) *testRepo {
	t.Helper()
	repo := newTestRepo(t)
	repo.write("tracked.txt", "original\n")
	repo.commit("initial")
	return repo
}

func create(t *testing.T, repo *testRepo, kind string) string {
	t.Helper()
	id, err := git.CreateCheckpoint(ctx(t), repo.Path, kind)
	require.NoError(t, err)
	return id
}

func list(t *testing.T, repo *testRepo) []git.Checkpoint {
	t.Helper()
	got, err := git.ListCheckpoints(ctx(t), repo.Path)
	require.NoError(t, err)
	return got
}

func read(t *testing.T, repo *testRepo, path string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repo.Path, filepath.FromSlash(path)))
	require.NoError(t, err)
	return string(content)
}

// ---- the three scenario vectors -----------------------------------------------------------------

// git_checkpoint.vectors.json#restore-reverts-edits-and-deletes-created-files
func TestRestoreRevertsEditsAndDeletesCreatedFiles(t *testing.T) {
	repo := checkpointed(t)
	id := create(t, repo, "fix-finding")

	repo.write("tracked.txt", "rewritten by the agent\n")
	repo.write("new.txt", "created by the agent\n")

	checkpoints := list(t, repo)
	require.Len(t, checkpoints, 1)
	assert.Equal(t, "fix-finding", checkpoints[0].Kind)
	assert.Equal(t, []string{"new.txt", "tracked.txt"}, checkpoints[0].ChangedPaths,
		"sorted, and it is what restoring right now would touch")

	restored, err := git.RestoreCheckpoint(ctx(t), repo.Path, id)

	require.NoError(t, err)
	assert.Equal(t, []string{"new.txt", "tracked.txt"}, restored)
	assert.Equal(t, "original\n", read(t, repo, "tracked.txt"))
	assert.NoFileExists(t, filepath.Join(repo.Path, "new.txt"),
		"absent from the snapshot's tree, so undoing means removing it")
}

// git_checkpoint.vectors.json#snapshot-leaves-index-alone
//
// The property the whole design exists for: the real .git/index is never opened.
func TestSnapshotLeavesTheIndexAlone(t *testing.T) {
	repo := checkpointed(t)
	repo.write("staged.txt", "staged content\n")
	repo.git("add", "staged.txt")
	repo.write("untracked.txt", "untracked content\n")

	id := create(t, repo, "chat")

	assert.Equal(t, []string{"staged.txt", "tracked.txt"},
		strings.Fields(repo.git("ls-files")),
		"the staging area reads exactly as the user left it")

	// Clobber both and put them back.
	repo.remove("untracked.txt")
	repo.write("staged.txt", "clobbered\n")

	_, err := git.RestoreCheckpoint(ctx(t), repo.Path, id)

	require.NoError(t, err)
	assert.Equal(t, "untracked content\n", read(t, repo, "untracked.txt"),
		"an untracked file is in the snapshot too")
	assert.Equal(t, "staged content\n", read(t, repo, "staged.txt"))
}

// git_checkpoint.vectors.json#unchanged-checkpoint-auto-drops
func TestARunThatChangedNothingDropsItsCheckpoint(t *testing.T) {
	repo := checkpointed(t)
	id := create(t, repo, "chat")

	dropped, err := git.RemoveCheckpointIfUnchanged(ctx(t), repo.Path, id)

	require.NoError(t, err)
	assert.True(t, dropped)
	assert.Empty(t, list(t, repo), "no undo entry for a run that touched nothing")
}

// ---- the rest -----------------------------------------------------------------------------------

func TestACheckpointTouchesNeitherHeadNorAnyBranch(t *testing.T) {
	repo := checkpointed(t)
	head := strings.TrimSpace(repo.git("rev-parse", "HEAD"))
	branches := repo.git("branch", "-a")

	create(t, repo, "chat")

	assert.Equal(t, head, strings.TrimSpace(repo.git("rev-parse", "HEAD")))
	assert.Equal(t, branches, repo.git("branch", "-a"))
	assert.Empty(t, strings.TrimSpace(repo.git("status", "--porcelain")),
		"git status reads the same before and after")
}

func TestTheCheckpointIdShape(t *testing.T) {
	repo := checkpointed(t)

	id := create(t, repo, "chat")

	assert.Regexp(t, regexp.MustCompile(`^\d{10}-[0-9a-f]{8}$`), id,
		"unix seconds and eight hex characters, as 2.x wrote them")
}

// Two in the same wall-clock second are told apart only by the random suffix.
func TestTwoCheckpointsInTheSameSecondAreDistinct(t *testing.T) {
	repo := checkpointed(t)

	first, second := create(t, repo, "chat"), create(t, repo, "chat")

	assert.NotEqual(t, first, second)
	assert.Len(t, list(t, repo), 2)
}

func TestChangedPathsIsComputedFreshNotRecorded(t *testing.T) {
	repo := checkpointed(t)
	create(t, repo, "chat")
	assert.Empty(t, list(t, repo)[0].ChangedPaths)

	repo.write("tracked.txt", "edited after the fact\n")
	assert.Equal(t, []string{"tracked.txt"}, list(t, repo)[0].ChangedPaths)

	// Edited back by hand: the diff is content-based, so the path drops out again.
	repo.write("tracked.txt", "original\n")
	assert.Empty(t, list(t, repo)[0].ChangedPaths)
}

func TestAnEmptyCheckpointListIsAnArray(t *testing.T) {
	repo := checkpointed(t)

	checkpoints, err := git.ListCheckpoints(ctx(t), repo.Path)

	require.NoError(t, err)
	assert.Empty(t, checkpoints)
	assert.NotNil(t, checkpoints)
	require.NoError(t, jsonwire.AssertNoNilSlices(checkpoints))
}

func TestACheckpointWithNoChangesCarriesAnArrayNotNull(t *testing.T) {
	repo := checkpointed(t)
	create(t, repo, "chat")

	require.NoError(t, jsonwire.AssertNoNilSlices(list(t, repo)),
		"changed_paths is mapped over by the modal without guarding")
}

func TestCheckpointsAreListedNewestFirst(t *testing.T) {
	repo := checkpointed(t)
	create(t, repo, "older")
	// A checkpoint whose commit is a second newer, without sleeping for it.
	newer := create(t, repo, "newer")
	repo.git("update-ref", checkpointRef(newer), bumpedCommit(t, repo, newer))

	kinds := []string{}
	for _, checkpoint := range list(t, repo) {
		kinds = append(kinds, checkpoint.Kind)
	}
	assert.Equal(t, []string{"newer", "older"}, kinds)
}

// checkpointRef is the namespace 2.7.x wrote and this version has to find.
func checkpointRef(id string) string { return "refs/codeflow/checkpoints/" + id }

// bumpedCommit rewrites a checkpoint's commit with a much later committer date, so ordering can be
// asserted without the test sleeping through it.
func bumpedCommit(t *testing.T, repo *testRepo, id string) string {
	t.Helper()
	tree := strings.TrimSpace(repo.git("rev-parse", checkpointRef(id)+"^{tree}"))
	return commitTreeAt(t, repo, tree, "newer", "2027-01-01T00:00:00+00:00")
}

// Pruning is by the checkpoint's own commit time, not by the timestamp inside its id — so the ids
// here run backwards on purpose, and a prune that trusted them would keep the wrong twenty.
func TestPruningKeepsTheTwentyNewestByCommitTime(t *testing.T) {
	repo := checkpointed(t)

	ids := make([]string, 0, 21)
	for i := range 21 {
		id := fmt.Sprintf("%d-aaaaaaa%d", 1700000020-i, i%10)
		ids = append(ids, id)
		repo.git("update-ref", checkpointRef(id), datedCommit(t, repo, i))
	}
	require.Len(t, list(t, repo), 21, "written straight to refs, so nothing has pruned yet")

	// Pruning happens only as a side effect of creating one.
	fresh := create(t, repo, "chat")

	surviving := make(map[string]bool, maxKept)
	for _, checkpoint := range list(t, repo) {
		surviving[checkpoint.ID] = true
	}

	assert.Len(t, surviving, maxKept)
	assert.True(t, surviving[fresh], "the one just taken is the newest of all")
	assert.False(t, surviving[ids[0]], "hour 0 is the oldest commit")
	assert.False(t, surviving[ids[1]], "hour 1 is the second oldest")
	assert.True(t, surviving[ids[2]], "hour 2 is inside the twenty")
	assert.True(t, surviving[ids[20]], "hour 20 is the newest of the written ones")
}

const maxKept = 20

// datedCommit builds a commit whose committer time is i hours past a fixed epoch.
func datedCommit(t *testing.T, repo *testRepo, i int) string {
	t.Helper()
	tree := strings.TrimSpace(repo.git("rev-parse", "HEAD^{tree}"))
	return commitTreeAt(t, repo, tree, "old", fmt.Sprintf("2026-01-01T%02d:00:00+00:00", i%24))
}

// commitTreeAt writes a commit object at a chosen time.
//
// The date variables are replaced rather than appended: the test repository already pins both, and
// which of two identically named entries a process sees is not something to rely on.
func commitTreeAt(t *testing.T, repo *testRepo, tree, message, when string) string {
	t.Helper()

	cmd := repo.command([]string{"commit-tree", tree, "-m", message})
	env := make([]string, 0, len(cmd.Env)+2)
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "GIT_COMMITTER_DATE=") || strings.HasPrefix(entry, "GIT_AUTHOR_DATE=") {
			continue
		}
		env = append(env, entry)
	}
	cmd.Env = append(env, "GIT_COMMITTER_DATE="+when, "GIT_AUTHOR_DATE="+when)

	out, err := cmd.Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

func TestDeletingACheckpointThatIsNotThereIsNotAnError(t *testing.T) {
	repo := checkpointed(t)

	assert.NoError(t, git.DeleteCheckpoint(ctx(t), repo.Path, "1700000000-deadbeef"))
}

// The asymmetry is deliberate: this one runs automatically after a run, where a missing checkpoint
// means something went wrong.
func TestRemoveIfUnchangedOnAMissingCheckpointIsAnError(t *testing.T) {
	repo := checkpointed(t)

	_, err := git.RemoveCheckpointIfUnchanged(ctx(t), repo.Path, "1700000000-deadbeef")

	assert.EqualError(t, err, "checkpoint '1700000000-deadbeef' no longer exists")
}

func TestRemoveIfUnchangedKeepsACheckpointThatMatters(t *testing.T) {
	repo := checkpointed(t)
	id := create(t, repo, "chat")
	repo.write("tracked.txt", "the run did something\n")

	dropped, err := git.RemoveCheckpointIfUnchanged(ctx(t), repo.Path, id)

	require.NoError(t, err)
	assert.False(t, dropped)
	assert.Len(t, list(t, repo), 1)
}

func TestRestoringAMissingCheckpoint(t *testing.T) {
	repo := checkpointed(t)

	_, err := git.RestoreCheckpoint(ctx(t), repo.Path, "1700000000-deadbeef")

	// VERBATIM: the modal shows this one.
	assert.EqualError(t, err, "checkpoint '1700000000-deadbeef' no longer exists")
}

// Restoring twice is idempotent past the first call.
func TestRestoringTwiceTouchesNothingTheSecondTime(t *testing.T) {
	repo := checkpointed(t)
	id := create(t, repo, "chat")
	repo.write("tracked.txt", "agent\n")

	first, err := git.RestoreCheckpoint(ctx(t), repo.Path, id)
	require.NoError(t, err)
	require.Equal(t, []string{"tracked.txt"}, first)

	second, err := git.RestoreCheckpoint(ctx(t), repo.Path, id)

	require.NoError(t, err)
	assert.Empty(t, second)
}

// An ignored file is not the agent's doing and must not be snapshotted, or restoring would resurrect
// build output the user deleted.
func TestIgnoredFilesStayOutOfTheSnapshot(t *testing.T) {
	repo := checkpointed(t)
	repo.write(".gitignore", "build/\n")
	repo.commit("ignore build output")
	repo.write("build/artifact.bin", "generated\n")

	id := create(t, repo, "chat")
	repo.remove("build/artifact.bin")

	restored, err := git.RestoreCheckpoint(ctx(t), repo.Path, id)

	require.NoError(t, err)
	assert.Empty(t, restored)
	assert.NoFileExists(t, filepath.Join(repo.Path, "build", "artifact.bin"))
}

// A file the agent deleted comes back, which needs the snapshot to have recorded its presence and
// the restore to recreate its parent directory.
func TestAFileTheRunDeletedComesBack(t *testing.T) {
	repo := checkpointed(t)
	repo.write("pkg/deep/thing.go", "package deep\n")
	repo.commit("nested file")
	id := create(t, repo, "chat")

	require.NoError(t, os.RemoveAll(filepath.Join(repo.Path, "pkg")))

	restored, err := git.RestoreCheckpoint(ctx(t), repo.Path, id)

	require.NoError(t, err)
	assert.Equal(t, []string{"pkg/deep/thing.go"}, restored)
	assert.Equal(t, "package deep\n", read(t, repo, "pkg/deep/thing.go"))
}

// Undo has to be byte-exact or it becomes a source of diffs of its own.
func TestRestorePreservesCRLFAndAMissingFinalNewline(t *testing.T) {
	repo := checkpointed(t)
	repo.write(".gitattributes", "* -text\n")
	repo.writeBytes("crlf.txt", []byte("one\r\ntwo"))
	repo.commit("crlf")
	id := create(t, repo, "chat")

	repo.writeBytes("crlf.txt", []byte("clobbered\n"))
	_, err := git.RestoreCheckpoint(ctx(t), repo.Path, id)

	require.NoError(t, err)
	assert.Equal(t, "one\r\ntwo", read(t, repo, "crlf.txt"))
}

// A repository with no identity must still be protected. git would otherwise invent one from the OS
// account and write the machine user's real name into the commit.
func TestARepositoryWithNoIdentityGetsTheCodeFlowFallback(t *testing.T) {
	repo := checkpointed(t)
	repo.git("config", "--unset", "user.name")
	repo.git("config", "--unset", "user.email")

	// The code under test builds its environment from this process's, so the developer's own global
	// config is visible to it unless the process's own is redirected. Without these three the test
	// passes on a machine with no ~/.gitconfig and fails on every other, which is worse than not
	// having it.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	id := create(t, repo, "chat")

	assert.Equal(t, "CodeFlow <codeflow@local>",
		strings.TrimSpace(repo.git("log", "-1", "--format=%an <%ae>", checkpointRef(id))))
}

func TestACheckpointInARepositoryWithNoCommits(t *testing.T) {
	repo := newTestRepo(t)
	repo.write("first.txt", "before any commit\n")

	id := create(t, repo, "chat")
	repo.write("first.txt", "clobbered\n")

	restored, err := git.RestoreCheckpoint(ctx(t), repo.Path, id)

	require.NoError(t, err)
	assert.Equal(t, []string{"first.txt"}, restored)
	assert.Equal(t, "before any commit\n", read(t, repo, "first.txt"))
	assert.Empty(t, strings.Fields(repo.git("log", "-1", "--format=%P", checkpointRef(id))),
		"parentless, because there is no HEAD to parent it on")
}
