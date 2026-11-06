package grabfail_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/arrapi/v3/internal/grabfail"
)

// The arrs' own refusal sentences, from Sonarr's (API v3 and v5) and Radarr's
// ReleaseController.cs.
const (
	cacheMiss      = "Couldn't find requested release in cache, try searching again"
	unparsed       = "Unable to parse episodes in the release, will need to be manually provided"
	noSeries       = "Unable to find matching series and episodes, will need to be manually provided"
	noMovie        = "Unable to find matching movie, will need to be manually provided"
	indexerFailed  = "Getting release from indexer failed"
	indexerFailedX = "Getting release from indexer failed: Connection refused"
)

func TestClassifyReadsTheArrsSentences(t *testing.T) {
	for name, tc := range map[string]struct {
		se   *arrapi.StatusError
		want arrapi.GrabReason
	}{
		"Sonarr 4's header-less cache miss":    {&arrapi.StatusError{Code: 404, Message: cacheMiss}, arrapi.GrabNotCached},
		"Sonarr 5's attributed cache miss":     {&arrapi.StatusError{Code: 404, Message: cacheMiss, Attributed: true}, arrapi.GrabNotCached},
		"episodes not parsed":                  {&arrapi.StatusError{Code: 404, Message: unparsed}, arrapi.GrabUnmapped},
		"no matching series":                   {&arrapi.StatusError{Code: 404, Message: noSeries, Attributed: true}, arrapi.GrabUnmapped},
		"no matching movie":                    {&arrapi.StatusError{Code: 404, Message: noMovie}, arrapi.GrabUnmapped},
		"the indexer failed":                   {&arrapi.StatusError{Code: 409, Message: indexerFailed}, arrapi.GrabUnavailable},
		"Sonarr 5's indexer failure and cause": {&arrapi.StatusError{Code: 409, Message: indexerFailedX, Attributed: true}, arrapi.GrabUnavailable},
		"a sentence padded by spaces":          {&arrapi.StatusError{Code: 404, Message: "  " + cacheMiss + "\n"}, arrapi.GrabNotCached},
	} {
		t.Run(name, func(t *testing.T) {
			err := grabfail.Classify(tc.se)
			ge, ok := errors.AsType[*arrapi.GrabError](err)
			if !ok || ge.Reason != tc.want || ge.Err != tc.se {
				t.Fatalf("Classify = %#v, want a *GrabError{Reason %d} over the input", err, tc.want)
			}
			if se, ok := errors.AsType[*arrapi.StatusError](err); !ok || se != tc.se {
				t.Errorf("errors.As(*StatusError) through the GrabError = %v, %v; want the input", se, ok)
			}
		})
	}
}

func TestClassifyLeavesAnyOtherErrorUnchanged(t *testing.T) {
	cached := &arrapi.StatusError{Code: 404, Message: cacheMiss}
	for name, err := range map[string]error{
		"a proxy's 404":                      &arrapi.StatusError{Code: 404, Message: "Not Found"},
		"a 404 web page, no message":         &arrapi.StatusError{Code: 404, Body: "<html><body>" + cacheMiss + "</body></html>"},
		"a 409 database constraint":          &arrapi.StatusError{Code: 409, Message: "UNIQUE constraint failed: History.Id"},
		"a 500 with the cache sentence":      &arrapi.StatusError{Code: 500, Message: cacheMiss},
		"a 409 with the cache sentence":      &arrapi.StatusError{Code: 409, Message: cacheMiss},
		"a 404 with the indexer sentence":    &arrapi.StatusError{Code: 404, Message: indexerFailed},
		"a 409 with the unmapped sentence":   &arrapi.StatusError{Code: 409, Message: noSeries},
		"the indexer sentence run on":        &arrapi.StatusError{Code: 409, Message: indexerFailed + "ed"},
		"the cache sentence in another case": &arrapi.StatusError{Code: 404, Message: "couldn't find requested release in cache, try searching again"},
		"the unmapped head alone":            &arrapi.StatusError{Code: 404, Message: "Unable to find matching series"},
		"the unmapped tail alone":            &arrapi.StatusError{Code: 404, Message: "Release, will need to be manually provided"},
		"a 302":                              &arrapi.StatusError{Code: 302, Message: cacheMiss},
		"another version's 404":              &arrapi.ServerChangedError{Status: cached, From: "4.0.1.1", To: "5.0.0.1"},
		"a wrapped 404":                      fmt.Errorf("wrapped: %w", cached),
		"not a status":                       errors.New("arrapi: request /api/v3/release: connection refused"),
		"an outcome unknown":                 fmt.Errorf("%w: %w", arrapi.ErrCommandOutcomeUnknown, errors.New("no header")),
	} {
		t.Run(name, func(t *testing.T) {
			if got := grabfail.Classify(err); got != err {
				t.Errorf("Classify = %#v, want the input unchanged", got)
			}
		})
	}
	t.Run("nil", func(t *testing.T) {
		if got := grabfail.Classify(nil); got != nil {
			t.Errorf("Classify(nil) = %#v, want nil", got)
		}
	})
}

// Sonarr 5's 409 quotes its download error, and TorrentClientBase's
// "Failed to parse magnetlink for episode '{0}': '{1}'" quotes the release's magnet
// link, whose announce URL can hold a private tracker's passkey.
const passkey = "PASSKEY0451"

func TestClassifyMasksMagnetLinks(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:zz&tr=https%3A%2F%2Ftracker.example%2Fannounce%2F" + passkey
	parseFailure := indexerFailed + ": Failed to parse magnetlink for episode 'Show S01E01': '" + magnet + "'"
	head := indexerFailed + ": Failed to parse magnetlink for episode 'Show S01E01': '"
	jsonHead := `{"message":"` + indexerFailed + `: Failed to parse magnetlink for episode \u0027Show S01E01\u0027: \u0027`
	for name, tc := range map[string]struct {
		message, body         string
		wantMessage, wantBody string
		want                  arrapi.GrabReason
	}{
		"Sonarr 5's magnet parse failure": {
			message:     parseFailure,
			body:        jsonHead + `magnet:?xt=urn:btih:zz\u0026tr=https%3A%2F%2Ftracker.example%2Fannounce%2F` + passkey + `\u0027"}`,
			wantMessage: head + "[***]",
			wantBody:    jsonHead + "[***]",
			want:        arrapi.GrabUnavailable,
		},
		"a display name holding a space": {
			message:     head + "magnet:?xt=urn:btih:zz&dn=Frieren Beyond Journeys End&tr=" + passkey + "'",
			body:        jsonHead + `magnet:?xt=urn:btih:zz\u0026dn=Frieren Beyond Journeys End\u0026tr=` + passkey + `\u0027"}`,
			wantMessage: head + "[***]",
			wantBody:    jsonHead + "[***]",
			want:        arrapi.GrabUnavailable,
		},
		"a display name holding an apostrophe": {
			message:     head + "magnet:?xt=urn:btih:zz&dn=Frieren.Journey's.End&tr=" + passkey + "'",
			body:        jsonHead + `magnet:?xt=urn:btih:zz\u0026dn=Frieren.Journey\u0027s.End\u0026tr=` + passkey + `\u0027"}`,
			wantMessage: head + "[***]",
			wantBody:    jsonHead + "[***]",
			want:        arrapi.GrabUnavailable,
		},
		"a display name holding a quote": {
			message:     head + `magnet:?xt=urn:btih:zz&dn=Show."Quoted"&tr=` + passkey + "'",
			body:        jsonHead + `magnet:?xt=urn:btih:zz\u0026dn=Show.\"Quoted\"\u0026tr=` + passkey + `\u0027"}`,
			wantMessage: head + "[***]",
			wantBody:    jsonHead + "[***]",
			want:        arrapi.GrabUnavailable,
		},
		"a display name holding a line break": {
			message:     head + "magnet:?xt=urn:btih:zz&dn=Show\r\nS01&tr=" + passkey + "'",
			body:        jsonHead + `magnet:?xt=urn:btih:zz\u0026dn=Show\r\nS01\u0026tr=` + passkey + `\u0027"}`,
			wantMessage: head + "[***]",
			wantBody:    jsonHead + "[***]",
			want:        arrapi.GrabUnavailable,
		},
		"two links, the second in another case": {
			message:     "Unable to add magnet:?tr=" + passkey + " or MAGNET:?tr=" + passkey + "\tnow",
			wantMessage: "Unable to add [***]",
		},
		"a link the body quotes": {
			body:     `"magnet:?tr=` + passkey + `" then magnet:` + passkey,
			wantBody: `"[***]`,
		},
		"a link the transport cut": {
			body:     `{"message":"` + indexerFailed + `: \u0027magnet:?tr=` + passkey[:4] + "...",
			wantBody: `{"message":"` + indexerFailed + `: \u0027[***]...`,
		},
		"the scheme alone": {
			message:     indexerFailed + ": magnet:",
			wantMessage: indexerFailed + ": [***]",
			want:        arrapi.GrabUnavailable,
		},
		"no link": {
			message:     indexerFailedX,
			body:        `{"message":"` + indexerFailedX + `"}`,
			wantMessage: indexerFailedX,
			wantBody:    `{"message":"` + indexerFailedX + `"}`,
			want:        arrapi.GrabUnavailable,
		},
	} {
		t.Run(name, func(t *testing.T) {
			se := &arrapi.StatusError{Code: 409, Message: tc.message, Body: tc.body}
			err := grabfail.Classify(se)
			if se.Message != tc.wantMessage || se.Body != tc.wantBody {
				t.Errorf("Message, Body = %q, %q; want %q, %q", se.Message, se.Body, tc.wantMessage, tc.wantBody)
			}
			if strings.Contains(err.Error(), passkey) {
				t.Errorf("Error() = %q, holds the passkey", err.Error())
			}
			ge, ok := errors.AsType[*arrapi.GrabError](err)
			switch {
			case tc.want == 0 && ok:
				t.Errorf("Classify = %v, want a plain *arrapi.StatusError", err)
			case tc.want != 0 && (!ok || ge.Reason != tc.want || ge.Err != se):
				t.Errorf("Classify = %#v, want a *GrabError{Reason %d} over the input", err, tc.want)
			}
		})
	}
	t.Run("another version's refusal", func(t *testing.T) {
		se := &arrapi.StatusError{Code: 409, Message: parseFailure, Body: parseFailure}
		changed := &arrapi.ServerChangedError{Status: se, From: "4.0.1.1", To: "5.0.0.1"}
		if got := grabfail.Classify(changed); got != error(changed) {
			t.Fatalf("Classify = %#v, want the input unchanged", got)
		}
		if strings.Contains(se.Message, passkey) || strings.Contains(se.Body, passkey) || strings.Contains(changed.Error(), passkey) {
			t.Errorf("the inner refusal still holds the passkey: %q, %q, %q", se.Message, se.Body, changed.Error())
		}
	})
}
