const { selectOption } = require('./select-menu-helpers.cjs')
// 所有接口均使用模拟响应，不连接 Telegram，也不修改正在使用的配置。
const assert = require('node:assert/strict')
const path = require('node:path')
const os = require('node:os')
const fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 950 }, serviceWorkers: 'block' })
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light'); localStorage.setItem('cinema115-management-sidebar', 'collapsed') })
    const page = await context.newPage(), calls = [], errors = []
    page.setDefaultTimeout(10000)
    page.on('pageerror', error => errors.push(error.message))
    page.on('console', message => { if (message.text().includes('[Vue warn]')) errors.push(message.text()) })
    const bots = [
      { id: 'bot-one', name: 'Bot One', configured: true, enabled: true, allowedChatIDs: '42', notificationChatID: '42', identity: { id: 1, first_name: 'Bot One', username: 'bot_one' }, revision: 1, running: true, connected: true, lastError: '' },
      { id: 'bot-two', name: 'Bot Two', configured: false, enabled: false, allowedChatIDs: '', notificationChatID: '', identity: { id: 0, first_name: '', username: '' }, revision: 1, running: false, connected: false, lastError: '' },
    ]
    const modules = [
      { id: 'share_notifications', name: '115 分享 STRM', description: '接收 115 分享链接，生成 STRM 并发送处理结果。', assignedBotId: '' },
      { id: 'cloud_download:target-xx', kind: 'cloud_download', name: '磁力/ED2K 转存：转存 XX', description: '将白名单用户提交的链接转存到指定目录。', assignedBotId: '' },
      { id: 'cloud_download:target-xc', kind: 'cloud_download', name: '磁力/ED2K 转存：转存 XC', description: '将白名单用户提交的链接转存到指定目录。', assignedBotId: '' },
      { id: 'follow_notifications', name: '追更通知', description: '通知管理员新的追更申请与处理结果。', assignedBotId: '' },
      { id: 'pool_notifications', name: '播放池健康通知', description: '报告动态播放池账号的异常状态。', assignedBotId: '' },
      { id: 'share_transfer:target-anime', kind: 'share_transfer', name: '115 链接转存：动画', description: '接收到 /动画。', assignedBotId: '' },
      { id: 'share_transfer:target-movie', kind: 'share_transfer', name: '115 链接转存：电影', description: '接收到 /电影。', assignedBotId: '' },
      { id: 'anime_notifications', name: '动漫订阅通知', description: '通知动漫订阅更新与转存结果。', assignedBotId: '' },
    ]
    let nextId = 0, rejectNextAssignment = false, failNextRead = false
    await page.route('**/api/**', async route => {
      const request = route.request(), endpoint = new URL(request.url()).pathname, method = request.method()
      const body = request.postDataJSON?.() || {}
      calls.push({ endpoint, method, body })
      if (endpoint === '/api/auth/me') return route.fulfill({ json: { user: { id: 'admin', username: 'mock', role: 'admin' } } })
      if (endpoint === '/api/admin/follows') return route.fulfill({ json: { counts: { pending: 0 }, items: [], total: 0 } })
      if (endpoint === '/api/admin/p115/accounts') return route.fulfill({ json: { accounts: [] } })
      if (endpoint === '/api/admin/telegram/bots' && method === 'GET') return route.fulfill({ json: { bots } })
      if (endpoint === '/api/admin/telegram/bots' && method === 'POST') {
        const id = `new-${++nextId}`
        bots.push({ id, name: body.name, configured: false, enabled: false, allowedChatIDs: '', notificationChatID: '', identity: { id: 0, first_name: '', username: '' }, revision: 1, running: false, connected: false, lastError: '' })
        return route.fulfill({ json: { id, bots } })
      }
      if (endpoint.startsWith('/api/admin/telegram/bots/') && method === 'PUT') {
        const bot = bots.find(item => item.id === endpoint.split('/').at(-1))
        if (!bot) return route.fulfill({ status: 404, json: { error: 'Bot 不存在' } })
        Object.assign(bot, { ...body, configured: bot.configured || !!body.token, identity: body.token ? { id: 9, first_name: body.name, username: 'edited_bot' } : bot.identity, revision: bot.revision + 1 })
        return route.fulfill({ json: { bots } })
      }
      if (endpoint.startsWith('/api/admin/telegram/bots/') && method === 'DELETE') {
        const id = endpoint.split('/').at(-1), index = bots.findIndex(item => item.id === id)
        for (const item of modules) if (item.assignedBotId === id) item.assignedBotId = ''
        if (index >= 0) bots.splice(index, 1)
        return route.fulfill({ status: 204, body: '' })
      }
      if (endpoint === '/api/admin/telegram/modules' && method === 'GET') {
        if (failNextRead) { failNextRead = false; return route.fulfill({ status: 503, json: { error: '模拟暂时无法刷新' } }) }
        return route.fulfill({ json: { modules } })
      }
      if (endpoint.startsWith('/api/admin/telegram/modules/') && method === 'PUT') {
        const item = modules.find(value => value.id === decodeURIComponent(endpoint.split('/').at(-1)))
        if (!item) return route.fulfill({ status: 404, json: { error: '功能不存在' } })
        if (rejectNextAssignment) { rejectNextAssignment = false; return route.fulfill({ status: 409, json: { error: '此功能刚刚分配给其他 Bot，请刷新后重试' } }) }
        if (body.expectedBotId !== item.assignedBotId) return route.fulfill({ status: 409, json: { error: '此功能刚刚转移，请刷新后重试' } })
        item.assignedBotId = body.botId
        return route.fulfill({ json: { modules } })
      }
      return route.fulfill({ json: { items: [], total: 0 } })
    })

    const moduleCard = name => page.locator('.telegram-module-card').filter({ has: page.getByRole('heading', { name, exact: true }) })
    const botCard = name => page.locator('.telegram-bot-card').filter({ has: page.getByRole('heading', { name, exact: true }) })
    const poolList = page.getByRole('region', { name: '待分配功能列表', exact: true })
    const poolPanel = page.getByRole('complementary', { name: '待分配功能', exact: true })
    const listNames = root => root.locator('.telegram-module-card h3').allTextContents()
    const putCalls = () => calls.filter(call => call.endpoint.startsWith('/api/admin/telegram/modules/') && call.method === 'PUT')
    const closeToasts = () => page.getByRole('button', { name: '关闭提示', exact: true }).evaluateAll(buttons => buttons.forEach(button => button.click()))
    const refresh = async () => {
      const response = page.waitForResponse(res => new URL(res.url()).pathname === '/api/admin/telegram/modules' && res.request().method() === 'GET')
      await page.getByRole('button', { name: '刷新 Bot 与功能', exact: true }).click()
      await response
      await page.waitForTimeout(100)
    }
    const assign = async (name, botId) => {
      await selectOption(moduleCard(name).getByRole('combobox', { name: `选择使用${name}的 Bot` }), botId)
      await page.getByRole('button', { name: `解除${name}分配`, exact: true }).waitFor()
      await page.waitForTimeout(150)
    }
    // Sortable 使用鼠标回退拖拽，不能使用原生 DataTransfer 伪造事件。
    const drag = async (source, target, edge = 'center', grab = 'handle') => {
      await target.scrollIntoViewIfNeeded()
      await source.scrollIntoViewIfNeeded()
      const handle = source.locator(grab === 'body' ? 'h3' : '[data-drag-handle]')
      const start = await handle.boundingBox(), end = await target.boundingBox()
      assert.ok(start && end, '拖拽源和目标必须可见')
      const x = start.x + start.width / 2, y = start.y + start.height / 2
      const visibleTop = Math.max(80, end.y), visibleBottom = Math.min(page.viewportSize().height - 50, end.y + end.height)
      const targetY = edge === 'top' ? visibleTop + 4 : edge === 'bottom' ? visibleBottom - 4 : (visibleTop + visibleBottom) / 2
      await page.mouse.move(x, y)
      await page.mouse.down()
      await page.mouse.move(x + 10, y + 8, { steps: 3 })
      await page.waitForTimeout(100)
      await page.mouse.move(end.x + end.width / 2, targetY, { steps: 18 })
      await page.waitForTimeout(180)
      await page.mouse.up()
      await page.waitForTimeout(350)
    }

    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/telegram/bot')
    await page.getByRole('heading', { name: '待分配功能', exact: true }).waitFor()
    const receiveRules = page.getByRole('note', { name: 'Bot 链接接收规则', exact: true })
    await receiveRules.waitFor()
    const receiveRulesText = await receiveRules.textContent()
    assert.match(receiveRulesText, /私聊直接发送链接/)
    assert.match(receiveRulesText, /115 分享可选网盘转存或分享 STRM/)
    assert.match(receiveRulesText, /仅一个适用目标且可用时自动处理，多个目标一次选择用途和目录/)
    assert.match(receiveRulesText, /磁力及与 ED2K 的混合消息整批走云下载/)
    assert.match(receiveRulesText, /115 分享请与磁力、ED2K 分开发送/)
    assert.equal(calls.filter(call => !['GET', 'HEAD', 'OPTIONS'].includes(call.method)).length, 0, '阅读接收规则不创建任务或修改分配')
    assert.equal(await page.locator('.telegram-bot-card').count(), 2)
    assert.equal(await page.locator('.telegram-module-card').count(), 8)
    assert.equal(await page.locator('.telegram-module-card[draggable="true"]').count(), 0, '功能卡片统一使用 Sortable 回退拖拽')
    assert.equal(await moduleCard('115 分享 STRM').getByRole('combobox').count(), 1, '多个 Bot 时直接显示选择控件')
    const animeTemplate = moduleCard('动漫订阅通知').getByRole('link', { name: '编辑动漫通知模板', exact: true })
    assert.equal(await animeTemplate.getAttribute('href'), '/anime/settings?tab=notifications')
    assert.equal((await animeTemplate.textContent()).trim(), '模板')

    const beforeScroll = await poolPanel.evaluate(panel => {
      const list = panel.querySelector('.module-pool-content')
      return { height: panel.getBoundingClientRect().height, client: list.clientHeight, scroll: list.scrollHeight }
    })
    assert.ok(beforeScroll.height <= 641 && beforeScroll.scroll > beforeScroll.client, '桌面待分配列表保持独立滚动')
    await poolList.hover()
    await page.mouse.wheel(0, 350)
    await page.waitForFunction(() => document.querySelector('.module-pool-content').scrollTop > 0)
    await poolList.evaluate(list => { list.scrollTop = 0 })

    // 同容器排序不请求后端，键盘和鼠标都修改本地展示顺序。
    const initialOrder = await listNames(poolList), beforeSortPuts = putCalls().length
    await moduleCard('115 分享 STRM').locator('[data-drag-handle]').press('Alt+ArrowDown')
    assert.deepEqual((await listNames(poolList)).slice(0, 2), [initialOrder[1], initialOrder[0]])
    await drag(moduleCard('115 分享 STRM'), moduleCard('磁力/ED2K 转存：转存 XX'), 'top', 'body')
    assert.deepEqual((await listNames(poolList)).slice(0, 2), initialOrder.slice(0, 2))
    assert.equal(putCalls().length, beforeSortPuts, '同容器调整顺序无需更新分配接口')
    await moduleCard('115 分享 STRM').locator('[data-drag-handle]').press('Alt+ArrowDown')
    const savedPoolOrder = await listNames(poolList)
    await refresh()
    assert.deepEqual(await listNames(poolList), savedPoolOrder, '刷新后保留待分配排序')
    await page.reload()
    await page.getByRole('heading', { name: '待分配功能', exact: true }).waitFor()
    assert.deepEqual(await listNames(poolList), savedPoolOrder, '重新加载页面后保留待分配排序')

    // 分配失败且刷新同时失败时，也不能遗失卡片或留下错误归属。
    rejectNextAssignment = true; failNextRead = true
    const beforeFailure = putCalls().length
    await drag(moduleCard('115 分享 STRM'), botCard('Bot One').locator('.bot-module-list'))
    await page.locator('[data-sonner-toast][data-type="error"]').filter({ hasText: '此功能刚刚分配给其他 Bot' }).waitFor()
    assert.equal(modules[0].assignedBotId, '')
    assert.equal(putCalls().length, beforeFailure + 1, '跨容器拖拽失败只发送一次分配请求')
    assert.deepEqual(await listNames(poolList), savedPoolOrder, '失败后恢复完整的原容器顺序')
    assert.equal(await botCard('Bot One').locator('.telegram-module-card').count(), 0)
    await closeToasts()
    await page.waitForFunction(() => !document.querySelector('button[aria-label="刷新 Bot 与功能"]').disabled)
    await refresh()

    await drag(moduleCard('115 分享 STRM'), botCard('Bot One').locator('.bot-module-list'))
    await botCard('Bot One').getByRole('button', { name: '解除115 分享 STRM分配', exact: true }).waitFor()
    assert.equal(modules[0].assignedBotId, 'bot-one')
    await assign('磁力/ED2K 转存：转存 XX', 'bot-one')
    const beforeBotSort = putCalls().length
    await moduleCard('磁力/ED2K 转存：转存 XX').locator('[data-drag-handle]').press('Alt+ArrowUp')
    const savedBotOrder = await listNames(botCard('Bot One'))
    assert.deepEqual(savedBotOrder, ['磁力/ED2K 转存：转存 XX', '115 分享 STRM'])
    assert.equal(putCalls().length, beforeBotSort)
    await refresh()
    assert.deepEqual(await listNames(botCard('Bot One')), savedBotOrder, 'Bot 内顺序在轮询式刷新后保持')

    const beforeTransfer = putCalls().length
    await drag(moduleCard('磁力/ED2K 转存：转存 XX'), botCard('Bot Two').locator('.bot-module-list'))
    await botCard('Bot Two').getByRole('button', { name: '解除磁力/ED2K 转存：转存 XX分配', exact: true }).waitFor()
    assert.equal(putCalls().length, beforeTransfer + 1, '跨 Bot 转移只提交一次')
    assert.deepEqual(putCalls().at(-1).body, { botId: 'bot-two', expectedBotId: 'bot-one' })
    assert.equal(modules[1].assignedBotId, 'bot-two', '未配置或停用 Bot 仍可接受功能分配')
    await drag(moduleCard('磁力/ED2K 转存：转存 XX'), moduleCard('115 分享 STRM'), 'top')
    await botCard('Bot One').getByRole('button', { name: '解除磁力/ED2K 转存：转存 XX分配', exact: true }).waitFor()
    assert.deepEqual(await listNames(botCard('Bot One')), savedBotOrder, '跨容器拖入时保留指定插入位置')

    const beforeRelease = putCalls().length
    await poolList.evaluate(list => { list.scrollTop = 0 })
    await drag(moduleCard('115 分享 STRM'), poolList.locator('.bot-module-list'), 'top')
    await moduleCard('115 分享 STRM').getByRole('combobox').waitFor()
    assert.equal(modules[0].assignedBotId, '')
    assert.equal(putCalls().length, beforeRelease + 1)
    assert.deepEqual(putCalls().at(-1).body, { botId: '', expectedBotId: 'bot-one' })
    await assign('动漫订阅通知', 'bot-one')
    assert.equal(await botCard('Bot One').getByRole('link', { name: '编辑动漫通知模板', exact: true }).count(), 1, '通知模板入口随功能保留在行内')
    await assign('115 链接转存：动画', 'bot-two')
    await assign('115 链接转存：电影', 'bot-two')
    const beforeShareAssignment = putCalls().length
    await assign('115 分享 STRM', 'bot-two')
    assert.equal(putCalls().length, beforeShareAssignment + 1, '已有分享转存目录的 Bot 可以直接分配分享 STRM')
    assert.deepEqual(await listNames(botCard('Bot Two')), ['115 链接转存：动画', '115 链接转存：电影', '115 分享 STRM'])
    assert.equal(modules[0].assignedBotId, modules[5].assignedBotId, '分享 STRM 与分享转存保留同一 Bot 归属')
    const shareSetupHint = '请先保存分享 STRM 播放地址和输出目录'
    modules[0].setupHint = shareSetupHint
    await refresh()
    assert.equal(await moduleCard('115 分享 STRM').getByText('待配置', { exact: true }).getAttribute('title'), '待配置 Token', '未连接 Bot 先提示凭据配置')
    bots[1].configured = true; bots[1].enabled = true
    await refresh()
    await moduleCard('115 分享 STRM').getByText('待配置', { exact: true }).waitFor()
    assert.equal(await moduleCard('115 分享 STRM').getByText('待配置', { exact: true }).getAttribute('title'), shareSetupHint, '优先显示分享 STRM 自身就绪提示')
    assert.equal(modules[5].assignedBotId, 'bot-two', 'STRM 暂未就绪不解除另一个用途的分配')
    delete modules[0].setupHint
    bots[1].configured = false; bots[1].enabled = false
    await moduleCard('115 分享 STRM').getByRole('button', { name: '解除115 分享 STRM分配', exact: true }).click()
    await moduleCard('115 分享 STRM').getByRole('combobox').waitFor()
    await assign('追更通知', 'bot-two')
    assert.equal(await botCard('Bot Two').locator('.telegram-module-card').count(), 3, '一个 Bot 可使用多个独立转存目标和通知功能')

    // 新增动态功能追加到本容器末尾，不打乱已保存的顺序。
    const beforeDynamic = await listNames(poolList)
    modules.push({ id: 'share_transfer:new', kind: 'share_transfer', name: '115 链接转存：新目录', description: '接收到 /新目录。', assignedBotId: '' })
    const ed2kDescription = '输出目录：电影'
    modules.push({ id: 'ed2k_strm:movie', kind: 'ed2k_strm', name: 'ED2K STRM：电影', description: ed2kDescription, assignedBotId: '', ready: true })
    await refresh()
    assert.deepEqual(await listNames(poolList), [...beforeDynamic, '115 链接转存：新目录', 'ED2K STRM：电影'])
    assert.equal(await moduleCard('ED2K STRM：电影').getByText(ed2kDescription, { exact: true }).count(), 1)
    await assign('ED2K STRM：电影', 'bot-one')
    assert.equal(await moduleCard('ED2K STRM：电影').getByText(ed2kDescription, { exact: true }).count(), 1, '分配后仍展示输出目录，发送链接无需命令前缀')
    bots[0].notificationChatID = ''
    await refresh()
    assert.equal(await moduleCard('ED2K STRM：电影').getByText('待配置通知聊天 ID', { exact: true }).count(), 0, 'ED2K 接收模块不要求通知聊天 ID')
    await moduleCard('ED2K STRM：电影').getByRole('button', { name: '解除ED2K STRM：电影分配', exact: true }).click()
    await moduleCard('ED2K STRM：电影').getByRole('combobox').waitFor()
    bots[0].notificationChatID = '42'
    await refresh()
    await page.reload()
    await page.getByRole('heading', { name: '待分配功能', exact: true }).waitFor()
    assert.deepEqual(await listNames(botCard('Bot One')), ['磁力/ED2K 转存：转存 XX', '动漫订阅通知'])

    const artifacts = process.env.TELEGRAM_BOT_UI_ARTIFACTS || path.join(__dirname, '../test-results')
    await fs.mkdir(artifacts, { recursive: true })
    await closeToasts()
    await page.evaluate(() => { window.scrollTo(0, 0); document.querySelector('.module-pool-content').scrollTop = 0 })
    await page.mouse.move(700, 80)
    await page.screenshot({ path: path.join(artifacts, 'telegram-bot-modules-1440-light.png'), animations: 'disabled' })
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.waitForTimeout(300)
    await page.screenshot({ path: path.join(artifacts, 'telegram-bot-modules-1440-dark.png'), animations: 'disabled' })
    await page.evaluate(() => document.documentElement.classList.remove('dark'))
    await page.waitForTimeout(300)
    for (const width of [390, 320]) {
      await page.setViewportSize({ width, height: 844 })
      await page.waitForTimeout(100)
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true, `${width}px 页面不能横向溢出`)
      const mobilePool = await poolList.evaluate(list => ({ client: list.clientHeight, scroll: list.scrollHeight }))
      assert.ok(mobilePool.scroll <= mobilePool.client + 1, '手机端待分配功能随页面滚动')
      await page.screenshot({ path: path.join(artifacts, `telegram-bot-modules-${width}-light.png`), fullPage: true, animations: 'disabled' })
      await page.evaluate(() => document.documentElement.classList.add('dark'))
      await page.waitForTimeout(300)
      await page.screenshot({ path: path.join(artifacts, `telegram-bot-modules-${width}-dark.png`), fullPage: true, animations: 'disabled' })
      await page.evaluate(() => document.documentElement.classList.remove('dark'))
      await page.waitForTimeout(300)
    }
    await page.setViewportSize({ width: 1440, height: 950 })
    // 拖到独立滚动区边缘时自动滚动，避免长列表只能手动滚动后再拖。
    await page.evaluate(() => window.scrollTo(0, 0))
    await poolList.evaluate(list => { list.scrollTop = 0 })
    await poolPanel.scrollIntoViewIfNeeded()
    await page.waitForFunction(() => {
      const list = document.querySelector('.module-pool-content').getBoundingClientRect()
      return list.top >= 80 && list.bottom <= innerHeight
    })
    const autoScrollSource = poolList.locator('.telegram-module-card').first().locator('[data-drag-handle]')
    const sourceBounds = await autoScrollSource.boundingBox(), poolBounds = await poolList.boundingBox(), beforeAutoScroll = putCalls().length
    await page.mouse.move(sourceBounds.x + sourceBounds.width / 2, sourceBounds.y + sourceBounds.height / 2)
    await page.mouse.down()
    await page.mouse.move(sourceBounds.x + 8, sourceBounds.y + 8, { steps: 3 })
    await page.mouse.move(poolBounds.x + poolBounds.width / 2, poolBounds.y + poolBounds.height - 5, { steps: 15 })
    await page.waitForTimeout(100)
    await page.mouse.move(poolBounds.x + poolBounds.width / 2 + 1, poolBounds.y + poolBounds.height - 5)
    await page.waitForFunction(() => document.querySelector('.module-pool-content').scrollTop > 100).catch(async error => {
      error.message += '\n拖拽滚动几何：' + JSON.stringify({ sourceBounds, poolBounds, viewport: page.viewportSize(), scroll: await poolList.evaluate(list => ({ top: list.scrollTop, height: list.clientHeight, full: list.scrollHeight })) })
      throw error
    })
    await page.mouse.up()
    await page.waitForTimeout(350)
    assert.equal(putCalls().length, beforeAutoScroll, '待分配池内自动滚动排序不触发分配请求')
    await botCard('Bot Two').getByRole('button', { name: '删除Bot Two', exact: true }).click()
    await page.getByRole('dialog', { name: '删除 Bot 配置？' }).getByRole('button', { name: '确认删除' }).click()
    await botCard('Bot Two').waitFor({ state: 'detached' })
    assert.equal(modules.filter(item => item.assignedBotId === 'bot-two').length, 0, '删除 Bot 后释放所有功能')
    const share = moduleCard('115 分享 STRM')
    const beforeSingle = putCalls().length
    await share.getByRole('button', { name: '分配给 Bot One', exact: true }).click()
    await botCard('Bot One').getByRole('button', { name: '解除115 分享 STRM分配', exact: true }).waitFor()
    assert.equal(putCalls().length, beforeSingle + 1, '只剩一个 Bot 时点击一次即可分配')
    await botCard('Bot One').getByRole('button', { name: '删除Bot One', exact: true }).click()
    await page.getByRole('dialog', { name: '删除 Bot 配置？' }).getByRole('button', { name: '确认删除' }).click()
    await botCard('Bot One').waitFor({ state: 'detached' })
    await moduleCard('115 分享 STRM').getByRole('button', { name: '新增 Bot', exact: true }).click()
    const editor = page.getByRole('dialog', { name: '编辑 Bot 配置' })
    await editor.getByLabel('配置名称').fill('可编辑 Bot')
    await editor.getByLabel('Bot Token').fill('123456789:abcdefghijklmnopqrstuvwxyz')
    await editor.getByRole('button', { name: '保存配置' }).click()
    await editor.waitFor({ state: 'detached' })
    await page.getByRole('heading', { name: '可编辑 Bot', exact: true }).waitFor()
    assert.ok(bots.some(bot => bot.name === '可编辑 Bot' && bot.configured), '没有 Bot 时可从功能卡片直接创建并配置')
    // 在隔离页面开启触摸模拟：快速滑动用于滚动，长按后才进入排序。
    await page.setViewportSize({ width: 390, height: 844 })
    const touchSession = await context.newCDPSession(page)
    await touchSession.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 })
    await page.reload()
    await page.getByRole('heading', { name: '待分配功能', exact: true }).waitFor()
    const touch = (type, x, y) => touchSession.send('Input.dispatchTouchEvent', { type, touchPoints: type === 'touchEnd' ? [] : [{ x, y, id: 1 }] })
    await poolList.locator('.telegram-module-card').first().scrollIntoViewIfNeeded()
    const swipeBounds = await poolList.locator('.telegram-module-card').first().boundingBox()
    const swipeX = swipeBounds.x + 60, swipeY = Math.min(700, swipeBounds.y + 70), beforeSwipe = await page.evaluate(() => scrollY)
    const beforeTouchPuts = putCalls().length
    await touch('touchStart', swipeX, swipeY)
    for (let step = 1; step <= 5; step++) {
      await touch('touchMove', swipeX, swipeY - step * 25)
      await page.waitForTimeout(20)
    }
    await touch('touchEnd')
    await page.waitForTimeout(250)
    assert.ok(await page.evaluate(() => scrollY) > beforeSwipe + 40, '卡片上快速滑动仍可滚动页面')
    await page.waitForTimeout(1000)
    await page.locator('[role="combobox"][aria-expanded="true"]').evaluateAll(buttons => buttons.forEach(button => button.click()))
    const touchOrder = await listNames(poolList)
    const touchSource = poolList.locator('.telegram-module-card').nth(0), touchTarget = poolList.locator('.telegram-module-card').nth(1)
    await touchTarget.scrollIntoViewIfNeeded()
    await touchSource.scrollIntoViewIfNeeded()
    const touchStartBounds = await touchSource.locator('h3').boundingBox(), touchTargetBounds = await touchTarget.boundingBox()
    const touchX = touchStartBounds.x + touchStartBounds.width / 2, touchY = touchStartBounds.y + touchStartBounds.height / 2
    const touchEndY = touchTargetBounds.y + touchTargetBounds.height - 10
    assert.ok(await page.evaluate(({ x, y }) => !!document.elementFromPoint(x, y)?.closest('.telegram-module-card'), { x: touchX, y: touchY }), '触摸起点必须落在卡片内')
    await touch('touchStart', touchX, touchY)
    await page.waitForTimeout(350)
    for (let step = 1; step <= 12; step++) {
      await touch('touchMove', touchX, touchY + (touchEndY - touchY) * step / 12)
      await page.waitForTimeout(25)
    }
    await touch('touchEnd')
    await page.waitForTimeout(350)
    assert.deepEqual((await listNames(poolList)).slice(0, 2), [touchOrder[1], touchOrder[0]], '长按卡片标题后可通过触摸拖动排序')
    assert.equal(putCalls().length, beforeTouchPuts, '触摸滚动与池内排序均不更改服务端分配')
    await touchSession.detach()
    await page.waitForTimeout(3600)
    assert.equal(await page.locator('[data-sonner-toast]').count(), 0, '操作反馈自动消失')
    assert.deepEqual(errors, [], `浏览器异常：${errors.join('; ')}`)
    console.log('PASS: 私聊链接分流提示、同 Bot 分享转存与分享 STRM 双分配、就绪提示、整卡/手柄/键盘排序持久化、跨容器单次提交、冲突与刷新失败恢复、模板入口、零/单/多 Bot 分配、动态功能追加、边缘自动滚动、触摸快滑/长按及深浅色响应式截图。')
    await context.close()
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
