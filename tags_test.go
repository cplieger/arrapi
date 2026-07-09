package arrapi_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/cplieger/arrapi"
)

func TestTagIDs(t *testing.T) {
	tags := []arrapi.Tag{
		{ID: 1, Label: "anime"},
		{ID: 2, Label: "4k"},
		{ID: 3, Label: "kids"},
		{ID: 4, Label: "Upgrade"}, // arr lowercases, but be defensive
	}
	tests := []struct {
		name   string
		labels []string
		want   map[int]struct{}
	}{
		{"single match", []string{"anime"}, set(1)},
		{"multiple matches", []string{"anime", "kids"}, set(1, 3)},
		{"case insensitive", []string{"ANIME", "Kids"}, set(1, 3)},
		{"whitespace trimmed", []string{"  anime  "}, set(1)},
		{"mixed-case stored label", []string{"upgrade"}, set(4)},
		{"no match", []string{"documentary"}, set()},
		{"no labels returns nil", nil, nil},
		{"empty label ignored", []string{"", "  "}, set()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := arrapi.TagIDs(tags, tc.labels...)
			if !sameSet(got, tc.want) {
				t.Errorf("TagIDs(%v) = %v, want %v", tc.labels, got, tc.want)
			}
		})
	}
}

func TestHasAnyTag(t *testing.T) {
	tests := []struct {
		name     string
		itemTags []int
		ids      map[int]struct{}
		want     bool
	}{
		{"present", []int{1, 5, 9}, set(5), true},
		{"first present", []int{5, 1}, set(5), true},
		{"absent", []int{1, 2, 3}, set(9), false},
		{"empty item tags", nil, set(1), false},
		{"empty id set", []int{1, 2}, set(), false},
		{"nil id set", []int{1, 2}, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := arrapi.HasAnyTag(tc.itemTags, tc.ids); got != tc.want {
				t.Errorf("HasAnyTag(%v, %v) = %v, want %v", tc.itemTags, tc.ids, got, tc.want)
			}
		})
	}
}

func TestGetTags(t *testing.T) {
	rs := newServer(t, http.StatusOK, `[{"id":1,"label":"anime"},{"id":2,"label":"4k"}]`)
	s := fastSonarr(t, rs.srv.URL)

	tags, err := s.GetTags(t.Context())
	if err != nil {
		t.Fatalf("GetTags: %v", err)
	}
	if len(tags) != 2 || tags[0].ID != 1 || tags[0].Label != "anime" {
		t.Errorf("tags = %+v, want [{anime 1} {4k 2}]", tags)
	}
	if got := deref(rs.lastPath.Load()); got != "/api/v3/tag?" {
		t.Errorf("path = %q, want /api/v3/tag?", got)
	}
}

func TestUnmatchedLabels(t *testing.T) {
	tags := []arrapi.Tag{
		{ID: 1, Label: "anime"},
		{ID: 2, Label: "4k"},
		{ID: 4, Label: "Upgrade"}, // arr lowercases, but be defensive
	}
	tests := []struct {
		name   string
		labels []string
		want   []string
	}{
		{"all match", []string{"anime", "4k"}, nil},
		{"one missing", []string{"anime", "documentary"}, []string{"documentary"}},
		{"case-insensitive match not reported", []string{"ANIME", "upgrade"}, nil},
		{"whitespace-trimmed match not reported", []string{"  anime  "}, nil},
		{"empty and whitespace-only ignored", []string{"", "  "}, nil},
		{"verbatim and input order preserved", []string{"zzz", "yyy"}, []string{"zzz", "yyy"}},
		{"no labels returns nil", nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := arrapi.UnmatchedLabels(tags, tc.labels...)
			if !slices.Equal(got, tc.want) {
				t.Errorf("UnmatchedLabels(%v) = %v, want %v", tc.labels, got, tc.want)
			}
		})
	}
}

func TestResolveTagIDs(t *testing.T) {
	rs := newServer(t, http.StatusOK, `[{"id":1,"label":"anime"},{"id":2,"label":"4k"},{"id":3,"label":"kids"}]`)
	s := fastSonarr(t, rs.srv.URL)

	ids, unmatched, err := s.ResolveTagIDs(t.Context(), "anime", "KIDS", "documentary")
	if err != nil {
		t.Fatalf("ResolveTagIDs: %v", err)
	}
	if !sameSet(ids, set(1, 3)) {
		t.Errorf("ids = %v, want {1,3} (anime + case-insensitive kids)", ids)
	}
	if !slices.Equal(unmatched, []string{"documentary"}) {
		t.Errorf("unmatched = %v, want [documentary]", unmatched)
	}
	if got := deref(rs.lastPath.Load()); got != "/api/v3/tag?" {
		t.Errorf("path = %q, want /api/v3/tag?", got)
	}
}

func TestResolveTagIDs_noLabelsSkipsRequest(t *testing.T) {
	rs := newServer(t, http.StatusOK, `[]`)
	s := fastSonarr(t, rs.srv.URL)

	ids, unmatched, err := s.ResolveTagIDs(t.Context())
	if err != nil || ids != nil || unmatched != nil {
		t.Fatalf("ResolveTagIDs() = (%v, %v, %v), want (nil, nil, nil)", ids, unmatched, err)
	}
	if rs.lastPath.Load() != nil {
		t.Errorf("no-labels ResolveTagIDs issued a request to %q, want none", deref(rs.lastPath.Load()))
	}
}

func TestResolveTagIDs_fetchError(t *testing.T) {
	rs := newServer(t, http.StatusInternalServerError, "boom")
	s := fastSonarr(t, rs.srv.URL, arrapi.WithMaxAttempts(1))

	if _, _, err := s.ResolveTagIDs(t.Context(), "anime"); err == nil {
		t.Fatal("expected error when the tag fetch fails")
	}
}

func set(ids ...int) map[int]struct{} {
	m := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

func sameSet(a, b map[int]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}
