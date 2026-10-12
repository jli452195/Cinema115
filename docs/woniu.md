# 蜗牛（wn4k）搜索

Cinema115 直接访问 `https://www.wn4k.com`，不依赖 pansou 服务、插件框架或 goquery。协议参考为 [pansou/woniu](https://github.com/fish2018/pansou/tree/main/plugin/woniu)，搜索分页、页面结构和资源类型均按当前站点重新实现。

## 配置和入口

在「资源配置 → 蜗牛配置」（`/woniu`）保存站点用户名、密码。服务端登录验证成功并保存后，电影、剧集详情页自动显示「蜗牛搜索」，无需额外启用开关。未配置时不显示入口；清除凭证会清空缓存并隐藏入口。登录失败保留之前成功保存的配置。

配置和搜索接口仅管理员可访问，后端重复校验权限。用户名、密码、会话以现有 SessionStore 的 AES-GCM 机制保存到 `woniu-config.enc`，不返回密码或 Cookie。站点地址固定，不接受前端提供的任意 URL。搜索关键词由后端重新读取 TMDB 信息。

## 搜索和资源

- 先搜索影片名称，无匹配条目时补充原名；不使用 IMDb 编号兜底。
- 搜索请求至少间隔 3.2 秒，可取消等待；详情读取最多 4 路并发。HTTP 200 的站点限流提示也按限流错误处理。
- 第一页使用 `/vodsearch/-------------/?wd=...`，后续使用站点实际分页路径。按站点 ID 追加、去重，最多 4 页、96 个条目。同名、季度或版本由用户核对。
- 网盘类型按 URL 域名识别，保留大小写敏感的提取码。「115离线下载」栏目中的 `115cdn.com/s/...` 是分享链接，不作为磁力云下载处理。
- 115 分享复用现有主账号分享 STRM 流程，转存临时目录固定在根目录（CID 0）。只有校验通过的磁力链接提供 115 云下载；其他网盘支持复制或打开。
- 桌面、手机共用观影飞窗布局和动效；手机默认显示全部条目。下一页失败保留已加载条目，并提供单独的重试提示。
- 成功结果缓存 10 分钟，无可用链接缓存 2 分钟；部分详情失败不缓存。重新搜索成功后清除该影片的旧分页缓存。
- 明确会话失效时，每次搜索最多重新登录一次；限流、安全验证不会触发自动重登录。

## 图片

品牌图标来自用户指定的[官方 SVG](https://www.wn4k.com/template/panlian_dark/images/logo.svg?v=2.3.4)，作为本地静态资源使用。

条目封面通过同源图片代理加载，支持 `/upload/vod/{32位hex}.jpg` 和 `/upload/vod/{日期目录}/{32位hex}.jpg`，包括实际使用的 `20260621-1` 目录。代理只访问固定站点，不携带站点会话或 Cinema115 凭据；校验扩展名、目录、图片签名与大小，按目录隔离缓存。缓存上限 200 MiB，下载最多 2 路并发。

## 灰度验证

后端测试：在 `backend` 运行：

```powershell
go test ./internal/client/woniu ./internal/client/imagecache ./internal/service ./internal/httpapi -run 'Test(Woniu|Gying)' -count=1
go vet ./internal/client/woniu ./internal/client/imagecache ./internal/service ./internal/httpapi
go test ./cmd/server -run '^$'
```

前端测试：Vite 启动后，在 `frontend` 运行：

```powershell
node --test tests/resource-search.test.mjs tests/share-strm-run.test.mjs
node scripts/woniu-search-ui.cjs
node scripts/gying-unavailable-ui.cjs
npm run build
```

UI 脚本拦截全部 API，模拟配置保存、清除、分页、限流、会话失效、部分详情失败、提取码复制、STRM 提交和后台结果。覆盖 1440、390、320 像素及深浅模式、资源品牌图标、观影回归、关闭取消、普通用户无入口。不会创建真实转存、STRM 或云下载任务。

真实站点只读验证已覆盖公开搜索、第二页分页、游客资源遮罩、已登录浏览器详情样本，以及项目现有凭证下《回复术士的重来人生》（站点 ID 15142）搜索：读取 3 个 115 分享，日期目录封面通过本地代理返回 HTTP 200。未自动提交真实站点登录密码，也未执行真实网盘写入。真实凭证的初次登录需在配置页操作；验证码或站点改版可能需要更新客户端。

可选的公开站点只读测试需要显式设置 `WONIU_LIVE_READONLY=1`，再运行 `go test ./internal/client/woniu -run TestWoniuCanary -v`。已登录浏览器 HTML 样本通过 `WONIU_BROWSER_FIXTURES` 指向本地目录；不包含 Cookie，默认不执行。普通测试不依赖外网。
