package git_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/git"
)

func TestCheckAttrsFlagsGeneratedAndVendoredFiles(t *testing.T) {
	repo := newTestRepo(t)
	repo.write(".gitattributes", "gen/** linguist-generated\nthird/** linguist-vendored=true\nthird/ours.go -linguist-vendored\nkeep.go linguist-generated=false\n")

	flagged, err := git.CheckAttrs(t.Context(), repo.Path,
		[]string{"linguist-generated", "linguist-vendored"},
		[]string{"gen/api.pb.go", "third/lib.go", "third/ours.go", "keep.go", "src/main.go", "dir with space/a b.go"})
	require.NoError(t, err)

	assert.Equal(t, map[string]bool{"gen/api.pb.go": true, "third/lib.go": true}, flagged)
}

func TestCheckAttrsAsksNothingForNoPaths(t *testing.T) {
	flagged, err := git.CheckAttrs(t.Context(), t.TempDir(), []string{"linguist-generated"}, nil)
	require.NoError(t, err)
	assert.Empty(t, flagged)
}
