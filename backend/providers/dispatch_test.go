package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/activity"
	"github.com/gastonlarap-a11y/code-flow/backend/providers"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
)

// The port of the ProviderIpcTests cases that dispatch: every pull-request command reaches one of
// two hosts, and which one is decided by the project row or by the pasted link. A command that
// dispatched to the wrong host would still answer — with somebody else's pull requests.

type fakeActivity struct {
	recorded []activity.NewJob
	err      error
}

func (f *fakeActivity) RecordJob(_ context.Context, job activity.NewJob) (activity.JobEntry, error) {
	f.recorded = append(f.recorded, job)
	if f.err != nil {
		return activity.JobEntry{}, f.err
	}
	return activity.JobEntry{
		ID: job.ID, ProjectID: job.ProjectID, Kind: job.Kind, Label: job.Label,
		Status: job.Status, Result: job.Result, Meta: job.Meta, CreatedAt: "2026-09-18T12:00:00Z",
	}, nil
}

// gitHubProject is a project row linked to the fake GitHub host.
func gitHubProject(host *fakeGitHub) workspaces.Project {
	hostname := host.hostname()
	return workspaces.Project{
		ID: "p1", WorkspaceID: "w1", Name: "Repo", LocalPath: "/repos/thing",
		GitHubOwner: text("acme"), GitHubRepo: text("widget"), GitHubHost: &hostname,
	}
}

// azureProject is a project row linked to the fake Azure host.
func azureProject() workspaces.Project {
	return workspaces.Project{
		ID: "p1", WorkspaceID: "w1", Name: "Repo", LocalPath: "/repos/thing",
		ADOOrg: text("contoso"), ADOProject: text("Dev"), ADORepoID: text("Dev.prueba"),
	}
}

// azureDeps points the provider commands at the fake Azure server.
func azureDeps(host *fakeAzure, project workspaces.Project) providers.Deps {
	return providers.Deps{
		Projects:    &fakeProjects{project: project},
		Credentials: fakeCredentials{adoOrgs: map[string]bool{"contoso": true}},
		Activity:    &fakeActivity{},
		HTTP: &http.Client{Transport: redirectToFake{
			target: host.server.URL, next: host.server.Client().Transport,
		}},
	}
}

// ---- which host answers (REVIEW-001, REVIEW-006) ----------------------------------------------

func TestAPullRequestListComesFromTheHostTheProjectIsLinkedTo(t *testing.T) {
	t.Run("github", func(t *testing.T) {
		host := newFakeGitHub(t)
		host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls", http.StatusOK, onePull)
		svc := newProviderService(t, providers.Deps{
			Projects:    &fakeProjects{project: gitHubProject(host)},
			Credentials: fakeCredentials{githubHosts: map[string]bool{host.hostname(): true}},
			HTTP:        host.server.Client(),
		})

		out := call(t, svc, "list_pull_requests", `{"projectId":"p1"}`)

		var pulls []providers.PullRequestSummary
		require.NoError(t, json.Unmarshal(out, &pulls))
		require.Len(t, pulls, 1)
		assert.Equal(t, providers.ProviderGitHub, pulls[0].Provider)
	})

	t.Run("azure", func(t *testing.T) {
		host := newFakeAzure(t)
		host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/Dev.prueba/pullrequests",
			http.StatusOK, `{"value":[`+azurePullBody+`]}`)
		svc := newProviderService(t, azureDeps(host, azureProject()))

		out := call(t, svc, "list_pull_requests", `{"projectId":"p1"}`)

		var pulls []providers.PullRequestSummary
		require.NoError(t, json.Unmarshal(out, &pulls))
		require.Len(t, pulls, 1)
		assert.Equal(t, providers.ProviderAzure, pulls[0].Provider)
	})
}

// A project carrying both links dispatches to GitHub, and the Azure side is not even consulted.
func TestAProjectWithBothLinksDispatchesToGitHub(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls", http.StatusOK, `[]`)

	project := gitHubProject(host)
	project.ADOOrg, project.ADOProject, project.ADORepoID = text("contoso"), text("Dev"), text("r")

	svc := newProviderService(t, providers.Deps{
		Projects:    &fakeProjects{project: project},
		Credentials: fakeCredentials{githubHosts: map[string]bool{host.hostname(): true}},
		HTTP:        host.server.Client(),
	})

	out := call(t, svc, "list_pull_requests", `{"projectId":"p1"}`)

	assert.Equal(t, "[]", string(out))
	assert.Equal(t, "/api/v3/repos/acme/widget/pulls", host.last().Path)
}

func TestAnUnlinkedProjectIsRefusedInTheWordsTheFrontendShows(t *testing.T) {
	svc := newProviderService(t, providers.Deps{
		Projects: &fakeProjects{project: workspaces.Project{ID: "p1"}},
	})

	for _, method := range []string{"list_pull_requests", "list_pr_comment_threads", "pr_review_decision"} {
		t.Run(method, func(t *testing.T) {
			_, err := svc.Invoke(t.Context(), method, json.RawMessage(`{"projectId":"p1","prId":1}`))

			require.Error(t, err)
			assert.Equal(t, "This project isn't linked to a pull-request host yet", err.Error())
		})
	}
}

func TestALinkedProjectWithNoSavedTokenNamesTheHost(t *testing.T) {
	host := newFakeGitHub(t)
	svc := newProviderService(t, providers.Deps{
		Projects:    &fakeProjects{project: gitHubProject(host)},
		Credentials: fakeCredentials{},
	})

	_, err := svc.Invoke(t.Context(), "list_pull_requests", json.RawMessage(`{"projectId":"p1"}`))

	assert.ErrorIs(t, err, providers.ErrNoGitHubToken)
}

func TestAnAzureLinkedProjectWithNoSavedPatNamesTheOrganisation(t *testing.T) {
	svc := newProviderService(t, providers.Deps{
		Projects:    &fakeProjects{project: azureProject()},
		Credentials: fakeCredentials{},
	})

	_, err := svc.Invoke(t.Context(), "list_pull_requests", json.RawMessage(`{"projectId":"p1"}`))

	assert.ErrorIs(t, err, providers.ErrNoADOPAT)
}

// A PAT the host refused is marked for the sidebar, which offers Settings instead of a retry that
// would fail identically (`XLANG-012`).
func TestListingPullRequestsMarksARefusedCredentialForTheSidebar(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/Dev.prueba/pullrequests",
		http.StatusUnauthorized, `{"message":"TF400813"}`)
	svc := newProviderService(t, azureDeps(host, azureProject()))

	_, err := svc.Invoke(t.Context(), "list_pull_requests", json.RawMessage(`{"projectId":"p1"}`))

	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), sentinel.CredentialRefused), err.Error())
	assert.Contains(t, err.Error(), "TF400813", "the host's own words survive behind the prefix")
}

// Any other failure carries no prefix: a 404 is a 404 on both hosts.
func TestAnyOtherHostFailureCarriesNoPrefix(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/Dev.prueba/pullrequests",
		http.StatusNotFound, `{"message":"no such repo"}`)
	svc := newProviderService(t, azureDeps(host, azureProject()))

	_, err := svc.Invoke(t.Context(), "list_pull_requests", json.RawMessage(`{"projectId":"p1"}`))

	require.Error(t, err)
	assert.NotContains(t, err.Error(), sentinel.CredentialRefused)
	assert.NotContains(t, err.Error(), sentinel.SelfApproval)
}

// ---- acting on a pull request (REVIEW-037) ----------------------------------------------------

func TestApprovingOnGitHubSubmitsAReviewAndFilesTheDecision(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls/7/reviews", http.StatusOK, `{}`)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7", http.StatusOK,
		`{"number":7,"title":"Add the thing","state":"closed","head":{"ref":"f","sha":"s"},"base":{"ref":"main"},"user":{"login":"g"},"html_url":"https://example.test/pr/7"}`)

	log := &fakeActivity{}
	svc := newProviderService(t, providers.Deps{
		Projects:    &fakeProjects{project: gitHubProject(host)},
		Credentials: fakeCredentials{githubHosts: map[string]bool{host.hostname(): true}},
		Activity:    log,
		HTTP:        host.server.Client(),
	})

	out := call(t, svc, "act_on_pull_request", `{"projectId":"p1","prId":7,"action":"approve"}`)

	var outcome map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &outcome))
	assert.Contains(t, outcome, "pr")
	assert.Contains(t, outcome, "activity")

	require.Len(t, log.recorded, 1)
	filed := log.recorded[0]
	assert.Equal(t, "pr-action", filed.Kind)
	assert.Equal(t, "p1", filed.ProjectID)
	assert.Equal(t, "#7 Add the thing", filed.Label)
	assert.Equal(t, "done", filed.Status)
	require.NotNil(t, filed.Result)
	assert.Equal(t, "https://example.test/pr/7", *filed.Result)
	assert.JSONEq(t, `{"prId":7,"prTitle":"Add the thing","action":"approve"}`, filed.Meta)
	assert.NotEmpty(t, filed.ID, "a fresh id per action")
}

// A blank request-changes body becomes the Spanish default: GitHub refuses the event without one.
func TestRequestingChangesWithNoCommentSendsTheDefaultSentence(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls/7/reviews", http.StatusOK, `{}`)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7", http.StatusOK,
		`{"number":7,"head":{"ref":"f","sha":"s"},"base":{"ref":"main"},"user":{"login":"g"}}`)

	svc := newProviderService(t, providers.Deps{
		Projects:    &fakeProjects{project: gitHubProject(host)},
		Credentials: fakeCredentials{githubHosts: map[string]bool{host.hostname(): true}},
		Activity:    &fakeActivity{},
		HTTP:        host.server.Client(),
	})

	call(t, svc, "act_on_pull_request", `{"projectId":"p1","prId":7,"action":"request_changes","body":"   "}`)

	assert.Contains(t, host.requests[0].Body, "Cambios solicitados desde CodeFlow.")
}

func TestApprovingOnAzureVotesAndClosingAbandons(t *testing.T) {
	tests := map[string]struct {
		action   string
		method   string
		path     string
		expected string
	}{
		"approve": {
			action: "approve", method: http.MethodPut,
			path:     "/contoso/Dev/_apis/git/repositories/Dev.prueba/pullRequests/7/reviewers/user-guid",
			expected: `{"vote":10}`,
		},
		"request changes": {
			action: "request_changes", method: http.MethodPut,
			path:     "/contoso/Dev/_apis/git/repositories/Dev.prueba/pullRequests/7/reviewers/user-guid",
			expected: `{"vote":-10}`,
		},
		"close": {
			action: "close", method: http.MethodPatch,
			path:     "/contoso/Dev/_apis/git/repositories/Dev.prueba/pullRequests/7",
			expected: `{"status":"abandoned"}`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			host := newFakeAzure(t)
			host.answer(http.MethodGet, "/contoso/_apis/connectionData", http.StatusOK,
				`{"authenticatedUser":{"id":"user-guid"}}`)
			host.answer(test.method, test.path, http.StatusOK, `{}`)
			host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/Dev.prueba/pullRequests/7",
				http.StatusOK, azurePullBody)

			svc := newProviderService(t, azureDeps(host, azureProject()))

			call(t, svc, "act_on_pull_request",
				fmt.Sprintf(`{"projectId":"p1","prId":7,"action":%q}`, test.action))

			var acted recordedRequest
			for _, request := range host.seen() {
				if request.Method == test.method && request.Path == test.path {
					acted = request
				}
			}
			require.NotEmpty(t, acted.Method, "the action never reached the host")
			assert.JSONEq(t, test.expected, acted.Body)
		})
	}
}

// Approving your own pull request is marked for the toast, which replaces the message rather than
// showing GitHub's error envelope (`XLANG-013`).
func TestApprovingYourOwnPullRequestIsMarkedForTheToast(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls/7/reviews", http.StatusUnprocessableEntity,
		`{"message":"Unprocessable Entity","errors":["Review Can not approve your own pull request"]}`)

	log := &fakeActivity{}
	svc := newProviderService(t, providers.Deps{
		Projects:    &fakeProjects{project: gitHubProject(host)},
		Credentials: fakeCredentials{githubHosts: map[string]bool{host.hostname(): true}},
		Activity:    log,
		HTTP:        host.server.Client(),
	})

	_, err := svc.Invoke(t.Context(), "act_on_pull_request",
		json.RawMessage(`{"projectId":"p1","prId":7,"action":"approve"}`))

	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), sentinel.SelfApproval), err.Error())
	assert.Empty(t, log.recorded, "a failed action files no Activity row, ever")
}

func TestAnUnknownActionIsRefusedBeforeAnyNetworkCall(t *testing.T) {
	host := newFakeGitHub(t)
	svc := newProviderService(t, providers.Deps{
		Projects:    &fakeProjects{project: gitHubProject(host)},
		Credentials: fakeCredentials{githubHosts: map[string]bool{host.hostname(): true}},
		Activity:    &fakeActivity{},
		HTTP:        host.server.Client(),
	})

	_, err := svc.Invoke(t.Context(), "act_on_pull_request",
		json.RawMessage(`{"projectId":"p1","prId":7,"action":"merge"}`))

	require.Error(t, err)
	assert.Equal(t, "unknown PR action: merge", err.Error())
	assert.Empty(t, host.requests, "nothing was sent")
}

// The Activity write is not best-effort: the host-side action already went through, and a review
// panel showing an action that left no trace would be worse than the failure.
func TestAFailedActivityWriteFailsTheCommand(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodPost, "/api/v3/repos/acme/widget/pulls/7/reviews", http.StatusOK, `{}`)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/7", http.StatusOK,
		`{"number":7,"head":{"ref":"f","sha":"s"},"base":{"ref":"main"},"user":{"login":"g"}}`)

	svc := newProviderService(t, providers.Deps{
		Projects:    &fakeProjects{project: gitHubProject(host)},
		Credentials: fakeCredentials{githubHosts: map[string]bool{host.hostname(): true}},
		Activity:    &fakeActivity{err: errStoreGone},
		HTTP:        host.server.Client(),
	})

	_, err := svc.Invoke(t.Context(), "act_on_pull_request",
		json.RawMessage(`{"projectId":"p1","prId":7,"action":"approve"}`))

	assert.ErrorIs(t, err, errStoreGone)
}

// errStoreGone stands in for any failure below the feature: a database that will not answer, a
// folder that is no longer there.
var errStoreGone = errors.New("the store is gone")

// ---- creating a pull request (REVIEW-016) -----------------------------------------------------

func TestCreatingAPullRequestDispatchesToTheLinkedHost(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodPost, "/contoso/Dev/_apis/git/repositories/Dev.prueba/pullrequests",
		http.StatusCreated, azurePullBody)
	svc := newProviderService(t, azureDeps(host, azureProject()))

	out := call(t, svc, "create_pull_request", `{
		"projectId": "p1", "title": "Add the thing", "description": "why",
		"sourceBranch": "feat/thing", "targetBranch": "main", "draft": false
	}`)

	var created providers.PullRequestSummary
	require.NoError(t, json.Unmarshal(out, &created))
	assert.Equal(t, int64(87266), created.ID)
	assert.Contains(t, host.last().Body, `"sourceRefName":"refs/heads/feat/thing"`)
}

// ---- the Azure dialogs (REVIEW's manual link) -------------------------------------------------

func TestTheManualDialogsProjectLookupNeedsAPatAndSaysSo(t *testing.T) {
	svc := newProviderService(t, providers.Deps{
		Projects:    &fakeProjects{},
		Credentials: fakeCredentials{},
	})

	_, err := svc.Invoke(t.Context(), "ado_list_projects", json.RawMessage(`{"org":"contoso"}`))

	assert.ErrorIs(t, err, providers.ErrNoADOPAT)
}

func TestTheManualDialogListsProjectsAndRepos(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/_apis/projects", http.StatusOK,
		`{"value":[{"id":"project-guid","name":"Dev"}]}`)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories", http.StatusOK,
		`{"value":[{"id":"repo-guid","name":"Dev.prueba"}]}`)
	svc := newProviderService(t, azureDeps(host, workspaces.Project{ID: "p1"}))

	out := call(t, svc, "ado_list_projects", `{"org":"contoso"}`)
	assert.JSONEq(t, `[{"id":"project-guid","name":"Dev"}]`, string(out))

	out = call(t, svc, "ado_list_repos", `{"org":"contoso","project":"Dev"}`)
	assert.JSONEq(t, `[{"id":"repo-guid","name":"Dev.prueba"}]`, string(out))
}

func TestAnOrganisationWithNothingInItAnswersArrays(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/_apis/projects", http.StatusOK, `{"value":[]}`)
	svc := newProviderService(t, azureDeps(host, workspaces.Project{ID: "p1"}))

	out := call(t, svc, "ado_list_projects", `{"org":"contoso"}`)

	assert.Equal(t, "[]", string(out), "a nil slice would crash the picker that maps it")
}

// ---- resolving a pasted link (REVIEW-007, REVIEW-011, REVIEW-012) -----------------------------

func TestAPastedLinkThatIsNotAPullRequestResolvesRatherThanErroring(t *testing.T) {
	svc := newProviderService(t, providers.Deps{Projects: &fakeProjects{}, Credentials: fakeCredentials{}})

	out := call(t, svc, "resolve_pr_link", `{"url":"https://example.com/not/a/pr"}`)

	assert.JSONEq(t, `{"status":"Unrecognized"}`, string(out))
}

func TestAPastedLinkForAHostWithNoTokenAsksForOneAndNamesTheHost(t *testing.T) {
	svc := newProviderService(t, providers.Deps{Projects: &fakeProjects{}, Credentials: fakeCredentials{}})

	out := call(t, svc, "resolve_pr_link", `{"url":"https://github.com/acme/widget/pull/42"}`)

	assert.JSONEq(t, `{"status":"NeedsToken","provider":"github","identifier":"github.com"}`, string(out))
}

// An Azure link names the **organisation** to connect, not the host: `dev.azure.com` is not what a
// user connects.
func TestAnAzureLinkWithNoSavedPatAsksForTheOrganisation(t *testing.T) {
	svc := newProviderService(t, providers.Deps{Projects: &fakeProjects{}, Credentials: fakeCredentials{}})

	out := call(t, svc, "resolve_pr_link",
		`{"url":"https://dev.azure.com/contoso/Dev/_git/Dev.prueba/pullrequest/87266"}`)

	assert.JSONEq(t, `{"status":"NeedsToken","provider":"azure","identifier":"contoso"}`, string(out))
}

// A saved PAT the host refused is a different screen from no PAT at all: the user has already
// connected this organisation, so the offer is to reconnect it (`DIVERGENCE-PROV-b`).
func TestAnAzureLinkWhoseSavedPatIsRefusedSaysSoRatherThanAskingToConnect(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/Dev.prueba/pullRequests/87266",
		http.StatusUnauthorized, `{"message":"TF400813"}`)
	svc := newProviderService(t, azureDeps(host, workspaces.Project{ID: "p1"}))

	out := call(t, svc, "resolve_pr_link",
		`{"url":"https://dev.azure.com/contoso/Dev/_git/Dev.prueba/pullrequest/87266"}`)

	assert.JSONEq(t, `{"status":"Expired","provider":"azure","identifier":"contoso"}`, string(out))
}

// With no local checkout the pull request still comes back, so a preview can be shown, plus a clone
// address to offer.
func TestALinkWithNoMatchingLocalRepoOffersACloneURL(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/42", http.StatusOK,
		strings.TrimSuffix(strings.TrimPrefix(onePull, "["), "]"))

	hostname := host.hostname()
	svc := newProviderService(t, providers.Deps{
		Projects: &fakeProjects{
			setting: text(fmt.Sprintf(`[{"host":%q}]`, hostname)),
			all:     []workspaces.Project{},
		},
		Remotes:     &fakeRemotes{},
		Credentials: fakeCredentials{githubHosts: map[string]bool{hostname: true}},
		HTTP:        host.server.Client(),
	})

	out := call(t, svc, "resolve_pr_link", fmt.Sprintf(`{"url":"https://%s/acme/widget/pull/42"}`, hostname))

	var resolution map[string]any
	require.NoError(t, json.Unmarshal(out, &resolution))
	assert.Equal(t, "NoLocalRepo", resolution["status"])
	assert.Equal(t, "acme/widget", resolution["repo_label"])
	assert.Equal(t, "https://"+hostname+"/acme/widget", resolution["clone_url"])
	assert.NotNil(t, resolution["pr"], "the pull request is still returned, for the preview")
}

// Pass 1: a project already linked to exactly this repository is used as it stands, and nothing is
// written.
func TestAlreadyLinkedProjectsAreFoundWithoutAWrite(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/42", http.StatusOK,
		strings.TrimSuffix(strings.TrimPrefix(onePull, "["), "]"))

	hostname := host.hostname()
	projects := &fakeProjects{
		setting: text(fmt.Sprintf(`[{"host":%q}]`, hostname)),
		project: gitHubProject(host),
		all:     []workspaces.Project{gitHubProject(host)},
	}
	svc := newProviderService(t, providers.Deps{
		Projects:    projects,
		Remotes:     &fakeRemotes{},
		Credentials: fakeCredentials{githubHosts: map[string]bool{hostname: true}},
		HTTP:        host.server.Client(),
	})

	out := call(t, svc, "resolve_pr_link", fmt.Sprintf(`{"url":"https://%s/acme/widget/pull/42"}`, hostname))

	var resolution map[string]any
	require.NoError(t, json.Unmarshal(out, &resolution))
	assert.Equal(t, "Ready", resolution["status"])
	assert.Equal(t, "p1", resolution["project_id"])
	assert.Equal(t, "w1", resolution["workspace_id"])
	assert.Equal(t, "Repo", resolution["project_name"])
	assert.False(t, projects.unlinked, "an already-correct link is not rewritten")
}

// Pass 2: a project whose own remote points at this repository is re-linked — unlinked first, so a
// stale pair pointing at the other host cannot misroute later commands.
func TestAProjectWhoseRemotePointsAtTheLinkIsReLinked(t *testing.T) {
	host := newFakeAzure(t)
	host.answer(http.MethodGet, "/contoso/Dev/_apis/git/repositories/Dev.prueba/pullRequests/87266",
		http.StatusOK, azurePullBody)

	stale := workspaces.Project{
		ID: "p1", WorkspaceID: "w1", Name: "Repo", LocalPath: "/repos/thing",
		GitHubOwner: text("acme"), GitHubRepo: text("widget"),
	}
	projects := &fakeProjects{project: stale, all: []workspaces.Project{stale}}

	deps := azureDeps(host, stale)
	deps.Projects = projects
	deps.Remotes = &fakeRemotes{remotes: []providers.Remote{
		{Name: "origin", URL: "https://dev.azure.com/contoso/Dev/_git/Dev.prueba"},
	}}
	svc := newProviderService(t, deps)

	out := call(t, svc, "resolve_pr_link",
		`{"url":"https://dev.azure.com/contoso/Dev/_git/Dev.prueba/pullrequest/87266"}`)

	var resolution map[string]any
	require.NoError(t, json.Unmarshal(out, &resolution))
	assert.Equal(t, "Ready", resolution["status"])
	assert.True(t, projects.unlinked, "the stale GitHub columns are cleared first")
	assert.Equal(t, []string{"contoso", "Dev", "Dev.prueba"}, projects.linkedADO)
}

// A project whose folder has moved is skipped rather than fatal — the tolerance auto_link_project
// deliberately does not have.
func TestAProjectWhoseFolderIsGoneIsSkippedNotFatal(t *testing.T) {
	host := newFakeGitHub(t)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/42", http.StatusOK,
		strings.TrimSuffix(strings.TrimPrefix(onePull, "["), "]"))

	hostname := host.hostname()
	svc := newProviderService(t, providers.Deps{
		Projects: &fakeProjects{
			setting: text(fmt.Sprintf(`[{"host":%q}]`, hostname)),
			all:     []workspaces.Project{{ID: "gone", LocalPath: "/repos/gone"}},
		},
		Remotes:     &fakeRemotes{err: errStoreGone},
		Credentials: fakeCredentials{githubHosts: map[string]bool{hostname: true}},
		HTTP:        host.server.Client(),
	})

	out := call(t, svc, "resolve_pr_link", fmt.Sprintf(`{"url":"https://%s/acme/widget/pull/42"}`, hostname))

	var resolution map[string]any
	require.NoError(t, json.Unmarshal(out, &resolution))
	assert.Equal(t, "NoLocalRepo", resolution["status"], "the broken project is skipped, not reported")
}

// The three link reads throw on an unreadable link instead of reporting a state: by the time they
// are called the modal has already resolved it.
func TestThePrLinkCommandsThrowOnAnUnreadableLink(t *testing.T) {
	svc := newProviderService(t, providers.Deps{Projects: &fakeProjects{}, Credentials: fakeCredentials{}})

	for _, method := range []string{
		"pr_link_pull_request", "pr_link_comment_threads", "pr_link_decision", "act_on_pr_link",
	} {
		t.Run(method, func(t *testing.T) {
			_, err := svc.Invoke(t.Context(), method,
				json.RawMessage(`{"url":"https://example.com/nope","action":"approve"}`))

			require.Error(t, err)
			assert.Equal(t, "That isn't a pull-request link CodeFlow can read", err.Error())
		})
	}
}

func TestTheLinkReadsDispatchStraightToTheirHost(t *testing.T) {
	host := newFakeGitHub(t)
	hostname := host.hostname()
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/42", http.StatusOK,
		strings.TrimSuffix(strings.TrimPrefix(onePull, "["), "]"))
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/42/comments", http.StatusOK, `[]`)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/issues/42/comments", http.StatusOK, `[]`)
	host.answer(http.MethodGet, "/api/v3/user", http.StatusOK, `{"login":"gaston"}`)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/42/reviews", http.StatusOK,
		`[{"user":{"login":"gaston"},"state":"APPROVED"}]`)

	svc := newProviderService(t, providers.Deps{
		Projects:    &fakeProjects{setting: text(fmt.Sprintf(`[{"host":%q}]`, hostname))},
		Credentials: fakeCredentials{githubHosts: map[string]bool{hostname: true}},
		HTTP:        host.server.Client(),
	})
	url := fmt.Sprintf(`{"url":"https://%s/acme/widget/pull/42"}`, hostname)

	out := call(t, svc, "pr_link_pull_request", url)
	var pull providers.PullRequestSummary
	require.NoError(t, json.Unmarshal(out, &pull))
	assert.Equal(t, int64(42), pull.ID)

	out = call(t, svc, "pr_link_comment_threads", url)
	assert.Equal(t, "[]", string(out))

	out = call(t, svc, "pr_link_decision", url)
	assert.JSONEq(t, `"approved"`, string(out))
}

// Acting through a link files no Activity row: that table belongs to a project, and a link review
// has none.
func TestActingThroughALinkFilesNothing(t *testing.T) {
	host := newFakeGitHub(t)
	hostname := host.hostname()
	host.answer(http.MethodPatch, "/api/v3/repos/acme/widget/pulls/42", http.StatusOK, `{}`)
	host.answer(http.MethodGet, "/api/v3/repos/acme/widget/pulls/42", http.StatusOK,
		strings.TrimSuffix(strings.TrimPrefix(onePull, "["), "]"))

	log := &fakeActivity{}
	svc := newProviderService(t, providers.Deps{
		Projects:    &fakeProjects{setting: text(fmt.Sprintf(`[{"host":%q}]`, hostname))},
		Credentials: fakeCredentials{githubHosts: map[string]bool{hostname: true}},
		Activity:    log,
		HTTP:        host.server.Client(),
	})

	out := call(t, svc, "act_on_pr_link",
		fmt.Sprintf(`{"url":"https://%s/acme/widget/pull/42","action":"close"}`, hostname))

	var pull providers.PullRequestSummary
	require.NoError(t, json.Unmarshal(out, &pull))
	assert.Equal(t, int64(42), pull.ID, "the pull request is re-read after the action")
	assert.Empty(t, log.recorded)
}

func TestTheDispatchingCommandsNameAMissingParameter(t *testing.T) {
	svc := newProviderService(t, providers.Deps{Projects: &fakeProjects{}, Credentials: fakeCredentials{}})

	tests := map[string]string{
		"list_pull_requests":      `{}`,
		"list_pr_comment_threads": `{"projectId":"p1"}`,
		"pr_review_decision":      `{"projectId":"p1"}`,
		"act_on_pull_request":     `{"projectId":"p1","prId":1}`,
		"create_pull_request":     `{"projectId":"p1","title":"t"}`,
		"ado_list_projects":       `{}`,
		"ado_list_repos":          `{"org":"contoso"}`,
		"resolve_pr_link":         `{}`,
		"pr_link_pull_request":    `{}`,
	}

	for method, params := range tests {
		t.Run(method, func(t *testing.T) {
			_, err := svc.Invoke(t.Context(), method, json.RawMessage(params))

			require.Error(t, err)
			assert.Contains(t, err.Error(), "missing required parameter")
		})
	}
}
