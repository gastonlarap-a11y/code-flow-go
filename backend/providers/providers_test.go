package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge/jsonwire"
	"github.com/gastonlarap-a11y/code-flow/backend/providers"
	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
)

// The ported halves of LinkedRepoTests and of the ProviderIpcTests cases this slice owns. The
// stores are hand-written fakes rather than a database: what these cases are about is the scan —
// "resolves but has no token", "two remotes and one of them is connected" — and none of that gets
// more true for going through SQLite. The three link writes are asserted against the real store in
// backend/workspaces, which owns the columns.

// ---- fakes ------------------------------------------------------------------------------------

type fakeProjects struct {
	project    workspaces.Project
	all        []workspaces.Project
	getErr     error
	listErr    error
	setting    *string
	settingErr error

	linkedGitHub []string // host, owner, repo
	linkedADO    []string // org, project, repoID
	unlinked     bool
	reads        int
}

func (f *fakeProjects) GetProject(_ context.Context, _ string) (workspaces.Project, error) {
	f.reads++
	return f.project, f.getErr
}

// ListAllProjects answers `all` when a test set it, and otherwise the one project it holds — so a
// case that only cares about a single project does not have to fill two fields.
func (f *fakeProjects) ListAllProjects(_ context.Context) ([]workspaces.Project, error) {
	if f.all != nil {
		return f.all, f.listErr
	}
	if f.project.ID == "" {
		return nil, f.listErr
	}
	return []workspaces.Project{f.project}, f.listErr
}

func (f *fakeProjects) LinkProjectGitHub(_ context.Context, _, host, owner, repo string) error {
	f.linkedGitHub = []string{host, owner, repo}
	f.project.GitHubHost, f.project.GitHubOwner, f.project.GitHubRepo = &host, &owner, &repo
	return nil
}

func (f *fakeProjects) LinkProjectADO(_ context.Context, _, org, project, repoID string) error {
	f.linkedADO = []string{org, project, repoID}
	f.project.ADOOrg, f.project.ADOProject, f.project.ADORepoID = &org, &project, &repoID
	return nil
}

func (f *fakeProjects) UnlinkProject(_ context.Context, _ string) error {
	f.unlinked = true
	return nil
}

func (f *fakeProjects) GetSetting(_ context.Context, _ string) (*string, error) {
	return f.setting, f.settingErr
}

type fakeRemotes struct {
	remotes  []providers.Remote
	err      error
	askedFor string
}

func (f *fakeRemotes) ListRemotes(_ context.Context, repoPath string) ([]providers.Remote, error) {
	f.askedFor = repoPath
	return f.remotes, f.err
}

// fakeCredentials stands in for the credential store. The maps hold connected hosts and
// organisations; `tokens` holds what a read answers, defaulting to a throwaway string so the
// "connected" and "readable" halves can be set independently — which is the distinction the
// keychain itself makes, and the one the scan depends on.
type fakeCredentials struct {
	githubHosts map[string]bool
	adoOrgs     map[string]bool
	tokens      map[string]string
	err         error
	readErr     error
}

func (f fakeCredentials) HasGitHubToken(host string) (bool, error) {
	return f.githubHosts[host], f.err
}

func (f fakeCredentials) HasADOPAT(org string) (bool, error) {
	return f.adoOrgs[org], f.err
}

func (f fakeCredentials) GitHubToken(host string) (string, error) {
	return f.read(host, f.githubHosts[host])
}

func (f fakeCredentials) ADOPAT(org string) (string, error) {
	return f.read(org, f.adoOrgs[org])
}

func (f fakeCredentials) read(key string, connected bool) (string, error) {
	if f.readErr != nil {
		return "", f.readErr
	}
	if token, found := f.tokens[key]; found {
		return token, nil
	}
	if !connected {
		return "", providers.ErrNoCredential
	}
	return "token-for-" + key, nil
}

func newProviderService(t *testing.T, deps providers.Deps) *bridge.Service {
	t.Helper()
	r := bridge.NewRegistry()
	providers.Register(r, deps)
	r.Seal()
	return bridge.NewService(r, nil)
}

func call(t *testing.T, svc *bridge.Service, method, params string) json.RawMessage {
	t.Helper()
	out, err := svc.Invoke(t.Context(), method, json.RawMessage(params))
	require.NoError(t, err, method)
	return out
}

func text(value string) *string { return &value }

// ---- LinkedRepoTests --------------------------------------------------------------------------

func TestAGitHubLinkedProjectResolvesToGitHub(t *testing.T) {
	linked, err := providers.LinkedRepoFor(workspaces.Project{
		GitHubOwner: text("acme"), GitHubRepo: text("widget"),
	})

	require.NoError(t, err)
	assert.Equal(t, providers.ProviderGitHub, linked.Provider)
	assert.Equal(t, "acme", linked.GitHub.Owner)
	assert.Equal(t, "widget", linked.GitHub.Repo)
	assert.Equal(t, "github.com", linked.GitHub.Host, "a null host reads as the public one")
}

func TestAnExplicitEnterpriseHostIsCarriedThrough(t *testing.T) {
	linked, err := providers.LinkedRepoFor(workspaces.Project{
		GitHubOwner: text("team"), GitHubRepo: text("app"), GitHubHost: text("ghe.contoso.com"),
	})

	require.NoError(t, err)
	assert.Equal(t, "ghe.contoso.com", linked.GitHub.Host)
}

func TestAnADOLinkedProjectResolvesToAzure(t *testing.T) {
	linked, err := providers.LinkedRepoFor(workspaces.Project{
		ADOOrg: text("contoso"), ADOProject: text("Web"), ADORepoID: text("api"),
	})

	require.NoError(t, err)
	assert.Equal(t, providers.ProviderAzure, linked.Provider)
	assert.Equal(t, "contoso", linked.Azure.Org)
	assert.Equal(t, "Web", linked.Azure.Project)
	assert.Equal(t, "api", linked.Azure.Repo)
}

// Both links set is a real state — nothing in the schema prevents it — and GitHub wins.
func TestGitHubWinsWhenAProjectCarriesBothLinks(t *testing.T) {
	linked, err := providers.LinkedRepoFor(workspaces.Project{
		ADOOrg: text("contoso"), ADOProject: text("Web"), ADORepoID: text("api"),
		GitHubOwner: text("acme"), GitHubRepo: text("widget"),
	})

	require.NoError(t, err)
	assert.Equal(t, providers.ProviderGitHub, linked.Provider)
}

func TestAHalfFilledLinkDoesNotCount(t *testing.T) {
	tests := map[string]workspaces.Project{
		"github without a repository":     {GitHubOwner: text("acme")},
		"github without an owner":         {GitHubRepo: text("widget")},
		"azure without a repository id":   {ADOOrg: text("contoso"), ADOProject: text("Web")},
		"azure without a project":         {ADOOrg: text("contoso"), ADORepoID: text("api")},
		"a host with nothing to go on it": {GitHubHost: text("ghe.contoso.com")},
	}

	for name, project := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := providers.LinkedRepoFor(project)
			assert.ErrorIs(t, err, providers.ErrNotLinked)
		})
	}
}

func TestAnUnlinkedProjectSaysSoInTheWordsTheFrontendShows(t *testing.T) {
	_, err := providers.LinkedRepoFor(workspaces.Project{})

	require.Error(t, err)
	assert.Equal(t, "This project isn't linked to a pull-request host yet", err.Error())
}

// ---- the wire shape ---------------------------------------------------------------------------

// The renderer switches on `status` and reads an arm's own fields only on that arm. A tag in the
// wrong case, or a payload field on the wrong arm, is a union that type-checks as none of them.
func TestEveryAutoLinkVariantCarriesTheDiscriminatorTheRendererSwitchesOn(t *testing.T) {
	tests := []struct {
		name     string
		deps     providers.Deps
		expected string
	}{
		{
			name: "Linked",
			deps: providers.Deps{
				Projects: &fakeProjects{project: workspaces.Project{
					ID: "p1", Name: "Repo", GitHubOwner: text("acme"), GitHubRepo: text("widget"),
				}},
				Remotes:     &fakeRemotes{},
				Credentials: fakeCredentials{},
			},
			expected: "Linked",
		},
		{
			name: "NeedsToken",
			deps: providers.Deps{
				Projects: &fakeProjects{project: workspaces.Project{ID: "p1", LocalPath: "/repos/thing"}},
				Remotes: &fakeRemotes{remotes: []providers.Remote{
					{Name: "origin", URL: "https://github.com/acme/widget.git"},
				}},
				Credentials: fakeCredentials{},
			},
			expected: "NeedsToken",
		},
		{
			name: "NotDetected",
			deps: providers.Deps{
				Projects: &fakeProjects{project: workspaces.Project{ID: "p1", LocalPath: "/repos/thing"}},
				Remotes: &fakeRemotes{remotes: []providers.Remote{
					{Name: "origin", URL: "https://git.example.com/acme/widget.git"},
				}},
				Credentials: fakeCredentials{},
			},
			expected: "NotDetected",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := newProviderService(t, test.deps)

			out := call(t, svc, "auto_link_project", `{"projectId":"p1"}`)

			var decoded map[string]any
			require.NoError(t, json.Unmarshal(out, &decoded))
			assert.Equal(t, test.expected, decoded["status"])
		})
	}
}

func TestAVariantsOwnFieldsStaySnakeCaseWhileItsTagStaysPascalCase(t *testing.T) {
	deps := providers.Deps{
		Projects: &fakeProjects{project: workspaces.Project{
			ID: "p1", WorkspaceID: "w1", Name: "Repo", LocalPath: "/repos/thing",
			Color: "#6366f1", Icon: "git-branch", CreatedAt: "2026-09-18T12:00:00Z",
			GitHubOwner: text("acme"), GitHubRepo: text("widget"), GitHubHost: text("github.com"),
		}},
		Remotes:     &fakeRemotes{},
		Credentials: fakeCredentials{},
	}
	svc := newProviderService(t, deps)

	out := call(t, svc, "auto_link_project", `{"projectId":"p1"}`)

	assert.JSONEq(t, `{
		"status": "Linked",
		"project": {
			"id": "p1",
			"workspace_id": "w1",
			"name": "Repo",
			"local_path": "/repos/thing",
			"remote_url": null,
			"color": "#6366f1",
			"icon": "git-branch",
			"ado_org": null,
			"ado_project": null,
			"ado_repo_id": null,
			"github_owner": "acme",
			"github_repo": "widget",
			"github_host": "github.com",
			"sort_order": 0,
			"created_at": "2026-09-18T12:00:00Z"
		}
	}`, string(out), "the arm's tag is PascalCase and the project's fields are snake_case")

	// The arms that did not happen carry no keys at all, rather than nulls the union does not type.
	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &decoded))
	assert.NotContains(t, decoded, "provider")
	assert.NotContains(t, decoded, "identifier")
}

func TestTheAutoLinkResultCarriesNoNilSlices(t *testing.T) {
	deps := providers.Deps{
		Projects:    &fakeProjects{project: workspaces.Project{ID: "p1", LocalPath: "/repos/thing"}},
		Remotes:     &fakeRemotes{},
		Credentials: fakeCredentials{},
	}

	result, err := deps.AutoLink(t.Context(), "p1")

	require.NoError(t, err)
	assert.NoError(t, jsonwire.AssertNoNilSlices(result))
}

// ---- the auto-link scan (REVIEW-002) ----------------------------------------------------------

func TestAnAlreadyLinkedProjectIsANoOp(t *testing.T) {
	projects := &fakeProjects{project: workspaces.Project{
		ID: "p1", GitHubOwner: text("acme"), GitHubRepo: text("widget"),
	}}
	remotes := &fakeRemotes{remotes: []providers.Remote{
		{Name: "origin", URL: "https://dev.azure.com/contoso/Web/_git/api"},
	}}
	deps := providers.Deps{Projects: projects, Remotes: remotes, Credentials: fakeCredentials{
		adoOrgs: map[string]bool{"contoso": true},
	}}

	result, err := deps.AutoLink(t.Context(), "p1")

	require.NoError(t, err)
	assert.Equal(t, providers.StatusLinked, result.Status)
	assert.Empty(t, remotes.askedFor, "the remotes were never read")
	assert.Nil(t, projects.linkedADO, "a hand-made link is not re-derived from the remote")
}

func TestTheFirstRemoteThatResolvesAndHasACredentialWins(t *testing.T) {
	projects := &fakeProjects{project: workspaces.Project{ID: "p1", LocalPath: "/repos/thing"}}
	deps := providers.Deps{
		Projects: projects,
		Remotes: &fakeRemotes{remotes: []providers.Remote{
			{Name: "upstream", URL: "https://github.com/upstream/widget.git"},
			{Name: "origin", URL: "https://github.com/acme/widget.git"},
		}},
		Credentials: fakeCredentials{githubHosts: map[string]bool{"github.com": true}},
	}

	result, err := deps.AutoLink(t.Context(), "p1")

	require.NoError(t, err)
	assert.Equal(t, providers.StatusLinked, result.Status)
	assert.Equal(t, []string{"github.com", "acme", "widget"}, projects.linkedGitHub,
		"origin is tried first, whatever order git listed the remotes in")
}

// A remote that resolves without a credential does not stop the scan: a repository with a GitHub
// mirror and an Azure origin links to whichever host the user has actually connected.
func TestARemoteWithNoCredentialDoesNotStopTheScan(t *testing.T) {
	projects := &fakeProjects{project: workspaces.Project{ID: "p1", LocalPath: "/repos/thing"}}
	deps := providers.Deps{
		Projects: projects,
		Remotes: &fakeRemotes{remotes: []providers.Remote{
			{Name: "origin", URL: "https://github.com/acme/widget.git"},
			{Name: "azure", URL: "https://dev.azure.com/contoso/Web/_git/api"},
		}},
		Credentials: fakeCredentials{adoOrgs: map[string]bool{"contoso": true}},
	}

	result, err := deps.AutoLink(t.Context(), "p1")

	require.NoError(t, err)
	assert.Equal(t, providers.StatusLinked, result.Status)
	assert.Equal(t, []string{"contoso", "Web", "api"}, projects.linkedADO)
	assert.Nil(t, projects.linkedGitHub)
}

// And when nothing has a credential, the answer is the **first** provider that resolved — the one
// the user most likely means by "this repository".
func TestTheRememberedCandidateIsTheFirstOneThatResolved(t *testing.T) {
	deps := providers.Deps{
		Projects: &fakeProjects{project: workspaces.Project{ID: "p1", LocalPath: "/repos/thing"}},
		Remotes: &fakeRemotes{remotes: []providers.Remote{
			{Name: "origin", URL: "https://github.com/acme/widget.git"},
			{Name: "azure", URL: "https://dev.azure.com/contoso/Web/_git/api"},
		}},
		Credentials: fakeCredentials{},
	}

	result, err := deps.AutoLink(t.Context(), "p1")

	require.NoError(t, err)
	assert.Equal(t, providers.StatusNeedsToken, result.Status)
	require.NotNil(t, result.Provider)
	require.NotNil(t, result.Identifier)
	assert.Equal(t, providers.ProviderGitHub, *result.Provider)
	assert.Equal(t, "github.com", *result.Identifier)
}

// An Azure candidate names the **organisation**, not the host: that is what a user connects.
func TestAnAzureRemoteWithNoSavedPatNamesTheOrganisation(t *testing.T) {
	deps := providers.Deps{
		Projects: &fakeProjects{project: workspaces.Project{ID: "p1", LocalPath: "/repos/thing"}},
		Remotes: &fakeRemotes{remotes: []providers.Remote{
			{Name: "origin", URL: "git@ssh.dev.azure.com:v3/contoso/Web/api"},
		}},
		Credentials: fakeCredentials{},
	}

	result, err := deps.AutoLink(t.Context(), "p1")

	require.NoError(t, err)
	assert.Equal(t, providers.StatusNeedsToken, result.Status)
	require.NotNil(t, result.Provider)
	require.NotNil(t, result.Identifier)
	assert.Equal(t, providers.ProviderAzure, *result.Provider)
	assert.Equal(t, "contoso", *result.Identifier)
}

func TestAutoLinkingARepoWithNoRecognisableRemoteReportsThatRatherThanFailing(t *testing.T) {
	deps := providers.Deps{
		Projects: &fakeProjects{project: workspaces.Project{ID: "p1", LocalPath: "/repos/thing"}},
		Remotes: &fakeRemotes{remotes: []providers.Remote{
			{Name: "origin", URL: "https://git.example.com/acme/widget.git"},
		}},
		Credentials: fakeCredentials{githubHosts: map[string]bool{"github.com": true}},
	}

	result, err := deps.AutoLink(t.Context(), "p1")

	require.NoError(t, err)
	assert.Equal(t, providers.StatusNotDetected, result.Status)
	assert.Nil(t, result.Provider)
}

// A project with no remotes at all is the same answer: nothing to detect, nothing broken.
func TestAProjectWithNoRemotesIsNotDetected(t *testing.T) {
	deps := providers.Deps{
		Projects:    &fakeProjects{project: workspaces.Project{ID: "p1", LocalPath: "/repos/thing"}},
		Remotes:     &fakeRemotes{},
		Credentials: fakeCredentials{},
	}

	result, err := deps.AutoLink(t.Context(), "p1")

	require.NoError(t, err)
	assert.Equal(t, providers.StatusNotDetected, result.Status)
}

// A folder that moved or was deleted fails, rather than reporting "not detected": that is a
// different problem and linking something does not fix it.
func TestAMissingWorkingCopyIsAHardError(t *testing.T) {
	deps := providers.Deps{
		Projects:    &fakeProjects{project: workspaces.Project{ID: "p1", LocalPath: "/repos/gone"}},
		Remotes:     &fakeRemotes{err: errors.New("not a git repository")},
		Credentials: fakeCredentials{},
	}

	_, err := deps.AutoLink(t.Context(), "p1")

	assert.ErrorContains(t, err, "not a git repository")
}

// An Enterprise host is recognised only because Settings connected it — which is what the
// github_connections setting is for.
func TestAConnectedEnterpriseRemoteIsDetected(t *testing.T) {
	projects := &fakeProjects{
		project: workspaces.Project{ID: "p1", LocalPath: "/repos/thing"},
		setting: text(`[{"host":"ghe.contoso.com","username":"gaston"}]`),
	}
	deps := providers.Deps{
		Projects: projects,
		Remotes: &fakeRemotes{remotes: []providers.Remote{
			{Name: "origin", URL: "git@ghe.contoso.com:team/app.git"},
		}},
		Credentials: fakeCredentials{githubHosts: map[string]bool{"ghe.contoso.com": true}},
	}

	result, err := deps.AutoLink(t.Context(), "p1")

	require.NoError(t, err)
	assert.Equal(t, providers.StatusLinked, result.Status)
	assert.Equal(t, []string{"ghe.contoso.com", "team", "app"}, projects.linkedGitHub)
}

// A malformed setting is tolerated down to github.com: a list of hosts that did not parse must not
// stop a review of a repository on the public host.
func TestAMalformedConnectionsSettingLeavesTheDefaultHost(t *testing.T) {
	tests := map[string]*string{
		"absent":               nil,
		"empty":                text(""),
		"not json":             text("{{{"),
		"not an array":         text(`{"host":"ghe.contoso.com"}`),
		"entries with no host": text(`[{"username":"gaston"}]`),
	}

	for name, setting := range tests {
		t.Run(name, func(t *testing.T) {
			projects := &fakeProjects{
				project: workspaces.Project{ID: "p1", LocalPath: "/repos/thing"},
				setting: setting,
			}
			deps := providers.Deps{
				Projects: projects,
				Remotes: &fakeRemotes{remotes: []providers.Remote{
					{Name: "origin", URL: "git@ghe.contoso.com:team/app.git"},
					{Name: "public", URL: "https://github.com/acme/widget.git"},
				}},
				Credentials: fakeCredentials{githubHosts: map[string]bool{
					"github.com": true, "ghe.contoso.com": true,
				}},
			}

			result, err := deps.AutoLink(t.Context(), "p1")

			require.NoError(t, err)
			assert.Equal(t, providers.StatusLinked, result.Status)
			assert.Equal(t, []string{"github.com", "acme", "widget"}, projects.linkedGitHub,
				"the Enterprise remote was not a known host, so the public one won")
		})
	}
}

// A failed settings read is not the same as an unparseable value: swallowing it would make every
// detection look like "not a known host".
func TestAFailedSettingsReadIsReported(t *testing.T) {
	deps := providers.Deps{
		Projects: &fakeProjects{
			project:    workspaces.Project{ID: "p1", LocalPath: "/repos/thing"},
			settingErr: errors.New("database is locked"),
		},
		Remotes: &fakeRemotes{remotes: []providers.Remote{
			{Name: "origin", URL: "https://github.com/acme/widget.git"},
		}},
		Credentials: fakeCredentials{},
	}

	_, err := deps.AutoLink(t.Context(), "p1")

	assert.ErrorContains(t, err, "database is locked")
}

// ---- repo_web_url (REVIEW-005) ----------------------------------------------------------------

// Rebuilt from the remote, not from the stored columns: `ado_repo_id` can hold a GUID, which has no
// web page.
func TestTheRepositoryWebURLIsRebuiltFromTheRemoteNotFromTheStoredLink(t *testing.T) {
	deps := providers.Deps{
		Projects: &fakeProjects{project: workspaces.Project{
			ID:        "p1",
			LocalPath: "/repos/thing",
			ADOOrg:    text("contoso"), ADOProject: text("Web"),
			ADORepoID: text("6f9619ff-8b86-d011-b42d-00c04fc964ff"),
		}},
		Remotes: &fakeRemotes{remotes: []providers.Remote{
			{Name: "origin", URL: "https://dev.azure.com/contoso/Marketing%20Website/_git/site"},
		}},
		Credentials: fakeCredentials{},
	}
	svc := newProviderService(t, deps)

	out := call(t, svc, "repo_web_url", `{"projectId":"p1"}`)

	assert.JSONEq(t, `"https://dev.azure.com/contoso/Marketing%20Website/_git/site"`, string(out),
		"the space is re-encoded and the GUID never appears")
}

func TestAGitHubRemoteRebuildsItsOwnHost(t *testing.T) {
	deps := providers.Deps{
		Projects: &fakeProjects{
			project: workspaces.Project{ID: "p1", LocalPath: "/repos/thing"},
			setting: text(`[{"host":"ghe.contoso.com"}]`),
		},
		Remotes: &fakeRemotes{remotes: []providers.Remote{
			{Name: "origin", URL: "git@ghe.contoso.com:team/app.git"},
		}},
		Credentials: fakeCredentials{},
	}
	svc := newProviderService(t, deps)

	out := call(t, svc, "repo_web_url", `{"projectId":"p1"}`)

	assert.JSONEq(t, `"https://ghe.contoso.com/team/app"`, string(out))
}

func TestARepositoryWhoseRemoteIsNotAKnownHostSaysSo(t *testing.T) {
	deps := providers.Deps{
		Projects: &fakeProjects{project: workspaces.Project{ID: "p1", LocalPath: "/repos/thing"}},
		Remotes: &fakeRemotes{remotes: []providers.Remote{
			{Name: "origin", URL: "https://git.example.com/acme/widget.git"},
		}},
		Credentials: fakeCredentials{},
	}
	svc := newProviderService(t, deps)

	_, err := svc.Invoke(t.Context(), "repo_web_url", json.RawMessage(`{"projectId":"p1"}`))

	require.Error(t, err)
	assert.Equal(t, "Couldn't determine this repository's web address from its remote", err.Error(),
		"shown as written")
}

// ---- the wire, end to end ---------------------------------------------------------------------

func TestTheCommandsThisSliceOwnsAreRegisteredUnderTheirContractNames(t *testing.T) {
	r := bridge.NewRegistry()
	providers.Register(r, providers.Deps{})
	r.Seal()

	assert.Equal(t, []string{
		"act_on_pr_link", "act_on_pull_request", "ado_list_projects", "ado_list_repos",
		"auto_link_project", "create_pull_request", "github_authenticated_user",
		"link_project_ado", "link_project_github", "list_pr_comment_threads", "list_pull_requests",
		"pr_link_comment_threads", "pr_link_decision", "pr_link_pull_request", "pr_review_decision",
		"repo_web_url", "resolve_pr_link", "unlink_project",
	}, r.Names())
}

func TestLinkingAProjectByHandAndUnlinkingItRoundTripThroughTheWire(t *testing.T) {
	projects := &fakeProjects{project: workspaces.Project{ID: "p1"}}
	svc := newProviderService(t, providers.Deps{Projects: projects})

	out := call(t, svc, "link_project_github",
		`{"id":"p1","githubOwner":"acme","githubRepo":"widget","githubHost":"ghe.contoso.com"}`)
	assert.Equal(t, "null", string(out), "a void command answers null, never {}")
	assert.Equal(t, []string{"ghe.contoso.com", "acme", "widget"}, projects.linkedGitHub,
		"the renderer sends host last and the store takes it first — the order must not slip")

	out = call(t, svc, "link_project_ado",
		`{"id":"p1","adoOrg":"contoso","adoProject":"Web","adoRepoId":"api"}`)
	assert.Equal(t, "null", string(out))
	assert.Equal(t, []string{"contoso", "Web", "api"}, projects.linkedADO)

	out = call(t, svc, "unlink_project", `{"id":"p1"}`)
	assert.Equal(t, "null", string(out))
	assert.True(t, projects.unlinked)
}

func TestAMissingParameterIsNamedRatherThanCrashingTheDispatch(t *testing.T) {
	svc := newProviderService(t, providers.Deps{Projects: &fakeProjects{}})

	tests := map[string]string{
		"repo_web_url":        `{}`,
		"auto_link_project":   `{}`,
		"unlink_project":      `{}`,
		"link_project_github": `{"id":"p1","githubOwner":"acme"}`,
		"link_project_ado":    `{"id":"p1","adoOrg":"contoso"}`,
	}

	for method, params := range tests {
		t.Run(method, func(t *testing.T) {
			_, err := svc.Invoke(t.Context(), method, json.RawMessage(params))

			require.Error(t, err)
			assert.Contains(t, err.Error(), "missing required parameter")
		})
	}
}
