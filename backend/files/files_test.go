package files_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge/jsonwire"
	"github.com/gastonlarap-a11y/code-flow/backend/files"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tempRepo is a plain directory. The file operations have no git dependency at all, which is what
// the fsops vectors say and why they say "no git init required".
func tempRepo(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func write(t *testing.T, repo, rel, content string) {
	t.Helper()
	full := filepath.Join(repo, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
}

// ---- the three fsops scenario vectors ------------------------------------------------------------

// fsops.vectors.json#creates-nested-file-and-dir
func TestCreatesNestedFileAndDir(t *testing.T) {
	repo := tempRepo(t)

	require.NoError(t, files.CreateDir(repo, "src/nested"))
	info, err := os.Stat(filepath.Join(repo, "src", "nested"))
	require.NoError(t, err)
	assert.True(t, info.IsDir(), "every missing intermediate component is made in one call")

	require.NoError(t, files.CreateFile(repo, "src/nested/new.ts"))

	content, err := files.ReadFileText(repo, "src/nested/new.ts")
	require.NoError(t, err)
	assert.Equal(t, "", content)
}

// The same thing without the `CreateDir` first, which is the only way one caller reaches it.
//
// The schema designer's "new document" field takes a whole relative path — typing `esquemas/ventas`
// is how a person makes a folder there, since the panel offers no other way to make one — and the
// store calls `create_file` with it directly. Nothing else in the app creates a file at a path
// whose parent may not exist, so without this the behaviour that feature depends on was asserted
// only in combination with the call that made the assertion unnecessary.
func TestCreateFileMakesItsOwnParentDirectory(t *testing.T) {
	repo := tempRepo(t)

	require.NoError(t, files.CreateFile(repo, "esquemas/ventas/2026.dbml"))

	content, err := files.ReadFileText(repo, "esquemas/ventas/2026.dbml")
	require.NoError(t, err)
	assert.Equal(t, "", content)

	info, err := os.Stat(filepath.Join(repo, "esquemas", "ventas"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

// fsops.vectors.json#move-refuses-destructive-cases
func TestMoveRefusesDestructiveCases(t *testing.T) {
	repo := tempRepo(t)
	require.NoError(t, files.CreateDir(repo, "src/nested"))
	require.NoError(t, files.CreateDir(repo, "other"))
	require.NoError(t, files.CreateFile(repo, "src/a.ts"))
	require.NoError(t, files.CreateFile(repo, "other/a.ts"))

	t.Run("into a sibling folder", func(t *testing.T) {
		moved, err := files.MovePath(repo, "src/a.ts", "src/nested")
		require.NoError(t, err)
		assert.Equal(t, "src/nested/a.ts", moved)
		assert.FileExists(t, filepath.Join(repo, "src", "nested", "a.ts"))
	})

	t.Run("back to the repository root", func(t *testing.T) {
		moved, err := files.MovePath(repo, "src/nested/a.ts", "")
		require.NoError(t, err)
		assert.Equal(t, "a.ts", moved)
	})

	t.Run("onto an existing name is refused, not overwritten", func(t *testing.T) {
		_, err := files.MovePath(repo, "other/a.ts", "")
		assert.EqualError(t, err, "a.ts already exists here")
		assert.FileExists(t, filepath.Join(repo, "other", "a.ts"), "the source is still there")
	})

	t.Run("a folder into itself", func(t *testing.T) {
		_, err := files.MovePath(repo, "src", "src")
		assert.EqualError(t, err, "cannot move a folder into itself")
	})

	t.Run("a folder into its own descendant", func(t *testing.T) {
		_, err := files.MovePath(repo, "src", "src/nested")
		assert.EqualError(t, err, "cannot move a folder into itself")
		info, err := os.Stat(filepath.Join(repo, "src", "nested"))
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("onto its own current location is a no-op success", func(t *testing.T) {
		moved, err := files.MovePath(repo, "other/a.ts", "other")
		require.NoError(t, err, "dropped back where it already lives is not a mistake")
		assert.Equal(t, "other/a.ts", moved)
	})

	t.Run("outside the repository", func(t *testing.T) {
		_, err := files.MovePath(repo, "a.ts", "..")
		assert.EqualError(t, err, "path escapes the repository root")
	})
}

// fsops.vectors.json#rejects-duplicates-empty-traversal
func TestRejectsDuplicatesEmptyAndTraversal(t *testing.T) {
	repo := tempRepo(t)

	require.NoError(t, files.CreateFile(repo, "dup.txt"))
	assert.EqualError(t, files.CreateFile(repo, "dup.txt"), "dup.txt already exists")

	require.NoError(t, files.CreateDir(repo, "dir"))
	assert.EqualError(t, files.CreateDir(repo, "dir"), "dir already exists")

	// Emptiness is checked before the component rule, so whitespace reports this and not
	// "invalid path".
	assert.EqualError(t, files.CreateFile(repo, "   "), "name cannot be empty")

	// The message interpolates the original argument, untrimmed.
	assert.EqualError(t, files.CreateFile(repo, "../escaped.txt"), "invalid path: ../escaped.txt")
	assert.EqualError(t, files.CreateDir(repo, "../escaped"), "invalid path: ../escaped")

	parent := filepath.Dir(repo)
	assert.NoFileExists(t, filepath.Join(parent, "escaped.txt"))
	assert.NoDirExists(t, filepath.Join(parent, "escaped"))
}

// ---- the guards themselves ------------------------------------------------------------------------

// The creation guard cannot canonicalise anything — the path does not exist — so it inspects
// components instead. A separator the platform does not use is still a separator to a guard.
func TestTheCreationGuardRejectsEveryEscapeShape(t *testing.T) {
	repo := tempRepo(t)

	for _, name := range []string{
		"../escaped.txt", "a/../../escaped.txt", "./x.txt", "a/./b.txt",
		"..\\escaped.txt", "/etc/passwd", "a//..//b",
	} {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, files.CreateFile(repo, name))
		})
	}
}

// The existence guard has the opposite problem and solves it the other way: a target that is not
// there yet is normalised lexically, which resolves `..` before containment is checked.
func TestTheExistenceGuardNormalisesAPathThatIsNotThere(t *testing.T) {
	repo := tempRepo(t)
	require.NoError(t, files.CreateDir(repo, "foo"))

	_, err := files.ReadFileText(repo, "foo/../../escaped.txt")

	assert.EqualError(t, err, "path escapes the repository root")
}

func TestReadingAFolder(t *testing.T) {
	repo := tempRepo(t)
	require.NoError(t, files.CreateDir(repo, "src"))

	_, err := files.ReadFileText(repo, "src")

	// Its own words rather than the operating system's "is a directory", which is worded
	// differently on each platform and reads like a crash.
	assert.EqualError(t, err, "src is a folder, not a file")
}

// Strict, not lossy: this text goes to the editor and comes back through write_file_text, so
// replacing undecodable bytes would corrupt the file on the next save.
func TestReadingABinaryFileIsRefusedRatherThanMangled(t *testing.T) {
	repo := tempRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "blob.bin"), []byte{0xff, 0xfe, 0x00}, 0o600))

	_, err := files.ReadFileText(repo, "blob.bin")

	assert.ErrorContains(t, err, "not valid UTF-8")
}

func TestWriteFileTextRoundTrip(t *testing.T) {
	repo := tempRepo(t)

	require.NoError(t, files.WriteFileText(repo, "notes.md", "# Título\r\nsin salto final"))

	content, err := files.ReadFileText(repo, "notes.md")
	require.NoError(t, err)
	assert.Equal(t, "# Título\r\nsin salto final", content, "byte for byte, CRLF included")
}

// FILE-005: the one operation with no repository, because the save dialog is the authorisation.
func TestWriteFileBytes(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "export.png")

	require.NoError(t, files.WriteFileBytes(target, []byte{0x89, 'P', 'N', 'G'}))

	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, []byte{0x89, 'P', 'N', 'G'}, content)
}

func TestWriteFileBytesChecks(t *testing.T) {
	t.Run("a relative path", func(t *testing.T) {
		err := files.WriteFileBytes("relative.png", nil)
		assert.EqualError(t, err, "expected an absolute path, got: relative.png")
	})

	t.Run("a parent that is not there", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "nope", "export.png")
		err := files.WriteFileBytes(missing, nil)
		assert.ErrorContains(t, err, "no such folder: ")
	})
}

// ---- listing --------------------------------------------------------------------------------------

func names(entries []files.FileEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name)
	}
	return out
}

func TestListDirSortsFoldersFirstThenCaseInsensitively(t *testing.T) {
	repo := tempRepo(t)
	for _, dir := range []string{"Zebra", "apple"} {
		require.NoError(t, files.CreateDir(repo, dir))
	}
	for _, file := range []string{"README.md", "banana.ts", ".hidden"} {
		require.NoError(t, files.CreateFile(repo, file))
	}
	require.NoError(t, files.CreateDir(repo, ".git"))

	entries, err := files.ListDir(repo, nil)

	require.NoError(t, err)
	assert.Equal(t, []string{"apple", "Zebra", ".hidden", "banana.ts", "README.md"}, names(entries))
	assert.NotContains(t, names(entries), ".git", "hidden by literal name, not by an ignore rule")
	require.NoError(t, jsonwire.AssertNoNilSlices(entries))
}

func TestListDirOfASubdirectory(t *testing.T) {
	repo := tempRepo(t)
	require.NoError(t, files.CreateFile(repo, "src/deep/thing.ts"))
	sub := "src/deep"

	entries, err := files.ListDir(repo, &sub)

	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "src/deep/thing.ts", entries[0].Path, "repo-relative and slash-separated")
	assert.False(t, entries[0].IsDir)
}

func TestAnEmptyListingIsAnArray(t *testing.T) {
	repo := tempRepo(t)

	entries, err := files.ListDir(repo, nil)

	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.NotNil(t, entries)
}

// FILE-017: the type comes from the entry the enumeration produced. A symlink to a directory is a
// directory, which is the case a second look would answer differently.
func TestASymlinkToADirectoryListsAsOne(t *testing.T) {
	repo := tempRepo(t)
	require.NoError(t, files.CreateDir(repo, "real"))
	require.NoError(t, os.Symlink(filepath.Join(repo, "real"), filepath.Join(repo, "link")))

	entries, err := files.ListDir(repo, nil)

	require.NoError(t, err)
	byName := map[string]bool{}
	for _, entry := range entries {
		byName[entry.Name] = entry.IsDir
	}
	assert.True(t, byName["link"], "the tree has to offer expanding it")
}

// ---- opening --------------------------------------------------------------------------------------

type fakeOpener struct{ opened, revealed string }

func (f *fakeOpener) OpenFile(path string) error            { f.opened = path; return nil }
func (f *fakeOpener) RevealInFileManager(path string) error { f.revealed = path; return nil }

func TestOpenInDefaultAppResolvesThroughTheRepository(t *testing.T) {
	repo := tempRepo(t)
	require.NoError(t, files.CreateFile(repo, "doc.pdf"))
	opener := &fakeOpener{}

	require.NoError(t, files.OpenInDefaultApp(opener, repo, "doc.pdf"))

	assert.True(t, strings.HasSuffix(opener.opened, filepath.Join("doc.pdf")))
	assert.True(t, filepath.IsAbs(opener.opened), "the OS gets an absolute path, not a relative one")
}

func TestOpenInDefaultAppRefusesOutsideTheRepository(t *testing.T) {
	repo := tempRepo(t)
	opener := &fakeOpener{}

	err := files.OpenInDefaultApp(opener, repo, "../../../etc/passwd")

	assert.EqualError(t, err, "path escapes the repository root")
	assert.Empty(t, opener.opened, "nothing reached the operating system")
}

// Unscoped on purpose: the paths it is given are project roots the user chose.
func TestRevealInFileManagerTakesAnAbsolutePathAsIs(t *testing.T) {
	opener := &fakeOpener{}
	dir := t.TempDir()

	require.NoError(t, files.RevealInFileManager(opener, dir))

	assert.Equal(t, dir, opener.revealed)
}

func TestOpeningWithNoDesktop(t *testing.T) {
	repo := tempRepo(t)
	require.NoError(t, files.CreateFile(repo, "doc.pdf"))

	assert.Error(t, files.OpenInDefaultApp(nil, repo, "doc.pdf"))
	assert.Error(t, files.RevealInFileManager(nil, repo))
}
