package dbml_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/dbml"
)

// touch creates a file and every directory above it.
func touch(t *testing.T, root string, parts ...string) string {
	t.Helper()

	path := filepath.Join(append([]string{root}, parts...)...)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("Table t { id int }\n"), 0o600))
	return path
}

func TestListDocumentsWalksTheFolder(t *testing.T) {
	root := t.TempDir()

	touch(t, root, "orders.dbml")
	touch(t, root, "schemas", "billing.dbml")
	touch(t, root, "schemas", "nested", "deep.dbml")
	touch(t, root, "notes.md")
	touch(t, root, "schemas", "readme.txt")

	found, err := dbml.ListDocuments(t.Context(), root)
	require.NoError(t, err)

	// Project-relative, sorted, and with `/` on every platform — these are layout keys as well as
	// labels, and a path that differed by separator would be a second document to the layout store.
	assert.Equal(t, []string{
		"orders.dbml",
		"schemas/billing.dbml",
		"schemas/nested/deep.dbml",
	}, found)
}

// The extension is compared case-insensitively, because two of the three platforms this ships to
// have case-insensitive filesystems and a `.DBML` is the same document to them.
func TestTheExtensionIsMatchedCaseInsensitively(t *testing.T) {
	root := t.TempDir()

	touch(t, root, "a.dbml")
	touch(t, root, "b.DBML")
	touch(t, root, "c.DbMl")
	touch(t, root, "d.dbmlx")

	found, err := dbml.ListDocuments(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.dbml", "b.DBML", "c.DbMl"}, found)
}

// Pruned rather than filtered: a `.dbml` under `node_modules` is a dependency's, not the user's,
// and reading all of it to throw the results away is the cost this avoids.
func TestPrunedDirectoriesAreNeverOffered(t *testing.T) {
	root := t.TempDir()

	touch(t, root, "mine.dbml")
	for _, pruned := range []string{"node_modules", ".git", "dist", "vendor", "__pycache__", "Pods"} {
		touch(t, root, pruned, "theirs.dbml")
		touch(t, root, pruned, "deeper", "also-theirs.dbml")
	}

	found, err := dbml.ListDocuments(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, []string{"mine.dbml"}, found)
}

// A directory **named like** a pruned one but nested deeper is still pruned; one merely containing
// the name is not.
func TestThePruneMatchesTheWholeDirectoryName(t *testing.T) {
	root := t.TempDir()

	touch(t, root, "my-node_modules", "kept.dbml")
	touch(t, root, "src", "node_modules", "dropped.dbml")

	found, err := dbml.ListDocuments(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, []string{"my-node_modules/kept.dbml"}, found)
}

// Depth alone would bound a loop, but a link pointing back up the tree reports the same file under
// two paths — and each path is a distinct layout key, so one table would have two positions and
// neither would be wrong.
func TestASymlinkedDirectoryIsSkippedRatherThanFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need an elevated process on Windows")
	}

	root := t.TempDir()
	touch(t, root, "real", "schema.dbml")
	require.NoError(t, os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")))

	found, err := dbml.ListDocuments(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, []string{"real/schema.dbml"}, found, "the document is reported once, under one path")
}

// A link that points at its own parent would loop for ever without this.
func TestALinkPointingBackUpTheTreeDoesNotLoop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need an elevated process on Windows")
	}

	root := t.TempDir()
	touch(t, root, "schema.dbml")
	require.NoError(t, os.Symlink(root, filepath.Join(root, "self")))

	found, err := dbml.ListDocuments(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, []string{"schema.dbml"}, found)
}

func TestAMissingFolderIsNamed(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-existe")

	_, err := dbml.ListDocuments(t.Context(), missing)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such folder")
	assert.Contains(t, err.Error(), "no-existe")

	_, err = dbml.ListDocuments(t.Context(), "")
	assert.Error(t, err)
}

// A file where a folder was asked for is the same refusal: there is nothing to walk.
func TestAFileInsteadOfAFolderIsRefused(t *testing.T) {
	root := t.TempDir()
	file := touch(t, root, "schema.dbml")

	_, err := dbml.ListDocuments(t.Context(), file)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such folder")
}

func TestAnEmptyFolderListsNothingRatherThanNull(t *testing.T) {
	found, err := dbml.ListDocuments(t.Context(), t.TempDir())
	require.NoError(t, err)

	// A nil slice marshals as `null` and the picker that maps over it crashes — while the same
	// folder with one document works.
	assert.NotNil(t, found)
	assert.Empty(t, found)
}

// A directory that cannot be read is skipped, so one unreadable folder does not cost the user every
// document in the tree.
func TestAnUnreadableDirectoryIsSkippedRatherThanFailing(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits do not stop this process")
	}

	root := t.TempDir()
	touch(t, root, "readable.dbml")
	touch(t, root, "locked", "hidden.dbml")

	locked := filepath.Join(root, "locked")
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o750) })

	found, err := dbml.ListDocuments(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, []string{"readable.dbml"}, found)
}

func TestTheWalkStopsWhenTheContextIsCancelled(t *testing.T) {
	root := t.TempDir()
	for i := range 50 {
		touch(t, root, "dir", string(rune('a'+i%26)), "schema.dbml")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := dbml.ListDocuments(ctx, root)
	assert.ErrorIs(t, err, context.Canceled)
}
