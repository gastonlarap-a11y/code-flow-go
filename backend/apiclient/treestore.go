package apiclient

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/gastonlarap-a11y/code-flow/backend/storage"
)

// The collections, folders and requests store (STORE-017…019).
//
// Three rules here are not CRUD and each came out of a real failure: a folder cannot be moved
// inside itself, a node cannot cross workspaces, and a duplicated collection's folders are inserted
// in two passes because a child can appear before its parent in the source order.

// maxFolderDepth caps the walk that looks for a cycle.
//
// A chain that does not terminate within this many hops is treated as cyclic and the move is
// **blocked** — a deliberate fail-closed default. The alternative on a corrupt `parent_id` chain is
// a loop that never ends, in a process that also holds the user's terminals.
const maxFolderDepth = 256

// historyHardCap is the per-workspace backstop every insert trims to.
//
// Deliberately well above the settings UI's own `historyLimit` (500), which only decides how many
// rows are *shown*: this one is the bound on what the database carries, and it is scoped to the
// workspace that just received an entry so heavy traffic in one never evicts another's history.
const historyHardCap = 2000

// Store is every query this feature makes.
type Store struct {
	db    *storage.DB
	clock storage.Clock
}

// NewStore wires the store.
func NewStore(db *storage.DB, clock storage.Clock) *Store {
	if clock == nil {
		clock = storage.SystemClock{}
	}
	return &Store{db: db, clock: clock}
}

// ---- the tree ------------------------------------------------------------------------------------

// LoadTree reads one workspace's collections, folders and requests in a single round trip.
//
// Folders and requests are scoped **through** their collection rather than by a workspace column of
// their own: there is exactly one place a row's workspace can be wrong, which is what keeps a moved
// collection from stranding its children in another workspace's tree.
func (s *Store) LoadTree(ctx context.Context, workspaceID string) (Tree, error) {
	tree := Tree{
		Collections: make([]Collection, 0, 8),
		Folders:     make([]Folder, 0, 16),
		Requests:    make([]Request, 0, 32),
	}

	err := s.db.Read(ctx, func(ctx context.Context, db *sql.DB) error {
		collections, err := readCollections(ctx, db, workspaceID)
		if err != nil {
			return err
		}
		folders, err := readFolders(ctx, db, workspaceID)
		if err != nil {
			return err
		}
		requests, err := readRequests(ctx, db, workspaceID)
		if err != nil {
			return err
		}
		tree.Collections, tree.Folders, tree.Requests = collections, folders, requests
		return nil
	})
	return tree, err
}

func readCollections(ctx context.Context, db *sql.DB, workspaceID string) ([]Collection, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, workspace_id, name, description, auth, pre_script, post_script, variables,
		       sort_order, created_at, updated_at
		  FROM api_collections
		 WHERE workspace_id = ?
		 ORDER BY sort_order, created_at`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Collection, 0, 8)
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.WorkspaceID, &c.Name, &c.Description, &c.Auth,
			&c.PreScript, &c.PostScript, &c.Variables, &c.SortOrder,
			&c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan a collection: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func readFolders(ctx context.Context, db *sql.DB, workspaceID string) ([]Folder, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT f.id, f.collection_id, f.parent_id, f.name, f.description, f.auth,
		       f.pre_script, f.post_script, f.sort_order, f.created_at
		  FROM api_folders f
		  JOIN api_collections c ON c.id = f.collection_id
		 WHERE c.workspace_id = ?
		 ORDER BY f.sort_order, f.created_at`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Folder, 0, 16)
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.ID, &f.CollectionID, &f.ParentID, &f.Name, &f.Description,
			&f.Auth, &f.PreScript, &f.PostScript, &f.SortOrder, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan a folder: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func readRequests(ctx context.Context, db *sql.DB, workspaceID string) ([]Request, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT r.id, r.collection_id, r.folder_id, r.name, r.protocol, r.method, r.url,
		       r.spec, r.sort_order, r.created_at, r.updated_at
		  FROM api_requests r
		  JOIN api_collections c ON c.id = r.collection_id
		 WHERE c.workspace_id = ?
		 ORDER BY r.sort_order, r.created_at`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list requests: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Request, 0, 32)
	for rows.Next() {
		var r Request
		if err := rows.Scan(&r.ID, &r.CollectionID, &r.FolderID, &r.Name, &r.Protocol,
			&r.Method, &r.URL, &r.Spec, &r.SortOrder, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan a request: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- collections ---------------------------------------------------------------------------------

// CreateCollection inserts an empty collection, last in its workspace.
func (s *Store) CreateCollection(ctx context.Context, workspaceID, name string) (Collection, error) {
	now := s.clock.Now()
	collection := Collection{
		ID:          uuid.NewString(),
		WorkspaceID: workspaceID,
		Name:        name,
		Variables:   "[]",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	err := s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		order, err := nextOrder(ctx, tx,
			`SELECT COALESCE(MAX(sort_order) + 1, 0) FROM api_collections WHERE workspace_id = ?`,
			workspaceID)
		if err != nil {
			return err
		}
		collection.SortOrder = order

		_, err = tx.ExecContext(ctx, `
			INSERT INTO api_collections (id, workspace_id, name, description, auth, pre_script,
			                             post_script, variables, sort_order, created_at, updated_at)
			VALUES (?, ?, ?, '', '', '', '', '[]', ?, ?, ?)`,
			collection.ID, workspaceID, name, collection.SortOrder, now, now)
		if err != nil {
			return fmt.Errorf("insert a collection: %w", err)
		}
		return nil
	})
	if err != nil {
		return Collection{}, err
	}
	return collection, nil
}

// UpdateCollection replaces a collection row. The workspace and creation time are not the caller's
// to change: a row that moved workspace would strand its whole subtree.
func (s *Store) UpdateCollection(ctx context.Context, c Collection) error {
	return s.write(ctx, `
		UPDATE api_collections
		   SET name = ?, description = ?, auth = ?, pre_script = ?, post_script = ?,
		       variables = ?, sort_order = ?, updated_at = ?
		 WHERE id = ?`,
		c.Name, c.Description, c.Auth, c.PreScript, c.PostScript, c.Variables, c.SortOrder,
		s.clock.Now(), c.ID)
}

// DeleteCollection removes a collection. Its folders and requests go with it, by cascade at the
// schema level rather than by a delete written here.
func (s *Store) DeleteCollection(ctx context.Context, id string) error {
	return s.write(ctx, `DELETE FROM api_collections WHERE id = ?`, id)
}

// DuplicateCollection deep-copies a collection with its whole subtree (STORE-019).
func (s *Store) DuplicateCollection(ctx context.Context, id string) (Collection, error) {
	var copied Collection

	err := s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		source, err := collectionByID(ctx, tx, id)
		if err != nil {
			return err
		}

		now := s.clock.Now()
		order, err := nextOrder(ctx, tx,
			`SELECT COALESCE(MAX(sort_order) + 1, 0) FROM api_collections WHERE workspace_id = ?`,
			source.WorkspaceID)
		if err != nil {
			return err
		}

		copied = source
		copied.ID = uuid.NewString()
		copied.Name = source.Name + " copy"
		copied.SortOrder = order
		copied.CreatedAt, copied.UpdatedAt = now, now

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO api_collections (id, workspace_id, name, description, auth, pre_script,
			                             post_script, variables, sort_order, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			copied.ID, copied.WorkspaceID, copied.Name, copied.Description, copied.Auth,
			copied.PreScript, copied.PostScript, copied.Variables, copied.SortOrder,
			now, now); err != nil {
			return fmt.Errorf("insert the copied collection: %w", err)
		}

		remapped, err := copyFolders(ctx, tx, source.ID, copied.ID, now)
		if err != nil {
			return err
		}
		return copyRequests(ctx, tx, source.ID, copied.ID, remapped, now)
	})
	if err != nil {
		return Collection{}, err
	}
	return copied, nil
}

// copyFolders deep-copies a collection's folders **in two passes**, and answers the old id → new id
// map the requests are then reparented through.
//
// The two passes are not a style choice. A child folder can appear before its own parent in the
// source listing, and inserting it with an already-remapped parent id that has not been written yet
// violates the self-referencing foreign key. So every folder lands first with no parent, and a
// second pass sets each one's remapped parent.
func copyFolders(ctx context.Context, tx *sql.Tx, sourceID, targetID, now string) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, parent_id, name, description, auth, pre_script, post_script, sort_order
		  FROM api_folders WHERE collection_id = ? ORDER BY sort_order, created_at`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("read the folders to copy: %w", err)
	}

	type sourceFolder struct {
		id, name, description, auth, preScript, postScript string
		parentID                                           *string
		sortOrder                                          int64
	}
	folders := make([]sourceFolder, 0, 16)

	for rows.Next() {
		var f sourceFolder
		if err := rows.Scan(&f.id, &f.parentID, &f.name, &f.description, &f.auth,
			&f.preScript, &f.postScript, &f.sortOrder); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan a folder to copy: %w", err)
		}
		folders = append(folders, f)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	remapped := make(map[string]string, len(folders))
	for _, folder := range folders {
		remapped[folder.id] = uuid.NewString()
	}

	// Pass one: every folder, parentless.
	for _, folder := range folders {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO api_folders (id, collection_id, parent_id, name, description, auth,
			                         pre_script, post_script, sort_order, created_at)
			VALUES (?, ?, NULL, ?, ?, ?, ?, ?, ?, ?)`,
			remapped[folder.id], targetID, folder.name, folder.description, folder.auth,
			folder.preScript, folder.postScript, folder.sortOrder, now); err != nil {
			return nil, fmt.Errorf("insert a copied folder: %w", err)
		}
	}

	// Pass two: the parents, now that every one of them exists.
	for _, folder := range folders {
		if folder.parentID == nil {
			continue
		}
		parent, found := remapped[*folder.parentID]
		if !found {
			// A parent outside this collection is a row the source tree could not have rendered
			// either; the copy keeps it at the root rather than pointing at another collection's
			// folder.
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE api_folders SET parent_id = ? WHERE id = ?`,
			parent, remapped[folder.id]); err != nil {
			return nil, fmt.Errorf("reparent a copied folder: %w", err)
		}
	}
	return remapped, nil
}

func copyRequests(ctx context.Context, tx *sql.Tx, sourceID, targetID string, remapped map[string]string, now string) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT folder_id, name, protocol, method, url, spec, sort_order
		  FROM api_requests WHERE collection_id = ? ORDER BY sort_order, created_at`, sourceID)
	if err != nil {
		return fmt.Errorf("read the requests to copy: %w", err)
	}

	type sourceRequest struct {
		folderID                          *string
		name, protocol, method, url, spec string
		sortOrder                         int64
	}
	requests := make([]sourceRequest, 0, 32)

	for rows.Next() {
		var r sourceRequest
		if err := rows.Scan(&r.folderID, &r.name, &r.protocol, &r.method, &r.url,
			&r.spec, &r.sortOrder); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan a request to copy: %w", err)
		}
		requests = append(requests, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, request := range requests {
		var folderID *string
		if request.folderID != nil {
			if mapped, found := remapped[*request.folderID]; found {
				folderID = &mapped
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO api_requests (id, collection_id, folder_id, name, protocol, method, url,
			                          spec, sort_order, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), targetID, folderID, request.name, request.protocol, request.method,
			request.url, request.spec, request.sortOrder, now, now); err != nil {
			return fmt.Errorf("insert a copied request: %w", err)
		}
	}
	return nil
}

// ---- folders -------------------------------------------------------------------------------------

// CreateFolder inserts a folder, last among its new siblings.
func (s *Store) CreateFolder(ctx context.Context, collectionID string, parentID *string, name string) (Folder, error) {
	now := s.clock.Now()
	folder := Folder{
		ID:           uuid.NewString(),
		CollectionID: collectionID,
		ParentID:     parentID,
		Name:         name,
		CreatedAt:    now,
	}

	err := s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		order, err := nextSiblingOrder(ctx, tx, "api_folders", "parent_id", collectionID, parentID)
		if err != nil {
			return err
		}
		folder.SortOrder = order

		_, err = tx.ExecContext(ctx, `
			INSERT INTO api_folders (id, collection_id, parent_id, name, description, auth,
			                         pre_script, post_script, sort_order, created_at)
			VALUES (?, ?, ?, ?, '', '', '', '', ?, ?)`,
			folder.ID, collectionID, parentID, name, folder.SortOrder, now)
		if err != nil {
			return fmt.Errorf("insert a folder: %w", err)
		}
		return nil
	})
	if err != nil {
		return Folder{}, err
	}
	return folder, nil
}

// UpdateFolder replaces a folder row. Reparenting goes through MoveNode, which has the guards.
func (s *Store) UpdateFolder(ctx context.Context, f Folder) error {
	return s.write(ctx, `
		UPDATE api_folders
		   SET name = ?, description = ?, auth = ?, pre_script = ?, post_script = ?, sort_order = ?
		 WHERE id = ?`,
		f.Name, f.Description, f.Auth, f.PreScript, f.PostScript, f.SortOrder, f.ID)
}

// DeleteFolder removes a folder; its child folders and requests cascade.
func (s *Store) DeleteFolder(ctx context.Context, id string) error {
	return s.write(ctx, `DELETE FROM api_folders WHERE id = ?`, id)
}

// ---- requests ------------------------------------------------------------------------------------

// CreateRequest inserts a request, denormalising its method and URL out of the spec.
func (s *Store) CreateRequest(ctx context.Context, collectionID string, folderID *string, name, protocol, spec string) (Request, error) {
	now := s.clock.Now()
	method, url := Denormalize(spec)

	request := Request{
		ID:           uuid.NewString(),
		CollectionID: collectionID,
		FolderID:     folderID,
		Name:         name,
		Protocol:     protocol,
		Method:       method,
		URL:          url,
		Spec:         spec,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	err := s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		order, err := nextSiblingOrder(ctx, tx, "api_requests", "folder_id", collectionID, folderID)
		if err != nil {
			return err
		}
		request.SortOrder = order

		_, err = tx.ExecContext(ctx, `
			INSERT INTO api_requests (id, collection_id, folder_id, name, protocol, method, url,
			                          spec, sort_order, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			request.ID, collectionID, folderID, name, protocol, method, url, spec,
			request.SortOrder, now, now)
		if err != nil {
			return fmt.Errorf("insert a request: %w", err)
		}
		return nil
	})
	if err != nil {
		return Request{}, err
	}
	return request, nil
}

// UpdateRequest replaces a request row, re-denormalising method and URL from the new spec.
func (s *Store) UpdateRequest(ctx context.Context, r Request) error {
	method, url := Denormalize(r.Spec)

	return s.write(ctx, `
		UPDATE api_requests
		   SET name = ?, protocol = ?, method = ?, url = ?, spec = ?, sort_order = ?, updated_at = ?
		 WHERE id = ?`,
		r.Name, r.Protocol, method, url, r.Spec, r.SortOrder, s.clock.Now(), r.ID)
}

// DeleteRequest removes a request.
func (s *Store) DeleteRequest(ctx context.Context, id string) error {
	return s.write(ctx, `DELETE FROM api_requests WHERE id = ?`, id)
}

// DuplicateRequest copies a request, last among its siblings (STORE-019).
func (s *Store) DuplicateRequest(ctx context.Context, id string) (Request, error) {
	var copied Request

	err := s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		source, err := requestByID(ctx, tx, id)
		if err != nil {
			return err
		}

		now := s.clock.Now()
		order, err := nextSiblingOrder(ctx, tx, "api_requests", "folder_id",
			source.CollectionID, source.FolderID)
		if err != nil {
			return err
		}

		copied = source
		copied.ID = uuid.NewString()
		copied.Name = source.Name + " copy"
		copied.SortOrder = order
		copied.CreatedAt, copied.UpdatedAt = now, now

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO api_requests (id, collection_id, folder_id, name, protocol, method, url,
			                          spec, sort_order, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			copied.ID, copied.CollectionID, copied.FolderID, copied.Name, copied.Protocol,
			copied.Method, copied.URL, copied.Spec, copied.SortOrder, now, now); err != nil {
			return fmt.Errorf("insert the copied request: %w", err)
		}
		return nil
	})
	if err != nil {
		return Request{}, err
	}
	return copied, nil
}

// Denormalize extracts a request's method and URL out of its spec JSON (STORE-018).
//
// Every failure degrades rather than propagating: a spec that is not JSON at all reads as
// `("GET", "")`. This keeps the two denormalised columns honest without letting a malformed blob —
// which the renderer owns and this package never validates — fail a save.
//
// `method` falls back to `GET` when empty; `url` has no equivalent fallback and can end up `""`.
func Denormalize(spec string) (method, url string) {
	var parsed struct {
		Method string `json:"method"`
		URL    string `json:"url"`
	}
	// A parse failure reads as an empty object, not as an error: the defaults below then apply.
	_ = json.Unmarshal([]byte(spec), &parsed)

	method = parsed.Method
	if method == "" {
		method = "GET"
	}
	return method, parsed.URL
}

// ---- moving and reordering -----------------------------------------------------------------------

// The four refusals MoveNode can answer with. Human-readable rather than typed, because they are
// caller mistakes the renderer shows verbatim in a toast, not database failures.
var (
	ErrFolderIntoItself = errors.New("A folder cannot be moved inside itself")                      //nolint:staticcheck // ST1005: VERBATIM
	ErrCrossWorkspace   = errors.New("A node cannot be moved to a collection in another workspace") //nolint:staticcheck // ST1005: VERBATIM
)

// MoveNode reparents one folder or request and renumbers its new siblings (STORE-017).
//
// Folders and requests are renumbered against siblings of their **own kind only**: the two tables
// have independent `sort_order` sequences, because the tree always renders folders above requests
// within a parent.
//
// One transaction, so a rejected move leaves nothing half-applied.
func (s *Store) MoveNode(ctx context.Context, kind, id, collectionID string, parentID *string, index int64) error {
	switch kind {
	case "folder", "request":
	default:
		return fmt.Errorf("Unknown node kind %s", kind) //nolint:staticcheck,err113 // ST1005: VERBATIM
	}

	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		destination, err := collectionWorkspace(ctx, tx, collectionID)
		if err != nil {
			return err
		}

		origin, err := nodeWorkspace(ctx, tx, kind, id)
		if err != nil {
			return err
		}
		if origin != destination {
			return ErrCrossWorkspace
		}

		if kind == "folder" {
			// The cycle guard runs before anything is written: a folder moved inside its own
			// subtree detaches that whole subtree from the tree, and nothing would name it again.
			inside, err := isWithinSubtree(ctx, tx, parentID, id)
			if err != nil {
				return err
			}
			if inside {
				return ErrFolderIntoItself
			}
		}

		if err := reparent(ctx, tx, kind, id, collectionID, parentID); err != nil {
			return err
		}
		if kind == "folder" {
			// Everything beneath the folder has to follow it into the new collection, or the
			// descendants stay addressed to a collection the tree no longer renders them under.
			if err := carrySubtreeToCollection(ctx, tx, id, collectionID); err != nil {
				return err
			}
		}
		return renumberSiblings(ctx, tx, kind, id, collectionID, parentID, index)
	})
}

// isWithinSubtree walks up from a destination parent looking for the folder being moved.
//
// The walk is capped at `maxFolderDepth`, and exceeding the cap answers **true** — a corrupt
// `parent_id` chain blocks the move rather than looping forever. Fail-closed on purpose: a refused
// move costs a drag, and a loop costs the process.
func isWithinSubtree(ctx context.Context, tx *sql.Tx, destination *string, moving string) (bool, error) {
	current := destination

	for range maxFolderDepth {
		if current == nil {
			return false, nil
		}
		if *current == moving {
			return true, nil
		}

		var parent *string
		err := tx.QueryRowContext(ctx,
			`SELECT parent_id FROM api_folders WHERE id = ?`, *current).Scan(&parent)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("walk the folder chain: %w", err)
		}
		current = parent
	}
	return true, nil
}

func reparent(ctx context.Context, tx *sql.Tx, kind, id, collectionID string, parentID *string) error {
	query := `UPDATE api_folders SET collection_id = ?, parent_id = ? WHERE id = ?`
	if kind == "request" {
		query = `UPDATE api_requests SET collection_id = ?, folder_id = ? WHERE id = ?`
	}
	if _, err := tx.ExecContext(ctx, query, collectionID, parentID, id); err != nil {
		return fmt.Errorf("reparent the node: %w", err)
	}
	return nil
}

// carrySubtreeToCollection rewrites `collection_id` on every folder and request beneath a moved
// folder, through one recursive CTE per table.
func carrySubtreeToCollection(ctx context.Context, tx *sql.Tx, folderID, collectionID string) error {
	const subtree = `
		WITH RECURSIVE descendants(id) AS (
		    SELECT ?
		    UNION
		    SELECT f.id FROM api_folders f JOIN descendants d ON f.parent_id = d.id
		)`

	if _, err := tx.ExecContext(ctx, subtree+`
		UPDATE api_folders SET collection_id = ?
		 WHERE id IN (SELECT id FROM descendants)`, folderID, collectionID); err != nil {
		return fmt.Errorf("carry the folder subtree: %w", err)
	}
	if _, err := tx.ExecContext(ctx, subtree+`
		UPDATE api_requests SET collection_id = ?
		 WHERE folder_id IN (SELECT id FROM descendants)`, folderID, collectionID); err != nil {
		return fmt.Errorf("carry the requests beneath the folder: %w", err)
	}
	return nil
}

// renumberSiblings rewrites the destination's children with a dense `0..n` ordering, with the moved
// node inserted at `index` — clamped, because the renderer supplies a target index from a drag and a
// list that changed under it would otherwise reorder by luck.
func renumberSiblings(ctx context.Context, tx *sql.Tx, kind, id, collectionID string, parentID *string, index int64) error {
	table, parentColumn := "api_folders", "parent_id"
	if kind == "request" {
		table, parentColumn = "api_requests", "folder_id"
	}

	siblings, err := siblingIDs(ctx, tx, table, parentColumn, collectionID, parentID, id)
	if err != nil {
		return err
	}

	if index < 0 {
		index = 0
	}
	if index > int64(len(siblings)) {
		index = int64(len(siblings))
	}
	ordered := append(append(append(make([]string, 0, len(siblings)+1),
		siblings[:index]...), id), siblings[index:]...)

	for position, sibling := range ordered {
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf(`UPDATE %s SET sort_order = ? WHERE id = ?`, table),
			position, sibling); err != nil {
			return fmt.Errorf("renumber the siblings: %w", err)
		}
	}
	return nil
}

func siblingIDs(ctx context.Context, tx *sql.Tx, table, parentColumn, collectionID string, parentID *string, exclude string) ([]string, error) {
	condition := parentColumn + " IS NULL"
	args := []any{collectionID, exclude}
	if parentID != nil {
		condition = parentColumn + " = ?"
		args = []any{collectionID, *parentID, exclude}
	}

	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
		SELECT id FROM %s
		 WHERE collection_id = ? AND %s AND id <> ?
		 ORDER BY sort_order, created_at`, table, condition), args...)
	if err != nil {
		return nil, fmt.Errorf("read the destination's children: %w", err)
	}
	defer func() { _ = rows.Close() }()

	ids := make([]string, 0, 16)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan a sibling: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ReorderCollections persists a new sidebar ordering.
//
// Only ids that belong to the workspace are written, and in the order given: an id from another
// workspace in the list is ignored rather than silently adopted.
func (s *Store) ReorderCollections(ctx context.Context, workspaceID string, ids []string) error {
	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for position, id := range ids {
			if _, err := tx.ExecContext(ctx,
				`UPDATE api_collections SET sort_order = ? WHERE id = ? AND workspace_id = ?`,
				position, id, workspaceID); err != nil {
				return fmt.Errorf("reorder the collections: %w", err)
			}
		}
		return nil
	})
}

// ---- shared helpers --------------------------------------------------------------------------------

// ErrNotFound is what a command naming a row that is not there answers.
var ErrNotFound = errors.New("not found")

func (s *Store) write(ctx context.Context, query string, args ...any) error {
	return s.db.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("write: %w", err)
		}
		return nil
	})
}

func nextOrder(ctx context.Context, tx *sql.Tx, query string, args ...any) (int64, error) {
	var order int64
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&order); err != nil {
		return 0, fmt.Errorf("read the next sort order: %w", err)
	}
	return order, nil
}

// nextSiblingOrder is the next order within one parent, where "no parent" is a distinct scope from
// "any parent" — `parent_id IS NULL` and `parent_id = ?` cannot be one query.
func nextSiblingOrder(ctx context.Context, tx *sql.Tx, table, parentColumn, collectionID string, parentID *string) (int64, error) {
	query := fmt.Sprintf(
		`SELECT COALESCE(MAX(sort_order) + 1, 0) FROM %s WHERE collection_id = ? AND %s IS NULL`,
		table, parentColumn)
	args := []any{collectionID}

	if parentID != nil {
		query = fmt.Sprintf(
			`SELECT COALESCE(MAX(sort_order) + 1, 0) FROM %s WHERE collection_id = ? AND %s = ?`,
			table, parentColumn)
		args = append(args, *parentID)
	}
	return nextOrder(ctx, tx, query, args...)
}

func collectionByID(ctx context.Context, tx *sql.Tx, id string) (Collection, error) {
	var c Collection
	err := tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, name, description, auth, pre_script, post_script, variables,
		       sort_order, created_at, updated_at
		  FROM api_collections WHERE id = ?`, id).
		Scan(&c.ID, &c.WorkspaceID, &c.Name, &c.Description, &c.Auth, &c.PreScript,
			&c.PostScript, &c.Variables, &c.SortOrder, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Collection{}, fmt.Errorf("Unknown collection %s: %w", id, ErrNotFound) //nolint:staticcheck // ST1005: VERBATIM
	}
	if err != nil {
		return Collection{}, fmt.Errorf("read the collection: %w", err)
	}
	return c, nil
}

func requestByID(ctx context.Context, tx *sql.Tx, id string) (Request, error) {
	var r Request
	err := tx.QueryRowContext(ctx, `
		SELECT id, collection_id, folder_id, name, protocol, method, url, spec, sort_order,
		       created_at, updated_at
		  FROM api_requests WHERE id = ?`, id).
		Scan(&r.ID, &r.CollectionID, &r.FolderID, &r.Name, &r.Protocol, &r.Method, &r.URL,
			&r.Spec, &r.SortOrder, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Request{}, fmt.Errorf("Unknown request %s: %w", id, ErrNotFound) //nolint:staticcheck // ST1005: VERBATIM
	}
	if err != nil {
		return Request{}, fmt.Errorf("read the request: %w", err)
	}
	return r, nil
}

// collectionWorkspace answers which workspace a collection belongs to.
func collectionWorkspace(ctx context.Context, tx *sql.Tx, collectionID string) (string, error) {
	var workspaceID string
	err := tx.QueryRowContext(ctx,
		`SELECT workspace_id FROM api_collections WHERE id = ?`, collectionID).Scan(&workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("Unknown collection %s: %w", collectionID, ErrNotFound) //nolint:staticcheck // ST1005: VERBATIM
	}
	if err != nil {
		return "", fmt.Errorf("read the destination collection: %w", err)
	}
	return workspaceID, nil
}

// nodeWorkspace answers which workspace a folder or request currently lives in, resolved by joining
// up to the collection — the one place a row's workspace is recorded.
func nodeWorkspace(ctx context.Context, tx *sql.Tx, kind, id string) (string, error) {
	table := "api_folders"
	if kind == "request" {
		table = "api_requests"
	}

	var workspaceID string
	err := tx.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT c.workspace_id FROM %s n
		  JOIN api_collections c ON c.id = n.collection_id
		 WHERE n.id = ?`, table), id).Scan(&workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("Unknown %s %s: %w", kind, id, ErrNotFound) //nolint:staticcheck // ST1005: VERBATIM
	}
	if err != nil {
		return "", fmt.Errorf("read the node's workspace: %w", err)
	}
	return workspaceID, nil
}
