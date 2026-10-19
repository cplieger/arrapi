package detect

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

func TestSessionReadReusesANewerAgreeingDetection(t *testing.T) {
	f := newFake()
	serveSonarr4(f, v4)
	c := newClient(t, f, transport.Config{})
	s := newSession(c)
	installed(t, c, f, AppSonarr)
	const upgraded = "4.0.21.1"
	hold, release := releaser(t)
	arrived := make(chan struct{})
	f.on("GET "+series, then(func() { close(arrived) }, held(hold, ans(200, upgraded, `{"stale":true}`))), ans(200, upgraded, `[1,2]`))
	done := make(chan error, 1)
	var out []any
	go func() { done <- s.Read(t.Context(), readSeries(&out)) }()
	<-arrived
	serveSonarr4(f, upgraded)
	f.on("GET "+series, ans(200, upgraded, `[1,2]`))
	if _, err := s.Info(t.Context()); err != nil {
		t.Fatalf("Info: %v", err)
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("Read decoded %v, want the answer on the newer detection", out)
	}
	wantRequests(t, f, "GET "+series, "GET /api", "GET /api/v3/system/status", "GET "+series)
}

func TestSessionCacheRunEvenWithADetectionInstalled(t *testing.T) {
	f := newFake()
	serveSonarr4(f, v4)
	c := newClient(t, f, transport.Config{})
	s := newSession(c)
	installed(t, c, f, AppSonarr)
	if _, err := s.cache.run(t.Context()); err != nil {
		t.Fatalf("run: %v", err)
	}
	wantRequests(t, f, "GET /api", "GET /api/v3/system/status")
}

func TestSessionAttributedErrorKeepsTheDetection(t *testing.T) {
	for name, tc := range map[string]struct {
		code, sends int
	}{"a 404": {404, 1}, "a 500, retried": {500, 3}} {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			serveSonarr4(f, v4)
			c := newClient(t, f, transport.Config{})
			s := newSession(c)
			d := installed(t, c, f, AppSonarr)
			f.on("GET "+series, ans(tc.code, v4, `{"message":"no"}`))
			var out []any
			wantStatus(t, s.Read(t.Context(), readSeries(&out)), tc.code)
			if c.Current() != d {
				t.Error("an answer carrying the sent header dropped the detection")
			}
			want := make([]string, tc.sends)
			for i := range want {
				want[i] = "GET " + series
			}
			wantRequests(t, f, want...)
		})
	}
}

func TestSessionHeaderlessErrorDetectsOnTheNextCall(t *testing.T) {
	for _, code := range []int{404, 400} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			f := newFake()
			serveSonarr4(f, v4)
			c := newClient(t, f, transport.Config{})
			s := newSession(c)
			installed(t, c, f, AppSonarr)
			f.on("GET "+series, ans(code, "", `{"message":"Series with ID 99999999 does not exist"}`), ans(200, v4, "[]"))
			var out []any
			wantStatus(t, s.Read(t.Context(), readSeries(&out)), code)
			wantRequests(t, f, "GET "+series)
			f.clearLog()
			if err := s.Read(t.Context(), readSeries(&out)); err != nil {
				t.Fatalf("next Read: %v", err)
			}
			wantRequests(t, f, "GET /api", "GET /api/v3/system/status", "GET "+series)
		})
	}
}

func TestSessionFirstReadHeaderless503Then200(t *testing.T) {
	f := newFake()
	serveSonarr4(f, v4)
	f.on("GET "+series, ans(503, "", ""), ans(200, v4, `[{"id":7}]`))
	c := newClient(t, f, transport.Config{})
	s := newSession(c)
	var out []any
	if err := s.Read(t.Context(), readSeries(&out)); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("Read decoded %v", out)
	}
	wantRequests(t, f, "GET /api", "GET /api/v3/system/status", "GET "+series, "GET "+series)
	f.clearLog()
	if err := s.Read(t.Context(), readSeries(&out)); err != nil {
		t.Fatalf("next Read: %v", err)
	}
	wantRequests(t, f, "GET /api", "GET /api/v3/system/status", "GET "+series)
}

func TestSessionHeaderlessSuccessIsNotSentAgain(t *testing.T) {
	f := newFake()
	serveSonarr4(f, v4)
	c := newClient(t, f, transport.Config{})
	s := newSession(c)
	installed(t, c, f, AppSonarr)
	f.on("GET "+series, ans(200, "", "[]"), ans(200, v4, "[]"))
	out := []any{"untouched"}
	wantUnsupported(t, s.Read(t.Context(), readSeries(&out)), arrapi.ReasonNoVersionHeader)
	if len(out) != 1 {
		t.Errorf("a header-less success was decoded: %v", out)
	}
	wantRequests(t, f, "GET "+series)
	f.clearLog()
	if err := s.Read(t.Context(), readSeries(&out)); err != nil {
		t.Fatalf("next Read: %v", err)
	}
	wantRequests(t, f, "GET /api", "GET /api/v3/system/status", "GET "+series)
}

func TestSessionMutate(t *testing.T) {
	const post = "POST /api/v3/command"
	t.Run("detects fresh with a detection installed and sends one POST", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		c := newClient(t, f, transport.Config{})
		s := newSession(c)
		installed(t, c, f, AppSonarr)
		if err := s.Mutate(t.Context(), rescan); err != nil {
			t.Fatalf("Mutate: %v", err)
		}
		wantRequests(t, f, "GET /api", "GET /api/v3/system/status", post)
	})
	t.Run("a 503 is sent once", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		f.on(post, ans(503, v4, ""))
		s := newSession(newClient(t, f, transport.Config{}))
		wantStatus(t, s.Mutate(t.Context(), rescan), 503)
		wantRequests(t, f, "GET /api", "GET /api/v3/system/status", post)
	})
	t.Run("row 10 a failure from another version", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		f.on(post, ans(500, "5.0.0.1", ""))
		s := newSession(newClient(t, f, transport.Config{}))
		err := s.Mutate(t.Context(), rescan)
		e, ok := errors.AsType[*arrapi.ServerChangedError](err)
		if !ok || e.Status == nil || e.Status.Code != 500 || errors.Is(err, arrapi.ErrCommandOutcomeUnknown) {
			t.Fatalf("error = %v, want a ServerChangedError carrying the 500", err)
		}
		wantRequests(t, f, "GET /api", "GET /api/v3/system/status", post)
	})
	t.Run("row 9 nothing is sent after a contradicting answer", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		f.on("GET /api/v3/tag", ans(500, "", ""))
		s := newSession(newClient(t, f, transport.Config{}))
		err := s.Mutate(t.Context(), func(ctx context.Context, d *transport.Detection, e engine) transport.Result {
			var out []any
			e.c.GetJSON(ctx, d, "/api/v3/tag", transport.MaxListBytes, &out)
			return rescan(ctx, d, e)
		})
		if _, ok := errors.AsType[*arrapi.ServerChangedError](err); !ok {
			t.Fatalf("error = %v (%T), want *arrapi.ServerChangedError", err, err)
		}
		wantRequests(t, f, "GET /api", "GET /api/v3/system/status", "GET /api/v3/tag")
	})
}

// TestCommandAnswerEndsCurrency: a command's 2xx that cannot be attributed is an
// unknown outcome, and the next call detects before any data path.
func TestCommandAnswerEndsCurrency(t *testing.T) {
	const post = "POST /api/v3/command"
	unknown := map[string]struct {
		post  step
		check func(t *testing.T, err error)
	}{
		"row 6 a header-less 2xx": {ans(201, "", "{}"), func(t *testing.T, err error) {
			wantUnsupported(t, err, arrapi.ReasonNoVersionHeader)
		}},
		"row 11 a 2xx from another version": {ans(201, "5.0.0.1", "{}"), func(t *testing.T, err error) {
			if e, ok := errors.AsType[*arrapi.ServerChangedError](err); !ok || e.To != "5.0.0.1" || e.Status != nil {
				t.Errorf("error = %v, want a ServerChangedError to 5.0.0.1 without Status", err)
			}
		}},
	}
	for name, tc := range unknown {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			serveSonarr4(f, v4)
			f.on(post, tc.post)
			c := newClient(t, f, transport.Config{})
			s := newSession(c)
			err := s.Mutate(t.Context(), rescan)
			if !errors.Is(err, arrapi.ErrCommandOutcomeUnknown) {
				t.Fatalf("error = %v, want ErrCommandOutcomeUnknown", err)
			}
			tc.check(t, err)
			wantRequests(t, f, "GET /api", "GET /api/v3/system/status", post)
			f.clearLog()
			serveSonarr4(f, v4)
			var out []any
			if err := s.Read(t.Context(), readSeries(&out)); err != nil {
				t.Fatalf("next Read: %v", err)
			}
			if got := f.requests(); got[0] != "GET /api" {
				t.Errorf("next Read sent %q, want GET /api first", got)
			}
		})
	}
}

func TestSessionInfo(t *testing.T) {
	t.Run("detects with a detection installed and one attempt per probe", func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		c := newClient(t, f, transport.Config{})
		s := newSession(c)
		installed(t, c, f, AppSonarr)
		f.on("GET /api/v3/system/status", ans(503, v4, ""))
		_, err := s.Info(t.Context())
		wantStatus(t, err, 503)
		wantRequests(t, f, "GET /api", "GET /api/v3/system/status")
	})
	t.Run("bounded at 5 seconds", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newFake()
			serveSonarr4(f, v4)
			f.on("GET /api/v3/system/status", func(_ http.ResponseWriter, r *http.Request) error {
				<-r.Context().Done()
				return r.Context().Err()
			})
			s := newSession(newClient(t, f, transport.Config{}))
			start := time.Now()
			if _, err := s.Info(t.Context()); err == nil {
				t.Fatal("Info succeeded against a stalled server")
			}
			if took := time.Since(start); took != 5*time.Second {
				t.Errorf("Info took %v, want 5s", took)
			}
		})
	})
}

func TestSessionCurrent(t *testing.T) {
	f := newFake()
	serveSonarr5(f, v5)
	c := newClient(t, f, transport.Config{})
	s := newSession(c)
	d, e, err := s.Current(t.Context())
	if err != nil || d == nil || e.prefix != "/api/v5" {
		t.Fatalf("Current = (%v, %q, %v)", d, e.prefix, err)
	}
	f.clearLog()
	if again, _, _ := s.Current(t.Context()); again != d {
		t.Error("Current detected again with a detection installed")
	}
	wantRequests(t, f)
}

// A pinned read's accept names the version of the detection it refuses,
// whatever answer reached the client between the move and the check: a header-less
// one, which leaves State.AppVersion "", or one from the pin's version again.
func TestAdmitPinNamesTheRefusedDetection(t *testing.T) {
	f := newFake()
	serveSonarr4(f, v4)
	c := newClient(t, f, transport.Config{})
	pin := installed(t, c, f, AppSonarr)
	serveSonarr5(f, v5)
	moved, err := run(t, c, AppSonarr)
	if err != nil {
		t.Fatalf("detection of Sonarr 5: %v", err)
	}
	admit := admitPin(c, pin)
	if err := admit(&transport.Detection{Identity: pin.Identity, Epoch: moved.Epoch}); err != nil {
		t.Errorf("admit(the pin's identity) = %v, want nil", err)
	}
	for name, since := range map[string]func(*testing.T){
		"a header-less answer since": func(*testing.T) { c.MarkUnknown("/api/v3/tag") },
		"an answer from the pinned version since": func(t *testing.T) {
			serveSonarr4(f, v4)
			if _, err := run(t, c, AppSonarr); err != nil {
				t.Fatalf("detection of Sonarr 4 again: %v", err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			since(t)
			e, ok := errors.AsType[*arrapi.ServerChangedError](admit(moved))
			if !ok {
				t.Fatal("admit(a Sonarr 5 detection) is not a *arrapi.ServerChangedError")
			}
			if e.From != v4 || e.To != v5 {
				t.Errorf("ServerChangedError From %q To %q, want From %q To %q", e.From, e.To, v4, v5)
			}
		})
	}
}
