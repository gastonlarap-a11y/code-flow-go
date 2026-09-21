// Package docwalk finds a folder's documents of one kind.
//
// Two features open documents that are simply files in the user's folder — the schema designer
// (`.dbml`) and the diagram editor (`.diagram.json`) — and they differ only in what they are
// looking for. Everything around that is the same and is the part worth having once: the
// directories never worth reading, the bounds that stop a pathological tree costing a minute, and
// the shape of the answer.
//
// **The answer is project-relative, sorted, and `/`-separated on every platform.** Those strings
// are keys as well as labels — a layout row, a picker entry, a path written back to disk — and one
// that differed by separator would be a second document as far as everything downstream is
// concerned.
package docwalk

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// MaxDocuments bounds the listing. A folder with more matching files than this is not a
	// documents project, and a picker with two thousand entries is not a picker.
	MaxDocuments = 2000
	// MaxDepth bounds the descent. Deep enough for any real tree, shallow enough that a
	// pathological one cannot cost a minute.
	MaxDepth = 24
)

// prunedDirectories are never read from disk at all.
//
// Pruned rather than filtered, which is the difference between not reading `node_modules` and
// reading all of it to throw the results away. It is also the substitute for gitignore rules, which
// this cannot use: a document may live in a plain folder, so there may be no repository to ask.
var prunedDirectories = map[string]bool{
	".git": true, "node_modules": true, "bin": true, "obj": true, "dist": true,
	"build": true, "out": true, "target": true, ".venv": true, "venv": true,
	"__pycache__": true, "vendor": true, ".next": true, ".nuxt": true, ".svelte-kit": true,
	".gradle": true, ".idea": true, ".vs": true, "Pods": true, "DerivedData": true,
}

// List walks rootPath for files whose name ends in suffix, case-insensitively.
//
// A suffix rather than an extension, because `filepath.Ext("flow.diagram.json")` is `.json` — which
// would list every JSON file in the tree. Callers pass the suffix in lower case; the comparison
// folds the file name, not the suffix.
func List(ctx context.Context, rootPath, suffix string) ([]string, error) {
	root := strings.TrimSpace(rootPath)
	if root == "" {
		return nil, ErrNoFolder(rootPath)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, ErrNoFolder(rootPath)
	}

	found := make([]string, 0, 16)

	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			// A directory that cannot be read is skipped rather than failing the whole listing: one
			// unreadable folder must not cost the user every document in the tree.
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if entry.IsDir() {
			return pruneOrDescend(root, path, entry)
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), suffix) {
			return nil
		}

		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		found = append(found, filepath.ToSlash(relative))

		if len(found) >= MaxDocuments {
			return fs.SkipAll
		}
		return nil
	})
	if walkErr != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}

	sort.Strings(found)
	return found, nil
}

// pruneOrDescend decides what to do with a directory.
//
// A symlinked directory is **skipped, not followed**, and that comes free: `filepath.WalkDir` reads
// entries through `lstat`, so a link to a directory is not a directory to it and is never descended
// into. The rule still matters — depth alone would bound a loop, but a link pointing back up the
// tree reports the same file under two paths, and each path is a distinct key downstream, so one
// document would have two identities and neither would be wrong.
func pruneOrDescend(root, path string, entry fs.DirEntry) error {
	if path == root {
		return nil
	}
	if prunedDirectories[entry.Name()] {
		return fs.SkipDir
	}

	relative, err := filepath.Rel(root, path)
	if err != nil {
		return fs.SkipDir
	}
	if strings.Count(relative, string(filepath.Separator))+1 >= MaxDepth {
		return fs.SkipDir
	}
	return nil
}

// ErrNoFolder is what a caller answers when the root is not a folder it can read.
//
// Exported because the message is the one the schema designer has always returned (DBML-001) and
// the renderer shows it as typed; a second spelling of the same refusal would be a second thing to
// keep in step.
func ErrNoFolder(path string) error {
	return fmt.Errorf("no such folder: %s", path) //nolint:err113 // VERBATIM, and names what was asked for
}
