package detect

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

func wantUnsupported(t *testing.T, err error, reason arrapi.UnsupportedReason) *arrapi.UnsupportedAPIError {
	t.Helper()
	e, ok := errors.AsType[*arrapi.UnsupportedAPIError](err)
	if !ok {
		t.Fatalf("error = %v (%T), want *arrapi.UnsupportedAPIError", err, err)
	}
	if e.Reason != reason {
		t.Fatalf("Reason = %v, want %v (%v)", e.Reason, reason, e)
	}
	return e
}

func wantStatus(t *testing.T, err error, code int) *arrapi.StatusError {
	t.Helper()
	if _, ok := errors.AsType[*arrapi.UnsupportedAPIError](err); ok {
		t.Fatalf("error = %v, want a *arrapi.StatusError, not UnsupportedAPIError", err)
	}
	e, ok := errors.AsType[*arrapi.StatusError](err)
	if !ok || e.Code != code {
		t.Fatalf("error = %v (%T), want *arrapi.StatusError with Code %d", err, err, code)
	}
	return e
}

func TestDetectSonarrV4APIv3(t *testing.T) {
	f := newFake()
	serveSonarr4(f, v4)
	rec := &recorder{}
	c := newClient(t, f, transport.Config{Logger: slog.New(rec)})
	d, err := run(t, c, AppSonarr)
	if err != nil {
		t.Fatalf("walkArms: %v", err)
	}
	if got := rec.find("arrapi: detected"); len(got) != 1 || got[0]["level"] != "DEBUG" || got[0]["app"] != AppSonarr ||
		got[0]["version"] != v4 || got[0]["api"] != "v3" || got[0]["deprecated"] != "false" {
		t.Errorf("detection records = %v", got)
	}
	if d.API != arrapi.APIv3 || d.Deprecated || d.Identity != (transport.Identity{App: AppSonarr, AppVersion: v4}) ||
		d.Version != v4 || d.Epoch != c.State().Epoch {
		t.Errorf("detection = %+v", d)
	}
	wantRequests(t, f, "GET /api", "GET /api/v3/system/status")
	if c.Current() != nil {
		t.Error("walkArms installed its detection")
	}
}

func TestDetectSonarrV5APIv5(t *testing.T) {
	f := newFake()
	serveSonarr5(f, v5)
	c := newClient(t, f, transport.Config{})
	d, err := run(t, c, AppSonarr)
	if err != nil {
		t.Fatalf("walkArms: %v", err)
	}
	if d.API != arrapi.APIv5 || d.Deprecated || d.Identity.AppVersion != v5 {
		t.Errorf("detection = %+v", d)
	}
	wantRequests(t, f, "GET /api", "GET /api/v5/system/status")
}

func TestDetectRadarrAPIv3(t *testing.T) {
	t.Run("api readable", func(t *testing.T) {
		f := newFake()
		serveRadarr(f, vR)
		d, err := run(t, newClient(t, f, transport.Config{}), AppRadarr)
		if err != nil {
			t.Fatalf("walkArms: %v", err)
		}
		if d.API != arrapi.APIv3 || d.Identity != (transport.Identity{App: AppRadarr, AppVersion: vR}) {
			t.Errorf("detection = %+v", d)
		}
		wantRequests(t, f, "GET /api", "GET /api/v3/system/status")
	})
	t.Run("api hidden is arm E", func(t *testing.T) {
		f := newFake()
		serveRadarr(f, vR)
		f.on("GET /api", ans(404, "", "not found"))
		d, err := run(t, newClient(t, f, transport.Config{}), AppRadarr)
		if err != nil {
			t.Fatalf("walkArms: %v", err)
		}
		if d.API != arrapi.APIv3 || d.Deprecated {
			t.Errorf("detection = %+v", d)
		}
		wantRequests(t, f, "GET /api", "GET /api/v3/system/status")
	})
}

func TestDetectRefusesSonarr5OverAPIv3(t *testing.T) {
	refusal, err := os.ReadFile("../../testdata/sonarr/v5-api-v3-refusal/system-status.json")
	if err != nil {
		t.Fatal(err)
	}
	var recorded StatusDoc
	if err := json.Unmarshal(refusal, &recorded); err != nil || !strings.HasPrefix(recorded.Version, "5.") {
		t.Fatalf("the refusal fixture does not name a Sonarr v5 (API v5): %+v, %v", recorded, err)
	}
	f := newFake()
	f.on("GET /api", ans(404, "", ""))
	f.on("GET /api/v5/system/status", ans(404, "", ""))
	f.on("GET /api/v3/system/status", ans(200, recorded.Version, string(refusal)))
	c := newClient(t, f, transport.Config{})
	d, err := run(t, c, AppSonarr)
	if d != nil {
		t.Fatalf("walkArms chose an engine for Sonarr v5 (API v5) over the /api/v3 refusal path: %+v", d)
	}
	e := wantUnsupported(t, err, arrapi.ReasonV5Unreachable)
	if e.App != "Sonarr" || e.Version != recorded.Version || e.Path != "/api/v3/system/status" || e.Current != "" {
		t.Errorf("error fields = %+v", e)
	}
	if !strings.Contains(e.Error(), "arrapi reads Sonarr 5 and later only over /api/v5") {
		t.Errorf("Error() = %q", e.Error())
	}
	wantRequests(t, f, "GET /api", "GET /api/v5/system/status", "GET /api/v3/system/status")
}

func TestDetectRefusesAnnouncedAPIv5Blocked(t *testing.T) {
	f := newFake()
	serveSonarr5(f, v5)
	f.on("GET /api/v5/system/status", ans(404, "", "blocked"))
	_, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
	e := wantUnsupported(t, err, arrapi.ReasonV5Unreachable)
	if e.Current != "v5" || !slices.Equal(e.Deprecated, []string{"v3"}) || e.Path != "/api/v5/system/status" {
		t.Errorf("error fields = %+v", e)
	}
	wantRequests(t, f, "GET /api", "GET /api/v5/system/status")
}

func TestDetectProxyHidesAPI(t *testing.T) {
	hidden := map[string]step{
		"404 without the header": ans(404, "", "not found"),
		"a landing page":         ans(200, "", "<!doctype html><title>proxy</title>"),
		"JSON without current":   ans(200, v4, `{"deprecated":[]}`),
	}
	for name, api := range hidden {
		t.Run(name+" then Sonarr v4 (API v3)", func(t *testing.T) {
			f := newFake()
			serveSonarr4(f, v4)
			f.on("GET /api", api)
			d, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
			if err != nil {
				t.Fatalf("walkArms: %v", err)
			}
			if d.API != arrapi.APIv3 || d.Identity.AppVersion != v4 {
				t.Errorf("detection = %+v", d)
			}
			wantRequests(t, f, "GET /api", "GET /api/v5/system/status", "GET /api/v3/system/status")
		})
	}
	t.Run("hidden then Sonarr v5 (API v5)", func(t *testing.T) {
		f := newFake()
		serveSonarr5(f, v5)
		f.on("GET /api", ans(404, "", ""))
		d, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
		if err != nil {
			t.Fatalf("walkArms: %v", err)
		}
		if d.API != arrapi.APIv5 || d.Deprecated {
			t.Errorf("detection = %+v", d)
		}
		wantRequests(t, f, "GET /api", "GET /api/v5/system/status")
	})
}

func TestDetectOversizedAPIBodyIsHidden(t *testing.T) {
	f := newFake()
	serveSonarr4(f, v4)
	f.on("GET /api", ans(200, v4, `{"current":"v3","pad":"`+strings.Repeat("x", 2<<20)+`"}`))
	d, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
	if err != nil {
		t.Fatalf("walkArms: %v", err)
	}
	if d.API != arrapi.APIv3 {
		t.Errorf("detection = %+v", d)
	}
	wantRequests(t, f, "GET /api", "GET /api/v5/system/status", "GET /api/v3/system/status")
}

func TestDetectNothingAnswersIs404(t *testing.T) {
	f := newFake()
	f.on("GET /api", ans(404, "", ""))
	f.on("GET /api/v5/system/status", ans(404, "", ""))
	f.on("GET /api/v3/system/status", ans(404, "", ""))
	_, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
	e := wantStatus(t, err, 404)
	if e.Path != "/api/v3/system/status" {
		t.Errorf("Path = %q", e.Path)
	}
	wantRequests(t, f, "GET /api", "GET /api/v5/system/status", "GET /api/v3/system/status")
}

func TestDetectAnnouncedAPIv3StatusMissingIs404(t *testing.T) {
	f := newFake()
	serveSonarr4(f, v4)
	f.on("GET /api/v3/system/status", ans(404, v4, ""))
	_, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
	wantStatus(t, err, 404)
	wantRequests(t, f, "GET /api", "GET /api/v3/system/status")
}

func TestDetectUnauthorizedStays401(t *testing.T) {
	t.Run("401 on the api path", func(t *testing.T) {
		f := newFake()
		f.on("GET /api", ans(401, "", ""))
		_, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
		wantStatus(t, err, 401)
		wantRequests(t, f, "GET /api")
	})
	t.Run("403 on the Sonarr v5 (API v5) status", func(t *testing.T) {
		f := newFake()
		serveSonarr5(f, v5)
		f.on("GET /api/v5/system/status", ans(403, "", ""))
		_, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
		wantStatus(t, err, 403)
		wantRequests(t, f, "GET /api", "GET /api/v5/system/status")
	})
}

func TestDetectUnexpectedSuccessCodeIsStatus(t *testing.T) {
	t.Run("204 to the api path", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		f.on("GET /api", ans(204, v4, ""))
		c := newClient(t, f, transport.Config{})
		_, err := run(t, c, AppSonarr)
		wantStatus(t, err, 204)
		wantRequests(t, f, "GET /api")
		if c.Current() != nil {
			t.Error("a detection was installed")
		}
	})
	t.Run("206 to the status path", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		f.on("GET /api/v3/system/status", ans(206, v4, statusBody("Sonarr", v4)))
		_, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
		wantStatus(t, err, 206)
		wantRequests(t, f, "GET /api", "GET /api/v3/system/status")
	})
}

func TestDetectWrongApp(t *testing.T) {
	t.Run("Radarr answering the Sonarr client", func(t *testing.T) {
		f := newFake()
		serveRadarr(f, vR)
		_, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
		e := wantUnsupported(t, err, arrapi.ReasonWrongApp)
		if e.App != "Radarr" || e.Want != AppSonarr || e.Path != "/api/v3/system/status" {
			t.Errorf("error fields = %+v", e)
		}
	})
	t.Run("Sonarr v4 (API v3) answering the Radarr client", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		_, err := run(t, newClient(t, f, transport.Config{}), AppRadarr)
		e := wantUnsupported(t, err, arrapi.ReasonWrongApp)
		if e.App != "Sonarr" || e.Want != AppRadarr {
			t.Errorf("error fields = %+v", e)
		}
	})
	t.Run("an appName equal only under Unicode folding", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		f.on("GET /api/v3/system/status", ans(200, v4, statusBody("\u017fonarr", v4)))
		_, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
		wantUnsupported(t, err, arrapi.ReasonWrongApp)
	})
	t.Run("the Radarr client against Sonarr v5 (API v5) reads no /api/v3 path", func(t *testing.T) {
		f := newFake()
		serveSonarr5(f, v5)
		_, err := run(t, newClient(t, f, transport.Config{}), AppRadarr)
		wantUnsupported(t, err, arrapi.ReasonWrongApp)
		wantRequests(t, f, "GET /api", "GET /api/v5/system/status")
	})
}

func TestDetectUnknownGeneration(t *testing.T) {
	t.Run("Sonarr v6 announcing only API v6", func(t *testing.T) {
		f := newFake()
		f.on("GET /api", ans(200, "6.0.0.1", apiBodyNoDeprecated("v6")))
		f.on("GET /api/v6/system/status", ans(200, "6.0.0.1", statusBody("Sonarr", "6.0.0.1")))
		_, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
		e := wantUnsupported(t, err, arrapi.ReasonUnknownGeneration)
		if e.Current != "v6" || e.App != "Sonarr" || e.Version != "6.0.0.1" {
			t.Errorf("error fields = %+v", e)
		}
		wantRequests(t, f, "GET /api", "GET /api/v6/system/status")
	})
	t.Run("Radarr announcing only API v4", func(t *testing.T) {
		f := newFake()
		f.on("GET /api", ans(200, "7.0.0.1", apiBodyNoDeprecated("v4")))
		f.on("GET /api/v4/system/status", ans(200, "7.0.0.1", statusBody("Radarr", "7.0.0.1")))
		_, err := run(t, newClient(t, f, transport.Config{}), AppRadarr)
		wantUnsupported(t, err, arrapi.ReasonUnknownGeneration)
	})
}

func TestDetectSonarr3Unsupported(t *testing.T) {
	t.Run("api readable", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, "3.0.10.1567")
		d, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
		if d != nil {
			t.Fatalf("walkArms chose an engine for Sonarr v3 (unsupported): %+v", d)
		}
		wantUnsupported(t, err, arrapi.ReasonUnsupportedVersion)
	})
	t.Run("arm D' with the api path hidden", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, "3.0.10.1567")
		f.on("GET /api", ans(404, "", ""))
		f.on("GET /api/v5/system/status", ans(404, "", ""))
		d, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
		if d != nil {
			t.Fatalf("walkArms chose an engine for Sonarr v3 (unsupported): %+v", d)
		}
		wantUnsupported(t, err, arrapi.ReasonUnsupportedVersion)
		wantRequests(t, f, "GET /api", "GET /api/v5/system/status", "GET /api/v3/system/status")
	})
}

func TestDetectFutureSonarrKeepsV5Deprecated(t *testing.T) {
	f := newFake()
	f.on("GET /api", ans(200, "6.0.0.1", apiBody("v6", "v5")))
	f.on("GET /api/v6/system/status", ans(200, "6.0.0.1", statusBody("Sonarr", "6.0.0.1")))
	d, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
	if err != nil {
		t.Fatalf("walkArms: %v", err)
	}
	if d.API != arrapi.APIv5 || !d.Deprecated || d.Identity.AppVersion != "6.0.0.1" {
		t.Errorf("detection = %+v", d)
	}
}

func TestDetectWrongBasePathSPAFallback(t *testing.T) {
	f := newFake()
	page := ans(200, "", "<!doctype html><html><body>Sonarr</body></html>")
	for _, p := range []string{"GET /api", "GET /api/v5/system/status", "GET /api/v3/system/status"} {
		f.on(p, page)
	}
	_, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
	e := wantUnsupported(t, err, arrapi.ReasonNotAPI)
	if e.Want != AppSonarr || e.Path != "/api/v5/system/status" {
		t.Errorf("error fields = %+v", e)
	}
	wantRequests(t, f, "GET /api", "GET /api/v5/system/status")
}

func TestDetectNotAnArr(t *testing.T) {
	for name, body := range map[string]string{
		"JSON without appName":  `{"version":"4.0.20.3014"}`,
		"JSON without version":  `{"appName":"Sonarr"}`,
		"appName not a string":  `{"appName":7,"version":"4.0"}`,
		"over the probe cap":    `{"appName":"Sonarr","version":"` + strings.Repeat("9", 1<<20) + `"}`,
		"a JSON array, not one": `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			serveSonarr4(f, v4)
			f.on("GET /api/v3/system/status", ans(200, v4, body))
			_, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
			wantUnsupported(t, err, arrapi.ReasonNotAPI)
		})
	}
}

func TestDetectNoVersionHeaderRefused(t *testing.T) {
	for _, app := range []string{AppSonarr, AppRadarr} {
		for name, header := range map[string]string{"stripped": "", "malformed": "abc"} {
			t.Run(app+" "+name, func(t *testing.T) {
				f := newFake()
				f.on("GET /api", ans(200, header, apiBodyNoDeprecated("v3")))
				f.on("GET /api/v3/system/status", ans(200, header, statusBody(app, v4)))
				d, err := run(t, newClient(t, f, transport.Config{}), app)
				if d != nil {
					t.Fatalf("walkArms chose an engine without a version header: %+v", d)
				}
				e := wantUnsupported(t, err, arrapi.ReasonNoVersionHeader)
				if e.App != app || e.Want != app || e.Path != "/api/v3/system/status" {
					t.Errorf("error fields = %+v", e)
				}
			})
		}
	}
}

func TestDetectMalformedGenerationTokens(t *testing.T) {
	t.Run("a malformed current hides the api path", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		f.on("GET /api", ans(200, v4, apiBodyNoDeprecated("../x")))
		if _, err := run(t, newClient(t, f, transport.Config{}), AppSonarr); err != nil {
			t.Fatalf("walkArms: %v", err)
		}
		wantRequests(t, f, "GET /api", "GET /api/v5/system/status", "GET /api/v3/system/status")
	})
	t.Run("malformed deprecated entries are ignored", func(t *testing.T) {
		f := newFake()
		f.on("GET /api", ans(200, "6.0.0.1", `{"current":"v6","deprecated":["../x","v05",3,"v5 ","v7"]}`))
		f.on("GET /api/v6/system/status", ans(200, "6.0.0.1", statusBody("Sonarr", "6.0.0.1")))
		_, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
		e := wantUnsupported(t, err, arrapi.ReasonUnknownGeneration)
		if !slices.Equal(e.Deprecated, []string{"v7"}) {
			t.Errorf("Deprecated = %q, want [v7]", e.Deprecated)
		}
	})
}

func TestDetectPresentationIsBounded(t *testing.T) {
	hostile := "Ra\r\ndarr\u202e" + strings.Repeat("\xff", 300) + strings.Repeat("y", 400<<10)
	bounded := func(t *testing.T, what, s string) {
		t.Helper()
		if len(s) > 64 || strings.ContainsAny(s, "\r\n\u202e") || !utf8.ValidString(s) {
			t.Errorf("%s = %q (%d bytes), want single-line, valid and at most 64 bytes", what, s, len(s))
		}
	}
	t.Run("an appName reaching ReasonWrongApp", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		f.on("GET /api/v3/system/status", ans(200, v4, `{"appName":"`+jsonEscape(hostile)+`","version":"`+jsonEscape(hostile)+`"}`))
		_, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
		e := wantUnsupported(t, err, arrapi.ReasonWrongApp)
		bounded(t, "App", e.App)
		bounded(t, "Version", e.Version)
	})
	t.Run("a version reaching the detection keeps the engine", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		f.on("GET /api/v3/system/status", ans(200, v4, `{"appName":"Sonarr","version":"`+jsonEscape(hostile)+`"}`))
		d, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
		if err != nil {
			t.Fatalf("walkArms: %v", err)
		}
		bounded(t, "Detection.Version", d.Version)
		if d.API != arrapi.APIv3 || d.Identity.AppVersion != v4 {
			t.Errorf("engine = %q at %q, want the clean value's Sonarr v4 (API v3) at %s", d.API, d.Identity.AppVersion, v4)
		}
	})
	t.Run("a deprecated list keeps at most 8 tokens", func(t *testing.T) {
		toks := make([]string, 20)
		for i := range toks {
			toks[i] = fmt.Sprintf("v%d", 10+i)
		}
		f := newFake()
		f.on("GET /api", ans(200, "6.0.0.1", apiBody("v6", toks...)))
		f.on("GET /api/v6/system/status", ans(200, "6.0.0.1", statusBody("Sonarr", jsonEscape(hostile))))
		_, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
		e := wantUnsupported(t, err, arrapi.ReasonUnknownGeneration)
		if len(e.Deprecated) != 8 {
			t.Errorf("len(Deprecated) = %d, want 8", len(e.Deprecated))
		}
		bounded(t, "Version", e.Version)
		bounded(t, "Current", e.Current)
	})
}

// jsonEscape writes s as the inside of a JSON string, invalid bytes kept raw.
func jsonEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", `\r`, "\n", `\n`).Replace(s)
}

func TestDetectAnchorCheck(t *testing.T) {
	t.Run("api and status from two versions", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		f.on("GET /api/v3/system/status", ans(200, "5.0.0.1", statusBody("Sonarr", "5.0.0.1")))
		_, err := run(t, newClient(t, f, transport.Config{}), AppSonarr)
		e, ok := errors.AsType[*arrapi.ServerChangedError](err)
		if !ok || e.From != v4 || e.To != "5.0.0.1" {
			t.Fatalf("error = %v, want ServerChangedError from %s to 5.0.0.1", err, v4)
		}
	})
	t.Run("a store between the probes fails the end check", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		c := newClient(t, f, transport.Config{})
		other := installed(t, c, f, AppSonarr)
		f.on("GET /api/v3/system/status", then(func() {
			f.on("GET /api/v3/tag", ans(500, "", "boom"))
			var out []any
			c.GetJSON(t.Context(), other, "/api/v3/tag", transport.MaxListBytes, &out)
		}, ans(200, v4, statusBody("Sonarr", v4))))
		_, err := run(t, c, AppSonarr)
		e, ok := errors.AsType[*arrapi.ServerChangedError](err)
		if !ok || e.From != v4 || e.To != "" {
			t.Fatalf("error = %v, want ServerChangedError from %s with To \"\"", err, v4)
		}
		if !strings.Contains(e.Error(), "is no longer confirmed") {
			t.Errorf("Error() = %q, want the no-longer-confirmed form", e.Error())
		}
	})
	t.Run("a contradiction after the anchor keeps the status probe unsent", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		c := newClient(t, f, transport.Config{})
		other := installed(t, c, f, AppSonarr)
		f.on("GET /api/v3/tag", ans(500, "", ""))
		f.beforeDecode("GET /api", func() {
			var out []any
			c.GetJSON(t.Context(), other, "/api/v3/tag", transport.MaxListBytes, &out)
		})
		_, err := run(t, c, AppSonarr)
		if e, ok := errors.AsType[*arrapi.ServerChangedError](err); !ok || e.From != v4 || e.To != "" {
			t.Fatalf("error = %v, want ServerChangedError from %s with To \"\"", err, v4)
		}
		wantRequests(t, f, "GET /api", "GET /api/v3/tag")
	})
	t.Run("a hidden api path anchors at the status answer", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		f.on("GET /api", ans(404, "", ""))
		f.on("GET /api/v5/system/status", ans(404, "", ""))
		c := newClient(t, f, transport.Config{})
		d, err := run(t, c, AppSonarr)
		if err != nil {
			t.Fatalf("walkArms: %v", err)
		}
		if d.Epoch != c.State().Epoch || d.Epoch == 0 {
			t.Errorf("detection epoch %d, State epoch %d", d.Epoch, c.State().Epoch)
		}
	})
}

func TestDetectTransportFailureIsArmK(t *testing.T) {
	f := newFake()
	f.on("GET /api", refused)
	c := newClient(t, f, transport.Config{MaxAttempts: 2})
	before := c.State()
	_, err := run(t, c, AppSonarr)
	if err == nil || !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("error = %v, want the transport error", err)
	}
	wantRequests(t, f, "GET /api", "GET /api")
	if c.State() != before {
		t.Error("a run that failed before any answer changed the State")
	}
}

// refused fails like a dial nothing accepted, a transient transport error.
func refused(http.ResponseWriter, *http.Request) error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
}

// TestDetectServerTextNeverCarriesTheKey echoes the client's key in each server
// string detection presents. Identity.AppVersion is the raw value answers are
// compared with, never presented, so it is outside this test.
func TestDetectServerTextNeverCarriesTheKey(t *testing.T) {
	type tcase struct {
		name  string
		key   arrapi.APIKey
		serve func(f *fakeServer, key string)
	}
	tests := []tcase{
		{"in appName", "sk-a91f-echo", func(f *fakeServer, key string) {
			serveSonarr4(f, v4)
			f.on("GET /api/v3/system/status", ans(200, v4, statusBody(key, v4)))
		}},
		{"in version", "sk-b72e-echo", func(f *fakeServer, key string) {
			serveSonarr4(f, "3.0.10.1567")
			f.on("GET /api/v3/system/status", ans(200, "3.0.10.1567", statusBody("Sonarr", key)))
		}},
		{"in version of a detection", "sk-c13d-echo", func(f *fakeServer, key string) {
			serveSonarr4(f, v4)
			f.on("GET /api/v3/system/status", ans(200, v4, statusBody("Sonarr", "x"+key)))
		}},
		{"in current", "v77", func(f *fakeServer, key string) {
			f.on("GET /api", ans(200, "6.0.0.1", apiBodyNoDeprecated(key)))
			f.on("GET /api/"+key+"/system/status", ans(200, "6.0.0.1", statusBody("Sonarr", "6.0.0.1")))
		}},
		{"in current, the status answer not the API", "v79", func(f *fakeServer, key string) {
			f.on("GET /api", ans(200, "6.0.0.1", apiBodyNoDeprecated(key)))
			f.on("GET /api/"+key+"/system/status", ans(200, "6.0.0.1", "<html></html>"))
		}},
		{"in current, the status answer without the header", "v80", func(f *fakeServer, key string) {
			f.on("GET /api", ans(200, "6.0.0.1", apiBodyNoDeprecated(key)))
			f.on("GET /api/"+key+"/system/status", ans(200, "", statusBody("Sonarr", "6.0.0.1")))
		}},
		{"in current, the status answer a 401", "v81", func(f *fakeServer, key string) {
			f.on("GET /api", ans(200, "6.0.0.1", apiBodyNoDeprecated(key)))
			f.on("GET /api/"+key+"/system/status", ans(401, "", "unauthorized"))
		}},
		{"in current, the status answer a 500 with the header", "v82", func(f *fakeServer, key string) {
			f.on("GET /api", ans(200, "6.0.0.1", apiBodyNoDeprecated(key)))
			f.on("GET /api/"+key+"/system/status", ans(500, "6.0.0.1", "boom"))
		}},
		{"in current, the status answer from another version", "v83", func(f *fakeServer, key string) {
			f.on("GET /api", ans(200, "6.0.0.1", apiBodyNoDeprecated(key)))
			f.on("GET /api/"+key+"/system/status", ans(200, "7.0.0.1", statusBody("Sonarr", "7.0.0.1")))
		}},
		{"in current, the status answer from another version failing", "v84", func(f *fakeServer, key string) {
			f.on("GET /api", ans(200, "6.0.0.1", apiBodyNoDeprecated(key)))
			f.on("GET /api/"+key+"/system/status", ans(503, "7.0.0.1", "down"))
		}},
		{"in deprecated", "v78", func(f *fakeServer, key string) {
			f.on("GET /api", ans(200, "6.0.0.1", apiBody("v6", key)))
			f.on("GET /api/v6/system/status", ans(200, "6.0.0.1", statusBody("Sonarr", "6.0.0.1")))
		}},
		{"as X-Application-Version", "7.7.7.7", func(f *fakeServer, key string) {
			serveSonarr4(f, v4)
			f.on("GET /api", ans(200, key, apiBodyNoDeprecated("v3")))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key := string(tc.key)
			f := newFake()
			tc.serve(f, key)
			rec := &recorder{}
			c := newClientKey(t, f, tc.key, transport.Config{Logger: slog.New(rec)})
			d, err := run(t, c, AppSonarr)
			if err == nil {
				if d.Version == "" || strings.Contains(d.Version, key) {
					t.Errorf("Detection.Version = %q holds the key", d.Version)
				}
			}
			for e := err; e != nil; e = errors.Unwrap(e) {
				if strings.Contains(e.Error(), key) || strings.Contains(fmt.Sprintf("%+v", e), key) {
					t.Errorf("%T renders the key: %q", e, e.Error())
				}
			}
			if se, ok := errors.AsType[*arrapi.StatusError](err); ok && strings.Contains(se.Path, key) {
				t.Errorf("StatusError.Path = %q holds the key", se.Path)
			}
			if ce, ok := errors.AsType[*arrapi.ServerChangedError](err); ok && strings.Contains(ce.Path, key) {
				t.Errorf("ServerChangedError.Path = %q holds the key", ce.Path)
			}
			if p := c.State().Path; strings.Contains(p, key) {
				t.Errorf("State.Path = %q holds the key", p)
			}
			if u, ok := errors.AsType[*arrapi.UnsupportedAPIError](err); ok {
				for _, s := range append([]string{u.App, u.Version, u.Current, u.Path}, u.Deprecated...) {
					if strings.Contains(s, key) {
						t.Errorf("UnsupportedAPIError field %q holds the key", s)
					}
				}
			}
			if strings.Contains(rec.text(), key) {
				t.Errorf("a log record holds the key:\n%s", rec.text())
			}
		})
	}
}

func TestFailedDetectionAfterAnswerMarksUnknown(t *testing.T) {
	timeout := func(w http.ResponseWriter, r *http.Request) error {
		<-r.Context().Done()
		return r.Context().Err()
	}
	arms := []struct {
		serve func(f *fakeServer)
		name  string
	}{
		{name: "A' Sonarr v5 (API v5) announced, /api/v5 blocked", serve: func(f *fakeServer) {
			serveSonarr5(f, v5)
			f.on("GET /api/v5/system/status", ans(404, "", ""))
		}},
		{name: "F Sonarr v5 (API v5) over the /api/v3 refusal path", serve: func(f *fakeServer) {
			serveSonarr5(f, "5.0.0.1")
			f.on("GET /api", ans(404, "", ""))
			f.on("GET /api/v5/system/status", ans(404, "", ""))
		}},
		{name: "G a 5xx with the header", serve: func(f *fakeServer) {
			serveSonarr4(f, v4)
			f.on("GET /api/v3/system/status", ans(500, v4, ""))
		}},
		{name: "G a header-less 401", serve: func(f *fakeServer) {
			serveSonarr4(f, v4)
			f.on("GET /api/v3/system/status", ans(401, "", ""))
		}},
		{name: "H", serve: func(f *fakeServer) {
			serveSonarr4(f, v4)
			f.on("GET /api/v3/system/status", ans(404, v4, ""))
		}},
		{name: "I Radarr", serve: func(f *fakeServer) { serveRadarr(f, "4.6.4.8481") }},
		{name: "I' not the API", serve: func(f *fakeServer) {
			serveSonarr4(f, v4)
			f.on("GET /api/v3/system/status", ans(200, v4, "<html>"))
		}},
		{name: "J at another version", serve: func(f *fakeServer) {
			f.on("GET /api", ans(200, "6.0.0.1", apiBodyNoDeprecated("v6")))
			f.on("GET /api/v6/system/status", ans(200, "6.0.0.1", statusBody("Sonarr", "6.0.0.1")))
		}},
		{name: "J at the installed version", serve: func(f *fakeServer) {
			f.on("GET /api", ans(200, v4, apiBodyNoDeprecated("v6")))
			f.on("GET /api/v6/system/status", ans(200, v4, statusBody("Sonarr", v4)))
		}},
		{name: "V", serve: func(f *fakeServer) {
			serveSonarr4(f, v4)
			f.on("GET /api/v3/system/status", ans(200, "", statusBody("Sonarr", v4)))
		}},
		{name: "W", serve: func(f *fakeServer) {
			serveSonarr4(f, v4)
			f.on("GET /api/v3/system/status", ans(200, "5.0.0.1", statusBody("Sonarr", "5.0.0.1")))
		}},
		{name: "K after the api path answered", serve: func(f *fakeServer) {
			serveSonarr4(f, v4)
			f.on("GET /api/v3/system/status", timeout)
		}},
	}
	for _, via := range []string{"Info", "Mutate"} {
		for _, arm := range arms {
			t.Run(via+" "+arm.name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					f := newFake()
					serveSonarr4(f, v4)
					c := newClient(t, f, transport.Config{Timeout: 20 * time.Millisecond})
					s := newSession(c)
					old := installed(t, c, f, AppSonarr)
					arm.serve(f)
					var err error
					if via == "Info" {
						_, err = s.Info(t.Context())
					} else {
						err = s.Mutate(t.Context(), rescan)
					}
					if err == nil {
						t.Fatal("detection succeeded")
					}
					if c.Current() != nil {
						t.Fatal("a usable detection is left after a failed detection that received an answer")
					}
					for _, r := range f.requests() {
						if strings.HasPrefix(r, "POST ") {
							t.Errorf("a command was sent after a failed detection: %q", r)
						}
					}
					var out []any
					res := c.GetJSON(t.Context(), old, series, transport.MaxListBytes, &out)
					sce, ok := errors.AsType[*arrapi.ServerChangedError](res.Err)
					if res.Verdict != transport.VerdictRefused || !ok {
						t.Fatalf("a read on the old detection = (%v, %v), want refused", res.Verdict, res.Err)
					}
					if sce.To != "" || !strings.Contains(sce.Error(), base+": version "+v4+" is no longer confirmed") ||
						strings.Contains(sce.Error(), "/api") {
						t.Errorf("refusal = %+v (%q), want To \"\" naming the origin and no path", sce, sce.Error())
					}
					serveSonarr4(f, v4)
					f.clearLog()
					if err := s.Read(t.Context(), readSeries(&out)); err != nil {
						t.Fatalf("next Read: %v", err)
					}
					if got := f.requests(); len(got) == 0 || got[0] != "GET /api" {
						t.Errorf("next Read sent %q, want GET /api first", got)
					}
				})
			})
		}
	}
	t.Run("K before any answer leaves the detection", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		c := newClient(t, f, transport.Config{})
		s := newSession(c)
		d := installed(t, c, f, AppSonarr)
		f.on("GET /api", refused)
		if _, err := s.Info(t.Context()); err == nil {
			t.Fatal("Info succeeded")
		}
		if c.Current() != d {
			t.Fatal("a detection that failed before any answer dropped the installed one")
		}
		f.clearLog()
		var out []any
		if err := s.Read(t.Context(), readSeries(&out)); err != nil {
			t.Fatalf("Read: %v", err)
		}
		wantRequests(t, f, "GET "+series)
	})
}

// TestUnanchoredRunPendingWindow holds a rescan's detection across header-less /api
// answers: reads on the cached Sonarr 4 detection still leave in that window, are
// answered by Sonarr 5 and never decoded, none leaves once the run identified the
// server or failed, and the run's own probes stop once another answer contradicts
// its anchor.
func TestUnanchoredRunPendingWindow(t *testing.T) {
	stale := func(t *testing.T, c *transport.Client, d *transport.Detection) transport.Result {
		t.Helper()
		out := []any{"untouched"}
		res := c.GetJSON(t.Context(), d, series, transport.MaxListBytes, &out)
		if len(out) != 1 || out[0] != "untouched" {
			t.Errorf("a stale read was decoded: %v", out)
		}
		return res
	}
	t.Run("the run identifies Sonarr v5 (API v5)", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		c := newClient(t, f, transport.Config{})
		s := newSession(c)
		d := installed(t, c, f, AppSonarr)
		serveSonarr5(f, v5)
		f.on("GET "+series, ans(200, v5, `{"not":"a list"}`))
		hold, release := releaser(t)
		second := make(chan struct{})
		f.on("GET /api",
			ans(503, "", ""),
			then(func() { close(second) }, held(hold, ans(503, "", ""))),
			ans(200, v5, apiBody("v5", "v3")))
		done := make(chan error, 1)
		go func() { done <- s.Mutate(t.Context(), rescan) }()
		<-second
		if res := stale(t, c, d); res.Verdict != transport.VerdictChanged || !res.Sent {
			t.Errorf("read after a header-less probe answer = (%v, sent %v), want sent and VerdictChanged", res.Verdict, res.Sent)
		}
		release()
		if err := <-done; err != nil {
			t.Fatalf("RescanSeries: %v", err)
		}
		if res := stale(t, c, d); res.Verdict != transport.VerdictRefused || res.Sent {
			t.Errorf("read after the run identified the server = (%v, sent %v), want refused unsent", res.Verdict, res.Sent)
		}
		wantRequests(t, f, "GET /api", "GET /api", "GET "+series, "GET /api", "GET /api/v5/system/status", "POST /api/v5/command")
	})
	t.Run("the run fails on header-less answers alone", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		c := newClient(t, f, transport.Config{})
		s := newSession(c)
		d := installed(t, c, f, AppSonarr)
		f.on("GET /api", ans(503, "", ""))
		f.on("GET "+series, ans(200, v4, "[]"))
		err := s.Mutate(t.Context(), rescan)
		wantStatus(t, err, 503)
		if res := stale(t, c, d); res.Verdict != transport.VerdictRefused || res.Sent {
			t.Errorf("read after the failed run = (%v, sent %v), want refused unsent", res.Verdict, res.Sent)
		}
		wantRequests(t, f, "GET /api", "GET /api", "GET /api")
	})
	t.Run("an answer after the run anchored refuses its redirect hop", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		hold, release := releaser(t)
		atSeries := make(chan struct{})
		staleDone := make(chan transport.Result, 1)
		var staleRes transport.Result
		once := sync.OnceFunc(func() {
			release()
			staleRes = <-staleDone
		})
		// The hook runs after the observer saw the run's anchoring 302 and before the
		// hop's send check.
		hook := func(*http.Request, []*http.Request) error {
			once()
			return nil
		}
		c := newClient(t, f, transport.Config{HTTPClient: &http.Client{Transport: f, CheckRedirect: hook}})
		s := newSession(c)
		d := installed(t, c, f, AppSonarr)
		serveSonarr5(f, v5)
		f.on("GET "+series, then(func() { close(atSeries) }, held(hold, ans(404, "", ""))))
		f.on("GET /api", func(w http.ResponseWriter, _ *http.Request) error {
			w.Header().Set("X-Application-Version", v5)
			w.Header().Set("Location", "/api/")
			w.WriteHeader(http.StatusFound)
			return nil
		})
		f.on("GET /api/", ans(200, v5, apiBody("v5", "v3")))
		go func() {
			var out []any
			staleDone <- c.GetJSON(t.Context(), d, series, transport.MaxListBytes, &out)
		}()
		<-atSeries
		err := s.Mutate(t.Context(), rescan)
		if _, ok := errors.AsType[*arrapi.ServerChangedError](err); !ok {
			t.Errorf("Mutate = %v, want *arrapi.ServerChangedError", err)
		}
		if staleRes.Verdict != transport.VerdictUnidentified {
			t.Errorf("the stale read's header-less 404 = (%v, %v), want VerdictUnidentified", staleRes.Verdict, staleRes.Err)
		}
		wantRequests(t, f, "GET "+series, "GET /api")
	})
}

func TestInfoBehindHiddenAPICostsNothing(t *testing.T) {
	f := newFake()
	serveSonarr4(f, v4)
	c := newClient(t, f, transport.Config{})
	s := newSession(c)
	d := installed(t, c, f, AppSonarr)
	f.on("GET /api", ans(404, "", ""))
	if _, err := s.Info(t.Context()); err != nil {
		t.Fatalf("Info: %v", err)
	}
	wantRequests(t, f, "GET /api", "GET /api/v5/system/status", "GET /api/v3/system/status")
	if c.Current() == nil || c.Current().Epoch != d.Epoch {
		t.Fatalf("Info behind a hidden api path left no usable detection: %+v", c.Current())
	}
	f.clearLog()
	var out []any
	if err := s.Read(t.Context(), readSeries(&out)); err != nil {
		t.Fatalf("Read: %v", err)
	}
	wantRequests(t, f, "GET "+series)
}

// releaser returns a channel a held step waits on and its idempotent release, which
// also runs at cleanup so a failed check never leaves the step blocked.
func releaser(t *testing.T) (<-chan struct{}, func()) {
	ch := make(chan struct{})
	release := sync.OnceFunc(func() { close(ch) })
	t.Cleanup(release)
	return ch, release
}
