# MoviePilot 双语标题识别核对（2026-11-16）

## 范围与版本

本次只调查识别机制并验证两个失败样本，没有修改 Cinema115 的双语识别逻辑，没有运行整理任务或修改历史文件。

- MoviePilot v3：`8deb98aba8ddbde044e404213fe15162a71a4e34`。
- MoviePilot v2：`1528176beacc0f9f8d8d9cbf1adf048b0da67b5c`。
- v2、v3 的核心搜索机制均已阅读；实际执行的是 v3 官方 Python `MetaVideo` 路径及从官方源码提取的候选校验方法。
- 未启动完整 MoviePilot 服务，没有验证特定部署的 Rust 解析加速器、自定义识别词或转移流程。

## 机制

MP 的 `Tokens` 按点、空白、括号等分隔资源名，解析中文名、英文名、年份、季集与资源规格。识别依次尝试中文名、简体中文名、英文名，去空、去重，成功即停止。这不是把中文标题切为多个关键词后分别搜索。

候选仍需满足标题、原标题或详情中的别名/译名比较。比较会去标点、统一大小写，再判断完整名称是否相等。

MP 还存在去掉年份重试的回退；此项不建议直接移植，应保留 Cinema115 对年份、季号的约束，避免再次误认 Wonderland。

源码入口：

- [v2 搜索名称顺序](https://github.com/jxxghp/MoviePilot/blob/1528176beacc0f9f8d8d9cbf1adf048b0da67b5c/app/modules/themoviedb/__init__.py#L121-L128)
- [v2 按名称逐个识别](https://github.com/jxxghp/MoviePilot/blob/1528176beacc0f9f8d8d9cbf1adf048b0da67b5c/app/modules/themoviedb/__init__.py#L506-L517)
- [v3 资源名分隔](https://github.com/jxxghp/MoviePilot/blob/8deb98aba8ddbde044e404213fe15162a71a4e34/app/domain/tokens.py#L31)
- [v3 完整名称比较](https://github.com/jxxghp/MoviePilot/blob/8deb98aba8ddbde044e404213fe15162a71a4e34/app/modules/themoviedb/tmdbapi.py#L189-L206)
- [v3 季候选校验](https://github.com/jxxghp/MoviePilot/blob/8deb98aba8ddbde044e404213fe15162a71a4e34/app/modules/themoviedb/tmdbapi.py#L763-L807)
- [v3 前置方括号处理](https://github.com/jxxghp/MoviePilot/blob/8deb98aba8ddbde044e404213fe15162a71a4e34/app/domain/meta/metavideo.py#L231-L239)

## 实测

将原始文件名交给未修改标题解析逻辑的官方 `MetaVideo`，显式配置 `.strm` 后缀，禁用可选加速器。下载并加载原始依赖模块，使用临时 Python 虚拟环境，未改工作区依赖。

随后通过现网 Cinema115 TMDB 搜索接口验证英文查询；另向 TMDB 官方 API 读取真实 `alternative_titles,translations` 详情，将真实详情交给官方 `_season_candidate_matches` 及其名称比较依赖方法。测试过程没有把预期英文别名写入模拟响应。

| 原始样本 | MP 中文名 | MP 英文名 | 年份与集号 | TMDB | 官方候选方法 |
| --- | --- | --- | --- | --- | --- |
| `你好，星期六.Hello.Saturday.2022.S05E24.2160p.WEB-DL.SDR.HEVC.AAC2.0.strm` | `你好，星期六` | `Hello Saturday` | 2022 / S05E24 | TV 154770 | 通过 |
| `[你的名字我的姓氏].Once.Given.Never.Forgotten.2021.S01E40.WEB-DL.4K.EDR.HEVC.AAC-CMCTV.strm` | 空 | `Once Given Never Forgotten` | 2021 / S01E40 | TV 129804 | 通过 |

- 第一条优先使用中文名，整剧首播于 2022，S05 存在且为 2026 季；文件中的 2022 是整剧首播年，因此无需放弃年份限制。英文查询还返回一个无日期的其他作品，说明搜到结果仍需候选校验。
- 第二条的开头方括号内容被 MP Python 解析器当发布组去除。真实详情包含 `Once Given, Never Forgotten`，其去标点后的完整英文名与解析结果一致；整剧首播于 2021，S01 共 40 集。
- 两条的官方候选校验均为 `True`，目标季均存在。这证明上述核心识别路径可通过，不代表已在完整 MP 整理任务中执行成功。

诊断脚本及真实详情保存在本机临时目录 `C:\Users\jli45\AppData\Local\Temp\cinema115-moviepilot-probe-8deb98ab`，脚本入口 `probe.py`，使用其中 `venv\Scripts\python.exe -P -X utf8 probe.py` 执行。临时目录没有保存 API 密钥。

## Cinema115 的改进方向

当前失败点是把中英文拼为一个名称去校验，第二条还残留右方括号；即使 TMDB 已返回正确作品，也会在名称校验阶段被排除。

建议保留完整中文标题、完整英文标题及来源信息，先用完整中文名识别，失败后再尝试完整英文名。候选验证使用同一组完整标题，对标题、原标题、可信别名进行完整名称比较，继续验证类型、年份、季号。中文方括号标题应按上下文保护，不能照搬 MP 无条件删除前置括号的行为。

性能上应限制标题变体数量、去重、成功短路，并复用同一任务的搜索/详情/别名缓存，避免每个文件重复请求。本次未实现双语优化，因此不对其准确率提升幅度或性能开销作基准结论；实施后应增加中英混合、括号标题、同名异年及错误候选的回归验证，并比较整组文件的网络调用次数和耗时。
