package sonarr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cplieger/arrapi/v3/internal/fixturegen"
)

// The order tests read the recorded Sonarr 5.0 order series of
// testdata/sonarr/v5-api-v5/ through a replaying client and assert the named values
// of each series' order. No Sonarr 4 series has another order than aired, so these
// fixtures exist only for Sonarr v5 (API v5).

// orderSeries returns the one series the recorded list file answers.
func orderSeries(t *testing.T, file string) Series {
	t.Helper()
	c, _ := replay(t, sonarrV5APIv5Shape(), file)
	return onlySeries(t, c)
}

func onlySeries(t *testing.T, c *Client) Series {
	t.Helper()
	got, err := c.Series(t.Context())
	if err != nil || len(got) != 1 {
		t.Fatalf("Series = %+v, %v; want one series", got, err)
	}
	return got[0]
}

func wantOrder(t *testing.T, s Series, tvdb int, want Order) {
	t.Helper()
	if s.IDs.TVDB != tvdb {
		t.Fatalf("series %q has TVDB id %d, want %d; %s", s.Title, s.IDs.TVDB, tvdb, reRecord)
	}
	if s.Order != want {
		t.Errorf("%s: Order = %+v, want %+v", s.Title, s.Order, want)
	}
}

func TestOrderFireflyDVD(t *testing.T) {
	c, _ := replay(t, sonarrV5APIv5Shape(), "series-firefly.json")
	s := onlySeries(t, c)
	wantOrder(t, s, tvdbFirefly, Order{Type: "dvd", Name: "DVD Order"})
	eps, err := c.Episodes(t.Context(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	files, err := c.EpisodeFiles(t.Context(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 17 || len(files) != 14 {
		t.Fatalf("%d episodes and %d files, want 17 and 14", len(eps), len(files))
	}
	// In DVD order the pilot "Serenity", aired in December, is S01E01, and "The Train
	// Job", aired first, is S01E02.
	at := map[[2]int]Episode{}
	for _, e := range eps {
		at[[2]int{e.Season, e.Number}] = e
		if e.Absolute != 0 {
			t.Errorf("S%02dE%02d: Absolute = %d, want 0 under a non-official order", e.Season, e.Number, e.Absolute)
		}
	}
	for se, want := range map[[2]int]struct{ title, airDate string }{
		{1, 1}:  {"Serenity", "2002-12-20"},
		{1, 2}:  {"The Train Job", "2002-09-20"},
		{1, 14}: {"Objects in Space", "2002-12-13"},
		{0, 1}:  {"Serenity", "2005-09-03"},
	} {
		if e := at[se]; e.Title != want.title || e.AirDate != want.airDate {
			t.Errorf("S%02dE%02d = %q aired %q, want %q aired %q", se[0], se[1], e.Title, e.AirDate, want.title, want.airDate)
		}
	}
	byID := map[int]EpisodeFile{}
	for _, f := range files {
		byID[f.ID] = f
	}
	for _, e := range eps {
		f, ok := byID[e.FileID]
		switch {
		case e.Season == 1 && (!e.HasFile || !ok || f.Season != 1):
			t.Errorf("S01E%02d: HasFile %t, file %d in season %d", e.Number, e.HasFile, e.FileID, f.Season)
		case e.Season == 0 && e.HasFile:
			t.Errorf("special S00E%02d has a file", e.Number)
		case ok && (f.MediaInfo == nil || len(f.MediaInfo.AudioLanguages) != 1 || f.MediaInfo.AudioLanguages[0] != "eng"):
			t.Errorf("file %d: MediaInfo = %s, want one eng audio stream", f.ID, show(f.MediaInfo))
		}
	}
}

func TestOrderOfficeSuperfan(t *testing.T) {
	wantOrder(t, orderSeries(t, "series-office.json"), 73244, Order{Type: "alttwo", Name: "Superfan Order"})
}

func TestOrderMonogatariBook(t *testing.T) {
	s := orderSeries(t, "series-monogatari.json")
	wantOrder(t, s, 102261, Order{Type: "alternate", Name: "Book Order"})
	if len(s.Seasons) != 18 || s.Seasons[0] != (Season{Title: "Bakemonogatari", Number: 1}) || s.Seasons[1] != (Season{Title: "Kizumonogatari", Number: 2}) {
		t.Errorf("Seasons = %+v, want 18 Book Order seasons led by Bakemonogatari and Kizumonogatari", s.Seasons)
	}
}

func TestOrderHaruhi2009Series(t *testing.T) {
	s := orderSeries(t, "series-haruhi.json")
	wantOrder(t, s, 79414, Order{Type: "alternate", Name: "2009 Series"})
	if want := (Season{Title: "The Melancholy of Haruhi Suzumiya (2009)", Number: 1}); len(s.Seasons) != 1 || s.Seasons[0] != want {
		t.Errorf("Seasons = %+v, want [%+v]", s.Seasons, want)
	}
}

func TestOrderStarWarsResistanceDisneyPlus(t *testing.T) {
	wantOrder(t, orderSeries(t, "series-swresistance.json"), 351575, Order{Type: "alternate", Name: "Disney+ Order"})
}

// editedSeries replays the recorded series list file with edit applied to its one
// series, for a series shape no recording holds.
func editedSeries(t *testing.T, file string, edit func(map[string]any)) Series {
	t.Helper()
	src := fixtureDir(dirSonarrV5APIv5)
	dir := t.TempDir()
	for _, name := range []string{"api.json", "system-status.json", file} {
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatalf("%v; %s", err, reRecord)
		}
		if name == file {
			v, err := fixturegen.Decode(b)
			list, ok := v.([]any)
			if err != nil || !ok || len(list) != 1 {
				t.Fatalf("%s is not a one-series list; %s", file, reRecord)
			}
			edit(list[0].(map[string]any))
			if b, err = json.Marshal(list); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c, _ := replayDir(t, dir, map[string]fixturegen.Route{
		"GET /api":                  {File: "api.json"},
		"GET /api/v5/system/status": {File: "system-status.json"},
		"GET /api/v5/series":        {File: file},
	}, nil)
	return onlySeries(t, c)
}

// An unrefreshed series lists seasonTypes [], the default of Sonarr's migration 208.
func TestOrderEmptySeasonTypesKeepsTypeWithoutName(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"empty":  func(s map[string]any) { s["seasonTypes"] = []any{} },
		"absent": func(s map[string]any) { delete(s, "seasonTypes") },
		"null":   func(s map[string]any) { s["seasonTypes"] = nil },
	} {
		t.Run(name, func(t *testing.T) {
			wantOrder(t, editedSeries(t, "series-firefly.json", edit), tvdbFirefly, Order{Type: "dvd"})
		})
	}
}

func TestOrderEmptyTypeIsNotOfficial(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"empty":  func(s map[string]any) { s["seasonType"] = "" },
		"absent": func(s map[string]any) { delete(s, "seasonType") },
		"null":   func(s map[string]any) { s["seasonType"] = nil },
		"empty beside an empty-typed order": func(s map[string]any) {
			s["seasonType"] = ""
			s["seasonTypes"] = append(s["seasonTypes"].([]any), map[string]any{"type": "", "name": "Untyped Order"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			wantOrder(t, editedSeries(t, "series-codegeass.json", edit), tvdbCodeGeass, Order{})
		})
	}
}
