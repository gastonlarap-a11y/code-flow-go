package providers_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/providers"
)

// The port of WorkItemLinkTests. No fixture file exists for this unit, so the cases are the ones
// the original test names describe, written out.

func TestAWorkItemPageGivesUpAllThreeParts(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		org     string
		project string
		id      int64
	}{
		{
			name:    "the current host",
			text:    "https://dev.azure.com/contoso/Web/_workitems/edit/1234",
			org:     "contoso",
			project: "Web",
			id:      1234,
		},
		{
			name:    "the legacy host keeps its subdomain as the organisation",
			text:    "https://contoso.visualstudio.com/Web/_workitems/edit/1234",
			org:     "contoso",
			project: "Web",
			id:      1234,
		},
		{
			name:    "a trailing slash",
			text:    "https://dev.azure.com/contoso/Web/_workitems/edit/1234/",
			org:     "contoso",
			project: "Web",
			id:      1234,
		},
		{
			name:    "a fragment the portal appends",
			text:    "https://dev.azure.com/contoso/Web/_workitems/edit/1234#comments",
			org:     "contoso",
			project: "Web",
			id:      1234,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			address, ok := providers.ParseWorkItemLink(test.text)
			require.True(t, ok)
			require.NotNil(t, address.Org)
			require.NotNil(t, address.Project)
			assert.Equal(t, test.org, *address.Org)
			assert.Equal(t, test.project, *address.Project)
			assert.Equal(t, test.id, address.ID)
		})
	}
}

// A taskboard URL puts the id in the query, and the path names a board rather than the work item.
// This is the URL most likely to be in a clipboard, which is why the query is not discarded.
func TestATaskboardURLIsReadFromItsQueryNotItsPath(t *testing.T) {
	address, ok := providers.ParseWorkItemLink(
		"https://dev.azure.com/contoso/Web/_boards/board/t/Equipo/Stories/?workitem=77")
	require.True(t, ok)

	assert.Equal(t, int64(77), address.ID)
	require.NotNil(t, address.Org)
	require.NotNil(t, address.Project)
	assert.Equal(t, "contoso", *address.Org)
	assert.Equal(t, "Web", *address.Project)
}

func TestTheQueryIsReadWhateverItsCaseOrPosition(t *testing.T) {
	for _, url := range []string{
		"https://dev.azure.com/contoso/Web/_backlogs?view=Stories&workitem=77",
		"https://dev.azure.com/contoso/Web/_backlogs?WorkItem=77",
		"https://dev.azure.com/contoso/Web/_boards/board/t/Equipo/Stories?workitem=77&full=true",
	} {
		t.Run(url, func(t *testing.T) {
			address, ok := providers.ParseWorkItemLink(url)
			require.True(t, ok)
			assert.Equal(t, int64(77), address.ID)
		})
	}
}

func TestAProjectNameWithPercentEncodedSpacesComesBackDecoded(t *testing.T) {
	address, ok := providers.ParseWorkItemLink(
		"https://dev.azure.com/contoso/Marketing%20Website/_workitems/edit/9")
	require.True(t, ok)
	require.NotNil(t, address.Project)

	assert.Equal(t, "Marketing Website", *address.Project)
	assert.Equal(t, int64(9), address.ID)
}

func TestABareIDIsAcceptedAndLeavesTheRestToBeFilledIn(t *testing.T) {
	for _, text := range []string{"1234", " 1234 ", "AB#1234", "ab#1234", "Ab#1234"} {
		t.Run(text, func(t *testing.T) {
			address, ok := providers.ParseWorkItemLink(text)
			require.True(t, ok)

			assert.Equal(t, int64(1234), address.ID)
			assert.Nil(t, address.Org, "a bare id names no organisation")
			assert.Nil(t, address.Project, "a bare id names no board project")
		})
	}
}

func TestAnOrganisationScopedLinkHasNoProjectToReport(t *testing.T) {
	address, ok := providers.ParseWorkItemLink("https://dev.azure.com/contoso/_workitems/edit/5")
	require.True(t, ok)

	assert.Equal(t, int64(5), address.ID)
	require.NotNil(t, address.Org)
	assert.Equal(t, "contoso", *address.Org)
	assert.Nil(t, address.Project)
}

func TestAnythingThatIsNotAWorkItemIsRefused(t *testing.T) {
	for _, text := range []string{
		"",
		"   ",
		"PROJ-45",
		"AB#PROJ-45",
		"0",
		"-3",
		"https://dev.azure.com/contoso/Web/_git/api/pullrequest/9",
		"https://dev.azure.com/contoso/Web/_workitems/edit/notanumber",
		"https://dev.azure.com/contoso/Web/_workitems",
		"https://dev.azure.com",
		"https://github.com/acme/widget/issues/42",
		"https://board.example.com/contoso/Web/_workitems/edit/5",
		"just some text/with a slash",
	} {
		t.Run(text, func(t *testing.T) {
			_, ok := providers.ParseWorkItemLink(text)
			assert.False(t, ok)
		})
	}
}
