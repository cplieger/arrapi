package transport

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
)

// nullItem finds the first null item of an array that a struct field of t declares,
// at any depth, and returns that field's declared JSON path and the item's index.
// encoding/json decodes such an item as a zero value (seasons [null] a season 0, tags
// [null] a tag id 0), and no Sonarr or Radarr resource holds one. An array the type
// does not declare, a top-level list (GetList judges its items), and an item type
// with its own UnmarshalJSON, which decides what null means, are not judged. data
// must already have decoded into t.
func nullItem(data []byte, t reflect.Type) (field string, index int, found bool) {
	if !mayHoldNullItem(data) {
		return "", 0, false
	}
	w := nullWalker{dec: json.NewDecoder(bytes.NewReader(data))}
	w.dec.UseNumber()
	tok, err := w.dec.Token()
	if err != nil {
		return "", 0, false
	}
	w.walk(tok, t, "")
	return w.field, w.index, w.found
}

// mayHoldNullItem reports whether data holds a null right after a '[' or a ',', the
// only places an array item starts; an object member starts with its key. A match
// inside a string only costs the exact walk.
func mayHoldNullItem(data []byte) bool {
	for i := 0; ; {
		j := bytes.Index(data[i:], []byte("null"))
		if j < 0 {
			return false
		}
		at := i + j
		prev := bytes.TrimRight(data[:at], " \t\r\n")
		if n := len(prev); n > 0 && (prev[n-1] == '[' || prev[n-1] == ',') {
			return true
		}
		i = at + len("null")
	}
}

type nullWalker struct {
	dec   *json.Decoder
	field string
	index int
	found bool
}

var unmarshalerType = reflect.TypeFor[json.Unmarshaler]()

// walk reads the value that starts with tok as t decodes it (nil: as nothing decodes
// it), under the declared path, and reports whether the walk is over.
func (w *nullWalker) walk(tok json.Token, t reflect.Type, path string) bool {
	d, ok := tok.(json.Delim)
	if !ok {
		return false
	}
	t = decodedAs(t)
	if d == '{' && w.object(t, path) || d == '[' && w.array(t, path) {
		return true
	}
	_, err := w.dec.Token()
	return err != nil
}

// object walks an object's members up to its closing brace: a struct's declared
// fields under their names, a map's values under path, anything else as undeclared.
func (w *nullWalker) object(t reflect.Type, path string) bool {
	var fields map[string]declaredField
	var elem reflect.Type
	switch {
	case t != nil && t.Kind() == reflect.Struct:
		fields = structFields(t)
	case t != nil && t.Kind() == reflect.Map:
		elem = t.Elem()
	}
	for w.dec.More() {
		key, err := w.dec.Token()
		if err != nil {
			return true
		}
		next, nextPath := elem, path
		if fields != nil {
			name, _ := key.(string)
			f := lookupField(fields, name)
			next, nextPath = f.typ, joinPath(path, f.name)
		}
		if w.member(next, nextPath) {
			return true
		}
	}
	return false
}

// array walks an array's items up to its closing bracket, recording the first null
// item of an array a struct field declares.
func (w *nullWalker) array(t reflect.Type, path string) bool {
	var elem reflect.Type
	if t != nil && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) {
		elem = t.Elem()
	}
	judged := elem != nil && path != "" && !decodesItself(elem)
	for i := 0; w.dec.More(); i++ {
		item, err := w.dec.Token()
		if err != nil {
			return true
		}
		if item == nil && judged {
			w.field, w.index, w.found = path, i, true
			return true
		}
		if w.walk(item, elem, path) {
			return true
		}
	}
	return false
}

func (w *nullWalker) member(t reflect.Type, path string) bool {
	tok, err := w.dec.Token()
	if err != nil {
		return true
	}
	return w.walk(tok, t, path)
}

// decodedAs is the type encoding/json fills for t: pointers followed, and nil for a
// type that decodes itself, whose members encoding/json never sees.
func decodedAs(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || decodesItself(t) {
		return nil
	}
	return t
}

func decodesItself(t reflect.Type) bool {
	return t.Implements(unmarshalerType) || reflect.PointerTo(t).Implements(unmarshalerType)
}

type declaredField struct {
	typ  reflect.Type
	name string
}

// structFields maps each JSON name t decodes, an untagged embedded struct's promoted
// names included, to its field.
func structFields(t reflect.Type) map[string]declaredField {
	out := map[string]declaredField{}
	for f := range t.Fields() {
		name, ok := jsonName(&f)
		if !ok {
			continue
		}
		if inner := promoted(&f); inner != nil {
			for n, df := range structFields(inner) {
				if _, taken := out[n]; !taken {
					out[n] = df
				}
			}
			continue
		}
		out[name] = declaredField{typ: f.Type, name: name}
	}
	return out
}

// promoted is the struct an untagged embedded field promotes its fields from, or nil.
func promoted(f *reflect.StructField) reflect.Type {
	if !f.Anonymous || f.Tag.Get("json") != "" {
		return nil
	}
	if inner := decodedAs(f.Type); inner != nil && inner.Kind() == reflect.Struct {
		return inner
	}
	return nil
}

// lookupField matches key as encoding/json does: the exact name, else one equal under
// case folding. An undeclared key matches nothing (a zero declaredField).
func lookupField(fields map[string]declaredField, key string) declaredField {
	if f, ok := fields[key]; ok {
		return f
	}
	for name, f := range fields {
		if strings.EqualFold(name, key) {
			return f
		}
	}
	return declaredField{}
}

func joinPath(path, name string) string {
	if name == "" {
		return ""
	}
	if path == "" {
		return name
	}
	return path + "." + name
}
