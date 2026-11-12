# 第三方代码说明

`backend/internal/client/p115/rsa.go` 的 RSA/XOR 协议实现及测试向量移植自
ChenyangGao 的 `p115cipher 0.0.6`，原作者标注 GPLv3。
许可证全文保留于 `licenses/p115cipher-GPL-3.0.txt`。
115 分享接口路径和参数参考 `p115client 0.0.9.6.5.1` 的公开实现。

项目运行不依赖 Python。测试向量由原实现离线生成，用于验证 Go 移植的一致性。

## bangumi-data 交叉引用数据

动漫订阅的 Bangumi → TMDB 作品关联使用 [bangumi-data](https://github.com/bangumi-data/bangumi-data)，由该项目的社区贡献者维护，数据采用 [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/) 许可。

本项目运行时读取其公开 `dist/data.json`，按精确 Bangumi ID 提取并去重 TMDB TV 作品与季线索；不分发原始数据副本。章节对应建议由 Cinema115 根据 Bangumi 和 TMDB 资料另外推导，并非 bangumi-data 提供的逐集映射，也不表示上游项目为本项目背书。

## 蜜柑品牌素材

`frontend/src/assets/mikan/` 的人物及 Mikan 字标来自 [Mikan Project](https://mikanani.me/)，用于标识蜜柑搜索的数据来源，不表示本站为蜜柑官方网站或获得其背书。

- 人物原图：<https://mikanani.me/images/mikan-pic.png>（400×400）。本地 `mikan-pic.svg` 内嵌原始 PNG，保留原图像素及透明度，无外部资源依赖。
- 字标：<https://mikanani.me/images/mikan-text.svg>。移除 XML/DOCTYPE 声明，保留原始路径和橙色描边；白色弹窗中固定使用官方浅色样式的白色填充。

素材获取日期：2026-09-19。素材权利归原权利人所有；未确认其另行授予的开源授权，本项目代码许可证不覆盖这些品牌素材。
