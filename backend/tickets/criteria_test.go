package tickets_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/tickets"
)

// The empty box a real organisation had declared on eight work-item types and filled on none.
const emptyCriteriaField = "<div><b>-</b></div>"

func TestReadCriteria(t *testing.T) {
	order := []string{tickets.FieldAcceptanceCriteria, tickets.FieldDescription}

	t.Run("a field carrying a list is numbered AC-1 upwards", func(t *testing.T) {
		criteria := tickets.ReadCriteria(tickets.FieldMap{
			tickets.FieldAcceptanceCriteria: "<ul>" +
				"<li>El usuario puede exportar la factura en PDF</li>" +
				"<li>La exportación incluye el detalle de impuestos</li>" +
				"<li>Un error de exportación se muestra sin perder el formulario</li>" +
				"</ul>",
		}, order, nil)

		assert.Equal(t, tickets.ModeList, criteria.Mode)
		require.NotNil(t, criteria.Field)
		assert.Equal(t, tickets.FieldAcceptanceCriteria, *criteria.Field)
		require.Len(t, criteria.Items, 3)
		assert.Equal(t, "AC-1: El usuario puede exportar la factura en PDF", criteria.Items[0])
		assert.Equal(t, "AC-3: Un error de exportación se muestra sin perder el formulario", criteria.Items[2])
	})

	t.Run("a nested bullet extends the criterion above it", func(t *testing.T) {
		// A sub-case qualifies the rule it sits under. Promoting it produces a criterion that reads
		// as a fragment, and the model then judges the fragment.
		criteria := tickets.ReadCriteria(tickets.FieldMap{
			tickets.FieldAcceptanceCriteria: "<ul>" +
				"<li>El usuario puede exportar la factura" +
				"<ul><li>en PDF</li><li>en CSV</li></ul></li>" +
				"<li>La exportación registra quién la pidió</li>" +
				"</ul>",
		}, order, nil)

		assert.Equal(t, tickets.ModeList, criteria.Mode)
		require.Len(t, criteria.Items, 2, "the two sub-bullets are not criteria of their own")
		assert.Contains(t, criteria.Items[0], "en PDF")
		assert.Contains(t, criteria.Items[0], "en CSV")
	})

	t.Run("prose is never split into numbered criteria", func(t *testing.T) {
		// Doing it by regex cuts rules in half, and the model then reports failures that belong to
		// the splitting rather than to the work.
		criteria := tickets.ReadCriteria(tickets.FieldMap{
			tickets.FieldDescription: "<p>Hay que permitir exportar la factura. Si el usuario no " +
				"tiene permiso, se muestra el mensaje de siempre y no se genera nada.</p>",
		}, order, nil)

		assert.Equal(t, tickets.ModeProse, criteria.Mode)
		assert.Empty(t, criteria.Items)
		assert.Contains(t, criteria.Markdown, "permitir exportar la factura")
	})

	t.Run("the empty box falls through to the description", func(t *testing.T) {
		criteria := tickets.ReadCriteria(tickets.FieldMap{
			tickets.FieldAcceptanceCriteria: emptyCriteriaField,
			tickets.FieldDescription: "<p>El reporte mensual se genera el primer día hábil y se " +
				"envía a la lista de finanzas.</p>",
		}, order, nil)

		require.NotNil(t, criteria.Field)
		assert.Equal(t, tickets.FieldDescription, *criteria.Field,
			"a field of 20 characters of markup and one of content is not a requirement")
	})

	t.Run("nothing usable is mode none, and says so with no field", func(t *testing.T) {
		criteria := tickets.ReadCriteria(tickets.FieldMap{
			tickets.FieldAcceptanceCriteria: emptyCriteriaField,
			tickets.FieldDescription:        "",
		}, order, nil)

		assert.Equal(t, tickets.ModeNone, criteria.Mode)
		assert.Nil(t, criteria.Field)
		assert.Empty(t, criteria.Items, "never nil: the renderer maps over this")
		assert.NotNil(t, criteria.Items)
	})

	t.Run("the configured field order wins", func(t *testing.T) {
		custom := "Custom.CriteriosDeAceptacion"
		criteria := tickets.ReadCriteria(tickets.FieldMap{
			custom:                   "<p>Lo que este equipo escribe en su propio campo de criterios.</p>",
			tickets.FieldDescription: "<p>Una descripción larga que no son los criterios de nada.</p>",
		}, tickets.CriteriaFieldOrder(&custom), nil)

		require.NotNil(t, criteria.Field)
		assert.Equal(t, custom, *criteria.Field)
	})
}

// `WI-008`: a field repeated across work items of the same type is the refinement form, not its
// answers.
func TestIsTemplate(t *testing.T) {
	form := "<div>Dado: <br>Cuando: <br>Entonces: </div>"

	t.Run("a field identical to another ticket's is the form", func(t *testing.T) {
		assert.True(t, tickets.IsTemplate(form, []string{form}))
	})

	t.Run("markup and case do not make two copies different", func(t *testing.T) {
		assert.True(t, tickets.IsTemplate(form,
			[]string{"<p>dado:  <br/>  cuando: <br>  ENTONCES: </p>"}))
	})

	t.Run("with no corpus nothing is excluded", func(t *testing.T) {
		// Guessing without a corpus would drop a real requirement the first time a board is used,
		// which is exactly when nobody would suspect the extraction.
		assert.False(t, tickets.IsTemplate(form, nil))
	})

	t.Run("a real requirement is not a form", func(t *testing.T) {
		assert.False(t, tickets.IsTemplate(
			"<p>El usuario puede exportar la factura en PDF.</p>", []string{form}))
	})

	t.Run("only the first twenty others are compared", func(t *testing.T) {
		others := make([]string, 0, 25)
		for range 20 {
			others = append(others, "<p>algo distinto que no es el formulario en absoluto</p>")
		}
		others = append(others, form)

		assert.False(t, tickets.IsTemplate(form, others),
			"the twenty-first is past the comparison window")
	})
}

// The extraction is skipped for a template, which is what makes a board of identical forms fall
// through to the description instead of judging the form.
func TestATemplateFieldIsSkipped(t *testing.T) {
	form := "<div>Dado: <br>Cuando: <br>Entonces: </div>"

	criteria := tickets.ReadCriteria(
		tickets.FieldMap{
			tickets.FieldAcceptanceCriteria: form,
			tickets.FieldDescription:        "<p>Lo que realmente pide este work item, en prosa.</p>",
		},
		[]string{tickets.FieldAcceptanceCriteria, tickets.FieldDescription},
		map[string][]string{tickets.FieldAcceptanceCriteria: {form}},
	)

	require.NotNil(t, criteria.Field)
	assert.Equal(t, tickets.FieldDescription, *criteria.Field)
}

func TestCriteriaFieldOrder(t *testing.T) {
	blank := "   "
	configured := " Custom.Uno , Custom.Dos "

	assert.Equal(t,
		[]string{tickets.FieldAcceptanceCriteria, tickets.FieldDescription},
		tickets.CriteriaFieldOrder(nil))
	assert.Equal(t,
		[]string{tickets.FieldAcceptanceCriteria, tickets.FieldDescription},
		tickets.CriteriaFieldOrder(&blank),
		"a cleared setting means the default, not an empty order")
	assert.Equal(t, []string{"Custom.Uno", "Custom.Dos"}, tickets.CriteriaFieldOrder(&configured))
}
