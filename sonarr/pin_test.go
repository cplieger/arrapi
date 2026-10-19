package sonarr

import (
	"testing"

	"github.com/cplieger/arrapi/v3"
)

// Expected values come from Pin's and Pinned's contracts, docs/request-behavior.md
// ("Reads from one server version", the retry rules), each engine's Features, and
// the fake's routes, which follow the recorded Sonarr 4.0 and 5.0 /api and
// system/status answers.

const v4EpisodeFiles = "GET /api/v3/episodefile"

// fileFrom is an episode-file list naming the server that served it in RelativePath,
// so a test can see whose answer a returned value was decoded from.
func fileFrom(server string) string {
	return `[{"id":1,"seriesId":7,"seasonNumber":1,"relativePath":"` + server + `"}]`
}

func pin(t *testing.T, c *Client) *Pinned {
	t.Helper()
	p, err := c.Pin(t.Context())
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	return p
}

func wantFileFrom(t *testing.T, got []EpisodeFile, server string) {
	t.Helper()
	if len(got) != 1 || got[0].RelativePath != server {
		t.Fatalf("EpisodeFiles = %+v, want one file served by %s", got, server)
	}
}

func wantChangedTo(t *testing.T, err error, to string) {
	t.Helper()
	e := wantChanged(t, err)
	if e.From != v4 || e.To != to {
		t.Errorf("ServerChangedError From %q To %q, want From %q To %q", e.From, e.To, v4, to)
	}
}

func TestPinDetectsAndReadsOnItsDetection(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	p := pin(t, c)
	wantRequests(t, f, detectV4...)
	want := Info{Version: v4, API: arrapi.APIv3, Features: Features{}}
	if got := p.Info(); got != want {
		t.Errorf("Info() = %+v, want %+v", got, want)
	}
	f.clearLog()
	f.on(v4Series, ans(200, v4, v4SeriesList))
	got, err := p.Series(t.Context())
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	wantTitle(t, got, "Sonarr Four Show")
	wantRequests(t, f, v4Series)
}

func TestPinDetectsFreshWithADetectionInstalled(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	cached(t, c, f)
	pin(t, c)
	wantRequests(t, f, detectV4...)
}

// A probe runs under the read budget, so one failed first probe is retried, where
// Info's single attempt would fail.
func TestPinRetriesAFailedFirstProbe(t *testing.T) {
	for name, first := range map[string]step{
		"a headered 503":    ans(503, v4, ""),
		"a header-less 503": ans(503, "", ""),
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake().sonarr4(v4)
			f.on("GET /api", first, ans(200, v4, apiBody("v3")))
			c := newClient(t, f)
			p := pin(t, c)
			if p.Info().API != arrapi.APIv3 {
				t.Errorf("Info().API = %q, want %q", p.Info().API, arrapi.APIv3)
			}
			wantRequests(t, f, "GET /api", "GET /api", "GET /api/v3/system/status")
		})
	}
}

func TestPinHonoursMaxAttempts(t *testing.T) {
	f := newFake().sonarr4(v4)
	f.on("GET /api", ans(503, v4, ""), ans(503, v4, ""), ans(200, v4, apiBody("v3")))
	c := newClient(t, f, WithMaxAttempts(2))
	_, err := c.Pin(t.Context())
	wantStatus(t, err, 503)
	wantRequests(t, f, "GET /api", "GET /api")
}

func TestPinnedReadRetriesOnThePin(t *testing.T) {
	t.Run("a headered 503 retries on the pin with no detection", func(t *testing.T) {
		f := newFake().sonarr4(v4)
		c := newClient(t, f)
		p := pin(t, c)
		f.clearLog()
		f.on(v4EpisodeFiles, ans(503, v4, ""), ans(200, v4, fileFrom("sonarr-4")))
		got, err := p.EpisodeFiles(t.Context(), 7)
		if err != nil {
			t.Fatalf("EpisodeFiles: %v", err)
		}
		wantFileFrom(t, got, "sonarr-4")
		wantRequests(t, f, v4EpisodeFiles, v4EpisodeFiles)
	})
	t.Run("a header-less 503 re-detects the same version and continues", func(t *testing.T) {
		f := newFake().sonarr4(v4)
		c := newClient(t, f)
		p := pin(t, c)
		f.clearLog()
		f.on(v4EpisodeFiles, ans(503, "", ""), ans(200, v4, fileFrom("sonarr-4")))
		got, err := p.EpisodeFiles(t.Context(), 7)
		if err != nil {
			t.Fatalf("EpisodeFiles: %v", err)
		}
		wantFileFrom(t, got, "sonarr-4")
		wantRequests(t, f, join([]string{v4EpisodeFiles}, detectV4, []string{v4EpisodeFiles})...)
	})
}

// After an in-place upgrade a Client read moves onto Sonarr v5 (API v5); a pinned
// read refuses instead and sends nothing to /api/v5.
func TestPinnedReadNeverSwitchesEngines(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	p := pin(t, c)
	f.clearLog()
	f.sonarr5(v5)
	f.on(v4EpisodeFiles, ans(200, v5, fileFrom("sonarr-5-api-v3")))
	f.on("GET /api/v5/episodefile", ans(200, v5, fileFrom("sonarr-5-api-v5")))

	got, err := p.EpisodeFiles(t.Context(), 7)
	wantChangedTo(t, err, v5)
	if got != nil {
		t.Errorf("EpisodeFiles = %+v, want nothing", got)
	}
	wantRequests(t, f, join([]string{v4EpisodeFiles}, detectV5)...)

	f.clearLog()
	_, err = p.Episodes(t.Context(), 7)
	wantChangedTo(t, err, v5)
	wantRequests(t, f)

	f.clearLog()
	files, err := c.EpisodeFiles(t.Context(), 7)
	if err != nil {
		t.Fatalf("Client.EpisodeFiles after the upgrade: %v", err)
	}
	wantFileFrom(t, files, "sonarr-5-api-v5")
	wantRequests(t, f, "GET /api/v5/episodefile")
}

func TestPinnedReadRefusesAMinorUpgrade(t *testing.T) {
	const upgraded = "4.0.21.1"
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	p := pin(t, c)
	f.clearLog()
	f.sonarr4(upgraded)
	f.on(v4Series, ans(200, upgraded, v4SeriesList))
	got, err := p.Series(t.Context())
	wantChangedTo(t, err, upgraded)
	if got != nil {
		t.Errorf("Series = %+v, want nothing", got)
	}
	wantRequests(t, f, join([]string{v4Series}, detectV4)...)
}

// An A-B-A swap: the read that meets B fails, and once the client knows A again the
// pin is served by A. No value decoded from B is ever returned.
func TestPinnedReadsAcrossAnABASwapReturnNothingFromB(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	p := pin(t, c)
	f.on(v4Series, ans(200, v4, v4SeriesList))
	if _, err := p.Series(t.Context()); err != nil {
		t.Fatalf("Series on A: %v", err)
	}

	f.sonarr5(v5)
	f.on(v4EpisodeFiles, ans(200, v5, fileFrom("sonarr-5-api-v3")))
	f.on("GET /api/v5/episodefile", ans(200, v5, fileFrom("sonarr-5-api-v5")))
	got, err := p.EpisodeFiles(t.Context(), 7)
	wantChangedTo(t, err, v5)
	if got != nil {
		t.Errorf("EpisodeFiles on B = %+v, want nothing", got)
	}

	f.sonarr4(v4)
	f.on(v4EpisodeFiles, ans(200, v4, fileFrom("sonarr-4")))
	if _, err := c.Info(t.Context()); err != nil {
		t.Fatalf("Info on A again: %v", err)
	}
	f.clearLog()
	got, err = p.EpisodeFiles(t.Context(), 7)
	if err != nil {
		t.Fatalf("EpisodeFiles on A again: %v", err)
	}
	wantFileFrom(t, got, "sonarr-4")
	wantRequests(t, f, v4EpisodeFiles)
	for _, r := range f.requests() {
		if r == "GET /api/v5/episodefile" {
			t.Errorf("a pinned read reached Sonarr v5 (API v5): %q", f.requests())
		}
	}
}

// An answer without X-Application-Version to another call ends the pin's epoch; the
// next pinned read re-detects once and continues on the same version, so a deleted
// series' header-less 404 never ends the pass.
func TestPinSurvivesAHeaderlessAnswerToAnotherCall(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	p := pin(t, c)
	f.clearLog()
	f.on("GET /api/v3/tag", missingParent404)
	f.on(v4Series, ans(200, v4, v4SeriesList))
	_, err := c.Tags(t.Context())
	wantStatus(t, err, 404)
	got, err := p.Series(t.Context())
	if err != nil {
		t.Fatalf("Series after another call's header-less 404: %v", err)
	}
	wantTitle(t, got, "Sonarr Four Show")
	wantRequests(t, f, join([]string{"GET /api/v3/tag"}, detectV4, []string{v4Series})...)
}

// A Sonarr 5 reached only over /api/v3 is refused, never pinned.
func TestPinRefusesSonarr5OverAPIv3(t *testing.T) {
	f := newFake()
	f.reset(v5)
	f.on("GET /api", ans(404, "", ""))
	f.on("GET /api/v5/system/status", ans(404, v5, ""))
	f.on("GET /api/v3/system/status", ans(200, v5, statusBody("Sonarr", v5)))
	c := newClient(t, f)
	p, err := c.Pin(t.Context())
	if p != nil {
		t.Errorf("Pin = %+v, want nil", p)
	}
	wantUnsupported(t, err, arrapi.ReasonV5Unreachable)
}
