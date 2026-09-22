# 17 — Usage indicator

A pill in the bottom-right corner showing what the app and its AI agents are consuming: how much of
an agent's window is gone and when it resets, what this process costs the machine, how much room the
app's data takes, and what the week's work has been.

**This document is not a port.** CodeFlow 2.x had no usage indicator, so there is no C# original to
match, no `BUG-*` to preserve and no `AMBIGUOUS-*` to resolve. Every rule below was decided here.

**The rule the whole feature rests on: everything shown is measured, never estimated.** No AI
provider publishes a quota API — Anthropic closed the request for one as *not planned* — so the
numbers come from what the CLIs write to disk about their own work and from this process's own
counters. Where there is no source, the panel says so. That is `AGENTS.md`'s "Do not guess" applied
to a panel whose entire worth is that its numbers are true.

## Scope

- `backend/usage/` — the readers, the windows, the calibration, the resource and disk meters
- `backend/storage/schema.sql` — `ai_usage_ceiling`
- `frontend/src/lib/usage/format.ts` — what the pill says
- `frontend/src/state/usageStore.ts`, `frontend/src/components/usage/`
- `frontend/src/components/common/ProgressBar.tsx` — shared with the updater

---

### USAGE-001 One snapshot, one command
**Implementation**: `backend/usage/commands.go` · `backend/usage/service.go`
**Behaviour**: `usage_snapshot` answers the AI providers, the process's resources, the data size and
the week's activity in one structure, measured at one instant.
**Inputs / outputs**: `usage_snapshot()` → `Snapshot`. No parameters: it measures the machine and
the user's own home, neither of which the renderer can name.
**Edge cases**: a section that cannot be measured answers its zero value rather than failing the
snapshot — one unreadable transcript must not cost the user every other number.
**Frontend dependency**: `frontend/src/lib/ipc/commands.ts` (`usageSnapshot`).
**Markers**: none. This is the second command this repository invented, so it is listed in
`newSincePort` in `backend/app/contract_test.go`.

**Why not a command per section.** The panel draws the four together. Four round trips would be four
chances for them to disagree about what "now" means, and a pill whose sections were measured seconds
apart is a pill that contradicts itself.

**It survives a failed database.** Registered beside the diagram editor, outside the storage stage:
without a database it loses the learned ceilings and the activity counts and still reports
consumption, reset times and resources.

### USAGE-002 Consumption is read from the agents' own transcripts, and nothing else is read
**Implementation**: `backend/usage/readers.go`
**Behaviour**: Claude Code keeps `~/.claude/projects/**/*.jsonl` and Codex keeps
`~/.codex/sessions/**/*.jsonl`, one JSON object per line, and an assistant turn carries what it
cost. The reader lifts out three things — when the turn happened, which model answered, and the
token count — and builds an `Event` from them.

Claude reports per-turn counts in `message.usage`, and all four are summed: input, output, cache
written and **cache read**. Codex reports the turn's own cost in `payload.info.last_token_usage`
(the running total sits beside it and is not what a window wants) and names its model once, in the
`session_meta` header, which every later turn in the file inherits.
**Inputs / outputs**: `Reader.Events(ctx, provider, since)` → `[]Event`, never nil.
**Edge cases**: a line that does not parse is skipped in silence — the tail of a live session is
half-written more often than not. An unreadable file is skipped, not fatal. A provider whose tree
does not exist answers no events and no error: never having run a CLI is an answer.
**Frontend dependency**: none directly.
**Markers**: none.

**The privacy rule, which is not negotiable.** Those transcripts hold entire conversations: the
user's code, their prompts, the answers. `Event` has three fields and none of them can carry
content, which is deliberate — the model makes the leak impossible rather than merely avoided.
`readers_test.go` asserts it against a line containing a private path and answer text.

**Why cache reads count.** They are cheaper than fresh input but they are not free, and a window
that ignored them would under-report a long session by most of its weight.

**Why the sweep is affordable.** There can be thousands of these files and they only grow. A file
whose last write predates the window is never opened — its last write is its last turn, so it
cannot hold an event inside the window — and a file whose size and modification time have not moved
since the last sweep is answered from cache.

### USAGE-003 The window is five hours, and that is inferred rather than documented
**Implementation**: `backend/usage/window.go`
**Behaviour**: A subscription is metered against a rolling block and a trailing week. A block opens
with the first call made outside any open block and stays open five hours; a call after it closes
opens the next one. The reset shown is that block's end. The week is a plain trailing seven days.
**Inputs / outputs**: `CurrentSession(events, now)` → `(Window, bool)`; `CurrentWeek(events, now)` →
`Window`. Both pure.
**Edge cases**: no open block is the ordinary state of somebody who has not worked in a while, and
the panel shows an empty window rather than an error or a stale block. Events arrive in whatever
order the filesystem gave them and are sorted on a copy, leaving the caller's slice alone.
**Frontend dependency**: none.
**Markers**: `UNVERIFIED` — see below.

**`UNVERIFIED`: the five hours are an inference.** Anthropic publishes neither the length nor the
exact semantics of the window; five hours is what its own interface reports and what every
third-party reader assumes. If the real semantics differ, the reset time moves with them. It is
recorded here as a marked assumption rather than presented as something the provider told us.

**Why a trailing week and not a calendar one.** The limit it stands for is rolling; a Monday reset
would report a budget nobody has.

### USAGE-004 A provider with no readable source says so
**Implementation**: `backend/usage/service.go` (`State`) · `frontend/src/types/domain.ts`
**Behaviour**: Three states. `measured` — the provider writes a transcript this app can read
(Claude, Codex). `unlimited` — a local engine metered by nothing (Ollama). `unavailable` — it
publishes no readable consumption, so **nothing is shown**.
**Inputs / outputs**: `state` on each provider in the snapshot.
**Edge cases**: opencode falls in the third group today — absent from the panel rather than present
with a zero, because a zero reads as "you have used nothing" and the truth is "nobody can tell".

**Consumption and limits are separate sources**, and a provider can have either without the other.
Antigravity logs no token counts at all, so there is nothing to measure — and its CLI reports its
limits happily, so it appears with a percentage, a reset and no token figure. Codex is the mirror
case: transcripts to count, no limits to ask for. The provider list is the union of the two.
**Frontend dependency**: `components/usage/UsageIndicator.tsx` hides a provider with nothing to show.
**Markers**: none.

**Reading a format nobody promised.** These transcripts are not a published contract. If a layout
changes, the reader stops finding turns and the provider reports as unmeasured — the required
failure, because a number that quietly stops moving is worse than one that says it is gone.

### USAGE-005 The plan is discovered, never asked for
**Implementation**: `backend/usage/plan.go`
**Behaviour**: `claude auth status` prints JSON, and `subscriptionType` in it names the tier. That
is what lets the indicator need nothing from the user.
**Inputs / outputs**: `ClaudePlan(ctx, binary)` → `Plan{Tier, LoggedIn}`.
**Edge cases**: a missing CLI, a refusal or nobody signed in all answer an empty plan and no error —
the consumption read from the transcripts is still shown. Cached for thirty minutes, because asking
a subprocess on every poll to learn something that changes once a year would be absurd.
**Frontend dependency**: `plan` on each provider, shown as a chip.
**Markers**: none. The probe goes through `shared/proc`, so it cannot be handed a credential
(SEC-007), and carries a deadline so a CLI that blocks cannot hang the poll behind it.

**Only the tier is kept.** The same output carries the account's e-mail and organisation id, and
this package has no business with either: the struct it decodes into names two fields.

### USAGE-006 The ceiling is learned by watching, and there is no percentage until it is
**Implementation**: `backend/usage/calibration.go` · `backend/usage/service.go` (`NoteFailure`) ·
`main.go` · `ai_usage_ceiling`
**Behaviour**: No provider publishes the token limit of a subscription window, the CLI does not
store it, and the limits move with demand. So the app waits. It **already** recognises a provider
saying "you have hit your limit" (`backend/ai/signals.go`, `QuotaSignal`, the `QUOTA_EXCEEDED::`
sentinel); the moment that happens, what the open window held is, by definition, a limit the user
reached. It is recorded, and from then on the percentage is a real division.

**Where it is wired.** `bridge.Service` already takes the failure recorder as a plain func, so that
every command's error passes one place. `main.go` chains two observers onto it: the error log, and
`Service.NoteFailure`, which reacts only to a message beginning with the quota sentinel and
attributes it to `ai.Router.ActiveProvider`. Neither package learns about the other — composition
joins them, which is what `main.go` is for. Every AI path benefits at once: chat, review, commit
message.
**Inputs / outputs**: `Service.NoteFailure(method, err)`; `Store.Observe(ctx, Ceiling)` and
`Store.Lookup(ctx, provider, plan, window)`. `percent` and `ceiling` cross the wire as **null**
until there is one.
**Edge cases**: a ceiling only ever moves up — running out early proves the window held at least
that much, and a later, larger number is the better reading of the same limit. Consumption past a
ceiling learned earlier reports 100%, not an impossible number; the next observation raises it.
A ceiling that cannot be read is treated as not having one, never as an error the user sees.
**Frontend dependency**: `lib/usage/format.ts` (`toneFor` answers `neutral` for null).
**Markers**: none.

**This is now the fallback rather than the only path.** `USAGE-011` asks the CLI for the provider's
own figures, which outrank anything inferred here. Calibration stays underneath for when the CLI
cannot be asked or its report changes shape — and when neither has anything to say, the panel still
says "limit not known yet" rather than inventing a number.

### USAGE-007 The resource figure is this process, and says so
**Implementation**: `backend/usage/resources.go`
**Behaviour**: CPU is the share of the machine's cores this process burned since the previous
reading; memory is what the Go runtime holds mapped from the operating system. Both come from
`runtime/metrics`, which is portable and needs nothing installed.
**Inputs / outputs**: `Meter.Read(now)` → `Resources{CPUPercent, MemoryBytes, Sampled}`.
**Edge cases**: the first reading has no previous one to rate against and answers `Sampled: false`
rather than a spike; two readings at the same instant likewise. CPU is clamped to 0–100.
**Frontend dependency**: the panel shows `—` while `sampled` is false.
**Markers**: none.

**It is not the whole application, and the panel says so.** The renderer runs in a WebView process
of its own that nothing here can see. Measuring it would need a process-inspection library, which
could not be added, so rather than publish a total that quietly excludes half of itself the panel
labels the section "CodeFlow process" and carries a line explaining the omission.

### USAGE-008 The data figure is the app's own directory
**Implementation**: `backend/usage/disk.go`
**Behaviour**: The size of everything under the app's base directory — the database, the logs, the
cloned repositories — in bytes.
**Inputs / outputs**: `MeasureDisk(ctx, root)` → `DiskUsage{Bytes, Complete}`.
**Edge cases**: bounded at 200 000 files; past that the sweep stops and `Complete` is false, which
the panel renders as a `≥` before the figure. A file that vanished mid-walk is ordinary in a tree
the app is writing to and is skipped.
**Frontend dependency**: `formatBytes`.
**Markers**: none.

**Bytes, not a share of the disk.** Free space is the operating system's business and moves for
reasons that have nothing to do with this app; "CodeFlow is holding 3.4 GB" is a fact about CodeFlow
and the only one that tells somebody whether to clean up.

### USAGE-009 Activity is counted from what was already being recorded
**Implementation**: `backend/usage/activity.go`
**Behaviour**: Conversations from `activity_log` and finished runs from `job_history`, both over the
trailing week — the same stretch the AI section uses, so the two read against one another honestly.
**Inputs / outputs**: `MeasureActivity(ctx, db, now)` → `Activity{Conversations, Jobs}`.
**Edge cases**: a count that cannot be read answers zero rather than failing the snapshot.
**Frontend dependency**: the panel's last section.
**Markers**: none.

**Nothing new is collected.** Both tables were already being written; turning the indicator on
starts recording nothing it was not recording before.

### USAGE-010 A pill, not a status bar
**Implementation**: `frontend/src/components/usage/UsageIndicator.tsx` ·
`frontend/src/state/usageStore.ts` · `frontend/src/lib/usage/format.ts`
**Behaviour**: At rest, a dot and one number in the bottom-right corner: the highest share of any
learned ceiling, because that is what decides whether somebody is about to be cut off mid-task. On
hover or click it expands into four sections. Escape closes it; the trigger is a real button with
`aria-expanded`.

Colour follows the same four semantic tokens the rest of the app uses: `--cf-success` below 75%,
`--cf-warning` from 75, `--cf-danger` from 90, and `--cf-text-muted` when nothing has a percentage.
**Inputs / outputs**: `headline(snapshot)`, `toneFor(percent)`, `minutesUntilExhausted(window, now)`
— all pure and tested.
**Edge cases**: nothing measured yet renders nothing at all, so a machine with no agents installed
has an empty corner rather than a permanent `—`. A failed poll leaves the previous snapshot on
screen with a line saying the reading is old, and raises no toast.
**Frontend dependency**: `ProgressBar`, `Chip`.
**Markers**: none.

**Why a pill.** The window's bottom strip was removed on purpose in the redesign — `App.tsx` and
`HeaderGitActions.tsx` both record it — and its pieces were redistributed into the header. Putting a
permanent band back to show four numbers most people glance at twice a day would undo that decision.

**Two cadences and a focus check.** Four seconds while the panel is open, because CPU is a rate and
a rate somebody is watching should move; sixty while it is only a dot; and **nothing at all while
the window is unfocused**. A background app measuring itself every four seconds is the sort of thing
people uninstall a tool over.

**The projection is withheld more often than it is shown.** "None left in 40 min" needs both a
learned ceiling and a rate, and it is suppressed when the window resets first — sending somebody to
make coffee over a limit they will never reach is worse than saying nothing.

### USAGE-011 The provider's own figures come from asking its CLI
**Implementation**: `backend/usage/limits.go` · `backend/usage/service.go` (`windowUsage`)
**Behaviour**: Both agent CLIs run `/usage` non-interactively, under the session the user already
has:

| Provider | Command | Report |
|---|---|---|
| `claude` | `claude -p "/usage" --output-format json` | prose, one line per window, **percent used**, reset as `Sep 22 at 1pm (America/Santiago)` |
| `gemini` (Antigravity) | `agy -p "/usage"` | tab-separated table, **percent remaining**, reset already RFC 3339 |

A reported share is the percentage and is marked `reported`; when there is none, the ceiling learned
by watching is used and marked `observed` (`USAGE-006`). A reported reset **replaces** the one
derived from the transcripts, because a measured instant outranks an inference about an inferred
window length (`USAGE-003`).
**Inputs / outputs**: `LimitsProbe.Read(ctx, now)` → `Limits{Session, Week}`, each a nil-able
`WindowLimit{UsedPercent, ResetsAt}`. `WindowUsage.source` crosses the wire as
`"reported" | "observed" | ""`.
**Edge cases**: a missing CLI, a non-zero exit, `is_error`, an unparseable envelope or an
unrecognised report all answer empty limits and no error — the panel falls back. Cached for ten
minutes: each probe spawns a process and the five-hour window moves slowly enough that the staleness
is invisible. A stale reading is **discarded rather than kept** when a later probe fails, because a
figure from before the window turned over would be shown as though it were current.
**Frontend dependency**: `components/usage/UsageIndicator.tsx` labels an `observed` percentage;
a `reported` one stands on its own.
**Markers**: none.

**Two parsers, deliberately not one.** Claude reports what is **used**; Antigravity reports what
**remains**. A single lenient scanner looking for "a percentage near a window word" would read 94%
remaining as 94% used and tell somebody they were nearly out at the moment they had almost
everything left. Antigravity's figure is inverted where it is read, not later, so no "remaining"
value exists anywhere downstream to be mistaken. `limits_test.go` asserts that neither report parses
as the other's.

**Two more traps the samples exposed.** Claude reports the week once for all models and again per
model, so the `all models` line wins wherever it sits rather than whichever comes first. And
Antigravity reports several model families at once, which the app cannot choose between, so the
**tightest** is the one shown — it is the one that cuts somebody off.

**Why this and not the private endpoint.** The interactive `/usage` gets its numbers from Anthropic,
and the apparent way to match it was to call the same endpoint with the user's OAuth token — which
would mean this app reading and transmitting a credential, exactly what `SEC-007` exists to prevent,
against a URL nobody documents. Driving the supported CLI gets the same numbers with none of that:
documented flags, the user's existing session, and no credential in this app's hands at any point.

**The scanner does not understand the report, and that is the design.** Anthropic promises no format
for that text. So a number is taken only when a line — or the heading above it — names the window it
belongs to, the contributing-behaviours block is skipped by the phrase that identifies it, and
anything unrecognised yields nothing. A wording change therefore costs a percentage and can never
produce a wrong one. The scanner is exported as `ParseLimitsReport` so it is tested directly, being
the piece most likely to need adjusting.

**Both probes are injected, and nil by default.** `Deps.Limits` and `Deps.Plan` each spawn a child
process, so composition wires them and tests do not: a test that grew a subprocess by accident would
depend on the machine it ran on, which is how two of these tests first failed.

---

## Deliberately not in this version

| Not built | Why |
|---|---|
| A percentage for Gemini, `agy`, opencode | They publish no readable consumption. An estimate is the one thing this feature refuses to show. |
| OpenAI's per-minute rate-limit headers | Real provider data, but a rolling one-minute window is a different thing from "how much of my plan is left" and would sit confusingly beside the others. Worth adding once there is a way to label it that does not mislead. |
| The renderer's own CPU and memory | Needs a process-inspection library that could not be added here. `USAGE-007` states the omission rather than papering over it. |
| Cost in currency | Token counts are measured; prices are not, and they change per plan and per model. Multiplying by a number from memory is the guess this document exists to avoid. |
| History or charts over time | The snapshot answers "now". Keeping a series means a table that grows forever for a panel nobody opens twice. |
