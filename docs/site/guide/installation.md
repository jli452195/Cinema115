---
title: Docker 部署
sidebarSort: 20
---

# Docker 部署

Cinema115 的 Docker 镜像支持 amd64 和 arm64。一个容器同时提供网页和 API，NAS 无需安装 Go、Node.js 或 Nginx。

## 准备工作

- NAS 已安装 Docker 与 Docker Compose。
- 准备一个保存配置和数据的目录，以及需要生成 STRM 的媒体库目录。
- 以下步骤使用 Linux NAS 的 host 网络，默认管理端口为 `8115`。

## 获取配置文件

从 [GitHub Releases](https://github.com/jli452195/Cinema115/releases) 下载目标版本附件中的 `compose.yaml`，放进部署目录。附件中的镜像已经固定到对应版本。

使用文本编辑器修改该文件，填写以下配置：

| 配置项 | 说明 |
| --- | --- |
| `ADMIN_USERNAME` | 管理员登录名称。 |
| `ADMIN_PASSWORD` | 管理员密码，不能为空。 |
| `JWT_SECRET` | 足够长的随机密钥。已有部署必须沿用原值。 |
| `TMDB_API_KEY` | TMDB API Key，用于影视资料与海报查询。 |
| `HTTP_PORT` | 默认 `8115`，可改成未占用的端口。 |
| `user` | 改成 NAS 用户对应的 UID/GID，确保能读写挂载目录。 |

TMDB API Key 可在 [TMDB 账号设置](https://www.themoviedb.org/settings/api) 中申请。

::: warning 保存好配置
`JWT_SECRET` 用于保护已保存的登录凭据。更新或迁移时需要保留原值。填写了真实密码和密钥的 Compose 文件只保存在自己的设备上。
:::

## 设置挂载目录

修改 `volumes` 左侧为自己的 NAS 路径，容器内右侧保持不变。例如：

```yaml
volumes:
  - "./data:/data"
  - "/vol1/1000/媒体库:/strm"
```

`./data` 保存数据库、登录会话和缓存；`/strm` 是网页中可选择的本地生成目录根路径。先创建这些 NAS 目录，并给 Compose 中指定的用户读写权限。

例如，网页选择 `/strm/待整理` 时，文件实际保存在 NAS 的 `/vol1/1000/媒体库/待整理`。

## 启动容器

在 `compose.yaml` 所在目录执行：

```sh
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose ps
```

打开 `http://NAS_IP:8115`。如果修改了 `HTTP_PORT`，使用修改后的端口。

host 网络下无需配置端口映射。Emby 302 反代端口在网页中单独设置，不能与管理端口相同。

## 完成首次配置

使用设置的管理员账号登录，进入「115 网盘 → 115 助手」，按 [115 账号配置](./accounts.md) 创建主账号。

需要生成 STRM 时，在对应功能中设置生成目录和播放地址。播放地址必须能被 Emby 和播放器访问，不能填写容器内部地址或仅 NAS 自己能访问的回环地址。

## 检查运行状态

```sh
docker compose logs --tail=100 cinema115
```

页面打不开时先检查容器状态、端口和 NAS 防火墙。更多排查见 [常见问题](../faq/index.md)。
