package model

// EmbyDeepDeleteJob 仅保存已匹配的文件身份，不持久化账号凭据。
type EmbyDeepDeleteJob struct {
	Key       string `json:"key"`
	ServerURL string `json:"serverUrl"`
	ServerID  string `json:"serverId"`
	AccountID string `json:"accountId"`
	UID       string `json:"uid"`
	FileID    string `json:"fileId"`
	ParentCID string `json:"parentCID"`
	MappingID string `json:"mappingId"`
	Digest    string `json:"digest"`
	Signature string `json:"signature"`
	Revision  uint64 `json:"revision"`
}
