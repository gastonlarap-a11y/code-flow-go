// Command parity is the differential oracle of MIGRATION-GO.md §9.7.
//
// It drives the **installed 2.7.1 core** and this repository's registry through the same scripted
// request list and compares the answers. Any difference is either a defect in the port or a
// behaviour to record in `docs/` — never something silently accepted.
//
// It is a developer tool. It is not shipped, nothing in `backend/` imports it, and it exists
// because the alternative — reading 36 000 lines of C# and believing the port matches — is not
// evidence.
//
//	go run ./tools/parity
//	go run ./tools/parity -core /path/to/codeflow-core -v
//
// # It never touches the real data directory
//
// Both cores are pointed at directories under one temporary root: the old one through a replaced
// `HOME`, the new one through `platform.NewPaths`. The run refuses to start if either resolves
// inside the real home, because "the tool that proves your data survives" is the last thing that
// should be able to open it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// defaultCorePath is where a macOS install keeps the core it shipped with.
const defaultCorePath = "/Applications/CodeFlow.app/Contents/Resources/core/codeflow-core"

// The old core's endpoint is a unix domain socket, and a unix domain socket path is not a path: it
// has to fit in `sun_path`, which is 104 bytes on macOS and 108 on Linux. The core builds it as
// `<home>/CodeFlow/.ipc-<pid>.sock`, so the home directory this tool invents has a budget — and on
// macOS the default `$TMPDIR` is a per-user path under `/var/folders/…` that spends 49 of those
// bytes before anything is added to it.
//
// That is why the temporary root is chosen rather than taken, and why the length is checked before
// the core is started. Letting it fail instead produces an unhandled .NET exception about a
// parameter named 'path', which says nothing about what to do next.
const (
	sunPathMax     = 104
	socketTemplate = "/CodeFlow/.ipc-2147483647.sock" // the longest a pid can make it
)

func main() {
	corePath := flag.String("core", defaultCorePath, "the installed 2.7.x core to compare against")
	root := flag.String("root", "", "where to put the temporary directories (default: the shortest writable temp path)")
	verbose := flag.Bool("v", false, "print every answer, not only the differing ones")
	keep := flag.Bool("keep", false, "keep the temporary directories for inspection")
	timings := flag.Bool("time", false, "print what each request cost on each side (§11 Phase 9 step 4)")
	flag.Parse()

	if err := run(*corePath, *root, *verbose, *keep, *timings); err != nil {
		fmt.Fprintln(os.Stderr, "parity: "+err.Error())
		os.Exit(1)
	}
}

// shortestTempDir prefers `/tmp` to `$TMPDIR` when both work, for the sun_path budget above.
func shortestTempDir() string {
	candidates := []string{os.TempDir()}
	if runtime.GOOS != "windows" {
		candidates = append([]string{"/tmp"}, candidates...)
	}

	for _, candidate := range candidates {
		probe, err := os.MkdirTemp(candidate, "cf-probe-")
		if err != nil {
			continue
		}
		_ = os.RemoveAll(probe)
		return candidate
	}
	return os.TempDir()
}

// outcome is what one step produced on one side: a result, or the verbatim error message.
type outcome struct {
	answer json.RawMessage
	err    error
}

func (o outcome) render(n normaliser) string {
	if o.err != nil {
		return "ERROR: " + n.text(o.err.Error())
	}
	rendered, err := n.normalise(o.answer)
	if err != nil {
		return "UNREADABLE: " + err.Error()
	}
	return rendered
}

func run(corePath, tempRoot string, verbose, keep, timings bool) error {
	if runtime.GOOS != "darwin" && corePath == defaultCorePath {
		return errors.New("point -core at the installed core; the default path is the macOS one")
	}
	if _, err := os.Stat(corePath); err != nil {
		return fmt.Errorf("the 2.7.x core is not at %s: %w", corePath, err)
	}

	if tempRoot == "" {
		tempRoot = shortestTempDir()
	}
	root, err := os.MkdirTemp(tempRoot, "cfp-")
	if err != nil {
		return fmt.Errorf("create the temporary root under %s: %w", tempRoot, err)
	}
	if keep {
		fmt.Println("keeping " + root)
	} else {
		defer func() { _ = os.RemoveAll(root) }()
	}

	oldHome := filepath.Join(root, "old")
	newBase := filepath.Join(root, "new")
	repo := filepath.Join(root, "fixture")
	if err := refuseTheRealHome(oldHome, newBase); err != nil {
		return err
	}
	if budget := len(oldHome) + len(socketTemplate); budget > sunPathMax {
		return fmt.Errorf(
			"%s is %d bytes too long for the old core's socket (a unix socket path is capped at %d); pass -root with a shorter directory",
			oldHome, budget-sunPathMax, sunPathMax)
	}

	// Ctrl-C has to reach the old core, or a cancelled run leaves a .NET process holding a socket.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := buildFixtureRepo(ctx, repo); err != nil {
		return fmt.Errorf("build the fixture repository: %w", err)
	}

	fmt.Println("starting the 2.7.1 core…")
	old, err := startOldCore(ctx, corePath, oldHome)
	if err != nil {
		return err
	}
	defer old.stop()

	fresh, err := startNewCore(ctx, newBase)
	if err != nil {
		return err
	}
	defer fresh.stop()

	oldNormaliser := normaliser{base: filepath.Join(oldHome, "CodeFlow"), home: oldHome, repo: repo}
	newNormaliser := normaliser{base: newBase, repo: repo}

	// Captured ids are per side: the same script produces a different uuid in each database, which
	// is why they are substituted here and normalised away in the comparison.
	oldCaptured, newCaptured := map[string]string{}, map[string]string{}

	var differences, expected int
	script := scriptFor(repo)

	fmt.Printf("replaying %d requests against both cores\n\n", len(script))
	for _, s := range script {
		oldStart := time.Now()
		oldResult := outcome{}
		oldResult.answer, oldResult.err = old.Call(s.method, resolve(s.params, oldCaptured))
		oldTook := time.Since(oldStart)

		newStart := time.Now()
		newResult := outcome{}
		newResult.answer, newResult.err = fresh.Call(ctx, s.method, resolve(s.params, newCaptured))
		newTook := time.Since(newStart)

		if timings {
			// 2.7.1's number includes one round trip over its unix socket, because that is what the
			// command cost a user: the transport was part of the answer's latency. 3.0's is an
			// in-process call, because there is no transport left to include.
			fmt.Printf("  %8s → %8s   %s\n", oldTook.Round(time.Microsecond), newTook.Round(time.Microsecond), s.name)
		}

		capture(s, oldResult, oldCaptured)
		capture(s, newResult, newCaptured)

		oldText := oldResult.render(oldNormaliser)
		newText := newResult.render(newNormaliser)

		switch {
		case oldText == newText:
			if verbose {
				fmt.Printf("  same       %s\n", s.name)
			}
		case s.differs != "":
			expected++
			// The differing lines are printed for an explained difference too, and deliberately:
			// an entry in `differs` is a claim about *which* difference was investigated, and a
			// report that only restated the claim would let the claim outlive the fact. If these
			// lines stop being the ones the reason describes, the reason is now wrong.
			fmt.Printf("  expected   %s   (%s)\n             %s\n", s.name, s.method, s.differs)
			fmt.Print(difference(oldText, newText))
		default:
			differences++
			fmt.Printf("  DIFFERENT  %s   (%s)\n", s.name, s.method)
			fmt.Print(difference(oldText, newText))
		}
	}

	fmt.Printf("\n%d requests · %d unexplained differences · %d explained\n",
		len(script), differences, expected)
	if differences > 0 {
		return fmt.Errorf("%d unexplained differences", differences)
	}
	fmt.Println("no unexplained differences")
	return nil
}

// capture binds a field of a successful result to a name later steps can substitute.
//
// A list result captures from its first element, which is what "the newest commit" means for
// `list_commits`; an empty list captures nothing and the dependent steps then run with the
// placeholder unsubstituted, which fails loudly on both sides rather than quietly on one.
func capture(s step, result outcome, into map[string]string) {
	if len(s.capture) == 0 || result.err != nil {
		return
	}

	object := result.answer
	var list []json.RawMessage
	if err := json.Unmarshal(object, &list); err == nil {
		if len(list) == 0 {
			return
		}
		object = list[0]
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(object, &fields); err != nil {
		return
	}
	for name, field := range s.capture {
		raw, present := fields[field]
		if !present {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err == nil {
			into[name] = value
		}
	}
}

// refuseTheRealHome is the guard that makes this tool safe to run without reading it first.
//
// §9.7 says "never run it against the real `~/CodeFlow`" — a sentence in a document, which is not a
// mechanism. This is the mechanism.
func refuseTheRealHome(paths ...string) error {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		// Unable to tell where the real data is, so unable to promise it is not being used.
		return fmt.Errorf("cannot resolve the home directory to check against: %w", err)
	}
	real := filepath.Clean(filepath.Join(home, "CodeFlow"))

	for _, path := range paths {
		clean := filepath.Clean(path)
		if clean == real || strings.HasPrefix(clean, real+string(filepath.Separator)) {
			return fmt.Errorf("refusing to run against %s: that is the real data directory", clean)
		}
	}
	return nil
}
