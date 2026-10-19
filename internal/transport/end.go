package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/httpx/v5"
)

// Meta is what a probe's caller needs beyond the decoded body.
type Meta struct {
	AppVersion string // the answer's well-formed X-Application-Version; "" when absent, repeated or malformed
	Major      int    // AppVersion's major; 0 when AppVersion is ""
}

// attempt is one request as a public method asked for it.
type attempt struct {
	out    any
	det    *Detection
	run    *ProbeRun
	path   string // with its query
	shown  string // the path every error and the State carry, without the query
	method string
	body   []byte
	limit  int64
	call   Call
}

// Probe sends one detection GET for run r and decodes a 1 MiB answer into out when
// Decide accepts it, judged against the run's running Identity. A probe path can
// embed a server's token, so the path its errors and the State carry is presented.
func (c *Client) Probe(ctx context.Context, r *ProbeRun, path string, out any) (Meta, Result) {
	shown := c.Present(stripQuery(path), ServerTextBytes)
	return c.send(ctx, &attempt{call: CallProbe, run: r, path: path, shown: shown, method: http.MethodGet, limit: MaxObjectBytes, out: out})
}

// GetJSON sends one attempt and decodes into out only when Decide accepts it; on any other verdict out is left untouched.
func (c *Client) GetJSON(ctx context.Context, d *Detection, path string, limit int64, out any) Result {
	_, r := c.send(ctx, &attempt{call: CallRead, det: d, path: path, shown: stripQuery(path), method: http.MethodGet, limit: limit, out: out})
	return r
}

// GetByID is GetJSON for one resource by id (1 MiB); VerdictAbsent, with a nil Err and out untouched, means the item is gone.
func (c *Client) GetByID(ctx context.Context, d *Detection, path string, out any) Result {
	_, r := c.send(ctx, &attempt{call: CallReadByID, det: d, path: path, shown: stripQuery(path), method: http.MethodGet, limit: MaxObjectBytes, out: out})
	return r
}

// Post sends one command attempt with body as JSON, through the client that follows no
// redirect; any 2xx Decide attributes is its success, and its body is not decoded.
func (c *Client) Post(ctx context.Context, d *Detection, path string, body any) Result {
	data, err := json.Marshal(body)
	if err != nil {
		return Result{Err: fmt.Errorf("arrapi: encode %s: %w", stripQuery(path), err), Verdict: VerdictTransport, Next: NextDone}
	}
	_, r := c.send(ctx, &attempt{call: CallMutation, det: d, path: path, shown: stripQuery(path), method: http.MethodPost, body: data})
	return r
}

func stripQuery(path string) string {
	p, _, _ := strings.Cut(path, "?")
	return p
}

func (c *Client) send(ctx context.Context, a *attempt) (Meta, Result) {
	t := newTag(a.shown, a.det, a.run)
	actx, cancel := httpx.ContextWithDefaultTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	var body io.Reader = http.NoBody
	if a.body != nil {
		body = bytes.NewReader(a.body)
	}
	req, err := http.NewRequestWithContext(withTag(actx, t), a.method, c.base+a.path, body)
	if err != nil {
		// net/url's parse error quotes the whole URL, the base's user info included.
		err = c.leaving(fmt.Errorf("arrapi: build request %s: %w", t.path, httpx.LogSafeError(err)))
		return Meta{}, Result{Err: err, Verdict: VerdictTransport, Next: NextDone}
	}
	req.Header.Set(headerAPIKey, string(c.key))
	req.Header.Set("User-Agent", userAgent)
	client := c.data
	if a.call == CallMutation {
		req.Header.Set("Content-Type", "application/json")
		client = c.cmd
	}

	resp, doErr := client.Do(req) //nolint:bodyclose // every verdict arm below reads, captures or discards it
	end, resp, endErr := c.endAttempt(ctx, actx, t, resp, doErr)
	var meta Meta
	if end.Kind == EndAnswer {
		meta.AppVersion, meta.Major, _ = ParseAppVersion(end.Header)
	}
	sent := t.sent()
	verdict, next := Decide(sent, a.call, end)
	r := Result{Verdict: verdict, Next: next, Sent: t.left, SelfMark: t.heldSelfMark()}
	switch verdict {
	case VerdictAccept:
		if a.call == CallMutation {
			discardBody(resp)
			break
		}
		readErr, err := c.readInto(resp.Body, a.limit, t.path, a.out)
		if readErr != nil {
			safe := markAttemptTimeout(ctx, actx, c.safeDoError(t.path, readErr))
			r.Verdict, r.Next = Decide(sent, a.call, End{Kind: EndTransport, Transient: transient(ctx, safe)})
			err = safe
		}
		r.Err = err
	case VerdictAbsent:
		discardBody(resp)
	case VerdictStatus, VerdictUnidentified:
		r.Err = c.capture(end.Code, resp, t.path)
	case VerdictChanged:
		r.Err = c.changed(sent, end, resp, t.path, isSuccess(a.call, end.Code))
	case VerdictNoHeader:
		discardBody(resp)
		r.Err = noHeader(sent, t.path)
	case VerdictUnconfirmed:
		discardBody(resp)
		cause := error(noHeader(sent, t.path))
		if _, _, ok := ParseAppVersion(end.Header); ok {
			cause = c.changed(sent, end, nil, t.path, true)
		}
		r.Err = fmt.Errorf("%w: %w", arrapi.ErrCommandOutcomeUnknown, cause)
	case VerdictRefused, VerdictTransport:
		r.Err = endErr
	}
	return meta, r
}

// changed builds the error of an answer from another server version: a failure is
// captured into Status, a success's body drained. resp nil leaves no body.
func (c *Client) changed(sent Identity, end End, resp *http.Response, path string, success bool) *arrapi.ServerChangedError {
	v, _, _ := ParseAppVersion(end.Header)
	e := &arrapi.ServerChangedError{
		Origin: c.origin,
		Path:   path,
		From:   c.Present(sent.AppVersion, ServerTextBytes),
		To:     c.Present(v, ServerTextBytes),
	}
	switch {
	case !success:
		e.Status = c.capture(end.Code, resp, path)
	case resp != nil:
		discardBody(resp)
	}
	return e
}

// isSuccess is Decide's success: exactly 200 to a GET, any 2xx to a command.
func isSuccess(call Call, code int) bool {
	if call == CallMutation {
		return code >= 200 && code <= 299
	}
	return code == http.StatusOK
}

func noHeader(sent Identity, path string) *arrapi.UnsupportedAPIError {
	return &arrapi.UnsupportedAPIError{Reason: arrapi.ReasonNoVersionHeader, App: sent.App, Want: sent.App, Path: path}
}

// heldSelfMark is the self mark the attempt holds at its end, 0 when none.
func (t *tag) heldSelfMark() uint64 {
	if t.holdsSelfMark() {
		return t.selfMark
	}
	return 0
}

// endAttempt selects the attempt's terminal outcome. A header-less recorded 3xx the
// attempt ended on was its last answer, so it marks the detection stale as the
// observer marks a header-less answer, except for an unanchored run, whose arms
// judge its answers.
func (c *Client) endAttempt(ctx, actx context.Context, t *tag, resp *http.Response, err error) (End, *http.Response, error) {
	if err == nil {
		if isFollowable(resp.StatusCode) {
			c.markRecorded(t)
		}
		return End{Kind: EndAnswer, Code: resp.StatusCode, Header: resp.Header}, resp, nil
	}
	if ns, ok := errors.AsType[*notSentError](err); ok {
		return End{Kind: EndNotSent}, nil, ns.err
	}
	safe := c.safeDoError(t.path, err)
	switch {
	case resp != nil:
		// net/http returns the closed 3xx with the policy's refusal, which is never
		// worth sending again.
		c.markRecorded(t)
		return End{Kind: EndTransport}, nil, safe
	case t.rec != nil && !t.rec.forwarded:
		// net/http could not parse Location and discarded the redirect before the
		// policy saw it: the recorded 3xx is the answer.
		c.markRecorded(t)
		return End{Kind: EndAnswer, Code: t.rec.code, Header: t.rec.header}, nil, nil
	}
	marked := markAttemptTimeout(ctx, actx, safe)
	end := End{Kind: EndTransport, Transient: transient(ctx, marked)}
	if t.rec != nil && c.markRecorded(t) {
		end.HopFailed = true
	}
	return end, nil, marked
}

// markRecorded marks the detection stale for a header-less recorded 3xx and reports
// whether it did.
func (c *Client) markRecorded(t *tag) bool {
	if t.rec == nil || t.unanchoredProbe() {
		return false
	}
	if _, _, ok := ParseAppVersion(t.rec.header); ok {
		return false
	}
	c.markHeaderless(t)
	return true
}

// markAttemptTimeout marks an expiry of the per-attempt bound under a live caller
// context, which httpx cannot tell from the caller's own deadline.
func markAttemptTimeout(ctx, actx context.Context, err error) error {
	if actx.Err() != nil && ctx.Err() == nil {
		return httpx.AttemptTimeout(err)
	}
	return err
}

// transient is httpx's verdict on a marked error, except that once the caller's
// context has ended no error is transient, whatever it surfaced as.
func transient(ctx context.Context, marked error) bool {
	return ctx.Err() == nil && httpx.IsTransient(marked)
}
