package git

import (
	"context"
	"fmt"
	"strings"
)

// BranchDiff is what one branch adds relative to another.
//
// Three dots, not two, and the difference is the whole point: `a..b` shows everything that differs
// between the two tips, which after the target branch has moved on includes other people's work.
// `a...b` compares against the merge base — what *this* branch changed — which is what a pull
// request is, and what a description drafted from it should describe.
func BranchDiff(ctx context.Context, repo, source, target string) (string, error) {
	result, err := NewRunner(repo).Run(ctx, "diff", "--no-color", target+"..."+source)
	if err != nil {
		return "", err
	}
	if result.Failed() {
		return "", fmt.Errorf("git diff failed: %s", result.Detail())
	}
	return strings.TrimRight(result.Stdout, "\n"), nil
}
