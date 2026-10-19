package transport

import (
	"strings"
	"testing"
)

func TestStatusErrorSecretStraddlingCapIsMasked(t *testing.T) {
	const key = "supersecretkey"
	const maskHead = "[re"
	pad := strings.Repeat("A", maxErrorBodyBytes-len(cutMarker)-len(maskHead))
	se := captureBody(t, pad+key, key)
	if strings.Contains(se.Body, key) {
		t.Error("Body contains the full key")
	}
	if want := maskHead + cutMarker; !strings.HasSuffix(se.Body, want) {
		t.Errorf("Body ends %q, want %q: the straddling key must be masked, not cut", se.Body[len(se.Body)-16:], want)
	}
	for n := 2; n <= len(key); n++ {
		if strings.Contains(se.Body, key[:n]) {
			t.Errorf("Body leaks a %d-byte key prefix", n)
		}
	}
	if len(se.Body) != maxErrorBodyBytes {
		t.Errorf("Body length %d, want exactly the %d cap", len(se.Body), maxErrorBodyBytes)
	}
}

func TestStatusErrorSecretPrefixSurvivesRedactionShrinkage(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef" // gitleaks:allow (fake key, redaction test fixture)
	for _, tc := range []struct {
		name string
		pad  int
	}{
		{"one byte fragment", 31},
		{"mid key fragment", 5},
		{"longest examined fragment", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := strings.Repeat("A", tc.pad) + strings.Repeat(key, maxErrorBodyBytes/len(key)+2)
			se := captureBody(t, payload, key)
			captured := strings.TrimSuffix(se.Body, cutMarker)
			if n := keyPrefixTail(captured, key); n != 0 {
				t.Errorf("captured body ends with a %d-byte key prefix", n)
			}
			if strings.Contains(se.Body, key) || len(se.Body) > maxErrorBodyBytes {
				t.Errorf("Body holds the key or is %d bytes", len(se.Body))
			}
		})
	}
}

func TestStatusErrorBodyRedactedToExactlyTheCapIsNotMarked(t *testing.T) {
	const key = "0123456789abcdef" // gitleaks:allow (fake key, redaction test fixture)
	const masked = "[redacted] [redacted]"
	payload := key + " " + key + strings.Repeat("A", maxErrorBodyBytes-len(masked))
	se := captureBody(t, payload, key)
	if !strings.HasPrefix(se.Body, masked) {
		t.Fatalf("Body starts %q, want both keys masked", se.Body[:24])
	}
	if strings.HasSuffix(se.Body, cutMarker) || len(se.Body) != maxErrorBodyBytes {
		t.Errorf("Body is %d bytes ending %q, want exactly the cap and no marker: nothing was lost", len(se.Body), se.Body[len(se.Body)-4:])
	}
}

func TestStatusErrorCapCutDropsTrailingKeyFragment(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef" // gitleaks:allow (fake key, redaction test fixture)
	const fragment = "01"
	keep := maxErrorBodyBytes - len(cutMarker)
	payload := strings.Repeat("A", keep-len(fragment)) + fragment + strings.Repeat("B", 34)
	se := captureBody(t, payload, key)
	captured := strings.TrimSuffix(se.Body, cutMarker)
	if !strings.HasSuffix(se.Body, cutMarker) {
		t.Fatalf("Body (%d bytes) does not end in the marker", len(se.Body))
	}
	if strings.HasSuffix(captured, fragment) || len(captured) != keep-len(fragment) {
		t.Errorf("captured body is %d bytes ending %q, want %d bytes with the fragment dropped", len(captured), captured[len(captured)-8:], keep-len(fragment))
	}
}

func TestStatusErrorFullyReadBodyEndingInKeyPrefixIsKept(t *testing.T) {
	const key = "0123456789abcdef" // gitleaks:allow (fake key, redaction test fixture)
	se := captureBody(t, key+" tail "+key[:8], key)
	if strings.Contains(se.Body, key) {
		t.Errorf("Body %q holds the key", se.Body)
	}
	if !strings.HasSuffix(se.Body, key[:8]) {
		t.Errorf("Body %q dropped legitimate trailing text of a body read whole", se.Body)
	}
}

func TestStatusErrorSoleKeyOccurrenceCutAtTheWindowLeavesNoFragment(t *testing.T) {
	const key = "abcdef0123456789abcdef01234" // gitleaks:allow (fake key, redaction test fixture)
	const keep = 6                            // "abcdef" falls inside the read window
	const lead = 64                           // whitespace TrimSpace removes after the cut, pulling the fragment under the cap
	window := maxErrorBodyBytes + len(key)
	payload := strings.Repeat(" ", lead) + strings.Repeat("A", window-lead-keep) + key + strings.Repeat("Z", 100)
	se := captureBody(t, payload, key)
	captured := strings.TrimSuffix(se.Body, cutMarker)
	if strings.Contains(se.Body, key[:keep]) {
		t.Errorf("Body ends %q, want the fragment dropped", captured[len(captured)-24:])
	}
	if !strings.HasSuffix(captured, "A") {
		t.Errorf("captured body ends %q, want filler", captured[len(captured)-24:])
	}
}

func TestStatusErrorBodyCappedAtTheWire(t *testing.T) {
	se := captureBody(t, strings.Repeat("x", maxErrorBodyBytes)+"after-limit", "irrelevant-key")
	if len(se.Body) != maxErrorBodyBytes || !strings.HasSuffix(se.Body, cutMarker) || strings.Contains(se.Body, "after-limit") {
		t.Errorf("Body is %d bytes, want the %d cap with the marker and nothing past it", len(se.Body), maxErrorBodyBytes)
	}
}
