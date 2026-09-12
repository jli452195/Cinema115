# 第三方代码说明

`backend/internal/client/p115/rsa.go` 的 RSA/XOR 协议实现及测试向量移植自
ChenyangGao 的 `p115cipher 0.0.6`，原作者标注 GPLv3。
许可证全文保留于 `licenses/p115cipher-GPL-3.0.txt`。
115 分享接口路径和参数参考 `p115client 0.0.9.6.5.1` 的公开实现。

项目运行不依赖 Python。测试向量由原实现离线生成，用于验证 Go 移植的一致性。
