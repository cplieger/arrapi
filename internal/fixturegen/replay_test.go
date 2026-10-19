package fixturegen_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v3/internal/fixturegen"
)

// replayDir writes files under a fresh directory and returns it.
func replayDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

type served struct {
	version, body string
	code          int
}

func get(t *testing.T, url string) served {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return served{version: resp.Header.Get("X-Application-Version"), body: string(b), code: resp.StatusCode}
}

func TestReplayServesTheRecordedAnswers(t *testing.T) {
	dir := replayDir(t, map[string]string{
		"system-status.json":                 `{"appName":"Sonarr","version":"4.0.20.3014"}`,
		"series-one.json":                    `[{"id":1}]`,
		"series-404.headers":                 "X-Application-Version: 4.0.20.3014\n",
		"series-404.json":                    `{"title":"error-1"}`,
		"v5-system-status-404.headers":       "X-Application-Version: 4.0.20.3014\n",
		"episodefile-missing-parent.headers": "",
		"episodefile-missing-parent.json":    `{"message":"error-2"}`,
	})
	p, err := fixturegen.NewReplay(dir, map[string]fixturegen.Route{
		"GET /api/v3/system/status":             {File: "system-status.json"},
		"GET /api/v3/series?x=1":                {File: "series-one.json"},
		"GET /api/v3/series/9":                  {Role: "series-404", Code: http.StatusNotFound},
		"GET /api/v5/system/status":             {Role: "v5-system-status-404", Code: http.StatusNotFound},
		"GET /api/v3/episodefile?seriesId=9":    {Role: "episodefile-missing-parent", Code: http.StatusNotFound},
		"GET /api/v3/episodefile?seriesId=9&x=": {Role: "episodefile-missing-parent", Code: http.StatusInternalServerError},
	})
	if err != nil {
		t.Fatalf("NewReplay: %v", err)
	}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)

	for _, tc := range []struct {
		uri  string
		want served
	}{
		{"/api/v3/system/status", served{"4.0.20.3014", `{"appName":"Sonarr","version":"4.0.20.3014"}`, 200}},
		{"/api/v3/series?x=1", served{"4.0.20.3014", `[{"id":1}]`, 200}},
		{"/api/v3/series/9", served{"4.0.20.3014", `{"title":"error-1"}`, 404}},
		{"/api/v5/system/status", served{"4.0.20.3014", "", 404}},
		{"/api/v3/episodefile?seriesId=9", served{"", `{"message":"error-2"}`, 404}},
		{"/api/v3/episodefile?seriesId=9&x=", served{"", `{"message":"error-2"}`, 500}},
		{"/api/v3/series", served{"", "", 404}},
		{"/api/v3/series?x=2", served{"", "", 404}},
	} {
		if got := get(t, srv.URL+tc.uri); got != tc.want {
			t.Errorf("GET %s = %+v, want %+v", tc.uri, got, tc.want)
		}
	}
	if got, want := p.Misses(), []string{"GET /api/v3/series", "GET /api/v3/series?x=2"}; !slices.Equal(got, want) {
		t.Errorf("Misses = %q, want %q", got, want)
	}
	if got := p.Requests(); len(got) != 8 || got[0] != "GET /api/v3/system/status" || got[7] != "GET /api/v3/series?x=2" {
		t.Errorf("Requests = %q, want the 8 requests in arrival order", got)
	}
}

func TestNewReplayRefuses(t *testing.T) {
	status := `{"appName":"Sonarr","version":"5.0.0.2668"}`
	for name, tc := range map[string]struct {
		files map[string]string
		route fixturegen.Route
		want  string
	}{
		"a missing data file": {
			map[string]string{"system-status.json": status}, fixturegen.Route{File: "series-gone.json"}, "series-gone.json",
		},
		"a missing headers file": {
			map[string]string{"system-status.json": status, "series-404.json": `{}`},
			fixturegen.Route{Role: "series-404", Code: 404},
			"series-404.headers",
		},
		"a headers file with another line": {
			map[string]string{"system-status.json": status, "series-404.headers": "Server: Kestrel\n"},
			fixturegen.Route{Role: "series-404", Code: 404},
			"series-404.headers",
		},
		"an error route without a code": {
			map[string]string{"system-status.json": status, "series-404.headers": ""},
			fixturegen.Route{Role: "series-404"},
			"series-404",
		},
		"a route naming a file and a role": {
			map[string]string{"system-status.json": status, "tags.json": `[]`, "series-404.headers": ""},
			fixturegen.Route{File: "tags.json", Role: "series-404", Code: 404},
			"GET /x",
		},
		"a route naming neither": {
			map[string]string{"system-status.json": status}, fixturegen.Route{}, "GET /x",
		},
		"no recorded version": {
			map[string]string{"system-status.json": `{"appName":"Sonarr"}`, "tags.json": `[]`},
			fixturegen.Route{File: "tags.json"},
			"system-status.json",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := fixturegen.NewReplay(replayDir(t, tc.files), map[string]fixturegen.Route{"GET /x": tc.route})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("NewReplay error = %v, want one naming %q", err, tc.want)
			}
		})
	}
}
