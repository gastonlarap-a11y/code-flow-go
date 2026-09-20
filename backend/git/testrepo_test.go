package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// testRepo is a real git repository in a temporary directory.
//
// Real, not faked: the whole point of replacing libgit2 with the `git` binary is that parity now
// comes from git's actual behaviour, so a test that mocked git would prove nothing about the
// thing that changed.
//
// HOME and GIT_CONFIG_GLOBAL point at the temporary directory, and GIT_CONFIG_NOSYSTEM is set.
// Without all three, the developer's own configuration leaks in — a global `core.autocrlf`, a
// `status.renames` setting, a commit template, an `init.defaultBranch` — and the suite passes or
// fails depending on whose machine it runs on.
type testRepo struct {
	t    *testing.T
	Path string
	env  []string
}

func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	dir := t.TempDir()

	repo := &testRepo{
		t:    t,
		Path: dir,
		env: append(os.Environ(),
			"HOME="+dir,
			"GIT_CONFIG_GLOBAL="+filepath.Join(dir, "gitconfig"),
			"GIT_CONFIG_NOSYSTEM=1",
			// A fixed identity and a fixed clock: a commit's hash is otherwise different on every
			// run, and any assertion involving one would have to be a pattern.
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_AUTHOR_DATE=2026-01-01T00:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-01-01T00:00:00+00:00",
		),
	}

	repo.git("init", "--quiet", "--initial-branch=main")

	// The identity again, this time in the repository's own config.
	//
	// The environment above only reaches git invocations this helper makes; the code under test
	// builds its own environment from the process's, so it sees none of them. Without a repository
	// config it would fall back to the developer's global one — which on a CI runner does not exist,
	// and every commit-creating test would fail there and nowhere else.
	repo.git("config", "user.name", "Test")
	repo.git("config", "user.email", "test@example.com")

	// And the line endings, for the same reason and one the comment above got wrong.
	//
	// `GIT_CONFIG_NOSYSTEM` keeps the *system* file out, but Git for Windows defaults
	// `core.autocrlf` to true in the build itself when nothing sets it — so on a Windows runner
	// every checkout came back with CRLF and seven tests compared "committed\n" against
	// "committed\r\n". Setting it here makes the fixture the same bytes on every platform, which
	// is what these tests are actually about.
	//
	// It says nothing about how the app treats a CRLF working tree; that is a real repository's
	// business and `Runner.RunRaw` exists for it.
	repo.git("config", "core.autocrlf", "false")

	return repo
}

// command builds a git invocation bound to the test's context, so a hung git ends with the test
// rather than outliving it.
func (r *testRepo) command(args []string) *exec.Cmd {
	r.t.Helper()
	cmd := exec.CommandContext(r.t.Context(), "git", args...) //nolint:gosec // literal arguments
	cmd.Dir = r.Path
	cmd.Env = r.env
	return cmd
}

// git runs a command in the repository and fails the test if it does not succeed.
func (r *testRepo) git(args ...string) string {
	r.t.Helper()
	out, err := r.command(args).CombinedOutput()
	require.NoError(r.t, err, "git %s: %s", strings.Join(args, " "), out)
	return string(out)
}

// tryGit runs a command that is expected to fail — creating a conflict, checking out over local
// changes — and returns its combined output without failing the test.
func (r *testRepo) tryGit(args ...string) string {
	r.t.Helper()
	out, _ := r.command(args).CombinedOutput()
	return string(out)
}

// write creates or replaces a file, making its parent directories.
func (r *testRepo) write(path, content string) {
	r.t.Helper()
	full := filepath.Join(r.Path, filepath.FromSlash(path))
	require.NoError(r.t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(r.t, os.WriteFile(full, []byte(content), 0o600))
}

// writeBytes creates a file with raw bytes, for the binary cases.
func (r *testRepo) writeBytes(path string, content []byte) {
	r.t.Helper()
	full := filepath.Join(r.Path, filepath.FromSlash(path))
	require.NoError(r.t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(r.t, os.WriteFile(full, content, 0o600))
}

// remove deletes a file from the working tree.
func (r *testRepo) remove(path string) {
	r.t.Helper()
	require.NoError(r.t, os.Remove(filepath.Join(r.Path, filepath.FromSlash(path))))
}

// commit stages everything and commits it.
func (r *testRepo) commit(message string) {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "--quiet", "--no-verify", "-m", message)
}

// seed makes a repository with one committed file, which most tests want as their starting point.
func (r *testRepo) seed() {
	r.t.Helper()
	r.write("README.md", "# Test\n")
	r.commit("initial")
}

func ctx(t *testing.T) context.Context { return t.Context() }
