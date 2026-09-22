package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

/*
Reading what the agent CLIs write about their own work (USAGE-002).

Claude Code and Codex both keep a JSONL transcript of every session, and every assistant turn in it
carries the tokens that turn cost. That file is the only place a subscription's consumption is
readable at all: neither CLI offers a command for it, and Anthropic closed the request for one as
not planned.

**Two rules govern this file and neither is negotiable.**

Privacy first. Those transcripts contain entire conversations — the user's code, their prompts, the
answers. This reader lifts out three things, when a turn happened, which model answered and what it
cost, and never touches, keeps or logs anything else. `Event` has nowhere to put content even by
accident, which is the point of it having three fields.

Then cost. There can be thousands of these files and they only grow. Reading them all on every poll
would make the indicator more expensive than the thing it measures, so a file untouched since the
start of the window being asked about is never opened, and a file whose size and modification time
have not moved since the last sweep is answered from cache.

**This depends on a format neither CLI promises to keep.** If a layout changes, the reader stops
finding turns and the provider reports as unmeasured — which is the required failure, because a
number that quietly stops moving is worse than one that says it is gone (USAGE-004).
*/

// maxLineBytes caps one JSONL line. A transcript line holds a whole assistant turn, so the ceiling
// is high — but it is a ceiling, because a corrupt file must not be read into memory unbounded.
const maxLineBytes = 8 << 20

// scanLimit bounds a single sweep. A tree larger than this is somebody's archive, not their current
// week, and the windows reported on only reach back seven days anyway.
const scanLimit = 5000

// Source is one provider's transcript tree and how to read a file of it.
type Source struct {
	// Provider is the id this app knows the engine by: "claude", "codex".
	Provider string
	// Root is the directory the transcripts live under.
	Root string
	// parseFile lifts one whole transcript into events. Per file rather than per line because
	// Codex names its model once, in a header line, and every later turn belongs to it.
	parseFile func(*bufio.Scanner) []Event
}

// ClaudeSource reads Claude Code's transcripts.
//
// `~/.claude/projects/<slug>/<session>.jsonl`, one JSON object per line. The lines that matter are
// assistant turns: `message.model` names the model and `message.usage` holds the counts, per turn.
func ClaudeSource(home string) Source {
	return Source{
		Provider:  "claude",
		Root:      filepath.Join(home, ".claude", "projects"),
		parseFile: parseClaudeFile,
	}
}

// CodexSource reads Codex's transcripts.
//
// `~/.codex/sessions/<date parts>/<session>.jsonl`. Two differences from Claude's shape, both
// verified against real files: the per-turn cost is `payload.info.last_token_usage` (the running
// total sits beside it and is not what a window wants), and the model is declared once in the
// `session_meta` header rather than on every turn.
func CodexSource(home string) Source {
	return Source{
		Provider:  "codex",
		Root:      filepath.Join(home, ".codex", "sessions"),
		parseFile: parseCodexFile,
	}
}

// claudeLine is the shape this reader cares about, and deliberately the whole of it: anything a
// transcript carries that is not named here is never decoded.
type claudeLine struct {
	Timestamp string `json:"timestamp"`
	Message   struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens         int64 `json:"input_tokens"`
			OutputTokens        int64 `json:"output_tokens"`
			CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadTokens     int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

func parseClaudeFile(scanner *bufio.Scanner) []Event {
	events := []Event{}

	for scanner.Scan() {
		var line claudeLine
		// A line that does not parse is skipped in silence: the tail of a live session is half
		// written more often than not, and that is normal rather than corruption.
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}

		at, err := time.Parse(time.RFC3339, line.Timestamp)
		if err != nil {
			continue
		}

		// Cache reads count. They are cheaper than fresh input but they are not free, and a window
		// that ignored them would under-report a long session by most of its weight.
		spend := line.Message.Usage
		total := spend.InputTokens + spend.OutputTokens + spend.CacheCreationTokens + spend.CacheReadTokens
		if total == 0 {
			continue
		}

		events = append(events, Event{At: at, Model: line.Message.Model, Tokens: total})
	}

	return events
}

// codexLine covers both line kinds this reader needs: the `session_meta` header that names the
// model, and the `token_count` turns that carry the cost.
type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		BaseInstructions struct {
			Provenance struct {
				Model string `json:"model"`
			} `json:"provenance"`
		} `json:"base_instructions"`
		Info struct {
			LastTokenUsage struct {
				TotalTokens int64 `json:"total_tokens"`
			} `json:"last_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

func parseCodexFile(scanner *bufio.Scanner) []Event {
	events := []Event{}
	model := ""

	for scanner.Scan() {
		var line codexLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}

		// The header names the session's model, and every turn after it belongs to that model.
		if line.Type == "session_meta" {
			if named := line.Payload.BaseInstructions.Provenance.Model; named != "" {
				model = named
			}
			continue
		}

		total := line.Payload.Info.LastTokenUsage.TotalTokens
		if total == 0 {
			continue
		}

		at, err := time.Parse(time.RFC3339, line.Timestamp)
		if err != nil {
			continue
		}

		events = append(events, Event{At: at, Model: model, Tokens: total})
	}

	// The header can arrive after the first turns in a file being appended to out of order; naming
	// them afterwards is cheaper than reading the file twice.
	if model != "" {
		for i := range events {
			if events[i].Model == "" {
				events[i].Model = model
			}
		}
	}

	return events
}

// cached is what a previous sweep learned about one file, kept so an untouched file is not reopened.
type cached struct {
	size    int64
	modTime time.Time
	events  []Event
}

// Reader sweeps transcript trees and answers the events inside a stretch of time.
//
// Not safe for concurrent use; the service that owns one calls it under its own lock.
type Reader struct {
	sources []Source
	cache   map[string]cached
}

func NewReader(sources ...Source) *Reader {
	return &Reader{sources: sources, cache: map[string]cached{}}
}

// Providers names what this reader can measure, in the order it was given them.
func (r *Reader) Providers() []string {
	names := make([]string, 0, len(r.sources))
	for _, source := range r.sources {
		names = append(names, source.Provider)
	}
	return names
}

/*
Events answers everything one provider spent at or after `since`.

A file whose last write predates `since` is skipped without opening: its last write is its last
turn, so it cannot hold an event inside the window. That is what keeps a sweep proportional to
recent work rather than to the size of the archive.

An unreadable file is skipped, not fatal. One corrupt transcript must not cost the user every
number on the panel. A provider with no tree at all answers no events and no error — never having
run a CLI is an answer, not a failure.
*/
func (r *Reader) Events(ctx context.Context, provider string, since time.Time) ([]Event, error) {
	source, found := r.sourceFor(provider)
	if !found {
		return []Event{}, nil
	}

	if info, err := os.Stat(source.Root); err != nil || !info.IsDir() {
		return []Event{}, nil
	}

	events := []Event{}
	scanned := 0

	err := filepath.WalkDir(source.Root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // an unreadable directory is skipped, not fatal
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		if scanned >= scanLimit {
			return fs.SkipAll
		}
		scanned++

		stat, statErr := entry.Info()
		if statErr != nil || stat.ModTime().Before(since) {
			return nil
		}

		events = append(events, r.fileEvents(source, path, stat, since)...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return events, nil
}

// fileEvents answers one file's events inside the window, from cache when the file has not moved.
func (r *Reader) fileEvents(source Source, path string, stat fs.FileInfo, since time.Time) []Event {
	if hit, ok := r.cache[path]; ok && hit.size == stat.Size() && hit.modTime.Equal(stat.ModTime()) {
		return within(hit.events, since)
	}

	events := readFile(source, path)
	r.cache[path] = cached{size: stat.Size(), modTime: stat.ModTime(), events: events}
	return within(events, since)
}

func readFile(source Source, path string) []Event {
	file, err := os.Open(path) //nolint:gosec // a path this reader walked to, under the user's own home
	if err != nil {
		return []Event{}
	}
	defer func() { _ = file.Close() }() // read-only: nothing to report on close

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	return source.parseFile(scanner)
}

// within narrows a file's cached events to the window being asked about.
func within(events []Event, since time.Time) []Event {
	kept := make([]Event, 0, len(events))
	for _, event := range events {
		if !event.At.Before(since) {
			kept = append(kept, event)
		}
	}
	return kept
}

func (r *Reader) sourceFor(provider string) (Source, bool) {
	for _, source := range r.sources {
		if source.Provider == provider {
			return source, true
		}
	}
	return Source{}, false
}
