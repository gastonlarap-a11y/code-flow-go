package update

import (
	"context"
	"net/http"
	"runtime"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
)

// Deps is what this feature needs from the composition root.
type Deps struct {
	// Version is the running build, stamped by the linker. Empty falls back to the same `0.0.0` the
	// binary defaults to, so a development build compares like one instead of like a release.
	Version string

	// Credentials reads the GitHub token the release feed is fetched with. The same adapter the
	// providers take: the key formats belong to the credential store and this package never sees
	// them.
	Credentials Credentials

	// HTTP is the shared client. One pool for the whole process, and the same one the providers and
	// the work items use — an hourly background check has no reason to own a second.
	HTTP *http.Client

	// Emitter publishes `update:progress`. Nil in a headless test, where the download still runs
	// and simply tells nobody.
	Emitter bridge.Emitter

	// Opener hands the verified artefact to the operating system.
	Opener Opener

	// FeedURL and Downloads are the two locations this package reads and writes, as parameters.
	//
	// Both default to the real ones when empty, and both exist for the reason `platform.Paths`
	// takes a base directory: a test that could only exercise this against the project's live
	// release feed, writing into the developer's own Downloads folder, is a test nobody runs.
	FeedURL   string
	Downloads string
}

// Credentials reads the GitHub token the update feed is fetched with.
//
// One method, declared at the consumer. Any error is "no token from the store" as far as this
// package is concerned — see Service.token for why the cascade is that forgiving.
type Credentials interface {
	GitHubToken(host string) (string, error)
}

// Service holds the resolved dependencies. Built once by Register; the three handlers close over it.
type Service struct {
	version     string
	credentials Credentials
	http        *http.Client
	emitter     bridge.Emitter
	opener      Opener
	feedURL     string
	downloads   string
	// goos is the platform the artefact is chosen for. A field rather than a call to runtime.GOOS
	// at each use, so the asset rules — which differ per platform in exactly the way that is hard
	// to test on one machine — can be exercised for all of them.
	goos string

	// ghToken is the `gh auth token` half of the credential cascade, held as a field for one
	// reason: otherwise every test of the cascade would depend on whether the machine running it
	// happens to be signed in to the GitHub CLI, which is neither knowable nor stable. It is not a
	// dependency the composition root sets — there is one implementation and it is right below.
	ghToken func(context.Context) string
}

// NewService resolves the defaults. Exported so a test can drive Check and Download without a
// registry, and so the composition root has one place to look for what is optional.
func NewService(deps Deps) *Service {
	service := &Service{
		version:     deps.Version,
		credentials: deps.Credentials,
		http:        deps.HTTP,
		emitter:     deps.Emitter,
		opener:      deps.Opener,
		feedURL:     deps.FeedURL,
		downloads:   deps.Downloads,
		goos:        runtime.GOOS,
		ghToken:     ghAuthToken,
	}
	if service.version == "" {
		service.version = "0.0.0"
	}
	if service.http == nil {
		service.http = platform.NewSharedHTTPClient()
	}
	if service.feedURL == "" {
		service.feedURL = GitHubFeedURL
	}
	if service.downloads == "" {
		service.downloads = platform.DownloadsDirectory()
	}
	return service
}

// Register adds the three updater commands.
//
// Outside the database block in the composition root, deliberately: an install whose storage failed
// is exactly the install most likely to be fixed by the next version, and an updater that stops
// working when the database does would be unreachable precisely when it is wanted.
func Register(r *bridge.Registry, deps Deps) {
	service := NewService(deps)

	// The running build, as the binary was stamped with it. Read once by the renderer and cached in
	// the store.
	r.Add("update_current_version", func(_ context.Context, _ bridge.Params) (any, error) {
		return service.version, nil
	})

	// Answers rather than fails: an `Availability` carrying `available: false` and a reason is how
	// the panel says "could not check, here is why".
	r.Add("update_check", func(ctx context.Context, _ bridge.Params) (any, error) {
		return service.Check(ctx), nil
	})

	// Both parameters come from what `update_check` answered. Neither is trusted for anything but
	// where to fetch from: the digest they are checked against is read from the release.
	r.Add("update_download", func(ctx context.Context, p bridge.Params) (any, error) {
		assetURL, err := bridge.Arg[string](p, "assetUrl")
		if err != nil {
			return nil, err
		}
		assetName, err := bridge.Arg[string](p, "assetName")
		if err != nil {
			return nil, err
		}
		return service.Download(ctx, assetURL, assetName)
	})
}
