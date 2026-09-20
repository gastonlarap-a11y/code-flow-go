package review_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/review"
)

// The finding format is written in two prompts and read by three parsers, and until now nothing
// joined the two halves.
//
// `XLANG-001` calls the finding format a three-way contract: the prompt asks for it, `memory.go`
// parses it, and `parseAnalysis.ts` parses it again for the renderer. Two of those three are pinned
// against each other by the cross-language fixture. The third — the prompt that causes the format
// to exist at all — is a text file nothing reads in a test, so a rewording there breaks every live
// review while the whole suite stays green.
//
// Found by the Phase 9 inventory audit: `Ai/PromptsTests` was the one C# class with no Go
// counterpart, and these are its behaviours.

// findingFormatStart and findingFormatEnd bracket the block the two review standards share.
//
// Anchored on their own headings rather than on line numbers, which is what keeps this test from
// becoming false the first time somebody adds a paragraph above them.
const (
	findingFormatStart = "## Quality Gate"
	findingFormatEnd   = "- Order the findings by severity (Blocker→Info) and, within each severity, by descending confidence."
)

// The pull-request standard and the ticket standard say the same thing about findings, and they say
// it in the same bytes.
//
// They have to. `ReviewMemory` reconciles a *ticket* review and a *pull-request* review with one
// parser, so a finding written under one standard is read by the code that expects the other. Two
// prompts that drift apart give the same parser two dialects, and the symptom is not an error — it
// is findings that quietly stop being recognised on re-review and are reported as new every time.
func TestTheTwoReviewStandardsShareTheFindingFormatVerbatim(t *testing.T) {
	pullRequest := ai.Prompt(ai.PromptPRReviewStandard)
	ticket := ai.Prompt(ai.PromptTicketReviewStandard)

	shared := between(t, pullRequest, findingFormatStart, findingFormatEnd)
	require.Greater(t, len(shared), 1000, "the extracted block is too short to be the finding format")

	assert.Contains(t, ticket, shared,
		"the two standards have drifted apart; one parser reads both, so they cannot")
}

// And what those prompts ask for is what the parser matches.
//
// Byte-level, and each literal is one the parser would silently fail to find rather than complain
// about: an emoji dropped from the header line, an accent added to `Ubicación`, a renamed afterword
// heading. Every one of them degrades to "no findings" rather than to an error.
func TestTheReviewStandardAsksForWhatTheFindingParserReads(t *testing.T) {
	for _, standard := range []string{
		ai.Prompt(ai.PromptPRReviewStandard),
		ai.Prompt(ai.PromptTicketReviewStandard),
	} {
		for name, literal := range map[string]string{
			"the location line":       "📍 Ubicación:",
			"the confidence line":     "🎯 Confianza:",
			"the quality ratings":     "📈 CALIDAD:",
			"the quality gate":        "🚦 Quality Gate:",
			"the finding id":          "F-{número correlativo de 3 dígitos}",
			"the strengths heading":   "## 👍 Lo que está bien",
			"the notes heading":       "## 🗒️ Notas",
			"the finding header form": "### {emoji} [{Severidad} · {Tipo}] {categoría} · F-",
		} {
			t.Run(name, func(t *testing.T) {
				assert.Contains(t, standard, literal,
					"the parser matches %q; the prompt has to be what asks for it", literal)
			})
		}
	}
}

// The end-to-end join: an answer written exactly as the standard demonstrates parses into the
// finding the standard describes.
//
// A worked example rather than the prompt itself, because the prompt's block is a template with
// `{placeholders}` where a model would put text — and a placeholder is not what the parser has to
// read. What this pins is that filling the template in the obvious way produces something the
// parser recognises, with the id, severity, type, category, location and confidence all landing in
// their own fields.
func TestAnAnswerShapedLikeTheStandardParses(t *testing.T) {
	answer := strings.Join([]string{
		"📈 CALIDAD: Fiabilidad=B Seguridad=A Mantenibilidad=C",
		"🚦 Quality Gate: PASSED",
		"",
		"### 🟠 [Mayor · Bug] error-handling · F-001",
		"",
		"El error del driver se descarta sin registrarlo.",
		"",
		"📍 Ubicación: backend/dbml/introspect.go:120-134",
		"",
		"💭 Por qué: el retorno se ignora y la conexión queda abierta.",
		"",
		"💡 Sugerencia: propagar el error al llamador.",
		"",
		"🎯 Confianza: 85/100",
		"",
		"---",
		"",
		"## 👍 Lo que está bien",
		"- El pooling queda desactivado en los cuatro motores.",
	}, "\n")

	findings := review.ParseFindings(answer)

	require.Len(t, findings, 1, "an answer written as the standard demonstrates produced no finding")
	finding := findings[0]

	assert.Equal(t, "F-001", finding.ID)
	// `Severity` is the bucket, not the word: `Mayor` → `warning` (`SeverityOf`, `XLANG-001`).
	// The Spanish vocabulary the prompt uses survives in `Tipo` and `Categoria`.
	assert.Equal(t, "warning", finding.Severity)
	assert.Equal(t, "Bug", finding.Tipo)
	assert.Equal(t, "error-handling", finding.Categoria)

	require.NotNil(t, finding.Archivo, "the 📍 Ubicación line did not reach the field")
	assert.Equal(t, "backend/dbml/introspect.go", *finding.Archivo)
	require.NotNil(t, finding.Lineas)
	assert.Equal(t, "120-134", *finding.Lineas)

	require.NotNil(t, finding.Confianza, "the 🎯 Confianza line did not reach the field")
	assert.Equal(t, int64(85), *finding.Confianza)
}

// between extracts the inclusive span from the first marker to the end of the line the second one
// starts, failing loudly rather than returning an empty string that would make the caller pass.
func between(t *testing.T, text, start, end string) string {
	t.Helper()

	from := strings.Index(text, start)
	require.GreaterOrEqual(t, from, 0, "the standard no longer contains %q", start)

	to := strings.Index(text[from:], end)
	require.GreaterOrEqual(t, to, 0, "the standard no longer contains %q", end)

	return text[from : from+to+len(end)]
}
