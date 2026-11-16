# 动漫花园 HTML 测试样本

2026-11-16 从原站只读抓取，裁剪为解析器回归所需片段，不包含广告和完整文件列表。

- `search.html`：`https://share.dmhy.org/topics/list?keyword=%E5%B9%BD%E6%B8%B8%E7%99%BD%E4%B9%A6`，每种实际分类保留一行，另保留仅在 `data-magnet` 中提供磁力的幽游白书 162636、龙鸣 160765 旧帖，共七行。龙鸣样本来自 `https://share.dmhy.org/topics/list?keyword=%E9%BE%99%E9%B8%A3`。
- `detail.html`：`https://share.dmhy.org/topics/view/658424_.html`，保留标题分享区的原帖编号证据及磁力、种子链接区域。

单元测试只读取这些本地样本，不访问外网。真实缺失、身份冲突、编码标题、超长 tracker 等边界在测试中生成，不保存冗长重复样本。
