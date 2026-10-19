package fixturegen

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// UnknownField is the member Project adds to every object, so a fixture keeps the
// path that ignores a member the DTO does not declare.
const UnknownField = "arrapiUnknownField"

var (
	unmarshalerType = reflect.TypeFor[json.Unmarshaler]()
	rawMessageType  = reflect.TypeFor[json.RawMessage]()
)

// Project returns v, generic JSON as Decode returns it, with only the members t's
// JSON tags declare, recursively, and UnknownField in every object. It fails, naming
// the member and never its value, on a value t cannot decode: a JSON kind other than
// the member's, a value the member's type refuses, or an object or list under a member
// that decodes itself (a time, a token). So a live answer the engine would reject
// never becomes a fixture it accepts, and no undeclared structure survives.
func Project(v any, t reflect.Type) (any, error) {
	out, err := project(v, t, "")
	if err != nil {
		return nil, errors.New("fixturegen: " + err.Error())
	}
	return out, nil
}

// project's error names the member as a path of declared names and list indices.
func project(v any, t reflect.Type, at string) (any, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case v == nil:
		return nil, nil
	case opaque(t) || scalarKind(t.Kind()):
		return projectScalar(v, t, at)
	case t.Kind() == reflect.Struct:
		if obj, ok := v.(map[string]any); ok {
			return projectObject(obj, t, at)
		}
	case t.Kind() == reflect.Slice || t.Kind() == reflect.Array:
		if items, ok := v.([]any); ok {
			return projectList(items, t, at)
		}
	default:
		return nil, fmt.Errorf("member %s: a %s, which fixturegen does not project", where(at), t.Kind())
	}
	return nil, refusal(at, v, t)
}

func projectScalar(v any, t reflect.Type, at string) (any, error) {
	switch v.(type) {
	case map[string]any, []any:
		return nil, refusal(at, v, t)
	default:
	}
	if decodes(v, t) != nil {
		return nil, refusal(at, v, t)
	}
	return v, nil
}

func projectList(items []any, t reflect.Type, at string) ([]any, error) {
	out := make([]any, len(items))
	for i, item := range items {
		p, err := project(item, t.Elem(), at+"["+strconv.Itoa(i)+"]")
		if err != nil {
			return nil, err
		}
		out[i] = p
	}
	return out, nil
}

func projectObject(obj map[string]any, t reflect.Type, at string) (map[string]any, error) {
	out := map[string]any{UnknownField: json.Number("1")}
	for name, ft := range fields(t) {
		v, present := obj[name]
		if !present {
			continue
		}
		member := name
		if at != "" {
			member = at + "." + name
		}
		p, err := project(v, ft, member)
		if err != nil {
			return nil, err
		}
		out[name] = p
	}
	return out, nil
}

// decodes runs t's own decoder on the scalar v, the judge of every leaf: encoding/json
// for a plain kind, the type's UnmarshalJSON for one that decodes itself.
func decodes(v any, t reflect.Type) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, reflect.New(t).Interface())
}

func refusal(at string, v any, t reflect.Type) error {
	return fmt.Errorf("member %s: a JSON %s that %s does not decode", where(at), jsonKind(v), t)
}

func where(at string) string { return cmp.Or(at, "(the answer)") }

func jsonKind(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "list"
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "bool"
	default:
		return "value"
	}
}

func scalarKind(k reflect.Kind) bool {
	switch k {
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

// opaque reports a type that decodes its own JSON, whose shape reflection cannot see.
func opaque(t reflect.Type) bool {
	return t == rawMessageType || reflect.PointerTo(t).Implements(unmarshalerType) || t.Implements(unmarshalerType)
}

// fields maps each JSON member name of struct t to its field's type.
func fields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for f := range t.Fields() {
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch name {
		case "-":
			continue
		case "":
			name = f.Name
		}
		out[name] = f.Type
	}
	return out
}
