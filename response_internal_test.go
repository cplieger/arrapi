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
func TestStatusError_secretStraddlingCapBoundaryIsRedacted(t *testing.T) {
	const apiKey = "supersecretkey"
	pad := strings.Repeat("A", maxErrorBodyBytes-3)
	payload := pad + apiKey
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(payload)),
	}
	err := statusError(resp, "/api/v3/series", apiKey)
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("statusError returned %T, want *StatusError", err)
	}
	if strings.Contains(se.Body, apiKey) {
		t.Error("StatusError.Body contains the full API key")
	}
	for n := 2; n <= len(apiKey); n++ {
		if strings.Contains(se.Body, apiKey[:n]) {
			t.Errorf("StatusError.Body leaks a %d-char key prefix %q", n, apiKey[:n])
		}
	}
	if len(se.Body) > maxErrorBodyBytes {
		t.Errorf("StatusError.Body length %d exceeds cap %d", len(se.Body), maxErrorBodyBytes)
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
// the cap, so no key prefix survives (maxPrefix == 0). The pad is a byte absent
// from the key, chosen so the read-window boundary falls mid-key.
func TestStatusError_secretPrefixSurvivesRedactionShrinkage(t *testing.T) {
	const apiKey = "0123456789abcdef0123456789abcdef" // 32 chars, no 'A'; gitleaks:allow (fake key, redaction test fixture)
	pad := strings.Repeat("A", 5)
	payload := pad + strings.Repeat(apiKey, (maxErrorBodyBytes/len(apiKey))+2)
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(payload)),
	}
	err := statusError(resp, "/api/v3/series", apiKey)
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("statusError returned %T, want *StatusError", err)
	}
	maxPrefix := 0
	for n := 1; n <= len(apiKey); n++ {
		if strings.HasSuffix(se.Body, apiKey[:n]) {
			maxPrefix = n
		}
	}
	if maxPrefix != 0 {
		t.Errorf("StatusError.Body ends with a %d-char key prefix %q; want maxPrefix=0", maxPrefix, apiKey[:maxPrefix])
	}
	if strings.Contains(se.Body, apiKey) {
		t.Error("StatusError.Body contains the full API key")
	}
	if len(se.Body) > maxErrorBodyBytes {
		t.Errorf("StatusError.Body length %d exceeds cap %d", len(se.Body), maxErrorBodyBytes)
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
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("statusError returned %T, want *StatusError", err)
	}
	if strings.Contains(se.Body, trimmedKey) {
		t.Errorf("StatusError.Body %q leaks the whitespace-trimmed key %q", se.Body, trimmedKey)
	}
	if strings.Contains(se.Body, apiKey) {
		t.Errorf("StatusError.Body %q leaks the padded key %q", se.Body, apiKey)
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
	var se *StatusError
	if !errors.As(err, &se) {
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
