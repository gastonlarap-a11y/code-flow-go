//go:build !windows

package proc_test

import (
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The child must not share CodeFlow's process group, or a signal it raises against its own group
// reaches the app. This is BOOT-037 inverted, and the only way to observe it is from the child.
//
// Unix-only by build tag rather than a runtime skip: Windows has no pgid to compare, and its
// equivalent (CREATE_NEW_PROCESS_GROUP) is verified by the Phase 1 manual checklist.
func TestChildGetsItsOwnProcessGroup(t *testing.T) {
	out, err := proc.Command(t.Context(), "/bin/sh", "-c", "ps -o pgid= -p $$").Output()
	require.NoError(t, err)

	childPGID := strings.TrimSpace(string(out))
	require.NotEmpty(t, childPGID)

	own, err := syscall.Getpgid(syscall.Getpid())
	require.NoError(t, err)
	assert.NotEqual(t, strconv.Itoa(own), childPGID, "the child must lead a group of its own")
}

// KillTree's whole reason for existing: `claude` spawns node, which spawns more. Killing only the
// direct child leaves the grandchild burning CPU after the user pressed stop.
func TestKillTreeReachesAGrandchild(t *testing.T) {
	marker := t.TempDir() + "/grandchild-alive"

	// The shell backgrounds a grandchild that would create the marker in one second, then waits.
	// If KillTree only reached the shell, the grandchild would survive and create it.
	cmd := proc.Command(t.Context(), "/bin/sh", "-c",
		"(sleep 1; touch "+marker+") & sleep 30")
	require.NoError(t, cmd.Start())

	require.NoError(t, cmd.KillTree())

	// Past the grandchild's own deadline, so an absent marker means it was killed rather than
	// merely slow.
	time.Sleep(2 * time.Second)
	assert.NoFileExists(t, marker, "the grandchild outlived KillTree")
}
