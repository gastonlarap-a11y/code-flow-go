package apiclient_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/apiclient"
	"github.com/gastonlarap-a11y/code-flow/backend/storage"
)

var clock = storage.FixedClock{At: time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)}

func newStore(t *testing.T) (*apiclient.Store, *storage.DB) {
	t.Helper()
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "codeflow.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, db.Write(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
		for _, id := range []string{"w1", "w2"} {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO workspaces (id, name, created_at) VALUES (?, ?, 'now')`,
				id, "Workspace "+id); err != nil {
				return err
			}
		}
		return nil
	}))
	return apiclient.NewStore(db, clock), db
}

// ---- the tree ------------------------------------------------------------------------------------

func TestLoadTreeIsScopedThroughTheCollection(t *testing.T) {
	store, _ := newStore(t)

	mine, err := store.CreateCollection(t.Context(), "w1", "Payments API")
	require.NoError(t, err)
	theirs, err := store.CreateCollection(t.Context(), "w2", "Otro workspace")
	require.NoError(t, err)

	_, err = store.CreateFolder(t.Context(), mine.ID, nil, "v1")
	require.NoError(t, err)
	_, err = store.CreateRequest(t.Context(), mine.ID, nil, "List", "http", `{"method":"GET","url":"/x"}`)
	require.NoError(t, err)
	_, err = store.CreateFolder(t.Context(), theirs.ID, nil, "no debería salir")
	require.NoError(t, err)

	tree, err := store.LoadTree(t.Context(), "w1")
	require.NoError(t, err)

	require.Len(t, tree.Collections, 1)
	assert.Equal(t, "Payments API", tree.Collections[0].Name)
	// Folders and requests carry no workspace column: they are scoped by joining up to their
	// collection, which is the one place a row's workspace can be wrong.
	require.Len(t, tree.Folders, 1)
	assert.Equal(t, "v1", tree.Folders[0].Name)
	require.Len(t, tree.Requests, 1)
}

func TestAnEmptyTreeIsThreeEmptyArraysNotThreeNulls(t *testing.T) {
	store, _ := newStore(t)

	tree, err := store.LoadTree(t.Context(), "w1")
	require.NoError(t, err)

	// A nil slice marshals as `null` and the panel that maps over it crashes — while the same
	// workspace with one collection works.
	assert.NotNil(t, tree.Collections)
	assert.NotNil(t, tree.Folders)
	assert.NotNil(t, tree.Requests)
	assert.Empty(t, tree.Collections)
}

// ---- denormalisation (STORE-018) --------------------------------------------------------------

func TestDenormalize(t *testing.T) {
	tests := []struct {
		name   string
		spec   string
		method string
		url    string
	}{
		{
			name:   "both present",
			spec:   `{"method":"POST","url":"https://api.test/v1/pagos"}`,
			method: "POST",
			url:    "https://api.test/v1/pagos",
		},
		{
			name:   "an empty method defaults to GET",
			spec:   `{"method":"","url":"/x"}`,
			method: "GET",
			url:    "/x",
		},
		{
			name: "a missing url has no fallback",
			spec: `{"method":"PUT"}`,
			// Deliberately asymmetric: a request with no URL is a request the user has not
			// finished, and inventing one would put a lie in the tree row.
			method: "PUT",
			url:    "",
		},
		{
			name:   "a non-string value reads as absent",
			spec:   `{"method":42,"url":{"raw":"/x"}}`,
			method: "GET",
			url:    "",
		},
		{
			name: "a spec that is not JSON at all",
			// Silently `("GET", "")` rather than an error: the spec is the renderer's to own, and a
			// malformed one must not fail a save.
			spec:   "no es json",
			method: "GET",
			url:    "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			method, url := apiclient.Denormalize(test.spec)
			assert.Equal(t, test.method, method)
			assert.Equal(t, test.url, url)
		})
	}
}

func TestCreateRequestDenormalizesIntoTheTreeColumns(t *testing.T) {
	store, _ := newStore(t)
	collection, err := store.CreateCollection(t.Context(), "w1", "API")
	require.NoError(t, err)

	request, err := store.CreateRequest(t.Context(), collection.ID, nil, "Crear pago", "http",
		`{"method":"POST","url":"https://api.test/pagos","headers":[]}`)
	require.NoError(t, err)

	assert.Equal(t, "POST", request.Method)
	assert.Equal(t, "https://api.test/pagos", request.URL)

	// And again on update, so the two columns cannot drift from the blob they describe.
	request.Spec = `{"method":"DELETE","url":"https://api.test/pagos/1"}`
	require.NoError(t, store.UpdateRequest(t.Context(), request))

	tree, err := store.LoadTree(t.Context(), "w1")
	require.NoError(t, err)
	require.Len(t, tree.Requests, 1)
	assert.Equal(t, "DELETE", tree.Requests[0].Method)
	assert.Equal(t, "https://api.test/pagos/1", tree.Requests[0].URL)
}

// ---- moving (STORE-017) --------------------------------------------------------------------------

// The guard that matters: a folder moved inside its own subtree detaches that whole subtree, and
// nothing would ever name it again.
func TestAFolderCannotBeMovedInsideItself(t *testing.T) {
	store, _ := newStore(t)
	collection, err := store.CreateCollection(t.Context(), "w1", "API")
	require.NoError(t, err)

	parent, err := store.CreateFolder(t.Context(), collection.ID, nil, "v1")
	require.NoError(t, err)
	child, err := store.CreateFolder(t.Context(), collection.ID, &parent.ID, "pagos")
	require.NoError(t, err)
	grandchild, err := store.CreateFolder(t.Context(), collection.ID, &child.ID, "reembolsos")
	require.NoError(t, err)

	t.Run("directly into itself", func(t *testing.T) {
		err := store.MoveNode(t.Context(), "folder", parent.ID, collection.ID, &parent.ID, 0)
		assert.ErrorIs(t, err, apiclient.ErrFolderIntoItself)
	})

	t.Run("into its own child", func(t *testing.T) {
		err := store.MoveNode(t.Context(), "folder", parent.ID, collection.ID, &child.ID, 0)
		assert.ErrorIs(t, err, apiclient.ErrFolderIntoItself)
	})

	t.Run("into its own grandchild", func(t *testing.T) {
		err := store.MoveNode(t.Context(), "folder", parent.ID, collection.ID, &grandchild.ID, 0)
		assert.ErrorIs(t, err, apiclient.ErrFolderIntoItself)
	})

	t.Run("and a refused move leaves nothing half-applied", func(t *testing.T) {
		tree, err := store.LoadTree(t.Context(), "w1")
		require.NoError(t, err)

		byID := map[string]*string{}
		for _, folder := range tree.Folders {
			byID[folder.ID] = folder.ParentID
		}
		assert.Nil(t, byID[parent.ID])
		require.NotNil(t, byID[child.ID])
		assert.Equal(t, parent.ID, *byID[child.ID])
	})
}

func TestANodeCannotBeMovedToAnotherWorkspacesCollection(t *testing.T) {
	store, _ := newStore(t)

	mine, err := store.CreateCollection(t.Context(), "w1", "Mía")
	require.NoError(t, err)
	theirs, err := store.CreateCollection(t.Context(), "w2", "Ajena")
	require.NoError(t, err)

	folder, err := store.CreateFolder(t.Context(), mine.ID, nil, "v1")
	require.NoError(t, err)
	request, err := store.CreateRequest(t.Context(), mine.ID, nil, "List", "http", "{}")
	require.NoError(t, err)

	assert.ErrorIs(t,
		store.MoveNode(t.Context(), "folder", folder.ID, theirs.ID, nil, 0),
		apiclient.ErrCrossWorkspace)
	assert.ErrorIs(t,
		store.MoveNode(t.Context(), "request", request.ID, theirs.ID, nil, 0),
		apiclient.ErrCrossWorkspace)
}

func TestMovingAFolderCarriesItsWholeSubtreeIntoTheNewCollection(t *testing.T) {
	store, _ := newStore(t)

	source, err := store.CreateCollection(t.Context(), "w1", "Origen")
	require.NoError(t, err)
	target, err := store.CreateCollection(t.Context(), "w1", "Destino")
	require.NoError(t, err)

	parent, err := store.CreateFolder(t.Context(), source.ID, nil, "v1")
	require.NoError(t, err)
	child, err := store.CreateFolder(t.Context(), source.ID, &parent.ID, "pagos")
	require.NoError(t, err)
	request, err := store.CreateRequest(t.Context(), source.ID, &child.ID, "Crear", "http", "{}")
	require.NoError(t, err)

	require.NoError(t, store.MoveNode(t.Context(), "folder", parent.ID, target.ID, nil, 0))

	tree, err := store.LoadTree(t.Context(), "w1")
	require.NoError(t, err)

	folders := map[string]apiclient.Folder{}
	for _, folder := range tree.Folders {
		folders[folder.ID] = folder
	}
	// Without the recursive rewrite the descendants stay addressed to the old collection, and the
	// tree renders them nowhere at all.
	assert.Equal(t, target.ID, folders[parent.ID].CollectionID)
	assert.Equal(t, target.ID, folders[child.ID].CollectionID, "the child follows its parent")

	require.Len(t, tree.Requests, 1)
	assert.Equal(t, target.ID, tree.Requests[0].CollectionID, "and so does the request beneath it")
	assert.Equal(t, request.ID, tree.Requests[0].ID)
}

func TestMoveRenumbersTheDestinationDensely(t *testing.T) {
	store, _ := newStore(t)
	collection, err := store.CreateCollection(t.Context(), "w1", "API")
	require.NoError(t, err)

	names := []string{"uno", "dos", "tres", "cuatro"}
	created := make([]apiclient.Request, 0, len(names))
	for _, name := range names {
		request, err := store.CreateRequest(t.Context(), collection.ID, nil, name, "http", "{}")
		require.NoError(t, err)
		created = append(created, request)
	}

	// Drag the last one to the front.
	require.NoError(t, store.MoveNode(t.Context(), "request", created[3].ID, collection.ID, nil, 0))

	tree, err := store.LoadTree(t.Context(), "w1")
	require.NoError(t, err)
	require.Len(t, tree.Requests, 4)

	ordered := make([]string, 0, 4)
	for position, request := range tree.Requests {
		ordered = append(ordered, request.Name)
		assert.Equal(t, int64(position), request.SortOrder, "dense 0..n, with no gaps")
	}
	assert.Equal(t, []string{"cuatro", "uno", "dos", "tres"}, ordered)
}

func TestAnOutOfRangeIndexIsClamped(t *testing.T) {
	store, _ := newStore(t)
	collection, err := store.CreateCollection(t.Context(), "w1", "API")
	require.NoError(t, err)

	first, err := store.CreateRequest(t.Context(), collection.ID, nil, "uno", "http", "{}")
	require.NoError(t, err)
	_, err = store.CreateRequest(t.Context(), collection.ID, nil, "dos", "http", "{}")
	require.NoError(t, err)

	// The renderer supplies a target index from a drag, against a list that may have changed under
	// it. Clamped rather than rejected: the drag still lands somewhere sensible.
	require.NoError(t, store.MoveNode(t.Context(), "request", first.ID, collection.ID, nil, 99))

	tree, err := store.LoadTree(t.Context(), "w1")
	require.NoError(t, err)
	assert.Equal(t, "dos", tree.Requests[0].Name)
	assert.Equal(t, "uno", tree.Requests[1].Name)
}

// Folders and requests are renumbered against siblings of their own kind only: the two tables have
// independent sequences, because the tree always renders folders above requests.
func TestFoldersAndRequestsAreRenumberedIndependently(t *testing.T) {
	store, _ := newStore(t)
	collection, err := store.CreateCollection(t.Context(), "w1", "API")
	require.NoError(t, err)

	folderA, err := store.CreateFolder(t.Context(), collection.ID, nil, "a")
	require.NoError(t, err)
	folderB, err := store.CreateFolder(t.Context(), collection.ID, nil, "b")
	require.NoError(t, err)
	requestA, err := store.CreateRequest(t.Context(), collection.ID, nil, "uno", "http", "{}")
	require.NoError(t, err)
	requestB, err := store.CreateRequest(t.Context(), collection.ID, nil, "dos", "http", "{}")
	require.NoError(t, err)

	require.NoError(t, store.MoveNode(t.Context(), "folder", folderB.ID, collection.ID, nil, 0))

	tree, err := store.LoadTree(t.Context(), "w1")
	require.NoError(t, err)

	folders := map[string]int64{}
	for _, folder := range tree.Folders {
		folders[folder.ID] = folder.SortOrder
	}
	assert.Equal(t, int64(0), folders[folderB.ID])
	assert.Equal(t, int64(1), folders[folderA.ID])

	requests := map[string]int64{}
	for _, request := range tree.Requests {
		requests[request.ID] = request.SortOrder
	}
	assert.Equal(t, int64(0), requests[requestA.ID], "the requests were not touched")
	assert.Equal(t, int64(1), requests[requestB.ID])
}

func TestAnUnknownNodeKindIsRefusedByName(t *testing.T) {
	store, _ := newStore(t)
	collection, err := store.CreateCollection(t.Context(), "w1", "API")
	require.NoError(t, err)

	err = store.MoveNode(t.Context(), "collection", "whatever", collection.ID, nil, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Unknown node kind collection")
}

func TestReorderCollectionsIgnoresAnotherWorkspacesID(t *testing.T) {
	store, _ := newStore(t)

	first, err := store.CreateCollection(t.Context(), "w1", "primera")
	require.NoError(t, err)
	second, err := store.CreateCollection(t.Context(), "w1", "segunda")
	require.NoError(t, err)
	foreign, err := store.CreateCollection(t.Context(), "w2", "ajena")
	require.NoError(t, err)

	require.NoError(t, store.ReorderCollections(t.Context(), "w1",
		[]string{second.ID, foreign.ID, first.ID}))

	tree, err := store.LoadTree(t.Context(), "w1")
	require.NoError(t, err)
	require.Len(t, tree.Collections, 2)
	assert.Equal(t, "segunda", tree.Collections[0].Name)
	assert.Equal(t, "primera", tree.Collections[1].Name)

	// The foreign id was named in the list and is not adopted: a reorder is not a move.
	other, err := store.LoadTree(t.Context(), "w2")
	require.NoError(t, err)
	require.Len(t, other.Collections, 1)
	assert.Equal(t, foreign.ID, other.Collections[0].ID)
}

// ---- duplicating (STORE-019) ---------------------------------------------------------------------

// The two-pass insert is what this pins: a child folder can appear before its own parent in the
// source order, and a single pass would violate the self-referencing foreign key.
func TestDuplicatingACollectionDeepCopiesItsWholeSubtree(t *testing.T) {
	store, _ := newStore(t)
	collection, err := store.CreateCollection(t.Context(), "w1", "Payments")
	require.NoError(t, err)

	parent, err := store.CreateFolder(t.Context(), collection.ID, nil, "v1")
	require.NoError(t, err)
	child, err := store.CreateFolder(t.Context(), collection.ID, &parent.ID, "pagos")
	require.NoError(t, err)
	_, err = store.CreateRequest(t.Context(), collection.ID, &child.ID, "Crear", "http",
		`{"method":"POST","url":"/pagos"}`)
	require.NoError(t, err)
	_, err = store.CreateRequest(t.Context(), collection.ID, nil, "Raíz", "http", "{}")
	require.NoError(t, err)

	copied, err := store.DuplicateCollection(t.Context(), collection.ID)
	require.NoError(t, err)

	assert.Equal(t, "Payments copy", copied.Name)
	assert.NotEqual(t, collection.ID, copied.ID)
	assert.Greater(t, copied.SortOrder, collection.SortOrder, "the copy lands last")

	tree, err := store.LoadTree(t.Context(), "w1")
	require.NoError(t, err)
	assert.Len(t, tree.Collections, 2)
	assert.Len(t, tree.Folders, 4, "two folders, copied")
	assert.Len(t, tree.Requests, 4, "two requests, copied")

	// The copy's nesting survived: its child folder points at its *own* new parent, not the
	// original's.
	copies := map[string]apiclient.Folder{}
	for _, folder := range tree.Folders {
		if folder.CollectionID == copied.ID {
			copies[folder.Name] = folder
		}
	}
	require.Len(t, copies, 2)
	assert.Nil(t, copies["v1"].ParentID)
	require.NotNil(t, copies["pagos"].ParentID)
	assert.Equal(t, copies["v1"].ID, *copies["pagos"].ParentID)
	assert.NotEqual(t, parent.ID, copies["v1"].ID, "fresh ids throughout")

	// And the request under the child came with it, reparented to the copy.
	for _, request := range tree.Requests {
		if request.CollectionID != copied.ID || request.FolderID == nil {
			continue
		}
		assert.Equal(t, copies["pagos"].ID, *request.FolderID)
	}
}

func TestDuplicatingARequestLandsLastAmongItsSiblings(t *testing.T) {
	store, _ := newStore(t)
	collection, err := store.CreateCollection(t.Context(), "w1", "API")
	require.NoError(t, err)

	source, err := store.CreateRequest(t.Context(), collection.ID, nil, "Crear pago", "http",
		`{"method":"POST","url":"/pagos"}`)
	require.NoError(t, err)
	_, err = store.CreateRequest(t.Context(), collection.ID, nil, "Otro", "http", "{}")
	require.NoError(t, err)

	copied, err := store.DuplicateRequest(t.Context(), source.ID)
	require.NoError(t, err)

	assert.Equal(t, "Crear pago copy", copied.Name)
	assert.Equal(t, source.Spec, copied.Spec)
	assert.Equal(t, "POST", copied.Method)
	assert.Equal(t, int64(2), copied.SortOrder)
}

func TestDuplicatingSomethingThatIsNotThere(t *testing.T) {
	store, _ := newStore(t)

	_, err := store.DuplicateCollection(t.Context(), "no-such-id")
	assert.ErrorIs(t, err, apiclient.ErrNotFound)

	_, err = store.DuplicateRequest(t.Context(), "no-such-id")
	assert.ErrorIs(t, err, apiclient.ErrNotFound)
}

// ---- deletes -------------------------------------------------------------------------------------

func TestDeletingACollectionTakesItsSubtreeByCascade(t *testing.T) {
	store, _ := newStore(t)
	collection, err := store.CreateCollection(t.Context(), "w1", "API")
	require.NoError(t, err)

	folder, err := store.CreateFolder(t.Context(), collection.ID, nil, "v1")
	require.NoError(t, err)
	_, err = store.CreateRequest(t.Context(), collection.ID, &folder.ID, "Crear", "http", "{}")
	require.NoError(t, err)

	require.NoError(t, store.DeleteCollection(t.Context(), collection.ID))

	tree, err := store.LoadTree(t.Context(), "w1")
	require.NoError(t, err)
	// By cascade at the schema level, not by three deletes written here — which is what keeps a
	// new child table from being forgotten.
	assert.Empty(t, tree.Collections)
	assert.Empty(t, tree.Folders)
	assert.Empty(t, tree.Requests)
}
