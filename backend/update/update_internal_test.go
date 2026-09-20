package update

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The three pure decisions behind the updater: which tag is newer, which artefact this platform is
// offered, and what digest a published checksum file records.
//
// Internal, unlike the rest of this repository's tests, because none of the three is part of the
// package's API — they are reached through `update_check` and `update_download` — and testing them
// through two HTTP round trips each would say nothing about the rule and a great deal about the
// fixture.

// ---- which tag is newer ------------------------------------------------------------------------

func TestReleaseVersion(t *testing.T) {
	for name, tc := range map[string]struct {
		candidate, current string
		newer              bool
	}{
		// A higher version is newer, in each of the three positions.
		"a higher major is newer":       {"2.0.0", "1.9.9", true},
		"a higher minor is newer":       {"1.3.0", "1.2.9", true},
		"a higher patch is newer":       {"1.2.3", "1.2.2", true},
		"the same version is not newer": {"1.2.3", "1.2.3", false},
		"a lower version is not newer":  {"1.2.2", "1.2.3", false},

		// The one a string comparison gets backwards, which is the entire reason this is parsed
		// rather than compared as text.
		"ten is newer than two":     {"1.10.0", "1.2.0", true},
		"two is not newer than ten": {"1.2.0", "1.10.0", false},

		// The tag prefix is not part of the version.
		"a v-prefixed tag is read as its version": {"v1.2.4", "1.2.3", true},
		"a prefix on both sides still compares":   {"v1.2.3", "v1.2.4", false},
		"an upper-case prefix is read too":        {"V1.2.4", "1.2.3", true},

		// A missing segment counts as zero.
		"a two-segment tag equals its three-segment self":  {"1.2", "1.2.0", false},
		"a three-segment zero equals its two-segment self": {"1.2.0", "1.2", false},
		"a missing segment still loses to a real one":      {"1.2", "1.2.1", false},

		// Pre-releases.
		"a prerelease is older than the release it precedes": {"1.2.0-beta.1", "1.2.0", false},
		"the release beats the prerelease it follows":        {"1.2.0", "1.2.0-beta.1", true},
		"a prerelease still beats an older release":          {"2.0.0-beta.1", "1.9.0", true},

		// Build metadata does not change the answer.
		"build metadata is ignored on the candidate": {"1.2.3+20260919", "1.2.3", false},
		"build metadata is ignored on both":          {"1.2.4+a", "1.2.3+b", true},

		// A tag nobody here chose the shape of does not throw — and does not offer an update
		// either, which is the safe half of not understanding it.
		"an empty tag is not newer":            {"", "1.2.3", false},
		"a word is not newer":                  {"nightly", "1.2.3", false},
		"a real version beats an unparsed one": {"1.0.0", "nightly", true},
		"two unparsed tags compare equal":      {"nightly", "latest", false},
		"a date-shaped tag still compares":     {"2026.9.19", "2026.9.18", true},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.newer, IsNewer(tc.candidate, tc.current))
		})
	}
}

// A version number long past any plausible release must not overflow into a negative one, which
// would make a nonsense tag look *older* than every real version — or, with the sign the other way,
// newer than all of them.
func TestAnAbsurdVersionNumberDoesNotOverflow(t *testing.T) {
	absurd := strings.Repeat("9", 40)

	assert.True(t, IsNewer(absurd+".0.0", "3.0.0"))
	assert.False(t, IsNewer("3.0.0", absurd+".0.0"))
}

// The version the panel shows loses the tag prefix and keeps everything that carries information.
func TestTheDisplayedVersionDropsOnlyTheTagPrefix(t *testing.T) {
	assert.Equal(t, "3.0.1", normalizeVersion("v3.0.1"))
	assert.Equal(t, "3.0.1", normalizeVersion("3.0.1"))
	assert.Equal(t, "3.0.1-rc.2", normalizeVersion("v3.0.1-rc.2"), "a prerelease is part of what was published")
	assert.Equal(t, "3.0.1+build7", normalizeVersion("v3.0.1+build7"))
}

// ---- which artefact this platform is offered ---------------------------------------------------

func windowsRelease() Release {
	return Release{TagName: "v3.0.1", Assets: []Asset{
		{Name: "CodeFlow-Portable-3.0.1-x64.exe", URL: "https://example.test/portable", Size: 90},
		{Name: "CodeFlow-Setup-3.0.1-x64.exe", URL: "https://example.test/setup", Size: 100},
		{Name: "CodeFlow-Setup-3.0.1-x64.exe.blockmap", URL: "https://example.test/blockmap", Size: 1},
		{Name: "CodeFlow-Setup-3.0.1-x64.exe.sha256", URL: "https://example.test/digest", Size: 2},
		{Name: "CodeFlow-3.0.1-arm64.dmg", URL: "https://example.test/dmg", Size: 110},
	}}
}

// The one that updates nothing if it is got wrong: with the install kind at `auto`, whatever is
// chosen here is *executed*. Handing over the portable build launches a loose copy of the new
// version and leaves the installed one untouched — an update that appears to work.
func TestTheWindowsInstallerWinsOverThePortableBuild(t *testing.T) {
	asset := assetFor(windowsRelease(), "windows")

	require.NotNil(t, asset)
	assert.Equal(t, "CodeFlow-Setup-3.0.1-x64.exe", asset.Name)
}

// A release older than v1.7.6 carries no `-Setup-` marker. An unmarked lone `.exe` is still
// offered rather than refused: refusing would mean nobody on an old build could ever update again.
func TestAnOlderReleaseWithoutTheMarkerIsStillOffered(t *testing.T) {
	asset := assetFor(Release{Assets: []Asset{
		{Name: "CodeFlow.1.7.5.exe", URL: "https://example.test/old"},
	}}, "windows")

	require.NotNil(t, asset)
	assert.Equal(t, "CodeFlow.1.7.5.exe", asset.Name)
}

func TestASha256FileIsNeverChosen(t *testing.T) {
	asset := assetFor(Release{Assets: []Asset{
		{Name: "CodeFlow-Setup-3.0.1-x64.exe.sha256"},
		{Name: "CodeFlow-Setup-3.0.1-x64.exe"},
	}}, "windows")

	require.NotNil(t, asset)
	assert.Equal(t, "CodeFlow-Setup-3.0.1-x64.exe", asset.Name)
}

func TestABlockmapIsNeverMistakenForAnInstaller(t *testing.T) {
	asset := assetFor(Release{Assets: []Asset{
		{Name: "CodeFlow-Setup-3.0.1-x64.exe.blockmap"},
	}}, "windows")

	assert.Nil(t, asset, "a differential-download index is not an installer")
}

func TestEachPlatformIsOfferedItsOwnInstaller(t *testing.T) {
	release := windowsRelease()

	windows := assetFor(release, "windows")
	require.NotNil(t, windows)
	assert.Equal(t, "CodeFlow-Setup-3.0.1-x64.exe", windows.Name)

	mac := assetFor(release, "darwin")
	require.NotNil(t, mac)
	assert.Equal(t, "CodeFlow-3.0.1-arm64.dmg", mac.Name)
}

func TestAReleaseWithoutThisPlatformOffersNothing(t *testing.T) {
	onlyWindows := Release{Assets: []Asset{{Name: "CodeFlow-Setup-3.0.1-x64.exe"}}}

	assert.Nil(t, assetFor(onlyWindows, "darwin"))
	assert.Nil(t, assetFor(Release{}, "windows"))
	// Linux has never had a target, and inventing an `.AppImage` branch would be a guess about a
	// release shape nothing publishes.
	assert.Nil(t, assetFor(onlyWindows, "linux"))
}

func TestOnlyWindowsClaimsItCanInstallOnItsOwn(t *testing.T) {
	assert.Equal(t, "auto", installKindFor("windows"))
	assert.Equal(t, "manual", installKindFor("darwin"))
	assert.Equal(t, "manual", installKindFor("linux"))
}

// The checksum is found by the artefact's own name with `.sha256` after it, case-insensitively —
// and the lookup is what binds the file to the artefact, which is what the single-entry rule below
// relies on.
func TestTheDigestFileIsFoundByTheArtefactsName(t *testing.T) {
	release := windowsRelease()

	found := digestAssetFor(release, "CodeFlow-Setup-3.0.1-x64.exe")
	require.NotNil(t, found)
	assert.Equal(t, "https://example.test/digest", found.URL)

	assert.Nil(t, digestAssetFor(release, "CodeFlow-Portable-3.0.1-x64.exe"),
		"the portable build publishes none in this release")
}

// The release 2.7.x will be offered, over the exact artefact names CI produces (MIGRATION-GO §5.5).
//
// This is the test that decides whether anyone reaches 3.0.0 at all. The 2.7.x updater runs *this
// rule* — the port is the same logic — so if it picks the portable build here, every Windows user
// upgrading from 2.7.1 launches a loose copy of 3.0.0 and keeps the Electron install they had. The
// names are the ones `win.artifactName` and `mac.artifactName` produce, and they must not acquire a
// space: GitHub rewrites spaces to dots when it stores an asset, which is the v1.7.5 incident.
func TestTheThreeZeroZeroReleaseIsPickedCorrectlyByTheVersionBeingReplaced(t *testing.T) {
	release := Release{TagName: "v3.0.0", Assets: []Asset{
		{Name: "CodeFlow-3.0.0-arm64.dmg"},
		{Name: "CodeFlow-3.0.0-arm64.dmg.sha256"},
		{Name: "CodeFlow-Setup-3.0.0-x64.exe"},
		{Name: "CodeFlow-Setup-3.0.0-x64.exe.sha256"},
		{Name: "CodeFlow-Portable-3.0.0-x64.exe"},
		{Name: "CodeFlow-Portable-3.0.0-x64.exe.sha256"},
	}}

	for _, name := range []string{
		"CodeFlow-3.0.0-arm64.dmg", "CodeFlow-Setup-3.0.0-x64.exe", "CodeFlow-Portable-3.0.0-x64.exe",
	} {
		assert.NotContains(t, name, " ", "GitHub rewrites a space to a dot and the digest stops matching")
	}

	windows := assetFor(release, "windows")
	require.NotNil(t, windows)
	assert.Equal(t, "CodeFlow-Setup-3.0.0-x64.exe", windows.Name, "the installer, never the portable build")
	require.NotNil(t, digestAssetFor(release, windows.Name), "an artefact with no digest is refused")

	mac := assetFor(release, "darwin")
	require.NotNil(t, mac)
	assert.Equal(t, "CodeFlow-3.0.0-arm64.dmg", mac.Name)
	require.NotNil(t, digestAssetFor(release, mac.Name))

	// And 2.7.1 sees it as newer, which is the other half of "anyone reaches 3.0.0".
	assert.True(t, IsNewer(release.TagName, "2.7.1"))
}

// ---- what a published checksum file records ----------------------------------------------------

const digestHex = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func TestTheDigestIsReadFromAPlainLine(t *testing.T) {
	digest, found := DigestFor(digestHex+"  CodeFlow-Setup-3.0.1-x64.exe\n", "CodeFlow-Setup-3.0.1-x64.exe")

	assert.True(t, found)
	assert.Equal(t, digestHex, digest)
}

func TestABinaryMarkerIsNotPartOfTheName(t *testing.T) {
	// GNU's `*` says the file was hashed in binary mode. It belongs to the format, not to the name.
	content := digestHex + " *CodeFlow-Setup-3.0.1-x64.exe\n" +
		"aaaa  something-else.exe\n"

	digest, found := DigestFor(content, "CodeFlow-Setup-3.0.1-x64.exe")
	assert.True(t, found)
	assert.Equal(t, digestHex, digest)
}

func TestARecordedDirectoryIsNotPartOfTheName(t *testing.T) {
	content := digestHex + "  dist/CodeFlow-Setup-3.0.1-x64.exe\n" +
		"aaaa  dist/other.exe\n"

	digest, found := DigestFor(content, "CodeFlow-Setup-3.0.1-x64.exe")
	assert.True(t, found)
	assert.Equal(t, digestHex, digest)

	// And a Windows-recorded path, since the file may be written on either platform.
	windowsPath := digestHex + `  dist\CodeFlow-Setup-3.0.1-x64.exe` + "\naaaa  other.exe\n"
	digest, found = DigestFor(windowsPath, "CodeFlow-Setup-3.0.1-x64.exe")
	assert.True(t, found)
	assert.Equal(t, digestHex, digest)
}

// The split is on the first whitespace run only. `strings.Fields` would cut this name in two and
// match nothing — which is how v1.7.5 refused every Windows update.
func TestANameWithSpacesSurvives(t *testing.T) {
	content := digestHex + "  CodeFlow 1.7.5.exe\n" +
		"aaaa  CodeFlow 1.7.5.dmg\n"

	digest, found := DigestFor(content, "CodeFlow 1.7.5.exe")
	assert.True(t, found)
	assert.Equal(t, digestHex, digest)
}

// The rule v1.7.5 shipped without: GitHub rewrites spaces to dots when it stores a release asset,
// so the API answered `CodeFlow.1.7.5.exe` while `sha256sum` had recorded `CodeFlow 1.7.5.exe`.
// With one entry the binding is already established by how the file was fetched.
func TestASingleEntryFileIsTrustedWithoutMatchingTheName(t *testing.T) {
	content := digestHex + "  CodeFlow 1.7.5.exe\n"

	digest, found := DigestFor(content, "CodeFlow.1.7.5.exe")
	assert.True(t, found, "the name disagrees and the file is still this artefact's")
	assert.Equal(t, digestHex, digest)

	// Including a file that records no name at all.
	digest, found = DigestFor(digestHex+"\n", "anything.exe")
	assert.True(t, found)
	assert.Equal(t, digestHex, digest)
}

func TestTheRightLineIsPickedOutOfSeveral(t *testing.T) {
	content := "1111111111111111111111111111111111111111111111111111111111111111  CodeFlow-3.0.1-arm64.dmg\n" +
		digestHex + "  CodeFlow-Setup-3.0.1-x64.exe\n" +
		"2222222222222222222222222222222222222222222222222222222222222222  CodeFlow-Portable-3.0.1-x64.exe\n"

	digest, found := DigestFor(content, "CodeFlow-Setup-3.0.1-x64.exe")
	assert.True(t, found)
	assert.Equal(t, digestHex, digest)
}

func TestAMultiEntryFileThatDoesNotListTheAssetYieldsNothing(t *testing.T) {
	content := "1111111111111111111111111111111111111111111111111111111111111111  a.exe\n" +
		"2222222222222222222222222222222222222222222222222222222222222222  b.exe\n"

	_, found := DigestFor(content, "CodeFlow-Setup-3.0.1-x64.exe")
	assert.False(t, found)
}

func TestAnEmptyFileYieldsNothing(t *testing.T) {
	for name, content := range map[string]string{
		"nothing at all":  "",
		"only whitespace": "   \n\t\n",
		"only newlines":   "\n\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, found := DigestFor(content, "CodeFlow-Setup-3.0.1-x64.exe")
			assert.False(t, found)
		})
	}
}

// With several entries the name is the evidence, so a near miss is a miss. The alternative —
// prefix or contains matching — would let `CodeFlow-Setup-3.0.1-x64.exe` be verified against the
// line recorded for `CodeFlow-Setup-3.0.10-x64.exe`.
func TestANameThatOnlyLooksSimilarDoesNotMatch(t *testing.T) {
	content := digestHex + "  CodeFlow-Setup-3.0.10-x64.exe\n" +
		"aaaa  CodeFlow-Setup-3.0.1-x64.exe.blockmap\n"

	_, found := DigestFor(content, "CodeFlow-Setup-3.0.1-x64.exe")
	assert.False(t, found)
}

// A file written on Windows arrives with CRLF line endings, and the `\r` must not become part of
// the name it is read from.
func TestCarriageReturnsAreNotPartOfTheName(t *testing.T) {
	content := "aaaa  other.exe\r\n" + digestHex + "  CodeFlow-Setup-3.0.1-x64.exe\r\n"

	digest, found := DigestFor(content, "CodeFlow-Setup-3.0.1-x64.exe")
	assert.True(t, found)
	assert.Equal(t, digestHex, digest)
}

// ---- where the bytes are allowed to land -------------------------------------------------------

// The asset name decides the path the download is written to, and it arrives from the renderer.
// BOOT-021 keeps the caller from choosing what the bytes are checked against; this keeps it from
// choosing where they go.
func TestAnAssetNameThatIsAPathIsRefused(t *testing.T) {
	for name, given := range map[string]string{
		"a parent traversal":     "../../.zshrc",
		"an absolute path":       "/etc/hosts",
		"a windows traversal":    `..\..\autoexec.bat`,
		"a bare parent":          "..",
		"a bare dot":             ".",
		"a nested relative path": "dist/CodeFlow-Setup.exe",
		"nothing at all":         "",
		"nothing but whitespace": "   ",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := safeAssetName(given)
			require.Error(t, err)
		})
	}
}

func TestAnOrdinaryAssetNamePassesThrough(t *testing.T) {
	name, err := safeAssetName("  CodeFlow-Setup-3.0.1-x64.exe  ")

	require.NoError(t, err)
	assert.Equal(t, "CodeFlow-Setup-3.0.1-x64.exe", name)
}

// ---- which credential the feed is read with ----------------------------------------------------

// The cascade is tested here rather than through the command surface for one reason: its second
// step shells out to `gh auth token`, and a test that ran the real one would pass or fail according
// to whether the machine running it happens to be signed in to the GitHub CLI.

type stubCredentials struct {
	token string
	err   error
}

func (c stubCredentials) GitHubToken(string) (string, error) { return c.token, c.err }

func TestTheKeychainIsAskedFirst(t *testing.T) {
	asked := false
	service := NewService(Deps{Credentials: stubCredentials{token: "from-the-keychain"}})
	service.ghToken = func(context.Context) string {
		asked = true
		return "from-gh"
	}

	token, err := service.token(t.Context())

	require.NoError(t, err)
	assert.Equal(t, "from-the-keychain", token)
	assert.False(t, asked, "the CLI is a fallback, not a second opinion")
}

// The fallback that makes this work for someone who signed in with `gh` and never pasted a token
// into CodeFlow.
func TestTheGitHubCLIIsTheFallback(t *testing.T) {
	for name, credentials := range map[string]Credentials{
		"nothing is stored":        stubCredentials{},
		"no credential store":      nil,
		"the store said no":        stubCredentials{err: errors.New("the keychain is locked")},
		"the store answered empty": stubCredentials{token: ""},
	} {
		t.Run(name, func(t *testing.T) {
			service := NewService(Deps{Credentials: credentials})
			service.ghToken = func(context.Context) string { return "from-gh" }

			token, err := service.token(t.Context())

			require.NoError(t, err)
			assert.Equal(t, "from-gh", token)
		})
	}
}

// Both halves empty is the `no-credential` reason, and it is reported as an *answer* — the panel
// says it could not check and why, rather than the command failing.
func TestNeitherHalfOfTheCascadeIsNoCredential(t *testing.T) {
	service := NewService(Deps{})
	service.ghToken = func(context.Context) string { return "" }

	_, err := service.token(t.Context())
	require.ErrorIs(t, err, errNoCredential)

	answer := service.Check(t.Context())
	assert.Equal(t, reasonNoCredential, answer.Reason)
	assert.False(t, answer.Available)
	assert.Equal(t, "0.0.0", answer.CurrentVersion)
}

// A locked keychain reaches `gh` and, when that has nothing either, reports `no-credential` — which
// names the wrong cause. It is the deliberate trade: an hourly background check that raised a
// keychain error toast every hour over a secret it can live without would be worse.
func TestALockedKeychainDegradesToNoCredentialRatherThanFailing(t *testing.T) {
	service := NewService(Deps{Credentials: stubCredentials{err: errors.New("the keychain is locked")}})
	service.ghToken = func(context.Context) string { return "" }

	answer := service.Check(t.Context())

	assert.Equal(t, reasonNoCredential, answer.Reason)
}

// `gh` prints the token and nothing else today. A future version printing a notice after it must
// not turn the notice into part of the token: a header value containing a newline is rejected by
// Go's transport outright, so the failure would surface as an unexplained `unreachable`.
func TestOnlyTheFirstLineOfTheCLIsAnswerIsTheToken(t *testing.T) {
	for given, want := range map[string]string{
		"gho_abc\n":                        "gho_abc",
		"  gho_abc  \n":                    "gho_abc",
		"gho_abc\nnote: a new release\n":   "gho_abc",
		"gho_abc\r\nnote: a new release\n": "gho_abc",
		"":                                 "",
		"\n\n":                             "",
	} {
		assert.Equal(t, want, firstLine(given))
	}
}
