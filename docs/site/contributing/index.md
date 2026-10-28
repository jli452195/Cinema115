---
title: 编写文档
sidebarSort: 30
---

# 编写文档

用户文档保存在项目的 `docs/site/`。Teek 根据目录和 Markdown 文件生成导航；日常补充教程不需要修改 Vue 组件。

## 本地预览

在项目根目录执行：

```powershell
cd docs/site
npm ci
npm run docs:dev
```

打开终端显示的地址，默认是 `http://localhost:5174/Cinema115/`。编辑 `.md` 文件后页面自动更新；增加或删除文档时，目录插件会重启预览服务以更新侧栏。

局域网内其他设备需要预览时，使用 `npm run docs:dev -- --host 0.0.0.0`，并访问本机 IP 对应的端口。

## 新增一篇教程

例如在 `guide/` 中添加 `example.md`：

````markdown
---
title: 教程名称
sidebarSort: 50
---

# 教程名称

说明这篇教程帮助用户完成什么。

## 准备工作

- 列出需要提前配置的账号、目录或服务。

## 操作步骤

1. 写清页面入口。
2. 说明需要填写的选项。
3. 描述保存后应该看到的结果。

## 检查结果

给出确认操作成功的方法。
````

`title` 用于侧栏名称，`sidebarSort` 越小越靠前。建议按 `10、20、30` 留出间隔，方便中间插入文章。文档内使用一个一级标题，章节使用二级或三级标题。

新增分类时创建目录和 `index.md`，在索引中说明该分类的用途，并在 `.vitepress/config.mts` 的 `sections` 中添加入口。分类中的文章由 Teek 自动扫描和排序，导航与侧栏分类共用这份入口配置。

## 插入截图与链接

截图放在 `public/images/`，使用能说明用途的文件名。Markdown 写法如下，发布时会自动处理站点路径前缀：

```markdown
![115 主账号登录页面](/images/115-login.png)

[查看账号配置](../guide/accounts.md)
```

以上图片仅为写法示例，使用前需要添加实际文件。图片可使用 Teek 自带的点击预览功能。截图隐藏 Cookie、密码、Token 和个人信息；只记录用户完成操作所需的界面。

## 构建检查

```powershell
npm run docs:build
npm run docs:preview
```

预览构建结果的默认地址为 `http://localhost:4174/Cinema115/`。构建会检查站内死链；发布前确认链接、图片和手机布局正常。

本地依赖、缓存和构建产物已经忽略，提交源文件与 `package-lock.json` 即可。文档有独立依赖，修改文档无需安装或构建业务前端。

## 提交修改

```sh
git add docs/site
git commit -m "docs: 补充使用教程"
git push origin master
```

也可以用页面底部的「在 GitHub 上编辑此页」在线修改，尚未推送的新文件需先提交到仓库。贡献者通过分支和 Pull Request 提交修改。

## 首次开启 GitHub Pages

仓库维护者只需要设置一次：

1. 打开仓库的 [Settings → Pages](https://github.com/jli452195/Cinema115/settings/pages)。
2. 在「Build and deployment → Source」中选择 **GitHub Actions**。现有 `Docs` 工作流负责构建，不需要额外创建 Jekyll 或静态 HTML 模板工作流。
3. 将 `docs/site/` 与 `.github/workflows/docs.yml` 提交并推送到 `master`。
4. 在 [Actions → Docs](https://github.com/jli452195/Cinema115/actions/workflows/docs.yml) 中查看 `build` 和 `deploy`。两项成功后访问 [在线文档](https://jli452195.github.io/Cinema115/)。

工作流使用 GitHub 自带的令牌和部署身份，不需要新增 PAT、Docker Hub 密钥或单独维护 `gh-pages` 分支。只有部署任务申请 Pages 写权限。

## 日常自动发布

| 操作 | 文档工作流行为 |
| --- | --- |
| PR 修改 `docs/site/` 或文档工作流 | 安装文档依赖并构建，检查死链，不发布。 |
| `master` 修改 `docs/site/` 或文档工作流 | 构建成功后自动发布。 |
| 仅修改业务代码或其他开发记录 | 不触发文档工作流。 |
| 手动运行工作流 | 构建所选分支，仅选择 `master` 时发布。 |

文档依赖与业务前端独立，文档发布不构建 Docker 镜像，也不执行 Go 测试。业务 CI 仍按自己的改动范围决定是否运行检查；同一次提交包含业务代码或公共配置时，正常执行相应检查。

修改文档无需创建版本标签。连续提交会取消旧的文档构建；已经开始的 Pages 部署会完成，后续部署按队列执行。

## 发布失败时

构建失败先查看 `build` 中的错误，修正文档或链接后重新推送。发布失败则检查 Pages 是否选择 GitHub Actions，以及仓库 `github-pages` 环境是否允许从 `master` 部署。

修复外部设置后，可以在 Actions 重跑失败任务；或选择「Docs → Run workflow」，分支选 `master`，手动发布当前文档。构建失败不会覆盖已上线的站点，部署结果以 Actions 的 `deploy` 状态为准。
