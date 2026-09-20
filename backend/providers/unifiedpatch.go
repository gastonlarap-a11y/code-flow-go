package providers

import (
	"crypto/sha1" //nolint:gosec // git's own object id format, not a security decision
	"fmt"
	"strings"
)

// contextLines is git's default: three lines either side of a change.
const contextLines = 3

// maxDiffCells bounds the table the line diff builds. Four million cells is a changed region of
// about 2 000 lines on both sides of one file — far past what anyone reads line by line. A region
// larger than that renders as one replacement instead: worse to read, but bounded, which is the
// trade a desktop app wants when a pull request contains a generated file.
const maxDiffCells = 4 << 20

// binarySniffBytes is how far in git looks for a NUL before calling content binary.
const binarySniffBytes = 8000

// UnifiedPatch renders one file's change as a git-style patch, from its two blobs (PROV-029).
//
// The same path is used on both sides: this never renders a rename, because the Azure change list
// it serves does not report one.
//
// Reports false — not an error — for content it will not diff, which is binary content. The caller
// turns that into the "(change, binary)" line 2.x produced, so a binary file in a pull request
// stays a listed change rather than a failed review.
//
// **`DIVERGENCE-PROV-e`**: 2.x rendered this through libgit2. There is no libgit2 here and no Go
// equivalent worth a dependency for one function, so the patch text is this file's own: git's
// format, git's hunk grouping, git's blob ids, and `/dev/null` for an absent side — which is what
// `git diff` writes and what every diff reader expects — but not guaranteed byte-identical to
// libgit2's output for the same input. The fixture asserts containment for exactly that reason
// (`test-vectors/ado.vectors.json`), and both consumers read diffs rather than apply them: a model,
// and the renderer's diff viewer.
func UnifiedPatch(path string, old, current []byte) (string, bool) {
	if isBinary(old) || isBinary(current) {
		return "", false
	}

	oldLines := splitPatchLines(old)
	newLines := splitPatchLines(current)

	hunks := buildHunks(diffLines(oldLines, newLines))
	patch := &strings.Builder{}
	patch.WriteString(patchHeader(path, old, current))

	// No hunks means the content is identical. The caller only asks about files the host reported
	// as changed, so the header alone is the answer: something changed, and it was not the bytes.
	for _, h := range hunks {
		writeHunk(patch, h, oldLines, newLines)
	}
	return patch.String(), true
}

// patchHeader is the four lines above the first hunk.
func patchHeader(path string, old, current []byte) string {
	header := &strings.Builder{}
	fmt.Fprintf(header, "diff --git a/%s b/%s\n", path, path)
	fmt.Fprintf(header, "index %s..%s\n", abbreviatedBlobID(old), abbreviatedBlobID(current))

	if len(old) == 0 {
		header.WriteString("--- /dev/null\n")
	} else {
		fmt.Fprintf(header, "--- a/%s\n", path)
	}
	if len(current) == 0 {
		header.WriteString("+++ /dev/null\n")
	} else {
		fmt.Fprintf(header, "+++ b/%s\n", path)
	}
	return header.String()
}

// abbreviatedBlobID is git's object id for the content, to seven characters.
//
// The same hash git computes — `sha1("blob <len>\0" + content)` — so the `index` line means what it
// means in a patch git wrote, and an absent side reads as the empty blob everyone recognises.
func abbreviatedBlobID(content []byte) string {
	hash := sha1.New() //nolint:gosec // git's object id format, fixed by git
	// Writing to a hash cannot fail — the interface says so — and there is nothing to report to if
	// it could: an id is being computed, not a file written.
	_, _ = fmt.Fprintf(hash, "blob %d\x00", len(content))
	_, _ = hash.Write(content)
	return fmt.Sprintf("%x", hash.Sum(nil))[:7]
}

// isBinary applies git's own rule: a NUL byte near the start means "do not diff this".
func isBinary(content []byte) bool {
	limit := min(len(content), binarySniffBytes)
	for i := range limit {
		if content[i] == 0 {
			return true
		}
	}
	return false
}

// splitPatchLines cuts content into lines that **keep their own terminator**.
//
// Carrying the `\n` rather than stripping it is what makes a missing final newline a difference the
// diff can see: `"b\n"` and `"b"` are two lines, and a change that only adds the newline at the end
// of a file renders as git renders it — the old line, the `\ No newline at end of file` marker, and
// the new line — instead of as no change at all.
//
// A carriage return stays part of the line's content too: a CRLF file diffed as if it were LF would
// show every line as changed.
func splitPatchLines(content []byte) []string {
	if len(content) == 0 {
		return nil
	}

	lines := strings.SplitAfter(string(content), "\n")
	// SplitAfter leaves an empty final element when the content ends with a newline.
	if last := len(lines) - 1; lines[last] == "" {
		lines = lines[:last]
	}
	return lines
}

type patchOpKind uint8

const (
	opEqual patchOpKind = iota
	opDelete
	opInsert
)

// patchOp is one line of the edit script.
//
// Both indices travel on every op, because a hunk header needs positions on **both** sides and
// reconstructing the missing one afterwards is where this kind of code goes wrong. For an insert,
// oldIndex is the number of old lines consumed before it — which is exactly the line number git
// prints in a zero-count range. Symmetrically for a delete's newIndex.
type patchOp struct {
	kind     patchOpKind
	oldIndex int
	newIndex int
}

// diffLines is the edit script between two line slices.
//
// Common prefix and suffix are trimmed first — the cheap step that makes the quadratic part of a
// real source-file diff tiny — and the middle is matched by longest common subsequence.
func diffLines(oldLines, newLines []string) []patchOp {
	script := make([]patchOp, 0, len(oldLines)+len(newLines))

	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix &&
		oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}

	for i := range prefix {
		script = append(script, patchOp{kind: opEqual, oldIndex: i, newIndex: i})
	}

	oldMid := oldLines[prefix : len(oldLines)-suffix]
	newMid := newLines[prefix : len(newLines)-suffix]
	script = append(script, diffMiddle(oldMid, newMid, prefix)...)

	for i := range suffix {
		script = append(script, patchOp{
			kind:     opEqual,
			oldIndex: len(oldLines) - suffix + i,
			newIndex: len(newLines) - suffix + i,
		})
	}
	return script
}

// diffMiddle matches the part left after trimming, offsetting every index by where it started.
func diffMiddle(oldMid, newMid []string, offset int) []patchOp {
	rows, columns := len(oldMid), len(newMid)
	script := make([]patchOp, 0, rows+columns)

	// One side empty, or a region too large to match line by line: delete everything, then insert
	// everything. For the first two that is the minimal script; for the third it is the bound.
	if rows == 0 || columns == 0 || (rows+1)*(columns+1) > maxDiffCells {
		for i := range rows {
			script = append(script, patchOp{kind: opDelete, oldIndex: offset + i, newIndex: offset})
		}
		for j := range columns {
			script = append(script, patchOp{kind: opInsert, oldIndex: offset + rows, newIndex: offset + j})
		}
		return script
	}

	// lcs[i][j] is the length of the longest common subsequence of oldMid[i:] and newMid[j:], as
	// one flat slice: the table is walked forward afterwards, so it has to be complete.
	stride := columns + 1
	lcs := make([]uint32, (rows+1)*stride)
	for i := rows - 1; i >= 0; i-- {
		for j := columns - 1; j >= 0; j-- {
			if oldMid[i] == newMid[j] {
				lcs[i*stride+j] = lcs[(i+1)*stride+j+1] + 1
				continue
			}
			lcs[i*stride+j] = max(lcs[(i+1)*stride+j], lcs[i*stride+j+1])
		}
	}

	i, j := 0, 0
	for i < rows && j < columns {
		switch {
		case oldMid[i] == newMid[j]:
			script = append(script, patchOp{kind: opEqual, oldIndex: offset + i, newIndex: offset + j})
			i, j = i+1, j+1
		case lcs[(i+1)*stride+j] >= lcs[i*stride+j+1]:
			// Deletions before insertions when both give a minimal script, which is how git shows
			// a replaced line: the old text first, the new text under it.
			script = append(script, patchOp{kind: opDelete, oldIndex: offset + i, newIndex: offset + j})
			i++
		default:
			script = append(script, patchOp{kind: opInsert, oldIndex: offset + i, newIndex: offset + j})
			j++
		}
	}
	for ; i < rows; i++ {
		script = append(script, patchOp{kind: opDelete, oldIndex: offset + i, newIndex: offset + j})
	}
	for ; j < columns; j++ {
		script = append(script, patchOp{kind: opInsert, oldIndex: offset + i, newIndex: offset + j})
	}
	return script
}

// hunk is one contiguous block of the patch.
type hunk struct {
	ops []patchOp
}

// buildHunks groups the edit script into hunks with three lines of context, splitting only where a
// run of unchanged lines is longer than twice the context — git's own grouping, so what reads as
// one change stays one block.
func buildHunks(script []patchOp) []hunk {
	hunks := make([]hunk, 0, 4)
	current := make([]patchOp, 0, 16)
	pendingEqual := make([]patchOp, 0, 8)
	started := false

	for _, op := range script {
		if op.kind == opEqual {
			pendingEqual = append(pendingEqual, op)
			continue
		}

		switch {
		case !started:
			current = append(current, tailOps(pendingEqual, contextLines)...)
			started = true
		case len(pendingEqual) > 2*contextLines:
			current = append(current, headOps(pendingEqual, contextLines)...)
			hunks = append(hunks, hunk{ops: current})
			current = make([]patchOp, 0, 16)
			current = append(current, tailOps(pendingEqual, contextLines)...)
		default:
			current = append(current, pendingEqual...)
		}
		pendingEqual = pendingEqual[:0]
		current = append(current, op)
	}

	if started {
		current = append(current, headOps(pendingEqual, contextLines)...)
		hunks = append(hunks, hunk{ops: current})
	}
	return hunks
}

func headOps(ops []patchOp, n int) []patchOp {
	if len(ops) <= n {
		return ops
	}
	return ops[:n]
}

func tailOps(ops []patchOp, n int) []patchOp {
	if len(ops) <= n {
		return ops
	}
	return ops[len(ops)-n:]
}

// writeHunk renders one hunk: its header, then one line per op.
func writeHunk(out *strings.Builder, h hunk, oldLines, newLines []string) {
	oldStart, oldCount := h.ops[0].oldIndex, 0
	newStart, newCount := h.ops[0].newIndex, 0
	for _, op := range h.ops {
		if op.kind != opInsert {
			oldCount++
		}
		if op.kind != opDelete {
			newCount++
		}
	}

	fmt.Fprintf(out, "@@ -%s +%s @@\n", rangeText(oldStart, oldCount), rangeText(newStart, newCount))

	for _, op := range h.ops {
		switch op.kind {
		case opEqual:
			writePatchLine(out, " ", oldLines[op.oldIndex])
		case opDelete:
			writePatchLine(out, "-", oldLines[op.oldIndex])
		case opInsert:
			writePatchLine(out, "+", newLines[op.newIndex])
		}
	}
}

// writePatchLine writes one line under its marker, and says so when the line it came from had no
// terminator — the last line of a file that does not end with a newline.
func writePatchLine(out *strings.Builder, marker, line string) {
	if terminated := strings.HasSuffix(line, "\n"); terminated {
		out.WriteString(marker + line)
		return
	}
	out.WriteString(marker + line + "\n")
	out.WriteString("\\ No newline at end of file\n")
}

// rangeText is git's `{start},{count}`: one-based, the count omitted when it is one, and a zero
// count keeping the preceding line's number so an insertion says where it goes.
func rangeText(start, count int) string {
	switch count {
	case 0:
		return fmt.Sprintf("%d,0", start)
	case 1:
		return fmt.Sprintf("%d", start+1)
	default:
		return fmt.Sprintf("%d,%d", start+1, count)
	}
}
