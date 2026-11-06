package sonarr

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/fixturegen"
	"github.com/cplieger/go/httpclient"
)

// checkVia runs Check against base with key, sending through f in memory when f is set.
func checkVia(t *testing.T, f *fake, base string, key arrapi.APIKey) (Info, error) {
	t.Helper()
	opts := []Option{WithLogger(slog.New(slog.DiscardHandler))}
	if f != nil {
		opts = append(opts, WithHTTPClient(&http.Client{Transport: f}))
	}
	return Check(t.Context(), base, key, opts...)
}

// wantCheck returns err as an *arrapi.CheckError with reason, failing the test otherwise.
func wantCheck(t *testing.T, err error, reason arrapi.CheckReason) *arrapi.CheckError {
	t.Helper()
	ce, ok := errors.AsType[*arrapi.CheckError](err)
	if !ok {
		t.Fatalf("err = %v, want *arrapi.CheckError", err)
	}
	if ce.Reason != reason {
		t.Fatalf("Reason = %d (%v), want %d", ce.Reason, err, reason)
	}
	return ce
}

func wantNoStatus(t *testing.T, err error) {
	t.Helper()
	if se, ok := errors.AsType[*arrapi.StatusError](err); ok {
		t.Errorf("errors.As(*StatusError) = %v, want none", se)
	}
}

// typed answers 200 with content type ct ("" for none, so nothing is sniffed) and body.
func typed(v, ct, body string) step {
	return func(w http.ResponseWriter, _ *http.Request) error {
		if v != "" {
			w.Header().Set("X-Application-Version", v)
		}
		if ct == "" {
			w.Header()["Content-Type"] = nil
		} else {
			w.Header().Set("Content-Type", ct)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
		return nil
	}
}

// everywhere routes every detection path of f to s.
func everywhere(f *fake, s step) *fake {
	f.reset("")
	for _, p := range []string{"GET /api", "GET /api/v5/system/status", "GET /api/v3/system/status"} {
		f.on(p, s)
	}
	return f
}

func TestCheckKey(t *testing.T) {
	t.Run("api answers 401", func(t *testing.T) {
		f := newFake().sonarr4(v4)
		f.on("GET /api", ans(401, "", ""))
		_, err := checkVia(t, f, fakeBase, testKey)
		wantCheck(t, err, arrapi.CheckKey)
		wantStatus(t, err, 401)
	})
	t.Run("a status path answers 403", func(t *testing.T) {
		f := newFake().sonarr4(v4)
		f.on("GET /api/v3/system/status", ans(403, "", ""))
		_, err := checkVia(t, f, fakeBase, testKey)
		wantCheck(t, err, arrapi.CheckKey)
		wantStatus(t, err, 403)
	})
	for _, tc := range []struct {
		name string
		key  arrapi.APIKey
		text string
	}{
		{"an empty key", "", "arrapi: key is empty"},
		{"a key outside the alphabet", "a+b", "arrapi: key may hold only letters, digits, '.', '_' and '-'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake().sonarr4(v4)
			_, err := checkVia(t, f, fakeBase, tc.key)
			wantCheck(t, err, arrapi.CheckKey)
			if err.Error() != tc.text {
				t.Errorf("Error() = %q, want %q", err.Error(), tc.text)
			}
			if got := f.requests(); len(got) != 0 {
				t.Errorf("requests = %q, want none", got)
			}
		})
	}
}

func TestCheckBasePath(t *testing.T) {
	f := everywhere(newFake(), ans(404, "", ""))
	_, err := checkVia(t, f, fakeBase, testKey)
	wantCheck(t, err, arrapi.CheckBasePath)
	wantStatus(t, err, 404)
}

func TestCheckURL(t *testing.T) {
	t.Run("a closed listener", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()
		_, err := checkVia(t, nil, srv.URL, testKey)
		wantCheck(t, err, arrapi.CheckURL)
		if !errors.Is(err, syscall.ECONNREFUSED) {
			t.Errorf("errors.Is(err, ECONNREFUSED) = false: %v", err)
		}
	})
	t.Run("an unknown host", func(t *testing.T) {
		_, err := checkVia(t, nil, "http://sonarr.invalid", testKey)
		wantCheck(t, err, arrapi.CheckURL)
		if _, ok := errors.AsType[*net.DNSError](err); !ok {
			t.Errorf("errors.As(*net.DNSError) = false: %v", err)
		}
	})
	t.Run("a certificate without its CA", func(t *testing.T) {
		srv := httptest.NewTLSServer(newFake().sonarr4(v4))
		t.Cleanup(srv.Close)
		_, err := checkVia(t, nil, srv.URL, testKey)
		wantCheck(t, err, arrapi.CheckURL)
		if _, ok := errors.AsType[*tls.CertificateVerificationError](err); !ok {
			t.Errorf("errors.As(*tls.CertificateVerificationError) = false: %v", err)
		}
	})
	t.Run("a plain request to a TLS port", func(t *testing.T) {
		srv := httptest.NewTLSServer(newFake().sonarr4(v4))
		t.Cleanup(srv.Close)
		_, err := checkVia(t, nil, "http://"+srv.Listener.Addr().String(), testKey)
		wantCheck(t, err, arrapi.CheckURL)
		wantStatus(t, err, 400)
		if !strings.Contains(err.Error(), "Client sent an HTTP request to an HTTPS server") {
			t.Errorf("Error() = %q, want the server's sentence", err.Error())
		}
	})
	t.Run("a redirect to another port", func(t *testing.T) {
		other := httptest.NewServer(newFake().sonarr4(v4))
		t.Cleanup(other.Close)
		f := newFake().sonarr4(v4)
		f.on("GET /api", redirect("", other.URL+"/api"))
		srv := httptest.NewServer(f)
		t.Cleanup(srv.Close)
		_, err := checkVia(t, nil, srv.URL, testKey)
		wantCheck(t, err, arrapi.CheckURL)
		if _, ok := errors.AsType[*httpclient.RedirectRefusedError](err); !ok {
			t.Errorf("errors.As(*httpclient.RedirectRefusedError) = false: %v", err)
		}
	})
	t.Run("a redirect to a sign-in on another origin", func(t *testing.T) {
		portal := httptest.NewServer(http.NotFoundHandler())
		t.Cleanup(portal.Close)
		f := newFake().sonarr4(v4)
		f.on("GET /api", redirect("", portal.URL+"/login?rd=/api"))
		srv := httptest.NewServer(f)
		t.Cleanup(srv.Close)
		_, err := checkVia(t, nil, srv.URL, testKey)
		wantCheck(t, err, arrapi.CheckURL)
		if _, ok := errors.AsType[*httpclient.RedirectRefusedError](err); !ok {
			t.Errorf("errors.As(*httpclient.RedirectRefusedError) = false: %v", err)
		}
		if !strings.Contains(err.Error(), portal.URL) {
			t.Errorf("Error() = %q, want it to name %s", err.Error(), portal.URL)
		}
	})
	t.Run("a redirect from http to https", func(t *testing.T) {
		secure := httptest.NewTLSServer(newFake().sonarr4(v4))
		t.Cleanup(secure.Close)
		f := newFake().sonarr4(v4)
		f.on("GET /api", redirect("", secure.URL+"/api"))
		srv := httptest.NewServer(f)
		t.Cleanup(srv.Close)
		_, err := checkVia(t, nil, srv.URL, testKey)
		wantCheck(t, err, arrapi.CheckURL)
		if want := "change the url to " + secure.URL; !strings.Contains(err.Error(), want) {
			t.Errorf("Error() = %q, want it to hold %q", err.Error(), want)
		}
	})
	for _, base := range []string{"ftp://x", "http://x/?q"} {
		t.Run("New refuses "+base, func(t *testing.T) {
			f := newFake().sonarr4(v4)
			_, err := checkVia(t, f, base, testKey)
			wantCheck(t, err, arrapi.CheckURL)
			if got := f.requests(); len(got) != 0 {
				t.Errorf("requests = %q, want none", got)
			}
		})
	}
	t.Run("a server that never answers", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			never := make(chan struct{})
			f := everywhere(newFake(), held(never, ans(200, v4, "")))
			start := time.Now()
			_, err := checkVia(t, f, fakeBase, testKey)
			wantCheck(t, err, arrapi.CheckURL)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("errors.Is(err, DeadlineExceeded) = false: %v", err)
			}
			if d := time.Since(start); d != 5*time.Second {
				t.Errorf("Check returned after %v, want 5s", d)
			}
		})
	})
}

func TestCheckWrongApp(t *testing.T) {
	f := newFake()
	f.radarr(vRadarr)
	_, err := checkVia(t, f, fakeBase, testKey)
	wantCheck(t, err, arrapi.CheckWrongApp)
	wantUnsupported(t, err, arrapi.ReasonWrongApp)
}

func TestCheckUnsupported(t *testing.T) {
	sonarr3 := newFake().sonarr4("3.0.10.1567")
	unreachable := newFake().sonarr5(v5)
	unreachable.on("GET /api/v5/system/status", ans(404, v5, ""))
	noHeader := everywhere(newFake(), ans(404, "", ""))
	noHeader.on("GET /api", ans(200, "", apiBody("v3")))
	noHeader.on("GET /api/v3/system/status", ans(200, "", statusBody("Sonarr", v4)))
	for _, tc := range []struct {
		f      *fake
		name   string
		reason arrapi.UnsupportedReason
	}{
		{sonarr3, "Sonarr 3", arrapi.ReasonUnsupportedVersion},
		{unreachable, "a Sonarr 5 whose /api/v5 answers 404", arrapi.ReasonV5Unreachable},
		{noHeader, "no version header", arrapi.ReasonNoVersionHeader},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := checkVia(t, tc.f, fakeBase, testKey)
			wantCheck(t, err, arrapi.CheckUnsupported)
			wantUnsupported(t, err, tc.reason)
		})
	}
}

func TestCheckNotArrAPI(t *testing.T) {
	const notAPIText = "arrapi: /api/v5/system/status answered with a web page or other content, not the Sonarr API: check the URL's base path"
	t.Run("a web page everywhere", func(t *testing.T) {
		f := everywhere(newFake(), typed("", "text/html; charset=utf-8", "<html><body>Sonarr</body></html>"))
		_, err := checkVia(t, f, fakeBase, testKey)
		wantCheck(t, err, arrapi.CheckNotArrAPI)
		de, ok := errors.AsType[*arrapi.DecodeError](err)
		if !ok || de.ContentType != "text/html" || de.Status != 200 {
			t.Errorf("errors.As(*DecodeError) = %+v, %v; want ContentType text/html and Status 200", de, ok)
		}
		if err.Error() != notAPIText {
			t.Errorf("Error() = %q, want %q", err.Error(), notAPIText)
		}
		wantNoStatus(t, err)
	})
	t.Run("a web page without a content type", func(t *testing.T) {
		f := everywhere(newFake(), typed("", "", "<html><body>Sonarr</body></html>"))
		_, err := checkVia(t, f, fakeBase, testKey)
		wantCheck(t, err, arrapi.CheckNotArrAPI)
		if de, ok := errors.AsType[*arrapi.DecodeError](err); !ok || de.ContentType != "" {
			t.Errorf("errors.As(*DecodeError) = %+v, %v; want no content type", de, ok)
		}
	})
	sonarr4With := func(s step) *fake {
		f := newFake().sonarr4(v4)
		f.on("GET /api/v3/system/status", s)
		return f
	}
	t.Run("JSON of another shape", func(t *testing.T) {
		_, err := checkVia(t, sonarr4With(typed(v4, "application/json", "[1]")), fakeBase, testKey)
		wantCheck(t, err, arrapi.CheckNotArrAPI)
		if _, ok := errors.AsType[*arrapi.DecodeError](err); !ok {
			t.Errorf("errors.As(*DecodeError) = false: %v", err)
		}
	})
	t.Run("a status without appName", func(t *testing.T) {
		_, err := checkVia(t, sonarr4With(typed(v4, "application/json", "{}")), fakeBase, testKey)
		wantCheck(t, err, arrapi.CheckNotArrAPI)
		ue, ok := errors.AsType[*arrapi.UnsupportedAPIError](err)
		if !ok || ue.Reason != arrapi.ReasonNotAPI || ue.Err != nil {
			t.Errorf("errors.As(*UnsupportedAPIError) = %+v, %v; want ReasonNotAPI with a nil Err", ue, ok)
		}
	})
	t.Run("a status over the cap", func(t *testing.T) {
		big := `{"appName":"Sonarr","pad":"` + strings.Repeat("x", 2<<20) + `"}`
		_, err := checkVia(t, sonarr4With(typed(v4, "application/json", big)), fakeBase, testKey)
		wantCheck(t, err, arrapi.CheckNotArrAPI)
		if _, ok := errors.AsType[*arrapi.ResponseTooLargeError](err); !ok {
			t.Errorf("errors.As(*ResponseTooLargeError) = false: %v", err)
		}
	})
}

func TestCheckServer(t *testing.T) {
	for _, tc := range []struct{ name, ct, body string }{
		{"cut JSON", "application/json; charset=utf-8", `{"appName":"Son`},
		{"cut problem JSON", "application/problem+json", `{"a`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake().sonarr4(v4)
			f.on("GET /api/v3/system/status", typed(v4, tc.ct, tc.body))
			_, err := checkVia(t, f, fakeBase, testKey)
			wantCheck(t, err, arrapi.CheckServer)
			de, ok := errors.AsType[*arrapi.DecodeError](err)
			if !ok {
				t.Fatalf("errors.As(*DecodeError) = false: %v", err)
			}
			if err.Error() != de.Error() || !strings.HasPrefix(err.Error(), "arrapi: decode /api/v3/system/status: invalid JSON at byte ") {
				t.Errorf("Error() = %q, want the *DecodeError's text %q", err.Error(), de.Error())
			}
			if strings.Contains(err.Error(), "base path") {
				t.Errorf("Error() = %q names the base path", err.Error())
			}
			wantUnsupported(t, err, arrapi.ReasonNotAPI)
		})
	}
	for _, code := range []int{500, 502, 429} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			f := newFake().sonarr4(v4)
			f.on("GET /api", ans(code, "", ""))
			_, err := checkVia(t, f, fakeBase, testKey)
			wantCheck(t, err, arrapi.CheckServer)
			wantStatus(t, err, code)
			if n := count(f, "GET /api"); n != 1 {
				t.Errorf("GET /api sent %d times, want 1", n)
			}
		})
	}
	t.Run("a server version change", func(t *testing.T) {
		f := newFake().sonarr4(v4)
		f.on("GET /api/v3/system/status", ans(200, "4.0.21.1", statusBody("Sonarr", "4.0.21.1")))
		_, err := checkVia(t, f, fakeBase, testKey)
		wantCheck(t, err, arrapi.CheckServer)
		wantChanged(t, err)
	})
}

// checkReplay serves the recorded detection answers of dir, statusPath's included,
// over httptest and returns the server's URL.
func checkReplay(t *testing.T, dir, statusPath string) string {
	t.Helper()
	p, err := fixturegen.NewReplay(fixtureDir(dir), map[string]fixturegen.Route{
		"GET /api":          {File: "api.json"},
		"GET " + statusPath: {File: "system-status.json"},
	})
	if err != nil {
		t.Fatalf("%v; %s", err, reRecord)
	}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		if m := p.Misses(); len(m) > 0 {
			t.Errorf("no recorded answer for %q; if the request is right, %s", m, reRecord)
		}
	})
	return srv.URL
}

func TestCheckReturnsInfo(t *testing.T) {
	for _, tc := range []struct {
		name, dir, status string
		want              []string
	}{
		{"Sonarr 4", dirSonarrV4APIv3, "/api/v3/system/status", detectV4},
		{"Sonarr 5", dirSonarrV5APIv5, "/api/v5/system/status", detectV5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url := checkReplay(t, tc.dir, tc.status)
			c, err := New(url, testKey, WithLogger(slog.New(slog.DiscardHandler)))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(c.Close)
			want, err := c.Info(t.Context())
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			got, err := checkVia(t, nil, url, testKey)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if got != want || got.Version == "" {
				t.Errorf("Check = %+v, want Info's %+v", got, want)
			}
		})
	}
	t.Run("only the detection is sent", func(t *testing.T) {
		f := newFake().sonarr5(v5)
		if _, err := checkVia(t, f, fakeBase, testKey); err != nil {
			t.Fatal(err)
		}
		wantRequests(t, f, detectV5...)
	})
}

func TestCheckEndedContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	srv := httptest.NewServer(newFake().sonarr4(v4))
	t.Cleanup(srv.Close)
	for _, base := range []string{srv.URL, "ftp://x"} {
		t.Run(base, func(t *testing.T) {
			_, err := Check(ctx, base, testKey)
			if !errors.Is(err, context.Canceled) {
				t.Errorf("err = %v, want context.Canceled", err)
			}
			if _, ok := errors.AsType[*arrapi.CheckError](err); ok {
				t.Errorf("err = %v, want no *arrapi.CheckError", err)
			}
		})
	}
}

func TestCheckLeavesTheCallerPool(t *testing.T) {
	tr := &idleCounter{RoundTripper: newFake().sonarr4(v4)}
	if _, err := Check(t.Context(), fakeBase, testKey, WithHTTPClient(&http.Client{Transport: tr})); err != nil {
		t.Fatal(err)
	}
	if n := tr.closed.Load(); n != 0 {
		t.Errorf("CloseIdleConnections ran %d times on the caller's transport, want 0", n)
	}
}

// TestCheckClosesItsOwnPool checks that the connection a check opened on the transport
// arrapi built is closed once Check returns, not left idle in a pool nobody holds.
func TestCheckClosesItsOwnPool(t *testing.T) {
	var mu sync.Mutex
	states := map[net.Conn]http.ConnState{}
	srv := httptest.NewUnstartedServer(newFake().sonarr4(v4))
	srv.Config.ConnState = func(c net.Conn, s http.ConnState) {
		mu.Lock()
		defer mu.Unlock()
		states[c] = s
	}
	srv.Start()
	t.Cleanup(srv.Close)
	if _, err := checkVia(t, nil, srv.URL, testKey); err != nil {
		t.Fatal(err)
	}
	open := func() []http.ConnState {
		mu.Lock()
		defer mu.Unlock()
		var left []http.ConnState
		for _, s := range states {
			if s != http.StateClosed {
				left = append(left, s)
			}
		}
		return left
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(open()) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	opened := len(states)
	mu.Unlock()
	if left := open(); len(left) > 0 || opened == 0 {
		t.Errorf("connections left open after Check: %v of %d", left, opened)
	}
}
