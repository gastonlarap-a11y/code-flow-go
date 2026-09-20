package update

import (
	"runtime"
	"strings"
)

// The release payload and the choice of which artefact this machine is offered (BOOT-021).

// Release is GitHub's `releases/latest` response, decoded by GitHub's own field names.
//
// It never crosses to the renderer — `Availability` does — so the tags here are the API's spelling
// rather than this project's wire convention, and the struct carries only the fields something
// reads. `draft` is one of them: a draft release is visible to a token that can see the repository
// and is not a published update.
type Release struct {
	TagName     string  `json:"tag_name"`
	Name        string  `json:"name"`
	Body        string  `json:"body"`
	PublishedAt string  `json:"published_at"`
	Draft       bool    `json:"draft"`
	Prerelease  bool    `json:"prerelease"`
	Assets      []Asset `json:"assets"`
}

// Asset is one file published on a release.
type Asset struct {
	Name string `json:"name"`
	// URL is the browser download url rather than the API's asset url. Both work for a public
	// repository; this one is also what the renderer is handed back and passes into
	// `update_download`, so keeping a single spelling means the two halves cannot disagree about
	// which url was checked.
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

const (
	// windowsInstallerMarker is the `-Setup-` that `win.artifactName` puts in the NSIS installer's
	// name, and the only thing that tells it apart from the portable build beside it.
	windowsInstallerMarker = "-setup-"

	// digestSuffix is what a published checksum is named: the artefact's own name with this
	// appended. One file per artefact rather than a shared SHA256SUMS, because the two installers
	// are built on different machines at different times and a shared file would be two uploads
	// racing (BOOT-021).
	digestSuffix = ".sha256"

	// blockmapSuffix is electron-builder's differential-download index. It sits beside the
	// installer, it is not one, and nothing here downloads it.
	blockmapSuffix = ".blockmap"
)

// InstallKind says what happens after the download.
//
// `auto` means the artefact is an installer this platform can run: Windows gets the NSIS installer
// started for it. `manual` means the artefact is handed over and the user finishes the job — on
// macOS the `.dmg` is mounted and they drag the app across. The renderer branches on it to decide
// whether to offer *Restart*, and telling a Mac user to restart would be an untruth.
func InstallKind() string { return installKindFor(runtime.GOOS) }

func installKindFor(goos string) string {
	if goos == "windows" {
		return "auto"
	}
	return "manual"
}

// AssetFor picks the artefact this platform should be offered, or nil when the release publishes
// none for it.
//
// Windows takes the **installer**, not merely the first `.exe`. A Windows release carries a
// portable build too, and with the install kind at `auto` the chosen artefact is executed — handing
// over the portable one launches a loose copy of the new version and leaves the installed build
// untouched, an update that appears to work and updates nothing. The installer is identified by the
// `-Setup-` its artifact name carries; a release older than v1.7.6 has no marker, so an unmarked
// lone `.exe` is still offered rather than refused.
//
// Everything else takes the `.dmg`. That is macOS in practice and the literal 2.x behaviour: there
// have never been Linux targets, and inventing a `.AppImage` branch here would be a guess about a
// release shape nothing publishes.
func AssetFor(release Release) *Asset { return assetFor(release, runtime.GOOS) }

func assetFor(release Release, goos string) *Asset {
	if goos != "windows" {
		return firstWithSuffix(release.Assets, ".dmg")
	}

	installers := candidates(release.Assets, ".exe")
	for i := range installers {
		if strings.Contains(strings.ToLower(installers[i].Name), windowsInstallerMarker) {
			return installers[i]
		}
	}
	if len(installers) > 0 {
		return installers[0]
	}
	return nil
}

func firstWithSuffix(assets []Asset, suffix string) *Asset {
	found := candidates(assets, suffix)
	if len(found) == 0 {
		return nil
	}
	return found[0]
}

// candidates are the assets whose name ends in the given extension.
//
// The two exclusions are redundant with the suffix test — neither `.exe.blockmap` nor
// `.exe.sha256` ends in `.exe` — and they are written anyway, because the rule being relied on is
// "these are never artefacts" and a reader should not have to re-derive it from string suffixes
// every time this is read.
func candidates(assets []Asset, suffix string) []*Asset {
	found := make([]*Asset, 0, len(assets))
	for i := range assets {
		name := strings.ToLower(assets[i].Name)
		if strings.HasSuffix(name, blockmapSuffix) || strings.HasSuffix(name, digestSuffix) {
			continue
		}
		if strings.HasSuffix(name, suffix) {
			found = append(found, &assets[i])
		}
	}
	return found
}

// digestAssetFor finds the checksum published beside an artefact.
//
// The match is on the whole name — `<asset>.sha256` — and case-insensitive, which is the same rule
// the digest file's own entries are matched by. A release that publishes no such file is refused
// rather than installed unverified, and refused *before* anything is downloaded: there is no point
// pulling ninety megabytes into someone's Downloads folder to then delete them.
func digestAssetFor(release Release, assetName string) *Asset {
	want := strings.ToLower(assetName) + digestSuffix
	for i := range release.Assets {
		if strings.ToLower(release.Assets[i].Name) == want {
			return &release.Assets[i]
		}
	}
	return nil
}
