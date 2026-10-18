# 订阅列表、详情与页面恢复（P1-10）

实施日期：2026-10-19。依据：[任务表](anime-subscription-tasks.md)、[设计方案](anime-subscription-plan.md)、[管理员 API](anime-admin-api-implementation.md)、[配置编辑页](anime-subscription-editor-implementation.md)。

## 交付

选番页增加“我的订阅”入口：`/anime/subscriptions`。列表提供全部、草稿、已暂停筛选，名称显式提交搜索，每页 20 项；状态、名称、页码写入 URL。打开详情进入 `/anime/subscriptions/:id`，共用列表路由组件，后台列表保持挂载。直接访问或刷新详情 URL 可重新读取本地配置，外部资料不可用不影响已保存配置的查看。

`AnimeCard` 增加可选状态和摘要插槽，默认发现页表现不变。订阅卡片使用订阅 ID 作为稳定 key，显示低饱和状态、主字幕组及备用数，不添加逐卡详情请求、下载计数或列表入场动画。同一 Bangumi 篇章的不同本地版本仍是独立卡片。

详情复用项目 `floating-surface` 磨砂样式和原生 dialog，独立提取 `AnimeFloatingDialog` 壳组件。展示已保存篇章、配置版本、保存时间、主备来源、发布范围、篇章编号对应、画质和历史策略。主来源首次默认展开，备用由用户展开；全部折叠也可记忆。主操作为进入现有配置编辑页；保存回执增加“查看订阅”。

预选按每个来源合并选中/待核对 ID 去重计数，标为“待核对预选”，没有把表单选择显示成已下载。没有增加启用、检查更新、下载、整理或移除接口。

## 页面恢复

- 列表 URL 保留 `status`、`q`、`page`，详情由路径 ID 表达。跨功能返回“我的订阅”时恢复最后的列表或详情位置。
- 本地只读配置使用最多 40 项的内存缓存，30 秒内直接恢复，过期后先显示缓存再读取。失败保留同查询的旧卡片/配置和读取时间；新查询不会沿用其他筛选结果。
- 每个列表查询分别保存窗口滚动，每个订阅分别保存来源展开与详情滚动；同一详情进入编辑再返回会恢复。原生浮层关闭后恢复触发卡片焦点，直接打开且卡片不在当前页时回落到名称搜索框。
- Bangumi 发现页把当前篇章写入 `subject` query，相关篇章切换只替换当前 URL，不连续增加历史记录。保存最多 32 个详情状态，每个状态的篇章历史最多 32 项，包含简介/关联展开、已展开数量及内部滚动。星期或历史搜索分页、列表滚动、当前关联篇章及返回历史可通过配置页往返恢复。
- 编辑回跳通过 `returnTo` 传递，仅接受 `/anime`、`/anime/subscriptions` 和具体订阅详情路径，拒绝外链、其他业务页及新建/编辑目标。没有缓存未确认的写入或自动重放未保存表单。
- 只有确认配置保存回执或目录保存回执后才使配置读缓存失效；待确认写入仍遵守 P1-09 的原键、原正文重试。返回后重新读取版本、状态、列表成员及分页边界。
- 令牌、用户 ID 或角色变化同步清缓存，正常同身份鉴权刷新不清。卸载的旧详情验证原会话，避免在退出后把详情重新写回缓存。取消、身份及配置代次校验拒绝迟到读取。

内存状态只在当前标签页会话内恢复；整页刷新会根据 URL 还原筛选、页码和打开的详情，滚动与展开重新开始。没有用 localStorage 保存查询结果或私有配置，没有新增周期轮询。

## 实现文件

- `frontend/src/views/AnimeSubscriptionsView.vue`：列表、查询、分页、详情路由及窗口恢复。
- `frontend/src/components/AnimeSubscriptionDialog.vue`、`AnimeFloatingDialog.vue`：只读详情、原生浮层及焦点/滚动。
- `frontend/src/composables/useAnimeSubscriptionBrowser.ts`：有界只读缓存、界面状态、保存失效及回跳校验。
- `frontend/src/utils/animeSubscriptionPresentation.ts`、`styles/animeSubscriptions.css`：本地字段展示与响应式样式。
- `AnimeDiscoveryView.vue`、`AnimeDetailDialog.vue`、`useAnimeDiscovery.ts`：当前关联篇章和配置往返恢复。
- `AnimeSubscriptionEditorView.vue`、`useAnimeSubscriptionEditor.ts`：来源回跳、查看订阅、确认保存后失效缓存。
- `AnimeCard.vue`、动漫 DTO 与 `router.ts`：兼容插槽、只读字段及管理员路由。

## Gemini CLI 讨论与审查

使用 [collaborating-with-gemini-cli 技能](C:/Users/jli45/.codex/skills/collaborating-with-gemini-cli/SKILL.md) 进行只读讨论、代码复核和实际截图审查，代码及接口边界由 Codex 实现和核对。

1. 设计讨论 `c613a044-1bdf-42d9-b04d-f100cb52d522`：采用现有海报卡片、主备摘要、URL 直达详情、显式搜索及先缓存后更新；不增加未实现的下载操作。
2. 代码审查 `7915a90c-4405-40c6-b06b-4ce69f4df860`：修正配置失效取消被显示为业务错误、滚动恢复事件晚于 Vue nextTick 的竞态、全折叠无法记忆；将来源展开判断集中到函数，折叠字段使用 v-show 保留 aria-controls 目标，补充暗色筛选文字与直接详情焦点回落。
3. 恢复代码复核 `b6bd42a1-33e7-4ce4-a958-3d620d09880d`：核对只读回跳、关联 replace、会话清理和恢复。对“initial 更新重置全折叠”的疑虑，检查实际 watch 未监听 initial，并增加直接 URL 加载后全折叠、编辑往返断言，通过；主来源占位只在首次初始化使用，折叠后不会重新展开。对详情加载期间强刷的疑虑，现有按钮明确禁用且请求有 30 秒上限，不存在可达的重复强刷入口，保留现有行为。
4. 实际截图审查 `c18b78dd-90d7-454a-b9b3-a7bb52ae6714`：Gemini 使用 read_file 成功读取四张 PNG，覆盖桌面列表、320px 详情顶部/底部、桌面暗色详情；层级、长标题、间距、对比和底部编辑按钮均无必须修改项。截图使用模拟海报，不代表真实下载验证；未覆盖 iOS 实机滚动惯性和安全区。

截图审查临时副本位于未忽略的根目录，审查后删除；原图保留在被 Git 忽略的 `frontend/test-results/`。

## 验证

在 `frontend` 执行：

```text
npm run test:anime-subscription-browser-ui
npm run test:anime-discovery-ui
npm run test:anime-subscription-ui
npm run build
```

新增浏览器脚本通过 10 组场景：

1. 本地每页 20 项、显式搜索/筛选/URL 分页，无逐卡或上游读取。
2. 列表/详情编辑往返、展开和内部滚动、关闭焦点、跨功能页码及窗口滚动恢复。
3. 刷新保留相同卡片 DOM，缓存过期先显示、读取失败保留结果及重试。
4. 迟到列表响应不覆盖新筛选，不混入其他查询卡片。
5. 确认保存失效缓存，新版本/状态、筛选成员及页码同步。
6. 发现页星期、搜索第 3 页、相关篇章/历史和列表滚动通过配置页恢复。
7. 详情直接访问与刷新、首次默认展开、全折叠编辑往返、404、读取失败及显式恢复。
8. 320px 双列和详情无横向溢出，底部动作可见，键盘焦点、ESC 和滚动锁、桌面/手机明暗截图。
9. 正常鉴权刷新保留缓存，身份切换重读，打开详情时退出不回写旧状态，普通用户不挂载管理页面。
10. 回跳白名单、待核对语义，无下载/启用/整理请求和真实业务写入。

连同原发现页 10 组、编辑页 12 组，共 32 组通过，无页面运行时异常。类型检查及构建通过，保留既有 `libarchive-wasm` 浏览器外置模块和大分包提示。

所有业务 API、封面均被拦截模拟，没有读取真实凭据、写真实订阅或 115 数据。本项未修改 Go、数据库迁移或 SQLite v41。P1-11 后端阶段验证及 P1-12 前端阶段验收继续保持未勾选。
