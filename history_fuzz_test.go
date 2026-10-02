package arrapi_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v2"
)

func FuzzEventType_UnmarshalJSON(f *testing.F) {
	for _, seed := range []string{
		"1", "7", "-1", "0", "99", "null", "true",
		`"grabbed"`, `"movieFileRenamed"`, `"someFutureEvent"`, `""`,
		`1e999`, `99999999999999999999`, `"\u0000"`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var e arrapi.EventType
		if err := json.Unmarshal(data, &e); err != nil {
			return // rejected input carries no post-decode invariant
		}
		// A successful decode always yields either the unknown type (0) or one of
		// the modeled constants; the isKnownEventType gate must clamp an unmodeled
		// integer (e.g. 99) or unknown string to 0. Asserting only e >= 0 cannot
		// detect that gate being removed, since an unmodeled positive int is
		// non-negative and still returns a non-empty String().
		if e < 0 || e > arrapi.EventDownloadIgnored {
			t.Errorf("UnmarshalJSON(%q) = %d, want a modeled EventType in [0,%d]", data, int(e), int(arrapi.EventDownloadIgnored))
		}
		if s := e.String(); s == "" {
			t.Errorf("EventType(%d).String() returned empty string", int(e))
		}
	})
}

func FuzzHistoryRecord_UnmarshalJSON(f *testing.F) {
	for _, seed := range []string{
		`{"id":1,"eventType":1,"seriesId":7}`,
		`{"id":2,"eventType":"movieFileRenamed","movieId":100}`,
		`{"id":3,"eventType":"someFutureEvent","seriesId":7}`,
		`{"id":4,"eventType":99}`,
		`{"eventType":-1}`,
		`{"eventType":null}`,
		`{"eventType":"","data":{"importedPath":"/media/x.mkv"}}`,
		`{"eventType":"\"quoted\\u0041"}`,
		`{"eventType":1,"EVENTTYPE":"someFutureEvent"}`,
		`{}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var rec arrapi.HistoryRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			return // a malformed record carries no post-decode invariant
		}
		// Every decoded event is either unknown (0) or one of the modeled
		// constants; the isKnownEventType gate must clamp anything else to 0.
		if rec.EventType < 0 || rec.EventType > arrapi.EventDownloadIgnored {
			t.Errorf("decoded eventType %d outside the modeled range [0,%d]", int(rec.EventType), int(arrapi.EventDownloadIgnored))
		}
		// RawEventType is preserved only for the unknown (0) case.
		if rec.EventType != 0 && rec.RawEventType != "" {
			t.Errorf("RawEventType %q set for modeled eventType %d, want empty", rec.RawEventType, int(rec.EventType))
		}
		if rec.EventType == 0 {
			if want := wantRawEventType(t, data); rec.RawEventType != want {
				t.Errorf("RawEventType = %q, want %q", rec.RawEventType, want)
			}
		}
		if rec.EventType.String() == "" {
			t.Errorf("EventType(%d).String() returned empty", int(rec.EventType))
		}
		_ = rec.ImportedPath() // must not panic on an absent/nil data map
	})
}

// wantRawEventType derives RawEventType for an unknown event from the input
// alone. The token is the value of the last top-level key matching eventType
// case-insensitively, which is the one the struct decoder assigns. A string
// token yields its decoded text; null yields "", the same as an absent key.
func wantRawEventType(t *testing.T, data []byte) string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	if open, err := dec.Token(); err != nil || open != json.Delim('{') {
		return "" // a top-level null decodes to the zero record
	}
	var tok json.RawMessage
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			t.Fatalf("Token() on accepted input %q: %v", data, err)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("Decode() on accepted input %q: %v", data, err)
		}
		if k, _ := key.(string); strings.EqualFold(k, "eventType") {
			tok = v
		}
	}
	switch {
	case tok == nil || string(tok) == "null":
		return ""
	case tok[0] == '"':
		var s string
		if err := json.Unmarshal(tok, &s); err != nil {
			t.Fatalf("json.Unmarshal(%s) into string: %v", tok, err)
		}
		return s
	default:
		return string(tok)
	}
}
