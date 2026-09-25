# NanShare RE0 登录与验证

管理员在右上角管理菜单进入「RE0 资源搜索」（`/nanshare`），填写 NanShare 地址、用户名和密码，点击「保存并登录」。仅支持账号密码登录，不再提供手动导入令牌。

用户名、密码和令牌使用项目现有 AES-GCM SessionStore 加密保存在数据目录的 `nanshare-config.enc`，API 不返回密码、令牌或远端原始配置。密码框提交后清空，不在浏览器持久保存。旧版仅令牌配置仍可读取，需要用账号密码重新登录一次以启用自动登录；页面显示是否已启用。重新登录验证失败保留原配置。清除登录信息删除本项目保存的密码和会话，不修改 NanShare 账号。

读取会话、配置或搜索遇到认证失效时，使用保存的密码自动登录一次，验证返回账号后加密保存新令牌，原读取请求重试一次。网络故障、解析错误不触发登录循环；并发请求共用已更新的令牌。更新会话不改变搜索编号及提交回执，显式修改配置会使旧搜索失效。STRM 提交前的配置读取可以自动恢复登录，但实际任务提交不会自动重发（包括认证失败或结果不确定），避免重复任务。

登录和重新检查会读取 `/api/session`、`/api/discover/resource/options` 与 `/api/strm/share/config`，分别显示会话、RE0 账号配置、分享 STRM 模式及目录的检查结果。登录成功会独立保存，远端功能未配置时显示对应提示。重启后状态重新验证，不把存在令牌当成在线验证通过。

「测试 RE0 搜索」使用 NanShare 明确配置的 `hdhive_account`，提交影视类型、TMDB 编号和账号名称至 `/api/discover/resource/hdhive`，只返回总资源数和 115 资源数。账号为空时提示在 NanShare 配置，不自动选择其他账号。示例：剧集 `106449` 为《凡人修仙传》。搜索不会调用解锁、转存或 STRM 生成接口。

电影和剧集详情页的「RE0 搜索」仅管理员可见。弹窗只展示 115 资源，支持刷新、按版本名称/清晰度/发布者筛选，展示积分、大小、字幕和有效性。资源操作只保留「分享 STRM」。点击后复用 NanShare 已保存的分享账号绑定；与 NanShare 按钮一致，只有一个生成模式、一个输出目录时直接提交，不再弹出选择框。有多个模式或目录时才显示选择框，并沿用 NanShare 的首项预选。付费资源显示积分，由 NanShare 按 RE0 规则解锁；不会预先解锁或调用一键转存接口。

提交使用 `/api/hdhive/resource/save`，参数为资源 slug、目录索引和转存模式。服务端只接受本次搜索中出现的 115 资源，提交前核对登录会话、RE0 账号和完整远端配置的摘要。缺少明确的 `use_pan115_config` 不允许提交；转存模式还要求 `reshare_pan115_open_config`。这些是 NanShare 功能配置中已有的账号绑定，不使用 Cinema115 本地 115 Cookie，也不回退本地或远端其他账号。浏览器通过 `accountId` 传递已保存的绑定供后端校验，不再要求用户重复选择。远端接口不接受账号覆盖或配置快照，接收任务后的凭据固定及任务执行由 NanShare 负责；请勿在接收和执行任务期间修改远端绑定。

收到成功响应保留「已提交」提示，按钮改为可用的「再次生成 STRM」，执行结果在 NanShare 查看。每次主动提交生成新的 `requestId`（32 位十六进制），同一个编号与相同参数的重试仅返回原回执，不再次调用远端；同一编号不能用于不同参数。提交期间禁用按钮。网络结果未知且无法恢复回执时提供「重试获取提交结果」，沿用原编号；远端结果不确定时允许核对 NanShare 后主动重新提交新任务。浏览器关闭后服务端会在有界超时内完成本次提交并记录结果。

解锁徽标独立于提交回执：`is_unlocked=true` 显示蓝色开锁勾选图标及「已解锁」，未解锁且 `unlock_points=0` 显示绿色开锁图标及「免费」，需积分时显示锁定图标和积分数，缺失积分显示「积分未知」。提交成功后重新搜索读取状态，刷新失败保留成功回执并提示稍后刷新。远端任务异步完成时可手动「刷新搜索」，不会根据提交成功推断已解锁。

搜索结果及按提交编号保存的回执在服务端内存保留 15 分钟，刷新保留搜索会话和回执；每个会话最多保留 256 次提交。重启或缓存到期后需先在 NanShare 核对历史任务。不会保存原始分享链接、Cookie 或远端原始配置。任务提交和解锁状态变化使用模拟远端测试，未消耗积分或创建真实任务。

所有本地 API 均在管理员鉴权下的 `/api/admin/nanshare`：`GET/POST/DELETE /config`、`POST /verify`、`POST /search-test`、`POST /search`、`GET /share-options/:searchId`、`POST /submit`。站点支持 HTTP(S) 和内网地址，禁止携带用户信息、查询参数或路径，HTTP 客户端不跟随重定向以免转发凭据。

验证：后端 `go test ./internal/client/nanshare ./internal/service ./internal/httpapi ./cmd/server`；前端 `npm run build`，运行开发服务后执行 `node scripts/nanshare-login-ui.cjs` 和 `node scripts/re0-search-ui.cjs`。可通过环境变量 `NANSHARE_TEST_URL`、`NANSHARE_TEST_USERNAME`、`NANSHARE_TEST_PASSWORD` 显式开启 `go test ./internal/service -run '^TestNanShareLive$' -v -count=1`，仅执行登录、配置和示例搜索验证。
