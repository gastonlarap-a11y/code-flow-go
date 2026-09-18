package providers_test

import (
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var github = []string{"github.com"}

// The four shapes a git remote comes in, all of which name the same repository.
func TestGitHubRemoteShapes(t *testing.T) {
	for name, remote := range map[string]string{
		"https":              "https://github.com/owner/repo.git",
		"https without .git": "https://github.com/owner/repo",
		"scp-like":           "git@github.com:owner/repo.git",
		"ssh URL":            "ssh://git@github.com/owner/repo.git",
		"trailing slash":     "https://github.com/owner/repo/",
		"with a port":        "https://github.com:443/owner/repo.git",
	} {
		t.Run(name, func(t *testing.T) {
			detected, ok := providers.DetectGitHub(remote, github)

			require.True(t, ok, remote)
			assert.Equal(t, "github.com", detected.Host)
			assert.Equal(t, "owner", detected.Owner)
			assert.Equal(t, "repo", detected.Repo)
		})
	}
}

// The matched entry travels, not the raw host: everything downstream builds API roots from it.
func TestTheMatchedKnownHostIsWhatIsReturned(t *testing.T) {
	detected, ok := providers.DetectGitHub("https://GitHub.COM/owner/repo.git", github)

	require.True(t, ok)
	assert.Equal(t, "github.com", detected.Host, "the canonical spelling, not the remote's")
}

// A host nobody connected is indistinguishable from any other self-hosted git server, so it falls
// back to manual linking rather than being guessed at.
func TestAnUnknownHostIsNotDetected(t *testing.T) {
	_, ok := providers.DetectGitHub("https://git.example.com/owner/repo.git", github)

	assert.False(t, ok)
}

func TestAConnectedEnterpriseHostIsDetected(t *testing.T) {
	hosts := providers.KnownGitHubHosts([]string{"git.acme.example"})

	detected, ok := providers.DetectGitHub("git@git.acme.example:team/service.git", hosts)

	require.True(t, ok)
	assert.Equal(t, "git.acme.example", detected.Host)
	assert.Equal(t, "team", detected.Owner)
	assert.Equal(t, "service", detected.Repo)
}

func TestKnownHostsAlwaysIncludeGitHubAndDeduplicate(t *testing.T) {
	assert.Equal(t, []string{"github.com"}, providers.KnownGitHubHosts(nil))

	// A host typed into Settings and a host in a remote are two people's spelling of one machine.
	assert.Equal(t, []string{"github.com", "git.acme.example"},
		providers.KnownGitHubHosts([]string{"GitHub.com", "git.acme.example", "  ", "git.acme.example"}))
}

func TestAPathWithTooFewSegments(t *testing.T) {
	for _, remote := range []string{
		"https://github.com/owner",
		"https://github.com/",
		"git@github.com:",
		"not a url at all",
		"",
	} {
		t.Run(remote, func(t *testing.T) {
			_, ok := providers.DetectGitHub(remote, github)
			assert.False(t, ok)
		})
	}
}

// Anything deeper than two segments still names the same repository.
func TestExtraPathSegmentsAreIgnored(t *testing.T) {
	detected, ok := providers.DetectGitHub("https://github.com/owner/repo/tree/main/src", github)

	require.True(t, ok)
	assert.Equal(t, "owner", detected.Owner)
	assert.Equal(t, "repo", detected.Repo)
}

// ---- Azure ---------------------------------------------------------------------------------------

// The three shapes git actually stores, all naming the same repository.
func TestAzureRemoteShapes(t *testing.T) {
	for name, remote := range map[string]string{
		"https":                  "https://dev.azure.com/myorg/MyProject/_git/myrepo",
		"https with .git":        "https://dev.azure.com/myorg/MyProject/_git/myrepo.git",
		"https with org user":    "https://myorg@dev.azure.com/myorg/MyProject/_git/myrepo",
		"clone button SSH":       "git@ssh.dev.azure.com:v3/myorg/MyProject/myrepo",
		"legacy":                 "https://myorg.visualstudio.com/MyProject/_git/myrepo",
		"legacy with collection": "https://myorg.visualstudio.com/DefaultCollection/MyProject/_git/myrepo",
	} {
		t.Run(name, func(t *testing.T) {
			detected, ok := providers.DetectAzure(remote)

			require.True(t, ok, remote)
			assert.Equal(t, "myorg", detected.Org)
			assert.Equal(t, "MyProject", detected.Project)
			assert.Equal(t, "myrepo", detected.Repo)
		})
	}
}

// Exact segment counts, unlike GitHub: a URL with anything extra is not recognised at all rather
// than truncated to fit.
func TestAzureRejectsAnythingExtra(t *testing.T) {
	for _, remote := range []string{
		"https://dev.azure.com/myorg/MyProject/_git/myrepo/extra",
		"https://dev.azure.com/myorg/MyProject/myrepo",
		"https://dev.azure.com/myorg/MyProject",
		"https://myorg.visualstudio.com/MyProject/_git/myrepo/extra",
	} {
		t.Run(remote, func(t *testing.T) {
			_, ok := providers.DetectAzure(remote)
			assert.False(t, ok)
		})
	}
}

// One literal prefix rather than a host pattern, so this near-miss is deliberately not recognised.
func TestTheOtherSSHHostIsNotRecognised(t *testing.T) {
	_, ok := providers.DetectAzure("git@vs-ssh.visualstudio.com:v3/myorg/MyProject/myrepo")

	assert.False(t, ok)
}

// Apart from that one prefix, a remote with no scheme is not an Azure remote.
func TestAzureRejectsTheScpLikeForm(t *testing.T) {
	_, ok := providers.DetectAzure("git@dev.azure.com:myorg/MyProject/_git/myrepo")

	assert.False(t, ok)
}

// The legacy suffix is matched case-sensitively and the organisation keeps the host's own casing:
// it becomes part of the keychain key, so folding it would look up a credential stored elsewhere.
func TestTheLegacyOrgKeepsItsCasing(t *testing.T) {
	detected, ok := providers.DetectAzure("https://MyOrg.visualstudio.com/MyProject/_git/myrepo")

	require.True(t, ok)
	assert.Equal(t, "MyOrg", detected.Org)
}

// BUG-PROV-b, preserved: `%20` becomes a space and nothing else does.
func TestOnlySpacesAreDecoded(t *testing.T) {
	detected, ok := providers.DetectAzure("https://dev.azure.com/my%20org/My%20Project/_git/re%2Fpo")

	require.True(t, ok)
	assert.Equal(t, "my org", detected.Org)
	assert.Equal(t, "My Project", detected.Project)
	assert.Equal(t, "re%2Fpo", detected.Repo, "not a percent-decoder — %2F survives verbatim")
}

// Silly input, but stopping after one pass costs a mismatch nobody would think to look for.
func TestEveryTrailingGitSuffixIsTrimmed(t *testing.T) {
	detected, ok := providers.DetectGitHub("https://github.com/owner/repo.git.git", github)

	require.True(t, ok)
	assert.Equal(t, "repo", detected.Repo)
}

func TestANonAzureRemoteIsNotAzure(t *testing.T) {
	_, ok := providers.DetectAzure("https://github.com/owner/repo.git")

	assert.False(t, ok)
}

// ---- web URLs -------------------------------------------------------------------------------------

// Spaces only, no other escaping: an Azure project called "My Project" is the case this exists
// for, and escaping more would change URLs that currently work.
func TestWebEncodingOnlyReplacesSpaces(t *testing.T) {
	assert.Equal(t, "My%20Project", providers.WebEncode("My Project"))
	assert.Equal(t, "a+b&c", providers.WebEncode("a+b&c"), "deliberately untouched")
}

func TestWebURLs(t *testing.T) {
	assert.Equal(t, "https://github.com/owner/repo",
		providers.GitHubWebURL("github.com", "owner", "repo"))
	assert.Equal(t, "https://git.acme.example/team/service",
		providers.GitHubWebURL("git.acme.example", "team", "service"))
	assert.Equal(t, "https://dev.azure.com/myorg/My%20Project/_git/myrepo",
		providers.AzureWebURL("myorg", "My Project", "myrepo"))
}
