package tickets

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The readable copy of a work item on disk (WI-003, WI-004, WI-016).
//
// **The mirror owns four names and no more.** It rewrites exactly `ticket.md`,
// `acceptance-criteria.md`, `raw.json` and `attachments/`, and creates `notes/` empty on first sync.
// There is no recursive delete anywhere in this file, so anything else a user puts in the directory
// survives **by construction** rather than by a rule someone has to remember — the discipline
// `WS-007` applies to skill paths.

// The four names, and the two directories.
const (
	fileTicket     = "ticket.md"
	fileCriteria   = "acceptance-criteria.md"
	fileRaw        = "raw.json"
	dirAttachments = "attachments"
	dirNotes       = "notes"
)

// maxNotesChars is the budget the user's own notes reach the model within (WI-017).
const maxNotesChars = 20_000

// Attachment is one downloaded file on its way into the mirror.
type Attachment struct {
	// Name is the file name the board gave it, which is **not** unique: Azure keys attachments by
	// GUID, so one work item can legitimately carry two files called `captura.png`.
	Name string
	// URL is what the rendered markdown points at today, and what gets rewritten to the local copy.
	URL string
	// Content is nil for an attachment that failed or did not fit. Such a one is **named** in
	// `ticket.md` rather than silently absent: a screenshot the model cannot see is a fact worth
	// stating.
	Content []byte
	// Failure says why it is not here, for the line that names it.
	Failure string
}

// MirrorContent is everything a sync writes.
type MirrorContent struct {
	Ticket      Ticket
	Markdown    string
	Criteria    Criteria
	RawJSON     string
	Attachments []Attachment
}

// WriteMirror rewrites the four names under a ticket's directory (WI-003).
//
// Every write is caller-best-effort: the sync swallows an I/O failure, for the same reason
// `SkillSync` does — a full disk must not turn a successful fetch into a failed command. What the
// app reads is the cache; the mirror is for people and for the model.
func WriteMirror(directory string, content MirrorContent) error {
	// 0700 like the rest of `{base}`: a work item can carry a customer's name and an incident's
	// details, and nothing but this user's app has business reading it.
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("make the mirror directory: %w", err)
	}

	saved, err := writeAttachments(directory, content.Attachments)
	if err != nil {
		return err
	}

	body := renderTicketMarkdown(content, saved)
	if err := os.WriteFile(filepath.Join(directory, fileTicket), []byte(body), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", fileTicket, err)
	}

	criteria := content.Criteria.Markdown
	if strings.TrimSpace(criteria) == "" {
		// VERBATIM, Spanish: it is what a reader opens the file and sees.
		criteria = "_Este work item no declara criterios de aceptación verificables._"
	}
	if err := os.WriteFile(filepath.Join(directory, fileCriteria), []byte(criteria+"\n"), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", fileCriteria, err)
	}

	if err := os.WriteFile(filepath.Join(directory, fileRaw), []byte(content.RawJSON), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", fileRaw, err)
	}

	// Created empty on first sync and never written to again. This is the one directory in here
	// nobody but the user owns.
	if err := os.MkdirAll(filepath.Join(directory, dirNotes), 0o700); err != nil {
		return fmt.Errorf("make the notes directory: %w", err)
	}
	return nil
}

// writeAttachments empties `attachments/` **file by file** and writes what came down.
//
// Never by deleting the directory: a recursive delete is how a user's own file inside it would
// disappear, and there is not one anywhere in this type.
//
// Two attachments sharing a name both survive, the second suffixed. The answer maps each
// attachment's original URL to the file name it landed under, which is what relinking needs.
func writeAttachments(directory string, attachments []Attachment) (map[string]string, error) {
	folder := filepath.Join(directory, dirAttachments)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return nil, fmt.Errorf("make the attachments directory: %w", err)
	}

	entries, err := os.ReadDir(folder)
	if err != nil {
		return nil, fmt.Errorf("read the attachments directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			// A directory in here was not put there by this application, so it is left alone — the
			// same promise the rest of the mirror makes.
			continue
		}
		if err := os.Remove(filepath.Join(folder, entry.Name())); err != nil {
			return nil, fmt.Errorf("clear an old attachment: %w", err)
		}
	}

	saved := make(map[string]string, len(attachments))
	taken := make(map[string]bool, len(attachments))

	for _, attachment := range attachments {
		if attachment.Content == nil {
			continue
		}
		name := uniqueName(attachment.Name, taken)
		if err := os.WriteFile(filepath.Join(folder, name), attachment.Content, 0o600); err != nil {
			return nil, fmt.Errorf("write attachment %s: %w", name, err)
		}
		taken[name] = true
		saved[attachment.URL] = name
	}
	return saved, nil
}

// uniqueName suffixes a repeated file name before its extension, so `captura.png` and its twin land
// as `captura.png` and `captura-2.png`.
func uniqueName(name string, taken map[string]bool) string {
	cleaned := filepath.Base(strings.TrimSpace(name))
	if cleaned == "" || cleaned == "." || cleaned == string(filepath.Separator) {
		cleaned = "attachment"
	}
	if !taken[cleaned] {
		return cleaned
	}

	extension := filepath.Ext(cleaned)
	stem := strings.TrimSuffix(cleaned, extension)
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, n, extension)
		if !taken[candidate] {
			return candidate
		}
	}
}

// renderTicketMarkdown is `ticket.md`: the work item as a person reads it, with its image sources
// pointing at the local copies (WI-004).
func renderTicketMarkdown(content MirrorContent, saved map[string]string) string {
	out := &strings.Builder{}

	ticket := content.Ticket
	fmt.Fprintf(out, "# %s %s — %s\n\n", ticket.WorkItemType, ticket.ExternalID, ticket.Title)
	fmt.Fprintf(out, "- Estado: %s\n", ticket.State)
	if ticket.AssignedTo != nil && *ticket.AssignedTo != "" {
		fmt.Fprintf(out, "- Asignado a: %s\n", *ticket.AssignedTo)
	}
	fmt.Fprintf(out, "- Organización: %s / %s\n", ticket.Org, ticket.Project)
	fmt.Fprintf(out, "- URL: %s\n\n", ticket.WebURL)

	out.WriteString(Relink(content.Markdown, saved))
	out.WriteString("\n")

	if missing := missingAttachments(content.Attachments); len(missing) > 0 {
		// Named rather than silently absent: a screenshot the model cannot see is a fact worth
		// stating, and a review that never mentions it reads as if there were none.
		out.WriteString("\n## Adjuntos no disponibles\n\n")
		for _, line := range missing {
			fmt.Fprintf(out, "- %s\n", line)
		}
	}
	return out.String()
}

func missingAttachments(attachments []Attachment) []string {
	missing := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		if attachment.Content != nil {
			continue
		}
		reason := attachment.Failure
		if reason == "" {
			reason = "no se pudo descargar"
		}
		missing = append(missing, fmt.Sprintf("%s (%s)", attachment.Name, reason))
	}
	return missing
}

// Relink rewrites the image sources of the rendered markdown to the local copies (WI-004).
//
// A source with no local copy is left exactly as it was: it is still a link a person can follow in a
// browser, and rewriting it to a file that is not there would replace a working link with a broken
// image.
func Relink(markdown string, saved map[string]string) string {
	for url, name := range saved {
		markdown = strings.ReplaceAll(markdown, "("+url+")", "("+dirAttachments+"/"+name+")")
	}
	return markdown
}

// ReadNotes is what the user wrote about this ticket, for the review to read (WI-016).
//
// **Reading is not writing**: `WI-003` still holds, and nothing here creates, deletes or modifies.
// Text extensions only — a screenshot dropped in there is not something to paste into a prompt — and
// a note that will not read is skipped rather than failing the review.
//
// What a ticket leaves unsaid is exactly what a review judging "does this deliver it" is missing,
// which is why the directory that exists for the user is also the one the model is told about.
func ReadNotes(directory string) string {
	if strings.TrimSpace(directory) == "" {
		return ""
	}

	// Opened as a root rather than joined as a path: every read below then cannot leave the notes
	// directory, symlink included. That matters here more than it looks — whatever is read goes
	// into a prompt, so a link pointing at `~/.ssh` would be quoted to a model.
	notes, err := os.OpenRoot(filepath.Join(directory, dirNotes))
	if err != nil {
		return ""
	}
	defer func() { _ = notes.Close() }()

	entries, err := os.ReadDir(filepath.Join(directory, dirNotes))
	if err != nil {
		return ""
	}

	out := &strings.Builder{}
	for _, entry := range entries {
		if entry.IsDir() || !isTextNote(entry.Name()) {
			continue
		}
		body, err := readWithin(notes, entry.Name())
		if err != nil {
			// A note that will not read is skipped rather than failing the review.
			continue
		}
		if out.Len() >= maxNotesChars {
			break
		}
		fmt.Fprintf(out, "### %s\n%s\n\n", entry.Name(), strings.TrimSpace(body))
	}

	collected := strings.TrimSpace(out.String())
	if len([]rune(collected)) > maxNotesChars {
		// Cut by character and not by byte: cutting mid-character sends invalid UTF-8 to the model
		// and fails the whole response rather than shortening it.
		return string([]rune(collected)[:maxNotesChars])
	}
	return collected
}

// readWithin reads one file that must be inside the root, capped so a note nobody meant to write
// cannot spend the whole budget on its own.
func readWithin(root *os.Root, name string) (string, error) {
	file, err := root.Open(name)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	body, err := io.ReadAll(io.LimitReader(file, maxNotesChars))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func isTextNote(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".txt":
		return true
	default:
		return false
	}
}
