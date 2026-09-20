package tickets_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/tickets"
)

// `WI-006`: the branch heuristic is a suggestion, never a link.
func TestSuggestForBranch(t *testing.T) {
	tests := []struct {
		name     string
		branch   string
		provider string
		id       string
	}{
		{
			name:     "Azure's own AB# reference",
			branch:   "feature/AB#1234-exportar-factura",
			provider: "azure",
			id:       "1234",
		},
		{
			name:     "a Jira key in upper case",
			branch:   "feature/CORE-45-exportar",
			provider: "jira",
			id:       "CORE-45",
		},
		{
			name:   "a lower-case Jira-shaped string is not a key",
			branch: "feature/utf-8-encoding",
			// Accepting lower case matches `utf-8` here and suggests a work item called `UTF-8`.
			// The leading-number rule does not fire either: the last segment starts with a letter.
			provider: "",
		},
		{
			name:     "a number opening the last segment",
			branch:   "feature/1234-exportar-factura",
			provider: "azure",
			id:       "1234",
		},
		{
			name:   "a number that does not open the last segment is not an id",
			branch: "feature/exportar-1234",
			// Otherwise a repository whose branches live under `2025/` would suggest the year on
			// every one of them.
			provider: "",
		},
		{
			name:     "a date-led branch resolves to a work item, and that is accepted",
			branch:   "release/2025-cleanup",
			provider: "azure",
			// An accepted false positive, pinned on purpose: nothing in the name separates a year
			// from a work-item number, and rejecting four-digit ids would reject the common case.
			id: "2025",
		},
		{
			name:     "a bare number",
			branch:   "1234",
			provider: "azure",
			id:       "1234",
		},
		{
			name:     "nothing to go on",
			branch:   "main",
			provider: "",
		},
		{
			name:     "an empty branch name",
			branch:   "   ",
			provider: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			suggestion := tickets.SuggestForBranch(test.branch)

			if test.provider == "" {
				assert.Nil(t, suggestion)
				return
			}
			require.NotNil(t, suggestion)
			assert.Equal(t, test.provider, suggestion.Provider)
			assert.Equal(t, test.id, suggestion.ExternalID)
		})
	}
}
