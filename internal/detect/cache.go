package detect

import (
	"context"
	"sync"

	"github.com/cplieger/arrapi/v3/internal/transport"
)

// cache shares one lazy detection run among concurrent callers. It stores no result:
// the client's State holds the installed detection, which lasts until an answer
// contradicts it.
type cache struct {
	t      *transport.Client
	flight *flight // the one in-flight lazy run; guarded by mu, never held across I/O
	app    string
	mu     sync.Mutex
}

// flight is one lazy run, published to its waiters when done closes.
type flight struct {
	det      *transport.Detection
	err      error
	done     chan struct{}
	anchor   uint64
	anchored bool
	canceled bool // the leader's context had ended
}

// newCache returns the single-flight cache of client t expecting app.
func newCache(t *transport.Client, app string) *cache { return &cache{t: t, app: app} }

// get returns the installed detection while it is current, else the detection of a
// run it led or joined, with ran true.
func (c *cache) get(ctx context.Context) (d *transport.Detection, ran bool, err error) {
	if cur := c.t.Current(); cur != nil {
		return cur, false, nil
	}
	d, err = c.run(ctx)
	return d, true, err
}

// run leads or joins the one in-flight lazy run, whatever is installed, and installs
// its detection. A waiter whose leader's context ended, or whose run was doomed when
// it joined, leads or joins the next run once, never a run beside it; that next
// run's outcome is final.
func (c *cache) run(ctx context.Context) (*transport.Detection, error) {
	for again := true; ; again = false {
		c.mu.Lock()
		f := c.flight
		if f == nil {
			f = &flight{done: make(chan struct{})}
			c.flight = f
			c.mu.Unlock()
			return c.lead(ctx, f)
		}
		joined := c.t.State().Epoch
		c.mu.Unlock()
		select {
		case <-f.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		// A run anchored before the epoch the waiter read could not pass its end
		// check, since the epoch only grows: its failure says nothing about the
		// server the waiter asked about.
		doomed := f.anchored && f.anchor < joined
		if f.err == nil || !again || (!f.canceled && !doomed) {
			return f.det, f.err
		}
	}
}

func (c *cache) lead(ctx context.Context, f *flight) (*transport.Detection, error) {
	r := transport.NewProbeRun()
	d, err := walkArms(ctx, c.t, r, c.app, false)
	if err == nil {
		c.t.Install(d)
	}
	f.det, f.err = d, err
	f.anchor, f.anchored = r.Anchor()
	f.canceled = ctx.Err() != nil
	c.mu.Lock()
	c.flight = nil
	c.mu.Unlock()
	close(f.done)
	return d, err
}

// fresh runs a detection of its own, not shared, and installs it; once gives each
// probe one attempt.
func (c *cache) fresh(ctx context.Context, once bool) (*transport.Detection, error) {
	d, err := walkArms(ctx, c.t, transport.NewProbeRun(), c.app, once)
	if err == nil {
		c.t.Install(d)
	}
	return d, err
}
