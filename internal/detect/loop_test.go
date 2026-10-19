package detect

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/statushook"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

// Detections of the loop tests, told apart by epoch: the call's own, one installed
// since, and the one a move's run returns.
func loopDetections() (own, inst, fromRun *transport.Detection) {
	id := func(v string) transport.Identity { return transport.Identity{App: AppSonarr, AppVersion: v} }
	return &transport.Detection{Epoch: 1, Identity: id(v4)},
		&transport.Detection{Epoch: 3, Identity: id("5.0.0.1")},
		&transport.Detection{Epoch: 4, Identity: id("5.0.0.1")}
}

// script is a scripted send: it answers with its results in order, repeating the
// last, and records the detection each attempt was handed and when.
type script struct {
	results  []transport.Result
	sentOn   []string
	at       []time.Duration
	start    time.Time
	sends    int
	overflow bool
}

// maxPasses ends a scripted call that loops: a loop that never ends fails the case
// with an overflow instead of hanging the run.
const maxPasses = 20

func (s *script) send(_ context.Context, d *transport.Detection) transport.Result {
	if len(s.sentOn) >= maxPasses {
		s.overflow = true
		return transport.Result{Verdict: transport.VerdictAccept, Next: transport.NextDone}
	}
	s.sentOn = append(s.sentOn, label(d))
	s.at = append(s.at, time.Since(s.start))
	r := s.results[0]
	if len(s.results) > 1 {
		s.results = s.results[1:]
	}
	if r.Sent {
		s.sends++
	}
	return r
}

// label names a detection by epoch and self mark: "e1", "e1@2"; "probe" for nil.
func label(d *transport.Detection) string {
	switch {
	case d == nil:
		return "probe"
	case d.SelfMark() != 0:
		return fmt.Sprintf("e%d@%d", d.Epoch, d.SelfMark())
	}
	return fmt.Sprintf("e%d", d.Epoch)
}

// scriptedMover answers current with its installed list in order, then nil, and run
// with fromRun or runErr.
type scriptedMover struct {
	fromRun   *transport.Detection
	runErr    error
	installed []*transport.Detection
	start     time.Time
	runAt     []time.Duration
	currentAt []time.Duration
	runs      int
	reuses    int
}

func (m *scriptedMover) mover() *mover {
	return &mover{
		current: func() *transport.Detection {
			m.currentAt = append(m.currentAt, time.Since(m.start))
			if len(m.installed) == 0 {
				return nil
			}
			d := m.installed[0]
			m.installed = m.installed[1:]
			if d != nil {
				m.reuses++
			}
			return d
		},
		run: func(context.Context) (*transport.Detection, error) {
			m.runAt = append(m.runAt, time.Since(m.start))
			m.runs++
			return m.fromRun, m.runErr
		},
	}
}

func statusErr(code int, retryAfter time.Duration) *arrapi.StatusError {
	e := &arrapi.StatusError{Code: code, Path: series}
	statushook.SetRetryAfter(e, retryAfter)
	return e
}

func changedErr(to string) *arrapi.ServerChangedError {
	return &arrapi.ServerChangedError{Origin: base, Path: series, From: v4, To: to}
}

// Results of one attempt, each with an error of its own so the test can tell which
// attempt's error a call returned.
func rOK() transport.Result {
	return transport.Result{Verdict: transport.VerdictAccept, Next: transport.NextDone, Sent: true}
}

func rHeadered(code int, next transport.Next) transport.Result {
	return transport.Result{Err: statusErr(code, 0), Verdict: transport.VerdictStatus, Next: next, Sent: true}
}

func rHeaderless(selfMark uint64) transport.Result {
	return transport.Result{Err: statusErr(502, 0), Verdict: transport.VerdictUnidentified, Next: transport.NextFresh, Sent: true, SelfMark: selfMark}
}

func rFailedHop(selfMark uint64) transport.Result {
	return transport.Result{
		Err: errors.New("arrapi: request /api/v3/series: dial refused"), Verdict: transport.VerdictTransport,
		Next: transport.NextFresh, Sent: true, SelfMark: selfMark,
	}
}

func rChanged() transport.Result {
	return transport.Result{Err: changedErr("5.0.0.1"), Verdict: transport.VerdictChanged, Next: transport.NextFresh, Sent: true}
}

func rRefused(to string) transport.Result {
	return transport.Result{Err: changedErr(to), Verdict: transport.VerdictRefused, Next: transport.NextFresh}
}

func TestAttemptLoopTable(t *testing.T) {
	own, inst, fromRun := loopDetections()
	runErr := errors.New("detection failed")
	type tcase struct {
		name      string
		installed []*transport.Detection
		runErr    error
		results   []transport.Result
		wantOn    []string
		attempts  int
		wantErr   int // index of the result whose error the call returns; -1 for nil, -2 for runErr
		wantRuns  int
		wantReuse int
		ran       bool
		probe     bool
		mutation  bool
	}
	tests := []tcase{
		// L1 and L2 for probes, whose loops have no mover.
		{
			name: "ProbeBudget unanchored header-less 502 then 200", probe: true, attempts: 3,
			results: []transport.Result{rHeadered(502, transport.NextRetry), rOK()}, wantOn: []string{"probe", "probe"}, wantErr: -1,
		},
		{
			name: "ProbeBudget a budget of 1 returns the first", probe: true, attempts: 1,
			results: []transport.Result{rHeadered(502, transport.NextRetry)}, wantOn: []string{"probe"}, wantErr: 0,
		},
		{
			name: "ProbeBudget an anchored probe's header-less 500 at once", probe: true, attempts: 3,
			results: []transport.Result{{Err: statusErr(500, 0), Verdict: transport.VerdictUnidentified, Next: transport.NextDone, Sent: true}},
			wantOn:  []string{"probe"}, wantErr: 0,
		},
		{
			name: "ProbeBudget an anchored probe's failed hop at once", probe: true, attempts: 3,
			results: []transport.Result{{Err: runErr, Verdict: transport.VerdictTransport, Next: transport.NextDone, Sent: true}},
			wantOn:  []string{"probe"}, wantErr: 0,
		},
		// L1 for commands.
		{
			name: "MutationNeverRetried a 503", mutation: true, ran: true, attempts: 3,
			results: []transport.Result{rHeadered(503, transport.NextDone)}, wantOn: []string{"e1"}, wantErr: 0,
		},
		{
			name: "MutationNeverRetried a header-less 502", mutation: true, ran: true, attempts: 3,
			results: []transport.Result{{Err: statusErr(502, 0), Verdict: transport.VerdictUnidentified, Next: transport.NextDone, Sent: true, SelfMark: 2}},
			wantOn:  []string{"e1"}, wantErr: 0,
		},
		{
			name: "MutationNeverRetried a refused send", mutation: true, ran: true, attempts: 3,
			results: []transport.Result{{Err: changedErr(""), Verdict: transport.VerdictRefused, Next: transport.NextDone}},
			wantOn:  []string{"e1"}, wantErr: 0,
		},
		{
			name: "MutationNeverRetried a transport error", mutation: true, ran: true, attempts: 3,
			results: []transport.Result{{Err: runErr, Verdict: transport.VerdictTransport, Next: transport.NextDone, Sent: true}},
			wantOn:  []string{"e1"}, wantErr: 0,
		},
		// L2 for reads.
		{
			name: "TransientTransportRetriesOnTheSameDetection", attempts: 3,
			results: []transport.Result{{Err: runErr, Verdict: transport.VerdictTransport, Next: transport.NextRetry, Sent: true}, rOK()},
			wantOn:  []string{"e1", "e1"}, wantErr: -1,
		},
		{
			name: "HeaderedTwiceThenHeaderlessSpent", attempts: 3,
			results: []transport.Result{rHeadered(503, transport.NextRetry), rHeadered(503, transport.NextRetry), rHeaderless(2)},
			wantOn:  []string{"e1", "e1", "e1"}, wantErr: 2,
		},
		{
			name: "SelfMarkedRetryWithoutAMarkReturnsToTheDrawnDetection", ran: true, attempts: 3,
			results: []transport.Result{rHeaderless(2), rHeadered(503, transport.NextRetry), rOK()},
			wantOn:  []string{"e1", "e1@2", "e1"}, wantErr: -1,
		},
		{
			name: "SelfMarkedRetryKeepsTheMarkItsAnswerCarries", ran: true, attempts: 3,
			results: []transport.Result{rHeaderless(2), {Err: statusErr(503, 0), Verdict: transport.VerdictStatus, Next: transport.NextRetry, Sent: true, SelfMark: 3}, rOK()},
			wantOn:  []string{"e1", "e1@2", "e1@3"}, wantErr: -1,
		},
		// L3: a cached read's own header-less answer moves it.
		{
			name: "HeaderlessFiveXXThen200OneDetection", attempts: 3,
			results: []transport.Result{rHeaderless(2), rOK()}, wantOn: []string{"e1", "e4"}, wantErr: -1, wantRuns: 1,
		},
		{
			name: "HeaderlessFiveXXTwiceOneDetection", attempts: 2,
			results: []transport.Result{rHeaderless(2), rHeaderless(5)}, wantOn: []string{"e1", "e4"}, wantErr: 1, wantRuns: 1,
		},
		{
			name: "HeaderlessFiveXXAlwaysOneDetection", attempts: 3,
			results: []transport.Result{rHeaderless(2), rHeaderless(5), rHeaderless(6)},
			wantOn:  []string{"e1", "e4", "e4@5"}, wantErr: 2, wantRuns: 1,
		},
		{
			name: "CachedFailedHopMoves", attempts: 3,
			results: []transport.Result{rFailedHop(2), rOK()}, wantOn: []string{"e1", "e4"}, wantErr: -1, wantRuns: 1,
		},
		{
			name: "CachedHeaderlessReusesAnInstalledDetection", attempts: 3, installed: []*transport.Detection{inst},
			results: []transport.Result{rHeaderless(2), rOK()}, wantOn: []string{"e1", "e3"}, wantErr: -1, wantReuse: 1,
		},
		{
			name: "HeaderlessThenHeaderedUsesBudget", attempts: 3,
			results: []transport.Result{rHeaderless(2), rHeadered(503, transport.NextRetry), rOK()},
			wantOn:  []string{"e1", "e4", "e4"}, wantErr: -1, wantRuns: 1,
		},
		{
			name: "AMoveWhoseRunFailsReturnsItsError", attempts: 3, runErr: runErr,
			results: []transport.Result{rHeaderless(2)}, wantOn: []string{"e1"}, wantErr: -2, wantRuns: 1,
		},
		// L4: after a reuse, the own header-less answer retries on the reused detection.
		{
			name: "ReusedThenHeaderlessRetriesOnReused", attempts: 3, installed: []*transport.Detection{inst},
			results: []transport.Result{rChanged(), rHeaderless(5), rOK()},
			wantOn:  []string{"e1", "e3", "e3@5"}, wantErr: -1, wantReuse: 1,
		},
		// L5: a read that ran its detection retries on it under the self mark.
		{
			name: "FirstCallHeaderless503Then200", ran: true, attempts: 3,
			results: []transport.Result{rHeaderless(2), rOK()}, wantOn: []string{"e1", "e1@2"}, wantErr: -1,
		},
		{
			name: "RanThenHeaderlessFiveXX", ran: true, attempts: 3, installed: []*transport.Detection{inst},
			results: []transport.Result{rHeaderless(2), rOK()}, wantOn: []string{"e1", "e1@2"}, wantErr: -1,
		},
		{
			name: "RanThenFailedHop", ran: true, attempts: 3,
			results: []transport.Result{rFailedHop(2), rOK()}, wantOn: []string{"e1", "e1@2"}, wantErr: -1,
		},
		// L6: outside evidence, no reuse yet.
		{
			name: "ChangedReusesInstalled", attempts: 3, installed: []*transport.Detection{inst},
			results: []transport.Result{rChanged(), rOK()}, wantOn: []string{"e1", "e3"}, wantErr: -1, wantReuse: 1,
		},
		{
			name: "ChangedReusesInstalledAfterARun", ran: true, attempts: 3, installed: []*transport.Detection{inst},
			results: []transport.Result{rChanged(), rOK()}, wantOn: []string{"e1", "e3"}, wantErr: -1, wantReuse: 1,
		},
		{
			name: "RefusedReusesInstalled", ran: true, attempts: 3, installed: []*transport.Detection{inst},
			results: []transport.Result{rRefused(""), rOK()}, wantOn: []string{"e1", "e3"}, wantErr: -1, wantReuse: 1,
		},
		{
			name: "ChangedRunsWhenNoneInstalled", attempts: 3,
			results: []transport.Result{rChanged(), rOK()}, wantOn: []string{"e1", "e4"}, wantErr: -1, wantRuns: 1,
		},
		{
			name: "OutsideHeaderlessRunsWhenNoneInstalled", attempts: 3,
			results: []transport.Result{rHeaderless(0), rOK()}, wantOn: []string{"e1", "e4"}, wantErr: -1, wantRuns: 1,
		},
		{
			name: "RanThenOutsideHeaderlessNoneInstalled", ran: true, attempts: 3,
			results: []transport.Result{rHeaderless(0)}, wantOn: []string{"e1"}, wantErr: 0,
		},
		{
			name: "RanThenOutsideHeaderlessReusesInstalled", ran: true, attempts: 3, installed: []*transport.Detection{inst},
			results: []transport.Result{rHeaderless(0), rOK()}, wantOn: []string{"e1", "e3"}, wantErr: -1, wantReuse: 1,
		},
		{
			name: "RanThenChangedNoneInstalled", ran: true, attempts: 3,
			results: []transport.Result{rChanged()}, wantOn: []string{"e1"}, wantErr: 0,
		},
		{
			name: "RanThenRefusedNoneInstalled", ran: true, attempts: 3,
			results: []transport.Result{rRefused("")}, wantOn: []string{"e1"}, wantErr: 0,
		},
		{
			name: "SelfMarkRetryRefusedMoves onto an installed detection", ran: true, attempts: 3, installed: []*transport.Detection{inst},
			results: []transport.Result{rHeaderless(2), rRefused(""), rOK()}, wantOn: []string{"e1", "e1@2", "e3"}, wantErr: -1, wantReuse: 1,
		},
		{
			name: "SelfMarkRetryRefusedMoves none installed", ran: true, attempts: 3,
			results: []transport.Result{rHeaderless(2), rRefused("")}, wantOn: []string{"e1", "e1@2"}, wantErr: 1,
		},
		// L7 and L8: outside evidence after a reuse.
		{
			name: "ReusedThenChangedRuns", attempts: 3, installed: []*transport.Detection{inst},
			results: []transport.Result{rChanged(), rChanged(), rOK()}, wantOn: []string{"e1", "e3", "e4"}, wantErr: -1, wantRuns: 1, wantReuse: 1,
		},
		{
			name: "ReusedRanThenChangedStops", attempts: 5, installed: []*transport.Detection{inst},
			results: []transport.Result{rChanged(), rChanged(), rChanged()}, wantOn: []string{"e1", "e3", "e4"}, wantErr: 2, wantRuns: 1, wantReuse: 1,
		},
		{
			name: "MoveBound", attempts: 10, installed: []*transport.Detection{inst, inst, inst},
			results: []transport.Result{rChanged()}, wantOn: []string{"e1", "e3", "e4"}, wantErr: 0, wantRuns: 1, wantReuse: 1,
		},
		{
			name: "RefusedBound", attempts: 3, installed: []*transport.Detection{inst, inst, inst},
			results: []transport.Result{rRefused("")}, wantOn: []string{"e1", "e3", "e4"}, wantErr: 0, wantRuns: 1, wantReuse: 1,
		},
		// Budget against moves.
		{
			name: "ChangedVersionUsesBudget", attempts: 3,
			results: []transport.Result{rChanged(), rHeadered(503, transport.NextRetry), rOK()}, wantOn: []string{"e1", "e4", "e4"}, wantErr: -1, wantRuns: 1,
		},
		{
			name: "ChangedVersionUsesBudget under one attempt", attempts: 1,
			results: []transport.Result{rChanged()}, wantOn: []string{"e1"}, wantErr: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// Results are shared values; MoveBound and RefusedBound repeat one, so
				// the returned error is compared with that result's.
				results := slices.Clone(tc.results)
				s := &script{results: results, start: time.Now()}
				m := &scriptedMover{installed: slices.Clone(tc.installed), fromRun: fromRun, runErr: tc.runErr, start: s.start}
				var d *transport.Detection
				var mv *mover
				if !tc.probe {
					d = own
				}
				if !tc.probe && !tc.mutation {
					mv = m.mover()
				}
				b := budget{logger: slog.New(slog.DiscardHandler), app: AppSonarr, base: time.Second, attempts: tc.attempts}
				err := loop(t.Context(), b, d, tc.ran, s.send, mv)
				var want error
				switch tc.wantErr {
				case -1:
				case -2:
					want = runErr
				default:
					want = tc.results[tc.wantErr].Err
				}
				if err != want {
					t.Errorf("error = %v, want %v", err, want)
				}
				if s.overflow {
					t.Fatalf("the loop did not end after %d attempts: %q", maxPasses, s.sentOn)
				}
				if !slices.Equal(s.sentOn, tc.wantOn) {
					t.Errorf("attempts sent on %q, want %q", s.sentOn, tc.wantOn)
				}
				if s.sends > tc.attempts {
					t.Errorf("%d sends, over the budget of %d", s.sends, tc.attempts)
				}
				if m.runs != tc.wantRuns || m.reuses != tc.wantReuse {
					t.Errorf("runs %d, reuses %d; want %d and %d", m.runs, m.reuses, tc.wantRuns, tc.wantReuse)
				}
			})
		})
	}
}

// TestAttemptLoopPinned runs the budget table with a pinned read's mover: a move may
// land only on a detection of the pin's Identity, and one of another version ends
// the call with accept's error before anything is sent on it.
func TestAttemptLoopPinned(t *testing.T) {
	pin, other, _ := loopDetections()
	same := func(epoch uint64) *transport.Detection {
		return &transport.Detection{Epoch: epoch, Identity: pin.Identity}
	}
	refused := changedErr("5.0.0.1")
	type tcase struct {
		name      string
		installed []*transport.Detection
		fromRun   *transport.Detection
		results   []transport.Result
		wantOn    []string
		wantErr   int // index of the result whose error the call returns; -1 for nil, -2 for accept's
		wantRuns  int
		wantReuse int
	}
	tests := []tcase{
		{
			name: "L2 a headered 503 retries on the pin", fromRun: same(4),
			results: []transport.Result{rHeadered(503, transport.NextRetry), rOK()}, wantOn: []string{"e1", "e1"}, wantErr: -1,
		},
		{
			name: "L3 its own header-less answer runs a detection of the pin's version", fromRun: same(4),
			results: []transport.Result{rHeaderless(2), rOK()}, wantOn: []string{"e1", "e4"}, wantErr: -1, wantRuns: 1,
		},
		{
			name: "L3 a run of another version ends the call", fromRun: other,
			results: []transport.Result{rHeaderless(2)}, wantOn: []string{"e1"}, wantErr: -2, wantRuns: 1,
		},
		{
			name: "L5 after a run of the pin's version its own header-less answer retries under the self mark", fromRun: same(4),
			results: []transport.Result{rChanged(), rHeaderless(5), rOK()}, wantOn: []string{"e1", "e4", "e4@5"}, wantErr: -1, wantRuns: 1,
		},
		{
			name: "L6 a refused send reuses an installed detection of the pin's version", installed: []*transport.Detection{same(3)}, fromRun: same(4),
			results: []transport.Result{rRefused(""), rOK()}, wantOn: []string{"e1", "e3"}, wantErr: -1, wantReuse: 1,
		},
		{
			name: "L6 an installed detection of another version ends the call with no run", installed: []*transport.Detection{other}, fromRun: same(4),
			results: []transport.Result{rChanged()}, wantOn: []string{"e1"}, wantErr: -2, wantReuse: 1,
		},
		{
			name: "L6 with none installed a run of another version ends the call", fromRun: other,
			results: []transport.Result{rChanged()}, wantOn: []string{"e1"}, wantErr: -2, wantRuns: 1,
		},
		{
			name: "L7 after a reuse the run must still be of the pin's version", installed: []*transport.Detection{same(3)}, fromRun: other,
			results: []transport.Result{rRefused(""), rChanged()}, wantOn: []string{"e1", "e3"}, wantErr: -2, wantRuns: 1, wantReuse: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := &script{results: slices.Clone(tc.results), start: time.Now()}
				m := &scriptedMover{installed: slices.Clone(tc.installed), fromRun: tc.fromRun, start: s.start}
				mv := m.mover()
				mv.accept = func(d *transport.Detection) error {
					if d.Identity == pin.Identity {
						return nil
					}
					return refused
				}
				b := budget{logger: slog.New(slog.DiscardHandler), app: AppSonarr, base: time.Second, attempts: 3}
				err := loop(t.Context(), b, pin, false, s.send, mv)
				var want error
				switch tc.wantErr {
				case -1:
				case -2:
					want = refused
				default:
					want = tc.results[tc.wantErr].Err
				}
				if err != want {
					t.Errorf("error = %v, want %v", err, want)
				}
				if s.overflow {
					t.Fatalf("the loop did not end after %d attempts: %q", maxPasses, s.sentOn)
				}
				if !slices.Equal(s.sentOn, tc.wantOn) {
					t.Errorf("attempts sent on %q, want %q", s.sentOn, tc.wantOn)
				}
				if m.runs != tc.wantRuns || m.reuses != tc.wantReuse {
					t.Errorf("runs %d, reuses %d; want %d and %d", m.runs, m.reuses, tc.wantRuns, tc.wantReuse)
				}
			})
		})
	}
}

func TestAttemptLoopWaits(t *testing.T) {
	own, inst, fromRun := loopDetections()
	read := func(t *testing.T, ctx context.Context, attempts int, installed []*transport.Detection, results ...transport.Result) (*script, *scriptedMover, error) {
		t.Helper()
		s := &script{results: results, start: time.Now()}
		m := &scriptedMover{installed: installed, fromRun: fromRun, start: s.start}
		b := budget{logger: slog.New(slog.DiscardHandler), app: AppSonarr, base: time.Second, attempts: attempts}
		return s, m, loop(ctx, b, own, false, s.send, m.mover())
	}
	t.Run("a 429's Retry-After", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := rHeadered(429, transport.NextRetry)
			r.Err = statusErr(429, 2*time.Second)
			s, _, err := read(t, t.Context(), 3, nil, r, rOK())
			if err != nil || s.at[1] != 2*time.Second {
				t.Errorf("second attempt at %v (%v), want 2s", s.at, err)
			}
		})
	})
	t.Run("the k-th backoff doubles the base k-1 times", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := rHeadered(503, transport.NextRetry)
			s, _, err := read(t, t.Context(), 4, nil, r, r, r, rOK())
			if err != nil {
				t.Fatal(err)
			}
			for k := 1; k <= 3; k++ {
				wait := s.at[k] - s.at[k-1]
				lo, hi := time.Second<<(k-1)/2, time.Second<<(k-1)
				if wait < lo || wait > hi {
					t.Errorf("wait %d = %v, want within [%v, %v]", k, wait, lo, hi)
				}
			}
		})
	})
	t.Run("no wait before a move after another version or a refused send", func(t *testing.T) {
		for name, r := range map[string]transport.Result{"changed": rChanged(), "refused": rRefused("")} {
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					s, _, err := read(t, t.Context(), 3, []*transport.Detection{inst}, r, rOK())
					if err != nil || s.at[1] != 0 {
						t.Errorf("moved attempt at %v (%v), want at once", s.at, err)
					}
				})
			})
		}
	})
	t.Run("outside header-less evidence lends no wait", func(t *testing.T) {
		outside := func() transport.Result {
			r := rHeaderless(0)
			r.Err = statusErr(503, 60*time.Second)
			return r
		}
		for _, tc := range []struct {
			name      string
			installed []*transport.Detection
			ran       bool
		}{
			{"Done when the call ran and none is installed", nil, true},
			{"a reuse", []*transport.Detection{inst}, true},
			{"a run", nil, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					start := time.Now()
					s := &script{results: []transport.Result{outside(), rOK()}, start: start}
					m := &scriptedMover{installed: slices.Clone(tc.installed), fromRun: fromRun, start: start}
					b := budget{logger: slog.New(slog.DiscardHandler), app: AppSonarr, base: time.Second, attempts: 3}
					_ = loop(t.Context(), b, own, tc.ran, s.send, m.mover())
					if elapsed := time.Since(start); elapsed != 0 {
						t.Errorf("the call ended after %v, want at once (attempts at %v)", elapsed, s.at)
					}
				})
			})
		}
	})
	t.Run("WaitBeforeMove", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := rHeaderless(2)
			r.Err = statusErr(502, 3*time.Second)
			_, m, err := read(t, t.Context(), 3, nil, r, rOK())
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(m.currentAt, []time.Duration{3 * time.Second}) || !slices.Equal(m.runAt, []time.Duration{3 * time.Second}) {
				t.Errorf("detection drawn at %v and run at %v, want both after the 3s wait", m.currentAt, m.runAt)
			}
		})
	})
	t.Run("ChangedRetryAfterNoWait", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := rChanged()
			r.Err = &arrapi.ServerChangedError{Origin: base, From: v4, To: "5.0.0.1", Status: statusErr(503, 30*time.Second)}
			s, _, err := read(t, t.Context(), 3, nil, r, rHeadered(503, transport.NextRetry), rOK())
			if err != nil {
				t.Fatal(err)
			}
			if s.at[1] != 0 || s.at[2]-s.at[1] > time.Second {
				t.Errorf("attempts at %v, want the move at once and the next wait a first backoff", s.at)
			}
		})
	})
	t.Run("a context ended before an outside move", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		s, m, err := read(t, ctx, 3, []*transport.Detection{inst}, rChanged(), rOK())
		if !errors.Is(err, context.Canceled) || len(s.at) != 1 || m.runs != 0 || m.reuses != 0 {
			t.Errorf("error = %v after %d attempts, %d runs, %d reuses; want ctx.Err() after one and no move",
				err, len(s.at), m.runs, m.reuses)
		}
	})
	t.Run("a context ending during a wait", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
			defer cancel()
			r := rHeadered(429, transport.NextRetry)
			r.Err = statusErr(429, 2*time.Second)
			s, _, err := read(t, ctx, 3, nil, r, rOK())
			if !errors.Is(err, context.DeadlineExceeded) || len(s.at) != 1 {
				t.Errorf("error = %v after %d attempts, want ctx.Err() after one", err, len(s.at))
			}
		})
	})
}

func TestAttemptLoopRecords(t *testing.T) {
	own, inst, fromRun := loopDetections()
	read := func(t *testing.T, attempts int, ran bool, installed []*transport.Detection, results ...transport.Result) *recorder {
		t.Helper()
		rec := &recorder{}
		s := &script{results: results, start: time.Now()}
		m := &scriptedMover{installed: installed, fromRun: fromRun}
		b := budget{logger: slog.New(rec), app: AppSonarr, base: time.Millisecond, attempts: attempts}
		_ = loop(t.Context(), b, own, ran, s.send, m.mover())
		return rec
	}
	t.Run("a retry, then a recovery", func(t *testing.T) {
		rec := read(t, 3, false, nil, rHeaderless(2), rOK())
		retry := rec.find("arrapi failed, retrying")
		if len(retry) != 1 || retry[0]["fresh"] != "true" || retry[0]["level"] != "DEBUG" || retry[0]["max"] != "3" {
			t.Errorf("retry records = %v", retry)
		}
		if got := rec.find("arrapi succeeded after retry"); len(got) != 1 || got[0]["attempts"] != "2" {
			t.Errorf("recovery records = %v", got)
		}
	})
	t.Run("exhaustion at Warn above a budget of 1", func(t *testing.T) {
		r := rHeadered(503, transport.NextRetry)
		rec := read(t, 3, false, nil, r, r, r)
		got := rec.find("arrapi retries exhausted")
		if len(got) != 1 || got[0]["level"] != "WARN" || got[0]["attempts"] != "3" || got[0]["bound"] != "attempts" {
			t.Errorf("exhaustion records = %v", got)
		}
	})
	t.Run("exhaustion at Debug under a budget of 1", func(t *testing.T) {
		rec := read(t, 1, false, nil, rHeadered(503, transport.NextRetry))
		got := rec.find("arrapi retries exhausted")
		if len(got) != 1 || got[0]["level"] != "DEBUG" || got[0]["attempts"] != "1" {
			t.Errorf("exhaustion records = %v", got)
		}
	})
	t.Run("RanThenChangedNoneInstalled names the detection bound", func(t *testing.T) {
		rec := read(t, 3, true, nil, rChanged())
		got := rec.find("arrapi retries exhausted")
		if len(got) != 1 || got[0]["attempts"] != "1" || got[0]["bound"] != "detection" {
			t.Errorf("exhaustion records = %v", got)
		}
	})
	t.Run("a server change is recorded only when To is a version", func(t *testing.T) {
		rec := read(t, 3, true, []*transport.Detection{inst}, rChanged(), rOK())
		if got := rec.find("arrapi: server version changed"); len(got) != 1 || got[0]["from"] != v4 || got[0]["to"] != "5.0.0.1" ||
			got[0]["app"] != AppSonarr || got[0]["level"] != "INFO" {
			t.Errorf("Changed: records = %v", got)
		}
		rec = read(t, 3, true, []*transport.Detection{inst}, rRefused("5.0.0.1"), rOK())
		if got := rec.find("arrapi: server version changed"); len(got) != 1 {
			t.Errorf("Refused with a version: records = %v", got)
		}
		rec = read(t, 3, true, []*transport.Detection{inst}, rHeaderless(2), rRefused(""), rOK())
		if got := rec.find("arrapi: server version changed"); len(got) != 0 {
			t.Errorf("self-marked retry refused with To \"\": records = %v", got)
		}
	})
}
