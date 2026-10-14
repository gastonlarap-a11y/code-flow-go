package codemap

import (
	"errors"
	"fmt"
	"go/types"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Export data and source must map a module identically: which one is used depends only on whether
// this binary can read the user's toolchain's export format, and the map must not change with it.
func TestBothLoadModesMapTheSameModule(t *testing.T) {
	tools := DiscoverTools()
	if tools.Go == "" {
		t.Skip("no go command on this machine")
	}
	root, err := filepath.Abs(filepath.Join("testdata", "repo"))
	require.NoError(t, err)
	root, err = filepath.EvalSymlinks(root)
	require.NoError(t, err)
	lister := goLister{binary: tools.Go, path: tools.Path}

	mapWith := func(t *testing.T, mode loadMode) []string {
		t.Helper()
		listed, err := lister.list(t.Context(), root, mode == exportMode)
		require.NoError(t, err)
		packages, err := newGoLoader(listed, mode).checkModule(t.Context(), listed)
		if errors.Is(err, errExportFormat) {
			t.Skip("the go command's export data is newer than this test binary can read")
		}
		require.NoError(t, err)
		for _, pkg := range packages {
			require.Empty(t, pkg.Errors, pkg.ID)
		}
		half := newGoHalf()
		newGoIndexer(root, half).index(packages)

		out := make([]string, 0, len(half.nodes)+len(half.edges))
		for _, n := range half.nodes {
			out = append(out, "node "+n.ID+" "+n.Kind.String()+" "+n.Bound)
		}
		for _, e := range half.edges {
			out = append(out, fmt.Sprintf("edge %s %s %s", e.From, e.Kind, e.To))
		}
		slices.Sort(out)
		return slices.Compact(out)
	}

	var fromSource []string
	t.Run("source", func(t *testing.T) { fromSource = mapWith(t, sourceMode) })
	require.NotEmpty(t, fromSource)
	t.Run("export data", func(t *testing.T) {
		assert.Equal(t, fromSource, mapWith(t, exportMode))
	})
}

// The standard library's export data reader panics on a format it does not know. The panic must
// come back as errExportFormat — which makes the load start over from source — and never reach the
// caller.
func TestAnUnreadableExportFormatFallsBackRatherThanPanicking(t *testing.T) {
	loader := newGoLoader(nil, exportMode)
	loader.gc = importerFunc(func(string) (*types.Package, error) {
		panic("export data version 9 is greater than maximum supported version 5")
	})

	_, err := loader.readExport("example.com/dep")

	require.ErrorIs(t, err, errExportFormat)
	assert.ErrorIs(t, loader.formatErr, errExportFormat)
}
