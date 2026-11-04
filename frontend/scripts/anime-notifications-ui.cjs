const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
  const page = await context.newPage(), errors = [], calls = []
  page.on('pageerror', error => errors.push(error.message)); page.setDefaultTimeout(10000)
  const defaultTemplate = '{icon} <b>动漫订阅 · {event}</b>\n\n<b>{title}</b>\n🎞 集数：{episode}\n🏷 字幕组：{group}\n\n<blockquote>{resource}</blockquote>\n\n📁 <code>{path}</code>\n🕒 {time}'
  let settings = { revision: 1, botId: '', downloadComplete: false, organized: true, failed: true, template: '', imageMode: 'cover', imageUrl: '' }, conflict = false, writes = 0, legacyBackend = false, legacyPreview = false
  const samplePhoto = 'https://images.example.com/covers/anime.jpg'
  await page.route('https://images.example.com/**', route => route.fulfill({ contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="420" height="260"><rect width="420" height="260" fill="#dbeafe"/><rect x="30" y="30" width="360" height="200" rx="16" fill="#bfdbfe"/><text x="210" y="140" text-anchor="middle" font-size="24" fill="#1e40af">Anime Cover</text></svg>' }))
  const bot = { id: 'anime-bot', name: '追番 Bot', configured: true, enabled: true, allowedChatIDs: '42', notificationChatID: '-10042', revision: 1, connected: true, identity: { id: 123, first_name: '追番', username: 'anime_bot' } }
  const values = { icon: '✅', event: '整理完成', title: '葬送的芙莉莲', episode: '第 07 集', group: '示例字幕组', resource: '[示例字幕组] 葬送的芙莉莲 - 07 [1080p]', path: '/动漫/葬送的芙莉莲/Season 01', time: '2026-11-05 20:30:00 +08:00' }
  const html = (template, event = 'organized') => (template || defaultTemplate).replace(/\{([a-z_]+)\}/g, (_, key) => key === 'event' ? { organized: '整理完成', downloaded: '下载完成', failed: '异常提醒' }[event] : values[key] || '示例')
  const config = () => ({ settings, defaultTemplate, maxTemplateLength: 2800, variables: [{ name: 'title', label: '动漫名称' }, { name: 'episode', label: '发布集数' }], preview: { html: html(settings.template), photoUrl: settings.imageMode === 'none' ? '' : settings.imageMode === 'custom' ? settings.imageUrl : samplePhoto } })
  await page.route('**/api/**', async route => {
    const request = route.request(), endpoint = new URL(request.url()).pathname, method = request.method()
    calls.push({ endpoint, method })
    let data = {}, status = 200
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (endpoint === '/api/admin/telegram/anime-notifications/preview') {
      const body = request.postDataJSON()
      if (body.template.includes('{unknown}')) { status = 400; data = { error: '通知模板格式错误：未知变量 {unknown}' } }
      else if (body.imageMode === 'custom' && !body.imageUrl.startsWith('https://images.example.com/')) { status = 400; data = { error: '请填写 Telegram 可访问的公开图片 URL' } }
      else data = legacyPreview ? { html: html(body.template, body.event) } : { html: html(body.template, body.event), photoUrl: body.imageMode === 'none' ? '' : body.imageMode === 'custom' ? body.imageUrl : samplePhoto }
    } else if (endpoint === '/api/admin/telegram/anime-notifications') {
      if (method === 'PUT') {
        if (conflict) { conflict = false; settings.revision++; status = 409; data = { error: '通知配置已变化，请保留输入并重新读取' } }
        else { const body = request.postDataJSON(); assert.equal(body.revision, settings.revision); settings = { ...body, revision: body.revision + 1 }; writes++; data = config() }
      } else {
        data = config()
        if (legacyBackend) { data.settings = { ...settings }; delete data.settings.imageMode; delete data.preview.photoUrl }
      }
    } else if (endpoint === '/api/admin/telegram/bots') data = { bots: [bot] }
    else if (endpoint === '/api/admin/telegram/modules') data = { modules: [{ id: 'anime_notifications', name: '动漫订阅通知', description: '发送动漫下载、整理与异常提醒。', assignedBotId: settings.botId }] }
    else if (endpoint.endsWith('/rss-settings')) data = { revision: 1, pollMinutes: 15, autoArchive: true, inactiveArchiveDays: 60 }
    else if (endpoint.endsWith('/subscriptions')) data = { items: [], total: 0, page: 1, pageSize: 20 }
    else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
    return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
  })
  try {
    await page.goto(origin + '/anime/settings?tab=notifications')
    const panel = page.getByRole('region', { name: 'Telegram 通知设置', exact: true })
    const editor = page.getByRole('textbox', { name: '通知消息模板' }), save = page.getByRole('button', { name: '保存通知设置', exact: true })
    await panel.locator('.ant-bubble').getByText('动漫订阅 · 整理完成', { exact: true }).waitFor()
    await page.getByRole('img', { name: '通知图片预览', exact: true }).waitFor()
    assert.equal(await save.isDisabled(), true)
    await page.getByLabel('推送 Bot', { exact: true }).selectOption(bot.id)
    await page.getByRole('checkbox', { name: /下载完成/ }).check()
    await save.click(); await panel.getByText('通知设置已保存', { exact: true }).waitFor()
    assert.equal(settings.botId, bot.id); assert.equal(settings.downloadComplete, true); assert.equal(settings.template, '')
    await page.getByLabel('通知图片', { exact: true }).selectOption('custom')
    await page.getByLabel('图片 URL', { exact: true }).fill('http://127.0.0.1/private.jpg')
    await panel.getByText('请填写 Telegram 可访问的公开图片 URL', { exact: true }).waitFor()
    assert.equal(await save.isDisabled(), true)
    await page.getByLabel('图片 URL', { exact: true }).fill('https://images.example.com/custom.jpg')
    await page.getByRole('img', { name: '通知图片预览', exact: true }).waitFor()
    assert.equal(await page.getByRole('img', { name: '通知图片预览', exact: true }).getAttribute('src'), 'https://images.example.com/custom.jpg')
    await page.getByLabel('通知图片', { exact: true }).selectOption('none')
    await panel.locator('.ant-bubble').waitFor()
    assert.equal(await page.getByRole('img', { name: '通知图片预览', exact: true }).count(), 0)
    await page.getByLabel('通知图片', { exact: true }).selectOption('cover')
    await page.getByRole('img', { name: '通知图片预览', exact: true }).waitFor()
    await editor.fill('<b>追番更新</b>\n{title}')
    await panel.locator('.ant-bubble').getByText('追番更新', { exact: true }).waitFor()
    await page.getByLabel('预览通知事件').selectOption('failed')
    await editor.fill('{icon} <b>{event}</b>\n{title}')
    await panel.locator('.ant-bubble').getByText('异常提醒', { exact: true }).waitFor()
    await editor.fill('{unknown}')
    await panel.getByText('通知模板格式错误：未知变量 {unknown}', { exact: true }).waitFor()
    assert.equal(await save.isDisabled(), true); assert.equal(await editor.inputValue(), '{unknown}')
    await editor.fill('<b>手动模板</b>\n')
    await panel.locator('.ant-variables button').filter({ hasText: '{title}' }).click()
    assert.match(await editor.inputValue(), /\{title\}/)
    await panel.locator('.ant-bubble b').getByText('手动模板').waitFor()
    conflict = true; await save.click()
    await panel.getByText(/通知配置已变化/).waitFor()
    await page.getByRole('button', { name: '读取最新版本，保留输入' }).click()
    await panel.getByText('已读取最新版本，本次输入已保留。请核对后保存。', { exact: true }).waitFor()
    assert.match(await editor.inputValue(), /手动模板/)
    await save.click(); await panel.getByText('通知设置已保存', { exact: true }).waitFor()
    await page.getByRole('button', { name: '恢复默认', exact: true }).click()
    await panel.locator('.ant-bubble').getByText('动漫订阅 · 异常提醒', { exact: true }).waitFor()
    await page.getByRole('button', { name: 'RSS 设置', exact: true }).click()
    await page.getByRole('button', { name: 'Telegram 通知', exact: true }).click()
    assert.equal(await editor.inputValue(), defaultTemplate)
    await page.getByRole('link', { name: '我的订阅', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: '继续编辑', exact: true }).click()
    assert.equal(new URL(page.url()).pathname, '/anime/settings')
    await save.click(); await panel.getByText('通知设置已保存', { exact: true }).waitFor()
    await fs.mkdir('test-results', { recursive: true })
    for (const width of [1440, 390, 320]) {
      await page.setViewportSize({ width, height: width === 1440 ? 1000 : 844 })
      for (const dark of [false, true]) {
        await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `页面溢出 ${width}`)
        await page.screenshot({ path: `test-results/anime-notifications-${width}-${dark ? 'dark' : 'light'}.png`, fullPage: true })
      }
    }
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.getByRole('link', { name: '管理 Bot', exact: true }).click()
    await page.getByRole('link', { name: '编辑通知模板', exact: true }).click()
    await panel.locator('.ant-bubble').waitFor()
    await editor.fill('<b onclick="window.__notificationXSS=1">预览</b><img src=x onerror="window.__notificationXSS=1"><script>window.__notificationXSS=1</script>')
    await panel.locator('.ant-bubble').getByText('预览', { exact: true }).waitFor()
    assert.equal(await page.evaluate(() => window.__notificationXSS), undefined)
    assert.equal(await panel.locator('.ant-bubble [onclick], .ant-bubble img, .ant-bubble script').count(), 0)
    legacyPreview = true
    await editor.fill('<b>旧接口检测</b>')
    await panel.getByText('后台尚未提供图片预览，请更新并重启后端。', { exact: true }).waitFor()
    assert.equal(await save.isDisabled(), true)
    legacyPreview = false; legacyBackend = true
    await page.reload()
    await panel.getByText('当前后台仍为旧版本，图片通知尚未生效。请更新并重启后端，再重新读取设置。', { exact: true }).waitFor()
    assert.equal(await editor.count(), 0)
    legacyBackend = false
    await panel.getByRole('button', { name: '重新读取通知设置', exact: true }).click()
    await panel.locator('.ant-bubble').waitFor()
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed: ['Bot 与事件设置保存', '自动封面、自定义图片、纯文字及无效图片地址', '三种事件预览与变量插入', '无效模板禁止保存', '并发冲突保留输入并重读版本', '切换设置保留草稿且离开需确认', '1440/390/320 深浅主题无溢出', 'Bot 模块直达模板', '预览移除可执行 HTML', '旧后台与旧预览接口明确提示更新且禁止保存'], writes, apiCalls: calls.length, errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
