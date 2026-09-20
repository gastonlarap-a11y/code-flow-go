package git_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/git"
)

// The port of ChangeContextTests. This exists because a diff trimmed to three lines either side
// does not show the method a changed line sits in, so the model went and opened it — nineteen
// `Read`s and seven `Grep`s across twelve files, six minutes, four of them spent reasoning over a
// context that grew with every one.

const goSource = `package main

import "fmt"

// Greet says hello to somebody.
func Greet(name string) string {
	if name == "" {
		name = "world"
	}
	return fmt.Sprintf("hola %s", name)
}

func Unrelated() int {
	return 42
}
`

func sourceLines(source string) []string {
	lines := strings.Split(strings.TrimSuffix(source, "\n"), "\n")
	return lines
}

// The block a change sits in is quoted whole, with the changed lines marked and real line numbers.
func TestTheDeclarationAChangeSitsInIsQuoted(t *testing.T) {
	lines := sourceLines(goSource)
	changed := indexOfLine(t, lines, `	return fmt.Sprintf("hola %s", name)`)

	rendered := git.RenderChangeContext([]git.FileDiff{
		file("main.go", lines, changed),
	}, git.ChangeContextBudgetChars)

	assert.Contains(t, rendered, "CODE AROUND THE CHANGES")
	assert.Contains(t, rendered, "--- main.go")
	assert.Contains(t, rendered, "func Greet(name string) string {", "the declaration the change sits in")
	assert.Contains(t, rendered, "// Greet says hello to somebody.", "and its own doc comment")
	assert.NotContains(t, rendered, "func Unrelated", "a declaration nothing touched is not quoted")

	// The changed line is marked, and the numbers are the file's own.
	assert.Regexp(t, `>\s+\d+: \treturn fmt\.Sprintf`, rendered)
	assert.Regexp(t, `\s+\d+: func Greet`, rendered)
}

// The block is the **innermost** one containing the change — the first line indented less than it,
// which for a statement nested inside an `if` is that `if` rather than the method around it. The
// rule is mechanical on purpose: an indentation walk has no language to be wrong about, and the
// diff travels alongside this for exactly the parts it does not reach.
func TestANestedChangeQuotesTheBlockThatHoldsIt(t *testing.T) {
	lines := sourceLines(goSource)
	changed := indexOfLine(t, lines, `		name = "world"`)

	rendered := git.RenderChangeContext([]git.FileDiff{file("main.go", lines, changed)},
		git.ChangeContextBudgetChars)

	assert.Contains(t, rendered, `if name == "" {`)
	assert.Regexp(t, `>\s+\d+: \t\tname = "world"`, rendered)
}

func indexOfLine(t *testing.T, lines []string, want string) int {
	t.Helper()
	for i, line := range lines {
		if line == want {
			return i + 1
		}
	}
	t.Fatalf("no line %q in the fixture", want)
	return 0
}

// A change no block contains — an import, a top-level constant — is quoted on its own. Nothing the
// pull request touched goes unquoted.
func TestAChangeNoBlockContainsIsStillQuoted(t *testing.T) {
	lines := sourceLines(goSource)
	changed := indexOfLine(t, lines, `import "fmt"`)

	rendered := git.RenderChangeContext([]git.FileDiff{file("main.go", lines, changed)},
		git.ChangeContextBudgetChars)

	assert.Contains(t, rendered, `import "fmt"`)
	assert.Regexp(t, `>\s+\d+: import "fmt"`, rendered)
}

// A deletion has no line of its own on the new side, so it marks the line that took its place —
// otherwise a deleted block would go unquoted, which is the case a reviewer most needs to see.
func TestADeletionMarksTheLineThatTookItsPlace(t *testing.T) {
	name := "main.go"
	deleted := int64(0)
	kept := int64(0)

	hunk := git.DiffHunk{Header: "@@"}
	add := func(origin, content string) {
		line := git.DiffLine{Origin: origin, Content: content}
		if origin != "+" {
			deleted++
			old := deleted
			line.OldLineNo = &old
		}
		if origin != "-" {
			kept++
			newNo := kept
			line.NewLineNo = &newNo
		}
		hunk.Lines = append(hunk.Lines, line)
	}
	add(" ", "func Greet() string {")
	add("-", `	old := "gone"`)
	add(" ", `	return "hola"`)
	add(" ", "}")

	rendered := git.RenderChangeContext([]git.FileDiff{{
		OldPath: &name, NewPath: &name, Status: "modified", Hunks: []git.DiffHunk{hunk},
	}}, git.ChangeContextBudgetChars)

	assert.Regexp(t, `>\s+2: \treturn "hola"`, rendered, "the line that took the deletion's place")
}

func TestADeletionAtTheEndOfAFileMarksTheLastLine(t *testing.T) {
	name := "main.go"
	first, second := int64(1), int64(2)
	oldThird := int64(3)

	hunk := git.DiffHunk{Lines: []git.DiffLine{
		{Origin: " ", Content: "func Greet() string {", OldLineNo: &first, NewLineNo: &first},
		{Origin: " ", Content: `	return "hola"`, OldLineNo: &second, NewLineNo: &second},
		{Origin: "-", Content: `	unreachable()`, OldLineNo: &oldThird},
	}}

	rendered := git.RenderChangeContext([]git.FileDiff{{
		OldPath: &name, NewPath: &name, Status: "modified", Hunks: []git.DiffHunk{hunk},
	}}, git.ChangeContextBudgetChars)

	assert.Regexp(t, `>\s+2: \treturn "hola"`, rendered)
}

// Added and deleted files are skipped: the diff already carries every line of those.
func TestAddedAndDeletedFilesAreSkipped(t *testing.T) {
	for _, status := range []string{"added", "deleted"} {
		t.Run(status, func(t *testing.T) {
			whole := file("src/new.go", sourceLines(goSource), 1)
			whole.Status = status

			assert.Empty(t, git.RenderChangeContext([]git.FileDiff{whole}, git.ChangeContextBudgetChars))
		})
	}
}

// The same paths the diff excludes are excluded here, for the same reasons.
func TestPathsWithNoReviewableSignalAreSkippedHereToo(t *testing.T) {
	rendered := git.RenderChangeContext([]git.FileDiff{
		file("pnpm-lock.yaml", sourceLines(goSource), 3),
	}, git.ChangeContextBudgetChars)

	assert.Empty(t, rendered)
}

// Past 400 lines the indentation guess was wrong — typically a file with no structure it can read —
// and "the whole file" becomes a wide window around the change instead.
func TestAnEnormousBlockBecomesAWindow(t *testing.T) {
	lines := make([]string, 0, 600)
	lines = append(lines, "func Enormous() {")
	for i := range 598 {
		lines = append(lines, fmt.Sprintf("\tstep%d()", i))
	}
	lines = append(lines, "}")

	rendered := git.RenderChangeContext([]git.FileDiff{file("big.go", lines, 300)},
		git.ChangeContextBudgetChars)

	quoted := strings.Count(rendered, ": ")
	assert.LessOrEqual(t, quoted, 2*20+2, "a ±20-line window, not six hundred lines")
	assert.Contains(t, rendered, "step299()", "the change itself is in it")
}

// A file whose share is too small is named in the preamble rather than half-shown — beside the
// files that did fit.
func TestAFileBelowItsShareIsNamedInThePreamble(t *testing.T) {
	files := []git.FileDiff{file("src/small.go", []string{"func Tiny() {", "\tdoIt()", "}"}, 2)}
	for i := range 20 {
		body := make([]string, 0, 60)
		body = append(body, fmt.Sprintf("func Thing%d() {", i))
		for j := range 58 {
			body = append(body, fmt.Sprintf("\tline %d of a reasonably long piece of source", j))
		}
		body = append(body, "}")
		files = append(files, file(fmt.Sprintf("src/f%d.go", i), body, 30))
	}

	rendered := git.RenderChangeContext(files, 6_000)

	assert.Contains(t, rendered, "NOTE: omitted for space: ")
	assert.Contains(t, rendered, "src/small.go", "the one that fitted is still quoted")
}

func TestNothingToQuoteRendersNothing(t *testing.T) {
	assert.Empty(t, git.RenderChangeContext(nil, git.ChangeContextBudgetChars))

	unchanged := file("src/app.go", sourceLines(goSource))
	assert.Empty(t, git.RenderChangeContext([]git.FileDiff{unchanged}, git.ChangeContextBudgetChars),
		"a file in the diff with no changed line has nothing to quote")
}

// Two changes inside one declaration are quoted once, not twice.
func TestTwoChangesInOneBlockAreQuotedOnce(t *testing.T) {
	lines := sourceLines(goSource)
	first := indexOfLine(t, lines, `	if name == "" {`)
	second := indexOfLine(t, lines, `	return fmt.Sprintf("hola %s", name)`)

	rendered := git.RenderChangeContext([]git.FileDiff{file("main.go", lines, first, second)},
		git.ChangeContextBudgetChars)

	require.Contains(t, rendered, "func Greet")
	assert.Equal(t, 1, strings.Count(rendered, "func Greet"), "one quotation, two marked lines")
	assert.Equal(t, 2, strings.Count(rendered, "\n> "), "both changes are marked")
}
