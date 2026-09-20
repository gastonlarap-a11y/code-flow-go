package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/xpty"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
	"github.com/gastonlarap-a11y/code-flow/backend/storage"
)

// smokeTimeout caps the whole run. The probes talk to the filesystem and spawn a process, both of
// which can hang on a sick machine, and a smoke test that hangs is worse than one that fails: CI
// waits for it and a user watching an installer sees nothing.
const smokeTimeout = 30 * time.Second

// Probe is one thing the packaged binary must be able to do on the machine it was installed on.
type Probe struct {
	Name string
	Run  func(ctx context.Context) error
}

// RunSmokeTest executes every probe and returns the process exit code: 0 if all passed, 1 if any
// failed.
//
// It exists because the interesting failures of a desktop application are environmental, not
// logical: a native library that will not load, a git that is not on PATH, a PTY the OS refuses.
// None of them shows up in a unit test on a developer's machine, and all of them turn into "the
// app opens and does nothing" for the user. `CodeFlow --smoke-test` gives CI and a support session
// one command that answers "is this binary viable here?".
//
// Output goes to stdout in a fixed shape so a human and a workflow read the same thing.
func RunSmokeTest(probes []Probe) int {
	ctx, cancel := context.WithTimeout(context.Background(), smokeTimeout)
	defer cancel()

	// Ignored throughout: the exit code is the result, and a stdout that cannot be written to has
	// no second channel to report itself on. CI reads the code; a human reads the lines.
	report := func(format string, args ...any) { _, _ = fmt.Fprintf(os.Stdout, format, args...) }

	failed := 0
	for _, probe := range probes {
		if err := probe.Run(ctx); err != nil {
			report("FAIL  %s: %v\n", probe.Name, err)
			failed++
			continue
		}
		report("ok    %s\n", probe.Name)
	}

	if failed > 0 {
		report("smoke test: %d of %d probes failed\n", failed, len(probes))
		return 1
	}
	report("smoke test: %d %s passed\n", len(probes), plural(len(probes), "probe"))
	return 0
}

// plural keeps the report readable when a caller passes a single probe, which the tests do.
func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// DefaultProbes are the three things the packaged binary must be able to do on the machine it was
// installed on: run git, open its database, and allocate a pseudo-terminal.
//
// The same three 2.x probed — SQLite, LibGit2Sharp and a PTY — with git standing in for libgit2,
// which is the swap this port made. Each is stricter than "does the library load", on purpose:
// loading proves nothing about a locked-down laptop, a read-only volume or an OS that refuses to
// hand out a PTY, and those are the failures that turn into "the app opens and does nothing".
//
// This list is what `task smoke` runs and what the release workflow runs against each installer
// before uploading it, so a probe missing here is a class of broken build that ships.
func DefaultProbes() []Probe {
	return []Probe{
		{Name: "git", Run: probeGit},
		{Name: "storage", Run: probeStorage},
		{Name: "pty", Run: probePTY},
	}
}

// probeStorage opens a database and runs every migration against it.
//
// Opening alone would prove the driver links. Migrating proves it can actually execute the schema
// this build expects — which is the half that breaks, because `modernc.org/sqlite` is a
// transpilation of SQLite to Go and its failures are platform-shaped rather than SQL-shaped.
//
// In a temp directory, never `{base}`: a smoke test that touched the user's real database would be
// the one command guaranteed to run on a machine where something is already wrong.
func probeStorage(ctx context.Context) error {
	dir, err := os.MkdirTemp("", "codeflow-smoke-db-")
	if err != nil {
		return fmt.Errorf("create a temp directory: %w", err)
	}
	// Ignored: a temp directory left behind costs nothing and the probe's verdict is what matters.
	defer func() { _ = os.RemoveAll(dir) }()

	db, err := storage.Open(ctx, filepath.Join(dir, "codeflow.db"))
	if err != nil {
		return fmt.Errorf("open a database: %w", err)
	}
	if err := db.Close(); err != nil {
		// Closing is where the write-ahead log is checkpointed, so a failure here is a database
		// that would have been left incomplete on disk.
		return fmt.Errorf("close the database: %w", err)
	}
	return nil
}

// probePTY allocates a pseudo-terminal and gives it back.
//
// The terminal is the one feature with no fallback: if the OS refuses a PTY there is nothing to
// degrade to, and the panel simply never opens. Allocating one is also the cheapest way to find out
// that a Windows build is missing ConPTY or that a hardened macOS install denies `/dev/ptmx`.
//
// Nothing is spawned into it. Starting a shell would make the probe depend on which shell the
// machine has and on that shell's start-up files, which is a different question from "can this
// binary get a terminal at all".
func probePTY(ctx context.Context) error {
	// The context is not used by the allocation itself; it is taken so this matches every other
	// probe's signature and so a future check inside can honour the run's deadline.
	_ = ctx

	pty, err := xpty.NewPty(80, 24)
	if err != nil {
		return fmt.Errorf("allocate a pseudo-terminal: %w", err)
	}
	if err := pty.Close(); err != nil {
		return fmt.Errorf("close the pseudo-terminal: %w", err)
	}
	return nil
}

func probeGit(ctx context.Context) error {
	dir, err := os.MkdirTemp("", "codeflow-smoke-git-")
	if err != nil {
		return fmt.Errorf("create a temp directory: %w", err)
	}
	// Ignored: a temp directory left behind costs nothing and the probe's verdict is what matters.
	defer func() { _ = os.RemoveAll(dir) }()

	version, err := proc.Command(ctx, "git", "--version").Output()
	if err != nil {
		return fmt.Errorf("run git --version: %w", err)
	}
	// A `git` on PATH that answers something else is a shim or a wrapper, and finding that out
	// here beats finding it out when a diff is parsed.
	if reported := strings.TrimSpace(proc.DecodeLossyUTF8(version)); !strings.HasPrefix(reported, "git version ") {
		return fmt.Errorf("git --version answered %q", reported)
	}

	// An isolated HOME and an explicit global config keep the probe from reading — or worse,
	// writing — the user's real git configuration, and from failing on a machine whose global
	// config sets something this repository cannot satisfy.
	env := append(proc.Environment(nil),
		"HOME="+dir,
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, "gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1",
	)

	for _, step := range [][]string{
		{"init", "--quiet", "--initial-branch=main", "."},
		{"rev-parse", "--git-dir"},
	} {
		cmd := proc.Command(ctx, "git", step...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("run git %s: %w: %s", step[0], err, proc.DecodeLossyUTF8(out))
		}
	}
	return nil
}
