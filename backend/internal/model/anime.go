package model

// Anime models belong exclusively to subscription/download/organization.
// No playback account, credential, or media-server object is persisted here.
const (
	AnimeSchemaVersion       = 1
	AnimeMaxRevision   int64 = 1<<53 - 1
	AnimeMaxSources          = 5
)

type AnimeMetadata struct {
	SchemaVersion      int    `json:"schemaVersion"`
	BangumiUnavailable bool   `json:"bangumiUnavailable,omitempty"`
	Synopsis           string `json:"synopsis"`
	CoverURL           string `json:"coverUrl"`
	AirDate            string `json:"airDate"`
	TotalEpisodes      *int   `json:"totalEpisodes"`
}

type AnimeEntry struct {
	ID            string        `json:"id"`
	BangumiID     *string       `json:"bangumiId"`
	EditionKey    string        `json:"editionKey"`
	Title         string        `json:"title"`
	OriginalTitle string        `json:"originalTitle"`
	Metadata      AnimeMetadata `json:"metadata"`
	Revision      int64         `json:"revision"`
	CreatedAt     int64         `json:"createdAt"`
	UpdatedAt     int64         `json:"updatedAt"`
}

type AnimeScope struct {
	Kind     string `json:"kind"`
	First    *int   `json:"first,omitempty"`
	Last     *int   `json:"last,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

type AnimeChapterCorrespondence struct {
	SourceEpisode  int `json:"sourceEpisode"`
	ChapterEpisode int `json:"chapterEpisode"`
}

type AnimeBinding struct {
	SchemaVersion         int                         `json:"schemaVersion"`
	Confirmed             bool                        `json:"confirmed"`
	Scope                 *AnimeScope                 `json:"scope"`
	ChapterCorrespondence *AnimeChapterCorrespondence `json:"chapterCorrespondence"`
	Review                *AnimeSourceReview          `json:"review,omitempty"`
}

// Review records the explicit source-review action, scoped to the immutable
// publisher identity. The authenticated save operation records its actor.
type AnimeSourceReview struct {
	Method  string `json:"method"`
	ItemID  string `json:"itemId"`
	GroupID string `json:"groupId"`
}

// AnimeSpec describes title-declared properties, never verified file tracks.
// Empty dimensions are unconstrained in a profile and unknown on a release.
type AnimeSpec struct {
	Resolution   string   `json:"resolution,omitempty"`
	Languages    []string `json:"languages,omitempty"`
	SubtitleMode string   `json:"subtitleMode,omitempty"`
	Codec        string   `json:"codec,omitempty"`
	Container    string   `json:"container,omitempty"`
	BitDepth     string   `json:"bitDepth,omitempty"`
}

type AnimeRules struct {
	SchemaVersion  int         `json:"schemaVersion"`
	Resolution     *string     `json:"resolution"`
	Include        []string    `json:"include"`
	Exclude        []string    `json:"exclude"`
	MinimumEpisode *int        `json:"minimumEpisode"`
	SpecMode       string      `json:"specMode,omitempty"`
	SpecProfiles   []AnimeSpec `json:"specProfiles,omitempty"`
}

type AnimeForm struct {
	SchemaVersion int `json:"schemaVersion"`
}

type AnimeSubscription struct {
	Naming         *AnimeNamingPlan `json:"naming"`
	ID             string           `json:"id"`
	EntryID        string           `json:"entryId"`
	Status         string           `json:"status"`
	Revision       int64            `json:"revision"`
	RunGeneration  int64            `json:"runGeneration"`
	FirstEnabledAt *int64           `json:"firstEnabledAt"`
	Form           AnimeForm        `json:"form"`
	CreatedAt      int64            `json:"createdAt"`
	UpdatedAt      int64            `json:"updatedAt"`
	Sources        []AnimeSource    `json:"sources"`
}

type AnimeSource struct {
	ID               string       `json:"id"`
	SubscriptionID   string       `json:"subscriptionId"`
	Role             string       `json:"role"`
	Position         int          `json:"position"`
	Status           string       `json:"configuredState"`
	RunWhenEnabled   bool         `json:"runWhenEnabled"`
	Provider         string       `json:"provider"`
	SourceKey        *string      `json:"sourceKey"`
	ItemID           *string      `json:"itemId"`
	GroupID          *string      `json:"groupId"`
	GroupName        *string      `json:"groupName"`
	NamespaceID      string       `json:"namespaceId"`
	Binding          AnimeBinding `json:"binding"`
	Rules            AnimeRules   `json:"rules"`
	Revision         int64        `json:"revision"`
	RunGeneration    int64        `json:"runGeneration"`
	ActiveBaselineID *string      `json:"activeBaselineId"`
	Health           string       `json:"health"`
	CheckedAt        *int64       `json:"checkedAt"`
	AttemptedAt      *int64       `json:"attemptedAt"`
	CheckToken       *string      `json:"checkToken"`
	NextCheckAt      *int64       `json:"nextCheckAt"`
	RetryAfter       *int64       `json:"retryAfter"`
	ErrorCode        *string      `json:"errorCode"`
	CreatedAt        int64        `json:"createdAt"`
	UpdatedAt        int64        `json:"updatedAt"`
}

// AcceptsIdentity allows a draft's unset identity fields to be completed once.
// A chosen item/group cannot be cleared or rebound to another source.
func (s AnimeSource) AcceptsIdentity(provider string, itemID, groupID *string) bool {
	accept := func(old, next *string) bool {
		if old != nil {
			return next != nil && *old == *next
		}
		return next == nil || s.Status == "draft" && s.ActiveBaselineID == nil
	}
	return s.Provider == provider && accept(s.ItemID, itemID) && accept(s.GroupID, groupID)
}

// Config writes deliberately omit execution state, health, namespace and keys.
// New IDs and source keys are generated by storage; omitted sources are kept.
type AnimeSourceConfig struct {
	Ref              string       `json:"ref"`
	ID               string       `json:"id"`
	ExpectedRevision int64        `json:"expectedRevision"`
	Role             string       `json:"role"`
	Position         int          `json:"position"`
	Status           string       `json:"status"`
	RunWhenEnabled   bool         `json:"runWhenEnabled"`
	Provider         string       `json:"provider"`
	ItemID           *string      `json:"itemId"`
	GroupID          *string      `json:"groupId"`
	GroupName        *string      `json:"groupName"`
	Binding          AnimeBinding `json:"binding"`
	Rules            AnimeRules   `json:"rules"`
}

type AnimeSourceRemoval struct {
	ID               string `json:"id"`
	ExpectedRevision int64  `json:"expectedRevision"`
}

type AnimeSubscriptionConfig struct {
	Naming           *AnimeNamingPlan     `json:"naming"`
	ID               string               `json:"id"`
	EntryID          string               `json:"entryId"`
	ExpectedRevision int64                `json:"expectedRevision"`
	Status           string               `json:"status"`
	Form             AnimeForm            `json:"form"`
	Sources          []AnimeSourceConfig  `json:"sources"`
	RemovedSources   []AnimeSourceRemoval `json:"removedSources"`
}

type AnimeConfigReceipt struct {
	ID                string            `json:"id"`
	EntryID           string            `json:"entryId,omitempty"`
	Revision          int64             `json:"revision"`
	Status            string            `json:"status"`
	SourceIDs         map[string]string `json:"sourceIds"`
	SourceRevisions   map[string]int64  `json:"sourceRevisions"`
	RequiresRepreview bool              `json:"requiresRepreview"`
}

type AnimeReleaseParse struct {
	SchemaVersion int       `json:"schemaVersion"`
	Kind          string    `json:"kind"`
	RawEpisode    string    `json:"rawEpisode"`
	Episode       *int      `json:"episode"`
	Resolution    *string   `json:"resolution"`
	VersionLabel  string    `json:"versionLabel"`
	Spec          AnimeSpec `json:"spec"`
	SpecAmbiguous bool      `json:"specAmbiguous,omitempty"`
}

type AnimeRelease struct {
	ID                 string            `json:"id"`
	Provider           string            `json:"provider"`
	PostKey            string            `json:"postKey"`
	OriginalGUID       *string           `json:"originalGuid"`
	Title              string            `json:"title"`
	PublishedRaw       *string           `json:"publishedRaw"`
	PublishedAt        *int64            `json:"publishedAt"`
	TimeVersion        int64             `json:"timeVersion"`
	Parse              AnimeReleaseParse `json:"parse"`
	ParserVersion      int64             `json:"parserVersion"`
	ContentFingerprint string            `json:"contentFingerprint"`
	ObservedAt         int64             `json:"observedAt"`
}

// P1 explanations omit Selection and cannot authorize downloads. P2 execution
// choices also fix the target, generations and interpretation versions.
type AnimeDecisionSnapshot struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Title         string               `json:"title"`
	Binding       AnimeBinding         `json:"binding"`
	Rules         AnimeRules           `json:"rules"`
	Parse         AnimeReleaseParse    `json:"parse"`
	Selection     *AnimeSelectionBasis `json:"selection,omitempty"`
	RuleVersion   int64                `json:"ruleVersion,omitempty"`
	ParserVersion int64                `json:"parserVersion,omitempty"`
	TimeVersion   int64                `json:"timeVersion,omitempty"`
}

type AnimeReleaseDecision struct {
	SourceID           string                `json:"sourceId"`
	ReleaseID          string                `json:"releaseId"`
	Disposition        string                `json:"disposition"`
	ReasonCode         *string               `json:"reasonCode"`
	SourceRevision     int64                 `json:"sourceRevision"`
	ReleaseFingerprint string                `json:"releaseFingerprint"`
	Snapshot           AnimeDecisionSnapshot `json:"snapshot"`
	Generation         int64                 `json:"generation"`
	UpdatedAt          int64                 `json:"updatedAt"`
}

type AnimeReleaseRecord struct {
	Release  AnimeRelease         `json:"release"`
	Decision AnimeReleaseDecision `json:"decision"`
}

type AnimeSettings struct {
	Revision        int64   `json:"revision"`
	AccountID       *string `json:"accountId"`
	AccountRevision *int64  `json:"accountRevision"`
	UID             *string `json:"uid"`
	DownloadCID     *string `json:"downloadCid"`
	DownloadPath    *string `json:"downloadPath"`
	OrganizeCID     *string `json:"organizeCid"`
	OrganizePath    *string `json:"organizePath"`
}
