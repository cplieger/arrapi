package arrapi

// Series is a Sonarr series.
type Series struct {
	Statistics       *SeriesStatistics `json:"statistics"`
	OriginalLanguage *Language         `json:"originalLanguage"`
	Title            string            `json:"title"`
	SortTitle        string            `json:"sortTitle"`
	ImdbID           string            `json:"imdbId"`
	FirstAired       string            `json:"firstAired"`
	Path             string            `json:"path"`
	RootFolderPath   string            `json:"rootFolderPath"`
	Status           string            `json:"status"`
	Seasons          []Season          `json:"seasons"`
	AlternateTitles  []AlternateTitle  `json:"alternateTitles"`
	Tags             []int             `json:"tags"`
	ID               int               `json:"id"`
	TvdbID           int               `json:"tvdbId"`
	TmdbID           int               `json:"tmdbId"`
	Year             int               `json:"year"`
	QualityProfileID int               `json:"qualityProfileId"`
	Monitored        bool              `json:"monitored"`
}

// Season is per-season metadata on a Sonarr series.
type Season struct {
	Statistics   *SeasonStatistics `json:"statistics"`
	SeasonNumber int               `json:"seasonNumber"`
	Monitored    bool              `json:"monitored"`
}

// SeasonStatistics holds per-season episode counts.
type SeasonStatistics struct {
	EpisodeFileCount  int `json:"episodeFileCount"`
	EpisodeCount      int `json:"episodeCount"`
	TotalEpisodeCount int `json:"totalEpisodeCount"`
}

// SeriesStatistics holds series-level episode counts.
type SeriesStatistics struct {
	SeasonCount       int `json:"seasonCount"`
	EpisodeFileCount  int `json:"episodeFileCount"`
	EpisodeCount      int `json:"episodeCount"`
	TotalEpisodeCount int `json:"totalEpisodeCount"`
}

// Episode is a Sonarr episode.
type Episode struct {
	EpisodeFile           *EpisodeFile `json:"episodeFile"`
	Title                 string       `json:"title"`
	AirDate               string       `json:"airDate"`
	ID                    int          `json:"id"`
	SeriesID              int          `json:"seriesId"`
	SeasonNumber          int          `json:"seasonNumber"`
	EpisodeNumber         int          `json:"episodeNumber"`
	AbsoluteEpisodeNumber int          `json:"absoluteEpisodeNumber"`
	SceneSeasonNumber     int          `json:"sceneSeasonNumber"`
	SceneEpisodeNumber    int          `json:"sceneEpisodeNumber"`
	HasFile               bool         `json:"hasFile"`
	Monitored             bool         `json:"monitored"`
}

// EpisodeFile holds file details for a Sonarr episode.
type EpisodeFile struct {
	MediaInfo    *MediaInfo `json:"mediaInfo"`
	RelativePath string     `json:"relativePath"`
	Path         string     `json:"path"`
	SceneName    string     `json:"sceneName"`
	ReleaseGroup string     `json:"releaseGroup"`
	ID           int        `json:"id"`
	Size         int64      `json:"size"`
}

// Movie is a Radarr movie.
type Movie struct {
	MovieFile        *MovieFile       `json:"movieFile"`
	OriginalLanguage *Language        `json:"originalLanguage"`
	Title            string           `json:"title"`
	SortTitle        string           `json:"sortTitle"`
	ImdbID           string           `json:"imdbId"`
	InCinemas        string           `json:"inCinemas"`
	DigitalRelease   string           `json:"digitalRelease"`
	Path             string           `json:"path"`
	RootFolderPath   string           `json:"rootFolderPath"`
	Status           string           `json:"status"`
	AlternateTitles  []AlternateTitle `json:"alternateTitles"`
	Tags             []int            `json:"tags"`
	ID               int              `json:"id"`
	TmdbID           int              `json:"tmdbId"`
	Year             int              `json:"year"`
	QualityProfileID int              `json:"qualityProfileId"`
	HasFile          bool             `json:"hasFile"`
	Monitored        bool             `json:"monitored"`
}

// MovieFile holds file details for a Radarr movie.
type MovieFile struct {
	MediaInfo    *MediaInfo `json:"mediaInfo"`
	RelativePath string     `json:"relativePath"`
	Path         string     `json:"path"`
	SceneName    string     `json:"sceneName"`
	ReleaseGroup string     `json:"releaseGroup"`
	ID           int        `json:"id"`
	Size         int64      `json:"size"`
}

// MediaInfo holds media-analysis details for an episode or movie file.
type MediaInfo struct {
	AudioLanguages string `json:"audioLanguages"`
	Subtitles      string `json:"subtitles"`
	VideoCodec     string `json:"videoCodec"`
	AudioCodec     string `json:"audioCodec"`
}

// AlternateTitle is an alternate title for a series or movie.
type AlternateTitle struct {
	Title string `json:"title"`
}

// Language is a Sonarr/Radarr language reference, such as a series' or movie's
// original language. Only the identity fields (name + id) are modeled; the arr
// resource carries no further language detail this client needs.
type Language struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

// Tag is a Sonarr or Radarr tag.
type Tag struct {
	Label string `json:"label"`
	ID    int    `json:"id"`
}

// SystemStatus is the subset of a Sonarr/Radarr system-status response used for
// connectivity checks and version reporting.
type SystemStatus struct {
	Version      string `json:"version"`
	AppName      string `json:"appName"`
	InstanceName string `json:"instanceName"`
}

// QualityProfile is a Sonarr or Radarr quality profile. Only the identity
// fields are modeled; the profile's item/cutoff detail is out of scope.
type QualityProfile struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

// RootFolder is a configured root folder on a Sonarr or Radarr instance.
type RootFolder struct {
	Path       string `json:"path"`
	ID         int    `json:"id"`
	FreeSpace  int64  `json:"freeSpace"`
	Accessible bool   `json:"accessible"`
}
