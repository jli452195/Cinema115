// All API calls are mocked; this test does not contact Telegram or save live settings.
const assert = require('node:assert/strict')
const path = require('node:path')
const os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 950 }, serviceWorkers: 'block' })
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light') })
    const page = await context.newPage(), calls = [], errors = []
    page.setDefaultTimeout(10000)
    page.on('pageerror', error => errors.push(error.message))
    page.on('console', message => { if (message.text().includes('[Vue warn]')) errors.push(message.text()) })

    const bots = [
      { id: 'bot-one', name: 'Bot One', configured: true, enabled: true, allowedChatIDs: '42', notificationChatID: '42', cloudDownload: { enabled: false, accountId: '', targetCID: '', targetPath: '' }, identity: { id: 1, first_name: 'Bot One', username: 'bot_one' }, revision: 1, running: true, connected: true, lastError: '' },
      { id: 'bot-two', name: 'Bot Two', configured: false, enabled: false, allowedChatIDs: '', notificationChatID: '', cloudDownload: { enabled: false, accountId: '', targetCID: '', targetPath: '' }, identity: { id: 0, first_name: '', username: '' }, revision: 1, running: false, connected: false, lastError: '' },
    ]
    const modules = [
      { id: 'share_notifications', name: '分享入口与结果通知', description: '接收分享链接并发送处理结果。', assignedBotId: '' },
      { id: 'cloud_download', name: '磁力与 ED2K 转存', description: '将白名单用户提交的链接转存到主网盘。', assignedBotId: '' },
      { id: 'follow_notifications', name: '追更通知', description: '通知管理员新的追更申请与处理结果。', assignedBotId: '' },
      { id: 'pool_notifications', name: '播放池健康通知', description: '报告动态播放池账号的异常状态。', assignedBotId: '' },
    ]
    let nextId = 0
    let rejectNextAssignment = false

    await page.route('**/api/**', async route => {
      const request = route.request(), endpoint = new URL(request.url()).pathname, method = request.method()
      const body = request.postDataJSON?.() || {}
      calls.push({ endpoint, method, body })
      if (endpoint === '/api/auth/me') return route.fulfill({ json: { user: { id: 'admin', username: 'mock', role: 'admin' } } })
      if (endpoint === '/api/admin/p115/accounts') return route.fulfill({ json: { accounts: [] } })
      if (endpoint === '/api/admin/telegram/bots' && method === 'GET') return route.fulfill({ json: { bots } })
      if (endpoint === '/api/admin/telegram/bots' && method === 'POST') {
        const id = `new-${++nextId}`
        bots.push({ id, name: body.name, configured: false, enabled: false, allowedChatIDs: '', notificationChatID: '', cloudDownload: { enabled: false, accountId: '', targetCID: '', targetPath: '' }, identity: { id: 0, first_name: '', username: '' }, revision: 1, running: false, connected: false, lastError: '' })
        return route.fulfill({ json: { id, bots } })
      }
      if (endpoint.startsWith('/api/admin/telegram/bots/') && method === 'PUT') {
        const id = endpoint.split('/').at(-1), bot = bots.find(item => item.id === id)
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
      if (endpoint === '/api/admin/telegram/modules' && method === 'GET') return route.fulfill({ json: { modules } })
      if (endpoint.startsWith('/api/admin/telegram/modules/') && method === 'PUT') {
        const item = modules.find(value => value.id === endpoint.split('/').at(-1))
        if (!item) return route.fulfill({ status: 404, json: { error: '功能不存在' } })
        if (body.botId && rejectNextAssignment) { rejectNextAssignment = false; return route.fulfill({ status: 409, json: { error: '此功能刚刚分配给其他 Bot，请刷新后重试' } }) }
        if (body.expectedBotId !== item.assignedBotId) return route.fulfill({ status: 409, json: { error: '此功能刚刚转移，请刷新后重试' } })
        item.assignedBotId = body.botId
        return route.fulfill({ json: { modules } })
      }
      return route.fulfill({ status: 404, json: { error: `Unexpected request ${method} ${endpoint}` } })
    })

    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/telegram/bot')
    await page.getByRole('heading', { name: '待分配功能', exact: true }).waitFor()
    assert.equal(await page.locator('.telegram-bot-card').count(), 2)
    assert.equal(await page.locator('.telegram-module-card').count(), 4)
    assert.equal(await page.getByRole('heading', { name: '分享入口与结果通知', exact: true }).count(), 1)

    const shareModule = page.locator('.telegram-module-card').filter({ hasText: '分享入口与结果通知' })
    const firstBot = page.locator('.telegram-bot-card').filter({ hasText: 'Bot One' })
    rejectNextAssignment = true
    await shareModule.dragTo(firstBot)
    const conflictToast = page.locator('.toast-card[role="alert"]').filter({ hasText: '此功能刚刚分配给其他 Bot' })
    await conflictToast.waitFor()
    await conflictToast.getByRole('button', { name: '关闭提示' }).click()
    assert.equal(modules[0].assignedBotId, '', 'A server-side conflict must leave the feature unassigned in the UI.')
    await shareModule.dragTo(firstBot)
    await page.getByRole('button', { name: '解除分享入口与结果通知分配' }).waitFor()
    assert.equal(modules[0].assignedBotId, 'bot-one')
    assert.equal(await firstBot.locator('input[role="switch"]').count(), 0, 'Assigned modules must not expose a separate enable switch.')
    assert.equal(await page.locator('.telegram-module-card').filter({ hasText: '分享入口与结果通知' }).count(), 1, 'An assigned feature must leave the pending pool.')
    assert.equal(await page.locator('.telegram-bot-card').filter({ hasText: 'Bot Two' }).getByText('分享入口与结果通知').count(), 0, 'A feature cannot appear on a second Bot.')
    assert.equal(calls.filter(call => call.endpoint === '/api/admin/telegram/modules/share_notifications' && call.method === 'PUT').length, 2, 'A conflict response must not stop a later valid assignment.')

    const secondBot = page.locator('.telegram-bot-card').filter({ hasText: 'Bot Two' })
    await firstBot.locator('.module-assigned').filter({ hasText: '分享入口与结果通知' }).dragTo(secondBot)
    await secondBot.getByRole('button', { name: '解除分享入口与结果通知分配' }).waitFor()
    assert.equal(modules[0].assignedBotId, 'bot-two', 'An assigned feature must move directly between Bot cards.')
    assert.equal(calls.filter(call => call.endpoint === '/api/admin/telegram/modules/share_notifications' && call.method === 'PUT').at(-1).body.expectedBotId, 'bot-one', 'A transfer must send the previous owner for compare-and-swap.')
    await secondBot.locator('.module-assigned').filter({ hasText: '分享入口与结果通知' }).dragTo(firstBot)
    await firstBot.getByRole('button', { name: '解除分享入口与结果通知分配' }).waitFor()
    assert.equal(modules[0].assignedBotId, 'bot-one', 'A feature must move in either direction without unassigning first.')

    await firstBot.locator('.module-assigned').filter({ hasText: '分享入口与结果通知' }).dragTo(page.locator('.module-pool-panel'))
    await page.locator('.telegram-module-card').filter({ hasText: '分享入口与结果通知' }).getByRole('button', { name: '选择 Bot' }).waitFor()
    assert.equal(modules[0].assignedBotId, '', 'Dragging an assigned feature to the pool must unassign it.')

    const cloudModule = page.locator('.telegram-module-card').filter({ hasText: '磁力与 ED2K 转存' })
    await cloudModule.dragTo(page.locator('.telegram-bot-card').filter({ hasText: 'Bot Two' }))
    await page.waitForTimeout(250)
    await page.getByRole('button', { name: '解除磁力与 ED2K 转存分配' }).waitFor()
    assert.equal(modules[1].assignedBotId, 'bot-two', 'Dragging must assign a module to an unconfigured, disabled Bot.')
    const assignedCloud = page.locator('.telegram-bot-card').filter({ hasText: 'Bot Two' }).locator('.module-assigned')
    const rowCenters = await assignedCloud.evaluate(card => ['h3', 'p', 'button'].map(selector => {
      const element = card.querySelector(selector)
      if (!element) return NaN
      const rect = element.getBoundingClientRect()
      return rect.top + rect.height / 2
    }))
    assert.equal(Math.max(...rowCenters) - Math.min(...rowCenters) < 4, true, 'Assigned module title, summary, and unassign action must share one row.')
    await page.locator('.telegram-module-card').filter({ hasText: '追更通知' }).getByRole('button', { name: '选择 Bot' }).click()
    const followModule = page.locator('.telegram-module-card').filter({ hasText: '追更通知' })
    await followModule.getByRole('combobox', { name: '选择使用追更通知的 Bot' }).selectOption('bot-two')
    await followModule.getByRole('button', { name: '分配', exact: true }).click()
    await page.getByRole('button', { name: '解除追更通知分配' }).waitFor()
    assert.equal(modules[2].assignedBotId, 'bot-two', 'Click-to-select assignment must include unconfigured Bots.')

    await page.locator('.telegram-bot-card').filter({ hasText: 'Bot Two' }).getByRole('button', { name: '删除Bot Two' }).click()
    await page.getByRole('dialog', { name: '删除 Bot 配置？' }).getByRole('button', { name: '确认删除' }).click()
    await page.locator('.toast-card[role="status"]').filter({ hasText: '关联功能已退回待分配区' }).waitFor()
    assert.equal(bots.some(bot => bot.id === 'bot-two'), false)
    assert.equal(modules[1].assignedBotId, '', 'Deleting a Bot must explicitly release its features.')
    assert.equal(modules[2].assignedBotId, '', 'Deleting a Bot must release every bound feature.')
    assert.equal(await page.locator('.telegram-module-card').filter({ hasText: '磁力与 ED2K 转存' }).getByRole('button', { name: '选择 Bot' }).count(), 1)

    await page.getByRole('button', { name: '新增 Bot' }).first().click()
    const editor = page.getByRole('dialog', { name: '编辑 Bot 配置' })
    await editor.getByLabel('配置名称').fill('可编辑 Bot')
    await editor.getByLabel('Bot Token').fill('123456789:abcdefghijklmnopqrstuvwxyz')
    await editor.getByRole('button', { name: '保存配置' }).click()
    await editor.waitFor({ state: 'detached' })
    await page.getByRole('heading', { name: '可编辑 Bot', exact: true }).waitFor()
    assert.equal(bots.some(bot => bot.name === '可编辑 Bot' && bot.configured), true, 'New Bot configuration must save through the editor dialog.')

    await page.setViewportSize({ width: 390, height: 844 })
    await page.waitForTimeout(100)
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true, 'The Bot and module layout must not overflow narrow screens.')
    await page.waitForTimeout(3600)
    assert.equal(await page.locator('.toast-card').count(), 0, 'Success and error notifications must auto-dismiss.')
    assert.deepEqual(errors, [], `Unexpected browser errors: ${errors.join('; ')}`)
    console.log('PASS: Bot cards, exclusive drag/click assignment, unassignment, delete release, Bot editing, and mobile layout.')
    await context.close()
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
