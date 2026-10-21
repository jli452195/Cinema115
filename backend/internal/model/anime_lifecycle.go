package model

// Nil options use the ordinary subscription defaults. Zero inactive days
// explicitly disables inactivity archiving without disabling completion.
func (f AnimeForm) ArchiveOnCompletion() bool { return f.AutoArchive == nil || *f.AutoArchive }

func (f AnimeForm) InactiveDays() int {
	if f.InactiveArchiveDays == nil {
		return 60
	}
	return *f.InactiveArchiveDays
}

type AnimeLifecycle struct {
	ArchiveReason   string `json:"archiveReason,omitempty"`
	ArchivedAt      *int64 `json:"archivedAt,omitempty"`
	QuietSince      *int64 `json:"quietSince,omitempty"`
	CompletionIssue string `json:"completionIssue,omitempty"`
}

// RSS policy has its own revision: changing a polling interval must not
// invalidate a download's fixed account/directory settings.
type AnimeRSSSettings struct {
	Revision            int64 `json:"revision"`
	PollMinutes         int   `json:"pollMinutes"`
	AutoArchive         bool  `json:"autoArchive"`
	InactiveArchiveDays int   `json:"inactiveArchiveDays"`
}

func (s AnimeRSSSettings) Valid() bool {
	return s.Revision > 0 && s.Revision < AnimeMaxRevision && s.PollMinutes >= 10 && s.PollMinutes <= 1440 && (s.InactiveArchiveDays == 0 || s.InactiveArchiveDays >= 30 && s.InactiveArchiveDays <= 365)
}

func (f AnimeForm) WithRSSDefaults(s AnimeRSSSettings) AnimeForm {
	if f.AutoArchive == nil {
		f.AutoArchive = &s.AutoArchive
	}
	if f.InactiveArchiveDays == nil {
		f.InactiveArchiveDays = &s.InactiveArchiveDays
	}
	return f
}
