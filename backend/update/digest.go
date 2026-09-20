package update

import "strings"

// Reading a published `.sha256` file (BOOT-021).
//
// The format is whatever `shasum -a 256` on macOS and `sha256sum` on Windows write: one entry per
// line, the hex digest, whitespace, then the name — with GNU's `*` binary marker in front of the
// name when it was hashed in binary mode.

// digestEntry is one line of a checksum file.
type digestEntry struct {
	digest string
	name   string
}

// DigestFor returns the digest recorded for an artefact, and whether one was found.
//
// # The single-entry rule
//
// A file with exactly one entry yields its digest **without checking the name**. That is not a
// convenience, it is the fix for a release that refused every Windows update: v1.7.5 shipped
// without it, GitHub rewrites spaces to dots when it stores a release asset, so the API answered
// `CodeFlow.1.7.5.exe` while `sha256sum` had recorded `CodeFlow 1.7.5.exe`, and the two never
// matched. The binding is already established by how the file was fetched — the caller asked for
// `<asset>.sha256` and got this — so the name inside it is corroboration, not the evidence.
//
// `win.artifactName` now names artefacts without spaces, which fixes it at the source. The rule
// stays anyway: a verifier whose failure mode is "silently refuse every update" must not depend on
// that holding.
//
// Where there are several entries the name has to be matched, because then the file is about more
// than one artefact and picking the wrong line means checking the right bytes against the wrong
// expectation. Only the last path segment is compared — a checksum recorded from inside a `dist/`
// directory names it that way — and the comparison is case-insensitive, matching how the file
// itself was found.
func DigestFor(content, assetName string) (string, bool) {
	entries := parseDigestFile(content)

	switch len(entries) {
	case 0:
		return "", false
	case 1:
		return entries[0].digest, true
	}

	want := strings.ToLower(assetName)
	for _, entry := range entries {
		if strings.ToLower(lastSegment(entry.name)) == want {
			return entry.digest, true
		}
	}
	return "", false
}

func parseDigestFile(content string) []digestEntry {
	lines := strings.Split(content, "\n")
	entries := make([]digestEntry, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// The split is on the *first* whitespace run only, never on every run: an asset name may
		// contain spaces — that is the whole v1.7.5 story — and `strings.Fields` would cut
		// `CodeFlow 1.7.5.exe` into two fields and lose half the name.
		digest, rest, found := cutOnWhitespace(trimmed)
		if !found {
			// A digest on its own, with no name beside it. Kept as an entry: in a single-entry file
			// it is exactly what is wanted, and in a multi-entry one it simply matches nothing.
			entries = append(entries, digestEntry{digest: digest})
			continue
		}

		// GNU's binary marker belongs to the format, not to the name.
		entries = append(entries, digestEntry{
			digest: digest,
			name:   strings.TrimPrefix(strings.TrimSpace(rest), "*"),
		})
	}
	return entries
}

func cutOnWhitespace(line string) (before, after string, found bool) {
	for i := 0; i < len(line); i++ {
		if line[i] == ' ' || line[i] == '\t' {
			return line[:i], strings.TrimLeft(line[i:], " \t"), true
		}
	}
	return line, "", false
}

// lastSegment drops any recorded directory. Both separators, because the file may have been
// written on either platform and is read on either.
func lastSegment(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		return name[i+1:]
	}
	return name
}
