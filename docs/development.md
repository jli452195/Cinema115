# 本地开发

以下命令从项目根目录开始执行。Go 与 Node.js 版本建议与 [Dockerfile](../Dockerfile) 保持一致，当前构建使用 Go 1.26 与 Node.js 22。

## 准备配置

将根目录 `.env.example` 复制为 `.env`，填写管理员账号、密码、随机 `JWT_SECRET` 和需要使用的 TMDB API Key。配置只由后端读取。

已有本地运行数据时保留原 `.env` 与 `data/`，尤其是原 `JWT_SECRET`。数据库及凭据恢复见 [数据库说明](database.md)。

## 启动后端

```sh
cd backend
go mod download
go run ./cmd/server
```

后端默认监听 `3002`，可通过根目录 `.env` 中的 `HTTP_PORT` 修改。已安装 Air 时，也可以在 `backend` 执行 `air -c .air.toml`。

## 启动前端

另开终端，从项目根目录执行：

```sh
cd frontend
npm ci
npm run dev
```

打开终端显示的 Vite 地址。开发代理读取根目录 `.env` 中的后端端口配置。

## 按修改范围检查

在对应目录运行所需检查：

| 范围 | 工作目录 | 命令 |
| --- | --- | --- |
| 后端 | `backend` | `go test ./...`、`go vet ./...` |
| 前端 | `frontend` | `node --test tests/*.test.mjs`、`npm run build` |
| 文档站 | `docs/site` | `npm ci`、`npm run docs:build` |

前端检查前先执行 `npm ci` 安装锁定依赖。临时验收脚本、截图和开发日志集中在 [tests/](../tests/README.md)。

文档实时预览见 [编写文档](site/contributing/index.md)，CI 检查范围与发布流程见 [版本发布](releasing.md)。
