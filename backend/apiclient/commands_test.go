package apiclient_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gastonlarap-a11y/code-flow/backend/apiclient"
	"github.com/gastonlarap-a11y/code-flow/backend/bridge"
)

func registryFor(t *testing.T, store *apiclient.Store) *bridge.Registry {
	t.Helper()
	registry := bridge.NewRegistry()
	apiclient.RegisterStore(registry, apiclient.Deps{Store: store})
	registry.Seal()
	return registry
}

// invoke calls a command the way the bridge does — through the JSON the renderer would have sent —
// so the parameter names and their decoding are exercised rather than bypassed.
func invoke(t *testing.T, registry *bridge.Registry, name string, params map[string]any) (any, error) {
	t.Helper()

	handler, found := registry.Lookup(name)
	require.True(t, found, "%s is not registered", name)

	raw, err := json.Marshal(params)
	require.NoError(t, err)
	return handler(t.Context(), bridge.NewParams(raw))
}

func TestTheStoreCommandsAreRegistered(t *testing.T) {
	registry := registryFor(t, nil)

	for _, name := range []string{
		"api_load_tree", "api_create_collection", "api_update_collection",
		"api_delete_collection", "api_duplicate_collection",
		"api_create_folder", "api_update_folder", "api_delete_folder",
		"api_create_request", "api_update_request", "api_delete_request",
		"api_duplicate_request", "api_move_node", "api_reorder_collections",
		"api_list_environments", "api_create_environment", "api_update_environment",
		"api_delete_environment", "api_duplicate_environment",
		"api_list_history", "api_add_history", "api_delete_history", "api_clear_history",
		"api_list_cookies", "api_upsert_cookie", "api_delete_cookie", "api_clear_cookies",
	} {
		_, registered := registry.Lookup(name)
		assert.True(t, registered, "%s", name)
	}

	// The two file readers take no store, so they answer on an install whose database did not
	// open — which is also when somebody is most likely to be importing a collection.
	for _, name := range []string{"api_read_file_base64", "api_read_text_file"} {
		_, registered := registry.Lookup(name)
		assert.True(t, registered, "%s", name)
	}
	assert.Equal(t, 29, registry.Len())
}

// gRPC is deferred on purpose: the panel knows statically that these two do not answer and says so
// before anything is clicked. Registering them would be the change, not leaving them out.
func TestTheDeferredGRPCCommandsAreNotRegistered(t *testing.T) {
	registry := registryFor(t, nil)

	for _, name := range []string{"api_grpc_describe", "api_grpc_call"} {
		_, registered := registry.Lookup(name)
		assert.False(t, registered, "%s is deferred", name)
	}
}

func TestTheTreeCrossesTheBridgeInTheRenderersShape(t *testing.T) {
	store, _ := newStore(t)
	registry := registryFor(t, store)

	created, err := invoke(t, registry, "api_create_collection",
		map[string]any{"workspaceId": "w1", "name": "Payments"})
	require.NoError(t, err)

	collection, ok := created.(apiclient.Collection)
	require.True(t, ok)

	_, err = invoke(t, registry, "api_create_request", map[string]any{
		"collectionId": collection.ID, "folderId": nil, "name": "Crear",
		"protocol": "http", "spec": `{"method":"POST","url":"/pagos"}`,
	})
	require.NoError(t, err)

	answer, err := invoke(t, registry, "api_load_tree", map[string]any{"workspaceId": "w1"})
	require.NoError(t, err)

	encoded, err := json.Marshal(answer)
	require.NoError(t, err)

	var wire struct {
		Collections []map[string]any `json:"collections"`
		Folders     []map[string]any `json:"folders"`
		Requests    []map[string]any `json:"requests"`
	}
	require.NoError(t, json.Unmarshal(encoded, &wire))

	require.Len(t, wire.Collections, 1)
	// snake_case, as the renderer's `types/api.ts` declares it. A field renamed on this side
	// compiles and arrives as `undefined`, which renders as a blank row rather than an error.
	assert.Contains(t, wire.Collections[0], "workspace_id")
	assert.Contains(t, wire.Collections[0], "sort_order")
	assert.Contains(t, wire.Collections[0], "created_at")

	require.Len(t, wire.Requests, 1)
	assert.Contains(t, wire.Requests[0], "collection_id")
	assert.Equal(t, "POST", wire.Requests[0]["method"])
	// `folder_id` is null and present, never omitted: the renderer types it as `string | null` and
	// branches on the null.
	assert.Contains(t, wire.Requests[0], "folder_id")
	assert.Nil(t, wire.Requests[0]["folder_id"])

	// And the two empty lists are `[]`, not `null`.
	assert.NotNil(t, wire.Folders)
	assert.Empty(t, wire.Folders)
}

func TestAnOptionalParentIsTheRootRatherThanAnError(t *testing.T) {
	store, _ := newStore(t)
	registry := registryFor(t, store)

	created, err := invoke(t, registry, "api_create_collection",
		map[string]any{"workspaceId": "w1", "name": "API"})
	require.NoError(t, err)
	collection := created.(apiclient.Collection) //nolint:errcheck // asserted in the test above

	// The renderer sends an explicit null for "directly under the collection"; both that and an
	// omitted key have to read the same way, because two call sites spell it differently.
	for _, params := range []map[string]any{
		{"collectionId": collection.ID, "parentId": nil, "name": "con null"},
		{"collectionId": collection.ID, "name": "sin la clave"},
	} {
		answer, err := invoke(t, registry, "api_create_folder", params)
		require.NoError(t, err)

		folder, ok := answer.(apiclient.Folder)
		require.True(t, ok)
		assert.Nil(t, folder.ParentID)
	}
}

func TestAMissingParameterIsNamed(t *testing.T) {
	store, _ := newStore(t)
	registry := registryFor(t, store)

	_, err := invoke(t, registry, "api_move_node", map[string]any{
		"kind": "folder", "id": "x", "collectionId": "c", "parentId": nil,
		// `index` missing.
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "index")
}

func TestAnUpdateCrossesTheBridgeAsTheWholeRow(t *testing.T) {
	store, _ := newStore(t)
	registry := registryFor(t, store)

	created, err := invoke(t, registry, "api_create_collection",
		map[string]any{"workspaceId": "w1", "name": "API"})
	require.NoError(t, err)
	collection := created.(apiclient.Collection) //nolint:errcheck // asserted above

	_, err = invoke(t, registry, "api_update_collection", map[string]any{
		"collection": map[string]any{
			"id": collection.ID, "workspace_id": "w1", "name": "API renombrada",
			"description": "algo", "auth": `{"type":"bearer"}`, "pre_script": "",
			"post_script": "", "variables": "[]", "sort_order": 0,
			"created_at": collection.CreatedAt, "updated_at": collection.UpdatedAt,
		},
	})
	require.NoError(t, err)

	tree, err := store.LoadTree(t.Context(), "w1")
	require.NoError(t, err)
	require.Len(t, tree.Collections, 1)
	assert.Equal(t, "API renombrada", tree.Collections[0].Name)
	assert.Equal(t, `{"type":"bearer"}`, tree.Collections[0].Auth,
		"the auth blob is opaque here and travels through unread")
}

func TestReadingAFileThroughTheBridge(t *testing.T) {
	registry := registryFor(t, nil)

	_, err := invoke(t, registry, "api_read_text_file", map[string]any{"path": "/no/such/file.json"})
	assert.Error(t, err)
}
