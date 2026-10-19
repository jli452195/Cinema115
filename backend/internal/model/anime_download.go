package model

import "strm115/backend/internal/animeresource"

// AnimeSelectionBasis is recorded when execution is explicitly authorized,
// separately from P1 form selections. Activation/discovery creates pending
// choices before bounded resource verification; P1 saves cannot create them.
type AnimeSelectionBasis struct {
	Kind                   string              `json:"kind"` // initial_history or automatic
	BaselineID             string              `json:"baselineId"`
	SelectedAt             int64               `json:"selectedAt"`
	Target                 AnimeDownloadTarget `json:"target"`
	SubscriptionGeneration int64               `json:"subscriptionGeneration"`
	SourceGeneration       int64               `json:"sourceGeneration"`
}

type AnimeDownloadSource struct {
	SubscriptionID         string `json:"subscriptionId"`
	SubscriptionRevision   int64  `json:"subscriptionRevision"`
	SubscriptionGeneration int64  `json:"subscriptionGeneration"`
	SourceID               string `json:"sourceId"`
	SourceRevision         int64  `json:"sourceRevision"`
	SourceGeneration       int64  `json:"sourceGeneration"`
	Provider               string `json:"provider"`
	ItemID                 string `json:"itemId"`
	GroupID                string `json:"groupId"`
	GroupName              string `json:"groupName"`
	NamespaceID            string `json:"namespaceId"`
}

type AnimeDownloadSnapshot struct {
	SchemaVersion       int                        `json:"schemaVersion"`
	Target              AnimeDownloadTarget        `json:"target"`
	Protocol            string                     `json:"protocol"`
	InfoHash            string                     `json:"infoHash"`
	VerificationVersion int                        `json:"verificationVersion"`
	Evidence            animeresource.Evidence     `json:"evidence"`
	Source              AnimeDownloadSource        `json:"source"`
	Release             AnimeRelease               `json:"release"`
	Decision            AnimeDecisionSnapshot      `json:"decision"`
	Naming              *AnimeNamingPlan           `json:"naming"`
	Organization        *AnimeOrganizationSnapshot `json:"organization,omitempty"`
}

type AnimeDownloadJob struct {
	ID                 string                   `json:"id"`
	ClaimID            string                   `json:"claimId"`
	Attempt            int64                    `json:"attempt"`
	AccountID          string                   `json:"accountId"`
	AccountRevision    int64                    `json:"accountRevision"`
	UID                string                   `json:"uid"`
	SettingsRevision   int64                    `json:"settingsRevision"`
	ParentCID          string                   `json:"parentCid"`
	DirectoryName      string                   `json:"directoryName"`
	DirectoryCID       *string                  `json:"directoryCid"`
	Snapshot           AnimeDownloadSnapshot    `json:"snapshot"`
	DownloadState      string                   `json:"downloadState"`
	FileState          string                   `json:"fileState"`
	WorkflowState      string                   `json:"workflowState"`
	BlockCode          *string                  `json:"blockCode"`
	Tracking           *AnimeDownloadTracking   `json:"tracking"`
	ResultObjectID     *string                  `json:"resultObjectId"`
	ResultObjectKind   *string                  `json:"resultObjectKind"`
	NextRunAt          *int64                   `json:"nextRunAt"`
	DirectoryOperation *AnimeDirectoryOperation `json:"directoryOperation"`
	Files              *AnimeFileManifest       `json:"files"`
	Revision           int64                    `json:"revision"`
	Generation         int64                    `json:"generation"`
	CreatedAt          int64                    `json:"createdAt"`
	UpdatedAt          int64                    `json:"updatedAt"`
}

type AnimeDownloadClaimResult struct {
	Job                AnimeDownloadJob `json:"job"`
	Created            bool             `json:"created"`
	DecisionGeneration int64            `json:"decisionGeneration"`
}
