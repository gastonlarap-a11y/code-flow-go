package repomap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// write creates a file under root, making its folders.
func write(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
}

// gitRepo makes a git repository in a temporary folder, away from the developer's own config.
func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // literal arguments
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "HOME="+root, "GIT_CONFIG_GLOBAL="+filepath.Join(root, ".gitconfig-test"), "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	}
	run("init", "--quiet")
	return root
}

func paths(s *Snapshot) []string {
	out := make([]string, 0, len(s.files))
	for _, f := range s.files {
		out = append(out, f.Path)
	}
	return out
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	s := NewService(t.TempDir())
	s.recheck = 0 // every question looks again
	return s
}

func TestTheMapReadsWhatGitWouldTrack(t *testing.T) {
	root := gitRepo(t)
	write(t, root, "src/app.ts", "import { add } from './util'\n")
	write(t, root, "src/util.ts", "export const add = 1\n")
	write(t, root, "src/new.ts", "// untracked, and not ignored\n")
	write(t, root, ".gitignore", "ignored.ts\nnode_modules/\n")
	write(t, root, "ignored.ts", "x\n")
	write(t, root, "node_modules/lib/index.js", "x\n")
	write(t, root, ".gitattributes", "gen/** linguist-generated\n")
	write(t, root, "gen/api.ts", "x\n")
	write(t, root, "dist/app.min.js", "x\n")
	write(t, root, "src/blob.ts", "a\x00b\n")
	write(t, root, "src/packed.js", strings.Repeat("x", 4000)+"\n")
	write(t, root, "README.md", "# not code\n")
	write(t, root, "go.mod", "module example.com/m\n")

	snap, build, err := newTestService(t).Snapshot(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, []string{"src/app.ts", "src/new.ts", "src/util.ts"}, paths(snap),
		"ignored, generated, minified and binary files are not the repository's code")
	assert.True(t, build.Git)
	assert.Equal(t, 3, build.Files)
	assert.Equal(t, GrammarsLinked, build.Grammars)
}

func TestASecondLookReadsOnlyWhatChanged(t *testing.T) {
	root := gitRepo(t)
	write(t, root, "a.py", "import b\n")
	write(t, root, "b.py", "x = 1\n")
	service := newTestService(t)

	first, build, err := service.Snapshot(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, 2, build.Parsed)

	again, build, err := service.Snapshot(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, 0, build.Parsed)
	assert.Same(t, first, again, "nothing changed, so the same map")

	write(t, root, "b.py", "x = 2\ny = 3\n")
	future := time.Now().Add(time.Minute)
	require.NoError(t, os.Chtimes(filepath.Join(root, "b.py"), future, future))
	changed, build, err := service.Snapshot(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, 1, build.Parsed)
	assert.NotSame(t, first, changed)
	detail := changed.File("b.py")
	require.NotNil(t, detail)
	assert.Equal(t, 2, detail.Lines)

	write(t, root, "c.py", "z = 0\n")
	grown, _, err := service.Snapshot(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.py", "b.py", "c.py"}, paths(grown))
}

func TestTheMapIsKeptOnDiskAndReadBack(t *testing.T) {
	root := gitRepo(t)
	write(t, root, "main.go", "package main\n")
	write(t, root, "util.go", "package main\n")
	cache := t.TempDir()

	_, build, err := NewService(cache).Snapshot(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, 2, build.Parsed)
	writer := NewService(cache)
	_, _, err = writer.Snapshot(t.Context(), root)
	require.NoError(t, err)
	require.NoError(t, writer.Flush(root))
	real, err := resolveRoot(root)
	require.NoError(t, err)
	require.FileExists(t, cachePath(cache, real))

	_, build, err = NewService(cache).Snapshot(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, 0, build.Parsed, "every file's facts came from the kept map")

	// The MCP server's service reads that map and never writes one of its own.
	empty := t.TempDir()
	reader := NewReadOnlyService(empty)
	_, build, err = reader.Snapshot(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, 2, build.Parsed)
	require.NoError(t, reader.Flush(root))
	entries, err := os.ReadDir(empty)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestAFolderOutsideGitIsWalked(t *testing.T) {
	root := t.TempDir()
	write(t, root, "src/app.py", "import os\n")
	write(t, root, "node_modules/x/index.js", "x\n")
	write(t, root, ".venv/lib/site.py", "x\n")
	write(t, root, "notes.txt", "not code\n")

	snap, build, err := newTestService(t).Snapshot(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, []string{"src/app.py"}, paths(snap))
	assert.False(t, build.Git)
}

func TestARootThatIsNoFolderIsRefused(t *testing.T) {
	service := newTestService(t)
	for _, root := range []string{"", "   ", filepath.Join(t.TempDir(), "missing")} {
		_, _, err := service.Snapshot(t.Context(), root)
		require.ErrorIs(t, err, ErrNotADirectory, root)
	}
}
