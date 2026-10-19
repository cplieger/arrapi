package arrapi_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/cplieger/arrapi/v3"
)

func set(ids ...int) map[int]struct{} {
	m := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

func TestTagIDs(t *testing.T) {
	tags := []arrapi.Tag{
		{ID: 1, Label: "anime"},
		{ID: 2, Label: "4k"},
		{ID: 3, Label: "kids"},
		{ID: 4, Label: "Upgrade"},
		{ID: 5, Label: "θ"},
		{ID: 6, Label: "σ"},
	}
	tests := []struct {
		name   string
		want   map[int]struct{}
		labels []string
	}{
		{"single match", set(1), []string{"anime"}},
		{"multiple matches", set(1, 3), []string{"anime", "kids"}},
		{"case folded", set(1, 3), []string{"ANIME", "Kids"}},
		{"whitespace trimmed", set(1), []string{"  anime\t"}},
		{"stored label lowercased too", set(4), []string{"upgrade"}},
		{"unicode lowercased", set(6), []string{"Σ"}},
		{"unicode case fold is not lowercasing", set(), []string{"ϑ"}},
		{"no match is a non-nil empty set", set(), []string{"documentary"}},
		{"whitespace-only labels are a non-nil empty set", set(), []string{"", "  "}},
		{"no labels is nil", nil, nil},
		{"empty label list is nil", nil, []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := arrapi.TagIDs(tags, tc.labels...)
			if (got == nil) != (tc.want == nil) {
				t.Fatalf("TagIDs(%q) = %v (nil %v), want nil %v", tc.labels, got, got == nil, tc.want == nil)
			}
			if !maps.Equal(got, tc.want) {
				t.Errorf("TagIDs(%q) = %v, want %v", tc.labels, slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(tc.want)))
			}
		})
	}
}

func TestHasAnyTag(t *testing.T) {
	tests := []struct {
		name     string
		ids      map[int]struct{}
		itemTags []int
		want     bool
	}{
		{"present", set(5), []int{1, 5, 9}, true},
		{"first present", set(5), []int{5, 1}, true},
		{"absent", set(9), []int{1, 2, 3}, false},
		{"no item tags", set(1), nil, false},
		{"empty id set", set(), []int{1, 2}, false},
		{"nil id set", nil, []int{1, 2}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := arrapi.HasAnyTag(tc.itemTags, tc.ids); got != tc.want {
				t.Errorf("HasAnyTag(%v, %v) = %v, want %v", tc.itemTags, tc.ids, got, tc.want)
			}
		})
	}
}
