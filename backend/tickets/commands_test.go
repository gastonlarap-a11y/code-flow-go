package tickets_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/activity"
	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/gastonlarap-a11y/code-flow/backend/providers"
	"github.com/gastonlarap-a11y/code-flow/backend/tickets"
	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
)

// ---- the fakes ---------------------------------------------------------------------------------

type fakeWorkspaces struct {
	project   workspaces.Project
	workspace workspaces.Workspace
	settings  map[string]string
	// written records what `update_workspace_ticket_account` stored.
	written []string
	err     error
}

func (f *fakeWorkspaces) GetProject(_ context.Context, id string) (workspaces.Project, error) {
	if f.err != nil {
		return workspaces.Project{}, f.err
	}
	if f.project.ID != id {
		return workspaces.Project{}, errors.New("no such project")
	}
	return f.project, nil
}

func (f *fakeWorkspaces) GetWorkspace(_ context.Context, _ string) (workspaces.Workspace, error) {
	return f.workspace, nil
}

func (f *fakeWorkspaces) GetSetting(_ context.Context, key string) (*string, error) {
	value, found := f.settings[key]
	if !found {
		return nil, nil
	}
	return &value, nil
}

func (f *fakeWorkspaces) SetWorkspaceTicketAccount(_ context.Context, id string, org, project *string) error {
	f.written = append(f.written, id+"|"+optional(org)+"|"+optional(project))
	return nil
}

func optional(value *string) string {
	if value == nil {
		return "<nil>"
	}
	return *value
}

type fakeCredentials struct {
	pat string
	err error
}

func (f fakeCredentials) ADOPAT(string) (string, error) { return f.pat, f.err }

type fakeReviewer struct {
	analyzed *ai.AnalyzeRequest
	judged   *ai.TicketReviewRequest
	answer   string
	err      error
}

// Both halves mirror the real operations' one guard: an empty diff is refused **before** the model
// is invoked (`AI-024`). A fake that answered anyway would let this package's "no row is written for
// a refusal" rule pass without ever reaching the refusal.
func (f *fakeReviewer) AnalyzeChanges(_ context.Context, _ string, request ai.AnalyzeRequest) (ai.Result, error) {
	f.analyzed = &request
	if strings.TrimSpace(request.Diff) == "" {
		return ai.Result{}, ai.ErrNothingToAnalyze
	}
	return ai.Result{Text: f.answer}, f.err
}

func (f *fakeReviewer) ReviewAgainstTicket(_ context.Context, _ string, request ai.TicketReviewRequest) (ai.Result, error) {
	f.judged = &request
	if strings.TrimSpace(request.Diff) == "" {
		return ai.Result{}, ai.ErrNothingToAnalyze
	}
	return ai.Result{Text: f.answer}, f.err
}

type fakeActivity struct{ jobs []activity.NewJob }

func (f *fakeActivity) RecordJob(_ context.Context, job activity.NewJob) (activity.JobEntry, error) {
	f.jobs = append(f.jobs, job)
	return activity.JobEntry{ID: job.ID}, nil
}

func registryFor(t *testing.T, deps tickets.Deps) *bridge.Registry {
	t.Helper()
	registry := bridge.NewRegistry()
	tickets.Register(registry, deps)
	registry.Seal()
	return registry
}

// ---- the command surface ------------------------------------------------------------------------

// `WI-022` is asserted in both directions: the comment is registered, and no transition verb is.
//
// The second half is the load-bearing one. A comment is additive and anyone can delete it; a state
// transition moves a card other people are looking at, and its legal states belong to the project's
// process — `New`/`Active`/`Resolved` under Agile, `Committed`/`Done` under Scrum — which is not
// something this app can name from the outside.
func TestTheOnlyWriteIsAComment(t *testing.T) {
	registry := registryFor(t, tickets.Deps{})

	_, registered := registry.Lookup("comment_ticket")
	assert.True(t, registered, "the verdict reaches the board because somebody pressed a button")

	for _, verb := range []string{
		"transition_ticket", "set_ticket_state", "close_ticket", "resolve_ticket",
		"update_ticket", "assign_ticket", "move_ticket",
	} {
		_, found := registry.Lookup(verb)
		assert.False(t, found, "%s must not exist: a transition is not this app's to make", verb)
	}
}

func TestTheSeventeenCommandsAreRegistered(t *testing.T) {
	registry := registryFor(t, tickets.Deps{})

	for _, name := range []string{
		"update_workspace_ticket_account", "resolve_ticket_account", "resolve_ticket_link",
		"suggest_ticket_for_branch", "sync_ticket", "get_ticket", "list_tickets",
		"get_ticket_criteria", "link_branch_ticket", "unlink_branch_ticket", "ticket_for_branch",
		"list_sprint_tickets", "list_my_tickets", "preview_ticket", "list_ticket_reviews",
		"review_changes", "comment_ticket",
	} {
		_, registered := registry.Lookup(name)
		assert.True(t, registered, "%s", name)
	}
	assert.Equal(t, 17, registry.Len())
}

// invoke calls a command the way the bridge does: through the JSON the renderer would have sent, so
// the parameter names and their decoding are exercised rather than bypassed.
func invoke(t *testing.T, registry *bridge.Registry, name string, params map[string]any) (any, error) {
	t.Helper()

	handler, found := registry.Lookup(name)
	require.True(t, found, "%s is not registered", name)

	raw, err := json.Marshal(params)
	require.NoError(t, err)
	return handler(t.Context(), bridge.NewParams(raw))
}

// ---- the parsers behind two commands -------------------------------------------------------------

func TestResolveTicketLinkAnswersNullForAnythingThatIsNotAnAddress(t *testing.T) {
	registry := registryFor(t, tickets.Deps{})

	answer, err := invoke(t, registry, "resolve_ticket_link", map[string]any{
		"text": "https://example.test/not-a-board",
	})
	require.NoError(t, err)
	assert.Nil(t, answer, "`TicketLinkRef | null`")

	answer, err = invoke(t, registry, "resolve_ticket_link", map[string]any{
		"text": "https://dev.azure.com/contoso/Payments/_workitems/edit/1234",
	})
	require.NoError(t, err)

	address, ok := answer.(providers.WorkItemAddress)
	require.True(t, ok)
	assert.Equal(t, int64(1234), address.ID)
	require.NotNil(t, address.Org)
	assert.Equal(t, "contoso", *address.Org)
}

func TestSuggestTicketForBranchAnswersNullWhenThereIsNothingToGoOn(t *testing.T) {
	registry := registryFor(t, tickets.Deps{})

	answer, err := invoke(t, registry, "suggest_ticket_for_branch", map[string]any{"branch": "main"})
	require.NoError(t, err)
	assert.Nil(t, answer)
}

// ---- the account commands -------------------------------------------------------------------------

func TestResolveTicketAccountThrowsForAnUnknownProject(t *testing.T) {
	// A caller error, not a missing account: answering `none` would send the user to a settings
	// screen that cannot fix anything.
	registry := registryFor(t, tickets.Deps{Workspaces: &fakeWorkspaces{}})

	_, err := invoke(t, registry, "resolve_ticket_account", map[string]any{"projectId": "nope"})
	assert.Error(t, err)
}

func TestResolveTicketAccountReadsTheWorkspaceFirst(t *testing.T) {
	store := &fakeWorkspaces{
		project:   workspaces.Project{ID: "p1", WorkspaceID: "w1", ADOOrg: new("desde-el-repo")},
		workspace: workspaces.Workspace{ID: "w1", ADOOrg: new("elegida"), ADOProject: new("Tablero")},
	}
	registry := registryFor(t, tickets.Deps{Workspaces: store})

	answer, err := invoke(t, registry, "resolve_ticket_account", map[string]any{"projectId": "p1"})
	require.NoError(t, err)

	account, ok := answer.(tickets.Account)
	require.True(t, ok)
	assert.Equal(t, tickets.SourceWorkspace, account.Source)
	require.NotNil(t, account.Org)
	assert.Equal(t, "elegida", *account.Org)
}

func TestUpdateWorkspaceTicketAccountWritesBothColumnsTogether(t *testing.T) {
	store := &fakeWorkspaces{}
	registry := registryFor(t, tickets.Deps{Workspaces: store})

	_, err := invoke(t, registry, "update_workspace_ticket_account", map[string]any{
		"workspaceId": "w1", "org": "contoso", "project": nil,
	})
	require.NoError(t, err)
	// A project name without the organisation it was listed from addresses nothing, so the two are
	// written and cleared as a pair.
	assert.Equal(t, []string{"w1|contoso|<nil>"}, store.written)
}

// ---- the board commands -----------------------------------------------------------------------

func TestABoardCommandWithNoPATRefusesBeforeAnyRequest(t *testing.T) {
	registry := registryFor(t, tickets.Deps{
		Credentials: fakeCredentials{err: providers.ErrNoCredential},
	})

	_, err := invoke(t, registry, "list_my_tickets", map[string]any{
		"org": "contoso", "project": "Payments",
	})
	require.ErrorIs(t, err, tickets.ErrNoADOPAT)
}

func TestAStoredButEmptyPATIsTheSameStateAsNone(t *testing.T) {
	registry := registryFor(t, tickets.Deps{Credentials: fakeCredentials{pat: "   "}})

	_, err := invoke(t, registry, "list_my_tickets", map[string]any{
		"org": "contoso", "project": "Payments",
	})
	require.ErrorIs(t, err, tickets.ErrNoADOPAT)
}

func TestARefusedCredentialStoreCarriesItsSentinel(t *testing.T) {
	registry := registryFor(t, tickets.Deps{
		Credentials: fakeCredentials{err: providers.ErrCredentialRefused},
	})

	_, err := invoke(t, registry, "sync_ticket", map[string]any{
		"org": "contoso", "project": "Payments", "externalId": "1234",
	})
	require.Error(t, err)
	// Position 0, because the renderer matches it with `startsWith` and offers "reconnect" instead
	// of a retry that would fail identically.
	assert.True(t, strings.HasPrefix(err.Error(), "CREDENTIAL_REFUSED: "), err.Error())
}

func TestPreviewOfAHalfTypedIDIsNullRatherThanAnError(t *testing.T) {
	// The field is debounced and this runs on every keystroke: a bare id parses on its first digit,
	// and everything before that is not a work item rather than a failure.
	registry := registryFor(t, tickets.Deps{Credentials: fakeCredentials{pat: "x"}})

	answer, err := invoke(t, registry, "preview_ticket", map[string]any{
		"org": "contoso", "project": "Payments", "externalId": "CORE-",
	})
	require.NoError(t, err)
	assert.Nil(t, answer)
}

func TestSyncRefusesANonNumericExternalIDBeforeAnyRequest(t *testing.T) {
	registry := registryFor(t, tickets.Deps{Credentials: fakeCredentials{pat: "x"}})

	_, err := invoke(t, registry, "sync_ticket", map[string]any{
		"org": "contoso", "project": "Payments", "externalId": "CORE-45",
	})
	require.ErrorIs(t, err, tickets.ErrNotNumeric)
}

// ---- the cached reads --------------------------------------------------------------------------

func TestGetTicketAnswersNullForOneThatIsNotCached(t *testing.T) {
	store, _ := newStore(t)
	registry := registryFor(t, tickets.Deps{Store: store})

	answer, err := invoke(t, registry, "get_ticket", map[string]any{"ticketId": "azure:a:b:9"})
	require.NoError(t, err)
	assert.Nil(t, answer, "`Ticket | null`: not cached is a state, not a failure")
}

func TestTicketForBranchAnswersNullForAnUnlinkedBranch(t *testing.T) {
	store, _ := newStore(t)
	registry := registryFor(t, tickets.Deps{Store: store})

	answer, err := invoke(t, registry, "ticket_for_branch", map[string]any{
		"projectId": "p1", "branch": "feature/1234-exportar",
	})
	require.NoError(t, err)
	assert.Nil(t, answer)
}

func TestGetTicketCriteriaRecomputesFromTheCacheWithNoNetwork(t *testing.T) {
	store, _ := newStore(t)

	ticket, err := store.Upsert(t.Context(), sampleTicket("1234", "Exportar"), `{
		"fields": {
			"Microsoft.VSTS.Common.AcceptanceCriteria":
				"<ul><li>El usuario puede exportar la factura</li><li>La exportación lleva el IVA</li></ul>"
		}
	}`)
	require.NoError(t, err)

	// No credentials at all: this command must not reach the network.
	registry := registryFor(t, tickets.Deps{Store: store, Workspaces: &fakeWorkspaces{}})

	answer, err := invoke(t, registry, "get_ticket_criteria", map[string]any{"ticketId": ticket.ID})
	require.NoError(t, err)

	criteria, ok := answer.(tickets.Criteria)
	require.True(t, ok)
	assert.Equal(t, tickets.ModeList, criteria.Mode)
	assert.Len(t, criteria.Items, 2)
}

func TestGetTicketCriteriaOfAnUncachedTicketIsModeNone(t *testing.T) {
	store, _ := newStore(t)
	registry := registryFor(t, tickets.Deps{Store: store, Workspaces: &fakeWorkspaces{}})

	answer, err := invoke(t, registry, "get_ticket_criteria", map[string]any{"ticketId": "azure:a:b:9"})
	require.NoError(t, err)

	criteria, ok := answer.(tickets.Criteria)
	require.True(t, ok)
	assert.Equal(t, tickets.ModeNone, criteria.Mode)
	assert.NotNil(t, criteria.Items, "empty, never nil")
}

// ---- the one write -----------------------------------------------------------------------------

func TestCommentRefusesEachWrongStateByName(t *testing.T) {
	store, _ := newStore(t)

	azure, err := store.Upsert(t.Context(), sampleTicket("1234", "Exportar"), "{}")
	require.NoError(t, err)

	jira := sampleTicket("CORE-45", "Algo")
	jira.ID = tickets.ID("jira", "acme", "CORE", "CORE-45")
	jira.Provider = "jira"
	_, err = store.Upsert(t.Context(), jira, "{}")
	require.NoError(t, err)

	registry := registryFor(t, tickets.Deps{Store: store, Credentials: fakeCredentials{pat: "x"}})

	tests := []struct {
		name     string
		ticketID string
		body     string
		expected error
	}{
		{
			name:     "a blank body",
			ticketID: azure.ID,
			body:     "   ",
			expected: tickets.ErrEmptyComment,
		},
		{
			name:     "a ticket that is not an Azure work item",
			ticketID: jira.ID,
			body:     "el veredicto",
			expected: tickets.ErrNotAzureTicket,
		},
		{
			name:     "a ticket no longer in the workspace",
			ticketID: "azure:contoso:Payments:9999",
			body:     "el veredicto",
			expected: tickets.ErrNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := invoke(t, registry, "comment_ticket", map[string]any{
				"ticketId": test.ticketID, "body": test.body,
			})
			// Each refused **by name**: a publish that silently does nothing is the failure this
			// feature already made once with linking.
			assert.ErrorIs(t, err, test.expected)
		})
	}
}

// ---- review_changes ----------------------------------------------------------------------------

func TestReviewChangesReadsEveryParameterBeforeResolvingAnything(t *testing.T) {
	registry := registryFor(t, tickets.Deps{Workspaces: &fakeWorkspaces{}})

	_, err := invoke(t, registry, "review_changes", map[string]any{
		"projectId": "p1", "jobId": "j1", "branch": "feature/x", "scope": "working",
		// `withTicket` missing.
		"level": "completo",
	})
	require.Error(t, err)
	// A missing parameter is a disagreement between the two sides. Reporting it as a ticket state
	// would send the reader looking in the wrong place.
	assert.Contains(t, err.Error(), "withTicket")
	assert.NotContains(t, err.Error(), "TICKET_NOT_LINKED")
}

func TestReviewChangesWithNoLinkedTicketCarriesItsSentinel(t *testing.T) {
	store, _ := newStore(t)
	registry := registryFor(t, tickets.Deps{
		Store: store,
		Workspaces: &fakeWorkspaces{
			project: workspaces.Project{ID: "p1", WorkspaceID: "w1", LocalPath: t.TempDir()},
		},
		AI:    &fakeReviewer{answer: "# nada"},
		Paths: platform.NewPaths(t.TempDir()),
	})

	_, err := invoke(t, registry, "review_changes", map[string]any{
		"projectId": "p1", "jobId": "j1", "branch": "feature/x", "scope": "working",
		"withTicket": true, "level": "completo",
	})
	require.Error(t, err)
	// A **state**, not a failure: the section shows how to link one, and the row does not stand in
	// for the branch's last real review.
	assert.True(t, strings.HasPrefix(err.Error(), "TICKET_NOT_LINKED: "), err.Error())
}
