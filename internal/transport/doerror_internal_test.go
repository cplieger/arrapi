package transport

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestSafeDoError(t *testing.T) {
	const key = "test-key-7f3a"
	c := internalClient(t, key)
	cause := errors.New("connection reset")
	t.Run("a chain without the key is kept", func(t *testing.T) {
		err := c.safeDoError("/api/v3/series", &url.Error{Op: "Get", URL: "http://sonarr:8989/api/v3/series?apikey=" + key, Err: cause})
		if err.Error() != "arrapi: request /api/v3/series: connection reset" {
			t.Errorf("Error() = %q", err.Error())
		}
		if !errors.Is(err, cause) {
			t.Error("the cause is no longer reachable")
		}
		if _, ok := errors.AsType[*url.Error](err); ok {
			t.Error("the *url.Error survived")
		}
	})
	t.Run("a chain rendering the key is cut", func(t *testing.T) {
		withKey := fmt.Errorf("dial tcp: lookup %s.example: no such host", key)
		err := c.safeDoError("/api/v3/series", &url.Error{Op: "Get", URL: "http://x", Err: withKey})
		if strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "[redacted]") {
			t.Errorf("Error() = %q, want the key redacted", err.Error())
		}
		if errors.Unwrap(err) != nil {
			t.Error("a chain that renders the key was kept")
		}
	})
	t.Run("a key hidden below a wrapper that does not render it", func(t *testing.T) {
		err := c.safeDoError("/api/v3/series", quiet{inner: errors.New("host " + key)})
		if e := treeNodeRendering(err, key); e != nil {
			t.Errorf("tree error %T renders the key", e)
		}
	})
	t.Run("a key below one branch of an error tree", func(t *testing.T) {
		tree := branched{errs: []error{cause, errors.New("X-Api-Key: " + key)}}
		err := c.safeDoError("/api/v3/series", &url.Error{Op: "Get", URL: "http://x", Err: tree})
		if e := treeNodeRendering(err, key); e != nil {
			t.Errorf("tree error %T renders the key", e)
		}
	})
	t.Run("an error tree without the key is kept", func(t *testing.T) {
		tree := branched{errs: []error{cause, errors.New("dial refused")}}
		if err := c.safeDoError("/api/v3/series", tree); !errors.Is(err, cause) {
			t.Errorf("Err = %v, want the tree's branches reachable", err)
		}
	})
}

// quiet renders a fixed text over an inner error that holds more.
type quiet struct{ inner error }

func (quiet) Error() string   { return "request failed" }
func (q quiet) Unwrap() error { return q.inner }

// branched renders a fixed text over several errors that hold more.
type branched struct{ errs []error }

func (branched) Error() string     { return "request failed" }
func (b branched) Unwrap() []error { return b.errs }

// treeNodeRendering returns the first error of err's tree, through both Unwrap
// forms, whose Error() or %+v holds s; nil when none does.
func treeNodeRendering(err error, s string) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), s) || strings.Contains(fmt.Sprintf("%+v", err), s) {
		return err
	}
	switch u := err.(type) {
	case interface{ Unwrap() error }:
		return treeNodeRendering(u.Unwrap(), s)
	case interface{ Unwrap() []error }:
		for _, e := range u.Unwrap() {
			if found := treeNodeRendering(e, s); found != nil {
				return found
			}
		}
	}
	return nil
}
