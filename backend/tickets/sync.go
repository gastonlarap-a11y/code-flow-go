package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gastonlarap-a11y/code-flow/backend/providers"
)

// Fetching a work item, caching it and rewriting its mirror (WI-009).
//
// **Three triggers, never a timer**: on link, on an explicit refresh, and best-effort immediately
// before a review so the criteria being judged are current. A background poll would spend a PAT's
// rate budget on tickets nobody is looking at.

// The Azure field reference names this feature reads by name.
const (
	fieldTitle      = "System.Title"
	fieldState      = "System.State"
	fieldType       = "System.WorkItemType"
	fieldAssignedTo = "System.AssignedTo"
)

// summaryFields is what a picker's row needs. Asked for explicitly so a list of fifty work items
// does not drag every custom field of every one.
var summaryFields = []string{fieldTitle, fieldState, fieldType, fieldAssignedTo}

// attachmentBudget is how many bytes of attachments one sync downloads (WI-004). A work item with a
// video on it is not a reason to spend a hundred megabytes on a ticket nobody has opened.
const attachmentBudget = 16 << 20

// ErrNotNumeric refuses a non-numeric external id before any request. Azure numbers its work items;
// a Jira key reaching this point is a link that was accepted by the wrong parser.
var ErrNotNumeric = errors.New("that work item id is not a number")

// Sync fetches one work item, caches it and rewrites its mirror.
//
// The mirror write is best-effort and its failure is swallowed, for the reason `WI-009` gives: a
// full disk must not turn a successful fetch into a failed command. What the app reads is the cache.
func (d Deps) Sync(ctx context.Context, org, project, externalID string) (Ticket, error) {
	id, err := numericID(externalID)
	if err != nil {
		return Ticket{}, err
	}

	client, err := d.clientFor(org)
	if err != nil {
		return Ticket{}, err
	}

	item, err := client.GetWorkItem(ctx, project, id)
	if err != nil {
		return Ticket{}, err
	}

	normalisedOrg := providers.NormalizeAzureOrg(org)
	ticket := ticketFrom(item, normalisedOrg, project, externalID)

	existing, err := d.Store.Get(ctx, ticket.ID)
	switch {
	case err == nil:
		ticket.MirrorPath = MirrorFor(existing.MirrorPath, d.ticketsRoot(ctx),
			normalisedOrg, project, externalID, ticket.Title)
	case errors.Is(err, ErrNotFound):
		ticket.MirrorPath = MirrorPath(d.ticketsRoot(ctx), normalisedOrg, project, externalID, ticket.Title)
	default:
		return Ticket{}, err
	}

	raw, err := json.Marshal(item)
	if err != nil {
		return Ticket{}, fmt.Errorf("encode the work item payload: %w", err)
	}

	stored, err := d.Store.Upsert(ctx, ticket, string(raw))
	if err != nil {
		return Ticket{}, err
	}

	d.writeMirror(ctx, client, stored, item, string(raw))
	return stored, nil
}

// writeMirror renders the mirror and swallows what goes wrong doing it (WI-009).
func (d Deps) writeMirror(
	ctx context.Context,
	client providers.AzureClient,
	ticket Ticket,
	item providers.RawWorkItem,
	raw string,
) {
	criteria, err := d.criteriaFor(ctx, ticket, item)
	if err != nil {
		// A criteria read that failed is a mirror without its criteria file's content, not a failed
		// sync: the cache already has the work item and the review re-reads it from there.
		criteria = NoCriteria()
	}

	// `_ =` on purpose: the mirror is for people and for the model, and its failure must not fail
	// the fetch that succeeded.
	_ = WriteMirror(ticket.MirrorPath, MirrorContent{
		Ticket:      ticket,
		Markdown:    ToMarkdown(item.Text(FieldDescription)),
		Criteria:    criteria,
		RawJSON:     raw,
		Attachments: d.downloadAttachments(ctx, client, ticket.Project, item),
	})
}

// downloadAttachments fetches every `AttachedFile` relation within the per-sync budget (WI-004).
//
// One failure never fails the sync, and neither does running out of budget: both come back as an
// attachment with no content and a reason, which `ticket.md` then names.
func (d Deps) downloadAttachments(
	ctx context.Context,
	client providers.AzureClient,
	project string,
	item providers.RawWorkItem,
) []Attachment {
	_ = project

	attachments := make([]Attachment, 0, 4)
	spent := 0

	for _, relation := range item.Relations {
		if !strings.EqualFold(relation.Rel, "AttachedFile") {
			continue
		}

		attachment := Attachment{Name: relation.Attributes.Name, URL: relation.URL}
		if spent >= attachmentBudget {
			attachment.Failure = "supera el presupuesto de adjuntos de esta sincronización"
			attachments = append(attachments, attachment)
			continue
		}

		content, err := client.GetAttachment(ctx, relation.URL, relation.Attributes.Name)
		switch {
		case err != nil:
			attachment.Failure = "no se pudo descargar"
		case spent+len(content) > attachmentBudget:
			attachment.Failure = "supera el presupuesto de adjuntos de esta sincronización"
		default:
			attachment.Content = content
			spent += len(content)
		}
		attachments = append(attachments, attachment)
	}
	return attachments
}

// criteriaFor reads a work item's requirements, with the template comparison its board allows
// (WI-007, WI-008).
func (d Deps) criteriaFor(ctx context.Context, ticket Ticket, item providers.RawWorkItem) (Criteria, error) {
	order := CriteriaFieldOrder(d.setting(ctx,
		fmt.Sprintf("ticket_criteria_fields:%s:%s", ticket.Org, ticket.Project)))

	payloads, err := d.Store.OthersOfType(ctx, ticket.Org, ticket.Project, ticket.WorkItemType, ticket.ID)
	if err != nil {
		return NoCriteria(), err
	}

	others := map[string][]string{}
	for _, payload := range payloads {
		var cached providers.RawWorkItem
		if err := json.Unmarshal([]byte(payload), &cached); err != nil {
			// A cached payload that will not parse says nothing and excludes nothing.
			continue
		}
		for _, field := range order {
			if value := cached.Text(field); value != "" {
				others[field] = append(others[field], value)
			}
		}
	}

	return ReadCriteria(fieldsOf(item), order, others), nil
}

// fieldsOf adapts a fetched work item to what the criteria reader asks for.
func fieldsOf(item providers.RawWorkItem) RawFields {
	return fieldReader{item: item}
}

type fieldReader struct{ item providers.RawWorkItem }

func (f fieldReader) Field(name string) string { return f.item.Text(name) }

// ticketFrom projects a fetched work item onto the cached row.
func ticketFrom(item providers.RawWorkItem, org, project, externalID string) Ticket {
	var assigned *string
	if name := item.Identity(fieldAssignedTo); name != "" {
		assigned = &name
	}

	return Ticket{
		ID:           ID("azure", org, project, externalID),
		Provider:     "azure",
		Org:          org,
		Project:      project,
		ExternalID:   externalID,
		Title:        item.Text(fieldTitle),
		State:        item.Text(fieldState),
		WorkItemType: item.Text(fieldType),
		AssignedTo:   assigned,
		WebURL:       providers.WorkItemWebURL(org, project, item.ID),
		Rev:          item.Rev,
	}
}

// summaryFrom projects a batch-read work item onto a picker's row.
func summaryFrom(item providers.RawWorkItem) Summary {
	var assigned *string
	if name := item.Identity(fieldAssignedTo); name != "" {
		assigned = &name
	}
	return Summary{
		ExternalID:   strconv.FormatInt(item.ID, 10),
		Title:        item.Text(fieldTitle),
		State:        item.Text(fieldState),
		WorkItemType: item.Text(fieldType),
		AssignedTo:   assigned,
	}
}

// numericID refuses a non-numeric external id before any request is built (WI-009).
func numericID(externalID string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(externalID), 10, 64)
	if err != nil {
		return 0, ErrNotNumeric
	}
	return id, nil
}
