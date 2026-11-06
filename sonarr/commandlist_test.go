package sonarr

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/arrapi/v3"
)

// commandList holds one command per shape GET {prefix}/command writes: the body is the
// command class's own members (PolymorphicWriteOnlyJsonConverter), a null started or
// ended is omitted (STJson's WhenWritingNull), and an unknown status is a string the
// enum does not hold.
const commandList = `[
	{"id":11,"name":"RefreshSeries","message":"Completed","status":"completed","body":{"seriesIds":[41,42],"isNewSeries":true},
		"queued":"2031-07-09T10:00:00Z","started":"2031-07-09T10:00:01Z","ended":"2031-07-09T10:00:05Z"},
	{"id":12,"name":"RescanSeries","message":"Scanning disk for Code Geass","status":"started","body":{"seriesId":41},
		"queued":"2031-07-09T10:01:00Z","started":"2031-07-09T10:01:01Z"},
	{"id":13,"name":"SeriesSearch","status":"queued","body":{"seriesId":41},"queued":"2031-07-09T10:02:00Z"},
	{"id":14,"name":"RescanSeries","status":"completed","body":{"seriesId":null},"queued":"2031-07-09T10:03:00Z"},
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
		ID: 11, Name: "RefreshSeries", Message: "Completed", Status: arrapi.CommandCompleted, SeriesIDs: []int{41, 42}, NewSeries: true,
		Queued: at("2031-07-09T10:00:00Z"), Started: at("2031-07-09T10:00:01Z"), Ended: at("2031-07-09T10:00:05Z"),
	},
	{
		ID: 12, Name: "RescanSeries", Message: "Scanning disk for Code Geass", Status: arrapi.CommandStarted, SeriesIDs: []int{41},
		Queued: at("2031-07-09T10:01:00Z"), Started: at("2031-07-09T10:01:01Z"),
	},
	{ID: 13, Name: "SeriesSearch", Status: arrapi.CommandQueued, SeriesIDs: []int{41}, Queued: at("2031-07-09T10:02:00Z")},
	{ID: 14, Name: "RescanSeries", Status: arrapi.CommandCompleted, Queued: at("2031-07-09T10:03:00Z")},
	{ID: 15, Name: "RefreshMonitoredDownloads", Queued: at("2031-07-09T10:04:00Z")},
}

func wantCommands(t *testing.T, got, want []Command) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("Commands returned %d commands, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.ID != w.ID || g.Name != w.Name || g.Message != w.Message || g.Status != w.Status || g.NewSeries != w.NewSeries ||
			!g.Queued.Equal(w.Queued) || !g.Started.Equal(w.Started) || !g.Ended.Equal(w.Ended) {
			t.Errorf("command %d = %+v, want %+v", i, g, w)
		}
		if !slices.Equal(g.SeriesIDs, w.SeriesIDs) || (g.SeriesIDs == nil) != (w.SeriesIDs == nil) {
			t.Errorf("command %d SeriesIDs = %#v, want %#v", i, g.SeriesIDs, w.SeriesIDs)
		}
	}
}

func TestCommandsReadsTheList(t *testing.T) {
	for name, ec := range engineCases() {
		t.Run(name, func(t *testing.T) {
			c, f := engineClient(t, ec)
			f.on("GET "+ec.prefix+"/command", ans(200, ec.version, commandList))
			got, err := c.Commands(t.Context())
			if err != nil {
				t.Fatalf("Commands: %v", err)
			}
			wantCommands(t, got, commandListWant)
			wantURIs(t, f, "GET "+ec.prefix+"/command")
		})
	}
}

// SeriesIDs merges the plural and the singular member; Sonarr 5 writes "seriesIds": []
// on every missing and cutoff search, which names no series.
func TestCommandsMergesTheBodyIDs(t *testing.T) {
	for name, ec := range engineCases() {
		t.Run(name, func(t *testing.T) {
			c, f := engineClient(t, ec)
			for body, want := range map[string][]int{
				`{"seriesIds":[41],"seriesId":41}`: {41},
				`{"seriesIds":[42],"seriesId":41}`: {42, 41},
				`{"seriesIds":[41,42,41]}`:         {41, 42},
				`{"seriesIds":[]}`:                 nil,
				`{"seriesIds":[],"seriesId":null}`: nil,
				`null`:                             nil,
				`{"seriesId":0}`:                   nil,
			} {
				f.on("GET "+ec.prefix+"/command", ans(200, ec.version, `[{"id":1,"name":"MissingEpisodeSearch","body":`+body+`}]`))
				got, err := c.Commands(t.Context())
				if err != nil || len(got) != 1 {
					t.Fatalf("body %s: Commands = %+v, %v", body, got, err)
				}
				if !slices.Equal(got[0].SeriesIDs, want) || (got[0].SeriesIDs == nil) != (want == nil) {
					t.Errorf("body %s: SeriesIDs = %#v, want %#v", body, got[0].SeriesIDs, want)
				}
			}
		})
	}
}

// A status outside the enum is written as a number (STJson.cs registers
// JsonStringEnumConverter with allowIntegerValues), so it reads "" without failing the
// list; a kind no server writes fails it with the field named.
func TestCommandsStatusOverTheWire(t *testing.T) {
	for name, ec := range engineCases() {
		t.Run(name, func(t *testing.T) {
			c, f := engineClient(t, ec)
			f.on("GET "+ec.prefix+"/command", ans(200, ec.version,
				`[{"id":1,"status":3},{"id":2,"status":null},{"id":3,"status":"failed"},{"id":4}]`))
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

			f.on("GET "+ec.prefix+"/command", ans(200, ec.version, `[{"id":1,"status":{}}]`))
			_, err = c.Commands(t.Context())
			if want := "arrapi: decode " + ec.prefix + "/command: field status: a JSON object cannot decode into arrapi.CommandStatus"; err == nil || err.Error() != want {
				t.Errorf("Commands over an object status = %v, want %q", err, want)
			}
		})
	}
}

func TestCommandsRefusesANullItem(t *testing.T) {
	for name, ec := range engineCases() {
		t.Run(name, func(t *testing.T) {
			c, f := engineClient(t, ec)
			for body, want := range map[string]string{
				`[{"id":1},null]`: "list item 1 is null",
				`[{"id":1,"body":{"seriesIds":[41,null]}}]`: "body.seriesIds",
			} {
				f.on("GET "+ec.prefix+"/command", ans(200, ec.version, body))
				_, err := c.Commands(t.Context())
				if _, ok := errors.AsType[*arrapi.DecodeError](err); !ok || !strings.Contains(err.Error(), want) {
					t.Errorf("Commands over %s = %v, want a *DecodeError naming %q", body, err, want)
				}
			}
		})
	}
}

func TestPinnedCommands(t *testing.T) {
	f := newFake().sonarr4(v4)
	c := newClient(t, f)
	p := pin(t, c)
	f.on("GET /api/v3/command", ans(200, v4, commandList))
	got, err := p.Commands(t.Context())
	if err != nil {
		t.Fatalf("Pinned.Commands: %v", err)
	}
	wantCommands(t, got, commandListWant)

	const upgraded = "4.0.21.1"
	f.clearLog()
	f.sonarr4(upgraded)
	f.on("GET /api/v3/command", ans(200, upgraded, commandList))
	got, err = p.Commands(t.Context())
	wantChangedTo(t, err, upgraded)
	if got != nil {
		t.Errorf("Pinned.Commands = %+v, want nothing", got)
	}
}
