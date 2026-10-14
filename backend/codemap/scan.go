package codemap

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// What a repository holds, found in one walk (CODEMAP-002).

// maxProjects bounds how many Go modules and how many tsconfig files one map covers. A repository
// with more is a monorepo of many applications, and mapping all of them at once would be slow
// without being more useful.
const maxProjects = 12

// repoScan is the result of walking the repository once.
type repoScan struct {
	// goModules are the directories holding a go.mod, shallowest first.
	goModules []string
	// tsconfigs are the TypeScript project files, shallowest first.
	tsconfigs []string
	sqlFiles  []string
	// packageJSONs say which test runner each JavaScript package uses.
	packageJSONs []string

	goFiles, tsFiles int
	// goPrint and tsPrint fingerprint each half: (path, size, mtime) of every file it reads. The
	// map rebuilds a half only when its print moved.
	goPrint, tsPrint string
}

// skipDir names the directories never walked: version control, dependencies, build output, and the
// caches tools leave in a working tree.
func skipDir(name string) bool {
	switch name {
	case "node_modules", "vendor", "dist", "build", "bin", "out", "target", "coverage",
		"testdata", "__pycache__", "Pods", "DerivedData":
		return true
	}
	return strings.HasPrefix(name, ".")
}

func isTSSource(name string) bool {
	switch filepath.Ext(name) {
	case ".ts", ".tsx", ".mts", ".cts":
		return true
	}
	return false
}

func isTSConfig(name string) bool {
	return name == "tsconfig.json" || (strings.HasPrefix(name, "tsconfig.") && strings.HasSuffix(name, ".json"))
}

// scanRepository walks root once.
func scanRepository(root string) (repoScan, error) {
	var scan repoScan
	goHash, tsHash := fnv.New64a(), fnv.New64a()

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil // an unreadable subdirectory costs that directory, not the map
		}
		if entry.IsDir() {
			if path != root && skipDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		name := entry.Name()
		var print io.Writer
		switch {
		case name == "go.mod":
			scan.goModules = append(scan.goModules, filepath.Dir(path))
			print = goHash
		case name == "go.sum":
			print = goHash
		case strings.HasSuffix(name, ".go"):
			scan.goFiles++
			print = goHash
		case strings.HasSuffix(name, ".sql"):
			scan.sqlFiles = append(scan.sqlFiles, path)
			print = goHash
		case isTSConfig(name):
			scan.tsconfigs = append(scan.tsconfigs, path)
			print = tsHash
		case name == "package.json":
			scan.packageJSONs = append(scan.packageJSONs, path)
			print = tsHash
		case isTSSource(name):
			scan.tsFiles++
			print = tsHash
		default:
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return nil //nolint:nilerr // a file deleted mid-walk is simply not part of this print
		}
		_, err = io.WriteString(print, path+"|"+strconv.FormatInt(info.Size(), 10)+"|"+
			strconv.FormatInt(info.ModTime().UnixNano(), 10)+"\n")
		return err
	})
	if err != nil {
		return repoScan{}, fmt.Errorf("walking %s: %w", root, err)
	}

	scan.goModules = shallowestFirst(scan.goModules)
	scan.tsconfigs = shallowestFirst(scan.tsconfigs)
	scan.goPrint = strconv.FormatUint(goHash.Sum64(), 16)
	scan.tsPrint = strconv.FormatUint(tsHash.Sum64(), 16)
	return scan, nil
}

// shallowestFirst orders paths by depth, then by name, and keeps at most maxProjects.
func shallowestFirst(paths []string) []string {
	slices.SortFunc(paths, func(a, b string) int {
		if da, db := strings.Count(a, string(filepath.Separator)), strings.Count(b, string(filepath.Separator)); da != db {
			return da - db
		}
		return strings.Compare(a, b)
	})
	if len(paths) > maxProjects {
		paths = paths[:maxProjects]
	}
	return paths
}

// repoMeta is what the queries need to know about the repository beyond its symbols: where each Go
// module starts, and which test runner each JavaScript package uses — the two things a test command
// is built from.
func repoMeta(root string, scan repoScan) Meta {
	meta := Meta{GoModules: make([]string, 0, len(scan.goModules)), TestRunners: map[string]string{}}
	for _, dir := range scan.goModules {
		if rel, err := relPath(root, dir); err == nil {
			meta.GoModules = append(meta.GoModules, strings.TrimPrefix(rel, "."))
		}
	}
	for _, manifest := range scan.packageJSONs {
		runner := testRunner(manifest)
		if runner == "" {
			continue
		}
		if rel, err := relPath(root, filepath.Dir(manifest)); err == nil {
			meta.TestRunners[strings.TrimPrefix(rel, ".")] = runner
		}
	}
	return meta
}

// testRunner reads which of the two runners a package.json depends on, "" for neither.
func testRunner(manifest string) string {
	raw, err := os.ReadFile(manifest) //nolint:gosec // G304: a package.json found by walking the repository the user opened
	if err != nil {
		return ""
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(raw, &pkg) != nil {
		return ""
	}
	for _, runner := range []string{"vitest", "jest"} {
		if _, ok := pkg.DevDependencies[runner]; ok {
			return runner
		}
		if _, ok := pkg.Dependencies[runner]; ok {
			return runner
		}
	}
	return ""
}

// relPath is path relative to root, with forward slashes.
func relPath(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", fmt.Errorf("relative path of %s: %w", path, err)
	}
	return filepath.ToSlash(rel), nil
}
