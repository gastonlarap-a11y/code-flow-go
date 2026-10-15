package codemap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/gastonlarap-a11y/code-flow/backend/archify"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
)

// Keeping a map fresh for each open repository (CODEMAP-002).

// recheckEvery bounds how often a map walks its repository looking for changes. Questions arrive
// in bursts — a search box asks on every keystroke — and one walk serves the whole burst.
const recheckEvery = 2 * time.Second

// maxIndexes is how many repositories keep a map in memory at once; the least recently asked about
// is dropped first and rebuilt if it is asked about again.
const maxIndexes = 4

// EventProgress reports which stage a build is in, so the panel can say what it is waiting for.
const EventProgress = "codemap:progress"

// Build stages, `VERBATIM` — the renderer matches them.
const (
	StageScanning   = "scanning"
	StageGo         = "go"
	StageTypeScript = "typescript"
	StageLinking    = "linking"
	StageReady      = "ready"
)

// ProgressEvent is the payload of EventProgress.
type ProgressEvent struct {
	RootPath string `json:"root_path"`
	Stage    string `json:"stage"`
}

// Tools are the binaries a build runs, and the PATH their children get.
type Tools struct {
	Go   string
	Node string
	Path []string
}

// goInstallDirs are where the Go installers put the toolchain, which a GUI app launched from the
// Finder may not have on its PATH.
func goInstallDirs() []string {
	if runtime.GOOS == "windows" {
		return []string{filepath.Join(os.Getenv("ProgramFiles"), "Go", "bin")}
	}
	return []string{"/usr/local/go/bin", "/opt/homebrew/bin", "/usr/local/bin"}
}

// DiscoverTools finds go and node the way the AI CLIs are found (AI-005): the known install
// directories first, then the inherited PATH.
func DiscoverTools() Tools {
	dirs := append(ai.SearchDirs(), goInstallDirs()...)
	return Tools{Go: findIn(dirs, "go"), Node: findIn(dirs, "node"), Path: dirs}
}

func findIn(dirs []string, name string) string {
	candidates := []string{name}
	if runtime.GOOS == "windows" {
		candidates = []string{name + ".exe", name + ".cmd"}
	}
	for _, dir := range dirs {
		for _, candidate := range candidates {
			full := filepath.Join(dir, candidate)
			info, err := os.Stat(full)
			if err != nil || info.IsDir() {
				continue
			}
			if runtime.GOOS == "windows" || info.Mode()&0o111 != 0 {
				return full
			}
		}
	}
	return ""
}

// Language states, `VERBATIM` — the renderer matches them.
const (
	StateMapped      = "mapped"
	StatePartial     = "partial"
	StateMissingTool = "missing_tool"
	StateAbsent      = "absent"
	StateFailed      = "failed"
)

// Status is what a build produced and what it could not do.
type Status struct {
	RootPath   string           `json:"root_path"`
	Nodes      int              `json:"nodes"`
	Edges      int              `json:"edges"`
	Languages  []LanguageStatus `json:"languages"`
	Notes      []string         `json:"notes"`
	BuiltAt    string           `json:"built_at"`
	DurationMs int64            `json:"duration_ms"`
	// Archify is the version of the Archify install a flow can be exported to, "" when there is
	// none — the view offers the export only then (CODEMAP-017).
	Archify string `json:"archify"`
}

// LanguageStatus is one language's share of the map.
type LanguageStatus struct {
	Lang    string `json:"lang"`
	State   string `json:"state"`
	Files   int    `json:"files"`
	Symbols int    `json:"symbols"`
	Detail  string `json:"detail"`
}

// Index is one repository's map, rebuilt half by half as its files change. Safe for concurrent
// use; a Graph it returns is never mutated afterwards.
type Index struct {
	root  string
	tools func() Tools

	mu      sync.Mutex
	graph   *Graph
	status  Status
	checked time.Time

	goPrint, tsPrint string
	goSide           *goHalf
	goErr            error
	tsSide           *tsExtract
	tsErr            error
	tables           tableSet
}

// Graph answers the repository's current map, rebuilding whichever half changed since the last
// question, and reports each stage of a rebuild to emit.
func (ix *Index) Graph(ctx context.Context, emit func(stage string)) (*Graph, Status, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.graph != nil && time.Since(ix.checked) < recheckEvery {
		return ix.graph, ix.status, nil
	}

	started := time.Now()
	emit(StageScanning)
	scan, err := scanRepository(ix.root)
	if err != nil {
		return nil, Status{}, err
	}
	ix.checked = time.Now()
	goStale := ix.graph == nil || scan.goPrint != ix.goPrint
	tsStale := ix.graph == nil || scan.tsPrint != ix.tsPrint
	if !goStale && !tsStale {
		return ix.graph, ix.status, nil
	}

	tools := ix.tools()
	if goStale {
		emit(StageGo)
		tables, err := replaySchema(ix.root, scan.sqlFiles)
		if err != nil {
			return nil, Status{}, err
		}
		ix.tables = tables
		ix.goSide, ix.goErr = nil, nil
		if len(scan.goModules) > 0 {
			ix.goSide, ix.goErr = indexGo(ctx, ix.root, scan.goModules, goLister{binary: tools.Go, path: tools.Path})
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, Status{}, ctxErr
		}
		ix.goPrint = scan.goPrint
	}
	if tsStale {
		emit(StageTypeScript)
		ix.tsSide, ix.tsErr = nil, nil
		if len(scan.tsconfigs) > 0 {
			ix.tsSide, ix.tsErr = tsRunner{node: tools.Node, path: tools.Path}.extract(ctx, ix.root, scan.tsconfigs)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, Status{}, ctxErr
		}
		ix.tsPrint = scan.tsPrint
	}

	emit(StageLinking)
	ix.graph = assemble(ix.tables, ix.goSide, ix.tsSide, repoMeta(ix.root, scan))
	ix.status = ix.describe(scan, time.Since(started))
	emit(StageReady)
	return ix.graph, ix.status, nil
}

// describe says, per language, what the build mapped and why anything was left out.
func (ix *Index) describe(scan repoScan, took time.Duration) Status {
	status := Status{
		RootPath:   ix.root,
		Nodes:      len(ix.graph.Nodes),
		Edges:      ix.graph.EdgeCount(),
		Languages:  make([]LanguageStatus, 0, 3),
		Notes:      make([]string, 0, 4),
		BuiltAt:    time.Now().UTC().Format(time.RFC3339),
		DurationMs: took.Milliseconds(),
	}
	symbols := map[Lang]int{}
	for _, n := range ix.graph.Nodes {
		symbols[n.Lang]++
	}

	goStatus := LanguageStatus{Lang: LangGo.String(), Files: scan.goFiles, Symbols: symbols[LangGo]}
	switch {
	case len(scan.goModules) == 0:
		goStatus.State = StateAbsent
	case errors.Is(ix.goErr, errNoGo):
		goStatus.State, goStatus.Detail = StateMissingTool, ix.goErr.Error()
	case ix.goErr != nil:
		goStatus.State, goStatus.Detail = StateFailed, ix.goErr.Error()
	case len(ix.goSide.notes) > 0:
		goStatus.State = StatePartial
		status.Notes = append(status.Notes, ix.goSide.notes...)
	default:
		goStatus.State = StateMapped
	}

	tsStatus := LanguageStatus{Lang: LangTS.String(), Files: scan.tsFiles, Symbols: symbols[LangTS]}
	switch {
	case len(scan.tsconfigs) == 0:
		tsStatus.State = StateAbsent
	case errors.Is(ix.tsErr, errNoNode):
		tsStatus.State, tsStatus.Detail = StateMissingTool, ix.tsErr.Error()
	case ix.tsErr != nil:
		tsStatus.State, tsStatus.Detail = StateFailed, ix.tsErr.Error()
	default:
		tsStatus.State = StateMapped
		versions := make([]string, 0, len(ix.tsSide.Projects))
		for _, project := range ix.tsSide.Projects {
			if project.Note != "" {
				tsStatus.State = StatePartial
				status.Notes = append(status.Notes, fmt.Sprintf("TypeScript (%s): %s", project.TSConfig, project.Note))
				continue
			}
			if version := "TypeScript " + project.TypeScript + " (" + project.API + ")"; !slices.Contains(versions, version) {
				versions = append(versions, version)
			}
		}
		if len(versions) > 0 {
			tsStatus.Detail = versions[0]
		}
	}

	sqlStatus := LanguageStatus{Lang: LangSQL.String(), Files: len(scan.sqlFiles), Symbols: symbols[LangSQL], State: StateMapped}
	if symbols[LangSQL] == 0 {
		sqlStatus.State = StateAbsent
	}

	status.Languages = append(status.Languages, goStatus, tsStatus, sqlStatus)
	if cli, err := archify.Find(ix.root); err == nil {
		status.Archify = cli.Version
	}
	return status
}

// Service keeps one Index per repository the renderer has asked about.
type Service struct {
	tools   func() Tools
	emitter bridge.Emitter

	mu      sync.Mutex
	indexes map[string]*Index
	recent  []string
}

// NewService builds the service. A nil emitter drops progress events.
func NewService(tools func() Tools, emitter bridge.Emitter) *Service {
	if tools == nil {
		tools = DiscoverTools
	}
	if emitter == nil {
		emitter = bridge.NopEmitter{}
	}
	return &Service{tools: tools, emitter: emitter, indexes: map[string]*Index{}}
}

// ErrNotADirectory refuses a root that is not a folder on this machine.
var ErrNotADirectory = errors.New("the repository folder does not exist") //nolint:staticcheck // ST1005: shown to the user as a sentence

// Graph answers the map of the repository at rootPath.
//
// The status and the progress events name the repository by rootPath exactly as it was sent: the
// renderer matches them against the path it holds, which need not be the resolved one.
func (s *Service) Graph(ctx context.Context, rootPath string) (*Graph, Status, error) {
	ix, err := s.index(rootPath)
	if err != nil {
		return nil, Status{}, err
	}
	g, status, err := ix.Graph(ctx, func(stage string) {
		s.emitter.Emit(EventProgress, ProgressEvent{RootPath: rootPath, Stage: stage})
	})
	status.RootPath = rootPath
	return g, status, err
}

func (s *Service) index(rootPath string) (*Index, error) {
	absolute, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", rootPath, err)
	}
	if info, err := os.Stat(absolute); err != nil || !info.IsDir() {
		return nil, ErrNotADirectory
	}
	// The real path: the go command reports files by it, and a repository opened through a symbolic
	// link — macOS's /var is one — would otherwise have every file fall outside its own root.
	root, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", rootPath, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.recent = append(slices.DeleteFunc(s.recent, func(r string) bool { return r == root }), root)
	if ix, ok := s.indexes[root]; ok {
		return ix, nil
	}
	if len(s.recent) > maxIndexes {
		delete(s.indexes, s.recent[0])
		s.recent = s.recent[1:]
	}
	ix := &Index{root: root, tools: s.tools}
	s.indexes[root] = ix
	return ix, nil
}
