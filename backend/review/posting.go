package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/providers"
)

// Publishing a review's findings to the pull request they came from.
//
// The one property that has to hold across a pull request's whole life: **one finding, one thread**.
// A finding posted in iteration 2 and still open in iteration 5 gets a reply in the conversation it
// already has, not a fifth copy of itself. Everything here serves that.

// PostFindingItem is one finding the user picked in the review panel.
//
// camelCase, because it arrives from the renderer exactly as the panel built it. `File` and
// `Category` are the identity — the same pair reconciliation matches on — and `Content` is the
// whole comment, used only when a thread has to be opened.
type PostFindingItem struct {
	File     *string                    `json:"file"`
	Category string                     `json:"category"`
	Content  string                     `json:"content"`
	Location *providers.CommentLocation `json:"location"`
}

// PostBatch is one publish: the findings the user selected, and optionally the summary above them.
type PostBatch struct {
	Items       []PostFindingItem
	PostSummary bool
	Summary     *string
	// Iter is the iteration the replies name. A link review has none and passes 1.
	Iter int64
	// Today is the date the replies carry, the posting machine's own. Passed in rather than read
	// here so a test does not have to freeze a clock it does not own.
	Today string
}

// The four reply sentences, `VERBATIM` Spanish — and the two hosts' wordings genuinely differ:
// Azure's carry italics and the "Marcado como fixed" suffix, GitHub's do not. That is what each
// host's threads have said since 2.x, and a reader scrolling an old pull request sees both.
const (
	azureResolvedReply = "✔️ _Resuelto en la iteración %d — %s. Marcado como fixed._"
	azurePresentReply  = "➡️ _Sigue presente en la iteración %d — %s._"

	gitHubResolvedReply = "✔️ Resuelto en la iteración %d — %s."
	gitHubPresentReply  = "➡️ Sigue presente en la iteración %d — %s."
)

// PublishFindings posts a batch and reports every item that failed (REVIEW-032…035).
//
// The findings it is given are mutated in place — a thread id recorded, a state moved from open to
// posted — and returned, because the caller writes them back into the run. Two items that share an
// identity see each other's writes: the second one replies into the thread the first just opened
// rather than starting a second conversation for the same finding.
//
// Every item is attempted whatever the ones before it did. A publish that stopped at the first
// failure would leave the reviewer with a pull request in a state nobody chose, and the failures
// come back together at the end.
func PublishFindings(
	ctx context.Context,
	host providers.PullRequestHost,
	number int64,
	findings []MemoryFinding,
	batch PostBatch,
	analysedHead string,
) ([]MemoryFinding, error) {
	// Before anything is written: a batch whose anchors no longer match the pull request is refused
	// whole (`XLANG-014`). Ordering matters because the summary goes first on GitHub, and posting a
	// summary for findings that are then refused would be the worst of both.
	if err := host.EnsureUnchanged(ctx, number, analysedHead); err != nil {
		return findings, err
	}

	failures := make([]string, 0, len(batch.Items))

	postSummary := func() {
		if !batch.PostSummary || batch.Summary == nil || strings.TrimSpace(*batch.Summary) == "" {
			return
		}
		if _, err := host.OpenThread(ctx, number, *batch.Summary, nil); err != nil {
			failures = append(failures, "summary: "+err.Error())
		}
	}

	// The summary is the first thing a reader meets, never a postscript to the conclusions it
	// introduces — which means posting it at whichever end of the conversation puts it on top
	// (`DIVERGENCE-PROV-d`).
	if !host.DiscussionNewestFirst() {
		postSummary()
	}

	for i, item := range batch.Items {
		index, found := indexOf(findings, item)

		var thread *int64
		resolved := false
		if found {
			thread = findings[index].ThreadID
			resolved = findings[index].Estado == StateResolved
		}

		if thread == nil {
			opened, err := host.OpenThread(ctx, number, item.Content, item.Location)
			if err != nil {
				failures = append(failures, fmt.Sprintf("#%d: %s", i+1, err))
				continue
			}
			if found {
				applyPostOutcome(&findings[index], opened)
			}
			// When nothing matched, the comment went out and no thread id is recorded for it: a
			// later re-post of the same unmatched item opens another thread. Preserved — the
			// alternative is inventing a finding to hang the id on.
			continue
		}

		if err := host.Reply(ctx, number, *thread, replyText(host, batch, resolved), resolved); err != nil {
			failures = append(failures, fmt.Sprintf("#%d: %s", i+1, err))
		}
	}

	if host.DiscussionNewestFirst() {
		postSummary()
	}

	if len(failures) > 0 {
		return findings, fmt.Errorf("%d comment(s) failed to post — %s", //nolint:err113 // the message is the contract
			len(failures), strings.Join(failures, "; "))
	}
	return findings, nil
}

// replyText picks the sentence for this host and this outcome.
func replyText(host providers.PullRequestHost, batch PostBatch, resolved bool) string {
	if host.Provider() == providers.ProviderAzure {
		if resolved {
			return fmt.Sprintf(azureResolvedReply, batch.Iter, batch.Today)
		}
		return fmt.Sprintf(azurePresentReply, batch.Iter, batch.Today)
	}
	if resolved {
		return fmt.Sprintf(gitHubResolvedReply, batch.Iter, batch.Today)
	}
	return fmt.Sprintf(gitHubPresentReply, batch.Iter, batch.Today)
}

// indexOf matches a selected item back to its stored finding by **file and category** — the same
// identity reconciliation uses, never the `F-NNN` id.
//
// The first match wins, as it always has. Two stored findings sharing an identity therefore both
// resolve to the first one; that is `BUG-REVIEW-b`'s shape on this side, and reconciliation's
// one-to-one matching is what keeps it from arising in a run this port wrote.
func indexOf(findings []MemoryFinding, item PostFindingItem) (int, bool) {
	key := FindingIdentity(item.File, item.Category)
	for i, f := range findings {
		if FindingIdentity(f.Archivo, f.Categoria) == key {
			return i, true
		}
	}
	return 0, false
}

// applyPostOutcome records a newly opened thread against its finding.
//
// The state only moves from open to posted. A finding that was already resolved when somebody got
// around to posting it — never posted before, fixed in the meantime — stays resolved, which is what
// the traceability section will say about it.
func applyPostOutcome(finding *MemoryFinding, threadID int64) {
	finding.ThreadID = &threadID
	if finding.Estado == StateOpen {
		finding.Estado = StatePosted
	}
}

// AnalysedHead reads the head commit a run was computed against out of its stored meta.
//
// Tolerant on purpose: a run whose meta is missing or unparseable answers "", which the stale check
// reads as "cannot compare" and lets through.
func AnalysedHead(meta string) string {
	var parsed struct {
		HeadSHA string `json:"head_sha"`
	}
	if err := json.Unmarshal([]byte(meta), &parsed); err != nil {
		return ""
	}
	return parsed.HeadSHA
}

// DecodeFindings reads a stored run's findings column.
//
// An unreadable column is an empty list rather than an error: the run's markdown still renders, and
// refusing to publish because a blob from an older version will not parse would lose the reviewer
// their work.
func DecodeFindings(stored string) []MemoryFinding {
	findings := make([]MemoryFinding, 0, 8)
	if strings.TrimSpace(stored) == "" {
		return findings
	}
	if err := json.Unmarshal([]byte(stored), &findings); err != nil {
		return findings[:0]
	}
	return findings
}

// EncodeFindings renders the findings back for storage, keeping an empty list an empty array.
func EncodeFindings(findings []MemoryFinding) string {
	if findings == nil {
		findings = []MemoryFinding{}
	}
	encoded, err := json.Marshal(findings)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}
