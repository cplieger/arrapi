package detect

import (
	"slices"
	"strconv"

	"github.com/cplieger/arrapi/v3"
)

// The applications a client expects.
const (
	AppSonarr = "Sonarr"
	AppRadarr = "Radarr"
)

// evidence is what says which API generations a server serves: /api's announcement
// when it was readable, else the generation of the status path that answered.
type evidence struct {
	current    string   // /api "current", or the answering status path's generation
	deprecated []string // well-formed tokens only; nil when /api was hidden
	announced  bool     // /api was readable
}

// selection is the engine a detection chose.
type selection struct {
	api        arrapi.API
	deprecated bool
}

// generation returns the number of a well-formed generation token, "v" and 1 to 3
// digits without a leading zero; ok is false for any other string.
func generation(tok string) (n int, ok bool) {
	if len(tok) < 2 || len(tok) > 4 || tok[0] != 'v' || tok[1] == '0' {
		return 0, false
	}
	for _, r := range tok[1:] {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(tok[1:])
	return n, err == nil
}

func (e evidence) lists(tok string) bool {
	return e.current == tok || slices.Contains(e.deprecated, tok)
}

// asciiEqualFold compares ASCII-case-insensitively: strings.EqualFold also folds
// non-ASCII runes ("ſonarr" would pass as "Sonarr").
func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// selectEngine applies the selection rule to a status answer from the application
// want that passed the not-an-API and header checks. It returns the reason of the
// refusal when no engine reads the server.
func selectEngine(want string, ev evidence, major int) (selection, arrapi.UnsupportedReason) {
	if want == AppRadarr {
		if ev.lists("v3") {
			return selection{api: arrapi.APIv3, deprecated: ev.current != "v3"}, 0
		}
		return selection{}, arrapi.ReasonUnknownGeneration
	}
	// The Sonarr v4 (API v3) engine is chosen only for current "v3" at version major
	// 4. Sonarr 5 stores each series in its chosen order and its /api/v3 reports those
	// numbers as if they were aired, so the Sonarr v4 (API v3) engine would read wrong
	// numbers there.
	switch {
	case ev.lists("v5") && major >= 5:
		return selection{api: arrapi.APIv5, deprecated: ev.current != "v5"}, 0
	case !ev.announced && ev.current == "v5":
		return selection{}, arrapi.ReasonUnsupportedVersion
	case ev.current == "v3" && major == 4:
		return selection{api: arrapi.APIv3}, 0
	case !ev.announced && ev.current == "v3" && major >= 5:
		return selection{}, arrapi.ReasonV5Unreachable
	case ev.current == "v3":
		return selection{}, arrapi.ReasonUnsupportedVersion
	}
	return selection{}, arrapi.ReasonUnknownGeneration
}
