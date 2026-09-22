package update

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/platform"
)

// Downloading an artefact and checking it against its published digest (BOOT-021).

const (
	// chunkBytes is the read size, carried over from 2.x. It is 80 KiB, which is neither a round
	// number of pages nor arbitrary: it is what the C# `Stream.CopyToAsync` default was set to, and
	// keeping it keeps the number of progress events per megabyte the same as the renderer's
	// progress bar was tuned against.
	chunkBytes = 81_920

	// progressEvery is how much has to arrive before the renderer is told again. Every chunk would
	// be a hundred and fifty events per megabyte crossing the bridge to move a bar by a pixel.
	progressEvery = 256 * 1024

	// maxAssetBytes caps what will be written to disk. The installers are around a hundred
	// megabytes; a gigabyte is well past any plausible one and well short of filling a disk.
	maxAssetBytes = 1 << 30
)

// Progress is the `update:progress` payload.
//
// `downloaded` is the running total rather than the delta. The renderer's bridge converts it to the
// deltas `updateStore` counts, which is the one place that conversion has to exist for the store to
// stay untouched.
type Progress struct {
	Downloaded int64 `json:"downloaded"`
	Total      int64 `json:"total"`
	Done       bool  `json:"done"`
}

// ProgressEvent is the event name the renderer subscribes to before it sends the command.
const ProgressEvent = "update:progress"

// ErrUnverified is what a refused artefact carries. Every path to it leaves nothing behind: either
// nothing was downloaded, or what was downloaded has been deleted.
var ErrUnverified = errors.New("the download could not be verified")

// Download fetches an artefact, verifies it against the digest the release published, and hands it
// to the operating system.
//
// # The order is the security property
//
// The digest is resolved **first**, from the release, before a single byte of the artefact is
// requested. Two things follow from that. A release with no published checksum is refused without
// downloading ninety megabytes into someone's Downloads folder to then delete them. And the
// expectation the bytes are checked against never came from the caller: the renderer chooses the
// url and the name, the sidecar chooses what they have to hash to. An attacker who reached the
// renderer can therefore choose which bytes are fetched and cannot choose the answer they are
// checked against, which is the whole of BOOT-021.
//
// A mismatch deletes the file rather than leaving a rejected installer one double-click away in
// Downloads.
func (s *Service) Download(ctx context.Context, assetURL, assetName string) (string, error) {
	name, err := safeAssetName(assetName)
	if err != nil {
		return "", err
	}

	token, err := s.token(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: no GitHub credential is available to read the release", ErrUnverified)
	}

	expected, err := s.expectedDigest(ctx, token, name)
	if err != nil {
		return "", err
	}

	// Every write and every delete below goes through this root rather than through a path.
	//
	// It is what makes "inside Downloads" a property of the handle instead of a property of a
	// string that was checked once: `safeAssetName` already refused a name that is a path, and
	// `os.Root` additionally refuses to follow a symlink out of the directory — the case where
	// `~/Downloads/CodeFlow-Setup-3.0.1-x64.exe` already exists and points somewhere else, which a
	// name check cannot see at all.
	if err := os.MkdirAll(s.downloads, platform.DirPerm); err != nil {
		return "", fmt.Errorf("prepare the downloads directory: %w", err)
	}
	root, err := os.OpenRoot(s.downloads)
	if err != nil {
		return "", fmt.Errorf("open the downloads directory: %w", err)
	}
	defer func() { _ = root.Close() }()

	destination := filepath.Join(s.downloads, name)
	written, err := s.fetchToFile(ctx, assetURL, token, root, name)
	if err != nil {
		// Whatever was written is a partial, unverified artefact. It goes.
		_ = root.Remove(name)
		return "", err
	}

	if subtle.ConstantTimeCompare(written, expected) != 1 {
		if removeErr := root.Remove(name); removeErr != nil {
			// Saying so matters more than the delete succeeding: the refusal is only half the
			// protection if the file is still sitting there and the user is never told.
			return "", fmt.Errorf("%w: %s does not match the digest the release published, and it could not be deleted: %w",
				ErrUnverified, destination, removeErr)
		}
		return "", fmt.Errorf("%w: %s does not match the digest the release published and has been deleted",
			ErrUnverified, name)
	}

	if err := s.handOff(ctx, destination); err != nil {
		// The artefact is verified and on disk; only the install or the shell refused it. The error
		// names the path for that reason — it is a different situation from a failed download, and
		// the file is there and good.
		return "", err
	}
	return destination, nil
}

// expectedDigest reads the checksum the release published for this artefact, as raw bytes.
func (s *Service) expectedDigest(ctx context.Context, token, assetName string) ([]byte, error) {
	release, reason := s.latestRelease(ctx, token)
	if reason != "" {
		return nil, fmt.Errorf("%w: the release could not be read (%s)", ErrUnverified, reason)
	}

	digestAsset := digestAssetFor(release, assetName)
	if digestAsset == nil {
		return nil, fmt.Errorf("%w: this release publishes no %s%s", ErrUnverified, assetName, digestSuffix)
	}

	content, err := s.fetchText(ctx, digestAsset.URL, token, maxDigestBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnverified, err)
	}

	recorded, found := DigestFor(content, assetName)
	if !found {
		return nil, fmt.Errorf("%w: %s does not record a digest for %s", ErrUnverified, digestAsset.Name, assetName)
	}

	expected, err := hex.DecodeString(strings.TrimSpace(recorded))
	if err != nil || len(expected) != sha256.Size {
		return nil, fmt.Errorf("%w: %s does not contain a SHA-256 digest", ErrUnverified, digestAsset.Name)
	}
	return expected, nil
}

// fetchToFile streams the artefact to disk, hashing it on the way past and reporting progress.
//
// Hashed while streaming rather than re-read afterwards: the file is around a hundred megabytes and
// reading it twice to avoid holding a hash state is the wrong trade.
func (s *Service) fetchToFile(ctx context.Context, url, token string, root *os.Root, name string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build the request: %w", err)
	}
	s.applyGitHubHeaders(request, token, "application/octet-stream")

	response, err := s.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download the update: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("downloading the update returned %d", response.StatusCode)
	}

	file, err := root.Create(name)
	if err != nil {
		return nil, fmt.Errorf("create %s in the downloads directory: %w", name, err)
	}

	digest, err := s.copyWithProgress(file, response.Body, response.ContentLength)
	// Closed before anything else looks at the file: on Windows an open handle is a file that
	// cannot be deleted, and the caller deletes this one whenever the digest does not match.
	if closeErr := file.Close(); closeErr != nil && err == nil {
		err = fmt.Errorf("finish writing %s: %w", name, closeErr)
	}
	if err != nil {
		return nil, err
	}
	return digest, nil
}

// copyWithProgress is the transfer loop: fixed-size reads, a running hash, and an event whenever
// enough has arrived to be worth telling the renderer about.
//
// The final event always fires, with `done` set, even when the transfer was shorter than one
// reporting interval — that event is what moves the store from `downloading` to `ready`, and a
// small artefact must not leave the bar stuck.
func (s *Service) copyWithProgress(dst io.Writer, src io.Reader, total int64) ([]byte, error) {
	hash := sha256.New()
	buffer := make([]byte, chunkBytes)

	var downloaded, reported int64
	for {
		read, err := src.Read(buffer)
		if read > 0 {
			if downloaded+int64(read) > maxAssetBytes {
				return nil, fmt.Errorf("the update is larger than %d bytes", int64(maxAssetBytes))
			}
			chunk := buffer[:read]
			if _, writeErr := dst.Write(chunk); writeErr != nil {
				return nil, fmt.Errorf("write the update: %w", writeErr)
			}
			// A hash.Hash never returns an error, by its own documented contract.
			_, _ = hash.Write(chunk)

			downloaded += int64(read)
			if downloaded-reported >= progressEvery {
				reported = downloaded
				s.emit(Progress{Downloaded: downloaded, Total: total, Done: false})
			}
		}

		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("download the update: %w", err)
		}
	}

	// `total` is what the far side announced; once the transfer is over, what actually arrived is
	// the better number, and it is the one the renderer's bar has been counting up to.
	s.emit(Progress{Downloaded: downloaded, Total: downloaded, Done: true})
	return hash.Sum(nil), nil
}

func (s *Service) emit(progress Progress) {
	if s.emitter == nil {
		return
	}
	s.emitter.Emit(ProgressEvent, progress)
}

// safeAssetName reduces the caller's asset name to a bare filename, or refuses it.
//
// The renderer passes back what `update_check` handed it, so in the normal path this changes
// nothing. It is here because the name decides *where* the bytes land, and BOOT-021's reasoning —
// the far side of the bridge must not get to choose what the download is checked against — applies
// just as directly to the path it is written to. Without this, an `assetName` of `../.zshrc` would
// have the updater write an attacker-chosen file outside the Downloads folder, with the digest
// check passing because the digest is about the bytes and not about where they went.
func safeAssetName(assetName string) (string, error) {
	name := strings.TrimSpace(assetName)
	if name == "" {
		return "", errors.New("missing required parameter 'assetName'")
	}
	// Both separators, whichever platform this runs on: a `\` is an ordinary character in a POSIX
	// filename, so `filepath.Base` alone would keep `..\..\x` whole on macOS and write it as one
	// very oddly named file — but a name that contains one is not an asset name either way.
	if strings.ContainsAny(name, `/\`) || name != filepath.Base(name) || name == "." || name == ".." {
		return "", fmt.Errorf("%w: %q is not an asset name", ErrUnverified, assetName)
	}
	return name, nil
}
