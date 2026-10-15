package repomap

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gastonlarap-a11y/code-flow/backend/platform"
)

// The map kept on disk between runs (REPOMAP-008): what each file was when it was last read, and the
// facts read from it, keyed by the file's content so a rename or a revert costs no parse.

// cacheFormat is the layout of the file itself; extractorID, the facts inside it.
const cacheFormat = 1

// seenFile is a file as it was last read. Sum is the content's SHA-256, "" for a file read and found
// not to be code (binary, minified), which is then not read again until it changes.
type seenFile struct {
	Size int64  `json:"size"`
	Mod  int64  `json:"mod"`
	Sum  string `json:"sum"`
}

type cacheDoc struct {
	Format    int                 `json:"format"`
	Extractor string              `json:"extractor"`
	Files     map[string]seenFile `json:"files"`
	Facts     map[string]*Facts   `json:"facts"`
}

// CacheDir is where the maps are kept, under the app's data folder — the app process and the MCP
// server it starts must agree on it.
func CacheDir(base string) string {
	return filepath.Join(base, "codemap", "repomap")
}

// cachePath is where a repository's map is kept: one file per repository, named by its path.
func cachePath(dir, root string) string {
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(dir, hex.EncodeToString(sum[:12])+".json.gz")
}

// loadCache reads a kept map; ok is false for none, an unreadable one, or one another extractor
// wrote — every one of which means reading the repository afresh.
func loadCache(path string) (cacheDoc, bool) {
	file, err := os.Open(path) //nolint:gosec // G304: the app's own cache file, named by cachePath
	if err != nil {
		return cacheDoc{}, false
	}
	defer func() { _ = file.Close() }() // read-only: a close error loses nothing
	reader, err := gzip.NewReader(file)
	if err != nil {
		return cacheDoc{}, false
	}
	var doc cacheDoc
	if json.NewDecoder(reader).Decode(&doc) != nil || doc.Format != cacheFormat || doc.Extractor != extractorID {
		return cacheDoc{}, false
	}
	if doc.Files == nil || doc.Facts == nil {
		return cacheDoc{}, false
	}
	return doc, true
}

// saveCache writes a map, through a temporary file renamed over the old one so a crash mid-write
// leaves the previous map rather than half of this one.
func saveCache(path string, doc cacheDoc) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), platform.DirPerm); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	temp := path + ".tmp-" + strconv.Itoa(os.Getpid())
	file, err := os.OpenFile(temp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, platform.FilePerm) //nolint:gosec // G304: the app's own cache file
	if err != nil {
		return fmt.Errorf("creating %s: %w", temp, err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(temp) // best effort: the error that matters is already being returned
		}
	}()
	writer, err := gzip.NewWriterLevel(file, gzip.BestSpeed)
	if err != nil {
		_ = file.Close() // the gzip error is the one to report
		return fmt.Errorf("compressing %s: %w", temp, err)
	}
	if err := json.NewEncoder(writer).Encode(doc); err != nil {
		_ = file.Close() // the encoding error is the one to report
		return fmt.Errorf("writing %s: %w", temp, err)
	}
	if err := writer.Close(); err != nil {
		_ = file.Close() // the flush error is the one to report
		return fmt.Errorf("writing %s: %w", temp, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", temp, err)
	}
	if err := os.Rename(temp, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}
