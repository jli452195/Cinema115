# 动漫确保提交与不确定结果对账

日期：2026-10-19。对应任务：P2-04。

当前首次完成检查与连续整理行为见[下载与整理等待优化](anime-download-latency-optimization.md)，已取消正常提交后的两分钟等待。

2026-10-20 P2-08阶段记录：正常提交成功直接保存接受回执，不立即读回；首次状态检查至少两分钟后。只有未知回执才在后续恢复中按固定Hash/目标最小补查，不重复提交。已确认目录复用创建回执，恢复会话不再查询父目录。以下保留原P2-04实现记录与[低请求执行恢复](anime-download-recovery-implementation.md)阶段说明。

## 交付范围

新增独立 `EnsureDownload` 服务入口：先持久保存提交意图，最终事务授权后只发送一次请求，再精确读取核对归属。请求超时、回执丢失、查询失败、数据库写回失败以及重启后的 `dispatched/unknown` 都只对账，不删除旧任务或自动重新提交。

本项接入提交基础，不开放订阅启用、后台调度或新 HTTP 操作。资源子目录的创建与持久绑定由 P2-07 接入；当前入口要求该目录已绑定并验证，缺失时拒绝提交，不能退回下载父目录。下载状态追踪、租约恢复和页面分别留在 P2-06、P2-08 和 P2-10。

## 代码与存储

| 位置 | 职责 |
| --- | --- |
| [提交模型](../backend/internal/model/anime_submit.go) | 白名单请求、观测和结果；只保存固定目标、Hash、目录与必要标识 |
| [版本47迁移](../backend/internal/storage/anime_submit_schema.go) | 新增 `anime_drive_operations`；当前仅允许 `submit`，后续建目录/整理操作随对应任务接入 |
| [提交事务](../backend/internal/storage/anime_submit.go) | 准备、单次派发许可、不可重放的结果写回与旧代次隔离 |
| [自动115客户端](../backend/internal/client/p115/anime_offline.go) | 精确查询和单次提交；严格响应解析，无删除/重提方法 |
| [确保提交服务](../backend/internal/service/anime_submit.go) | 固定会话、共享协调、目录读回、单次请求与对账 |
| [目录读取](../backend/internal/client/p115/directory.go) | 从已验证面包屑提供父目录和名称，保留最多一行的有界读取 |

SQLite 由 v46 升至 v47。每个下载任务有唯一 `submit:<jobID>` 操作键，`prepared/dispatched/confirmed/unknown/failed` 分开保存；已发出或结果未知的操作不能退回 `prepared`。任务与操作的派发、回执更新均同事务提交。日志不保存 Cookie、磁力原文、签名、直链或任意上游正文；内存会话固定原账号凭据。

## 写入许可与目标归属

1. 在 P2-03 的同一实际 UID/资源协调范围内重读任务，核对固定主账号编号、revision、UID、设置快照及资源身份。
2. `PrepareAnimeSubmit` 仅为当前有效排队任务建立意图，资源目录必须是已绑定的 `anime-<jobID>` 子目录。至少一条独立来源授权仍须有效；主源撤回但备用来源仍有效时可以继续，不改变原命名/字幕组快照。
3. 精确查询同 Hash。派发前已经存在的任务一律记为归属冲突，即使目标相同也不自动接管其他工具任务。只有本模块已有派发记录时才进入恢复对账。
4. 按确切 CID 读取资源目录，核对父 CID 和生成名称。最后复核会话，再由 `DispatchAnimeSubmit` 事务重验账号、任务/来源/订阅代次、基线、发布与规则/解析/时间版本及选择授权。
5. 先提交 `dispatched/authorized_at` 和任务 `submitting`，再用固定 Cookie 发送一次请求。并发调用只能取得一个派发许可，网络调用不占用数据库事务。

暂停或撤回阻止后续写入；已授权请求的真实回执仍可保存。任务代次、目标、工作阶段发生变化时，只在原操作中保留 `unknown/STALE_GENERATION` 观测，不推进当前任务。

## 精确对账

| 观测 | 记录与后续行为 |
| --- | --- |
| 精确 Hash 且目标 CID 等于本资源目录 | 操作 `confirmed`、任务 `accepted`；不代表下载或文件已完成 |
| 精确查询缺少目标，但结果目录的可信面包屑证明其位于本资源目录 | 同上；结果就是本资源目录时，还核对原父目录和生成名称 |
| 单次提交明确拒绝，随后精确查询明确不存在 | `failed/SUBMIT_REJECTED`，不自动重试 |
| 已派发后查不到任务、查询失败、归属不明或指向其他目录 | `unknown`，保留原因，仅允许对账 |
| 提交回执丢失，但读回精确归属一致 | 可以确认原操作，不能再次提交 |
| 结果写回失败 | 原 `dispatched` 仍持久存在；下次只读核对 |
| 已确认或明确失败的原操作被重复调用 | 返回已知结果，不继续查询或提交，不回退终态 |

自动客户端仅调用精确 `get_user_task`。明确不存在限定为合法失败信封、错误码 `10021`、没有任务载荷或任务身份字段；列表缺项、空/非法响应、鉴权及网络错误均不作为不存在。解析拒绝重复 JSON 键、尾随数据、冲突错误码/Hash 别名、错误状态类型和非法 ID，大整数保留字符串精度。

提交成功信封只表示收到响应，仍需读回。重复任务响应或含糊回执不能证明失败；已有任务不会被删除。文件型结果对象、实际文件完整性与下载状态不在本项推断。

整步使用请求 Context 和 90 秒上限；断连后的结果落库与最终结果读取同步执行，各有 5 秒独立收尾预算，不启动脱离生命周期的后台协程。

## 验证与审查

新增 17 项顶层测试：客户端 3 项、提交存储 3 项、迁移 2 项、服务组合 9 项。使用模拟网关、`httptest` 和临时 SQLite，覆盖：

- 严格信封、精确查询、Hash/目标冲突、大整数、单次请求及不调用删除。
- 24 路并发准备/派发只获得一次许可；有效备用来源独立授权。
- 请求前持久意图可见；重复确保只提交一次，已确认不再轮询。
- 超时、明确拒绝、取消、回执丢失、提交后缺项、外部目录及目标关系不明。
- 准备/派发/结果写回故障回滚；下一次通过原日志对账。
- 派发前暂停、撤权、规则/任务/来源代次变化、账号或目标变更以及目录缺失/错误均拒绝写入。
- 派发后暂停保留事实；旧任务代次不推进当前任务；非法结果输入不改变已派发意图。
- 在 prepared、dispatched、unknown、confirmed 四个边界实际关闭重开 SQLite，重建账号服务、会话和协调后恢复。
- 实际 v46→v47 升级保留任务/来源选择，迁移失败保持原版本并完整回滚。

验证命令：

```text
go test ./internal/client/p115 ./internal/service ./internal/storage -run 'Test(AnimeEnsure|AnimeSubmit|AnimeTaskObservation|AnimeOffline)' -count=5
go test ./internal/btresource ./internal/anime/preview ./internal/model ./internal/storage ./internal/service ./internal/httpapi ./internal/client/p115 ./internal/bot
go vet ./internal/btresource ./internal/anime/preview ./internal/model ./internal/storage ./internal/service ./internal/httpapi ./internal/client/p115 ./internal/bot
go build ./...
```

以上通过。完整回归 service 151.6 秒、httpapi 80.5 秒、storage 10.8 秒。最终自审进一步收紧已派发冲突不得写成失败、未知结果必须带原因，并补断言；修改后相关提交服务/存储场景、两包 vet 和全后端构建再次通过。`git diff --check` 及文档链接/任务统计核对通过。

后端由 Codex 设计和自审，无前端交互改动。本机 `CGO_ENABLED=0` 且无 gcc/clang，竞争检测未运行，不记作通过。未访问真实 115、提交真实下载、保存真实订阅或更改真实目录；子目录绑定由测试构造，实站生命周期验证继续留在后续任务。

最终文件流程保持：目录 1 的资源子目录下载，重命名并移动原文件到目录 2，移动成功后原位置不再有该文件；实际整理由 P3 实现，不增加复制后删除流程。
