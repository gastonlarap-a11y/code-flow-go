package tickets_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/review"
	"github.com/gastonlarap-a11y/code-flow/backend/tickets"
)

// The Go half of `XLANG-016`.
//
// The verdict block is a three-way contract: a prompt tells the model to write it, this parser reads
// it to store it, and `renderer/src/lib/parseTicketVerdict.ts` reads it to render. Two parsers that
// drift do not fail — they silently disagree about whether a criterion was met.
//
// Both halves read the **same file**, `testdata/crosslang-verdict.md`. The TypeScript half is
// `frontend/src/lib/parseTicketVerdict.crosslang.test.ts`, and the two assert the same four verdicts.

func crossLanguageFixture(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "crosslang-verdict.md"))
	require.NoError(t, err, "the fixture both parsers read must exist")
	return string(body)
}

func TestTheCrossLanguageVerdictParsesTheSameHere(t *testing.T) {
	parsed := tickets.ParseVerdict(crossLanguageFixture(t))
	require.NotNil(t, parsed)

	t.Run("it reads the four criteria in order", func(t *testing.T) {
		require.Len(t, parsed.Criteria, 4)

		ids := make([]string, 0, 4)
		verdicts := make([]string, 0, 4)
		for _, criterion := range parsed.Criteria {
			ids = append(ids, criterion.ID)
			verdicts = append(verdicts, criterion.Verdict)
		}
		assert.Equal(t, []string{"AC-1", "AC-2", "AC-3", "AC-4"}, ids)
		assert.Equal(t,
			[]string{tickets.VerdictMet, tickets.VerdictPartial, tickets.VerdictNotMet, tickets.VerdictUnverifiable},
			verdicts)
	})

	t.Run("it joins a wrapped evidence line back into one", func(t *testing.T) {
		assert.Equal(t,
			"backend/tickets/mirror.go:181-196 — se nombra el adjunto, pero el motivo se pierde "+
				"cuando el error no trae texto propio.",
			parsed.Criteria[1].Evidence)
	})

	t.Run("it strips the emphasis the model was told not to add", func(t *testing.T) {
		// AC-4 is written `Veredicto: **no verificable**`. Reading the asterisks as part of the word
		// would fall through to the default, which happens to be the same answer — so this asserts
		// the cleaning rather than the outcome.
		assert.Equal(t, tickets.VerdictUnverifiable, parsed.Criteria[3].Verdict)
		assert.NotContains(t, parsed.Criteria[3].Evidence, "*")
	})

	t.Run("a criterion with no confidence line carries none", func(t *testing.T) {
		require.NotNil(t, parsed.Criteria[0].Confidence)
		assert.Equal(t, int64(90), *parsed.Criteria[0].Confidence)
		assert.Nil(t, parsed.Criteria[2].Confidence, "AC-3 has no 🎯 line")
	})

	t.Run("it reads the coverage block", func(t *testing.T) {
		require.NotNil(t, parsed.Coverage)
		assert.Equal(t, tickets.CoverageIncomplete, parsed.Coverage.Coverage)
		assert.True(t, parsed.Coverage.Relevant, "the fixture's ticket does describe the change")
		assert.Contains(t, parsed.Coverage.Missing, "AC-3")
		assert.Contains(t, parsed.Coverage.OutOfScope, "otro work item")
		assert.Contains(t, parsed.Coverage.Summary, "dos de cuatro")
	})
}

// The second defence of `XLANG-016`: the finding parser reads the same findings whether or not the
// verdict sections are there.
//
// `### AC-1:` carries no emoji, no bracketed severity and no `F-NNN`, so it can never match a
// finding header — and this asserts that on the **unsplit** text, which is what proves the split in
// `SplitReview` is belt and braces rather than the only thing keeping the two apart.
func TestTheFindingParserIgnoresTheVerdictSections(t *testing.T) {
	fixture := crossLanguageFixture(t)

	whole := review.ParseFindings(fixture)
	findings, _, hasVerdict := tickets.SplitReview(fixture)
	require.True(t, hasVerdict)
	head := review.ParseFindings(findings)

	require.Len(t, whole, 2, "two findings, and the four AC blocks are not among them")
	assert.Len(t, head, len(whole), "cutting at the criteria heading changes nothing")
	assert.Equal(t, whole[0].ID, head[0].ID)
	assert.Equal(t, whole[1].ID, head[1].ID)
}

func TestSplitReviewLeavesAnOrdinaryAnalysisAlone(t *testing.T) {
	analysis := "📈 CALIDAD: Fiabilidad=A Seguridad=A Mantenibilidad=A\n\n✅ Nada que reportar.\n"

	findings, verdict, hasVerdict := tickets.SplitReview(analysis)
	assert.False(t, hasVerdict)
	assert.Empty(t, verdict)
	assert.Equal(t, analysis, findings, "a review with no verdict block comes back untouched")
	assert.Nil(t, tickets.ParseVerdict(analysis))
}

func TestAnUnreadableVerdictBecomesNoVerificable(t *testing.T) {
	tests := []struct {
		name     string
		answer   string
		expected string
	}{
		{
			name:     "a word the four literals do not cover",
			answer:   "Veredicto: probablemente sí",
			expected: tickets.VerdictUnverifiable,
		},
		{
			name:     "no Veredicto line at all",
			answer:   "Evidencia: algo",
			expected: tickets.VerdictUnverifiable,
		},
		{
			name:     "the word with a trailing explanation",
			answer:   "Veredicto: cumple, ver el test nuevo",
			expected: tickets.VerdictMet,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			review := "## VERIFICACIÓN DE CRITERIOS DE ACEPTACIÓN\n\n### AC-1: algo\n" + test.answer + "\n"

			parsed := tickets.ParseVerdict(review)
			require.NotNil(t, parsed)
			require.Len(t, parsed.Criteria, 1)
			assert.Equal(t, test.expected, parsed.Criteria[0].Verdict)
		})
	}
}

// `WI-026`: only an explicit `no corresponde` disowns a ticket. An absent line reads as relevant, so
// a review written before the question existed keeps its meaning.
func TestOnlyAnExplicitNoCorrespondeDisownsTheTicket(t *testing.T) {
	tests := []struct {
		name      string
		coverage  string
		relevant  bool
		relevance string
	}{
		{
			name:      "the line is missing entirely",
			coverage:  "Cobertura: completa\nResumen: todo bien\n",
			relevant:  true,
			relevance: "",
		},
		{
			name:      "the model said the ticket is about this",
			coverage:  "Relevancia: sí, describe el cambio\nCobertura: completa\n",
			relevant:  true,
			relevance: "sí, describe el cambio",
		},
		{
			name:      "the model disowned it",
			coverage:  "Relevancia: no corresponde — el ticket habla de facturación\nCobertura: no verificable\n",
			relevant:  false,
			relevance: "no corresponde — el ticket habla de facturación",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed := tickets.ParseVerdict(
				"## VERIFICACIÓN DE CRITERIOS DE ACEPTACIÓN\n\n## VEREDICTO DE COBERTURA\n\n" + test.coverage)

			require.NotNil(t, parsed)
			require.NotNil(t, parsed.Coverage)
			assert.Equal(t, test.relevant, parsed.Coverage.Relevant)
			assert.Equal(t, test.relevance, parsed.Coverage.Relevance)
		})
	}
}

// Both parsers accept a dropped accent on the two `##` headers, and only there.
func TestBothHeadersToleranteADroppedAccent(t *testing.T) {
	parsed := tickets.ParseVerdict(
		"## VERIFICACION DE CRITERIOS DE ACEPTACION\n\n### AC-1: algo\nVeredicto: cumple\n\n" +
			"## VEREDICTO DE COBERTURA\n\nCobertura: completa\n")

	require.NotNil(t, parsed)
	require.Len(t, parsed.Criteria, 1)
	require.NotNil(t, parsed.Coverage)
	assert.Equal(t, tickets.CoverageComplete, parsed.Coverage.Coverage)
}
