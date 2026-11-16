package storage

// 删除通知可指向单个条目、剧集、父目录或媒体库，每个查找分支都需要范围内的索引。
const embyDeletionIndexesSchema = `
CREATE INDEX IF NOT EXISTS emby_parent ON emby_items(snapshot_id,parent_id);
CREATE INDEX IF NOT EXISTS emby_library ON emby_items(snapshot_id,library_id);
`
