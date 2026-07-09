package arrapi_test

import (
	"encoding/json"
	"testing"

	"github.com/cplieger/arrapi"
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
		if e < 0 {
			t.Errorf("UnmarshalJSON(%q) = %d, want a non-negative EventType", data, int(e))
		}
		if s := e.String(); s == "" {
			t.Errorf("EventType(%d).String() returned empty string", int(e))
		}
	})
}
