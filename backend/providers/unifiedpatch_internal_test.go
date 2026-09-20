package providers

import (
	"math/rand/v2"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The edit script is the part of the patch renderer that can be wrong without looking wrong: a
// patch with a plausible shape and a line in the wrong place reads as a reviewer's mistake, not as
// a bug. Two properties pin it, over generated inputs rather than examples:
//
//   - replaying the script reproduces both sides exactly;
//   - it keeps as many lines as the longest common subsequence has, computed here by a separate,
//     deliberately naive implementation — so a minimal script is proved against something other
//     than itself.
func TestTheEditScriptIsValidAndMinimal(t *testing.T) {
	// Fixed seed: a property test that changes what it tests between runs reports failures nobody
	// can reproduce.
	random := rand.New(rand.NewPCG(20260918, 5))

	for round := range 300 {
		oldLines := randomLines(random, 0, 12)
		newLines := mutate(random, oldLines)

		t.Run("round-"+strconv.Itoa(round), func(t *testing.T) {
			script := diffLines(oldLines, newLines)

			replayedOld := make([]string, 0, len(oldLines))
			replayedNew := make([]string, 0, len(newLines))
			kept := 0
			for _, op := range script {
				switch op.kind {
				case opEqual:
					require.Equal(t, oldLines[op.oldIndex], newLines[op.newIndex],
						"an equal op must name the same text on both sides")
					replayedOld = append(replayedOld, oldLines[op.oldIndex])
					replayedNew = append(replayedNew, newLines[op.newIndex])
					kept++
				case opDelete:
					replayedOld = append(replayedOld, oldLines[op.oldIndex])
				case opInsert:
					replayedNew = append(replayedNew, newLines[op.newIndex])
				}
			}

			assert.Equal(t, oldLines, replayedOld, "the deletes and equals are the old side")
			assert.Equal(t, newLines, replayedNew, "the inserts and equals are the new side")
			assert.Equal(t, naiveLCSLength(oldLines, newLines), kept, "the script is not minimal")
		})
	}
}

// Indices must also be monotonic, because the hunk header is derived from the first op's pair: a
// script that walked backwards would produce a header claiming lines the hunk does not hold.
func TestTheScriptWalksBothSidesForward(t *testing.T) {
	script := diffLines(
		[]string{"a", "b", "c", "d", "e"},
		[]string{"a", "x", "c", "d", "y", "e"},
	)

	previousOld, previousNew := -1, -1
	for _, op := range script {
		assert.GreaterOrEqual(t, op.oldIndex, previousOld)
		assert.GreaterOrEqual(t, op.newIndex, previousNew)
		previousOld, previousNew = op.oldIndex, op.newIndex
	}
}

// Past the cell budget the script stops being minimal on purpose: everything is deleted and
// everything re-inserted. The bound is what keeps one generated file from stalling a review.
func TestAnOversizedRegionFallsBackToAWholeReplacement(t *testing.T) {
	const lines = 2100 // (2101 * 2101) is past maxDiffCells

	oldLines := make([]string, 0, lines)
	newLines := make([]string, 0, lines)
	for i := range lines {
		oldLines = append(oldLines, "vieja "+strconv.Itoa(i))
		newLines = append(newLines, "nueva "+strconv.Itoa(i))
	}

	script := diffLines(oldLines, newLines)
	require.Len(t, script, 2*lines)
	for i, op := range script {
		if i < lines {
			assert.Equal(t, opDelete, op.kind)
		} else {
			assert.Equal(t, opInsert, op.kind)
		}
	}
}

// A region that only *looks* large because the file is long still diffs line by line: the trimming
// of a common prefix and suffix is what keeps the budget from being reached by a one-line change in
// a ten-thousand-line file.
func TestALongFileWithOneChangedLineIsStillDiffedPrecisely(t *testing.T) {
	const lines = 10_000

	oldLines := make([]string, 0, lines)
	for i := range lines {
		oldLines = append(oldLines, "linea "+strconv.Itoa(i))
	}
	newLines := append([]string{}, oldLines...)
	newLines[5_000] = "CAMBIADA"

	script := diffLines(oldLines, newLines)

	changes := 0
	for _, op := range script {
		if op.kind != opEqual {
			changes++
		}
	}
	assert.Equal(t, 2, changes, "one delete and one insert, not two rewritten files")
}

// randomLines builds terminated lines, the shape splitPatchLines produces.
func randomLines(random *rand.Rand, minimum, maximum int) []string {
	count := minimum + random.IntN(maximum-minimum+1)
	lines := make([]string, 0, count)
	for range count {
		lines = append(lines, randomLine(random))
	}
	return lines
}

// A small alphabet on purpose: repeated lines are where a diff's tie-breaking shows.
func randomLine(random *rand.Rand) string {
	return string(rune('a'+random.IntN(4))) + "\n"
}

// mutate applies a handful of random edits, which is what a real diff input looks like.
func mutate(random *rand.Rand, lines []string) []string {
	out := append([]string{}, lines...)

	for range random.IntN(5) {
		switch random.IntN(3) {
		case 0: // insert
			at := random.IntN(len(out) + 1)
			out = append(out[:at], append([]string{randomLine(random)}, out[at:]...)...)
		case 1: // delete
			if len(out) == 0 {
				continue
			}
			at := random.IntN(len(out))
			out = append(out[:at], out[at+1:]...)
		default: // replace
			if len(out) == 0 {
				continue
			}
			out[random.IntN(len(out))] = randomLine(random)
		}
	}
	return out
}

// naiveLCSLength is the textbook table, written separately from the renderer's own so the two
// cannot be wrong in the same way.
func naiveLCSLength(a, b []string) int {
	table := make([][]int, len(a)+1)
	for i := range table {
		table[i] = make([]int, len(b)+1)
	}

	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				table[i][j] = table[i-1][j-1] + 1
				continue
			}
			table[i][j] = max(table[i-1][j], table[i][j-1])
		}
	}
	return table[len(a)][len(b)]
}
