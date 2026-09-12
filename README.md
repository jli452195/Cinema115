# Cinema115

纯 Go 后端与 Vue 3 前端。当前支持管理员登录、115 扫码/Cookie 登录、分享目录分页浏览、STRM 下载和媒体代理。

## NAS 部署（Docker）

生产环境使用根目录 `Dockerfile` 和 `compose.yaml`，多阶段构建后只运行一个容器，由 Go 同时提供前端页面与 API，默认通过 `http://NAS_IP:8115` 访问。根目录 `.env` 配置 NAS 访问地址和账号；`compose.yaml` 使用 `./data:/data` 与 `/vol1/1000/媒体库:/strm` 两条挂载。页面填写 `strm`、`strm2` 等相对目录，在同一个媒体库中生成。完整步骤见 [NAS Docker 部署](docs/docker.md)。

配置 `.env` 并创建挂载目录后执行：

```sh
docker compose config --quiet
docker compose up -d --build
```

## Windows / 本地开发

在项目根目录将 `.env.example` 复制为 `.env`，填写管理员账号、密码和随机 `JWT_SECRET`。配置仅由后端读取。

```powershell
cd backend
go mod tidy
go run ./cmd/server
```

```powershell
cd frontend
npm install
npm run dev
```

已安装 Air 时在 `backend` 执行 `air -c .air.toml`。不要同时运行两个后端占用 3002。

## 分享播放与 Emby

1. 登录管理员后台，在「115 网盘」扫码并确认，等待后端获取 Cookie 并保存成功。
2. 进入首页「分享播放 / STRM」卡片，输入分享链接和提取码。
3. 选择生成目录（例如 `strm` 或 `strm2`），点击「一键生成 STRM」。自动分页读取视频、按配置扫描子目录，直接保存到媒体库中的对应子目录。默认不转存；打开「转存再分享」后先转换为自己的分享。预览和单个下载仍使用原分享。
4. 把 STRM 放进 Emby 媒体库。Emby 必须能够访问 STRM 中的 Cinema115 地址。

根目录 `.env` 可选配置：

```dotenv
# 换成 Emby 和播放器能访问的 Cinema115 地址，不能照抄示例 IP。
PUBLIC_BASE_URL=http://192.168.0.3:3002
# 相对 backend 工作目录；默认保存到项目根目录 data。
DATA_DIR=../data
# 通常通过扫码登录；此项可留空。
P115_COOKIE=
```

STRM 写入固定的 Cinema115 代理地址。每次播放用已登录账户请求最新 115 直链，并转发 Range、HEAD 和媒体响应头；不会落盘保存视频。代理流量经过后端，受后端带宽限制。

新生成的链接格式为 `/api/?share_code=分享码&id=文件ID&receive_code=提取码`，可直接拼接使用，不要求签名、过期时间或管理员 JWT。无提取码的分享使用 `receive_code=`。后台分享管理接口仍需管理员登录。旧的 `/api/stream/share/{token}` 地址继续支持。

新播放链接没有 Cinema115 设置的过期时间，能否播放取决于分享是否有效和后端 115 登录状态。旧路径中的播放凭证仍按原有效期校验。更换 `JWT_SECRET` 会使旧路径凭证失效，也会使原有加密登录会话无法解密。

Cookie 用 AES-GCM 加密保存在 `data/p115-session.enc`，目录已被 Git 忽略。热重启后会恢复；115 撤销或过期的 Cookie 仍需重新扫码。退出 115 登录会移除保存的会话。若设置了 `P115_COOKIE`，重启时会重新导入该值。

「115 网盘」的 Cookie 登录页默认隐藏凭证。留空点击「测试 Cookie」会验证服务器已保存的凭证；填写新值时只测试输入内容，不修改原配置。「验证并保存」在 115 验证通过后才替换凭证。状态区会区分未配置、已连接、已失效和暂时无法验证；网络故障不会被误判为登录失效。

批量生成支持播放服务地址、视频后缀、最小文件大小、是否扫描子目录和起始目录 ID（默认 `0`）配置。预览中可进入文件夹并设为起始目录。页面支持添加、选择、移除多个相对目录选项；移除选项不删除服务器文件。挂载 `/vol1/1000/媒体库:/strm` 后，页面填写 `strm` 对应 NAS 的 `媒体库/strm`，填写 `strm2` 对应 `媒体库/strm2`，不需要分别挂载。子目录自动创建。Windows 开发默认根目录为项目 `data/media`，可用 `.env` 的 `STRM_ROOT` 配置。

生成时直接保留分享的文件夹层级，不加批次目录，不打包 ZIP。重复生成跳过已有普通文件，不覆盖内容；更换播放地址后如需重新生成，可选择新目录。结果显示新增、已存在和过滤数量及实际路径，Emby 可直接读取该目录。选项保存在当前浏览器，分享链接和提取码不持久化到浏览器配置。

`POST /api/admin/p115/share/generate` 使用管理员 JWT 鉴权，接收 `url`、`password`、`cid`、`baseUrl`、`outputDir`、`videoExtensions`、`minSizeMB`、`recursive`，返回 JSON：`directory`、`count`（新增）、`existing`、`skipped`、`total`；页面默认开启 `recursive`。`GET /api/admin/p115/share/generation-config` 返回默认配置和目录的实际路径。单次扫描限制为 5 分钟、500 页、20 层目录、20000 个条目和 10000 个视频，超限需缩小起始目录。扫描失败不写文件；写入阶段失败会保留已完成文件，重试会跳过这些文件。

「转存再分享」开关保存到服务器，供网页和 Telegram 共用。开启后在 115 创建独立临时目录，等待所有子文件完整转存后创建分享，显式设置并确认长期有效，再校验完整分享快照、生成 STRM，最后将临时目录移入回收站并复查分享。不会清空回收站。失败时保留目录及任务记录。网页实时显示阶段、条目数和等待原因，支持停止等待；单个等待阶段最多 90 秒，整个任务最多 5 分钟。

Telegram 已接入可选长轮询接收器。在根目录 `.env` 设置 `TELEGRAM_BOT_TOKEN`、`TELEGRAM_ALLOWED_CHAT_IDS`，在 STRM 页面保存 Bot 生成选项后重启后端。每条消息接收一个分享链接，只处理允许的聊天，并回复生成结果。未配置 Token 时不启动 Bot。完整说明及失败处理见 [转存再分享与 Telegram](docs/reshare.md)。

浏览器不支持所有 MKV、HEVC 或音频格式，此项目不做转码。不兼容的文件可交给 Emby 转码播放。生成 STRM 仅证明播放入口已创建，并不证明当前账户有权获得 115 直链。

## 验证

```powershell
cd backend
go test ./...
go vet ./...
```

```powershell
cd frontend
npm run build
```

测试包含 p115cipher 原实现 RSA 向量、损坏数据、分享分页归一化、播放凭证、Range/HEAD 转发和登录会话加密恢复。运行时无需 Python。

第三方来源与许可证见 `THIRD_PARTY_NOTICES.md`。
