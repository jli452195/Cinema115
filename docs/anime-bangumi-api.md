# 动漫订阅：Bangumi 接口验证（P0-01）

验证日期：2026-10-18，17:05～17:06（Asia/Shanghai）。对应[实施任务表](anime-subscription-tasks.md)的 P0-01，结论供 P1 客户端与缓存实现使用；本项没有实现业务 API 或页面。

后续实现：P1-02 已完成[Bangumi 客户端与缓存](anime-bangumi-implementation.md)，相关 Go 模拟测试和静态检查通过；API/页面仍未接入。以下保留 P0-01 实站验证时的范围，不把后续模拟结果记为实站请求。

## 验证范围与结论

已用公开、无凭据请求验证周历、历史搜索、条目详情、条目关系及分集资料；同时验证分页末尾、零结果、非法参数、资料不存在和条件缓存。共 14 次顺序请求，每次间隔至少 1 秒，响应正文合计 133,869 字节；未读取网盘、未提交下载。

主机固定为 `https://api.bgm.tv`。请求使用 `Accept: application/json`，POST 使用 UTF-8 JSON；不携带 Authorization 或 Cookie。User-Agent 为：

```text
jli452195/Cinema115-P0Probe/1.0 (https://github.com/jli452195/Cinema115)
```

这符合官方要求的开发者 ID、应用名、版本及真实项目主页。1 秒间隔是本次探测的自定预算，不是上游公布的速率额度。

结论：首版周历与历史搜索可以使用这些公开接口。搜索被官方标记为实验性，需隔离上游类型、校验本地输入，并保留真实失败状态。当前样本不证明完整季度覆盖、长期可用性或稳定排序。

## 请求契约与字段

| 用途 | 方法与路径 | 参数、返回与首版使用方式 |
| --- | --- | --- |
| 周历 | `GET /calendar` | 无分页、无已声明季度参数；数组，每项含 `weekday` 与 `items`；一次读取即可提供卡片资料 |
| 历史搜索 | `POST /v0/search/subjects?limit=2&offset=0` | body 包含 `keyword`、`sort: "match"`、`filter: {"type": [2]}`；返回 `total/limit/offset/data` |
| 条目详情 | `GET /v0/subjects/278826` | 按需读取选中条目；返回 `id/type/name/name_cn/date/platform/images/eps/total_episodes` 等 |
| 条目关系 | `GET /v0/subjects/278826/subjects` | 返回数组，无分页约定；元素包含 `id/type/name/name_cn/images/relation`；需要筛选动画类型并人工核对 |
| 分集资料 | `GET /v0/episodes?subject_id=278826&type=0&limit=2&offset=0` | `type=0` 为本篇；返回 `total/limit/offset/data`；保留 `id/type/sort/ep/name/name_cn/airdate` |

### 周历

本次返回七个星期分组，`weekday.id` 为 1～7，周一至周日分别有 12、10、13、11、14、22、26 个条目。这些数量只记录本次观察，不作为测试中的固定值。

条目已有 `id/type/name/name_cn/air_date/air_weekday/images/url`，并可能带简介、评分和收藏信息。卡片只需投影必要字段，不需要逐卡请求详情。中文名为空时使用原名，缺图使用已有占位。

精简实际样本：

```json
{
  "weekday": {"en": "Wed", "cn": "星期三", "ja": "水耀日", "id": 3},
  "items": [{
    "id": 975,
    "type": 2,
    "name": "ONE PIECE",
    "name_cn": "航海王",
    "air_date": "1999-10-20",
    "air_weekday": 3
  }]
}
```

长期放送作品也在返回中，不能按首播年份把周历解释为当前季度新作目录。放送星期不表示蜜柑发布时间或已有下载资源。本项没有验证或承诺季度目录能力。

周历样本的封面链接使用 `http://lain.bgm.tv/...`，详情样本使用 `https://lain.bgm.tv/...`，不能直接假设所有图片都是 HTTPS。P1 封面接入应复用本地缓存与受控来源校验，不为每张海报额外查询详情；图片下载及代理验收不在本项范围内。

### 历史搜索与分页

实际请求正文：

```json
{
  "keyword": "Re:ゼロから始める異世界生活",
  "sort": "match",
  "filter": {"type": [2]}
}
```

`type=2` 表示动画，不指定它可能得到书籍、游戏等条目。官方还声明 `heat/rank/score` 排序和 `air_date` 日期范围筛选，但本次只实测 `match` 和动画筛选；日期筛选不等于 `/calendar` 支持季度参数。

| 请求 | HTTP | 实际分页结果 | 本次返回 ID |
| --- | --- | --- | --- |
| `limit=2&offset=0` | 200 | `total=69, limit=2, offset=0` | 140001、225462 |
| `limit=2&offset=2` | 200 | `total=69, limit=2, offset=2` | 261805、278826 |
| `limit=2&offset=71` | 200 | `total=69, limit=2, offset=71, data=[]` | 无，已过末尾 |
| 唯一无结果词，`limit=2&offset=0` | 200 | `total=0, limit=2, offset=0, data=[]` | 无，查询成功 |

无结果词为 `cinema115_p0_no_such_anime_b8f2c05e`，其余正文条件相同。搜索结果包含本传、OVA、不同篇章和衍生作品，标题匹配不能自动合并订阅。返回还带封面、日期等资料，可以直接用于搜索卡片。

分页使用条目偏移量，不是页号。搜索 schema 只声明 integer，没有给出 limit 上限；后续服务端需自定有界分页并明确传参，不能把分集接口的 200 上限套给搜索。总数、排名和翻页结果可能随上游更新变化，不保证搜索快照或完整系列清单。

### 详情、关系与编号

条目 `278826` 的实际资料：

```json
{
  "id": 278826,
  "type": 2,
  "name": "Re:ゼロから始める異世界生活 2nd season",
  "name_cn": "Re：从零开始的异世界生活 第二季",
  "date": "2020-07-08",
  "platform": "TV",
  "eps": 13,
  "total_episodes": 13
}
```

关系接口本次返回 15 个条目，包括书籍、音乐、游戏及动画。其中动画关系为：

| ID | 名称 | relation |
| --- | --- | --- |
| 140001 | Re：从零开始的异世界生活 | 前传 |
| 310194 | Re：从零开始的休息时间2 | 衍生 |
| 316247 | Re：从零开始的异世界生活 第二季 后半部分 | 续集 |

这些关系只作为版本与前后作核对线索，不生成字幕组偏移或 TMDB 季集对应；不递归抓取全部关系。`relation` 为上游文本，客户端不能假定只有固定三种值。

分集请求实际返回 `total=13, limit=2, offset=0`：

```json
[
  {"id": 957951, "subject_id": 278826, "type": 0, "sort": 26, "ep": 1, "name_cn": "各自的誓言"},
  {"id": 957952, "subject_id": 278826, "type": 0, "sort": 27, "ep": 2, "name_cn": "下一个地方"}
]
```

官方定义 `sort` 为同类型章节的排序/集数，`ep` 为当前条目内从 1 开始的集数，非本篇时 `ep` 没有此意义。这个真实样本说明两者不可混用；也不能据此宣称字幕组标题一定写 26。后续需同时保存原字段、资源原编号与管理员确认的对应，不能用浮点数作为特殊集号的唯一身份。

分集接口官方声明 `limit` 范围 1～200（默认 100）、`offset>=0`（默认 0）；本次以小分页读取，没有穷举上下限。`eps`、`total_episodes` 和最大分集编号均不作为下载完成依据。

## 缓存观察

| 接口 | 本次响应缓存头 | 条件请求实测 |
| --- | --- | --- |
| 周历 | `Cache-Control: max-age=3600`，无 ETag、Last-Modified | 没有可用验证器，未发送条件请求 |
| 搜索（含零结果） | 未见 Cache-Control、ETag、Last-Modified | 未测试条件 POST；采用本地有界查询缓存 |
| 详情 | `public, max-age=3600`；`Last-Modified: Sun, 18 Oct 2026 09:00:03 GMT` | 发送相同 `If-Modified-Since`，返回 304、空正文 |
| 关系 | `public, max-age=3600`；`Last-Modified: Sun, 18 Oct 2026 09:05:44 GMT` | 发送相同 `If-Modified-Since`，返回 304、空正文 |
| 分集 | 未见 Cache-Control、ETag、Last-Modified | 未发送条件请求 |
| 本次 400/404 | 未见 Cache-Control、ETag、Last-Modified | 不作为成功资料缓存 |

官方详情 schema 的描述是 `cache with 300s`，实际 HTTP 响应为 3600 秒；记录两者差异，客户端应按实际响应及本地策略处理，计算新鲜度时考虑 Age/Date，不能每次命中 CDN 又从头延长一小时。未观察到 ETag，不代表上游永远不返回。

P1 应保存成功数据和验证器，共享缓存及进行中请求；详情、关系各自验证。304 必须有对应的成功旧正文才可复用，不能当成空条目。上游失败保留带更新时间的成功旧资料；没有旧资料则返回真实失败，不能伪造空日历或零搜索结果。缓存键包含关键词、排序、过滤及分页条件，不为了合并查询删除有意义的文字或标点。

## 错误与输入校验

| 实际请求 | HTTP | 观察与后续处理 |
| --- | --- | --- |
| `GET /v0/subjects/0` | 400 | `title="Bad Request"`，描述 `"0" is not valid ID`；本地先校验正整数 ID |
| `GET /v0/subjects/999999999` | 404 | `title="Not Found"`，描述资料不存在或已删除；保留本地订阅和业务历史 |
| 搜索 body 为 `{"keyword":{}}` | 400 | `title="JSON Error"`，描述 keyword 需要 string；本地校验字段类型 |
| 搜索 body 为 `{}` | 200 | 返回 `total=1000` 及书籍/游戏结果；缺 keyword 没有被实际服务拒绝，不能依赖上游必填校验 |

实际错误正文既有 `title/description/details/request_id`，也有仅 `title/description` 的情况；例如：

```json
{"title":"JSON Error","description":"Unmarshal type error: expected=string, got=object, field=keyword, offset=13"}
```

后端必须自行拒绝缺失或空白关键词、非法分页及 ID，不能将它们透传为泛搜索。按 HTTP 状态分类错误，错误详情字段允许缺失；也要能处理代理返回的非 JSON。空数组只在有效 200 响应且结构正常时表示查询成功无结果。

本次没有主动触发 401/403、429、5xx、超时或损坏 JSON。后续客户端采用取消/超时、响应大小上限、访问预算和退避；尊重 Retry-After，不把异常当未找到，不反复重试无权限请求。这些属于实现约定，需在 P1 模拟上游测试，不能记为本项实站验证通过。

## 可复现记录

本地探测脚本及精简记录位于 `tests/anime-bangumi/probe.py`、`tests/anime-bangumi/observations.json`，按仓库惯例不提交。记录包含方法、路径、请求正文、时间、状态、关键缓存头、正文字节数/SHA-256 和公开字段投影。本报告保留关键样本与请求参数，独立于本地文件也可复现。

以下从项目根目录在 PowerShell 执行，使用 Python 标准库；仅访问固定 Bangumi 公共接口，最多 15 次请求。每次结果可能随上游资料和缓存变化；错误探测也只查公开元数据。

```powershell
$OutputEncoding = [System.Text.UTF8Encoding]::new()
@'
import json, time, urllib.request, urllib.error
base = 'https://api.bgm.tv'
ua = 'jli452195/Cinema115-P0Probe/1.0 (https://github.com/jli452195/Cinema115)'
def read(path, body=None, validators=None):
    time.sleep(1)
    headers = {'User-Agent': ua, 'Accept': 'application/json', **(validators or {})}
    payload = None
    if body is not None:
        headers['Content-Type'] = 'application/json'
        payload = json.dumps(body, ensure_ascii=False).encode('utf-8')
    req = urllib.request.Request(base + path, data=payload, headers=headers)
    try:
        response = urllib.request.urlopen(req, timeout=20)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        raw = response.read(2097153)
        if len(raw) > 2097152:
            raise ValueError('response exceeds 2 MiB')
        cache = {k: response.headers[k] for k in
                 ('Cache-Control', 'ETag', 'Last-Modified', 'Age', 'Date')
                 if response.headers.get(k) is not None}
        value = json.loads(raw) if raw else None
        print(response.status, req.get_method(), path, cache)
        if isinstance(value, dict):
            print({k: value[k] for k in ('total', 'limit', 'offset', 'title', 'description') if k in value})
        return cache, value
calendar, _ = read('/calendar')
search = '/v0/search/subjects?limit=2&offset='
query = {'keyword': 'Re:ゼロから始める異世界生活', 'sort': 'match', 'filter': {'type': [2]}}
_, first = read(search + '0', query)
read(search + '2', query)
if isinstance(first, dict) and isinstance(first.get('total'), int) and 0 < first['total'] < 1000:
    read(search + str(first['total'] + 2), query)
read(search + '0', {**query, 'keyword': 'cinema115_p0_no_such_anime_b8f2c05e'})
subject, _ = read('/v0/subjects/278826')
relations, _ = read('/v0/subjects/278826/subjects')
read('/v0/episodes?subject_id=278826&type=0&limit=2&offset=0')
read('/v0/subjects/0')
read('/v0/subjects/999999999')
read(search + '0', {})
read(search + '0', {'keyword': {}})
for path, cached in [('/calendar', calendar), ('/v0/subjects/278826', subject),
                     ('/v0/subjects/278826/subjects', relations)]:
    if cached.get('ETag'):
        read(path, validators={'If-None-Match': cached['ETag']})
    elif cached.get('Last-Modified'):
        read(path, validators={'If-Modified-Since': cached['Last-Modified']})
'@ | py -3 -X utf8 -
```

若周历后续新增验证器，上述脚本会多一次周历条件请求，最多 15 次；本次实际周历无验证器，因此为 14 次。

官方契约来源均于本日读取，HEAD 内容会变化，保留下列内容摘要用于比对：

| 来源 | SHA-256 |
| --- | --- |
| [v0 OpenAPI](https://raw.githubusercontent.com/bangumi/api/HEAD/open-api/v0.yaml) | `5a7ddb7ddec132293b1aa08102e6ac63e31b2925574658e927d6f931df2519da` |
| [legacy /calendar OpenAPI](https://raw.githubusercontent.com/bangumi/api/HEAD/open-api/api.yml) | `616e2d485815175d034c9261734576a9f00df4212dee34b702dd05a010ec4724` |
| [官方 User-Agent 规范](https://raw.githubusercontent.com/bangumi/api/HEAD/docs-raw/user%20agent.md) | `e5db3dbb0849fe62d4ac51f0a902dc3b230eb25f2a9cb2101ab94a94366d9609` |

没有复制上游实现代码或新增第三方运行依赖。P0-01 的结论仅覆盖公开 API 的当前观察；蜜柑历史、115 关联能力及最终本地 API/DDL 分别在后续任务验证。
