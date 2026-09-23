package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Commit is one entry of the history graph.
type Commit struct {
	ID          string   `json:"id"`
	ShortID     string   `json:"short_id"`
	Summary     string   `json:"summary"`
	AuthorName  string   `json:"author_name"`
	AuthorEmail string   `json:"author_email"`
	Timestamp   int64    `json:"timestamp"`
	ParentIDs   []string `json:"parent_ids"`
	Refs        []string `json:"refs"`
}

// commitFormat uses US (0x1f) between fields rather than a printable separator: a commit summary
// can contain anything a person can type, and every printable delimiter has been used in one at
// some point. The records themselves are NUL-separated by -z.
const commitFormat = "%H%x1f%P%x1f%an%x1f%ae%x1f%at%x1f%s"

// ListCommits walks the history (GIT-020).
//
// allRefs walks branches **and remotes, but not tags** — that is 2.x's behaviour and the graph is
// built around it: a repository with hundreds of release tags would otherwise draw a lane per tag.
// Without it the walk is HEAD only.
func ListCommits(ctx context.Context, repo string, allRefs bool, limit int64) ([]Commit, error) {
	commits := make([]Commit, 0, 64)

	// A limit of zero means "none", not "unlimited": the renderer passes it while a view is
	// closing, and asking git for an unbounded log on a large repository would block for seconds
	// producing something nobody is going to look at.
	if limit <= 0 {
		return commits, nil
	}

	args := []string{"log", "--topo-order", "--date-order", "-z", "--format=" + commitFormat}
	if allRefs {
		args = append(args, "--branches", "--remotes")
	} else {
		args = append(args, "HEAD")
	}
	args = append(args, "-n", strconv.FormatInt(limit, 10))

	runner := NewRunner(repo)
	result, err := runner.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	if result.Failed() {
		// An empty repository has no HEAD, and `git log` fails rather than printing nothing. That
		// is a state the UI shows as "no commits yet", not an error.
		if strings.Contains(result.Detail(), "does not have any commits yet") ||
			strings.Contains(result.Detail(), "unknown revision") {
			return commits, nil
		}
		return nil, fmt.Errorf("git log failed: %s", result.Detail())
	}

	refs, err := refsByCommit(ctx, runner)
	if err != nil {
		return nil, err
	}

	for _, record := range splitNUL(result.Stdout) {
		fields := strings.Split(record, "\x1f")
		if len(fields) < 6 {
			continue
		}

		commit := Commit{
			ID:          fields[0],
			AuthorName:  fields[2],
			AuthorEmail: fields[3],
			Summary:     fields[5],
			ParentIDs:   make([]string, 0, 2),
			Refs:        make([]string, 0, 1),
		}
		// Seven characters, as libgit2's short id was. Not git's own abbreviation, which varies
		// with repository size — a short id that changed length between repositories would make
		// the graph's columns jump.
		if len(commit.ID) >= 7 {
			commit.ShortID = commit.ID[:7]
		}
		if parents := strings.Fields(fields[1]); len(parents) > 0 {
			commit.ParentIDs = append(commit.ParentIDs, parents...)
		}
		if timestamp, err := strconv.ParseInt(fields[4], 10, 64); err == nil {
			commit.Timestamp = timestamp
		}
		if named, ok := refs[commit.ID]; ok {
			commit.Refs = append(commit.Refs, named...)
		}
		commits = append(commits, commit)
	}
	return commits, nil
}

// refsByCommit maps a commit id to the refs pointing at it, for the labels on the graph.
//
// `%(*objectname)` is the peeled target: an annotated tag is an object of its own pointing at a
// commit, so without peeling its label would attach to nothing in the graph.
func refsByCommit(ctx context.Context, runner Runner) (map[string][]string, error) {
	result, err := runner.Run(ctx, "for-each-ref", "--format=%(objectname) %(*objectname) %(refname:short)")
	if err != nil {
		return nil, err
	}
	if result.Failed() {
		return nil, fmt.Errorf("git for-each-ref failed: %s", result.Detail())
	}

	refs := make(map[string][]string, 16)
	for line := range strings.SplitSeq(result.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		// Three fields means the ref was peeled: object, peeled target, name. Two means it points
		// straight at a commit.
		target, name := fields[0], fields[len(fields)-1]
		if len(fields) >= 3 {
			target = fields[1]
		}
		refs[target] = append(refs[target], name)
	}
	return refs, nil
}

// ListUnpushedCommits is the commits on HEAD that the upstream does not have (GIT-021).
//
// `@{upstream}..HEAD` when there is an upstream, and `HEAD --not --remotes` when there is none —
// the second is the case that matters, because a branch created locally and never pushed has no
// upstream at all, and reporting nothing for it is what made 1.7.2's version useless.
func ListUnpushedCommits(ctx context.Context, repo string) ([]Commit, error) {
	commits := make([]Commit, 0, 16)
	runner := NewRunner(repo)

	upstream, err := runner.Run(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return nil, err
	}

	args := []string{"log", "--topo-order", "-z", "--format=" + commitFormat}
	if upstream.Failed() {
		args = append(args, "HEAD", "--not", "--remotes")
	} else {
		args = append(args, "@{upstream}..HEAD")
	}

	result, err := runner.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	if result.Failed() {
		if strings.Contains(result.Detail(), "does not have any commits yet") ||
			strings.Contains(result.Detail(), "unknown revision") {
			return commits, nil
		}
		return nil, fmt.Errorf("git log failed: %s", result.Detail())
	}

	for _, record := range splitNUL(result.Stdout) {
		fields := strings.Split(record, "\x1f")
		if len(fields) < 6 {
			continue
		}
		commit := Commit{
			ID: fields[0], AuthorName: fields[2], AuthorEmail: fields[3], Summary: fields[5],
			ParentIDs: make([]string, 0, 2), Refs: make([]string, 0),
		}
		if len(commit.ID) >= 7 {
			commit.ShortID = commit.ID[:7]
		}
		if parents := strings.Fields(fields[1]); len(parents) > 0 {
			commit.ParentIDs = append(commit.ParentIDs, parents...)
		}
		if timestamp, err := strconv.ParseInt(fields[4], 10, 64); err == nil {
			commit.Timestamp = timestamp
		}
		commits = append(commits, commit)
	}
	return commits, nil
}
