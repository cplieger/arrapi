package arrapi

// Series is a Sonarr series.
type Series struct {
	Statistics       *SeriesStatistics `json:"statistics,omitempty"`
	Title            string            `json:"title"`
	SortTitle        string            `json:"sortTitle,omitempty"`
	ImdbID           string            `json:"imdbId,omitempty"`
	Path             string            `json:"path,omitempty"`
	RootFolderPath   string            `json:"rootFolderPath,omitempty"`
	Status           string            `json:"status,omitempty"`
	Seasons          []Season          `json:"seasons,omitempty"`
	AlternateTitles  []AlternateTitle  `json:"alternateTitles,omitempty"`
	Tags             []int             `json:"tags"`
	ID               int               `json:"id"`
	TvdbID           int               `json:"tvdbId"`
	TmdbID           int               `json:"tmdbId,omitempty"`
	Year             int               `json:"year"`
	QualityProfileID int               `json:"qualityProfileId,omitempty"`
	Monitored        bool              `json:"monitored"`
}

// Season is per-season metadata on a Sonarr series.
type Season struct {
	Statistics   *SeasonStatistics `json:"statistics,omitempty"`
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
	EpisodeFile           *EpisodeFile `json:"episodeFile,omitempty"`
	Title                 string       `json:"title,omitempty"`
	AirDate               string       `json:"airDate,omitempty"`
	ID                    int          `json:"id"`
	SeriesID              int          `json:"seriesId"`
	SeasonNumber          int          `json:"seasonNumber"`
	EpisodeNumber         int          `json:"episodeNumber"`
	AbsoluteEpisodeNumber int          `json:"absoluteEpisodeNumber,omitempty"`
	HasFile               bool         `json:"hasFile"`
	Monitored             bool         `json:"monitored"`
}

// EpisodeFile holds file details for a Sonarr episode.
type EpisodeFile struct {
	MediaInfo    *MediaInfo `json:"mediaInfo,omitempty"`
	RelativePath string     `json:"relativePath,omitempty"`
	Path         string     `json:"path,omitempty"`
	SceneName    string     `json:"sceneName,omitempty"`
	ReleaseGroup string     `json:"releaseGroup,omitempty"`
	ID           int        `json:"id"`
	Size         int64      `json:"size"`
}

// Movie is a Radarr movie.
type Movie struct {
	MovieFile        *MovieFile       `json:"movieFile,omitempty"`
	Title            string           `json:"title"`
	SortTitle        string           `json:"sortTitle,omitempty"`
	ImdbID           string           `json:"imdbId,omitempty"`
	Path             string           `json:"path,omitempty"`
	RootFolderPath   string           `json:"rootFolderPath,omitempty"`
	Status           string           `json:"status,omitempty"`
	AlternateTitles  []AlternateTitle `json:"alternateTitles,omitempty"`
	Tags             []int            `json:"tags"`
	ID               int              `json:"id"`
	TmdbID           int              `json:"tmdbId"`
	Year             int              `json:"year"`
	QualityProfileID int              `json:"qualityProfileId,omitempty"`
	HasFile          bool             `json:"hasFile"`
	Monitored        bool             `json:"monitored"`
}

// MovieFile holds file details for a Radarr movie.
type MovieFile struct {
	MediaInfo    *MediaInfo `json:"mediaInfo,omitempty"`
	RelativePath string     `json:"relativePath,omitempty"`
	Path         string     `json:"path,omitempty"`
	SceneName    string     `json:"sceneName,omitempty"`
	ReleaseGroup string     `json:"releaseGroup,omitempty"`
	ID           int        `json:"id"`
	Size         int64      `json:"size"`
}

// MediaInfo holds media-analysis details for an episode or movie file.
type MediaInfo struct {
	AudioLanguages string `json:"audioLanguages,omitempty"`
	Subtitles      string `json:"subtitles,omitempty"`
	VideoCodec     string `json:"videoCodec,omitempty"`
	AudioCodec     string `json:"audioCodec,omitempty"`
}

// AlternateTitle is an alternate title for a series or movie.
type AlternateTitle struct {
	Title string `json:"title"`
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
	AppName      string `json:"appName,omitempty"`
	InstanceName string `json:"instanceName,omitempty"`
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
	FreeSpace  int64  `json:"freeSpace,omitempty"`
	Accessible bool   `json:"accessible,omitempty"`
}
