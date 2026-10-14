package codemap

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

// The TypeScript half of the map (CODEMAP-005).

// extractorSource is the extractor, handed to Node on stdin: nothing is written to disk to run it.
//
//go:embed extract.mjs
var extractorSource string

// extractVersion is the shape of the extractor's JSON this file reads.
const extractVersion = 1

// tsExtract is the extractor's output.
type tsExtract struct {
	Version        int              `json:"version"`
	Projects       []tsProject      `json:"projects"`
	Symbols        []tsSymbol       `json:"symbols"`
	Edges          []tsEdge         `json:"edges"`
	Bindings       []tsBinding      `json:"bindings"`
	BindingAliases []tsBindingAlias `json:"binding_aliases"`
	Calls          []tsCall         `json:"calls"`
	Strings        []tsString       `json:"strings"`
}

type tsProject struct {
	TSConfig   string `json:"tsconfig"`
	TypeScript string `json:"typescript"`
	API        string `json:"api"`
	Files      int    `json:"files"`
	Note       string `json:"note"`
}

type tsSymbol struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	EndLine   int    `json:"end_line"`
	Parent    string `json:"parent"`
	Signature string `json:"signature"`
	Doc       string `json:"doc"`
}

type tsEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
	File string `json:"file"`
	Line int    `json:"line"`
}

// tsBinding is a call into Wails bindings: the import names the Go package and service, the call
// names the method.
type tsBinding struct {
	From      string `json:"from"`
	Specifier string `json:"specifier"`
	Name      string `json:"name"`
	Member    string `json:"member"`
	File      string `json:"file"`
	Line      int    `json:"line"`
}

// tsBindingAlias is an interface of the repository satisfied by a Wails binding as a whole.
type tsBindingAlias struct {
	Interface string `json:"iface"`
	Specifier string `json:"specifier"`
	Name      string `json:"name"`
}

// tsCall is a call whose first argument is a literal string: an endpoint key, a command name, a
// table name.
type tsCall struct {
	From   string `json:"from"`
	Callee string `json:"callee"`
	Value  string `json:"value"`
	File   string `json:"file"`
	Line   int    `json:"line"`
}

type tsString struct {
	From string `json:"from"`
	Text string `json:"text"`
	File string `json:"file"`
	Line int    `json:"line"`
}

var tsKinds = map[string]Kind{
	"module":           KindModule,
	"test_file":        KindTestFile,
	"function":         KindFunc,
	"component":        KindComponent,
	"hook":             KindHook,
	"method":           KindMethod,
	"class":            KindType,
	"interface":        KindInterface,
	"interface_method": KindInterfaceMethod,
}

var tsEdgeKinds = map[string]EdgeKind{
	"call":     EdgeCall,
	"ref":      EdgeRef,
	"render":   EdgeRender,
	"dispatch": EdgeDispatch,
}

// errNoNode is Node missing, which costs the TypeScript half of the map and nothing else.
var errNoNode = errors.New("Node.js was not found, so TypeScript code is not mapped") //nolint:staticcheck // ST1005: shown to the user as a sentence

// tsRunner runs the extractor.
type tsRunner struct {
	node string
	path []string
}

// extract runs the extractor over every project at once: one Node process, one TypeScript load per
// project.
func (r tsRunner) extract(ctx context.Context, root string, tsconfigs []string) (*tsExtract, error) {
	if r.node == "" {
		return nil, errNoNode
	}
	args := []string{"--input-type=module", "-", "--root", root}
	for _, tsconfig := range tsconfigs {
		args = append(args, "--project", tsconfig)
	}
	cmd := proc.Command(ctx, r.node, args...)
	cmd.Dir = root
	cmd.Env = proc.Environment(r.path)
	cmd.Stdin = strings.NewReader(extractorSource)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("the TypeScript extractor failed: %s", firstLine(stderr.String()+" "+err.Error()))
	}

	var out tsExtract
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("reading the TypeScript extractor's output: %w", err)
	}
	if out.Version != extractVersion {
		return nil, fmt.Errorf("the TypeScript extractor answered version %d, expected %d", out.Version, extractVersion) //nolint:err113 // names both versions
	}
	return &out, nil
}

func (s tsSymbol) node() *Node {
	return &Node{
		ID:        s.ID,
		Name:      s.Name,
		Lang:      LangTS,
		Kind:      tsKinds[s.Kind],
		Pos:       Pos{File: s.File, Line: s.Line, EndLine: s.EndLine},
		Signature: s.Signature,
		Doc:       s.Doc,
		Parent:    s.Parent,
	}
}
