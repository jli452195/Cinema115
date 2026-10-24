# 筛选与首次历史预览（P1-06）

> Historical development record. Retired activation/history-selection flows and real test records were removed in v53. Current behavior: [configuration and RSS lifecycle](anime-subscription-dialog-plan.md).

日期：2026-10-18。依据[首版规则](anime-first-release-rules.md)和[预览契约第6节](anime-subscription-contract.md)。本项只实现后端预览，管理员 API 在 P1-07 装配。

## 规则与解释

新增纯计算包 `internal/anime/preview`，复用已有编号、时间解析与逐源篇章投影。处理顺序为实际来源/组、编号、篇章范围、持续下限、排除/包含词、标题画质、资源身份提示。保存经过的各步与最先阻止匹配的原因；未识别编号与范围待确认独立于被过滤项。

每条 RSS 独立使用自己的规则和编号基准。起始下限包含本集，不改变编号基准；例如25→篇章1、从30补，仍显示篇章6。同集不同原帖、画质及v2分别列出，不按集号或GUID合并。原帖标识不是已验证的 BT Hash，匹配项明确保留 `HASH_NOT_VERIFIED` 提示。

关键词先去首尾空白、按 ASCII 大小写归一、去重及排序；包含词全部满足，排除词任一个满足就优先排除。非 ASCII 字符按原文匹配，`*` 等为字面字符；各10词、每词64个Unicode字符，超限拒绝。画质只使用标题解析结果，未知/冲突及1080i不匹配明确1080p；选择any时不过滤画质。

## 快照、选择与时间

`AnimePreviewService.Generate` 一次只读取目标来源的累计历史，默认take=50。使用共享 MikanService 的真实组校验、缓存、访问间隔和3请求/45秒/正文预算，不逐行下载种子。组名来自已验证条目，账号/UID/目录来自本地设置，浏览器提交的对应文字不作为事实。

- `from_episode` 首次默认勾选已加载匹配发布；`new_only`、`selected_history` 首次选择为0，均可明确选择历史。
- 继续加载替换累计窗口，新增发布进入addedIds；已有选择只按稳定发布ID与确认指纹保留，不默认勾选后来加载的历史或新发布。
- 同内容的原选择为retained；内容、来源规则或映射变化为changed；本次没看到的旧选择为out_of_loaded_scope，不宣称上游删除。待复核集合保留原标题、编号解释和指纹，不能作为幽灵选择提交。
- 显式选择更新覆盖当前来源全部已加载资源，不受显示页限制。取消原选择保留dismissed；待复核项须明确重新选择或移出。`ValidateSelection` 在有未处理项时返回 `ErrReview`。
- 默认每页50、最大100，只做本地投影，不隐式抓取下一批；摘要返回原始/已加载/匹配/待确认/已选数量和待复核数量。

每源generatedAt起绝对15分钟，等于expiresAt即过期。继续加载、选择更新、复用进其他batch均不续期。显式重新生成可建立新有效期，但沿用旧源T0及首读已见集合；无成功读取时不生成previewId/T0。P1尚不消费或创建正式baseline。

T0采用实际observedAt并按首读中最粗的有效时间精度取整；空源/无有效日期时保守采用分钟精度。缓存命中沿用原observedAt，generatedAt与T0用途不同。后续未在首读集合中的发布区分：时间可信且>=T0为post_preview_new；较旧补录、缺日期、冲突及未来异常待明确确认。这一解释不会自行增加初批选择，持续调度在P2接入。

缺口只计算已加载匹配资源最小至最大来源编号之间的空档，并返回范围；不把未加载的较早集数、篇章总集数或整个站点历史当作已知缺集。`visible_end`仅接受结构可靠且rawCount<take的正常短历史响应，其他保留partial/具体stopReason。

## 持久化与并发

SQLite版本40在预览批次、单源预览各补一个JSON快照字段，保留现有12表。迁移先检查字段，允许已有旧版本结构检查重放；在同一迁移事务中完成。静态[完整DDL](anime-subscription-schema.sql)同步字段。

随后按用户明确要求清理废案，当前版本为41：删除先前保留的v38原型归档；尚未升级的已知原型直接清除，不再归档。新方案的配置/预览和其他功能数据保留，详见[旧数据清理记录](anime-storage-implementation.md)。

批次保存规范化配置，单源保存读取窗口/选择，逐发布行保存不可变解释。释放发布观测写入的事务辅助函数，发布稳定ID与预览快照原子提交，避免单连接嵌套事务；即使全局发布随后更新，旧预览仍读取自己的观测快照。

存取按可信actor及篇章/订阅隔离；未保存表单使用稳定draftKey。总指纹含全部配置、订阅revision、设置/目录和引擎版本；每源指纹只含本源依赖，不受其他草稿规则编辑影响。复用时校验归属、当前本地版本、源指纹与有效期，复制到新批次并返回新的预览ID，原绝对期限保持。

加载/选择使用expectedLoadRevision，在事务中再次核对配置版本、期限和CAS，成功递增。网络在事务外执行；较迟返回的请求不能覆盖另一窗口的新选择。失败完整回滚，保留原快照和其他源。账号/目录、订阅或来源版本变化会拒绝旧操作。

一个batch最多2000原始行，包含过滤、待确认及去重前重复行；累计展开替换原来源计数。服务先检查剩余预算，事务提交再核对总数。响应固定带budget与逐源摘要，行数预算和请求/正文预算使用不同issue，避免误报达到2000条。

某源失败时可复用其他有效来源；已有有效目标预览也保留原期限和选择。从未成功与正常空源分别返回。某源过期不阻止生成其他源；过期快照仍可读取以恢复输入。管理员HTTP鉴权及保存表单选择由P1-07接入，当前没有浏览器入口。

## 验证与自审

新增23个顶层测试：纯规则8、存储8、服务7。覆盖关键词与指纹归一、排除优先、包含起始集、同集共存、三种首次模式、时间精度/边界、已加载缺口、跨页选择、旧选择缺项/变更/恢复、取消保持、重启恢复、actor/篇章隔离、CAS单胜、迟到响应、共享原始行预算、失败回滚、v39升级、缓存和部分来源失败。

用户追加旧版数据清理后，另新增1个归档清理回归，更新旧结构升级/失败回滚测试，验证新版配置和其他业务保留；本轮合计新增24个顶层测试。

检查命令：

```powershell
go test ./internal/anime/preview ./internal/anime/binding ./internal/animeparse ./internal/client/mikan ./internal/client/bangumi ./internal/model ./internal/storage ./internal/service -count=1
go vet ./internal/anime/preview ./internal/anime/binding ./internal/animeparse ./internal/client/mikan ./internal/model ./internal/storage ./internal/service
go test ./... -run '^$'
py -3 docs/examples/anime-contract/validate.py
```

自审修正了输入深复制、可靠末尾校验、旧选择快照、来源顺序稳定与迁移重复加列问题。验证使用模拟上游、临时SQLite及内存DDL，不访问真实115。没有创建baseline、执行selected决定、Hash claim、下载任务或整理操作；没有前端改动，本项无需Gemini CLI页面审查。race工具链限制维持此前记录。

下一项：P1-07，管理员配置API。
