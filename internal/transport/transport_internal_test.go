package transport

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestDefaultClientShape(t *testing.T) {
	c := internalClient(t, "test-key")
	for name, hc := range map[string]*http.Client{"data": c.data, "cmd": c.cmd} {
		if hc.Timeout != 0 {
			t.Errorf("%s client Timeout = %v, want 0: each attempt's context bounds it", name, hc.Timeout)
		}
		obs, ok := hc.Transport.(*observer)
		if !ok {
			t.Fatalf("%s client Transport is %T, want the observer", name, hc.Transport)
		}
		pool, ok := obs.next.(*http.Transport)
		if !ok || pool.MaxIdleConns != 20 || pool.MaxIdleConnsPerHost != 5 || pool.IdleConnTimeout != 90*time.Second {
			t.Errorf("%s client pool = %#v, want the pooled transport", name, obs.next)
		}
	}
	if c.data.Transport != c.cmd.Transport {
		t.Error("data and cmd clients do not share one observer")
	}
	req := &http.Request{URL: &url.URL{Scheme: "http", Host: "sonarr:8989", Path: "/x"}}
	via := []*http.Request{{URL: &url.URL{Scheme: "http", Host: "sonarr:8989", Path: "/api/v3/series"}}}
	if err := c.data.CheckRedirect(req, via); err != nil {
		t.Errorf("data client refused a same-origin redirect: %v", err)
	}
	if err := c.cmd.CheckRedirect(req, via); err != http.ErrUseLastResponse {
		t.Errorf("cmd client CheckRedirect = %v, want http.ErrUseLastResponse", err)
	}
}

func TestCallerClientCopiesKeepItsPolicy(t *testing.T) {
	var asked int
	own := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { asked++; return nil }}
	c, err := New("http://sonarr:8989", "test-key", Config{HTTPClient: own})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	via := []*http.Request{{URL: &url.URL{Scheme: "http", Host: "sonarr:8989", Path: "/api/v3/series"}}}
	same := &http.Request{URL: &url.URL{Scheme: "http", Host: "sonarr:8989", Path: "/x"}}
	if err := c.data.CheckRedirect(same, via); err != nil || asked != 1 {
		t.Errorf("data client did not keep the caller's policy (err %v, asked %d)", err, asked)
	}
	other := &http.Request{URL: &url.URL{Scheme: "http", Host: "elsewhere:8989", Path: "/x"}}
	if err := c.data.CheckRedirect(other, via); err == nil || asked != 1 {
		t.Errorf("data client let the caller's policy follow another origin (err %v, asked %d)", err, asked)
	}
	if obs := c.data.Transport.(*observer); obs.next != http.DefaultTransport {
		t.Errorf("a caller client without a Transport wraps %T, want http.DefaultTransport", obs.next)
	}
}

func TestOriginOf(t *testing.T) {
	tests := map[string]string{
		"http://sonarr:8989/sonarr":      "http://sonarr:8989",
		"https://u:p@Host.example:443/x": "https://Host.example",
		"http://host:80":                 "http://host",
		"HTTPS://host:8443":              "https://host:8443",
		"http://[::1]:8989":              "http://[::1]:8989",
		"http://[::1]":                   "http://[::1]",
	}
	for in, want := range tests {
		u, err := url.Parse(in)
		if err != nil {
			t.Fatalf("parse %q: %v", in, err)
		}
		if got := originOf(u); got != want {
			t.Errorf("originOf(%q) = %q, want %q", in, got, want)
		}
	}
}
