package sonarr

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
)

// serverMarker stands for text a server writes into an error: an indexer's passkey
// in a feed URL its failure message quotes.
const serverMarker = "pk-0d1e-marker"

// jsonLog returns the buffer and the option of a logger writing every record, Debug
// included, as slog's JSON handler renders it.
func jsonLog() (*bytes.Buffer, Option) {
	var out bytes.Buffer
	return &out, WithLogger(slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

// onlyRecord returns the one JSON record of out whose message is msg.
func onlyRecord(t *testing.T, out *bytes.Buffer, msg string) map[string]any {
	t.Helper()
	var found []map[string]any
	for line := range strings.Lines(out.String()) {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("%v in %q", err, line)
		}
		if rec["msg"] == msg {
			found = append(found, rec)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d records %q, want 1:\n%s", len(found), msg, out)
	}
	return found[0]
}

// wantErrorAttr checks rec's error attribute: the class by default, and the error's
// text quoting the server's with WithLoggedErrorText(true).
func wantErrorAttr(t *testing.T, rec map[string]any, class string, logged bool) {
	t.Helper()
	got, _ := rec["error"].(string)
	switch {
	case !logged && got != class:
		t.Errorf("%s: error = %q, want the class %q", rec["msg"], got, class)
	case logged && !strings.Contains(got, serverMarker):
		t.Errorf("%s: error = %q, want the error's text quoting %q", rec["msg"], got, serverMarker)
	}
}

var loggedErrorText = map[string]bool{"by default": false, "WithLoggedErrorText": true}

// TestReadRecordsCarryNoServerText: a read answered 503 twice writes a retry record
// and an exhaustion record, each naming the app, the path and the status; the
// server's message or body reaches neither unless the client logs error text.
func TestReadRecordsCarryNoServerText(t *testing.T) {
	answers := map[string]string{
		"the message":  `{"message":"Indexer failed: https://tracker.example/rss/` + serverMarker + `/feed"}`,
		"a plain body": "<html><body>Bad gateway at /srv/" + serverMarker + "</body></html>",
	}
	for answerName, body := range answers {
		for mode, logged := range loggedErrorText {
			t.Run(answerName+" "+mode, func(t *testing.T) {
				out, logger := jsonLog()
				f := newFake().sonarr4(v4)
				f.on(v4Series, ans(http.StatusServiceUnavailable, v4, body))
				c := newClient(t, f, WithMaxAttempts(2), logger, WithLoggedErrorText(logged))
				if _, err := c.Series(t.Context()); err == nil {
					t.Fatal("Series succeeded on a 503")
				}
				for _, msg := range []string{"arrapi failed, retrying", "arrapi retries exhausted"} {
					rec := onlyRecord(t, out, msg)
					if rec["app"] != "Sonarr" || rec["path"] != "/api/v3/series" || rec["status"] != 503.0 {
						t.Errorf("%s: app, path, status = %v, %v, %v; want Sonarr, /api/v3/series, 503",
							msg, rec["app"], rec["path"], rec["status"])
					}
					wantErrorAttr(t, rec, "status", logged)
				}
				if got := strings.Contains(out.String(), serverMarker); got != logged {
					t.Errorf("the log holds the server's text: %v, want %v:\n%s", got, logged, out)
				}
			})
		}
	}
}

// failingBody is f with the body of each answer to route failing at its first read
// with err.
type failingBody struct {
	f     *fake
	err   error
	route string
}

func (b failingBody) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := b.f.RoundTrip(req)
	if err == nil && req.Method+" "+req.URL.Path == b.route {
		resp.Body = io.NopCloser(iotest.ErrReader(b.err))
	}
	return resp, err
}

// TestCommandRecordCarriesNoServerText: an accepted command whose answer's read
// fails with an error quoting server bytes, as an HTTP/2 GOAWAY's debug data does
// (net/http/internal/http2 GoAwayError.Error), writes one record naming the app and
// the path; the server's bytes reach it only when the client logs error text.
func TestCommandRecordCarriesNoServerText(t *testing.T) {
	goAway := errors.New(`http2: server sent GOAWAY and closed the connection; LastStreamID=1, ErrCode=NO_ERROR, debug="` + serverMarker + `"`)
	for mode, logged := range loggedErrorText {
		t.Run(mode, func(t *testing.T) {
			out, logger := jsonLog()
			f := newFake().sonarr4(v4)
			f.on(v4Command, ans(http.StatusCreated, v4, `{"id":77}`))
			rt := failingBody{f: f, route: v4Command, err: goAway}
			c := newClient(t, f, WithHTTPClient(&http.Client{Transport: rt}), logger, WithLoggedErrorText(logged))
			if id, err := c.SeriesSearch(t.Context(), 41); id != 0 || err != nil {
				t.Fatalf("SeriesSearch = (%d, %v), want (0, nil)", id, err)
			}
			rec := onlyRecord(t, out, unreadAnswer)
			if rec["app"] != "Sonarr" || rec["path"] != "/api/v3/command" {
				t.Errorf("app, path = %v, %v; want Sonarr, /api/v3/command", rec["app"], rec["path"])
			}
			wantErrorAttr(t, rec, "transport", logged)
			if got := strings.Contains(out.String(), serverMarker); got != logged {
				t.Errorf("the log holds the server's text: %v, want %v:\n%s", got, logged, out)
			}
		})
	}
}

// TestDetectedRecordCarriesNoServerText: the detection record's version is the
// answer's X-Application-Version, two to four runs of digits, never the status
// body's version text, whether or not the client logs error text.
func TestDetectedRecordCarriesNoServerText(t *testing.T) {
	for mode, logged := range loggedErrorText {
		t.Run(mode, func(t *testing.T) {
			out, logger := jsonLog()
			f := newFake().sonarr4(v4)
			f.on("GET /api/v3/system/status", ans(http.StatusOK, v4, statusBody("Sonarr", v4+" "+serverMarker)))
			c := newClient(t, f, logger, WithLoggedErrorText(logged))
			if _, err := c.Series(t.Context()); err != nil {
				t.Fatalf("Series: %v", err)
			}
			if rec := onlyRecord(t, out, "arrapi: detected"); rec["version"] != v4 {
				t.Errorf("version = %v, want the header's %s", rec["version"], v4)
			}
			if strings.Contains(out.String(), serverMarker) {
				t.Errorf("the log holds the server's text:\n%s", out)
			}
		})
	}
}
