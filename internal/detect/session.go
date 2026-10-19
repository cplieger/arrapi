package detect

import (
	"context"
	"time"

	"github.com/cplieger/arrapi/v3/internal/transport"
)

// infoTimeout bounds Info's whole detection, every probe included.
const infoTimeout = 5 * time.Second

// Session is the only path from a public method to an engine: it hands each call a
// detection and the engine pick names for it, and runs the call's attempts through
// the one attempt loop. It decides nothing itself.
type Session[E any] struct {
	t     *transport.Client
	cache *cache
	pick  func(*transport.Detection) E
	b     budget
}

// NewSession returns the session of client t expecting app; pick names the engine
// that reads a detection's API.
func NewSession[E any](t *transport.Client, app string, pick func(*transport.Detection) E) *Session[E] {
	return &Session[E]{t: t, cache: newCache(t, app), pick: pick, b: budgetOf(t, app)}
}

// Read runs one read: call sends one attempt on the detection and engine it is
// handed and returns its Result, and the loop retries or moves it within the call's
// budget.
func (s *Session[E]) Read(ctx context.Context, call func(context.Context, *transport.Detection, E) transport.Result) error {
	d, ran, err := s.cache.get(ctx)
	if err != nil {
		return err
	}
	m := &mover{current: s.t.Current, run: s.cache.run}
	return loop(ctx, s.b, d, ran, s.bind(call), m)
}

// Mutate runs one command on a detection drawn by a fresh run immediately before
// it; the command is sent at most once.
func (s *Session[E]) Mutate(ctx context.Context, call func(context.Context, *transport.Detection, E) transport.Result) error {
	d, err := s.cache.fresh(ctx, false)
	if err != nil {
		return err
	}
	return loop(ctx, s.b, d, true, s.bind(call), nil)
}

// Info runs a fresh detection with one attempt per probe, inside 5 seconds.
func (s *Session[E]) Info(ctx context.Context) (*transport.Detection, error) {
	ctx, cancel := context.WithTimeout(ctx, infoTimeout)
	defer cancel()
	return s.cache.fresh(ctx, true)
}

// Pin runs a fresh detection whose probes each have the read budget, for ReadPinned.
func (s *Session[E]) Pin(ctx context.Context) (*transport.Detection, error) {
	return s.cache.fresh(ctx, false)
}

// ReadPinned runs one read as Read does, starting on pin, except that a detection the
// call moves onto must have pin's Identity: one of another server version ends the call
// with *arrapi.ServerChangedError. One server version announces one API, so pin's
// engine reads every attempt.
func (s *Session[E]) ReadPinned(ctx context.Context, pin *transport.Detection, call func(context.Context, *transport.Detection, E) transport.Result) error {
	m := &mover{current: s.t.Current, run: s.cache.run, accept: admitPin(s.t, pin)}
	return loop(ctx, s.b, pin, false, s.bind(call), m)
}

// admitPin is a pinned read's accept: a detection without pin's Identity ends the call
// with a *arrapi.ServerChangedError whose To is that detection's version, whatever
// answer has reached t since, so a caller deciding on To sees what was refused.
func admitPin(t *transport.Client, pin *transport.Detection) func(*transport.Detection) error {
	return func(d *transport.Detection) error {
		if d.Identity == pin.Identity {
			return nil
		}
		st := *t.State()
		st.AppVersion = d.Identity.AppVersion
		return t.ChangedError(&st, pin.Identity.AppVersion)
	}
}

// Current returns a current detection and its engine, detecting when none is
// installed; it sends nothing beyond detection.
func (s *Session[E]) Current(ctx context.Context) (*transport.Detection, E, error) {
	d, _, err := s.cache.get(ctx)
	if err != nil {
		var zero E
		return nil, zero, err
	}
	return d, s.pick(d), nil
}

func (s *Session[E]) bind(call func(context.Context, *transport.Detection, E) transport.Result) func(context.Context, *transport.Detection) transport.Result {
	return func(ctx context.Context, d *transport.Detection) transport.Result {
		return call(ctx, d, s.pick(d))
	}
}
