package transport

import (
	"log/slog"
	"net/http"
	"time"
)

// Config is the resolved client configuration the service packages' options fill.
// New applies the defaults and clamps; the zero value is valid.
type Config struct {
	HTTPClient  *http.Client  // nil: the default pooled client with the redirect policy of redirect.go
	Logger      *slog.Logger  // nil: slog.Default()
	MaxAttempts int           // < 1: the default 3
	BaseDelay   time.Duration // <= 0: 1s
	Timeout     time.Duration // <= 0: 120s
}

const (
	defaultMaxAttempts = 3
	defaultBaseDelay   = time.Second
	// defaultTimeout bounds one attempt, its body read included, when the caller's
	// context has no deadline; generous enough for a full library list.
	defaultTimeout = 120 * time.Second
)

func (cfg Config) resolved() Config {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = defaultBaseDelay
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	return cfg
}
