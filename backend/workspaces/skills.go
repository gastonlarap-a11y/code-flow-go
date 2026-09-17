package workspaces

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
	"github.com/gastonlarap-a11y/code-flow/backend/shared/safego"
)

// The skills subsystem.
//
// A workspace skill is a folder containing at minimum a SKILL.md, kept under
//
//	{base}/workspaces/{workspace_id}/skills/.claude/skills/{skill_name}
//
// The doubled path is not an accident: `npx skills add` creates the `.claude/skills` part itself,
// so the installer's working directory is the outer `skills` folder and the skills land inside.
// Reproducing that layout exactly is what lets 3.0 find skills 2.7.x installed.

// SourceRepo values recorded per skill, so the UI can say where one came from.
const (
	// SourceCustom is a skill authored in the app.
	SourceCustom = "custom"
	// SourceLocal is one imported from a folder on disk.
	SourceLocal = "local"
)

// skillsRoot is where `npx skills add` is run from — the outer folder, not the .claude/skills one.
func skillsRoot(paths platform.Paths, workspaceID string) string {
	return paths.WorkspaceSkills(workspaceID)
}

// skillsDir is where the skill folders actually live.
func skillsDir(paths platform.Paths, workspaceID string) string {
	return filepath.Join(skillsRoot(paths, workspaceID), ".claude", "skills")
}

// skillDir is one skill's folder.
func skillDir(paths platform.Paths, workspaceID, name string) string {
	return filepath.Join(skillsDir(paths, workspaceID), name)
}

// ErrInvalidSkillPath is what every file-editing command answers for a path that tries to leave
// its skill folder.
var ErrInvalidSkillPath = errors.New("invalid file path")

// safeSkillPath resolves a relative path inside a skill folder, or refuses.
//
// Splits on both separators and rejects the whole path if any segment is ".." or empty. That is
// the only guard — there is no canonicalisation or prefix check afterwards, which matters on a
// case-insensitive filesystem and matters more where symlinks exist, so the rule is kept strict
// and lexical rather than clever. Transcribed from 2.x (WS-007), including the message: the
// renderer shows it verbatim.
func safeSkillPath(base, rel string) (string, error) {
	if rel == "" {
		return "", ErrInvalidSkillPath
	}

	// strings.Split rather than Fields or FieldsFunc: those drop empty segments, which is exactly
	// the case being guarded against — "a//b" and a leading "/" both have to be refused.
	segments := strings.Split(strings.ReplaceAll(rel, "\\", "/"), "/")
	for _, segment := range segments {
		if segment == ".." || segment == "" {
			return "", ErrInvalidSkillPath
		}
	}
	return filepath.Join(base, filepath.Join(segments...)), nil
}

// ---- store ------------------------------------------------------------------------------------

// ListSkills returns a workspace's installed skills.
func (s *Store) ListSkills(ctx context.Context, workspaceID string) ([]Skill, error) {
	out := make([]Skill, 0, 8)
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx,
			`SELECT id, workspace_id, skill_name, source_repo, enabled, installed_at
			   FROM workspace_skills WHERE workspace_id = ? ORDER BY skill_name`, workspaceID)
		if err != nil {
			return fmt.Errorf("list skills: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var skill Skill
			if err := rows.Scan(&skill.ID, &skill.WorkspaceID, &skill.SkillName, &skill.SourceRepo,
				&skill.Enabled, &skill.InstalledAt); err != nil {
				return fmt.Errorf("scan skill: %w", err)
			}
			out = append(out, skill)
		}
		return rows.Err()
	})
	return out, err
}

// RecordSkill inserts or replaces the row for an installed skill. Reinstalling one keeps its id
// and its enabled flag, so a disabled skill does not quietly come back on.
func (s *Store) RecordSkill(ctx context.Context, workspaceID, name, sourceRepo string) (Skill, error) {
	skill := Skill{
		WorkspaceID: workspaceID,
		SkillName:   name,
		SourceRepo:  sourceRepo,
		Enabled:     true,
		InstalledAt: s.clock.Now(),
	}

	err := s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var existingID string
		var existingEnabled bool
		err := tx.QueryRowContext(ctx,
			`SELECT id, enabled FROM workspace_skills WHERE workspace_id = ? AND skill_name = ?`,
			workspaceID, name).Scan(&existingID, &existingEnabled)
		switch {
		case err == nil:
			skill.ID = existingID
			skill.Enabled = existingEnabled
			_, err = tx.ExecContext(ctx,
				`UPDATE workspace_skills SET source_repo = ?, installed_at = ? WHERE id = ?`,
				sourceRepo, skill.InstalledAt, skill.ID)
			if err != nil {
				return fmt.Errorf("update skill: %w", err)
			}
			return nil
		case errors.Is(err, sql.ErrNoRows):
			skill.ID = newID()
			_, err = tx.ExecContext(ctx,
				`INSERT INTO workspace_skills (id, workspace_id, skill_name, source_repo, enabled, installed_at)
				 VALUES (?, ?, ?, ?, 1, ?)`,
				skill.ID, workspaceID, name, sourceRepo, skill.InstalledAt)
			if err != nil {
				return fmt.Errorf("record skill: %w", err)
			}
			return nil
		default:
			return fmt.Errorf("look up skill: %w", err)
		}
	})
	return skill, err
}

// SkillByID returns one row, or ErrNotFound.
func (s *Store) SkillByID(ctx context.Context, id string) (Skill, error) {
	var skill Skill
	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		err := db.QueryRowContext(ctx,
			`SELECT id, workspace_id, skill_name, source_repo, enabled, installed_at
			   FROM workspace_skills WHERE id = ?`, id).
			Scan(&skill.ID, &skill.WorkspaceID, &skill.SkillName, &skill.SourceRepo,
				&skill.Enabled, &skill.InstalledAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get skill: %w", err)
		}
		return nil
	})
	return skill, err
}

// SetSkillEnabled flips the flag. A disabled skill stays on disk and stays listed; what changes is
// that the project sync removes it from `.claude/skills` rather than copying it in.
func (s *Store) SetSkillEnabled(ctx context.Context, id string, enabled bool) error {
	return s.update(ctx, `UPDATE workspace_skills SET enabled = ? WHERE id = ?`, boolToInt(enabled), id)
}

// DeleteSkillRow removes the database row only; the caller removes the folder.
func (s *Store) DeleteSkillRow(ctx context.Context, id string) error {
	return s.update(ctx, `DELETE FROM workspace_skills WHERE id = ?`, id)
}

// ---- commands ---------------------------------------------------------------------------------

// SkillDeps is what the skills commands need.
type SkillDeps struct {
	Store   *Store
	Paths   platform.Paths
	Emitter bridge.Emitter
}

// RegisterSkills adds the ten skill commands.
func RegisterSkills(r *bridge.Registry, deps SkillDeps) {
	r.Add("list_workspace_skills", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, err := bridge.Arg[string](p, "workspaceId")
		if err != nil {
			return nil, err
		}
		return deps.Store.ListSkills(ctx, workspaceID)
	})

	r.Add("install_workspace_skill", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, sourceRepo, err := twoStrings(p, "workspaceId", "sourceRepo")
		if err != nil {
			return nil, err
		}
		skillName, err := bridge.Arg[string](p, "skillName")
		if err != nil {
			return nil, err
		}
		return installSkill(ctx, deps, workspaceID, sourceRepo, skillName)
	})

	r.Add("create_custom_skill", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, name, err := twoStrings(p, "workspaceId", "name")
		if err != nil {
			return nil, err
		}
		skillMD, err := bridge.Arg[string](p, "skillMd")
		if err != nil {
			return nil, err
		}
		if err := validSkillName(name); err != nil {
			return nil, err
		}

		dir := skillDir(deps.Paths, workspaceID, name)
		if err := os.MkdirAll(dir, platform.DirPerm); err != nil {
			return nil, fmt.Errorf("create the skill folder: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), platform.FilePerm); err != nil {
			return nil, fmt.Errorf("write SKILL.md: %w", err)
		}
		return deps.Store.RecordSkill(ctx, workspaceID, name, SourceCustom)
	})

	r.Add("import_skill_from_folder", func(ctx context.Context, p bridge.Params) (any, error) {
		workspaceID, srcDir, err := twoStrings(p, "workspaceId", "srcDir")
		if err != nil {
			return nil, err
		}
		return importSkill(ctx, deps, workspaceID, srcDir)
	})

	r.Add("remove_workspace_skill", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		skill, err := deps.Store.SkillByID(ctx, id)
		if err != nil {
			return nil, notFoundAsError("skill", id, err)
		}
		// The folder goes first: a row without a folder is a skill the UI offers and cannot open,
		// while a folder without a row is invisible and harmless until the next install.
		if err := os.RemoveAll(skillDir(deps.Paths, skill.WorkspaceID, skill.SkillName)); err != nil {
			return nil, fmt.Errorf("remove the skill folder: %w", err)
		}
		return nil, deps.Store.DeleteSkillRow(ctx, id)
	})

	r.Add("set_workspace_skill_enabled", func(ctx context.Context, p bridge.Params) (any, error) {
		id, err := bridge.Arg[string](p, "id")
		if err != nil {
			return nil, err
		}
		enabled, err := bridge.Arg[bool](p, "enabled")
		if err != nil {
			return nil, err
		}
		return nil, notFoundAsError("skill", id, deps.Store.SetSkillEnabled(ctx, id, enabled))
	})

	registerSkillFiles(r, deps)
}

func registerSkillFiles(r *bridge.Registry, deps SkillDeps) {
	// Every path below goes through safeSkillPath before anything touches disk.
	resolve := func(p bridge.Params) (string, error) {
		workspaceID, skillName, err := twoStrings(p, "workspaceId", "skillName")
		if err != nil {
			return "", err
		}
		relPath, err := bridge.Arg[string](p, "relPath")
		if err != nil {
			return "", err
		}
		return safeSkillPath(skillDir(deps.Paths, workspaceID, skillName), relPath)
	}

	r.Add("list_skill_files", func(_ context.Context, p bridge.Params) (any, error) {
		workspaceID, skillName, err := twoStrings(p, "workspaceId", "skillName")
		if err != nil {
			return nil, err
		}
		return listSkillFiles(skillDir(deps.Paths, workspaceID, skillName))
	})

	r.Add("read_skill_file", func(_ context.Context, p bridge.Params) (any, error) {
		path, err := resolve(p)
		if err != nil {
			return nil, err
		}
		content, err := os.ReadFile(path) //nolint:gosec // the path went through safeSkillPath
		if err != nil {
			return nil, fmt.Errorf("read the skill file: %w", err)
		}
		return string(content), nil
	})

	r.Add("write_skill_file", func(_ context.Context, p bridge.Params) (any, error) {
		path, err := resolve(p)
		if err != nil {
			return nil, err
		}
		content, err := bridge.Arg[string](p, "content")
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), platform.DirPerm); err != nil {
			return nil, fmt.Errorf("create the skill folder: %w", err)
		}
		if err := os.WriteFile(path, []byte(content), platform.FilePerm); err != nil {
			return nil, fmt.Errorf("write the skill file: %w", err)
		}
		return nil, nil
	})

	r.Add("delete_skill_file", func(_ context.Context, p bridge.Params) (any, error) {
		path, err := resolve(p)
		if err != nil {
			return nil, err
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("delete the skill file: %w", err)
		}
		return nil, nil
	})
}

// listSkillFiles walks a skill folder and returns its files as relative, forward-slashed paths —
// the shape the renderer's file tree expects on both operating systems.
func listSkillFiles(dir string) ([]string, error) {
	files := make([]string, 0, 8)

	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		// A skill whose folder is gone lists nothing rather than failing: the row is what the UI
		// is working from, and an error here would make the whole panel unusable.
		return files, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list the skill's files: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

// validSkillName refuses a name that would escape the skills folder. A skill name becomes a
// directory name directly, so it gets the same treatment as a relative path.
func validSkillName(name string) error {
	if name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return fmt.Errorf("invalid skill name %q", name)
	}
	return nil
}

// installSkill runs `npx --yes skills add <repo> --skill <name>` and records the result (WS-005).
func installSkill(ctx context.Context, deps SkillDeps, workspaceID, sourceRepo, skillName string) (Skill, error) {
	if err := validSkillName(skillName); err != nil {
		return Skill{}, err
	}

	root := skillsRoot(deps.Paths, workspaceID)
	if err := os.MkdirAll(root, platform.DirPerm); err != nil {
		return Skill{}, fmt.Errorf("create the skill store: %w", err)
	}

	// npx is a .cmd shim on Windows; spawning it directly fails to launch at all, which is the
	// same class of issue as every other npm-installed shim.
	name, args := "npx", []string{"--yes", "skills", "add", sourceRepo, "--skill", skillName}
	if runtime.GOOS == "windows" {
		name, args = "cmd", append([]string{"/C", "npx"}, args...)
	}

	cmd := proc.Command(ctx, name, args...)
	// The outer folder, not .claude/skills — `npx skills add` creates that part itself.
	cmd.Dir = root

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Skill{}, fmt.Errorf("capture npx output: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Skill{}, fmt.Errorf("capture npx output: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return Skill{}, fmt.Errorf("run npx skills add: %w", err)
	}

	// Both streams are emitted as one event, indistinguishably, exactly as 2.x did — the installer
	// writes progress to both and telling them apart in the UI would be noise. They are collected
	// separately only so a failure can report the more useful of the two.
	var (
		mu                 sync.Mutex
		outLines, errLines []string
		wg                 sync.WaitGroup
	)
	pump := func(reader io.Reader, into *[]string) {
		defer wg.Done()
		for line := range proc.Lines(reader) {
			mu.Lock()
			*into = append(*into, line)
			mu.Unlock()
			if deps.Emitter != nil {
				deps.Emitter.Emit("skills:progress", map[string]any{"line": line})
			}
		}
	}
	wg.Add(2)
	safego.Go("skills-install-stdout", func() { pump(stdout, &outLines) })
	safego.Go("skills-install-stderr", func() { pump(stderr, &errLines) })
	wg.Wait()

	if err := cmd.Wait(); err != nil {
		detail := strings.Join(errLines, "\n")
		if strings.TrimSpace(detail) == "" {
			detail = strings.Join(outLines, "\n")
		}
		return Skill{}, fmt.Errorf("npx skills add failed: %s", detail)
	}

	// A zero exit status is not trusted on its own: the installer can report success and write
	// nothing, and a database row for a skill that is not on disk is worse than a failed install.
	if _, err := os.Stat(skillDir(deps.Paths, workspaceID, skillName)); err != nil {
		return Skill{}, fmt.Errorf("npx skills add reported success but %s was not created", skillName)
	}

	return deps.Store.RecordSkill(ctx, workspaceID, skillName, sourceRepo)
}

// importSkill copies an existing skill folder into the workspace's store.
func importSkill(ctx context.Context, deps SkillDeps, workspaceID, srcDir string) (Skill, error) {
	info, err := os.Stat(srcDir)
	if err != nil || !info.IsDir() {
		return Skill{}, fmt.Errorf("%s is not a folder", srcDir)
	}

	// A skill is defined by having a SKILL.md; importing a folder without one would produce an
	// entry the engines silently ignore.
	if _, err := os.Stat(filepath.Join(srcDir, "SKILL.md")); err != nil {
		return Skill{}, fmt.Errorf("%s has no SKILL.md", srcDir)
	}

	name := filepath.Base(filepath.Clean(srcDir))
	if err := validSkillName(name); err != nil {
		return Skill{}, err
	}

	dest := skillDir(deps.Paths, workspaceID, name)
	if err := os.RemoveAll(dest); err != nil {
		return Skill{}, fmt.Errorf("replace the existing skill: %w", err)
	}
	if err := copyTree(srcDir, dest); err != nil {
		return Skill{}, err
	}
	return deps.Store.RecordSkill(ctx, workspaceID, name, SourceLocal)
}

// copyTree copies a directory recursively. Symlinks are skipped rather than followed: a skill
// folder is user-supplied, and following one would copy whatever it points at into the store.
func copyTree(src, dest string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)

		// WalkDir only yields paths under src, so rel cannot climb out — but that is an argument,
		// not a check, and the source folder is whatever the user pointed at. Verifying the joined
		// path is still inside dest turns it into one, and costs a string comparison per file.
		if !strings.HasPrefix(target, dest+string(os.PathSeparator)) && target != dest {
			return fmt.Errorf("refusing to write %s outside the skill folder", rel)
		}

		switch {
		case entry.IsDir():
			return os.MkdirAll(target, platform.DirPerm)
		case entry.Type()&os.ModeSymlink != 0:
			return nil
		default:
			content, err := os.ReadFile(path) //nolint:gosec // walking a folder the user chose
			if err != nil {
				return fmt.Errorf("read %s: %w", rel, err)
			}
			if err := os.MkdirAll(filepath.Dir(target), platform.DirPerm); err != nil {
				return err
			}
			// gosec G703: target is verified to be inside dest a few lines above, before any of
			// these branches run. The taint analyser cannot follow that check across the switch.
			return os.WriteFile(target, content, platform.FilePerm) //nolint:gosec
		}
	})
}
