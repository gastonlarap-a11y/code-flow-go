// Package dbml is the schema designer's backend: the documents in a folder, the positions a person
// gave their tables, the saved database connections, and the AI assistant that reads a schema.
//
// **Parsing, layout and rendering are not here.** They live in the renderer, where `@dbml/core` is,
// and that split is the shape of the feature: a schema document is a file in the user's folder, so
// opening and saving one are the file commands that already exist. What this side owns is the four
// things a webview cannot do — walk a folder, keep a layout, hold a credential, and reach a
// database.
package dbml

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Walking a folder for documents (DBML-001).

const (
	// maxDocuments bounds the listing. A folder with more `.dbml` files than this is not a schema
	// project, and a picker with two thousand entries is not a picker.
	maxDocuments = 2000
	// maxDepth bounds the descent. Deep enough for any real tree, shallow enough that a pathological
	// one cannot cost a minute.
	maxDepth = 24
)

// prunedDirectories are never read from disk at all.
//
// Pruned rather than filtered, which is the difference between not reading `node_modules` and
// reading all of it to throw the results away. It is also the substitute for gitignore rules, which
// this module cannot use: a schema designer has to work in a plain folder, so there may be no
// repository to ask.
var prunedDirectories = map[string]bool{
	".git": true, "node_modules": true, "bin": true, "obj": true, "dist": true,
	"build": true, "out": true, "target": true, ".venv": true, "venv": true,
	"__pycache__": true, "vendor": true, ".next": true, ".nuxt": true, ".svelte-kit": true,
	".gradle": true, ".idea": true, ".vs": true, "Pods": true, "DerivedData": true,
}

// ListDocuments walks a folder for `.dbml` files (DBML-001).
//
// Answers them project-relative, sorted, with `/` separators on every platform — they are keys into
// the layout store as well as labels in a picker, and a path that differed by separator would be a
// second document as far as the layout is concerned.
func ListDocuments(ctx context.Context, rootPath string) ([]string, error) {
	root := strings.TrimSpace(rootPath)
	if root == "" {
		return nil, errNoFolder(rootPath)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, errNoFolder(rootPath)
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
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".dbml") {
			return nil
		}

		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		found = append(found, filepath.ToSlash(relative))

		if len(found) >= maxDocuments {
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
// tree reports the same file under two paths, and each path is a distinct layout key, so one table
// would have two positions and neither would be wrong.
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
	if strings.Count(relative, string(filepath.Separator))+1 >= maxDepth {
		return fs.SkipDir
	}
	return nil
}

func errNoFolder(path string) error {
	return fmt.Errorf("no such folder: %s", path) //nolint:err113 // VERBATIM, and names what was asked for
}
