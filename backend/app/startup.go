// Package app is the start-up sequence and the state it leaves behind.
//
// It is the one place that knows the order the stages run in, and it owns the single most visible
// change this port makes to the application's behaviour: a failed stage no longer stops the
// process.
//
// In 2.x the core recorded the failure and rethrew, the process died, and the shell reported "the
// core is down" — an app whose window was up and whose every button did nothing (BOOT-032). Now
// the window opens either way, State carries what went wrong, and the renderer's existing
// SidecarBanner shows it. Same information, delivered where the user is looking.
package app

import (
	"errors"
	"fmt"
	"os"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
)

// Status is what the renderer's sidecarStore branches on. The three values are its own union type
// ("starting" | "ready" | "down"); "starting" belongs to the renderer before it has asked, so Go
// only ever produces the other two.
type Status string

const (
	StatusReady Status = "ready"
	StatusDown  Status = "down"
)

// State is the answer to host.sidecarStatus().
//
// camelCase, not snake_case: this crosses through HostService, whose 2.x counterpart was the Ipc
// envelope's camelCase context (§3.4). The renderer reads `current.logsDirectory` literally.
//
// Detail is omitted when empty so it arrives as `undefined`, matching the renderer's optional
// `detail?: string` — an empty string would be kept by its `?? null` and render a blank reason.
type State struct {
	Status        Status `json:"status"`
	Detail        string `json:"detail,omitempty"`
	LogsDirectory string `json:"logsDirectory"`

	// FailedStage is the stage that stopped the sequence, for the logs and for tests. Not sent to
	// the renderer, which has no use for the distinction.
	FailedStage string `json:"-"`
}

// Failed reports whether start-up stopped early.
func (s State) Failed() bool { return s.Status == StatusDown }

// StartupLogger is the slice of diagnostics.StartupLog this package needs, declared at the
// consumer so app does not depend on the concrete logger.
type StartupLogger interface {
	Record(stage string, failure error)
}

// stage is one named step. The name is part of the contract with startup.log: support reads those
// lines, and the four spellings are the ones 2.x wrote.
type stage struct {
	name string
	run  func() error
}

// RunStages executes the start-up sequence and returns the state the window will report.
//
// The order is load-bearing:
//
//  1. reset-marker — before anything opens a file under {base}, because the wipe deletes the
//     directory a live SQLite connection would be holding.
//  2. directories  — exactly {base}, {base}/logs and {base}/repos, in that order (BOOT-005).
//  3. scratch-sweep — orphaned AI payloads older than an hour.
//  4. storage      — the database and its migrations.
//
// A stage that fails stops the sequence: the later stages assume the earlier ones ran, and running
// them anyway would replace one clear failure with a cascade of confusing ones.
//
// openStorage is a parameter because storage is Phase 2. Passing nil makes it a no-op, which is
// what lets the whole desktop shell be built and exercised before the database exists.
func RunStages(paths platform.Paths, log StartupLogger, openStorage func() error) State {
	state := State{Status: StatusReady, LogsDirectory: paths.Logs()}

	stages := []stage{
		{"reset-marker", func() error { return applyResetMarker(paths) }},
		{"directories", paths.EnsureStartupDirectories},
		{"scratch-sweep", func() error { ai.SweepOrphans(os.TempDir()); return nil }},
		{"storage", func() error {
			if openStorage == nil {
				return nil
			}
			return openStorage()
		}},
	}

	for _, s := range stages {
		err := s.run()
		if err == nil {
			continue
		}
		if log != nil {
			log.Record(s.name, err)
		}
		return State{
			Status:        StatusDown,
			Detail:        fmt.Sprintf("start-up stage %q failed: %s", s.name, err),
			LogsDirectory: paths.Logs(),
			FailedStage:   s.name,
		}
	}
	return state
}

// applyResetMarker wipes {base} when the previous session asked for it (BOOT-001/006/017).
//
// The result is deliberately ignored in 2.x and here: reset_app_data has already told the user it
// worked, and a wipe that half-succeeded must not leave the app unable to start. Whatever survives
// is picked up by the next stage, which recreates the three directories.
//
// The keychain is never touched. Resetting the application's data is not the same as revoking the
// user's tokens, and the 2.x behaviour — credentials survive a reset — is what people rely on.
func applyResetMarker(paths platform.Paths) error {
	if _, err := os.Stat(paths.ResetMarker()); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		// Anything other than "not there" is worth not guessing about: leaving the data alone is
		// always the safe reading of an unreadable marker.
		return nil
	}

	// Ignored by design, as above.
	_ = os.RemoveAll(paths.Base())
	return nil
}
