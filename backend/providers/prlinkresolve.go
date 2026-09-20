package providers

import (
	"context"
	"errors"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
)

// Resolving a pasted pull-request link (REVIEW-007).
//
// The shape of the answer is what the modal switches on, and each arm is a different screen: a
// review that can run, an offer to connect a host, an offer to reconnect an expired one, an offer
// to clone, or "that is not a link I can read". Getting the arm wrong shows the wrong screen, so
// every failure here is classified rather than reported.

// The five arms of PrLinkResolution, PascalCase because the renderer switches on these literals.
const (
	StatusReady        = "Ready"
	StatusExpired      = "Expired"
	StatusNoLocalRepo  = "NoLocalRepo"
	StatusUnrecognized = "Unrecognized"
)

// PrLinkResolution is what a pasted pull-request link turned out to be.
//
// `omitempty` on the payload fields for the same reason `AutoLinkResult` has it: these are a tagged
// union's arms, and the renderer's type says an arm's fields exist only on that arm.
type PrLinkResolution struct {
	Status string `json:"status"`

	// Ready
	ProjectID   string `json:"project_id,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectName string `json:"project_name,omitempty"`

	// NeedsToken and Expired
	Provider   *Provider `json:"provider,omitempty"`
	Identifier string    `json:"identifier,omitempty"`

	// NoLocalRepo
	RepoLabel string `json:"repo_label,omitempty"`
	CloneURL  string `json:"clone_url,omitempty"`

	// Ready and NoLocalRepo both carry the pull request itself — the first to review it, the second
	// so a preview can be shown with nothing local to attach it to.
	PR *PullRequestSummary `json:"pr,omitempty"`
}

// ResolvePRLink turns a pasted URL into a state the modal can act on (REVIEW-007).
func (d Deps) ResolvePRLink(ctx context.Context, rawURL string) (PrLinkResolution, error) {
	hosts, err := d.knownGitHubHosts(ctx)
	if err != nil {
		return PrLinkResolution{}, err
	}

	link, ok := ParsePRLink(rawURL, hosts)
	if !ok {
		// Before any network call: a string that is not a pull-request link is not a failure, it
		// is an answer.
		return PrLinkResolution{Status: StatusUnrecognized}, nil
	}

	host, err := d.hostForLink(ctx, link)
	if err != nil {
		return d.classifyLinkFailure(link, err)
	}

	pull, err := host.Get(ctx, link.Number)
	if err != nil {
		return d.classifyLinkFailure(link, err)
	}

	project, found, err := d.findProjectForLink(ctx, link)
	if err != nil {
		return PrLinkResolution{}, err
	}
	if !found {
		return PrLinkResolution{
			Status:    StatusNoLocalRepo,
			Provider:  &link.Provider,
			RepoLabel: linkRepoLabel(link),
			CloneURL:  linkCloneURL(link),
			PR:        &pull,
		}, nil
	}

	return PrLinkResolution{
		Status:      StatusReady,
		ProjectID:   project.ID,
		WorkspaceID: project.WorkspaceID,
		ProjectName: project.Name,
		PR:          &pull,
	}, nil
}

// classifyLinkFailure turns a credential failure into the arm that offers the right screen.
//
// A host with nothing saved and a host that refused a saved credential are different sentences —
// "connect this organisation" against "reconnect it" — and telling them apart is what
// `DIVERGENCE-PROV-b` bought. Anything else is a real failure and is reported as one.
func (d Deps) classifyLinkFailure(link PRLink, err error) (PrLinkResolution, error) {
	provider := link.Provider

	if errors.Is(err, ErrNoGitHubToken) || errors.Is(err, ErrNoADOPAT) {
		return PrLinkResolution{
			Status:     StatusNeedsToken,
			Provider:   &provider,
			Identifier: linkIdentifier(link),
		}, nil
	}

	var azure *AzureError
	if errors.As(err, &azure) && azure.Unauthorized {
		return PrLinkResolution{
			Status:     StatusExpired,
			Provider:   &provider,
			Identifier: linkIdentifier(link),
		}, nil
	}
	return PrLinkResolution{}, err
}

// linkIdentifier is what the user has to connect: a **host** for GitHub, an **organisation** for
// Azure. They are not interchangeable in the sentence the sidebar shows.
func linkIdentifier(link PRLink) string {
	if link.Provider == ProviderGitHub {
		return link.GitHub.Host
	}
	return link.Azure.Org
}

// linkRepoLabel is how the repository is named on screen when there is nothing local to open.
func linkRepoLabel(link PRLink) string {
	if link.Provider == ProviderGitHub {
		return link.GitHub.Owner + "/" + link.GitHub.Repo
	}
	return link.Azure.Project + "/" + link.Azure.Repo
}

// linkCloneURL is the address the "clone it and review" offer uses. No `.git` suffix: it is shown
// as well as used.
func linkCloneURL(link PRLink) string {
	if link.Provider == ProviderGitHub {
		return "https://" + link.GitHub.Host + "/" + link.GitHub.Owner + "/" + link.GitHub.Repo
	}
	return AzureWebURL(link.Azure.Org, link.Azure.Project, link.Azure.Repo)
}

// findProjectForLink looks for a local project this pull request belongs to (REVIEW-007).
//
// Two passes, and the second one writes:
//
//  1. a project already linked to exactly this repository, compared case-insensitively. Nothing is
//     written — it is already right.
//  2. the first project whose **own git remote** resolves to this repository. That project is
//     unlinked first and then linked to what the link says, because a stale pair pointing at the
//     other host would misroute every later command (`REVIEW-001` prefers GitHub), and re-read
//     afterwards so the caller gets the row as it now stands.
//
// A project whose folder has moved is skipped rather than fatal — the opposite of
// `auto_link_project`, which is looking at one project the user just clicked and has every reason
// to report that it is gone.
func (d Deps) findProjectForLink(ctx context.Context, link PRLink) (workspaces.Project, bool, error) {
	projects, err := d.Projects.ListAllProjects(ctx)
	if err != nil {
		return workspaces.Project{}, false, err
	}

	for _, project := range projects {
		if alreadyLinkedTo(project, link) {
			return project, true, nil
		}
	}

	hosts, err := d.knownGitHubHosts(ctx)
	if err != nil {
		return workspaces.Project{}, false, err
	}

	for _, project := range projects {
		remotes, err := d.Remotes.ListRemotes(ctx, project.LocalPath)
		if err != nil {
			continue
		}
		if !remoteMatchesLink(remotes, link, hosts) {
			continue
		}

		if err := d.Projects.UnlinkProject(ctx, project.ID); err != nil {
			return workspaces.Project{}, false, err
		}
		if link.Provider == ProviderGitHub {
			err = d.Projects.LinkProjectGitHub(ctx, project.ID,
				link.GitHub.Host, link.GitHub.Owner, link.GitHub.Repo)
		} else {
			err = d.Projects.LinkProjectADO(ctx, project.ID,
				link.Azure.Org, link.Azure.Project, link.Azure.Repo)
		}
		if err != nil {
			return workspaces.Project{}, false, err
		}

		linked, err := d.Projects.GetProject(ctx, project.ID)
		if err != nil {
			return workspaces.Project{}, false, err
		}
		return linked, true, nil
	}
	return workspaces.Project{}, false, nil
}

// alreadyLinkedTo is pass 1's predicate.
func alreadyLinkedTo(project workspaces.Project, link PRLink) bool {
	if link.Provider == ProviderGitHub {
		return sameText(project.GitHubOwner, link.GitHub.Owner) &&
			sameText(project.GitHubRepo, link.GitHub.Repo) &&
			sameHost(project.GitHubHost, link.GitHub.Host)
	}

	// For Azure the project must also carry **no** GitHub columns: dispatch prefers GitHub, so a
	// project holding both would never reach Azure however well its Azure columns matched. Pass 1
	// defers to pass 2 in that case, which repairs the columns.
	if project.GitHubOwner != nil || project.GitHubRepo != nil {
		return false
	}
	return sameText(project.ADOOrg, link.Azure.Org) &&
		sameText(project.ADOProject, link.Azure.Project) &&
		sameText(project.ADORepoID, link.Azure.Repo)
}

func remoteMatchesLink(remotes []Remote, link PRLink, hosts []string) bool {
	for _, remote := range originFirst(remotes) {
		if link.Provider == ProviderGitHub {
			if repo, ok := DetectGitHub(remote.URL, hosts); ok &&
				strings.EqualFold(repo.Owner, link.GitHub.Owner) &&
				strings.EqualFold(repo.Repo, link.GitHub.Repo) {
				return true
			}
			continue
		}
		if repo, ok := DetectAzure(remote.URL); ok &&
			strings.EqualFold(repo.Org, link.Azure.Org) &&
			strings.EqualFold(repo.Project, link.Azure.Project) &&
			strings.EqualFold(repo.Repo, link.Azure.Repo) {
			return true
		}
	}
	return false
}

func sameText(stored *string, value string) bool {
	return stored != nil && strings.EqualFold(*stored, value)
}

// sameHost treats a null host as github.com, which is how every other reader of these columns
// treats it.
func sameHost(stored *string, value string) bool {
	host := DefaultGitHubHost
	if stored != nil && *stored != "" {
		host = *stored
	}
	return strings.EqualFold(host, value)
}
