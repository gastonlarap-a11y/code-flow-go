// Package update is the updater: what version is running, whether a newer one was published, and
// fetching it with its published digest before handing it to the operating system.
//
// Three commands and one event (`update:progress`). Everything here is governed by BOOT-021 and
// §2.9 of MIGRATION-GO.md, and the part worth stating twice is the trust boundary: the renderer
// hands back an asset url and an asset name, and **the digest is never taken from the caller**. It
// is read from the release, sidecar-side, so that choosing the bytes and choosing the expectation
// they are checked against are not the same privilege.
//
// # Why not Wails' pkg/updater
//
// It swaps a binary or an .app out of a zip and publishes its own events under `wails:updater:*`
// with its own window. The renderer's `updateStore.ts` and `lib/bridge/updater.ts` were written
// against a different contract — snake_case `Availability`, `update:progress`, an install kind that
// differs per platform — and 2.7.x's own updater has to keep finding what it expects on the
// release for anyone upgrading into 3.0. Revisiting it is a 3.x decision, not a port decision.
package update

import "strings"

// Version comparison for the release feed (§2.9).
//
// Not semver in full, and deliberately so: the tags this reads are whatever a human typed into a
// GitHub release, so the parser's job is to be unsurprising on the shapes that actually occur and
// to never fail on the ones that do not. A tag nobody here chose the shape of compares as 0.0.0
// rather than raising — refusing to answer "is there an update?" because a tag was odd is a worse
// outcome than answering "no".

// releaseVersion is a tag reduced to what ordering actually depends on.
type releaseVersion struct {
	// segments are the dot-separated numbers of the core, in order. Missing ones count as zero at
	// comparison time rather than being padded here, because the two versions being compared can
	// have different lengths and neither is authoritative about how long a version "should" be.
	segments []int
	// prerelease is everything after the first hyphen, lowercased. Only its presence is compared:
	// see isNewerThan.
	prerelease string
}

// parseVersion reduces a tag to its comparable parts.
//
// Three things are stripped, in this order and for these reasons:
//
//   - a leading `v`, because tags are written `v3.0.1` and versions are reported `3.0.1`;
//   - `+build` metadata, because semver says it takes no part in ordering and two builds of one
//     version are the same version;
//   - everything from the first `-`, which is the pre-release and is compared separately.
func parseVersion(tag string) releaseVersion {
	core := strings.TrimSpace(tag)
	core = strings.TrimPrefix(core, "v")
	core = strings.TrimPrefix(core, "V")

	if plus := strings.IndexByte(core, '+'); plus >= 0 {
		core = core[:plus]
	}

	prerelease := ""
	if hyphen := strings.IndexByte(core, '-'); hyphen >= 0 {
		prerelease = strings.ToLower(core[hyphen+1:])
		core = core[:hyphen]
	}

	parts := strings.Split(core, ".")
	segments := make([]int, 0, len(parts))
	for _, part := range parts {
		segments = append(segments, leadingNumber(part))
	}
	return releaseVersion{segments: segments, prerelease: prerelease}
}

// leadingNumber reads the digits a segment starts with, and answers 0 when it starts with none.
//
// It caps rather than overflowing: a tag is not a place to take an integer overflow, and any
// version number past a million is already outside the range where "which is bigger" is a question
// about releases rather than about typos.
const maxSegment = 1_000_000

func leadingNumber(segment string) int {
	value := 0
	for i := 0; i < len(segment); i++ {
		if segment[i] < '0' || segment[i] > '9' {
			break
		}
		if value >= maxSegment {
			return maxSegment
		}
		value = value*10 + int(segment[i]-'0')
	}
	if value > maxSegment {
		return maxSegment
	}
	return value
}

// IsNewer reports whether the candidate tag names a release newer than the running version.
//
// The comparison is numeric segment by segment — `1.10.0` beats `1.2.0`, which a string comparison
// gets backwards — with a missing segment counting as zero, so `1.2` and `1.2.0` are the same
// release.
//
// When the cores are equal the pre-release decides, and only in the one direction the specification
// fixes: a pre-release ranks below the release it precedes. `3.0.0` is therefore newer than
// `3.0.0-beta.1`, and `3.0.0-beta.2` is **not** offered over `3.0.0-beta.1`. That asymmetry is
// deliberate rather than an omission: ordering two pre-releases of the same core means deciding
// whether `beta.2` beats `beta.10` and whether `rc` beats `beta`, and every answer to that is a
// convention this project has never stated. Not offering an update is the safe half of the
// uncertainty — the user stays on what they installed — and nothing in the feed has ever produced
// the case.
func IsNewer(candidate, current string) bool {
	return parseVersion(candidate).isNewerThan(parseVersion(current))
}

func (v releaseVersion) isNewerThan(other releaseVersion) bool {
	length := max(len(v.segments), len(other.segments))
	for i := range length {
		mine, theirs := v.segmentAt(i), other.segmentAt(i)
		if mine != theirs {
			return mine > theirs
		}
	}

	// Equal cores: the only ordering fixed by the specification is that a final release beats the
	// pre-releases leading up to it.
	return v.prerelease == "" && other.prerelease != ""
}

func (v releaseVersion) segmentAt(i int) int {
	if i >= len(v.segments) {
		return 0
	}
	return v.segments[i]
}

// normalizeVersion is the display form of a tag: what IsNewer compares, written back out.
//
// The check reports the running version and the offered one side by side, and the "what's new"
// modal renders both in one sentence. `current_version` is always bare — it is what the build
// stamped in — so leaving the tag's `v` on would put "3.0.0 → v3.0.1" in front of the user. The
// pre-release and the build metadata are kept: they are part of what was published, and unlike the
// prefix they carry information.
func normalizeVersion(tag string) string {
	trimmed := strings.TrimSpace(tag)
	trimmed = strings.TrimPrefix(trimmed, "v")
	return strings.TrimPrefix(trimmed, "V")
}
