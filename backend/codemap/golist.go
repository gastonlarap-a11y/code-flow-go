package codemap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

// Listing a Go module's packages (CODEMAP-003).

// listFields are the only `go list` fields read. Naming them keeps the output small: the default
// JSON of a large module's dependency graph runs to megabytes of fields nobody reads.
const listFields = "ImportPath,Name,Dir,GoFiles,CgoFiles,Imports,ImportMap,Export,ForTest,DepOnly,Standard,Module,Error"

// listedPackage is one package as `go list -json` reports it. ImportPath doubles as the package's
// identity: a test variant carries its suffix ("p [p.test]").
type listedPackage struct {
	ImportPath string
	Name       string
	Dir        string
	GoFiles    []string
	CgoFiles   []string
	Imports    []string
	ImportMap  map[string]string
	Export     string
	ForTest    string
	DepOnly    bool
	Standard   bool
	Module     *struct {
		Path      string
		GoVersion string
	}
	Error *struct{ Err string }
}

// isTestMain reports whether p is the generated `p.test` main package, whose only file lives in
// the build cache.
func (p *listedPackage) isTestMain() bool {
	return p.Name == "main" && strings.HasSuffix(p.ImportPath, ".test")
}

// errNoGo is the toolchain missing, which costs the Go half of the map and nothing else.
var errNoGo = errors.New("the Go toolchain was not found, so Go code is not mapped") //nolint:staticcheck // ST1005: shown to the user as a sentence

// goLister runs `go list`; a variable so tests can swap the binary.
type goLister struct {
	binary string
	path   []string
}

// list answers every package of the module in dir — its own, their test variants, and every
// dependency with the path of its compiler export data — in dependency order.
//
// `-export` makes the go command build what is missing from its cache, which is what lets the
// module's packages be type-checked against their dependencies without ever parsing a dependency's
// source. `-e` keeps a package that does not build in the answer, carrying its error.
//
// Without export it only lists — no build happens — which is what source mode needs.
func (l goLister) list(ctx context.Context, dir string, export bool) ([]*listedPackage, error) {
	if l.binary == "" {
		return nil, errNoGo
	}
	args := []string{"list", "-e", "-json=" + listFields, "-deps", "-test"}
	if export {
		args = append(args, "-export")
	}
	cmd := proc.Command(ctx, l.binary, append(args, "./...")...)
	cmd.Dir = dir
	cmd.Env = proc.Environment(l.path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	packages, decodeErr := decodePackages(&stdout)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if len(packages) == 0 {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" && runErr != nil {
			detail = runErr.Error()
		}
		if detail == "" && decodeErr != nil {
			detail = decodeErr.Error()
		}
		return nil, fmt.Errorf("go list in %s: %s", dir, firstLine(detail))
	}
	return packages, nil
}

// version answers the toolchain the go command uses in dir — after any automatic toolchain switch
// the module asks for — as "go1.27.2".
func (l goLister) version(ctx context.Context, dir string) (string, error) {
	cmd := proc.Command(ctx, l.binary, "env", "GOVERSION")
	cmd.Dir = dir
	cmd.Env = proc.Environment(l.path)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go env GOVERSION: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func decodePackages(r io.Reader) ([]*listedPackage, error) {
	decoder := json.NewDecoder(r)
	packages := make([]*listedPackage, 0, 256)
	for {
		var p listedPackage
		err := decoder.Decode(&p)
		if errors.Is(err, io.EOF) {
			return packages, nil
		}
		if err != nil {
			return packages, fmt.Errorf("decoding go list output: %w", err)
		}
		packages = append(packages, &p)
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return clip(line, 300)
}
