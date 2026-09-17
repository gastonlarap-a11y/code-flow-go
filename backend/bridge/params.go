package bridge

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Params is one command's argument object, decoded lazily.
//
// The renderer always sends a camelCase JSON object (or nothing). Handlers pull named values out
// of it with Arg and OptionalArg, mirroring the Arg/OptionalArg/Number helpers each 2.x
// *Commands.cs defined for itself.
type Params struct {
	fields map[string]json.RawMessage
}

// NewParams decodes the argument object. A malformed or absent object yields empty Params rather
// than an error: the failure the user needs to see is "missing required parameter 'repoPath'",
// named and actionable, not "invalid character '}'".
func NewParams(raw json.RawMessage) Params {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return Params{}
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return Params{}
	}
	return Params{fields: fields}
}

// Raw returns one field's undecoded JSON, and whether it was present and non-null.
//
// Missing and null are the same thing throughout, because the renderer sends both for an absent
// value: an optional field left out of the object, and one set to an explicit null by a
// `value ?? null`. A handler that distinguished them would behave differently depending on which
// call site reached it.
func (p Params) Raw(name string) (json.RawMessage, bool) {
	raw, ok := p.fields[name]
	if !ok {
		return nil, false
	}
	if string(bytes.TrimSpace(raw)) == "null" {
		return nil, false
	}
	return raw, true
}

// Has reports whether a field is present and not null.
func (p Params) Has(name string) bool {
	_, ok := p.Raw(name)
	return ok
}

// Names lists the fields that arrived. Diagnostics only.
func (p Params) Names() []string {
	names := make([]string, 0, len(p.fields))
	for name := range p.fields {
		names = append(names, name)
	}
	return names
}

// Bind decodes the whole argument object into a struct, for the handlers with enough parameters
// that naming each one twice stops being readable.
func Bind(p Params, target any) error {
	if len(p.fields) == 0 {
		return nil
	}
	encoded, err := json.Marshal(p.fields)
	if err != nil {
		return fmt.Errorf("re-encode parameters: %w", err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		return fmt.Errorf("decode parameters: %w", err)
	}
	return nil
}

// MissingParameterError is the exact text 2.x produced, verified against the installed 2.7.1 core.
// The renderer surfaces it verbatim, so the quotes and the wording are part of what the user sees.
func MissingParameterError(name string) error {
	return fmt.Errorf("missing required parameter '%s'", name)
}

// Arg reads a required parameter.
//
// It is a package-level generic rather than a method because Go methods cannot take type
// parameters. That is also why the call reads Arg[string](p, "repoPath") instead of
// p.Arg[string]("repoPath").
func Arg[T any](p Params, name string) (T, error) {
	var zero T

	raw, ok := p.Raw(name)
	if !ok {
		return zero, MissingParameterError(name)
	}

	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return zero, fmt.Errorf("parameter '%s' has the wrong type: %w", name, err)
	}
	return value, nil
}

// OptionalArg reads a parameter that may be absent, returning nil when it is.
//
// A pointer rather than (T, bool) because that is what the calling code then passes around: an
// optional string that reaches storage as NULL, or a bool whose absence means "leave it alone"
// rather than "false".
func OptionalArg[T any](p Params, name string) (*T, error) {
	raw, ok := p.Raw(name)
	if !ok {
		return nil, nil
	}

	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("parameter '%s' has the wrong type: %w", name, err)
	}
	return &value, nil
}

// ArgOr reads an optional parameter, falling back to a default.
func ArgOr[T any](p Params, name string, fallback T) (T, error) {
	value, err := OptionalArg[T](p, name)
	if err != nil {
		return fallback, err
	}
	if value == nil {
		return fallback, nil
	}
	return *value, nil
}
