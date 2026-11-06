package sonarr

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// FuzzSeriesType checks that any server text maps to one of the three types or "", and
// to a type only when the text is that type's exact code.
func FuzzSeriesType(f *testing.F) {
	for _, seed := range []string{"standard", "daily", "anime", "", "Anime", "movie", "anime\x00", "\u00e1nime"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		switch got := seriesType(s); got {
		case "":
			if s == "standard" || s == "daily" || s == "anime" {
				t.Errorf("seriesType(%q) = \"\", want the type of that code", s)
			}
		case SeriesStandard, SeriesDaily, SeriesAnime:
			if string(got) != s {
				t.Errorf("seriesType(%q) = %q, want the input's own code", s, got)
			}
		default:
			t.Errorf("seriesType(%q) = %q, outside the closed set", s, got)
		}
	})
}

// FuzzSeriesTypeToken feeds each input as a series' seriesType: the value's JSON kind
// alone decides whether the decode errors, a number or null never does and reads "",
// and an error names the kind, the field and SeriesType.
func FuzzSeriesTypeToken(f *testing.F) {
	for _, seed := range []string{
		`"anime"`, `"standard"`, `"daily"`, `"Anime"`, `"an\u0069me"`, `""`, `"\u0000"`,
		`3`, `-1`, `1e999`, `99999999999999999999`, `null`, `true`, `{}`, `{"a":1}`, `[]`, `["anime"]`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		kind, ok := jsonKind(data)
		if !ok {
			return
		}
		record := append(append([]byte(`{"id":1,"seriesType":`), data...), '}')
		var w sonarrV4Series
		err := json.Unmarshal(record, &w)
		switch kind {
		case "number", "null":
			if err != nil {
				t.Fatalf("seriesType %s (a %s) failed the series decode: %v", data, kind, err)
			}
			if got := w.series().Type; got != "" {
				t.Errorf("seriesType %s (a %s) decoded to %q, want \"\"", data, kind, got)
			}
		case "string":
			if err != nil {
				t.Fatalf("seriesType %s (a string) failed the series decode: %v", data, err)
			}
			if got := w.series().Type; got != seriesType(jsonString(t, data)) {
				t.Errorf("seriesType %s decoded to %q, want the type of that exact code", data, got)
			}
		default:
			te, ok := errors.AsType[*json.UnmarshalTypeError](err)
			if !ok || te.Value != kind || te.Field != "seriesType" || te.Type != reflect.TypeFor[SeriesType]() {
				t.Errorf("seriesType %s (a %s): error %v, want a *json.UnmarshalTypeError naming %s at seriesType", data, kind, err, kind)
			}
		}
	})
}
