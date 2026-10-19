package transport_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

type wireItem struct {
	ID int `json:"id"`
}

func itemID(w *wireItem) int { return w.ID }

func TestGetListMapsEveryItemInOrder(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want []int
	}{
		"three items":   {`[{"id":3},{"id":1},{"id":2}]`, []int{3, 1, 2}},
		"an empty list": {`[]`, []int{}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			f.set(reply(200, v4, tc.body))
			got, res := transport.GetList(t.Context(), c, d, "/api/v3/tag", itemID)
			if !res.Accepted() {
				t.Fatalf("GetList = (verdict %v, err %v), want accepted", res.Verdict, res.Err)
			}
			if got == nil || !slices.Equal(got, tc.want) {
				t.Errorf("GetList = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestGetListRejectsANullList(t *testing.T) {
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{})
	d := detect(t, c, f, v4)
	f.set(reply(200, v4, `null`))
	got, res := transport.GetList(t.Context(), c, d, "/api/v3/tag", itemID)
	if want := "arrapi: decode /api/v3/tag: the answer is null"; res.Err == nil || res.Err.Error() != want {
		t.Errorf("GetList error = %v, want %q", res.Err, want)
	}
	if got != nil || res.Accepted() {
		t.Errorf("GetList = (%#v, accepted %v), want no items, not accepted", got, res.Accepted())
	}
}

func TestGetListRejectsANullItem(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"the only item":  {`[null]`, "arrapi: decode /api/v3/tag: list item 0 is null"},
		"a later item":   {`[{"id":1},{"id":2},null]`, "arrapi: decode /api/v3/tag: list item 2 is null"},
		"padded by JSON": {"[{\"id\":1}, \n null ]", "arrapi: decode /api/v3/tag: list item 1 is null"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			f.set(reply(200, v4, tc.body))
			got, res := transport.GetList(t.Context(), c, d, "/api/v3/tag?x=1", itemID)
			if res.Err == nil || res.Err.Error() != tc.want {
				t.Errorf("GetList error = %v, want %q", res.Err, tc.want)
			}
			if errors.Unwrap(res.Err) != nil {
				t.Errorf("error wraps %v, want nothing", errors.Unwrap(res.Err))
			}
			if got != nil || res.Accepted() || res.Next != transport.NextDone {
				t.Errorf("GetList = (%#v, accepted %v, next %v), want no items, not accepted, done", got, res.Accepted(), res.Next)
			}
		})
	}
}

func TestGetOne(t *testing.T) {
	t.Run("an answer maps", func(t *testing.T) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		f.set(reply(200, v4, `{"id":7}`))
		got, res := transport.GetOne(t.Context(), c, d, "/api/v3/series/7", itemID)
		if !res.Accepted() || got != 7 {
			t.Errorf("GetOne = (%d, verdict %v, err %v), want 7, accepted", got, res.Verdict, res.Err)
		}
	})
	t.Run("an attributed 404 is absent", func(t *testing.T) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		f.set(reply(404, v4, `{"id":7}`))
		got, res := transport.GetOne(t.Context(), c, d, "/api/v3/series/7", itemID)
		if res.Verdict != transport.VerdictAbsent || res.Err != nil || res.Accepted() || got != 0 {
			t.Errorf("GetOne = (%d, verdict %v, err %v), want 0, absent, no error", got, res.Verdict, res.Err)
		}
	})
	t.Run("null is a decode error", func(t *testing.T) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		f.set(reply(200, v4, `null`))
		got, res := transport.GetOne(t.Context(), c, d, "/api/v3/series/7", itemID)
		if want := "arrapi: decode /api/v3/series/7: the answer is null"; res.Err == nil || res.Err.Error() != want {
			t.Errorf("GetOne error = %v, want %q", res.Err, want)
		}
		if res.Accepted() || got != 0 {
			t.Errorf("GetOne = (%d, accepted %v), want 0, not accepted", got, res.Accepted())
		}
	})
}

func TestResultAccepted(t *testing.T) {
	for name, tc := range map[string]struct {
		r    transport.Result
		want bool
	}{
		"accepted and decoded":   {transport.Result{Verdict: transport.VerdictAccept}, true},
		"accepted, not decoded":  {transport.Result{Verdict: transport.VerdictAccept, Err: errors.New("decode")}, false},
		"absent":                 {transport.Result{Verdict: transport.VerdictAbsent}, false},
		"a status answer":        {transport.Result{Verdict: transport.VerdictStatus, Err: &arrapi.StatusError{Code: 500}}, false},
		"refused, with no error": {transport.Result{Verdict: transport.VerdictRefused}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.r.Accepted(); got != tc.want {
				t.Errorf("Accepted() = %v, want %v", got, tc.want)
			}
		})
	}
}

// token decodes itself, null included, as sonarr's and radarr's history event token
// does, so a null item of a list of tokens is its own to judge.
type token int

//deadset:ignore DS1103 -- encoding/json calls it for each tokens item; readInto decodes through an any, which deadset cannot follow
func (k *token) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*k = -1
	}
	return nil
}

type wireNested struct {
	wireExtra
	Stats   *wireStats       `json:"stats"`
	ByName  map[string][]int `json:"byName"`
	Seasons []wireSeason     `json:"seasons"`
	Tags    []int            `json:"tags"`
	Tokens  []token          `json:"tokens"`
	Title   string           `json:"title"`
	ID      int              `json:"id"`
}

type wireSeason struct {
	Names  []string `json:"names"`
	Number int      `json:"number"`
}

// wireExtra is embedded untagged, so encoding/json promotes its fields.
type wireExtra struct {
	Extra []int `json:"extra"`
}

type wireStats struct {
	Groups []string `json:"groups"`
}

func nestedID(w *wireNested) int { return w.ID }

// A null item of an array a wire type declares, at any depth, would decode as a
// zero-value item (seasons [null] a phantom season 0, tags [null] a tag id 0); no
// Sonarr or Radarr resource holds one, so it is a decode error naming the declared
// field path. Arrays the type does not declare, an item type
// that decodes itself, and a null array are not judged, and decode as encoding/json
// documents: a null array is a nil slice, undeclared members are ignored, and a
// json.Unmarshaler item is handed its null.
func TestNestedNullItemIsADecodeError(t *testing.T) {
	const path = "/api/v3/series"
	for name, tc := range map[string]struct {
		body, want string
		decoded    []wireNested
	}{
		"a struct item":          {`[{"id":1,"seasons":[null]}]`, "arrapi: decode " + path + ": field seasons: item 0 is null", nil},
		"a later struct item":    {`[{"id":1},{"id":2,"seasons":[{"number":1}, null]}]`, "arrapi: decode " + path + ": field seasons: item 1 is null", nil},
		"an int item":            {`[{"id":1,"tags":[4,null]}]`, "arrapi: decode " + path + ": field tags: item 1 is null", nil},
		"an item two deep":       {`[{"id":1,"seasons":[{"number":1,"names":["a",null]}]}]`, "arrapi: decode " + path + ": field seasons.names: item 1 is null", nil},
		"under an object":        {`[{"id":1,"stats":{"groups":[null]}}]`, "arrapi: decode " + path + ": field stats.groups: item 0 is null", nil},
		"a key in another case":  {`[{"id":1,"Seasons":[null]}]`, "arrapi: decode " + path + ": field seasons: item 0 is null", nil},
		"padded by JSON":         {"[{\"id\":1,\"tags\":[ 1 ,\n null ]}]", "arrapi: decode " + path + ": field tags: item 1 is null", nil},
		"a promoted field":       {`[{"id":1,"extra":[null]}]`, "arrapi: decode " + path + ": field extra: item 0 is null", nil},
		"a map value":            {`[{"id":1,"byName":{"a key":[1,null]}}]`, "arrapi: decode " + path + ": field byName: item 1 is null", nil},
		"an undeclared array":    {`[{"id":1,"genres":[null],"ratings":{"votes":[null]}}]`, "", []wireNested{{ID: 1}}},
		"a self-decoding item":   {`[{"id":1,"tokens":[null,2]}]`, "", []wireNested{{ID: 1, Tokens: []token{-1, 0}}}},
		"a null array":           {`[{"id":1,"seasons":null,"tags":null}]`, "", []wireNested{{ID: 1}}},
		"null text in a string":  {`[{"id":1,"title":"[null, null]"}]`, "", []wireNested{{ID: 1, Title: "[null, null]"}}},
		"an escaped quote first": {`[{"id":1,"title":"a\"[null","tags":[1]}]`, "", []wireNested{{ID: 1, Title: `a"[null`, Tags: []int{1}}}},
		"every member":           {`[{"id":1,"title":"T","tags":[2],"extra":[3],"stats":{"groups":["g"]},"byName":{"k":[4]},"seasons":[{"number":1,"names":["n"]}]}]`, "", []wireNested{{Extra: []int{3}, Stats: &wireStats{Groups: []string{"g"}}, ByName: map[string][]int{"k": {4}}, Seasons: []wireSeason{{Names: []string{"n"}, Number: 1}}, Tags: []int{2}, Title: "T", ID: 1}}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, nil)
			c := newClient(t, f.srv.URL, transport.Config{})
			d := detect(t, c, f, v4)
			f.set(reply(200, v4, tc.body))
			got, res := transport.GetList(t.Context(), c, d, path+"?includeX=true", func(w *wireNested) wireNested { return *w })
			if tc.want == "" {
				if !res.Accepted() || !reflect.DeepEqual(got, tc.decoded) {
					t.Fatalf("GetList = (%+v, verdict %v, err %v), want accepted as %+v", got, res.Verdict, res.Err, tc.decoded)
				}
				return
			}
			if res.Err == nil || res.Err.Error() != tc.want {
				t.Errorf("GetList error = %v, want %q", res.Err, tc.want)
			}
			if errors.Unwrap(res.Err) != nil {
				t.Errorf("error wraps %v, want nothing", errors.Unwrap(res.Err))
			}
			if got != nil || res.Accepted() || res.Next != transport.NextDone {
				t.Errorf("GetList = (%#v, accepted %v, next %v), want no items, not accepted, done", got, res.Accepted(), res.Next)
			}
		})
	}
	t.Run("a by-id object", func(t *testing.T) {
		f := newFake(t, nil)
		c := newClient(t, f.srv.URL, transport.Config{})
		d := detect(t, c, f, v4)
		f.set(reply(200, v4, `{"id":7,"seasons":[null]}`))
		got, res := transport.GetOne(t.Context(), c, d, path+"/7", nestedID)
		if want := "arrapi: decode " + path + "/7: field seasons: item 0 is null"; res.Err == nil || res.Err.Error() != want {
			t.Errorf("GetOne error = %v, want %q", res.Err, want)
		}
		if res.Accepted() || got != 0 {
			t.Errorf("GetOne = (%d, accepted %v), want 0, not accepted", got, res.Accepted())
		}
	})
}
