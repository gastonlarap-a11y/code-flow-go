package archify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCitationsSkipWhatIsNotCommitted(t *testing.T) {
	repo := Repo{
		Meta:  &Repository{URL: "https://github.com/acme/app", Provider: "github", LinkMode: "web", Revision: strings.Repeat("a", 40)},
		Dirty: map[string]bool{"web/dirty.ts": true},
	}
	cited := repo.Cite([]Ref{
		{Path: "web/dirty.ts", Line: 1},
		{Path: "web/a.ts", Line: 2, EndLine: 9},
		{Path: "web/a.ts", Line: 2},
		{Path: "web/b.ts", Line: 3}, {Path: "web/c.ts", Line: 4}, {Path: "web/d.ts", Line: 5},
	})
	assert.Equal(t, []Source{{Path: "web/a.ts", Line: 2, EndLine: 9}, {Path: "web/b.ts", Line: 3}, {Path: "web/c.ts", Line: 4}}, cited)
	assert.Nil(t, repo.Pinned(false))
	assert.Same(t, repo.Meta, repo.Pinned(true))
	assert.Nil(t, Repo{}.Cite([]Ref{{Path: "a.ts", Line: 1}}), "no repository, no citation")
}

// Archify's own rules: strip HTTP(S) userinfo and keep the rest; a supported host's SSH origin may
// be written as the HTTPS address it matches; never rewrite another SSH origin; a folder is no
// repository address at all.
func TestRemote(t *testing.T) {
	tests := []struct {
		origin, address, provider string
		ok                        bool
	}{
		{"git@github.com:acme/app.git", "https://github.com/acme/app", "github", true},
		{"https://token:x-oauth@github.com/acme/app.git", "https://github.com/acme/app.git", "github", true},
		{"https://user@dev.azure.com/org/p/_git/r", "https://dev.azure.com/org/p/_git/r", "", true},
		{"git@git.internal:team/app.git", "git@git.internal:team/app.git", "", true},
		{"ssh://git@git.internal:2222/team/app.git", "ssh://git@git.internal:2222/team/app.git", "", true},
		{"http://git.internal:8080/app", "http://git.internal:8080/app", "", true},
		{"/local/path", "", "", false},
		{"file:///local/path", "", "", false},
		{"../sibling", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.origin, func(t *testing.T) {
			address, provider, ok := Remote(tt.origin)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.address, address)
			assert.Equal(t, tt.provider, provider)
		})
	}
}

// fakeInstall lays out an install the way `npx skills add` does.
func fakeInstall(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bin"), 0o750))
	script := filepath.Join(dir, "bin", "archify.mjs")
	require.NoError(t, os.WriteFile(script, []byte("// archify"), 0o600))
	manifest, err := json.Marshal(map[string]string{"version": version})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), manifest, 0o600))
	return script
}

func TestOnlyASupportedArchifyIsOffered(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no real install interferes

	t.Setenv("ARCHIFY_BIN", fakeInstall(t, "3.1.0"))
	cli, err := Find(t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "3.1.0", cli.Version)

	t.Setenv("ARCHIFY_BIN", fakeInstall(t, "2.9.0"))
	_, err = Find(t.TempDir())
	require.ErrorIs(t, err, ErrNotInstalled)

	for version, want := range map[string]bool{"3.1.0": true, "3.4.2": true, "3.0.9": false, "4.0.0": false, "x": false} {
		assert.Equal(t, want, supported(version), version)
	}
}

// `npx skills add -g` puts the skill wherever each agent the user picked reads skills; any of them
// is found, and the repository's own copy too.
func TestArchifyIsFoundWhereverTheSkillsCLIPutIt(t *testing.T) {
	t.Setenv("ARCHIFY_BIN", "")
	for _, dir := range [][]string{
		{".claude", "skills"}, {".agents", "skills"}, {".config", "opencode", "skills"},
		{".cursor", "skills"}, {".gemini", "antigravity", "skills"}, {".codex", "skills"},
	} {
		t.Run(filepath.Join(dir...), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			install := filepath.Join(append(append([]string{home}, dir...), "archify")...)
			require.NoError(t, os.MkdirAll(filepath.Join(install, "bin"), 0o750))
			require.NoError(t, os.WriteFile(filepath.Join(install, "bin", "archify.mjs"), []byte("// archify"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(install, "package.json"), []byte(`{"version":"3.1.0"}`), 0o600))

			cli, err := Find(t.TempDir())
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(install, "bin", "archify.mjs"), cli.Script)
		})
	}

	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	install := filepath.Join(root, ".agents", "skills", "archify")
	require.NoError(t, os.MkdirAll(filepath.Join(install, "bin"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(install, "bin", "archify.mjs"), []byte("// archify"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(install, "package.json"), []byte(`{"version":"3.1.0"}`), 0o600))
	cli, err := Find(root)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(install, "bin", "archify.mjs"), cli.Script, "the repository's own copy")
}

func TestDeliverNeedsNode(t *testing.T) {
	_, err := CLI{Script: "archify.mjs"}.Deliver(t.Context(), Runtime{}, "architecture", t.TempDir(), "a.json", "a.html", "")
	require.ErrorIs(t, err, ErrNoNode)
}

func TestFirstLine(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"the first line", "  one\ntwo", "one"},
		{"a long line is cut", strings.Repeat("é", 400), strings.Repeat("é", 299) + "…"},
		{"empty stays empty", " \n ", ""},
		{"a heading takes the line under it", "class schema validation failed:\n  /meta/title is empty\n  more", "class schema validation failed: /meta/title is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, firstLine(tt.in))
		})
	}
}
