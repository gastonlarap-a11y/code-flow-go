#!/bin/bash
#
# Installs CodeFlow on macOS without the first-launch refusal.
#
# # Why this exists
#
# The published builds are ad-hoc signed and not notarized (MIGRATION-GO.md §14 D4 (a)). That on
# its own is not what stops them: an ad-hoc bundle launches fine. What stops them is
# `com.apple.quarantine`, the attribute a **browser** writes on everything it downloads. Gatekeeper
# evaluates a quarantined bundle on first launch, an unnotarized one fails that evaluation, and the
# user is told macOS "could not verify" it.
#
# `curl` is not a LaunchServices client and writes no such attribute. So this script does not work
# around Gatekeeper — it never creates the flag that summons it. There is no blind `xattr -d` here:
# the only attribute this script ever removes is on a **temporary copy** of a disk image whose
# SHA-256 it has just matched against the digest published beside it, which is the same contract
# the in-app updater enforces (BOOT-021, backend/update/digest.go) and the release workflow checks
# before publishing.
#
# # Usage
#
#   curl -fsSL https://raw.githubusercontent.com/gastonlarap-a11y/code-flow-go/main/scripts/install-macos.sh | bash
#   ./install-macos.sh v3.3.0                  # a specific release instead of the latest
#   ./install-macos.sh --prefix ~/Applications # somewhere other than /Applications
#   CODEFLOW_DMG=~/Downloads/CodeFlow-3.3.0-arm64.dmg ./install-macos.sh   # a disk image you have
#
# The last form is what the release workflow smoke-tests, and what turns an already-downloaded
# (and therefore quarantined) disk image into a working install without downloading it again.

set -euo pipefail

# The feed this app's releases are published to. **It is `code-flow-go`, not `code-flow`** — the
# same distinction, and the same cutover, as `update.GitHubFeedURL` in backend/update/check.go:32.
# When that constant moves to `code-flow`, this one moves with it.
REPO="gastonlarap-a11y/code-flow-go"
APP="CodeFlow"

PREFIX="/Applications"
TAG="latest"
DMG="${CODEFLOW_DMG:-}"

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

usage() {
	cat <<'EOF'
Usage: install-macos.sh [vX.Y.Z] [--prefix DIR]

  vX.Y.Z          the release to install (default: the latest published one)
  --prefix DIR    where to put CodeFlow.app (default: /Applications)

Environment:
  CODEFLOW_DMG    install from this disk image instead of downloading one. Its digest is still
                  checked, against <image>.sha256 beside it or the one published for the release.
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
		--prefix)
			[ $# -ge 2 ] || die "--prefix needs a directory"
			PREFIX="$2"
			shift 2
			;;
		-h | --help)
			usage
			exit 0
			;;
		v[0-9]*)
			TAG="$1"
			shift
			;;
		*) die "unexpected argument: $1 (try --help)" ;;
	esac
done

[ "$(uname -s)" = "Darwin" ] || die "this installer is for macOS; on Windows run the .exe installer"
# Only an arm64 disk image is published (§14 D8). Saying so beats a bundle that installs and then
# refuses to launch.
[ "$(uname -m)" = "arm64" ] || die "only Apple Silicon (arm64) builds are published; this Mac is $(uname -m)"

# An explicit template rather than a bare `mktemp -d`: BSD mktemp only reads TMPDIR for `-t`, so a
# caller that redirected TMPDIR (CI, a sandbox) would otherwise be ignored.
work="$(mktemp -d "${TMPDIR:-/tmp}/codeflow-install.XXXXXXXX")"
mnt="$work/mnt"
cleanup() {
	# `hdiutil detach` on a mountpoint that was never attached is not an error worth reporting, and
	# this runs on the failure path too.
	if [ -d "$mnt" ]; then
		hdiutil detach "$mnt" -quiet 2>/dev/null || true
	fi
	rm -rf "$work"
}
trap cleanup EXIT

# ---- the disk image and the digest it must match -------------------------------------------------

# `curl` writes no quarantine attribute, so a downloaded image needs nothing removed. A local one
# handed in through CODEFLOW_DMG usually does carry it, and that copy is dealt with below.
if [ -n "$DMG" ]; then
	[ -f "$DMG" ] || die "CODEFLOW_DMG is set to $DMG, which is not a file"
	image="$work/$(basename "$DMG")"
	cp "$DMG" "$image"

	if [ -f "$DMG.sha256" ]; then
		digest_file="$DMG.sha256"
	else
		digest_file="$work/published.sha256"
		version="${TAG#v}"
		[ "$TAG" != "latest" ] ||
			die "$DMG has no .sha256 beside it; pass the release it came from (e.g. v3.3.0) so its published digest can be fetched"
		say "Fetching the published digest for $TAG..."
		curl -fsSL --proto '=https' --tlsv1.2 \
			"https://github.com/$REPO/releases/download/$TAG/$APP-$version-arm64.dmg.sha256" \
			-o "$digest_file" || die "no published digest for $TAG"
	fi
else
	if [ "$TAG" = "latest" ]; then
		say "Resolving the latest release..."
		# The API rather than a guessed name: the artefact name carries the version (§5.5), so the
		# version has to be known before the URL can be built. Parsed with sed because `jq` is not
		# on a stock macOS.
		TAG="$(curl -fsSL --proto '=https' --tlsv1.2 "https://api.github.com/repos/$REPO/releases/latest" |
			sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)"
		[ -n "$TAG" ] || die "could not read the latest release of $REPO (rate-limited, or no release published)"
	fi
	version="${TAG#v}"
	name="$APP-$version-arm64.dmg"
	image="$work/$name"
	digest_file="$work/$name.sha256"

	say "Downloading $name..."
	base="https://github.com/$REPO/releases/download/$TAG"
	curl -fL --proto '=https' --tlsv1.2 --progress-bar "$base/$name" -o "$image" ||
		die "could not download $name from $TAG"
	curl -fsSL --proto '=https' --tlsv1.2 "$base/$name.sha256" -o "$digest_file" ||
		die "could not download the digest for $name; refusing to install unverified"
fi

# Compared by digest rather than by `shasum -c`, which matches on the file name recorded in the
# .sha256 — a name a locally renamed download ("CodeFlow-3.3.0-arm64 (2).dmg") no longer has.
expected="$(awk 'NR==1 {print $1}' "$digest_file")"
case "$expected" in
	[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]*)
		[ "${#expected}" -eq 64 ] || die "$digest_file does not hold a SHA-256"
		;;
	*) die "$digest_file does not hold a SHA-256" ;;
esac

actual="$(shasum -a 256 "$image" | awk '{print $1}')"
[ "$actual" = "$expected" ] || die "the disk image does not match its published digest
  expected $expected
  actual   $actual
Nothing was installed. Download it again, or report it if it keeps happening."
say "Digest verified."

# Only now, and only on this temporary copy: an image whose contents are known to be exactly what
# was published. The file the user downloaded is left untouched.
if xattr -p com.apple.quarantine "$image" >/dev/null 2>&1; then
	xattr -d com.apple.quarantine "$image"
	say "Cleared the browser's quarantine flag from the verified copy (your original is untouched)."
fi

# ---- install ------------------------------------------------------------------------------------

dest="$PREFIX/$APP.app"

# Replacing a bundle out from under a running app leaves it running against files that no longer
# exist, and the relaunch is the user's anyway.
if pgrep -x "$APP" >/dev/null 2>&1; then
	die "$APP is running; quit it and run this again"
fi

mkdir -p "$PREFIX" 2>/dev/null || die "cannot create $PREFIX"
[ -w "$PREFIX" ] || die "$PREFIX is not writable by $(id -un); re-run with sudo, or pass --prefix ~/Applications"

mkdir -p "$mnt"
# macOS 26 deprecates this spelling in favour of `diskutil image attach --mountPoint`, and says so
# on stderr unless `-quiet` is passed. `hdiutil` is kept anyway: `diskutil image` does not exist on
# macOS 15, which is what the release workflow's runner is, and a second code path for a warning
# that is already suppressed buys nothing. Revisit if `hdiutil attach` is actually removed.
hdiutil attach "$image" -mountpoint "$mnt" -nobrowse -readonly -quiet ||
	die "could not mount the disk image"
[ -d "$mnt/$APP.app" ] || die "the disk image holds no $APP.app"

# The old bundle is moved aside rather than written over: `ditto` merges, and a merge of two
# versions leaves files the new signature does not seal, which breaks `codesign --verify`. Moved
# rather than deleted so a failure mid-copy can put it back.
previous=""
if [ -e "$dest" ]; then
	previous="$work/previous-$APP.app"
	mv "$dest" "$previous"
fi
restore() {
	if [ -n "$previous" ] && [ -e "$previous" ] && [ ! -e "$dest" ]; then
		mv "$previous" "$dest"
	fi
	cleanup
}
trap restore EXIT

say "Installing to $dest..."
# `ditto` rather than `cp -R`: it preserves the extended attributes and resource forks the code
# signature seals over, which is what keeps `codesign --verify` passing on the copy.
ditto "$mnt/$APP.app" "$dest"

hdiutil detach "$mnt" -quiet
rm -rf "$mnt"

# ---- prove the promise --------------------------------------------------------------------------

codesign --verify --deep --strict "$dest" ||
	die "the installed bundle does not verify; it has been left at $dest for inspection"

# The whole point of the script. If this ever finds the attribute, something upstream put it there
# and the user is about to be refused — better to say so than to silently remove it.
if xattr -p com.apple.quarantine "$dest" >/dev/null 2>&1; then
	die "the installed bundle is quarantined, which this script exists to prevent; please report it"
fi

previous=""
trap cleanup EXIT

# Read back rather than echoed from $TAG, which is "latest" when CODEFLOW_DMG supplied its own
# digest and no release was ever resolved. This is also the bundle's own answer to "which version
# did I just install?", the one Finder's Get Info shows.
installed="$(defaults read "$dest/Contents/Info" CFBundleShortVersionString 2>/dev/null || true)"

say ""
say "$APP ${installed:-${TAG#v}} is installed at $dest."
say "Open it from Finder or with: open -a \"$dest\""
