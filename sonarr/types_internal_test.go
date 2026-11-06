package sonarr

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3"
)

// The codes are NzbDrone.Core/Tv/SeriesTypes.cs's members under STJson's camelCase
// naming, the same on v4-develop and develop.
func TestSeriesType(t *testing.T) {
	for in, want := range map[string]SeriesType{
		"standard": SeriesStandard,
		"daily":    SeriesDaily,
		"anime":    SeriesAnime,
		"":         "",
		"Anime":    "",
		"movie":    "",
		"anime ":   "",
	} {
		if got := seriesType(in); got != want {
			t.Errorf("seriesType(%q) = %q, want %q", in, got, want)
		}
	}
}

// A seriesType outside the enum is written as a number (STJson.cs registers
// JsonStringEnumConverter with allowIntegerValues), so it and null read "" without
// failing the list; a kind no server writes fails it with the field named.
func TestSeriesTypeOverTheWire(t *testing.T) {
	for name, ec := range engineCases() {
		t.Run(name, func(t *testing.T) {
			c, f := engineClient(t, ec)
			f.on("GET "+ec.prefix+"/series", ans(200, ec.version,
				`[{"id":1,"seriesType":3},{"id":2,"seriesType":null},{"id":3,"seriesType":"anime"},{"id":4,"seriesType":"standard"},{"id":5,"seriesType":"daily"},{"id":6}]`))
			got, err := c.Series(t.Context())
			if err != nil {
				t.Fatalf("Series: %v", err)
			}
			want := []SeriesType{"", "", SeriesAnime, SeriesStandard, SeriesDaily, ""}
			if len(got) != len(want) {
				t.Fatalf("Series returned %d series, want %d", len(got), len(want))
			}
			for i, s := range got {
				if s.Type != want[i] {
					t.Errorf("series %d Type = %q, want %q", s.ID, s.Type, want[i])
				}
			}

			f.on("GET "+ec.prefix+"/series/9", ans(200, ec.version, `{"id":9,"seriesType":"daily"}`))
			if s, found, err := c.SeriesByID(t.Context(), 9); err != nil || !found || s.Type != SeriesDaily {
				t.Errorf("SeriesByID = (Type %q, %v, %v), want SeriesDaily", s.Type, found, err)
			}

			f.on("GET "+ec.prefix+"/series", ans(200, ec.version, `[{"id":1,"seriesType":{}}]`))
			_, err = c.Series(t.Context())
			if want := "arrapi: decode " + ec.prefix + "/series: field seriesType: a JSON object cannot decode into sonarr.SeriesType"; err == nil || err.Error() != want {
				t.Errorf("Series over an object seriesType = %v, want %q", err, want)
			}
		})
	}
}

func TestSeriesTypeTokenKeepsAString(t *testing.T) {
	for in, want := range map[string]string{
		`"anime"`:      "anime",
		`"an\u0069me"`: "anime",
		`"someFuture"`: "someFuture",
		`""`:           "",
		`3`:            "",
		`-1`:           "",
		`1e3`:          "",
		`1e999`:        "",
		`null`:         "",
	} {
		var tok seriesTypeToken
		if err := json.Unmarshal([]byte(in), &tok); err != nil {
			t.Errorf("decode %s: %v, want no error", in, err)
			continue
		}
		if string(tok) != want {
			t.Errorf("decode %s = %q, want %q", in, tok, want)
		}
	}
}

func TestSeriesTypeTokenOtherKindIsError(t *testing.T) {
	for in, kind := range map[string]string{`{}`: "object", `["anime"]`: "array", `true`: "bool", `false`: "bool"} {
		var w sonarrV5Series
		err := json.Unmarshal([]byte(`{"id":1,"seriesType":`+in+`}`), &w)
		te, ok := errors.AsType[*json.UnmarshalTypeError](err)
		if !ok {
			t.Errorf("decode seriesType %s: %v, want a *json.UnmarshalTypeError (no server writes that kind)", in, err)
			continue
		}
		if te.Value != kind || te.Type != reflect.TypeFor[SeriesType]() || te.Field != "seriesType" {
			t.Errorf("decode seriesType %s: Value %q, Type %v, Field %q; want %s, SeriesType, seriesType", in, te.Value, te.Type, te.Field, kind)
		}
	}
}

// The flags are each level's own, uncombined; monitored is non-nullable on every wire,
// so an absent member reads false.
func TestMonitoredOverTheWire(t *testing.T) {
	for name, ec := range engineCases() {
		t.Run(name, func(t *testing.T) {
			c, f := engineClient(t, ec)
			f.on("GET "+ec.prefix+"/series", ans(200, ec.version,
				`[{"id":1,"monitored":true,"seasons":[{"seasonNumber":1,"monitored":true},{"seasonNumber":2,"monitored":false},{"seasonNumber":3}]},{"id":2,"monitored":false},{"id":3}]`))
			got, err := c.Series(t.Context())
			if err != nil {
				t.Fatalf("Series: %v", err)
			}
			if len(got) != 3 {
				t.Fatalf("Series returned %d series, want 3", len(got))
			}
			for i, want := range []bool{true, false, false} {
				if got[i].Monitored != want {
					t.Errorf("series %d Monitored = %v, want %v", got[i].ID, got[i].Monitored, want)
				}
			}
			wantSeasons := []Season{{Number: 1, Monitored: true}, {Number: 2}, {Number: 3}}
			if !slices.Equal(got[0].Seasons, wantSeasons) {
				t.Errorf("series 1 Seasons = %+v, want %+v", got[0].Seasons, wantSeasons)
			}

			f.on("GET "+ec.prefix+"/series/9", ans(200, ec.version, `{"id":9,"monitored":true}`))
			if s, found, err := c.SeriesByID(t.Context(), 9); err != nil || !found || !s.Monitored {
				t.Errorf("SeriesByID = (Monitored %v, %v, %v), want true", s.Monitored, found, err)
			}

			f.on("GET "+ec.prefix+"/episode", ans(200, ec.version,
				`[{"id":1,"seriesId":9,"monitored":true},{"id":2,"seriesId":9,"monitored":false},{"id":3,"seriesId":9}]`))
			eps, err := c.Episodes(t.Context(), 9)
			if err != nil {
				t.Fatalf("Episodes: %v", err)
			}
			if len(eps) != 3 {
				t.Fatalf("Episodes returned %d episodes, want 3", len(eps))
			}
			for i, want := range []bool{true, false, false} {
				if eps[i].Monitored != want {
					t.Errorf("episode %d Monitored = %v, want %v", eps[i].ID, eps[i].Monitored, want)
				}
			}

			f.on("GET "+ec.prefix+"/series", ans(200, ec.version, `[{"id":1,"seasons":[{"seasonNumber":1,"monitored":"yes"}]}]`))
			_, err = c.Series(t.Context())
			if de, ok := errors.AsType[*arrapi.DecodeError](err); !ok || !strings.Contains(err.Error(), "monitored") {
				t.Errorf("Series over a string monitored = %v (DecodeError %v), want a *DecodeError naming monitored", err, de)
			}
		})
	}
}

// The list's ids in order without repeats, then the single id when it is 1 or more and
// not listed; nil when none.
func TestIDsOf(t *testing.T) {
	for _, tc := range []struct {
		name string
		list []int
		want []int
		one  int
	}{
		{"nothing", nil, nil, 0},
		{"an empty list", []int{}, nil, 0},
		{"a repeat", []int{41, 42, 41}, []int{41, 42}, 0},
		{"the single id alone", nil, []int{41}, 41},
		{"the single id listed", []int{41}, []int{41}, 41},
		{"the single id after the list", []int{42}, []int{42, 41}, 41},
		{"a negative single id", nil, nil, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := idsOf(tc.list, tc.one)
			if !slices.Equal(got, tc.want) || (got == nil) != (tc.want == nil) {
				t.Errorf("idsOf(%#v, %d) = %#v, want %#v", tc.list, tc.one, got, tc.want)
			}
		})
	}
}
