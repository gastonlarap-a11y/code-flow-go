package ai_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
)

// `WI-023` and `WI-024`: the two axes a review of local changes has, seen from what actually reaches
// the model.

// ticketReviewRun runs one ticket review against the local stand-in and returns what was sent, split
// where the engine splits it: the methodology, then the thing to judge.
//
// Split, and not searched whole, for the reason the pull-request review's own helper gives: the
// built-in methodology talks *about* the payload — it names `TICKET:` and `CRITERIA MODE:` to tell
// the model what it will receive — so a search over the whole text finds those labels whether or not
// they were sent.
func ticketReviewRun(t *testing.T, request ai.TicketReviewRequest) (prompt, payload string) {
	t.Helper()
	operations, received := ollamaOperations(t, "## la revisión")

	_, err := operations.ReviewAgainstTicket(t.Context(), "run-1", request)
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

func analyzeRun(t *testing.T, request ai.AnalyzeRequest) (prompt, payload string) {
	t.Helper()
	operations, received := ollamaOperations(t, "## el análisis")

	_, err := operations.AnalyzeChanges(t.Context(), "run-1", request)
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

func TestAnAnalysisOfNothingIsRefusedBeforeTheModelIsInvoked(t *testing.T) {
	operations := ai.NewOperations(ai.NewRouter(nil), ai.NewRunRegistry(nil, 0), nil, nil)

	_, analyzeErr := operations.AnalyzeChanges(t.Context(), "run-1", ai.AnalyzeRequest{Diff: "   "})
	assert.ErrorIs(t, analyzeErr, ai.ErrNothingToAnalyze)
	// VERBATIM: the sentinel goes in front of exactly this sentence at the command boundary.
	assert.Equal(t, "No hay cambios sin commitear para analizar", analyzeErr.Error())

	_, ticketErr := operations.ReviewAgainstTicket(t.Context(), "run-1", ai.TicketReviewRequest{Diff: ""})
	assert.ErrorIs(t, ticketErr, ai.ErrNothingToAnalyze)
}

func TestScopeLineNamesWhichDiffTheModelIsLookingAt(t *testing.T) {
	working := ai.ScopeLine(ai.ScopeWorking, "main")
	branch := ai.ScopeLine(ai.ScopeBranch, "develop")

	assert.True(t, strings.HasPrefix(working, "SCOPE: "))
	assert.True(t, strings.HasPrefix(branch, "SCOPE: "))

	assert.Contains(t, working, "sin commitear")
	assert.Contains(t, working, "NO están en este diff",
		"the model must not assume the earlier commits are there")

	assert.Contains(t, branch, "contribución completa")
	assert.Contains(t, branch, "`develop`", "the base branch is named, since the user chose it")
	assert.Contains(t, branch, "commits ya hechos")

	// An unrecognised scope reads as the working tree, which is the narrower claim: telling the
	// model it has the whole branch when it does not is the mistake that produces wrong verdicts.
	assert.Equal(t, working, ai.ScopeLine("", "main"))
	assert.Equal(t, working, ai.ScopeLine("nonsense", "main"))
}

// `WI-024`: the caveat is present for one scope and absent for the other.
func TestOnlyTheWorkingScopeCarriesTheCriteriaCaveat(t *testing.T) {
	caveat := ai.CriteriaCaveat(ai.ScopeWorking)

	assert.NotEmpty(t, caveat)
	assert.Contains(t, caveat, "ausencia de evidencia NO es evidencia de ausencia")
	assert.Contains(t, caveat, "`no verificable`")
	assert.Contains(t, caveat, "Nunca respondas")

	// A branch scope has the evidence, so the caveat would be telling the model to doubt something
	// it can see.
	assert.Empty(t, ai.CriteriaCaveat(ai.ScopeBranch))
}

// `WI-017`: the ticket block has its own budget and the criteria have none.
func TestTheTicketBlockIsCappedAndTheCriteriaAreNot(t *testing.T) {
	// Before this, the diff spent a deliberate budget and the ticket was concatenated after it with
	// no ceiling at all — a work item with a long refinement thread would have starved the branch's
	// own contribution out of the prompt.
	block := ai.TicketBlock{
		ExternalID:   "1234",
		WorkItemType: "User Story",
		Title:        "Exportar",
		State:        "Active",
		// Three markers that appear nowhere in the labels, the methodology or the Spanish scope
		// line, so counting them counts only what this block contributed.
		Description:  strings.Repeat("~", 60_000),
		CriteriaMode: "list",
		// Longer than the description's cap, and every character of it has to survive: truncating
		// the criteria turns "the model did not check AC-7" into a finding about the work rather
		// than about the prompt.
		CriteriaMarkdown: strings.Repeat("^", 60_000),
		Notes:            strings.Repeat("¤", 40_000),
	}

	_, payload := ticketReviewRun(t, ai.TicketReviewRequest{
		Ticket: block, Diff: "diff --git a b", Scope: ai.ScopeBranch, BaseRef: "main",
	})

	assert.Equal(t, 40_000, strings.Count(payload, "~"), "the description is capped at 40 000")
	assert.Equal(t, 60_000, strings.Count(payload, "^"), "the criteria are never capped")
	assert.Equal(t, 20_000, strings.Count(payload, "¤"), "the notes are capped at 20 000")
}

func TestTheTicketPayloadCarriesTheBlocksThePromptDescribes(t *testing.T) {
	_, payload := ticketReviewRun(t, ai.TicketReviewRequest{
		Ticket: ai.TicketBlock{
			ExternalID:       "1234",
			WorkItemType:     "User Story",
			Title:            "Exportar la factura",
			State:            "Active",
			Description:      "Hay que poder exportar.",
			CriteriaMode:     "list",
			CriteriaMarkdown: "- AC-1: exporta en PDF",
			Notes:            "el IVA va aparte",
		},
		Contexts: []ai.ReviewContext{{Name: "Convenciones", Content: "usa tabs"}},
		Diff:     "diff --git a b",
		Scope:    ai.ScopeWorking,
		BaseRef:  "main",
	})

	// The prompt tells the model to expect these labels by name, so they are payload rather than
	// prose.
	for _, label := range []string{
		"TICKET: User Story 1234 — Exportar la factura",
		"CRITERIA MODE: list",
		"ACCEPTANCE CRITERIA:",
		"USER NOTES ON THIS TICKET:",
		"PROJECT REVIEW CONTEXT:",
		"SCOPE: ",
		"DIFF:",
	} {
		assert.Contains(t, payload, label)
	}

	// Order matters: the change is last, after everything that frames it.
	assert.Less(t, strings.Index(payload, "ACCEPTANCE CRITERIA:"), strings.Index(payload, "DIFF:"))
	assert.Less(t, strings.Index(payload, "SCOPE: "), strings.Index(payload, "DIFF:"))
	assert.Less(t, strings.Index(payload, "USER NOTES"), strings.Index(payload, "PROJECT REVIEW CONTEXT:"))
}

func TestATicketWithNoCriteriaSaysSoRatherThanLeavingTheBlockEmpty(t *testing.T) {
	_, payload := ticketReviewRun(t, ai.TicketReviewRequest{
		Ticket: ai.TicketBlock{ExternalID: "1234", CriteriaMode: "none"},
		Diff:   "diff --git a b",
	})

	assert.Contains(t, payload, "CRITERIA MODE: none")
	// An empty block reads as a prompt that lost its content; a sentence reads as a work item that
	// has none, which is what the review is meant to say out loud.
	assert.Contains(t, payload, "(el work item no declara criterios verificables)")
}

func TestATicketReviewWithNoNotesAndNoContextsOmitsThoseBlocks(t *testing.T) {
	_, payload := ticketReviewRun(t, ai.TicketReviewRequest{
		Ticket: ai.TicketBlock{ExternalID: "1234", CriteriaMode: "prose", CriteriaMarkdown: "algo"},
		Diff:   "diff --git a b",
	})

	assert.NotContains(t, payload, "USER NOTES ON THIS TICKET:")
	assert.NotContains(t, payload, "PROJECT REVIEW CONTEXT:")
}

// `AI-024`'s own payload, with the scope line `WI-023` added to it.
func TestTheAnalysisPayloadCarriesTheContextTheScopeAndTheDiff(t *testing.T) {
	_, payload := analyzeRun(t, ai.AnalyzeRequest{
		Contexts: []ai.ReviewContext{
			{Name: "Convenciones", Content: "los errores se envuelven"},
			{Name: "Vacío", Content: "   "},
		},
		Diff:    "--- app.go (modified)\n@@ -1 +1 @@\n-uno\n+dos\n",
		Scope:   ai.ScopeBranch,
		BaseRef: "develop",
	})

	assert.Contains(t, payload, "PROJECT CONTEXT:\n- Convenciones: los errores se envuelven")
	assert.NotContains(t, payload, "- Vacío:", "a blank context is not a line")
	assert.Contains(t, payload, "SCOPE: la contribución completa de la rama sobre `develop`")
	assert.Contains(t, payload, "DIFF:\n--- app.go (modified)")

	assert.Less(t, strings.Index(payload, "SCOPE:"), strings.Index(payload, "DIFF:"))
}

// The scope is in the **payload** and not in the methodology, and this is what says so: the built-in
// analyse template is a user-editable setting whose own text says "UNCOMMITTED changes".
func TestTheScopeIsNotInTheEditableTemplate(t *testing.T) {
	prompt, payload := analyzeRun(t, ai.AnalyzeRequest{
		Diff: "diff", Scope: ai.ScopeBranch, BaseRef: "main",
	})

	assert.NotContains(t, prompt, "SCOPE:")
	assert.Contains(t, payload, "SCOPE:")
}

func TestAnAnalysisWithNoContextsCarriesNoContextBlock(t *testing.T) {
	_, payload := analyzeRun(t, ai.AnalyzeRequest{Diff: "diff", Scope: ai.ScopeWorking})

	assert.NotContains(t, payload, "PROJECT CONTEXT")
}
