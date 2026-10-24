# 115 纯 v2 与混合种子实测

测试时间：2026-10-24 15:05–15:07（Asia/Shanghai）。使用项目明确配置的主账号，固定凭据，通过当前 Cookie 云下载接口上传、解析种子并提交磁力。

## 样本

使用 libtorrent 官方仓库的公开测试文件，固定提交 `dd3fd78be28d01fd32c6e0c33c1f13135cb35472`。本地通过 `metainfo.ValidatePieceLayers` 校验。

| 格式 | 来源 | 内容大小 |
| --- | --- | ---: |
| 纯 v2 | [v2_only.torrent](https://github.com/arvidn/libtorrent/blob/dd3fd78be28d01fd32c6e0c33c1f13135cb35472/test/test_torrents/v2_only.torrent) | 1,048,576 字节 |
| v1/v2 混合 | [v2.torrent](https://github.com/arvidn/libtorrent/blob/dd3fd78be28d01fd32c6e0c33c1f13135cb35472/test/test_torrents/v2.torrent) | 65,536 字节 |

另从混合样本移除 v2 字段并使用独立名称，生成合法 v1 对照，排除账号或提交接口不可用的情况。

## 实测结果

| 格式 | 上传及种子解析 | 磁力提交 | 实际任务 |
| --- | --- | --- | --- |
| 纯 v2 | 上传成功；解析返回 `state=true`，但大小错误地为 0 | 正确 `btmh` 磁力返回 `state=false, errcode=10004`，提示“错误的链接” | 未创建成功 |
| 混合 | 文件数量、大小及 v1 InfoHash 正确 | 同时包含 `btih`、`btmh` 的磁力返回成功 | 以 v1 InfoHash 建立任务，大小正确 |
| v1 对照 | 文件数量、大小及 InfoHash 正确 | `btih` 磁力返回成功 | 正常建立任务 |

纯 v2 样本的真实 SHA256 InfoHash：

```text
95e04d0c4bad94ab206efa884666fd89777dbe4f7bd9945af1829037a85c6192
```

提交的磁力为 `magnet:?xt=urn:btmh:1220` 加上述 SHA256。种子解析接口返回的 `72cc28d743f6e31af51f5183b8eb1b7468211e77` 只是 info 字节的 SHA1；该样本没有合法 v1 文件及 pieces 结构，不能将这个 SHA1 视为可下载的 v1 身份。使用真实 64 位 v2 hash 查询任务，返回 `10023`：“错误的info_hash”。

混合样本的两个 InfoHash：

```text
v1: c14199bbec64d0e9e439aa3b6b7639e666b86eca
v2: 597b180c1a170a585dfc5e85d834d69013ceda174b8f357d5bb1a0ca509faf0a
```

混合与 v1 对照任务观察 15 秒后均为 0%，下载字节为 0。这些协议测试样本没有已确认的活跃做种，本次只证实混合格式可以解析、通过 v1 标识建任务，未验证下载完成或 v2 网络传输。

## 结论及清理

- 当前测试账号的 Cookie/SSP 云下载接口拒绝该纯 v2 磁力，种子解析也没有得到正确大小，不能视为支持纯 v2。
- 混合种子可以通过 v1 兼容路径解析并建任务；不应因此认定 115 支持纯 v2。
- 本次未测试 Open API，也未修改产品解析器的格式限制。
- 两个成功创建的测试任务已精确删除；两轮独立测试目录及上传的种子已回收。既有任务和用户文件保留，不清空回收站。
- 本地探针目录 `backend/tmp/torrent-protocol-probe` 和样本克隆 `backend/tmp/libtorrent-protocol-fixtures` 尚未清理：用户明确确认后重试，自动审批仍以 `blocked by policy` 拒绝删除。临时记录不包含 Cookie 或请求签名。
