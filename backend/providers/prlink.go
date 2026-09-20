package providers

import (
	"strconv"
	"strings"
)

// Provider is the wire spelling of a pull-request host — the renderer's `VcsProvider`.
type Provider string

// The two hosts this application knows. Both literals reach the renderer inside `AutoLinkResult`
// and `PrLinkResolution`, which switch on them, so they are contracts rather than labels.
const (
	ProviderGitHub Provider = "github"
	ProviderAzure  Provider = "azure"
)

// PRLink is a pull request addressed by a **browser** URL — the thing a user pastes.
//
// Exactly one half is filled, named by Provider, and both halves are the same types a git remote
// resolves to: a link and a remote describe the same repository, and everything downstream (fetch
// the PR, find the local project, build the API root) wants them to arrive in one shape.
type PRLink struct {
	Provider Provider
	GitHub   GitHubRepo
	Azure    AzureRepo
	Number   int64
}

// ParsePRLink reads a pasted pull-request URL, or reports that it is not one (PROV-042).
//
// GitHub is tried first and the first match wins. A URL matching neither grammar is not an error
// here: the caller is the one that can tell "not a pull-request link" from "a link to a host you
// have not connected", and it needs the difference to say something useful.
//
// This is deliberately **not** the remote detector of detection.go. A browser URL and a git remote
// are written by different things, carry different noise (tabs, queries, fragments) and decode
// differently — `BUG-PROV-b`'s `%20`-only decoder is the remote's, and the full decoder below is
// the link's. Merging them would change one of the two.
func ParsePRLink(rawURL string, knownGitHubHosts []string) (PRLink, bool) {
	host, segments, ok := splitLink(rawURL)
	if !ok {
		return PRLink{}, false
	}

	if repo, number, found := parseGitHubPRLink(host, segments, knownGitHubHosts); found {
		return PRLink{Provider: ProviderGitHub, GitHub: repo, Number: number}, true
	}
	if repo, number, found := parseAzurePRLink(host, segments); found {
		return PRLink{Provider: ProviderAzure, Azure: repo, Number: number}, true
	}
	return PRLink{}, false
}

// splitLink reduces a browser URL to its host and its decoded path segments (PROV-039).
func splitLink(rawURL string) (host string, segments []string, ok bool) {
	url := rawURL

	// Fragment first, then query: a GitHub deep link carries both (`/files?w=1#diff-abc`) and
	// neither says anything about which pull request it is.
	if hash := strings.Index(url, "#"); hash >= 0 {
		url = url[:hash]
	}
	if query := strings.Index(url, "?"); query >= 0 {
		url = url[:query]
	}
	url = strings.TrimSuffix(url, "/")

	url, _ = cutScheme(url)

	// Userinfo, taken from the **last** `@` in what is left, as 2.x does. A `@` in a later path
	// segment therefore also counts as the boundary; that is the source's behaviour and the paths
	// this parser sees do not contain one, so it is preserved rather than narrowed.
	url = stripUser(url)

	hostPart, path, found := strings.Cut(url, "/")
	if !found || hostPart == "" {
		return "", nil, false
	}

	decoded := make([]string, 0, 6)
	for _, segment := range strings.Split(path, "/") {
		if segment != "" {
			decoded = append(decoded, percentDecode(segment))
		}
	}
	return hostPart, decoded, true
}

// parseGitHubPRLink matches `{host}/{owner}/{repo}/pull|pulls/{number}[/…]` (PROV-040).
func parseGitHubPRLink(host string, segments, knownHosts []string) (GitHubRepo, int64, bool) {
	matched := ""
	for _, known := range knownHosts {
		if strings.EqualFold(known, host) {
			matched = known
			break
		}
	}
	if matched == "" {
		return GitHubRepo{}, 0, false
	}

	// Four segments at least; anything after the number is a tab (`/files`, `/commits`) and is
	// ignored rather than rejected.
	if len(segments) < 4 {
		return GitHubRepo{}, 0, false
	}
	kind := segments[2]
	if !strings.EqualFold(kind, "pull") && !strings.EqualFold(kind, "pulls") {
		return GitHubRepo{}, 0, false
	}
	number, err := strconv.ParseInt(segments[3], 10, 64)
	if err != nil {
		return GitHubRepo{}, 0, false
	}

	return GitHubRepo{
		Host:  matched,
		Owner: segments[0],
		// One trailing `.git`, unlike a remote's `trimAllGitSuffixes`: this is what 2.x strips here.
		Repo: strings.TrimSuffix(segments[1], ".git"),
	}, number, true
}

// parseAzurePRLink matches both of the shapes Azure's portal produces (PROV-041).
func parseAzurePRLink(host string, segments []string) (AzureRepo, int64, bool) {
	org := ""
	rest := segments

	switch {
	case strings.EqualFold(host, azureHostname):
		if len(segments) == 0 {
			return AzureRepo{}, 0, false
		}
		org, rest = segments[0], segments[1:]

	case strings.HasSuffix(strings.ToLower(host), azureLegacySuffix):
		// The organisation keeps the host's own casing — it becomes part of a keychain key.
		org = host[:len(host)-len(azureLegacySuffix)]
		if len(rest) > 0 && strings.EqualFold(rest[0], "DefaultCollection") {
			rest = rest[1:]
		}

	default:
		return AzureRepo{}, 0, false
	}

	// `{project}/_git/{repo}/pullrequest/{n}`, and the same without the project segment — Azure
	// drops it when it matches the repository's name.
	switch {
	case len(rest) >= 5 && strings.EqualFold(rest[1], "_git") && isPullRequestSegment(rest[3]):
		number, err := strconv.ParseInt(rest[4], 10, 64)
		if err != nil {
			return AzureRepo{}, 0, false
		}
		return AzureRepo{Org: org, Project: rest[0], Repo: rest[2]}, number, true

	case len(rest) >= 4 && strings.EqualFold(rest[0], "_git") && isPullRequestSegment(rest[2]):
		number, err := strconv.ParseInt(rest[3], 10, 64)
		if err != nil {
			return AzureRepo{}, 0, false
		}
		return AzureRepo{Org: org, Project: rest[1], Repo: rest[1]}, number, true
	}
	return AzureRepo{}, 0, false
}

func isPullRequestSegment(segment string) bool {
	return strings.EqualFold(segment, "pullrequest") || strings.EqualFold(segment, "pullrequests")
}

// percentDecode is the link parser's own `%XX` decoder (PROV-038).
//
// A `%` that is not followed by two hex digits — truncated at the end of the string, or followed by
// anything else — is emitted **literally** rather than dropped or refused: the input is a URL a
// person pasted, and a project legitimately called "50% done" is likelier than a malformed escape
// worth an error message.
//
// The decoded bytes are then made valid UTF-8 the lossy way, matching 2.x: a repository name is
// going to be displayed, not round-tripped, so a replacement character beats refusing the link.
func percentDecode(segment string) string {
	if !strings.Contains(segment, "%") {
		return segment
	}

	out := make([]byte, 0, len(segment))
	for i := 0; i < len(segment); {
		if segment[i] == '%' && i+2 < len(segment) {
			high, highOK := hexValue(segment[i+1])
			low, lowOK := hexValue(segment[i+2])
			if highOK && lowOK {
				out = append(out, high<<4|low)
				i += 3
				continue
			}
		}
		out = append(out, segment[i])
		i++
	}
	return strings.ToValidUTF8(string(out), "�")
}

func hexValue(digit byte) (byte, bool) {
	switch {
	case digit >= '0' && digit <= '9':
		return digit - '0', true
	case digit >= 'a' && digit <= 'f':
		return digit - 'a' + 10, true
	case digit >= 'A' && digit <= 'F':
		return digit - 'A' + 10, true
	}
	return 0, false
}
