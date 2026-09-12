# 本地测试与验收资料

临时测试脚本、浏览器验收、截图、测试结果和参考客户端统一放在本目录。目录内容不进入 Docker 构建上下文；除本说明外，也不提交 Git，避免把本地测试配置、分享记录和大体积依赖带入仓库。

- `browser/`：浏览器验收脚本及本地 Playwright 依赖，从该目录运行脚本。
- `strm-output/`：专门用于验收的 STRM 输出。
- `docker-smoke/`、`docker-relative/`：Docker 测试配置和数据。
- 根目录下的 `check-*.cjs`、`test-*.cjs` 等脚本：通常从 Cinema115 项目根目录运行；运行前阅读脚本，部分真实接口验收会转存或创建分享。
- 截图、日志、JSON 结果以及下载的参考代码：本地排查资料。

Go 单元测试保留在 `backend/internal/**` 对应包内，使用 `cd backend` 后执行 `go test ./...`。Docker 同时排除 `*_test.go` 和 `testdata/`，发布镜像无需携带这些文件。

运行数据仍由根目录 `data/` 管理，并独立从 Docker 构建上下文排除；Docker 通过卷挂载保存运行数据。
