package transport

import "github.com/cplieger/arrapi/v3"

// State is the client's knowledge of the server. It is immutable: every change stores
// a new one. Det != nil implies Det.Epoch == Epoch and Det.Identity.AppVersion ==
// AppVersion, so the store that advances the epoch also drops the detection.
type State struct {
	Det        *Detection // the detection drawn in this epoch; nil until one is
	AppVersion string     // the well-formed X-Application-Version of the answer that last advanced Epoch; "" when it had none
	Path       string     // that call's path as its errors carry it, never a redirect target's
	Epoch      uint64     // advanced by every answer that contradicts the State before it
}

// Detection is one completed detection, usable only while State.Epoch equals its Epoch.
type Detection struct {
	Identity   Identity // App and the status answer's full X-Application-Version
	API        arrapi.API
	Version    string // status "version" in its presentation form; display only
	Epoch      uint64
	selfMark   uint64
	Deprecated bool
}

// UnderSelfMark returns a copy of d that the send check admits only while State.Epoch
// == m, the self mark the call's own header-less answer stored. Install refuses the copy.
func (d *Detection) UnderSelfMark(m uint64) *Detection {
	cp := *d
	cp.selfMark = m
	return &cp
}

// SelfMark returns the self mark an UnderSelfMark copy is admitted at; 0 for a
// detection as drawn.
func (d *Detection) SelfMark() uint64 { return d.selfMark }

// ProbeRun tags the probes of one detection run. Only the run's own sequential probes
// write it.
type ProbeRun struct {
	identity Identity
	answered string
	epoch    uint64
	anchored bool
}

// NewProbeRun returns an unanchored run: its first identified answer anchors it.
func NewProbeRun() *ProbeRun { return &ProbeRun{} }

// AnchoredRun returns a run anchored at d's Identity and epoch, for reads judged as
// probes on a detection that already exists.
//
//deadset:ignore DS1004 -- read only by the fixture recorder, internal/fixturegen and the sonarr and radarr record tests
func AnchoredRun(d *Detection) *ProbeRun {
	return &ProbeRun{identity: d.Identity, epoch: d.Epoch, anchored: true}
}

// Anchor reports the epoch the run anchored at, ok false when no answer identified a
// server. Read it only after the run returned.
func (r *ProbeRun) Anchor() (epoch uint64, ok bool) { return r.epoch, r.anchored }

// Answered returns the path of the run's last probe that received any answer, a 3xx
// or a header-less one included; "" when none did. Read it only after the run returned.
func (r *ProbeRun) Answered() string { return r.answered }

// State returns the current State.
func (c *Client) State() *State { return c.state.Load() }

// Current returns the installed detection while it is current, else nil.
func (c *Client) Current() *Detection { return c.state.Load().Det }

// Install stores d only into its own epoch's empty Det, never an UnderSelfMark copy;
// it never changes Epoch, so the first of two detections drawn in one epoch stays.
func (c *Client) Install(d *Detection) {
	if d == nil || d.selfMark != 0 {
		return
	}
	for {
		s := c.state.Load()
		if s.Epoch != d.Epoch || s.Det != nil || s.AppVersion != d.Identity.AppVersion {
			return
		}
		if c.state.CompareAndSwap(s, &State{Epoch: s.Epoch, AppVersion: s.AppVersion, Path: s.Path, Det: d}) {
			return
		}
	}
}

// MarkUnknown records that a detection failed after it received an answer: it always
// advances the epoch, even from AppVersion "", because another call may hold a self
// mark at that epoch and a failed detection is evidence from outside that call.
func (c *Client) MarkUnknown(path string) {
	for {
		s := c.state.Load()
		if c.state.CompareAndSwap(s, &State{Epoch: s.Epoch + 1, Path: path}) {
			return
		}
	}
}

// ChangedError builds the error of a refused send or a failed end check against the
// State s it compared. To is s.AppVersion only when that is a version other than from:
// a State naming from again shows no other version, only that from is unconfirmed.
func (c *Client) ChangedError(s *State, from string) *arrapi.ServerChangedError {
	e := &arrapi.ServerChangedError{Origin: c.origin, Path: s.Path, From: c.Present(from, ServerTextBytes)}
	if s.AppVersion != "" && s.AppVersion != from {
		e.To = c.Present(s.AppVersion, ServerTextBytes)
	}
	return e
}
