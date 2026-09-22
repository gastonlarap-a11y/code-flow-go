package usage

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/sentinel"
)

/*
The one answer the indicator asks for (USAGE-001).

A single snapshot rather than a command per section, because the panel draws them together and four
round trips to render one pill would be four chances for the sections to disagree about what "now"
is.

Every field is measured or absent. A provider with no readable source reports `unavailable`, a local
one reports `unlimited`, and a window with no learned ceiling reports its consumption with no
percentage at all. Nothing here is ever filled in with an estimate.
*/

// State says how much can be known about a provider.
type State string

const (
	// Measured: the provider writes a transcript this app can read.
	Measured State = "measured"
	// Unlimited: a local engine, metered by nothing.
	Unlimited State = "unlimited"
	// Unavailable: the provider publishes no readable consumption, so nothing is shown for it.
	Unavailable State = "unavailable"
)

// Snapshot is everything the indicator draws.
type Snapshot struct {
	Providers []ProviderUsage `json:"providers"`
	Resources ResourceUsage   `json:"resources"`
	Data      DataUsage       `json:"data"`
	Activity  ActivityUsage   `json:"activity"`
	TakenAt   string          `json:"taken_at"`
}

// ProviderUsage is one agent's consumption.
type ProviderUsage struct {
	Provider string `json:"provider"`
	State    State  `json:"state"`
	// Plan is the provider's own word for the tier, empty when it does not say.
	Plan string `json:"plan"`
	// Session is the rolling block, nil when none is open.
	Session *WindowUsage `json:"session"`
	// Week is the trailing seven days, nil when nothing was spent in them.
	Week *WindowUsage `json:"week"`
}

// PercentSource says where a percentage came from.
//
// "The provider told us" and "we worked it out from watching you run out" are not the same claim,
// and a panel that presented them identically would be overstating the second.
type PercentSource string

const (
	// Reported: the provider's own figure, read from its CLI (USAGE-011).
	Reported PercentSource = "reported"
	// Observed: divided by a ceiling this app watched the user reach (USAGE-006).
	Observed PercentSource = "observed"
)

// WindowUsage is one metered stretch of time.
type WindowUsage struct {
	Tokens int64 `json:"tokens"`
	// Percent is nil when neither source has anything to say. "Not known yet" and "zero" are
	// different answers and the panel draws them differently.
	Percent *float64 `json:"percent"`
	// Source is where Percent came from, empty when there is no percentage.
	Source PercentSource `json:"source"`
	// Ceiling is the limit this app watched the user reach, nil before that ever happened. Absent
	// for a reported percentage, which arrives as a share with no denominator attached.
	Ceiling     *int64       `json:"ceiling"`
	StartedAt   string       `json:"started_at"`
	ResetsAt    string       `json:"resets_at"`
	BurnPerHour int64        `json:"burn_per_hour"`
	Models      []ModelUsage `json:"models"`
}

// ModelUsage is one model's share of a window.
type ModelUsage struct {
	Model  string `json:"model"`
	Tokens int64  `json:"tokens"`
}

// ResourceUsage is what the CodeFlow process costs the machine.
type ResourceUsage struct {
	CPUPercent  float64 `json:"cpu_percent"`
	MemoryBytes uint64  `json:"memory_bytes"`
	// Sampled is false on the first reading, which has no previous one to rate against.
	Sampled bool `json:"sampled"`
}

// DataUsage is the room the app's own files take.
type DataUsage struct {
	Bytes int64 `json:"bytes"`
	// Complete is false when the sweep stopped at its limit, making the figure a floor.
	Complete bool `json:"complete"`
}

// ActivityUsage is what the app has been used for in the trailing week.
type ActivityUsage struct {
	Conversations int64 `json:"conversations"`
	Jobs          int64 `json:"jobs"`
}

// Deps is what the service needs from the rest of the app.
type Deps struct {
	// Reader sweeps the agent transcripts.
	Reader *Reader
	// Store remembers the ceilings calibration learns. May be nil when the database failed to open.
	Store *Store
	// DB backs the activity counts. May be nil for the same reason.
	DB execer
	// DataDir is the directory holding the app's own files.
	DataDir string
	// ClaudeBinary is where the Claude CLI lives, empty to look it up on PATH.
	ClaudeBinary string
	// Limits asks each provider what it says is left, keyed by provider id (USAGE-011). **Empty by
	// default, on purpose**: each probe spawns a subprocess, and a test that grew one by accident
	// would depend on the machine it ran on. Composition passes real probes; tests pass fakes.
	Limits map[string]LimitsReader
	// Plan discovers the subscription tier (USAGE-005). Nil for the same reason as Limits — it is
	// another subprocess — and then the tier is simply unknown, which the panel already handles.
	Plan func(ctx context.Context) Plan
	// ActiveProvider names the engine a run would use, so a quota failure can be attributed to the
	// provider that hit it. Declared as a func at the consumer: this package needs one question
	// answered and knows nothing else about AI routing. Nil resolves to the default.
	ActiveProvider func(context.Context) string
	// Now is the clock, injectable so a test is not at the mercy of one.
	Now func() time.Time
}

// LimitsReader answers what a provider says is left. Declared at the consumer, and small enough
// that a fake in a test is three lines.
type LimitsReader interface {
	Read(ctx context.Context, now time.Time) Limits
}

// reportedOnly builds a window from a provider's figure alone, for an engine that reports its
// limits and logs no token counts. There is no consumption, no rate and no model breakdown to show
// — just the share and when it turns over.
func reportedOnly(limit *WindowLimit) *WindowUsage {
	if limit == nil {
		return nil
	}

	share := limit.UsedPercent
	usage := &WindowUsage{Percent: &share, Source: Reported, Models: []ModelUsage{}}
	if limit.ResetsAt != nil {
		usage.ResetsAt = limit.ResetsAt.UTC().Format(time.RFC3339)
	}
	return usage
}

// sortedKeys keeps the provider order stable between polls, so the panel does not reshuffle.
func sortedKeys(probes map[string]LimitsReader) []string {
	names := make([]string, 0, len(probes))
	for name := range probes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Service answers snapshots.
type Service struct {
	deps  Deps
	meter *Meter

	// mu guards the reader, which caches per file and is not safe for concurrent sweeps, and the
	// plan cache below it.
	mu       sync.Mutex
	plan     Plan
	planAt   time.Time
	planOnce bool
}

// planTTL is how long a discovered subscription is trusted. Somebody changing plan mid-session is
// rare; asking a subprocess on every poll to find that out would be absurd.
const planTTL = 30 * time.Minute

func NewService(deps Deps) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{deps: deps, meter: NewMeter()}
}

// Snapshot measures everything the indicator shows.
func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	now := s.deps.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot := Snapshot{
		Providers: s.providers(ctx, now),
		Resources: s.meter.Read(now),
		Data:      MeasureDisk(ctx, s.deps.DataDir),
		Activity:  MeasureActivity(ctx, s.deps.DB, now),
		TakenAt:   now.UTC().Format(time.RFC3339),
	}

	return snapshot, ctx.Err()
}

/*
providers walks everything there is anything to say about.

The two sources are independent: a provider can have transcripts to count and no limits to report
(Codex), limits to report and no transcripts (Antigravity, whose CLI does not log token counts), or
both (Claude). So the list is the union, in a stable order — the readers first, then anything only
a probe knows about.
*/
func (s *Service) providers(ctx context.Context, now time.Time) []ProviderUsage {
	measured := []ProviderUsage{}
	seen := map[string]bool{}

	if s.deps.Reader != nil {
		for _, provider := range s.deps.Reader.Providers() {
			seen[provider] = true
			measured = append(measured, s.provider(ctx, provider, now))
		}
	}

	for _, provider := range sortedKeys(s.deps.Limits) {
		if seen[provider] {
			continue
		}
		measured = append(measured, s.provider(ctx, provider, now))
	}

	return measured
}

func (s *Service) provider(ctx context.Context, provider string, now time.Time) ProviderUsage {
	usage := ProviderUsage{Provider: provider, State: Measured}
	limits := Limits{}

	if provider == "claude" {
		usage.Plan = s.claudePlan(ctx).Tier
	}
	if probe, ok := s.deps.Limits[provider]; ok && probe != nil {
		limits = probe.Read(ctx, now)
	}

	// Only the trailing week is ever needed: it is the longer of the two windows, and the session
	// block is found inside it.
	events := []Event{}
	if s.deps.Reader != nil {
		found, err := s.deps.Reader.Events(ctx, provider, now.Add(-WeekLength))
		if err != nil {
			usage.State = Unavailable
			return usage
		}
		events = found
	}

	// Nothing to count, but the provider may still have told us how much of its windows is gone —
	// Antigravity's CLI reports limits and logs no token counts at all.
	if len(events) == 0 {
		usage.Session = reportedOnly(limits.Session)
		usage.Week = reportedOnly(limits.Week)
		return usage
	}

	if window, open := CurrentSession(events, now); open {
		usage.Session = s.windowUsage(ctx, provider, usage.Plan, SessionWindow, window, limits, now)
	}

	week := CurrentWeek(events, now)
	if week.Tokens > 0 {
		usage.Week = s.windowUsage(ctx, provider, usage.Plan, WeekWindow, week, limits, now)
	}

	return usage
}

/*
windowUsage builds one window's row, and decides which percentage it carries.

**The provider's own figure wins.** It is Anthropic's number for Anthropic's limit; the ceiling
learned by watching is this app's inference about the same thing, and an inference does not outrank
the source. The learned ceiling stays underneath for when the CLI cannot be asked or its report
changes shape — which is the whole reason calibration was not deleted when the probe arrived.
*/
func (s *Service) windowUsage(
	ctx context.Context,
	provider, plan string,
	kind WindowKind,
	window Window,
	limits Limits,
	now time.Time,
) *WindowUsage {
	usage := &WindowUsage{
		Tokens:      window.Tokens,
		StartedAt:   window.Start.UTC().Format(time.RFC3339),
		ResetsAt:    window.End.UTC().Format(time.RFC3339),
		BurnPerHour: window.BurnPerHour(now),
		Models:      window.Models,
	}

	if reported := limits.forWindow(kind); reported != nil {
		share := reported.UsedPercent
		usage.Percent = &share
		usage.Source = Reported
		// The provider's own reset replaces the one derived from the transcripts, which rests on an
		// inferred window length (USAGE-003). A measured instant outranks an inference about it.
		if reported.ResetsAt != nil {
			usage.ResetsAt = reported.ResetsAt.UTC().Format(time.RFC3339)
		}
		return usage
	}

	if ceiling, known := s.deps.Store.Lookup(ctx, provider, plan, kind); known {
		usage.Ceiling = &ceiling
		usage.Percent = percentOf(window.Tokens, ceiling, true)
		usage.Source = Observed
	}

	return usage
}

// claudePlan asks the CLI at most once every planTTL.
func (s *Service) claudePlan(ctx context.Context) Plan {
	now := s.deps.Now()
	if s.planOnce && now.Sub(s.planAt) < planTTL {
		return s.plan
	}

	s.plan = Plan{}
	if s.deps.Plan != nil {
		s.plan = s.deps.Plan(ctx)
	}
	s.planAt = now
	s.planOnce = true
	return s.plan
}

/*
NoteFailure is handed every command failure and reacts only to the ones that mean "out of quota".

Wired into the bridge's failure recorder at composition, which is the one place every command's
error passes through — an AI run refused for quota reaches it whether it came from the chat, a
review or a commit message, and none of those paths had to learn about this package.

The sentinel is matched at position 0, the way the renderer matches it, and never wrapped.
*/
func (s *Service) NoteFailure(method string, err error) {
	if err == nil || !strings.HasPrefix(err.Error(), sentinel.QuotaExceeded) {
		return
	}

	provider := DefaultProvider
	if s.deps.ActiveProvider != nil {
		provider = s.deps.ActiveProvider(context.Background())
	}

	// Best effort and deliberately silent: failing to learn a ceiling costs a percentage the panel
	// already knows how to live without, and the command's own error is what the user is seeing.
	_ = s.ObserveLimit(context.Background(), provider)
}

// DefaultProvider is where an unnameable provider lands, matching `ai.DefaultProvider`.
const DefaultProvider = "claude"

/*
ObserveLimit records that a provider just refused for having run out.

What the open window held at that moment is, by definition, a limit the user reached — which is the
only honest denominator available (USAGE-006).
*/
func (s *Service) ObserveLimit(ctx context.Context, provider string) error {
	now := s.deps.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deps.Reader == nil || s.deps.Store == nil {
		return nil
	}

	events, err := s.deps.Reader.Events(ctx, provider, now.Add(-WeekLength))
	if err != nil || len(events) == 0 {
		return err
	}

	plan := ""
	if provider == "claude" {
		plan = s.claudePlan(ctx).Tier
	}

	if window, open := CurrentSession(events, now); open {
		if err := s.deps.Store.Observe(ctx, Ceiling{
			Provider: provider, Plan: plan, Window: SessionWindow,
			Tokens: window.Tokens, SeenAt: now,
		}); err != nil {
			return err
		}
	}

	return nil
}
