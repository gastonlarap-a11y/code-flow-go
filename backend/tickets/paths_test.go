package tickets_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gastonlarap-a11y/code-flow/backend/tickets"
)

func TestID(t *testing.T) {
	// Composed in one place so the primary key and `idx_tickets_identity` cannot disagree about
	// what "the same ticket" means.
	assert.Equal(t, "azure:contoso:Payments:1234",
		tickets.ID("azure", "contoso", "Payments", "1234"))

	// Text, not a number: Jira names its work items.
	assert.Equal(t, "jira:acme:CORE:CORE-45", tickets.ID("jira", "acme", "CORE", "CORE-45"))
}

func TestSlug(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		expected string
	}{
		{
			name:  "accented letters fold through the table, not through normalisation",
			title: "Facturación",
			// `facturaci-n` is what `Normalize(FormD)` produced under InvariantGlobalization, and it
			// is the exact failure the explicit table exists to prevent.
			expected: "Facturacion",
		},
		{
			name:     "case is preserved",
			title:    "CF-E2E Ajuste de tabla",
			expected: "CF-E2E-Ajuste-de-tabla",
		},
		{
			name:     "punctuation collapses into single separators",
			title:    "Ajuste de tabla (criterios en prosa)",
			expected: "Ajuste-de-tabla-criterios-en-prosa",
		},
		{
			name:     "the German sharp s becomes two letters",
			title:    "Straße",
			expected: "Strasse",
		},
		{
			name: "a segment is cut at 60 characters, mid-word if that is where 60 lands",
			// Cut by character count, not at a word boundary: a directory name is not prose, and a
			// rule that hunted for the previous separator would make two similar titles collide on
			// the same shortened name.
			title:    "Un título larguísimo que describe con todo lujo de detalle - el cambio entero",
			expected: "Un-titulo-larguisimo-que-describe-con-todo-lujo-de-detalle-e",
		},
		{
			name:     "a title of pure punctuation names nothing",
			title:    "!!! ???",
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, tickets.Slug(test.title))
			assert.LessOrEqual(t, len([]rune(tickets.Slug(test.title))), 60)
		})
	}
}

func TestMirrorPathLeadsWithTheID(t *testing.T) {
	// The id leads so directories sort and complete by the number a person quotes, and a retitled
	// work item keeps its prefix.
	assert.Equal(t,
		filepath.Join("/root", "contoso", "Payments", "1234-Ajustar-la-factura"),
		tickets.MirrorPath("/root", "contoso", "Payments", "1234", "Ajustar la factura"))

	// A title that slugs to nothing still produces a directory named by the id alone, rather than
	// one ending in a dangling separator.
	assert.Equal(t,
		filepath.Join("/root", "contoso", "Payments", "1234"),
		tickets.MirrorPath("/root", "contoso", "Payments", "1234", "???"))
}

// `WI-022`: a mirror never moves. This is the rule that keeps a user's `notes/` where they left it
// when somebody renames the work item on the board.
func TestAMirrorNeverMoves(t *testing.T) {
	existing := "/root/contoso/Payments/1234-El-titulo-viejo"

	t.Run("a mirrored ticket keeps its directory whatever the title says now", func(t *testing.T) {
		assert.Equal(t, existing,
			tickets.MirrorFor(existing, "/root", "contoso", "Payments", "1234", "Un título completamente nuevo"))
	})

	t.Run("blank counts as never mirrored", func(t *testing.T) {
		assert.Equal(t,
			filepath.Join("/root", "contoso", "Payments", "1234-Primer-titulo"),
			tickets.MirrorFor("  ", "/root", "contoso", "Payments", "1234", "Primer título"))
	})
}

func TestRootFallsBackWhenTheSettingIsBlank(t *testing.T) {
	configured := "/somewhere/else"
	blank := "   "

	assert.Equal(t, "/somewhere/else", tickets.Root(&configured, "/base/tickets"))
	assert.Equal(t, "/base/tickets", tickets.Root(&blank, "/base/tickets"),
		"clearing the field in Settings writes an empty string and means the default")
	assert.Equal(t, "/base/tickets", tickets.Root(nil, "/base/tickets"))
}
