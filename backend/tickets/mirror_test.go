package tickets_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/tickets"
)

func sampleContent() tickets.MirrorContent {
	assigned := "Ada Lovelace"
	field := tickets.FieldDescription

	return tickets.MirrorContent{
		Ticket: tickets.Ticket{
			ID:           "azure:contoso:Payments:1234",
			Org:          "contoso",
			Project:      "Payments",
			ExternalID:   "1234",
			Title:        "Exportar la factura",
			State:        "Active",
			WorkItemType: "User Story",
			AssignedTo:   &assigned,
			WebURL:       "https://dev.azure.com/contoso/Payments/_workitems/edit/1234",
		},
		Markdown: "El usuario puede exportar la factura desde el detalle.",
		Criteria: tickets.Criteria{
			Mode:     tickets.ModeProse,
			Field:    &field,
			Markdown: "El usuario puede exportar la factura desde el detalle.",
			Items:    []string{},
		},
		RawJSON: `{"id":1234}`,
	}
}

// The load-bearing test of this feature: this is the first thing that writes into a directory a
// person also uses, and not touching their files is the promise made to them.
func TestAnythingTheUserPutInTheDirectorySurvivesAResync(t *testing.T) {
	directory := t.TempDir()

	require.NoError(t, tickets.WriteMirror(directory, sampleContent()))

	// What a user puts there between syncs: a note, a directory of their own, and a file dropped
	// into `attachments/` by hand.
	require.NoError(t, os.WriteFile(filepath.Join(directory, "notes", "pendientes.md"),
		[]byte("preguntar por el IVA"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(directory, "mis-cosas"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "mis-cosas", "diagrama.txt"),
		[]byte("un diagrama"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "CONTEXTO.md"),
		[]byte("lo que me contaron en la reunión"), 0o600))

	// A second sync, with a different title and different content.
	second := sampleContent()
	second.Ticket.Title = "Exportar la factura (v2)"
	second.Markdown = "El usuario puede exportar la factura en PDF y en CSV."
	require.NoError(t, tickets.WriteMirror(directory, second))

	for path, expected := range map[string]string{
		filepath.Join("notes", "pendientes.md"):    "preguntar por el IVA",
		filepath.Join("mis-cosas", "diagrama.txt"): "un diagrama",
		"CONTEXTO.md": "lo que me contaron en la reunión",
	} {
		body, err := os.ReadFile(filepath.Join(directory, path))
		require.NoError(t, err, "%s must survive a resync", path)
		assert.Equal(t, expected, string(body))
	}

	// And the four it does own were rewritten.
	ticket, err := os.ReadFile(filepath.Join(directory, "ticket.md"))
	require.NoError(t, err)
	assert.Contains(t, string(ticket), "Exportar la factura (v2)")
	assert.Contains(t, string(ticket), "PDF y en CSV")
}

// `notes/` is created empty on first sync and never written to again.
func TestWriteMirrorCreatesNotesEmptyAndLeavesItAlone(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, tickets.WriteMirror(directory, sampleContent()))

	entries, err := os.ReadDir(filepath.Join(directory, "notes"))
	require.NoError(t, err)
	assert.Empty(t, entries, "the one directory in here nobody but the user owns")
}

func TestWriteMirrorOwnsFourNamesAndNoMore(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, tickets.WriteMirror(directory, sampleContent()))

	entries, err := os.ReadDir(directory)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	assert.ElementsMatch(t,
		[]string{"ticket.md", "acceptance-criteria.md", "raw.json", "attachments", "notes"},
		names)
}

// Azure keys attachments by GUID, so one work item can legitimately carry two files called
// `captura.png`. Both survive.
func TestTwoAttachmentsSharingANameBothSurvive(t *testing.T) {
	directory := t.TempDir()

	content := sampleContent()
	content.Markdown = "Ver ![primera](https://dev.azure.test/a/uno) y ![segunda](https://dev.azure.test/a/dos)."
	content.Attachments = []tickets.Attachment{
		{Name: "captura.png", URL: "https://dev.azure.test/a/uno", Content: []byte("primera")},
		{Name: "captura.png", URL: "https://dev.azure.test/a/dos", Content: []byte("segunda")},
	}
	require.NoError(t, tickets.WriteMirror(directory, content))

	first, err := os.ReadFile(filepath.Join(directory, "attachments", "captura.png"))
	require.NoError(t, err)
	assert.Equal(t, "primera", string(first))

	second, err := os.ReadFile(filepath.Join(directory, "attachments", "captura-2.png"))
	require.NoError(t, err)
	assert.Equal(t, "segunda", string(second))

	// And both image sources point at the local copies.
	ticket, err := os.ReadFile(filepath.Join(directory, "ticket.md"))
	require.NoError(t, err)
	assert.Contains(t, string(ticket), "(attachments/captura.png)")
	assert.Contains(t, string(ticket), "(attachments/captura-2.png)")
}

// `WI-004`: an attachment that failed or did not fit is **named**, because a screenshot the model
// cannot see is a fact worth stating.
func TestAFailedAttachmentIsNamedRatherThanSilentlyAbsent(t *testing.T) {
	directory := t.TempDir()

	content := sampleContent()
	content.Attachments = []tickets.Attachment{
		{Name: "diagrama.png", URL: "https://dev.azure.test/a/uno", Failure: "no se pudo descargar"},
		{Name: "video.mp4", URL: "https://dev.azure.test/a/dos",
			Failure: "supera el presupuesto de adjuntos de esta sincronización"},
	}
	require.NoError(t, tickets.WriteMirror(directory, content))

	ticket, err := os.ReadFile(filepath.Join(directory, "ticket.md"))
	require.NoError(t, err)
	assert.Contains(t, string(ticket), "Adjuntos no disponibles")
	assert.Contains(t, string(ticket), "diagrama.png (no se pudo descargar)")
	assert.Contains(t, string(ticket), "video.mp4 (supera el presupuesto")
}

func TestAnAttachmentNoLongerOnTheWorkItemIsCleared(t *testing.T) {
	directory := t.TempDir()

	first := sampleContent()
	first.Attachments = []tickets.Attachment{
		{Name: "vieja.png", URL: "https://dev.azure.test/a/uno", Content: []byte("x")},
	}
	require.NoError(t, tickets.WriteMirror(directory, first))

	// The user drops their own file in there, and the attachment is removed on the board.
	require.NoError(t, os.MkdirAll(filepath.Join(directory, "attachments", "mias"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "attachments", "mias", "propia.png"),
		[]byte("mía"), 0o600))

	require.NoError(t, tickets.WriteMirror(directory, sampleContent()))

	_, err := os.Stat(filepath.Join(directory, "attachments", "vieja.png"))
	assert.True(t, os.IsNotExist(err), "an attachment the work item no longer has is cleared")

	// Emptied file by file, never by deleting the directory — so a directory the user made inside
	// it is still there.
	body, err := os.ReadFile(filepath.Join(directory, "attachments", "mias", "propia.png"))
	require.NoError(t, err)
	assert.Equal(t, "mía", string(body))
}

// `WI-016`: reading the notes is not writing them, and only text is read.
func TestReadNotes(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, tickets.WriteMirror(directory, sampleContent()))

	notes := filepath.Join(directory, "notes")
	require.NoError(t, os.WriteFile(filepath.Join(notes, "reunion.md"),
		[]byte("el IVA se calcula aparte"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(notes, "pendiente.txt"),
		[]byte("falta confirmar el formato"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(notes, "captura.png"),
		[]byte("\x89PNG binario"), 0o600))

	read := tickets.ReadNotes(directory)

	assert.Contains(t, read, "el IVA se calcula aparte")
	assert.Contains(t, read, "falta confirmar el formato")
	assert.NotContains(t, read, "PNG", "a screenshot is not something to paste into a prompt")

	// Nothing was created, deleted or modified by reading.
	entries, err := os.ReadDir(notes)
	require.NoError(t, err)
	assert.Len(t, entries, 3)
}

func TestReadNotesOnADirectoryWithNoNotes(t *testing.T) {
	assert.Empty(t, tickets.ReadNotes(t.TempDir()))
	assert.Empty(t, tickets.ReadNotes(""))
}
