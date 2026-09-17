package app

import (
	"context"

	"github.com/gastonlarap-a11y/code-flow/backend/activity"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/files"
	"github.com/gastonlarap-a11y/code-flow/backend/git"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/gastonlarap-a11y/code-flow/backend/review"
	"github.com/gastonlarap-a11y/code-flow/backend/security"
	"github.com/gastonlarap-a11y/code-flow/backend/storage"
	"github.com/gastonlarap-a11y/code-flow/backend/terminal"
	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
)

// Deps is everything the feature packages need from the composition root.
//
// It grows one field per phase — the database, the shared HTTP client, the credential store, the
// registries that own an application-lifetime context — and each feature takes only the slice of
// it that it declares in its own Deps struct. main builds this once; nothing else constructs one
// except the contract test.
type Deps struct {
	Paths   platform.Paths
	Emitter bridge.Emitter

	// DB is nil until the storage stage has run, and stays nil when it failed. Features that need
	// it take it from here; a nil one means the window opens and those commands report the
	// start-up failure rather than panicking (BOOT-032).
	DB *storage.DB

	// Credentials is always present: the OS credential store needs no start-up stage, and it must
	// keep working even when the database did not open — "reconnect your account" is one of the
	// few useful things a broken install can still offer.
	Credentials *security.Store

	// Opener hands a path to the operating system. Nil in a headless test, where "open this in
	// Finder" has nowhere to go.
	Opener files.Opener

	// Watchers owns the filesystem watches and Terminals the running shells. Both may be nil, and
	// then BuildRegistry makes its own: constructing either starts nothing, so the commands are
	// always registered and answer. What main passes its own for is shutdown — it is the only
	// caller that has to stop them again.
	Watchers  *files.WatcherRegistry
	Terminals *terminal.Registry
}

// gitCheckpointer lets a repo-wide replace take the same snapshot an AI run does.
//
// An adapter rather than a shared interface in the git package: `files` declares the one method it
// needs (that is this repository's rule for interfaces), and git exposes a plain function. This is
// the seam between them, and it lives in the composition root because that is the only place that
// knows both.
type gitCheckpointer struct{}

func (gitCheckpointer) CreateCheckpoint(ctx context.Context, repo, kind string) (string, error) {
	return git.CreateCheckpoint(ctx, repo, kind)
}

// BuildRegistry registers every command and seals the registry.
//
// It lives here rather than inline in main for one reason: the command-coverage contract test
// (§9.4 #1) has to inspect the *real* registry, not a copy of the wiring that can drift from it.
// A test that builds its own list would pass on the day someone forgot to add a line to main.
//
// The order matches the 2.x composition root. It has no functional meaning — the registry is a map
// and panics on a duplicate either way — but keeping it makes the two files diffable while the
// port is in progress.
func BuildRegistry(deps Deps) *bridge.Registry {
	registry := bridge.NewRegistry()

	platform.Register(registry, platform.Deps{Paths: deps.Paths})

	credentials := deps.Credentials
	if credentials == nil {
		credentials = security.NewStore()
	}
	security.Register(registry, security.Deps{Store: credentials})

	// The workspace store is built before git because git asks it one question — who commits in
	// this directory (WS-008) — and nil when the database did not open, which git handles by
	// falling back to the repository's own config.
	clock := storage.SystemClock{}
	var workspaceStore *workspaces.Store
	if deps.DB != nil {
		workspaceStore = workspaces.NewStore(deps.DB, clock)
	}

	// Git otherwise needs no database: it reads and writes the user's repositories directly, so it
	// keeps working when the storage stage failed — which is also when someone is most likely to be
	// trying to get their work out.
	//
	// The nil check is not redundant with the one inside resolveAuthor: a nil *workspaces.Store in
	// a non-nil interface is a typed nil, and calling through it would panic instead of falling
	// back. The interface is left genuinely nil instead.
	gitDeps := git.Deps{Emitter: deps.Emitter}
	if workspaceStore != nil {
		gitDeps.Identity = workspaceStore
	}
	git.Register(registry, gitDeps)

	// Files needs no database either, for the same reason git does not: it reads and writes the
	// user's own tree.
	files.Register(registry, files.Deps{Opener: deps.Opener, Checkpointer: gitCheckpointer{}})
	watchers := deps.Watchers
	if watchers == nil {
		watchers = files.NewWatcherRegistry(deps.Emitter)
	}
	files.RegisterWatcher(registry, watchers)

	terminals := deps.Terminals
	if terminals == nil {
		terminals = terminal.NewRegistry(deps.Emitter)
	}
	terminal.Register(registry, terminals)

	// Everything below needs the database. When the storage stage failed it is nil, and these
	// commands are simply not registered: the window still opens, the renderer's banner says why,
	// and a call to one of them answers `unknown command` rather than dereferencing nil. That is a
	// worse message than "storage is down", so the alternative — registering handlers that all
	// return the start-up error — is the better shape once there is a second phase to justify it.
	if deps.DB != nil {
		workspaces.Register(registry, workspaces.Deps{Store: workspaceStore, Paths: deps.Paths})
		workspaces.RegisterSkills(registry, workspaces.SkillDeps{
			Store:   workspaceStore,
			Paths:   deps.Paths,
			Emitter: deps.Emitter,
		})
		activity.Register(registry, activity.Deps{Store: activity.NewStore(deps.DB, clock)})

		// The review *store* only reads and edits saved runs, so it lands with storage. The
		// pipeline that produces them — running a review, reconciling, posting — is Phase 5 and
		// will add its own Register.
		review.RegisterStore(registry, review.Deps{Store: review.NewStore(deps.DB, clock)})
	}

	// Phases 3–8 add their remaining lines here:
	//   git.Register / files.Register / files.RegisterWatcher / terminal.Register
	//   ai.Register / providers.Register / tickets.Register / review.Register
	//   apiclient.Register / dbml.Register / update.Register

	registry.Seal()
	return registry
}
