package usage

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

/*
Asking each CLI what it knows about its own limits (USAGE-011).

`/usage` inside a session shows the share of each window that is gone and when it resets. Those come
from the provider; nothing on disk has them. For a while the only apparent route was calling the
same private endpoint with the user's OAuth token — this app reading and transmitting a credential,
which `SEC-007` exists to prevent. It is not the only route. Both CLIs run the command
non-interactively:

	claude -p "/usage" --output-format json
	agy    -p "/usage"

Supported flags, the user's **already established session**, and no credential in this app's hands
at any point.

**Two providers, two formats, two parsers — deliberately not one.** Claude reports what is *used*;
Antigravity reports what *remains*. A single lenient scanner that found "a percentage near a window
word" would read 94% remaining as 94% used and tell somebody they were nearly out when they had
almost everything left. The formats are different enough that pretending otherwise is how that bug
gets written, so each provider gets a parser that knows its own report and refuses anything else.

Neither format is a contract. Unrecognised text yields nothing, the panel falls back to the ceiling
learned by watching (USAGE-006), and a wording change therefore costs a percentage rather than
producing a wrong one.
*/

// limitsTimeout bounds one probe. Antigravity starts a language server before answering, so this is
// more generous than a status read would need.
const limitsTimeout = 90 * time.Second

// limitsTTL is how long a reading is trusted. It spawns a process, so it is asked rarely; the
// five-hour window moves slowly enough that ten minutes of staleness is invisible.
const limitsTTL = 10 * time.Minute

// WindowLimit is what a provider says about one of its windows.
type WindowLimit struct {
	// UsedPercent is the share consumed, always normalised to "used" whatever the report said.
	UsedPercent float64
	// ResetsAt is when the window turns over, nil when the report did not say.
	ResetsAt *time.Time
}

// Limits is a provider's own answer for both windows. A nil member means it said nothing.
type Limits struct {
	Session *WindowLimit
	Week    *WindowLimit
}

func (l Limits) forWindow(kind WindowKind) *WindowLimit {
	if kind == WeekWindow {
		return l.Week
	}
	return l.Session
}

// usageEnvelope is the shape `--output-format json` wraps an answer in. Two fields are read.
type usageEnvelope struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

// reportParser turns one provider's report into limits.
type reportParser func(report string, now time.Time) Limits

// LimitsProbe asks one CLI, and remembers the answer for a while.
type LimitsProbe struct {
	binary string
	args   []string
	// envelope is true when the CLI wraps its answer in `--output-format json`.
	envelope bool
	parse    reportParser

	mu     sync.Mutex
	last   Limits
	lastAt time.Time
	asked  bool
}

// NewLimitsProbe builds the probe for Claude Code.
func NewLimitsProbe(binary string) *LimitsProbe {
	return &LimitsProbe{
		binary:   orDefault(binary, "claude"),
		args:     []string{"-p", "/usage", "--output-format", "json"},
		envelope: true,
		parse:    parseClaudeReport,
	}
}

// NewAntigravityProbe builds the probe for the Antigravity CLI.
//
// Plain text rather than the JSON envelope: its report is already a table, and its `--output-format
// json` wraps a conversational answer this command does not produce.
func NewAntigravityProbe(binary string) *LimitsProbe {
	return &LimitsProbe{
		binary: orDefault(binary, "agy"),
		args:   []string{"-p", "/usage"},
		parse:  parseAntigravityReport,
	}
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// Read answers the provider's own figures, from cache when they are recent enough.
//
// A failure is not an error the caller handles: it answers empty limits, and the panel falls back
// to what it learned by watching. An indicator that cannot ask is not one that should interrupt.
func (p *LimitsProbe) Read(ctx context.Context, now time.Time) Limits {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.asked && now.Sub(p.lastAt) < limitsTTL {
		return p.last
	}

	report, ok := p.run(ctx)
	p.lastAt = now
	p.asked = true
	if !ok {
		// Forgotten rather than kept: a figure from before the window turned over would be shown
		// as though it were current, which is the one failure this feature refuses (USAGE-004).
		p.last = Limits{}
		return p.last
	}

	p.last = p.parse(report, now)
	return p.last
}

func (p *LimitsProbe) run(ctx context.Context) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, limitsTimeout)
	defer cancel()

	// `proc.Command` builds the environment that cannot carry a credential (SEC-007). Nothing here
	// reads one: each CLI uses the session it already has.
	out, err := proc.Command(ctx, p.binary, p.args...).Output()
	if err != nil {
		return "", false
	}

	if !p.envelope {
		return string(out), len(out) > 0
	}

	var wrapped usageEnvelope
	if err := json.Unmarshal(out, &wrapped); err != nil || wrapped.IsError {
		return "", false
	}
	return wrapped.Result, wrapped.Result != ""
}

// ---- Claude Code --------------------------------------------------------------------------

/*
claudeLimitLine matches the shape Claude Code prints, one window per line:

	Current session: 9% used · resets Sep 22 at 1pm (America/Santiago)
	Current week (all models): 61% used · resets Sep 23 at 12am (America/Santiago)
	Current week (Fable): 0% used · resets Sep 23 at 12am (America/Santiago)

The `used` keyword is required, which is what keeps the contributing-behaviours block below it —
"95% of your usage was at >150k context" — from being read as a limit.
*/
var claudeLimitLine = regexp.MustCompile(
	`(?i)^current\s+(session|week)([^:]*):\s*(\d{1,3}(?:\.\d+)?)\s*%\s*used(?:\s*·\s*resets\s+(.+?))?\s*$`)

// ParseLimitsReport exposes Claude's parser under the name its test calls it by. The parsers are
// the pieces most likely to need adjusting when a report's wording moves, so they are exercised
// directly rather than through a subprocess.
func ParseLimitsReport(report string, now time.Time) Limits {
	return parseClaudeReport(report, now)
}

func parseClaudeReport(report string, now time.Time) Limits {
	limits := Limits{}

	for _, raw := range strings.Split(report, "\n") {
		match := claudeLimitLine.FindStringSubmatch(strings.TrimSpace(raw))
		if match == nil {
			continue
		}

		used, err := strconv.ParseFloat(match[3], 64)
		if err != nil || used < 0 || used > 100 {
			continue
		}

		limit := &WindowLimit{UsedPercent: used, ResetsAt: parseClaudeReset(match[4], now)}

		if strings.EqualFold(match[1], "session") {
			if limits.Session == nil {
				limits.Session = limit
			}
			continue
		}

		// The week is reported once for all models and again per model. The overall figure is the
		// one that decides whether somebody is cut off, so it wins wherever it appears in the list
		// rather than whichever line happens to come first.
		qualifier := strings.ToLower(match[2])
		if limits.Week == nil || strings.Contains(qualifier, "all models") {
			limits.Week = limit
		}
	}

	return limits
}

// claudeReset matches "Sep 22 at 1pm (America/Santiago)" and its half-hour variants.
var claudeReset = regexp.MustCompile(
	`(?i)^([A-Za-z]{3,9})\s+(\d{1,2})\s+at\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)\s*\(([^)]+)\)`)

/*
parseClaudeReset turns the reported reset into an instant, or nil when it cannot.

The report names no year, so the current one is assumed and a date that lands more than a month
behind `now` is read as next year's — which is what a window resetting on 2 January looks like on
31 December.
*/
func parseClaudeReset(text string, now time.Time) *time.Time {
	match := claudeReset.FindStringSubmatch(strings.TrimSpace(text))
	if match == nil {
		return nil
	}

	month, ok := monthByName(match[1])
	if !ok {
		return nil
	}

	day, _ := strconv.Atoi(match[2])
	hour, _ := strconv.Atoi(match[3])
	minute := 0
	if match[4] != "" {
		minute, _ = strconv.Atoi(match[4])
	}

	switch {
	case strings.EqualFold(match[5], "pm") && hour != 12:
		hour += 12
	case strings.EqualFold(match[5], "am") && hour == 12:
		hour = 0
	}

	location, err := time.LoadLocation(strings.TrimSpace(match[6]))
	if err != nil {
		return nil
	}

	at := time.Date(now.Year(), month, day, hour, minute, 0, 0, location)
	if at.Before(now.Add(-30 * 24 * time.Hour)) {
		at = at.AddDate(1, 0, 0)
	}
	return &at
}

func monthByName(name string) (time.Month, bool) {
	for month := time.January; month <= time.December; month++ {
		full := month.String()
		if strings.EqualFold(name, full) || strings.EqualFold(name, full[:3]) {
			return month, true
		}
	}
	return 0, false
}

// ---- Antigravity --------------------------------------------------------------------------

/*
antigravityLimitLine matches its tab-separated table:

	Gemini Models	Weekly Limit Remaining	100%	2026-09-29T11:34:17Z
	Claude and GPT models	Five Hour Limit Remaining	94%	2026-09-22T16:13:50Z

Two things differ from Claude's report and both matter. The percentage is what **remains**, so it is
inverted here rather than anywhere later — a "remaining" figure that reached the panel unconverted
would say somebody was nearly out when they had almost everything left. And the reset is already an
instant, so nothing has to be inferred about it.
*/
var antigravityLimitLine = regexp.MustCompile(
	`(?i)^(.*?)\t\s*(weekly|five\s*hour)\s+limit\s+remaining\s*\t\s*(\d{1,3}(?:\.\d+)?)\s*%\s*\t\s*(\S+)\s*$`)

// ParseAntigravityReport exposes the parser for its test.
func ParseAntigravityReport(report string, now time.Time) Limits {
	return parseAntigravityReport(report, now)
}

func parseAntigravityReport(report string, _ time.Time) Limits {
	limits := Limits{}

	for _, raw := range strings.Split(report, "\n") {
		match := antigravityLimitLine.FindStringSubmatch(strings.TrimRight(raw, "\r"))
		if match == nil {
			continue
		}

		remaining, err := strconv.ParseFloat(match[3], 64)
		if err != nil || remaining < 0 || remaining > 100 {
			continue
		}

		limit := &WindowLimit{UsedPercent: 100 - remaining}
		if at, err := time.Parse(time.RFC3339, match[4]); err == nil {
			limit.ResetsAt = &at
		}

		// One report covers several model families and the app cannot tell which one a run will
		// use. The tightest is the one that cuts somebody off, so that is the one reported.
		if strings.Contains(strings.ToLower(match[2]), "week") {
			limits.Week = tighter(limits.Week, limit)
			continue
		}
		limits.Session = tighter(limits.Session, limit)
	}

	return limits
}

// tighter keeps whichever of two limits is closer to being spent.
func tighter(current, candidate *WindowLimit) *WindowLimit {
	if current == nil || candidate.UsedPercent > current.UsedPercent {
		return candidate
	}
	return current
}
