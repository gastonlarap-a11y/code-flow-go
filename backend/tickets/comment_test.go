package tickets_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gastonlarap-a11y/code-flow/backend/tickets"
)

// `WI-022`: the verdict is converted on the way out, because Azure comments are rich text and
// markdown arrives as its own punctuation — `## VERIFICACIÓN` and `**cumple**`, literally, on a page
// other people read.
func TestToHTML(t *testing.T) {
	tests := []struct {
		name     string
		markdown string
		expected string
	}{
		{
			name:     "a heading becomes one, a level down",
			markdown: "## VERIFICACIÓN DE CRITERIOS DE ACEPTACIÓN",
			// A comment sits inside somebody else's page, so its headings start below the page's.
			expected: "<h3>VERIFICACIÓN DE CRITERIOS DE ACEPTACIÓN</h3>",
		},
		{
			name:     "bold, italic and code become tags",
			markdown: "Veredicto: **cumple** con *matices* y `AC-1`",
			expected: "<div>Veredicto: <b>cumple</b> con <i>matices</i> y <code>AC-1</code></div>",
		},
		{
			name:     "a bullet list becomes a list",
			markdown: "- Uno\n- Dos",
			expected: "<ul><li>Uno</li><li>Dos</li></ul>",
		},
		{
			name:     "a numbered list is a list too",
			markdown: "1. Primero\n2. Segundo",
			expected: "<ul><li>Primero</li><li>Segundo</li></ul>",
		},
		{
			name:     "a paragraph is a div",
			markdown: "Una línea suelta",
			expected: "<div>Una línea suelta</div>",
		},
		{
			name:     "a fenced block keeps its own whitespace",
			markdown: "```\nif (a < b) {\n    return;\n}\n```",
			expected: "<pre>if (a &lt; b) {\n    return;\n}\n</pre>",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, tickets.ToHTML(test.markdown))
		})
	}
}

// Escaping happens **before** any tag is added, so a verdict quoting a diff cannot close one.
func TestAVerdictQuotingADiffCannotCloseATag(t *testing.T) {
	converted := tickets.ToHTML(
		"Evidencia: se cambió `<script>alerta()</script>` por &nbsp; en a.go:10-12")

	assert.NotContains(t, converted, "<script>")
	assert.Contains(t, converted, "&lt;script&gt;")
	assert.Contains(t, converted, "&amp;nbsp;",
		"an entity the model quoted is text, not an entity of ours")

	// And the tags this function wrote are intact.
	assert.True(t, strings.HasPrefix(converted, "<div>"))
	assert.True(t, strings.HasSuffix(converted, "</div>"))
}

func TestToHTMLClosesAnUnclosedFence(t *testing.T) {
	// The model's typo is not a reason to publish a broken element onto somebody's board.
	converted := tickets.ToHTML("```\nalgo sin cerrar")

	assert.Equal(t, strings.Count(converted, "<pre>"), strings.Count(converted, "</pre>"))
}

func TestToHTMLOnAWholeVerdict(t *testing.T) {
	converted := tickets.ToHTML(strings.Join([]string{
		"## VEREDICTO DE COBERTURA",
		"",
		"Cobertura: **incompleta**",
		"Faltante: AC-3",
		"",
		"- AC-1: cumple",
		"- AC-2: parcial",
	}, "\n"))

	assert.Contains(t, converted, "<h3>VEREDICTO DE COBERTURA</h3>")
	assert.Contains(t, converted, "<b>incompleta</b>")
	assert.Contains(t, converted, "<ul><li>AC-1: cumple</li><li>AC-2: parcial</li></ul>")
	// One list, opened and closed once: the blank line between the two blocks closes the first.
	assert.Equal(t, 1, strings.Count(converted, "<ul>"))
	assert.Equal(t, 1, strings.Count(converted, "</ul>"))
}
