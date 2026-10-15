// Package requirements finds the programs the app's features need and does not ship (REQ-001): on
// this machine or not, which version, and whether that version will do.
//
// The AI engines are not here: each is already looked for, with its own version and sign-in, by
// `check_ai_provider` (AI-004), and the requirements view shows those beside these.
package requirements

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/archify"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

// The tools, `VERBATIM` — the renderer's catalogue keys its install commands by them.
const (
	Git     = "git"
	Node    = "node"
	Go      = "go"
	Archify = "archify"
)

// minNode is the oldest Node.js the `skills` CLI that installs Archify runs on (its package's
// `engines`, skills 1.7.2): the code map's TypeScript reader is content with less, the installer is
// not.
var minNode = [3]int{22, 20, 0}

// versionTimeout bounds one `--version`: a binary that hangs is reported as not answering.
const versionTimeout = 5 * time.Second

// Tool is one program as found on this machine.
type Tool struct {
	ID        string `json:"id"`
	Installed bool   `json:"installed"`
	// Version is what it reported, "" when it is not installed or did not say.
	Version string `json:"version"`
	Path    string `json:"path"`
	// Supported is false for an install too old for what it is needed for.
	Supported bool `json:"supported"`
}

// Finder is where the probes look: the search path the app's child processes get.
type Finder struct {
	// Lookup resolves a program's name to the executable a child process would run, or "".
	Lookup func(name string) string
	// Path is the PATH child processes are given.
	Path []string
}

// Status answers every tool, in a fixed order. root is the open repository, "" for none — Archify
// may be installed for one repository only.
func (f Finder) Status(ctx context.Context, root string) []Tool {
	out := make([]Tool, 0, 4)
	out = append(out, f.program(ctx, Git, "--version", nil))
	out = append(out, f.program(ctx, Node, "--version", func(v string) bool { return atLeast(v, minNode) }))
	out = append(out, f.program(ctx, Go, "version", nil))

	tool := Tool{ID: Archify}
	if script, version, found := archify.Located(root); found {
		tool = Tool{ID: Archify, Installed: true, Version: version, Path: script, Supported: archify.Supported(version)}
	}
	return append(out, tool)
}

// program finds a program and asks it its version. supported judges the version; nil takes any.
func (f Finder) program(ctx context.Context, id, versionArg string, supported func(string) bool) Tool {
	path := f.Lookup(id)
	if path == "" {
		return Tool{ID: id}
	}
	version, err := f.version(ctx, path, versionArg)
	tool := Tool{ID: id, Installed: true, Version: version, Path: path, Supported: true}
	if err != nil {
		// Found but not answering: there, and nothing known of its version.
		return tool
	}
	if supported != nil {
		tool.Supported = supported(version)
	}
	return tool
}

var versionPattern = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)

// ErrNoVersion is a program whose answer names no version.
var ErrNoVersion = errors.New("the program printed no version")

func (f Finder) version(ctx context.Context, path, arg string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	cmd := proc.Command(ctx, path, arg)
	cmd.Env = proc.Environment(f.Path)
	out, err := cmd.Output()
	if err != nil {
		return "", err //nolint:wrapcheck // a probe's failure is only ever logged as "not answering"
	}
	return parseVersion(string(out))
}

// parseVersion reads the first dotted version in what a program printed: `git version 2.50.1`,
// `v24.22.0`, `go version go1.27.2 darwin/arm64`.
func parseVersion(output string) (string, error) {
	if v := versionPattern.FindString(output); v != "" {
		return v, nil
	}
	return "", ErrNoVersion
}

// atLeast compares a dotted version with a minimum, missing parts reading as 0.
func atLeast(version string, minimum [3]int) bool {
	parts := strings.Split(version, ".")
	for i := range 3 {
		n := 0
		if i < len(parts) {
			var err error
			if n, err = strconv.Atoi(parts[i]); err != nil {
				return false
			}
		}
		if n != minimum[i] {
			return n > minimum[i]
		}
	}
	return true
}
