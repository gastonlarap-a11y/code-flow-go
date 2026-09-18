package ai_test

import (
	"os"
	"strings"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/ai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func argsOf(builder ai.CommandBuilder, inv ai.Invocation) string {
	return strings.Join(builder.BuildCommand(inv), " ")
}

// ---- Codex ----------------------------------------------------------------------------------

// The positional argument is a fixed pointer and the real brief travels on stdin, so a multi-line
// prompt template never touches argv.
func TestCodexSendsItsBriefOnStdin(t *testing.T) {
	inv := ai.Invocation{
		SystemPrompt: "you are a reviewer",
		Prompt:       "line one\nline two",
		StdinContent: "the diff",
	}

	args := argsOf(ai.CodexCommands{}, inv)
	payload := ai.CodexCommands{}.StdinPayload(inv)

	assert.NotContains(t, args, "line two", "nothing multi-line reaches argv")
	assert.Contains(t, args, "exec")
	assert.Contains(t, payload, "you are a reviewer")
	assert.Contains(t, payload, "line one\nline two")
	assert.Contains(t, payload, "----- INPUT -----")
	assert.Contains(t, payload, "the diff")
}

func TestCodexSandboxFollowsWriteAccess(t *testing.T) {
	assert.Contains(t, argsOf(ai.CodexCommands{}, ai.Invocation{}), "--sandbox read-only")
	assert.Contains(t, argsOf(ai.CodexCommands{}, ai.Invocation{AutoApproveEdits: true}),
		"--sandbox workspace-write")
}

// The config-key form, not the flag: `codex exec` errors on --ask-for-approval as of 0.145 while
// the config key works on every version.
func TestCodexSetsApprovalThroughTheConfigKey(t *testing.T) {
	args := argsOf(ai.CodexCommands{}, ai.Invocation{})

	assert.Contains(t, args, `-c approval_policy="never"`)
	assert.NotContains(t, args, "--ask-for-approval")
	// Always: a link-only review runs in an app-owned workspace with no .git directory.
	assert.Contains(t, args, "--skip-git-repo-check")
}

// `resume <id>` is a subcommand of exec and has to come before the prompt argument.
func TestCodexResumeComesBeforeThePrompt(t *testing.T) {
	args := ai.CodexCommands{}.BuildCommand(ai.Invocation{SessionID: "roll-1"})

	require.GreaterOrEqual(t, len(args), 4)
	assert.Equal(t, []string{"exec", "resume", "roll-1"}, args[:3])
}

// Scraped leniently from prose the CLI prints for a person, not from a committed format.
func TestCodexReadsItsSessionFromThePreamble(t *testing.T) {
	stderr := "codex 0.150\nsession id: 3f8a2b61-0000-4444-8888-aaaabbbbcccc\nworking…\n"

	result, err := ai.CodexInterpreter{Binary: "codex"}.Interpret("the answer\n", stderr, 0)

	require.NoError(t, err)
	assert.Equal(t, "the answer", result.Text)
	assert.Equal(t, "3f8a2b61-0000-4444-8888-aaaabbbbcccc", result.SessionID)
}

// A preamble that changed shape costs continuity — a fresh session next turn — rather than
// resuming an unrelated rollout, which is the better of the two failures.
func TestCodexWithNoSessionLineReportsNone(t *testing.T) {
	result, err := ai.CodexInterpreter{Binary: "codex"}.Interpret("answer\n", "a new banner shape\n", 0)

	require.NoError(t, err)
	assert.Empty(t, result.SessionID)
}

// ---- agy ------------------------------------------------------------------------------------

func TestAgyCarriesASmallBriefInline(t *testing.T) {
	inv := ai.Invocation{SystemPrompt: "system", Prompt: "ask", StdinContent: "data"}

	commands, cleanup, err := ai.PrepareBrief(inv)
	defer cleanup()
	require.NoError(t, err)

	args := argsOf(commands, inv)
	assert.Contains(t, args, "system")
	assert.Contains(t, args, "ask")
	assert.Contains(t, args, "data")
	assert.NotContains(t, args, "--add-dir")
	assert.NotContains(t, args, "--dangerously-skip-permissions",
		"an inline brief needs no file to read")
}

// A review diff alone can reach 120 000 characters, past the ~32k Windows argv limit.
func TestAgyWritesALargeBriefToAFile(t *testing.T) {
	inv := ai.Invocation{Prompt: "ask", StdinContent: strings.Repeat("x", 20_000)}

	commands, cleanup, err := ai.PrepareBrief(inv)
	defer cleanup()
	require.NoError(t, err)

	args := argsOf(commands, inv)
	assert.Contains(t, args, "--add-dir")
	// Reading it headlessly needs the flag: there is no way to answer an approval prompt.
	assert.Contains(t, args, "--dangerously-skip-permissions")
	assert.NotContains(t, args, strings.Repeat("x", 100), "the data is in the file, not in argv")

	assert.True(t, ai.IsScratchPath(commands.BriefPath), "the sweep has to recognise it")
	content, err := os.ReadFile(commands.BriefPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "ask")
}

// Deleted on every exit path, which is what closed BUG-AI-a.
func TestAgyCleansUpItsBriefDirectory(t *testing.T) {
	commands, cleanup, err := ai.PrepareBrief(ai.Invocation{StdinContent: strings.Repeat("x", 20_000)})
	require.NoError(t, err)
	require.FileExists(t, commands.BriefPath)

	cleanup()

	assert.NoFileExists(t, commands.BriefPath)
}

// The CLI does not read stdin, and saying so is a declaration AI-054 reads — not an omission.
func TestAgyAndOpenCodeDeclareTheyReadNoStdin(t *testing.T) {
	inv := ai.Invocation{StdinContent: "would be ignored"}

	assert.Empty(t, ai.AgyCommands{}.StdinPayload(inv))
	assert.Empty(t, ai.OpenCodeCommands{}.StdinPayload(inv))
}

// It identifies nothing: agy cannot resume a specific conversation from print mode, so the string
// exists only to keep the app's chat state at "there is a session" (DIVERGENCE-AI-b).
func TestAgyReportsItsFixedSessionSentinel(t *testing.T) {
	result, err := ai.AgyInterpreter{Binary: "agy"}.Interpret("answer\n", "banner\n", 0)

	require.NoError(t, err)
	assert.Equal(t, "agy-last", result.SessionID)
	assert.Contains(t, argsOf(ai.AgyCommands{}, ai.Invocation{SessionID: "agy-last"}), "--continue")
}

// ---- opencode -------------------------------------------------------------------------------

// The pointer must precede --file, which is variadic and would otherwise swallow it as another
// attachment.
func TestOpenCodePutsItsPointerBeforeTheFile(t *testing.T) {
	commands, cleanup, err := ai.PreparePayload(ai.Invocation{Prompt: "ask", StdinContent: "data"})
	defer cleanup()
	require.NoError(t, err)

	args := commands.BuildCommand(ai.Invocation{})

	require.GreaterOrEqual(t, len(args), 2)
	assert.Equal(t, "run", args[0])
	assert.NotEqual(t, "--file", args[1], "the positional pointer comes first")

	pointerAt, fileAt := -1, -1
	for i, arg := range args {
		if arg == "--file" {
			fileAt = i
		}
		if strings.HasPrefix(arg, "Read the attached file") {
			pointerAt = i
		}
	}
	require.Positive(t, fileAt)
	require.GreaterOrEqual(t, pointerAt, 0)
	assert.Less(t, pointerAt, fileAt)
}

// Always set, not optional: it is the only way to recover the real session id.
func TestOpenCodeAlwaysAsksForJSON(t *testing.T) {
	assert.Contains(t, argsOf(ai.OpenCodeCommands{}, ai.Invocation{}), "--format json")
}

func TestOpenCodeReadsItsEventStream(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"start","sessionID":"ses_abc"}`,
		`{"type":"text","sessionID":"ses_abc","part":{"text":"first"}}`,
		`{"type":"text","sessionID":"ses_abc","part":{"text":"second"}}`,
	}, "\n")

	result, err := ai.OpenCodeInterpreter{Binary: "opencode"}.Interpret(stream, "", 0)

	require.NoError(t, err)
	assert.Equal(t, "first\nsecond", result.Text, "text parts joined in order")
	assert.Equal(t, "ses_abc", result.SessionID)
}

// opencode can emit an error event on an otherwise zero-exit run — an expired provider token does
// exactly that — so the status alone would report success and hand back an empty reply.
func TestAnErrorEventBeatsACleanExitStatus(t *testing.T) {
	stream := `{"type":"error","sessionID":"ses_1","error":{"name":"AuthError","data":{"message":"token expired"}}}`

	_, err := ai.OpenCodeInterpreter{Binary: "opencode"}.Interpret(stream, "", 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "AuthError: token expired")
}

// The app re-sends the dead id on every turn, so the raw CLI message leaves the user with a
// conversation that looks permanently broken and no hint why.
func TestAStaleSessionGetsAnActionableMessage(t *testing.T) {
	_, err := ai.OpenCodeInterpreter{Binary: "opencode"}.
		Interpret(`{"type":"error","error":{"name":"Error","data":{"message":"Session not found"}}}`, "", 1)

	require.Error(t, err)
	assert.Equal(t, "Esta conversación ya no existe en opencode. Empieza una nueva.", err.Error())
}

// A build that ignored the format flag still works, just without a session id.
func TestOpenCodeFallsBackToPlainStdout(t *testing.T) {
	result, err := ai.OpenCodeInterpreter{Binary: "opencode"}.Interpret("just text\n", "", 0)

	require.NoError(t, err)
	assert.Equal(t, "just text", result.Text)
	assert.Empty(t, result.SessionID)
}

// Even a failed run reports which session it failed in, which is what lets the next turn avoid
// reusing it.
func TestAFailedRunStillReportsItsSession(t *testing.T) {
	stream := `{"type":"error","sessionID":"ses_9","error":{"name":"Boom","data":{"message":"went wrong"}}}`

	_, err := ai.OpenCodeInterpreter{Binary: "opencode"}.Interpret(stream, "", 1)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Boom: went wrong")
}
