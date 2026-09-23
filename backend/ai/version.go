package ai

import (
	"context"
	"strings"
	"sync"
	"unicode"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

// Reading an engine CLI's version (AI-016).
//
// Cached per binary for the life of the process, **including a failed probe**. Without that, an
// engine that is not installed is re-spawned on every single chat turn — and spawning something
// that is not there is not free on Windows, where each attempt walks the whole search path.

// maxVersionChars caps what is stored. A CLI that prints a paragraph on `--version` should not put
// a paragraph in every chat turn's row.
const maxVersionChars = 40

// versionCache remembers what each binary answered. The empty string means "asked, and there is no
// answer" — which is why it is a cache of results rather than of successes.
var versionCache sync.Map

// EngineVersion reports a CLI's version, or the empty string.
//
// Subprocess engines only: an HTTP endpoint has no version to read, and asking would mean a
// request per chat turn for a field the user barely looks at.
func EngineVersion(ctx context.Context, provider, binary string) string {
	if EngineFor(provider).Transport() != TransportSubprocess || binary == "" {
		return ""
	}
	if cached, found := versionCache.Load(binary); found {
		if version, ok := cached.(string); ok {
			return version
		}
	}

	version := probeVersion(ctx, binary)
	versionCache.Store(binary, version)
	return version
}

func probeVersion(ctx context.Context, binary string) string {
	cmd := proc.Command(ctx, ResolveBinary(binary, SearchDirs()), "--version")
	cmd.Env = proc.Environment(SearchDirs())

	// CombinedOutput, because not every CLI prints its banner on stdout — checking only one stream
	// reports "no version" for an engine that answered perfectly well on the other.
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	return parseVersion(proc.DecodeLossyUTF8(out))
}

// parseVersion pulls a version out of a CLI's banner.
//
// The first whitespace-separated token that looks like one — a leading `v` stripped, contains a
// dot, starts with a digit — because these banners are prose: "claude 2.1.3 (build 44)",
// "opencode version 1.18.7". Falling back to the whole first line keeps something useful for a CLI
// whose banner has no recognisable token at all.
func parseVersion(output string) string {
	firstLine := ""
	for line := range strings.SplitSeq(output, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			firstLine = trimmed
			break
		}
	}
	if firstLine == "" {
		return ""
	}

	for token := range strings.FieldsSeq(firstLine) {
		candidate := strings.TrimPrefix(token, "v")
		if strings.Contains(candidate, ".") && candidate != "" &&
			unicode.IsDigit(rune(candidate[0])) {
			return cap40(candidate)
		}
	}
	return cap40(firstLine)
}

func cap40(text string) string {
	if runes := []rune(text); len(runes) > maxVersionChars {
		return string(runes[:maxVersionChars])
	}
	return text
}
