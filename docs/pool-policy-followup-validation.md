# 设备策略与动态池待办实施记录

日期：2026-10-03。本轮采用临时 SQLite、模拟上游、可控时钟及模拟 API 浏览器验证，不包含真实 Yamby、Emby 或 115 联动。

## 参数与策略交叉状态

心跳静默默认 **60 秒**，合法范围 **30–180 秒**，多设备并行仍为默认策略。已保存的 90 秒等合法配置保留；首次配置或空值使用新默认。候选预加载的 90 秒期限、Emby Sessions 的新鲜度窗口及取链总预算没有随之改变。

设置位置：**Emby → 302 反代 → 播放设备策略**。用户会话页面读取当前策略，分别解释并行与心跳熔断；正式会话记录数、UID 逻辑名额和 CDN 连接数不能混用。

`TestDevicePolicyConfiguredBoundaries` 覆盖 0/30/60/90/180 秒，以及 29/181 拒绝；每个合法阈值分别验证前 1 毫秒拒绝、到期可接替。`TestDevicePolicyCrossStateMatrix` 覆盖：

- 并行已有两台设备时切入熔断：已有记录不主动删除，静默前阻止新设备，静默后事务接替。
- 准备期间改变策略或阈值：旧策略请求提交失败，旧播放记录保留。
- 正常停止残余：并行继续计数；切入熔断后可以继承。
- 下调容量：旧设备刷新与不增加占用的名额继承允许；全新用户申请拒绝。

本轮修复了下调容量后提交检查过严的问题：只有仍有效、已签发的同设备名额或代次匹配的被借用名额可提供继承额度，提交后的占用不得增加。过期或消失的旧名额不提供额度，原有过期借位防超发测试保留。

## 统一错误决策

`ClassifyPoolFault` 接收显式来源和结构化错误。探测与目标取链共用分类，不按未知错误文字推断封号。所有换 UID 仍受原候选次数、UID 去重、全局冷却和总截止时间约束。

| 已知错误 | 作用范围 | 目标/探测可换 UID | 目标健康状态 |
| --- | --- | --- | --- |
| 登录失效 | 凭据配置 | 是 | invalid_credentials |
| 目录无效 | 凭据配置及目录 | 是 | invalid_directory |
| HTTP 429 | 全局，另保存 UID 冷却 | 否，遵守冷却 | throttled |
| 明确 UID 限流 | UID | 否，保留原 UID | throttled |
| HTTP 5xx、网络错误或单次探测超时 | 当前配置的暂时故障 | 首次短冷却复核原 UID，连续第二轮失败才允许 | cooldown |
| 文件缺失、不可见、取链受限 | 文件 | 否 | 不改变 |
| 未知业务码、无效文件响应 | 文件/请求 | 否 | 不改变 |
| 写入结果不确定 | 当前文件及写入目标 | 否 | 不改变 |
| 请求取消、队列满、配置变更 | 请求 | 否 | 不改变 |

源盘、校验副本和 Emby 来源的错误均不改变目标健康、不授权目标切换；源盘 HTTP 429 的全局请求预算仍独立生效。健康更新与目标重试只消费可信错误类型。取消的父请求优先结束，不将取消造成的探测失败计入健康。

对外保持已有安全响应：设备冲突为 HTTP 409 `USER_DEVICE_CONFLICT`，缺可信设备为 HTTP 403 `PLAYBACK_DEVICE_REQUIRED`，旧会话为 HTTP 409 `USER_PLAYBACK_STOPPED`；容量、池冷却、池不可用为 HTTP 503 安全文案；授权阶段超时为 504，客户端取消为 499。文件取链错误保留现有取链路径的安全文案，不把内部分类或上游原文暴露为新的公共协议。

`TestPoolFaultMatrix` 对 15 类错误乘 5 种来源做表驱动校验，并实际运行调度器，验证文件/未知/不确定写入错误只尝试一次、不隔离健康配置、不泄漏预占。已知 115 类型之外仍保持保守拒绝，不宣称覆盖所有业务码。

## 诊断与指标定义

每个请求最多记录 32 条候选诊断，超出记录 dropped 数。记录候选排序未选中、排除、选中、探测结果及目标故障；UID 使用摘要，包含租约代次，关联编号沿用请求日志上下文。复用已有租约会提前结束选择，不为诊断额外扫描池或查询云端。

API 统计在已有统一预算放行处计数：凭据检查、文件查询、取链、秒传提交、目录写入、其他元数据；来源分为源盘、校验副本、目标和后台探测。阶段、来源两组各自合计等于放行总数。请求日志与运行累计均提供这些计数。预算放行不等同于成功 HTTP 响应，也不包含 CDN 片段读取；未纳入播放池预算的其他业务流量不在分母内。

| 指标 | 分子 / 分母或计数含义 |
| --- | --- |
| 成功率 | 成功 302 / 已完成的池用户取链请求 |
| 故障换 UID 请求比例 | 至少尝试两个 UID 的请求 / 已完成的池用户取链请求；失败请求也包含在分母中 |
| Switches | 尝试不同 UID 的转换次数，不是请求比例 |
| 已有副本复用率 | 成功复用已有目标副本（含副本直链缓存命中） / 成功副本路径；新建秒传不计复用 |
| 设备冲突 | 以可信设备冲突结束的请求数 |
| UID 容量拒绝 | 明确以 uid_capacity_full 结束的请求数；混合拒绝不猜归类 |
| 容量转交 | 池与用户事务成功、退休旧设备或实际继承旧名额的提交数；事务失败不计 |
| p50/p95 | 最近 256 次完成请求在反代内的耗时，不是播放器起播耗时 |

累计指标重启清零，数据库持久健康事件继续保留。统计及快照均加锁，返回独立 map/slice；日志没有 Cookie、Token、文件 URL 或上游原始错误。`TestPoolDiagnosticBoundedAndPrivate` 与 `TestPoolBudgetStageOriginAndRequestSnapshots` 验证并发、有界、脱敏、分母和快照隔离。

## 可重复模拟基线

仓库根目录运行：

```powershell
node backend/scripts/pool-baseline.cjs docs/pool-simulation-baseline.json
```

脚本运行实际 Go 服务测试并输出 JSON，任何断言失败均非零退出。首份结果在 [pool-simulation-baseline.json](pool-simulation-baseline.json)。这是本次基线，没有同条件的历史数据，不计算改造前后改善百分比。

- 冷启动与已有副本缓存：并发 4，每个 worker 使用独立缓存与固定一部影片；被插桩的模拟客户端操作固定延迟 2 毫秒。冷启动共 4 次上传，后续 80 次复用为 0 次上传。
- 同 UID 转交：单并发连续 20 次，容量 1，探测即时成功；每次断言占用 1。
- 跨 UID 回滚：单并发连续 20 次，旧 UID 明确登录失效，新 UID 准备后取消提交；每次断言旧会话保留、新预占释放。
- API 排队：4 路并发，同 UID，固定 1 毫秒间隔，仅测试预算调度，记录最大等待者及耗时。

报告中的 `mockClientOperations` 是被插桩的模拟客户端方法调用数，转交场景为模拟探测数；不能当作真实 115 HTTP 调用数。报告的零队列/占用表示该子场景未经过此组件。缓存、调度与预算分别测量，不能将各子场景拼成真实端到端性能结论。延迟含实际 SQLite、系统调度及文件系统开销，会受本机负载影响；无真实凭据、云端写入或账号压测。

## 验证命令

- 相关后端回归：`go test ./internal/service ./internal/httpapi ./internal/client/p115 ./internal/storage -count=1 -timeout=180s`。
- 策略模拟灰度：`go test ./internal/service -run 'TestDevicePolicy|TestPoolFault|TestPoolDiagnostic|TestPoolSimulationBaseline' -count=20 -timeout=180s`。
- 浏览器模拟 API：`playback-device-policy-ui.cjs`、`user-playback-sessions-ui.cjs`、`dynamic-pool-settings-ui.cjs`。
- 静态检查与构建：`go vet ./...`、`go build ./...`、`npm run build`。
- Linux 四包完整竞争检测：`go test -race ./internal/service ./internal/httpapi ./internal/client/p115 ./internal/storage -count=1 -timeout=300s`。

本轮结果：全仓 `go test ./... -count=1 -timeout=180s` 通过；20 轮策略/故障/诊断/基线模拟灰度通过（141.910 秒）；Linux 四包完整 race 通过（service 267.805 秒、HTTP 67.851 秒、p115 1.410 秒、storage 27.129 秒）。三个浏览器模拟脚本、前端构建、`go vet ./...`、`go build ./...` 与 `git diff --check` 通过。Vite 保留已有大包提示。

末次自查补充：未分类探测失败保留健康状态，但必须向管理端返回错误，不能报告检查成功。该调整后 service/HTTP 的 `TestPool|TestDynamicPool|TestDevicePolicy` 专项再次通过，vet 和后端构建再次通过；这处错误返回调整未重跑整包 race。没有替换运行后端、清空租约或改动真实账号。
