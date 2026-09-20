package providers_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/providers"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/testvectors"
)

// The port of PrLinkTests: `Parse_matches_the_extracted_vector` over every case in
// pr_link.vectors.json, plus `Every_vector_case_is_exercised`.
//
// The fixture is the test. Its eleven cases were extracted from the C# suite and are consumed
// unchanged here, which is what makes a pass evidence about the grammar rather than about this
// port's reading of it.

type prLinkInput struct {
	URL              string   `json:"url"`
	KnownGitHubHosts []string `json:"knownGithubHosts"`
}

// prLinkExpected is the fixture's flattened variant shape: `type` names the variant and the rest is
// its payload. `expected: null` means the URL is not a pull-request link.
type prLinkExpected struct {
	Type    string `json:"type"`
	Host    string `json:"host"`
	Owner   string `json:"owner"`
	Org     string `json:"org"`
	Project string `json:"project"`
	Repo    string `json:"repo"`
	Number  int64  `json:"number"`
}

func TestParseMatchesTheExtractedVector(t *testing.T) {
	fixture, err := testvectors.LoadUnit("pr_link.vectors.json", "parse")
	require.NoError(t, err)
	require.NotEmpty(t, fixture.Cases)

	for _, testCase := range fixture.Cases {
		t.Run(testCase.ID, func(t *testing.T) {
			var input prLinkInput
			require.NoError(t, testvectors.Decode(testCase, testCase.Input, &input))

			link, ok := providers.ParsePRLink(input.URL, input.KnownGitHubHosts)

			if string(testCase.Expected) == "null" {
				assert.False(t, ok, "the fixture expects this URL to be refused")
				return
			}

			var expected prLinkExpected
			require.NoError(t, testvectors.Decode(testCase, testCase.Expected, &expected))
			require.True(t, ok, "the fixture expects this URL to parse")
			assert.Equal(t, expected.Number, link.Number)

			switch expected.Type {
			case "GitHub":
				require.Equal(t, providers.ProviderGitHub, link.Provider)
				assert.Equal(t, expected.Host, link.GitHub.Host, "the matched allowlist entry travels, not the input host")
				assert.Equal(t, expected.Owner, link.GitHub.Owner)
				assert.Equal(t, expected.Repo, link.GitHub.Repo)
			case "Azure":
				require.Equal(t, providers.ProviderAzure, link.Provider)
				assert.Equal(t, expected.Org, link.Azure.Org)
				assert.Equal(t, expected.Project, link.Azure.Project)
				assert.Equal(t, expected.Repo, link.Azure.Repo)
			default:
				t.Fatalf("unknown variant %q in the fixture", expected.Type)
			}
		})
	}
}

// Every case in the file is exercised above — the guard that a case added to the fixture cannot sit
// unread, which is how a fixture stops being a test without anything failing.
func TestEveryVectorCaseIsExercised(t *testing.T) {
	fixture, err := testvectors.LoadUnit("pr_link.vectors.json", "parse")
	require.NoError(t, err)

	assert.Len(t, fixture.Cases, 11, "eleven cases ship with the specification")
	for _, testCase := range fixture.Cases {
		assert.NotEmpty(t, testCase.ID, "a case with no id cannot be reported on")
		assert.True(t, json.Valid(testCase.Input), "case %s has no readable input", testCase.ID)
	}
}

// The parts of the grammar the fixture does not reach, each one a shape seen in the wild.
func TestLinkShapesTheFixtureDoesNotCover(t *testing.T) {
	hosts := []string{"github.com", "ghe.contoso.com"}

	tests := []struct {
		name     string
		url      string
		provider providers.Provider
		number   int64
		repo     string
	}{
		{name: "the plural kind GitHub's API uses", url: "https://github.com/acme/widget/pulls/8", provider: providers.ProviderGitHub, number: 8, repo: "widget"},
		{name: "a repository whose URL kept its .git", url: "https://github.com/acme/widget.git/pull/8", provider: providers.ProviderGitHub, number: 8, repo: "widget"},
		{name: "the kind in the casing a mail client rewrote", url: "https://github.com/acme/widget/PULL/8", provider: providers.ProviderGitHub, number: 8, repo: "widget"},
		{name: "no scheme, as pasted from a browser's title bar", url: "github.com/acme/widget/pull/8", provider: providers.ProviderGitHub, number: 8, repo: "widget"},
		{name: "a trailing slash", url: "https://github.com/acme/widget/pull/8/", provider: providers.ProviderGitHub, number: 8, repo: "widget"},
		{name: "userinfo in the host", url: "https://someone@github.com/acme/widget/pull/8", provider: providers.ProviderGitHub, number: 8, repo: "widget"},
		{name: "Azure's plural pullrequests", url: "https://dev.azure.com/contoso/Web/_git/api/pullrequests/12", provider: providers.ProviderAzure, number: 12, repo: "api"},
		{name: "Azure with a tab after the number", url: "https://dev.azure.com/contoso/Web/_git/api/pullrequest/12/files", provider: providers.ProviderAzure, number: 12, repo: "api"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			link, ok := providers.ParsePRLink(test.url, hosts)
			require.True(t, ok)
			assert.Equal(t, test.provider, link.Provider)
			assert.Equal(t, test.number, link.Number)
			if test.provider == providers.ProviderGitHub {
				assert.Equal(t, test.repo, link.GitHub.Repo)
			} else {
				assert.Equal(t, test.repo, link.Azure.Repo)
			}
		})
	}
}

func TestLinksThatAreRefused(t *testing.T) {
	hosts := []string{"github.com"}

	for _, url := range []string{
		"",
		"https:///acme/widget/pull/1",
		"https://github.com/acme/widget/pull/notanumber",
		"https://github.com/acme/widget/pull",
		"https://dev.azure.com/contoso/Web/_git/api/pullrequest/notanumber",
		"https://dev.azure.com",
		"https://contoso.visualstudio.com/DefaultCollection/_git/api",
	} {
		t.Run(url, func(t *testing.T) {
			_, ok := providers.ParsePRLink(url, hosts)
			assert.False(t, ok)
		})
	}
}

// A stray `%` is the case worth pinning: PROV-038 leaves it literal, so a project called "50% done"
// survives a paste instead of erroring or losing a character.
func TestPercentDecodingLeavesAStrayEscapeAlone(t *testing.T) {
	link, ok := providers.ParsePRLink("https://dev.azure.com/contoso/50%25%20done/_git/api/pullrequest/4", nil)
	require.True(t, ok)
	assert.Equal(t, "50% done", link.Azure.Project)

	truncated, ok := providers.ParsePRLink("https://dev.azure.com/contoso/done%2/_git/api/pullrequest/4", nil)
	require.True(t, ok)
	assert.Equal(t, "done%2", truncated.Azure.Project, "a truncated escape stays as typed")
}
