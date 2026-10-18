# P1-01 基础模型与 SQLite 存储

完成日期：2026-10-18。依据：[最终契约](anime-subscription-contract.md)、[实施任务表](anime-subscription-tasks.md)。本项仅交付后端模型和本地存储。

## 已接入范围

- `backend/internal/model/anime.go`：本地篇章、资料白名单、主/备用来源、独立绑定与规则、草稿/暂停配置、表单选择、发布观测和逐源决定。
- `backend/internal/model/anime_validation.go`：schemaVersion、外部十进制 ID、集号和关键词上限、范围确认、草稿完整性及 JSON 未知字段/多段输入校验。
- `backend/internal/storage/anime*.go`：篇章保存与查找，订阅原子创建/修改、详情/分页，发布批次保存/读取，逐源决定保存/分页，以及只读功能设置。
- `backend/internal/storage/sqlite.go`：结构版本由 38 升到 39，迁移与版本写入共用事务；初始化最终统一写当前版本，防止旧的结构修复语句在重开时把版本降回 38。

本地 ID 和来源编号空间由服务端生成。sourceKey 由实际蜜柑条目和组 ID 计算，不接收客户端传入的键。篇章允许暂缺 Bangumi ID，确认的 `(bangumi_id, edition_key)` 唯一；已确认篇章身份不能原地改绑。

## 本项边界

迁移接入 P1 的 12 张表：settings、entries、subscriptions、subscription_sources、releases、release_decisions、preview_batches、source_previews、preview_items、source_baselines、baseline_releases、request_keys，均使用 `anime_` 前缀。

预览和基线表仅预留结构；预览计算、逐源加载 CAS、有效期/预算业务及基线激活分别在 P1-06、P2 实现。本项没有这些运行接口。P2 的 `releases.resource_id`、P3 的 `subscriptions.organize_enabled/naming_plan_id` 暂缓，实际库不存在引用未来表的外键或列；[完整 SQL](anime-subscription-schema.sql)仍表示最终 24 表契约。

功能设置初始 revision=1，账号和目录绑定均为 null。本项只提供读取，不允许直接保存调用者声称的账号/目录；明确主账号、实际路径与目录归属验证留在服务接入阶段。存储层验证暂停配置的主源与规则结构，服务/API 还须验证上游资料和当前主账号/目录条件。

P1 写方法只接受草稿/暂停配置，明确拒绝 enabled，以及可执行的 selected/linked 决定。formSelections 仅用于表单恢复，临时来源 ref 保存时转换为稳定来源 ID，不消耗预览、不建立 baseline/下载任务、不设置执行时间。该转换不修改调用者的输入。

没有注册 HTTP 路由、启动调度、接入界面、创建云端目录或调用 115。未改变运行中的真实数据库；版本 39 在新程序下次正常打开数据库时应用。本模块不依赖 Emby、STRM、追更或通知模型。

## 保存与历史规则

- 一次事务保存订阅、来源变更/移除和可选幂等回执，任一步失败全部回滚。已有订阅/来源使用 expectedRevision，检查影响行数；并发旧版本只有一次能成功。
- 未列出的来源保留；移除必须显式传 ID 与版本。最终最多一个主源、四个备用源；完整主源可以与未完成的备用草稿共同保存为暂停，缺少完整主源时不会静默降级保存。
- 条目/字幕组身份不能在已有 sourceId 上替换。通过同事务软移除、新建来源，生成新的 sourceId/namespace；旧发布决定仍可查询。主备角色互换先处理降级，避免唯一索引使正确请求依赖输入顺序。
- 来源配置实际变化才增加来源 revision；后台观测不增加订阅/来源配置版本。不修改原基线及旧决定，也不把同集当作独占键。
- 发布按 `(provider, post_key)` 保存；GUID、集号、字幕组集号均不是唯一键。同集不同原帖/v2 可共存，各来源保留独立决定。过时观测及较低解析/时间版本不会覆盖新记录。
- 决定要求来源版本、发布指纹及白名单快照一致。dismissed 不被后续自动解释覆盖；重新观察发布不会直接改写旧决定。决定批次失败整体回滚。
- 幂等回执按管理主体、操作及 key 隔离，与配置同事务保存。重放先于当前版本检查，相同 key 不同摘要冲突。认证、必传键校验、规范化摘要和 HTTP 投影在 P1-07 接入，内部存储调用允许不传 key。
- 不提供级联清理或业务历史硬删除方法；外部元数据删除不会触发本地删除。列表先关闭父级 rows 再查询子记录，避免单连接自锁，空列表返回空切片。

## 验证与审查

在 backend 目录执行：

```powershell
go test ./internal/model ./internal/storage
go vet ./internal/model ./internal/storage
go test ./... -run '^$'
```

三项均通过。最后一项只编译所有包，不代表全后端测试执行。新增 9 项顶层 Go 测试（含子场景），并运行全部现有 storage 测试。全部使用临时 SQLite，不访问网络或应用真实数据库。

覆盖新库 12 表、无动漫表的 v38 夹具升级、连续重开仍为 v39、旧业务记录保留、外键检查、后续表/列未提前创建；篇章身份与版本；独立规则和编号基准；备用草稿；表单选择不产生执行意图；并发 CAS 单胜；幂等回放；达到来源上限、来源越权和原地换组拒绝；在回执写入处注入失败后的完整回滚；主备互换；软移除后历史保留；GUID 碰撞/同集三个版本；dismissed 保留；旧观测与旧指纹拒绝；发布/决定批次回滚及 context 取消。

尝试 `go test -race ./internal/model ./internal/storage`，环境返回 `-race requires cgo`。本机 CGO_ENABLED=0，PATH 中无 gcc/clang；未安装工具链，未把竞争检测记为通过。普通并发 CAS 测试已执行通过。

Codex 自行完成后端复核，补齐影响行数检查、输入切片隔离、跨订阅来源拒绝、来源主备互换顺序及未来阶段外键边界。没有前端交互改动，因此本项未调用 Gemini CLI 或执行页面验收。P1-11/12 的整阶段验证仍待完成。

下一项：P1-02，Bangumi 客户端与缓存。

## 启动迁移修复（2026-10-18）

实际开发库仍为 v38，残留早期原型的 `anime_subscriptions`、`anime_releases`、`anime_download_claims`、`anime_transfers`。旧订阅按 TMDB/条目/字幕组保存 JSON，没有新版的 `entry_id`。原迁移的 `CREATE TABLE IF NOT EXISTS` 跳过同名表，创建 `anime_subscriptions_entry` 索引时失败；原来的升级测试只覆盖无动漫表的 v38，遗漏了这个场景。

新增 `anime_migration.go`，在 v39 建表前辨认已知旧结构，将四张旧表改名为 `legacy_anime_v38_*`，保留全部列、记录、索引和旧发布到旧订阅的外键。归档与新版建表、版本写入共用事务；结构不匹配、归档名冲突或后续 DDL 失败均不提交。全新库和已迁移的新表不触发归档。不把旧原型的任务解释为新版订阅或自动恢复执行，后续仍按已确认的新方案配置。

新增两项顶层回归测试（含三个失败子场景）：使用实际旧 DDL 的临时夹具复现原错误，再验证旧记录逐字段保留、新订阅可保存、无自动激活、外键指向归档父表、其他业务记录保留、连续重开；归档名冲突、未知旧列和新版 DDL 失败均完整回滚至 v38。原有新库/无动漫表升级测试继续保留。

`go test ./internal/model ./internal/storage ./internal/client/bangumi`、`go vet ./internal/storage`、`go test ./... -run '^$'` 通过。最后一项仅全后端编译。

本地实库在代码修改前使用 SQLite 一致性备份，文件为 `data/backups/cinema115-before-anime-v39-fix-20261018-202358.db`。开发服务热重载后自动迁移至 v39；只读核对四张归档表的行数和内容摘要与备份一致，分别为订阅 1、发布 5、下载认领 3、传输 2，新版创建 12 表且订阅为空。`integrity_check=ok`、外键错误为 0；35 张其他业务表内容未变，运行中的 TMDB 缓存、追更检查及播放池记录有更新、行数未变，未将其宣称为逐字相同。此项属于 P1-01 缺陷修复，未推进 P1-03 或新增完成任务数。

## 用户要求清理废案（2026-10-18）

用户明确要求：“将之前旧版本的数据全删掉，那是废案一直没清理。”上述v39归档保留方式因此停止使用，当前SQLite版本为41。

- v41事务删除四张固定的 `legacy_anime_v38_*` 表及其记录/索引；未升级的已知v38原型先校验结构，再删除四张原型表并创建新方案表，不再产生归档。混合/未知结构仍拒绝处理，失败完整回滚。
- 本地开发服务已热重载到v41，废弃归档已消失；原有旧数据为订阅1、发布5、认领3、传输2。当前新方案12表保留。
- `data/anime-settings.enc` 已确认没有代码引用并删除。没有读取或输出其中凭据。
- 已扫描data目录下全部5份SQLite备份，仅 `cinema115-before-anime-v39-fix-20261018-202358.db` 含旧原型。已在单个事务内删除其中四张旧表，其他37张业务表的行数和逐行内容摘要前后一致；备份完整性及外键检查通过。备份文件继续保留其他业务数据，没有另存废案副本。

新增归档清理保留新版配置/其他业务的回归，旧DDL升级测试改为验证清理结果；未知结构、清理失败及后续DDL失败仍验证整体回滚。相关完整回归、静态检查与全后端编译记录见[P1-06实现](anime-preview-implementation.md)。
