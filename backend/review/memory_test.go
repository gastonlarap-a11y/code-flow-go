package review_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/review"
)

// The port of ReviewMemoryParseTests. The format is `XLANG-001`: the prompt makes the model write
// it, this parses it for reconciliation, and the renderer parses it for display. A parser that
// drifts produces a review with zero findings and no error anywhere.

const twoFindings = "🔁 algo de resumen antes de los hallazgos\n" +
	"\n" +
	"### 🔴 [Blocker · Bug] race-condition · F-001\n" +
	"El contador se lee sin lock\n" +
	"\n" +
	"📍 Ubicación: src/app.ts:12-14\n" +
	"💭 Por qué: dos goroutines escriben a la vez\n" +
	"🎯 Confianza: 90\n" +
	"\n" +
	"### 🟡 [Menor · Code Smell] naming · F-002\n" +
	"El nombre no dice qué hace\n" +
	"\n" +
	"📍 Ubicación: `src/util.ts`\n" +
	"🎯 Confianza: 40\n"

func TestAFindingIsParsedFromItsOwnBlock(t *testing.T) {
	findings := review.ParseFindings(twoFindings)

	require.Len(t, findings, 2)

	first := findings[0]
	assert.Equal(t, "F-001", first.ID)
	assert.Equal(t, "critical", first.Severity)
	assert.Equal(t, "Bug", first.Tipo)
	assert.Equal(t, "race-condition", first.Categoria)
	assert.Equal(t, "El contador se lee sin lock", first.Subtitulo, "the first line that says something")
	require.NotNil(t, first.Archivo)
	assert.Equal(t, "src/app.ts", *first.Archivo)
	require.NotNil(t, first.Lineas)
	assert.Equal(t, "12-14", *first.Lineas)
	require.NotNil(t, first.Confianza)
	assert.Equal(t, int64(90), *first.Confianza)
	assert.Equal(t, "abierto", first.Estado, "every first parse starts open")
	assert.Zero(t, first.IntroducidoEnIter, "0 means 'not assigned yet'")
	assert.Nil(t, first.Delta)
	assert.Nil(t, first.ThreadID)

	second := findings[1]
	assert.Equal(t, "info", second.Severity)
	require.NotNil(t, second.Archivo)
	assert.Equal(t, "src/util.ts", *second.Archivo, "the model's backticks are stripped")
	assert.Nil(t, second.Lineas, "a bare path has no lines")
	require.NotNil(t, second.Confianza)
	assert.Equal(t, int64(40), *second.Confianza, "each field comes from its own block")
}

// The word wins over the emoji: reading only the emoji once rendered two `Mayor` findings as
// critical and turned the Quality Gate red for them.
func TestTheSeverityWordWinsOverTheEmoji(t *testing.T) {
	tests := []struct {
		name     string
		word     string
		emoji    string
		expected string
	}{
		{name: "the word, against a louder emoji", word: "Mayor", emoji: "🚨", expected: "warning"},
		{name: "Blocker", word: "Blocker", emoji: "🔵", expected: "critical"},
		{name: "Crítico", word: "Crítico", emoji: "🟡", expected: "critical"},
		{name: "Critico without its accent", word: "Critico", emoji: "🟡", expected: "critical"},
		{name: "Menor", word: "Menor", emoji: "🔴", expected: "info"},
		{name: "Info", word: "Info", emoji: "🔴", expected: "info"},
		{name: "in any case", word: "  mAyOr ", emoji: "🔵", expected: "warning"},
		{name: "an unknown word falls back to 🔴", word: "Alta", emoji: "🔴", expected: "critical"},
		{name: "an unknown word falls back to 🚨", word: "Alta", emoji: "🚨", expected: "critical"},
		{name: "an unknown word falls back to 🟠", word: "Media", emoji: "🟠", expected: "warning"},
		{name: "1.7.2's ⚠️ still parses", word: "Media", emoji: "⚠️", expected: "warning"},
		{name: "1.7.2's ℹ️ still parses", word: "Baja", emoji: "ℹ️", expected: "info"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, review.SeverityOf(test.word, test.emoji))
		})
	}
}

// The trailing sections are cut off before the last block, or a digit in a "what went well" bullet
// becomes that finding's confidence.
func TestTheTrailingSectionsDoNotBleedIntoTheLastFinding(t *testing.T) {
	markdown := "### 🔴 [Blocker · Bug] race · F-001\n" +
		"Algo\n" +
		"📍 Ubicación: src/app.ts:1\n" +
		"\n" +
		"## 👍 Lo que está bien\n" +
		"\n" +
		"- La cobertura sube del 30 al 80\n" +
		"🎯 Confianza: 99\n"

	findings := review.ParseFindings(markdown)

	require.Len(t, findings, 1)
	assert.Nil(t, findings[0].Confianza, "the bullet's number belongs to nobody")
}

func TestTheTrailingSectionsAreRecognisedWithoutTheirAccents(t *testing.T) {
	for _, heading := range []string{"## 👍 Lo que está bien", "## 👍 Lo que esta bien", "## 🗒️ Notas", "## 🗒 Notas"} {
		t.Run(heading, func(t *testing.T) {
			markdown := "### 🔴 [Blocker · Bug] race · F-001\nAlgo\n\n" + heading + "\n\n- 🎯 Confianza: 99\n"

			findings := review.ParseFindings(markdown)

			require.Len(t, findings, 1)
			assert.Nil(t, findings[0].Confianza)
		})
	}
}

func TestTheLocationIsSplitOnItsLastColon(t *testing.T) {
	tests := []struct {
		name  string
		given string
		file  string
		lines string
	}{
		{name: "a range", given: "src/app.ts:12-14", file: "src/app.ts", lines: "12-14"},
		{name: "one line", given: "src/app.ts:12", file: "src/app.ts", lines: "12"},
		{name: "markdown wrapping", given: "`**src/app.ts:12**`", file: "src/app.ts", lines: "12"},
		{name: "a bare path", given: "src/app.ts", file: "src/app.ts", lines: ""},
		{name: "a windows drive with no line", given: "C:\\repo\\app.ts", file: "C:\\repo\\app.ts", lines: ""},
		{name: "a windows path with a line", given: "C:\\repo\\app.ts:12", file: "C:\\repo\\app.ts", lines: "12"},
		{name: "a tail with no digit", given: "src/app.ts:algo", file: "src/app.ts:algo", lines: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			markdown := "### 🔴 [Blocker · Bug] c · F-001\nx\n📍 Ubicación: " + test.given + "\n"

			findings := review.ParseFindings(markdown)

			require.Len(t, findings, 1)
			require.NotNil(t, findings[0].Archivo)
			assert.Equal(t, test.file, *findings[0].Archivo)
			if test.lines == "" {
				assert.Nil(t, findings[0].Lineas)
				return
			}
			require.NotNil(t, findings[0].Lineas)
			assert.Equal(t, test.lines, *findings[0].Lineas)
		})
	}
}

func TestTheLocationIsAcceptedWithoutItsAccent(t *testing.T) {
	findings := review.ParseFindings("### 🔴 [Blocker · Bug] c · F-001\nx\n📍 Ubicacion: src/app.ts:3\n")

	require.Len(t, findings, 1)
	require.NotNil(t, findings[0].Lineas)
	assert.Equal(t, "3", *findings[0].Lineas)
}

func TestTextWithNoFindingsParsesToNone(t *testing.T) {
	findings := review.ParseFindings("El cambio se ve bien, no hay hallazgos.\n")

	assert.Empty(t, findings)
	assert.NotNil(t, findings, "an empty list, never a nil one")
}

// A finding with neither a file nor a category would otherwise share the key "|" with every other
// such finding, and the two would reconcile as one.
func TestIdentityFallsBackToTheSubtitleWhenThereIsNothingElse(t *testing.T) {
	assert.Equal(t, "src/app.ts|race", review.FindingIdentity(text("src/app.ts"), "race"))
	assert.Equal(t, "src/app.ts|race", review.FindingIdentity(text("/src/app.ts"), "RACE"),
		"the leading slash and the case are normalised")
	assert.Equal(t, "|race", review.FindingIdentity(nil, "race"), "a category with no file is still a key")
	assert.Equal(t, "|", review.FindingIdentity(nil, ""))
}

func text(value string) *string { return &value }

// The renderer and this parser are two implementations of one format. They are exercised over the
// same fixture in `frontend/src/lib/parseAnalysis.crosslang.test.ts`, which reads the file below;
// this test is the Go half, and the two assert the same findings.
func TestTheCrossLanguageFixtureParsesTheSameHere(t *testing.T) {
	markdown := crossLanguageFixture(t)

	findings := review.ParseFindings(markdown)

	require.Len(t, findings, 3)

	assert.Equal(t, "F-001", findings[0].ID)
	assert.Equal(t, "critical", findings[0].Severity)
	assert.Equal(t, "Bug", findings[0].Tipo)
	assert.Equal(t, "race-condition", findings[0].Categoria)
	require.NotNil(t, findings[0].Archivo)
	assert.Equal(t, "src/app.ts", *findings[0].Archivo)
	require.NotNil(t, findings[0].Lineas)
	assert.Equal(t, "12-14", *findings[0].Lineas)
	require.NotNil(t, findings[0].Confianza)
	assert.Equal(t, int64(90), *findings[0].Confianza)

	// The severity word against a louder emoji: the case that made this contract explicit.
	assert.Equal(t, "F-002", findings[1].ID)
	assert.Equal(t, "warning", findings[1].Severity)
	assert.Equal(t, "Security Hotspot", findings[1].Tipo)

	assert.Equal(t, "F-003", findings[2].ID)
	assert.Equal(t, "info", findings[2].Severity)
	require.NotNil(t, findings[2].Archivo)
	assert.Equal(t, "src/util.ts", *findings[2].Archivo, "the markdown wrapping is stripped on both sides")
	assert.Nil(t, findings[2].Confianza, "the strengths bullet's number belongs to nobody")
}

// crossLanguageFixtureName is read by both halves of the contract: this package's test, and
// `frontend/src/lib/parseAnalysis.crosslang.test.ts`, which reads the same file across the tree. One
// file, because two copies of a fixture drift and the drift is what the test exists to catch.
const crossLanguageFixtureName = "testdata/crosslang-review.md"

func crossLanguageFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(crossLanguageFixtureName)
	require.NoError(t, err)

	markdown := string(raw)
	require.True(t, strings.Contains(markdown, "F-002"), "the fixture is the one both parsers read")
	return markdown
}
