# 动漫发布解析实现记录（P1-04）

日期：2026-10-18。

依据：[首版规则](anime-first-release-rules.md)、[蜜柑公开样本](anime-mikan-api.md)及[数据契约](anime-subscription-contract.md)。

## 实现范围

新增独立纯解析包 `backend/internal/animeparse`。不依赖 Emby、TMDB、其他追更服务、网络或数据库，不请求种子、图片或 115。P1-03 负责获取原始元数据，本项解释这些元数据；篇章归属、偏移、筛选、预览和写入仍由后续任务接入。

- `Title(title)` 返回现有 `model.AnimeReleaseParse`，对应 `ParserVersion = 1`。调用方保存发布时须同时保存完整原标题及该版本；JSON schema 版本与解析器版本分别维护。
- `Published(raw, alternate, observedAt)` 返回原始日期、同次观察的备用日期、状态、UTC Unix 毫秒、来源精度、时区解释和 `TimeVersion = 1`。读取时刻必须来自可信快照，不在解析器内调用当前时间。
- 未改动现有模型、JSON schema、SQLite 表或启动迁移。本步未装配业务 API、发布写入或页面，不创建任何下载决定。

## 集号与资源标签

识别带明确语法的来源编号：方括号/常见中文括号中的编号、`第30话`、`EP26`、`S02E01`、` - 06`，以及尾部只有画质等元数据的常见双位数编号。普通整数限 1～99999；保留前导零及原始写法，不把季号当来源集号。

| 输入片段 | 分类 | 普通集号 |
| --- | --- | --- |
| `Re Zero S2][26]` | episode | 26 |
| `S2][13]` | episode | 13，不继承其他组的偏移 |
| `[01v2]` | episode，版本标签为 v2 | 1，不排斥原版本 |
| `[12.5]`、`07A`、`06B V2`、`SP01`、`OVA 2`、样片 | special | 空 |
| `[01-12]`、`第01-第03话`、`S02E01-E03`、`[38+SP38]`、全集 | batch | 空 |
| `[59][3rd - 09]`、`[01][02]` | composite | 空 |
| 无可靠编号、零、超出范围 | unknown | 空 |

解析器收集全部编号证据并合并重叠位置，不优先选择第一个或最后一个数字。多个独立普通编号保留为 composite；合集、特殊和未知结果均不携带普通 `Episode`。`RawEpisode` 保存原始证据片段，多个片段以 ` / ` 分隔；证据超过模型上限时返回 unknown 和空集号，完整原标题仍由发布记录保留，不截出一个看似可信的数字。

裸数字中的年份（1900～2199）、常见分辨率/编码数字不作为集号；明确 `第2026话` 等语法仍允许合法长编号。不从年份、CRC、文件大小、编码或 v2 中推算集号；无法可靠解释时待确认。它是保守规则，不宣称覆盖所有字幕组命名。

画质仅使用明确标题标签：720p、1080p、2160p，4K 归为 2160p。无标记、冲突标记及不支持的标记（包括 1080i）返回 unknown；1080p/1080i 不会返回 1080p。不从 `1920x1080` 猜测逐行扫描，不声称检测过视频流。

版本标签独立保留 v2/rev3、修正、重发等，不择优、不替换、不按集号互斥。发布身份和已核验 BT info-hash 不由此解析器生成；字幕组仍以已确认的来源组 ID 为准。

## 时间解释

无偏移 ISO 日期及蜜柑 HTML 分钟日期按 IANA `Asia/Shanghai` 解释，嵌入 Go 时区数据以避免依赖机器设置。带偏移日期尊重其偏移，支持 RFC3339、常见数字偏移和 GMT/UTC 的 RSS 日期；CST 等含糊缩写、非法日期/偏移及过长小数秒不猜测解释。

输出精度分别保留到分钟、秒、百毫秒、十毫秒或毫秒；更细的小数秒原文保留，数据库时间投影到毫秒。主日期与同次观察的备用日期按各自精度区间核对，只有区间相交才可互相印证，较粗日期不替换主日期。主日期缺失时可解释备用字段，主日期损坏时不通过备用值掩盖错误。

状态为 `known/missing/unparseable/conflict/invalid/future`。只有 known 有 UTC 时间；不合理年代、超过本次实际读取时间五分钟、缺失、冲突及解析失败均无可执行发布时间，不回填 observedAt。恰好五分钟在允许范围内。

调用方应保留时间原文/备用原文及状态，仅把可信时间投影到 `AnimeRelease.PublishedAt` 并记录 `TimeVersion`。RSS 与 HTML 是不同观察，不能把 HTML 分钟时间覆盖 RSS 时间；跨观察合并及预览基线的使用在 P1-05/P1-06 接入，本项不自动保存或更新历史发布。

## 验证与审查

12 项常规顶层测试及标题模糊测试，覆盖普通/连续编号、特殊类型、复合/冲突、修正版、画质冲突、元数据误识别、UTF-8/控制字符/长度、输出模型校验、时区、精度、日期冲突及未来时间。提交的 `testdata/mikan_titles.json` 含 11 条 P0-02 公开标题原文，覆盖 Re:0 原季编号、季内编号、后半篇章、新编集 A/B、复合编号、跨篇章合集和混合 SP；测试离线执行，不依赖 ignored 的全页样本。

验证命令：

```powershell
go test ./internal/animeparse ./internal/model ./internal/storage ./internal/client/mikan ./internal/client/bangumi -count=1
go test ./internal/animeparse -run '^$' -fuzz '^FuzzTitleInvariants$' -fuzztime=15s -parallel=2
go vet ./internal/animeparse ./internal/model ./internal/storage
go test ./... -run '^$'
```

上述检查通过。模糊测试验证成功解析结果始终满足现有模型，并且只有 episode 类型具有普通集号。Codex 后端审查已修正画质冲突保留旧值、SPECIAL 前缀和中文特典边界等问题，并补充回归用例。本机 race 工具链限制维持原记录；本项无前端改动，无需 Gemini CLI 页面审查。

下一项：P1-05，篇章来源与编号范围确认。
