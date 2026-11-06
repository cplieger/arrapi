package radarr

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/cplieger/arrapi/v3"
)

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

// A status outside the enum is written as a number (STJson.cs registers
// JsonStringEnumConverter with allowIntegerValues), so a number and null read "".
func TestCommandStatusTokenKeepsAString(t *testing.T) {
	for in, want := range map[string]string{
		`"completed"`:      "completed",
		`"compl\u0065ted"`: "completed",
		`"someFuture"`:     "someFuture",
		`""`:               "",
		`3`:                "",
		`-1`:               "",
		`1e999`:            "",
		`null`:             "",
	} {
		var tok commandStatusToken
		if err := json.Unmarshal([]byte(in), &tok); err != nil {
			t.Errorf("decode %s: %v, want no error", in, err)
			continue
		}
		if string(tok) != want {
			t.Errorf("decode %s = %q, want %q", in, tok, want)
		}
	}
}

func TestCommandStatusTokenOtherKindIsError(t *testing.T) {
	for in, kind := range map[string]string{`{}`: "object", `["completed"]`: "array", `true`: "bool", `false`: "bool"} {
		var w radarrCommand
		err := json.Unmarshal([]byte(`{"id":1,"status":`+in+`}`), &w)
		te, ok := errors.AsType[*json.UnmarshalTypeError](err)
		if !ok {
			t.Errorf("decode status %s: %v, want a *json.UnmarshalTypeError (no server writes that kind)", in, err)
			continue
		}
		if te.Value != kind || te.Type != reflect.TypeFor[arrapi.CommandStatus]() || te.Field != "status" {
			t.Errorf("decode status %s: Value %q, Type %v, Field %q; want %s, arrapi.CommandStatus, status", in, te.Value, te.Type, te.Field, kind)
		}
	}
}
