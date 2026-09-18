// Package providers turns a git remote into a pull-request host, and back into a web address.
//
// Everything here is string handling over URLs a user never typed deliberately: a remote is
// whatever `git clone` wrote, in whichever of four shapes the host offered that day. The rules are
// small and the edge cases are all real ones seen in the wild.
package providers

import (
	"strings"
)

// GitHubRepo is a remote recognised as GitHub.
type GitHubRepo struct {
	// Host is the **matched known-hosts entry**, not the raw host from the remote. The two differ
	// in case — `GitHub.com` in a remote matches the entry `github.com` — and everything
	// downstream builds API roots from it, so the canonical spelling is the one that must travel.
	Host  string
	Owner string
	Repo  string
}

// AzureRepo is a remote recognised as Azure DevOps.
type AzureRepo struct {
	Org     string
	Project string
	Repo    string
}

// DefaultGitHubHost is always a known host, with or without any Enterprise connection.
const DefaultGitHubHost = "github.com"

// KnownGitHubHosts is github.com plus every Enterprise host the user has connected.
//
// Case-insensitive de-duplication, because a host typed into Settings and a host in a remote are
// two different people's spelling of the same machine.
//
// A real Enterprise remote whose host was never connected is indistinguishable, at detection time,
// from any other self-hosted git server — which is why the list is an allowlist rather than a
// pattern: guessing would link a project to a host the user has no credential for.
func KnownGitHubHosts(connected []string) []string {
	hosts := []string{DefaultGitHubHost}

	for _, host := range connected {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		seen := false
		for _, existing := range hosts {
			if strings.EqualFold(existing, host) {
				seen = true
				break
			}
		}
		if !seen {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// DetectGitHub recognises a GitHub remote, or reports that it is not one.
func DetectGitHub(remoteURL string, knownHosts []string) (GitHubRepo, bool) {
	host, path, ok := splitHostPath(remoteURL)
	if !ok {
		return GitHubRepo{}, false
	}

	matched := ""
	for _, known := range knownHosts {
		if strings.EqualFold(known, host) {
			matched = known
			break
		}
	}
	if matched == "" {
		return GitHubRepo{}, false
	}

	// Exactly the first two segments. A deeper path is not a plain clone URL, and the extra
	// segments are dropped rather than making the whole remote unrecognised — the opposite of the
	// Azure rule, and deliberately so: a GitHub URL pasted from a browser has a tree path on it.
	segments := nonEmptySegments(path)
	if len(segments) < 2 {
		return GitHubRepo{}, false
	}
	return GitHubRepo{Host: matched, Owner: segments[0], Repo: trimAllGitSuffixes(segments[1])}, true
}

const (
	azureHost = "dev.azure.com"
	// azureLegacySuffix is the pre-2018 form, still what a great many long-lived repositories have
	// in their remote. Matched **case-sensitively**, as in 2.x, and the organisation keeps the
	// host's own casing rather than being folded — it becomes part of the keychain key, so folding
	// it would look up a credential that was stored under a different name.
	azureLegacySuffix = ".visualstudio.com"
	// azureSSHPrefix is one literal prefix rather than a host pattern, so `vs-ssh.visualstudio.com`
	// is deliberately not recognised.
	azureSSHPrefix = "git@ssh.dev.azure.com:v3/"
)

// DetectAzure recognises an Azure DevOps remote in the three shapes git actually stores:
//
//	https://dev.azure.com/{org}/{project}/_git/{repo}
//	https://{org}.visualstudio.com/[DefaultCollection/]{project}/_git/{repo}
//	git@ssh.dev.azure.com:v3/{org}/{project}/{repo}
//
// Each is matched on an **exact** segment count, unlike the GitHub path: a URL with anything extra
// is not recognised at all rather than truncated to fit. And apart from the one SSH prefix, a
// remote with no scheme is not an Azure remote — the scp-like form is not accepted here.
func DetectAzure(remoteURL string) (AzureRepo, bool) {
	// Applied to the whole URL, and repeatedly, before anything is split off it.
	url := trimAllGitSuffixes(strings.TrimSpace(remoteURL))

	if after, found := strings.CutPrefix(url, azureSSHPrefix); found {
		segments := nonEmptySegments(after)
		if len(segments) != 3 {
			return AzureRepo{}, false
		}
		return AzureRepo{
			Org:     decodeSpaces(segments[0]),
			Project: decodeSpaces(segments[1]),
			Repo:    decodeSpaces(segments[2]),
		}, true
	}

	rest, hasScheme := cutScheme(url)
	if !hasScheme {
		return AzureRepo{}, false
	}

	withoutUser := stripUser(rest)
	host, path, _ := strings.Cut(withoutUser, "/")
	segments := nonEmptySegments(path)

	if strings.EqualFold(host, azureHost) {
		if len(segments) != 4 || segments[2] != "_git" {
			return AzureRepo{}, false
		}
		return AzureRepo{
			Org:     decodeSpaces(segments[0]),
			Project: decodeSpaces(segments[1]),
			Repo:    decodeSpaces(segments[3]),
		}, true
	}

	if !strings.HasSuffix(host, azureLegacySuffix) {
		return AzureRepo{}, false
	}
	org := host[:len(host)-len(azureLegacySuffix)]

	// An optional collection segment, which older organisations still carry.
	if len(segments) > 0 && segments[0] == "DefaultCollection" {
		segments = segments[1:]
	}
	if len(segments) != 3 || segments[1] != "_git" {
		return AzureRepo{}, false
	}
	return AzureRepo{
		Org:     org,
		Project: decodeSpaces(segments[0]),
		Repo:    decodeSpaces(segments[2]),
	}, true
}

// cutScheme removes an http or https scheme, reporting whether there was one.
func cutScheme(url string) (string, bool) {
	for _, scheme := range []string{"https://", "http://"} {
		if after, found := strings.CutPrefix(url, scheme); found {
			return after, true
		}
	}
	return url, false
}

// trimAllGitSuffixes removes **every** trailing `.git`, not just one.
//
// So `repo.git.git` becomes `repo`. Silly input, but the loop costs nothing and stopping after one
// pass costs a mismatch nobody would think to look for.
func trimAllGitSuffixes(value string) string {
	for strings.HasSuffix(value, ".git") {
		value = strings.TrimSuffix(value, ".git")
	}
	return value
}

// decodeSpaces is Azure's own path "decoder": `%20` to a space, and nothing else.
//
// **Not** a percent-decoder — `%2F` and `%C3%A9` survive verbatim. That is `BUG-PROV-b`, preserved:
// it is a different function from the full decoder a pasted browser link goes through, and
// widening it here would change org and project names that are already stored.
func decodeSpaces(segment string) string {
	return strings.ReplaceAll(segment, "%20", " ")
}

// splitHostPath separates a git remote into its host and path.
//
// Two grammars, because git accepts both and hosts hand out both:
//
//	scheme://[user@]host[:port]/path   the URL form
//	[user@]host:path                   the scp-like form, which has no scheme and no slash
//
// Telling them apart is the whole job: `git@github.com:owner/repo.git` has a colon that is not a
// port, and `https://github.com:443/owner/repo` has one that is.
func splitHostPath(remoteURL string) (host, path string, ok bool) {
	remote := strings.TrimSpace(remoteURL)
	if remote == "" {
		return "", "", false
	}

	if scheme := strings.Index(remote, "://"); scheme >= 0 {
		rest := remote[scheme+3:]
		hostPart, pathPart, _ := strings.Cut(rest, "/")
		host, path = stripUser(hostPart), pathPart
	} else {
		hostPart, pathPart, found := strings.Cut(remote, ":")
		if !found {
			return "", "", false
		}
		host, path = stripUser(hostPart), pathPart
	}

	// A port is not part of the host's identity: `github.com:443` is github.com.
	if colon := strings.LastIndex(host, ":"); colon > 0 {
		host = host[:colon]
	}

	host = strings.TrimSpace(host)
	path = strings.Trim(strings.TrimSpace(path), "/")
	if host == "" || path == "" {
		return "", "", false
	}
	return host, path, true
}

func stripUser(host string) string {
	if at := strings.LastIndex(host, "@"); at >= 0 {
		return host[at+1:]
	}
	return host
}

func nonEmptySegments(path string) []string {
	out := make([]string, 0, 4)
	for _, segment := range strings.Split(path, "/") {
		if segment != "" {
			out = append(out, segment)
		}
	}
	return out
}

// WebEncode prepares a path segment for a web URL.
//
// **Spaces only** — no other character is escaped. That is what 2.x did, and it is preserved
// deliberately: an Azure project legitimately called "My Project" is the case this exists for, and
// escaping more would change URLs that currently work for every other project.
func WebEncode(segment string) string {
	return strings.ReplaceAll(segment, " ", "%20")
}

// GitHubWebURL is a GitHub repository's home page.
func GitHubWebURL(host, owner, repo string) string {
	return "https://" + host + "/" + WebEncode(owner) + "/" + WebEncode(repo)
}

// AzureWebURL is an Azure DevOps repository's home page.
func AzureWebURL(org, project, repo string) string {
	return "https://dev.azure.com/" + WebEncode(org) + "/" + WebEncode(project) +
		"/_git/" + WebEncode(repo)
}
