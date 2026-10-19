package sonarr

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// decodeWire decodes body into a fresh W, failing the test on a decode error.
func decodeWire[W any](t *testing.T, body string) W {
	t.Helper()
	var w W
	if err := json.Unmarshal([]byte(body), &w); err != nil {
		t.Fatalf("decode %T: %v", w, err)
	}
	return w
}

// assertFields reports each top-level field of got that differs from want, so a
// failure names the field rather than dumping both structs.
func assertFields[T any](t *testing.T, got, want T) {
	t.Helper()
	gv, wv := reflect.ValueOf(got), reflect.ValueOf(want)
	for i := range gv.NumField() {
		g, w := gv.Field(i).Interface(), wv.Field(i).Interface()
		if !reflect.DeepEqual(g, w) {
			t.Errorf("%T.%s = %s, want %s", got, gv.Type().Field(i).Name, show(g), show(w))
		}
	}
}

// show renders v with pointers followed, and a nil slice apart from an empty one.
func show(v any) string {
	rv := reflect.ValueOf(v)
	switch {
	case !rv.IsValid():
		return "nil"
	case rv.Kind() == reflect.Pointer && rv.IsNil():
		return "nil"
	case rv.Kind() == reflect.Pointer:
		return "&" + fmt.Sprintf("%+v", rv.Elem().Interface())
	case rv.Kind() == reflect.Slice && rv.IsNil():
		return "nil slice"
	}
	return fmt.Sprintf("%#v", v)
}
