package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/gastonlarap-a11y/code-flow/backend/activity"
	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/apiclient"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/dbml"
	"github.com/gastonlarap-a11y/code-flow/backend/diagram"
	"github.com/gastonlarap-a11y/code-flow/backend/files"
	"github.com/gastonlarap-a11y/code-flow/backend/git"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/gastonlarap-a11y/code-flow/backend/providers"
	"github.com/gastonlarap-a11y/code-flow/backend/review"
	"github.com/gastonlarap-a11y/code-flow/backend/security"
	"github.com/gastonlarap-a11y/code-flow/backend/storage"
	"github.com/gastonlarap-a11y/code-flow/backend/terminal"
	"github.com/gastonlarap-a11y/code-flow/backend/tickets"
	"github.com/gastonlarap-a11y/code-flow/backend/update"
	"github.com/gastonlarap-a11y/code-flow/backend/usage"
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

	// Version is the running build, stamped into main by the linker. It reaches exactly one
	// feature — the updater, which answers it to the renderer and compares it against the feed —
	// and travels as a value rather than being read from a package variable so a test can ask what
	// a 2.7.1 or a 3.0.1 install would decide.
	Version string

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

	// AIRuns owns the AI operations in flight. Same rule as the two above: nil builds one, and
	// main passes its own because shutdown has to stop them.
	AIRuns *ai.RunRegistry
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

// gitConflicts lets the AI conflict resolver read the three sides from the index. Same seam, same
// reason: `ai` declares the one question it asks, git exposes a plain function.
type gitConflicts struct{}

func (gitConflicts) ConflictVersions(ctx context.Context, repo, relPath string) (string, string, string, error) {
	versions, err := git.ConflictVersionsFor(ctx, repo, relPath)
	if err != nil {
		return "", "", "", err
	}
	return versions.Base, versions.Ours, versions.Theirs, nil
}

// gitBranches compares two branches, for drafting a pull request description.
//
// The comparison is reshaped for a prompt before it leaves here (`GIT-031`): trimmed to what sits
// around each change, with what was excluded or omitted named. A model drafting a description from
// a flattened diff cut at a fixed length describes the first files and nothing from the rest.
type gitBranches struct{}

func (gitBranches) BranchDiff(ctx context.Context, repo, source, target string) (string, error) {
	diff, err := git.BranchDiff(ctx, repo, source, target)
	if err != nil {
		return "", err
	}
	return git.RenderTextForPrompt(diff, git.PromptBudgetChars), nil
}

// aiCheckpoints protects a working tree around an AI run that can write to it.
type aiCheckpoints struct{}

func (aiCheckpoints) CreateCheckpoint(ctx context.Context, repo, kind string) (string, error) {
	return git.CreateCheckpoint(ctx, repo, kind)
}

func (aiCheckpoints) RemoveCheckpointIfUnchanged(ctx context.Context, repo, id string) (bool, error) {
	return git.RemoveCheckpointIfUnchanged(ctx, repo, id)
}

// chatTurns adapts the activity store to the one row the AI package writes.
//
// It exists because `ai` declares the two questions it asks — record this turn, who answered the
// last one — while the store owns the table. The conversion is the seam, and it lives here because
// this is the only place that knows both.
type chatTurns struct{ store *activity.Store }

func (c chatTurns) RecordTurn(ctx context.Context, turn ai.NewTurn) (string, error) {
	stored, err := c.store.RecordTurn(ctx, activity.NewTurn{
		ProjectID:       turn.ProjectID,
		SessionID:       turn.SessionID,
		EngineSessionID: turn.EngineSessionID,
		Question:        turn.Question,
		Answer:          turn.Answer,
		Trace:           turn.Trace,
		ResponseTimeMs:  turn.ResponseTimeMs,
		IsError:         turn.IsError,
		Provider:        turn.Provider,
		Model:           turn.Model,
		EngineVersion:   turn.EngineVersion,
	})
	if err != nil {
		return "", err
	}
	return stored.CreatedAt, nil
}

func (c chatTurns) LastTurnProvider(ctx context.Context, projectID, conversationID string) (*string, error) {
	return c.store.LastTurnProvider(ctx, projectID, conversationID)
}

// projectPaths resolves a project id to the repository it names.
type projectPaths struct{ store *workspaces.Store }

func (p projectPaths) ProjectPath(ctx context.Context, projectID string) (string, error) {
	project, err := p.store.GetProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	return project.LocalPath, nil
}

/*
newUsageService builds the indicator's one service (USAGE-001).

Its two readable sources are the agent CLIs' own transcripts, which live under the user's home and
not under the app's directory — so the home is resolved here rather than taken from `platform.Paths`,
whose base is `~/CodeFlow`. A home that cannot be resolved leaves the reader with no sources, and the
indicator reports every provider as unmeasured instead of failing a start-up over a panel.

The database is passed as it is: nil when storage failed, which costs the learned ceilings and the
activity counts and nothing else.
*/
func newUsageService(deps Deps, activeProvider func(context.Context) string) *usage.Service {
	home, err := os.UserHomeDir()
	sources := []usage.Source{}
	if err == nil {
		sources = append(sources, usage.ClaudeSource(home), usage.CodexSource(home))
	}

	service := usage.Deps{
		Reader:         usage.NewReader(sources...),
		DataDir:        deps.Paths.Base(),
		ActiveProvider: activeProvider,
		// Only composition wires the subprocess probes, because nothing that runs in a test should
		// grow a child process by accident (USAGE-011, USAGE-005). Antigravity is here even though
		// it keeps no readable transcript: its CLI reports the limits it cannot log.
		Limits: map[string]usage.LimitsReader{
			"claude": usage.NewLimitsProbe(""),
			"gemini": usage.NewAntigravityProbe(""),
		},
		Plan: func(ctx context.Context) usage.Plan { return usage.ClaudePlan(ctx, "") },
	}
	if deps.DB != nil {
		service.Store = usage.NewStore(deps.DB)
		service.DB = deps.DB
	}

	return usage.NewService(service)
}

// gitRemotes answers "what does this repository point at", for the provider detection that links a
// project to its pull-request host. The provider package asks in its own terms so a remote scan is
// testable without a repository on disk.
type gitRemotes struct{}

func (gitRemotes) ListRemotes(ctx context.Context, repoPath string) ([]providers.Remote, error) {
	found, err := git.ListRemotes(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	remotes := make([]providers.Remote, 0, len(found))
	for _, remote := range found {
		remotes = append(remotes, providers.Remote{Name: remote.Name, URL: remote.URL})
	}
	return remotes, nil
}

// gitFetcher brings refs in before a review reads them.
//
// The review pipeline asks in its own terms — fetch this repository, fetch these refspecs — and
// every call through it is best-effort at the call site: being offline makes a review weaker, not
// impossible.
type gitFetcher struct{ network git.Network }

func (f gitFetcher) Fetch(ctx context.Context, repo string) error {
	return f.network.Fetch(ctx, repo, nil)
}

func (f gitFetcher) FetchRefspecs(ctx context.Context, repo, remote string, refspecs []string) error {
	return f.network.FetchRefspecs(ctx, repo, remote, refspecs)
}

// providerCredentials answers whether a host or organisation is already connected, and hands over
// its token when it is.
//
// The key formats stay in the credential store, which owns them as `VERBATIM` contracts — this
// adapter is the only place that knows both those formats and the questions the providers ask. It
// also translates the store's two actionable failures into the provider package's own, so the
// decision about the `CREDENTIAL_REFUSED: ` sentinel stays at the command boundary (`XLANG-012`)
// rather than travelling with every read.
type providerCredentials struct{ store *security.Store }

func (c providerCredentials) HasGitHubToken(host string) (bool, error) {
	return c.store.Has(security.GitHubTokenKey(host))
}

func (c providerCredentials) HasADOPAT(org string) (bool, error) {
	return c.store.Has(security.ADOPATKey(org))
}

func (c providerCredentials) GitHubToken(host string) (string, error) {
	return c.read(security.GitHubTokenKey(host))
}

func (c providerCredentials) ADOPAT(org string) (string, error) {
	return c.read(security.ADOPATKey(org))
}

func (c providerCredentials) read(key string) (string, error) {
	secret, err := c.store.Get(key)
	switch {
	case errors.Is(err, security.ErrNoEntry):
		return "", providers.ErrNoCredential
	case errors.Is(err, security.ErrRefused):
		// Both errors are wrapped: the platform's own words are kept after the marker — "User
		// interaction is not allowed" says something a generic sentence does not — and a caller
		// that knows the credential store can still match its error too.
		return "", fmt.Errorf("%w: %w", providers.ErrCredentialRefused, err)
	case err != nil:
		return "", err
	}
	return secret, nil
}

// dbPasswords is the schema designer's half of the same seam (DBML-024).
//
// Separate from `providerCredentials` because the questions are different — a provider asks "is this
// host connected", a database asks for one secret by connection id — and because this one is keyed
// by id rather than by host: renaming a server must not strand its password, and two logins to the
// same server are two secrets.
type dbPasswords struct{ store *security.Store }

func (c dbPasswords) SetDBPassword(connectionID, password string) error {
	return c.store.Set(security.DBPasswordKey(connectionID), password)
}

func (c dbPasswords) DBPassword(connectionID string) (string, error) {
	secret, err := c.store.Get(security.DBPasswordKey(connectionID))
	if errors.Is(err, security.ErrNoEntry) {
		// No stored password is an ordinary state — a database that takes none, or a connection
		// saved before one was set — and not a failure to read.
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return secret, nil
}

func (c dbPasswords) DeleteDBPassword(connectionID string) error {
	return c.store.Delete(security.DBPasswordKey(connectionID))
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
// BuildRegistry wires every feature and answers the registry plus a failure observer.
//
// The observer is the usage indicator's hook: every command's error passes through the bridge's
// recorder, so that is where a quota refusal is noticed, and composition is what connects the two
// without either package learning about the other (USAGE-006). A caller with no use for it — the
// contract test — discards it.
func BuildRegistry(deps Deps) (*bridge.Registry, func(method string, err error)) {
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

	// The diagram editor walks a folder and nothing else — no database, no credential, no emitter —
	// so it registers here with the rest of what survives a failed storage stage, and takes no deps
	// at all (DIAG-002).
	diagram.Register(registry)

	// The usage indicator is registered further down, once the AI router exists: it needs to ask
	// which engine is active in order to attribute a quota failure to the provider that hit it
	// (USAGE-006).

	// The AI routing and the Settings queries need no database: a nil settings reader resolves to
	// the built-in defaults, so an install whose storage failed can still be configured. The run
	// lifecycle and the operations that use it arrive with the rest of Phase 4.
	var aiSettings ai.SettingsReader
	if workspaceStore != nil {
		aiSettings = workspaceStore
	}
	runs := deps.AIRuns
	if runs == nil {
		runs = ai.NewRunRegistry(deps.Emitter, 0)
	}
	var (
		aiProjects ai.ProjectPaths
		aiTurns    ai.TurnRecorder
	)
	if workspaceStore != nil {
		aiProjects = projectPaths{store: workspaceStore}
	}
	if deps.DB != nil {
		// Chat history is the one AI dependency that genuinely needs the database. Without it a
		// turn still answers and simply is not remembered, which beats refusing to answer.
		aiTurns = chatTurns{store: activity.NewStore(deps.DB, clock)}
	}

	// One client for the whole process — the AI HTTP engines, both providers, the work items and
	// the updater. Built here rather than per feature: it is where the connection pool, the proxy
	// setting and the one-retry-for-a-bodyless-request rule (PROV-049) live.
	httpClient := platform.NewSharedHTTPClient()
	aiRouter := ai.NewRouter(aiSettings)
	ai.Register(registry, ai.Deps{
		Router:      aiRouter,
		Catalogue:   ai.NewCatalogue(httpClient, credentials),
		Runs:        runs,
		Operations:  ai.NewOperations(aiRouter, runs, httpClient, credentials),
		Conflicts:   gitConflicts{},
		Projects:    aiProjects,
		Turns:       aiTurns,
		Checkpoints: aiCheckpoints{},
		Branches:    gitBranches{},
	})

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
		activityStore := activity.NewStore(deps.DB, clock)
		activity.Register(registry, activity.Deps{Store: activityStore})

		// The review *store* only reads and edits saved runs, so it lands with storage. The
		// pipeline that produces them — running a review, reconciling, posting — is Phase 5 and
		// will add its own Register.
		review.RegisterStore(registry, review.Deps{Store: review.NewStore(deps.DB, clock)})

		// The providers read and write the project row and file a decision in the project's
		// history, so they need the database.
		providerDeps := providers.Deps{
			Projects:    workspaceStore,
			Remotes:     gitRemotes{},
			Credentials: providerCredentials{store: credentials},
			Activity:    activityStore,
			HTTP:        httpClient,
		}
		providers.Register(registry, providerDeps)

		// The review pipeline. Publishing goes through the same host resolution the provider
		// commands dispatch through, so a review cannot post to a host the sidebar would not have
		// listed from; and the run reads its methodology, contexts and MCP servers from the same
		// workspace store Settings writes them to.
		pipeline := review.PipelineDeps{
			Store:    review.NewStore(deps.DB, clock),
			Hosts:    providerDeps,
			Projects: workspaceStore,
			AI:       ai.NewOperations(aiRouter, runs, httpClient, credentials),
			Fetcher:  gitFetcher{network: git.NewNetwork(deps.Emitter)},
			Activity: activityStore,
			Paths:    deps.Paths,
		}
		review.RegisterPublishing(registry, pipeline)
		review.RegisterPipeline(registry, pipeline)

		// The work items. They share the HTTP client and the credential store with the providers —
		// a board and a repository live on the same host and under the same PAT — and the same AI
		// operations as the pull-request review, because `review_changes` is a dispatcher over two
		// orchestrations rather than a third one.
		tickets.Register(registry, tickets.Deps{
			Store:       tickets.NewStore(deps.DB, clock),
			Workspaces:  workspaceStore,
			Prompts:     workspaceStore,
			Credentials: providerCredentials{store: credentials},
			AI:          ai.NewOperations(aiRouter, runs, httpClient, credentials),
			Activity:    activityStore,
			Paths:       deps.Paths,
			HTTP:        httpClient,
		})

		// The API workbench's own data: collections, environments, history and the cookie jar.
		apiclient.RegisterStore(registry, apiclient.Deps{
			Store: apiclient.NewStore(deps.DB, clock),
		})

		// The schema designer keeps two things: where a person dragged each table, and which
		// databases they saved. The password for one of those never appears in either — it goes to
		// the credential store, and the type that crosses the bridge has no field for it.
		dbml.Register(registry, dbml.Deps{
			Store:       dbml.NewStore(deps.DB, clock),
			Credentials: dbPasswords{store: credentials},
			AI:          ai.NewOperations(aiRouter, runs, httpClient, credentials),
		})
	}

	// The workbench's transports, registered outside the database block on purpose: they need no
	// storage, so an install whose database did not open can still send one request by hand —
	// which is when somebody is most likely to want to.
	apiclient.RegisterHTTP(registry, apiclient.HTTPDeps{Cancels: apiclient.NewCancels()})
	apiclient.RegisterStreams(registry, apiclient.StreamDeps{
		Streams: apiclient.NewStreams(deps.Emitter),
	})

	// The updater, outside the database block for the same reason the transports are: an install
	// whose storage failed is the one most likely to be fixed by the next version, and an updater
	// that needed the database would be unreachable exactly when it is wanted. It takes the same
	// credential adapter the providers do — the feed is read with the user's own GitHub token.
	update.Register(registry, update.Deps{
		Version:     deps.Version,
		Credentials: providerCredentials{store: credentials},
		HTTP:        httpClient,
		Emitter:     deps.Emitter,
		Opener:      deps.Opener,
	})

	// Last, because it asks the router which engine is active (USAGE-006). Everything else about it
	// survives a failed storage stage: without a database it loses the learned ceilings and the
	// activity counts and still reports consumption, reset times and resources.
	usageService := newUsageService(deps, aiRouter.ActiveProvider)
	usage.Register(registry, usageService)

	registry.Seal()
	return registry, usageService.NoteFailure
}
