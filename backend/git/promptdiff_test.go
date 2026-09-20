package git_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/git"
)

// The port of PromptDiffTests. What this replaced was a flatten — whole unchanged files
// concatenated and then cut at a fixed character count with no marker — so the properties that
// matter are: only what is near a change travels, what was dropped is **said**, and one huge file
// cannot starve twenty small ones.

// file builds a parsed diff of one file whose content is the given lines, with `changed` naming the
// 1-based line numbers the change touched.
func file(path string, lines []string, changed ...int) git.FileDiff {
	touched := make(map[int]bool, len(changed))
	for _, at := range changed {
		touched[at] = true
	}

	// Two counters, as git's own numbering has: an added line has no number on the old side, and
	// every old line after it keeps a number the new side has moved past.
	hunk := git.DiffHunk{Header: "@@", Lines: make([]git.DiffLine, 0, len(lines))}
	oldNo, newNo := int64(0), int64(0)
	for i, content := range lines {
		newNo++
		newLine := newNo

		if touched[i+1] {
			hunk.Lines = append(hunk.Lines, git.DiffLine{Origin: "+", Content: content, NewLineNo: &newLine})
			continue
		}

		oldNo++
		oldLine := oldNo
		hunk.Lines = append(hunk.Lines, git.DiffLine{
			Origin: " ", Content: content, OldLineNo: &oldLine, NewLineNo: &newLine,
		})
	}

	name := path
	return git.FileDiff{OldPath: &name, NewPath: &name, Status: "modified", Hunks: []git.DiffHunk{hunk}}
}

func numberedLines(count int, prefix string) []string {
	lines := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		lines = append(lines, fmt.Sprintf("%s %d", prefix, i))
	}
	return lines
}

// Only the change and three lines either side survive, and what went is declared.
func TestAPromptDiffKeepsThreeLinesEitherSideOfAChange(t *testing.T) {
	rendered := git.RenderForPrompt([]git.FileDiff{
		file("src/app.ts", numberedLines(40, "line"), 20),
	}, git.PromptBudgetChars)

	assert.Contains(t, rendered, "line 17", "three lines above")
	assert.Contains(t, rendered, "+line 20", "the change itself")
	assert.Contains(t, rendered, "line 23", "three lines below")
	assert.NotContains(t, rendered, "line 16", "and nothing further out")
	assert.NotContains(t, rendered, "line 24")
	assert.Contains(t, rendered, "~ 16 unchanged lines omitted", "what went is said, not dropped in silence")
	assert.Contains(t, rendered, "~ 17 unchanged lines omitted")
}

// Each surviving run carries its own anchor: with whole-file context the source hunk header
// describes the file rather than the change, and a finding that cites a line needs a line that
// exists.
func TestEachRunCarriesItsOwnAnchor(t *testing.T) {
	rendered := git.RenderForPrompt([]git.FileDiff{
		file("src/app.ts", numberedLines(60, "line"), 10, 50),
	}, git.PromptBudgetChars)

	assert.Contains(t, rendered, "@@ -7,6 +7,7 @@", "the first run's real line numbers")
	assert.Contains(t, rendered, "@@ -46,6 +47,7 @@", "the second run's, further down the file")
	assert.Equal(t, 2, strings.Count(rendered, "@@ -"), "one anchor per run")
}

// A one-line gap is shown rather than declared, and absorbed before anything is written — so the
// runs either side of it stay one run.
func TestASingleLineGapIsShownRatherThanDeclared(t *testing.T) {
	rendered := git.RenderForPrompt([]git.FileDiff{
		// Changes seven lines apart leave exactly one line between their context.
		file("src/app.ts", numberedLines(40, "line"), 10, 18),
	}, git.PromptBudgetChars)

	assert.Contains(t, rendered, "line 14", "the lone gap line is shown")
	assert.NotContains(t, rendered, "~ 1 unchanged lines omitted")
	assert.Equal(t, 1, strings.Count(rendered, "@@ -"), "one run, not two")
}

func TestATwoLineGapIsDeclared(t *testing.T) {
	rendered := git.RenderForPrompt([]git.FileDiff{
		file("src/app.ts", numberedLines(40, "line"), 10, 19),
	}, git.PromptBudgetChars)

	assert.Contains(t, rendered, "~ 2 unchanged lines omitted")
	assert.Equal(t, 2, strings.Count(rendered, "@@ -"))
}

// A file with no reviewable signal is excluded and named — never silently missing.
func TestFilesWithNoReviewableSignalAreExcludedAndNamed(t *testing.T) {
	tests := map[string]string{
		"pnpm-lock.yaml":                 "lock file",
		"go.sum":                         "lock file",
		"web/assets/app.min.js":          "generated",
		"web/assets/app.css.map":         "generated",
		"backend/api.pb.go":              "generated",
		"lib/model.freezed.dart":         "generated",
		"src/Schema.designer.cs":         "generated",
		"src/thing.generated.ts":         "generated",
		"node_modules/left-pad/index.js": "vendored",
		"vendor/github.com/x/y/z.go":     "vendored",
	}

	for path, reason := range tests {
		t.Run(path, func(t *testing.T) {
			got, skipped := git.SkipReason(path)
			assert.True(t, skipped)
			assert.Equal(t, reason, got)

			rendered := git.RenderForPrompt([]git.FileDiff{
				file(path, numberedLines(10, "x"), 5),
				file("src/real.ts", numberedLines(10, "y"), 5),
			}, git.PromptBudgetChars)

			assert.Contains(t, rendered, "NOTE: this diff is not complete.")
			assert.Contains(t, rendered, "excluded, no reviewable signal: "+path)
			assert.NotContains(t, rendered, "+x 5", "its content never travels")
			assert.Contains(t, rendered, "+y 5", "the real file still does")
		})
	}
}

// Deliberately not dist/build/out: git ignores those where they are generated, and where it does
// not they hold real source — this repository stages into `shell/build/` itself.
func TestBuildDirectoriesAreNotExcluded(t *testing.T) {
	for _, path := range []string{"shell/build/main.js", "dist/app.ts", "out/thing.go"} {
		t.Run(path, func(t *testing.T) {
			_, skipped := git.SkipReason(path)
			assert.False(t, skipped)
		})
	}
}

// A complete diff carries no note at all.
func TestACompleteDiffCarriesNoNote(t *testing.T) {
	rendered := git.RenderForPrompt([]git.FileDiff{file("src/app.ts", numberedLines(10, "line"), 5)},
		git.PromptBudgetChars)

	assert.NotContains(t, rendered, "NOTE:")
	assert.True(t, strings.HasPrefix(rendered, "--- src/app.ts (modified)"))
}

// The budget is shared cheapest-first: twenty small files survive one enormous one.
func TestTheBudgetIsSharedCheapestFirst(t *testing.T) {
	files := []git.FileDiff{file("src/huge.ts", numberedLines(4000, "a very long line of source code"), 2000)}
	for i := range 20 {
		files = append(files, file(fmt.Sprintf("src/small%d.ts", i), numberedLines(8, "small"), 4))
	}

	rendered, coverage := git.ShapeForPrompt(files, 20_000, nil)

	assert.Equal(t, 21, coverage.Touched)
	for i := range 20 {
		assert.Contains(t, rendered, fmt.Sprintf("src/small%d.ts", i), "a small file is not starved")
	}
	assert.LessOrEqual(t, len(rendered), 20_000+1_000, "the budget is respected, give or take the note")
}

// A file that is cut is cut on a line boundary and says so; one whose share is too small to be
// worth anything is named instead of half-shown.
func TestAFileIsEitherShownCutAndSaidOrNamed(t *testing.T) {
	// Every line changed, so trimming cannot shrink it and the budget is what decides.
	rewritten := make([]int, 0, 400)
	for i := 1; i <= 400; i++ {
		rewritten = append(rewritten, i)
	}
	big := file("src/big.ts", numberedLines(400, "a line of source that is reasonably long"), rewritten...)
	small := file("src/small.ts", numberedLines(6, "x"), 3)

	rendered, coverage := git.ShapeForPrompt([]git.FileDiff{big, small}, 3_000, nil)

	const marker = "~ cut for space, the rest of this file is not shown"
	assert.Contains(t, rendered, marker)
	assert.Equal(t, 1, coverage.Truncated)

	// The cut lands on a line boundary: a half-written line of source reads as a syntax error the
	// model then reports.
	at := strings.Index(rendered, marker)
	require.Positive(t, at)
	assert.Equal(t, byte('\n'), rendered[at-1])
	assert.Contains(t, rendered, "src/small.ts", "the file that fitted is still whole")
}

func TestAFileBelowItsMinimumShareIsNamedRatherThanHalfShown(t *testing.T) {
	files := make([]git.FileDiff, 0, 30)
	for i := range 30 {
		files = append(files, file(fmt.Sprintf("src/f%d.ts", i),
			numberedLines(200, "a line of source that is reasonably long"), 100))
	}

	rendered, coverage := git.ShapeForPrompt(files, 5_000, nil)

	require.NotEmpty(t, coverage.Omitted)
	assert.Contains(t, rendered, "omitted for space: "+coverage.Omitted[0])
}

// A file a previous review already covered is named rather than sent again.
func TestACarriedFileIsNamedNotResent(t *testing.T) {
	rendered, coverage := git.ShapeForPrompt([]git.FileDiff{
		file("src/reviewed.ts", numberedLines(10, "old"), 5),
		file("src/new.ts", numberedLines(10, "new"), 5),
	}, git.PromptBudgetChars, []string{"src/reviewed.ts"})

	assert.Contains(t, rendered, "unchanged since the previous review, already reviewed: src/reviewed.ts")
	assert.NotContains(t, rendered, "+old 5")
	assert.Contains(t, rendered, "+new 5")
	assert.Equal(t, []string{"src/reviewed.ts"}, coverage.Carried)
}

// An added file has no unchanged lines, so it is shown whole.
func TestAnAddedFileIsShownWhole(t *testing.T) {
	added := file("src/new.ts", numberedLines(12, "line"), 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12)
	added.Status = "added"

	rendered := git.RenderForPrompt([]git.FileDiff{added}, git.PromptBudgetChars)

	assert.NotContains(t, rendered, "omitted")
	assert.Contains(t, rendered, "+line 12")
}

// A binary file has no hunks, and its banner is still the signal that it changed.
func TestABinaryFileContributesItsBannerAlone(t *testing.T) {
	name := "assets/logo.png"
	binary := git.FileDiff{OldPath: &name, NewPath: &name, Status: "modified"}

	rendered := git.RenderForPrompt([]git.FileDiff{binary}, git.PromptBudgetChars)

	assert.Contains(t, rendered, "--- assets/logo.png (modified)")
}

func TestAnEmptyDiffRendersNothing(t *testing.T) {
	assert.Empty(t, git.RenderForPrompt(nil, git.PromptBudgetChars))
}

// ---- a provider's own diff text (a review reached by a pasted link) ---------------------------

// A link review has no clone, so the host hands the diff back as text. It takes the same road —
// this route once had no budget at all and the provider's diff went into the prompt whole.
func TestADiffThatArrivedAsTextIsReshapedToo(t *testing.T) {
	text := "diff --git a/src/app.ts b/src/app.ts\n--- a/src/app.ts\n+++ b/src/app.ts\n@@ -1,6 +1,6 @@\n" +
		" uno\n dos\n tres\n-cuatro\n+CUATRO\n cinco\n"

	rendered := git.RenderTextForPrompt(text, git.PromptBudgetChars)

	assert.Contains(t, rendered, "--- src/app.ts")
	assert.Contains(t, rendered, "+CUATRO")
	assert.Contains(t, rendered, "@@ -")
}

// A shape the parser does not recognise is truncated **and said**, never passed through: an
// unfamiliar format has to degrade to less content, not to no limit.
func TestAnUnrecognisedDiffShapeIsStillBounded(t *testing.T) {
	text := strings.Repeat("something that is not a unified diff at all\n", 500)

	rendered := git.RenderTextForPrompt(text, 1_000)

	assert.LessOrEqual(t, len(rendered), 1_000)
	assert.Contains(t, rendered, "~ cut for space")
}

func TestAnEmptyTextDiffRendersNothing(t *testing.T) {
	assert.Empty(t, git.RenderTextForPrompt("   ", git.PromptBudgetChars))
}
