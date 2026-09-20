package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// The Azure Boards client (PROV-045…047).
//
// A sibling of the pull-request endpoints rather than part of them: pull requests and work items are
// separate features that share a host. They share this file's transport with `AzureClient`, so a
// refused PAT is reported here exactly as `DIVERGENCE-PROV-b` specifies and the organisation cannot
// be normalised one way for one feature and another way for the other.
//
// **One call here writes**, and it is `AddComment`. Everything else reads. The line is drawn where
// `WI-022` draws it: a comment is additive and anyone can delete it, while a state transition moves
// a card other people are looking at and its legal states belong to the project's process.

const (
	// azureCommentsAPIVersion and azureIterationItemsAPIVersion are pinned to their preview suffixes
	// (PROV-046). A plain 7.1 is rejected on **both** with a 400 demanding the suffix, and these two
	// literals are the ones in this file most likely to be "tidied" into consistency with their
	// neighbours.
	azureCommentsAPIVersion       = "7.1-preview.4"
	azureIterationItemsAPIVersion = "7.1-preview.1"

	// azureWorkItemBatchSize is the server's own cap on `workitemsbatch`. A larger batch is rejected
	// outright rather than truncated, so the caller chunks.
	azureWorkItemBatchSize = 200
)

// RawWorkItem is one work item as Azure returns it.
//
// `Fields` is a map and cannot be a struct (PROV-047): Azure keys every field by reference name —
// `System.Title`, `Microsoft.VSTS.Common.AcceptanceCriteria` — a customised process adds its own,
// and one real board carries sixteen `Custom.*` fields, four of them named by GUID. A fixed shape
// would drop every one of them, which is exactly where a team's acceptance criteria live.
//
// The values stay `json.RawMessage` because they are not all strings: `System.AssignedTo` is an
// identity object and `System.CommentCount` a number. Reading one as the wrong type is then a
// caller's decision at the one place it matters, rather than a decode failure that loses the whole
// work item.
type RawWorkItem struct {
	ID        int64                      `json:"id"`
	Rev       int64                      `json:"rev"`
	Fields    map[string]json.RawMessage `json:"fields"`
	Relations []WorkItemRelation         `json:"relations"`
}

// WorkItemRelation is an attachment, a link or a parent — whatever the work item points at.
type WorkItemRelation struct {
	Rel        string `json:"rel"`
	URL        string `json:"url"`
	Attributes struct {
		Name    string `json:"name"`
		Comment string `json:"comment"`
	} `json:"attributes"`
}

// Text reads one field as a string, answering "" for an absent field and for one that is not text.
//
// The two are not told apart on purpose: every caller here wants "the title, or nothing", and a
// board whose process made `System.Title` an object would otherwise fail a sync rather than render
// a work item with a blank title.
func (w RawWorkItem) Text(field string) string {
	raw, found := w.Fields[field]
	if !found {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

// Identity reads the display name out of an identity field such as `System.AssignedTo`.
func (w RawWorkItem) Identity(field string) string {
	raw, found := w.Fields[field]
	if !found {
		return ""
	}
	var identity struct {
		DisplayName string `json:"displayName"`
	}
	if err := json.Unmarshal(raw, &identity); err != nil {
		return ""
	}
	return identity.DisplayName
}

// WorkItemWebURL is where a person opens a work item (PROV-045's table).
//
// Built rather than read from the payload: the `url` Azure returns is the API's own, which renders
// JSON in a browser instead of the board.
func WorkItemWebURL(org, project string, id int64) string {
	return fmt.Sprintf("https://dev.azure.com/%s/%s/_workitems/edit/%d",
		encodeSegment(NormalizeAzureOrg(org)), encodeSegment(project), id)
}

// GetWorkItem reads one work item with every field and relation it carries.
func (c AzureClient) GetWorkItem(ctx context.Context, project string, id int64) (RawWorkItem, error) {
	var item RawWorkItem
	url := withVersion(
		fmt.Sprintf("%s/_apis/wit/workitems/%d?$expand=all", c.projectRoot(project), id),
		azureAPIVersion)
	if err := c.get(ctx, url, &item); err != nil {
		return RawWorkItem{}, err
	}
	return item, nil
}

// BatchWorkItems reads many work items in one call, in chunks of 200.
//
// `errorPolicy: omit` is what makes a batch survive one unreadable id: without it a single work item
// the PAT cannot see fails the whole request, and a sprint list would go blank because of one card.
// Fields are named explicitly so a picker's list does not drag every custom field of every row.
func (c AzureClient) BatchWorkItems(ctx context.Context, project string, ids []int64, fields []string) ([]RawWorkItem, error) {
	items := make([]RawWorkItem, 0, len(ids))

	for start := 0; start < len(ids); start += azureWorkItemBatchSize {
		end := min(start+azureWorkItemBatchSize, len(ids))

		body := map[string]any{"ids": ids[start:end], "errorPolicy": "omit"}
		if len(fields) > 0 {
			body["fields"] = fields
		}

		var batch azureList[RawWorkItem]
		url := withVersion(c.projectRoot(project)+"/_apis/wit/workitemsbatch", azureAPIVersion)
		if err := c.post(ctx, url, body, &batch); err != nil {
			return nil, err
		}
		items = append(items, batch.Value...)
	}
	return items, nil
}

// QueryIDs runs a WIQL query and answers the ids it matched (PROV-045).
//
// The caller passes a **condition**, never a whole query, and this composes the `WHERE` around it.
// That is the rule rather than a convenience: the project segment in the URL does not reliably
// filter the query, and what happens without the clause differs by organisation — one answers 200
// with zero rows on every project, another answers 200 with every work item it has. Neither is an
// error, and the first is indistinguishable from "this board is empty", so the clause-less form is
// made unreachable instead of merely discouraged.
//
// The response carries ids whatever the `SELECT` names, which is why every query here is followed by
// a batch read.
func (c AzureClient) QueryIDs(ctx context.Context, project, condition string, top int) ([]int64, error) {
	query := fmt.Sprintf("SELECT [System.Id] FROM WorkItems WHERE [System.TeamProject] = '%s'",
		escapeWIQL(project))
	if trimmed := strings.TrimSpace(condition); trimmed != "" {
		query += " AND (" + trimmed + ")"
	}

	url := c.projectRoot(project) + "/_apis/wit/wiql"
	if top > 0 {
		url += fmt.Sprintf("?$top=%d", top)
	}

	var answer struct {
		WorkItems []struct {
			ID int64 `json:"id"`
		} `json:"workItems"`
	}
	if err := c.post(ctx, withVersion(url, azureAPIVersion), map[string]string{"query": query}, &answer); err != nil {
		return nil, err
	}

	ids := make([]int64, 0, len(answer.WorkItems))
	for _, item := range answer.WorkItems {
		ids = append(ids, item.ID)
	}
	return ids, nil
}

// escapeWIQL doubles a single quote, which is how WIQL escapes one. Unescaped, a project named
// `O'Brien` is a syntax error surfacing as an opaque 400.
func escapeWIQL(value string) string { return strings.ReplaceAll(value, "'", "''") }

// ListTeams is a project's teams — the first step of the sprint route.
func (c AzureClient) ListTeams(ctx context.Context, project string) ([]AdoRef, error) {
	var found azureList[AdoRef]
	url := withVersion(
		c.orgRoot()+"/_apis/projects/"+encodeSegment(project)+"/teams", azureAPIVersion)
	if err := c.get(ctx, url, &found); err != nil {
		return nil, err
	}
	return nonNil(found.Value), nil
}

// AzureIteration is one of a team's sprints.
type AzureIteration struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Attributes struct {
		TimeFrame string `json:"timeFrame"`
	} `json:"attributes"`
}

// Current reports the sprint that is running now. Azure says so itself rather than leaving it to a
// date comparison against a machine's own clock and time zone.
func (i AzureIteration) Current() bool {
	return strings.EqualFold(i.Attributes.TimeFrame, "current")
}

// TeamIterations lists a team's sprints.
func (c AzureClient) TeamIterations(ctx context.Context, project, team string) ([]AzureIteration, error) {
	var found azureList[AzureIteration]
	url := withVersion(
		c.projectRoot(project)+"/"+encodeSegment(team)+"/_apis/work/teamsettings/iterations",
		azureAPIVersion)
	if err := c.get(ctx, url, &found); err != nil {
		return nil, err
	}
	return nonNil(found.Value), nil
}

// IterationWorkItems is what sits on one sprint's taskboard (PROV-046: preview.1).
//
// The relations it answers are `(parent, child)` pairs of the board's hierarchy; what this needs is
// the set of ids, so a work item reached as somebody's child and as its own row is returned once.
func (c AzureClient) IterationWorkItems(ctx context.Context, project, team, iterationID string) ([]int64, error) {
	var answer struct {
		WorkItemRelations []struct {
			Target struct {
				ID int64 `json:"id"`
			} `json:"target"`
		} `json:"workItemRelations"`
	}

	url := withVersion(
		fmt.Sprintf("%s/%s/_apis/work/teamsettings/iterations/%s/workitems",
			c.projectRoot(project), encodeSegment(team), encodeSegment(iterationID)),
		azureIterationItemsAPIVersion)
	if err := c.get(ctx, url, &answer); err != nil {
		return nil, err
	}

	ids := make([]int64, 0, len(answer.WorkItemRelations))
	seen := make(map[int64]bool, len(answer.WorkItemRelations))
	for _, relation := range answer.WorkItemRelations {
		id := relation.Target.ID
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

// ListWorkItemTypes is a project's process types.
func (c AzureClient) ListWorkItemTypes(ctx context.Context, project string) ([]AdoRef, error) {
	var found azureList[struct {
		Name          string `json:"name"`
		ReferenceName string `json:"referenceName"`
	}]
	url := withVersion(c.projectRoot(project)+"/_apis/wit/workitemtypes", azureAPIVersion)
	if err := c.get(ctx, url, &found); err != nil {
		return nil, err
	}

	types := make([]AdoRef, 0, len(found.Value))
	for _, value := range found.Value {
		types = append(types, AdoRef{ID: value.ReferenceName, Name: value.Name})
	}
	return types, nil
}

// TypeFields lists the fields one work-item type declares — which is how the criteria picker knows
// that `Technical Story` does not have an acceptance-criteria field at all.
func (c AzureClient) TypeFields(ctx context.Context, project, workItemType string) ([]AdoRef, error) {
	var found azureList[struct {
		Name          string `json:"name"`
		ReferenceName string `json:"referenceName"`
	}]
	url := withVersion(
		c.projectRoot(project)+"/_apis/wit/workitemtypes/"+encodeSegment(workItemType)+"/fields",
		azureAPIVersion)
	if err := c.get(ctx, url, &found); err != nil {
		return nil, err
	}

	fields := make([]AdoRef, 0, len(found.Value))
	for _, value := range found.Value {
		fields = append(fields, AdoRef{ID: value.ReferenceName, Name: value.Name})
	}
	return fields, nil
}

// WorkItemComment is one comment on a work item.
type WorkItemComment struct {
	ID        int64  `json:"id"`
	Text      string `json:"text"`
	CreatedBy struct {
		DisplayName string `json:"displayName"`
	} `json:"createdBy"`
	CreatedDate string `json:"createdDate"`
}

// Comments reads a work item's discussion (PROV-046: preview.4).
func (c AzureClient) Comments(ctx context.Context, project string, id int64) ([]WorkItemComment, error) {
	var answer struct {
		Comments []WorkItemComment `json:"comments"`
	}
	url := withVersion(
		fmt.Sprintf("%s/_apis/wit/workItems/%d/comments", c.projectRoot(project), id),
		azureCommentsAPIVersion)
	if err := c.get(ctx, url, &answer); err != nil {
		return nil, err
	}
	return nonNil(answer.Comments), nil
}

// AddComment publishes one comment on a work item and answers it (WI-022).
//
// The **only** write this client makes. `html` is already HTML: Azure comments are rich text, so
// markdown posted as-is renders its own punctuation — `## VERIFICACIÓN` and `**cumple**`, literally,
// on a page other people read.
func (c AzureClient) AddComment(ctx context.Context, project string, id int64, html string) (WorkItemComment, error) {
	var comment WorkItemComment
	url := withVersion(
		fmt.Sprintf("%s/_apis/wit/workItems/%d/comments", c.projectRoot(project), id),
		azureCommentsAPIVersion)
	if err := c.post(ctx, url, map[string]string{"text": html}, &comment); err != nil {
		return WorkItemComment{}, err
	}
	return comment, nil
}

// GetAttachment downloads one attached file.
//
// The URL comes from the relation rather than being composed here: an attachment is addressed by the
// GUID Azure keyed it under, which is nowhere else in the payload. `download=true` is what makes the
// server answer with the bytes instead of with a JSON descriptor of them.
func (c AzureClient) GetAttachment(ctx context.Context, rawURL, fileName string) ([]byte, error) {
	url := rawURL
	if !strings.Contains(url, "?") {
		url += "?"
	} else {
		url += "&"
	}
	url += "fileName=" + encodeSegment(fileName) + "&download=true&api-version=" + azureAPIVersion

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build the attachment request: %w", err)
	}
	c.authorize(request)

	response, err := c.client.Do(request)
	if err != nil {
		return nil, &AzureError{Err: err}
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, &AzureError{Err: err}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, newAzureStatusError(response.StatusCode, string(body))
	}
	return body, nil
}
