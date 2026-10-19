package sonarr

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/runesafe/v3"
)

// errorTree is every error of err's tree, walked as errors.Is walks it.
func errorTree(err error) []error {
	if err == nil {
		return nil
	}
	out := []error{err}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range multi.Unwrap() {
			out = append(out, errorTree(e)...)
		}
		return out
	}
	return append(out, errorTree(errors.Unwrap(err))...)
}

// forgedNameCert is a self-signed server certificate for the one DNS name given.
func forgedNameCert(t *testing.T, name string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// TestTransportErrorRendersOneLine: crypto/x509's HostnameError.Error joins the
// certificate's DNS names as the server wrote them (go1.27 crypto/x509/verify.go), so
// a TLS failure can quote CR, LF and ESC. The returned error's Error() is still one
// line (docs/request-behavior.md "Errors"), each unsafe rune a space
// (runesafe.SanitizeSingleLine), and the TLS cause stays reachable for errors.As.
func TestTransportErrorRendersOneLine(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.NotFoundHandler())
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{forgedNameCert(t, "evil\r\nlevel=INFO msg=forged\x1b[2J")}}
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	c, err := New("https://localhost:"+port, testKey, WithMaxAttempts(1), WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(c.Close)
	_, err = c.Info(t.Context())
	if err == nil {
		t.Fatal("Info succeeded against a certificate for another name")
	}
	got := err.Error()
	for _, r := range got {
		if runesafe.IsUnsafeSingleLine(r) {
			t.Errorf("Error() = %q holds unsafe rune %U", got, r)
		}
	}
	if want := "certificate is valid for evil  level=INFO msg=forged [2J, not localhost"; !strings.Contains(got, want) {
		t.Errorf("Error() = %q, want it to hold %q", got, want)
	}
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); !ok {
		t.Errorf("err = %v, want a *tls.CertificateVerificationError in its tree", err)
	}
	if _, ok := errors.AsType[x509.HostnameError](err); !ok {
		t.Errorf("err = %v, want an x509.HostnameError in its tree", err)
	}
}

// TestStatusErrorRendersOneLine: Body keeps the answer's line breaks (its godoc: the
// capture sanitized, which runesafe.Sanitize does keeping CR and LF), while Error() is
// one line for a line-oriented log, each unsafe rune a space (runesafe.SanitizeSingleLine).
func TestStatusErrorRendersOneLine(t *testing.T) {
	f := newFake().sonarr4(v4)
	f.on(v4Series, ans(http.StatusInternalServerError, v4, "upstream failed\r\nlevel=INFO msg=forged\u2028end"))
	_, err := newClient(t, f, WithMaxAttempts(1)).Series(t.Context())
	se := wantStatus(t, err, http.StatusInternalServerError)
	if want := "upstream failed\r\nlevel=INFO msg=forged end"; se.Body != want {
		t.Errorf("Body = %q, want %q", se.Body, want)
	}
	if want := "arrapi: /api/v3/series: HTTP 500: upstream failed  level=INFO msg=forged end"; se.Error() != want {
		t.Errorf("Error() = %q, want %q", se.Error(), want)
	}
	for _, e := range errorTree(err) {
		for _, r := range e.Error() {
			if runesafe.IsUnsafeSingleLine(r) {
				t.Errorf("%T renders unsafe rune %U: %q", e, r, e.Error())
			}
		}
	}
}
