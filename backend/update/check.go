package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

// Checking the feed (§2.9, BOOT-021).

// GitHubFeedURL is the release the updater reads. A literal, because it is this application's own
// release feed and not something an install can be pointed at: a configurable update source is a
// configurable place to be handed a binary from.
//
// **It is `code-flow-go`, and 2.7.x's updater reads `code-flow`. The two are not the same feed and
// that is deliberate.** The port's releases are published here (§14 D2 chose it: a 3.x release on
// `code-flow` becomes `latest` there and offers itself to every 2.7.x install, which is the cutover
// and has not been made). An app that reads a feed its own releases are not published to is an app
// that reports "up to date" forever — which is what 3.0.0 and 3.1.0 did: they asked `code-flow`,
// were told `v2.7.1`, found it older than themselves, and said nothing. Correct arithmetic on the
// wrong shelf.
//
// **At the cutover this constant moves back to `code-flow`** and both feeds hold the same releases
// from then on. It is pinned by a test so the move is a deliberate edit rather than a drift.
// `scripts/install-macos.sh` holds the same repository for the same reason and moves with it.
const GitHubFeedURL = "https://api.github.com/repos/gastonlarap-a11y/code-flow-go/releases/latest"

const (
	// ghTokenTimeout bounds the `gh auth token` fallback. It is a local process reading a local
	// config file; five seconds is already generous, and the reason there is a bound at all is that
	// an update check runs hourly in the background and must never be the thing that hangs.
	ghTokenTimeout = 5 * time.Second

	// feedTimeout bounds one check. The shared client's own timeout is five minutes, sized for a
	// review posting forty threads against a slow enterprise host; an hourly background check that
	// waits five minutes for a dead network is a different thing entirely.
	feedTimeout = 30 * time.Second

	// maxFeedBytes caps the release payload. A release body is release notes; anything past a
	// megabyte is not one, and decoding it into memory unbounded because the far side said so is
	// the kind of thing that only shows up once it is being exploited.
	maxFeedBytes = 4 << 20

	// maxDigestBytes caps a checksum file. A `.sha256` is one line.
	maxDigestBytes = 64 << 10
)

// The five reasons a check can fail to reach an answer. `VERBATIM`: the renderer maps each to a
// translation key, so a reworded one becomes a missing sentence rather than a compile error
// (`lib/bridge/updater.ts`, `UpdateUnavailableReason`).
//
// gosec G101 reads `reasonNoCredential = "no-credential"` as a hardcoded credential, on the name
// alone. It is the opposite: these five are what the panel says when there is no credential, and
// renaming them to please the pattern would put the rule's own vocabulary out of step with the
// renderer's.
//
//nolint:gosec // G101 false positive: reason codes shown to the user, not secrets.
const (
	reasonNoCredential = "no-credential"
	reasonUnauthorized = "unauthorized"
	reasonNoRelease    = "no-release"
	reasonNoAsset      = "no-asset"
	reasonUnreachable  = "unreachable"
)

// Availability is what `update_check` answers.
//
// Every field is a value rather than a pointer, and none is `omitempty`: the renderer reads
// `found.reason`, `found.notes` and `found.date` for truthiness and substitutes its own fallback,
// so an absent field and an empty one mean the same thing to it — while a missing key would make
// `found.install_kind` undefined and `updateStore` render "undefined" into the modal.
type Availability struct {
	Available      bool   `json:"available"`
	CurrentVersion string `json:"current_version"`
	Version        string `json:"version"`
	Notes          string `json:"notes"`
	Date           string `json:"date"`
	AssetName      string `json:"asset_name"`
	AssetURL       string `json:"asset_url"`
	AssetSize      int64  `json:"asset_size"`
	InstallKind    string `json:"install_kind"`
	Reason         string `json:"reason"`
}

// errNoCredential ends the token cascade. It is internal: it never reaches the renderer as an
// error, only as the `no-credential` reason on an otherwise successful answer, because "we could
// not check" is a state of the update panel rather than a failed command.
var errNoCredential = errors.New("no GitHub credential is available")

// Check asks the feed whether a newer release exists.
//
// It answers rather than fails. An `Availability` with `available: false` and a `reason` is how the
// panel says "could not check, here is why" — the alternative, a rejected promise, is what 1.7.2
// did and it is indistinguishable from "you are up to date" once the store has swallowed it.
//
// The one thing this must never do is report "up to date" for a request that never reached GitHub.
// That is the failure the whole shape exists to prevent, and it is why every path below either
// compares two real versions or carries a reason.
func (s *Service) Check(ctx context.Context) Availability {
	answer := Availability{
		CurrentVersion: s.version,
		// Set whatever the answer turns out to be: it describes what this platform does with an
		// installer, which is a property of the machine and not of the release.
		InstallKind: installKindFor(s.goos),
	}

	ctx, cancel := context.WithTimeout(ctx, feedTimeout)
	defer cancel()

	token, err := s.token(ctx)
	if err != nil {
		answer.Reason = reasonNoCredential
		return answer
	}

	release, reason := s.latestRelease(ctx, token)
	if reason != "" {
		answer.Reason = reason
		return answer
	}

	if !IsNewer(release.TagName, s.version) {
		// Current. No reason, which is what tells the renderer to resolve `null` rather than throw.
		return answer
	}

	asset := assetFor(release, s.goos)
	if asset == nil {
		answer.Reason = reasonNoAsset
		return answer
	}

	answer.Available = true
	answer.Version = normalizeVersion(release.TagName)
	answer.Notes = release.Body
	answer.Date = release.PublishedAt
	answer.AssetName = asset.Name
	answer.AssetURL = asset.URL
	answer.AssetSize = asset.Size
	return answer
}

// latestRelease fetches and decodes the feed, or names why it could not.
//
// A draft is `no-release` rather than an error: a token that can see the repository can see drafts,
// and a release still being written is not one anybody should be offered.
func (s *Service) latestRelease(ctx context.Context, token string) (Release, string) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.feedURL, nil)
	if err != nil {
		return Release{}, reasonUnreachable
	}
	s.applyGitHubHeaders(request, token, "application/vnd.github+json")

	response, err := s.http.Do(request)
	if err != nil {
		return Release{}, reasonUnreachable
	}
	defer func() { _ = response.Body.Close() }()

	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		// The credential exists and was not accepted, which is a different next step from having
		// none: reconnect the account rather than connect one.
		return Release{}, reasonUnauthorized
	case response.StatusCode < 200 || response.StatusCode > 299:
		return Release{}, reasonNoRelease
	}

	var release Release
	if err := json.NewDecoder(io.LimitReader(response.Body, maxFeedBytes)).Decode(&release); err != nil {
		// A 2xx that is not a release is the same situation as no release: there is nothing here to
		// offer, and the body is GitHub's to explain, not this panel's.
		return Release{}, reasonNoRelease
	}
	if release.Draft {
		return Release{}, reasonNoRelease
	}
	return release, ""
}

// applyGitHubHeaders sets the four headers every call to the feed carries.
//
// The `User-Agent` is required — GitHub answers 403 to a request without one — and carries the
// running version, which makes the release's traffic tell which builds are still checking in. The
// API version is pinned to a dated snapshot rather than tracking "latest", so a change GitHub makes
// to its default cannot change what an installed 3.0.0 sees.
func (s *Service) applyGitHubHeaders(request *http.Request, token, accept string) {
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", accept)
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("User-Agent", "CodeFlow/"+s.version)
}

// githubAPIVersion pins the REST behaviour to a dated snapshot.
const githubAPIVersion = "2022-11-28"

// token resolves the credential the feed is read with: the keychain first, then `gh auth token`.
//
// A token is required although the repository is public, which is 2.x's behaviour and is preserved
// rather than improved: dropping the requirement would change who can check for updates, and that
// is a product decision with its own consequences (rate limits are per-token, and an anonymous
// check shares one bucket with everybody else on the same egress address).
//
// The cascade falls through on *any* keychain outcome, not only on "not stored". A locked keychain
// therefore reaches `gh auth token` and, if that also has nothing, reports `no-credential` — which
// names the wrong cause. That is the trade the shape makes: an update check runs hourly in the
// background, and one that raises a keychain prompt or an error toast every hour because a secret
// it can live without was unavailable is worse than one that quietly says it could not check.
func (s *Service) token(ctx context.Context) (string, error) {
	if s.credentials != nil {
		if secret, err := s.credentials.GitHubToken("github.com"); err == nil && secret != "" {
			return secret, nil
		}
	}

	if s.ghToken != nil {
		if secret := s.ghToken(ctx); secret != "" {
			return secret, nil
		}
	}
	return "", errNoCredential
}

// ghAuthToken asks the GitHub CLI for the token it is signed in with.
//
// Best effort throughout: `gh` may not be installed, may not be signed in, or may be signed in to
// an enterprise host. Every one of those is an empty answer rather than an error, because the only
// caller's next step is the same in all of them.
//
// Spawned through `shared/proc`, like every child process: its own process group, and an
// environment that cannot carry a credential into it (SEC-007).
func ghAuthToken(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, ghTokenTimeout)
	defer cancel()

	cmd := proc.Command(ctx, "gh", "auth", "token")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return firstLine(string(out))
}

// firstLine is what a token read from a process's output is reduced to.
//
// `gh` prints the token and nothing else today. A future version printing a notice after it must
// not turn the notice into part of the token — and an `Authorization` header with a newline in it
// is rejected by Go's transport outright, so the failure would be an unexplained `unreachable`
// rather than an obvious one.
func firstLine(out string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(line)
}

// fetchText reads a small resource from the release with the same credential the feed was read
// with, capped so a far side that lies about its size cannot be believed.
func (s *Service) fetchText(ctx context.Context, url, token string, limit int64) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build the request: %w", err)
	}
	s.applyGitHubHeaders(request, token, "application/octet-stream")

	response, err := s.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", url, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", fmt.Errorf("fetching %s returned %d", url, response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, limit))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", url, err)
	}
	return string(body), nil
}
