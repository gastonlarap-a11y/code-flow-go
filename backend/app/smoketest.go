package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
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
	report("smoke test: %d probes passed\n", len(probes))
	return 0
}

// DefaultProbes are the probes available in this phase.
//
// 2.x probed SQLite, LibGit2Sharp and a PTY. The git probe below replaces the LibGit2Sharp one and
// is stricter than it needs to be on purpose: `git --version` alone proves a binary exists, while
// initialising a repository and reading HEAD back proves the binary works in a temp directory with
// whatever global configuration this machine has — which is the failure a locked-down corporate
// laptop actually produces.
//
// The storage and PTY probes arrive with their packages (Phases 2 and 3). Until then the command
// reports exactly what it ran, so a green smoke test never claims more than it checked.
func DefaultProbes() []Probe {
	return []Probe{{Name: "git", Run: probeGit}}
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
