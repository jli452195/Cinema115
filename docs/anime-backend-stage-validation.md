# 动漫订阅后端阶段验证（P1-11）

验证日期：2026-10-19。结论：P1-11 完成，下一项为 P1-12 前端审查与阶段验收。

本次验证覆盖当前 SQLite v45、三个站点来源、配置与预览 API，以及提前完成的订阅独立 TMDB 命名快照。后端由 Codex 设计和审查；没有前端改动。本阶段仍为 `configure=true, download=false, organize=false`，下载闭环和云端整理分别在 P2、P3 实现。

## 验收覆盖

| 验收项 | 已执行的代表性回归 | 结果 |
| --- | --- | --- |
| 解析与版本共存 | `TestTitleOrdinaryNumbering`、`TestTitleUnsupportedAndAmbiguousNumbering`、`TestTitleVersionsAreIndependentReleases`、`TestSpecificationSilverBulletRealTitles`；标题模糊测试 | 普通集、SP、合集、画质、修正版及不确定编号保持区分，未建立分集独占 |
| 来源范围与独立偏移 | `TestProjectPerPublisherNumbering`、`TestProjectRejectsInvalidData`、`TestAnimeBindingMissingScopeAndIndependentGroups`、`TestAnimeNamingAtomicSaveReplayAndOfflineLocalEdit` | 下限、编号基准和最终 TMDB 季集分别保存，不继承其他来源偏移 |
| 草稿与暂停 | `TestAnimeAdminPausedReadinessAndDraftStandby`、`TestAnimeAdminCompletesDraftSourceIdentityOnce`、`TestAnimeAdminSourceEffectiveStatesPreserveIntent` | 不完整配置不能冒充就绪；备用草稿与来源暂停意图保留；已选身份不可直接改绑 |
| 版本、幂等与事务 | `TestAnimeConfigCASAndIdempotency`、`TestAnimeAdminAtomicChapterAndReceipt`、`TestAnimeAdminConcurrentSameKeyReturnsOriginalReceipt`、`TestAnimeAdminSettingsReadbackReplayAndAccountChange` | 父级、来源、目录及主账号版本校验有效；同键重放保持原回执；失败无部分提交 |
| 历史与预览 | `TestAnimeMikanContinuationBoundaries`、`TestAnimePreviewServiceLoadAndSelectAcrossPages`、`TestAnimePreviewBudgetAndFailureRollback`、`TestAnimePreviewServiceLateResponseCannotOverwriteChoices` | 有界累计历史、原始行预算、跨页选择及 CAS 有效，迟到加载不覆盖选择，新发布不自动扩大初批 |
| 过期、隔离与恢复 | `TestAnimePreviewPersistenceAndNoExecution`、`TestAnimePreviewExpiryReuseAndScope`、`TestAnimeAdminSavesExpiredPreviewHintsWithoutConsuming` | 重启保留预览与输入；管理员/篇章/来源隔离有效；绝对期限和 T0 不随保存续期 |
| 三站与持久缓存 | `TestAnibtProviderPreviewAndSave`、`TestGardenProviderPreviewAndSave`、`TestAnimeCatalogProvidersPersistObservedLinksWithoutExtraReads`、`TestAnimeCatalogCrossSiteStatusSurvivesUpgradeAndRestart` | 本站读取与来源身份保留，跨站状态仅用已核实关联，升级和重启保留配置，无逐卡补抓 |
| API 与阶段边界 | `TestAnimeHTTPAuthBeforeAnyBusiness`、`TestAnimeHTTPStrictBoundaryAndSafeErrors`、`TestAnimeHTTPConfigReceiptAndLocalReads`、`TestAnimeCatalogStatusIsLocalAndAdminOnly` | 鉴权先行、严格输入、安全错误及本地读取有效；启用、下载重试和整理仍由服务端拒绝 |

上述测试分布在 `backend/internal/animeparse`、`anime/binding`、`anime/preview`、`model`、`storage`、`service`、`httpapi` 与相关客户端包，不仅是对文档样例的静态核对。

## 新增组合回归

新增 [anime_stage_test.go](../backend/internal/service/anime_stage_test.go) 的 `TestAnimeStageMixedProvidersSaveAndRestart`：

1. 在临时 SQLite 中固定合成主账号与功能目录，建立一个主源和两个备用源，分别来自蜜柑、AniBT、Garden。
2. 来源下限分别为 30、2、1，分别映射到同一 TMDB `S02E01`；合并三源预览，共 7 条原始观测，各源独立选择后保存暂停配置。
3. 关闭数据库，重新打开并重建服务及全部站点缓存。让全部模拟上游不可用，核对来源、命名快照、选择、预览期限和幂等回执不变。
4. 离线修改主源下限，确认备用源下限与三源编号映射保持独立；旧版本保存被拒绝，本地列表/详情继续可用，所有上游读取计数为零。
5. 检查首次启用时间、来源基线与检查时间仍为空，任务及执行选择统计为零。表单选择不会变为下载授权。

来源样本故意使用不同作品的既有测试夹具，只验证人工确认后的系统组合，不声称这些作品在真实站点上属于同一 Bangumi。

初次构造夹具时修正了新数据库的账号存储初始版本和预览所需的显示名称；重复运行又发现蜜柑单源夹具的固定时钟可能早于另一个站点的实际读取时间。组合测试改用实时时钟后，相关服务回归连续 20 轮通过。这些调整只涉及新增测试，没有修改线上业务代码或数据库结构。

## 检查与结果

环境：Windows/amd64，Go 1.27.1，默认 `CGO_ENABLED=0`，Python 验证使用 SQLite 3.50.4。

相关包完整回归通过：14 个有测试的包，1095 个顶层测试/模糊测试入口通过；包含子测试与种子共 2333 项通过。`animefeed` 无独立测试文件，其身份校验由调用包测试覆盖。该统计包括共享包的既有其他业务回归，不能视为新增动漫测试数量。耗时约为 service 94.9 秒、HTTP 34.2 秒、storage 5.5 秒。

15 项既有测试跳过：13 项需要显式启用的实站/凭据探针，以及 2 项已标记停用的旧目录监控流程测试。没有动漫配置阶段测试因失败或缺少数据被跳过，没有启用真实写入探针。

```powershell
cd backend

go test ./internal/animeparse ./internal/anime/binding ./internal/anime/preview ./internal/client/animefeed ./internal/client/bangumi ./internal/client/mikan ./internal/client/anibt ./internal/client/garden ./internal/client/tmdb ./internal/client/imagecache ./internal/client/p115 ./internal/model ./internal/storage ./internal/service ./internal/httpapi -count=1

go vet ./internal/animeparse ./internal/anime/binding ./internal/anime/preview ./internal/client/animefeed ./internal/client/bangumi ./internal/client/mikan ./internal/client/anibt ./internal/client/garden ./internal/client/tmdb ./internal/client/imagecache ./internal/client/p115 ./internal/model ./internal/storage ./internal/service ./internal/httpapi

go build ./...

go test ./internal/service -run '^(TestAnimeStageMixedProvidersSaveAndRestart|TestAnimeAdminConcurrentSameKeyReturnsOriginalReceipt|TestAnimePreviewServiceLateResponseCannotOverwriteChoices|TestAnimeMikanConcurrentMergeAndWaiterCancellation)$' -count=20

go test ./internal/storage -run '^(TestAnimePreviewCASSelectionAndLoad|TestAnimeConfigCASAndIdempotency)$' -count=20

go test ./internal/animeparse -run '^$' -fuzz '^FuzzTitleInvariants$' -fuzztime=5s -parallel=2

cd ..
py -3 -X utf8 docs/examples/anime-contract/validate.py
git diff --check
```

结果：完整相关包回归、vet、全后端编译通过；6 项并发/恢复/版本测试各重复 20 轮通过；模糊测试运行约 6 秒、21608 次输入通过；契约内存数据库的 24 表、15 项约束反例及完整性验证通过。组合测试时钟修正后另执行 service vet 通过。

竞争检测未通过工具链准备，不能记为已完成：默认执行 `go test -race` 提示需要 CGO；在命令进程中设置 `CGO_ENABLED=1` 后，编译报 `C compiler "gcc" not found`。重复并发测试不能替代 race 检测，后续可在具备 C 编译器的环境补跑。

契约脚本只验证 `:memory:` 中的 P0 设计 DDL；实际 v45 迁移、事务与重开由 Go 存储测试验证，不将契约的未来下载/整理表计为已实现。

本次未访问真实上游、保存真实订阅、修改真实功能目录或执行 115 写入；未接入 Emby。P1-12 的实际页面代码、屏幕尺寸、键盘/触屏、返回缓存与失败输入保留仍待下一项验收，不在本项提前打卡。

## 用户样本回归：Season 季号与集号（2026-10-19）

用户截图补充了既有测试未覆盖的 ANi 标题 `Season 2 - 01/02`。实际分别是第 1、2 话，旧解析器将季号纳入 `2 - 01/02` 的合集范围，预览因此显示“编号需确认”。修复保留词边界完整的 `Season N` 为元数据、独立解析后面的集号，并升级 `ParserVersion=4`；不从发布季号覆盖已配置的 TMDB 目标季，不放宽合集或复合编号的确认要求。详见[解析修复记录](anime-release-parser-implementation.md)。

修复前用截图两条标题复现失败；修复后新增 11 条解析样例及 1 项 AniBT 规格匹配预览回归通过，确认普通第 1、2 话可选、真实 `01-12` 合集仍需确认。重新执行 animeparse、anime/binding、anime/preview、model、storage、service、httpapi 七个包的完整测试、对应 vet 和 `go build ./...` 均通过（service 95.9 秒、HTTP 35.8 秒）；5 秒标题模糊测试通过，共 26429 次输入。

仅调整后端解析和回归测试，无 UI、数据库迁移、真实订阅或下载写入。已有预览快照不原地变更，点击“重新预览”后按新规则生成结果。该修复不增加任务数量，下一项仍为 P1-12。
