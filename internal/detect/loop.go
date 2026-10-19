package detect

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
	"github.com/cplieger/httpx/v5"
)

// budget is one call's attempt budget, wait base and log destination.
type budget struct {
	logger   *slog.Logger
	app      string
	base     time.Duration
	attempts int
}

func budgetOf(t *transport.Client, app string) budget {
	cfg := t.Config()
	return budget{logger: cfg.Logger, app: app, base: cfg.BaseDelay, attempts: cfg.MaxAttempts}
}

// mover is how a read reaches another detection; nil for probes and commands.
type mover struct {
	current func() *transport.Detection                         // an installed current detection, reused with zero runs
	run     func(context.Context) (*transport.Detection, error) // lead or join one run, which installs its detection
	accept  func(*transport.Detection) error                    // nil admits every detection; a pinned read's admits only the pin's server
}

func (m *mover) admit(d *transport.Detection) error {
	if m.accept == nil {
		return nil
	}
	return m.accept(d)
}

// The bound that ended a call, the exhaustion record's "bound" attr.
const (
	boundAttempts  = "attempts"
	boundDetection = "detection"
)

// callState is the loop's own state: the inputs of the budget table that Decide
// does not give.
type callState struct {
	cur     *transport.Detection
	drawn   *transport.Detection // cur without a self mark
	backoff time.Duration
	sends   int
	passes  int
	ran     bool
	reused  bool
}

// loop sends attempts until the budget table says Done; it is the only code that
// retries a request or sends it again on another detection. d is the call's
// detection, nil for a probe; ran says it came from a run the call led or joined.
func loop(ctx context.Context, b budget, d *transport.Detection, ran bool,
	send func(context.Context, *transport.Detection) transport.Result, m *mover,
) error {
	st := &callState{cur: d, drawn: d, ran: ran, backoff: b.base}
	for {
		r := send(ctx, st.cur)
		st.passes++
		if r.Sent {
			st.sends++
		}
		logChanged(b, r)
		var err error
		switch {
		case r.Next == transport.NextRetry:
			err = st.retry(ctx, b, r)
		case r.Next == transport.NextFresh && m != nil:
			err = st.fresh(ctx, b, r, m)
		default:
			if r.Err == nil && st.passes > 1 {
				b.logger.Debug("arrapi succeeded after retry", "attempts", st.sends)
			}
			return r.Err
		}
		if err != nil {
			return err
		}
	}
}

// retry is L2: wait, then send again on the same detection, or on the self mark the
// attempt's Result carries; on the detection as drawn when it carries none.
func (st *callState) retry(ctx context.Context, b budget, r transport.Result) error {
	if st.sends >= b.attempts {
		return st.exhausted(ctx, b, r, boundAttempts)
	}
	delay, err := st.wait(ctx, r)
	if err != nil {
		return err
	}
	if st.cur != nil && st.cur.SelfMark() != 0 {
		if r.SelfMark != 0 {
			st.cur = st.drawn.UnderSelfMark(r.SelfMark)
		} else {
			st.cur = st.drawn
		}
	}
	st.logRetry(b, r, delay, false)
	return nil
}

// fresh is L3 to L8: the detection the attempt was sent on is stale.
func (st *callState) fresh(ctx context.Context, b budget, r transport.Result, m *mover) error {
	if st.sends >= b.attempts {
		return st.exhausted(ctx, b, r, boundAttempts)
	}
	own := r.SelfMark != 0 && (r.Verdict == transport.VerdictUnidentified || r.Verdict == transport.VerdictTransport)
	if !own {
		// L6 to L8 move at once: outside evidence means the answer may be another
		// server's, so it neither delays the move nor lends it its Retry-After.
		if err := ctx.Err(); err != nil {
			return err
		}
		return st.move(ctx, b, r, m, 0)
	}
	// The wait comes before any detection is drawn, so a move lands on one drawn
	// after it.
	delay, err := st.wait(ctx, r)
	if err != nil {
		return err
	}
	if st.ran || st.reused {
		st.cur = st.drawn.UnderSelfMark(r.SelfMark)
		st.logRetry(b, r, delay, false)
		return nil
	}
	return st.move(ctx, b, r, m, delay)
}

// move reuses an installed current detection once, else leads or joins the call's
// one run; with both spent the call is Done.
func (st *callState) move(ctx context.Context, b budget, r transport.Result, m *mover, delay time.Duration) error {
	if !st.reused {
		if d := m.current(); d != nil {
			if err := m.admit(d); err != nil {
				return err
			}
			st.reused = true
			st.cur, st.drawn = d, d
			st.logRetry(b, r, delay, true)
			return nil
		}
	}
	if st.ran {
		return st.exhausted(ctx, b, r, boundDetection)
	}
	st.logRetry(b, r, delay, true)
	d, err := m.run(ctx)
	if err != nil {
		return err
	}
	if err := m.admit(d); err != nil {
		return err
	}
	st.ran = true
	st.cur, st.drawn = d, d
	return nil
}

// wait sleeps before an L2 retry or an own header-less Fresh (L3 to L5): the
// attempt's Retry-After when positive, else the doubling jittered backoff.
func (st *callState) wait(ctx context.Context, r transport.Result) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	d := httpx.JitteredBackoff(st.backoff)
	if h, ok := errors.AsType[httpx.RetryAfterHint](r.Err); ok && h.RetryAfterHint() > 0 {
		d = h.RetryAfterHint()
	}
	st.backoff = httpx.SafeDouble(st.backoff)
	return d, httpx.SleepCtx(ctx, d)
}

func (st *callState) logRetry(b budget, r transport.Result, delay time.Duration, fresh bool) {
	b.logger.Debug("arrapi failed, retrying", "attempt", st.passes, "max", b.attempts,
		"delay", delay.String(), "fresh", fresh, "error", httpx.LogSafeError(r.Err))
}

// exhausted ends a call whose Retry or Fresh the budget table turned into Done.
func (st *callState) exhausted(ctx context.Context, b budget, r transport.Result, bound string) error {
	level := slog.LevelDebug
	if b.attempts > 1 {
		level = slog.LevelWarn
	}
	b.logger.Log(ctx, level, "arrapi retries exhausted", "attempts", st.sends, "bound", bound,
		"error", httpx.LogSafeError(r.Err))
	return r.Err
}

// logChanged writes the server-change record for an attempt whose error names
// another version; a ServerChangedError with To "" names none.
func logChanged(b budget, r transport.Result) {
	if r.Verdict != transport.VerdictChanged && r.Verdict != transport.VerdictRefused {
		return
	}
	logChangedErr(b, r.Err)
}

func logChangedErr(b budget, err error) {
	if e, ok := errors.AsType[*arrapi.ServerChangedError](err); ok && e.To != "" {
		b.logger.Info("arrapi: server version changed", "app", b.app, "from", e.From, "to", e.To)
	}
}
