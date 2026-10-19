# 动漫下载账号与目录快照

2026-10-20 P2-08修订：准备和恢复目标不再读取115父目录，仅做本地固定账号、版本和目录快照检查。设置保存时复用原有目录检查，执行时信任创建成功回执；换号后新任务重新选择目录即可，不增加账号探测或迁移。现行实现见[低请求执行恢复](anime-download-recovery-implementation.md)。

日期：2026-10-19。对应任务：P2-01。

## 本次交付

复用 P1 已独立保存的 `anime_settings`，补齐新下载任务准备和重启恢复所需的账号、目录及凭据边界。没有增加数据库迁移，SQLite 仍为 v45；资源认领、任务落库和下载提交分别由后续 P2 任务接入。

- [下载目标模型](../backend/internal/model/anime_download_target.go)：`AnimeDownloadTarget` 保存 schema、功能设置版本、实际账号编号、账号版本、UID、父目录 CID 与原始显示路径。全部为值字段，不保存 Cookie，不引用可变的设置指针。
- [事务校验](../backend/internal/storage/anime_download_target.go)：新任务在同一 SQLite 事务内核对功能设置和实际主账号；恢复任务只核对原账号身份与版本。资源认领/任务创建时须在其写事务内再次调用 `checkAnimeDownloadTargetTx`，准备成功不代表预留了后续写入权限。
- [执行会话](../backend/internal/service/anime_download_target.go)：`PrepareDownloadTarget` 从指定版本的动漫设置准备目标，`ResumeDownloadTarget` 从已保存目标恢复；`AnimeDownloadSession` 私有保存固定凭据，`Target()` 只返回不含凭据的值副本。

这些入口当前只供服务层使用，没有新增 HTTP 执行接口或启用订阅下载开关。P2-02 将把目标快照接入资源认领与任务事务。

## 行为边界

| 情况 | 处理 |
| --- | --- |
| 准备新任务 | 要求功能设置版本一致，绑定明确主账号、账号版本及 UID；核对 Cookie 中的 UID 与记录一致，再以固定凭据只读核验准确 CID |
| 更改默认下载目录 | 新任务使用新设置；现有会话及恢复任务保留原 CID、原设置版本和原始显示路径 |
| 原目录改名 | 按账号、UID 和 CID 核对，可读取新路径；原任务快照保持不变，不按名称寻找替代目录 |
| 原目录缺失、无权读取、响应缺少身份或重定向 | 拒绝继续，不改设置、不扫描其他账号、不回退根目录；上游读取失败不冒充已确认删除 |
| 明确配置 CID `0` | 允许根目录 `/`；缺失目录配置不会隐式变成 `0` |
| 同 UID 更新 Cookie 或账号版本变化 | 旧任务返回 `ACCOUNT_SNAPSHOT_CHANGED`，不自动采用新凭据；新任务也须重新核验并保存目录绑定 |
| 主账号换 UID、删除或清空凭据 | 原绑定拒绝继续；已有健康小号不会成为后备账号，目录与任务不随换号转移 |
| 请求期间更改账号或设置 | 网络读取后再次核对；发现账号变化或新任务设置变化则拒绝返回可用快照 |
| 数据库与内存账号版本不一致 | 持久账号校验拒绝执行，在访问上游前拦住陈旧内存凭据 |
| 进程重启 | 仅在原账号编号、UID、版本及凭据仍匹配时恢复原目标，不读取当前默认目录替代原目标 |

准备、恢复和目录读取使用调用方 context，并设置最长 20 秒期限。网络读取不占用 SQLite 事务。会话在新 I/O 前核对账号，读取后复核，始终使用原凭据；这不是对已发请求的撤回机制，后续写入结果未知仍须走任务对账。

凭据仅存在会话内存，不能从 `Target()`、JSON 或常用 `fmt` 格式化中读出。目录上游错误仅保留固定分类，取消和超时保留可识别的 context 错误，避免上游回显 Cookie 进入日志或响应。没有新缓存、后台协程或轮询。

## 验证与审查

新增 15 个顶层测试，包含模型反例、临时 SQLite 事务与真实关闭/重开、实际加密账号存储及模拟目录请求。覆盖固定快照和格式化脱敏、修改默认目录、目录改名、同 UID 更新凭据、换 UID 产生新账号编号、删除/清空主账号、健康小号不回退、Cookie UID 冲突、错误归属、数据库/内存不一致、请求期间变更、目录重定向/缺失/异常路径、取消及超时、未配置状态与明确根目录。

执行命令（目录为 `backend`）：

```text
go test ./internal/model ./internal/storage ./internal/service -run TestAnimeDownloadTarget -count=5
go test ./internal/model ./internal/storage ./internal/service ./internal/httpapi ./internal/client/p115
go vet ./internal/model ./internal/storage ./internal/service ./internal/httpapi ./internal/client/p115
go build ./...
```

以上检查通过。相关包完整回归耗时：service 96.1 秒、httpapi 33.3 秒、storage 6.6 秒；新增目标测试连续五轮通过。目录改名补充回归也包含在五轮验证中。

`go test -race ./internal/service -run TestAnimeDownloadTarget -count=1` 因 `CGO_ENABLED=0` 无法运行，环境也未安装 gcc/clang，不计作竞争检测通过。P2-11 仍需在适用工具链下做并发与竞争检测验收。

后端由 Codex 自审：确认没有账号回退、网络 I/O 跨数据库事务、持久保存 Cookie 或运行中重定向目标；校验入口不能代替 P2-02 的最终写事务。没有前端交互改动，没有访问真实 115、创建真实订阅、修改真实目录或提交下载。用户浏览器中的未保存配置保持不动。
