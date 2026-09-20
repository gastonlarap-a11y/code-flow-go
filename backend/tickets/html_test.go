package tickets_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gastonlarap-a11y/code-flow/backend/tickets"
)

// The substance floor (`WI-007`), which is the measurement that keeps an empty box from being read
// as a requirement.
func TestSubstanceLength(t *testing.T) {
	tests := []struct {
		name     string
		fragment string
		length   int
		enough   bool
	}{
		{
			name: "the empty box a real board had on every work item",
			// Twenty characters of markup and one of content. Counting the markup is what calls this
			// a requirement.
			fragment: "<div><b>-</b> </div>",
			length:   1,
			enough:   false,
		},
		{
			name:     "entities resolve before counting",
			fragment: "<div>a&nbsp;&amp;&nbsp;b</div>",
			length:   5,
			enough:   false,
		},
		{
			name:     "a real criterion clears the floor",
			fragment: "<div>El usuario puede exportar la factura en PDF desde el detalle.</div>",
			length:   61,
			enough:   true,
		},
		{
			name:     "whitespace padding does not count",
			fragment: "<div>   \n\n   </div>",
			length:   0,
			enough:   false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.length, tickets.SubstanceLength(test.fragment))
			assert.Equal(t, test.enough, tickets.HasSubstance(test.fragment))
		})
	}
}

func TestToMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		expected string
	}{
		{
			name:     "the one-div-per-line shape that editor produces",
			html:     "<div>Primera línea</div><div>Segunda línea</div>",
			expected: "Primera línea\n\nSegunda línea",
		},
		{
			name:     "headings keep their level",
			html:     "<h2>Contexto</h2><p>Algo</p>",
			expected: "## Contexto\n\nAlgo",
		},
		{
			name:     "emphasis and code become their markers",
			html:     "<p>Esto es <b>importante</b> y esto es <code>literal</code>.</p>",
			expected: "Esto es **importante** y esto es `literal`.",
		},
		{
			name:     "a bullet list becomes a bullet list",
			html:     "<ul><li>Uno</li><li>Dos</li></ul>",
			expected: "- Uno\n- Dos",
		},
		{
			name:     "a numbered list is numbered from one",
			html:     "<ol><li>Primero</li><li>Segundo</li></ol>",
			expected: "1. Primero\n2. Segundo",
		},
		{
			name:     "a nested list indents rather than restarting",
			html:     "<ul><li>Padre<ul><li>Hijo</li></ul></li></ul>",
			expected: "- Padre\n  - Hijo",
		},
		{
			name:     "links keep their target",
			html:     `<p>Ver <a href="https://example.test/spec">la especificación</a>.</p>`,
			expected: "Ver [la especificación](https://example.test/spec).",
		},
		{
			name:     "an image keeps its source for relinking",
			html:     `<p><img src="https://dev.azure.com/a/_apis/wit/attachments/guid" alt="captura"></p>`,
			expected: "![captura](https://dev.azure.com/a/_apis/wit/attachments/guid)",
		},
		{
			name:     "entities are resolved",
			html:     "<p>a &lt; b &amp;&amp; c &gt; d</p>",
			expected: "a < b && c > d",
		},
		{
			name:     "plain text that is not HTML passes through",
			html:     "Una descripción escrita en texto plano.",
			expected: "Una descripción escrita en texto plano.",
		},
		{
			name:     "an empty field converts to nothing",
			html:     "   ",
			expected: "",
		},
		{
			name: "a table gets the separator row markdown needs",
			html: "<table><tr><td>Campo</td><td>Valor</td></tr><tr><td>Estado</td><td>Activo</td></tr></table>",
			// The separator goes in whether or not there was a `<th>`: a board's tables frequently
			// have none, and without the row the whole thing renders as a wall of pipes.
			expected: "| Campo | Valor |\n| --- | --- |\n| Estado | Activo |",
		},
		{
			name:     "a pipe inside a cell is escaped rather than opening a column",
			html:     "<table><tr><td>a|b</td></tr></table>",
			expected: `| a\|b |` + "\n| --- |",
		},
		{
			name:     "a preformatted block keeps its own whitespace and its asterisks",
			html:     "<pre>for (const *p : xs) {\n    use(p);\n}</pre>",
			expected: "```\nfor (const *p : xs) {\n    use(p);\n}\n```",
		},
		{
			name:     "an unclosed angle bracket is content, not a tag",
			html:     "<p>si a < b entonces</p>",
			expected: "si a < b entonces",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, tickets.ToMarkdown(test.html))
		})
	}
}

func TestToMarkdownCollapsesTheEditorsBlankLines(t *testing.T) {
	// `<div><br></div>` is what a pressed Enter produces, and a description written with generous
	// spacing turns into a page of blank lines without this.
	converted := tickets.ToMarkdown(
		"<div>Uno</div><div><br></div><div><br></div><div><br></div><div>Dos</div>")

	assert.Equal(t, "Uno\n\nDos", converted)
}
