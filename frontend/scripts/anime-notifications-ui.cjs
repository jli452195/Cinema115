const { selectOption, inputValue } = require('./select-menu-helpers.cjs')
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
  let photoStarted, releasePhoto, previewStarted, releasePreview
  await page.route('https://images.example.com/**', async route => {
    if (route.request().url().endsWith('/slow.jpg')) {
      await new Promise(resolve => { releasePhoto = resolve; photoStarted?.() })
    }
    await route.fulfill({ contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="420" height="600"><rect width="420" height="600" fill="#dbeafe"/><rect x="30" y="30" width="360" height="540" rx="16" fill="#bfdbfe"/><text x="210" y="300" text-anchor="middle" font-size="24" fill="#1e40af">Anime Cover</text></svg>' })
  })
  const bot = { id: 'anime-bot', name: '追番 Bot', configured: true, enabled: true, allowedChatIDs: '42', notificationChatID: '-10042', revision: 1, connected: true, identity: { id: 123, first_name: '追番', username: 'anime_bot' } }
  const values = { icon: '✅', event: '整理完成', title: '葬送的芙莉莲', episode: '第 07 集', group: '示例字幕组', resource: '[示例字幕组] 葬送的芙莉莲 - 07 [1080p]', path: '/动漫/葬送的芙莉莲/Season 01', time: '2026-11-05 20:30:00 +08:00' }
  const html = (template, event = 'organized') => (template || defaultTemplate).replace(/\{([a-z_]+)\}/g, (_, key) => key === 'event' ? { organized: '整理完成', downloaded: '下载完成', failed: '异常提醒' }[event] : values[key] || '示例')
  const variables = Object.entries({ icon: '状态图标', event: '事件名称', title: '动漫名称', episode: '发布集数', group: '字幕组', provider: '资源站点', resource: '资源名称', path: '下载或整理目录', reason: '状态说明', time: '发生时间（北京时间）', job_id: '任务编号' }).map(([name, label]) => ({ name, label }))
  const config = () => ({ settings, defaultTemplate, maxTemplateLength: 2800, variables, preview: { html: html(settings.template), photoUrl: settings.imageMode === 'none' ? '' : settings.imageMode === 'custom' ? settings.imageUrl : samplePhoto } })
  await page.route('**/api/**', async route => {
    const request = route.request(), endpoint = new URL(request.url()).pathname, method = request.method()
    calls.push({ endpoint, method })
    let data = {}, status = 200
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (endpoint === '/api/admin/telegram/anime-notifications/preview') {
      const body = request.postDataJSON()
      if (previewStarted) {
        const started = previewStarted; previewStarted = undefined
        await new Promise(resolve => { releasePreview = resolve; started() })
      }
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
    await selectOption(page.getByLabel('推送 Bot', { exact: true }), bot.id)
    await page.getByRole('checkbox', { name: /下载完成/ }).check()
    await save.click(); await panel.getByText('通知设置已保存', { exact: true }).waitFor()
    assert.equal(settings.botId, bot.id); assert.equal(settings.downloadComplete, true); assert.equal(settings.template, '')
    await selectOption(page.getByLabel('通知图片', { exact: true }), 'custom')
    await page.getByLabel('图片 URL', { exact: true }).fill('https://invalid.example.com/unavailable.jpg')
    await panel.getByText('请填写 Telegram 可访问的公开图片 URL', { exact: true }).waitFor()
    assert.equal(await save.isDisabled(), true)
    await page.getByLabel('图片 URL', { exact: true }).fill('https://images.example.com/custom.jpg')
    await page.waitForFunction(() => document.querySelector('.ant-photo')?.getAttribute('src') === 'https://images.example.com/custom.jpg')
    assert.equal(await page.getByRole('img', { name: '通知图片预览', exact: true }).getAttribute('src'), 'https://images.example.com/custom.jpg')
    const slowPhotoStarted = new Promise(resolve => { photoStarted = resolve })
    await page.getByLabel('图片 URL', { exact: true }).fill('https://images.example.com/slow.jpg')
    await slowPhotoStarted
    await panel.locator('.ant-photo-skeleton').waitFor()
    const mediaBefore = await panel.locator('.ant-photo-frame').boundingBox()
    releasePhoto()
    await panel.locator('.ant-photo-skeleton').waitFor({ state: 'detached' })
    const mediaAfter = await panel.locator('.ant-photo-frame').boundingBox()
    assert.ok(Math.abs(mediaBefore.height - mediaAfter.height) < 1, '图片加载不应改变封面区域高度')
    const previousHTML = await panel.locator('.ant-bubble').innerHTML()
    const previousHeight = await panel.locator('.ant-sim-card').evaluate(card => card.getBoundingClientRect().height)
    const slowPreviewStarted = new Promise(resolve => { previewStarted = resolve })
    await editor.fill('<b>保留上一版预览</b>\n' + '长文本预览\n'.repeat(100))
    await slowPreviewStarted
    assert.equal(await panel.locator('.ant-bubble').innerHTML(), previousHTML, '新请求完成前保留上一版预览')
    assert.ok(Math.abs(await panel.locator('.ant-sim-card').evaluate(card => card.getBoundingClientRect().height) - previousHeight) < 1, '等待响应期间应保持原消息尺寸')
    releasePreview()
    await panel.locator('.ant-bubble').getByText('保留上一版预览', { exact: true }).waitFor()
    assert.ok(await panel.locator('.ant-sim-card').evaluate(card => card.getBoundingClientRect().height) > previousHeight, '长文应自然撑开消息卡片，完整展示内容')
    assert.equal(await panel.locator('.ant-preview-pane').evaluate(pane => [pane, ...pane.querySelectorAll('*')].every(element => {
      const style = getComputedStyle(element)
      return !['auto', 'scroll'].includes(style.overflowY) && element.scrollHeight <= element.clientHeight + 1
    })), true, '完整预览不应有内部滚动或被截断的正文')
    await page.clock.install()
    await page.clock.pauseAt(Date.now())
    const previewCalls = () => calls.filter(call => call.endpoint.endsWith('/anime-notifications/preview')).length
    const beforeDebounce = previewCalls()
    await editor.fill('<b>防抖一</b>'); await page.clock.runFor(50)
    await editor.fill('<b>防抖二</b>'); await page.clock.runFor(50)
    await editor.fill('<b>防抖三</b>'); await page.clock.runFor(149)
    assert.equal(previewCalls(), beforeDebounce, '最后一次输入后150ms内不发起预览请求')
    await page.clock.runFor(1)
    await panel.locator('.ant-bubble').getByText('防抖三', { exact: true }).waitFor()
    assert.equal(previewCalls(), beforeDebounce + 1, '连续输入合并为一次预览请求')
    await page.clock.resume()
    await selectOption(page.getByLabel('通知图片', { exact: true }), 'none')
    await page.getByRole('img', { name: '通知图片预览', exact: true }).waitFor({ state: 'detached' })
    assert.equal(await page.getByRole('img', { name: '通知图片预览', exact: true }).count(), 0)
    await selectOption(page.getByLabel('通知图片', { exact: true }), 'cover')
    await page.getByRole('img', { name: '通知图片预览', exact: true }).waitFor()
    await editor.fill('<b>追番更新</b>\n{title}')
    await panel.locator('.ant-bubble').getByText('追番更新', { exact: true }).waitFor()
    await selectOption(page.getByLabel('预览通知事件'), 'failed')
    await editor.fill('{icon} <b>{event}</b>\n{title}')
    await panel.locator('.ant-bubble').getByText('异常提醒', { exact: true }).waitFor()
    await editor.fill('{unknown}')
    await panel.getByText('通知模板格式错误：未知变量 {unknown}', { exact: true }).waitFor()
    assert.equal(await save.isDisabled(), true); assert.equal(await inputValue(editor), '{unknown}')
    await editor.fill('<b>手动模板</b>\n')
    await panel.locator('.ant-variables button').filter({ hasText: '{title}' }).click()
    assert.match(await inputValue(editor), /\{title\}/)
    await panel.locator('.ant-bubble b').getByText('手动模板').waitFor()
    conflict = true; await save.click()
    await panel.getByText(/通知配置已变化/).waitFor()
    await page.getByRole('button', { name: '读取最新版本，保留输入' }).click()
    await panel.getByText('已读取最新版本，本次输入已保留。请核对后保存。', { exact: true }).waitFor()
    assert.match(await inputValue(editor), /手动模板/)
    await save.click(); await panel.getByText('通知设置已保存', { exact: true }).waitFor()
    await page.getByRole('button', { name: '恢复默认', exact: true }).click()
    await panel.locator('.ant-bubble').getByText('动漫订阅 · 异常提醒', { exact: true }).waitFor()
    await page.getByRole('button', { name: 'RSS 设置', exact: true }).click()
    await page.getByRole('button', { name: 'Telegram 通知', exact: true }).click()
    assert.equal(await inputValue(editor), defaultTemplate)
    await page.getByRole('link', { name: '我的订阅', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: '继续编辑', exact: true }).click()
    assert.equal(new URL(page.url()).pathname, '/anime/settings')
    await save.click(); await panel.getByText('通知设置已保存', { exact: true }).waitFor()
    await fs.mkdir('test-results', { recursive: true })
    await page.evaluate(() => window.scrollTo({ top: 300, behavior: 'instant' }))
    await page.setViewportSize({ width: 320, height: 1000 })
    await page.waitForFunction(() => {
      const nav = document.querySelector('.ars-tabs'), active = nav.querySelector('[aria-current="page"]').getBoundingClientRect(), box = nav.getBoundingClientRect()
      return active.left >= box.left - 1 && active.right <= box.right + 1
    })
    assert.ok(Math.abs(await page.evaluate(() => scrollY) - 300) < 2, '分类栏适配宽度时不应改变页面纵向滚动位置')
    const layouts = []
    const saveIsReachable = () => save.evaluate(button => {
      const box = button.getBoundingClientRect()
      const hit = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2)
      return box.width > 0 && box.height > 0 && box.top >= 0 && box.bottom <= innerHeight && box.left >= 0 && box.right <= innerWidth && (hit === button || button.contains(hit))
    })
    for (const width of [2560, 1920, 1440, 1280, 1024, 1023, 768, 390, 320]) {
      await page.setViewportSize({ width, height: width >= 1024 ? 1080 : 844 })
      await page.waitForFunction(() => document.querySelector('.management-layout').classList.contains('has-management-sidebar') === (innerWidth >= 1024))
      await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }))
      for (const dark of [false, true]) {
        await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `页面溢出 ${width}`)
        assert.equal(await page.locator('.ars-tabs').evaluate(nav => {
          const box = nav.getBoundingClientRect(), active = nav.querySelector('[aria-current="page"]').getBoundingClientRect()
          return active.left >= box.left - 1 && active.right <= box.right + 1
        }), true, `当前分类应滚入可见范围 ${width}`)
        const layout = await panel.evaluate(panel => {
          const rect = selector => {
            const box = panel.querySelector(selector).getBoundingClientRect()
            return { x: box.x, y: box.y + scrollY, right: box.right, width: box.width, height: box.height, bottom: box.bottom + scrollY }
          }
          const toolbar = rect('.ant-variables'), image = panel.querySelector('.ant-photo')
          return {
            panel: panel.getBoundingClientRect().width,
            form: rect('.ant-config-card'), editor: rect('.ant-editor-pane'), preview: rect('.ant-preview-pane'),
            footer: rect('.ant-footer'), footerPosition: getComputedStyle(panel.querySelector('.ant-footer')).position,
            saveInForm: panel.querySelector('.ant-config-card').contains(panel.querySelector('button[type="submit"]')),
            stickyPosition: getComputedStyle(panel.querySelector('.ant-preview-pane')).position,
            photoFrame: rect('.ant-photo-frame'),
            previewContent: rect('.ant-preview-content'),
            previewScrolls: [panel.querySelector('.ant-preview-pane'), ...panel.querySelectorAll('.ant-preview-pane *')].some(element => ['auto', 'scroll'].includes(getComputedStyle(element).overflowY)),
            bodyClipped: panel.querySelector('.ant-preview-body').scrollHeight > panel.querySelector('.ant-preview-body').clientHeight + 1,
            messageBackground: getComputedStyle(panel.querySelector('.ant-sim-card')).backgroundColor,
            messageColor: getComputedStyle(panel.querySelector('.ant-bubble')).color,
            variablesFit: [...panel.querySelectorAll('.ant-variables button')].every(button => {
              const box = button.getBoundingClientRect()
              return box.x >= toolbar.x && box.right <= toolbar.right + 1
            }),
            imageLoaded: image.complete && image.naturalHeight > image.naturalWidth,
            imageFit: getComputedStyle(image).objectFit,
          }
        })
        assert.equal(layout.variablesFit, true, `变量按钮溢出 ${width}`)
        assert.equal(layout.imageLoaded, true, `竖版封面未加载 ${width}`)
        assert.equal(layout.imageFit, 'contain', `竖版封面应完整展示 ${width}`)
        assert.ok(Math.abs(layout.photoFrame.width / layout.photoFrame.height - 16 / 9) < .02, `封面应固定16:9 ${width}`)
        assert.ok(layout.previewContent.width <= 401, `预览应适度缩小 ${width}`)
        assert.equal(layout.previewScrolls, false, `预览不应有内部竖向滚动条 ${width}`)
        assert.equal(layout.bodyClipped, false, `预览正文应完整展示 ${width}`)
        const brightness = color => color.match(/\d+/g).slice(0, 3).reduce((sum, part) => sum + Number(part), 0) / 3
        assert.ok(dark ? brightness(layout.messageBackground) < 100 && brightness(layout.messageColor) > 150 : brightness(layout.messageBackground) > 200 && brightness(layout.messageColor) < 100, `图文消息背景与文字应适配主题且可读 ${width}`)
        assert.equal(layout.saveInForm, true, `保存区应位于左侧卡片内部 ${width}`)
        assert.equal(layout.footerPosition, 'static', `保存区不应吸底 ${width}`)
        if (width >= 1024) {
          assert.ok(Math.abs(layout.form.y - layout.preview.y) < 2, `表单与预览应从顶部并排 ${width}`)
          assert.ok(layout.form.width / (layout.form.width + layout.preview.width) >= .58 && layout.form.width / (layout.form.width + layout.preview.width) <= .61, `双栏比例应约60:40 ${width}`)
          assert.equal(layout.stickyPosition, 'sticky')
        } else {
          assert.ok(layout.preview.y >= layout.form.bottom, `lg以下应先表单后预览 ${width}`)
          assert.equal(layout.stickyPosition, 'static')
        }
        if (width >= 1920) {
          assert.ok(layout.panel > 1100, `宽屏通知面板未充分展开 ${width}`)
          assert.ok(layout.editor.width > 500 && layout.preview.width > 400, `编辑或预览过窄 ${width}`)
        }
        if (!dark) layouts.push({ width, panel: Math.round(layout.panel), editor: Math.round(layout.editor.width), preview: Math.round(layout.preview.width), templateTop: Math.round(layout.editor.y) })
        await page.screenshot({ path: `test-results/anime-notifications-${width}-${dark ? 'dark' : 'light'}.png`, fullPage: true })
        await panel.locator('.ant-footer').scrollIntoViewIfNeeded()
        assert.equal(await saveIsReachable(), true, `滚动到左侧Footer后应可保存 ${width}`)
        if (width >= 1024) {
          const sticky = await panel.locator('.ant-preview-pane').evaluate(pane => ({ y: pane.getBoundingClientRect().y, top: parseFloat(getComputedStyle(pane).top) }))
          assert.ok(Math.abs(sticky.y - sticky.top) < 2, `预览应吸附在固定页头下方 ${width}`)
        }
        if (!dark && [1440, 390].includes(width)) await page.screenshot({ path: `test-results/anime-notifications-save-${width}.png` })
        await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }))
      }
    }
    await page.setViewportSize({ width: 1440, height: 720 })
    await page.evaluate(() => document.documentElement.classList.remove('dark'))
    await panel.locator('.ant-footer').scrollIntoViewIfNeeded()
    const compact = await panel.locator('.ant-preview-pane').evaluate(pane => {
      const box = pane.getBoundingClientRect(), content = pane.querySelector('.ant-preview-content').getBoundingClientRect()
      return { width: content.width, top: box.top, bottom: box.bottom, viewport: innerHeight }
    })
    assert.ok(compact.width <= 361, '矮屏应进一步收窄预览卡片')
    assert.ok(compact.top >= 0 && compact.bottom <= compact.viewport, '常规消息在矮屏吸顶后也应完整展示')
    assert.equal(await panel.locator('.ant-chat, .ant-chat-header, .ant-chat-viewport').count(), 0, '仅保留图文消息本体，移除蓝色聊天外框与Bot顶栏')
    await page.screenshot({ path: 'test-results/anime-notifications-compact-720.png' })
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
    console.log(JSON.stringify({ passed: ['Bot 与事件设置保存', '自动封面、自定义图片、纯文字及无效图片地址', '三种事件预览与变量插入', '无效模板禁止保存', '并发冲突保留输入并重读版本', '切换设置保留草稿且离开需确认', '320 至 2560px 深浅主题无溢出，完整变量折行，固定16:9封面且完整显示竖版图片', '左侧Card Footer保存且无跨栏吸底', 'lg起60:40独立双栏与预览吸顶，lg以下上下排列', '图片加载尺寸稳定，长文自然展开且无预览内部滚动', '150ms预览防抖且请求中保留上一版', 'Bot 模块直达模板', '预览移除可执行 HTML', '旧后台与旧预览接口明确提示更新且禁止保存'], layouts, writes, apiCalls: calls.length, errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
