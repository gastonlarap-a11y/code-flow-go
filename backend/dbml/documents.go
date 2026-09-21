// Package dbml is the schema designer's backend: the documents in a folder, the positions a person
// gave their tables, the saved database connections, and the AI assistant that reads a schema.
//
// **Parsing, layout and rendering are not here.** They live in the renderer, where `@dbml/core` is,
// and that split is the shape of the feature: a schema document is a file in the user's folder, so
// opening and saving one are the file commands that already exist. What this side owns is the four
// things a webview cannot do — walk a folder, keep a layout, hold a credential, and reach a
// database.
package dbml

import (
	"context"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/docwalk"
)

// documentSuffix is what a schema document is called. Matched case-insensitively, so a file saved
// as `Orders.DBML` on a case-preserving filesystem is still a document.
const documentSuffix = ".dbml"

// ListDocuments walks a folder for `.dbml` files (DBML-001).
//
// Answers them project-relative, sorted, with `/` separators on every platform — they are keys into
// the layout store as well as labels in a picker, and a path that differed by separator would be a
// second document as far as the layout is concerned.
//
// The walk itself is `shared/docwalk`, shared with the diagram editor: what made it worth moving is
// that everything except this one constant was the same, down to the list of directories not worth
// reading and the error a missing folder answers.
func ListDocuments(ctx context.Context, rootPath string) ([]string, error) {
	return docwalk.List(ctx, rootPath, documentSuffix)
}
