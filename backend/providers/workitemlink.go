package providers

import (
	"strconv"
	"strings"
)

// WorkItemAddress is a work item parsed out of pasted text — the renderer's `TicketLinkRef`.
//
// Org and Project are pointers because a bare id has neither: the caller fills them from the
// workspace's own account (WI-005), and a link that names a board must be able to override it.
type WorkItemAddress struct {
	ID      int64   `json:"id"`
	Org     *string `json:"org"`
	Project *string `json:"project"`
}

// ParseWorkItemLink reads a work-item address in any of the four shapes a user pastes (PROV-048,
// WI-010): the work-item page on either Azure host, any board URL carrying `?workitem=`, a bare id,
// and `AB#1234`.
//
// It deliberately does **not** reuse the pull-request link splitter, which throws the query string
// away. Every board and taskboard URL carries the id there, and that is the URL most likely to be
// in a clipboard — a taskboard is what a person is looking at when they decide to link a ticket.
//
// A Jira key is refused rather than read as a number: Jira is recognised in branch names (WI-006)
// and nowhere else, and silently turning `PROJ-45` into work item 45 would mirror a real Azure
// ticket under a Jira one's name.
func ParseWorkItemLink(text string) (WorkItemAddress, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return WorkItemAddress{}, false
	}

	// No slash at all means it cannot be a URL, so the bare forms are the only ones left. `AB#1234`
	// has no slash either, which is why this test comes before any URL handling.
	if !strings.Contains(trimmed, "/") {
		return parseBareWorkItem(trimmed)
	}
	return parseWorkItemURL(trimmed)
}

// parseBareWorkItem accepts `1234` and `AB#1234`, and nothing else that looks like an id.
func parseBareWorkItem(text string) (WorkItemAddress, bool) {
	value := text
	if after, found := cutPrefixFold(value, "AB#"); found {
		value = after
	}

	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id <= 0 {
		return WorkItemAddress{}, false
	}
	return WorkItemAddress{ID: id}, true
}

// parseWorkItemURL reads an Azure DevOps URL, query string included.
func parseWorkItemURL(text string) (WorkItemAddress, bool) {
	url := text
	if hash := strings.Index(url, "#"); hash >= 0 {
		url = url[:hash]
	}

	// The query is kept, unlike the pull-request splitter: it is where a board URL puts the id.
	// Cutting it before the trailing slash matters — a taskboard URL ends `…/Stories/?workitem=42`.
	query := ""
	if mark := strings.Index(url, "?"); mark >= 0 {
		url, query = url[:mark], url[mark+1:]
	}
	url = strings.TrimSuffix(url, "/")

	url, _ = cutScheme(url)
	url = stripUser(url)

	host, path, found := strings.Cut(url, "/")
	if !found || host == "" {
		return WorkItemAddress{}, false
	}

	segments := make([]string, 0, 6)
	for segment := range strings.SplitSeq(path, "/") {
		if segment != "" {
			segments = append(segments, percentDecode(segment))
		}
	}

	var org string
	switch {
	case strings.EqualFold(host, azureHostname):
		if len(segments) == 0 {
			return WorkItemAddress{}, false
		}
		org, segments = segments[0], segments[1:]

	case strings.HasSuffix(strings.ToLower(host), azureLegacySuffix):
		// The organisation keeps the host's own casing — it becomes part of a keychain key.
		org = host[:len(host)-len(azureLegacySuffix)]

	default:
		// Any other host: there is no organisation to report, and guessing one would mirror the
		// ticket under the wrong board.
		return WorkItemAddress{}, false
	}

	id, ok := workItemIDFromQuery(query)
	if !ok {
		id, ok = workItemIDFromPath(segments)
	}
	if !ok {
		return WorkItemAddress{}, false
	}

	address := WorkItemAddress{ID: id, Org: &org}

	// The project is the segment before Azure's own route names, which all start with an
	// underscore (`_workitems`, `_boards`, `_backlogs`). An organisation-scoped link has none.
	if len(segments) > 0 && !strings.HasPrefix(segments[0], "_") {
		project := segments[0]
		address.Project = &project
	}
	return address, true
}

// workItemIDFromQuery reads `workitem=1234` out of a query string, whatever its case or position.
func workItemIDFromQuery(query string) (int64, bool) {
	if query == "" {
		return 0, false
	}

	for pair := range strings.SplitSeq(query, "&") {
		key, value, found := strings.Cut(pair, "=")
		if !found || !strings.EqualFold(strings.TrimSpace(key), "workitem") {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || id <= 0 {
			return 0, false
		}
		return id, true
	}
	return 0, false
}

// workItemIDFromPath reads the `_workitems/edit/{id}` tail of a work-item page.
func workItemIDFromPath(segments []string) (int64, bool) {
	for i, segment := range segments {
		if !strings.EqualFold(segment, "_workitems") {
			continue
		}
		if i+2 >= len(segments) || !strings.EqualFold(segments[i+1], "edit") {
			return 0, false
		}
		id, err := strconv.ParseInt(segments[i+2], 10, 64)
		if err != nil || id <= 0 {
			return 0, false
		}
		return id, true
	}
	return 0, false
}

// cutPrefixFold is strings.CutPrefix, case-insensitively.
func cutPrefixFold(value, prefix string) (string, bool) {
	if len(value) < len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return value, false
	}
	return value[len(prefix):], true
}
