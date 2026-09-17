package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/app"
	"github.com/gastonlarap-a11y/code-flow/backend/platform"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stageRecorder struct {
	stages []string
	errs   []error
}

func (r *stageRecorder) Record(stage string, failure error) {
	r.stages = append(r.stages, stage)
	r.errs = append(r.errs, failure)
}

func TestRunStagesReportsReadyAndCreatesTheThreeDirectories(t *testing.T) {
	base := filepath.Join(t.TempDir(), "CodeFlow")
	paths := platform.NewPaths(base)
	log := &stageRecorder{}

	state := app.RunStages(paths, log, nil)

	assert.Equal(t, app.StatusReady, state.Status)
	assert.False(t, state.Failed())
	assert.Empty(t, state.Detail)
	assert.Equal(t, paths.Logs(), state.LogsDirectory)
	assert.Empty(t, log.stages, "a clean start-up writes nothing to startup.log")

	assert.DirExists(t, base)
	assert.DirExists(t, paths.Logs())
	assert.DirExists(t, paths.Repos())
}

func TestRunStagesRunsTheStorageStage(t *testing.T) {
	paths := platform.NewPaths(filepath.Join(t.TempDir(), "CodeFlow"))
	opened := false

	state := app.RunStages(paths, nil, func() error { opened = true; return nil })

	assert.True(t, opened)
	assert.Equal(t, app.StatusReady, state.Status)
}

// The change this port makes to BOOT-032: a failed stage no longer ends the process. The window
// opens, and this state is what the renderer's banner shows instead of an app whose buttons all
// silently do nothing.
func TestAFailedStageLeavesAReportableStateInsteadOfDying(t *testing.T) {
	paths := platform.NewPaths(filepath.Join(t.TempDir(), "CodeFlow"))
	log := &stageRecorder{}

	state := app.RunStages(paths, log, func() error { return errors.New("database is locked") })

	assert.Equal(t, app.StatusDown, state.Status)
	assert.True(t, state.Failed())
	assert.Equal(t, "storage", state.FailedStage)
	assert.Contains(t, state.Detail, "database is locked")
	assert.Equal(t, paths.Logs(), state.LogsDirectory, "the banner needs a path to point at")
	assert.Equal(t, []string{"storage"}, log.stages)
}

// Later stages assume the earlier ones ran, so running them anyway would replace one clear
// failure with a cascade of confusing ones.
func TestAFailedStageStopsTheSequence(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "a-file")
	require.NoError(t, os.WriteFile(blocked, []byte("not a directory"), 0o644))
	log := &stageRecorder{}
	storageRan := false

	state := app.RunStages(platform.NewPaths(blocked), log, func() error { storageRan = true; return nil })

	assert.Equal(t, "directories", state.FailedStage)
	assert.False(t, storageRan, "storage must not run after directories failed")
	assert.Equal(t, []string{"directories"}, log.stages)
}

func TestResetMarkerWipesTheBaseDirectory(t *testing.T) {
	base := filepath.Join(t.TempDir(), "CodeFlow")
	paths := platform.NewPaths(base)
	require.NoError(t, paths.EnsureStartupDirectories())
	require.NoError(t, os.WriteFile(paths.Database(), []byte("old database"), 0o644))
	require.NoError(t, os.WriteFile(paths.ResetMarker(), nil, 0o644))

	state := app.RunStages(paths, nil, nil)

	assert.Equal(t, app.StatusReady, state.Status)
	assert.NoFileExists(t, paths.Database(), "the wipe must have removed the old data")
	assert.NoFileExists(t, paths.ResetMarker(), "the marker goes with the directory it asked to wipe")
	assert.DirExists(t, paths.Logs(), "the directories stage recreates the tree afterwards")
	assert.DirExists(t, paths.Repos())
}

func TestWithoutAMarkerTheDataSurvives(t *testing.T) {
	paths := platform.NewPaths(filepath.Join(t.TempDir(), "CodeFlow"))
	require.NoError(t, paths.EnsureStartupDirectories())
	require.NoError(t, os.WriteFile(paths.Database(), []byte("live database"), 0o644))

	app.RunStages(paths, nil, nil)

	assert.FileExists(t, paths.Database())
}

// RunStages must survive a nil logger: main builds one, but the Phase 2 database check and every
// test call it without.
func TestRunStagesToleratesANilLogger(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "a-file")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o644))

	assert.NotPanics(t, func() { app.RunStages(platform.NewPaths(blocked), nil, nil) })
}

func TestSmokeTestReturnsZeroWhenEveryProbePasses(t *testing.T) {
	code := app.RunSmokeTest([]app.Probe{
		{Name: "a", Run: func(context.Context) error { return nil }},
		{Name: "b", Run: func(context.Context) error { return nil }},
	})

	assert.Equal(t, 0, code)
}

func TestSmokeTestReturnsOneAndKeepsGoingWhenAProbeFails(t *testing.T) {
	ran := 0
	code := app.RunSmokeTest([]app.Probe{
		{Name: "a", Run: func(context.Context) error { ran++; return errors.New("no") }},
		{Name: "b", Run: func(context.Context) error { ran++; return nil }},
	})

	assert.Equal(t, 1, code)
	assert.Equal(t, 2, ran, "one failure must not hide the state of the others")
}

// The probe that replaces 2.x's LibGit2Sharp check. It runs against the real git on this machine,
// which is the point: it is an environment probe, not a unit test.
func TestDefaultProbesPassOnThisMachine(t *testing.T) {
	probes := app.DefaultProbes()
	require.NotEmpty(t, probes)

	for _, probe := range probes {
		t.Run(probe.Name, func(t *testing.T) {
			assert.NoError(t, probe.Run(t.Context()))
		})
	}
}
