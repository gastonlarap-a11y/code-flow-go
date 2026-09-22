package usage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/usage"
)

/*
Reading each CLI's own usage report.

Neither format is a contract, so what is asserted is not a wording but a discipline: a figure is
taken only from a line the parser recognises in full, and anything else yields nothing. The samples
below are the real shape of each report, with the numbers changed — the format is what is being
tested, and nobody's consumption belongs in a repository.

The one asymmetry that would be a serious bug if it were ever flattened: **Claude reports what is
used and Antigravity reports what remains.** 94% has opposite meanings in the two reports.
*/

// fakeLimits stands in for a probe, so nothing in a test spawns a process.
type fakeLimits struct {
	limits usage.Limits
	calls  int
}

func (f *fakeLimits) Read(context.Context, time.Time) usage.Limits {
	f.calls++
	return f.limits
}

func used(percent float64) *usage.WindowLimit { return &usage.WindowLimit{UsedPercent: percent} }

// ---- Claude Code ------------------------------------------------------------------------------

const claudeReport = `You are currently using your subscription to power your Claude Code usage

Current session: 9% used · resets Sep 22 at 1pm (America/Santiago)
Current week (all models): 61% used · resets Sep 23 at 12am (America/Santiago)
Current week (Fable): 4% used · resets Sep 23 at 12am (America/Santiago)

What's contributing to your limits usage?
Approximate, based on local sessions on this machine — does not include other devices or claude.ai.

Last 24h · 1666 requests · 4 sessions
  100% of your usage came from subagent-heavy sessions
  94% of your usage was at >150k context
  Top skills: /verify 3%

Last 7d · 6969 requests · 29 sessions
  95% of your usage was at >150k context
  Top subagents: Explore 2%`

func TestClaudesReportGivesBothWindows(t *testing.T) {
	limits := usage.ParseLimitsReport(claudeReport, at(10, 0))

	require.NotNil(t, limits.Session)
	require.NotNil(t, limits.Week)
	assert.InDelta(t, 9.0, limits.Session.UsedPercent, 0.01)
	assert.InDelta(t, 61.0, limits.Week.UsedPercent, 0.01)
}

// The week is reported once for all models and again per model. The overall figure is the one that
// decides whether somebody is cut off, and it must win wherever it sits in the list.
func TestTheAllModelsWeekWinsOverAPerModelOne(t *testing.T) {
	report := `Current week (Fable): 4% used · resets Sep 23 at 12am (America/Santiago)
Current week (all models): 61% used · resets Sep 23 at 12am (America/Santiago)`

	limits := usage.ParseLimitsReport(report, at(10, 0))

	require.NotNil(t, limits.Week)
	assert.InDelta(t, 61.0, limits.Week.UsedPercent, 0.01, "even when it comes second")
}

/*
The contributing-behaviours block below the limits is percentages about something else entirely.

"94% of your usage was at >150k context" next to a session at 9% is exactly the pair that would
put a confident wrong number on the panel, which is why the parser requires the whole line shape
rather than hunting for a percentage near a word.
*/
func TestTheContributingBreakdownIsNeverReadAsALimit(t *testing.T) {
	limits := usage.ParseLimitsReport(claudeReport, at(10, 0))

	require.NotNil(t, limits.Session)
	assert.InDelta(t, 9.0, limits.Session.UsedPercent, 0.01, "not the 94% from the breakdown")
}

func TestClaudesResetIsReadIntoAnInstant(t *testing.T) {
	limits := usage.ParseLimitsReport(claudeReport, at(10, 0))

	require.NotNil(t, limits.Session)
	require.NotNil(t, limits.Session.ResetsAt)
	// 1pm in Santiago on 22 September 2026.
	santiago, err := time.LoadLocation("America/Santiago")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 22, 13, 0, 0, 0, santiago).UTC(), limits.Session.ResetsAt.UTC())
}

// The two that a 12-hour clock gets wrong in opposite directions: 12am is the start of the day and
// 12pm is the middle of it, and neither is 12 o'clock plus twelve.
func TestNoonAndMidnightAreReadTheRightWayRound(t *testing.T) {
	santiago, err := time.LoadLocation("America/Santiago")
	require.NoError(t, err)

	midnight := usage.ParseLimitsReport(
		"Current session: 5% used · resets Sep 23 at 12am (America/Santiago)", at(10, 0))
	noon := usage.ParseLimitsReport(
		"Current session: 5% used · resets Sep 23 at 12pm (America/Santiago)", at(10, 0))

	require.NotNil(t, midnight.Session.ResetsAt)
	require.NotNil(t, noon.Session.ResetsAt)
	assert.Equal(t, time.Date(2026, 9, 23, 0, 0, 0, 0, santiago).UTC(), midnight.Session.ResetsAt.UTC())
	assert.Equal(t, time.Date(2026, 9, 23, 12, 0, 0, 0, santiago).UTC(), noon.Session.ResetsAt.UTC())
}

func TestAShareWithNoResetIsStillAShare(t *testing.T) {
	limits := usage.ParseLimitsReport("Current session: 40% used", at(10, 0))

	require.NotNil(t, limits.Session)
	assert.InDelta(t, 40.0, limits.Session.UsedPercent, 0.01)
	assert.Nil(t, limits.Session.ResetsAt)
}

func TestAnUnreadableTimezoneCostsTheResetAndNotTheShare(t *testing.T) {
	limits := usage.ParseLimitsReport(
		"Current session: 40% used · resets Sep 22 at 1pm (Not/AZone)", at(10, 0))

	require.NotNil(t, limits.Session)
	assert.Nil(t, limits.Session.ResetsAt)
}

// ---- Antigravity ------------------------------------------------------------------------------

const antigravityReport = "Gemini Models\tWeekly Limit Remaining\t100%\t2026-09-29T11:34:17Z\n" +
	"Gemini Models\tFive Hour Limit Remaining\t100%\t2026-09-22T16:34:17Z\n" +
	"Claude and GPT models\tWeekly Limit Remaining\t91%\t2026-09-22T12:23:08Z\n" +
	"Claude and GPT models\tFive Hour Limit Remaining\t94%\t2026-09-22T16:13:50Z"

/*
The asymmetry that would be a serious bug if it were ever flattened into one parser.

Antigravity reports what is **left**, so 94% remaining is 6% used. Reading it the other way would
tell somebody they were nearly out at the moment they had almost everything.
*/
func TestAntigravitysRemainingIsInvertedIntoUsed(t *testing.T) {
	limits := usage.ParseAntigravityReport(antigravityReport, at(10, 0))

	require.NotNil(t, limits.Session)
	assert.InDelta(t, 6.0, limits.Session.UsedPercent, 0.01, "94% remaining is 6% used")
}

// One report covers several model families and the app cannot tell which a run will use, so the
// tightest is the one that matters.
func TestTheTightestFamilyIsTheOneReported(t *testing.T) {
	limits := usage.ParseAntigravityReport(antigravityReport, at(10, 0))

	require.NotNil(t, limits.Week)
	assert.InDelta(t, 9.0, limits.Week.UsedPercent, 0.01, "91% remaining beats the family at 100%")
}

func TestAntigravitysResetIsAlreadyAnInstant(t *testing.T) {
	limits := usage.ParseAntigravityReport(antigravityReport, at(10, 0))

	require.NotNil(t, limits.Session)
	require.NotNil(t, limits.Session.ResetsAt)
	assert.Equal(t, "2026-09-22T16:13:50Z", limits.Session.ResetsAt.UTC().Format(time.RFC3339))
}

// ---- both -------------------------------------------------------------------------------------

func TestAReportInAnUnknownShapeYieldsNothing(t *testing.T) {
	for _, report := range []string{"", "Todo bien por aquí, nada que declarar.", "50%"} {
		claude := usage.ParseLimitsReport(report, at(10, 0))
		antigravity := usage.ParseAntigravityReport(report, at(10, 0))

		assert.Nil(t, claude.Session, report)
		assert.Nil(t, claude.Week, report)
		assert.Nil(t, antigravity.Session, report)
		assert.Nil(t, antigravity.Week, report)
	}
}

func TestAnImpossibleShareIsRefused(t *testing.T) {
	assert.Nil(t, usage.ParseLimitsReport("Current session: 140% used", at(10, 0)).Session)
	assert.Nil(t, usage.ParseAntigravityReport(
		"X\tFive Hour Limit Remaining\t140%\t2026-09-22T16:13:50Z", at(10, 0)).Session)
}

// Each CLI's report must not parse as the other's, or a format change in one would start feeding
// the other's numbers into the panel.
func TestNeitherReportParsesAsTheOthers(t *testing.T) {
	assert.Nil(t, usage.ParseLimitsReport(antigravityReport, at(10, 0)).Session)
	assert.Nil(t, usage.ParseAntigravityReport(claudeReport, at(10, 0)).Session)
}

// ---- how the service uses them ------------------------------------------------------------

func TestAReportedShareOutranksTheCeilingLearnedByWatching(t *testing.T) {
	now := at(10, 0)
	home := t.TempDir()
	store := usage.NewStore(openTestDB(t))

	// A ceiling learned earlier would put this window at 50%.
	require.NoError(t, store.Observe(t.Context(), usage.Ceiling{
		Provider: "claude", Plan: "", Window: usage.SessionWindow, Tokens: 2_000, SeenAt: now,
	}))

	service := usage.NewService(usage.Deps{
		Reader:  usage.NewReader(usage.ClaudeSource(home)),
		Store:   store,
		DataDir: t.TempDir(),
		Now:     func() time.Time { return now },
		Limits: map[string]usage.LimitsReader{
			"claude": &fakeLimits{limits: usage.Limits{Session: used(83)}},
		},
	})
	writeTranscript(t, filepath.Join(home, ".claude", "projects", "p", "s.jsonl"),
		claudeTurn("2026-09-22T09:00:00.000Z", "claude-opus-5", 1_000, 0, 0, 0))

	snapshot, err := service.Snapshot(t.Context())

	require.NoError(t, err)
	session := providerNamed(t, snapshot, "claude").Session
	require.NotNil(t, session.Percent)
	assert.InDelta(t, 83.0, *session.Percent, 0.01, "the provider's own number, not our inference")
	assert.Equal(t, usage.Reported, session.Source)
}

func TestWithoutAReportedShareTheLearnedCeilingIsUsed(t *testing.T) {
	now := at(10, 0)
	home := t.TempDir()
	store := usage.NewStore(openTestDB(t))

	require.NoError(t, store.Observe(t.Context(), usage.Ceiling{
		Provider: "claude", Plan: "", Window: usage.SessionWindow, Tokens: 2_000, SeenAt: now,
	}))

	service := usage.NewService(usage.Deps{
		Reader:  usage.NewReader(usage.ClaudeSource(home)),
		Store:   store,
		DataDir: t.TempDir(),
		Now:     func() time.Time { return now },
		Limits:  map[string]usage.LimitsReader{"claude": &fakeLimits{}},
	})
	writeTranscript(t, filepath.Join(home, ".claude", "projects", "p", "s.jsonl"),
		claudeTurn("2026-09-22T09:00:00.000Z", "claude-opus-5", 1_000, 0, 0, 0))

	snapshot, err := service.Snapshot(t.Context())

	require.NoError(t, err)
	session := providerNamed(t, snapshot, "claude").Session
	require.NotNil(t, session.Percent)
	assert.InDelta(t, 50.0, *session.Percent, 0.01)
	assert.Equal(t, usage.Observed, session.Source, "labelled as ours, not as the provider's")
}

// Antigravity's CLI logs no token counts at all, so it has consumption nobody can measure and
// limits it will happily report. Leaving it out would hide a real number.
func TestAProviderWithNoTranscriptStillShowsItsReportedShare(t *testing.T) {
	now := at(10, 0)

	service := usage.NewService(usage.Deps{
		Reader:  usage.NewReader(),
		DataDir: t.TempDir(),
		Now:     func() time.Time { return now },
		Limits: map[string]usage.LimitsReader{
			"gemini": &fakeLimits{limits: usage.Limits{Session: used(6), Week: used(9)}},
		},
	})

	snapshot, err := service.Snapshot(t.Context())

	require.NoError(t, err)
	gemini := providerNamed(t, snapshot, "gemini")
	require.NotNil(t, gemini.Session)
	assert.InDelta(t, 6.0, *gemini.Session.Percent, 0.01)
	assert.Zero(t, gemini.Session.Tokens, "nothing to count, and none claimed")
	assert.NotNil(t, gemini.Session.Models, "an empty list, never null")
}

// A provider's figure belongs to that provider. Applying one to another is a number about the
// wrong account.
func TestOneProvidersShareIsNotAppliedToAnother(t *testing.T) {
	now := at(10, 0)
	home := t.TempDir()

	service := usage.NewService(usage.Deps{
		Reader:  usage.NewReader(usage.CodexSource(home)),
		Store:   usage.NewStore(openTestDB(t)),
		DataDir: t.TempDir(),
		Now:     func() time.Time { return now },
		Limits: map[string]usage.LimitsReader{
			"claude": &fakeLimits{limits: usage.Limits{Session: used(83)}},
		},
	})
	writeTranscript(t, filepath.Join(home, ".codex", "sessions", "r.jsonl"),
		codexMeta("2026-09-22T09:00:00.000Z", "gpt-5.6-sol"),
		codexTurn("2026-09-22T09:01:00.000Z", 100, 100))

	snapshot, err := service.Snapshot(t.Context())

	require.NoError(t, err)
	assert.Nil(t, providerNamed(t, snapshot, "codex").Session.Percent)
}

// The provider's own reset replaces the one derived from an inferred window length (USAGE-003).
func TestAReportedResetReplacesTheDerivedOne(t *testing.T) {
	now := at(10, 0)
	home := t.TempDir()
	reset := at(18, 30)

	service := usage.NewService(usage.Deps{
		Reader:  usage.NewReader(usage.ClaudeSource(home)),
		DataDir: t.TempDir(),
		Now:     func() time.Time { return now },
		Limits: map[string]usage.LimitsReader{
			"claude": &fakeLimits{limits: usage.Limits{
				Session: &usage.WindowLimit{UsedPercent: 9, ResetsAt: &reset},
			}},
		},
	})
	writeTranscript(t, filepath.Join(home, ".claude", "projects", "p", "s.jsonl"),
		claudeTurn("2026-09-22T09:00:00.000Z", "claude-opus-5", 1_000, 0, 0, 0))

	snapshot, err := service.Snapshot(t.Context())

	require.NoError(t, err)
	session := providerNamed(t, snapshot, "claude").Session
	assert.Equal(t, "2026-09-22T18:30:00Z", session.ResetsAt, "not the 14:00 the transcript implies")
}

// Each probe spawns a process. Asking twice for one provider would be twice per poll.
func TestEachProbeIsAskedOncePerSnapshot(t *testing.T) {
	now := at(10, 0)
	home := t.TempDir()
	probe := &fakeLimits{limits: usage.Limits{Session: used(10)}}

	service := usage.NewService(usage.Deps{
		Reader:  usage.NewReader(usage.ClaudeSource(home)),
		DataDir: t.TempDir(),
		Now:     func() time.Time { return now },
		Limits:  map[string]usage.LimitsReader{"claude": probe},
	})
	writeTranscript(t, filepath.Join(home, ".claude", "projects", "p", "s.jsonl"),
		claudeTurn("2026-09-22T09:00:00.000Z", "claude-opus-5", 1_000, 0, 0, 0))

	_, err := service.Snapshot(t.Context())

	require.NoError(t, err)
	assert.Equal(t, 1, probe.calls, "one provider, one ask — the windows share the reading")
}
