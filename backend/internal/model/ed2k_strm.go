package model

import "time"

// ED2KSTRMSettings 保存输出地址和主账号缓存根目录，空 CID 表示使用自动目录。
type ED2KSTRMSettings struct {
	BaseURL        string `json:"baseUrl"`
	CacheAccountID string `json:"cacheAccountId,omitempty"`
	CacheCID       string `json:"cacheCID,omitempty"`
	CachePath      string `json:"cachePath,omitempty"`
}

// ED2KSTRMTarget 是独立的本地投递目录，可作为 Bot 功能分配。
type ED2KSTRMTarget struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	OutputDir     string `json:"outputDir"`
	AssignedBotID string `json:"assignedBotId,omitempty"`
}

// ED2KSTRMTask 仅在输出执行期间保存目录快照，完成后压缩成短期请求收据。
type ED2KSTRMTask struct {
	ID          string    `json:"id"`
	RequestID   string    `json:"-"`
	Fingerprint string    `json:"-"`
	Hash        string    `json:"hash"`
	Size        int64     `json:"size"`
	Name        string    `json:"name"`
	TargetID    string    `json:"-"`
	TargetName  string    `json:"targetName"`
	OutputDir   string    `json:"-"`
	BaseURL     string    `json:"-"`
	Status      string    `json:"status"`
	Message     string    `json:"message,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type ED2KSTRMImportResult struct {
	Resources []ED2KSTRMResource `json:"resources"`
	Tasks     []ED2KSTRMTask     `json:"tasks"`
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
	Hash             string    `json:"hash"`
	SHA1             string    `json:"sha1"`
	Size             int64     `json:"size"`
	Name             string    `json:"name"`
	Aliases          []string  `json:"aliases,omitempty"`
	AccountID        string    `json:"-"`
	UID              string    `json:"-"`
	AccountRevision  uint64    `json:"-"`
	CredentialScope  string    `json:"-"`
	CacheCID         string    `json:"cacheCID,omitempty"`
	CacheTargetCID   string    `json:"-"`
	CacheTargetPath  string    `json:"-"`
	CacheParentCID   string    `json:"-"`
	DirectoryPending bool      `json:"-"`
	FileID           string    `json:"-"`
	PickCode         string    `json:"-"`
	Status           string    `json:"status"`
	CacheState       string    `json:"cacheState"`
	Message          string    `json:"message"`
	Stage            string    `json:"-"`
	Attempts         int       `json:"-"`
	NextRunAt        time.Time `json:"-"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}
