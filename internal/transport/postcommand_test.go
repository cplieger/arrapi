package transport_test

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/cplieger/arrapi/v3/internal/transport"
)

const unreadAnswer = "arrapi: command accepted; its answer was not read"

// records captures log records.
type records struct {
	recs []slog.Record
	mu   sync.Mutex
}

func (*records) Enabled(context.Context, slog.Level) bool { return true }
func (r *records) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, rec.Clone())
	return nil
}
func (r *records) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *records) WithGroup(string) slog.Handler      { return r }

// find returns the attrs of the records with message msg, rendered as text.
func (r *records) find(msg string) []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]string
	for _, rec := range r.recs {
		if rec.Message != msg {
			continue
		}
		m := map[string]string{"level": rec.Level.String()}
		rec.Attrs(func(a slog.Attr) bool {
			m[a.Key] = a.Value.String()
			return true
		})
		out = append(out, m)
	}
	return out
}

func postCommand(t *testing.T, h http.HandlerFunc) (int, transport.Result, *records) {
	t.Helper()
	rec := &records{}
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{Logger: slog.New(rec)})
	d := detect(t, c, f, v4)
	f.set(h)
	id, res := c.PostCommand(t.Context(), d, "/api/v3/command", map[string]any{"name": "SeriesSearch", "seriesId": 41})
	if n := len(f.requests()); n != 1 {
		t.Errorf("server saw %d requests, want 1", n)
	}
	return id, res, rec
}

func TestPostCommandReadsTheID(t *testing.T) {
	id, res, rec := postCommand(t, reply(201, v4, `{"id":77,"name":"SeriesSearch","status":"queued"}`))
	if id != 77 || res.Err != nil || res.Verdict != transport.VerdictAccept {
		t.Errorf("PostCommand = (%d, err %v, verdict %v), want (77, nil, accept)", id, res.Err, res.Verdict)
	}
	if got := rec.find(unreadAnswer); len(got) != 0 {
		t.Errorf("logged %v for a readable answer", got)
	}
}

func TestPostCommandUnreadableAnswerIsAcceptedWithIDZero(t *testing.T) {
	cut := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Application-Version", v4)
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":77`))
	}
	for name, tc := range map[string]struct {
		h     http.HandlerFunc
		class string
	}{
		"not JSON":          {reply(201, v4, "not a command resource"), "decode"},
		"empty":             {reply(201, v4, ""), "decode"},
		"null":              {reply(201, v4, "null"), "decode"},
		"a string id":       {reply(201, v4, `{"id":"77"}`), "decode"},
		"id 0":              {reply(201, v4, `{"id":0}`), "no_id"},
		"a negative id":     {reply(201, v4, `{"id":-3}`), "no_id"},
		"no id":             {reply(201, v4, `{"name":"SeriesSearch"}`), "no_id"},
		"partial JSON":      {reply(201, v4, `{"id":77,`), "decode"},
		"over the cap":      {reply(201, v4, `{"id":77,"x":"`+strings.Repeat("a", transport.MaxObjectBytes)+`"}`), "too_large"},
		"cut by the server": {cut, "transport"},
	} {
		t.Run(name, func(t *testing.T) {
			id, res, rec := postCommand(t, tc.h)
			if id != 0 || res.Err != nil || res.Verdict != transport.VerdictAccept || res.Next != transport.NextDone {
				t.Errorf("PostCommand = (%d, err %v, verdict %v, next %v), want (0, nil, accept, done)", id, res.Err, res.Verdict, res.Next)
			}
			got := rec.find(unreadAnswer)
			if len(got) != 1 {
				t.Fatalf("logged %d unread-answer records, want 1", len(got))
			}
			if got[0]["path"] != "/api/v3/command" || got[0]["level"] != "WARN" || got[0]["error"] != tc.class {
				t.Errorf("record = %v, want a WARN naming /api/v3/command and the class %q", got[0], tc.class)
			}
		})
	}
}

func TestPostCommandOtherVerdictsLogNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		h    http.HandlerFunc
		want transport.Verdict
	}{
		"a 503 from the server": {reply(503, v4, `{"id":77}`), transport.VerdictStatus},
		"a header-less 201":     {reply(201, "", `{"id":77}`), transport.VerdictUnconfirmed},
		"a 201 from Sonarr 5":   {reply(201, v5, `{"id":77}`), transport.VerdictUnconfirmed},
		"a 404 from Sonarr 5":   {reply(404, v5, `{"id":77}`), transport.VerdictChanged},
	} {
		t.Run(name, func(t *testing.T) {
			id, res, rec := postCommand(t, tc.h)
			if id != 0 || res.Verdict != tc.want || res.Err == nil {
				t.Errorf("PostCommand = (%d, err %v, verdict %v), want (0, an error, %v)", id, res.Err, res.Verdict, tc.want)
			}
			if got := rec.find(unreadAnswer); len(got) != 0 {
				t.Errorf("logged %v on verdict %v", got, res.Verdict)
			}
		})
	}
}

func TestPostDiscardsTheAnswerUnread(t *testing.T) {
	rec := &records{}
	f := newFake(t, nil)
	c := newClient(t, f.srv.URL, transport.Config{Logger: slog.New(rec)})
	d := detect(t, c, f, v4)
	f.set(reply(201, v4, "not a command resource"))
	if res := c.Post(t.Context(), d, "/api/v3/command", map[string]any{}); res.Err != nil || res.Verdict != transport.VerdictAccept {
		t.Errorf("Post = (err %v, verdict %v), want accepted", res.Err, res.Verdict)
	}
	if got := rec.find(unreadAnswer); len(got) != 0 {
		t.Errorf("Post logged %v", got)
	}
}
