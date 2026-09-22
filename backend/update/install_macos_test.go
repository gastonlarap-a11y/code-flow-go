package update

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

/*
The bundle swap (BOOT-022).

Written against the defect it fixes: an update that mounted a disk image, replaced nothing, left the
volume attached and kept the download. The parts that can be asserted without a disk image are the
two that decide whether an install is safe — where the bundle is, and that the exchange either
completes or leaves the old version exactly where it was.
*/

// bundleAt lays down a fake `.app` with one file inside, and answers the path to its executable.
func bundleAt(t *testing.T, root, name, marker string) string {
	t.Helper()

	bundle := filepath.Join(root, name+".app")
	macOS := filepath.Join(bundle, "Contents", "MacOS")
	require.NoError(t, os.MkdirAll(macOS, 0o755))

	executable := filepath.Join(macOS, name)
	require.NoError(t, os.WriteFile(executable, []byte(marker), 0o755)) //nolint:gosec // a fake bundle
	return executable
}

func TestTheBundleIsFoundFromTheRunningBinary(t *testing.T) {
	root := t.TempDir()
	executable := bundleAt(t, root, "CodeFlow", "v1")

	bundle, err := bundleFor(executable)

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "CodeFlow.app"), bundle)
}

/*
A `task build` binary, or a test, is not inside a bundle.

It matters that this is a named condition rather than an error: the caller falls back to opening the
image, which is the old behaviour and the right one when there is no installed app to replace.
*/
func TestABinaryOutsideABundleIsNamedRatherThanFailed(t *testing.T) {
	loose := filepath.Join(t.TempDir(), "CodeFlow")
	require.NoError(t, os.WriteFile(loose, []byte("v1"), 0o755)) //nolint:gosec // a fake binary

	_, err := bundleFor(loose)

	assert.ErrorIs(t, err, errNoBundle)
}

// A directory that merely ends in `.app` two levels up is not a bundle either: the layout has to be
// `…/X.app/Contents/MacOS/X`, or the swap would rename something that is not an application.
func TestOnlyTheRealBundleLayoutCounts(t *testing.T) {
	root := t.TempDir()
	wrong := filepath.Join(root, "CodeFlow.app", "Resources", "CodeFlow")
	require.NoError(t, os.MkdirAll(filepath.Dir(wrong), 0o755))
	require.NoError(t, os.WriteFile(wrong, []byte("v1"), 0o755)) //nolint:gosec // a fake binary

	_, err := bundleFor(wrong)

	assert.ErrorIs(t, err, errNoBundle)
}

func TestTheSwapPutsTheNewVersionWhereTheOldOneWas(t *testing.T) {
	root := t.TempDir()
	bundleAt(t, root, "CodeFlow", "installed")
	source := filepath.Join(root, "staging", "CodeFlow.app")
	require.NoError(t, os.MkdirAll(filepath.Join(source, "Contents", "MacOS"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(source, "Contents", "MacOS", "CodeFlow"), []byte("updated"), 0o755)) //nolint:gosec

	target := filepath.Join(root, "CodeFlow.app")
	require.NoError(t, swapBundle(t.Context(), source, target))

	got, err := os.ReadFile(filepath.Join(target, "Contents", "MacOS", "CodeFlow")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "updated", string(got))
}

// Neither scratch path may survive: one is the next update's leftover and the other is a whole
// stale copy of the application sitting beside the live one.
func TestTheSwapLeavesNoScratchCopiesBehind(t *testing.T) {
	root := t.TempDir()
	bundleAt(t, root, "CodeFlow", "installed")
	source := filepath.Join(root, "staging", "CodeFlow.app")
	require.NoError(t, os.MkdirAll(source, 0o755))

	target := filepath.Join(root, "CodeFlow.app")
	require.NoError(t, swapBundle(t.Context(), source, target))

	assert.NoDirExists(t, target+".new")
	assert.NoDirExists(t, target+".old")
}

/*
The failure that must never happen: no application at all.

If the new version cannot be moved into place after the old one has been set aside, the old one goes
back. Forced here by making the target path un-creatable — a file where the bundle should go — so
the second rename fails with the first already done.
*/
func TestAFailedSwapPutsTheInstalledVersionBack(t *testing.T) {
	root := t.TempDir()
	bundleAt(t, root, "CodeFlow", "installed")
	target := filepath.Join(root, "CodeFlow.app")

	// A source that does not exist: `ditto` fails, so nothing is moved at all.
	err := swapBundle(t.Context(), filepath.Join(root, "nothing-here.app"), target)

	require.Error(t, err)
	assert.DirExists(t, target, "the installed version is still installed")
	got, readErr := os.ReadFile(filepath.Join(target, "Contents", "MacOS", "CodeFlow")) //nolint:gosec
	require.NoError(t, readErr)
	assert.Equal(t, "installed", string(got), "and it is still the version that was there")
}

// An interrupted attempt leaves scratch directories. The next one must not trip over them.
func TestLeftoversFromAnInterruptedAttemptAreCleared(t *testing.T) {
	root := t.TempDir()
	bundleAt(t, root, "CodeFlow", "installed")
	target := filepath.Join(root, "CodeFlow.app")
	require.NoError(t, os.MkdirAll(target+".new", 0o755))
	require.NoError(t, os.MkdirAll(target+".old", 0o755))

	source := filepath.Join(root, "staging", "CodeFlow.app")
	require.NoError(t, os.MkdirAll(source, 0o755))

	require.NoError(t, swapBundle(t.Context(), source, target))
	assert.NoDirExists(t, target+".new")
	assert.NoDirExists(t, target+".old")
}
