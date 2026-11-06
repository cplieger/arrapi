package radarr

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
)

// commandList holds one command per shape GET /api/v3/command writes: the body is the
// command class's own members (PolymorphicWriteOnlyJsonConverter), a null started or
// ended is omitted (STJson's WhenWritingNull), and an unknown status is a string the
// enum does not hold.
const commandList = `[
	{"id":11,"name":"RefreshMovie","message":"Completed","status":"completed","body":{"movieIds":[41,42],"isNewMovie":true},
		"queued":"2031-07-09T10:00:00Z","started":"2031-07-09T10:00:01Z","ended":"2031-07-09T10:00:05Z"},
	{"id":12,"name":"RescanMovie","message":"Scanning disk for Serenity","status":"started","body":{"movieId":41},
		"queued":"2031-07-09T10:01:00Z","started":"2031-07-09T10:01:01Z"},
	{"id":13,"name":"MoviesSearch","status":"queued","body":{"movieIds":[41]},"queued":"2031-07-09T10:02:00Z"},
	{"id":14,"name":"RescanMovie","status":"completed","body":{"movieId":null},"queued":"2031-07-09T10:03:00Z"},
	{"id":15,"name":"RefreshMonitoredDownloads","status":"paused","body":{},"queued":"2031-07-09T10:04:00Z"}
]`

func at(s string) time.Time {
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return ts
}

var commandListWant = []Command{
	{
		ID: 11, Name: "RefreshMovie", Message: "Completed", Status: arrapi.CommandCompleted, MovieIDs: []int{41, 42}, NewMovie: true,
		Queued: at("2031-07-09T10:00:00Z"), Started: at("2031-07-09T10:00:01Z"), Ended: at("2031-07-09T10:00:05Z"),
	},
	{
		ID: 12, Name: "RescanMovie", Message: "Scanning disk for Serenity", Status: arrapi.CommandStarted, MovieIDs: []int{41},
		Queued: at("2031-07-09T10:01:00Z"), Started: at("2031-07-09T10:01:01Z"),
	},
	{ID: 13, Name: "MoviesSearch", Status: arrapi.CommandQueued, MovieIDs: []int{41}, Queued: at("2031-07-09T10:02:00Z")},
	{ID: 14, Name: "RescanMovie", Status: arrapi.CommandCompleted, Queued: at("2031-07-09T10:03:00Z")},
	{ID: 15, Name: "RefreshMonitoredDownloads", Queued: at("2031-07-09T10:04:00Z")},
}

func wantCommands(t *testing.T, got, want []Command) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Commands returned %d commands, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.ID != w.ID || g.Name != w.Name || g.Message != w.Message || g.Status != w.Status || g.NewMovie != w.NewMovie ||
			!g.Queued.Equal(w.Queued) || !g.Started.Equal(w.Started) || !g.Ended.Equal(w.Ended) {
			t.Errorf("command %d = %+v, want %+v", i, g, w)
		}
		if !slices.Equal(g.MovieIDs, w.MovieIDs) || (g.MovieIDs == nil) != (w.MovieIDs == nil) {
			t.Errorf("command %d MovieIDs = %#v, want %#v", i, g.MovieIDs, w.MovieIDs)
		}
	}
}

func commandClient(t *testing.T) (*Client, *fake) {
	t.Helper()
	f := newFake().radarr(vRadarr)
	c := newClient(t, f)
	cached(t, c, f)
	return c, f
}

func TestCommandsReadsTheList(t *testing.T) {
	c, f := commandClient(t)
	f.on("GET /api/v3/command", ans(200, vRadarr, commandList))
	got, err := c.Commands(t.Context())
	if err != nil {
		t.Fatalf("Commands: %v", err)
	}
	wantCommands(t, got, commandListWant)
	wantURIs(t, f, "GET /api/v3/command")
}

func TestCommandsMergesTheBodyIDs(t *testing.T) {
	c, f := commandClient(t)
	for body, want := range map[string][]int{
		`{"movieIds":[41],"movieId":41}`: {41},
		`{"movieIds":[42],"movieId":41}`: {42, 41},
		`{"movieIds":[41,42,41]}`:        {41, 42},
		`{"movieIds":[]}`:                nil,
		`{"movieIds":[],"movieId":null}`: nil,
		`null`:                           nil,
		`{"movieId":0}`:                  nil,
	} {
		f.on("GET /api/v3/command", ans(200, vRadarr, `[{"id":1,"name":"MissingMoviesSearch","body":`+body+`}]`))
		got, err := c.Commands(t.Context())
		if err != nil || len(got) != 1 {
			t.Fatalf("body %s: Commands = %+v, %v", body, got, err)
		}
		if !slices.Equal(got[0].MovieIDs, want) || (got[0].MovieIDs == nil) != (want == nil) {
			t.Errorf("body %s: MovieIDs = %#v, want %#v", body, got[0].MovieIDs, want)
		}
	}
}

func TestCommandsStatusOverTheWire(t *testing.T) {
	c, f := commandClient(t)
	f.on("GET /api/v3/command", ans(200, vRadarr, `[{"id":1,"status":3},{"id":2,"status":null},{"id":3,"status":"failed"},{"id":4}]`))
	got, err := c.Commands(t.Context())
	if err != nil {
		t.Fatalf("Commands: %v", err)
	}
	want := []arrapi.CommandStatus{"", "", arrapi.CommandFailed, ""}
	if len(got) != len(want) {
		t.Fatalf("Commands returned %d commands, want %d", len(got), len(want))
	}
	for i, cmd := range got {
		if cmd.Status != want[i] {
			t.Errorf("command %d Status = %q, want %q", cmd.ID, cmd.Status, want[i])
		}
	}
	f.on("GET /api/v3/command", ans(200, vRadarr, `[{"id":1,"status":{}}]`))
	_, err = c.Commands(t.Context())
	if want := "arrapi: decode /api/v3/command: field status: a JSON object cannot decode into arrapi.CommandStatus"; err == nil || err.Error() != want {
		t.Errorf("Commands over an object status = %v, want %q", err, want)
	}
}

func TestCommandsRefusesANullItem(t *testing.T) {
	c, f := commandClient(t)
	for body, want := range map[string]string{
		`[{"id":1},null]`:                          "list item 1 is null",
		`[{"id":1,"body":{"movieIds":[41,null]}}]`: "body.movieIds",
	} {
		f.on("GET /api/v3/command", ans(200, vRadarr, body))
		_, err := c.Commands(t.Context())
		if _, ok := errors.AsType[*arrapi.DecodeError](err); !ok || !strings.Contains(err.Error(), want) {
			t.Errorf("Commands over %s = %v, want a *DecodeError naming %q", body, err, want)
		}
	}
}
