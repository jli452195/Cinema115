// All API calls are mocked; this test does not contact Telegram or save live settings.
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
      { id: 'share_notifications', name: '115 分享 STRM', description: '接收分享链接并发送处理结果。', assignedBotId: '' },
      { id: 'cloud_download:target-xx', kind: 'cloud_download', name: '磁力/ED2K 转存：转存 XX', description: '将白名单用户提交的链接转存到指定目录。', assignedBotId: '' },
      { id: 'cloud_download:target-xc', kind: 'cloud_download', name: '磁力/ED2K 转存：转存 XC', description: '将白名单用户提交的链接转存到指定目录。', assignedBotId: '' },
      { id: 'follow_notifications', name: '追更通知', description: '通知管理员新的追更申请与处理结果。', assignedBotId: '' },
      { id: 'pool_notifications', name: '播放池健康通知', description: '报告动态播放池账号的异常状态。', assignedBotId: '' },
      { id: 'share_transfer:target-anime', kind: 'share_transfer', name: '115 链接转存：动画', description: '接收到 /动画。', assignedBotId: '' },
      { id: 'share_transfer:target-movie', kind: 'share_transfer', name: '115 链接转存：电影', description: '接收到 /电影。', assignedBotId: '' },
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
        bots.push({ id, name: body.name, configured: false, enabled: false, allowedChatIDs: '', notificationChatID: '', identity: { id: 0, first_name: '', username: '' }, revision: 1, running: false, connected: false, lastError: '' })
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
        const item = modules.find(value => value.id === decodeURIComponent(endpoint.split('/').at(-1)))
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
    assert.equal(await page.locator('.telegram-module-card').count(), 7)
    assert.equal(await page.getByRole('heading', { name: '115 分享 STRM', exact: true }).count(), 1)

    const shareModule = page.locator('.telegram-module-card').filter({ hasText: '115 分享 STRM' })
    const poolList = page.getByRole('region', { name: '待分配功能列表', exact: true })
    const poolPanel = page.getByRole('complementary', { name: '待分配功能', exact: true })
    const poolGeometry = () => poolPanel.evaluate(panel => {
      const list = panel.querySelector('.module-pool-content'), header = panel.querySelector('header'), footer = panel.querySelector('footer')
      return { height: panel.getBoundingClientRect().height, listHeight: list.clientHeight, contentHeight: list.scrollHeight, scroll: list.scrollTop, headerTop: header.getBoundingClientRect().top, footerTop: footer.getBoundingClientRect().top }
    })
    const beforeScroll = await poolGeometry()
    assert.equal(beforeScroll.height <= 641 && beforeScroll.contentHeight > beforeScroll.listHeight, true, 'Desktop pending features must scroll within a bounded panel.')
    await poolList.hover()
    await page.mouse.wheel(0, 350)
    await page.waitForFunction(() => document.querySelector('.module-pool-content').scrollTop > 0)
    const afterScroll = await poolGeometry()
    assert.equal(Math.abs(afterScroll.headerTop - beforeScroll.headerTop) < 1 && Math.abs(afterScroll.footerTop - beforeScroll.footerTop) < 1, true, 'Only the feature list should move; the panel header and footer must stay visible.')
    await poolList.evaluate(list => { list.scrollTop = 0 })
    const dragData = await page.evaluateHandle(() => new DataTransfer())
    await shareModule.dispatchEvent('dragstart', { dataTransfer: dragData })
    const listBounds = await poolList.boundingBox()
    await poolList.dispatchEvent('dragover', { dataTransfer: dragData, clientY: listBounds.y + listBounds.height - 4 })
    await page.waitForFunction(() => document.querySelector('.module-pool-content').scrollTop > 100)
    await shareModule.dispatchEvent('dragend', { dataTransfer: dragData })
    await poolList.evaluate(list => { list.scrollTop = 0 })
    await dragData.dispose()
    const firstBot = page.locator('.telegram-bot-card').filter({ hasText: 'Bot One' })
    rejectNextAssignment = true
    await shareModule.dragTo(firstBot)
    const conflictToast = page.locator('.toast-card[role="alert"]').filter({ hasText: '此功能刚刚分配给其他 Bot' })
    await conflictToast.waitFor()
    const toastPosition = await conflictToast.evaluate(card => {
      const bounds = card.closest('.toast-container').getBoundingClientRect()
      return { top: bounds.top, right: document.documentElement.clientWidth - bounds.right }
    })
    assert.equal(toastPosition.top >= 0 && toastPosition.top <= 32 && toastPosition.right >= 8 && toastPosition.right <= 32, true, `Global notices must appear in the top-right corner: ${JSON.stringify(toastPosition)}`)
    await conflictToast.getByRole('button', { name: '关闭提示' }).click()
    assert.equal(modules[0].assignedBotId, '', 'A server-side conflict must leave the feature unassigned in the UI.')
    await shareModule.dragTo(firstBot)
    await page.getByRole('button', { name: '解除115 分享 STRM分配' }).waitFor()
    assert.equal(modules[0].assignedBotId, 'bot-one')
    assert.equal(await firstBot.locator('input[role="switch"]').count(), 0, 'Assigned modules must not expose a separate enable switch.')
    assert.equal(await page.locator('.telegram-module-card').filter({ hasText: '115 分享 STRM' }).count(), 1, 'An assigned feature must leave the pending pool.')
    assert.equal(await page.locator('.telegram-bot-card').filter({ hasText: 'Bot Two' }).getByText('115 分享 STRM').count(), 0, 'A feature cannot appear on a second Bot.')
    assert.equal(calls.filter(call => call.endpoint === '/api/admin/telegram/modules/share_notifications' && call.method === 'PUT').length, 2, 'A conflict response must not stop a later valid assignment.')

    const secondBot = page.locator('.telegram-bot-card').filter({ hasText: 'Bot Two' })
    await firstBot.locator('.module-assigned').filter({ hasText: '115 分享 STRM' }).dragTo(secondBot)
    await secondBot.getByRole('button', { name: '解除115 分享 STRM分配' }).waitFor()
    assert.equal(modules[0].assignedBotId, 'bot-two', 'An assigned feature must move directly between Bot cards.')
    assert.equal(calls.filter(call => call.endpoint === '/api/admin/telegram/modules/share_notifications' && call.method === 'PUT').at(-1).body.expectedBotId, 'bot-one', 'A transfer must send the previous owner for compare-and-swap.')
    await secondBot.locator('.module-assigned').filter({ hasText: '115 分享 STRM' }).dragTo(firstBot)
    await firstBot.getByRole('button', { name: '解除115 分享 STRM分配' }).waitFor()
    assert.equal(modules[0].assignedBotId, 'bot-one', 'A feature must move in either direction without unassigning first.')

    await firstBot.locator('.module-assigned').filter({ hasText: '115 分享 STRM' }).dragTo(page.locator('.module-pool-panel'))
    await page.locator('.telegram-module-card').filter({ hasText: '115 分享 STRM' }).getByRole('button', { name: '选择 Bot' }).waitFor()
    assert.equal(modules[0].assignedBotId, '', 'Dragging an assigned feature to the pool must unassign it.')

    const cloudModule = page.locator('.telegram-module-card').filter({ hasText: '磁力/ED2K 转存：转存 XX' })
    await cloudModule.dragTo(page.locator('.telegram-bot-card').filter({ hasText: 'Bot Two' }))
    await page.waitForTimeout(250)
    await page.getByRole('button', { name: '解除磁力/ED2K 转存：转存 XX分配' }).waitFor()
    assert.equal(modules[1].assignedBotId, 'bot-two', 'Dragging must assign a module to an unconfigured, disabled Bot.')
    const cloudModuleTwo = page.locator('.telegram-module-card').filter({ hasText: '磁力/ED2K 转存：转存 XC' })
    await cloudModuleTwo.dragTo(page.locator('.telegram-bot-card').filter({ hasText: 'Bot Two' }))
    await page.getByRole('button', { name: '解除磁力/ED2K 转存：转存 XC分配' }).waitFor()
    assert.equal(modules[2].assignedBotId, 'bot-two', 'Each named cloud-download target must be independently assignable.')
    const assignedCloud = page.locator('.telegram-bot-card').filter({ hasText: 'Bot Two' }).locator('.module-assigned').first()
    const assignedLayout = await assignedCloud.evaluate(card => {
      const heading = card.querySelector('h3'), summary = card.querySelector('p'), action = card.querySelector('button')
      return { title: heading.textContent, titleBottom: heading.getBoundingClientRect().bottom, summaryTop: summary.getBoundingClientRect().top, cardRight: card.getBoundingClientRect().right, actionRight: action.getBoundingClientRect().right, titleClipped: heading.scrollHeight > heading.clientHeight + 1 }
    })
    assert.equal(assignedLayout.title, '磁力/ED2K 转存：转存 XX')
    assert.equal(assignedLayout.summaryTop >= assignedLayout.titleBottom && assignedLayout.actionRight <= assignedLayout.cardRight && !assignedLayout.titleClipped, true, 'The full feature name must wrap above its directory without crowding the unassign action.')
    await page.locator('.telegram-module-card').filter({ hasText: '追更通知' }).getByRole('button', { name: '选择 Bot' }).click()
    const followModule = page.locator('.telegram-module-card').filter({ hasText: '追更通知' })
    await followModule.getByRole('combobox', { name: '选择使用追更通知的 Bot' }).selectOption('bot-two')
    await followModule.getByRole('button', { name: '分配', exact: true }).click()
    await page.getByRole('button', { name: '解除追更通知分配' }).waitFor()
    assert.equal(modules[3].assignedBotId, 'bot-two', 'Click-to-select assignment must include unconfigured Bots.')

    const shareTransferModule = page.locator('.telegram-module-card').filter({ hasText: '115 链接转存：动画' })
    await shareTransferModule.getByRole('button', { name: '选择 Bot' }).click()
    await shareTransferModule.getByRole('combobox', { name: '选择使用115 链接转存：动画的 Bot' }).selectOption('bot-two')
    await shareTransferModule.getByRole('button', { name: '分配', exact: true }).click()
    await page.getByRole('button', { name: '解除115 链接转存：动画分配' }).waitFor()
    assert.equal(modules[5].assignedBotId, 'bot-two', '115 share transfer must be assignable as a separate Bot module.')
    const secondShareTransfer = page.locator('.telegram-module-card').filter({ hasText: '115 链接转存：电影' })
    await secondShareTransfer.getByRole('button', { name: '选择 Bot' }).click()
    await secondShareTransfer.getByRole('combobox', { name: '选择使用115 链接转存：电影的 Bot' }).selectOption('bot-two')
    await secondShareTransfer.getByRole('button', { name: '分配', exact: true }).click()
    await page.getByRole('button', { name: '解除115 链接转存：电影分配' }).waitFor()
    assert.equal(modules[6].assignedBotId, 'bot-two', 'one Bot must be able to own multiple 115 share destinations.')

    const artifacts = path.join(__dirname, '../test-results')
    await fs.mkdir(artifacts, { recursive: true })
    await page.mouse.move(700, 80)
    await page.getByRole('button', { name: '关闭提示', exact: true }).evaluateAll(buttons => buttons.forEach(button => button.click()))
    await page.evaluate(() => { window.scrollTo(0, 0); document.querySelector('.module-pool-content').scrollTop = 0 })
    await page.screenshot({ path: path.join(artifacts, 'telegram-bot-modules-1440-light.png'), animations: 'disabled' })
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.screenshot({ path: path.join(artifacts, 'telegram-bot-modules-1440-dark.png'), animations: 'disabled' })
    await page.evaluate(() => document.documentElement.classList.remove('dark'))

    await page.locator('.telegram-bot-card').filter({ hasText: 'Bot Two' }).getByRole('button', { name: '删除Bot Two' }).click()
    await page.getByRole('dialog', { name: '删除 Bot 配置？' }).getByRole('button', { name: '确认删除' }).click()
    await page.locator('.toast-card[role="status"]').filter({ hasText: '关联功能已退回待分配区' }).waitFor()
    assert.equal(bots.some(bot => bot.id === 'bot-two'), false)
    assert.equal(modules[1].assignedBotId, '', 'Deleting a Bot must explicitly release its features.')
    assert.equal(modules[2].assignedBotId, '', 'Deleting a Bot must release every named cloud-download target.')
    assert.equal(modules[3].assignedBotId, '', 'Deleting a Bot must release every bound feature.')
    assert.equal(modules[5].assignedBotId, '', 'Deleting a Bot must release the share-transfer module.')
    assert.equal(modules[6].assignedBotId, '', 'Deleting a Bot must release each named share-transfer destination.')
    assert.equal(await page.locator('.telegram-module-card').filter({ hasText: '磁力/ED2K 转存：转存 XX' }).getByRole('button', { name: '选择 Bot' }).count(), 1)
    assert.equal(await page.locator('.telegram-module-card').filter({ hasText: '磁力/ED2K 转存：转存 XC' }).getByRole('button', { name: '选择 Bot' }).count(), 1)

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
    const mobilePool = await poolGeometry()
    assert.equal(mobilePool.contentHeight <= mobilePool.listHeight + 1, true, 'Mobile pending features must use page scrolling instead of a nested scroll area.')
    await page.screenshot({ path: path.join(artifacts, 'telegram-bot-modules-390-light.png'), fullPage: true })
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.screenshot({ path: path.join(artifacts, 'telegram-bot-modules-390-dark.png'), fullPage: true })
    await page.waitForTimeout(3600)
    assert.equal(await page.locator('.toast-card').count(), 0, 'Success and error notifications must auto-dismiss.')
    assert.deepEqual(errors, [], `Unexpected browser errors: ${errors.join('; ')}`)
    console.log('PASS: feature names, desktop list scrolling and drag autoscroll, wrapped assigned titles, drag/click assignment, delete release, Bot editing, and mobile page scrolling in both themes.')
    await context.close()
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
