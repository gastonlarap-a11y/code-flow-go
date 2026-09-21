package docwalk_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/docwalk"
)

// touch creates a file and every directory above it.
func touch(t *testing.T, root string, parts ...string) {
	t.Helper()

	path := filepath.Join(append([]string{root}, parts...)...)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0o600))
}

// The behaviour the schema designer's own tests already pin is not repeated here. What is new in
// this package is the suffix match, which exists because an extension match cannot express
// `.diagram.json`, and that is what these assert.
func TestAMultiDotSuffixMatchesOnlyItsOwnDocuments(t *testing.T) {
	root := t.TempDir()

	touch(t, root, "checkout.diagram.json")
	touch(t, root, "flows", "onboarding.diagram.json")
	touch(t, root, "package.json")
	touch(t, root, "tsconfig.json")
	touch(t, root, "notes.diagram.txt")

	found, err := docwalk.List(t.Context(), root, ".diagram.json")
	require.NoError(t, err)

	assert.Equal(t, []string{"checkout.diagram.json", "flows/onboarding.diagram.json"}, found)
}

func TestTheMatchFoldsTheFileNameNotTheSuffix(t *testing.T) {
	root := t.TempDir()

	touch(t, root, "Orders.DBML")
	touch(t, root, "billing.dbml")

	found, err := docwalk.List(t.Context(), root, ".dbml")
	require.NoError(t, err)

	assert.Equal(t, []string{"Orders.DBML", "billing.dbml"}, found,
		"the name is folded for the comparison and kept as written in the answer")
}

func TestTheAnswerIsProjectRelativeAndSlashSeparated(t *testing.T) {
	root := t.TempDir()

	touch(t, root, "a", "b", "deep.diagram.json")

	found, err := docwalk.List(t.Context(), root, ".diagram.json")
	require.NoError(t, err)

	assert.Equal(t, []string{"a/b/deep.diagram.json"}, found)
}

func TestPrunedDirectoriesAreNeverDescendedInto(t *testing.T) {
	root := t.TempDir()

	touch(t, root, "kept.diagram.json")
	for _, pruned := range []string{"node_modules", ".git", "dist", "vendor", "DerivedData"} {
		touch(t, root, pruned, "hidden.diagram.json")
	}

	found, err := docwalk.List(t.Context(), root, ".diagram.json")
	require.NoError(t, err)

	assert.Equal(t, []string{"kept.diagram.json"}, found)
}

func TestTheListingIsBounded(t *testing.T) {
	root := t.TempDir()

	for i := range docwalk.MaxDocuments + 25 {
		touch(t, root, "d"+strconv.Itoa(i)+".diagram.json")
	}

	found, err := docwalk.List(t.Context(), root, ".diagram.json")
	require.NoError(t, err)

	assert.Len(t, found, docwalk.MaxDocuments)
}

func TestARootThatIsNotAFolderIsRefusedByName(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.diagram.json")
	require.NoError(t, os.WriteFile(file, []byte("{}"), 0o600))

	for name, root := range map[string]string{
		"missing":   filepath.Join(t.TempDir(), "nowhere"),
		"a file":    file,
		"empty":     "",
		"blank":     "   ",
		"not there": "/definitely/not/a/folder",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := docwalk.List(t.Context(), root, ".diagram.json")

			require.Error(t, err)
			assert.Contains(t, err.Error(), "no such folder: ")
		})
	}
}

// A cancelled context stops the walk rather than returning half a listing as if it were the whole
// one: a picker showing four of nine documents is worse than one showing an error.
func TestACancelledWalkAnswersTheCancellation(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "a.diagram.json")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := docwalk.List(ctx, root, ".diagram.json")

	require.ErrorIs(t, err, context.Canceled)
}
