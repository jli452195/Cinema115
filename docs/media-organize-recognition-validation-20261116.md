# 整理作品识别准确性与性能验证（2026-11-16）

## 修改范围

- 纯 `S`/`s` 加星号的目录不再遮住上层作品 TMDB ID，不推断季号，不放开独立作品或合集边界。
- 明确季号不再无条件忽略年份；依次采用作品首播年、指定季首播年、指定季分集年份作为作品识别证据。
- 同候选同季补查结果在一次识别内复用，每次最多补查 4 个候选季，沿用 TMDB 服务缓存。详情与季查询固定同一编号方案。
- 父目录的明确年份不会因中英文片名尚未核实而丢弃；另一季度副标题的年份不覆盖文件明确季号。
- 缺资料与年份不符提供具体原因；真实查询错误继续按临时异常处理。已选作品、成功历史和实际文件不自动纠正。

## 准确性验证

固定元数据回归覆盖：原始 `S*******` 路径、ID 冲突、合集与未知作品边界、文件年份优先、跨语言目录年份、备用数字片名年份、后续季与第一季跨年、特别篇跨年、其他季年份不能替代当前季、404/空季/空日期/季号不符、超时与取消、查询上限及编号方案变化。

批量测试验证：

- 289 条 `五十公里桃花坞 (2021) {tmdb-126398}/S*******/Wonderland.2021.S00Exxx...strm` 归并为一组，匹配 `tv:126398`；识别阶段仅 1 次详情，0 次搜索，0 次季补查。
- 289 条无 ID 的跨年特别篇归并为一组，识别阶段共 1 次搜索、1 次详情、1 次季补查；元数据仅有 1 条单集也不限制文件集号。
- 临时本地文件系统实际移动两个 STRM，验证目标作品、季号、命名及正文原样保留。Windows 不允许创建星号目录，因此仅该文件系统子场景跳过；精确星号原路径由内存扫描与 289 条分组测试覆盖。

相关回归命令：

```powershell
go test ./internal/service ./internal/mediafacts ./internal/releaseparse ./internal/httpapi ./internal/storage -count=1
go test ./internal/mediafacts -count=1
go test ./internal/service -run '^(TestMediaRecognition|TestMediaOrganizerWonderland)' -count=1
go vet ./internal/service ./internal/mediafacts ./internal/httpapi
go build ./cmd/... ./internal/...
```

上述相关包完整回归、专项准确性测试、静态检查和实际应用包构建均通过。首次完整回归中的本地监控用例 `TestMediaLocalMonitorFullCacheStillRecordsExistingWrites` 曾发生 4 秒等待超时；后续完整回归通过，未改动该监控用例或生产逻辑。最终目录年份副标题收紧与年份扫描合并后，再次通过全部 `TestMedia*`、解析专项回归和静态检查。

## 性能对照

环境：Windows amd64、Ryzen 7 5700X3D；固定内存元数据，无网络访问。使用相同新增基准，旧版生产源码从 `6472b4c` 通过 Go overlay 读取，不切换或覆盖工作区。每项运行 3 次，以下为中位值。实际网络延迟与 NAS 写入耗时不在这些 CPU 基准范围内。

| 场景 | 修改前 | 修改后 | 内存分配次数（前 → 后） |
| --- | ---: | ---: | ---: |
| 明确 ID 匹配 | 57.5 ns | 57.8 ns | 2 → 2 |
| 首播年份匹配 | 2.36 μs | 2.36 μs | 25 → 25 |
| 对应季首播年份匹配 | 2.61 μs | 2.70 μs | 25 → 25 |
| 普通季跨年 | 2.62 μs | 3.18 μs | 25 → 29 |
| 特别篇跨年 | 2.65 μs | 3.69 μs | 25 → 29 |
| 289 文件分组与识别 | 3.85 ms | 3.69 ms | 2474 → 1846 |
| 星号季目录上下文 | 148.2 μs | 114.9 μs | 90 → 69 |
| 标准季目录上下文 | 117.7 μs | 114.3 μs | 76 → 76 |
| 剧集文件名事实提取 | 28.6 μs | 29.5 μs | 30 → 30 |
| 电影文件名事实提取 | 26.6 μs | 25.6 μs | 26 → 26 |

普通识别的分配与请求数量保持一致。跨年场景新增一次有必要的季资料核验，所列 CPU 增量不含该请求的网络耗时。289 文件旧版会执行 1 次搜索并误选 2013 年作品，新版不搜索且准确继承 ID；不同轮次整批时间存在波动，不把个位数百分比的 CPU 差异视为显著提升。星号目录解析约减少 22% 耗时；289 文件分配次数约减少 25%。

文件名路径另外验证了年份提取开销：新增强年份标记最初造成二次扫描，已合并成一次扫描，剧集文件名事实提取分配次数恢复为原来的 30 次。上述分组基准不包含预先提取文件名事实的耗时，因此单独列出该项，避免遗漏逐文件成本。

基准命令（在 `backend` 目录）：

```powershell
go test -vet=off ./internal/service ./internal/mediafacts -run '^$' -bench 'Benchmark(MediaRecognitionYearEvidence|MediaRecognitionFilenameFacts|MediaRecognitionWonderlandBatch|DirectoryContext)$' -benchmem -benchtime=300ms -count=3
```

## 范围限制

季级日期证据只说明指定季存在该年份内容，不宣称核对了具体文件的分集日期，也不以 TMDB 集数限制文件编号。以上为第一轮年份优化记录；后续双语与无年份策略见下文。

`go build ./...` 会包含工作区现有 `backend/tmp` 下不完整的历史源码副本，因重复入口或缺少依赖而失败；实际应用包的构建使用上面的 `./cmd/... ./internal/...`。本次不删除或修改这些临时资料。

本次没有部署，也没有重跑线上整理或移动历史误归类文件。

## 后续双语与无年份优化

- 完整中英文标题分别提取和搜索，两条真实失败文件正确匹配 TV 154770、TV 129804，继续保留原年份与 S05E24、S01E40。
- 无有效年份时核验当前精确匹配层的唯一性，不自动接受模糊结果；明确季号须存在。同名歧义、元数据不足和核验达到预算均不猜测作品。
- 独立审查使用同一份外部 Go overlay 复测，确认弱目录年份弃用、数字片名备用解释、类型未知的 ID、别名截断这四种绕过路径均已修复。
- 固定成功样本表为 `backend/internal/service/testdata/media_organize_regression.csv`，初始 8 条、4 列；两条来自当前成功历史，其余为既有正确识别和本次适配代表。失败边界不塞入表内。测试读取整表，验证实际分组、JSON 恢复、匹配与命名；另从同表选取 3 条双语/无年份样本，在临时目录实际整理 STRM 并核对正文不变。
- 根 `AGENTS.md` 已约定后续相关修改追加代表样本并重跑整表，同类文件不逐集重复，不保存候选、历史或测试日志到样本表。

性能使用固定内存元数据、300ms、3 轮中位值，不含网络和 NAS I/O：无年份唯一候选约 1.93μs（1 搜索、1 详情），两个同名候选约 2.71μs（1 搜索、2 详情），均不补查季接口；明确 ID 约 59ns，仍为 0 搜索、1 详情。40 文件的双语综艺与括号双语剧分组识别约 0.99ms、1.24ms，均仅 1 搜索、1 详情；289 文件桃花坞仍为 0 搜索、1 详情，约 3.64ms。分组基准预先提取文件事实，不将这些 CPU 时间当作实际远端整理耗时。普通有年份路径本轮约 2.77μs、26 次分配，较第一轮有少量固定 CPU 和内存开销，没有增加网络调用。

专项命令（在 `backend` 目录）：

```powershell
go test ./internal/service -run '^(TestMediaOrganizeRegression|TestMediaRecognition)' -count=1
go test ./internal/service ./internal/mediafacts -run '^$' -bench 'Benchmark(MediaRecognitionUndated|MediaRecognitionYearEvidence|MediaRecognitionWonderlandBatch|MediaRecognitionBilingualBatch|MediaRecognitionFilenameFacts|ParseIdentityBilingual)$' -benchmem -benchtime=300ms -count=3
```

最终完整 service 回归通过；mediafacts、releaseparse、naming、recognition、httpapi、storage 全包通过，相关包 `go vet` 和 `go build ./cmd/... ./internal/...` 通过。集成时补全了原成功测试省略的季列表，不放宽生产校验。另修正旧 `TestMediaOrganizerGrayRun` 的取样竞态：先读取后台活动状态再读取最终快照，保留原断言，连续 20 次及后续完整 service 回归均通过。新策略未部署，未重跑实际 NAS 整理或改动历史文件。
