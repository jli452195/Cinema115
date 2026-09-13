# SQLite 数据库与旧任务迁移

Cinema115 使用 `database/sql + modernc.org/sqlite v1.34.5`，不需要 CGO、系统 SQLite 或额外的数据库容器。当前锁定版本兼容项目 Go 1.22 基线、Windows 386 开发环境和 Linux amd64/arm64 NAS。数据库路径固定为 `DATA_DIR/cinema115.db`，默认在项目根目录 `data/`，Docker 内为 `/data/cinema115.db`。

## 当前迁移范围

本次将转存再分享任务迁入 `reshare_jobs` 表，保留任务编号、原分享、临时目录 ID、新分享、执行阶段、结束标记、错误和更新时间。按时间和状态建立索引。`PRAGMA user_version` 记录数据库结构版本，`data_migrations` 记录数据导入完成状态。

数据库结构版本 2 新增 `share_tasks` 表，保存普通分享和转存分享的任务日志、生成数量、原链接与新链接、审核重试次数及下次执行时间。升级时自动建表，保留已有 `reshare_jobs` 检查点。任务内部保存生成选项快照和 Cookie 的 SHA-256 指纹，不保存 Cookie 明文；后台重试时登录发生变化会停止任务。

数据库结构版本 3 新增 `emby_items`、`emby_state`、`emby_events`，分别保存 Emby 媒体索引、同步状态与持久化通知队列。完整同步使用独立快照，成功后事务切换；现有分享任务表不变。Emby API Key 另行加密保存在 `data/emby-config.enc`，备份数据目录时一并保留。

数据库结构版本 4 将 Emby 的范围和快照字符串移入 `emby_snapshots`，条目通过整数 `snapshot_id` 关联。`emby_items` 使用 `WITHOUT ROWID`，季号、起止集号和名称直接存列，不再重复保存整段 JSON；联合索引支持按影视与季集查询。迁移在事务内保留全部版本、特殊季和其他任务数据，不需要重新全量同步。旧快照删除通过外键级联清理对应条目。

新增 `tmdb_cache` 只有 `key`、`data`、`fetched_at` 三列。播出缓存按季保存压缩后的集号与日期；目录缓存保存季列表；完整简介等详情压缩保存，最多保留 512 项。缺集区间、计算时间、是否过期、下次刷新时间不入库，前端使用当前播出表和 Emby 索引计算。缓存失败不覆盖旧记录，也不推进成功刷新时间。详情和目录通常六小时重新验证，播出表一小时，全部已播超过三十天的历史季延长至七天；手动刷新跳过缓存。

迁移后的空闲页会保留供 SQLite 复用，因此主文件不一定立即缩小。不能根据文件大小直接推算当前媒体记录占用，也不要直接删除 WAL。运行中的一致性备份会生成整理后的独立数据库文件。

管理员账号密码仍从根目录 `.env` 读取；115 Cookie、Telegram Bot 配置继续保存在原加密文件中。`automation.json`（含审核重试设置）和 Telegram 接收队列仍使用原文件。「115分享STRM → 日志」可以按状态、模式和链接筛选，或清理已结束的展示记录；不会删除 STRM、网盘文件、待重试任务或 `reshare_jobs` 检查点。

服务重启后，审核等待任务按保存的时间继续执行；上次处于执行中的任务标记为中断，禁止自动重放执行状态不确定的云端操作。新任务日志从升级后开始记录，不将旧转存检查点当作新任务重新执行。

## 首次启动与迁移

1. 停止旧版后端，避免旧程序继续写 JSON；更新前备份 `data/` 和 `.env`。
2. 部署新版后端。程序在接收 HTTP 请求和启动 Bot 之前创建数据库并导入 `data/reshare-jobs/*.json`。
3. 全部文件在一个事务中导入，完成后写入迁移标记。文件格式损坏、编号不匹配或写入失败会回滚本次导入并停止启动，修复后可重新启动。错误信息只标识文件和错误类型，不输出其分享凭证。
4. 旧 JSON 保持原样，作为迁移前备份；新任务和后续检查点仅写数据库。同编号已有数据库记录优先，重复启动不再导入旧文件，也不会覆盖新进度。

没有旧目录的新安装也会写入迁移完成标记。不要在完成迁移后再往旧目录放 JSON 期待自动导入；恢复应使用数据库备份。数据库结构比程序更新时拒绝启动，避免旧程序修改新结构。

导入不会执行转存、删除或自动重试云端任务。`finished=true` 只代表本次执行结束，不代表成功；必须结合 `status` 和 `lastError` 判断。`deleting`、`deleted` 等阶段仍需要核实云端实际状态，不能简单重放删除操作。

## 查询与检查

在 `backend/` 下执行（工具不读取 `.env`，使用 `-data-dir` 明确指定目录）：

```sh
go run ./cmd/dbtool -data-dir ../data -action migrate
go run ./cmd/dbtool -data-dir ../data -action status
```

`migrate` 可独立迁移，不启动 HTTP、Telegram 或任何 115 请求。`status` 执行数据库完整性检查并输出版本、任务数量、各阶段数量和迁移状态，不打印分享地址。

NAS 容器内检查：

```sh
docker compose exec cinema115 /app/dbtool -action status
```

管理员 JWT 接口（均禁止缓存）：

- `GET /api/admin/reshare/jobs?status=completed&limit=20&offset=0`：按更新时间倒序查询；省略 `status` 获取全部阶段，`limit` 为 1～100。
- `GET /api/admin/reshare/jobs/任务编号`：读取完整检查点。不存在返回 404。

这些接口包含分享链接和提取码，需要管理员认证；运行日志仍由原「查看日志」页面提供，当前尚无专门的任务列表页面。

## 备份与恢复

数据库采用 WAL、FULL 同步和 5 秒锁等待，当前连接池限制为一个连接。运行时可能同时存在 `cinema115.db-wal`、`cinema115.db-shm`，它们属于数据库状态，不要单独清理。数据库应存放在 NAS 本机磁盘的绑定挂载目录，不放在 SMB/NFS 网络共享上。

运行中使用一致性快照备份，目标文件必须不存在：

```sh
# 在 backend 下执行；把文件名改为本次备份的唯一名称。
go run ./cmd/dbtool -data-dir ../data -action backup -output ../data/backups/cinema115-20260913.db

# NAS 容器内执行。
docker compose exec cinema115 /app/dbtool -action backup -output /data/backups/cinema115-20260913.db
```

工具通过 `VACUUM INTO` 包含 WAL 中已提交的数据，不能用运行中直接复制主 `.db` 文件代替。数据库备份只包含任务数据；完整迁移到另一台 NAS 还需保留配置、加密凭证、Telegram 队列以及对应的 `JWT_SECRET`。停止服务后复制整个 `data/` 和 `.env` 可用于完整备份。

恢复前停止全部使用数据库的进程，备份当前主数据库及其 WAL/SHM 文件，再将一致性快照放回 `data/cinema115.db`；不要让恢复的数据库混用旧 WAL/SHM。启动相同或兼容的新版本，使用 `dbtool -action status` 核对任务数量和状态。

回退到迁移前程序只会读取旧 JSON，无法看到迁移后的新任务，因此不能仅换回旧镜像继续执行任务。应停止服务并恢复相匹配的完整备份，核实云端操作状态后再启用自动任务。
