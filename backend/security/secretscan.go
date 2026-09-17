package security

import (
	"regexp"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/git"
)

// The pre-commit secret gate (SEC-008 … SEC-011).
//
// Explicitly **not** the credential store in this same package: that one holds the user's own
// tokens on purpose, this one looks at the staged diff and flags credentials about to be committed.
//
// Two rules shape everything below and both are about keeping the report worth reading. Only added
// lines are scanned — a secret sitting in a context line was already in the repository and is not
// this gate's concern — and at most one hit is reported per line, so a line matching four rules
// produces one row rather than four.

// SecretHit is one credential found in the staged diff.
//
// The preview is always masked. A report that printed the secret would put it in a log, a
// screenshot and a support ticket — which is worse than the commit it was trying to prevent.
type SecretHit struct {
	File     string `json:"file"`
	Line     int64  `json:"line"`
	Rule     string `json:"rule"`
	RuleName string `json:"rule_name"`
	Severity string `json:"severity"`
	Preview  string `json:"preview"`
}

// secretRule is one pattern and what to call it.
//
// ruleName is deliberately left untranslated: it is what the report shows and what a user searches
// for when they look the warning up.
type secretRule struct {
	id               string
	name             string
	severity         string
	pattern          *regexp.Regexp
	checkPlaceholder bool
}

// secretRules are tried **in this order**, and the first match wins. The order is significant: an
// earlier rule shadows a later one on the same line, which is why the fifteen specific patterns come
// before the generic assignment rule that would otherwise claim most of them.
//
// Every pattern is VERBATIM.
var secretRules = []secretRule{
	{"private-key", "Private key (PEM)", "critical",
		regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----`), false},
	{"aws-access-key", "AWS access key id", "critical",
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), false},
	{"aws-secret-key", "AWS secret access key", "critical",
		regexp.MustCompile(`(?i)aws_secret_access_key\s*[:=]\s*['"]?(?P<val>[A-Za-z0-9/+=]{40})['"]?`), false},
	{"github-token", "GitHub token", "critical",
		regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36}\b`), false},
	{"github-pat", "GitHub fine-grained PAT", "critical",
		regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}\b`), false},
	{"google-api-key", "Google API key", "critical",
		regexp.MustCompile(`\bAIza[0-9A-Za-z\-_]{35}\b`), false},
	{"slack-token", "Slack token", "critical",
		regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{10,48}\b`), false},
	{"slack-webhook", "Slack webhook URL", "warning",
		regexp.MustCompile(`https://hooks\.slack\.com/services/[A-Za-z0-9/]+`), false},
	{"stripe-secret-key", "Stripe secret key", "critical",
		regexp.MustCompile(`\bsk_live_[0-9A-Za-z]{16,}\b`), false},
	{"stripe-restricted-key", "Stripe restricted key", "critical",
		regexp.MustCompile(`\brk_live_[0-9A-Za-z]{16,}\b`), false},
	{"openai-key", "OpenAI API key", "critical",
		regexp.MustCompile(`\bsk-proj-[A-Za-z0-9_-]{20,}\b`), false},
	{"npm-token", "npm access token", "critical",
		regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`), false},
	{"azure-storage-key", "Azure storage account key", "critical",
		regexp.MustCompile(`(?i)AccountKey=[A-Za-z0-9+/=]{40,}`), false},
	{"jwt", "JSON Web Token (JWT)", "warning",
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`), false},
	{"hardcoded-secret", "Hardcoded secret assignment", "warning",
		regexp.MustCompile(`(?i)(?:password|passwd|pwd|secret|api[_-]?key|apikey|access[_-]?token|` +
			`auth[_-]?token|client[_-]?secret|private[_-]?key|token)\s*[:=]\s*['"](?P<val>[^'"\n]{8,})['"]`), true},
}

// ScanDiff finds credentials in a staged diff.
//
// It takes git.FileDiff rather than a shape of its own. That is a data type on the wire — the same
// one get_staged_diff already hands the renderer — not a behaviour this package should abstract
// over, and a private copy would only add a conversion between two identical structs.
//
// It cannot fail. A scanner that could would leave the caller deciding whether a failed scan means
// "clean" or "do not commit", and neither answer is one worth acting on — the only failures worth
// reporting come from reading the diff, which is the caller's job.
func ScanDiff(files []git.FileDiff) []SecretHit {
	hits := make([]SecretHit, 0, 8)

	for _, file := range files {
		path := resolveDiffPath(file)

		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				// Only added content. Context and removed lines are never considered, whatever
				// they contain.
				if line.Origin != "+" {
					continue
				}
				if hit, found := scanLine(path, line); found {
					hits = append(hits, hit)
				}
			}
		}
	}
	return hits
}

// resolveDiffPath is the new path, then the old one, then a literal question mark.
//
// The last case is a diff with neither, which should not happen and is reported as `"?"` rather
// than as an empty string — an empty file name in the report reads as a rendering bug.
func resolveDiffPath(file git.FileDiff) string {
	if file.NewPath != nil && *file.NewPath != "" {
		return *file.NewPath
	}
	if file.OldPath != nil && *file.OldPath != "" {
		return *file.OldPath
	}
	return "?"
}

// scanLine tries every rule in order and stops at the first that matches.
func scanLine(path string, line git.DiffLine) (SecretHit, bool) {
	for _, rule := range secretRules {
		match := rule.pattern.FindStringSubmatchIndex(line.Content)
		if match == nil {
			continue
		}

		value := matchedValue(rule, line.Content, match)

		// A placeholder verdict continues to the next rule rather than ending the line. Only the
		// last rule sets the flag, so in practice it ends the line with no hit — but the ordering
		// is what the specification says and a rule added before it would depend on it.
		if rule.checkPlaceholder && isPlaceholder(value) {
			continue
		}

		var lineNo int64
		if line.NewLineNo != nil {
			lineNo = *line.NewLineNo
		}
		return SecretHit{
			File:     path,
			Line:     lineNo,
			Rule:     rule.id,
			RuleName: rule.name,
			Severity: rule.severity,
			Preview:  mask(value),
		}, true
	}
	return SecretHit{}, false
}

// matchedValue is the rule's `val` capture where it has one, else the whole match.
//
// Only two rules define the group. Everything else reports the whole match including any prefix the
// pattern contains — `AccountKey=…` is reported with the prefix, which is what 2.x did.
func matchedValue(rule secretRule, content string, match []int) string {
	if index := rule.pattern.SubexpIndex("val"); index > 0 && 2*index+1 < len(match) && match[2*index] >= 0 {
		return content[match[2*index]:match[2*index+1]]
	}
	return content[match[0]:match[1]]
}

// placeholderNeedles are the substrings that mark a value as a template rather than a secret.
// VERBATIM, and matched against the lowercased value.
var placeholderNeedles = []string{
	"example", "changeme", "placeholder", "your-", "your_", "yourtoken", "xxxx", "todo", "<",
}

// placeholderMarkers are matched **case-sensitively** against the value as written: these are
// syntax, and `Process.Env` is not the same thing as `process.env`.
var placeholderMarkers = []string{"${", "{{", "process.env", "os.environ", "getenv"}

// isPlaceholder reports whether a value looks like a template rather than a real credential.
//
// Two independent checks, either one enough. It exists because the generic assignment rule would
// otherwise flag every example in every README and configuration template in the repository, and a
// gate that cries wolf on documentation is a gate people learn to click through.
func isPlaceholder(value string) bool {
	for _, marker := range placeholderMarkers {
		if strings.Contains(value, marker) {
			return true
		}
	}

	lower := strings.ToLower(value)
	for _, needle := range placeholderNeedles {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// mask turns a matched value into something safe to display (SEC-010).
//
// Counted in characters rather than bytes, so a value with accented or non-Latin characters is cut
// where a reader would expect rather than mid-encoding.
//
// Two shapes, and the reason for each:
//
//   - Six characters or fewer: bullets only, at least three of them. The floor is deliberate — a
//     two-character value masked as two bullets would tell the reader it was two characters long.
//   - Longer: the first three and last two survive, which is enough to recognise *which* key it is
//     without being enough to use. The bullet run is capped at sixteen, so past twenty-one
//     characters the mask stops revealing the true length as well.
func mask(value string) string {
	runes := []rune(strings.TrimSpace(value))
	n := len(runes)

	if n <= 6 {
		return strings.Repeat("•", max(n, 3))
	}

	dots := min(n-5, 16)
	return string(runes[:3]) + strings.Repeat("•", dots) + string(runes[n-2:])
}
