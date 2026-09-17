package files

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
	"github.com/syncthing/notify"
)

// The working-tree watcher (FILE-012, FILE-013).
//
// **This is not a debounce, and reimplementing it as one is a regression rather than a
// simplification** (DIVERGENCE-FILE-b). It is a leading-edge throttle with a trailing catch-up: the
// first event of a burst emits immediately, anything within the next 400 ms marks a change as
// pending instead of being dropped, and the next poll tick flushes that pending change once the
// window has actually elapsed.
//
// The bug that shape exists to prevent is specific and was real: a plain leading-edge throttle
// (emit, then ignore for 400 ms, and nothing afterwards) silently lost whatever landed inside the
// window when no later event arrived to wake it up — which is exactly what an AI agent writing four
// files in a row produces. Everything after the first write vanished from the tree until something
// unrelated forced a reload.
//
// A fixed-window debounce (schedule on the first event, reset on every event, fire once after
// quiet) reintroduces it. The poll-plus-pending-flag design guarantees a flush within roughly
// 600 ms of the *first* event of a burst, whether or not any later event ever arrives.

const (
	// pollInterval is how often the loop wakes even with nothing happening.
	pollInterval = 200 * time.Millisecond
	// minEmitInterval is the floor between two emissions.
	minEmitInterval = 400 * time.Millisecond
	// eventBuffer is the channel the OS watcher writes into.
	//
	// Generous on purpose: a burst on a large repository arrives faster than any consumer drains
	// it, and this library drops events when the channel is full rather than blocking. Dropping is
	// survivable here in a way it would not be elsewhere — the payload carries no information
	// beyond "something changed", so losing one of a thousand identical signals costs nothing as
	// long as at least one arrives, and the throttle below guarantees one does. That is also this
	// port's answer to the Windows buffer-overflow case 2.x handled explicitly: the library does
	// not surface it as an event, and the design no longer needs it to.
	eventBuffer = 1024
)

// WatcherRegistry owns one watcher per repository path.
type WatcherRegistry struct {
	emitter bridge.Emitter

	mu       sync.Mutex
	watchers map[string]*repoWatcher
}

type repoWatcher struct {
	events chan notify.EventInfo
	stop   chan struct{}
	done   chan struct{}
	// root is the canonical form of the watched path, for comparing against event paths. It is
	// **not** what gets emitted: the renderer keys its refresh on the path it asked for.
	root string
}

// NewWatcherRegistry builds the registry. The emitter is the only thing it needs from outside.
func NewWatcherRegistry(emitter bridge.Emitter) *WatcherRegistry {
	if emitter == nil {
		emitter = bridge.NopEmitter{}
	}
	return &WatcherRegistry{emitter: emitter, watchers: make(map[string]*repoWatcher, 2)}
}

// Start begins watching a repository, replacing any watch already running for it.
//
// Restarting rather than refusing or duplicating: the renderer calls this whenever the active
// project changes, and a second watcher on the same path would double every event.
func (r *WatcherRegistry) Start(repoPath string) error {
	r.Stop(repoPath)

	events := make(chan notify.EventInfo, eventBuffer)

	// `...` is this library's recursive form, and recursion is the whole reason for choosing it:
	// a per-directory watcher opens a descriptor per directory on macOS and runs out on a real
	// repository.
	if err := notify.Watch(filepath.Join(repoPath, "..."), events, notify.All); err != nil {
		return err
	}

	// The event paths arrive symlink-resolved while the renderer's path is not: on macOS every
	// temporary directory and many home directories live under /var, which is a symlink to
	// /private/var, so a raw comparison against the watched root never matches. Resolving it here
	// is what makes the root-event filter below actually fire.
	root, err := canonical(repoPath)
	if err != nil {
		root = repoPath
	}

	watcher := &repoWatcher{
		events: events,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
		root:   root,
	}

	r.mu.Lock()
	r.watchers[repoPath] = watcher
	r.mu.Unlock()

	safego.Go("repo-watcher", func() { r.loop(repoPath, watcher) })
	return nil
}

// Stop ends a repository's watch. Stopping one that is not running is not an error — the renderer
// stops watching on unmount, which can happen after a failed start.
func (r *WatcherRegistry) Stop(repoPath string) {
	r.mu.Lock()
	watcher, found := r.watchers[repoPath]
	delete(r.watchers, repoPath)
	r.mu.Unlock()

	if !found {
		return
	}
	close(watcher.stop)
	<-watcher.done
	notify.Stop(watcher.events)
}

// StopAll ends every watch, for shutdown.
func (r *WatcherRegistry) StopAll() {
	r.mu.Lock()
	paths := make([]string, 0, len(r.watchers))
	for path := range r.watchers {
		paths = append(paths, path)
	}
	r.mu.Unlock()

	for _, path := range paths {
		r.Stop(path)
	}
}

// loop is the throttle.
func (r *WatcherRegistry) loop(repoPath string, watcher *repoWatcher) {
	defer close(watcher.done)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	pending := false
	// Far enough in the past that the first qualifying event emits immediately rather than waiting
	// out an initial window — the leading edge.
	lastEmit := time.Now().Add(-time.Hour)

	flush := func() {
		if !pending || time.Since(lastEmit) < minEmitInterval {
			return
		}
		pending = false
		lastEmit = time.Now()
		r.emitter.Emit("repo:fs-changed", map[string]string{"repo_path": repoPath})
	}

	for {
		select {
		case <-watcher.stop:
			return

		case event, ok := <-watcher.events:
			if !ok {
				return
			}
			if !isNoise(watcher.root, event.Path()) {
				pending = true
			}
			flush()

		case <-ticker.C:
			// The catch-up. Nothing new arrived, but something may still be waiting from a burst
			// that went quiet inside the window.
			flush()
		}
	}
}

// isNoise reports whether a path is git's own bookkeeping rather than the user's work (FILE-013).
//
// git rewrites these constantly during ordinary operations — index locks, fetch and commit
// bookkeeping — and refreshing on them means refreshing on git's internal churn rather than on
// anything the user did. A status read alone would trigger a refresh, which triggers a status read.
//
// The first two checks are **DIVERGENCE-FILE-e**, added here because the three names alone do not
// hold on this platform. Measured: FSEvents delivers a directory-level event for `.git` and for the
// watched root *alongside* each file event, and those two pass a filter that only looks at the
// three names — so every `git status` the app itself runs would wake the loop the rule exists to
// prevent. Discarding them loses nothing: a change inside either directory always arrives as its
// own event too, which is exactly why the pair is redundant.
// root is the canonical, symlink-resolved watched path — event paths arrive in that form, and
// comparing against the renderer's spelling would never match on macOS.
func isNoise(root, path string) bool {
	if path == root {
		return true
	}

	name := filepath.Base(path)
	if name == ".git" {
		return true
	}
	return strings.HasSuffix(name, ".lock") || name == "FETCH_HEAD" || name == "COMMIT_EDITMSG"
}
