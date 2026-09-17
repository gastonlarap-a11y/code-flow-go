// Package jsonwire owns the encoding rules for everything that crosses to the renderer.
//
// The rules exist because their violations are silent. A wrong field name, a nil slice or an
// untyped nil all compile, all serialise, and all surface as a blank panel or an "undefined is not
// a function" three screens away from the handler that caused them. Each rule below is one of
// those, closed once and in one place (MIGRATION-GO.md §3.4).
package jsonwire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// nullBytes is what a void command answers with.
var nullBytes = json.RawMessage("null")

// Marshal encodes a handler's return value for the bridge.
//
// Two departures from encoding/json's defaults, both load-bearing:
//
//   - A nil result becomes the literal `null`. Wails' HTTP transport writes `{}` for a nil result
//     (transport_http.go calls json(nil)), so a void command would resolve to an empty object in
//     the renderer instead of null — measured, and exactly the kind of difference that turns into
//     `if (result)` being true when nothing happened. Returning a pre-encoded RawMessage("null")
//     takes that decision away from the transport.
//   - HTML escaping is off. Go escapes <, > and & as < and friends. JSON.parse decodes them
//     back, so the renderer never notices — but these payloads also get written into SQLite and
//     exported to files a human reads, and a diff full of < is unreadable.
func Marshal(value any) (json.RawMessage, error) {
	if isNil(value) {
		return nullBytes, nil
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("encode response: %w", err)
	}

	// Encode appends a newline; the wire wants the value alone.
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

// isNil covers both an untyped nil and a typed nil pointer/map/slice/interface held in an `any`,
// which `value == nil` alone would miss — the classic Go trap where a (*T)(nil) in an interface is
// not equal to nil.
func isNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func, reflect.Chan:
		return v.IsNil()
	default:
		return false
	}
}

// ByteArray carries a file's contents in the shape the renderer sends them.
//
// write_file_bytes passes `contents` as a JSON array of numbers, because that is what the 2.x
// renderer built from a Uint8Array and nothing about that changed. Go's []byte expects base64, so
// a plain []byte field would fail to unmarshal every call. This type accepts the array form only —
// silently accepting base64 as well would hide the day the renderer starts sending it.
type ByteArray []byte

// UnmarshalJSON implements json.Unmarshaler.
func (b *ByteArray) UnmarshalJSON(data []byte) error {
	if string(bytes.TrimSpace(data)) == "null" {
		*b = nil
		return nil
	}

	var numbers []json.Number
	if err := json.Unmarshal(data, &numbers); err != nil {
		return fmt.Errorf("expected an array of byte values: %w", err)
	}

	out := make(ByteArray, len(numbers))
	for i, n := range numbers {
		value, err := strconv.ParseUint(n.String(), 10, 8)
		if err != nil {
			return fmt.Errorf("element %d (%s) is not a byte value between 0 and 255", i, n)
		}
		out[i] = byte(value)
	}
	*b = out
	return nil
}

// MarshalJSON writes the same array-of-numbers shape back, so a round trip is symmetric.
func (b ByteArray) MarshalJSON() ([]byte, error) {
	if b == nil {
		return []byte("null"), nil
	}
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = strconv.Itoa(int(v))
	}
	return []byte("[" + strings.Join(parts, ",") + "]"), nil
}

// AssertNoNilSlices reports every nil slice reachable from value, by path.
//
// Go marshals a nil slice as `null` and an empty one as `[]`. The renderer's 246 command wrappers
// are typed as returning arrays and call .map, .length and .filter on them without guarding, so a
// handler that returns a nil slice on the empty path crashes the panel that displays it — while
// the same handler with one row works perfectly. It is the single most likely defect of this port,
// which is why it gets a mechanical check rather than a review habit: every response type's golden
// fixture runs through here (§9.4 #2).
//
// The fix at the handler is always the same: build with make([]T, 0, n), never var s []T.
func AssertNoNilSlices(value any) error {
	var found []string
	walk(reflect.ValueOf(value), "$", &found, make(map[uintptr]bool))
	if len(found) == 0 {
		return nil
	}
	return fmt.Errorf("nil slices marshal as null and crash the renderer's .map: %s", strings.Join(found, ", "))
}

func walk(v reflect.Value, path string, found *[]string, seen map[uintptr]bool) {
	if !v.IsValid() {
		return
	}

	switch v.Kind() {
	case reflect.Slice:
		if v.IsNil() {
			*found = append(*found, path)
			return
		}
		// A slice header can be revisited through different paths; the pointer guard stops a
		// self-referential structure from looping forever.
		if ptr := v.Pointer(); ptr != 0 {
			if seen[ptr] {
				return
			}
			seen[ptr] = true
		}
		for i := range v.Len() {
			walk(v.Index(i), fmt.Sprintf("%s[%d]", path, i), found, seen)
		}

	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return
		}
		walk(v.Elem(), path, found, seen)

	case reflect.Struct:
		t := v.Type()
		for i := range v.NumField() {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			// A field the encoder skips cannot reach the renderer, so it cannot break it.
			name, omitted := wireName(field)
			if omitted {
				continue
			}
			walk(v.Field(i), path+"."+name, found, seen)
		}

	case reflect.Map:
		if v.IsNil() {
			return // a nil map marshals as null too, but no renderer code iterates one blindly
		}
		for _, key := range v.MapKeys() {
			walk(v.MapIndex(key), fmt.Sprintf("%s[%v]", path, key.Interface()), found, seen)
		}

	default:
	}
}

// wireName resolves what a field is called on the wire, and whether it is sent at all.
func wireName(field reflect.StructField) (name string, omitted bool) {
	tag, ok := field.Tag.Lookup("json")
	if !ok {
		return field.Name, false
	}
	parts := strings.Split(tag, ",")
	if parts[0] == "-" && len(parts) == 1 {
		return "", true
	}
	if parts[0] == "" {
		return field.Name, false
	}
	return parts[0], false
}
