package providers

import (
	"context"
	"errors"
	"fmt"

	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
)

// LinkedRepo is the pull-request host a project row points at.
//
// Same two halves as a parsed link, for the same reason: what a project is linked to and what a
// pasted URL names are the same thing, and the code that fetches a pull request should not care
// which of the two it came from.
type LinkedRepo struct {
	Provider Provider
	GitHub   GitHubRepo
	Azure    AzureRepo
}

// ErrNotLinked is refused in the words the frontend shows — it is displayed as written.
var ErrNotLinked = errors.New("This project isn't linked to a pull-request host yet") //nolint:staticcheck // ST1005: VERBATIM

// LinkedRepoFor resolves a project row to its host (REVIEW-001).
//
// **GitHub wins when both links are set**, and the Azure one is then ignored by every command that
// dispatches through here. Nothing in the schema prevents a row from carrying both (`STORE-011`),
// so this is a real state rather than a defensive branch — preserved as 2.x ordered it, because a
// project linked to both and reviewed on GitHub must keep being reviewed on GitHub.
func LinkedRepoFor(project workspaces.Project) (LinkedRepo, error) {
	if project.GitHubOwner != nil && project.GitHubRepo != nil {
		host := DefaultGitHubHost
		if project.GitHubHost != nil && *project.GitHubHost != "" {
			host = *project.GitHubHost
		}
		return LinkedRepo{
			Provider: ProviderGitHub,
			GitHub:   GitHubRepo{Host: host, Owner: *project.GitHubOwner, Repo: *project.GitHubRepo},
		}, nil
	}

	// All three, not two: an organisation and a project without a repository id cannot address a
	// pull request, and a half-filled link is what a cancelled dialog leaves behind.
	if project.ADOOrg != nil && project.ADOProject != nil && project.ADORepoID != nil {
		return LinkedRepo{
			Provider: ProviderAzure,
			Azure:    AzureRepo{Org: *project.ADOOrg, Project: *project.ADOProject, Repo: *project.ADORepoID},
		}, nil
	}
	return LinkedRepo{}, ErrNotLinked
}

// The three arms of AutoLinkResult, PascalCase because the renderer switches on these literals.
const (
	StatusLinked      = "Linked"
	StatusNeedsToken  = "NeedsToken"
	StatusNotDetected = "NotDetected"
)

// AutoLinkResult is what auto-detection concluded, as the renderer's tagged union.
//
// `omitempty` on the payload fields is deliberate and is not the "never omit a nullable field"
// rule's case: these are the union's **arms**, and the renderer's type says an arm's fields exist
// only on that arm. A `NotDetected` carrying `project: null` would type-check as neither.
type AutoLinkResult struct {
	Status     string              `json:"status"`
	Project    *workspaces.Project `json:"project,omitempty"`
	Provider   *Provider           `json:"provider,omitempty"`
	Identifier *string             `json:"identifier,omitempty"`
}

func linkedTo(project workspaces.Project) AutoLinkResult {
	return AutoLinkResult{Status: StatusLinked, Project: &project}
}

// needsToken names the thing to connect: a **host** for GitHub, an **organisation** for Azure.
// The sentence the sidebar shows is built from it, and the two are not interchangeable — an Azure
// user connects `dev.azure.com/contoso`, never `dev.azure.com`.
func needsToken(provider Provider, identifier string) AutoLinkResult {
	return AutoLinkResult{Status: StatusNeedsToken, Provider: &provider, Identifier: &identifier}
}

// AutoLink derives a project's pull-request host from its own git remotes (REVIEW-002).
//
// git already knows where the repository lives, so the user is not asked to pick it again. The scan
// rules are exact, and each one is a case that was met:
//
//   - an already-linked project is a no-op, not a re-detection: the link may have been made by hand
//     against a host the remote does not name;
//   - `origin` is tried first, then the rest in the order git lists them;
//   - the first remote that resolves **and** already has a credential wins and is written;
//   - a remote that resolves without one is remembered as the answer to give **if nothing else
//     wins**, and the scan continues — a repository with both a GitHub mirror and an Azure origin
//     links to whichever the user has actually connected;
//   - nothing resolving at all is `NotDetected`, which the sidebar shows as an offer to link by hand.
//
// A repository whose folder moved or was deleted fails here rather than reporting `NotDetected`:
// "this is not a pull-request repository" and "this is not a repository" are different problems,
// and only one of them is fixed by linking something.
func (d Deps) AutoLink(ctx context.Context, projectID string) (AutoLinkResult, error) {
	project, err := d.Projects.GetProject(ctx, projectID)
	if err != nil {
		return AutoLinkResult{}, err
	}
	if _, err := LinkedRepoFor(project); err == nil {
		return linkedTo(project), nil
	}

	remotes, err := d.Remotes.ListRemotes(ctx, project.LocalPath)
	if err != nil {
		return AutoLinkResult{}, err
	}
	hosts, err := d.knownGitHubHosts(ctx)
	if err != nil {
		return AutoLinkResult{}, err
	}

	var deferred *AutoLinkResult
	remember := func(result AutoLinkResult) {
		if deferred == nil {
			deferred = &result
		}
	}

	for _, remote := range originFirst(remotes) {
		if repo, ok := DetectGitHub(remote.URL, hosts); ok {
			saved, err := d.Credentials.HasGitHubToken(repo.Host)
			if err != nil {
				return AutoLinkResult{}, err
			}
			if !saved {
				remember(needsToken(ProviderGitHub, repo.Host))
				continue
			}
			if err := d.Projects.LinkProjectGitHub(ctx, projectID, repo.Host, repo.Owner, repo.Repo); err != nil {
				return AutoLinkResult{}, fmt.Errorf("link %s to github: %w", projectID, err)
			}
			return d.reread(ctx, projectID)
		}

		if repo, ok := DetectAzure(remote.URL); ok {
			saved, err := d.Credentials.HasADOPAT(repo.Org)
			if err != nil {
				return AutoLinkResult{}, err
			}
			if !saved {
				remember(needsToken(ProviderAzure, repo.Org))
				continue
			}
			// The repository **name** goes into `ado_repo_id`, which is what 2.x wrote and what the
			// Azure endpoints then interpolate. It is also what puts `BUG-PROV-a`'s unencoded
			// `repo_id` on the live path for a name that needs percent-encoding: preserved, because
			// every install's rows already read this way.
			if err := d.Projects.LinkProjectADO(ctx, projectID, repo.Org, repo.Project, repo.Repo); err != nil {
				return AutoLinkResult{}, fmt.Errorf("link %s to azure devops: %w", projectID, err)
			}
			return d.reread(ctx, projectID)
		}
	}

	if deferred != nil {
		return *deferred, nil
	}
	return AutoLinkResult{Status: StatusNotDetected}, nil
}

// reread returns the row as it now stands, so the sidebar shows the link it just made without
// asking for the project again.
func (d Deps) reread(ctx context.Context, projectID string) (AutoLinkResult, error) {
	project, err := d.Projects.GetProject(ctx, projectID)
	if err != nil {
		return AutoLinkResult{}, err
	}
	return linkedTo(project), nil
}

// originFirst puts `origin` at the front and leaves the rest in git's own order.
//
// Not a sort: the order git lists remotes in is the order they were added, which is the closest
// thing to "the one the user cares about" after origin itself.
func originFirst(remotes []Remote) []Remote {
	ordered := make([]Remote, 0, len(remotes))
	for _, remote := range remotes {
		if remote.Name == "origin" {
			ordered = append(ordered, remote)
		}
	}
	for _, remote := range remotes {
		if remote.Name != "origin" {
			ordered = append(ordered, remote)
		}
	}
	return ordered
}
