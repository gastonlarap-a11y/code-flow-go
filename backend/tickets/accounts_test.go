package tickets_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/tickets"
	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
)

// `WI-005`: the organisation is workspace → project → the single connection → none, and the board
// project follows the same "explicit choice wins" order.
func TestResolveAccount(t *testing.T) {
	tests := []struct {
		name        string
		project     workspaces.Project
		workspace   workspaces.Workspace
		connections []string
		org         *string
		board       *string
		source      string
	}{
		{
			name:      "the workspace's choice wins over the repository's link",
			project:   workspaces.Project{ADOOrg: new("desde-el-repo"), ADOProject: new("Repo")},
			workspace: workspaces.Workspace{ADOOrg: new("elegida"), ADOProject: new("Tablero")},
			org:       new("elegida"),
			board:     new("Tablero"),
			source:    tickets.SourceWorkspace,
		},
		{
			name:      "with nothing chosen, the repository's own link answers",
			project:   workspaces.Project{ADOOrg: new("desde-el-repo"), ADOProject: new("Repo")},
			workspace: workspaces.Workspace{},
			org:       new("desde-el-repo"),
			board:     new("Repo"),
			source:    tickets.SourceProject,
		},
		{
			name: "a GitHub-hosted repository takes its board from the workspace alone",
			// This is the defect that proved the board project needs a column of its own: the
			// organisation resolved, the module rendered, and the picker then asked for the very
			// thing the user had just configured.
			project:   workspaces.Project{GitHubOwner: new("acme"), GitHubRepo: new("web")},
			workspace: workspaces.Workspace{ADOOrg: new("elegida"), ADOProject: new("Tablero")},
			org:       new("elegida"),
			board:     new("Tablero"),
			source:    tickets.SourceWorkspace,
		},
		{
			name:        "exactly one configured connection decides it",
			project:     workspaces.Project{},
			workspace:   workspaces.Workspace{},
			connections: []string{"la-unica"},
			org:         new("la-unica"),
			source:      tickets.SourceOnlyConnection,
		},
		{
			name:      "several connections decide nothing",
			project:   workspaces.Project{},
			workspace: workspaces.Workspace{},
			// Picking the first would read the wrong board and show an empty list, which reads as
			// "this sprint has nothing in it" rather than as "this is the wrong board".
			connections: []string{"una", "otra"},
			source:      tickets.SourceNone,
		},
		{
			name:      "nothing at all is none, and the board goes with it",
			project:   workspaces.Project{ADOProject: new("Huérfano")},
			workspace: workspaces.Workspace{},
			source:    tickets.SourceNone,
		},
		{
			name:      "a blank column counts as unset",
			project:   workspaces.Project{ADOOrg: new("   ")},
			workspace: workspaces.Workspace{ADOOrg: new("")},
			source:    tickets.SourceNone,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			account := tickets.ResolveAccount(test.project, test.workspace, test.connections)

			assert.Equal(t, test.source, account.Source)
			if test.org == nil {
				assert.Nil(t, account.Org)
			} else {
				require.NotNil(t, account.Org)
				assert.Equal(t, *test.org, *account.Org)
			}
			if test.board == nil {
				assert.Nil(t, account.Project)
			} else {
				require.NotNil(t, account.Project)
				assert.Equal(t, *test.board, *account.Project)
			}
		})
	}
}

func TestADOConnections(t *testing.T) {
	tests := []struct {
		name     string
		setting  *string
		expected []string
	}{
		{
			name:     "the object form Settings writes",
			setting:  new(`[{"org":"contoso","pat_saved":true},{"org":"acme"}]`),
			expected: []string{"contoso", "acme"},
		},
		{
			name:     "a bare list of names",
			setting:  new(`["contoso","acme"]`),
			expected: []string{"contoso", "acme"},
		},
		{
			name: "malformed JSON reads as no connections rather than throwing",
			// This is called to decide which board to show, and a JSON error would take down a panel
			// over a stored string nobody can see to fix.
			setting:  new(`{"org": "contoso"`),
			expected: nil,
		},
		{
			name:     "an unset setting",
			setting:  nil,
			expected: nil,
		},
		{
			name:     "an empty list",
			setting:  new(`[]`),
			expected: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connections := tickets.ADOConnections(test.setting)

			// Emptiness is the contract, not nil-versus-empty: this value never crosses to the
			// renderer, and its one caller asks whether there is exactly one connection.
			if len(test.expected) == 0 {
				assert.Empty(t, connections)
				return
			}
			assert.Equal(t, test.expected, connections)
		})
	}
}
