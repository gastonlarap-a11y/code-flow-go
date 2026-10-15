package repomap

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/git"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/docwalk"
)

// Which files a map reads (REPOMAP-002).

const (
	// maxFiles bounds a map. A repository with more code files than this is mapped by its first
	// ones in path order, and says so.
	maxFiles = 20_000
	// maxFileBytes skips a file too big to be hand-written code: a bundle, a dump, a generated table.
	maxFileBytes = 1 << 20
	// minifiedLineBytes is the average line length past which a file is minified rather than code.
	minifiedLineBytes = 500
)

// listed is one file the map may read.
type listed struct {
	path string // repository-relative, `/`-separated
	lang Lang
}

// listing is what a walk found.
type listing struct {
	files []listed
	// manifests are the files the resolver reads — go.mod, package.json, tsconfig…
	manifests []string
	// truncated is how many code files were left out past maxFiles.
	truncated int
	// git says the folder is a git work tree, so ignored and generated files were left out by git's
	// own rules rather than by a fixed list of folder names.
	git bool
}

// attributesThatSkip are the `.gitattributes` flags GitHub's own language statistics honour: a file
// marked generated or vendored is not the repository's code.
var attributesThatSkip = []string{"linguist-generated", "linguist-vendored"}

// listCodeFiles lists root's code files, sorted by path.
//
// In a git work tree it asks git — tracked files plus untracked ones that are not ignored, which is
// what a person means by "the code" — and drops what `.gitattributes` marks generated or vendored.
// Outside one it walks the folder, pruning the directories nobody reads.
func listCodeFiles(ctx context.Context, root string) (listing, error) {
	if git.IsRepo(ctx, root) {
		if found, ok := listFromGit(ctx, root); ok {
			return found, nil
		}
	}
	return listByWalking(ctx, root)
}

func listFromGit(ctx context.Context, root string) (listing, bool) {
	result, err := git.NewRunner(root).RunRaw(ctx, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil || result.Failed() {
		return listing{}, false
	}
	seen := make(map[string]bool)
	candidates := make([]listed, 0, 256)
	manifests := make([]string, 0, 8)
	for raw := range bytes.SplitSeq(result.Stdout, []byte{0}) {
		path := string(raw)
		// `--cached` repeats a path once per conflict stage, and lists a submodule as its folder;
		// `--others` lists an untracked nested repository as `dir/`. Only regular files count.
		if path == "" || seen[path] || strings.HasSuffix(path, "/") {
			continue
		}
		seen[path] = true
		lang, code := languageOf(path)
		manifest := isManifest(path)
		if !code && !manifest {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil || !info.Mode().IsRegular() {
			continue // deleted from disk but still in the index, a symlink, a gitlink
		}
		if manifest {
			manifests = append(manifests, path)
			continue
		}
		candidates = append(candidates, listed{path: path, lang: lang})
	}

	paths := make([]string, len(candidates))
	for i, c := range candidates {
		paths[i] = c.path
	}
	skipped, err := git.CheckAttrs(ctx, root, attributesThatSkip, paths)
	if err != nil {
		skipped = map[string]bool{} // no attributes is the safe reading: the map shows a little too much
	}
	kept := candidates[:0]
	for _, c := range candidates {
		if !skipped[c.path] {
			kept = append(kept, c)
		}
	}
	found := bounded(kept, true)
	found.manifests = manifests
	return found, true
}

func listByWalking(ctx context.Context, root string) (listing, error) {
	found := make([]listed, 0, 256)
	manifests := make([]string, 0, 8)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if entry != nil && entry.IsDir() {
				return fs.SkipDir // one unreadable folder must not cost the whole map
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			if path != root && docwalk.Pruned(entry.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if lang, ok := languageOf(relative); ok {
			found = append(found, listed{path: relative, lang: lang})
		} else if isManifest(relative) {
			manifests = append(manifests, relative)
		}
		return nil
	})
	if err != nil && ctx.Err() != nil {
		return listing{}, ctx.Err()
	}
	out := bounded(found, false)
	out.manifests = manifests
	return out, nil
}

// bounded sorts the files and keeps the first maxFiles.
func bounded(files []listed, fromGit bool) listing {
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	out := listing{files: files, git: fromGit}
	if len(files) > maxFiles {
		out.truncated = len(files) - maxFiles
		out.files = files[:maxFiles]
	}
	return out
}

// readable says whether content read from a file is hand-written code worth parsing: not binary,
// not minified.
func readable(path string, content []byte) bool {
	head := content
	if len(head) > 8192 {
		head = head[:8192]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	name := strings.ToLower(path[strings.LastIndexByte(path, '/')+1:])
	if strings.Contains(name, ".min.") || strings.HasSuffix(name, ".bundle.js") {
		return false
	}
	lines := countLines(content)
	return lines == 0 || len(content)/lines <= minifiedLineBytes
}
