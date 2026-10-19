package transport_test

import (
	"net/http"
	"sync"
	"testing"

	"github.com/cplieger/arrapi/v3/internal/transport"
)

func TestConcurrentReadsAreSafe(t *testing.T) {
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{})
	d := detect(t, c, f, v4)
	f.set(reply(200, v4, `[{"id":1}]`))
	const n = 16
	var wg sync.WaitGroup
	results := make([][]struct{ ID int }, n)
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			errs[i] = c.GetJSON(t.Context(), d, "/api/v3/series", transport.MaxListBytes, &results[i]).Err
		})
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil || len(results[i]) != 1 {
			t.Errorf("caller %d: %v, %d items", i, errs[i], len(results[i]))
		}
	}
	if c.Current() != d {
		t.Error("agreeing concurrent answers dropped the detection")
	}
}

func TestConcurrentContradictionsKeepTheInvariant(t *testing.T) {
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{})
	d := detect(t, c, f, v4)
	f.set(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") == "none" {
			reply(500, "", ``)(w, r)
			return
		}
		reply(200, v5, `[]`)(w, r)
	})
	const n = 32
	var wg sync.WaitGroup
	for i := range n {
		path := "/api/v3/series?v=five"
		if i%2 == 0 {
			path = "/api/v3/series?v=none"
		}
		wg.Go(func() {
			var out any
			c.GetJSON(t.Context(), d, path, transport.MaxListBytes, &out)
			c.Install(d)
		})
	}
	wg.Wait()
	s := c.State()
	if s.Epoch <= d.Epoch || s.Det != nil && (s.Det.Epoch != s.Epoch || s.Det.Identity.AppVersion != s.AppVersion) {
		t.Errorf("State = %+v breaks Det.Epoch == Epoch and Det's version == AppVersion", s)
	}
}
