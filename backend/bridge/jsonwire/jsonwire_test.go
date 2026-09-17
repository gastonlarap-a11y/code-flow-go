package jsonwire_test

import (
	"encoding/json"
	"testing"

	"github.com/gastonlarap-a11y/code-flow/backend/bridge/jsonwire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type workspace struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Projects []string `json:"projects"`
	Note     *string  `json:"note"`
	internal string   //nolint:unused // present to prove unexported fields are skipped
}

// A void command must resolve to null in the renderer. Wails' HTTP transport writes {} for a nil
// result, so the null has to be produced here, before the transport ever sees it.
func TestMarshalTurnsNilIntoNull(t *testing.T) {
	for name, value := range map[string]any{
		"untyped nil":       nil,
		"a typed nil slice": []string(nil),
		"a typed nil map":   map[string]string(nil),
		"a nil pointer":     (*workspace)(nil),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := jsonwire.Marshal(value)
			require.NoError(t, err)
			assert.Equal(t, "null", string(got))
		})
	}
}

// Go escapes <, > and & by default. JSON.parse decodes them back so the renderer never notices,
// but these payloads are also written into SQLite and exported to files people read.
func TestMarshalDoesNotEscapeHTML(t *testing.T) {
	got, err := jsonwire.Marshal(map[string]string{"body": `<b>a & b</b>`})

	require.NoError(t, err)
	assert.JSONEq(t, `{"body":"<b>a & b</b>"}`, string(got))
	assert.Contains(t, string(got), "<b>")
	// The literal six characters backslash-u-0-0-3-c, which is what encoding/json writes by default.
	assert.NotContains(t, string(got), "\\u003c")
	assert.NotContains(t, string(got), "\\u0026")
}

func TestMarshalEmitsNoTrailingNewline(t *testing.T) {
	got, err := jsonwire.Marshal(map[string]int{"a": 1})

	require.NoError(t, err)
	assert.Equal(t, `{"a":1}`, string(got))
}

func TestMarshalKeepsAnEmptySliceAsAnArray(t *testing.T) {
	got, err := jsonwire.Marshal(workspace{ID: "w1", Projects: []string{}})

	require.NoError(t, err)
	assert.Contains(t, string(got), `"projects":[]`)
	assert.Contains(t, string(got), `"note":null`, "a nullable field must be sent, not omitted")
}

func TestAssertNoNilSlicesAcceptsAnEmptySlice(t *testing.T) {
	assert.NoError(t, jsonwire.AssertNoNilSlices(workspace{ID: "w1", Projects: []string{}}))
}

func TestAssertNoNilSlicesNamesTheOffendingPath(t *testing.T) {
	err := jsonwire.AssertNoNilSlices(workspace{ID: "w1"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "$.projects")
}

func TestAssertNoNilSlicesReachesIntoNestedValues(t *testing.T) {
	type response struct {
		Items []workspace `json:"items"`
	}

	err := jsonwire.AssertNoNilSlices(response{Items: []workspace{{ID: "w1", Projects: []string{}}, {ID: "w2"}}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "$.items[1].projects")
	assert.NotContains(t, err.Error(), "$.items[0]")
}

func TestAssertNoNilSlicesFollowsPointersAndInterfaces(t *testing.T) {
	var boxed any = &workspace{ID: "w1"}

	err := jsonwire.AssertNoNilSlices(boxed)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "$.projects")
}

// A field the encoder never writes cannot break the renderer, so flagging it would be noise.
func TestAssertNoNilSlicesIgnoresSkippedFields(t *testing.T) {
	type response struct {
		Hidden  []string `json:"-"`
		Visible []string `json:"visible"`
	}

	assert.NoError(t, jsonwire.AssertNoNilSlices(response{Visible: []string{}}))
}

func TestAssertNoNilSlicesSurvivesACycle(t *testing.T) {
	type node struct {
		Children []*node `json:"children"`
	}
	n := &node{Children: []*node{}}
	n.Children = append(n.Children, n)

	assert.NoError(t, jsonwire.AssertNoNilSlices(n))
}

func TestAssertNoNilSlicesAcceptsANilPointerAndANilMap(t *testing.T) {
	type response struct {
		Ref *workspace        `json:"ref"`
		Map map[string]string `json:"map"`
	}

	assert.NoError(t, jsonwire.AssertNoNilSlices(response{}))
}

// write_file_bytes sends an array of numbers, because that is what the renderer builds from a
// Uint8Array. Go's []byte would expect base64 and fail on every call.
func TestByteArrayDecodesAnArrayOfNumbers(t *testing.T) {
	var got struct {
		Contents jsonwire.ByteArray `json:"contents"`
	}

	require.NoError(t, json.Unmarshal([]byte(`{"contents":[72,101,108,108,111]}`), &got))
	assert.Equal(t, "Hello", string(got.Contents))
}

func TestByteArrayAcceptsAnEmptyArrayAndNull(t *testing.T) {
	var empty jsonwire.ByteArray
	require.NoError(t, json.Unmarshal([]byte(`[]`), &empty))
	assert.Empty(t, empty)

	var null jsonwire.ByteArray
	require.NoError(t, json.Unmarshal([]byte(`null`), &null))
	assert.Nil(t, null)
}

// Silently accepting base64 as well would hide the day the renderer starts sending it, which is
// exactly the kind of drift this port is trying not to introduce.
func TestByteArrayRejectsBase64AndOutOfRangeValues(t *testing.T) {
	for name, payload := range map[string]string{
		"base64 string":  `"SGVsbG8="`,
		"above 255":      `[72,256]`,
		"negative":       `[-1]`,
		"not an integer": `[72.5]`,
		"nested array":   `[[72]]`,
	} {
		t.Run(name, func(t *testing.T) {
			var got jsonwire.ByteArray
			assert.Error(t, json.Unmarshal([]byte(payload), &got))
		})
	}
}

func TestByteArrayRoundTrips(t *testing.T) {
	encoded, err := json.Marshal(jsonwire.ByteArray("Hi"))
	require.NoError(t, err)
	assert.Equal(t, "[72,105]", string(encoded))

	var back jsonwire.ByteArray
	require.NoError(t, json.Unmarshal(encoded, &back))
	assert.Equal(t, "Hi", string(back))
}
