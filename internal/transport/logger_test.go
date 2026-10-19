package transport_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/cplieger/arrapi/v3/internal/transport"
)

func TestConfigLogger(t *testing.T) {
	own := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name string
		in   *slog.Logger
		want *slog.Logger
	}{
		{"nil is slog.Default", nil, slog.Default()},
		{"a given logger is kept", own, own},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := transport.New("http://sonarr:8989", testKey, transport.Config{Logger: tc.in})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer c.Close()
			if got := c.Config().Logger; got != tc.want {
				t.Errorf("Config().Logger = %p, want %p", got, tc.want)
			}
		})
	}
}
