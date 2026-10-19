package detect

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

func TestCacheNoExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		c := newClient(t, f, transport.Config{})
		cache := newCache(c, AppSonarr)
		d, ran, err := cache.get(t.Context())
		if err != nil || !ran {
			t.Fatalf("first get = (ran %v, %v), want a run", ran, err)
		}
		f.clearLog()
		time.Sleep(24 * time.Hour)
		got, ran, err := cache.get(t.Context())
		if err != nil || ran || got != d {
			t.Errorf("get a day later = (%p, ran %v, %v), want the same detection without a run", got, ran, err)
		}
		wantRequests(t, f)
	})
}

func TestCacheSharesOneRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		hold, release := releaser(t)
		f.on("GET /api/v3/system/status", held(hold, ans(200, v4, statusBody("Sonarr", v4))))
		c := newClient(t, f, transport.Config{})
		cache := newCache(c, AppSonarr)
		var got [3]*transport.Detection
		var wg sync.WaitGroup
		for i := range got {
			wg.Go(func() {
				d, ran, err := cache.get(t.Context())
				if err != nil || !ran {
					t.Errorf("get %d = (ran %v, %v)", i, ran, err)
				}
				got[i] = d
			})
		}
		synctest.Wait()
		release()
		wg.Wait()
		if got[0] == nil || got[0] != got[1] || got[1] != got[2] {
			t.Errorf("detections = %p %p %p, want one shared", got[0], got[1], got[2])
		}
		wantRequests(t, f, "GET /api", "GET /api/v3/system/status")
		if c.Current() != got[0] {
			t.Error("the shared run's detection is not installed")
		}
	})
}

func TestCacheCancelledWaiterReturnsItsOwnError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		hold, release := releaser(t)
		f.on("GET /api/v3/system/status", held(hold, ans(200, v4, statusBody("Sonarr", v4))))
		c := newClient(t, f, transport.Config{})
		cache := newCache(c, AppSonarr)
		leader := make(chan error, 1)
		go func() { _, err := cache.run(t.Context()); leader <- err }()
		synctest.Wait()
		ctx, cancel := context.WithCancel(t.Context())
		waiter := make(chan error, 1)
		go func() { _, err := cache.run(ctx); waiter <- err }()
		synctest.Wait()
		cancel()
		if err := <-waiter; !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled waiter = %v, want context.Canceled", err)
		}
		release()
		if err := <-leader; err != nil {
			t.Errorf("leader = %v, want its detection", err)
		}
	})
}

func TestCacheFailedRunInstallsNothing(t *testing.T) {
	f := newFake()
	serveRadarr(f, vR)
	c := newClient(t, f, transport.Config{})
	cache := newCache(c, AppSonarr)
	if _, _, err := cache.get(t.Context()); err == nil {
		t.Fatal("get against Radarr succeeded")
	}
	if c.Current() != nil {
		t.Error("a failed run installed a detection")
	}
	if _, err := cache.fresh(t.Context(), false); err == nil {
		t.Fatal("fresh against Radarr succeeded")
	}
	if c.Current() != nil {
		t.Error("a failed fresh installed a detection")
	}
}

// TestConcurrentGetAndFreshSameServerBothUsable runs a lazy detection and a rescan's
// fresh one against one Sonarr 4.0 at once: both anchor in the same epoch, so both
// pass the end check although the first install replaced the State pointer.
func TestConcurrentGetAndFreshSameServerBothUsable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		hold, release := releaser(t)
		f.on("GET /api/v3/system/status", held(hold, ans(200, v4, statusBody("Sonarr", v4))))
		c := newClient(t, f, transport.Config{})
		s := newSession(c)
		readErr, rescanErr := make(chan error, 1), make(chan error, 1)
		var out []any
		go func() { readErr <- s.Read(t.Context(), readSeries(&out)) }()
		go func() { rescanErr <- s.Mutate(t.Context(), rescan) }()
		synctest.Wait()
		release()
		if err := <-readErr; err != nil {
			t.Errorf("Read = %v", err)
		}
		if err := <-rescanErr; err != nil {
			t.Errorf("RescanSeries = %v", err)
		}
		posts := 0
		for _, r := range f.requests() {
			if r == "POST /api/v3/command" {
				posts++
			}
		}
		if posts != 1 {
			t.Errorf("POST sent %d times, want once: %q", posts, f.requests())
		}
	})
}

func TestLeaderCancelDoesNotSurface(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFake()
		serveSonarr4(f, v4)
		hold, release := releaser(t)
		f.on("GET /api/v3/system/status", held(hold, ans(200, v4, statusBody("Sonarr", v4))),
			ans(200, v4, statusBody("Sonarr", v4)))
		c := newClient(t, f, transport.Config{})
		cache := newCache(c, AppSonarr)
		ctx, cancel := context.WithCancel(t.Context())
		leader := make(chan error, 1)
		go func() { _, err := cache.run(ctx); leader <- err }()
		synctest.Wait()
		waiter := make(chan *transport.Detection, 1)
		go func() {
			d, err := cache.run(t.Context())
			if err != nil {
				t.Errorf("waiter = %v, want a detection of its own run", err)
			}
			waiter <- d
		}()
		synctest.Wait()
		cancel()
		if err := <-leader; !errors.Is(err, context.Canceled) {
			t.Errorf("leader = %v, want its own context.Canceled", err)
		}
		release()
		if d := <-waiter; d == nil || c.Current() != d {
			t.Errorf("waiter's detection %p is not the installed %p", d, c.Current())
		}
		wantRequests(t, f, "GET /api", "GET /api/v3/system/status", "GET /api", "GET /api/v3/system/status")
	})
}

// TestJoinAfterDoomLeadsNextRun: a lazy run R1 anchors at epoch e; a read on a
// detection fresh installed at e gets a header-less 503, whose mark dooms R1; the
// read waits for R1 to end and then leads the next run, while R1's leader and a
// waiter that joined before the 503 get R1's error.
func TestJoinAfterDoomLeadsNextRun(t *testing.T) {
	setup := func(t *testing.T) (*fakeServer, *transport.Client, *Session[engine], *transport.Detection) {
		f := newFake()
		serveSonarr4(f, v4)
		c := newClient(t, f, transport.Config{})
		s := newSession(c)
		d, err := s.cache.fresh(t.Context(), false)
		if err != nil {
			t.Fatalf("fresh: %v", err)
		}
		f.clearLog()
		return f, c, s, d
	}
	retryAfter503 := func(w http.ResponseWriter, _ *http.Request) error {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusServiceUnavailable)
		return nil
	}
	t.Run("a doomed run", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f, c, s, _ := setup(t)
			r1Status, releaseR1 := releaser(t)
			leaderDone := make(chan struct{})
			f.on("GET /api/v3/system/status",
				held(r1Status, ans(200, v4, statusBody("Sonarr", v4))),
				func(w http.ResponseWriter, r *http.Request) error {
					<-leaderDone
					return ans(200, v4, statusBody("Sonarr", v4))(w, r)
				})
			f.on("GET "+series, retryAfter503, ans(200, v4, `[{"id":1}]`))
			var leaderErr, waiterErr error
			go func() { _, leaderErr = s.cache.run(t.Context()); close(leaderDone) }()
			synctest.Wait()
			waiterDone := make(chan struct{})
			go func() { _, waiterErr = s.cache.run(t.Context()); close(waiterDone) }()
			synctest.Wait()
			readDone := make(chan error, 1)
			var out []any
			go func() { readDone <- s.Read(t.Context(), readSeries(&out)) }()
			synctest.Wait()
			time.Sleep(time.Second)
			synctest.Wait()
			releaseR1()
			<-leaderDone
			<-waiterDone
			for who, err := range map[string]error{"leader": leaderErr, "earlier waiter": waiterErr} {
				e, ok := errors.AsType[*arrapi.ServerChangedError](err)
				if !ok || e.To != "" {
					t.Errorf("%s = %v, want R1's ServerChangedError with To \"\"", who, err)
				}
			}
			if err := <-readDone; err != nil {
				t.Fatalf("Read = %v, want the next run's data", err)
			}
			if len(out) != 1 {
				t.Errorf("Read decoded %v", out)
			}
			wantRequests(t, f, "GET /api", "GET /api/v3/system/status", "GET "+series,
				"GET /api", "GET /api/v3/system/status", "GET "+series)
			if c.Current() == nil {
				t.Error("the next run installed nothing")
			}
		})
	})
	t.Run("a run that anchors after the mark is shared", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f, _, s, _ := setup(t)
			r1API, releaseR1 := releaser(t)
			f.on("GET /api", held(r1API, ans(200, v4, apiBodyNoDeprecated("v3"))))
			f.on("GET "+series, retryAfter503, ans(200, v4, `[{"id":1}]`))
			var leaderErr, waiterErr error
			leaderDone, waiterDone := make(chan struct{}), make(chan struct{})
			go func() { _, leaderErr = s.cache.run(t.Context()); close(leaderDone) }()
			synctest.Wait()
			go func() { _, waiterErr = s.cache.run(t.Context()); close(waiterDone) }()
			synctest.Wait()
			readDone := make(chan error, 1)
			var out []any
			go func() { readDone <- s.Read(t.Context(), readSeries(&out)) }()
			synctest.Wait()
			time.Sleep(time.Second)
			synctest.Wait()
			releaseR1()
			<-leaderDone
			<-waiterDone
			if leaderErr != nil || waiterErr != nil {
				t.Errorf("leader = %v, waiter = %v, want R1's detection", leaderErr, waiterErr)
			}
			if err := <-readDone; err != nil {
				t.Fatalf("Read = %v", err)
			}
			wantRequests(t, f, "GET /api", "GET "+series, "GET /api/v3/system/status", "GET "+series)
		})
	})
}
