package arrapi_test

import (
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
