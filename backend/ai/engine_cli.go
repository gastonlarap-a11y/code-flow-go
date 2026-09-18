package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// The three remaining subprocess engines.
//
// What separates them is not capability but where each CLI will accept a multi-line brief. Codex's
// positional argument is a fixed pointer sentence and the real instructions arrive on stdin;
// opencode reads no stdin at all and takes an attached file; agy reads neither and carries
// everything inline until the brief outgrows the argument limit. Every one of those is a property
// of the CLI, not a preference.

// briefSeparator joins the ask to its data, VERBATIM. Every engine that composes a brief uses it,
// so a prompt template written for one reads the same to another.
const briefSeparator = "\n\n----- INPUT -----\n\n"

// composeBrief is system prompt, then ask, then data — the order every engine shares.
func composeBrief(inv Invocation) string {
	var parts []string
	if inv.SystemPrompt != "" {
		parts = append(parts, inv.SystemPrompt)
	}
	if inv.Prompt != "" {
		parts = append(parts, inv.Prompt)
	}
	brief := strings.Join(parts, "\n\n")

	if inv.StdinContent != "" {
		brief += briefSeparator + inv.StdinContent
	}
	return brief
}

// ---- Codex ---------------------------------------------------------------------------------------

// codexPointer is the CLI's single positional argument. VERBATIM, single-line and ASCII.
//
// Kept shim-safe even though Codex is a native binary: a multi-line prompt template simply never
// touches argv here, which is one fewer platform difference to remember.
const codexPointer = "Follow the instructions in the input piped on stdin and reply with only the requested output."

// CodexCommands drives the `codex` CLI — a ChatGPT subscription at a flat fee, which is what
// separates it from the OpenAI engine paying per token against the same company's API.
type CodexCommands struct{}

// BuildCommand assembles `codex exec`.
func (CodexCommands) BuildCommand(inv Invocation) []string {
	args := []string{"exec"}

	// `resume <id>` is a subcommand of exec and goes before the prompt argument.
	if inv.SessionID != "" {
		args = append(args, "resume", inv.SessionID)
	}
	args = append(args, codexPointer, "--skip-git-repo-check")

	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}

	sandbox := "read-only"
	if inv.AutoApproveEdits {
		sandbox = "workspace-write"
	}
	args = append(args, "--sandbox", sandbox)

	// The config-key form rather than the flag: `codex exec` errors on `--ask-for-approval` as of
	// 0.145 while the config key works on every version.
	args = append(args, "-c", `approval_policy="never"`)

	// The sandbox's workspace root and the process's working directory are two separate things to
	// Codex, so both are set.
	if inv.WorkingDir != "" {
		args = append(args, "--cd", inv.WorkingDir)
	}
	return args
}

// StdinPayload is the whole brief: the positional argument is a pointer to it.
func (CodexCommands) StdinPayload(inv Invocation) string {
	brief := codexPointer
	if inv.SystemPrompt != "" {
		brief = inv.SystemPrompt + "\n\n" + brief
	}
	if inv.Prompt != "" {
		brief += "\n\n" + inv.Prompt
	}
	if inv.StdinContent != "" {
		brief += briefSeparator + inv.StdinContent
	}
	return brief
}

// codexSessionLine matches the human-readable preamble's session line.
//
// Leniently, and on purpose: the banner is prose the CLI prints for a person, not a committed
// format. A preamble that changes shape costs continuity — a fresh session next turn — rather than
// resuming an unrelated rollout, which is the better of the two failures.
var codexSessionLine = regexp.MustCompile(`(?i)session[ _]id:\s*([0-9a-f-]{8,})`)

// CodexInterpreter reads the reply from stdout, with stderr as progress.
type CodexInterpreter struct{ Binary string }

// Interpret takes stdout as the final agent message.
func (c CodexInterpreter) Interpret(stdout, stderr string, exitCode int) (Result, error) {
	session := ""
	if match := codexSessionLine.FindStringSubmatch(stderr); len(match) == 2 {
		session = match[1]
	}

	if exitCode != 0 {
		return Result{}, errors.New(Classify(FailureDetail(c.Binary, exitCode, stdout, stderr), false))
	}
	// Quota is tested over successful output too — that is BUG-AI-b's placement, shared by every
	// engine.
	if text := strings.TrimSpace(stdout); QuotaSignal(stderr) {
		return Result{}, errors.New(MarkQuota(text))
	}
	return Result{Text: strings.TrimSpace(stdout), SessionID: session}, nil
}

// ---- agy (the Antigravity CLI, labelled Gemini) ---------------------------------------------------

// inlineLimit is where a brief stops travelling inside the argument.
//
// A review diff alone can reach 120 000 characters, well past Windows' ~32k argv limit. Below the
// limit the brief is inline, which is simpler and leaves no file to clean up.
const inlineLimit = 12_000

// agySessionSentinel is what this engine reports as a session id. VERBATIM.
//
// It identifies nothing. `agy` cannot resume a specific conversation from print mode — the id is
// printed on neither stream and there is no JSON output — so the engine keeps `--continue`, which
// resumes agy's own idea of "the last run", and reports this string purely to keep the app's chat
// state at "there is a session". **Two conversations open on the same project can silently resume
// each other's context** (DIVERGENCE-AI-b): a known upstream limitation, accepted rather than
// worked around, and replaced by `--conversation <id>` when that lands.
const agySessionSentinel = "agy-last"

// AgyCommands drives `agy`. The provider is labelled Gemini because that is the login the user
// picks; the binary is not called gemini and that is deliberate (DIVERGENCE-AI-a).
type AgyCommands struct {
	// BriefPath is set by PrepareBrief when the brief was too large to pass inline. The runner
	// deletes the directory it lives in once the invocation ends.
	BriefPath string
}

// PrepareBrief writes an oversized brief to a scratch directory, returning the commands to use.
//
// A directory rather than a bare file because `--add-dir` grants agy a directory, and because the
// scratch sweep claims the whole entry at once. The caller deletes it on every exit path — reply,
// CLI error, launch failure or cancellation — which is what closed BUG-AI-a.
func PrepareBrief(inv Invocation) (AgyCommands, func(), error) {
	brief := composeBrief(inv)
	if len([]rune(brief)) <= inlineLimit {
		return AgyCommands{}, func() {}, nil
	}

	dir := filepath.Join(os.TempDir(), agyPrefix+uuid.NewString())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return AgyCommands{}, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	path := filepath.Join(dir, "brief.txt")
	if err := os.WriteFile(path, []byte(brief), 0o600); err != nil {
		cleanup()
		return AgyCommands{}, func() {}, err
	}
	return AgyCommands{BriefPath: path}, cleanup, nil
}

// BuildCommand assembles `agy -p …`.
func (a AgyCommands) BuildCommand(inv Invocation) []string {
	args := []string{"-p"}

	if a.BriefPath == "" {
		args = append(args, composeBrief(inv))
	} else {
		args = append(args,
			fmt.Sprintf("Read the file at %s and follow the instructions it contains.", a.BriefPath),
			"--add-dir", filepath.Dir(a.BriefPath))
	}

	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}

	// Permissions are all-or-nothing here: agy has no granular allow-list flag, so reading the
	// brief file headlessly needs the same flag that lets it write.
	if inv.AutoApproveEdits || a.BriefPath != "" {
		args = append(args, "--dangerously-skip-permissions")
	}
	if inv.SessionID != "" {
		args = append(args, "--continue")
	}
	return args
}

// StdinPayload is empty: `-p` does not read stdin. That is a **declaration** rather than an
// omission — AI-054 reads it to tell a broken pipe from an engine that never wanted one.
func (AgyCommands) StdinPayload(Invocation) string { return "" }

// AgyInterpreter reads stdout; any banner goes to stderr and is ignored on success.
type AgyInterpreter struct{ Binary string }

// Interpret answers the sentinel session id on success.
func (a AgyInterpreter) Interpret(stdout, stderr string, exitCode int) (Result, error) {
	if exitCode != 0 {
		return Result{}, errors.New(Classify(FailureDetail(a.Binary, exitCode, stdout, stderr), false))
	}
	if QuotaSignal(stderr) {
		return Result{}, errors.New(MarkQuota(strings.TrimSpace(stdout)))
	}
	return Result{Text: strings.TrimSpace(stdout), SessionID: agySessionSentinel}, nil
}

// ---- opencode -------------------------------------------------------------------------------------

// openCodePointer is the positional argument. VERBATIM, single-line and ASCII.
//
// It **must** come before `--file`, which is variadic and would otherwise swallow it as another
// attachment.
const openCodePointer = "Read the attached file and follow the instructions it contains."

// OpenCodeCommands drives `opencode run`.
//
// Three constraints shape it, all verified against a live run: the CLI reads no piped stdin, there
// is no system-prompt flag, and on Windows an npm-installed `opencode.cmd` runs through cmd.exe,
// which rejects any argument containing a newline — while every prompt template is multi-line.
// So the whole brief goes to a file.
type OpenCodeCommands struct{ PayloadPath string }

// PreparePayload writes the brief to a scratch file.
func PreparePayload(inv Invocation) (OpenCodeCommands, func(), error) {
	path := filepath.Join(os.TempDir(), openCodePrefix+uuid.NewString()+".txt")
	if err := os.WriteFile(path, []byte(composeBrief(inv)), 0o600); err != nil {
		return OpenCodeCommands{}, func() {}, err
	}
	return OpenCodeCommands{PayloadPath: path}, func() { _ = os.Remove(path) }, nil
}

// BuildCommand assembles `opencode run`.
func (o OpenCodeCommands) BuildCommand(inv Invocation) []string {
	args := []string{"run", openCodePointer}

	// Always set, not optional: it is the only way to recover the real session id.
	args = append(args, "--format", "json")

	if inv.Model != "" {
		args = append(args, "--model", inv.Model)
	}
	if inv.AutoApproveEdits {
		args = append(args, "--auto")
	}
	if inv.WorkingDir != "" {
		args = append(args, "--dir", inv.WorkingDir)
	}
	if inv.SessionID != "" {
		args = append(args, "--session", inv.SessionID)
	}
	if o.PayloadPath != "" {
		args = append(args, "--file", o.PayloadPath)
	}
	return args
}

// StdinPayload is empty: the CLI does not read stdin.
func (OpenCodeCommands) StdinPayload(Invocation) string { return "" }

// openCodeEvent is one line of the JSON event stream.
type openCodeEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	Part      struct {
		Text string `json:"text"`
	} `json:"part"`
	Error struct {
		Name string `json:"name"`
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	} `json:"error"`
}

// staleSessionHint is the Spanish, actionable replacement for a bare "Session not found".
//
// VERBATIM. The app re-sends the dead session id on every turn, so the raw CLI message gives the
// user no hint why the conversation looks permanently broken.
const staleSessionHint = "Esta conversación ya no existe en opencode. Empieza una nueva."

// OpenCodeInterpreter reads the event stream.
type OpenCodeInterpreter struct{ Binary string }

// Interpret judges an error event **before** the exit status.
//
// opencode can emit one on an otherwise zero-exit run — an expired provider token does exactly
// that — so the status alone would report success and hand the caller an empty reply.
func (o OpenCodeInterpreter) Interpret(stdout, stderr string, exitCode int) (Result, error) {
	texts, failure, session, parsed := parseOpenCodeEvents(stdout)

	if failure != "" {
		return Result{}, errors.New(Classify(withStaleSessionHint(failure), false))
	}
	if exitCode != 0 {
		detail := FailureDetail(o.Binary, exitCode, stdout, stderr)
		return Result{}, errors.New(Classify(withStaleSessionHint(detail), false))
	}

	// No parseable event at all: a build that ignored the format flag. Plain stdout is the reply,
	// just without a session id.
	if !parsed {
		return Result{Text: strings.TrimSpace(stdout)}, nil
	}
	return Result{Text: strings.Join(texts, "\n"), SessionID: session}, nil
}

// withStaleSessionHint rewrites the CLI's own bare message, matched anywhere and case-insensitively.
func withStaleSessionHint(detail string) string {
	if strings.Contains(strings.ToLower(detail), "session not found") {
		return staleSessionHint
	}
	return detail
}

// parseOpenCodeEvents reads the stream, taking the session id off the first event that carries one.
//
// Including an error event: a failed run can still report which session it failed in, which is
// what lets the next turn avoid reusing it.
func parseOpenCodeEvents(stdout string) (texts []string, failure, session string, parsed bool) {
	texts = make([]string, 0, 8)

	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var event openCodeEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		parsed = true

		if session == "" && event.SessionID != "" {
			session = event.SessionID
		}
		switch event.Type {
		case "text":
			if text := strings.TrimSpace(event.Part.Text); text != "" {
				texts = append(texts, text)
			}
		case "error":
			failure = combineOpenCodeError(event)
		}
	}
	return texts, failure, session, parsed
}

func combineOpenCodeError(event openCodeEvent) string {
	name, message := event.Error.Name, event.Error.Data.Message
	switch {
	case name != "" && message != "":
		return name + ": " + message
	case message != "":
		return message
	default:
		return name
	}
}
