package detect

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3"
)

func TestSelectEngine(t *testing.T) {
	announced := func(current string, deprecated ...string) evidence {
		return evidence{current: current, deprecated: deprecated, announced: true}
	}
	pathV5, pathV3 := evidence{current: "v5"}, evidence{current: "v3"}
	tests := []struct {
		name       string
		want       string
		ev         evidence
		major      int
		api        arrapi.API
		deprecated bool
		reason     arrapi.UnsupportedReason
	}{
		{"Sonarr v4 (API v3) announced in /api", AppSonarr, announced("v3"), 4, arrapi.APIv3, false, 0},
		{"Sonarr v4 (API v3) with the /api path hidden", AppSonarr, pathV3, 4, arrapi.APIv3, false, 0},
		{"Sonarr v5 (API v5) announced in /api", AppSonarr, announced("v5", "v3"), 5, arrapi.APIv5, false, 0},
		{"Sonarr v5 (API v5) with the /api path hidden", AppSonarr, pathV5, 5, arrapi.APIv5, false, 0},
		{"Sonarr v6 deprecating API v5", AppSonarr, announced("v6", "v5"), 6, arrapi.APIv5, true, 0},
		{"Sonarr v6 announcing only API v6", AppSonarr, announced("v6"), 6, "", false, arrapi.ReasonUnknownGeneration},
		{"Sonarr v5 (API v5) reachable only over the /api/v3 refusal path", AppSonarr, pathV3, 5, "", false, arrapi.ReasonV5Unreachable},
		{"Sonarr v5 (API v5) announcing API v3, refused", AppSonarr, announced("v3"), 5, "", false, arrapi.ReasonUnsupportedVersion},
		{"Sonarr v3 (unsupported) with the /api path readable", AppSonarr, announced("v3"), 3, "", false, arrapi.ReasonUnsupportedVersion},
		{"Sonarr v3 (unsupported) with the /api path hidden", AppSonarr, pathV3, 3, "", false, arrapi.ReasonUnsupportedVersion},
		{"Sonarr v4 (API v3) answering the /api/v5 status", AppSonarr, pathV5, 4, "", false, arrapi.ReasonUnsupportedVersion},
		{"Radarr announcing API v3", AppRadarr, announced("v3"), 6, arrapi.APIv3, false, 0},
		{"Radarr with the api path hidden", AppRadarr, pathV3, 5, arrapi.APIv3, false, 0},
		{"Radarr deprecating API v3", AppRadarr, announced("v4", "v3"), 7, arrapi.APIv3, true, 0},
		{"Radarr announcing only API v4", AppRadarr, announced("v4"), 7, "", false, arrapi.ReasonUnknownGeneration},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sel, reason := selectEngine(tc.want, tc.ev, tc.major)
			if reason != tc.reason {
				t.Fatalf("reason = %v, want %v", reason, tc.reason)
			}
			if sel.api != tc.api || sel.deprecated != tc.deprecated {
				t.Errorf("selection = %+v, want API %q deprecated %v", sel, tc.api, tc.deprecated)
			}
		})
	}
}

func TestASCIIEqualFold(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"Sonarr", "Sonarr", true},
		{"SONARR", "Sonarr", true},
		{"sonarr", "Sonarr", true},
		{"\u017fonarr", "Sonarr", false},
		{"Radarr", "Sonarr", false},
		{"Sonar", "Sonarr", false},
		{"Sonarr ", "Sonarr", false},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%q", tc.a), func(t *testing.T) {
			if got := asciiEqualFold(tc.a, tc.b); got != tc.want {
				t.Errorf("asciiEqualFold(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestGeneration(t *testing.T) {
	tests := []struct {
		tok string
		n   int
		ok  bool
	}{
		{"v3", 3, true},
		{"v5", 5, true},
		{"v123", 123, true},
		{"v", 0, false},
		{"v0", 0, false},
		{"v05", 0, false},
		{"v1234", 0, false},
		{"V5", 0, false},
		{"../x", 0, false},
		{"v5 ", 0, false},
		{"5", 0, false},
		{"", 0, false},
	}
	for _, tc := range tests {
		t.Run(strings.ReplaceAll(fmt.Sprintf("%q", tc.tok), "/", "slash"), func(t *testing.T) {
			n, ok := generation(tc.tok)
			if n != tc.n || ok != tc.ok {
				t.Errorf("generation(%q) = (%d, %v), want (%d, %v)", tc.tok, n, ok, tc.n, tc.ok)
			}
		})
	}
}
