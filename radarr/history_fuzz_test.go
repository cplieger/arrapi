package radarr

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

// FuzzHistoryRecord_UnmarshalJSON feeds each input both as a whole history record and
// as the value of its eventType: the token's JSON kind alone decides whether the
// decode errors, a number or null never does, and an error names that kind.
func FuzzHistoryRecord_UnmarshalJSON(f *testing.F) {
	for _, seed := range []string{
		`{"id":1,"eventType":"grabbed","movieId":7}`,
		`{"id":2,"eventType":"someFutureEvent","movieId":7}`,
		`{"id":3,"eventType":5}`,
		`{"eventType":-1}`,
		`{"eventType":null}`,
		`{"eventType":{"a":1}}`,
		`{"eventType":""}`,
		`{"eventType":"\"quoted\\u0041"}`,
		`{"eventType":"grabbed","EVENTTYPE":"someFutureEvent"}`,
		`{"date":"2031-07-09T13:57:11Z","eventType":"downloadIgnored"}`,
		`{}`,
		`1`, `1e999`, `99999999999999999999`, `-0.5`, `null`, `true`, `[]`, `""`, `"movieFileRenamed"`, `"\u0000"`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var w radarrHistory
		if err := json.Unmarshal(data, &w); err == nil {
			if e := w.record().EventType; e != 0 && !defined(e) {
				t.Errorf("record %q decoded EventType %d, want 0 or a defined event", data, int(e))
			}
		}

		kind, ok := jsonKind(data)
		if !ok {
			return
		}
		record := append(append([]byte(`{"eventType":`), data...), '}')
		var wrapped radarrHistory
		err := json.Unmarshal(record, &wrapped)
		switch kind {
		case "number", "null":
			if err != nil {
				t.Fatalf("eventType %s (a %s) failed the record decode: %v", data, kind, err)
			}
			if e := wrapped.record().EventType; e != 0 {
				t.Errorf("eventType %s (a %s) decoded to %v, want 0", data, kind, e)
			}
		case "string":
			if err != nil {
				t.Fatalf("eventType %s (a string) failed the record decode: %v", data, err)
			}
			if e := wrapped.record().EventType; e != 0 && (!defined(e) || e.String() != jsonString(t, data)) {
				t.Errorf("eventType %s decoded to %v, want 0 or the event of that exact token", data, e)
			}
		default:
			te, ok := errors.AsType[*json.UnmarshalTypeError](err)
			if !ok || te.Value != kind {
				t.Errorf("eventType %s (a %s): error %v, want a *json.UnmarshalTypeError naming %s", data, kind, err, kind)
			}
		}
	})
}

// defined reports whether e is one of Radarr's events.
func defined(e EventType) bool {
	switch e {
	case EventGrabbed, EventDownloadImported, EventDownloadFailed, EventFileDeleted,
		EventFolderImported, EventFileRenamed, EventDownloadIgnored:
		return true
	}
	return false
}

// jsonKind names the kind of the single JSON value data holds, or reports false when
// data is not exactly one JSON value.
func jsonKind(data []byte) (string, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return "", false
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", false
	}
	switch v.(type) {
	case json.Number:
		return "number", true
	case string:
		return "string", true
	case nil:
		return "null", true
	case bool:
		return "bool", true
	case []any:
		return "array", true
	default:
		return "object", true
	}
}

func jsonString(t *testing.T, data []byte) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("json.Unmarshal(%s) into string: %v", data, err)
	}
	return s
}
