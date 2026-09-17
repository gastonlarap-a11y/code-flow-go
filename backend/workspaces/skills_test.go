package workspaces_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/gastonlarap-a11y/code-flow/backend/storage"
	"github.com/gastonlarap-a11y/code-flow/backend/workspaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type skillFixture struct {
	svc      *bridge.Service
	store    *workspaces.Store
	paths    platform.Paths
	emitter  *bridge.RecordingEmitter
	workspce workspaces.Workspace
}

func newSkills(t *testing.T) skillFixture {
	t.Helper()
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "codeflow.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	store := workspaces.NewStore(db, fixedClock)
	paths := platform.NewPaths(filepath.Join(t.TempDir(), "CodeFlow"))
	emitter := &bridge.RecordingEmitter{}

	r := bridge.NewRegistry()
	workspaces.RegisterSkills(r, workspaces.SkillDeps{Store: store, Paths: paths, Emitter: emitter})
	r.Seal()

	w, err := store.CreateWorkspace(t.Context(), "Work", "folder", "#6366f1", nil)
	require.NoError(t, err)

	return skillFixture{svc: bridge.NewService(r, nil), store: store, paths: paths, emitter: emitter, workspce: w}
}

// The doubled path is not an accident: `npx skills add` creates the `.claude/skills` part itself,
// so the installer runs in the outer folder. Reproducing it exactly is what lets 3.0 find skills
// 2.7.x installed.
func TestSkillsLiveWhereTwoPointXPutThem(t *testing.T) {
	f := newSkills(t)

	out, err := f.svc.Invoke(t.Context(), "create_custom_skill",
		json.RawMessage(`{"workspaceId":"`+f.workspce.ID+`","name":"reviewer","skillMd":"# Reviewer"}`))
	require.NoError(t, err)

	expected := filepath.Join(f.paths.WorkspaceSkills(f.workspce.ID), ".claude", "skills", "reviewer")
	assert.FileExists(t, filepath.Join(expected, "SKILL.md"))

	var skill workspaces.Skill
	require.NoError(t, json.Unmarshal(out, &skill))
	assert.Equal(t, "reviewer", skill.SkillName)
	assert.Equal(t, workspaces.SourceCustom, skill.SourceRepo)
	assert.True(t, skill.Enabled)
}

// WS-007. The only traversal guard there is — no canonicalisation afterwards — so it has to refuse
// on the lexical shape alone.
func TestSkillFilePathsRefuseToLeaveTheirFolder(t *testing.T) {
	f := newSkills(t)
	_, err := f.svc.Invoke(t.Context(), "create_custom_skill",
		json.RawMessage(`{"workspaceId":"`+f.workspce.ID+`","name":"reviewer","skillMd":"# Reviewer"}`))
	require.NoError(t, err)

	for name, rel := range map[string]string{
		"parent":               "../escape.md",
		"parent in the middle": "docs/../../escape.md",
		"backslash parent":     `..\escape.md`,
		"mixed separators":     `docs\..\..\escape.md`,
		"absolute":             "/etc/passwd",
		"empty segment":        "docs//a.md",
		"trailing separator":   "docs/",
		"empty":                "",
	} {
		t.Run(name, func(t *testing.T) {
			params := json.RawMessage(`{"workspaceId":"` + f.workspce.ID +
				`","skillName":"reviewer","relPath":` + mustJSON(t, rel) + `}`)

			_, err := f.svc.Invoke(t.Context(), "read_skill_file", params)
			require.Error(t, err)
			assert.Equal(t, "invalid file path", err.Error(), "the renderer shows this verbatim")

			_, err = f.svc.Invoke(t.Context(), "write_skill_file",
				json.RawMessage(`{"workspaceId":"`+f.workspce.ID+
					`","skillName":"reviewer","relPath":`+mustJSON(t, rel)+`,"content":"x"}`))
			require.Error(t, err)

			_, err = f.svc.Invoke(t.Context(), "delete_skill_file", params)
			require.Error(t, err)
		})
	}
}

// A skill name becomes a directory name directly, so it needs the same treatment as a path.
func TestSkillNamesRefuseToEscape(t *testing.T) {
	f := newSkills(t)

	for _, name := range []string{"..", ".", "", "../evil", `..\evil`, "a/b", ".hidden"} {
		_, err := f.svc.Invoke(t.Context(), "create_custom_skill",
			json.RawMessage(`{"workspaceId":"`+f.workspce.ID+`","name":`+mustJSON(t, name)+`,"skillMd":"x"}`))
		assert.Error(t, err, "name %q was accepted", name)
	}
}

func TestSkillFilesRoundTripAndList(t *testing.T) {
	f := newSkills(t)
	scope := `{"workspaceId":"` + f.workspce.ID + `","skillName":"reviewer"`
	_, err := f.svc.Invoke(t.Context(), "create_custom_skill",
		json.RawMessage(`{"workspaceId":"`+f.workspce.ID+`","name":"reviewer","skillMd":"# Reviewer"}`))
	require.NoError(t, err)

	_, err = f.svc.Invoke(t.Context(), "write_skill_file",
		json.RawMessage(scope+`,"relPath":"references/style.md","content":"be brief"}`))
	require.NoError(t, err)

	out, err := f.svc.Invoke(t.Context(), "list_skill_files", json.RawMessage(scope+`}`))
	require.NoError(t, err)
	var files []string
	require.NoError(t, json.Unmarshal(out, &files))
	// Forward slashes on both operating systems: the renderer's file tree expects one shape.
	assert.Equal(t, []string{"SKILL.md", "references/style.md"}, files)

	out, err = f.svc.Invoke(t.Context(), "read_skill_file",
		json.RawMessage(scope+`,"relPath":"references/style.md"}`))
	require.NoError(t, err)
	assert.Equal(t, `"be brief"`, string(out))

	_, err = f.svc.Invoke(t.Context(), "delete_skill_file",
		json.RawMessage(scope+`,"relPath":"references/style.md"}`))
	require.NoError(t, err)

	out, err = f.svc.Invoke(t.Context(), "list_skill_files", json.RawMessage(scope+`}`))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(out, &files))
	assert.Equal(t, []string{"SKILL.md"}, files)
}

// A skill whose folder is gone lists nothing rather than failing: the row is what the UI works
// from, and an error here would make the whole panel unusable.
func TestListingAMissingSkillFolderIsEmptyNotAnError(t *testing.T) {
	f := newSkills(t)

	out, err := f.svc.Invoke(t.Context(), "list_skill_files",
		json.RawMessage(`{"workspaceId":"`+f.workspce.ID+`","skillName":"never-installed"}`))

	require.NoError(t, err)
	assert.Equal(t, "[]", string(out))
}

// Deleting a file that is not there is the state the caller asked for.
func TestDeletingAMissingSkillFileSucceeds(t *testing.T) {
	f := newSkills(t)
	_, err := f.svc.Invoke(t.Context(), "create_custom_skill",
		json.RawMessage(`{"workspaceId":"`+f.workspce.ID+`","name":"reviewer","skillMd":"x"}`))
	require.NoError(t, err)

	_, err = f.svc.Invoke(t.Context(), "delete_skill_file",
		json.RawMessage(`{"workspaceId":"`+f.workspce.ID+`","skillName":"reviewer","relPath":"nope.md"}`))

	assert.NoError(t, err)
}

// Reinstalling keeps the row's id and its enabled flag, so a skill the user turned off does not
// quietly come back on.
func TestRecordingASkillTwiceKeepsItsIdentityAndItsFlag(t *testing.T) {
	f := newSkills(t)

	first, err := f.store.RecordSkill(t.Context(), f.workspce.ID, "reviewer", "skills.sh/reviewer")
	require.NoError(t, err)
	require.NoError(t, f.store.SetSkillEnabled(t.Context(), first.ID, false))

	second, err := f.store.RecordSkill(t.Context(), f.workspce.ID, "reviewer", "skills.sh/reviewer")
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID)
	assert.False(t, second.Enabled, "a disabled skill must not come back on")

	list, err := f.store.ListSkills(t.Context(), f.workspce.ID)
	require.NoError(t, err)
	assert.Len(t, list, 1, "reinstalling must not leave a second row")
}

// Removing a skill takes the folder as well as the row: a row without a folder is a skill the UI
// offers and cannot open.
func TestRemovingASkillTakesItsFolder(t *testing.T) {
	f := newSkills(t)
	out, err := f.svc.Invoke(t.Context(), "create_custom_skill",
		json.RawMessage(`{"workspaceId":"`+f.workspce.ID+`","name":"reviewer","skillMd":"x"}`))
	require.NoError(t, err)
	var skill workspaces.Skill
	require.NoError(t, json.Unmarshal(out, &skill))
	dir := filepath.Join(f.paths.WorkspaceSkills(f.workspce.ID), ".claude", "skills", "reviewer")
	require.DirExists(t, dir)

	_, err = f.svc.Invoke(t.Context(), "remove_workspace_skill", json.RawMessage(`{"id":"`+skill.ID+`"}`))
	require.NoError(t, err)

	assert.NoDirExists(t, dir)
	list, err := f.store.ListSkills(t.Context(), f.workspce.ID)
	require.NoError(t, err)
	assert.Empty(t, list)
}

// ---- importing --------------------------------------------------------------------------------

func TestImportingAFolderCopiesItAndRecordsItAsLocal(t *testing.T) {
	f := newSkills(t)
	src := filepath.Join(t.TempDir(), "my-skill")
	require.NoError(t, os.MkdirAll(filepath.Join(src, "references"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# Mine"), 0o640))
	require.NoError(t, os.WriteFile(filepath.Join(src, "references", "a.md"), []byte("ref"), 0o640))

	out, err := f.svc.Invoke(t.Context(), "import_skill_from_folder",
		json.RawMessage(`{"workspaceId":"`+f.workspce.ID+`","srcDir":`+mustJSON(t, src)+`}`))
	require.NoError(t, err)

	var skill workspaces.Skill
	require.NoError(t, json.Unmarshal(out, &skill))
	assert.Equal(t, "my-skill", skill.SkillName)
	assert.Equal(t, workspaces.SourceLocal, skill.SourceRepo)

	dest := filepath.Join(f.paths.WorkspaceSkills(f.workspce.ID), ".claude", "skills", "my-skill")
	assert.FileExists(t, filepath.Join(dest, "SKILL.md"))
	assert.FileExists(t, filepath.Join(dest, "references", "a.md"))
}

// A skill is defined by having a SKILL.md; importing a folder without one would produce an entry
// the engines silently ignore.
func TestImportingRefusesAFolderWithoutASkillFile(t *testing.T) {
	f := newSkills(t)
	src := filepath.Join(t.TempDir(), "not-a-skill")
	require.NoError(t, os.MkdirAll(src, 0o750))

	_, err := f.svc.Invoke(t.Context(), "import_skill_from_folder",
		json.RawMessage(`{"workspaceId":"`+f.workspce.ID+`","srcDir":`+mustJSON(t, src)+`}`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no SKILL.md")
}

func TestImportingRefusesSomethingThatIsNotAFolder(t *testing.T) {
	f := newSkills(t)
	file := filepath.Join(t.TempDir(), "a-file")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o640))

	_, err := f.svc.Invoke(t.Context(), "import_skill_from_folder",
		json.RawMessage(`{"workspaceId":"`+f.workspce.ID+`","srcDir":`+mustJSON(t, file)+`}`))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a folder")
}

// A skill folder is user-supplied; following a symlink would copy whatever it points at into the
// workspace store.
func TestImportingSkipsSymlinks(t *testing.T) {
	f := newSkills(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("not yours"), 0o640))

	src := filepath.Join(t.TempDir(), "my-skill")
	require.NoError(t, os.MkdirAll(src, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# Mine"), 0o640))
	require.NoError(t, os.Symlink(outside, filepath.Join(src, "linked.txt")))

	_, err := f.svc.Invoke(t.Context(), "import_skill_from_folder",
		json.RawMessage(`{"workspaceId":"`+f.workspce.ID+`","srcDir":`+mustJSON(t, src)+`}`))
	require.NoError(t, err)

	dest := filepath.Join(f.paths.WorkspaceSkills(f.workspce.ID), ".claude", "skills", "my-skill")
	assert.FileExists(t, filepath.Join(dest, "SKILL.md"))
	assert.NoFileExists(t, filepath.Join(dest, "linked.txt"))
}

func TestEmptySkillListIsAnArray(t *testing.T) {
	f := newSkills(t)

	out, err := f.svc.Invoke(t.Context(), "list_workspace_skills",
		json.RawMessage(`{"workspaceId":"`+f.workspce.ID+`"}`))

	require.NoError(t, err)
	assert.Equal(t, "[]", string(out))
}

func TestSkillCommandsReportAMissingParameterByName(t *testing.T) {
	f := newSkills(t)

	for _, tc := range []struct{ method, params, missing string }{
		{"list_workspace_skills", `{}`, "workspaceId"},
		{"install_workspace_skill", `{"workspaceId":"w1","sourceRepo":"r"}`, "skillName"},
		{"create_custom_skill", `{"workspaceId":"w1","name":"n"}`, "skillMd"},
		{"set_workspace_skill_enabled", `{"id":"s1"}`, "enabled"},
		{"read_skill_file", `{"workspaceId":"w1","skillName":"s"}`, "relPath"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			_, err := f.svc.Invoke(t.Context(), tc.method, json.RawMessage(tc.params))
			require.Error(t, err)
			assert.Equal(t, "missing required parameter '"+tc.missing+"'", err.Error())
		})
	}
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}
