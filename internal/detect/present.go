package detect

import (
	"encoding/json"

	"github.com/cplieger/arrapi/v3/internal/transport"
)

// maxDeprecated bounds the /api "deprecated" tokens a detection keeps.
const maxDeprecated = 8

// present is the presentation of one server string; transport.ServerTextBytes owns
// its bound.
func present(t *transport.Client, s string) string {
	return t.Present(s, transport.ServerTextBytes)
}

// wellFormedTokens keeps the raw well-formed generation tokens of a "deprecated"
// list, at most maxDeprecated; an entry that is not a string is ignored like a
// malformed one.
func wellFormedTokens(raw []json.RawMessage) []string {
	var out []string
	for _, m := range raw {
		var tok string
		if json.Unmarshal(m, &tok) != nil {
			continue
		}
		if _, ok := generation(tok); ok && len(out) < maxDeprecated {
			out = append(out, tok)
		}
	}
	return out
}

func presentAll(t *transport.Client, toks []string) []string {
	if toks == nil {
		return nil
	}
	out := make([]string, len(toks))
	for i, s := range toks {
		out[i] = present(t, s)
	}
	return out
}
