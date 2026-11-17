package model

import "time"

// ED2KSTRMSettings 独立保存导入输出设置，不影响目录 STRM。
type ED2KSTRMSettings struct {
	BaseURL   string `json:"baseUrl"`
	OutputDir string `json:"outputDir"`
}

// ED2KSTRMCache 只记录本模块创建的主账号缓存目录及创建意图。
type ED2KSTRMCache struct {
	AccountID string `json:"accountId"`
	UID       string `json:"uid"`
	CID       string `json:"cid"`
	Name      string `json:"name"`
	Pending   bool   `json:"pending"`
}

// ED2KSTRMResource 的内容身份长期保存；缓存位置与任务状态允许独立恢复。
type ED2KSTRMResource struct {
	Hash             string           `json:"hash"`
	SHA1             string           `json:"sha1"`
	Size             int64            `json:"size"`
	Name             string           `json:"name"`
	Aliases          []string         `json:"aliases,omitempty"`
	AccountID        string           `json:"-"`
	UID              string           `json:"-"`
	AccountRevision  uint64           `json:"-"`
	CredentialScope  string           `json:"-"`
	Settings         ED2KSTRMSettings `json:"-"`
	CacheCID         string           `json:"cacheCID,omitempty"`
	DirectoryPending bool             `json:"-"`
	FileID           string           `json:"-"`
	PickCode         string           `json:"-"`
	Status           string           `json:"status"`
	CacheState       string           `json:"cacheState"`
	Message          string           `json:"message"`
	STRMPath         string           `json:"strmPath,omitempty"`
	Stage            string           `json:"-"`
	Attempts         int              `json:"-"`
	NextRunAt        time.Time        `json:"-"`
	CreatedAt        time.Time        `json:"createdAt"`
	UpdatedAt        time.Time        `json:"updatedAt"`
}
