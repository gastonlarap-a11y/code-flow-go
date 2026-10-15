package repomap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
)

// Keeping each open repository's map current (REPOMAP-008).

const (
	// recheckEvery bounds how often a map looks for changed files. Questions arrive in bursts — a
	// search box asks on every keystroke — and one look serves the whole burst.
	recheckEvery = 2 * time.Second
	// saveEvery bounds how often a changed map is written to disk; Flush writes it at once.
	saveEvery = 15 * time.Second
	// maxMaps is how many repositories keep a map in memory; the least recently asked about is
	// dropped first, its cache written, and read back from disk if it is asked about again.
	maxMaps = 4
	// maxWorkers bounds the files parsed at once.
	maxWorkers = 8
)

// ErrNotADirectory is a root that is not a folder on this machine.
var ErrNotADirectory = errors.New("not a folder on this machine") //nolint:staticcheck // ST1005: shown to the user as a sentence

// Service keeps one map per repository the renderer or an AI run has asked about.
type Service struct {
	// cacheDir holds the maps between runs; "" keeps none.
	cacheDir string
	// readOnly reads the cache and never writes it: the MCP server, which is another process and
	// writes nothing of the user's (CODEMAP-015).
	readOnly bool
	// recheck is recheckEvery, which a test shortens to see a change at once.
	recheck time.Duration

	mu     sync.Mutex
	maps   map[string]*repoMap
	recent []string
}

// NewService keeps maps under cacheDir.
func NewService(cacheDir string) *Service {
	return &Service{cacheDir: cacheDir, recheck: recheckEvery, maps: make(map[string]*repoMap)}
}

// NewReadOnlyService reads maps under cacheDir and never writes one.
func NewReadOnlyService(cacheDir string) *Service {
	s := NewService(cacheDir)
	s.readOnly = true
	return s
}

// Snapshot is the repository's map as it is now, reading only the files that changed since it was
// last asked about.
func (s *Service) Snapshot(ctx context.Context, rootPath string) (*Snapshot, Build, error) {
	m, err := s.mapFor(rootPath)
	if err != nil {
		return nil, Build{}, err
	}
	return m.refresh(ctx)
}

// Flush writes a repository's map to disk now, if anything in it changed since the last write — so
// another process reading the cache sees what this one knows.
func (s *Service) Flush(rootPath string) error {
	m, err := s.mapFor(rootPath)
	if err != nil {
		return err
	}
	return m.flush()
}

func (s *Service) mapFor(rootPath string) (*repoMap, error) {
	root, err := resolveRoot(rootPath)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	m, ok := s.maps[root]
	if !ok {
		m = &repoMap{root: root, readOnly: s.readOnly, recheck: s.recheck}
		if s.cacheDir != "" {
			m.cacheFile = cachePath(s.cacheDir, root)
		}
		s.maps[root] = m
	}
	s.recent = append(slices.DeleteFunc(s.recent, func(r string) bool { return r == root }), root)
	var evicted *repoMap
	if len(s.recent) > maxMaps {
		evicted = s.maps[s.recent[0]]
		delete(s.maps, s.recent[0])
		s.recent = s.recent[1:]
	}
	s.mu.Unlock()
	if evicted != nil {
		// Losing what was read is only a slower next time; the map being asked about must not fail
		// for it.
		_ = evicted.flush()
	}
	return m, nil
}

func resolveRoot(rootPath string) (string, error) {
	if strings.TrimSpace(rootPath) == "" {
		return "", ErrNotADirectory
	}
	absolute, err := filepath.Abs(rootPath)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", rootPath, err)
	}
	if info, err := os.Stat(absolute); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: %s", ErrNotADirectory, rootPath)
	}
	real, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", rootPath, err)
	}
	return real, nil
}

// repoMap is one repository's map and what it was built from.
type repoMap struct {
	root      string
	cacheFile string
	readOnly  bool
	recheck   time.Duration

	mu        sync.Mutex
	loaded    bool
	checked   time.Time
	files     map[string]seenFile
	facts     map[string]*Facts
	manifests string // the manifests' paths, sizes and times: a change rebuilds the snapshot
	snap      *Snapshot
	last      Build
	dirty     bool
	savedAt   time.Time
}

// job is one file to read.
type job struct {
	path string
	lang Lang
	seen seenFile
}

// done is what reading one file gave.
type done struct {
	job
	facts *Facts // nil for a file that turned out not to be code
	fresh bool   // parsed now rather than found under its content's sum
}

func (m *repoMap) refresh(ctx context.Context) (*Snapshot, Build, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.snap != nil && time.Since(m.checked) < m.recheck {
		return m.snap, m.cached(), nil
	}
	m.load()
	started := time.Now()

	found, err := listCodeFiles(ctx, m.root)
	if err != nil {
		return nil, Build{}, err
	}

	next := make(map[string]seenFile, len(found.files))
	rows := make([]fileRow, 0, len(found.files))
	jobs := make([]job, 0, 64)
	for _, f := range found.files {
		info, err := os.Stat(filepath.Join(m.root, filepath.FromSlash(f.path)))
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileBytes {
			continue
		}
		seen := seenFile{Size: info.Size(), Mod: info.ModTime().UnixNano()}
		if prev, ok := m.files[f.path]; ok && prev.Size == seen.Size && prev.Mod == seen.Mod {
			if prev.Sum == "" {
				next[f.path] = prev // not code, and not changed since it was found not to be
				continue
			}
			if facts := m.facts[prev.Sum]; facts != nil && facts.Lang == f.lang {
				next[f.path] = prev
				rows = append(rows, fileRow{Path: f.path, Facts: facts})
				continue
			}
		}
		jobs = append(jobs, job{path: f.path, lang: f.lang, seen: seen})
	}

	results, err := m.read(ctx, jobs)
	if err != nil {
		return nil, Build{}, err
	}
	parsed := 0
	for _, r := range results {
		next[r.path] = r.seen
		if r.facts == nil {
			continue
		}
		m.facts[r.seen.Sum] = r.facts
		rows = append(rows, fileRow{Path: r.path, Facts: r.facts})
		if r.fresh {
			parsed++
		}
	}
	slices.SortFunc(rows, func(a, b fileRow) int { return strings.Compare(a.Path, b.Path) })

	manifestKey, contents := m.readManifests(found.manifests)
	changed := m.snap == nil || len(jobs) > 0 || len(next) != len(m.files) || manifestKey != m.manifests
	if !changed {
		for path := range next {
			if _, ok := m.files[path]; !ok {
				changed = true
				break
			}
		}
	}
	if changed {
		m.snap = buildSnapshot(rows, contents, found.truncated)
		m.dirty = true
	}
	m.files, m.manifests = next, manifestKey
	m.prune()
	m.checked = time.Now()
	m.last = Build{
		Files:     len(rows),
		Parsed:    parsed,
		Millis:    time.Since(started).Milliseconds(),
		Grammars:  GrammarsLinked,
		Truncated: found.truncated,
		Git:       found.git,
	}
	if m.dirty && time.Since(m.savedAt) >= saveEvery {
		_ = m.save() // a map that could not be kept is only a slower next start
	}
	return m.snap, m.last, nil
}

// cached is the last build, as an answer that read nothing new.
func (m *repoMap) cached() Build {
	b := m.last
	b.Parsed, b.Millis = 0, 0
	return b
}

// read parses the jobs on up to maxWorkers goroutines, each with its own parser.
//
// A file whose parse panics is reported and recorded as not code, so it is not read again until it
// changes; its worker starts a new parser and carries on, which is what keeps the jobs still being
// handed out from waiting on a worker that is gone.
func (m *repoMap) read(ctx context.Context, jobs []job) ([]done, error) {
	results := make([]done, len(jobs))
	if len(jobs) == 0 {
		return results, nil
	}
	workers := min(maxWorkers, runtime.NumCPU(), len(jobs))
	next := make(chan int)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		safego.Go("repomap.parse."+strconv.Itoa(w), func() {
			defer wg.Done()
			p := newParser()
			defer func() { p.close() }()
			for i := range next {
				if !safego.Do("repomap.parse "+jobs[i].path, func() { results[i] = m.readOne(p, jobs[i]) }) {
					results[i] = done{job: jobs[i]}
					p.close()
					p = newParser()
				}
			}
		})
	}
	var cancelled error
	for i := range jobs {
		if err := ctx.Err(); err != nil {
			cancelled = err
			break
		}
		next <- i
	}
	close(next)
	wg.Wait()
	if cancelled != nil {
		return nil, cancelled
	}
	return results, nil
}

// readOne reads and parses one file, or finds its facts under its content's sum.
func (m *repoMap) readOne(p parser, j job) done {
	result := done{job: j}
	content, err := os.ReadFile(filepath.Join(m.root, filepath.FromSlash(j.path)))
	if err != nil || !readable(j.path, content) {
		return result // seen, not code: Sum stays ""
	}
	sum := sha256.Sum256(content)
	result.seen.Sum = hex.EncodeToString(sum[:])
	if known := m.facts[result.seen.Sum]; known != nil && known.Lang == j.lang {
		result.facts = known
		return result
	}
	result.facts = extract(p, j.lang, content)
	result.fresh = true
	return result
}

// readManifests reads the manifests the resolver needs, and a key that changes when any of them does.
func (m *repoMap) readManifests(paths []string) (string, manifests) {
	contents := make(manifests, len(paths))
	var key strings.Builder
	for _, p := range paths {
		full := filepath.Join(m.root, filepath.FromSlash(p))
		info, err := os.Stat(full)
		if err != nil || info.Size() > maxManifestBytes {
			continue
		}
		content, err := os.ReadFile(full) //nolint:gosec // G304: a manifest inside the repository being mapped
		if err != nil {
			continue
		}
		contents[p] = content
		fmt.Fprintf(&key, "%s:%d:%d;", p, info.Size(), info.ModTime().UnixNano())
	}
	return key.String(), contents
}

// prune drops facts no file points at any more.
func (m *repoMap) prune() {
	used := make(map[string]bool, len(m.files))
	for _, f := range m.files {
		used[f.Sum] = true
	}
	for sum := range m.facts {
		if !used[sum] {
			delete(m.facts, sum)
		}
	}
}

// load reads the map kept on disk, once.
func (m *repoMap) load() {
	if m.loaded {
		return
	}
	m.loaded = true
	m.files, m.facts = make(map[string]seenFile), make(map[string]*Facts)
	if m.cacheFile == "" {
		return
	}
	if doc, ok := loadCache(m.cacheFile); ok {
		m.files, m.facts = doc.Files, doc.Facts
	}
}

func (m *repoMap) flush() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.dirty {
		return nil
	}
	return m.save()
}

// save writes the map; the caller holds m.mu.
func (m *repoMap) save() error {
	if m.readOnly || m.cacheFile == "" {
		return nil
	}
	err := saveCache(m.cacheFile, cacheDoc{Format: cacheFormat, Extractor: extractorID, Files: m.files, Facts: m.facts})
	if err != nil {
		return err
	}
	m.dirty, m.savedAt = false, time.Now()
	return nil
}
