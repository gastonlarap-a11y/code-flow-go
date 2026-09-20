package providers_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/providers"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/testvectors"
)

// The port of UnifiedPatchTests. The fixture's own note is load-bearing: the C# tests assert
// **containment**, not byte equality, because the renderer was libgit2's. This port is not
// libgit2 (`DIVERGENCE-PROV-e`), so containment is what is asserted here too.

type unifiedPatchInput struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

type unifiedPatchExpected struct {
	IsSome      bool     `json:"isSome"`
	ContainsAll []string `json:"containsAll"`
}

func TestUnifiedPatchMatchesTheExtractedVector(t *testing.T) {
	fixture, err := testvectors.LoadUnit("ado.vectors.json", "unified_patch")
	require.NoError(t, err)
	require.Len(t, fixture.Cases, 3, "three cases ship with the specification")

	for _, testCase := range fixture.Cases {
		t.Run(testCase.ID, func(t *testing.T) {
			var input unifiedPatchInput
			require.NoError(t, testvectors.Decode(testCase, testCase.Input, &input))
			var expected unifiedPatchExpected
			require.NoError(t, testvectors.Decode(testCase, testCase.Expected, &expected))

			patch, ok := providers.UnifiedPatch(input.Path, []byte(input.Old), []byte(input.New))
			require.Equal(t, expected.IsSome, ok)

			for _, fragment := range expected.ContainsAll {
				assert.Contains(t, patch, fragment, "the rendered patch must carry %q", fragment)
			}
		})
	}
}

func TestAModifiedLineRendersAsGitWritesIt(t *testing.T) {
	old := "uno\ndos\ntres\ncuatro\ncinco\nseis\nsiete\n"
	current := "uno\ndos\ntres\nCUATRO\ncinco\nseis\nsiete\n"

	patch, ok := providers.UnifiedPatch("src/app.ts", []byte(old), []byte(current))
	require.True(t, ok)

	// Byte for byte what `git diff --no-index` writes for the same two files, minus the file mode
	// git knows and a blob-to-blob patch does not.
	assert.Equal(t, strings.Join([]string{
		"diff --git a/src/app.ts b/src/app.ts",
		"index d9dbae2..7f075b1",
		"--- a/src/app.ts",
		"+++ b/src/app.ts",
		"@@ -1,7 +1,7 @@",
		" uno",
		" dos",
		" tres",
		"-cuatro",
		"+CUATRO",
		" cinco",
		" seis",
		" siete",
		"",
	}, "\n"), patch)
}

func TestAnAddedFileNamesDevNullOnTheSideItIsMissingFrom(t *testing.T) {
	patch, ok := providers.UnifiedPatch("nuevo.txt", nil, []byte("hola\n"))
	require.True(t, ok)

	assert.Contains(t, patch, "--- /dev/null")
	assert.Contains(t, patch, "+++ b/nuevo.txt")
	assert.Contains(t, patch, "@@ -0,0 +1 @@")
	assert.Contains(t, patch, "+hola")
}

func TestADeletedFileNamesDevNullOnTheOtherSide(t *testing.T) {
	patch, ok := providers.UnifiedPatch("viejo.txt", []byte("adios\n"), nil)
	require.True(t, ok)

	assert.Contains(t, patch, "--- a/viejo.txt")
	assert.Contains(t, patch, "+++ /dev/null")
	assert.Contains(t, patch, "@@ -1 +0,0 @@")
	assert.Contains(t, patch, "-adios")
}

// Two changes far apart are two hunks; two changes close together are one. The boundary is twice
// the context, and getting it wrong is invisible until a reviewer reads a hunk that claims lines it
// does not contain.
func TestChangesFarApartBecomeTwoHunks(t *testing.T) {
	old := make([]string, 0, 30)
	for i := range 30 {
		old = append(old, "linea "+string(rune('a'+i%26))+"\n")
	}
	current := append([]string{}, old...)
	current[2] = "CAMBIO uno\n"
	current[25] = "CAMBIO dos\n"

	patch, ok := providers.UnifiedPatch("f.txt", []byte(strings.Join(old, "")), []byte(strings.Join(current, "")))
	require.True(t, ok)
	assert.Equal(t, 2, strings.Count(patch, "@@ -"), "the two changes are 22 unchanged lines apart")

	// And adjacent ones stay together.
	adjacent := append([]string{}, old...)
	adjacent[2] = "CAMBIO uno\n"
	adjacent[6] = "CAMBIO dos\n"
	patch, ok = providers.UnifiedPatch("f.txt", []byte(strings.Join(old, "")), []byte(strings.Join(adjacent, "")))
	require.True(t, ok)
	assert.Equal(t, 1, strings.Count(patch, "@@ -"), "three unchanged lines between them is within the context")
}

func TestAFileWithNoTrailingNewlineSaysSo(t *testing.T) {
	patch, ok := providers.UnifiedPatch("f.txt", []byte("uno\ndos"), []byte("uno\nDOS"))
	require.True(t, ok)

	assert.Contains(t, patch, "-dos\n\\ No newline at end of file")
	assert.Contains(t, patch, "+DOS\n\\ No newline at end of file")
}

// A change that adds nothing but the final newline is still a change. It renders as git renders it,
// which is only possible because a line carries its own terminator through the diff.
func TestAddingTheFinalNewlineIsADifference(t *testing.T) {
	patch, ok := providers.UnifiedPatch("f.txt", []byte("a\nb"), []byte("a\nb\n"))
	require.True(t, ok)

	assert.Equal(t, strings.Join([]string{
		"@@ -1,2 +1,2 @@",
		" a",
		"-b",
		"\\ No newline at end of file",
		"+b",
		"",
	}, "\n"), patch[strings.Index(patch, "@@"):])
}

func TestBinaryContentIsRefusedRatherThanRendered(t *testing.T) {
	_, ok := providers.UnifiedPatch("logo.png", []byte("\x89PNG\x00\x01\x02"), []byte("\x89PNG\x00\x01\x03"))
	assert.False(t, ok, "the caller turns this into its own (binary) line")

	_, ok = providers.UnifiedPatch("logo.png", []byte("texto\n"), []byte("bytes\x00aqui\n"))
	assert.False(t, ok, "binary on either side is enough")
}

// Identical content still answers, because the caller only asks about files the host reported as
// changed: the header says "this file, nothing in its bytes".
func TestIdenticalContentRendersTheHeaderAndNoHunks(t *testing.T) {
	patch, ok := providers.UnifiedPatch("f.txt", []byte("igual\n"), []byte("igual\n"))
	require.True(t, ok)

	assert.Contains(t, patch, "diff --git a/f.txt b/f.txt")
	assert.NotContains(t, patch, "@@")
}

// The `index` line is git's real object id, so a patch from this renderer can be read next to one
// git wrote without the ids looking invented.
func TestTheIndexLineCarriesGitsOwnBlobIds(t *testing.T) {
	patch, ok := providers.UnifiedPatch("f.txt", nil, []byte("hola\n"))
	require.True(t, ok)

	// `printf '' | git hash-object --stdin` and `printf 'hola\n' | git hash-object --stdin`.
	assert.Contains(t, patch, "index e69de29..5c1b149")
}
