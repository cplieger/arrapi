package radarr

import (
	"log/slog"
	"net/http"
	"testing"
	"time"
)

func TestOptionsReachTheConfig(t *testing.T) {
	hc := &http.Client{}
	logger := slog.New(slog.DiscardHandler)
	c, err := New(fakeBase, testKey,
		WithHTTPClient(hc), WithMaxAttempts(5), WithBaseDelay(3*time.Second),
		WithTimeout(7*time.Second), WithLogger(logger))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	cfg := c.t.Config()
	if cfg.HTTPClient != hc || cfg.Logger != logger || cfg.MaxAttempts != 5 ||
		cfg.BaseDelay != 3*time.Second || cfg.Timeout != 7*time.Second {
		t.Errorf("Config = %+v, want every option applied", cfg)
	}
}

func TestOptionsNilAndNonPositiveKeepDefaults(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	hc := &http.Client{}
	c, err := New(fakeBase, testKey,
		WithHTTPClient(hc), WithHTTPClient(nil), WithLogger(logger), WithLogger(nil),
		WithMaxAttempts(0), WithBaseDelay(-time.Second), WithTimeout(0))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	cfg := c.t.Config()
	if cfg.HTTPClient != hc || cfg.Logger != logger {
		t.Errorf("a nil HTTP client or logger replaced the one set before it: %+v", cfg)
	}
	if cfg.MaxAttempts != 3 || cfg.BaseDelay != time.Second || cfg.Timeout != 120*time.Second {
		t.Errorf("Config = %+v, want the defaults 3, 1s and 120s", cfg)
	}
}
