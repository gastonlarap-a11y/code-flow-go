package git

import (
	"fmt"
	"strings"
)

// The code around each change (GIT-033).
//
// Why this exists, measured rather than assumed: bounding a review's toolset removed `Bash`, and
// the agent replaced it with nineteen `Read`s and seven `Grep`s across twelve files — six minutes,
// four of them spent reasoning over a context that grew with every one. It was not idle
// exploration. A diff trimmed to three lines either side does not show the method a changed line
// sits in, so the model went and opened it, once per file, guessing the range each time. The range
// is computed here instead: once, exactly, before the model is asked anything.
//
// It needs no filesystem access. Every diff in this application is produced with whole-file
// context, so every line of every changed file is already in the structure this is handed.

const (
	// ChangeContextBudgetChars is what the extract gets by default — smaller than the diff's on
	// purpose: the two overlap on the changed lines, and when something has to give, the half that
	// repeats gives first.
	ChangeContextBudgetChars = 150_000

	// maxBlockLines is where the indentation guess is treated as wrong. Past it the extract becomes
	// a window around the change instead of "most of the file".
	maxBlockLines = 400
	// windowLines is that window, either side.
	windowLines = 20
)

// RenderChangeContext quotes the declaration each change sits in, for every changed file
// (GIT-033).
//
// Added and deleted files are skipped: the diff already carries every line of those. So are the
// paths with no reviewable signal, for the same reason the diff skips them.
func RenderChangeContext(files []FileDiff, budgetChars int) string {
	if budgetChars <= 0 {
		budgetChars = ChangeContextBudgetChars
	}

	type extract struct {
		path string
		body string
	}
	extracts := make([]extract, 0, len(files))

	for _, file := range files {
		if file.Status == "added" || file.Status == "deleted" {
			continue
		}
		display := displayPath(file)
		if _, skip := SkipReason(display); skip {
			continue
		}
		if body := extractFileContext(file); body != "" {
			extracts = append(extracts, extract{path: display, body: body})
		}
	}
	if len(extracts) == 0 {
		return ""
	}

	costs := make([]int, len(extracts))
	for i, e := range extracts {
		costs[i] = len(e.body)
	}
	shares := shareBudget(costs, budgetChars)

	named := make([]string, 0, len(extracts))
	body := &strings.Builder{}
	for i, e := range extracts {
		switch {
		case shares[i] < minimumFileShare && costs[i] > shares[i]:
			named = append(named, e.path)
		case costs[i] <= shares[i]:
			body.WriteString(e.body)
		default:
			body.WriteString(cutOnALineBoundary(e.body, shares[i]))
		}
	}
	if body.Len() == 0 {
		return ""
	}

	out := &strings.Builder{}
	out.WriteString("CODE AROUND THE CHANGES\n")
	out.WriteString("(the block each change sits in, with `>` marking the changed lines)\n")
	for _, path := range named {
		fmt.Fprintf(out, "NOTE: omitted for space: %s\n", path)
	}
	out.WriteString("\n")
	out.WriteString(body.String())
	return out.String()
}

// newSideLine is one line of the file as it now reads, with whether the change touched it.
type newSideLine struct {
	number  int64
	content string
	changed bool
}

// extractFileContext quotes every block this file's changes sit in.
func extractFileContext(file FileDiff) string {
	lines := newSide(file)
	if len(lines) == 0 {
		return ""
	}

	blocks := make([][2]int, 0, 4)
	for i, line := range lines {
		if !line.changed {
			continue
		}
		if len(blocks) > 0 && i <= blocks[len(blocks)-1][1] {
			// Already inside the block the previous change opened.
			continue
		}
		blocks = append(blocks, blockAround(lines, i))
	}
	if len(blocks) == 0 {
		return ""
	}

	out := &strings.Builder{}
	fmt.Fprintf(out, "--- %s\n", displayPath(file))
	for _, block := range blocks {
		for i := block[0]; i <= block[1] && i < len(lines); i++ {
			marker := " "
			if lines[i].changed {
				marker = ">"
			}
			fmt.Fprintf(out, "%s %d: %s\n", marker, lines[i].number, lines[i].content)
		}
		out.WriteString("\n")
	}
	return out.String()
}

// newSide rebuilds the file as it now reads, marking the lines the change touched.
//
// A deletion has no line of its own on the new side, so it marks the line that took its place —
// and, at the very end of a file, the last line. Otherwise a deleted block would go unquoted, which
// is exactly the case a reviewer most needs to see the surroundings of.
func newSide(file FileDiff) []newSideLine {
	lines := make([]newSideLine, 0, 256)
	pendingDeletion := false

	for _, hunk := range file.Hunks {
		for _, line := range hunk.Lines {
			switch line.Origin {
			case "-":
				pendingDeletion = true
			default:
				if line.NewLineNo == nil {
					continue
				}
				lines = append(lines, newSideLine{
					number:  *line.NewLineNo,
					content: line.Content,
					changed: line.Origin == "+" || pendingDeletion,
				})
				pendingDeletion = false
			}
		}
	}

	if pendingDeletion && len(lines) > 0 {
		lines[len(lines)-1].changed = true
	}
	return lines
}

// blockAround finds the declaration a change sits in, by indentation rather than by a parser.
//
// Upwards to the first line indented less than the change — skipping a lone `{`, `(` or `[`, which
// is punctuation belonging to the line above it, so the block starts on the line that *names* it in
// both brace conventions and in the languages that have neither. Then the declaration's own doc
// comment, attributes or decorators when they sit at its indentation. Downwards while the
// indentation stays inside, taking the closing delimiter.
func blockAround(lines []newSideLine, at int) [2]int {
	indent := indentOf(lines[at].content)

	start := at
	for i := at - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i].content) == "" {
			continue
		}
		if isLoneOpener(lines[i].content) {
			continue
		}
		if indentOf(lines[i].content) < indent {
			start = i
			break
		}
		if i == 0 {
			start = 0
		}
	}

	// The declaration's own preamble: comments, attributes and decorators at its indentation.
	declarationIndent := indentOf(lines[start].content)
	for i := start - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i].content)
		if trimmed == "" || indentOf(lines[i].content) != declarationIndent {
			break
		}
		if !isPreamble(trimmed) {
			break
		}
		start = i
	}

	end := at
	for i := at + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i].content)
		if trimmed == "" {
			end = i
			continue
		}
		if indentOf(lines[i].content) > declarationIndent {
			end = i
			continue
		}
		// The closing delimiter belongs to the block it closes.
		if isCloser(trimmed) {
			end = i
		}
		break
	}

	// Past this many lines the indentation guess was wrong — typically a file with no structure it
	// can read — and "the whole file" becomes a wide window around the change instead.
	if end-start+1 > maxBlockLines {
		start = max(0, at-windowLines)
		end = min(len(lines)-1, at+windowLines)
	}
	return [2]int{start, end}
}

func indentOf(line string) int {
	for i, r := range line {
		if r != ' ' && r != '\t' {
			return i
		}
	}
	return len(line)
}

// isLoneOpener reports a line that is only an opening delimiter: punctuation belonging to the line
// above it, in the brace convention that puts it on its own.
func isLoneOpener(line string) bool {
	switch strings.TrimSpace(line) {
	case "{", "(", "[":
		return true
	}
	return false
}

func isCloser(trimmed string) bool {
	return strings.HasPrefix(trimmed, "}") || strings.HasPrefix(trimmed, ")") ||
		strings.HasPrefix(trimmed, "]") || trimmed == "end"
}

// isPreamble recognises what sits directly above a declaration and belongs to it.
func isPreamble(trimmed string) bool {
	for _, prefix := range []string{"//", "/*", "*", "#", "--", "@", "[", "///"} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}
