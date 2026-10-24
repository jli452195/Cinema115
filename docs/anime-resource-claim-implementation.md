# 动漫资源核验与任务认领

日期：2026-10-19。对应任务：P2-02。

## 本次交付与文件流程

增加 BT v1 身份核验、持久去重、来源授权关联和下载意图事务。当前只接入服务/存储入口，没有开放 HTTP 启用动作、启动 RSS 调度或发送 115 下载请求。

最终文件流程继续为：下载到目录 1 的资源独立子目录，完成后按规则重命名，将原文件移动到目录 2，核实目标后结束。移动后目录 1 不再保留对应原文件，不另做复制后删除。下载与整理目录分别保存在动漫功能设置；本项固定下载目标与已保存的命名配置，整理目标和写操作快照由 P3-04 接入，实际移动与完成核对在后续 P3 完成。

## 资源身份与请求边界

[btresource](../backend/internal/btresource/verify.go) 使用白名单证据保存来源、原帖键、内容指纹、核验方法与种子正文 SHA-256，不存 Cookie、原始网页、任意请求头或带参数的下载 URL。

| 来源 | 核验方式 |
| --- | --- |
| 蜜柑 | 仅本站 `/Download/日期/原帖键.torrent`，读取实际种子，计算原始 `info` 字节的 SHA1；文件名及原 GUID 不作为 info-hash |
| AniBT | 仅本站 `/api/torrent/原帖键.torrent`，同样计算实际种子身份 |
| Anime Garden | 使用受控来源适配器读取并关联的 BT v1 磁力；严格要求一个 `xt=urn:btih:`，base32 与十六进制归一为同一 Hash |

Garden 的磁力提供来源声明的资源身份，不代表已下载种子或核实实际文件。蜜柑/AniBT 若同时带磁力，其 Hash 必须与种子相符；冲突保留待处理。纯 BT v2、混合 v2、ED2K 和其他协议不进入首版动漫下载。

[元数据读取](../backend/internal/btresource/http.go) 不接受任意主机、HTTP、端口、userinfo、查询参数或编码路径，不跟随重定向、不使用 CookieJar；最长 20 秒、正文最多 4 MiB。上游异常不回显原始请求或正文。

[种子解析](../backend/internal/btresource/torrent.go) 哈希原始 info 字节，不重新编码；拒绝重复/乱序键、非法整数、尾随内容、过深结构与过多节点，并核对 BT v1 文件长度、piece length 和 pieces 数量。深度上限 32，节点上限 32768；验证码 HTML 不能成为有效种子。

### 2026-10-24：种子解析内核替换

解析移至纯工具包 [torrentmeta](../backend/internal/utils/torrentmeta/torrent.go)，使用 `github.com/anacrolix/torrent/metainfo v1.56.1`，保持项目 Go 1.22 要求。`Parse([]byte)` 一次返回原始 info 的 InfoHash、根名称、单/多文件标记及有序文件清单；文件大小使用 `int64`，多文件路径相对根目录，单文件路径为文件名。显式 UTF-8 名称优先，不清洗路径、不筛选视频/字幕/ISO，也不执行下载或整理。

现有 `btresource.TorrentHash` 仅适配新工具及原有错误类别，旧解码树已删除。库没有深度/节点限制，且允许部分类型转换，因此保留无元数据树的编码边界检查及必需字段类型检查；4 MiB、32 层、32768 节点和 BT v1 分片一致性限制保持。另拒绝路径穿越、绝对路径、重复文件路径及总大小溢出。纯 v2 和混合种子仍返回不支持。

验证包含单文件、合集、字幕、零字节附件、精确大整数大小、UTF-8 别名、原始哈希及异常输入回归。读取依赖库自带真实样本，Sintel 11 文件合集、Debian 和 Arch Linux 单文件种子的哈希/文件顺序/路径/大小均一致；真实 v2/混合样本被拒绝。短时模糊测试最终版本约 79 万次输入通过；150 集加 150 字幕的本机解析基准约 1 ms/次（只测解析，不含网络）。临时验收程序已删除。

本轮只替换解析内核并提供文件清单接口；现有身份核验仍每次读取一次种子，没有新增 HTTP 或 115 请求，没有接入新的页面、RSS 生命周期或云端写入。

`FindAnimeVerifiedRelease` 从已提交的具体来源选择快照复用证明。原帖 ID、当前内容指纹与核验版本必须完全匹配，重启后仍有效；内容或核验版本变化不能继承旧证明。复用只减少元数据请求，最终授权和账号校验仍执行。

## 存储与事务

SQLite 升至 v46，在原有 13 张动漫表上新增四表：

- `anime_resources`：`(protocol,info_hash)` 唯一的身份与核验证据。
- `anime_resource_claims`：`(实际 UID,resource_id)` 唯一的处理依据，保留原账号编号及父目录 CID。
- `anime_download_jobs`：显式尝试、固定账号/设置/目录及来源、规则、解析、时间、命名快照；每 claim 最多一个有效活跃尝试。
- `anime_job_selections`：同一任务的多来源/发布独立授权，显式撤回不自动恢复。

原 `anime_releases` 增加可空 `resource_id` 外键。保留订阅、原帖、预览、取消记录与命名配置，不预建文件核对、外部写操作或 P3 历史方案表。

[ClaimAnimeDownload](../backend/internal/storage/anime_downloads.go) 在同一事务内复核：P2-01 的主账号/revision/UID/功能设置/目录；已持久授权的 pending `selected/linked` 决定与 generation；父订阅及来源启用状态、初批/新增依据、基线归属、固定目标与父/来源代次；当前原帖指纹、绑定/规则/解析快照及规则/解析/时间版本。随后保存资源身份、原帖关联、UID 资源认领、任务、来源授权及 linked 决定。任一步失败，全部回滚。

P1 的表单勾选和非执行解释不能授权此入口。待执行选择由后续启用/发现阶段从有效预览或受控 RSS 建立，本项测试在临时数据库构造这些未来授权状态，未提前开放启用流程。新建意图为 `queued/pending/active`，没有 due 时间或 worker。

[ClaimDownload 服务入口](../backend/internal/service/anime_downloads.go) 串联固定凭据会话、准确来源观测、身份核验/复用及最终事务。网络前拒绝明显失效选择，核验后复核账号，最终事务再次核对持久事实；网络 I/O 不占用数据库事务。

## 去重与冲突

| 情况 | 处理 |
| --- | --- |
| 多轮、多个 RSS 或重复操作发现同 UID/Hash | 一个 claim 和一个初始任务，各原帖与来源选择单独保留 |
| 相同原 GUID、不同原帖 | 保留不同原帖，下载仅按核验后的 Hash 去重 |
| 同集不同字幕组、不同 Hash、修正版 | 分别保存，不按集号、篇章或目标季集建立独占 |
| 同 Hash 的账号配置、凭据版本或父目录不兼容 | `ErrAnimeResourceTarget`，保留原任务及候选，不跨配置/目录继承 |
| 同 Hash 的组名归属冲突 | `ErrAnimeResourceSource`，待人工核对，不切换实际组或改旧命名 |
| 来源/规则升级后再次遇到既有资源 | 不重新下载、不覆盖旧任务及旧授权快照 |
| 原任务失败、取消、结果未知或完成 | 关联已有尝试，不由普通轮询新增 attempt，后续显式重试先对账 |
| dismissed 或 withdrawn | 拒绝自动重新选择或授权 |
| 原帖已有另一资源关联 | 拒绝冲突，不覆盖已核验身份 |
| 不同实际 UID 的同一资源 | 可有独立 claim，不迁移旧账号目录、文件或任务 |

内部错误暂只由服务层使用，HTTP 错误和下载进度交互在后续 P2 接入。本地创建任务不等于 115 已接受或下载完成。

## 验证与审查

新增 24 个顶层测试及一个 fuzz 入口：资源 7 项、认领/缓存 11 项、迁移 2 项、服务组合 4 项。覆盖实际 info 字节、Hash 归一及冲突、受控请求、24 路并发/多 RSS 同 Hash、同集多组/版本、共享 GUID、明确撤回、旧任务不重开、规则升级不改旧快照、最后一笔插入失败完整回滚、网络期间账号/设置/来源变更、旧代次/版本、初批依据、空证明、真实关闭/重开、持久证明复用及迁移保留/失败回滚。既有 P1 保存仍不能产生 P2 执行记录。

执行命令（目录为 `backend`）：

```text
go test ./internal/btresource ./internal/anime/preview ./internal/model ./internal/storage ./internal/service ./internal/httpapi ./internal/client/p115
go test ./internal/btresource ./internal/storage ./internal/service -run 'Test(Torrent|Magnet|Verify|RestoreVerified|AnimeDownloadClaim|AnimeDownloadMigration|AnimeDownloadVerification|AnimeClaimDownload)' -count=5
go test ./internal/btresource -run '^$' -fuzz '^FuzzTorrentHash$' -fuzztime=5s -parallel=2
go vet ./internal/btresource ./internal/anime/preview ./internal/model ./internal/storage ./internal/service ./internal/httpapi ./internal/client/p115
go build ./...
```

相关七包完整回归通过（service 98.9 秒、HTTP 36.5 秒、storage 9.9 秒）；新增目标场景连续五轮通过。模糊测试约 5 秒完成 103049 次输入，无失败。Codex 后端审查补严首次启用事实、基线归属、当前解析/时间版本和代次检查，补严后目标回归、vet 及全后端构建通过。

`go test -race` 因 `CGO_ENABLED=0` 被工具链拒绝；之前已确认本机没有 C 编译器，不计作通过，P2-11 继续保留该验收事项。

本项没有前端交互变更，无需 Gemini CLI 页面审查。未访问真实 115、保存真实订阅、修改真实目录或导航用户未保存页面。下一项 P2-03：手动与自动下载共享协调；真实提交及不确定结果对账在 P2-04 实现。
