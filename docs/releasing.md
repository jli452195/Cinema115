# 版本发布

Git 标签是应用发布版本的唯一来源。前端、Go 服务、数据库工具和 Docker OCI 标签使用同一组构建信息：标签版本、完整提交 SHA、提交时间。源码开发默认显示 `dev`；`frontend/package.json` 的版本只属于前端包，不用于应用发布，也无需跟着修改。提交时间用于确定构建来源，不表示工作流的执行时间。

## 首次配置

1. 将源码推送到 `jli452195/Cinema115`，默认分支为 `master`。
2. 在 Docker Hub 创建公开仓库 `jli452195/cinema115`，创建具有该仓库读写权限的访问令牌。
3. 在 GitHub 仓库「Settings → Secrets and variables → Actions」添加两个 Repository secrets：`DOCKERHUB_USERNAME`（值为 `jli452195`）与 `DOCKERHUB_TOKEN`（访问令牌）。不将令牌写进文件或提交记录。
4. 确认仓库已启用 Actions，组织策略允许工作流发布 GitHub Release。发布任务会申请 `contents: write`，CI 记录查询使用 `actions: read`，其他任务只读取源码。

日常 CI 在 `master` 推送和 Pull Request 时运行，不发布镜像。功能分支通过 PR 检查，避免同一次更新同时触发分支 push 与 PR 两套检查。同一分支或 PR 连续更新时自动取消旧 CI，只保留最新提交；标签发布不受此取消规则影响。

CI 按 `master` 最近一次成功 CI 后的全部改动或 PR 的完整改动范围选择检查，防止连续推送取消旧任务后漏掉尚未验证的代码：

| 修改范围 | 应用检查 |
| --- | --- |
| 仅 `backend/` | Go 测试、`go vet` |
| 仅 `frontend/` | 前端契约测试、前端构建 |
| 仅 Markdown、`docs/`、`licenses/`、`.gitignore` 或文档工作流 | 跳过应用检查 |
| 工作流、Dockerfile、Compose 等公共配置或其他路径 | 前后端完整检查 |
| 发布版本标签 | 同一提交的 master CI 已成功则复用，否则前后端完整检查 |

首次运行、强推后旧提交不可用或不再是当前提交祖先等无法确定可靠比较起点的情况会完整检查。

用户文档使用独立的 `Docs` 工作流：PR 变更 `docs/site/` 或 `.github/workflows/docs.yml` 时只构建，`master` 上的相同变更构建成功后发布到 GitHub Pages。仅修改业务代码不重建文档；文档发布不需要版本标签，也不构建 Docker。首次 Pages 设置与写作步骤见 [编写文档](site/contributing/index.md)。

发布只复用本仓库 `master` 上 push 触发、提交 SHA 完全相同且最新一次已成功完成的 CI。PR 检查、其他提交、运行中、失败或已取消的记录均不复用；查不到记录或查询异常时，调用同一份 CI 执行前后端完整检查。复用成功或补充检查通过后才登录 Docker Hub，补充检查失败或取消时不发布。

## 发布一个版本

先提交并推送本轮代码，再在要发布的提交上创建标签。以下为首个测试版示例；执行前确认工作区没有遗漏的改动：

```sh
git status
git switch master
git pull --ff-only
git tag -a v0.1.0-beta.1 -m "Cinema115 v0.1.0-beta.1"
git push origin v0.1.0-beta.1
```

为避免重复测试，先等要发布提交的 master CI 成功，再推送版本标签。如果推送标签时 CI 仍在运行，发布流程直接执行完整检查，不轮询等待。

工作流只接受 `vX.Y.Z` 和 `vX.Y.Z-alpha.N`、`vX.Y.Z-beta.N`、`vX.Y.Z-rc.N`，数字不带多余前导零。Docker 标签省略开头的 `v`，应用显示保留 `v`。

| Git 标签 | Docker 标签 | GitHub Release |
| --- | --- | --- |
| `v0.1.0-beta.1` | `0.1.0-beta.1` | 测试版，不更新 `latest` |
| `v0.1.0-rc.1` | `0.1.0-rc.1` | 候选版，不更新 `latest` |
| `v0.1.0` | `0.1.0`、`latest` | 正式版 |
| `v0.1.1` | `0.1.1`、`latest` | 补丁版 |

版本递增遵循语义化版本：修复增加补丁号，兼容的新功能增加次版本号，不兼容变更增加主版本号。开发阶段可先发布 `0.x` 测试版。Git 提交描述改动，例如 `fix: 修复整理任务恢复`，不需要每个提交都写版本号。不要移动或复用已发布的版本标签。

标签推送后，在 GitHub「Actions → Release」查看进度：校验标签 → 复用成功 CI 或完整测试 → 构建并推送 `linux/amd64` 与 `linux/arm64` 镜像 → 分别执行两个架构的两个程序的版本检查、核对一次前端信息 → 生成固定版本的 Compose → 发布 GitHub Release。正式版同时更新 Docker 的 `latest`；补发比现有最新正式版更低的版本不会把 `latest` 回退。

Release 自动附带改动说明和 `compose.yaml`，其中已经使用对应版本的镜像。首个正式版发布前，根目录模板的 `latest` 无法拉取，测试用户应下载测试版 Release 附件。镜像推送后仍需完成校验，只有工作流成功、Release 正式发布后才通知用户升级。

更新说明直接读取两个版本标签之间的提交，不要求创建 PR。正式版从当前历史中上一个已发布的正式版标签开始；没有正式版时使用上一个已发布的测试版。测试版使用上一个较低版本的已发布标签。草稿、其他分支及高于当前版本的标签不作基线；首次发布则读取已有提交历史。

提交标题按 `feat:`（新增功能）、`fix:`（问题修复）、`perf:`（性能优化）分类，也支持 `fix(rss):` 这类范围前缀。其他提交进入「其他更新」，未加前缀的中文标题原样保留；`chore:`、`ci:`、`test:`、`build:` 维护提交不列出。带 `!` 的标题进入「不兼容变更」，例如 `feat!: 删除旧配置格式`。说明附带每条提交链接及完整版本对比链接，跳过合并提交本身；已存在的 Release 在重跑时保留正文，避免覆盖手工修改。

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

需要自行构建时，可在源码根目录执行 `docker build -t cinema115:dev .`；该镜像默认记录 `dev`。发布构建由工作流传入 `APP_VERSION`、`APP_REVISION`、`APP_CREATED` 三个构建参数，不把运行凭据传入构建。Go 和 Node 版本分别与 Dockerfile 的 Go 1.26、Node 22 保持一致。

发布使用 Docker Hub 的 `jli452195/cinema115:buildcache` 保存中间构建层，让不同版本标签复用依赖缓存。该标签仅供构建使用，不用于部署，也不改变版本镜像与 `latest` 的规则；首次发布没有缓存时正常构建并生成缓存。Go 模块下载独立保存在构建层中，修改业务代码可复用该层；依赖清单或基础构建镜像变化时重新下载。编译缓存挂载仅在同一构建器内复用，缓存和依赖不进入最终运行镜像。
