package transport

import (
	"context"
	"errors"
	"net/http"

	"github.com/cplieger/arrapi/v3"
)

// tag rides each attempt's context, which net/http copies onto every redirect hop,
// so the observer can judge every request and response of the attempt. The goroutine
// that called client.Do is the only one that touches it.
type tag struct {
	det      *Detection // a data request's or command's detection
	run      *ProbeRun  // a probe's run
	rec      *redirectRecord
	path     string // the attempt's path as errors and the State carry it (attempt.shown)
	admitted uint64 // the epoch the send check admits the attempt at
	selfMark uint64 // the self mark the attempt holds while it equals admitted
	left     bool   // a request of the attempt was handed to the next RoundTripper
}

// redirectRecord is the attempt's last 301, 302, 303, 307 or 308.
type redirectRecord struct {
	header    http.Header
	code      int
	forwarded bool // its hop was handed to the next RoundTripper
}

type tagKey struct{}

func newTag(path string, det *Detection, run *ProbeRun) *tag {
	t := &tag{det: det, run: run, path: path}
	switch {
	case det != nil && det.selfMark != 0:
		t.admitted, t.selfMark = det.selfMark, det.selfMark
	case det != nil:
		t.admitted = det.Epoch
	case run.anchored:
		t.admitted = run.epoch
	}
	return t
}

func (t *tag) unanchoredProbe() bool { return t.run != nil && !t.run.anchored }

func (t *tag) holdsSelfMark() bool { return t.selfMark != 0 && t.selfMark == t.admitted }

// sent is the Identity the attempt is judged against: its detection's, or its run's
// running Identity.
func (t *tag) sent() Identity {
	if t.det != nil {
		return t.det.Identity
	}
	return t.run.identity
}

// observer sees every response, each followed redirect hop included, before net/http's
// redirect loop acts on it, and every request before it is sent; Decide sees only how
// each attempt ended. An answer that contradicts the State advances the epoch and drops
// its detection, and nothing is sent on a detection, or by a detection run after its
// anchor, once the epoch has moved on, except a read's retry at the self mark its own
// header-less answer stored, while nothing else has arrived since.
type observer struct {
	next http.RoundTripper
	c    *Client
}

func (o *observer) RoundTrip(req *http.Request) (*http.Response, error) {
	t, ok := req.Context().Value(tagKey{}).(*tag)
	if !ok {
		// send tags every request; an untagged one would pass the send check unjudged.
		closeBody(req)
		return nil, errUntagged
	}
	if !t.unanchoredProbe() {
		if s := o.c.state.Load(); s.Epoch != t.admitted {
			closeBody(req)
			t.rec = nil
			return nil, &notSentError{err: o.c.ChangedError(s, t.sent().AppVersion)}
		}
	}
	if t.left && t.rec != nil {
		t.rec.forwarded = true
	}
	t.left = true
	resp, err := o.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	o.c.observe(t, resp)
	return resp, nil
}

var errUntagged = errors.New("arrapi: request sent outside an attempt")

// closeBody closes a request body the observer will not send, as http.RoundTripper requires.
func closeBody(req *http.Request) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
}

// CloseIdleConnections reaches the pool of the transport the observer wraps.
func (o *observer) CloseIdleConnections() {
	if ci, ok := o.next.(interface{ CloseIdleConnections() }); ok {
		ci.CloseIdleConnections()
	}
}

// notSentError is the send check's refusal; it never leaves transport.
type notSentError struct{ err *arrapi.ServerChangedError }

func (e *notSentError) Error() string { return e.err.Error() }

func isFollowable(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

func (c *Client) observe(t *tag, resp *http.Response) {
	if t.run != nil {
		t.run.answered = t.path
	}
	followable := isFollowable(resp.StatusCode)
	if followable {
		t.rec = &redirectRecord{header: resp.Header, code: resp.StatusCode}
	} else {
		t.rec = nil
	}
	if v, _, ok := ParseAppVersion(resp.Header); ok {
		c.observeVersion(t, v)
		return
	}
	// A followed redirect is judged by its hop's answer or where its attempt ends, and
	// an unanchored run's answer by the run's arms.
	if followable || t.unanchoredProbe() {
		return
	}
	c.markHeaderless(t)
}

func (c *Client) observeVersion(t *tag, v string) {
	for {
		s := c.state.Load()
		agrees := v == s.AppVersion
		if c.afterCompare != nil {
			c.afterCompare()
		}
		if agrees {
			if t.unanchoredProbe() {
				t.anchor(s.Epoch, v)
			}
			return
		}
		next := &State{Epoch: s.Epoch + 1, AppVersion: v, Path: t.path}
		if !c.state.CompareAndSwap(s, next) {
			continue
		}
		// The run anchors at the State this loop compared or stored, never a reload:
		// a reload could stamp this answer's identity with another server's epoch.
		if t.unanchoredProbe() {
			t.anchor(next.Epoch, v)
			return
		}
		t.stored(s.Epoch, next)
		return
	}
}

// markHeaderless records an answer that names no server: the epoch advances, except
// for a self-marked attempt's own answer, whose call already stored that mark.
func (c *Client) markHeaderless(t *tag) {
	for {
		s := c.state.Load()
		if t.holdsSelfMark() && s.Epoch == t.admitted && s.AppVersion == "" {
			return
		}
		next := &State{Epoch: s.Epoch + 1, Path: t.path}
		if c.state.CompareAndSwap(s, next) {
			t.stored(s.Epoch, next)
			return
		}
	}
}

func (t *tag) anchor(epoch uint64, v string) {
	t.run.anchored, t.run.epoch, t.run.identity.AppVersion = true, epoch, v
	t.admitted = epoch
}

// stored applies the self-mark rule to a store this attempt made over the State at
// epoch prev: a data attempt's store at its admitted epoch of no version, or of its
// detection's own version, moves its admission to the stored epoch; any other store
// leaves the admitted epoch behind, so later hops are refused.
func (t *tag) stored(prev uint64, next *State) {
	if t.det != nil && prev == t.admitted && (next.AppVersion == "" || next.AppVersion == t.det.Identity.AppVersion) {
		t.admitted, t.selfMark = next.Epoch, next.Epoch
		return
	}
	t.selfMark = 0
}

func withTag(ctx context.Context, t *tag) context.Context {
	return context.WithValue(ctx, tagKey{}, t)
}
