package git_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// origin builds a repository that can be cloned and pushed to.
//
// Bare, because pushing to a non-bare repository's checked-out branch is refused by git — which is
// the same reason real remotes are bare.
func origin(t *testing.T) (*testRepo, string) {
	t.Helper()
	source := newTestRepo(t)
	source.write("README.md", "# Origin\n")
	source.commit("initial")

	bare := filepath.Join(t.TempDir(), "origin.git")
	source.git("clone", "--quiet", "--bare", source.Path, bare)
	source.git("remote", "add", "origin", bare)
	source.git("push", "--quiet", "-u", "origin", "main")

	return source, bare
}

func progressLines(events []any) []string {
	lines := make([]string, 0, len(events))
	for _, event := range events {
		lines = append(lines, event.(git.GitProgressEvent).Line)
	}
	return lines
}

func doneEvent(t *testing.T, recorder *bridge.RecordingEmitter) git.GitDoneEvent {
	t.Helper()
	done := recorder.Named("git:done")
	require.Len(t, done, 1, "exactly one git:done per operation")
	return done[0].(git.GitDoneEvent)
}

func TestClone(t *testing.T) {
	_, bare := origin(t)
	recorder := &bridge.RecordingEmitter{}
	dest := filepath.Join(t.TempDir(), "clone")

	err := git.NewNetwork(recorder).Clone(ctx(t), bare, dest)

	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dest, "README.md"))

	done := doneEvent(t, recorder)
	assert.Equal(t, "clone", done.Op)
	assert.True(t, done.Success)
	assert.Equal(t, "ok", done.Message, "a success says ok, never the output")

	// git writes its progress to stderr; treating that as an error channel would leave this empty.
	assert.NotEmpty(t, progressLines(recorder.Named("git:progress")))
	for _, payload := range recorder.Named("git:progress") {
		assert.Equal(t, "clone", payload.(git.GitProgressEvent).Op)
	}
}

func TestFetchDefaultsToOrigin(t *testing.T) {
	source, bare := origin(t)
	recorder := &bridge.RecordingEmitter{}

	// A second clone moves the remote forward behind the first one's back.
	other := filepath.Join(t.TempDir(), "other")
	source.git("clone", "--quiet", bare, other)
	writer := &testRepo{t: t, Path: other, env: source.env}
	writer.git("config", "user.name", "Test")
	writer.git("config", "user.email", "test@example.com")
	writer.write("added.txt", "from elsewhere\n")
	writer.git("add", "added.txt")
	writer.git("commit", "--quiet", "-m", "elsewhere")
	writer.git("push", "--quiet", "origin", "main")

	err := git.NewNetwork(recorder).Fetch(ctx(t), source.Path, nil)

	require.NoError(t, err)
	assert.Contains(t, source.git("log", "--format=%s", "-1", "origin/main"), "elsewhere",
		"the remote-tracking ref moved")
	assert.Equal(t, "fetch", doneEvent(t, recorder).Op)
}

func TestFetchUsesTheNamedRemote(t *testing.T) {
	source, bare := origin(t)
	source.git("remote", "add", "elsewhere", bare)
	name := "elsewhere"

	err := git.NewNetwork(nil).Fetch(ctx(t), source.Path, &name)

	require.NoError(t, err)
	assert.NotEmpty(t, strings.TrimSpace(source.git("rev-parse", "refs/remotes/elsewhere/main")))
}

// The refspec form is what makes reviewing a fork's pull request work: the head ref is not one of
// the remote's default branches, so a plain fetch never brings it.
func TestFetchRefspecsBringsARefNoDefaultFetchWould(t *testing.T) {
	source, bare := origin(t)
	source.git("checkout", "--quiet", "-b", "pr-head")
	source.write("proposed.txt", "a change\n")
	source.commit("proposed")
	source.git("push", "--quiet", "origin", "pr-head:refs/pull/7/head")
	source.git("checkout", "--quiet", "main")
	source.git("branch", "-D", "pr-head")
	source.git("update-ref", "-d", "refs/remotes/origin/pr-head")

	consumer := filepath.Join(t.TempDir(), "consumer")
	source.git("clone", "--quiet", bare, consumer)
	require.Empty(t, strings.TrimSpace(
		(&testRepo{t: t, Path: consumer, env: source.env}).tryGit("rev-parse", "--verify", "--quiet", "FETCH_HEAD")),
		"nothing fetched yet")

	err := git.NewNetwork(nil).FetchRefspecs(ctx(t), consumer, "origin",
		[]string{"refs/pull/7/head:refs/remotes/origin/pr-7"})

	require.NoError(t, err)
	fetched := &testRepo{t: t, Path: consumer, env: source.env}
	assert.Contains(t, fetched.git("log", "--format=%s", "-1", "refs/remotes/origin/pr-7"), "proposed")
}

func TestPull(t *testing.T) {
	source, bare := origin(t)
	other := filepath.Join(t.TempDir(), "other")
	source.git("clone", "--quiet", bare, other)
	writer := &testRepo{t: t, Path: other, env: source.env}
	writer.git("config", "user.name", "Test")
	writer.git("config", "user.email", "test@example.com")
	writer.write("added.txt", "pulled\n")
	writer.git("add", "added.txt")
	writer.git("commit", "--quiet", "-m", "to be pulled")
	writer.git("push", "--quiet", "origin", "main")

	err := git.NewNetwork(nil).Pull(ctx(t), source.Path)

	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(source.Path, "added.txt"))
}

// GIT-037: without --no-edit a divergent pull tries to open an editor against a child with no TTY,
// and exits with the merge applied but never committed — leaving the repository silently
// mid-merge. core.editor points at a binary that does not exist, so any attempt fails loudly here
// instead of hanging.
func TestPullingADivergentBranchCompletesWithoutAnEditor(t *testing.T) {
	source, bare := origin(t)
	source.git("config", "core.editor", "/nonexistent/editor")

	// `pull.rebase false` is required to reach the merge path at all: since git 2.27 a divergent
	// pull with no reconciliation configured refuses outright ("Need to specify how to reconcile
	// divergent branches") rather than merging. Measured — it is git's behaviour and was equally
	// true of 2.x, which shelled out to the same command, so the editor hazard GIT-037 describes
	// only exists for repositories whose owner has chosen merge.
	source.git("config", "pull.rebase", "false")

	other := filepath.Join(t.TempDir(), "other")
	source.git("clone", "--quiet", bare, other)
	writer := &testRepo{t: t, Path: other, env: source.env}
	writer.git("config", "user.name", "Test")
	writer.git("config", "user.email", "test@example.com")
	writer.write("theirs.txt", "remote work\n")
	writer.git("add", "theirs.txt")
	writer.git("commit", "--quiet", "-m", "theirs")
	writer.git("push", "--quiet", "origin", "main")

	// Local work that did not come from the remote: the histories have diverged.
	source.write("ours.txt", "local work\n")
	source.commit("ours")

	recorder := &bridge.RecordingEmitter{}
	err := git.NewNetwork(recorder).Pull(ctx(t), source.Path)

	require.NoError(t, err)
	assert.True(t, doneEvent(t, recorder).Success)

	merging, err := git.IsMerging(ctx(t), source.Path)
	require.NoError(t, err)
	assert.False(t, merging, "the merge commit was made, not left pending")
	assert.Len(t, strings.Fields(source.git("log", "-1", "--format=%P")), 2)
}

func TestPush(t *testing.T) {
	source, bare := origin(t)
	source.write("pushed.txt", "new work\n")
	source.commit("to push")
	recorder := &bridge.RecordingEmitter{}

	err := git.NewNetwork(recorder).Push(ctx(t), source.Path, false)

	require.NoError(t, err)
	assert.Equal(t, "push", doneEvent(t, recorder).Op)

	remote := &testRepo{t: t, Path: bare, env: source.env}
	assert.Contains(t, remote.git("log", "--format=%s", "-1", "main"), "to push")
}

func TestPushSettingTheUpstream(t *testing.T) {
	source, bare := origin(t)
	source.git("checkout", "--quiet", "-b", "feature")
	source.write("feature.txt", "work\n")
	source.commit("feature work")

	err := git.NewNetwork(nil).Push(ctx(t), source.Path, true)

	require.NoError(t, err)
	assert.Equal(t, "origin/feature",
		strings.TrimSpace(source.git("rev-parse", "--abbrev-ref", "feature@{upstream}")))

	remote := &testRepo{t: t, Path: bare, env: source.env}
	assert.Contains(t, remote.git("log", "--format=%s", "-1", "feature"), "feature work")
}

// Refused before anything is spawned: there is no branch name to set an upstream on.
func TestPushSettingTheUpstreamFromADetachedHead(t *testing.T) {
	source, _ := origin(t)
	source.git("checkout", "--quiet", "--detach")
	recorder := &bridge.RecordingEmitter{}

	err := git.NewNetwork(recorder).Push(ctx(t), source.Path, true)

	assert.EqualError(t, err, "cannot push -u from a detached HEAD")
	assert.Empty(t, recorder.Events(), "nothing ran, so nothing reported")
}

// An unborn HEAD has a symbolic ref but no commit, and 2.x reported it with the same message.
func TestPushSettingTheUpstreamFromAnUnbornHead(t *testing.T) {
	repo := newTestRepo(t)

	err := git.NewNetwork(nil).Push(ctx(t), repo.Path, true)

	assert.EqualError(t, err, "cannot push -u from a detached HEAD")
}

// A failure is both an event and a rejection, and the two strings are deliberately not identical.
func TestAFailureIsReportedTwiceInTwoShapes(t *testing.T) {
	repo := newTestRepo(t)
	repo.seed()
	recorder := &bridge.RecordingEmitter{}

	err := git.NewNetwork(recorder).Fetch(ctx(t), repo.Path, nil)

	require.Error(t, err, "there is no remote called origin")
	done := doneEvent(t, recorder)
	assert.False(t, done.Success)
	assert.NotEmpty(t, done.Message)
	assert.NotEqual(t, "ok", done.Message)

	assert.Equal(t, "git fetch failed: "+done.Message, err.Error(),
		"the rejection prefixes the operation; the event does not, so a listener showing both "+
			"does not print the name twice")
}

// Progress is streamed while the operation runs, not replayed at the end.
func TestProgressArrivesBeforeDone(t *testing.T) {
	_, bare := origin(t)
	dest := filepath.Join(t.TempDir(), "clone")

	var order []string
	emitter := bridge.EmitterFunc(func(name string, _ any) { order = append(order, name) })

	require.NoError(t, git.NewNetwork(emitter).Clone(ctx(t), bare, dest))

	require.NotEmpty(t, order)
	assert.Equal(t, "git:done", order[len(order)-1], "done is last")
	assert.Equal(t, "git:progress", order[0])
}
