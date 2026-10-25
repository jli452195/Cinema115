# 版本发布

Git 标签是应用发布版本的唯一来源。前端、Go 服务、数据库工具和 Docker OCI 标签使用同一组构建信息：标签版本、完整提交 SHA、提交时间。源码开发默认显示 `dev`；`frontend/package.json` 的版本只属于前端包，不用于应用发布，也无需跟着修改。提交时间用于确定构建来源，不表示工作流的执行时间。

## 首次配置

1. 将源码推送到 `jli452195/Cinema115`，默认分支为 `master`。
2. 在 Docker Hub 创建公开仓库 `jli452195/cinema115`，创建具有该仓库读写权限的访问令牌。
3. 在 GitHub 仓库「Settings → Secrets and variables → Actions」添加两个 Repository secrets：`DOCKERHUB_USERNAME`（值为 `jli452195`）与 `DOCKERHUB_TOKEN`（访问令牌）。不将令牌写进文件或提交记录。
4. 确认仓库已启用 Actions，组织策略允许工作流发布 GitHub Release。发布任务会申请 `contents: write`，其他任务只有源码读取权限。

分支推送和 Pull Request 自动运行 Go 测试、`go vet`、前端契约测试和前端构建，不发布镜像。发布工作流会复用相同检查，全部通过后才登录 Docker Hub。

## 发布一个版本

先提交并推送本轮代码，再在要发布的提交上创建标签。以下为首个测试版示例；执行前确认工作区没有遗漏的改动：

```sh
git status
git switch master
git pull --ff-only
git tag -a v0.1.0-beta.1 -m "Cinema115 v0.1.0-beta.1"
git push origin v0.1.0-beta.1
```

工作流只接受 `vX.Y.Z` 和 `vX.Y.Z-alpha.N`、`vX.Y.Z-beta.N`、`vX.Y.Z-rc.N`，数字不带多余前导零。Docker 标签省略开头的 `v`，应用显示保留 `v`。

| Git 标签 | Docker 标签 | GitHub Release |
| --- | --- | --- |
| `v0.1.0-beta.1` | `0.1.0-beta.1` | 测试版，不更新 `latest` |
| `v0.1.0-rc.1` | `0.1.0-rc.1` | 候选版，不更新 `latest` |
| `v0.1.0` | `0.1.0`、`latest` | 正式版 |
| `v0.1.1` | `0.1.1`、`latest` | 补丁版 |

版本递增遵循语义化版本：修复增加补丁号，兼容的新功能增加次版本号，不兼容变更增加主版本号。开发阶段可先发布 `0.x` 测试版。Git 提交描述改动，例如 `fix: 修复整理任务恢复`，不需要每个提交都写版本号。不要移动或复用已发布的版本标签。

标签推送后，在 GitHub「Actions → Release」查看进度：校验标签 → 测试 → 构建并推送 `linux/amd64` 与 `linux/arm64` 镜像 → 分别执行两个架构的版本检查并核对前端信息 → 生成固定版本的 Compose → 发布 GitHub Release。正式版同时更新 Docker 的 `latest`；补发比现有最新正式版更低的版本不会把 `latest` 回退。

Release 自动附带改动说明和 `compose.yaml`，其中已经使用对应版本的镜像。首个正式版发布前，根目录模板的 `latest` 无法拉取，测试用户应下载测试版 Release 附件。镜像推送后仍需完成校验，只有工作流成功、Release 正式发布后才通知用户升级。

发布失败时修正凭据、网络等外部问题后，在 Actions 中重新运行失败任务；无需删除标签。如果代码需要修改，提交后发布一个新版本。镜像推送成功但后续步骤失败时，明确版本镜像可能已经存在；GitHub Release 可能仍为草稿。重跑会继续完成附件和发布。不要把新的代码强推到旧标签。

正式版先记录 GitHub Release，再更新 Docker `latest`；如果最后一步失败，Release 和固定版本镜像已经可用，`latest` 暂时仍指向之前版本，重新运行失败任务即可补齐。

## 部署与核对

用户操作见 [NAS Docker 部署](docker.md)。Docker Hub 页面是 <https://hub.docker.com/r/jli452195/cinema115>。

```sh
docker compose pull
docker compose up -d
docker compose exec cinema115 /app/server -version
docker compose exec cinema115 /app/dbtool -version
```

两个命令都应输出本次标签、对应提交 SHA 和提交时间。前端页面 HTML 中的 `cinema115-*` 元信息应与后端一致。前端已启用 PWA 更新提示，缓存中的旧页面要完成更新后才显示新版本信息。

## 本地检查

```sh
cd backend
go test ./...
go vet ./...
cd ../frontend
npm ci
node --test tests/*.test.mjs
npm run build
cd ..
docker compose config --quiet
```

需要自行构建时，可在源码根目录执行 `docker build -t cinema115:dev .`；该镜像默认记录 `dev`。发布构建由工作流传入 `APP_VERSION`、`APP_REVISION`、`APP_CREATED` 三个构建参数，不把运行凭据传入构建。Go 和 Node 版本分别与 Dockerfile 的 Go 1.26、Node 22 保持一致。BuildKit 缓存仅用于加速构建，不进入最终运行镜像。
