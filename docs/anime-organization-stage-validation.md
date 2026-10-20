# 动漫自动整理阶段验收（P3-11～12）

日期：2026-10-20。结论：P3-11、P3-12完成，下一阶段为P4-04～05。范围为既有默认自动整理的集中验证及页面验收。P3-10历史任务补整理已取消，本轮不新增整理入口、开关、数据库结构或后台循环，不操作真实115文件。后端由Codex审查，前端由Gemini CLI参与交互讨论及实际截图复核。

## 后端验收矩阵

| 验收项 | 证据 | 验证结果 |
| --- | --- | --- |
| 编号基准与下载下限独立 | `TestAnimeNamingPerSourceAndDownloadMinimum`；新增 `TestAnimeOrganizationStageDownloadToArchiveAndRestart` | 来源25对应S02E01，从30开始下载仍整理为S02E06；主/备用独立编号 |
| 连续分段与特别篇 | 新增 `TestAnimeOrganizationSplitSeasonsAndExplicitSpecial`；已有编号重叠/冲突测试 | 12与13分别对应S01E12、S02E01；显式SP01及唯一配对字幕进入Season 00；没有目标集则保留原路径并待处理 |
| 标准命名与字幕 | `TestAnimeOrganizationNamesAndUniqueSubtitles`、`SubtitleDirectoryAndUnicode`、`CleansAndBoundsUTF8Names` | 作品/季目录、字幕组、原扩展名大小写、语言及forced后缀保持；多组共存，附件保留 |
| 本订阅方案更新及任务快照 | `TestAnimeNamingAtomicSaveReplayAndOfflineLocalEdit`、`TestAnimeNamingDoesNotReadOrChangeGlobalNumbering`、`TestAnimeOrganizationClaimFreezesTargetAndLeavesExistingTaskUntouched` | 使用已保存资料离线编辑，不改变其他业务编号；新任务使用新方案，已认领任务不改目标、不重复下载 |
| 同名冲突 | `TestAnimeOrganizationDuplicateTargetsBlockVideosAndSubtitles`、`TestAnimeOrganizeConflictStopsBeforeRenaming` | 内部重复路径或云端已有同名文件均不覆盖，冲突在改名前阻止执行 |
| 部分成功及失败 | `TestAnimeOrganizePartialMoveKeepsSuccessAndNeverReplays`；新增组合流程的 `move_without_effect` | 改名成功、移动未生效时保留改名结果；只核对原步骤，未确认移动不重放、不自动重下 |
| 真实SQLite重开恢复 | 新增组合流程的 `mkdir`、`rename`、`move`；既有日志重开/CAS测试 | 关闭Store与测试探针的数据库连接，重开SQLite并重建账号服务、业务服务、协调器与目录缓存；第一轮恢复仅一次固定目录读取，无新写入 |
| 暂停、换凭据、并发及授权 | 既有 `TestAnimeOrganizePauseKeepsReceiptAndStopsNewWrites`、`CredentialChangeAfterReceiptStopsNextWrite`、`SharesManualDirectoryReservation`，存储代次/租约/事务故障测试 | 保存已发出操作的成功事实，拒绝下一次未授权写入，旧租约不能覆盖新记录，复用目录工作锁 |
| 低请求及完成终态 | 新增组合流程；已有60文件批次测试及 `TestAnimeOrganizeMinimumRequestsAndNoTrackingAfterCompletion` | 成功无额外读回，批次共用目录列表；完成后模拟删除目标目录，多轮调度不增加读取、提交、改名或移动 |

新增组合测试沿用真实的保存配置、预览、八路同键启用、资源核验与任务认领事务，再由同一个 `AnimeStatusRunner` 完成提交、下载状态读取、文件清单和整理；没有用SQL制造已完成任务或整理进度。所有上游均为fake网关；临时SQLite与模拟凭据独立于开发业务库。

## 请求数量

以下从目录已配置、资源已选中并完成本地认领开始计算，样例为一个视频、一份匹配字幕和一个保留附件，目标作品/季目录尚未创建。包括下载与整理整个执行过程；不包含管理员前期主动浏览/选择目录或读取TMDB资料。

| 情况 | 115模拟调用数 | 写入次数与恢复边界 |
| --- | --- | --- |
| 正常成功 | 10 | 下载目录创建1、既有离线任务核对1、提交1、状态1、任务清单1；整理根列表1、建作品/季目录2、批量改名1、批量移动1 |
| 建目录回执丢失并重启 | 12 | 写入次数与正常流程一致；恢复核对原父目录，随后读取恢复所得作品目录，避免猜测其为空 |
| 改名回执丢失并重启 | 12 | 写入次数与正常流程一致；核对来源目录中的固定文件ID/名称，后续移动前读取目标目录检查冲突 |
| 移动回执丢失并重启 | 11 | 额外只列固定目标目录一次，确认文件ID/名称后完成，不重复移动 |
| 改名成功、移动未生效 | 11 | 核对固定目标后待人工处理，保留成功改名；后续自动调度零请求 |

上述次数为固定样例断言，实际大目录分页、既有目录、不同季目录及异常冷却会改变读取次数。不存在文件SHA1或逐文件元数据查询；正常成功不追加读回。

## 页面验收与修复

整理专项由8组扩为12组，与既有配置28组、列表11组、日历13组、下载8组共72组模拟浏览器交互。所有业务API均拦截，使用独立无头Chrome上下文，不操作真实订阅或115。

- 订阅级示例随目录设置变化失效；实际任务预览继续使用固定历史方案。
- 任务记录或实际计划读取失败，原DOM、展开和上次成功结果保持。
- 三轮30秒可见静默轮询保持任务、计划DOM和滚动位置，不重新读取冻结命名计划；既有测试继续验证隐藏后停止读取。
- 未知整理只发原任务 `reconcile`，正文仅含原 `expectedRevision`；模拟回执丢失后，即使列表版本已变化，仍沿用原幂等键与正文，不改路径、不重提写入。
- 复现并修复键盘确认后按钮移除导致焦点丢失：仅当原触发器已移除且焦点落回body时，将焦点移到操作反馈，使用 `preventScroll`；用户等待时已移到其他按钮则不抢回焦点。两种情况均有实际浏览器断言。
- 正在整理、未知、部分成功与完成状态清晰区分；未知只核对、完成无执行按钮；预览和路径均说明是保存方案或执行历史。
- 桌面、320px明暗、连续英文长路径及键盘展开通过；保留默认整理流程，无处理模式或历史补整理入口。

Gemini CLI初审会话：`193fd6c1-4752-4347-90bd-edb58e87f0a1`。其关于任务预览被版本刷新销毁、缺少折行/防重复/不重放说明的担心，已结合稳定key、v-show、现有CSS与原因文案排除；没有据此增加冗余提示或功能。键盘焦点问题经浏览器复现后修复。

Gemini CLI最终代码、三张整理PNG及映射补验会话：`f25f4e4b-b442-4e4a-a883-e367c47ebea4`，结论通过，无阻塞问题。审查截图保存在 `frontend/test-results/anime-organization-unknown.png`、`anime-organization-job-320.png`、`anime-organization-job-dark.png`，映射真实视口为 `anime-mapping-anchor-viewport-320.png`、`anime-mapping-special-viewport-320.png`。原映射整元素长截图会包含页面sticky导航在取景中的位置，改用真实视口补验后确认产品层级正常，无需调整z-index；另有目录和编辑页截图。根目录的临时审查副本用后删除。

## 检查结果与边界

- 新增2个Go顶层测试，组合执行含5个子场景；命名/整理/映射专项连续3轮、完整组合执行连续5轮通过。
- 相关15个Go包（其中animefeed无独立测试）的回归通过；曾遇既有 `Test302TakeoverLifecycle` 一次端口监听失败，单独重跑通过。随后完整重跑暴露Garden测试混合时钟：基础预览夹具冻结时刻，Garden使用真实观测时刻，执行跨过冻结时刻时被判未来数据；仅将该测试的预览时钟改为真实时钟，保持生产判断不变，Garden及新组合执行专项连续5轮通过，最终服务包完整重跑通过（102.930秒）。未修改反代监听或其他业务逻辑。
- 相关15包 `go vet`、全后端 `go build ./...` 通过。
- 前端72组交互、`vue-tsc -b`与Vite构建通过。既有libarchive浏览器外部化和构建包大小提示不影响本轮结果。
- 本机 `CGO_ENABLED=0` 且未找到C编译器，未运行race，不将其记为通过。
- SQLite仍为v52，没有迁移或开发库数据变更，没有真实115请求、云端改名、移动或删除；外部115写入及真实上游回执形态仍未做实站验证。

本轮不提前打卡P4：跨页面汇总状态、暂停/移除后的任务入口，以及整个模块的最终综合验收继续由P4-04～05负责。
