package security_test

import (
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge/jsonwire"
	"github.com/gastonlarap-a11y/code-flow/backend/git"
	"github.com/gastonlarap-a11y/code-flow/backend/security"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// addedLine builds the one-file, one-hunk, one-line diff every secret_scan vector uses.
func addedLine(origin, content string) []git.FileDiff {
	path, lineNo := "config.ts", int64(42)
	return []git.FileDiff{{
		NewPath: &path,
		Status:  "modified",
		Hunks: []git.DiffHunk{{
			Header: "@@",
			Lines:  []git.DiffLine{{Origin: origin, Content: content, NewLineNo: &lineNo}},
		}},
	}}
}

// The eight secret_scan vectors, as one table. Their previews are asserted byte for byte: the mask
// is what the user sees, and the whole point is that it reveals enough to recognise a key and not
// enough to use one.
func TestScanDiffVectors(t *testing.T) {
	for _, vector := range []struct {
		name     string
		origin   string
		content  string
		expected []security.SecretHit
	}{
		{
			name: "detects-github-token", origin: "+",
			content: `const t = "ghp_0123456789abcdefghijklmnopqrstuvwxyz";`,
			expected: []security.SecretHit{{
				File: "config.ts", Line: 42, Rule: "github-token", RuleName: "GitHub token",
				Severity: "critical", Preview: "ghp••••••••••••••••yz",
			}},
		},
		{
			name: "detects-aws-access-key", origin: "+",
			content: "key = AKIAIOSFODNN7EXAMPLE",
			expected: []security.SecretHit{{
				File: "config.ts", Line: 42, Rule: "aws-access-key", RuleName: "AWS access key id",
				Severity: "critical", Preview: "AKI•••••••••••••••LE",
			}},
		},
		{
			name: "detects-private-key-pem-header", origin: "+",
			content: "-----BEGIN RSA PRIVATE KEY-----",
			expected: []security.SecretHit{{
				File: "config.ts", Line: 42, Rule: "private-key", RuleName: "Private key (PEM)",
				Severity: "critical", Preview: "---••••••••••••••••--",
			}},
		},
		{
			// Same content as the first case, but a context line: it was already in the repository
			// and is not this gate's concern.
			name: "ignores-context-lines", origin: " ",
			content: `const t = "ghp_0123456789abcdefghijklmnopqrstuvwxyz";`,
		},
		{
			name: "skips-placeholder-your-prefix", origin: "+",
			content: `password = "your-password-here"`,
		},
		{
			name: "skips-placeholder-env-var-interpolation", origin: "+",
			content: `token = "${GITHUB_TOKEN}"`,
		},
		{
			name: "flags-real-hardcoded-password", origin: "+",
			content: `password = "hunter2correcthorse"`,
			expected: []security.SecretHit{{
				File: "config.ts", Line: 42, Rule: "hardcoded-secret",
				RuleName: "Hardcoded secret assignment",
				Severity: "warning", Preview: "hun••••••••••••••se",
			}},
		},
		{
			name: "clean-line-has-no-hits", origin: "+",
			content: "const total = a + b; // sums the values",
		},
	} {
		t.Run(vector.name, func(t *testing.T) {
			hits := security.ScanDiff(addedLine(vector.origin, vector.content))

			if vector.expected == nil {
				assert.Empty(t, hits)
				assert.NotNil(t, hits, "the renderer maps over it without guarding")
				return
			}
			assert.Equal(t, vector.expected, hits)
			require.NoError(t, jsonwire.AssertNoNilSlices(hits))
		})
	}
}

// Order is significant and an earlier rule shadows a later one. A GitHub token assigned to a
// variable called `token` matches both rule 4 and rule 15, and must report as the specific one.
func TestTheFirstMatchingRuleWinsAndOnlyOnePerLine(t *testing.T) {
	hits := security.ScanDiff(addedLine("+",
		`const token = "ghp_0123456789abcdefghijklmnopqrstuvwxyz";`))

	require.Len(t, hits, 1, "one line, one row, however many rules it matches")
	assert.Equal(t, "github-token", hits[0].Rule, "the specific rule shadows the generic one")
}

// Only two rules capture a value; the rest report the whole match, prefix included.
func TestTheReportedValueIsTheCaptureWhereThereIsOne(t *testing.T) {
	t.Run("a captured value excludes the assignment", func(t *testing.T) {
		hits := security.ScanDiff(addedLine("+",
			`aws_secret_access_key = "abcdefghijklmnopqrstuvwxyz0123456789ABCD"`))

		require.Len(t, hits, 1)
		assert.Equal(t, "aws-secret-key", hits[0].Rule)
		assert.Equal(t, "abc••••••••••••••••CD", hits[0].Preview, "the key alone, not the line")
	})

	t.Run("no capture means the whole match, prefix and all", func(t *testing.T) {
		hits := security.ScanDiff(addedLine("+",
			"AccountKey=abcdefghijklmnopqrstuvwxyz0123456789ABCDEF=="))

		require.Len(t, hits, 1)
		assert.Equal(t, "azure-storage-key", hits[0].Rule)
		assert.Equal(t, "Acc••••••••••••••••==", hits[0].Preview)
	})
}

// A diff line with no number reports 0 rather than omitting the hit: the file is still worth
// flagging even when the position is unknown.
func TestAHitWithNoLineNumber(t *testing.T) {
	path := "config.ts"
	hits := security.ScanDiff([]git.FileDiff{{
		NewPath: &path,
		Hunks: []git.DiffHunk{{Lines: []git.DiffLine{{
			Origin: "+", Content: "key = AKIAIOSFODNN7EXAMPLE",
		}}}},
	}})

	require.Len(t, hits, 1)
	assert.EqualValues(t, 0, hits[0].Line)
}

// Neither path present should not happen, and reports "?" rather than an empty name — which in the
// report reads as a rendering bug rather than as missing information.
func TestAFileWithNeitherPath(t *testing.T) {
	hits := security.ScanDiff([]git.FileDiff{{
		Hunks: []git.DiffHunk{{Lines: []git.DiffLine{{
			Origin: "+", Content: "key = AKIAIOSFODNN7EXAMPLE",
		}}}},
	}})

	require.Len(t, hits, 1)
	assert.Equal(t, "?", hits[0].File)
}

func TestADeletedFileUsesItsOldPath(t *testing.T) {
	old := "removed.ts"
	hits := security.ScanDiff([]git.FileDiff{{
		OldPath: &old,
		Hunks: []git.DiffHunk{{Lines: []git.DiffLine{{
			Origin: "+", Content: "key = AKIAIOSFODNN7EXAMPLE",
		}}}},
	}})

	require.Len(t, hits, 1)
	assert.Equal(t, "removed.ts", hits[0].File)
}

// The mask is the part that has to be right: it is what reaches a screenshot or a support ticket.
func TestMaskingNeverRevealsAShortValue(t *testing.T) {
	// A two-character value masked as two bullets would tell the reader it was two characters
	// long. The floor of three is what stops that.
	for _, short := range []string{`ab`, `abcdef`} {
		t.Run(short, func(t *testing.T) {
			hits := security.ScanDiff(addedLine("+", `aws_secret_access_key = "`+short+`"`))
			assert.Empty(t, hits, "too short to match the 40-character pattern at all")
		})
	}

	t.Run("a long value stops revealing its length", func(t *testing.T) {
		long := "sk-proj-" + string(make([]byte, 0)) +
			"abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz"
		hits := security.ScanDiff(addedLine("+", "const k = "+long))

		require.Len(t, hits, 1)
		assert.Len(t, []rune(hits[0].Preview), 21,
			"three, sixteen bullets and two — past 21 characters the mask stops growing")
	})
}

// A placeholder verdict on the generic rule ends the line with no hit, because that rule is last.
func TestPlaceholderShapes(t *testing.T) {
	for _, placeholder := range []string{
		`password = "your-password-here"`,
		`token = "${GITHUB_TOKEN}"`,
		`api_key = "{{ vault_key }}"`,
		`secret = "process.env.SECRET"`,
		`password = "os.environ['PW']"`,
		`token = "getenv('TOKEN')"`,
		`password = "changeme123"`,
		`api_key = "EXAMPLE_KEY_VALUE"`,
		`secret = "<your secret here>"`,
		`password = "xxxxxxxxxxxx"`,
		`token = "TODO replace me"`,
	} {
		t.Run(placeholder, func(t *testing.T) {
			assert.Empty(t, security.ScanDiff(addedLine("+", placeholder)),
				"a gate that cries wolf on documentation is one people click through")
		})
	}
}

// Case matters for the syntax markers and not for the English words, which is how they were
// written and is the distinction worth keeping: `Process.Env` is not the same token.
func TestPlaceholderMarkersAreCaseSensitiveAndNeedlesAreNot(t *testing.T) {
	assert.NotEmpty(t, security.ScanDiff(addedLine("+", `password = "Process.Env.SECRET"`)),
		"not the syntax, so not a placeholder")
	assert.Empty(t, security.ScanDiff(addedLine("+", `password = "CHANGEME-NOW"`)),
		"an English needle, matched however it is capitalised")
}

func TestAnEmptyDiffIsAnArray(t *testing.T) {
	hits := security.ScanDiff(nil)

	assert.Empty(t, hits)
	assert.NotNil(t, hits)
}
