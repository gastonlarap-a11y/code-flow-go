// Package archify drives the user's own Archify install (github.com/tt-a1i/archify, MIT): finding
// it, rendering a document into its self-contained HTML page, and pinning the repository a document
// cites. Archify is never redistributed with the app; `npx skills add tt-a1i/archify -g` installs it.
package archify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrNotInstalled means no supported Archify install was found; the diagram editor's Mermaid export
// does not need one.
var ErrNotInstalled = errors.New("Archify is not installed: run `npx skills add tt-a1i/archify -g` once") //nolint:staticcheck // ST1005: shown to the user as a sentence

// ErrNoNode means Node.js, which Archify runs on, was not found.
var ErrNoNode = errors.New("Node.js was not found, so Archify cannot render the diagram") //nolint:staticcheck // ST1005: shown to the user as a sentence

// The Archify releases whose documents this app writes: 3.1 and later 3.x.
const (
	supportedMajor    = 3
	supportedMinMinor = 1
)

// Runtime is what an Archify run needs from the machine: node, and the PATH its child processes see.
type Runtime struct {
	Node string
	Path []string
}

// CLI is an installed Archify command line.
type CLI struct {
	Script  string
	Version string
}

// userSkillDirs are where `npx skills add -g` puts a skill for each agent it installs for, under the
// user's home (the skills CLI's README): Claude Code, Codex (and every agent reading `.agents`),
// opencode, Cursor and Antigravity — plus `.codex/skills`, where Codex read skills before.
var userSkillDirs = [][]string{
	{".claude", "skills"},
	{".agents", "skills"},
	{".config", "opencode", "skills"},
	{".cursor", "skills"},
	{".gemini", "antigravity", "skills"},
	{".codex", "skills"},
}

// projectSkillDirs are where `npx skills add` without `-g` puts it, in the repository.
var projectSkillDirs = [][]string{{".claude", "skills"}, {".agents", "skills"}}

// candidates are where Archify may be — for the user, then for the project — after an explicit
// ARCHIFY_BIN.
func candidates(root string) []string {
	out := make([]string, 0, 1+len(userSkillDirs)+len(projectSkillDirs))
	if bin := os.Getenv("ARCHIFY_BIN"); bin != "" {
		out = append(out, bin)
	}
	script := func(base string, dir []string) string {
		return filepath.Join(append(append([]string{base}, dir...), "archify", "bin", "archify.mjs")...)
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, dir := range userSkillDirs {
			out = append(out, script(home, dir))
		}
	}
	for _, dir := range projectSkillDirs {
		out = append(out, script(root, dir))
	}
	return out
}

// Located answers the first install found for the repository at root and its version, supported or
// not — what the requirements list shows, so an install too old for this app reads as one to update
// rather than as missing.
func Located(root string) (script, version string, found bool) {
	for _, script := range candidates(root) {
		if _, err := os.Stat(script); err != nil {
			continue
		}
		version, _ := versionOf(filepath.Join(filepath.Dir(script), "..", "package.json")) // unreadable reads as no version
		return script, version, true
	}
	return "", "", false
}

// Supported says whether this app works with an Archify version: 3.1 or a later 3.x.
func Supported(version string) bool { return supported(version) }

// Find answers the first supported install for the repository at root, or ErrNotInstalled.
func Find(root string) (CLI, error) {
	for _, script := range candidates(root) {
		if _, err := os.Stat(script); err != nil {
			continue
		}
		version, err := versionOf(filepath.Join(filepath.Dir(script), "..", "package.json"))
		if err != nil || !supported(version) {
			continue
		}
		return CLI{Script: script, Version: version}, nil
	}
	return CLI{}, ErrNotInstalled
}

func versionOf(manifest string) (string, error) {
	raw, err := os.ReadFile(manifest) //nolint:gosec // G304: Archify's own package.json, beside the script found
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", manifest, err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return "", fmt.Errorf("decoding %s: %w", manifest, err)
	}
	return pkg.Version, nil
}

func supported(version string) bool {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, errMajor := strconv.Atoi(parts[0])
	minor, errMinor := strconv.Atoi(parts[1])
	return errMajor == nil && errMinor == nil && major == supportedMajor && minor >= supportedMinMinor
}

// renderQuality is the quality every page is drawn at, and every document checked at: one Archify
// accepts here it also draws.
const renderQuality = "standard"

// Deliver validates and renders one document with `archify deliver`, in dir, checking its
// citations against the checkout at repoRoot — "" for a document that pins no repository. Answers
// Archify's own report.
func (c CLI) Deliver(ctx context.Context, rt Runtime, diagramType, dir, document, html, repoRoot string) (string, error) {
	args := []string{"deliver", diagramType, document, html, "--quality", renderQuality, "--json"}
	if repoRoot != "" {
		args = append(args, "--repo-root", repoRoot)
	}
	stdout, stderr, err := c.run(ctx, rt, dir, args...)
	report := strings.TrimSpace(string(stdout) + "\n" + stderr)
	if errors.Is(err, ErrNoNode) {
		return "", err
	}
	if err != nil {
		return report, fmt.Errorf("archify could not render the diagram: %s", firstLine(problem(report, err))) //nolint:err113 // Archify's own words are the message
	}
	return report, nil
}

// problem picks the first thing Archify said was wrong from its JSON report.
func problem(report string, err error) string {
	var parsed struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(report), &parsed) == nil {
		if len(parsed.Errors) > 0 {
			return parsed.Errors[0].Message
		}
		if parsed.Error != "" {
			return parsed.Error
		}
	}
	if report != "" {
		return report
	}
	return err.Error()
}

// firstLine is the first line of s, at most 300 characters, which is all a toast has room for.
func firstLine(s string) string {
	line, rest, _ := strings.Cut(strings.TrimSpace(s), "\n")
	// "class schema validation failed:" says nothing until the line under it.
	if strings.HasSuffix(line, ":") {
		if detail, _, _ := strings.Cut(strings.TrimSpace(rest), "\n"); detail != "" {
			line += " " + strings.TrimSpace(detail)
		}
	}
	if runes := []rune(line); len(runes) > 300 {
		return string(runes[:299]) + "…"
	}
	return line
}
