# NAS Docker 部署

根目录 `Dockerfile` 分别构建 Vue 和 Go，最终一个镜像、一个容器、一个 Go 进程同时提供页面和 API。默认通过 `http://NAS_IP:8115` 访问，容器端口为 3002。NAS 无需安装 Go、Node.js 或 Nginx。

## 配置

在项目根目录首次创建配置（已有 `.env` 则保留原账号和密钥）：

```sh
cp -n .env.example .env
```

编辑 `.env`，填写管理员账号、密码、随机 `JWT_SECRET`，以及端口和运行用户：

```dotenv
HTTP_PORT=8115
PUID=1000
PGID=1000
TZ=Asia/Shanghai
```

用 NAS 上的 `id` 命令确认 UID/GID。启动后在「分享播放 / STRM」填写播放地址（例如 `http://192.168.0.3:8115`）并点击「保存生成设置」。地址要能被 Emby/播放器访问；有 HTTPS 反向代理时填写实际域名。Telegram Bot 在「Telegram 配置 → Bot 配置」中填写并保存，这些选项不需要环境变量。

`compose.yaml` 只需要两条挂载，媒体库路径直接改左侧：

```yaml
    volumes:
      - "./data:/data"
      - "/vol1/1000/媒体库:/strm"
```

`./data` 保存 SQLite 数据库、加密登录会话和配置，整个 NAS 媒体库挂载到容器 `/strm`。数据库使用 NAS 本机磁盘目录，避免放到 SMB/NFS 网络共享。首次启动前创建目录，并通过 NAS 目录权限/ACL 给 `PUID:PGID` 读写权限，给 Emby 用户读取权限：

```sh
mkdir -p ./data
mkdir -p /vol1/1000/媒体库
```

## 生成目录

页面填写媒体库内的**相对目录**，不要带开头的 `/`：

| 页面填写 | 容器生成位置 | NAS 生成位置 |
| --- | --- | --- |
| `strm` | `/strm/strm` | `/vol1/1000/媒体库/strm` |
| `strm2` | `/strm/strm2` | `/vol1/1000/媒体库/strm2` |
| `strm/电影` | `/strm/strm/电影` | `/vol1/1000/媒体库/strm/电影` |

子目录自动创建，新增相对目录无需修改 Docker 挂载或重启容器。保留分享文件夹结构，不加批次目录、不打包 ZIP；已有文件跳过。移除页面选项不删除 NAS 文件。绝对路径、`..` 和符号链接目录会被拒绝，文件不能越出媒体库。

如果 Emby 也在 Docker 中运行，可给 Emby 挂载 `/vol1/1000/媒体库:/media:ro`，再把 `/media/strm`、`/media/strm2` 添加到 Emby 媒体库。

## 启动和更新

```sh
docker compose config --quiet
docker compose up -d --build --remove-orphans
docker compose ps
curl -f http://127.0.0.1:8115/api/health
```

也可在根目录执行 `docker build -t cinema115:local .` 构建镜像，然后在 NAS 容器管理界面配置相同的环境变量、`8115:3002` 端口和两个挂载。支持 amd64 和 arm64 NAS。

Go 构建使用默认并发与垃圾回收参数，不再固定为低内存编译。BuildKit 缓存依赖和编译结果，修改后端代码后可复用未变更的包；首次构建 Telegram 协议依赖耗时和内存占用较高，缓存仅用于构建，不进入最终镜像。

更新代码后重新执行构建启动命令。修改 `.env` 或挂载后执行 `docker compose up -d`；仅重启用 `docker compose restart`；查看日志用 `docker compose logs --tail=100 cinema115`；停止用 `docker compose down`。NAS 挂载数据保留，容器已配置自动重启、健康检查和日志轮转。

首次部署后在「115 助手」页面添加账号并扫码登录，再到具体功能中选择账号。多账号配置保存于 `/data/cinema115.db`，Cookie 字段使用 AES-GCM 加密，没有全局当前账号。从 Windows 迁移到 NAS 时保留完整 `data/`、原 `JWT_SECRET` 并保证文件权限正确；运行中备份数据库应使用 `dbtool -action backup`。旧 `p115-accounts.enc` 或 `p115-session.enc` 首次启动时自动导入，成功后仅使用数据库，保留旧文件作为备份。根目录密钥和登录凭证不会打包到镜像中。

升级到 SQLite 版本前停止旧服务并备份 `data/` 和 `.env`。新版首次启动自动导入旧转存任务 JSON，原文件保留，新任务写入 `/data/cinema115.db`。无需增加端口或挂载。运行中通过 `docker compose exec cinema115 /app/dbtool -action status` 检查数据库；一致性备份和恢复方法见 [数据库说明](database.md)，不要只复制运行中的主 `.db` 文件。

旧版浏览器保存的绝对路径目录需要按相对名称重新添加。已有 STRM 不自动搬移；更换播放地址时可选择新目录生成。NAS 外层反向代理若存在，应允许超过 5 分钟的请求并支持视频流转发。

## Windows 本地开发

继续使用 Go/Air 和 Vite。媒体库根目录默认是项目 `data/media`，可在 `.env` 用 `STRM_ROOT` 修改；Docker 中固定为 `/strm`，不受 Windows 路径影响。Telegram Bot 也通过网页配置。
