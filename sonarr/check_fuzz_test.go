package sonarr

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3"
)

// answerAll answers every request with one fuzzed status, content type, version and
// body.
type answerAll struct {
	ct, version, body string
	code              int
}

func (a answerAll) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", a.ct)
	if a.version != "" {
		rec.Header().Set("X-Application-Version", a.version)
	}
	rec.WriteHeader(a.code)
	_, _ = io.WriteString(rec, a.body)
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

// FuzzCheckClassifies holds that whatever a server answers, Check succeeds or returns
// a *arrapi.CheckError with a defined reason, and its text never renders the key.
func FuzzCheckClassifies(f *testing.F) {
	f.Add(200, "application/json", v4, statusBody("Sonarr", v4))
	f.Add(200, "application/json", v4, `{"appName":"Son`)
	f.Add(200, "text/html", "", "<html></html>")
	f.Add(401, "", "", "")
	f.Add(404, "application/json", v5, `{"message":"`+string(testKey)+`"}`)
	f.Add(500, "application/problem+json", v5, `{"title":"boom"}`)
	f.Add(302, "", "", "")
	f.Fuzz(func(t *testing.T, code int, ct, version, body string) {
		a := answerAll{ct: ct, version: version, body: body, code: 200 + int(uint(code)%400)}
		_, err := Check(t.Context(), fakeBase, testKey,
			WithHTTPClient(&http.Client{Transport: a}), WithLogger(slog.New(slog.DiscardHandler)))
		if err == nil {
			return
		}
		ce, ok := errors.AsType[*arrapi.CheckError](err)
		if !ok {
			t.Fatalf("err = %v, want *arrapi.CheckError", err)
		}
		if ce.Reason < arrapi.CheckKey || ce.Reason > arrapi.CheckServer {
			t.Errorf("Reason = %d, outside the defined reasons", ce.Reason)
		}
		if strings.Contains(err.Error(), string(testKey)) {
			t.Errorf("Error() = %q renders the key", err.Error())
		}
	})
}
