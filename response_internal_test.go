package arrapi

import "testing"

// TestRedactSecret pins the API-key redaction contract, including the
// empty-secret guard. Without that guard, strings.ReplaceAll(s, "", ...)
// splices the placeholder between every byte of the captured error body,
// so the guard is load-bearing for both correctness and the redaction
// security property. Only the non-empty path is exercised today (indirectly
// via errors_test.go); this covers the empty-secret branch directly.
func TestRedactSecret(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		secret string
		want   string
	}{
		{"empty secret returns input unchanged", "unauthorized: bad key", "", "unauthorized: bad key"},
		{"empty secret does not splice placeholder into input", "abc", "", "abc"},
		{"single occurrence replaced", "key my-key-123 rejected", "my-key-123", "key [REDACTED] rejected"},
		{"every occurrence replaced", "my-key-123 and my-key-123", "my-key-123", "[REDACTED] and [REDACTED]"},
		{"absent secret leaves input unchanged", "no key here", "my-key-123", "no key here"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactSecret(tc.s, tc.secret); got != tc.want {
				t.Errorf("redactSecret(%q, %q) = %q, want %q", tc.s, tc.secret, got, tc.want)
			}
		})
	}
}
