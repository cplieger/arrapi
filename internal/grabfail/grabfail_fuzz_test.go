package grabfail_test

import (
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/grabfail"
)

// FuzzClassify: a *GrabError only for 404 or 409, over the input itself, with a reason
// whose sentence opens the trimmed message; anything else is the input unchanged; a
// refusal another server version sent is never classified; no magnet scheme, in any
// case, is left in the Message or Body of either, which never grow and keep a trailing
// cut marker; and no byte after a magnet scheme reaches Message, Body or Error().
func FuzzClassify(f *testing.F) {
	for _, msg := range []string{cacheMiss, unparsed, noSeries, noMovie, indexerFailed, indexerFailedX, "", "Not Found", " " + cacheMiss, "Unable to , will need to be manually provided", indexerFailed + ": 'magnet:?tr=x'", "MaGnEt:magnet:x"} {
		for _, code := range []int{404, 409, 500} {
			f.Add(code, msg, `{"message":"`+msg+`"}`)
		}
	}
	f.Add(409, indexerFailed+": magnet:", `{"message":"\u0027magnet:?a=1\u0026tr=x\u0027"}`)
	f.Add(409, "magnet:...", `{"message":"magnet:?tr=x...`)
	f.Add(409, indexerFailed+": 'magnet:?dn=Journey's End\r\n&tr=x'", `{"message":"magnet:?dn=A \"B\"\u0026tr=x"}`)
	heads := map[arrapi.GrabReason]string{
		arrapi.GrabNotCached:   cacheMiss,
		arrapi.GrabUnmapped:    "Unable to ",
		arrapi.GrabUnavailable: indexerFailed,
	}
	noMagnet := func(t *testing.T, se *arrapi.StatusError, msg, body string) {
		t.Helper()
		for _, c := range [][2]string{{msg, se.Message}, {body, se.Body}} {
			in, out := c[0], c[1]
			switch {
			case strings.Contains(strings.ToLower(out), "magnet:"):
				t.Fatalf("Classify left a magnet scheme in %q", out)
			case len(out) > len(in):
				t.Fatalf("Classify grew %q to %q", in, out)
			case strings.HasSuffix(in, "...") && !strings.HasSuffix(out, "..."):
				t.Fatalf("Classify dropped the cut marker of %q: %q", in, out)
			}
		}
	}
	f.Fuzz(func(t *testing.T, code int, msg, body string) {
		se := &arrapi.StatusError{Code: code, Message: msg, Body: body}
		got := grabfail.Classify(se)
		if ge, ok := got.(*arrapi.GrabError); ok {
			head, known := heads[ge.Reason]
			if (code != 404 && code != 409) || ge.Err != se || !known || !strings.HasPrefix(strings.TrimSpace(msg), head) {
				t.Fatalf("Classify(%d, %q) = %#v, want a GrabError only for 404 or 409, over the input, with a known reason", code, msg, ge)
			}
		} else if got != error(se) {
			t.Fatalf("Classify(%d, %q) = %#v, want the input unchanged", code, msg, got)
		}
		noMagnet(t, se, msg, body)
		inner := &arrapi.StatusError{Code: code, Message: msg, Body: body}
		changed := &arrapi.ServerChangedError{Status: inner}
		if got := grabfail.Classify(changed); got != error(changed) {
			t.Fatalf("Classify(ServerChangedError over %d, %q) = %#v, want it unchanged", code, msg, got)
		}
		noMagnet(t, inner, msg, body)
		if strings.Contains(msg, passkey) || strings.Contains(body, passkey) {
			return
		}
		planted := func() *arrapi.StatusError {
			return &arrapi.StatusError{Code: code, Message: msg + "magnet:" + body + passkey, Body: body + "MAGNET:" + msg + passkey}
		}
		direct, wrapped := planted(), planted()
		for _, c := range []struct {
			se  *arrapi.StatusError
			err error
		}{
			{direct, grabfail.Classify(direct)},
			{wrapped, grabfail.Classify(&arrapi.ServerChangedError{Status: wrapped})},
		} {
			for _, out := range []string{c.se.Message, c.se.Body, c.err.Error()} {
				if strings.Contains(out, passkey) {
					t.Fatalf("Classify left the text after a magnet scheme in %q", out)
				}
			}
		}
	})
}
