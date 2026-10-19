package transport_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/cplieger/arrapi/v3/internal/transport"
)

const (
	v4 = "4.0.20.3014"
	v5 = "5.0.0.1"
)

func hdr(v string) http.Header {
	h := http.Header{}
	if v != "" {
		h.Set("X-Application-Version", v)
	}
	return h
}

func answer(code int, v string) transport.End {
	return transport.End{Kind: transport.EndAnswer, Code: code, Header: hdr(v)}
}

func TestDecideTable(t *testing.T) {
	sonarr4 := transport.Identity{App: "Sonarr", AppVersion: v4}
	unanchored := transport.Identity{}
	type row struct {
		name    string
		sent    transport.Identity
		call    transport.Call
		end     transport.End
		verdict transport.Verdict
		next    transport.Next
	}
	rows := []row{
		{"row 1 probe 200 equal", sonarr4, transport.CallProbe, answer(200, v4), transport.VerdictAccept, transport.NextDone},
		{"row 1 read 200 equal", sonarr4, transport.CallRead, answer(200, v4), transport.VerdictAccept, transport.NextDone},
		{"row 1 by-id 200 equal", sonarr4, transport.CallReadByID, answer(200, v4), transport.VerdictAccept, transport.NextDone},
		{"row 1 unanchored probe adopts a well-formed header", unanchored, transport.CallProbe, answer(200, v5), transport.VerdictAccept, transport.NextDone},
		{"row 2 mutation 201 equal", sonarr4, transport.CallMutation, answer(201, v4), transport.VerdictAccept, transport.NextDone},
		{"row 2 mutation 204 equal", sonarr4, transport.CallMutation, answer(204, v4), transport.VerdictAccept, transport.NextDone},
		{"row 2 mutation 206 equal", sonarr4, transport.CallMutation, answer(206, v4), transport.VerdictAccept, transport.NextDone},
		{"row 3 read 204 is a failure", sonarr4, transport.CallRead, answer(204, v4), transport.VerdictStatus, transport.NextDone},
		{"row 3 read 206 is a failure", sonarr4, transport.CallRead, answer(206, v4), transport.VerdictStatus, transport.NextDone},
		{"row 3 by-id 204 is a failure", sonarr4, transport.CallReadByID, answer(204, v4), transport.VerdictStatus, transport.NextDone},
		{"row 3 probe 206 is a failure", sonarr4, transport.CallProbe, answer(206, v4), transport.VerdictStatus, transport.NextDone},
		{"row 3 read 429 retried", sonarr4, transport.CallRead, answer(429, v4), transport.VerdictStatus, transport.NextRetry},
		{"row 3 read 500 retried", sonarr4, transport.CallRead, answer(500, v4), transport.VerdictStatus, transport.NextRetry},
		{"row 3 read 599 retried", sonarr4, transport.CallRead, answer(599, v4), transport.VerdictStatus, transport.NextRetry},
		{"row 3 read 499 not retried", sonarr4, transport.CallRead, answer(499, v4), transport.VerdictStatus, transport.NextDone},
		{"row 3 read 600 not retried", sonarr4, transport.CallRead, answer(600, v4), transport.VerdictStatus, transport.NextDone},
		{"row 3 read 404 is a status", sonarr4, transport.CallRead, answer(404, v4), transport.VerdictStatus, transport.NextDone},
		{"row 3 by-id 404 is absent", sonarr4, transport.CallReadByID, answer(404, v4), transport.VerdictAbsent, transport.NextDone},
		{"row 3 anchored probe 503 retried", sonarr4, transport.CallProbe, answer(503, v4), transport.VerdictStatus, transport.NextRetry},
		{"row 3 unanchored probe 503 retried", unanchored, transport.CallProbe, answer(503, v4), transport.VerdictStatus, transport.NextRetry},
		{"row 3 mutation 503 not retried", sonarr4, transport.CallMutation, answer(503, v4), transport.VerdictStatus, transport.NextDone},
		{"row 3 mutation 302 is a failure", sonarr4, transport.CallMutation, answer(302, v4), transport.VerdictStatus, transport.NextDone},
		{"row 4 read header-less 502 goes fresh", sonarr4, transport.CallRead, answer(502, ""), transport.VerdictUnidentified, transport.NextFresh},
		{"row 4 read header-less 429 goes fresh", sonarr4, transport.CallRead, answer(429, ""), transport.VerdictUnidentified, transport.NextFresh},
		{"row 4 read header-less 404 is done", sonarr4, transport.CallRead, answer(404, ""), transport.VerdictUnidentified, transport.NextDone},
		{"row 4 by-id header-less 404 is never absent", sonarr4, transport.CallReadByID, answer(404, ""), transport.VerdictUnidentified, transport.NextDone},
		{"row 4 unanchored probe header-less 502 retried", unanchored, transport.CallProbe, answer(502, ""), transport.VerdictStatus, transport.NextRetry},
		{"row 4 unanchored probe header-less 404 done", unanchored, transport.CallProbe, answer(404, ""), transport.VerdictStatus, transport.NextDone},
		{"row 4 anchored probe header-less 502 done", sonarr4, transport.CallProbe, answer(502, ""), transport.VerdictUnidentified, transport.NextDone},
		{"row 4 mutation header-less 502 done", sonarr4, transport.CallMutation, answer(502, ""), transport.VerdictUnidentified, transport.NextDone},
		{"row 5 read header-less 200", sonarr4, transport.CallRead, answer(200, ""), transport.VerdictNoHeader, transport.NextDone},
		{"row 5 by-id header-less 200", sonarr4, transport.CallReadByID, answer(200, ""), transport.VerdictNoHeader, transport.NextDone},
		{"row 6 mutation header-less 201", sonarr4, transport.CallMutation, answer(201, ""), transport.VerdictUnconfirmed, transport.NextDone},
		{"row 7 anchored probe header-less 200", sonarr4, transport.CallProbe, answer(200, ""), transport.VerdictAccept, transport.NextDone},
		{"row 7 unanchored probe header-less 200", unanchored, transport.CallProbe, answer(200, ""), transport.VerdictAccept, transport.NextDone},
		{"row 8 read 200 from another version", sonarr4, transport.CallRead, answer(200, v5), transport.VerdictChanged, transport.NextFresh},
		{"row 8 read 503 from another version", sonarr4, transport.CallRead, answer(503, v5), transport.VerdictChanged, transport.NextFresh},
		{"row 8 a minor upgrade differs", sonarr4, transport.CallRead, answer(200, "4.0.21.1"), transport.VerdictChanged, transport.NextFresh},
		{"row 8 by-id 404 from another version", sonarr4, transport.CallReadByID, answer(404, v5), transport.VerdictChanged, transport.NextFresh},
		{"row 9 read not sent", sonarr4, transport.CallRead, transport.End{Kind: transport.EndNotSent}, transport.VerdictRefused, transport.NextFresh},
		{"row 9 by-id not sent", sonarr4, transport.CallReadByID, transport.End{Kind: transport.EndNotSent}, transport.VerdictRefused, transport.NextFresh},
		{"row 9 probe not sent", sonarr4, transport.CallProbe, transport.End{Kind: transport.EndNotSent}, transport.VerdictRefused, transport.NextDone},
		{"row 9 mutation not sent", sonarr4, transport.CallMutation, transport.End{Kind: transport.EndNotSent}, transport.VerdictRefused, transport.NextDone},
		{"row 10 mutation 500 from another version", sonarr4, transport.CallMutation, answer(500, v5), transport.VerdictChanged, transport.NextDone},
		{"row 11 mutation 201 from another version", sonarr4, transport.CallMutation, answer(201, v5), transport.VerdictUnconfirmed, transport.NextDone},
		{"row 12 anchored probe 200 from another version", sonarr4, transport.CallProbe, answer(200, v5), transport.VerdictChanged, transport.NextDone},
		{"row 12 anchored probe 500 from another version", sonarr4, transport.CallProbe, answer(500, v5), transport.VerdictChanged, transport.NextDone},
	}
	transportEnd := func(transient, hop bool) transport.End {
		return transport.End{Kind: transport.EndTransport, Transient: transient, HopFailed: hop}
	}
	for _, call := range []struct {
		name string
		call transport.Call
		sent transport.Identity
	}{
		{"read", transport.CallRead, sonarr4},
		{"by-id", transport.CallReadByID, sonarr4},
		{"anchored probe", transport.CallProbe, sonarr4},
		{"unanchored probe", transport.CallProbe, unanchored},
		{"mutation", transport.CallMutation, sonarr4},
	} {
		retry := transport.NextRetry
		if call.call == transport.CallMutation {
			retry = transport.NextDone
		}
		rows = append(rows,
			row{"row 13 " + call.name + " transient", call.sent, call.call, transportEnd(true, false), transport.VerdictTransport, retry},
			row{"row 13 " + call.name + " not transient", call.sent, call.call, transportEnd(false, false), transport.VerdictTransport, transport.NextDone},
		)
	}
	rows = append(rows,
		row{"row 14 read transient goes fresh", sonarr4, transport.CallRead, transportEnd(true, true), transport.VerdictTransport, transport.NextFresh},
		row{"row 14 by-id transient goes fresh", sonarr4, transport.CallReadByID, transportEnd(true, true), transport.VerdictTransport, transport.NextFresh},
		row{"row 14 read not transient done", sonarr4, transport.CallRead, transportEnd(false, true), transport.VerdictTransport, transport.NextDone},
		row{"row 14 anchored probe done", sonarr4, transport.CallProbe, transportEnd(true, true), transport.VerdictTransport, transport.NextDone},
	)
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			verdict, next := transport.Decide(tc.sent, tc.call, tc.end)
			if verdict != tc.verdict || next != tc.next {
				t.Errorf("Decide = (%v, %v), want (%v, %v)", verdict, next, tc.verdict, tc.next)
			}
		})
	}
}

func TestDecideHeaderAbsentWhenNotWellFormed(t *testing.T) {
	sent := transport.Identity{App: "Sonarr", AppVersion: v4}
	for _, value := range []string{"abc", "5", "5.0.0.0.1", "1234567890.0", v4 + " ", "4..0", ""} {
		t.Run(fmt.Sprintf("%q", value), func(t *testing.T) {
			end := transport.End{Kind: transport.EndAnswer, Code: 200, Header: http.Header{"X-Application-Version": {value}}}
			if verdict, _ := transport.Decide(sent, transport.CallRead, end); verdict != transport.VerdictNoHeader {
				t.Errorf("Decide verdict = %v, want VerdictNoHeader", verdict)
			}
		})
	}
	t.Run("repeated", func(t *testing.T) {
		end := transport.End{Kind: transport.EndAnswer, Code: 200, Header: http.Header{"X-Application-Version": {v4, v4}}}
		if verdict, _ := transport.Decide(sent, transport.CallRead, end); verdict != transport.VerdictNoHeader {
			t.Errorf("Decide verdict = %v, want VerdictNoHeader", verdict)
		}
	})
}

func TestParseAppVersion(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		want   string
		major  int
		ok     bool
	}{
		{"four runs", hdr(v4), v4, 4, true},
		{"two runs", hdr("5.0"), "5.0", 5, true},
		{"nine-digit run", hdr("123456789.1"), "123456789.1", 123456789, true},
		{"absent", http.Header{}, "", 0, false},
		{"repeated", http.Header{"X-Application-Version": {v4, v4}}, "", 0, false},
		{"letters", hdr("abc"), "", 0, false},
		{"one run", hdr("5"), "", 0, false},
		{"five runs", hdr("5.0.0.0.1"), "", 0, false},
		{"ten-digit run", hdr("1234567890.1"), "", 0, false},
		{"trailing space", http.Header{"X-Application-Version": {"4.0 "}}, "", 0, false},
		{"empty run", hdr("4..0"), "", 0, false},
		{"sign", hdr("+4.0"), "", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, major, ok := transport.ParseAppVersion(tc.header)
			if v != tc.want || major != tc.major || ok != tc.ok {
				t.Errorf("ParseAppVersion = (%q, %d, %v), want (%q, %d, %v)", v, major, ok, tc.want, tc.major, tc.ok)
			}
		})
	}
}
