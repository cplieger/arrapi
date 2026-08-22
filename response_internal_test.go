package arrapi

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestStatusError_secretStraddlingCapBoundaryIsRedacted pins the boundary case
// the redact-before-cap ordering exists to close: a hostile endpoint that
// reflects the request API key so it starts just before maxErrorBodyBytes and
// ends past it. Before the fix, statusError truncated to maxErrorBodyBytes
// first, so redactSecret only saw the leading fragment of the key and could not
// match it, leaving a credential prefix in StatusError.Body. Redaction now runs
// over a maxErrorBodyBytes+len(apiKey) read window before the final cap, so the
// full key is matched and stripped. Padding uses a byte absent from the key so
// any surviving key prefix is unambiguous.
//
// Absence alone is a weak witness -- a capture that simply cut the key off
// satisfies it too -- so the capture is also pinned positively: the key is
// REPLACED by the redaction mask, and it is the mask, not the key, that the
// final cap then cuts through.
func TestStatusError_secretStraddlingCapBoundaryIsRedacted(t *testing.T) {
	const apiKey = "supersecretkey"
	const maskHead = "RED" // the mask "REDACTED" starts 3 bytes below the cap
	pad := strings.Repeat("A", maxErrorBodyBytes-len(maskHead))
	payload := pad + apiKey
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(payload)),
	}
	err := statusError(resp, "/api/v3/series", apiKey)
	se, ok := errors.AsType[*StatusError](err)
	if !ok {
		t.Fatalf("statusError returned %T, want *StatusError", err)
	}
	if strings.Contains(se.Body, apiKey) {
		t.Error("StatusError.Body contains the full API key")
	}
	if want := maskHead + truncationMarker; !strings.HasSuffix(se.Body, want) {
		t.Errorf("StatusError.Body (length %d) ends %q, want it to end %q: the straddling key must be masked, not cut away",
			len(se.Body), se.Body[max(0, len(se.Body)-16):], want)
	}
	for n := 2; n <= len(apiKey); n++ {
		if strings.Contains(se.Body, apiKey[:n]) {
			t.Errorf("StatusError.Body leaks a %d-char key prefix %q", n, apiKey[:n])
		}
	}
	if len(se.Body) > maxErrorBodyBytes+len(truncationMarker) {
		t.Errorf("StatusError.Body length %d exceeds cap %d + marker", len(se.Body), maxErrorBodyBytes)
	}
}

// TestStatusError_secretPrefixSurvivesRedactionShrinkage pins the residual leak
// that redact-before-cap alone does not close: redaction shrinkage. A body of
// many full keys followed by a key straddling the END of the read window leaves
// that trailing key truncated to a proper prefix RedactSecretString cannot match
// (ReplaceAll matches only whole occurrences). Because "REDACTED" (8 bytes)
// is shorter than the key, redacting the earlier full copies shrinks the buffer
// and shifts that unmatched prefix back below the maxErrorBodyBytes cap, where
// it survives. trimTrailingSecretPrefix strips the trailing key-prefix run after
// the cap, so no key prefix survives (maxPrefix == 0), for a prefix of ANY
// length -- a single byte of a credential is as unwelcome as thirty.
//
// The pad picks the fragment length: the read window (maxErrorBodyBytes plus the
// 32-byte key) is a whole multiple of the key, so a pad of p bytes made of a
// byte absent from the key leaves a trailing fragment of 32-p bytes. 31 is the
// longest fragment the trim examines (a proper prefix of a 32-byte key), and the
// marker is stripped before the check because it is appended AFTER the trim.
func TestStatusError_secretPrefixSurvivesRedactionShrinkage(t *testing.T) {
	const apiKey = "0123456789abcdef0123456789abcdef" // 32 chars, no 'A'; gitleaks:allow (fake key, redaction test fixture)
	tests := []struct {
		name string
		pad  int
	}{
		{"one_byte_fragment", 31},
		{"mid_key_fragment", 5},
		{"longest_examined_fragment", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := strings.Repeat("A", tc.pad) + strings.Repeat(apiKey, (maxErrorBodyBytes/len(apiKey))+2)
			se := captureStatusError(t, payload, apiKey)
			captured := strings.TrimSuffix(se.Body, truncationMarker)
			maxPrefix := 0
			for n := 1; n <= len(apiKey); n++ {
				if strings.HasSuffix(captured, apiKey[:n]) {
					maxPrefix = n
				}
			}
			if maxPrefix != 0 {
				t.Errorf("captured body (pad %d) ends with a %d-char key prefix %q; want maxPrefix=0",
					tc.pad, maxPrefix, apiKey[:maxPrefix])
			}
			if strings.Contains(se.Body, apiKey) {
				t.Errorf("StatusError.Body (pad %d) contains the full API key", tc.pad)
			}
			if len(se.Body) > maxErrorBodyBytes+len(truncationMarker) {
				t.Errorf("StatusError.Body (pad %d) length %d exceeds cap %d + marker", tc.pad, len(se.Body), maxErrorBodyBytes)
			}
		})
	}
}

// TestStatusError_bodyRedactedToExactlyTheCapIsNotMarkedTruncated pins the
// other side of the marker contract: the marker means bytes were LOST, so a
// body the read window held in full must not carry one however close to the cap
// it lands. Both cuts that could mark it sit exactly one byte away here -- the
// payload fills the read window to its last byte, and redaction brings it to
// exactly maxErrorBodyBytes -- so a marker on this capture would be reporting a
// loss that never happened, and an operator reading the logged body would
// mistake a complete arr error for a clipped one.
func TestStatusError_bodyRedactedToExactlyTheCapIsNotMarkedTruncated(t *testing.T) {
	const apiKey = "0123456789abcdef" // 16 chars, no 'A'; gitleaks:allow (fake key, redaction test fixture)
	// The read window is maxErrorBodyBytes+16. Fill it to the byte with two
	// reflected keys plus padding, so redaction (16 bytes in, 8 bytes of mask
	// out, twice) shrinks the capture by exactly 16 to land on the cap itself.
	payload := apiKey + apiKey + strings.Repeat("A", maxErrorBodyBytes-len(apiKey))
	se := captureStatusError(t, payload, apiKey)

	if !strings.HasPrefix(se.Body, "REDACTEDREDACTED") {
		t.Fatalf("StatusError.Body starts %q, want it to start with both keys masked", se.Body[:min(24, len(se.Body))])
	}
	if strings.HasSuffix(se.Body, truncationMarker) {
		t.Errorf("StatusError.Body ends in the %q marker, want no marker: the read window held the whole body", truncationMarker)
	}
	if len(se.Body) != maxErrorBodyBytes {
		t.Errorf("StatusError.Body length = %d, want exactly %d (the cap, unmarked)", len(se.Body), maxErrorBodyBytes)
	}
}

// TestStatusError_whitespacePaddedKeyVariantIsRedacted pins the OWS-reflection
// leak: validateClientParams accepts a non-empty key that retains leading or
// trailing whitespace, and setStandardHeaders sends it verbatim. An HTTP peer
// that treats field-value outer whitespace as optional (OWS) may observe and
// reflect the TrimSpace'd key, which redactSecret(body, "  key  ") cannot match
// because the reflected token has no padding. readErrorBody now also redacts the
// whitespace-normalized key variant, so neither the padded key nor its trimmed
// form survives in StatusError.Body. The body is short and fully read, so the
// guarded trailing-prefix trim does not fire here -- the additive variant
// redaction is what closes the leak.
func TestStatusError_whitespacePaddedKeyVariantIsRedacted(t *testing.T) {
	const apiKey = "  pasted-secret-key  "
	trimmedKey := strings.TrimSpace(apiKey)
	payload := "unauthorized: " + trimmedKey
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(payload)),
	}
	err := statusError(resp, "/api/v3/series", apiKey)
	se, ok := errors.AsType[*StatusError](err)
	if !ok {
		t.Fatalf("statusError returned %T, want *StatusError", err)
	}
	if strings.Contains(se.Body, trimmedKey) {
		t.Errorf("StatusError.Body %q leaks the whitespace-trimmed key %q", se.Body, trimmedKey)
	}
	if strings.Contains(se.Body, apiKey) {
		t.Errorf("StatusError.Body %q leaks the padded key %q", se.Body, apiKey)
	}
}

// TestStatusError_keyVariantRebuiltBySanitizationIsRedacted pins the OWS
// variant's half of the redact-after-sanitize rule. Sanitization is a
// normalizing transform: it maps every unsafe rune to a space, so a body
// carrying "arr\x01key\x017f3a" holds no occurrence of the trimmed key on the
// wire, and the pre-transform redaction pass cannot match it -- then
// sanitization turns those controls into the very spaces the key contains and
// ASSEMBLES the credential after the mask has already run. Only a second
// redaction of the trimmed variant, after the transform, catches it, and
// without one the reflected key would land in a caller's log verbatim.
func TestStatusError_keyVariantRebuiltBySanitizationIsRedacted(t *testing.T) {
	const apiKey = "  arr key 7f3a  " // pasted with OWS the peer may strip
	const trimmedKey = "arr key 7f3a" // what an OWS-stripping peer reflects
	se := captureStatusError(t, "{\"error\":\"bad key: arr\x01key\x017f3a\"}", apiKey)

	if strings.Contains(se.Body, trimmedKey) {
		t.Errorf("StatusError.Body = %q, want no occurrence of the trimmed key %q that sanitization reassembled", se.Body, trimmedKey)
	}
	if !strings.Contains(se.Body, "REDACTED") {
		t.Errorf("StatusError.Body = %q, want the reassembled key replaced by REDACTED", se.Body)
	}
}

// TestStatusError_keyVariantGarbledBySanitizationIsRedacted pins the mirror
// case, and the reason the OWS variant is redacted on BOTH sides of the
// transform. Here the trimmed key itself carries an unsafe rune, so the body
// reflects it verbatim but sanitization rewrites that rune to a space in the
// body while the needle keeps it: after the transform the two can never match
// again, and only the redaction that ran BEFORE it removes the credential. Skip
// that pass and a near-complete fragment of the key -- every character but the
// rewritten one -- survives into the caller's log.
func TestStatusError_keyVariantGarbledBySanitizationIsRedacted(t *testing.T) {
	const apiKey = "  arr\x01key7f3a  " // pasted with OWS the peer may strip
	const trimmedKey = "arr\x01key7f3a" // what an OWS-stripping peer reflects
	const sanitizedKey = "arr key7f3a"  // what the transform makes of it
	se := captureStatusError(t, "{\"error\":\"bad key: "+trimmedKey+"\"}", apiKey)

	if strings.Contains(se.Body, trimmedKey) {
		t.Errorf("StatusError.Body = %q, want no occurrence of the trimmed key %q", se.Body, trimmedKey)
	}
	if strings.Contains(se.Body, sanitizedKey) {
		t.Errorf("StatusError.Body = %q, want no occurrence of %q either: the transform must not be able to leave a near-complete key behind", se.Body, sanitizedKey)
	}
	if !strings.Contains(se.Body, "REDACTED") {
		t.Errorf("StatusError.Body = %q, want the reflected key replaced by REDACTED", se.Body)
	}
}

// TestStatusError_capCutDropsTrailingKeyVariantFragment pins the trailing-prefix
// cleanup for the OWS variant. RedactSecretString matches whole occurrences
// only, so a fragment of the key is invisible to it and reaches the cap intact;
// when the cap then cuts the body right after that fragment, the fragment
// becomes the captured body's tail. The cleanup keyed on the PADDED key cannot
// remove it -- every prefix of "  key  " starts with a space, and the capture
// was whitespace-trimmed -- so the pass keyed on the trimmed variant is the one
// that closes it. The body here is read whole and cut only by the cap, which is
// what keeps the pre-cap cleanup out of the way.
func TestStatusError_capCutDropsTrailingKeyVariantFragment(t *testing.T) {
	const apiKey = "  0123456789abcdef0123456789abcdef  " // gitleaks:allow (fake key, redaction test fixture)
	const fragment = "01"                                 // the trimmed variant's first two bytes
	// Sit the fragment on the last two bytes of the cap, with a tail that keeps
	// the whole payload inside the read window (maxErrorBodyBytes+36) so the
	// cap is the only cut. 'A' and 'B' are absent from the key.
	payload := strings.Repeat("A", maxErrorBodyBytes-len(fragment)) + fragment + strings.Repeat("B", 34)
	se := captureStatusError(t, payload, apiKey)
	captured := strings.TrimSuffix(se.Body, truncationMarker)

	if !strings.HasSuffix(se.Body, truncationMarker) {
		t.Fatalf("StatusError.Body (length %d) does not end in the %q marker; the cap cut must be marked", len(se.Body), truncationMarker)
	}
	if strings.HasSuffix(captured, fragment) {
		t.Errorf("captured body ends %q, want the trailing key fragment %q dropped", captured[max(0, len(captured)-8):], fragment)
	}
	if len(captured) != maxErrorBodyBytes-len(fragment) {
		t.Errorf("captured body length = %d, want %d (the cap less the dropped fragment)", len(captured), maxErrorBodyBytes-len(fragment))
	}
}

// TestStatusError_truncatedBodyDropsUnsafeKeyVariantFragment pins the cleanup
// that has to run BEFORE sanitization. When the read window cuts mid-key and
// the fragment left behind contains an unsafe rune, the post-transform cleanup
// is already too late: sanitization has rewritten that rune in the body while
// the needle still holds it, so no suffix of the body matches any prefix of the
// key and the fragment stays. Removing it while the capture still holds raw
// wire bytes is the only pass that can, so a body of reflected keys cut mid-key
// must come back holding nothing but mask.
func TestStatusError_truncatedBodyDropsUnsafeKeyVariantFragment(t *testing.T) {
	const apiKey = "  ab\x01cdef0123456789abcdef01234567  " // gitleaks:allow (fake key, redaction test fixture)
	const trimmedKey = "ab\x01cdef0123456789abcdef01234567" // 31 bytes, an unsafe rune at index 2
	// The read window (maxErrorBodyBytes+35) is not a whole multiple of the
	// key, so filling past it with whole keys leaves a partial key as the tail.
	payload := strings.Repeat(trimmedKey, (maxErrorBodyBytes/len(trimmedKey))+2)
	se := captureStatusError(t, payload, apiKey)
	captured := strings.TrimSuffix(se.Body, truncationMarker)

	if !strings.HasSuffix(captured, "REDACTED") {
		t.Errorf("captured body ends %q, want it to end in mask: the partial key at the cut must be dropped, not carried through the transform",
			captured[max(0, len(captured)-12):])
	}
	// Every byte of the payload is mask or hex, so a space can only be an
	// unsafe rune of the key that survived the transform.
	if strings.ContainsRune(captured, ' ') {
		t.Errorf("captured body %q contains a space, want none: a rewritten key rune survived capture", captured[max(0, len(captured)-24):])
	}
}

// TestStatusError_fullyReadBodyEndingInKeyPrefixNotOverRedacted pins the
// over-redaction guard: trimTrailingSecretPrefix runs only when the read
// window actually truncated the body. A fully-read body that (a) contains an
// earlier full key -- so redaction shrinks it -- and (b) happens to end with
// the key's first characters must keep that trailing text: a non-truncated
// body has no straddling key to leak, so the trailing run is legitimate
// content, not a truncated credential. Here redactionShrank is true but
// truncatedAtReadWindow is false, so the guard must not fire.
func TestStatusError_fullyReadBodyEndingInKeyPrefixNotOverRedacted(t *testing.T) {
	const apiKey = "0123456789abcdef" // 16 chars, no space; gitleaks:allow (fake key, redaction test fixture)
	payload := apiKey + " tail " + apiKey[:8]
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(payload)),
	}
	err := statusError(resp, "/api/v3/series", apiKey)
	se, ok := errors.AsType[*StatusError](err)
	if !ok {
		t.Fatalf("statusError returned %T, want *StatusError", err)
	}
	if strings.Contains(se.Body, apiKey) {
		t.Errorf("StatusError.Body %q contains the full API key", se.Body)
	}
	if !strings.HasSuffix(se.Body, apiKey[:8]) {
		t.Errorf("StatusError.Body %q dropped the legitimate trailing key-prefix %q; "+
			"the over-redaction guard must not trim a fully-read body", se.Body, apiKey[:8])
	}
}

// TestStatusError_soleKeyOccurrenceCutAtTheWindowLeavesNoFragment pins the
// pre-sanitization cleanup for the case where the cut key is the body's ONLY
// occurrence. Every other truncation test here reflects the key many times, so
// redaction replaces whole occurrences and the body shrinks; a cleanup gated on
// that shrinkage still runs and the fragment goes. Reflect the key ONCE,
// straddling the read window, and redaction finds nothing to replace: the body
// keeps its length, so a shrinkage gate skips the cleanup entirely.
//
// The fragment then reaches sanitization, which rewrites the key's unsafe rune
// in the body while the needle keeps it, and from there no suffix of the body
// matches any prefix of the key -- so the post-transform cleanup cannot remove
// it either and it lands in the caller's log. Measured leak: "ab cde", five of
// the credential's first six characters.
//
// Two pieces of arithmetic make the case reachable, and both are load-bearing.
// The read window is maxErrorBodyBytes+len(apiKey), and the cap immediately
// slices back to maxErrorBodyBytes, which would discard a fragment sitting in
// those last len(apiKey) bytes -- so the body opens with whitespace that
// TrimSpace removes AFTER the cut, pulling the fragment under the cap. And the
// filler is sized so exactly `keep` bytes of the key fall inside the window,
// which is what makes the tail a proper prefix rather than the whole key.
func TestStatusError_soleKeyOccurrenceCutAtTheWindowLeavesNoFragment(t *testing.T) {
	const apiKey = "ab\x01cdef0123456789abcdef01234" // 28 bytes, unsafe rune at index 2; gitleaks:allow (fake key, redaction test fixture)
	const keep = 6                                   // "ab\x01cde" is the part that falls inside the window
	const lead = 40                                  // >= len(apiKey), so the trim pulls the fragment under the cap

	readLimit := maxErrorBodyBytes + len(apiKey)
	// 'A' and 'Z' share no byte with the key, so any key byte in the captured
	// body came from the reflected key and nothing else.
	filler := strings.Repeat("A", readLimit-lead-keep)
	payload := strings.Repeat(" ", lead) + filler + apiKey + strings.Repeat("Z", 100)

	se := captureStatusError(t, payload, apiKey)
	captured := strings.TrimSuffix(se.Body, truncationMarker)
	tail := captured[max(0, len(captured)-24):]

	if strings.Contains(se.Body, apiKey) {
		t.Errorf("StatusError.Body contains the whole API key; tail %q", tail)
	}
	if strings.Contains(se.Body, apiKey[:keep]) {
		t.Errorf("StatusError.Body ends %q, want the raw key fragment %q dropped", tail, apiKey[:keep])
	}
	// The sanitized spelling is the one that actually leaked: the transform
	// rewrote the key's unsafe rune, so the raw needle no longer matches.
	if sanitizedFragment := "ab cde"; strings.Contains(se.Body, sanitizedFragment) {
		t.Errorf("StatusError.Body ends %q, want no occurrence of %q: the fragment must be dropped BEFORE the transform can garble it",
			tail, sanitizedFragment)
	}
	if !strings.HasSuffix(captured, "A") {
		t.Errorf("captured body ends %q, want it to end in filler with the whole fragment removed", tail)
	}
}
