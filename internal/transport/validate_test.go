package transport_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/transport"
)

func TestNewValidation(t *testing.T) {
	tests := []struct {
		name string
		url  string
		key  arrapi.APIKey
		want string // "" when valid
	}{
		{"valid http", "http://sonarr:8989", testKey, ""},
		{"valid https", "https://radarr.example.com", testKey, ""},
		{"trailing slash", "http://sonarr:8989/", testKey, ""},
		{"reverse-proxy subpath", "https://host.example.com/sonarr", testKey, ""},
		{"user info", "https://user:pass@host.example.com", testKey, ""},
		{"missing scheme", "sonarr:8989", testKey, "arrapi: baseURL must be an absolute http or https URL"},
		{"relative", "/sonarr", testKey, "arrapi: baseURL must be an absolute http or https URL"},
		{"empty", "", testKey, "arrapi: baseURL must be an absolute http or https URL"},
		{"ftp scheme", "ftp://sonarr:8989", testKey, "arrapi: baseURL must be an absolute http or https URL"},
		{"malformed escape", "http://exa%mple.com", testKey, "arrapi: baseURL is not a valid URL"},
		{"no host", "http:///series", testKey, "arrapi: baseURL must have a host"},
		{"port only", "http://:8989", testKey, "arrapi: baseURL must have a host"},
		{"user info and port only", "http://user:pass@:8989/sonarr", testKey, "arrapi: baseURL must have a host"},
		{"query", "http://sonarr:8989/api?x=1", testKey, "arrapi: baseURL must not carry a query or a fragment"},
		{"empty query", "http://sonarr:8989/api?", testKey, "arrapi: baseURL must not carry a query or a fragment"},
		{"fragment", "http://sonarr:8989/#frag", testKey, "arrapi: baseURL must not carry a query or a fragment"},
		{"empty fragment", "http://sonarr:8989/#", testKey, "arrapi: baseURL must not carry a query or a fragment"},
		{"empty key", "http://sonarr:8989", "", "arrapi: key is empty"},
		{"whitespace key", "http://sonarr:8989", " \t ", "arrapi: key is empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := transport.New(tc.url, tc.key, transport.Config{})
			if tc.want == "" {
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				c.Close()
				return
			}
			if err == nil {
				t.Fatalf("New succeeded, want %q", tc.want)
			}
			if err.Error() != tc.want {
				t.Errorf("New error = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

// keptByEveryEncoder reports whether every encoder that can write the key into an
// answer leaves b as it is: Go's url.QueryEscape, url.PathEscape and encoding/json,
// and .NET's Uri.EscapeDataString (dotnet/runtime UriHelper.cs, Unreserved),
// WebUtility.UrlEncode (WebUtility.cs, s_safeUrlChars) and System.Text.Json's default
// encoder (printable ASCII but " & ' + < > ` and \, DefaultJavaScriptEncoder.cs).
func keptByEveryEncoder(b byte) bool {
	const unreserved = "-.0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ_abcdefghijklmnopqrstuvwxyz~"
	const safeURLChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.!*()"
	s := string([]byte{b})
	js, err := json.Marshal(s)
	return err == nil && string(js) == `"`+s+`"` &&
		url.QueryEscape(s) == s && url.PathEscape(s) == s &&
		strings.IndexByte(unreserved, b) >= 0 && strings.IndexByte(safeURLChars, b) >= 0 &&
		b >= 0x20 && b <= 0x7e && strings.IndexByte("\"&'+<>`\\", b) < 0
}

// TestNewAcceptsOnlyAKeyEveryEncoderKeeps: a key holding a byte some encoder rewrites
// is refused, so the key as sent is the only form the encoders in play write it in.
// The refusal is a fixed sentence that renders no key text.
func TestNewAcceptsOnlyAKeyEveryEncoderKeeps(t *testing.T) {
	const refusal = "arrapi: key may hold only letters, digits, '.', '_' and '-'"
	for b := range 256 {
		key := arrapi.APIKey("k" + string([]byte{byte(b)}) + "y")
		c, err := transport.New("http://sonarr:8989", key, transport.Config{})
		switch {
		case keptByEveryEncoder(byte(b)) && err != nil:
			t.Errorf("byte %#02x: New = %v, want the key accepted", b, err)
		case !keptByEveryEncoder(byte(b)) && (err == nil || err.Error() != refusal):
			t.Errorf("byte %#02x: New = %v, want %q", b, err, refusal)
		}
		if err == nil {
			c.Close()
		}
	}
	// Sonarr and Radarr write the key as Guid.NewGuid().ToString().Replace("-", ""),
	// 32 lowercase hex digits (NzbDrone.Core/Configuration/ConfigFileProvider.cs).
	c, err := transport.New("http://sonarr:8989", "0123456789abcdef0123456789abcdef", transport.Config{}) // gitleaks:allow (fake key)
	if err != nil {
		t.Fatalf("New refused a provider-format key: %v", err)
	}
	c.Close()
	for _, key := range []arrapi.APIKey{"k ey", "k/ey", "k+ey", "k%2Fey", "k~ey", "kéy"} {
		_, err := transport.New("http://sonarr:8989", key, transport.Config{})
		if err == nil || err.Error() != refusal {
			t.Errorf("New(%q) = %v, want %q", key, err, refusal)
		}
		assertChainClean(t, err, string(key))
	}
}

func TestNewErrorNeverRendersURLOrKey(t *testing.T) {
	const secret = "pw-9c1e" // gitleaks:allow (fake password, redaction test fixture)
	tests := []struct {
		name    string
		url     string
		secrets []string
	}{
		{"key in the query", "https://sonarr.example.com/?apikey=" + string(testKey), []string{string(testKey), "sonarr.example.com"}},
		{"password in malformed user info", "https://u:" + secret + "@exa mple", []string{secret, "exa mple"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := transport.New(tc.url, testKey, transport.Config{})
			if err == nil {
				t.Fatal("New succeeded, want a validation error")
			}
			assertChainClean(t, err, tc.secrets...)
		})
	}
}

func TestBaseURLTrimsTrailingSlash(t *testing.T) {
	c, err := transport.New("https://host.example.com/sonarr/", testKey, transport.Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	if got := c.BaseURL(); got != "https://host.example.com/sonarr" {
		t.Errorf("BaseURL() = %q, want https://host.example.com/sonarr", got)
	}
}
