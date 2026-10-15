package archify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/repomap"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

func TestTheDocumentIsFoundInWhateverTheModelAnswered(t *testing.T) {
	tests := []struct {
		name, answer, want string
	}{
		{"json alone", `{"a":1}`, `{"a":1}`},
		{"in a fence", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"after a sentence", "Here is the diagram:\n{\"a\":{\"b\":\"}\"}}\nDone.", `{"a":{"b":"}"}}`},
		{"a brace in prose first", "Use {braces} carefully: {\"a\":2}", `{"a":2}`},
		{"an escaped quote", `{"a":"say \"}\" twice"}`, `{"a":"say \"}\" twice"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractDocument(tt.answer)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(got))
		})
	}
	for _, answer := range []string{"", "I could not find it.", "[1,2]", "{not json"} {
		_, err := extractDocument(answer)
		require.ErrorIs(t, err, ErrNoDocument, answer)
	}
}

func decoded(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	return doc
}

func TestADocumentIsMadeReadyForArchify(t *testing.T) {
	root := t.TempDir()

	t.Run("its type, version and output are CodeFlow's to set", func(t *testing.T) {
		raw, err := prepare([]byte(`{"diagram_type":"workflow","meta":{"title":"T","output":"../../etc/x.html","repository":{"url":"https://evil"}}}`),
			"architecture", "architecture.html", root, Repo{})
		require.NoError(t, err)
		doc := decoded(t, raw)
		assert.Equal(t, "architecture", doc["diagram_type"])
		assert.InDelta(t, 1, doc["schema_version"], 0)
		meta := doc["meta"].(map[string]any)
		assert.Equal(t, "architecture.html", meta["output"])
		assert.NotContains(t, meta, "repository", "a model never pins a repository")
	})

	t.Run("a sequence's missing steps are placed after the one before", func(t *testing.T) {
		raw, err := prepare([]byte(`{"meta":{"title":"T"},"messages":[{"label":"a"},{"label":"b","y":300},{"label":"c"},{"label":"d","y":20}]}`),
			"sequence", "sequence.html", root, Repo{})
		require.NoError(t, err)
		ys := []float64{}
		for _, m := range decoded(t, raw)["messages"].([]any) {
			ys = append(ys, m.(map[string]any)["y"].(float64))
		}
		assert.Equal(t, []float64{160, 300, 340, 380}, ys, "a y the model gave stays; one below the first row is replaced")
	})

	t.Run("a data flow's missing rows are the next free ones of their stage", func(t *testing.T) {
		raw, err := prepare([]byte(`{"meta":{"title":"T"},"nodes":[{"id":"a","stage":0,"row":0},{"id":"b","stage":0},{"id":"c","stage":1},{"id":"d"}]}`),
			"dataflow", "dataflow.html", root, Repo{})
		require.NoError(t, err)
		rows := map[string]any{}
		for _, n := range decoded(t, raw)["nodes"].([]any) {
			node := n.(map[string]any)
			rows[node["id"].(string)] = node["row"]
		}
		assert.Equal(t, map[string]any{"a": 0.0, "b": 1.0, "c": 0.0, "d": nil}, rows, "a node with no stage is left for the repair round")
	})

	t.Run("not JSON is no document", func(t *testing.T) {
		_, err := prepare([]byte(`nope`), "sequence", "sequence.html", root, Repo{})
		require.ErrorIs(t, err, ErrNoDocument)
	})
}

func TestOnlyCitationsOfRealCommittedFilesAreKept(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "app.ts"), []byte("one\ntwo\nthree"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "dirty.ts"), []byte("x\n"), 0o600))
	outside := filepath.Join(t.TempDir(), "secret.ts")
	require.NoError(t, os.WriteFile(outside, []byte("s\n"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "src", "link.ts")))
	repo := Repo{Meta: &Repository{URL: "https://github.com/acme/app", LinkMode: "local-only", Revision: strings.Repeat("b", 40)}, Dirty: map[string]bool{"src/dirty.ts": true}}

	raw, err := prepare([]byte(`{"meta":{"title":"T"},"components":[
		{"id":"a","sources":[{"path":"./src/app.ts","line":2,"end_line":3},"src/app.ts",{"path":"src/app.ts","line":9}]},
		{"id":"b","sources":[{"path":"src/missing.ts","line":1},{"path":"../etc/passwd"},{"path":"src/link.ts"},{"path":"src/dirty.ts"},{"path":"/abs.ts"}]},
		{"id":"c","sources":[{"path":"src/app.ts","line":2,"end_line":99}]}]}`), "architecture", "architecture.html", root, repo)
	require.NoError(t, err)
	doc := decoded(t, raw)
	components := doc["components"].([]any)
	sourcesOf := func(i int) any { return components[i].(map[string]any)["sources"] }
	assert.Equal(t, []any{
		map[string]any{"path": "src/app.ts", "line": 2.0, "end_line": 3.0},
		map[string]any{"path": "src/app.ts"},
	}, sourcesOf(0), "a line past the end of the file is no line")
	assert.Nil(t, sourcesOf(1), "missing, outside, through a symlink out, uncommitted, absolute: none is cited")
	assert.Equal(t, []any{map[string]any{"path": "src/app.ts", "line": 2.0}}, sourcesOf(2), "an end past the file is dropped, the start kept")
	assert.NotNil(t, doc["meta"].(map[string]any)["repository"], "what cites the commit pins it")

	raw, err = prepare([]byte(`{"meta":{"title":"T"},"components":[{"id":"a","sources":["src/app.ts"]}]}`), "architecture", "architecture.html", root, Repo{})
	require.NoError(t, err)
	doc = decoded(t, raw)
	assert.Nil(t, doc["components"].([]any)[0].(map[string]any)["sources"], "with no commit pinned, nothing can be cited")
	assert.NotContains(t, doc["meta"], "repository")
}

func TestEveryKindHasItsBrief(t *testing.T) {
	for _, kind := range append(Refinable, "system", "refine", "repair") {
		assert.NotEmpty(t, prompt(kind), kind)
	}
	brief := systemPrompt("sequence", `{"schema":1}`, `{"common":1}`, true)
	assert.Contains(t, brief, `{"schema":1}`)
	assert.Contains(t, brief, `{"common":1}`)
	assert.Contains(t, brief, "changing an existing diagram")
	assert.Contains(t, brief, "You may only read")
}

func TestARepairCarriesArchifysWordsAndTheDocument(t *testing.T) {
	kept := repairBrief([]string{"/meta must have title"}, []byte(`{"a":1}`), "the brief", false)
	assert.Contains(t, kept, "- /meta must have title")
	assert.Contains(t, kept, `{"a":1}`)
	assert.NotContains(t, kept, "the brief", "a conversation the engine keeps already has it")
	assert.Contains(t, repairBrief([]string{"x"}, nil, "the brief", true), "the brief")
}

func TestARunIsRefusedBeforeAnyModelIsAsked(t *testing.T) {
	deps := Deps{Runtime: func() Runtime { return Runtime{} }}
	_, err := Generate(t.Context(), deps, GenerateRequest{RootPath: t.TempDir(), Kind: "erd"})
	require.ErrorIs(t, err, ErrUnknownKind)
	_, err = Generate(t.Context(), deps, GenerateRequest{RootPath: t.TempDir(), Kind: "sequence"})
	require.ErrorIs(t, err, ErrNoAI)
	_, err = Refine(t.Context(), deps, RefineRequest{RootPath: t.TempDir(), Path: "a.archify.json", Instruction: "  "})
	require.ErrorIs(t, err, ErrNoInstruction)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("ARCHIFY_BIN", "")
	deps.AI = &scriptedModel{}
	_, err = Generate(t.Context(), deps, GenerateRequest{RootPath: t.TempDir(), Kind: "sequence"})
	require.ErrorIs(t, err, ErrNotInstalled)
}

func TestAWorkflowIsSuggestedFromTheRepositorysOwnProcesses(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"build":"vite build","test:unit":"vitest","format":"x"}}`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github", "workflows", "release.yml"), []byte("on: push"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Makefile"), []byte("deploy: build\n\tx\nVAR := 1\nclean:\n"), 0o600))

	labels := []string{}
	for _, s := range processes(root) {
		labels = append(labels, s.Label)
		assert.NotEmpty(t, s.Topic)
		assert.NotEmpty(t, s.Hint)
	}
	assert.Equal(t, []string{"npm run build", "npm run test:unit", "CI: release", "deploy"}, labels)
	assert.Empty(t, processes(t.TempDir()))
}

func TestATestFileIsRecognisedInEveryLanguage(t *testing.T) {
	for path, want := range map[string]bool{
		"backend/bridge/registry_test.go":     true,
		"src/cart.test.ts":                    true,
		"src/cart.spec.tsx":                   true,
		"src/__tests__/cart.ts":               true,
		"app/tests/test_cart.py":              true,
		"cart_test.py":                        true,
		"src/test/java/acme/CartTest.java":    true,
		"test/cart_test.dart":                 true,
		"backend/bridge/registry.go":          false,
		"src/attestation/router.ts":           false,
		"src/contest.ts":                      false,
		"src/main/java/acme/TestPlanner.java": false,
	} {
		assert.Equal(t, want, isTestPath(path), path)
	}
}

// scriptedModel answers a run with the next of its answers, and records what it was asked.
type scriptedModel struct {
	mu      sync.Mutex
	answers []string
	session string
	asked   []ai.Invocation
	tasks   []ai.Task
}

func (m *scriptedModel) Invoke(_ context.Context, _ string, task ai.Task, inv ai.Invocation, _ bool) (ai.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.asked = append(m.asked, inv)
	m.tasks = append(m.tasks, task)
	if len(m.answers) == 0 {
		return ai.Result{}, errors.New("no more answers") //nolint:err113 // a test's script ran out
	}
	answer := m.answers[0]
	m.answers = m.answers[1:]
	return ai.Result{Text: answer, SessionID: m.session}, nil
}

func committedRepo(t *testing.T, rt Runtime, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
	}
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture"},
		{"remote", "add", "origin", "https://github.com/acme/shop.git"},
	} {
		cmd := proc.Command(t.Context(), "git", append([]string{"-C", root}, args...)...)
		cmd.Env = proc.Environment(rt.Path)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	resolved, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	return resolved
}

// With a real Archify (ARCHIFY_BIN) and node: a model's refused answer goes back to it with
// Archify's words, in the same conversation, and the fixed one comes out validated and cited.
func TestARealArchifyRefusesAndThenAcceptsWhatTheModelDraws(t *testing.T) {
	script := os.Getenv("ARCHIFY_BIN")
	node, err := exec.LookPath("node")
	if script == "" || err != nil {
		t.Skip("needs ARCHIFY_BIN and node")
	}
	t.Setenv("HOME", t.TempDir())
	rt := Runtime{Node: node, Path: filepath.SplitList(os.Getenv("PATH"))}
	root := committedRepo(t, rt, map[string]string{
		"web/checkout.ts": "export async function checkout() {\n  return fetch('/orders', { method: 'POST' })\n}\n",
		"api/orders.go":   "package api\n\nfunc CreateOrder() {}\n",
	})

	model := &scriptedModel{session: "s-1", answers: []string{
		"Here it is:\n```json\n" + `{"meta":{"title":"Checkout"},"participants":[{"id":"web","type":"frontend","label":"Web"}],"messages":[]}` + "\n```",
		`{"meta":{"title":"Checkout","subtitle":"POST /orders"},
		  "participants":[
		    {"id":"web","type":"frontend","label":"checkout()","sources":[{"path":"web/checkout.ts","line":1,"end_line":3}]},
		    {"id":"api","type":"backend","label":"CreateOrder","sources":["api/orders.go","nowhere.go"]}],
		  "messages":[{"from":"web","to":"api","label":"POST /orders"},{"from":"api","to":"web","label":"201","variant":"return"}]}`,
	}}
	var removed bool
	deps := Deps{
		Runtime:   func() Runtime { return rt },
		AI:        model,
		Maps:      repomap.NewService(t.TempDir()),
		MCPConfig: func(string) (string, func(), error) { return "mcp.json", func() { removed = true }, nil },
	}

	drawn, err := Generate(t.Context(), deps, GenerateRequest{RootPath: root, Kind: "sequence", Topic: "the checkout", Language: "es"})
	require.NoError(t, err)
	assert.Equal(t, 1, drawn.Rounds)
	assert.Equal(t, "Checkout", drawn.Title)
	assert.Equal(t, "sequence", drawn.DiagramType)
	assert.True(t, removed, "the MCP configuration is removed when the run ends")

	require.Len(t, model.asked, 2)
	first, second := model.asked[0], model.asked[1]
	assert.Equal(t, []ai.Task{ai.TaskDiagram, ai.TaskDiagram}, model.tasks)
	assert.True(t, first.RequireReadOnly)
	assert.True(t, first.ReadOnly)
	assert.Equal(t, root, first.WorkingDir)
	assert.Equal(t, []string{"Read", "Grep", "Glob", "mcp__codemap"}, first.AllowedTools)
	assert.Equal(t, "mcp.json", first.MCPConfig)
	assert.Contains(t, first.SystemPrompt, `"messages"`, "the installed schema is in the brief")
	assert.Contains(t, first.StdinContent, "What to draw: the checkout")
	assert.Contains(t, first.StdinContent, "Write the labels in Spanish.")
	assert.Contains(t, first.StdinContent, "Repository map:", "the map's overview orients the model")
	assert.Empty(t, first.SessionID)
	assert.Equal(t, "s-1", second.SessionID, "the repair goes back to the same conversation")
	assert.Contains(t, second.StdinContent, "/messages", "Archify's own words go back")

	doc := decoded(t, []byte(drawn.Document))
	meta := doc["meta"].(map[string]any)
	assert.Equal(t, "sequence.html", meta["output"])
	require.NotNil(t, meta["repository"], "the citations pin the commit")
	participants := doc["participants"].([]any)
	assert.Equal(t, []any{map[string]any{"path": "web/checkout.ts", "line": 1.0, "end_line": 3.0}}, participants[0].(map[string]any)["sources"])
	assert.Equal(t, []any{map[string]any{"path": "api/orders.go"}}, participants[1].(map[string]any)["sources"], "a file that does not exist is not cited")

	// What the run accepted is what the viewer can draw: both check at the same quality.
	cli, err := Find(root)
	require.NoError(t, err)
	page, err := cli.Render(t.Context(), rt, root, []byte(drawn.Document), t.TempDir())
	require.NoError(t, err)
	assert.FileExists(t, page.HTMLPath)

	// A model that never gets it right is refused after two repairs, in Archify's words.
	stubborn := &scriptedModel{answers: []string{"no", "still no", "nothing"}}
	deps.AI = stubborn
	_, err = Generate(t.Context(), deps, GenerateRequest{RootPath: root, Kind: "architecture"})
	require.ErrorIs(t, err, ErrInvalidDiagram)
	require.Len(t, stubborn.asked, 3)
	assert.Contains(t, stubborn.asked[2].StdinContent, "What it was asked to draw:", "an engine with no conversation gets the brief again")

	// Refining: the document in the repository, the change, the same checks.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "codemap"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "codemap", "checkout-sequence.archify.json"), []byte(drawn.Document), 0o600))
	changed := strings.Replace(drawn.Document, `"Checkout"`, `"Checkout flow"`, 1)
	deps.AI = &scriptedModel{answers: []string{changed}}
	refined, err := Refine(t.Context(), deps, RefineRequest{RootPath: root, Path: "codemap/checkout-sequence.archify.json", Instruction: "rename it"})
	require.NoError(t, err)
	assert.Equal(t, "Checkout flow", refined.Title)
	assert.Equal(t, 0, refined.Rounds)
	asked := deps.AI.(*scriptedModel).asked[0]
	assert.Contains(t, asked.StdinContent, "The change: rename it")
	assert.Contains(t, asked.StdinContent, `"Checkout"`, "the current document is handed over")
}
