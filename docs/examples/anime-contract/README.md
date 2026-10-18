# P0-05 契约验证样例

[cases.json](cases.json) 是合成资料：三个来源、同集两个字幕组、同组 v2、GUID 碰撞、同 Hash 多源关联及 23 个后续服务/API 场景。编号对应与 ID 均为样例，不声明真实站点资料。

[validate.py](validate.py) 用 Python 标准库执行[完整 DDL](../../anime-subscription-schema.sql)，只创建 `:memory:` SQLite，验证实际唯一键、外键、状态 CHECK、资源/文件类型与终态调度约束。脚本不读取应用配置或数据库，不使用网络，不调用真实115接口。

从项目根目录执行：

```powershell
py -3 -X utf8 docs/examples/anime-contract/validate.py
```

这些验证证明契约 SQL 可以执行，并保留后续实现的反例。跨表业务归属、RSS 解析、调度、指纹、HTTP code 和 UI 行为由后续 Go/前端实现验证；`serviceCases` 只是明确输入/预期，不冒充已经执行的业务测试。生产迁移仍在 P1-01 及 P2/P3 对应任务接入，结构版本届时核定。
