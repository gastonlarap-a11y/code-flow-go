package ai_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
)

// The payload and the depth directive of a review (AI-023, AI-022). Both are contracts with the
// model rather than internal shapes: the payload is what it is asked to judge, and the directive is
// what tells it how deep to look.

func TestTheLevelDirectiveFallsBackToCompleto(t *testing.T) {
	completo := ai.ReviewLevelDirective("completo", true)

	for _, level := range []string{"", "   ", "COMPLETO", "medio", "no-such-level"} {
		t.Run(level, func(t *testing.T) {
			assert.Equal(t, completo, ai.ReviewLevelDirective(level, true),
				"an unrecognised level is the middle depth, never an error")
		})
	}
}

func TestEachLevelCarriesItsOwnHeader(t *testing.T) {
	// The headers are the prompts' own, accents included: the model echoes them and a reader looks
	// for them in a stored review.
	tests := map[string]string{
		"basico":   "## NIVEL DE REVISIÓN ACTIVO: básico",
		"básico":   "## NIVEL DE REVISIÓN ACTIVO: básico",
		"completo": "## NIVEL DE REVISIÓN ACTIVO: completo",
		"ultra":    "## NIVEL DE REVISIÓN ACTIVO: ultra",
	}

	for level, header := range tests {
		t.Run(level, func(t *testing.T) {
			assert.Contains(t, ai.ReviewLevelDirective(level, true), header,
				"the header is VERBATIM — the model echoes it and a reader looks for it")
		})
	}
}

// `ultra` without a checkout is a different block: the old one told the model to read the method
// around every change, while stdin told it there was nothing to read. The two instructions reached
// it through different channels of one invocation, so neither read as wrong on its own.
func TestUltraWithoutACheckoutDoesNotAskForCodeItCannotSee(t *testing.T) {
	explorable := ai.ReviewLevelDirective("ultra", true)
	linkReview := ai.ReviewLevelDirective("ultra", false)

	require.NotEqual(t, explorable, linkReview)
	assert.Contains(t, linkReview, "no checkout")
	assert.Contains(t, explorable, "CODE AROUND THE CHANGES")
}

func TestTheOtherLevelsIgnoreTheCheckoutFlag(t *testing.T) {
	for _, level := range []string{"basico", "completo"} {
		t.Run(level, func(t *testing.T) {
			assert.Equal(t, ai.ReviewLevelDirective(level, true), ai.ReviewLevelDirective(level, false))
		})
	}
}

// A review judges what it was given. Only `ultra` over a real checkout gets to look further, and a
// link review gets nothing at any level because there is nothing there to read.
func TestWhatAReviewIsAllowedToDo(t *testing.T) {
	tests := []struct {
		level      string
		explorable bool
		expected   []string
	}{
		{level: "basico", explorable: true, expected: []string{}},
		{level: "completo", explorable: true, expected: []string{}},
		{level: "ultra", explorable: true, expected: []string{"Read", "Grep", "Glob"}},
		{level: "ultra", explorable: false, expected: []string{}},
		{level: "completo", explorable: false, expected: []string{}},
	}

	for _, test := range tests {
		t.Run(test.level+"/"+map[bool]string{true: "checkout", false: "link"}[test.explorable], func(t *testing.T) {
			assert.Equal(t, test.expected, ai.ReviewTools(test.level, test.explorable))
		})
	}
}

// ---- the payload ------------------------------------------------------------------------------

func TestAReviewOfNothingIsRefusedBeforeTheModelIsInvoked(t *testing.T) {
	operations := ai.NewOperations(ai.NewRouter(nil), ai.NewRunRegistry(nil, 0), nil, nil)

	_, err := operations.Review(t.Context(), "run-1", ai.ReviewRequest{Diff: "   "})

	assert.ErrorIs(t, err, ai.ErrNothingToReview)
	assert.Equal(t, "This pull request has no changes to review", err.Error())
}

// reviewRun runs one review against the local stand-in and returns what actually reached the model,
// split where the engine splits it: the instructions, then the change to judge.
//
// They are asserted apart because the built-in methodology talks *about* the payload — it names
// `PROJECT REVIEW CONTEXT` and `CODE AROUND THE CHANGES` to tell the model what it will receive —
// so a search over the whole text finds those words whether or not they were actually sent.
func reviewRun(t *testing.T, request ai.ReviewRequest) (prompt, payload string) {
	t.Helper()
	operations, received := ollamaOperations(t, "## la revisión")

	_, err := operations.Review(t.Context(), "run-1", request)
	require.NoError(t, err)

	var decoded struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal([]byte(*received), &decoded))
	require.NotEmpty(t, decoded.Messages)

	sent := decoded.Messages[len(decoded.Messages)-1].Content
	prompt, payload, found := strings.Cut(sent, "----- INPUT -----")
	require.True(t, found, "the engine separates the instructions from the data")
	return prompt, payload
}

func TestThePayloadCarriesTheChangeAndWhatItLandsIn(t *testing.T) {
	_, payload := reviewRun(t, ai.ReviewRequest{
		Title:       "Add the thing",
		Description: "why it is needed",
		Contexts: []ai.ReviewContext{
			{Name: "Conventions", Content: "errors are wrapped"},
			{Name: "Empty", Content: "   "},
		},
		Diff:        "--- src/app.ts (modified)\n@@ -1,2 +1,2 @@\n-uno\n+dos\n",
		CodeContext: "CODE AROUND THE CHANGES\n--- src/app.ts\n> 1: dos\n",
		Level:       "completo",
		Explorable:  true,
	})

	assert.Contains(t, payload, "PR TITLE: Add the thing")
	assert.Contains(t, payload, "PR DESCRIPTION: why it is needed")
	assert.Contains(t, payload, "PROJECT REVIEW CONTEXT:\n- Conventions: errors are wrapped")
	assert.NotContains(t, payload, "- Empty:", "a blank context is not a line")
	assert.Contains(t, payload, "DIFF:\n--- src/app.ts (modified)")
	assert.Contains(t, payload, "CODE AROUND THE CHANGES")

	assert.Less(t, strings.Index(payload, "DIFF:"), strings.Index(payload, "CODE AROUND THE CHANGES"),
		"the code the change lands in rides after the change")
}

func TestABlankDescriptionSaysSo(t *testing.T) {
	_, payload := reviewRun(t, ai.ReviewRequest{
		Title: "t", Description: "  ", Diff: "diff", Level: "completo", Explorable: true,
	})

	assert.Contains(t, payload, "PR DESCRIPTION: (no description)")
}

func TestAReviewWithNoContextsCarriesNoContextBlock(t *testing.T) {
	_, payload := reviewRun(t, ai.ReviewRequest{
		Title: "t", Diff: "diff", Level: "completo", Explorable: true,
	})

	assert.NotContains(t, payload, "PROJECT REVIEW CONTEXT")
}

// A context is a free-text field somebody pastes into. Capped per context, and the cut says how
// much it left behind — an architecture document pasted into one used to enter the prompt whole.
func TestAnEnormousContextIsCappedAndSaysSo(t *testing.T) {
	_, payload := reviewRun(t, ai.ReviewRequest{
		Title: "t", Diff: "diff", Level: "completo", Explorable: true,
		Contexts: []ai.ReviewContext{{Name: "Architecture", Content: strings.Repeat("a", 45_000)}},
	})

	assert.Less(t, len(payload), 45_000, "the context was cut")
	assert.Contains(t, payload, "characters of this context were left out")
}

// The directive is appended after the methodology, so it overrides the depth the methodology
// implies — which is what makes one stored methodology usable at three depths.
func TestTheDirectiveIsAppendedAfterTheMethodology(t *testing.T) {
	prompt, _ := reviewRun(t, ai.ReviewRequest{
		Title: "t", Diff: "diff", Template: "MI METODOLOGÍA", Level: "basico", Explorable: true,
	})

	assert.True(t, strings.HasPrefix(prompt, "MI METODOLOGÍA\n\n"), prompt)
	assert.Contains(t, prompt, "## NIVEL DE REVISIÓN ACTIVO: básico")
}

// A workspace that stored no methodology gets the built-in one, never an empty prompt.
func TestABlankTemplateFallsBackToTheBuiltInMethodology(t *testing.T) {
	prompt, _ := reviewRun(t, ai.ReviewRequest{
		Title: "t", Diff: "diff", Template: "   ", Level: "completo", Explorable: true,
	})

	assert.Contains(t, prompt, "🚦 Quality Gate:", "the built-in standard is in there")
}
