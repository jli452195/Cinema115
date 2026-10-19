# 115 下载状态追踪

日期：2026-10-19。对应任务：P2-06。

## 实现范围

为提交日志已经确认归属、账号/目录/Hash 固定的下载任务接入后台只读追踪。下载、实际文件和整理工作流仍分开：远端完成只更新 `downloadState=completed`，不把 `fileState` 改为 verified，也不结束工作流。

追踪器不提供提交、删除、重提或云端文件写入方法。prepared/dispatched/unknown 的提交日志由 P2-04 对账基础和后续 P2-08 执行恢复处理，不能直接当成已确认任务来追踪。已有 confirmed 日志的任务后来失踪，则保留本地认领和记录，绝不退回 queued 或新建提交。

公开 API 的 download/organize 能力继续关闭；本项没有启用真实订阅或完成真实下载闭环。资源子目录创建、文件/目录辨认及实际文件核验由 P2-07 实现。

## 代码与持久状态

| 位置 | 职责 |
| --- | --- |
| [状态模型](../backend/internal/model/anime_tracking.go) | 固定接口来源、严格规范化、状态白名单与查询摘要 |
| [115 只读适配](../backend/internal/client/p115/anime_tracking.go) | 一次精确 Hash GET；复用严格信封和身份解析，丢弃任意上游正文 |
| [追踪调度器](../backend/internal/service/anime_tracking.go) | 固定凭据恢复、共享 UID/资源协调、取消等待、错误分类与同步收尾 |
| [追踪事务](../backend/internal/storage/anime_tracking.go) | 原子领取、UID 间隔/冷却、回执 CAS、状态和下次时间同时提交 |
| [v49 索引](../backend/internal/storage/anime_tracking_schema.go) | 为 UID 下的有效任务查询增加部分索引，保留既有任务和提交事实 |
| [应用装配](../backend/cmd/server/main.go) | 共享原 cloudClient/coordinator，将 Run 纳入应用取消和退出等待 |

复用 v46 预留的 `raw_status_json/result_object_id/result_object_kind/lease_token/lease_until/next_run_at`；v49 只新增索引，没有新增业务表。`raw_status_json` 实际保存类型化白名单摘要，包含 attemptedAt、上次有效读取 observedAt、失败计数及规范化观测，不保存原始响应、名称、URL、Cookie、签名或错误正文。

任务模型现可读回追踪摘要、结果对象 ID/类型和下次调度时间。结果类型在本阶段始终只补为 unknown，不能把 `file_id` 命名或解释成视频文件身份。

## 状态规则

接口来源固定为 `web_get_user_task_v1`，只调用 `ac=get_user_task&info_hash=...`。不读取整个任务列表，不使用列表缺项、分页总数或 `stat` 筛选参数判断不存在；列表响应即使 HTTP 成功，也不能替代精确任务响应。列表的 `status=2` 不能混入精确查询的完成规则。

| 精确响应 | 规范化结果 |
| --- | --- |
| status=1，明确 pending/waiting/parsing | accepted |
| status=1，明确 running/downloading | downloading，即使进度为 100 |
| status=1，明确 finished/completed/success，进度合法且为 100 | completed，仅证明远端任务状态 |
| status 为合法整数，明确 failed/stopped/cancelled/canceled | failed；不再自动轮询或重提 |
| 缺失、null、空或非法数字；不识别的显示状态；非法或冲突进度 | unknown，保留待核对及退避 |
| 严格失败信封 10021，无数据和任务身份字段 | missing 观测；未曾完成时 downloadState=unknown，不授权重提 |
| HTTP/鉴权/解析错误、Hash 不符、列表缺页 | 查询失败或冲突，不推断完成/不存在 |

数字和数字字符串显式解析，ID 不经过浮点数；拒绝身份别名冲突、重复 JSON 键及尾随数据。进度别名必须均合法且一致，不能选一个方便的值覆盖矛盾。显示状态仅保存有限枚举；未知文本和上游错误消息不会进入任务 JSON。

失败状态使用明确显示语义，不猜测未实测数字枚举。测试中的失败/停止样本属于模拟兼容行为，不作为这些状态已经实站验证的证据；真实证据范围仍以 [115 接口验证](anime-p115-api.md) 为准。

查询错误保留原下载状态、上次成功观测及其时间，只更新本次尝试、固定原因和退避。一次有效但未知/缺失的观测可以把未完成任务变为 unknown。已观察的远端 completed 不因未知/缺失读回而抹去；若随后明确变成运行/失败，则标为状态冲突并停止自动处理，等待核对。

## 归属、调度与停止

1. 只领取 active、未 verified、持有同 generation 的 confirmed 提交日志的任务。无需来源仍启用，暂停订阅后必要的只读追踪继续。
2. 按保存的主账号编号、revision、UID 和目标快照恢复只读会话，内存固定 Cookie；使用与手动下载相同的实际 UID/协议/Hash 协调。排队后重读任务，网络前后及落库事务复核身份，不跟随当前默认目录换目标，不回退其他账号。每条状态查询只有一次精确 Hash 请求，不额外读取父目录或枚举文件；实际目录可访问性留给文件核验阶段。
3. Hash 和已供给的提交目录必须一致。精确查询省略目录时，只允许沿用已经确认匹配的相同结果对象；新/变化的对象不能凭相同标题接管。目录或结果冲突进入 needs_review，保留原结果 ID/观测，停止自动查询。
4. 每轮最多四个 UID，各最多一条任务；同 UID 仅一个有效领取，持久保留至少五秒的查询间隔。通常每任务 60 秒加 0～10 秒稳定错峰；远端完成、尚待文件核对时降到五分钟加错峰。
5. 失败依次至少 1/5/15/30 分钟退避，遵守更晚 Retry-After。429/带 Retry-After 的 503 对同 UID 全部待追踪任务共享冷却，其他 Hash 不能绕开；401/403 停止相同账号版本的新追踪，账号变化也进入待核对，不自动试探恢复。
6. 领取期限两分钟，每条只读流程最多 45 秒。无网络 I/O 位于 SQLite 事务内；退出取消当前请求并等待，其后仅同步执行至多五秒的回执写入。generation、revision、随机 token、期限及当前阶段均参与回执校验，旧回执不能推进/复活任务或释放新领取。
7. 文件 verified、工作流终态、明确远端失败或归属/凭据受阻时不再领取。P2-07 成功核对实际文件后同时清除其调度，即停止离线轮询；本项用模拟文件事实验证停止条件，没有伪造“实际文件核验完成”的写接口。

状态查询的短领取只保护读取和回执，不能替代 P2-08 的写入执行租约、续期和完整重启恢复。

## 验证与边界

新增 19 项顶层测试：客户端 3 项、存储 9 项、迁移 2 项、服务 5 项。专项连续五轮通过，覆盖：

- 23 组严格状态样本：接口数字差异、100% 仍下载中、空/非法/巨大状态、冲突进度、错误与显示文本脱敏。
- 精确 Hash 单次 GET、HTTP 502 不重试；缺列表结构、列表缺页、重复 JSON 键、错误身份不能证明不存在。
- 24 路领取单胜、同 UID 多 Hash 排队、持久最小间隔、共享 Retry-After 和鉴权停止，不因主备来源重复选择增加任务。
- 缺失/未知不重新提交；失败读取保留最后成功时间和状态；远端完成与实际文件/整理终态分开。
- 结果/目录/Hash 冲突、完成后状态回退、请求前后凭据变化、旧 token/代次/版本及取消/verified 后迟到回执隔离。
- 实际关闭重开临时 SQLite 后保留成功观测和 due 时间；v48→v49 保留任务和提交日志，回执及迁移故障完整回滚。
- 共享协调冲突时不发请求；Run 退出取消并等待请求、释放领取；来源暂停后继续只读核对；文件 verified 后不再访问115。

验证命令：

```text
go test ./internal/client/p115 ./internal/storage ./internal/service -run '^TestAnimeStatus' -count=5
go test ./internal/animeparse ./internal/animepreview ./internal/animeresource ./internal/model ./internal/storage ./internal/service ./internal/httpapi ./internal/client/mikan ./internal/client/anibt ./internal/client/garden ./internal/client/p115 ./internal/bot
go vet ./internal/animeparse ./internal/animepreview ./internal/animeresource ./internal/model ./internal/storage ./internal/service ./internal/httpapi ./internal/client/mikan ./internal/client/anibt ./internal/client/garden ./internal/client/p115 ./internal/bot
go build ./...
```

相关 12 包完整回归、vet 与全后端构建已通过（service 106.6 秒、httpapi 35.4 秒、storage 12.2 秒）。最终去除冗余目录读取后，专项五轮、相关四包 vet 和全后端构建再次通过。随后补充“10021同时带列表/状态字段不能当成未命中”的反例，115客户端全量测试、vet及全后端构建再次通过。后端由 Codex 设计和自审，没有前端 UI 改动。本机 CGO_ENABLED=0 且没有 C 编译器，未运行竞争检测，不记为通过。所有验证使用模拟网关/本地 HTTP 和临时 SQLite，没有真实 115/RSS 请求、下载提交或云端文件变更。
