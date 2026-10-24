# 动漫管理员配置 API（P1-07）

实施日期：2026-10-18。

本项接通 `/api/admin/anime`，复用已有管理员鉴权、Bangumi 客户端、蜜柑站点闸门和持久预览。能力固定为 `configure=true, download=false, organize=false`。前端页面在 P1-08 起实施，本项没有创建下载调度或接入媒体服务器。

## 接口与输入

已注册契约第 7.1 节的日历、搜索、详情、来源候选、实际字幕组、绑定预览、资源预览、继续加载、本地资源分页、草稿/暂停配置保存、订阅列表/详情及功能设置接口。新增 `POST /previews/:batchId/selections`，只修改预览的表单选择快照，需要 `sourceRef/sourcePreviewId/expectedLoadRevision` 以及明确的 `selectedReleaseIds/dismissedReleaseIds` 数组；空数组代表明确清空，不代表默认全选。选择和加载共享 loadRevision CAS。

配置保存使用下面的外部 DTO；PATCH 的实体 ID 取自路径。普通字段遵循契约的字符、枚举及数量限制。`removedSourceIds` 的每项是 `{id,expectedRevision}`，名称表达显式移除，不能只传裸 ID。

```json
{
  "saveMode": "draft",
  "expectedRevision": 0,
  "settingsRevision": 1,
  "config": {
    "entry": {"bangumiId": "278826", "editionKey": "original"},
    "sources": [{
      "ref": "main",
      "id": "",
      "expectedRevision": 0,
      "role": "main",
      "position": 0,
      "status": "paused",
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
        "include": [],
        "exclude": [],
        "minimumEpisode": 30,
        "initialMode": "from_episode"
      }
    }],
    "removedSourceIds": [],
    "formSelections": [],
    "previewRefs": []
  }
}
```

已有篇章使用 `entry={id}`，不能再附带可重绑定的外部身份。蜜柑快捷入口允许 `bangumiId=null`，标题从实际蜜柑条目取得；未选组的草稿也可按已选 itemId 读取标题。已有本地篇章、订阅详情和列表不依赖上游资料继续存在。相同 Bangumi/edition 已有订阅时直接返回本地 existingId；不再次请求上游。

暂停配置必须具备完整主源、完整的 paused 来源规则以及当前主账号目录绑定；不完整返回 `409 CONFIG_INCOMPLETE`，没有静默降级。未完成的备用来源可以保持 draft。保存 paused 只校验元数据，不要求 RSS/历史请求成功。已有来源复用本地核验资料，不为纯本地配置编辑重复请求源站。

草稿的未设置 itemId/groupId 允许在原来源第一次补全，保存和预览均校验所属订阅与版本；原 namespace 保持稳定。已经选定的字段不能清空或换成其他条目/组，换组需显式移除并建立新来源。遗漏来源保留，只有显式移除才改变其状态。

设置 PATCH 输入为 `expectedRevision/downloadCid/organizeCid/expectedPrimaryAccountId/expectedPrimaryAccountRevision`，organizeCid 可为 null。账号参数只作并发校验，不能选用小号。服务器固定一次主账号凭据，读取目录的真实 CID 和路径后保存；不接受客户端 downloadPath/organizePath。相同下载/整理 CID 只读一次，目录读取请求 limit=1，不遍历文件。

## 原子性与请求边界

- 写配置和设置必须带 8～128 个 ASCII 字母、数字、`-_` 构成的 `Idempotency-Key`。作用域为可信管理员 ID、方法及含实体 ID 的规范化路径、key。
- 配置指纹按规范化 DTO 计算：规则词折叠/去重/排序，来源、移除及表单集合排序，空集合统一，浏览器提供的 groupName 不参与。请求 key 未提交成功时可重试原请求，不自动换 key 或更新 expectedRevision。
- 鉴权后先查原回执，再检查当前版本或请求上游；并发请求在初次查询后提交成功时，失败请求结束前再查同 key 回执。配置、可信新篇章、父/来源版本、设置快照及回执同事务提交。新篇章随配置失败回滚，不留孤立行。设置和回执也同事务。
- 保存设置最终事务核对 `p115_accounts` 的明确主账号 ID、revision、UID、admin 归属及非空凭据密文；完整 paused 配置也在最终事务复核，网络读取期间换号不能提交。没有网络等待持有数据库事务。
- POST 重放保持原 201，PATCH 重放保持原 200 与原 receipt。回执只含本地 ID、版本、非敏感配置，不持久化 Cookie、上游原响应或请求正文。
- 普通请求最多 64 KiB；配置/选择/预览最多 1 MiB。先有界读完再严格解码，因此超限的无效 JSON 也返回 413。未知字段、多段 JSON、非法 ID/数字/枚举返回 400，校验失败不调用上游。请求 context 向下传递。
- 所有路由先执行现有认证和管理员校验；本模块为认证失败补充固定 code，不改变其他模块的错误响应。管理响应保持 `Cache-Control: no-store`，不回显内部错误、HTML 或凭据。冷却响应保留 Retry-After/retryAfterAt。

## 预览与读取投影

预览响应返回 batchId、总预算、逐源摘要、当前源默认 50 条分页和 dataVersion，不序列化整个内部 batch。外部引用统一叫 `sourcePreviewId`；`:id` 路径仍为 batchId。加载/选择使用 expectedLoadRevision，选择更新会递增版本。其他管理员不能读取、选择或引用本管理员的预览。

保存时 `formSelections` 保存 selected/unresolved/dismissed 输入，来源 ref 转为稳定 sourceId；`previewRefs` 保存 batchId、原 sourceRef、sourcePreviewId、loadRevision，并另存稳定 sourceId，避免改写旧预览的查询键。引用须属于同篇章、来源及管理员；已保存的恢复引用可以再次保存。过期/旧 loadRevision 引用仅作恢复提示，不授予执行权限，不修改 T0、绝对过期时间或初始集合。回执明确 `requiresRepreview=true`；后续执行必须按当前本地身份和新版本重新核对。

列表与详情只查本地数据：返回篇章、配置、来源 configuredState/runWhenEnabled/effectiveState/blockedBy/health、各层独立统计和 permittedActions。未启用配置的表单勾选不计入 releaseStats.selectedCount，P1 的任务/文件/整理统计为零。来源有效状态按契约优先级投影，父订阅暂停不会把备用草稿显示为运行。列表/资源分页默认 50、最大 100，订阅排序为 updatedAt DESC,id DESC，带 total/page/pageSize/items/dataVersion。资源读取先核对 sourceId 属于路径中的订阅。

启用、检查、下载重试及整理写入口均返回 `409 CAPABILITY_UNAVAILABLE`。本项不建立 baseline、执行 selected/dismissed 决定、Hash claim、115 下载任务或 nextCheckAt；没有为 P2 提前创建资源/任务表。SQLite 仍为 v41，新增恢复字段使用既有 form_json，无新增迁移或旧功能兼容。

## 验证与审查

新增 16 项顶层测试（HTTP 4、service 9、storage 2、115 目录读取 1，含多组子场景），覆盖认证先行、严格 JSON/大小限制、非法参数不联网、安全错误、阶段关闸、真实来源名称、主账号读取固定及换号竞态、父/来源 CAS、显式移除、来源身份首次补全、过期预览恢复、跨管理员/订阅隔离、回执先行、并发同 key、配置失败原子回滚以及无执行记录。并发同 key 测试另重复 10 次通过。

Codex 后端自审修正了不完整草稿无法首次补全来源、保存后原 previewRef 查询键丢失、相同 key 并发回执与空 map 响应不一致等边界。没有前端交互修改，本项未调用 Gemini，页面讨论与审查在 P1-08 起进行。

验证命令：

```powershell
cd backend
go test ./internal/anime/preview ./internal/anime/binding ./internal/animeparse ./internal/client/mikan ./internal/client/bangumi ./internal/client/p115 ./internal/model ./internal/storage ./internal/service ./internal/httpapi -count=1
go vet ./internal/anime/preview ./internal/anime/binding ./internal/animeparse ./internal/client/mikan ./internal/client/bangumi ./internal/client/p115 ./internal/model ./internal/storage ./internal/service ./internal/httpapi
go test ./... -run '^$'
cd ..
py -3 docs/examples/anime-contract/validate.py
git diff --check
```

上述检查全部通过，相关包完整回归的 service 包约 99 秒，HTTP 包约 20 秒。另核对本文 JSON 示例、本地 Markdown 链接及任务表统计。

接口用真实鉴权中间件、临时 SQLite 与模拟上游验证，没有向真实 115 提交或整理文件。开发 Air 已重载新路由，真实服务健康检查返回 200；匿名访问 capabilities 返回 401、UNAUTHENTICATED 和 no-store。本项没有通过业务接口保存真实订阅或目录，也未新增数据迁移。当前环境无 C 编译器，race 限制沿用 P1-06 记录；页面和 115 实站目录/子文件验收分别由后续任务完成。
