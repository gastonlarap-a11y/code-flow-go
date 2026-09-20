package tickets_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/tickets"
)

// The prompt and the parser are two halves of one contract, and this is the half nothing checked.
//
// `XLANG-016` pins the verdict block across Go and TypeScript: one fixture, two parsers, and a test
// on each side. What neither of them covers is where the block comes from. The model emits it
// because `DEFAULT_TICKET_REVIEW_STANDARD.txt` asks for it "with EXACTLY these headers and labels",
// and the literals in that sentence are the same literals `verdict.go` matches with — written out a
// second time, in a different file, in a different language, with nothing joining them.
//
// So editing the standard is a silent way to break every ticket review. Change
// `## VEREDICTO DE COBERTURA` to `## VEREDICTO` in the prompt and every test in this repository
// still passes: the parsers keep parsing their fixtures, the renderer keeps rendering, and the only
// thing that changes is that live answers stop carrying a verdict the parser can find — which
// surfaces as a coverage section that is silently empty for every ticket, forever.
//
// Found by the Phase 9 inventory audit: `Ai/PromptsTests` is the one C# class whose behaviour had
// no Go counterpart, and `The_verdict_headers_the_prompt_asks_for_are_the_ones_the_parser_looks_for`
// is why it mattered.

// TestTheTicketStandardAsksForWhatTheVerdictParserReads is the join.
//
// It reads the embedded prompt and asserts, literal by literal, that everything `verdict.go`
// matches is something the prompt actually demands. Byte-level on purpose: an accent dropped here
// is an accent the parser tolerates by regex and a reader would never notice.
func TestTheTicketStandardAsksForWhatTheVerdictParserReads(t *testing.T) {
	standard := ai.Prompt(ai.PromptTicketReviewStandard)
	require.NotEmpty(t, standard)

	for name, literal := range map[string]string{
		// The two section headers, which are where the parser starts and stops.
		"the criteria section header": "## VERIFICACIÓN DE CRITERIOS DE ACEPTACIÓN",
		"the coverage section header": "## VEREDICTO DE COBERTURA",

		// The per-criterion header. `AC-1` rather than the regex's `AC-\d+`: the prompt shows an
		// example, and the example is what a model copies.
		"the criterion header": "### AC-1:",

		// Every field label the parser ends a continuation on. A label the prompt stopped asking
		// for is a field that arrives empty; one it renamed is a field whose text runs into the
		// next one.
		"the verdict label":        "Veredicto:",
		"the evidence label":       "Evidencia:",
		"the relevance label":      "Relevancia:",
		"the coverage label":       "Cobertura:",
		"the missing label":        "Faltante:",
		"the out-of-scope label":   "Fuera de alcance:",
		"the summary label":        "Resumen:",
		"the confidence marker":    "🎯 Confianza:",
		"the quality gate line":    "🚦 Quality Gate:",
		"the quality ratings line": "📈 CALIDAD:",
	} {
		t.Run(name, func(t *testing.T) {
			assert.Contains(t, standard, literal,
				"the parser matches %q; the prompt has to be what asks for it", literal)
		})
	}
}

// And the other direction: what the prompt asks for, the parser finds.
//
// The test above would pass against a prompt that asked for the right headers in the wrong shape —
// a `###` where the parser wants `##`, a label inside a code fence. This one runs the parser over
// the prompt's own worked example, which is the closest thing to an answer the prompt can produce
// without a model.
func TestTheStandardsOwnExampleParses(t *testing.T) {
	standard := ai.Prompt(ai.PromptTicketReviewStandard)

	parsed := tickets.ParseVerdict(standard)
	require.NotNil(t, parsed, "the parser found neither closing section in the prompt that demands both")

	require.NotEmpty(t, parsed.Criteria,
		"the criteria block the prompt demonstrates is one the parser cannot read")
	assert.Equal(t, "AC-1", parsed.Criteria[0].ID)
	assert.NotEmpty(t, parsed.Criteria[0].Verdict, "the Veredicto label did not reach the field")
	assert.NotEmpty(t, parsed.Criteria[0].Evidence, "the Evidencia label did not reach the field")

	require.NotNil(t, parsed.Coverage, "the coverage block the prompt demonstrates was not read")
	assert.NotEmpty(t, parsed.Coverage.Summary, "the Resumen label did not reach the field")
	assert.NotEmpty(t, parsed.Coverage.Relevance, "the Relevancia label did not reach the field")
}
