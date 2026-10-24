# 动漫下载阶段实现与验收

日期：2026-10-20。对应任务 P2-10、P2-11、P2-12。

本轮合并完成下载进度交互、失败与并发验证、下载阶段验收，并补齐首次启用接口。流程为“保存配置 → 预览历史 → 显式启用 → 核验资源身份并认领 → 115 下载 → 保存一次文件清单”。P3 云端改名、移动尚未开放。

## 实现范围

- `storage/anime_activation.go`：首次启用事务、历史基线、明确选择、原子幂等回执。
- `service/anime_activation.go`：从已保存配置生成启用预览、恢复表单预选、固定下载目标。
- `storage/anime_job_actions.go`：原任务核对/读取重试、CAS、租约和冷却检查。
- `storage/anime_job_counts.go`、`anime_counts.go`：本地资源、发布和文件统计。
- `service/anime_job_views.go`：精简任务响应及实际依赖能力检测；HTTP 路由在 `httpapi/anime_control.go`。
- `AnimeSubscriptionActivation.vue`、`AnimeDownloadJobs.vue`、`AnimeDownloadActions.vue`：启用确认、紧凑任务列表和逐任务反馈；复用现有浮层、卡片和图标。
- `useAnimeSubscriptionActions.ts`、`useAnimeLocalPolling.ts`：原请求恢复、逐作用域操作状态和可见页面的本地轮询。

没有新增 SQLite 表、迁移、队列或后端常驻循环，结构版本仍为 v50；沿用现有发现、核验和下载执行器。动漫模块不调用 Emby 业务。

## 首次启用

创建/保存仍只接受 draft/paused，不支持 saveMode=enabled。详情中的启用操作先读取本地设置，再逐个为参与运行的来源生成新预览：

```text
POST /api/admin/anime/subscriptions/:id/enable-preview
{ expectedRevision, settingsRevision, sourceId }
```

服务端使用已保存的篇章、来源、规则、设置构造配置，不接受浏览器另行提供执行规则。已保存表单中仍在当前范围且可选的资源恢复勾选；未加载或身份无法确认的旧预选提示展开历史重新核对，不用默认全选覆盖手动小范围选择。

预览每页10条，分页只读本地快照；跨页勾选保留。展开历史沿用原预览选择与加载版本，合计最多2000条。提交字段为：

```json
{
  "expectedRevision": 1,
  "settingsRevision": 2,
  "sources": [{
    "batchId": "batch-example",
    "sourceRef": "saved-source-id",
    "sourcePreviewId": "preview-example",
    "loadRevision": 1,
    "selectedReleaseIds": ["release-a"],
    "dismissedReleaseIds": []
  }]
}
```

`POST /subscriptions/:id/enable` 带 Idempotency-Key，返回200本地控制回执。sourceRef 为已保存来源 ID，允许各源来自不同 batch；集合必须准确覆盖全部 runWhenEnabled 来源，并包含完整主源。服务端在同一事务核对 revision、固定主账号/目录归属、预览所有者/有效期/完整配置指纹、来源/加载版本和资源内容/解析版本。

已加载历史全部冻结为基线，仅显式 selected 且可选的资源产生执行选择；未勾选历史不会在后续 RSS 检查中变成补下载。基线、选择、来源启用、父订阅状态和幂等回执同事务保存。启用本身不直接请求115。成功回执重放优先于当前版本检查，回执未知时可原 key、原 body 核对。

主按钮区分“启用并下载已选 N 条”“启用订阅”“启用并等待更新”；未知编号不能直接勾选为可下载资源。资源核验是下载 Hash 身份核验，不读取网盘文件 SHA1。

## 下载记录与恢复

`GET /subscriptions/:id/jobs` 和 `GET /jobs/:id` 只读本地，返回标题、字幕组、downloadState/fileState/workflowState、错误码、执行时路径/CID、观测与尝试时间、进度、文件清单和 permittedActions。文件大小按十进制字符串返回，避免 JavaScript 大整数精度丢失；不返回 Cookie、签名或下载直链。

任务每页20条，两行紧凑显示，原位展开执行详情；明确区分“待提交、115已接收、115下载中、网盘已完成、文件清单已保存”，不把资源数当集数或把接收当完成。执行时路径是历史快照，不承诺用户二次整理后的位置。

列表、详情、任务页码/展开/滚动使用现有内存缓存，按登录身份清理。每30秒仅在页面可见时读取本地 API；隐藏时停止并取消当前读取。静默更新保留 DOM、按钮焦点、展开和操作反馈，失败保留上次成功内容；任务翻页失败保留原页。分页按钮使用 aria-disabled 和范围守卫，末页不会因原生 disabled 丢失焦点。320px 下预览由外层自然滚动。

暂停、恢复、归档、取消归档、移除接入已有控制事务。移除使用原位确认，Esc 可取消确认；不删除115文件。订阅与每条任务使用独立的 pending scope，未知任务操作不会锁住暂停订阅。未确认的操作在关闭/重开后保留原 key 和不可变 body，不套用新 revision 自动重提。

任务动作均带 expectedRevision 和 Idempotency-Key，返回200，仅安排现有任务 next_run_at：

| 动作 | 允许范围 | 保留边界 |
| --- | --- | --- |
| reconcile | 已派发/未知提交的固定对象核对、已确认任务状态读取、完成后的未完整清单读取 | 不新建任务、attempt 或写授权；暂停时可进行必要只读恢复 |
| retry | 读取失败/清单不完整，或现有授权下尚未派发的准备失败 | 不重放已派发写入；unknown 仅核对，失败终态不盲目重新离线 |

服务端再次校验 CAS、租约、当前主账号和原目录归属；最近30秒的尝试、未到期 Retry-After 不允许手动加速。目录创建结果未知的 queued 任务留在原自动恢复计划，不把可能接着提交的流程标成“只读核对”。清单已保存、取消、失败终态、账号变化和归属冲突无重试入口。

统计分开计算：发布选择可按源重复，下载任务按资源 claim 的最新 attempt 去重；同 Hash 多来源在父订阅只计一次，各来源分别计一次。待核验数排除已授权认领任务。文件数只统计已保存清单中的 file 条目，不用任务数估算，未知清单的 needsReview 文件数当前为0，资源受阻在 jobStats 展示。

## 115 请求数量证据

`TestAnimeDownloadStageEndToEndMinimumRequests` 使用真实保存、预览、启用、选择和认领事务，没有用 SQL 伪造 enabled 或 selected。115 网关使用模拟对象；8路同 key 并发启用均得到同一操作结果。

| 模拟情形 | 创建资源目录 | 提交前精确查重 | 提交离线 | 额外未知核对 | 状态读取 | 文件目录页 |
| --- | --- | --- | --- | --- | --- | --- |
| 正常成功回执 | 1 | 1 | 1 | 0 | 1 | 1 |
| 提交回执丢失 | 1 | 1 | 1 | 1 | 1 | 1 |

这是单个资源、一次状态即完成、单页清单的模拟样本，不代表所有任务固定只需5次请求。下载较久或结果多页时按原调度读取必要状态/页面。正常创建与提交成功不额外补读回执；不查询文件 SHA1 或逐文件元数据。未知场景在暂停后仍仅恢复原对象，提交次数保持1。保存清单后模拟推进24小时再次执行，所有115计数保持不变，手动核对也被拒绝。

## 验证与审查

新增7项顶层测试：storage 启用3项/任务动作2项、service 完整流程1项、httpapi 实际接口1项。覆盖多来源同 Hash 去重、同集多 Hash 共存、显式空选择、11类启用无效/故障事务回滚、幂等/CAS/冷却、回执写回失败、未知不重提、终态/账号冲突，以及已保存预选恢复。专项连续5轮通过，最后补充的真实文件数/任务数/待核验数断言单独复跑通过。

此前 P2-01～09 的故障与并发矩阵随相关包完整回归，包含重启、列表缺页、外部清理、换号、旧代次/租约和共享 RSS 等，证据分别见[提交与核对](anime-submit-implementation.md)、[目录及清单](anime-files-implementation.md)、[低请求恢复](anime-download-recovery-implementation.md)、[订阅控制](anime-subscription-control-implementation.md)。

已通过：

```text
go test ./internal/storage ./internal/service ./internal/httpapi -run '^TestAnime(Activation|JobAction|DownloadStage|DownloadHTTP)' -count=5
go test ./internal/storage ./internal/service ./internal/httpapi ./internal/animeparse ./internal/anime/preview ./internal/btresource ./internal/anime/binding ./internal/client/p115 ./internal/client/mikan ./internal/client/animefeed ./internal/client/anibt ./internal/client/garden
go vet <同上12包>
go build ./...
npm run test:anime-subscription-ui           # 28组
npm run test:anime-subscription-browser-ui   # 11组
npm run test:anime-discovery-ui              # 13组
npm run test:anime-download-ui               # 8组
npm run build                              # vue-tsc + Vite
```

合计60组前端模拟场景通过；日历旧测试补全新增 jobs 响应，并在只有周一番剧的固定样本上明确选择周一，消除当天星期依赖。构建保留原有 libarchive 浏览器外置和大 chunk 提示。当前 CGO_ENABLED=0 且缺少 C 编译器，未运行 race，不将并发用例通过等同于竞争检测通过。

Gemini CLI 参与设计和两轮审查：会话 `d2bcc91a-927e-4310-93e2-a63e098e639d` 讨论任务行、原位展开及恢复交互；会话 `63fe6a49-e731-4e79-b64e-3bff1085e4d8` 完成最终代码与实际图像复核。修复逐任务状态锁、静默刷新闪动、分页焦点和手机嵌套滚动。实际读取桌面、320px、暗色、320px启用操作四张 PNG 后，结论为无可见阻塞问题；原始截图由测试写入 gitignored `frontend/test-results/`。后端由 Codex 独立设计与复核。

全部本轮验证使用临时 SQLite、模拟上游/115 网关和浏览器 API 拦截；没有执行真实115创建、离线下载、改名、移动或删除，也未把模拟样本记为真实下载验收。

## 阶段边界与下一步

实际服务装配齐全时 download 能力已开放；缺依赖时仍关闭，organize 继续关闭。首次启用后规则修改及重确认、逐源启停/恢复、手动 RSS check/history apply 保留后续范围，详情不展示当前无法执行的编辑入口。下载失败没有“删除旧任务再重新离线”的快捷操作。

P2 为12/12，核心为37/47，实施与验收为32/42。下一项 P3-04；建议合并 P3-04～06 完成固定整理目标与命名快照、标准文件名/字幕关联、自动整理配置与路径预览，再接云端写入。继续遵循“下载到目录1 → 移动原文件到目录2 → 记录完成”，不增加完成后的定位、SHA1检查或重复下载。
