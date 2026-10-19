// Package detect finds which API generation a server serves and keeps every call on
// a detection that still names it. walkArms walks the detection arms; cache shares one
// lazy run among concurrent callers; loop is the one attempt loop, retrying or moving
// a call to another detection within one budget; Session is the only path from a
// public method to an engine.
package detect

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

const (
	pathAPI      = "/api"
	pathV5Status = "/api/v5/system/status"
	pathV3Status = "/api/v3/system/status"
)

// APIDoc is every member of GET /api detection reads.
type APIDoc struct {
	Current    json.RawMessage   `json:"current"`
	Deprecated []json.RawMessage `json:"deprecated"`
}

// StatusDoc is every member of a system/status answer detection reads.
type StatusDoc struct {
	AppName string `json:"appName"`
	Version string `json:"version"`
}

// prober runs one detection's probes, each in its own attempt loop.
type prober struct {
	t   *transport.Client
	r   *transport.ProbeRun
	app string
	b   budget
}

// probeOutcome is how one probe's loop ended.
type probeOutcome struct {
	meta transport.Meta
	err  error // the loop's error: the last attempt's, or the context's during a wait
	last transport.Result
}

// verdictAccept and is404 read the last attempt's verdict. Neither verdict is
// retried, so when either holds, err is that attempt's own error, never the context's.
func (o *probeOutcome) verdictAccept() bool { return o.last.Verdict == transport.VerdictAccept }

func (o *probeOutcome) is404() bool {
	se, ok := errors.AsType[*arrapi.StatusError](o.err)
	return ok && se.Code == http.StatusNotFound &&
		(o.last.Verdict == transport.VerdictStatus || o.last.Verdict == transport.VerdictUnidentified)
}

func (p *prober) probe(ctx context.Context, path string, out any) *probeOutcome {
	o := &probeOutcome{}
	o.err = loop(ctx, p.b, nil, false, func(ctx context.Context, _ *transport.Detection) transport.Result {
		o.meta, o.last = p.t.Probe(ctx, p.r, path, out)
		return o.last
	}, nil)
	return o
}

// walkArms walks the detection arms for app with r, the run's one ProbeRun, and makes the
// end check. Each probe has its own budget, the client's attempts or 1 when once. A
// run that fails after any answer marks the client's server unknown; walkArms installs
// nothing.
func walkArms(ctx context.Context, t *transport.Client, r *transport.ProbeRun, app string, once bool) (*transport.Detection, error) {
	b := budgetOf(t, app)
	if once {
		b.attempts = 1
	}
	p := &prober{t: t, r: r, app: app, b: b}
	d, err := p.walk(ctx)
	if err == nil {
		if s := t.State(); s.Epoch != d.Epoch {
			err = t.ChangedError(s, d.Identity.AppVersion)
			logChangedErr(b, err)
		}
	}
	if err != nil {
		if path := r.Answered(); path != "" {
			t.MarkUnknown(path)
		}
		return nil, err
	}
	b.logger.Debug("arrapi: detected", "app", app, "version", d.Version, "api", string(d.API), "deprecated", d.Deprecated)
	return d, nil
}

func (p *prober) walk(ctx context.Context) (*transport.Detection, error) {
	var doc APIDoc
	o := p.probe(ctx, pathAPI, &doc)
	switch {
	case o.verdictAccept() && o.err == nil:
		var current string
		if json.Unmarshal(doc.Current, &current) == nil {
			if _, ok := generation(current); ok {
				return p.announced(ctx, evidence{current: current, deprecated: wellFormedTokens(doc.Deprecated), announced: true})
			}
		}
		return p.hidden(ctx)
	case o.verdictAccept(), o.is404():
		return p.hidden(ctx)
	}
	return nil, o.err
}

// announced is arm A: the status path of the generation the server announced, so a
// Radarr client pointed at a Sonarr 5 reads /api/v5/system/status and never /api/v3.
func (p *prober) announced(ctx context.Context, ev evidence) (*transport.Detection, error) {
	path := "/api/" + ev.current + "/system/status"
	var st StatusDoc
	o := p.probe(ctx, path, &st)
	// The path holds a server token, so the copy an error carries is presented.
	shown := present(p.t, path)
	if o.is404() {
		if n, _ := generation(ev.current); p.app == AppSonarr && n >= 5 {
			return nil, &arrapi.UnsupportedAPIError{
				Reason: arrapi.ReasonV5Unreachable, Want: p.app, Path: shown,
				Current: present(p.t, ev.current), Deprecated: presentAll(p.t, ev.deprecated),
			}
		}
		return nil, o.err
	}
	return p.status(o, st, shown, ev)
}

// hidden is arms B and C: /api is not readable, so the status paths decide.
func (p *prober) hidden(ctx context.Context) (*transport.Detection, error) {
	if p.app == AppSonarr {
		var st StatusDoc
		o := p.probe(ctx, pathV5Status, &st)
		if !o.is404() {
			return p.status(o, st, pathV5Status, evidence{current: "v5"})
		}
	}
	// On a Sonarr 5 behind a proxy that blocks /api/v5 this reaches its deprecated
	// /api/v3; only appName and version are read, so the answer can be refused (arm F).
	var st StatusDoc
	o := p.probe(ctx, pathV3Status, &st)
	if o.is404() {
		return nil, o.err
	}
	return p.status(o, st, pathV3Status, evidence{current: "v3"})
}

// status applies the status checks in their fixed order (not the API, wrong
// application, no version header), then the selection rule; path is the status
// path as an error shows it.
func (p *prober) status(o *probeOutcome, st StatusDoc, path string, ev evidence) (*transport.Detection, error) {
	if !o.verdictAccept() {
		return nil, o.err
	}
	refuse := func(reason arrapi.UnsupportedReason) error {
		e := &arrapi.UnsupportedAPIError{
			Reason: reason, Want: p.app, Path: path,
			App: present(p.t, st.AppName), Version: present(p.t, st.Version),
		}
		if ev.announced {
			e.Current, e.Deprecated = present(p.t, ev.current), presentAll(p.t, ev.deprecated)
		}
		return e
	}
	switch {
	case o.err != nil || st.AppName == "" || st.Version == "":
		return nil, &arrapi.UnsupportedAPIError{Reason: arrapi.ReasonNotAPI, Want: p.app, Path: path}
	case !asciiEqualFold(st.AppName, p.app):
		return nil, refuse(arrapi.ReasonWrongApp)
	case o.meta.AppVersion == "":
		return nil, refuse(arrapi.ReasonNoVersionHeader)
	}
	sel, reason := selectEngine(p.app, ev, o.meta.Major)
	if reason != 0 {
		return nil, refuse(reason)
	}
	epoch, _ := p.r.Anchor()
	return &transport.Detection{
		Identity:   transport.Identity{App: p.app, AppVersion: o.meta.AppVersion},
		Epoch:      epoch,
		API:        sel.api,
		Deprecated: sel.deprecated,
		Version:    present(p.t, st.Version),
	}, nil
}
