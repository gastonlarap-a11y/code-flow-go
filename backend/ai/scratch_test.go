package ai_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func agedFile(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("payload"), 0o644))
	stamp := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(path, stamp, stamp))
	return path
}

func agedDir(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(path, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(path, "brief.txt"), []byte("brief"), 0o644))
	stamp := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(path, stamp, stamp))
	return path
}

func TestSweepRemovesOrphansOfBothShapes(t *testing.T) {
	dir := t.TempDir()
	openCode := agedFile(t, dir, "codeflow-opencode-abc.txt", 2*time.Hour)
	agy := agedDir(t, dir, "codeflow-agy-def", 2*time.Hour)

	removed := ai.SweepOrphans(dir)

	assert.Equal(t, 2, removed)
	assert.NoFileExists(t, openCode)
	assert.NoDirExists(t, agy)
}

// A development build and an installed CodeFlow can run at the same time, so a young scratch file
// may be another process's live invocation. Age is the discriminator that needs no coordination.
func TestSweepLeavesYoungEntriesAlone(t *testing.T) {
	dir := t.TempDir()
	live := agedFile(t, dir, "codeflow-opencode-live.txt", 5*time.Minute)

	assert.Equal(t, 0, ai.SweepOrphans(dir))
	assert.FileExists(t, live)
}

func TestSweepIgnoresEverythingItDidNotCreate(t *testing.T) {
	dir := t.TempDir()
	foreign := agedFile(t, dir, "some-other-tool-cache.txt", 10*time.Hour)
	alsoForeign := agedDir(t, dir, "com.apple.something", 10*time.Hour)

	assert.Equal(t, 0, ai.SweepOrphans(dir))
	assert.FileExists(t, foreign)
	assert.DirExists(t, alsoForeign)
}

// The sweep runs at start-up, before the window exists. A temp directory it cannot read is not a
// reason to fail a launch.
func TestSweepIsSilentOnAnUnreadableDirectory(t *testing.T) {
	assert.Equal(t, 0, ai.SweepOrphans(filepath.Join(t.TempDir(), "does-not-exist")))
}

func TestIsScratchPath(t *testing.T) {
	temp := os.TempDir()

	for name, tc := range map[string]struct {
		path string
		want bool
	}{
		"an opencode payload":              {filepath.Join(temp, "codeflow-opencode-abc.txt"), true},
		"an agy brief directory":           {filepath.Join(temp, "codeflow-agy-def"), true},
		"the file inside an agy directory": {filepath.Join(temp, "codeflow-agy-def", "brief.txt"), true},
		"another tool's temp file":         {filepath.Join(temp, "something-else.txt"), false},
		"the temp root itself":             {temp, false},
		"a matching name outside temp":     {filepath.Join("/not/temp", "codeflow-agy-def"), false},
		"empty":                            {"", false},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, ai.IsScratchPath(tc.path))
		})
	}
}
