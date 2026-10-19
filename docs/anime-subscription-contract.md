# 动漫订阅数据与接口契约（P0-05）

版本：1，2026-10-18。依据[首版规则](anime-first-release-rules.md)、[总体方案](anime-subscription-plan.md)及 P0-01～P0-03 的验证报告。本文件确定后续实施契约；配套 [SQLite DDL](anime-subscription-schema.sql)用于空白临时数据库验证，尚未接入应用迁移或上线任何 API。进度见[任务表](anime-subscription-tasks.md)。

2026-10-19 更新：P1 配置接口已接入；按用户确认提前实现 P3-01～03，当前以[订阅命名实现](anime-naming-implementation.md)为准。来源新增 `scope:{kind:"feed"}`，仅表示选择该 RSS，不证明整条来源属于 Bangumi 篇章，也不附带 `chapterCorrespondence`。编号范围是可选过滤，最终发布编号→TMDB 季集直接存于 `config.naming`，不再强制经过篇章编号。SQLite 当前结构版本为 v50，配置命名快照保存在 `anime_subscriptions.naming_json`；下文独立历史方案表仍为有执行任务后的设计，不代表已经建表。

P2-01～02 接入注记：固定主账号/目录/凭据内存会话，新增资源核验、资源/UID认领、下载意图和具体来源选择四表，以及发布可空 `resource_id`。核验方法仅为实际 BT v1 种子 info 字节或 Garden 受控磁力身份；P1表单选择仍不授权执行。新增执行选择另固定 `selection`（初批/新增依据、基线、目标和父/来源代次）、规则/解析/时间版本；本地事务不能代替后续115提交/对账。未创建文件、外部操作或P3历史表，未开放HTTP启用与后台调度。实际行为与验证见[下载目标快照](anime-download-target-implementation.md)及[资源核验与认领](anime-resource-claim-implementation.md)。

P2-03 接入注记：网页单条/批量、Telegram及动漫任务读取共享应用装配的实际UID/协议/Hash协调；手动magnet与动漫bt_v1归一，同UID操作闸门可取消，缺少共享实例不创建私有锁。自动 `LookupDownload` 只读固定持久任务并复核账号/目标/代次，不清除已结束任务或将未查到视为允许提交；持久写意图/对账、状态严格规范化及文件核对仍由后续P2实现。详见[下载共享协调](anime-download-coordination-implementation.md)，无新增迁移。

P2-04 接入注记：v47 新增 `anime_drive_operations`，目前仅接 `submit` 操作。独立 `EnsureDownload` 在已绑定且验证的资源子目录内持久准备、事务派发一次并精确对账；派发前既有任务不自动接管，派发后不确定结果不删除、不重提。至少一条有效来源授权才允许新写，旧代次回执只能保留原操作观测。目录创建/绑定、状态追踪、租约及P3文件表/操作仍待对应阶段；HTTP启用与后台调度尚未开放。详见[确保提交与对账](anime-submit-implementation.md)。

P2-05 接入注记：v48 在既有来源/决定表增加检查期限、失败计数、成功RSS窗口摘要及待核验原始观测。应用装配共享三站读取服务的发现循环和独立资源核验循环，每轮各最多四项、取消后等待退出；持久来源基线固定目标及解释版本，保留原T0和已见集合，只对可信新发布建立自动选择。核验成功复用P2-02认领本地任务，未触发115提交。主备独立错峰、退避、Retry-After及不可用停止生效，旧token/代次不能推进新检查或认领。当前HTTP启用仍关闸，普通草稿/暂停配置不会被自动激活；下载执行及其租约、状态、子目录/文件仍待后续阶段。详见[RSS持续发现与调度](anime-discovery-scheduler-implementation.md)。

P2-06 接入注记：v49为既有下载任务增加UID状态查询索引，复用预留观测/结果/调度列。只追踪同代次confirmed提交，精确Hash单次GET按接口来源规范化，不以100%、列表缺页或查询错误判断成功/不存在。任务缺失不重提；错误保留原成功时间与观测，凭据/目标/结果冲突待核对。同UID共享间隔和Retry-After，暂停来源不阻止已接受任务只读核对；旧token/代次/版本不能推进任务。远端completed仍待P2-07核对文件，verified/终态后停止领取。公开启用保持关闭，完整执行恢复见P2-08记录，详见[115状态追踪](anime-status-tracking-implementation.md)。

站点调整补充：当前来源支持 `mikan`、`anibt`、`garden`，provider 参与来源、发布及预览身份；AniBT 字幕组为 slug，历史窗口最多 100 条。Garden 发布方 ID 包含原站、字幕组/发布者类别及稳定数字 ID（如 `dmhy_f776`），候选来自最近 200 条资源，历史最多 1000 条，保留原站帖身份，不按集数合并版本；资源 JSON 与生成的 RSS 使用相同发布方筛选。公共配置页以 Bangumi 为主体，主/备用 RSS 独立选站，保存时保持单篇章有效订阅约束；换来源使用新身份和旧来源移除同事务提交。实现与验证见[按站点选番](anime-catalog-implementation.md)。配套 P0 DDL 保留为当时契约记录，实际结构以版本化迁移为准。

2026-10-20 低请求修订及 P2-07：按用户确认取消文件 SHA1 校验与正常成功后的例行读回，完成后不维护文件当前位置。v50 仅给 `anime_download_jobs` 增加 `directory_operation_json`、`files_json` 两列；目录创建成功回执直接保存，下载完成只列本任务目录及必要子目录一次，保存 ID/父级/名称/类型/随列表返回的大小。`file_state=verified` 沿用状态枚举，现仅表示清单已收集，不表示内容校验；`collectedAt` 表示这次收集时间。目录回执不明才只读恢复。P3 后续复用现有任务和操作记录，批量改名/移动收到明确成功即完成，只有未知回执才最小补查，不再创建独立文件注册表。下文旧 DDL 的文件与后处理表仅为历史草案，不作为新建表要求。详见[本步实现](anime-files-implementation.md)和[任务表](anime-subscription-tasks.md)，此修订优先。

P2-08 接入注记：SQLite 仍为 v50。复用任务租约、调度及观测，提交成功保存 `confirmed/kind=accepted`，不立即 GET；未知回执恢复匹配为 `kind=matched`，不重提。单次执行九十秒、租约两分钟；创建/提交/清单写入校验 token，重启按阶段恢复，completed且有结果ID直接收集文件。恢复目标及换号校验纯本地，新账号重新选目录即可；不做跨账号迁移或探测。完成后不领取、不定位文件。详见[低请求执行恢复](anime-download-recovery-implementation.md)。

P2-09 接入注记：新增本地暂停、归档、移除和取消归档 API 及移除后的任务查询；恢复服务复用原基线和任务，公开 resume/enable 与 download/organize 能力仍保持关闸。停止动作清未获准租约，不递增父执行代次；已获准操作继续保存回执，已接受任务继续必要只读追踪。移除只取消独占且未提交的本地意图，保留共享任务、未结束入口和资源幂等；不操作云端文件。回执包括 cancelledJobs、pendingJobs、inFlightOperationCount、resumedSourceIds、unchangedSources。详见[订阅控制实现](anime-subscription-control-implementation.md)。

## 1. 不变量与复用边界

- 模块只负责订阅、主网盘下载、原文件整理，不引用 Emby 用户/媒体索引、追更、STRM、播放或通知表及事件。TMDB 不是下载前提。
- 同一集可以有多个字幕组、画质及 v2；不建立分集独占键、不洗版。发布身份、BT 资源身份、115 文件身份分别保存。
- 每条 RSS 独立范围、编号、下限、规则、基线、健康与启用计划；主/备用表示配置角色。单源错误不改变其他源状态；站点级限流/风控冷却仍共享。
- 明确主账号编号及实际 UID 共同约束任务与目录；不能仅按 UID 找任意凭据。账号删除不级联删除任务；不新增普通功能账号选择。任务明文不存 Cookie、签名、直链或上游完整响应。
- 事务只保存意图、领取和结果，网络调用放在事务外；成功提交后更新内存索引。现有单实例锁与 SQLite 单连接约束继续适用。

| 现有位置 | 复用 | 独立实现/禁止直接复用的部分 |
| --- | --- | --- |
| `backend/internal/storage/sqlite.go` | `database/sql`、modernc SQLite、逐连接外键与 busy_timeout、WAL、事务迁移 | 当前结构版本 38；实际接入时取最新版本递增，不在本契约占用版本号；P1/P2/P3 分阶段建表 |
| `backend/cmd/server/main.go`、`httpapi/portal_auth.go` | 既有管理端鉴权组 `PortalAuthenticated`、`PortalAdminOnly` | 新增 `RegisterAnime`；cmd 只装配生命周期，不放业务规则 |
| `client/mikan`、`service/mikan*.go` | 受控地址、网页解析、累计 take、封面缓存、进行中合并及站点间隔 | 新增有界 RSS 解码与动漫预算；将网页/RSS/交互/调度共享协调实例，不复制一个独立限流器 |
| `client/p115/cloud_download.go`、`service/cloud_download.go` | BT v1 规范化、精确查询、基础写接口与主账号验证；P2-03已接入共享实际UID/协议/Hash协调 | 自动入口独立“确保提交/核对”；手动 `Submit` 会删除已结束任务记录再重提，不能直接用于轮询；持久提交与对账由P2-04实现 |
| `service/manual_organize*.go` | 提取文件身份、命名清洗、冲突及云端改名/移动能力 | 不复用其内存队列、跨业务扫描、自动清理或不可恢复任务历史 |
| `service/tv_numbering.go`、`storage/tv_numbering.go` | TMDB 客户端、缓存和纯校验可复用 | 不调用会重置追更/机器人任务的保存方法；动漫编号快照存自己的表 |
| `frontend/src/components` | 现有卡片、浮层、选择器、目录选择及 SVG 组件 | 新增动漫内容组件与状态缓存；不让 API 内部字段成为用户菜单文案 |

新增网络、存储和耗时方法以 `ctx context.Context` 为首参。通过小接口注入 `DiscoveryReader`、`SourceReader`、`PrimaryAccountReader`、`OfflineGateway`、`DriveReader/Writer`；接口方法按实际阶段增加，不把手动整理服务整体注入。共享协调由应用装配一次。调度拥有可取消 Context 和等待退出机制，错误用 `errors.Is/As` 分类并逐层包装，由边界统一记录。

## 2. 通用类型与标识

| 类型 | 契约 |
| --- | --- |
| 本地 ID | 服务端生成不透明字符串，最多 64 个 ASCII 字符；创建表单的 `source.ref` 是客户端稳定临时标识，不是持久 ID |
| 外部 ID | Bangumi、蜜柑条目/组、TMDB、115 UID/CID/文件 ID 均为十进制字符串；零只允许合法根目录 CID `"0"`，不经 JS 浮点转换 |
| `revision` | 乐观锁，正整数，不超过 JS 安全整数；新对象 `expectedRevision=0`。订阅/来源配置版本只在配置/管理动作改变时递增，后台进度不使表单持续冲突；任务 revision 随任务状态改变，用于任务动作与执行写回的 CAS |
| `generation` | 后端执行代次；撤权、计划变更使旧许可失效；整体暂停按 P2-09 清除未获准租约并保留确认代次，与配置 revision、租约 token 不混用 |
| 时间 | JSON/SQLite 均用 UTC Unix 毫秒，未知为 null；日期原文另存。无时区蜜柑日期按 Asia/Shanghai 解释，记录 `timeVersion` |
| Hash | BT v1 转为 40 位小写十六进制；32 位 base32 磁力规范化后再核验。保留该 Hash 仅用于资源幂等；不读取、保存或比对文件 SHA1 |
| JSON 字段 | SQLite 仅检查 JSON 合法；Go 类型严格校验字段、枚举、上下限和跨表归属，拒绝未知字段及多段 JSON；不把任意 JSON 当作可执行表达式 |

蜜柑发布键为受控来源加规范化 Episode 原帖标识；原 GUID 仅为观测字段。缺少可靠原帖身份则待确认，不用标题/集数/GUID 自造可信唯一键。`sourceKey=mikan:{itemId}:{groupId}` 由服务端产生；粘贴地址只接受固定蜜柑主机和已允许的 RSS 路径/参数，不接受 userinfo、任意端口、未知参数、任意重定向或内网目标。

## 3. DDL 与表职责

配套 SQL 不含 `PRAGMA user_version`、现有业务表外键或级联删除。DDL 一次覆盖核心字段契约，实际按下表阶段接入；不新增 Redis 或 Java 服务。

| 阶段 | 表 | 职责及约束 |
| --- | --- | --- |
| P1 | `anime_settings` | 单例功能目录与主账号快照；无绑定用 null，不写入凭据。账号/UID/目录整体保存，整理目录可空 |
| P1 | `anime_entries` | 本地篇章、发行版和资料；已确认的 `(bangumi_id, edition_key)` 唯一，外部资料删除不删本地条目 |
| P1 | `anime_subscriptions` | 一个未移除订阅对应一个本地篇章；配置状态、revision、执行代次、表单恢复、当前可选整理方案 |
| P1 | `anime_subscription_sources` | 主/备用、稳定编号空间、绑定/规则 JSON、配置状态、启用计划、独立健康与基线；每订阅一个未移除主源，相同规范化来源不重复 |
| P1 | `anime_releases`、`anime_release_decisions` | 全局原帖身份/解析观测，以及每来源对该发布的决定；`dismissed` 保留显式取消，后续轮询不自动重新选中 |
| P1 | `anime_preview_batches`、`anime_source_previews`、`anime_preview_items` | 表单总指纹、逐源有效期/历史边界及候选快照；不建立下载任务 |
| P1 | `anime_source_baselines`、`anime_baseline_releases` | 确认后的来源基线及当时已见发布集合；P1 只建表，不启用调度；不外键引用可过期删除的预览 |
| P1 | `anime_request_keys` | 持久化管理操作幂等回执，和本地业务变更同事务提交；不用于替代 115 对账 |
| P2 | `anime_resources` | 仅保存已验证 BT v1 身份及白名单证据，`(protocol, info_hash)` 唯一；P1 release 的可空关联在此阶段生效 |
| P2 | `anime_resource_claims` | `UID + resource_id` 唯一的资源处理依据；不包含集号，保留原账号与目标边界 |
| P2 | `anime_download_jobs`、`anime_job_selections` | 同资源的显式尝试与多来源选中关联；每 claim 同时最多一个未结束有效尝试，任务保存固定账号/规则/目录/可选命名快照 |
| P2 | `anime_download_jobs.directory_operation_json/files_json` | 单次创建记录与有界一次文件清单；不建立 `anime_files/anime_job_files`，不维护实时网盘索引 |
| P2 | `anime_drive_operations` | 单次建目录、提交、改名、移动的写意图和结果；唯一 `operation_key`，不确定结果不可直接重放 |
| P3 | `anime_naming_plans`、`anime_naming_mappings`、`anime_action_previews` | 本订阅独立资料/编号/模板版本、各来源映射及命名/资料/已有文件预览；确认后追加新版本，不原地改旧任务引用的快照 |
| P3 | 复用下载任务清单与既有操作记录 | 保存改名/移动步骤与成功回执，批量执行；未知结果才补查。原独立后处理两表为历史设计，后续不按原案新增队列 |

阶段性外键：P1 建表时对尚不存在的 P2/P3 表关联字段只允许 null；对应后续迁移建齐目标表后才能写非空值。完整 SQL 用于验证最终契约，P1 的实际迁移应暂缓这些跨阶段外键/列到相应阶段，不能留下运行时引用不存在表的结构。

2026-10-18 P1-01 接入注记：实际结构版本为 39，已建 P1 的 12 张表；暂不创建 `releases.resource_id` 与 `subscriptions.organize_enabled/naming_plan_id`，预览/基线仅建表，执行写入保持关闭。具体模型、存取边界与验证见[基础存储实现记录](anime-storage-implementation.md)，下文 P0 审查记录仍保留其完成时状态。

以下必须在服务层同事务校验，不能声称 SQLite `CHECK` 已覆盖：最多 5 个未移除来源；新建完整订阅有有效主源；baseline 确属该来源且版本匹配；来源与篇章/字幕组确认一致；任务 UID/账号/目录等于 claim；所有文件属于任务固定主网盘及精确任务目录；整理项属于对应后处理/下载任务；预览总条数；分段重叠、目标存在和完整路径冲突。DDL 不按 `(篇章,集号)`、`(字幕组,集号)` 或目标季集建资源唯一约束。

`form_json` 只保存管理员输入、具体发布选择及未完成项，不保存上游 HTML/XML。rules/binding/parse/mapping/snapshot JSON 均包含独立 `schemaVersion`。表中的快照使用专门 DTO 白名单序列化，不能直接序列化含 Cookie 的账号模型。移除采用软删除，后台清理界面历史不能删除发布决定、claim、写意图或幂等依据。

## 4. 来源规则与不可变快照

每条来源 DTO 的业务字段：

```json
{
  "ref": "source-main",
  "id": null,
  "expectedRevision": 0,
  "role": "main",
  "runWhenEnabled": true,
  "provider": "mikan",
  "itemId": "2259",
  "groupId": "45",
  "binding": {
    "schemaVersion": 1,
    "confirmed": true,
    "scope": {"kind": "range", "first": 25, "last": 36},
    "chapterCorrespondence": {"sourceEpisode": 25, "chapterEpisode": 1}
  },
  "rules": {
    "schemaVersion": 1,
    "resolution": "1080p",
    "include": ["HEVC"],
    "exclude": ["合集"],
    "minimumEpisode": 30,
    "initialMode": "from_episode"
  }
}
```

这些 ID 只沿用 P0 读取样本，编号对应是算法示例，不声明该蜜柑条目的实际编排。组名取已确认来源资料，不能由客户端任意覆盖实际组。`resolution` 可为 `720p/1080p/2160p/any`；null 仅在草稿中允许。include/exclude 各最多 10 个去重词、每词 64 Unicode 字符，字面匹配、排除优先。`minimumEpisode` 为 1～99999 且含本集。

当前选择来源使用 `scope:{kind:"feed"}`，`confirmed:true` 同时要求 `review:{method:"source_review",itemId,groupId}`，`chapterCorrespondence:null`。上例为既有有限范围/篇章对应示例；`range` 的 first/last 均为合法整数且 first<=last，`confirmed_chapter` 仅用于明确核对过篇章归属的既有输入，不能从名称或点选来源推断。初次历史范围三种值为 `from_episode/new_only/selected_history`。最终命名映射使用独立 `naming.sources[].segments/overrides`。

P1-05 补充实现校验：有 `chapterCorrespondence` 的有限范围，其首尾换算结果须在1～99999内；基准可在该范围之外，但不得产生非法篇章序号。无对应时保持篇章序号未知，不从已见最小集号、补番下限或其他来源推断。来源核验与只读编号预览见[实现记录](anime-source-binding-implementation.md)，管理API装配仍在P1-07。

来源 `namespace_id` 在确认编号空间时固定，解析升级不改变它；更换条目/组/篇章通过替换来源完成，旧源软移除，新源生成新的 sourceId/namespace 与预览，不拿已有编号直接覆盖旧任务。PATCH 已有 sourceId 不允许原地更换其身份字段，使用 removedSourceIds 与新来源 ref 同事务替换。普通规则编辑保留旧发布决定与资源处理依据。

P3 的 mapping JSON 使用 `segments[{first,last,sourceBase,targetSeason,targetBase}]`、`overrides[{rawKind,rawNumber,action,targetSeason,targetEpisode}]`，action 为 `map/ignore`；单集例外/忽略优先于唯一适用段。一个来源不同剧情分集不能误指同一目标；同集不同版本或不同组的目标相同是正常共存。目标快照保存作品名称/首播年、TMDB ID、默认或剧集组名称/ID、本地方案版本、目标季集及原始 episode ID，文件名取实际来源组名。

下载任务快照保存：实际来源/组、原帖及原始编号、规则/解析/时间版本、确认的 Hash、初批选择依据、账号编号/revision/UID、功能设置版本、父目录/独立目录名称、可选 namingPlanId 和整理目标。Cookie 只在任务启动时从固定账号取得内存快照；进程重启只接受仍匹配原账号、UID 与 revision 的凭据。凭据更新后旧任务不自动换凭据续写，返回 `ACCOUNT_SNAPSHOT_CHANGED`。允许显式只读审计核对同账号同 UID 的旧结果，但不改变旧写计划；需要新尝试时先确认旧结果再固定新快照，未知结果不得重提。目录或任务不随主账号换号迁移。

尚未核验 Hash 的 selected 发布决定也保存上述非敏感配置快照、next_verify_at 与租约/代次，P2 才开放有界领取；身份核验是只读操作，可合并请求。核验后同事务创建/关联资源、claim 和任务并结束该决定的核验调度。配置变化不能让这些已选发布在稍后悄悄改用新账号目录。验证种子时对原始 bencoded info 字节计算 Hash，拒绝重复/含糊 info 字典及超预算数据，不对解码重编码结果取 Hash。

## 5. 状态转换与执行许可

### 5.1 配置、来源与健康

| 对象 | 状态/转换 | 约束 |
| --- | --- | --- |
| 订阅 | `draft -> paused -> enabled`；`enabled -> paused -> enabled` | 首次启用走 enable 校验；曾启用且规则未变走 resume，不重建初批 |
| 订阅 | 活动状态 `-> archived -> paused`；非 removed `-> removed` | 归档后先恢复为暂停再启用；removed 不恢复，重新配置不清历史幂等 |
| 来源 | `draft -> paused -> enabled`；可单独暂停/移除 | 完整但尚未首次启用为 paused；只有有效预览建立基线后成为 enabled |
| 来源计划 | `runWhenEnabled` 独立保存 | 整体暂停不清此计划；来源单独停用设 false 并撤回该源授权；草稿不参与激活 |
| 来源有效状态 | 由来源、父订阅、共享冷却和能力合成 | 父订阅暂停时，即使来源内部仍为 enabled，API `effectiveState` 也返回 paused |
| 健康 | `unknown/ok/error/cooldown/unavailable` | 与配置状态分开；错误与退避不直接改成“整个订阅已暂停” |

来源响应始终并列返回 `configuredState`（原来源状态）、`runWhenEnabled`、`effectiveState` 和 `blockedBy`，不要求前端反推。effectiveState 优先级：来源 removed/draft 保留自身状态；父订阅 removed/archived 时其余源为 stopped；来源单独暂停或 runWhenEnabled=false 为 paused（blockedBy=source）；父订阅非 enabled 时其余源为 paused（blockedBy=subscription）；其余为 enabled，健康/冷却另标注。这样整体恢复不会把备用草稿或已单独停用来源展示为已启动。父订阅为 draft 时不能有已启用源。

resume 只解除父级遮罩，重新按上述优先级计算来源状态，不把所有来源改为 enabled 或 runWhenEnabled=true。回执返回 resumedSourceIds 和 unchangedSources（含 draft/source_paused 原因），页面摘要可显示“恢复X个来源，Y个草稿或单独停用来源保持原状”。来源 enable 动作仅在父订阅已 enabled 时可执行；父级暂停时返回 PAUSED，允许编辑配置，但不能借单源启用绕过整体暂停建立执行意图。

配置变更递增相关 revision；父订阅暂停/归档保留 run_generation 和确认快照，清 RSS token、递增未认领选择的核验 generation、清未获准外部写入的任务租约；在途单次写许可保留以接收回执。单源停用递增该源代次并撤回对应 selections，任务许可按关联重新计算。规则改变使旧未提交选择失效并要求重预览；已被 115 接受的尝试保留原快照只读核对。

### 5.2 资源、下载与文件事实

| 对象 | 转换 | 处理 |
| --- | --- | --- |
| 发布决定 | `seen -> filtered/history_skipped/needs_confirmation/selected/dismissed` | 仅明确可执行选择变 selected；取消勾选的具体发布记 dismissed，不按集号永久排除 |
| 同 Hash 选择 | `selected -> linked` | 关联既有任务；多个来源保留关联，不制造多个 115 提交 |
| 下载 | `queued -> submitting -> accepted -> downloading -> completed` | 允许上游直接 accepted/complete；不同接口的数字状态分别适配 |
| 提交异常 | `submitting -> unknown` | 超时、断连、回执/数据库写回丢失先只读核对；租约过期不等于未提交 |
| 可证明失败 | 活跃状态 `-> failed/blocked` | failed 需明确业务失败；blocked 表示账号、目录、授权等受阻，不伪造下载失败 |
| 取消 | 仅未写入且无未知操作 `-> cancelled` | 取消本地意图不取消 115 已接受任务；在途事实继续核对 |
| 文件 | `pending -> checking -> verified/needs_review` | 完成状态、目录身份及实际文件事实分开核实，空/缺页/解析异常不证明文件不存在 |
| 工作终态 | `active -> original_saved/organized` | 前者要 download completed + 一次文件清单；后者另需全部所选整理操作收到成功回执或异常补查确认；终态 `next_run_at=NULL` 且无租约 |

任务完成或外部清理不清 claim；正常轮询不会重开相同 Hash。同集其他 Hash 有独立 claim 和任务。显式失败重试先核对旧尝试，成功领取新 attempt 后保留所有旧记录；不修改旧失败行伪装为同一次重试。曾被 115 接受但随后列表消失，只能待核对，不能当作不存在再提交。

失败待人工重试的尝试和无人授权的受阻写阶段不安排自动写重试；需要只读核对的 unknown 阶段仍可按退避调度。failed 不得留下自动提交的 due 意图，retry 接口必须重新判断旧操作是否已确认未执行。

同 UID/协议/Hash 由同一资源协调实例串行处理；数据库 `(uid,resource_id)` 是最后一道幂等约束。只有账号、UID、目标边界及资源事实兼容时，多来源关联同一任务。不同账号配置或目标归属不明返回 `RESOURCE_TARGET_CONFLICT`，保留候选，不接管其他工具任务。共享任务至少有一个未撤回、订阅/来源仍有效且快照匹配的 selection 才能获准新写阶段；全部撤回则停止新写。主/备用同 Hash 的命名采用已固定的实际来源快照，其他来源显示“关联已存在资源”，不能静默换组重命名；组归属冲突先待确认。

### 5.3 单次外部写操作与恢复

1. 在 UID/资源或目录协调范围内复核租约、任务代次、有效 selection、来源/订阅代次、固定账号及目录。事务保存 `prepared` 操作及唯一 operation_key。
2. 事务再次复核并保存 `dispatched`、authorized_at 与 generation，提交后才发送一次请求。此刻定义为已获准的单个操作；暂停可等它核对结果，但不允许下一写操作。
3. 外部调用在事务外，使用固定内存凭据。明确成功回执直接记录 `confirmed`，不追加例行读回；明确失败为 failed，其他为 unknown。P2-08 已移除旧版提交后立即查询，至少两分钟后进入首轮状态追踪。
4. 重启遇到 dispatched/unknown 只读对账；同文件 ID 已在目标则完成原操作，仍未能确定则保留 needs_review。查询报错、短暂缺项或有限次数重试不能证明未执行。只在足以证明未执行且版本/许可仍有效时，才重新授权该未执行阶段；不能由超时自行推断。

租约使用随机 token + generation + expiresAt，所有写回 CAS；旧执行者不得推进阶段或释放新租约。旧代次已发操作的真实回执可按 operation ID 记录为观察结果，由当前恢复流程核验合并，不能直接推进任务。租约 120 秒、单次执行最多 90 秒，P2-08复用现有循环且不增加续期线程；网络完成后仍须校验 token，丢失租约只对账。调度查询只能领取 due 的非终态工作，暂停阻止新写、不阻止必要只读核对。

写协调的获取顺序统一为实际 UID 写闸门、资源键、按 CID 排序的目录键；只在单个操作及其结果核对期间持有，不跨整条下载/整理流程占用。网络等待不得持有数据库事务，手动/自动使用同一协调实例，不能各自维护一把互不相知的锁。

P3 每文件 `planned -> renamed -> moved -> completed`，冲突/不确定进入 conflict/needs_review。无需改名或移动的阶段依据本次清单与已确认操作跳过，不另发查询或写请求。改名/移动分别保存步骤与批次回执，全部明确成功即可完成，不追加最后读回、不比对SHA1或大小；只有未知回执才按固定ID最小补查。完整路径已存在则暂停对应整理、保留下载目录中的原文件；不覆盖、不删旧版本、不自动随机后缀。未选附件及空目录保留。整理失败不让下载回到 queued，成功后停止该任务全部定时复查。

未配置整理或明确忽略全部待整理视频的资源，以 original_saved 结束，并说明“按原名保存”；不能因为 ignored 项没有移动就误报 organized。完成视频与唯一匹配字幕的所有已选项才允许 organized。已有终态文件的显式整理另建后处理记录，不使原下载任务重新轮询115。

## 6. 预览、版本和历史边界

### 6.1 指纹及有效期

P1-06 实现补充：预览批次和单源各保存服务端类型化 `snapshot_json`，包含规范化配置、原始读取窗口、显式选择及待复核的旧发布快照；零资源预览也能恢复，项目 SQLite 版本升至40。未保存篇章用同一表单内稳定的 `draftKey` 与篇章/发行版共同限定复用范围，仍须校验认证 actor；此标识不授予额外权限。来源复用进新 batch 时复制快照并返回新的 sourcePreviewId，沿用原 generatedAt/expiresAt/observedAt/T0 和 loadRevision。

服务端规范化输入并对带类型的 DTO 按固定字段顺序、UTF-8 JSON、SHA-256 计算 `v1:<lowerhex>`；不依赖客户端 JSON 键顺序。关键词首尾清理、去重、排序，集合型 ID 排序，角色顺序/映射段保留语义；空值与缺失按各字段定义归一。不包含 Cookie、签名、图片二进制或当前轮询时间。

- 总配置指纹包括篇章/发行版、subscription revision、来源列表及各自 revision/规范化规则与编号、设置版本及目录归属、解析/时间/规则引擎版本。只用于判定本次保存输入是否相符，不让无关备用草稿编辑使有效来源的预览失效。
- 每来源指纹只包括其篇章、绑定、规则、来源 revision、相关账号/目录版本和引擎版本。换组、范围/筛选/下限或账号目录变化使该源失效；其他源自己的有效预览可复用进新 batch，不续期。
- 继续加载带 `expectedLoadRevision`，成功合并后递增；保存携带实际使用的 loadRevision 与明确 selected/dismissed release IDs，禁止两个窗口的旧加载清单悄悄覆盖新选择。
- 各源从首次成功生成 generatedAt 起绝对 15 分钟；`now>=expiresAt` 即过期。继续加载或重用缓存不续期。重新生成新预览保留输入/明确选择并返回 added/removed/changed，不把新出现的旧历史自动勾进已确认清单。

源 T0 取产生首读有效快照的实际成功读取时间，按上游发布时间精度向下取整；缓存命中沿用 observedAt，不能把生成页面时间冒充最新读取。预览有效期仍由 generatedAt 计算，两个时间含义分开。这样缓存读取与启用之间的可信新发布不会因 T0 被人为后移而漏掉。新的来源独立 T0；普通规则编辑与恢复沿用原已确认 T0 和已见集合，不把历史重新定义成新发布；显式历史选择单独保存。

已有来源修改规则后的重新确认只更新版本依据和明确选择，保留原 T0/历史基线成员；不能把新预览中全部已见发布重新灌入历史基线，否则会跳过原基线之后尚未处理的新发布。新发布处理依据由版本化的 release_decisions 保留，显式历史选择可以覆盖自己的 history_skipped 决定，但不清同资源 claim。

首次启用在一个本地事务中校验所有**本次明确选中激活**来源的有效预览、revision、目录，固定其 T0/已见集合/初批 selected 与 dismissed 决定并启用。未完成备用源保留草稿，不参与该校验；管理员选择激活的源若过期则返回逐源问题，不能静默漏掉它。已确认初批意图不因后来预览到期丢失；保存暂停不提交初批，也不把临时预览当作有效基线。

保存 draft/paused 时传入的 previewRefs 和选择仅用于恢复表单，不消费预览、不落下载基线/已选执行决定、claim 或任务。首次启用无成功预览返回409 `PREVIEW_REQUIRED`，过期为409 `PREVIEW_EXPIRED`；未确认来源范围仍是422 `SOURCE_SCOPE_UNCONFIRMED`，三者不混用。details.issues 精确列出 sourceRef/sourceId；本次选中激活的任一来源无效则整次启用事务不提交。页面可让用户取消该源的本次激活后提交其他有效来源，不能静默抛弃已勾选来源，也不因草稿或未选来源限制其他源。

启用被拦截后留在原表单，问题直接显示在对应来源的现有预览/激活区域，不整表报错或清空其他源。该源保留“参与本次启用”勾选，用户可取消该勾选或更新该源预览；激活集合仍有无效源时主提交不可用，并说明需处理哪条来源，处理后恢复可用。复用既有来源选择控件，不新增多层确认弹窗。

预览归属于已认证 actor 与同一本地订阅/未保存表单篇章；复用 previewRefs 也要检查归属、每源版本与绝对到期时间。构建新 batch 不更新被复用来源的 generatedAt/observedAt/T0。规则或来源内容已变的条目返回 changed，保留用户输入但要求重新核对该条选择，不暗中用新发布替换旧确认。

### 6.2 读取和选择

- 一次生成/继续操作只针对一条来源，最多 3 次未命中缓存的元数据请求、45 秒、总正文 8 MiB、单响应 4 MiB、单请求 20 秒；同站至少间隔 1 秒。源数量不倍增单次预算。
- 同一 batch 各来源已加载快照合计最多 2000 原始发布，含过滤/待确认行，不能按 Hash 合并规避。`take<=2000`，累计展开替换/按原帖合并，不能当增量追加；先预估预算再请求，超出时不丢旧快照。
- 当前来源正常短响应、结构与原始行数可靠且 rawCount<requestedTake，才可返回 `visible_end`；其他为 partial，stopReason 可为 `more_available/budget/limit/read_failed/invalid_rows/no_progress`。表示本次可见末尾，不保证全站历史完整。
- 页面分页每页 50 条，选择作用于当前来源全部已加载匹配发布，明确返回 loaded/matched/review/selected 数量。后端校验 selected IDs 全部在对应未过期快照中且可选；大于当前页可选合法。
- `from_episode` 只在首次成功生成时默认勾选已加载匹配发布；`new_only` 与 `selected_history` 初选为0。后续继续加载和重预览只保留原合法选择，新加载历史与预览后新发布都列入 addedIds，须显式勾选，不扩大已确认初批。完整选择更新也递增 loadRevision；取消原已选项记录为显式 dismissed，重预览不恢复默认勾选。
- P1 不下载种子。P2 只核验明确选中资源，得到相同 Hash 后关联；“已选 N 条”始终是发布数，不承诺 N 个新任务或文件。待核验阶段用持久发布决定 `selected` 承接，后台可恢复，不能先建虚假的已验证 Hash claim。
- T0 后可信新发布要求未在基线、时间可解析且>=T0、未超读取时刻 5 分钟，并通过该源规则。缺日期、异常未来时间、旧帖补录先待确认。过滤后的同帖若内容变化，保留旧决定并重新解释，不把 dismissed 自动改 selected。

部分成功返回有效旧预览和逐源 issue；从未有效读取的源无 previewId、无 T0/基线。失败和零结果分别返回，不把其他来源的错误合入整个订阅“0 条”。

重预览比较时，旧选择未在当前已加载集合出现只表示 `out_of_loaded_scope`，不能凭窗口缺项宣称上游删除。返回 `selectionChanges={retainedIds,changedIds,unresolvedIds}`；合法选择计数只含新快照中仍可选的 retainedIds，旧未确认选择保留在独立的“原选择待复核”集合中，不能静默减少或作为幽灵勾选提交。页面提示“有X项原选择未在本次已加载范围中确认”，可继续在预算内加载/核对或显式将它们移出本次提交；未处理时阻止该源提交，其他源选择仍保留。后端始终拒绝不在当前有效快照中的 selected IDs。

预览响应固定包含 `budget={totalRawCount,limit:2000,remaining}`，每来源包含 `sourceRef/previewId/loadRevision/generatedAt/expiresAt/rawCount/loadedCount/matchedCount/reviewCount/completeness/stopReason`。totalRawCount 为各来源原始快照行数相加，不能用规范化后去重数量代替。达到共享上限仍返回有效已加载清单和中性提示“本次预览已达到2000条上限，未包含更早历史；已加载内容仍可选择”；单次读取时间/正文预算只显示该次未继续完成，不误写为2000条上限。visible_end 显示“已读到当前来源可见末尾”。单源过期仅标记该源并保留其他源操作，不把整张表单置灰。

## 7. HTTP 通用契约

基路径 `/api/admin/anime`，全部沿用既有管理员鉴权；401/403 在业务查询前返回。请求/响应为 JSON；沿用现有兼容字段 `error: string`，新增 code/details，前端按 code 判断，不解析中文错误。管理数据 HTTP `Cache-Control: no-store`，页面成功内容缓存仍由客户端状态层维护。

本模块只读功能能力 `capabilities={configure,download,organize}` 由服务端返回。P1 为 true/false/false，P2 为 true/true/false，P3 验收后才开放 organize；无主账号/目录等当前就绪状态另外返回 readiness，不与阶段能力混成一个布尔值。P1 伪造 enabled 或写执行动作返回 `CAPABILITY_UNAVAILABLE`。

写配置、启用/暂停/恢复/移除、重试和应用整理均需 `Idempotency-Key`（8～128 个 ASCII 字母/数字/`-_`）及 expectedRevision。幂等作用域为已认证管理主体 + 方法/规范化路由含实体 ID + key；相同 key 不同规范化 body 返回 409。先鉴权，再查已有回执，再校验当前 revision，保证首次响应丢失后重试能取到原回执。回执与本地变更同事务提交，成功后才响应；不持久化含凭据的请求/响应。幂等回执保留，不因资源详情清理失效。外部写只由持久 worker 推进，HTTP 不包着一整轮网盘调用。

普通请求体最多 64 KiB；配置/选择/映射最多 1 MiB、单字段按 DTO 上限，超限 413，未知字段/多段 JSON 为 400。ID 非空、非法枚举及数值先本地拒绝。读请求/操作上下文取消有效；关闭浏览器不取消已经事务接受的后台任务。

本地列表采用 `page` 从 1 开始、pageSize 默认 50、最大 100，固定 `createdAt DESC,id DESC`（订阅列表 `updatedAt DESC,id DESC`，仅配置编辑改变 updatedAt）。返回 `total/page/pageSize/items/dataVersion`；变化后按稳定 ID 合并、保留选择，不能保证上游搜索快照。URL 保存筛选/页码/选中详情，UI 恢复成功缓存再后台读取。后台本地状态更新不重建卡片、不重播入场动画。

### 7.1 P1 API

| 方法/路径 | 输入 | 成功结果/边界 |
| --- | --- | --- |
| `GET /capabilities` | 无 | 能力、readiness、settingsRevision；只查本地 |
| `GET /discovery/calendar` | 可选 weekday=1～7，只是本地筛选 | `days/items`、fetchedAt、expiresAt、stale、dataVersion；无季度参数、不逐卡补详情 |
| `POST /discovery/search` | keyword 去首尾后 1～128 字符，limit 默认20/最大50，offset=0～1000，sort 只接受 match | 动画 type=2 的 `items/total/limit/offset` 与缓存时间；空白词拒绝 |
| `GET /discovery/subjects/:id` | 可选 includeRelations=true | 按需详情与独立关系缓存；外部404保留本地历史，不伪造空资料 |
| `POST /sources/mikan/search` | keyword、可选 bangumiId、groupId | 受控来源候选及读取范围；同名不自动关联 |
| `GET /sources/mikan/:id` | 无 | 条目和实际组列表/已知历史展开入口；不默认抓全部组 |
| `POST /source-bindings/preview` | entry、单源 binding 输入 | 已核验来源/组、编号/范围回显与需人工确认项；不保存、不创建任务 |
| `POST /subscriptions/preview` | 完整配置、可选 subscriptionId、各 expectedRevision、previewSourceRef、可选复用的其他源 previewRefs | batchId、该源 preview、其他复用源摘要、合计预算；全新读取失败返回结构化错误 |
| `POST /previews/:id/load` | sourceRef、expectedLoadRevision、nextTake | 有效来源继续加载，返回新 loadRevision、保留/变更身份、范围；过期409不清输入 |
| `GET /previews/:id/releases` | sourceRef、page/pageSize | 当前快照投影及可选身份；不隐式补上游历史 |
| `POST /previews/:id/selections` | sourceRef、sourcePreviewId、expectedLoadRevision、明确 selectedReleaseIds/dismissedReleaseIds 数组 | 只更新预览快照的表单选择及 loadRevision，不写执行决定；空数组明确清空 |
| `POST /subscriptions` | config、saveMode=draft/paused、expectedRevision=0、settingsRevision、可选 previewRefs 与表单选择 | 201，实体 ID、revision、各 ref->id、保存状态；paused 不建立初批下载任务 |
| `PATCH /subscriptions/:id` | 完整 config、expectedRevision；不以列表缺项隐式删除来源，移除用 removedSourceIds | 200，新 revision、逐源 revision、失效预览/受影响未执行选择说明；不触发旧文件整理 |
| `GET /subscriptions`、`GET /subscriptions/:id` | 列表筛选 status/keyword；详情为稳定 ID | 本地配置、effectiveState、各源健康、各层统计和 permittedActions；外部资料不再逐次必读 |
| `GET /subscriptions/:id/releases` | sourceId、decision、page/pageSize | 发布、原始/篇章编号、资源身份核验状态、关联任务与解释原因；同集多版本单列 |
| `GET /settings` | 无 | 只读当前明确主账号摘要/目录归属、revision、就绪问题；不含 Cookie |
| `PATCH /settings` | expectedRevision、download/organize CID 及 expectedPrimaryAccountId/revision | 服务器核验当前主账号与实际目录后保存；路径来自读回，不信客户端文字，账号参数只作并发校验，不是选择账号 |

配置主体：`entry={id}` 或新建 `{bangumiId,editionKey}`（蜜柑快捷入口允许 bangumiId=null、保留来源名称）；`sources[]` 使用第4节 DTO，最多5条。服务端从可信缓存/受控读取取得资料快照，不直接信任任意标题与外部 URL。订阅重复创建返回 409 `SUBSCRIPTION_EXISTS` 和 existingId，以便定位原配置；相同 key 的原操作重放仍返回原201回执。

完整配置保存暂停不要求首次读取成功，但来源/规则/主账号目录结构必须有效；否则明确保存 draft。用户请求 paused 时不能静默降级 draft，返回 `CONFIG_INCOMPLETE` 及缺项，让页面保留输入并提供“保存草稿”。草稿备用源不影响完整主源。P1 保存主按钮“保存配置”，结果暂停；不显示禁用的下载占位。

`/previews/:id` 的 id 指 batchId，`previewRefs` 每项引用对应 sourcePreviewId 与 loadRevision，不把两类标识混用。保存 draft/paused 的显式选择放在 `formSelections` 中，仅作为表单输入恢复。

P1-07 已接通上述 API，实际 DTO 与验证见[管理员 API 实现](anime-admin-api-implementation.md)。配置保存的 entry/sources/removedSourceIds/formSelections/previewRefs 放在 config 中，removedSourceIds 每项携带 id/expectedRevision；设置的账号版本字段为 expectedPrimaryAccountRevision。预览生成沿用 chapter/subscriptionId/revision/settings/sources 结构，不将 config 保存 DTO 与预览快照 DTO 混用。恢复引用保留原 sourceRef，另存稳定 sourceId，保存不续期或消费预览，回执 requiresRepreview=true。未选定身份的草稿允许首次补全条目/组，已选字段不能重绑定。

### 7.2 P2 API

2026-10-20，P2-10～12 实际交付如下，详见[下载阶段实现与验收](anime-download-stage-implementation.md)。创建/保存仍仅接受 draft/paused；先保存，再用已保存配置预览并独立启用。首次启用和未修改规则的恢复已开放；启用后规则重确认、单独来源控制、手动 RSS 检查及历史后补尚未开放。下方“后续接口”仅为规划。

| 方法/路径 | 输入 | 成功结果/边界 |
| --- | --- | --- |
| `POST /subscriptions/:id/enable-preview` | expectedRevision、settingsRevision、sourceId | 200，从已保存配置生成该来源新预览，返回 batchId/page；恢复当前可确认的表单预选，无法确认的旧预选提示重新核对，不自动下载 |
| `POST /subscriptions/:id/enable` | expectedRevision、settingsRevision、sources[]，每项含 batchId/sourceRef/sourcePreviewId/loadRevision/selectedReleaseIds/dismissedReleaseIds | 200，首次启用；全部参与来源必须有有效预览且含主源，基线、显式选择、启用状态与幂等回执同事务；不直接提交115 |
| `POST /subscriptions/:id/pause` | expectedRevision | 200，停止后续写阶段，返回 inFlightOperationCount；已获准单操作继续确认结果 |
| `POST /subscriptions/:id/resume` | expectedRevision | 200，曾启用且未变规则恢复原意图；首次或修改后返回 BASELINE_RECONFIRM_REQUIRED，不偷偷重选历史 |
| `POST /subscriptions/:id/archive`、`/remove` | expectedRevision | 200，软状态改变，保留未结束任务入口；不删115任务/文件 |
| `POST /subscriptions/:id/unarchive` | expectedRevision | 200，仅回到暂停；不直接重新下载 |
| `GET /subscriptions/:id/jobs`、`GET /jobs/:id` | 前者按订阅分页，后者按任务 ID | 200，本地精简投影，含 withdrawn 关联、title/groupName、下载/文件/workflow 状态、错误码、执行时路径/CID、时间、进度、文件清单和 permittedActions；文件 size 为十进制字符串，不触发115刷新 |
| `POST /jobs/:id/reconcile` | expectedRevision | 200，安排原任务必要的只读核对，暂停中也允许；不新建任务或提交授权，已保存清单的任务拒绝核对 |
| `POST /jobs/:id/retry` | expectedRevision | 200，仅重试读取或尚未派发的准备，沿用原任务/操作；不创建新 attempt、不删除旧任务重提。unknown 只能核对，failed/终态/归属冲突无重试入口 |

任务动作由服务端当前 permittedActions 控制，执行时重新校验 revision、租约、固定主账号及目录归属；30秒内最近尝试和未到期 Retry-After 不可被手动点击绕过。queued 目录未知仍走原自动恢复，不提供可能继续写入的“只读核对”按钮。幂等 key 与请求体必须原样保留；成功回执重放优先于当前版本校验。

后续接口（尚未开放）：

| 方法/路径 | 规划输入 | 规划成功结果/边界 |
| --- | --- | --- |
| `POST /subscriptions/:id/check` | expectedRevision、可选 sourceIds | 202，持久或合并的只读检查安排回执；返回 check token/sourceIds，详情靠 checkedAt/health/候选更新反馈，不代表下载已完成 |
| `POST /subscriptions/:id/sources/:sourceId/pause` | 父及来源 expectedRevision | 200，撤回该源新意图和写授权；其他源继续 |
| `POST /subscriptions/:id/sources/:sourceId/enable` | 父/来源 expectedRevision、该源有效 preview/选择 | 200，该源首次启用/规则变更重确认；不重选其他源历史 |
| `POST /subscriptions/:id/sources/:sourceId/recover` | 父/来源 expectedRevision | 202，显式解除该源 unavailable 后安排一次受限探测；共享站点 Retry-After 未到仍不请求 |

新历史后补规划通过同样的有效预览和显式选择接口 `POST /subscriptions/:id/history/apply`（expectedRevision、previewRefs、selected/dismissed IDs），202 返回本地选择回执；不重置 T0，不把未加载历史转为全量后台任务。当前接口继续关闸。

首次激活已保存订阅的实际请求如下。sourceRef 使用已保存的稳定来源 ID，各来源可来自不同 batch。sources 必须准确覆盖所有 runWhenEnabled 来源；selected/dismissed 集合必须显式传入、互不相交，且属于该源快照；为空是合法的明确选择，不自动改为全选。

```json
{
  "expectedRevision": 1,
  "settingsRevision": 1,
  "sources": [{
    "batchId": "batch-example",
    "sourceRef": "source-a",
    "sourcePreviewId": "preview-example",
    "loadRevision": 1,
    "selectedReleaseIds": ["release-a"],
    "dismissedReleaseIds": ["release-a-v2"]
  }]
}
```

有匹配发布但用户本次不选历史时，动作显示“启用订阅”，说明“本次未选择历史资源，后续按规则检查”；不出现“下载已选0条”，也不伪装成来源零匹配。仅新发布模式同样不提交当前历史。来源启用计划不能由漏传字段暗中改变。首次启用回执复用 AnimeControlReceipt，返回 id/revision/status/resumedSourceIds 等控制字段，未返回示意中的下载完成数。

来源问题处理优先于按钮数量判断；在有效激活集合内，主动作依次为：selectedCount>0 时“启用并下载已选N条”；selectedCount=0、matchedCount=0 且 reviewCount>0 时“查看待确认”；有效零匹配且无待确认时“启用并等待资源”；有合法匹配但未选历史或明确 new_only 时“启用订阅”。reviewCount 是解析/归属不明且不可勾选的发布，pendingVerificationCount 是已明确选择、尚待 Hash 核验的发布，二者不能混用。待确认项要先修正规则/归属并生成新有效预览，成为匹配项后才能勾选；不提供直接勾选未知编号的入口。有已选N条及M条待确认时邻近说明“另有M条待确认，本次不提交”，不承诺它们之后自动变为可下载。

后续手动异步只读检查不另造长期任务系统：以来源 next_check_at 原子置为到期及 check token 回执实现调度合并，返回的 token 只关联这次安排，不是有独立完成状态的资源任务 ID。无有效新鲜数据时详情继续显示旧 checkedAt；检查失败也更新独立的 attemptedAt/errorCode，不能把旧成功数据写成刚刷新。

检查按钮被接受后显示中性“检查已安排”，不锁住整个表单；同来源在途请求合并，站点冷却/来源 unavailable 不能被 check 绕过。缓存复用时展示对应 fetchedAt，不把点击时刻写成上次成功检查时间。

### 7.3 P3 API

| 方法/路径 | 输入 | 成功结果/边界 |
| --- | --- | --- |
| `POST /subscriptions/:id/naming-mapping/preview` | expectedRevision、expectedPlanRevision、目标TMDB/方案、每源对应、明确预览范围 | 200，原名/编号/目标完整路径、正常/目标暂缺/冲突；不写网盘 |
| `PUT /subscriptions/:id/naming-mapping` | 相同版本与有效命名previewId，organizeEnabled | 200，追加独立方案版本，只影响新任务；不改其他业务编号 |
| `POST /subscriptions/:id/naming-metadata/check` | expectedRevision | 200，读取资料差异，不自动替换当前快照 |
| `POST /subscriptions/:id/naming-metadata/apply` | expectedRevision、expectedPlanRevision、有效差异previewId | 200，本订阅新快照版本；保留旧任务计划 |
| `POST /subscriptions/:id/organize/preview` | 明确 downloadJobIds/fileIds、expectedRevision、expectedPlanRevision | 200，只接受本模块已确认精确文件，已有任务/文件的新路径计划及冲突 |
| `POST /subscriptions/:id/organize/apply` | 有效previewId及其版本、明确文件集合 | 202，幂等建立独立后处理计划；不自动批量处理所有旧文件 |

命名及已有文件预览沿用绝对15分钟、指纹/文件事实版本与失败保留选择，另包含文件 UID/ID/执行时路径、目标目录与 namingPlan revision，不包含文件SHA1。它们不共享资源预览的 T0，也不建立下载基线。选中的未完成文件/目标方案变化只阻止受影响的整理，下载事实保留。已完成记录不跟踪文件后续位置，不做后台重定位或补下载。

## 8. 响应与错误投影

规划成功回执示例（仅示意本地接受，非115回执；P2 首次启用实际采用第7.2节控制回执）：

```json
{
  "id": "subscription-example",
  "revision": 2,
  "status": "enabled",
  "selectionCount": 4,
  "pendingVerificationCount": 4,
  "createdJobCount": 0,
  "linkedJobCount": 0,
  "replayed": false,
  "sourceIds": {"source-main": "source-example"}
}
```

后台资源 Hash 核验后任务数可能小于已选发布数；页面显示“已保存4条选择，正在核验资源”，不可显示“115已下载4集”。此处 Hash 是下载资源身份，不读取网盘文件 SHA1。P2 任务详情实际字段见第7.2节，permittedActions 为 reconcile/retry 布尔对象；订阅级动作仍使用动作数组。size 以十进制字符串投影给 JS。

订阅/来源统计字段固定如下；界面按所在区域选择少量必要指标，不全塞进卡片：

| 字段 | 定义及可用文案 |
| --- | --- |
| `releaseStats.selectedCount/dismissedCount/pendingVerificationCount` | 来源-发布选择数；跨来源可重复关联同一发布，仅表示选择意图，文案“已选 N 条发布/待核验 N 条” |
| `jobStats.totalCount/queuedCount/downloadingCount/remoteCompletedCount/failedCount/needsReviewCount` | 当前范围按 claim 去重、仅计最新有效尝试；历史失败尝试不重复加总。remoteCompleted 只代表115任务返回完成，文案“关联 M 个下载任务/网盘标记完成 X 个任务” |
| `fileStats.verifiedCount/needsReviewCount` | P2 按已保存任务清单中的 file 条目计数，不按资源数估算文件数；目录未读完整不计 verified，needsReviewCount 暂为0，资源受阻另计 jobStats，文案“已保存 X 个文件记录” |
| `completionStats.originalSavedCount/organizedCount` | 按资源 claim 去重的最终工作结果；后续显式整理成功可将同资源投影更新为 organized，二者不重复计。文案“原名保存 X 条/整理完成 Y 条资源” |

不同统计维度不能相加，downloadState=completed 不能直接增加 organizedCount。默认不显示“已完成 N 集”，如后续需要剧情集覆盖统计，须另按已确认篇章映射去重，而非发布/任务数。预览已选数是前端明确选择的集合大小，状态条区分“已加载/匹配/待确认/已选”；待确认数量不含在 matchedCount 中。

错误格式：

```json
{
  "error": "预览需要更新，你的选择已保留",
  "code": "PREVIEW_EXPIRED",
  "requestId": "request-example",
  "retryable": false,
  "action": "repreview",
  "details": {"sourceRef": "source-main", "expiresAt": 1792311300000}
}
```

| HTTP | code | 页面/调用方处理 |
| --- | --- | --- |
| 400/413 | `INVALID_INPUT/BODY_TOO_LARGE` | 指向字段，不清输入；非法来源地址不发网络请求 |
| 401/403 | `UNAUTHENTICATED/ADMIN_REQUIRED` | 沿用登录/权限流程，不读个人网盘凭据 |
| 404 | `NOT_FOUND` | 本地不存在；外部资料删除另用 `UPSTREAM_NOT_FOUND`，保留本地配置 |
| 409 | `REVISION_CONFLICT/IDEMPOTENCY_KEY_REUSED` | 保留输入，读取新版本/提示重复请求冲突，不用新 key 自动再提交 |
| 409 | `PREVIEW_REQUIRED/PREVIEW_EXPIRED/PREVIEW_CHANGED/REPREVIEW_REQUIRED` | 保留明确选择，逐源生成/更新预览；details.issues 指明 sourceRef/sourceId，未执行核对前不显示“已自动核对” |
| 409 | `SUBSCRIPTION_EXISTS/SOURCE_EXISTS` | 返回已有本地ID，定位原配置，不重复建立 |
| 409 | `ACCOUNT_SNAPSHOT_CHANGED/DIRECTORY_BINDING_CHANGED` | 说明重新配置/核对，不能选择小号或静默换账号 |
| 409 | `RESOURCE_TARGET_CONFLICT/WRITE_RESULT_UNKNOWN` | 保留候选或下载事实，提供核对详情；不盲目接管/重提 |
| 409 | `PAUSED/CONFIG_INCOMPLETE` | 指出前提及可执行动作；不借重试绕过暂停 |
| 409 | `TARGET_NAME_CONFLICT/PLAN_CHANGED` | 只阻止整理；说明文件保留、未覆盖，重新预览范围 |
| 409 | `CAPABILITY_UNAVAILABLE` | P1 隐藏下载/整理动作，服务端同样拒绝 |
| 422 | `SOURCE_SCOPE_UNCONFIRMED/RESOURCE_UNSUPPORTED` | 保留待确认，不当作普通零结果 |
| 429 | `SOURCE_COOLDOWN` | 返回 retryAfterAt 与 Retry-After，手动动作不旁路 |
| 502/504 | `UPSTREAM_UNAVAILABLE/UPSTREAM_TIMEOUT/UPSTREAM_INVALID` | 保留旧成功快照；没有有效旧数据才显示读取失败，不伪造空结果 |

issues 中 reasonCode 与 HTTP code 共用固定枚举；资源行还允许 `BELOW_MINIMUM/OUT_OF_SCOPE/KEYWORD_EXCLUDED/KEYWORD_REQUIRED/RESOLUTION_MISMATCH/NUMBER_UNCERTAIN/HISTORY_UNLOADED/TIME_UNCERTAIN/IDENTITY_UNVERIFIED/ALREADY_LINKED/DISMISSED`。未知 code 显示安全通用说明并可查看 requestId，不把原始上游 HTML 或 Cookie 回显。普通零匹配不是错误，返回200、matchedCount=0、reviewCount=0，以中性文字说明。

版本冲突用行内提示保留当前输入；读取服务器新版本供比对，只有用户明确按核对后的配置再次保存，才使用新 revision 与新 key。不能仅把旧表单的 revision 替换成最新值后自动重试，造成其他窗口配置被覆盖；不额外叠加模态确认。

## 9. 后续验证样本与阶段接入

可复用样例和本项临时 SQLite 验证脚本位于 [样例目录](examples/anime-contract/README.md)。样例均为合成数据，不包含真实账号/文件凭据；离线执行只验证契约约束与样例一致性，不创建应用存储或访问网络。

后续 Go 测试需覆盖：GUID碰撞的两个原帖；两个组同集不同 Hash；同组v1/v2；同 Hash多RSS并发一个claim/活跃attempt；用户取消后不重选；预览绝对过期/独立source版本/总预算；缓存 observedAt 与生成时刻不同；基准25对应E01且从30下载仍E06；无TMDB照常下载；目录结果不能视作视频；未知提交重启先核对；暂停获准单操作后阻止下一阶段；旧租约回执不能推进新任务；完整路径冲突不覆盖；完成后不再有 due 任务。

P1-01 只接入该阶段模型/事务，保留后续字段的契约名称；P1-07 注册本阶段 API，服务端能力关闸；P2-02/03 接资源身份/共享协调；P2-07 补115子文件验证；P3 独立接入命名快照与后处理。实际 Go 测试、HTTP 验证和页面验收分别在对应任务打卡，本项 SQL/样例验证不能替代它们。

## 10. 审查记录

2026-10-18，Codex 核对现有 SQLite 初始化/迁移、管理员鉴权装配、蜜柑网页读取与缓存、手动云下载、手动整理和全站季集方案存储，划定可复用底层能力及不能直接接入的业务副作用。独立审查覆盖：不按集号互斥、UID/账号/目录快照与资源认领、同 Hash 多源授权、单操作许可/租约恢复、缓存实际读取边界、规则重确认不重建历史基线、配置与任务版本区分、来源独立状态、终态无调度及不级联删除。

Gemini CLI 只参与交互投影审阅。首轮提出状态层级、保存/启用边界、统计维度及逐源预算提示问题；因该轮累计上下文超出技能建议范围，后续换新会话使用精简内联契约复核。再处理无效源原位提示、按钮判定优先级、重预览越界选择、父级恢复不覆盖子源意图后，最终复核明确“无剩余必须修正的问题”。未采纳将待确认行直接勾选为下载资源、将 pendingVerificationCount 当作 reviewCount、或自动用新 revision 覆盖旧表单等会改变约定的解释。该结论是交互契约审阅，实际页面仍在 P1/P2/P3 验收。

本项执行 Python 3 标准库与 SQLite 3.50.4 的内存验证：24张表成功建成，15项应拒绝的约束操作确实被拒绝，正例验证 GUID 碰撞、同集不同资源、同资源多源关联、不同 UID 资源认领、显式尝试历史、基线外键、终态无调度、配置 CAS、冲突保留事实，以及 foreign_key_check/integrity_check。23个后续服务/API 场景保存为合成样例，未冒充已执行的 Go/API 测试。执行方式及验证脚本可随文档复现。

另核对本地 Markdown 链接、JSON 示例、任务编号/依赖/完成统计与差异格式。本项未修改生产 Go/Vue 代码、应用 schemaVersion、真实数据库或云端文件，未执行页面构建/浏览器验收。P0-05 到此完成；下一项 P1-01 才开始实际模型与 SQLite 存储接入。
