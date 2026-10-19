package sonarr

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"testing"
)

// equivalenceExcluded are the Series fields Features marks as Sonarr v5 (API v5) only,
// which the oracle does not compare; "[]" stands for every index. Episode.TVDBID is
// compared only where the Sonarr v5 (API v5) answer has one: 0 means unknown, and the
// recorded Sonarr 5 series had not been refreshed since its metadata carried the id.
// Every other field of Series, Episode and EpisodeFile is compared.
var equivalenceExcluded = []string{
	"Series.OriginalTitle",
	"Series.Language",
	"Series.Translations",
	"Series.IDs.AniList",
	"Series.Order.Name",
	"Series.Seasons[].Title",
}

var indexPattern = regexp.MustCompile(`\[[^]]*\]`)

// differences returns one "path: Sonarr v4 (API v3) <a>, Sonarr v5 (API v5) <b>" line
// per leaf, or per slice whose length or nil-ness differs, where a and b differ,
// skipping the excluded paths.
func differences(path string, a, b any) []string {
	var out []string
	walkDiff(path, reflect.ValueOf(a), reflect.ValueOf(b), &out)
	return out
}

func walkDiff(path string, a, b reflect.Value, out *[]string) {
	if slices.Contains(equivalenceExcluded, indexPattern.ReplaceAllString(path, "[]")) {
		return
	}
	differ := func() {
		*out = append(*out, fmt.Sprintf("%s: Sonarr v4 (API v3) %s, Sonarr v5 (API v5) %s", path, show(a.Interface()), show(b.Interface())))
	}
	switch a.Kind() {
	case reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			if a.IsNil() != b.IsNil() {
				differ()
			}
			return
		}
		walkDiff(path, a.Elem(), b.Elem(), out)
	case reflect.Struct:
		for i := range a.NumField() {
			walkDiff(path+"."+a.Type().Field(i).Name, a.Field(i), b.Field(i), out)
		}
	case reflect.Slice:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			differ()
			return
		}
		for i := range a.Len() {
			walkDiff(fmt.Sprintf("%s[%d]", path, i), a.Index(i), b.Index(i), out)
		}
	default:
		if !a.Equal(b) {
			differ()
		}
	}
}

// episodeDifferences pairs the two engines' episodes, and files, by id.
func episodeDifferences(v4, v5 []Episode) []string {
	if len(v4) != len(v5) {
		return []string{fmt.Sprintf("Episodes: Sonarr v4 (API v3) %d, Sonarr v5 (API v5) %d", len(v4), len(v5))}
	}
	byID := map[int]Episode{}
	for _, e := range v5 {
		byID[e.ID] = e
	}
	var out []string
	for _, a := range v4 {
		b, ok := byID[a.ID]
		if !ok {
			out = append(out, fmt.Sprintf("Episode[id=%d]: only on Sonarr v4 (API v3)", a.ID))
			continue
		}
		if b.TVDBID == 0 {
			b.TVDBID = a.TVDBID
		}
		out = append(out, differences(fmt.Sprintf("Episode[id=%d]", a.ID), a, b)...)
	}
	return out
}

func fileDifferences(v4, v5 []EpisodeFile) []string {
	if len(v4) != len(v5) {
		return []string{fmt.Sprintf("EpisodeFiles: Sonarr v4 (API v3) %d, Sonarr v5 (API v5) %d", len(v4), len(v5))}
	}
	byID := map[int]EpisodeFile{}
	for _, f := range v5 {
		byID[f.ID] = f
	}
	var out []string
	for _, a := range v4 {
		b, ok := byID[a.ID]
		if !ok {
			out = append(out, fmt.Sprintf("EpisodeFile[id=%d]: only on Sonarr v4 (API v3)", a.ID))
			continue
		}
		out = append(out, differences(fmt.Sprintf("EpisodeFile[id=%d]", a.ID), a, b)...)
	}
	return out
}

// codeGeass is what one engine reads of Code Geass from its recording.
type codeGeass struct {
	series   Series
	episodes []Episode
	files    []EpisodeFile
}

func readCodeGeass(t *testing.T, sh sonarrShape) codeGeass {
	t.Helper()
	c, _ := replay(t, sh, "series-codegeass.json")
	list, err := c.Series(t.Context())
	if err != nil {
		t.Fatalf("%s Series: %v", sh.label, err)
	}
	var cg codeGeass
	for _, s := range list {
		if s.IDs.TVDB == tvdbCodeGeass {
			cg.series = s
		}
	}
	if cg.series.ID == 0 {
		t.Fatalf("%s lists no Code Geass; %s", sh.label, reRecord)
	}
	if cg.episodes, err = c.EpisodesWithFiles(t.Context(), cg.series.ID); err != nil {
		t.Fatalf("%s EpisodesWithFiles: %v", sh.label, err)
	}
	if cg.files, err = c.EpisodeFiles(t.Context(), cg.series.ID); err != nil {
		t.Fatalf("%s EpisodeFiles: %v", sh.label, err)
	}
	return cg
}

func (cg codeGeass) differences(v5 codeGeass) []string {
	return slices.Concat(
		differences("Series", cg.series, v5.series),
		episodeDifferences(cg.episodes, v5.episodes),
		fileDifferences(cg.files, v5.files),
	)
}

// TestEngineEquivalenceCodeGeass reads Code Geass, in official order on both servers,
// through each engine from its own recording and requires the same normalized values.
func TestEngineEquivalenceCodeGeass(t *testing.T) {
	v4 := readCodeGeass(t, sonarrV4APIv3Shape())
	v5 := readCodeGeass(t, sonarrV5APIv5Shape())
	if len(v4.episodes) == 0 || len(v4.files) == 0 {
		t.Fatalf("the recordings hold %d episodes and %d files; %s", len(v4.episodes), len(v4.files), reRecord)
	}
	for _, d := range v4.differences(v5) {
		t.Error(d)
	}
}

// TestEquivalenceOracleNamesEveryDifference plants differences in a copy of the
// Sonarr v5 (API v5) values and requires one line naming each, while an excluded field
// and a zero Sonarr v5 (API v5) episode TVDB id stay silent.
func TestEquivalenceOracleNamesEveryDifference(t *testing.T) {
	v4 := readCodeGeass(t, sonarrV4APIv3Shape())
	v5 := readCodeGeass(t, sonarrV5APIv5Shape())
	v5.series.FileCount = 50
	v5.series.Order.Official = false
	v5.series.Seasons[2].Title = "planted"
	v5.series.Translations = nil
	v5.series.Seasons[1].Number = 9
	v5.episodes = slices.Clone(v5.episodes)
	v5.episodes[0].Absolute = 0
	v5.episodes[1].TVDBID = 1
	v5.files = slices.Clone(v5.files)
	mi := *v5.files[0].MediaInfo
	mi.AudioLanguages = []string{"jpn", "eng"}
	v5.files[0].MediaInfo = &mi
	v5.files[1].Revision = nil

	want := []string{
		"Series.Seasons[1].Number: Sonarr v4 (API v3) 1, Sonarr v5 (API v5) 9",
		"Series.Order.Official: Sonarr v4 (API v3) true, Sonarr v5 (API v5) false",
		"Series.FileCount: Sonarr v4 (API v3) 51, Sonarr v5 (API v5) 50",
		"Episode[id=1].Absolute: Sonarr v4 (API v3) 1, Sonarr v5 (API v5) 0",
		"Episode[id=2].TVDBID: Sonarr v4 (API v3) 328516, Sonarr v5 (API v5) 1",
		`EpisodeFile[id=1].MediaInfo.AudioLanguages: Sonarr v4 (API v3) []string{"jpn", "jpn", "eng"}, Sonarr v5 (API v5) []string{"jpn", "eng"}`,
		"EpisodeFile[id=2].Revision: Sonarr v4 (API v3) &{Version:1 IsRepack:false}, Sonarr v5 (API v5) nil",
	}
	if got := v4.differences(v5); !slices.Equal(got, want) {
		t.Errorf("differences =\n  %q\nwant\n  %q", got, want)
	}
}
