package radarr

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/arrenum"
)

// FuzzCommandStatusToken feeds each input as a command's status: the value's JSON kind
// alone decides whether the decode errors, a number or null never does and reads "",
// and an error names the kind, the field and CommandStatus.
func FuzzCommandStatusToken(f *testing.F) {
	for _, seed := range []string{
		`"completed"`, `"compl\u0065ted"`, `"someFuture"`, `"Completed"`, `""`, `"\u0000"`,
		`3`, `-1`, `1e999`, `99999999999999999999`, `null`, `true`, `false`, `{}`, `{"a":1}`, `[]`, `["completed"]`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		kind, ok := jsonKind(data)
		if !ok {
			return
		}
		record := append(append([]byte(`{"id":1,"status":`), data...), '}')
		var w radarrCommand
		err := json.Unmarshal(record, &w)
		switch kind {
		case "number", "null":
			if err != nil {
				t.Fatalf("status %s (a %s) failed the command decode: %v", data, kind, err)
			}
			if got := w.command().Status; got != "" {
				t.Errorf("status %s (a %s) decoded to %q, want \"\"", data, kind, got)
			}
		case "string":
			if err != nil {
				t.Fatalf("status %s (a string) failed the command decode: %v", data, err)
			}
			if got := w.command().Status; got != arrenum.CommandStatus(jsonString(t, data)) {
				t.Errorf("status %s decoded to %q, want the status of that exact text", data, got)
			}
		default:
			te, ok := errors.AsType[*json.UnmarshalTypeError](err)
			if !ok || te.Value != kind || te.Field != "status" || te.Type != reflect.TypeFor[arrapi.CommandStatus]() {
				t.Errorf("status %s (a %s): error %v, want a *json.UnmarshalTypeError naming %s at status", data, kind, err, kind)
			}
		}
	})
}
