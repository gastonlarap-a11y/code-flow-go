// CodeFlow — a code review and API workbench.
//
// One Go binary hosting a Wails v3 window, replacing the Electron shell and the .NET sidecar of
// CodeFlow 2.x. This file is composition only: it builds the pieces in dependency order, hands
// them to each other, and starts the application. Anything with a decision in it belongs in a
// package, where it can be tested without a window.
package main

import (
	"context"
	"embed"
	"os"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/app"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/desktop"
	"github.com/gastonlarap-a11y/code-flow/backend/diagnostics"
	"github.com/gastonlarap-a11y/code-flow/backend/files"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/gastonlarap-a11y/code-flow/backend/security"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
	"github.com/gastonlarap-a11y/code-flow/backend/storage"
	"github.com/gastonlarap-a11y/code-flow/backend/terminal"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// assets is the built renderer. `all:` so Vite's hashed asset directories — whose names begin with
// an underscore in some plugins — are not silently skipped by go:embed's default exclusion rules.
//
// frontend/dist is gitignored except for a placeholder index.html, which exists so this line
// compiles on a clean clone: without the directory, `go test ./...` fails before any test runs.
//
//go:embed all:frontend/dist
var assets embed.FS

// version is set by the build: -ldflags "-X main.version=3.0.0". The default matches what the 2.x
// core answered when it was started without --app-version, so the updater's "am I current?" logic
// sees the same shape in a development build as it did before.
var version = "0.0.0"

func main() {
	// Before anything else: a smoke test must not open a window, create directories or touch the
	// user's data. It answers "is this binary viable on this machine?" and exits.
	if len(os.Args) > 1 && os.Args[1] == "--smoke-test" {
		os.Exit(app.RunSmokeTest(app.DefaultProbes()))
	}

	paths := platform.DefaultPaths()
	shellLog := diagnostics.NewShellLog(paths.Logs())
	errorLog := diagnostics.NewErrorLog(paths.Logs())
	startupLog := diagnostics.NewStartupLog(paths.Logs())

	// Every recovered goroutine panic goes to the same two files a command failure does, so one
	// log tells the whole story (§3.8, BOOT-035).
	safego.SetHandler(func(r safego.Recovered) {
		shellLog.Error("[shell] " + r.String())
		errorLog.Record("goroutine/"+r.Name, recoveredError{r})
	})

	// First line of every session. A support log that does not say which build wrote it is a log
	// whose every other line has to be guessed at, and this one costs nothing.
	shellLog.Info("[shell] CodeFlow " + version + " starting")

	// macOS only, and before anything is spawned: a GUI app inherits launchd's PATH, not the
	// user's, so git and every AI CLI would be missing.
	platform.ApplyLoginShellPath(shellLog)

	// The storage stage opens the database and runs every migration. A failure here is reported
	// rather than fatal, unlike 2.x: the window still opens and says what went wrong, which is a
	// great deal more useful than a process that exits before anyone sees why (BOOT-032).
	var db *storage.DB
	state := app.RunStages(paths, startupLog, func() error {
		opened, err := storage.Open(context.Background(), paths.Database())
		if err != nil {
			return err
		}
		db = opened
		return nil
	})
	if state.Failed() {
		shellLog.Error("[shell] " + state.Detail)
	}

	emitter := desktop.NewEmitter()
	opener := desktop.NewOpener()
	watchers := files.NewWatcherRegistry(emitter)
	terminals := terminal.NewRegistry(emitter)
	aiRuns := ai.NewRunRegistry(emitter, 0)

	// The feature wiring lives in app.BuildRegistry so the contract test can inspect the real
	// registry rather than a second copy of this list.
	registry, noteFailure := app.BuildRegistry(app.Deps{
		Paths:       paths,
		Version:     version,
		Emitter:     emitter,
		DB:          db,
		Credentials: security.NewStore(),
		Opener:      opener,
		Watchers:    watchers,
		Terminals:   terminals,
		AIRuns:      aiRuns,
	})

	host := desktop.NewHostService(state, paths)
	shell := desktop.New(desktop.Options{ShellLog: shellLog, Paths: paths, State: state, Host: host})

	wailsApp := application.New(application.Options{
		Name:        "CodeFlow",
		Description: "Code review and API workbench",
		Services: []application.Service{
			// Two observers on one failure: the error log records it, and the usage indicator
			// notices the ones that mean "out of quota" so it can learn that provider's ceiling
			// (USAGE-006). Composition is what joins them; neither package knows the other.
			application.NewService(bridge.NewService(registry, func(method string, err error) {
				errorLog.Record(method, err)
				noteFailure(method, err)
			})),
			application.NewService(host),
		},
		Assets: application.AssetOptions{Handler: application.AssetFileServerFS(assets)},

		// Closing the last window hides it; only a named quit exits (BOOT-007).
		Mac:     application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		Windows: application.WindowsOptions{DisableQuitOnLastWindowClosed: true},

		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               "com.codeflow.app",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { shell.ShowMain() },
		},

		// Wails' own handler would quit without naming anything; BOOT-036 requires every exit to
		// say who asked.
		DisableDefaultSignalHandler: true,
		ShouldQuit:                  shell.LogExternalQuit,

		// Closing the database checkpoints the write-ahead log. Without it the .db file on disk
		// can be missing entire tables — measured on a real 2.7.1 install, whose bare .db did not
		// contain `workspaces` at all — which is how a backup taken behind the app's back turns
		// out to be almost empty.
		OnShutdown: func() {
			// Before the database, because a watcher firing during shutdown would have the
			// renderer ask for a status read against a closing connection. The terminals go with
			// them: a shell left running is a process the user cannot see and did not keep.
			watchers.StopAll()
			terminals.CloseAll()
			// An AI run left alive keeps a CLI — and its model call — running and billing after
			// the window is gone.
			aiRuns.CancelAll()

			if db != nil {
				if err := db.Close(); err != nil {
					shellLog.Error("[shell] closing the database: " + err.Error())
				}
			}
			shellLog.Info("[shell] shutdown complete")
		},
	})

	emitter.Bind(wailsApp)
	opener.Bind(wailsApp)
	shell.Install(wailsApp)

	if err := wailsApp.Run(); err != nil {
		shellLog.Error("[shell] run failed: " + err.Error())
		os.Exit(1)
	}
}

// recoveredError adapts a recovered panic to the error the log expects. It is here rather than in
// safego because safego must not depend on how a particular application reports.
type recoveredError struct{ r safego.Recovered }

func (e recoveredError) Error() string { return e.r.String() }
