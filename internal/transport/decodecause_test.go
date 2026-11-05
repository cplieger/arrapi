package transport

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"testing"

	"github.com/cplieger/arrapi/v3"
)

var (
	decodeSentence = regexp.MustCompile(`^arrapi: decode [^:]*: (?:field ([^:]+): )?(.*)$`)
	syntaxReason   = regexp.MustCompile(`^invalid JSON at byte (\d+)$`)
	typeReason     = regexp.MustCompile(`^a JSON (\w+) cannot decode into (.+)$`)
)

// AssertDecodeCause fails t unless err holds an *arrapi.DecodeError whose Err is the
// one its reason names: a *json.SyntaxError at the byte the text names, a
// *json.UnmarshalTypeError holding only the kind word, the text's field and Go type
// and no cause of its own, or nil for every other reason.
func AssertDecodeCause(t testing.TB, err error) {
	t.Helper()
	de, ok := errors.AsType[*arrapi.DecodeError](err)
	if !ok {
		t.Fatalf("error %v (%T) holds no *arrapi.DecodeError", err, err)
	}
	m := decodeSentence.FindStringSubmatch(de.Error())
	if m == nil {
		t.Fatalf("DecodeError text %q is not arrapi's decode sentence", de.Error())
	}
	field, reason := m[1], m[2]
	if s := syntaxReason.FindStringSubmatch(reason); s != nil {
		offset, _ := strconv.ParseInt(s[1], 10, 64)
		se, ok := de.Err.(*json.SyntaxError)
		if !ok || se.Offset != offset {
			t.Errorf("%q: Err = %#v, want a *json.SyntaxError at offset %d", de.Error(), de.Err, offset)
		}
		return
	}
	if s := typeReason.FindStringSubmatch(reason); s != nil {
		te, ok := de.Err.(*json.UnmarshalTypeError)
		if !ok {
			t.Errorf("%q: Err = %#v, want a *json.UnmarshalTypeError", de.Error(), de.Err)
			return
		}
		if te.Value != s[1] || te.Type == nil || te.Type.String() != s[2] || te.Field != field || te.Err != nil {
			t.Errorf("%q: Err = %#v, want Value %q, Type %s, Field %q and no Err", de.Error(), te, s[1], s[2], field)
		}
		return
	}
	if de.Err != nil {
		t.Errorf("%q: Err = %#v, want nil", de.Error(), de.Err)
	}
}
