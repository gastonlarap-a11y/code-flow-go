package providers

import (
	"context"
	"errors"
)

// ErrNoWebAddress is shown as written when no remote resolves to a host with a web page.
var ErrNoWebAddress = errors.New("Couldn't determine this repository's web address from its remote") //nolint:staticcheck // ST1005: VERBATIM

// RepoWebURL rebuilds a repository's home page from its **live git remote** (REVIEW-005).
//
// Not from the stored link columns, deliberately: `ado_repo_id` can hold a GUID, which has no web
// page, and a project linked moments ago in another window has stale columns here. The remote is
// the thing that cannot be out of date.
//
// Opening it is the renderer's half — `openRepoInBrowser` calls this and hands the answer to the
// shell — so this command is the one that has to refuse when there is nothing to open.
func (d Deps) RepoWebURL(ctx context.Context, projectID string) (string, error) {
	project, err := d.Projects.GetProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	remotes, err := d.Remotes.ListRemotes(ctx, project.LocalPath)
	if err != nil {
		return "", err
	}
	hosts, err := d.knownGitHubHosts(ctx)
	if err != nil {
		return "", err
	}

	// Same order as auto-detection: whatever `origin` says is what the user means by "this
	// repository", and a mirror added later should not win the browser.
	for _, remote := range originFirst(remotes) {
		if repo, ok := DetectGitHub(remote.URL, hosts); ok {
			return GitHubWebURL(repo.Host, repo.Owner, repo.Repo), nil
		}
		if repo, ok := DetectAzure(remote.URL); ok {
			// Always the modern host, even for a remote still on `{org}.visualstudio.com`: the
			// legacy host redirects there anyway, and one form in the address bar is one fewer
			// thing to explain.
			return AzureWebURL(repo.Org, repo.Project, repo.Repo), nil
		}
	}
	return "", ErrNoWebAddress
}
