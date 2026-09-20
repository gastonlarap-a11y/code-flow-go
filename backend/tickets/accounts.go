package tickets

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
)

// Which account a project's tickets come from (WI-005).

// The four ways an organisation can be decided, `VERBATIM` — the UI branches on the word.
const (
	SourceWorkspace      = "workspace"
	SourceProject        = "project"
	SourceOnlyConnection = "only_connection"
	SourceNone           = "none"
)

// Account is the organisation and board project a project's work items come from, and how that was
// decided. `source: "none"` means it was **not**: the UI has to ask, because guessing shows the
// wrong board's (empty) list and blames the board.
type Account struct {
	Org     *string `json:"org"`
	Project *string `json:"project"`
	Source  string  `json:"source"`
}

// ResolveAccount decides the organisation and the board project for one repository (WI-005).
//
// The organisation is `workspaces.ado_org` → `projects.ado_org` → the single configured connection →
// `none`. The board project follows the same "explicit choice wins" order: `workspaces.ado_project`
// → `projects.ado_project`. Neither is inferred from the repository once a workspace has chosen: a
// board can live in a different organisation from the code, which is what having work and personal
// projects in one install looks like.
//
// **The board project needs a column of its own, and a real defect proved it.** It used to come only
// from `projects.ado_project`, which is filled when the repository is linked to an Azure repository.
// A repository hosted on GitHub has none — so the organisation resolved, the module rendered, and
// the ticket picker then failed with *"choose an account in Settings"*: the very thing the user had
// just done.
func ResolveAccount(project workspaces.Project, workspace workspaces.Workspace, connections []string) Account {
	boardProject := firstSet(workspace.ADOProject, project.ADOProject)

	if org := text(workspace.ADOOrg); org != "" {
		return Account{Org: &org, Project: boardProject, Source: SourceWorkspace}
	}
	if org := text(project.ADOOrg); org != "" {
		return Account{Org: &org, Project: boardProject, Source: SourceProject}
	}
	// Exactly one connection, and only then. Picking the first of several would read the wrong board
	// and show an empty list, which reads as "this sprint has nothing in it".
	if len(connections) == 1 && strings.TrimSpace(connections[0]) != "" {
		org := strings.TrimSpace(connections[0])
		return Account{Org: &org, Project: boardProject, Source: SourceOnlyConnection}
	}

	// No organisation means no account, and the board project goes with it: a project name without
	// the organisation it was listed from addresses nothing.
	return Account{Source: SourceNone}
}

// ADOConnections reads the configured organisations out of the `ado_connections` setting.
//
// A malformed setting reads as **no connections** rather than throwing: this is called to decide
// which board to show, and a JSON error there would take down a panel over a stored string nobody
// can see to fix.
func ADOConnections(setting *string) []string {
	if setting == nil || strings.TrimSpace(*setting) == "" {
		return nil
	}

	// Two shapes are accepted because two have been written: a list of objects with an `org`, and a
	// bare list of names.
	var objects []struct {
		Org string `json:"org"`
	}
	if err := json.Unmarshal([]byte(*setting), &objects); err == nil {
		orgs := make([]string, 0, len(objects))
		for _, entry := range objects {
			if org := strings.TrimSpace(entry.Org); org != "" {
				orgs = append(orgs, org)
			}
		}
		if len(orgs) > 0 {
			return orgs
		}
	}

	var names []string
	if err := json.Unmarshal([]byte(*setting), &names); err != nil {
		return nil
	}
	orgs := make([]string, 0, len(names))
	for _, name := range names {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			orgs = append(orgs, trimmed)
		}
	}
	return orgs
}

// Workspaces is what account resolution needs of the store. Declared at the consumer, as everything
// else here is.
type Workspaces interface {
	GetProject(ctx context.Context, id string) (workspaces.Project, error)
	GetWorkspace(ctx context.Context, id string) (workspaces.Workspace, error)
	GetSetting(ctx context.Context, key string) (*string, error)
	SetWorkspaceTicketAccount(ctx context.Context, workspaceID string, org, project *string) error
}

// firstSet is the "explicit choice wins" order, with blank counting as unset.
func firstSet(values ...*string) *string {
	for _, value := range values {
		if trimmed := text(value); trimmed != "" {
			return &trimmed
		}
	}
	return nil
}

func text(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
