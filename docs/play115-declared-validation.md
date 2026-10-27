# play115 完整声明与灰度验证

后续目录核验与上传凭据并行优化及最新实测见 [秒传并行预检与灰度验证](playback-parallel-preflight-validation.md)。

当前已移除主动预热及独立文件准备模式，下文对应覆盖项为当时验收记录；正式播放、下载和请求内并行预检继续保留。

2026-10-06：按用户约定直接使用 play115 中的 SHA1、大小、文件名和源文件 ID，不预先读取主账号元数据。保留格式校验，来源关联不覆盖声明；按 SHA1 + 大小复用已接受片段及目标副本。正式播放目标命中通常只需 SHA1 查询和取链，不检查保存目录。目标未命中则检查目录、准备上传凭据并直接提交秒传。只有 115 要求片段且本地片段及合格副本不能提供时，才按需读取主账号来源；此时合并获取元数据和校验源直链，核对文件 ID、SHA1、大小。

声明暂存于请求上下文；目标身份核验成功后才保存可信资料。秒传失败或目标核验失败不保存可信声明，保留原未确认写入冷却；上传响应不明但只读确认成功时可保存文件资料，不能保存未经明确接受的本次校验片段。来源关联不由外部声明创建。直链及来源映射缓存包含声明大小、名称和文件 ID，避免 STRM 变更误用旧映射。

## 验证

在 backend 目录运行：

```powershell
go test ./... -count=1 -timeout=180s
go test ./internal/service ./internal/httpapi -run 'TestPlay115|TestPickCode|TestRedirect115DeferredDirectoryAuthorization|TestFileReuseDeferredAuthorization' -count=20 -timeout=180s
go vet ./...
go build ./...
```

完整后端回归、20 轮定向测试、vet 和构建通过。覆盖：目标命中两次读取、直接秒传零主账号读取、挑战后才取源、跨 UID 复用已接受片段、文件 ID 冲突、旧来源冲突、失败不保存可信声明、声明大小改变不能复用旧映射、目录检查、权限撤销、文件准备、下载与预热。补充的未确认响应后只读确认成功分支及 play115 目录延迟授权也通过 20 轮测试。HTTP 隔离灰度经过 loopback 入口和动态池授权流程。

## 真实 115 只读灰度

```powershell
$env:CINEMA115_REUSE_CANARY_ROOT = 'D:/code/Cinema115'
try { go test ./internal/service -run '^TestPlay115TargetFirstLiveCanary$' -count=1 -v -timeout=120s } finally { Remove-Item Env:CINEMA115_REUSE_CANARY_ROOT }
```

使用固定管理员小号的已有副本、只读数据库和隔离空缓存。首次 792 ms：SHA1 查询 1 次、取链 1 次；再次 231 ms：取链 1 次。目录检查、主账号读取和云端写入均为 0。未替换或重启运行实例，未进行真实秒传写入或播放器长时间播放。单次耗时不代表平均提速比例；首次遇到挑战时按需读源也可能失去旧预取的重叠收益。race 检查仍受现有编译器环境限制。
