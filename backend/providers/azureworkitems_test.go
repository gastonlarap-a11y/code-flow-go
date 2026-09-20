package providers_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/providers"
)

// The port of AzureWorkItemClientTests. Same harness as the pull-request client's, because the two
// share a transport on purpose: a refused PAT must be reported identically by both.

// `PROV-045`: the client cannot express a query without the project clause.
//
// The reason it is a rule and not a preference: the project segment in the URL does not reliably
// filter a WIQL query, and what happens without the clause **differs by organisation** — one
// answers 200 with zero rows on every project, another answers 200 with every work item it has.
// Neither is an error, and the first is indistinguishable from "this board is empty".
func TestWIQLAlwaysNamesTheProjectInItsWhereClause(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodPost, "/contoso/Payments/_apis/wit/wiql", http.StatusOK,
		`{"workItems":[{"id":12},{"id":34}]}`)

	ids, err := host.client("contoso").QueryIDs(t.Context(), "Payments", "", 0)
	require.NoError(t, err)
	assert.Equal(t, []int64{12, 34}, ids)

	var sent struct {
		Query string `json:"query"`
	}
	require.NoError(t, json.Unmarshal([]byte(host.last().Body), &sent))
	assert.Equal(t,
		"SELECT [System.Id] FROM WorkItems WHERE [System.TeamProject] = 'Payments'",
		sent.Query)
}

func TestWIQLAppendsAConditionInsideParentheses(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodPost, "/contoso/Payments/_apis/wit/wiql", http.StatusOK, `{"workItems":[]}`)

	_, err := host.client("contoso").QueryIDs(t.Context(), "Payments",
		"[System.AssignedTo] = @Me OR [System.State] = 'Active'", 200)
	require.NoError(t, err)

	var sent struct {
		Query string `json:"query"`
	}
	require.NoError(t, json.Unmarshal([]byte(host.last().Body), &sent))
	// The parentheses matter: without them an `OR` in the condition would escape the project clause
	// and the query would read the whole organisation.
	assert.Equal(t,
		"SELECT [System.Id] FROM WorkItems WHERE [System.TeamProject] = 'Payments' "+
			"AND ([System.AssignedTo] = @Me OR [System.State] = 'Active')",
		sent.Query)
	assert.Contains(t, host.last().Query, "$top=200")
}

// A project name containing `'` is escaped by doubling, as WIQL requires. Unescaped it is a syntax
// error surfacing as an opaque 400.
func TestAProjectNameWithAQuoteIsEscapedForWIQL(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodPost, "/contoso/O%27Brien/_apis/wit/wiql", http.StatusOK, `{"workItems":[]}`)

	_, err := host.client("contoso").QueryIDs(t.Context(), "O'Brien", "", 0)
	require.NoError(t, err)

	var sent struct {
		Query string `json:"query"`
	}
	require.NoError(t, json.Unmarshal([]byte(host.last().Body), &sent))
	assert.Contains(t, sent.Query, `[System.TeamProject] = 'O''Brien'`)
}

// `PROV-046`: two endpoints are preview-only, at **different** suffixes. A plain 7.1 is rejected on
// both with a 400 demanding the suffix, and these are the two literals most likely to be "tidied"
// into consistency with their neighbours.
func TestTheTwoPreviewEndpointsKeepTheirOwnSuffixes(t *testing.T) {
	t.Run("comments are 7.1-preview.4", func(t *testing.T) {
		host := newFakeAzure(t)
		host.answer(http.MethodGet, "/contoso/Payments/_apis/wit/workItems/1234/comments",
			http.StatusOK, `{"comments":[]}`)

		_, err := host.client("contoso").Comments(t.Context(), "Payments", 1234)
		require.NoError(t, err)
		assert.Contains(t, host.last().Query, "api-version=7.1-preview.4")
	})

	t.Run("an iteration's work items are 7.1-preview.1", func(t *testing.T) {
		host := newFakeAzure(t)
		host.answer(http.MethodGet,
			"/contoso/Payments/Equipo/_apis/work/teamsettings/iterations/it-1/workitems",
			http.StatusOK, `{"workItemRelations":[]}`)

		_, err := host.client("contoso").IterationWorkItems(t.Context(), "Payments", "Equipo", "it-1")
		require.NoError(t, err)
		assert.Contains(t, host.last().Query, "api-version=7.1-preview.1")
	})

	t.Run("everything else is plain 7.1", func(t *testing.T) {
		host := newFakeAzure(t)
		host.answer(http.MethodGet, "/contoso/Payments/_apis/wit/workitems/1234",
			http.StatusOK, `{"id":1234,"rev":3,"fields":{}}`)

		_, err := host.client("contoso").GetWorkItem(t.Context(), "Payments", 1234)
		require.NoError(t, err)
		assert.Contains(t, host.last().Query, "api-version=7.1")
		assert.NotContains(t, host.last().Query, "preview")
	})
}

// `PROV-047`: a work item's fields cannot be a record. Azure keys every field by reference name, a
// customised process adds its own, and a fixed shape would drop every one of them — which is where
// a team's acceptance criteria live.
func TestAWorkItemKeepsEveryCustomField(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Payments/_apis/wit/workitems/1234", http.StatusOK, `{
		"id": 1234,
		"rev": 7,
		"fields": {
			"System.Title": "Exportar la factura",
			"System.State": "Active",
			"System.CommentCount": 4,
			"System.AssignedTo": {"displayName": "Ada Lovelace", "uniqueName": "ada@contoso.test"},
			"Custom.8f3a1c2e": "<div>criterios del equipo</div>"
		}
	}`)

	item, err := host.client("contoso").GetWorkItem(t.Context(), "Payments", 1234)
	require.NoError(t, err)

	assert.Equal(t, int64(1234), item.ID)
	assert.Equal(t, int64(7), item.Rev)
	assert.Equal(t, "Exportar la factura", item.Text("System.Title"))
	assert.Equal(t, "<div>criterios del equipo</div>", item.Text("Custom.8f3a1c2e"),
		"a GUID-named custom field survives")

	t.Run("an identity field is read as one", func(t *testing.T) {
		assert.Equal(t, "Ada Lovelace", item.Identity("System.AssignedTo"))
	})

	t.Run("reading a non-string field as text answers nothing rather than failing", func(t *testing.T) {
		// `System.CommentCount` is a number. Losing the whole work item over it would be the wrong
		// trade, since nothing here needs that field.
		assert.Empty(t, item.Text("System.CommentCount"))
		assert.Empty(t, item.Text("System.NoSuchField"))
	})

	t.Run("the expand is asked for", func(t *testing.T) {
		assert.Contains(t, host.last().Query, "$expand=all")
	})
}

// The batch is capped at 200 by the server, so more than that is chunked rather than rejected.
func TestTheBatchIsChunkedAtTwoHundred(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodPost, "/contoso/Payments/_apis/wit/workitemsbatch", http.StatusOK,
		`{"value":[{"id":1,"fields":{"System.Title":"uno"}}]}`)

	ids := make([]int64, 0, 201)
	for i := range 201 {
		ids = append(ids, int64(i+1))
	}

	items, err := host.client("contoso").BatchWorkItems(t.Context(), "Payments", ids,
		[]string{"System.Title"})
	require.NoError(t, err)
	assert.Len(t, items, 2, "one row per chunk, from a fake that answers one each time")

	requests := host.seen()
	require.Len(t, requests, 2)

	var first struct {
		IDs         []int64  `json:"ids"`
		ErrorPolicy string   `json:"errorPolicy"`
		Fields      []string `json:"fields"`
	}
	require.NoError(t, json.Unmarshal([]byte(requests[0].Body), &first))
	assert.Len(t, first.IDs, 200)
	assert.Equal(t, []string{"System.Title"}, first.Fields)
	// Without `omit`, a single work item the PAT cannot see fails the whole request and a sprint
	// list goes blank because of one card.
	assert.Equal(t, "omit", first.ErrorPolicy)

	var second struct {
		IDs []int64 `json:"ids"`
	}
	require.NoError(t, json.Unmarshal([]byte(requests[1].Body), &second))
	assert.Equal(t, []int64{201}, second.IDs)
}

func TestAnIterationsWorkItemsComeBackOnceEach(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet,
		"/contoso/Payments/Equipo/_apis/work/teamsettings/iterations/it-1/workitems",
		http.StatusOK, `{"workItemRelations":[
			{"target":{"id":10}},
			{"target":{"id":11}},
			{"target":{"id":10}}
		]}`)

	ids, err := host.client("contoso").IterationWorkItems(t.Context(), "Payments", "Equipo", "it-1")
	require.NoError(t, err)
	// The endpoint answers `(parent, child)` pairs, so a work item reached as somebody's child and
	// as its own row appears twice.
	assert.Equal(t, []int64{10, 11}, ids)
}

func TestAzureSaysWhichIterationIsCurrent(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Payments/Equipo/_apis/work/teamsettings/iterations",
		http.StatusOK, `{"value":[
			{"id":"it-0","name":"Sprint 1","attributes":{"timeFrame":"past"}},
			{"id":"it-1","name":"Sprint 2","attributes":{"timeFrame":"current"}},
			{"id":"it-2","name":"Sprint 3","attributes":{"timeFrame":"future"}}
		]}`)

	iterations, err := host.client("contoso").TeamIterations(t.Context(), "Payments", "Equipo")
	require.NoError(t, err)
	require.Len(t, iterations, 3)

	// Azure says so itself, rather than leaving it to a date comparison against this machine's own
	// clock and time zone.
	assert.False(t, iterations[0].Current())
	assert.True(t, iterations[1].Current())
	assert.False(t, iterations[2].Current())
}

// `WI-022`: the one write, and it is a comment. The body is already HTML, because Azure comments are
// rich text.
func TestAddCommentIsTheOnlyWrite(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodPost, "/contoso/Payments/_apis/wit/workItems/1234/comments",
		http.StatusOK, `{"id":9,"text":"<div>hola</div>"}`)

	comment, err := host.client("contoso").AddComment(t.Context(), "Payments", 1234, "<div>hola</div>")
	require.NoError(t, err)
	assert.Equal(t, int64(9), comment.ID)

	sent := host.last()
	assert.Equal(t, http.MethodPost, sent.Method)
	assert.Contains(t, sent.Query, "api-version=7.1-preview.4")

	var body struct {
		Text string `json:"text"`
	}
	require.NoError(t, json.Unmarshal([]byte(sent.Body), &body))
	assert.Equal(t, "<div>hola</div>", body.Text)
}

func TestAnAttachmentIsFetchedWithItsNameAndTheDownloadFlag(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/_apis/wit/attachments/the-guid", http.StatusOK, "los bytes")

	content, err := host.client("contoso").GetAttachment(t.Context(),
		"https://dev.azure.com/contoso/_apis/wit/attachments/the-guid", "captura de pantalla.png")
	require.NoError(t, err)
	assert.Equal(t, "los bytes", string(content))

	query := host.last().Query
	assert.Contains(t, query, "download=true")
	// The name is percent-encoded the same way every other segment is: a space in it would
	// otherwise break the URL.
	assert.Contains(t, query, "fileName=captura%20de%20pantalla.png")
}

// A refused PAT is reported here exactly as it is on the pull-request side — the two share the
// transport so they cannot drift.
func TestARefusedPATIsClassifiedOnTheBoardsClientToo(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Payments/_apis/wit/workitems/1234",
		http.StatusUnauthorized, "<!DOCTYPE html><html><body>sign in</body></html>")

	_, err := host.client("contoso").GetWorkItem(t.Context(), "Payments", 1234)
	require.Error(t, err)

	var azure *providers.AzureError
	require.ErrorAs(t, err, &azure)
	assert.True(t, azure.Unauthorized)
	assert.Contains(t, azure.Body, "sign-in page",
		"tens of kilobytes of markup do not go where an error toast goes")
}

func TestWorkItemWebURLIsTheBoardsNotTheAPIs(t *testing.T) {
	// The `url` Azure returns is the API's own, which renders JSON in a browser instead of the
	// board.
	assert.Equal(t,
		"https://dev.azure.com/contoso/Payments/_workitems/edit/1234",
		providers.WorkItemWebURL("https://dev.azure.com/contoso", "Payments", 1234))

	assert.True(t, strings.HasPrefix(
		providers.WorkItemWebURL("contoso", "Mi Proyecto", 7),
		"https://dev.azure.com/contoso/Mi%20Proyecto/_workitems/edit/7"))
}
