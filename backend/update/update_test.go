package update_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge/jsonwire"
	"github.com/gastonlarap-a11y/code-flow/backend/update"
)

// ---- the fakes ---------------------------------------------------------------------------------

// fakeCredentials is the keychain half of the token cascade. Every test here supplies a token: the
// other half of the cascade runs `gh auth token`, and a test that depended on whether the machine
// running it happens to be signed in to the GitHub CLI would pass or fail for reasons that have
// nothing to do with this code. That half is exercised in the internal tests, where it has a seam.
type fakeCredentials struct {
	token string
	hosts []string
}

func (c *fakeCredentials) GitHubToken(host string) (string, error) {
	c.hosts = append(c.hosts, host)
	return c.token, nil
}

// fakeOpener is the hand-off. It records the path rather than launching an installer, which is the
// one step of this pipeline a test must never actually perform.
type fakeOpener struct {
	opened string
	err    error
}

func (o *fakeOpener) OpenFile(path string) error {
	o.opened = path
	return o.err
}

// ---- a release, served ---------------------------------------------------------------------------

// release is a GitHub release a test server publishes, holding the artefacts' real bytes so the
// digests it serves are the real ones.
type release struct {
	mu sync.Mutex

	baseURL string
	tag     string
	draft   bool
	status  int
	notes   string
	garbage bool

	// artefacts maps an asset name to its contents. Each gets a `<name>.sha256` sibling unless it
	// is listed in noDigest, and what that sibling contains can be replaced through digestBody.
	artefacts  map[string][]byte
	noDigest   map[string]bool
	digestBody map[string]string

	requests []recordedRequest
}

type recordedRequest struct {
	path          string
	authorization string
	userAgent     string
	accept        string
	apiVersion    string
}

// newRelease publishes one artefact per platform, each with its digest beside it, which is the
// shape a real release has.
func newRelease() *release {
	return &release{
		tag:    "v9.9.9",
		status: http.StatusOK,
		notes:  "## What's new\n\n- Everything.",
		artefacts: map[string][]byte{
			"CodeFlow-Setup-9.9.9-x64.exe": []byte("the windows installer, pretend"),
			"CodeFlow-9.9.9-arm64.dmg":     []byte("the macos disk image, pretend"),
		},
		noDigest:   map[string]bool{},
		digestBody: map[string]string{},
	}
}

// serve starts the test server and answers its feed url.
//
// The feed's asset urls point back at this same server, which is what makes the digest the
// downloader fetches the one *the release* publishes rather than anything a caller handed in.
func (r *release) serve(t *testing.T) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.record(req)

		switch {
		case req.URL.Path == "/releases/latest":
			r.writeFeed(w)
		case strings.HasPrefix(req.URL.Path, "/assets/"):
			r.writeAsset(w, strings.TrimPrefix(req.URL.Path, "/assets/"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	r.mu.Lock()
	r.baseURL = server.URL
	r.mu.Unlock()

	return server.URL + "/releases/latest"
}

func (r *release) record(req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, recordedRequest{
		path:          req.URL.Path,
		authorization: req.Header.Get("Authorization"),
		userAgent:     req.Header.Get("User-Agent"),
		accept:        req.Header.Get("Accept"),
		apiVersion:    req.Header.Get("X-GitHub-Api-Version"),
	})
}

func (r *release) recorded() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append(make([]recordedRequest, 0, len(r.requests)), r.requests...)
}

func (r *release) writeFeed(w http.ResponseWriter) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.status != http.StatusOK {
		w.WriteHeader(r.status)
		_, _ = w.Write([]byte(`{"message":"no"}`))
		return
	}
	if r.garbage {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name": 17, "assets": "not an array"}`))
		return
	}

	payload := update.Release{
		TagName:     r.tag,
		Name:        "CodeFlow " + r.tag,
		Body:        r.notes,
		PublishedAt: "2026-09-19T10:00:00Z",
		Draft:       r.draft,
		Assets:      make([]update.Asset, 0, 2*len(r.artefacts)),
	}
	for name, content := range r.artefacts {
		payload.Assets = append(payload.Assets, update.Asset{
			Name: name,
			URL:  r.baseURL + "/assets/" + name,
			Size: int64(len(content)),
		})
		if r.noDigest[name] {
			continue
		}
		payload.Assets = append(payload.Assets, update.Asset{
			Name: name + ".sha256",
			URL:  r.baseURL + "/assets/" + name + ".sha256",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (r *release) writeAsset(w http.ResponseWriter, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if artefact, isDigest := strings.CutSuffix(name, ".sha256"); isDigest {
		content, known := r.artefacts[artefact]
		if !known || r.noDigest[artefact] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if body, replaced := r.digestBody[artefact]; replaced {
			_, _ = fmt.Fprint(w, body)
			return
		}
		_, _ = fmt.Fprintf(w, "%x  %s\n", sha256.Sum256(content), artefact)
		return
	}

	content, known := r.artefacts[name]
	if !known {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, _ = w.Write(content)
}

// set runs a mutation against the release while it is already being served.
func (r *release) set(mutate func(*release)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	mutate(r)
}

// ---- the harness -------------------------------------------------------------------------------

type harness struct {
	registry  *bridge.Registry
	events    *bridge.RecordingEmitter
	opener    *fakeOpener
	downloads string
	feed      *release
}

func newHarness(t *testing.T, version string, feed *release, credentials *fakeCredentials) *harness {
	t.Helper()

	events := &bridge.RecordingEmitter{}
	opener := &fakeOpener{}
	downloads := t.TempDir()

	registry := bridge.NewRegistry()
	update.Register(registry, update.Deps{
		Version:     version,
		Credentials: credentials,
		Emitter:     events,
		Opener:      opener,
		FeedURL:     feed.serve(t),
		Downloads:   downloads,
	})
	registry.Seal()

	return &harness{registry: registry, events: events, opener: opener, downloads: downloads, feed: feed}
}

func (h *harness) invoke(t *testing.T, name string, params map[string]any) (any, error) {
	t.Helper()

	handler, found := h.registry.Lookup(name)
	require.True(t, found, "%s is not registered", name)

	raw, err := json.Marshal(params)
	require.NoError(t, err)
	return handler(t.Context(), bridge.NewParams(raw))
}

func (h *harness) check(t *testing.T) update.Availability {
	t.Helper()

	answer, err := h.invoke(t, "update_check", nil)
	require.NoError(t, err, "a check answers rather than failing")

	availability, ok := answer.(update.Availability)
	require.True(t, ok, "update_check answers an Availability")
	return availability
}

// download names the artefact this platform was offered, which is the one the rules chose — the
// choice itself is pinned per platform in the internal tests.
func (h *harness) download(t *testing.T, found update.Availability) (any, error) {
	t.Helper()
	return h.invoke(t, "update_download",
		map[string]any{"assetUrl": found.AssetURL, "assetName": found.AssetName})
}

func goodCredentials() *fakeCredentials { return &fakeCredentials{token: "ghp_the-users-own-token"} }

// ---- the command surface -----------------------------------------------------------------------

func TestTheThreeCommandsAreRegistered(t *testing.T) {
	registry := bridge.NewRegistry()
	update.Register(registry, update.Deps{})
	registry.Seal()

	for _, name := range []string{"update_current_version", "update_check", "update_download"} {
		_, found := registry.Lookup(name)
		assert.True(t, found, "%s is not registered", name)
	}
}

// The renderer reads it once at start-up and caches it, so it has to be the build's own version and
// not something derived at call time.
func TestTheCurrentVersionIsWhatTheBuildWasStampedWith(t *testing.T) {
	harness := newHarness(t, "3.0.0", newRelease(), goodCredentials())

	answer, err := harness.invoke(t, "update_current_version", nil)
	require.NoError(t, err)
	assert.Equal(t, "3.0.0", answer)
}

// A development build reports the same `0.0.0` the 2.x core answered when it was started without
// `--app-version`, so the "am I current?" comparison sees the shape it always did.
func TestAnUnstampedBuildReportsTheDevelopmentVersion(t *testing.T) {
	registry := bridge.NewRegistry()
	update.Register(registry, update.Deps{})
	registry.Seal()

	handler, _ := registry.Lookup("update_current_version")
	answer, err := handler(t.Context(), bridge.NewParams(nil))

	require.NoError(t, err)
	assert.Equal(t, "0.0.0", answer)
}

func TestUpdateDownloadNamesAMissingParameter(t *testing.T) {
	harness := newHarness(t, "3.0.0", newRelease(), goodCredentials())

	_, err := harness.invoke(t, "update_download", map[string]any{"assetName": "x.dmg"})
	require.Error(t, err)
	assert.Equal(t, "missing required parameter 'assetUrl'", err.Error())

	_, err = harness.invoke(t, "update_download", map[string]any{"assetUrl": "https://example.test/x"})
	require.Error(t, err)
	assert.Equal(t, "missing required parameter 'assetName'", err.Error())
}

// ---- checking ----------------------------------------------------------------------------------

func TestANewerReleaseIsOffered(t *testing.T) {
	harness := newHarness(t, "3.0.0", newRelease(), goodCredentials())

	found := harness.check(t)

	assert.True(t, found.Available)
	assert.Equal(t, "3.0.0", found.CurrentVersion)
	assert.Equal(t, "9.9.9", found.Version, "the tag prefix is not shown beside a bare current version")
	assert.Equal(t, "## What's new\n\n- Everything.", found.Notes)
	assert.Equal(t, "2026-09-19T10:00:00Z", found.Date)
	assert.Empty(t, found.Reason, "an available update carries no reason")
	assert.NotEmpty(t, found.AssetName)
	assert.NotEmpty(t, found.AssetURL)
	assert.Positive(t, found.AssetSize)
	assert.Contains(t, []string{"auto", "manual"}, found.InstallKind)
}

// The answer the renderer turns into `null`: current, and no reason to explain. Reporting "up to
// date" is only honest when two real versions were compared, which is what an empty reason means.
func TestAnAlreadyCurrentInstallIsOfferedNothingAndToldNothing(t *testing.T) {
	feed := newRelease()
	feed.tag = "v3.0.0"
	harness := newHarness(t, "3.0.0", feed, goodCredentials())

	found := harness.check(t)

	assert.False(t, found.Available)
	assert.Empty(t, found.Reason)
	assert.Equal(t, "3.0.0", found.CurrentVersion)
}

func TestAnOlderReleaseIsNotOffered(t *testing.T) {
	feed := newRelease()
	feed.tag = "v2.7.1"
	harness := newHarness(t, "3.0.0", feed, goodCredentials())

	found := harness.check(t)

	assert.False(t, found.Available)
	assert.Empty(t, found.Reason)
}

// The feed names the repository this application's releases are published to.
//
// Pinned because the test above passes either way. 3.0.0 and 3.1.0 shipped asking `code-flow`,
// whose latest release is 2.7.1 — older than themselves — so every check answered "up to date",
// correctly, forever. The arithmetic was never wrong; the shelf was. Nothing in the comparison can
// catch that, and neither can an integration test that serves its own feed, so what is asserted is
// the one fact both depend on.
//
// **This moves to `code-flow` at the cutover (§14 D2) and not before**, at which point this test is
// the reminder that the constant is the decision.
func TestTheFeedNamesTheRepositoryTheReleasesArePublishedTo(t *testing.T) {
	assert.Equal(t,
		"https://api.github.com/repos/gastonlarap-a11y/code-flow-go/releases/latest",
		update.GitHubFeedURL,
		"the updater must read the repository `.github/workflows/release.yml` publishes to")
}

// An unavailable answer carries why, and the running version with it — the panel shows both, and a
// reason with no version beside it is half a sentence.
func TestAnUnavailableAnswerCarriesWhyAndTheRunningVersion(t *testing.T) {
	for name, tc := range map[string]struct {
		arrange func(*release)
		reason  string
	}{
		"a token that was not accepted is unauthorized": {
			arrange: func(r *release) { r.status = http.StatusUnauthorized },
			reason:  "unauthorized",
		},
		"a forbidden answer is unauthorized too": {
			arrange: func(r *release) { r.status = http.StatusForbidden },
			reason:  "unauthorized",
		},
		"any other failure is no-release": {
			arrange: func(r *release) { r.status = http.StatusInternalServerError },
			reason:  "no-release",
		},
		"a repository with no release is no-release": {
			arrange: func(r *release) { r.status = http.StatusNotFound },
			reason:  "no-release",
		},
		"a draft is not a published update": {
			arrange: func(r *release) { r.draft = true },
			reason:  "no-release",
		},
		"a 200 that is not a release is no-release": {
			arrange: func(r *release) { r.garbage = true },
			reason:  "no-release",
		},
		"a newer release with nothing for this platform is no-asset": {
			arrange: func(r *release) {
				r.artefacts = map[string][]byte{"CodeFlow-9.9.9.tar.gz": []byte("nope")}
			},
			reason: "no-asset",
		},
	} {
		t.Run(name, func(t *testing.T) {
			feed := newRelease()
			tc.arrange(feed)
			harness := newHarness(t, "3.0.0", feed, goodCredentials())

			found := harness.check(t)

			assert.False(t, found.Available)
			assert.Equal(t, tc.reason, found.Reason)
			assert.Equal(t, "3.0.0", found.CurrentVersion, "the running version is reported whatever happened")
			assert.NotEmpty(t, found.InstallKind, "what this platform does with an installer is not a property of the release")
		})
	}
}

// A feed that cannot be reached at all is `unreachable`, which is a different sentence from every
// answer the server could have given.
func TestAFeedThatCannotBeReachedIsUnreachable(t *testing.T) {
	registry := bridge.NewRegistry()
	update.Register(registry, update.Deps{
		Version:     "3.0.0",
		Credentials: goodCredentials(),
		// Port 1 on loopback: resolvable without a lookup, and nothing listens there.
		FeedURL:   "http://127.0.0.1:1/releases/latest",
		Downloads: t.TempDir(),
	})
	registry.Seal()

	handler, _ := registry.Lookup("update_check")
	answer, err := handler(t.Context(), bridge.NewParams(nil))
	require.NoError(t, err)

	found, ok := answer.(update.Availability)
	require.True(t, ok)
	assert.Equal(t, "unreachable", found.Reason)
	assert.Equal(t, "3.0.0", found.CurrentVersion)
}

// The four headers the feed is read with. The User-Agent is not optional — GitHub answers 403 to a
// request without one — and the API version is pinned so a change to GitHub's default cannot change
// what an installed 3.0.0 sees.
func TestTheFeedIsReadWithTheUsersOwnTokenAndAPinnedAPIVersion(t *testing.T) {
	credentials := goodCredentials()
	harness := newHarness(t, "3.0.0", newRelease(), credentials)

	harness.check(t)

	requests := harness.feed.recorded()
	require.Len(t, requests, 1)
	assert.Equal(t, "Bearer ghp_the-users-own-token", requests[0].authorization)
	assert.Equal(t, "application/vnd.github+json", requests[0].accept)
	assert.Equal(t, "2022-11-28", requests[0].apiVersion)
	assert.Equal(t, "CodeFlow/3.0.0", requests[0].userAgent)

	assert.Equal(t, []string{"github.com"}, credentials.hosts,
		"the updater reads the token saved for github.com, whatever else is connected")
}

// A GitHub release payload deserialises by its own names: a field renamed on this side compiles and
// silently arrives empty, which here would mean "no update" for every user forever.
func TestAGitHubReleasePayloadDeserialisesByItsOwnNames(t *testing.T) {
	var decoded update.Release
	require.NoError(t, json.Unmarshal([]byte(`{
		"tag_name": "v9.9.9",
		"name": "CodeFlow 9.9.9",
		"body": "notes",
		"published_at": "2026-09-19T10:00:00Z",
		"draft": false,
		"prerelease": true,
		"assets": [
			{"name": "CodeFlow-9.9.9-arm64.dmg", "browser_download_url": "https://example.test/dmg", "size": 12345}
		]
	}`), &decoded))

	assert.Equal(t, "v9.9.9", decoded.TagName)
	assert.Equal(t, "CodeFlow 9.9.9", decoded.Name)
	assert.Equal(t, "notes", decoded.Body)
	assert.Equal(t, "2026-09-19T10:00:00Z", decoded.PublishedAt)
	assert.False(t, decoded.Draft)
	assert.True(t, decoded.Prerelease)
	require.Len(t, decoded.Assets, 1)
	assert.Equal(t, "https://example.test/dmg", decoded.Assets[0].URL)
	assert.Equal(t, int64(12345), decoded.Assets[0].Size)
}

// The wire shape `lib/bridge/updater.ts` declares. Every key snake_case and present: the renderer
// reads `install_kind` unconditionally and would render "undefined" into the modal for a missing
// one.
func TestTheAvailabilityCrossesInTheRenderersShape(t *testing.T) {
	harness := newHarness(t, "3.0.0", newRelease(), goodCredentials())

	raw, err := jsonwire.Marshal(harness.check(t))
	require.NoError(t, err)

	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &keys))

	for _, key := range []string{
		"available", "current_version", "version", "notes", "date",
		"asset_name", "asset_url", "asset_size", "install_kind", "reason",
	} {
		assert.Contains(t, keys, key)
	}
	assert.Len(t, keys, 10, "every field the renderer's Availability declares, and nothing else")
}

// ---- downloading -------------------------------------------------------------------------------

func TestAnArtefactMatchingItsDigestIsHandedOver(t *testing.T) {
	harness := newHarness(t, "3.0.0", newRelease(), goodCredentials())
	found := harness.check(t)

	answer, err := harness.download(t, found)
	require.NoError(t, err)

	destination := filepath.Join(harness.downloads, found.AssetName)
	assert.Equal(t, destination, answer, "the command answers where the artefact landed")

	written, err := os.ReadFile(destination)
	require.NoError(t, err)
	assert.Equal(t, harness.feed.artefacts[found.AssetName], written)

	// And it was handed to the operating system: on Windows that runs the installer, on macOS it
	// mounts the disk image. Nothing quits the app here — the renderer's Restart does.
	assert.Equal(t, destination, harness.opener.opened)
}

// A mismatch deletes the file rather than leaving a rejected installer one double-click away in
// Downloads, and nothing is handed over.
func TestAnArtefactThatDoesNotMatchItsDigestIsRefusedAndDeleted(t *testing.T) {
	feed := newRelease()
	harness := newHarness(t, "3.0.0", feed, goodCredentials())
	found := harness.check(t)

	feed.set(func(r *release) {
		r.digestBody[found.AssetName] = strings.Repeat("ab", sha256.Size) + "  " + found.AssetName + "\n"
	})

	_, err := harness.download(t, found)

	require.Error(t, err)
	assert.ErrorIs(t, err, update.ErrUnverified)
	assert.Contains(t, err.Error(), "deleted")

	assert.NoFileExists(t, filepath.Join(harness.downloads, found.AssetName),
		"a rejected artefact does not stay in Downloads")
	assert.Empty(t, harness.opener.opened, "nothing unverified is handed to the operating system")
}

// Refused *before* anything is downloaded: there is no point pulling ninety megabytes into
// someone's Downloads folder to then delete them, and an unverifiable release is not installed
// unverified.
func TestAReleaseThatPublishesNoDigestIsRefusedBeforeAnythingIsDownloaded(t *testing.T) {
	feed := newRelease()
	harness := newHarness(t, "3.0.0", feed, goodCredentials())
	found := harness.check(t)

	var before int
	feed.set(func(r *release) {
		r.noDigest[found.AssetName] = true
		before = len(r.requests)
	})

	_, err := harness.download(t, found)

	require.Error(t, err)
	assert.ErrorIs(t, err, update.ErrUnverified)
	assert.Contains(t, err.Error(), ".sha256")

	entries, readErr := os.ReadDir(harness.downloads)
	require.NoError(t, readErr)
	assert.Empty(t, entries, "nothing was written")

	for _, request := range harness.feed.recorded()[before:] {
		assert.NotEqual(t, "/assets/"+found.AssetName, request.path,
			"the artefact must not be fetched before its digest is known")
	}
}

func TestADigestFileThatDoesNotListTheAssetIsRefused(t *testing.T) {
	feed := newRelease()
	harness := newHarness(t, "3.0.0", feed, goodCredentials())
	found := harness.check(t)

	// Two entries, neither of them this artefact's: with more than one the name has to match, so
	// the single-entry rule does not rescue it.
	feed.set(func(r *release) {
		r.digestBody[found.AssetName] = strings.Repeat("11", sha256.Size) + "  somebody-elses.exe\n" +
			strings.Repeat("22", sha256.Size) + "  and-anothers.dmg\n"
	})

	_, err := harness.download(t, found)

	require.Error(t, err)
	assert.ErrorIs(t, err, update.ErrUnverified)

	entries, readErr := os.ReadDir(harness.downloads)
	require.NoError(t, readErr)
	assert.Empty(t, entries, "the refusal happens before the artefact is fetched")
}

// The digest travels with the same credential the artefact does. A release private to the token is
// a release whose checksum is private to it too, and an unauthenticated digest fetch would 404 on
// exactly the installs that most need verifying.
func TestTheDigestIsFetchedWithTheSameCredentialAsTheArtefact(t *testing.T) {
	harness := newHarness(t, "3.0.0", newRelease(), goodCredentials())
	found := harness.check(t)

	_, err := harness.download(t, found)
	require.NoError(t, err)

	var digestFetch, assetFetch *recordedRequest
	for _, request := range harness.feed.recorded() {
		switch request.path {
		case "/assets/" + found.AssetName + ".sha256":
			digestFetch = &request
		case "/assets/" + found.AssetName:
			assetFetch = &request
		}
	}

	require.NotNil(t, digestFetch, "the digest was never fetched")
	require.NotNil(t, assetFetch, "the artefact was never fetched")
	assert.Equal(t, "Bearer ghp_the-users-own-token", digestFetch.authorization)
	assert.Equal(t, assetFetch.authorization, digestFetch.authorization)
	assert.Equal(t, "application/octet-stream", digestFetch.accept)
}

// BOOT-021's trust boundary, stated as a test.
//
// The renderer chooses the url and the name; the sidecar chooses what the bytes have to hash to. So
// an asset url that serves *different* bytes — which is what someone who reached the renderer would
// supply — is refused against the real release's digest, and the download never becomes an
// installer.
func TestTheDigestIsReadFromTheReleaseRatherThanFromTheCaller(t *testing.T) {
	harness := newHarness(t, "3.0.0", newRelease(), goodCredentials())
	found := harness.check(t)

	impostor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("an installer nobody published"))
	}))
	t.Cleanup(impostor.Close)

	_, err := harness.invoke(t, "update_download",
		map[string]any{"assetUrl": impostor.URL + "/CodeFlow.exe", "assetName": found.AssetName})

	require.Error(t, err)
	assert.ErrorIs(t, err, update.ErrUnverified)
	assert.Empty(t, harness.opener.opened)
	assert.NoFileExists(t, filepath.Join(harness.downloads, found.AssetName))
}

// An asset name that is a path would have the updater write caller-chosen bytes outside the
// Downloads folder, with the digest check passing because a digest is about the bytes and not about
// where they went.
func TestAnAssetNameThatEscapesDownloadsIsRefused(t *testing.T) {
	harness := newHarness(t, "3.0.0", newRelease(), goodCredentials())
	found := harness.check(t)

	_, err := harness.invoke(t, "update_download", map[string]any{
		"assetUrl":  found.AssetURL,
		"assetName": filepath.Join("..", "escaped.exe"),
	})

	require.Error(t, err)
	assert.NoFileExists(t, filepath.Join(filepath.Dir(harness.downloads), "escaped.exe"))
	assert.Empty(t, harness.opener.opened)
}

// The case a name check cannot see: the name is a perfectly ordinary filename, and something is
// already sitting at that path in Downloads pointing somewhere else. The write goes through an
// `os.Root` for exactly this, so it is refused rather than following the link out.
func TestADownloadDoesNotFollowASymlinkOutOfDownloads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symlink on Windows needs a privilege this test should not assume")
	}

	harness := newHarness(t, "3.0.0", newRelease(), goodCredentials())
	found := harness.check(t)

	outside := filepath.Join(t.TempDir(), "somebody-elses-file")
	require.NoError(t, os.WriteFile(outside, []byte("not the updater's"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(harness.downloads, found.AssetName)))

	_, err := harness.download(t, found)
	require.Error(t, err)

	untouched, readErr := os.ReadFile(outside)
	require.NoError(t, readErr)
	assert.Equal(t, "not the updater's", string(untouched), "the link's target was not written through")
	assert.Empty(t, harness.opener.opened)
}

// The progress the renderer's bar is driven by. The final event is what moves the store from
// `downloading` to `ready`, so it fires even for an artefact smaller than one reporting interval —
// which the ones in this file are.
func TestTheDownloadReportsItsProgressAndAlwaysFinishes(t *testing.T) {
	harness := newHarness(t, "3.0.0", newRelease(), goodCredentials())
	found := harness.check(t)

	_, err := harness.download(t, found)
	require.NoError(t, err)

	events := harness.events.Named("update:progress")
	require.NotEmpty(t, events, "the renderer subscribes before the call and would otherwise see nothing")

	last, ok := events[len(events)-1].(update.Progress)
	require.True(t, ok)
	assert.True(t, last.Done, "the last event is the one that ends the transfer")
	assert.Equal(t, int64(len(harness.feed.artefacts[found.AssetName])), last.Downloaded)
	assert.Equal(t, last.Downloaded, last.Total, "what arrived is the total the bar counted up to")

	raw, err := jsonwire.Marshal(last)
	require.NoError(t, err)
	assert.JSONEq(t,
		fmt.Sprintf(`{"downloaded":%d,"total":%d,"done":true}`, last.Downloaded, last.Total),
		string(raw))
}

// A download of a real size reports more than once, which is the case the bar actually moves in.
//
// How many events exactly is not asserted, and cannot honestly be: the count depends on how the
// TCP stack happens to deliver the body, since the threshold is measured against bytes accumulated
// rather than against reads. What the renderer depends on is asserted instead — the running total
// never goes backwards, only the last event is `done`, and it carries everything that arrived.
func TestALargerDownloadReportsMoreThanOnce(t *testing.T) {
	feed := newRelease()
	big := make([]byte, 900*1024)
	for i := range big {
		big[i] = byte(i)
	}
	feed.artefacts = map[string][]byte{
		"CodeFlow-Setup-9.9.9-x64.exe": big,
		"CodeFlow-9.9.9-arm64.dmg":     big,
	}
	harness := newHarness(t, "3.0.0", feed, goodCredentials())
	found := harness.check(t)

	_, err := harness.download(t, found)
	require.NoError(t, err)

	events := harness.events.Named("update:progress")
	require.GreaterOrEqual(t, len(events), 2, "900 KiB at one event per 256 KiB reports before it finishes")

	var previous int64
	for i, event := range events {
		progress, ok := event.(update.Progress)
		require.True(t, ok)
		assert.GreaterOrEqual(t, progress.Downloaded, previous, "the running total never goes backwards")
		previous = progress.Downloaded
		assert.Equal(t, i == len(events)-1, progress.Done, "only the last event is done")
	}
	assert.Equal(t, int64(len(big)), previous)
}

// The artefact is verified and on disk; only the hand-off failed. The error names that, because it
// is a different situation from a failed download — the file is there and it is good.
func TestAFailedHandOffStillLeavesTheVerifiedArtefact(t *testing.T) {
	harness := newHarness(t, "3.0.0", newRelease(), goodCredentials())
	harness.opener.err = fmt.Errorf("no application is registered for this file")
	found := harness.check(t)

	_, err := harness.download(t, found)

	require.Error(t, err)
	assert.NotErrorIs(t, err, update.ErrUnverified, "the artefact verified; the shell is what refused")
	assert.Contains(t, err.Error(), found.AssetName)
	assert.FileExists(t, filepath.Join(harness.downloads, found.AssetName))
}
