# 动漫订阅：蜜柑资源与历史验证（P0-02）

验证日期：2026-10-18，17:14～17:20（Asia/Shanghai）。对应[实施任务表](anime-subscription-tasks.md)的 P0-02，承接 [Bangumi 接口验证](anime-bangumi-api.md)。本项核实来源能力，不实现业务接口或页面。

后续客户端已在 P1-03 实现，见[蜜柑RSS与历史读取记录](anime-mikan-implementation.md)；本报告保留原始实站观察，不将模拟测试补写成实站结论。

## 验证范围与结论

共 20 次公开 GET 请求，不携带 Cookie 或 Authorization，不跟随重定向。19 次读取并解析完整正文，合计 2,946,054 字节；另一次宽泛搜索在超过本次自定 4 MiB 正文预算时中止，明确记录为未读完整。没有下载图片、种子、视频或调用 115。

固定主机为 `https://mikanani.me`，User-Agent 为 `Cinema115-MikanP0Probe/1.0 (https://github.com/jli452195/Cinema115)`；顺序请求，完成上次请求后至少间隔 1.2 秒。间隔与大小限制是本次验证预算，不是上游公布限额。

已经确认：

- 页面提供按番剧和字幕组限定的 RSS；历史展开按累计数量读取，不是 offset 分页。
- 同一个条目可出现不同编号方式，以及超出页面所标篇章的发布。条目标题或 Bangumi 外链不能替代范围确认。
- RSS GUID 在真实样本中重复且对应不同资源，不能单独作为发布唯一键；样本 RSS 只有种子链接，没有磁力。
- RSS 可以包含多年历史，但没有已验证的完整性标志或分页契约，不能由本次返回推断“全集已齐”。

这些是上游当前观察，后续客户端仍需限制响应大小、校验页面/XML 结构，并将来源失败、零结果、未读完整分别处理。

## 请求约定与实际样本

| 用途 | 方法与路径 | 已观察行为 |
| --- | --- | --- |
| 搜索 | `GET /Home/Search?searchstr=关键词` | HTML；可选 `subgroupid` 限定发布组，候选卡片与资源都要保留原始身份 |
| 番剧页 | `GET /Home/Bangumi/{id}` | HTML；名称、Bangumi 外链、发布组 ID/名称、RSS 地址及各组首批资源 |
| 历史展开 | `GET /Home/ExpandEpisodeTable?bangumiId=2259&subtitleGroupId=45&take=65` | 返回该组累计前 N 条的 HTML 表格片段，不带总数或显式 hasMore |
| 番剧 RSS | `GET /RSS/Bangumi?bangumiId=2259` | XML；本次 175 条，跨所有组 |
| 单组 RSS | `GET /RSS/Bangumi?bangumiId=2259&subgroupid=45` | XML；本次 20 条，仅在已确认的番剧与组范围内使用 |

RSS 参数为 `subgroupid`，历史展开为 `subtitleGroupId`，不能混用。首版由服务端根据已校验 ID 构造固定地址，不接收任意订阅 URL，不执行页面脚本或跟随广告链接。

### 篇章与版本

| 蜜柑 ID | 蜜柑原名 | 页面声明的 Bangumi ID | 本次首屏资源 / 组数 |
| --- | --- | --- | --- |
| 701 | Re：从零开始的异世界生活 | 140001 | 121 / 13 |
| 2153 | Re：从零开始的异世界生活 新编集版 | 296195 | 20 / 4 |
| 2259 | Re：从零开始的异世界生活 第二季 | 278826 | 126 / 14 |
| 2348 | Re：从零开始的异世界生活 第二季 后半部分 | 316247 | 70 / 9 |
| 3464 | Re：从零开始的异世界生活 第三季 袭击篇 | 425998 | 117 / 12 |

首屏数量是发布行数，既不是集数，也不是文件数；多画质、多字幕、SP、合集会产生多条发布。页面同名、同系列和外链只能提供候选依据，不能据此自动合并。

发布组通过页面 `subgroup-name[data-anchor]`、`subgroup-text[id]` 和资源所处表格关联。例如 2259 同时含 `28=DHR动研字幕组`、`45=爱恋字幕社`、`554=百冬练习组`、`1254=7³ACG`。以数字 ID 关联，不从标题里的方括号猜组；联合发布组名称也不拆分为多个独立订阅组。

本次有资源页面使用 `js-expand-episode` 提供下一次请求参数。例如 2259 的 45、522、554 组均给出 `data-take=65`，首屏各展示 15 条。并非每组都固定 15 条，不存在更多时可直接显示该组全部当前结果。

## RSS 字段与身份

实际 XML 结构为 RSS 2.0，条目字段包含 `title/guid/link/description/enclosure`，以及命名空间 `https://mikanani.me/0.1/` 下的 `torrent`：

```xml
<item>
  <guid isPermaLink="false">发布标题</guid>
  <link>https://mikanani.me/Home/Episode/资源标识</link>
  <title>发布标题</title>
  <description>发布标题和大小</description>
  <torrent xmlns="https://mikanani.me/0.1/">
    <link>https://mikanani.me/Home/Episode/资源标识</link>
    <contentLength>6560562688</contentLength>
    <pubDate>2020-11-04T13:19:24.888</pubDate>
  </torrent>
  <enclosure type="application/x-bittorrent" length="6560562688"
    url="https://mikanani.me/Download/20201104/e5189fd0bda5e2fbfb1a0e208c0b7e77edda56cb.torrent" />
</item>
```

这段展示字段结构；标题和 Episode 路径已缩写。日期、大小及 enclosure 来自实际条目。`contentLength/length` 表示资源内容大小，不能当作种子元数据的下载长度；具体文件仍以 115 核对为准。

实际样本没有 item 顶层 `pubDate`，时间位于 `torrent/pubDate`；有小数秒且无时区。保留原始时间，不擅自补 Z 当作 UTC，也不能用第一次发现时间代替发布时间。HTML 显示时间只有分钟精度，本次同一资源还出现页面与 RSS 时间不同的情况，不作为不可变的去重身份。

### GUID 碰撞实证

2259 全组 RSS 中，下面这个 GUID 文本与标题相同：

```text
【动漫国&KNA&轻之国度字幕组】★07月新番[Re:从零开始的异世界生活][26][1080P][繁体][MP4]
```

它出现两次，分别指向 Episode：

```text
012b2633be7ce764e54de56a06dc0b8381843f82
125bb450747e00a96ed3229242f9a5422249379d
```

因此 GUID 与原标题只保存为来源原文，不能对 `(来源, GUID)` 建不允许碰撞的唯一约束，也不能因 GUID 相同就覆盖记录。发布身份与下载资源身份分层：保留受控来源的原帖标识，使用已核验 BT info-hash 防止轮询反复提交同资源。按 P0-04 中用户补充的共存要求，不按集号排斥其他 Hash 或字幕组版本；分集身份只用于归属和命名。最终 DDL 在 P0-05 确定。

本次 RSS 无磁力字段；HTML 行提供磁力，且 Episode 路径带 40 位十六进制标识。路径及种子文件名中的 Hash 只是可核对线索，不能把任意 URL 文件名直接升级为已验证 info-hash。后续优先复用已缓存网页中的有效磁力与身份对应；只有种子链接且无法交叉确认时，按受控来源取得种子元数据并计算实际 info-hash，不能下载视频内容。本项没有取得种子，不能声称已验证其内部 info-hash。

### RSS 的历史与空结果边界

- 2259 全组 RSS 本次包含 175 条，原始发布时间从 `2020-07-09T00:37:00` 到 `2026-07-24T17:43:53.432835`，说明它可以返回多年历史，并非只给最近几天。
- 2259/45 组 RSS 返回 20 条，资源链接集合与 `take=65` 展开得到的 20 条一致；本次该组可见历史得到交叉确认。2259/1254 组返回 2 条，均为 26–50 话合集。
- 没有发现可用的 next/offset 或总数/完整标志；本次未测出全站 RSS 上限，不宣称无限历史或固定条数上限。
- 请求 2259 搭配未在该页列出的组号 `999999999` 仍返回 HTTP 200、有效 XML、0 条，频道名为泛化的“番组”。因此“XML 正常且为空”不能证明绑定有效，需先核实组属于所选条目。

预览可以展示本次实际读取的发布，但只有可核对的历史边界才能说明“当前来源读取至末尾”。它仍不证明每一集有资源，更不证明已下载。

## 历史展开与分页边界

| 番剧 / 组 | 请求 | 实际行数 | 交叉核对 |
| --- | --- | --- | --- |
| 2259 / 45 | 首屏 | 15 | 页面提供下一次 `take=65` |
| 2259 / 45 | `take=15` | 15 | 与首屏该组相同顺序、相同 Episode 路径 |
| 2259 / 45 | `take=65` | 20 | 前 15 条与 `take=15` 相同，新增 5 条；不是新增 20 条 |
| 2259 / 28 | `take=8` | 8 | 恰好等于请求数，本次仍无法仅据此确定末尾 |
| 2259 / 28 | `take=9` | 8 | 与 `take=8` 正文 SHA-256 相同，确认这次观察只有 8 条 |

展开响应是 HTML 表格片段，没有字幕组标题、组 ID、下一页链接或显式总数。组归属应由请求的固定番剧/组上下文赋予，不能依赖 fragment 自带元数据，也不能默认附给当前页面后来选中的组。

现有客户端用 `rowCount >= take` 提示继续获取，并按 `take+50` 增长；这是本地保守推断。`take=8` 满页并不证明还有第 9 条；只有可靠解析且行数小于 take，才能说明该次可见列表未取满。不能按有效资源去重后的数量判断末尾，解析失败或坏行也不能被当作已到末尾。

首版继续复用累计展开。刷新展示时替换对应组并按稳定资源身份合并，持久发布记录和已选预览快照另行保留，不把累计返回直接追加造成重复。上游新增发布可能改变前 N 条；请求预算到达、无进展、列表变化及边界不明时都保留“历史尚未读完整”，不能静默扩大初批选择或反复请求。

现有项目接受 `take=1～10000`，10000 是本地保护上限，本项没有向站点请求 10000 条，也没验证上游最大值。到达保护上限仍取满时应停在未读完整，不能继续透传本地计算出的 10050；最终初始预算与继续读取契约在 P0-04、P0-05 固定。

## 解析与人工确认样本

| 情况 | 实际来源与标题片段 | 处理边界 |
| --- | --- | --- |
| 原版 / 新编集 | 701/1254：`S01 \| 01-25`；2153/202：`新编集版 ... 1-13` | 不按固定偏移合并；保留独立版本 |
| 编号后缀 | 2153/552：`... 新编集版 ... - 07A`、`... - 06B` | 原文保存，不能剥掉 A/B 当普通整数集；首版待确认 |
| 同篇章不同编号 | 2259/28：`... 第二季_Re Zero S2][26]`；2259/554：`... S2】[13]` | 对应关系按字幕组保存，不使用全局统一偏移 |
| 复合编号 | 3464/1248：`...[59][3rd - 09]...` | 不盲取第一个或最后一个数字，确认采用的来源编号体系 |
| 跨篇章混入 | 3464 标为“袭击篇”，636 组标题含 `第三季 反击篇 ... [59-66]` | 条目名称和外链不保证每条发布归属；确认范围，范围外不下载 |
| 跨篇章合集 | 2259/1254：`S01 \| 26-50`；3464/1254：`S01 \| 51-66` | 不按单集处理，首版保留候选，不自动拆集或重封装 |
| 正片与 SP 混合 | 2259/522：`[26-38+SP26-38(前半全集)]`、`[33-34+SP33-34]` | 不提取其中一个数字当本篇单集 |
| 后半仍沿用季内编号 | 2348/45：`S2 14-25`；554 组单条标题含 `[18]`、`[19]` | 不能默认后半篇从 1 起，也不能从最小已见编号推断基准 |

原始完整标题、组和 Episode 标识保存在本地样本中。P1 解析器测试需同时覆盖普通单集与上述拒绝/待确认情况；本项尚未实现解析器，不能把列出规则当作代码已通过。

### 没有找到来源

使用 P0-01 已返回的真实 Bangumi 关联条目 `310194`：中文名 `Re：从零开始的休息时间2`，原名 `Re:ゼロから始める休憩時間 2nd Season`。分别搜索这两个完整名称，本次均返回正常零结果：搜索框存在、明确“找不到对应结果”标记、无结果表、无推荐条目。

另外保留受控零结果词 `cinema115_p0_no_source_b8f2c05e`，验证同样空页结构。以上只证明这些查询当时未找到候选，不证明整站或全网没有资源。处理为“未找到来源”，允许手动改词或保存待选资源草稿；不自动绑定同系列本篇、从 SP 标题猜篇章或切换其他来源。

宽泛词 `从零开始的异世界生活` 未加组筛选时，正文超过本次 4 MiB 预算，记录为未完成读取；加 `subgroupid=28` 返回 16 条及 2 个候选，加 `1254` 返回 6 条及 3 个候选。这些是不同查询范围，不能用缩小范围的结果冒充完整搜索，也不能把超限当作零资源。项目当前网页客户端上限是 16 MiB，不能把探测的 4 MiB 误写为线上限制。

## 复用范围与后续约定

已有 `backend/internal/client/mikan` 支持安全地址构造、网页/fragment 解析、磁力规范化及番剧展开；`backend/internal/service/mikan.go` 与 `mikan_bangumi.go` 提供缓存、进行中请求合并及全局访问间隔。继续复用这些边界，RSS 接入也须共享协调，不能另起无限并发客户端。

目前没有 RSS XML 读取器，也没有对外输出 Bangumi 外链关联字段。P1 分别补充有界 XML 解码、来源字段与记录身份；直接将 `mikan.Bangumi` 类型当成 bgm.tv 客户端不成立，它表示蜜柑番剧。RSS 解析不请求外部 XML 资源，不执行 description HTML，不跟随 enclosure 自动下载；需要核验种子时走单独受控动作。

本次完整页面返回 `Cache-Control: no-cache, no-store`，展开与 RSS 未见可用 ETag/Last-Modified，未发送条件读取；没有验证“蜜柑一定支持 304”。继续复用项目已有的有界应用资料缓存与请求合并，失败保留当前有效内容，不缓存成正常空结果。

本次未主动触发 403/429/5xx、验证码、网络超时及损坏 XML。现有网页客户端对此已有部分处理；RSS 的错误分类、取消、预算、退避和部分历史恢复仍需 P1 模拟上游验证，不记为本项实站通过。

## 可复现记录与检查结果

本地脚本、原始公开正文及字段投影分别在 `tests/anime-mikan/probe.py`、`*.body`、`observations.json`，按仓库惯例不提交。记录包含时间、固定路径、状态、关键头、正文大小/SHA-256 及原文；本报告保存请求矩阵、关键字段与反例，不依赖这些本地文件也可重放查询。

20 次请求由以下路径组成（关键词须按 UTF-8 URL 编码，均为 GET）：

| 类别 | 路径 / 参数 | 次数 |
| --- | --- | --- |
| 番剧页 | `/Home/Bangumi/` 后分别为 2259、3464、2153、701、2348 | 5 |
| 宽泛搜索与组筛选 | `/Home/Search`；`searchstr=从零开始的异世界生活`，分别不带组、`subgroupid=28`、`subgroupid=1254` | 3 |
| 新编集搜索 | `/Home/Search?searchstr=新编集` | 1 |
| 零结果搜索 | `/Home/Search`；上文受控词、短篇中文名、短篇原名各一次 | 3 |
| 历史累计 | `/Home/ExpandEpisodeTable`；2259/45 的 take=15、65，2259/28 的 take=8、9 | 4 |
| RSS | `/RSS/Bangumi?bangumiId=2259`；分别不带组、`subgroupid=1254`、45、999999999 | 4 |

独立复现单个请求的 PowerShell 示例，不跟随重定向；其他样本按上表替换参数，批量请求须保持间隔：

```powershell
$OutputEncoding = [System.Text.UTF8Encoding]::new()
@'
import hashlib, time, urllib.request, urllib.error, xml.etree.ElementTree as ET
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None
path = '/RSS/Bangumi?bangumiId=2259&subgroupid=45'
request = urllib.request.Request('https://mikanani.me' + path, headers={
    'User-Agent': 'Cinema115-MikanP0Probe/1.0 (https://github.com/jli452195/Cinema115)',
    'Accept': 'application/rss+xml, application/xml, text/html'})
time.sleep(1.2)
try:
    response = urllib.request.build_opener(NoRedirect).open(request, timeout=25)
except urllib.error.HTTPError as error:
    response = error
with response:
    raw = response.read(4194305)
    if len(raw) > 4194304:
        raise ValueError('response over 4 MiB; incomplete, not empty')
    print(response.status, len(raw), hashlib.sha256(raw).hexdigest())
    if response.status == 200:
        items = ET.fromstring(raw).findall('./channel/item')
        print('items:', len(items))
        for item in items[:2]:
            print(item.findtext('title'), item.findtext('link'))
'@ | py -3 -X utf8 -
```

该示例只演示 RSS 的 XML 投影，替换为网页路径时应仅检查正文，不使用 XML 解码。摘要可能随上游数据变化，不能断言每次都相同；本次 2259/28 的 take=8、9 正文均为：

```text
SHA-256: 6129a35dd30613f4a7f264eeaab24221c18372b5d8629f1ee0e892de5ea059e3
bytes: 22934
```

已执行并通过：

- 本地样本核对：组 45 累计前缀、RSS/展开 20 条身份集合、组 28 的 8/9 边界、GUID 碰撞、原版/新编集关联、零结果标记与超限记录。
- `cd backend; go test ./internal/client/mikan -count=1`：通过，覆盖现有网页客户端；未设置 MIKAN_LIVE，没有额外实站请求。此结果不代表尚未实现的 RSS 解析器或新动漫规则已测试。
- 任务表统计、文档链接与复现代码语法检查。

P0-02 至此完成。来源默认策略与读取预算留到 P0-04，最终 API/DDL 留到 P0-05；不新增媒体服务器或其他业务联动。
