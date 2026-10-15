package archify

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
)

// Diagrams a model draws by reading the repository (ARCHIFY-007, ARCHIFY-008).
//
// The model describes; Archify validates and draws. The prompt hands the model the schema of the
// user's own Archify — so it always matches the version that will check the answer — and the code
// map's overview to orient itself, and lets it read the repository and ask the map, never change
// anything. Its answer is made ready (diagram type, output, a sequence's steps, every citation
// checked against the repository) and validated; what Archify refuses goes back to the model with
// Archify's own words, twice at most.

//go:embed prompts/*.txt
var prompts embed.FS

// Kinds are the diagrams a model is asked to draw from the repository.
var Kinds = []string{"architecture", "workflow", "sequence", "dataflow", "lifecycle"}

// Refinable are the documents a model may be asked to change: what it draws, and the class
// diagrams the map draws.
var Refinable = append(slices.Clone(Kinds), "class")

// The refusals, each naming what is wrong.
var (
	ErrUnknownKind   = errors.New("archify draws no such diagram from a repository")
	ErrNoAI          = errors.New("no AI engine is configured")
	ErrNoInstruction = errors.New("say what to change in the diagram")
)

const (
	// maxRepairRounds is how many times a refused document goes back to the model.
	maxRepairRounds = 2
	// orientationChars bounds the map's overview a prompt carries.
	orientationChars = 12_000
)

// tools are what a diagram's run may use: reading the repository, and asking the map.
var tools = []string{"Read", "Grep", "Glob", "mcp__codemap"}

// Invoker is what the diagrams ask of the AI layer.
type Invoker interface {
	Invoke(ctx context.Context, runID string, task ai.Task, inv ai.Invocation, withTrace bool) (ai.Result, error)
}

// GenerateRequest asks for one diagram of a repository.
type GenerateRequest struct {
	RootPath string
	Kind     string
	// Topic narrows the diagram — a route, a process, an entity; empty lets the model pick the most
	// important one.
	Topic    string
	RunID    string
	Language string
}

// RefineRequest asks for a change to a document in the repository.
type RefineRequest struct {
	RootPath    string
	Path        string
	Instruction string
	RunID       string
	Language    string
}

// Drawn is a document a model drew and Archify accepted.
type Drawn struct {
	Document    string `json:"document"`
	Title       string `json:"title"`
	DiagramType string `json:"diagram_type"`
	// Rounds counts the times it went back to the model to be fixed.
	Rounds int `json:"rounds"`
}

// briefs are the prompts by name, read once: they are embedded, so a missing one is a build mistake
// the package's own test catches.
var briefs = readBriefs()

func readBriefs() map[string]string {
	entries, err := prompts.ReadDir("prompts")
	if err != nil {
		panic(fmt.Sprintf("archify: reading the embedded prompts: %v", err)) // composition-time: the files are embedded
	}
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		text, err := prompts.ReadFile("prompts/" + entry.Name())
		if err != nil {
			panic(fmt.Sprintf("archify: reading prompt %s: %v", entry.Name(), err)) // composition-time, as above
		}
		out[strings.TrimSuffix(entry.Name(), ".txt")] = string(text)
	}
	return out
}

func prompt(name string) string { return briefs[name] }

// systemPrompt is the brief for one diagram type: how to work, what this type shows, and the
// install's own schema.
func systemPrompt(kind, schema, common string, refine bool) string {
	var b strings.Builder
	b.WriteString(prompt("system"))
	b.WriteString("\n")
	b.WriteString(prompt(kind))
	if refine {
		b.WriteString("\n")
		b.WriteString(prompt("refine"))
	}
	b.WriteString("\nThe JSON Schema of this diagram type:\n")
	b.WriteString(schema)
	b.WriteString("\n\nThe definitions it refers to (common.schema.json):\n")
	b.WriteString(common)
	b.WriteString("\n")
	return b.String()
}

func languageName(code string) string {
	if strings.HasPrefix(strings.ToLower(code), "es") {
		return "Spanish"
	}
	return "English"
}

// Generate draws a diagram of the repository with the AI engine routed to the diagram task.
func Generate(ctx context.Context, deps Deps, request GenerateRequest) (Drawn, error) {
	if !slices.Contains(Kinds, request.Kind) {
		return Drawn{}, fmt.Errorf("%w: %q", ErrUnknownKind, request.Kind)
	}
	root, cli, schema, common, err := prepareRun(deps, request.RootPath, request.Kind)
	if err != nil {
		return Drawn{}, err
	}
	overview := ""
	if deps.Maps != nil {
		if snap, _, err := deps.Maps.Snapshot(ctx, root); err == nil {
			overview = snap.Overview(orientationChars)
			// So the map's MCP server, another process, reads what this one knows. A cache that could
			// not be written only costs that server the time to read the changed files itself.
			_ = deps.Maps.Flush(root)
		}
	}
	topic := strings.TrimSpace(request.Topic)
	if topic == "" {
		topic = "the most important one in this repository — you choose"
	}
	var stdin strings.Builder
	fmt.Fprintf(&stdin, "Repository: %s\nWorking directory: %s\nWrite the labels in %s.\n\nWhat to draw: %s\n",
		filepath.Base(root), root, languageName(request.Language), topic)
	if overview != "" {
		fmt.Fprintf(&stdin, "\nThe code map's overview of the repository — to orient yourself; check in the files whatever you cite:\n%s\n", overview)
	}
	return draw(ctx, deps, cli, root, request.Kind, request.RunID, ai.Invocation{
		Prompt:       fmt.Sprintf("Read the repository and answer the %s diagram it asks for, as one Archify JSON document.", request.Kind),
		SystemPrompt: systemPrompt(request.Kind, schema, common, false),
		StdinContent: stdin.String(),
	})
}

// Refine asks the model for a changed version of a document in the repository, and answers it
// without writing it: the view keeps the previous one to undo to.
func Refine(ctx context.Context, deps Deps, request RefineRequest) (Drawn, error) {
	instruction := strings.TrimSpace(request.Instruction)
	if instruction == "" {
		return Drawn{}, ErrNoInstruction
	}
	root, document, err := readDocument(request.RootPath, request.Path)
	if err != nil {
		return Drawn{}, err
	}
	kind, err := DiagramType(document)
	if err != nil {
		return Drawn{}, err
	}
	if !slices.Contains(Refinable, kind) {
		return Drawn{}, fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}
	_, cli, schema, common, err := prepareRun(deps, root, kind)
	if err != nil {
		return Drawn{}, err
	}
	stdin := fmt.Sprintf("Write the labels in %s.\n\nThe change: %s\n\nThe current document:\n%s\n", languageName(request.Language), instruction, document)
	return draw(ctx, deps, cli, root, kind, request.RunID, ai.Invocation{
		Prompt:       "Apply the change to the diagram and answer the whole updated Archify document.",
		SystemPrompt: systemPrompt(kind, schema, common, true),
		StdinContent: stdin,
	})
}

// prepareRun checks what every run needs: an engine, a repository, and an Archify whose schema the
// model will be handed.
func prepareRun(deps Deps, rootPath, kind string) (root string, cli CLI, schema, common string, err error) {
	if deps.AI == nil {
		return "", CLI{}, "", "", ErrNoAI
	}
	root, err = filepath.EvalSymlinks(strings.TrimSpace(rootPath))
	if err != nil {
		return "", CLI{}, "", "", fmt.Errorf("resolving %s: %w", rootPath, err)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return "", CLI{}, "", "", fmt.Errorf("%s is not a folder", rootPath) //nolint:err113 // names what was asked for
	}
	cli, err = Find(root)
	if err != nil {
		return "", CLI{}, "", "", err
	}
	schema, common, err = cli.Schema(kind)
	if err != nil {
		return "", CLI{}, "", "", err
	}
	return root, cli, schema, common, nil
}

// draw runs the model, makes its answer ready and validates it, sending what Archify refuses back
// to the model — in the same conversation where the engine keeps one.
func draw(ctx context.Context, deps Deps, cli CLI, root, kind, runID string, inv ai.Invocation) (Drawn, error) {
	inv.AllowedTools = tools
	inv.WorkingDir = root
	inv.ReadOnly = true
	inv.RequireReadOnly = true
	if deps.MCPConfig != nil {
		config, cleanup, err := deps.MCPConfig(root)
		if err != nil {
			return Drawn{}, err
		}
		// Deleted on every exit path: a document, a refusal, a cancellation.
		defer cleanup()
		inv.MCPConfig = config
	}
	rt := deps.Runtime()
	repo := ReadRepo(ctx, rt, root)
	output := kind + ".html"
	brief := inv.StdinContent

	var problems []string
	for round := 0; ; round++ {
		result, err := deps.AI.Invoke(ctx, runID, ai.TaskDiagram, inv, false)
		if err != nil {
			return Drawn{}, err
		}
		document, problem := answerToDocument(result.Text, kind, output, root, repo)
		if problem == nil {
			report, err := cli.Validate(ctx, rt, root, document)
			if err != nil {
				return Drawn{}, err
			}
			if report.OK {
				return Drawn{Document: string(document), Title: titleOf(document), DiagramType: kind, Rounds: round}, nil
			}
			problems = report.Problems()
		} else {
			problems = []string{problem.Error()}
		}
		if round == maxRepairRounds {
			return Drawn{}, fmt.Errorf("%w: %s", ErrInvalidDiagram, strings.Join(problems, "; "))
		}
		inv.Prompt = "Fix the problems Archify found and answer the whole document again."
		inv.StdinContent = repairBrief(problems, document, brief, result.SessionID == "")
		if result.SessionID != "" {
			inv.SessionID = result.SessionID
		}
	}
}

func answerToDocument(answer, kind, output, root string, repo Repo) ([]byte, error) {
	raw, err := extractDocument(answer)
	if err != nil {
		return nil, err
	}
	return prepare(raw, kind, output, root, repo)
}

// repairBrief is what goes back to the model: Archify's problems and the document they are about,
// and — for an engine that keeps no conversation — the original brief too.
func repairBrief(problems []string, document []byte, original string, fresh bool) string {
	var b strings.Builder
	b.WriteString(prompt("repair"))
	b.WriteString("\nProblems:\n")
	for _, p := range problems {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	if len(document) > 0 {
		fmt.Fprintf(&b, "\nThe document:\n%s\n", document)
	}
	if fresh {
		fmt.Fprintf(&b, "\nWhat it was asked to draw:\n%s\n", original)
	}
	return b.String()
}

func titleOf(document []byte) string {
	var head struct {
		Meta struct {
			Title string `json:"title"`
		} `json:"meta"`
	}
	_ = json.Unmarshal(document, &head) // a document without a title names its file after its type
	return head.Meta.Title
}
