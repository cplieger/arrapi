package arrapi

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3/internal/statushook"
	"github.com/cplieger/httpx/v5"
)

func TestStatusErrorRetryAfterHint(t *testing.T) {
	tests := []struct {
		name string
		set  time.Duration
		want time.Duration
	}{
		{"within the cap", 7 * time.Second, 7 * time.Second},
		{"two hours capped", 2 * time.Hour, httpx.RetryAfterCap},
		{"negative is zero", -time.Second, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := &StatusError{Code: 503, Path: "/api/v3/series"}
			statushook.SetRetryAfter(fmt.Errorf("attempt: %w", e), tc.set)
			if got := e.RetryAfterHint(); got != tc.want {
				t.Errorf("RetryAfterHint() after SetRetryAfter(%v) = %v, want %v", tc.set, got, tc.want)
			}
		})
	}
	t.Run("a chain without a StatusError is unchanged", func(t *testing.T) {
		other := &ResponseTooLargeError{Path: "/api/v3/series", Limit: 1}
		err := fmt.Errorf("attempt: %w", other)
		statushook.SetRetryAfter(err, time.Second)
		if other.Path != "/api/v3/series" || other.Limit != 1 {
			t.Errorf("SetRetryAfter changed an unrelated error: %+v", other)
		}
	})
}

func TestStatusErrorRetryAfterHintReturnsTheStoredHint(t *testing.T) {
	e := &StatusError{Code: 503, Path: "/api/v3/series", retryAfter: 7 * time.Second}
	if got := e.RetryAfterHint(); got != 7*time.Second {
		t.Errorf("RetryAfterHint() = %v, want 7s", got)
	}
	hint, ok := errors.AsType[httpx.RetryAfterHint](fmt.Errorf("attempt: %w", e))
	if !ok {
		t.Fatal("errors.As(httpx.RetryAfterHint) found nothing through a wrapper")
	}
	if got := hint.RetryAfterHint(); got != 7*time.Second {
		t.Errorf("httpx.RetryAfterHint view through a wrapper = %v, want 7s", got)
	}
}
