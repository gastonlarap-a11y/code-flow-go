package ai

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"slices"
)

// The seventeen prompt files, embedded byte-for-byte.
//
// They are VERBATIM contracts, not text the port is free to tidy: two parsers match on what they
// make the model emit (XLANG-001 pins the finding format, XLANG-016 the acceptance-criteria
// verdict block), and a stored user prompt is recognised as "still the default" by the SHA-256 of
// its bytes. A trailing newline added by an editor changes that digest and silently turns every
// unedited prompt in every existing install into "the user customised this".
//
// Embedded rather than read from disk for the same reason the tray icon is: a file that can go
// missing produces a degraded app that never says so.
//
//go:embed prompts/*.txt
var promptFS embed.FS

// Prompt names, as the files are called. Exported because the storage backfill, the review
// pipeline and the DBML assistant all reach for specific ones.
const (
	PromptPRReviewStandard        = "DEFAULT_PR_REVIEW_STANDARD"
	PromptTicketReviewStandard    = "DEFAULT_TICKET_REVIEW_STANDARD"
	PromptPRDescription           = "DEFAULT_PR_DESCRIPTION_TEMPLATE"
	PromptReview                  = "DEFAULT_REVIEW_PROMPT"
	PromptAnalyze                 = "DEFAULT_ANALYZE_TEMPLATE"
	PromptCommit                  = "DEFAULT_COMMIT_TEMPLATE"
	PromptResolveConflict         = "DEFAULT_RESOLVE_CONFLICT_TEMPLATE"
	PromptChatSystem              = "DEFAULT_CHAT_SYSTEM_PROMPT"
	PromptInlineEdit              = "DEFAULT_INLINE_EDIT_PROMPT"
	PromptFixFinding              = "FIX_FINDING_SYSTEM_PROMPT"
	PromptDBMLEdit                = "DBML_EDIT_PROMPT"
	PromptDBMLExplain             = "DBML_EXPLAIN_PROMPT"
	PromptDBMLReview              = "DBML_REVIEW_PROMPT"
	PromptReviewLevelBasico       = "REVIEW_LEVEL_BASICO"
	PromptReviewLevelCompleto     = "REVIEW_LEVEL_COMPLETO"
	PromptReviewLevelUltra        = "REVIEW_LEVEL_ULTRA"
	PromptReviewLevelUltraNoClone = "REVIEW_LEVEL_ULTRA_NO_CLONE"
)

// AllPrompts is every embedded name, for the test that proves none went missing in the move from
// docs/verbatim/.
func AllPrompts() []string {
	return []string{
		PromptPRReviewStandard, PromptTicketReviewStandard, PromptPRDescription, PromptReview,
		PromptAnalyze, PromptCommit, PromptResolveConflict, PromptChatSystem, PromptInlineEdit,
		PromptFixFinding, PromptDBMLEdit, PromptDBMLExplain, PromptDBMLReview,
		PromptReviewLevelBasico, PromptReviewLevelCompleto, PromptReviewLevelUltra,
		PromptReviewLevelUltraNoClone,
	}
}

// Prompt returns one prompt's text. It panics on an unknown name: every caller passes one of the
// constants above, so a miss is a typo caught at start-up rather than an empty prompt sent to a
// model — which would produce a plausible-looking review with none of the rules applied.
func Prompt(name string) string {
	data, err := promptFS.ReadFile("prompts/" + name + ".txt")
	if err != nil {
		panic("ai: unknown prompt " + name)
	}
	return string(data)
}

// Digest is the lowercase hex SHA-256 of a prompt's UTF-8 bytes, which is the identity
// SeededPromptHistory compares against.
func Digest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// formerDefaults are the digests of prompts CodeFlow shipped in earlier versions.
//
// The problem they solve: a workspace's `review_standard` row is seeded with the built-in default
// when the workspace is created. When a release improves that default, every workspace that never
// touched it is still holding the old text — and the app cannot tell "never edited" from
// "deliberately kept" without remembering what the old default was.
//
// So it remembers. A stored row whose digest matches one of these is an unedited former default
// and is replaced by the current one; anything else is the user's own writing and is never touched.
//
// **Before changing a seeded prompt, append its outgoing digest here.** Forgetting means every
// user who never edited it keeps the old text forever, silently.
var formerDefaults = map[string][]string{
	PromptPRReviewStandard: {
		"6b8bdda6da739ae4f60809830e7854a91278d0a32862a8e80385ac76d1f3d0c4", // v2.5.1
	},
	PromptTicketReviewStandard: {
		"a5cb429d5f7e034aec95e3e381164cc8198293f339b6b6c606653ebc6cc1756c", // v2.5.1
	},
}

// IsFormerDefault reports whether stored text is a superseded built-in default of the named
// prompt, and therefore safe to replace.
func IsFormerDefault(name, stored string) bool {
	digest := Digest(stored)
	return slices.Contains(formerDefaults[name], digest)
}

// RefreshSeededPrompt returns the text a stored prompt row should hold now.
//
// The three cases, in the order they are tested:
//
//	empty            → the current default (empty means "use the built-in", so it is reseeded)
//	a former default → the current default (unedited, so the improvement lands)
//	anything else    → unchanged (the user wrote it)
func RefreshSeededPrompt(name, stored string) string {
	if stored == "" || IsFormerDefault(name, stored) {
		return Prompt(name)
	}
	return stored
}

// VerifyPrompts checks that every declared prompt is present and non-empty. Called by the storage
// migration that backfills them, so a packaging mistake fails at start-up with a name in the
// message rather than at the moment someone asks for a review.
func VerifyPrompts() error {
	for _, name := range AllPrompts() {
		data, err := promptFS.ReadFile("prompts/" + name + ".txt")
		if err != nil {
			return fmt.Errorf("prompt %s is missing from the binary: %w", name, err)
		}
		if len(data) == 0 {
			return fmt.Errorf("prompt %s is empty", name)
		}
	}
	return nil
}
