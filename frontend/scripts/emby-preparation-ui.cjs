const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict'), fs = require('node:fs')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage(), errors = [], writes = [], runIds = []
    page.on('pageerror', e => errors.push(e.message))
    page.on('dialog', d => d.accept())
    let proxy = { enabled: true, debug: false, port: 8097, url: 'http://emby:8096', accountId: 'main', embyPathPrefix: '', directLinkCacheMinutes: 9 }
    let access = { allowDownload: false, publicUrl: '' }, policy = { mode: 'parallel', silenceSeconds: 60 }
    let upload = { config: { userId: 'pool-user', sourceAccountId: 'main', accountId: '', targetCid: '', targetPath: '' }, records: [], running: false }
    let failProxyRead = false, failAccountRead = false, failProxyWrite = '', failAccessWrite = false, runDelay = 0, failRun = false
    await page.route('**/api/**', async route => {
      const req = route.request(), p = new URL(req.url()).pathname; let data = {}, status = 200
      if (req.method() !== 'GET') writes.push({ p, body: req.postDataJSON() })
      if (p === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
      else if (p === '/api/admin/p115/accounts') {
        if (failAccountRead) { status = 500; data = { error: '模拟账号读取失败' } }
        else data = { accounts: [{ id: 'main', primary: true, configured: true, name: '主账号' }, { id: 'pool', primary: false, configured: true, name: '小号 A' }] }
      } else if (p === '/api/admin/member-playback/settings') data = { settings: access }
      else if (p === '/api/admin/member-playback') {
        if (failAccessWrite) { status = 500; data = { error: '模拟访问保存失败' } }
        else { access = req.postDataJSON(); data = { settings: access } }
      } else if (p.endsWith('/device-policy')) { if (req.method() === 'POST') policy = req.postDataJSON(); data = policy }
      else if (p.endsWith('/playback-cache/users')) data = {
        users: [{ Id: 'pool-user' }, { Id: 'personal' }, { Id: 'disabled', Policy: { IsDisabled: true } }, { Id: 'sub' }, { Id: 'suspended' }],
        members: [{ embyId: 'pool-user', embyName: '池用户', source: 'assigned' }, { embyId: 'personal', source: 'personal' }, { embyId: 'disabled', source: 'assigned' }, { embyId: 'sub', source: 'assigned', playbackOnly: true }, { embyId: 'suspended', source: 'assigned', suspended: true }],
      }
      else if (p.endsWith('/instant-upload/run')) {
        runIds.push(req.postDataJSON().id)
        await new Promise(resolve => setTimeout(resolve, runDelay))
        if (failRun) { status = 500; data = { error: '模拟秒传结果不确定' } }
        else {
          data = { id: req.postDataJSON().id, name: '测试影片.mkv', accountId: 'pool', targetCid: '123', status: 'success', message: '模拟秒传成功', durationMs: 2000, startedAt: new Date().toISOString() }
          upload.records = [data, ...upload.records.filter(r => r.id !== data.id)]
        }
      } else if (p.endsWith('/instant-upload')) { if (req.method() === 'POST') upload.config.userId = req.postDataJSON().userId; data = upload }
      else if (p.endsWith('/proxy')) {
        if (req.method() === 'POST') {
          if (failProxyWrite) { status = 400; data = { error: failProxyWrite } } else proxy = req.postDataJSON()
        } else if (failProxyRead) { status = 500; data = { error: '模拟反代读取失败' } }
        if (status === 200) data = { config: proxy, listening: proxy.enabled, error: '' }
      }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const base = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    await page.goto(base + '/emby/proxy')
    const saveProxy = page.getByRole('button', { name: '保存反代配置', exact: true }), port = page.getByLabel('反代监听端口', { exact: false })
    await saveProxy.waitFor()
    await page.locator('#proxy-runtime-title').getByText('反代正在监听', { exact: true }).waitFor()
    await port.fill('8098')
    assert.equal(await page.locator('.proxy-entry code').innerText(), 'http://127.0.0.1:8097')
    await page.getByRole('button', { name: '刷新状态', exact: true }).click()
    await page.getByRole('button', { name: '刷新状态', exact: true }).waitFor()
    assert.equal(await port.inputValue(), '8098')
    failProxyWrite = '请先保存同一 Emby 服务的媒体访问设置'
    await saveProxy.click(); await page.getByRole('button', { name: '前往保存访问设置', exact: true }).waitFor()
    assert.equal(writes.length, 1); assert.ok(writes[0].p.endsWith('/emby/proxy'))
    assert.equal(await port.inputValue(), '8098')
    assert.equal(await page.locator('.proxy-entry code').innerText(), 'http://127.0.0.1:8097')
    await page.getByRole('button', { name: '前往保存访问设置', exact: true }).click()
    assert.equal(await page.evaluate(() => document.activeElement.closest('#proxy-access') !== null), true)
    const publicUrl = page.getByLabel('提供给用户的 Emby 连接地址（选填）', { exact: true }), saveAccess = page.getByRole('button', { name: '保存访问设置', exact: true })
    await publicUrl.fill('https://emby.example.com')
    failAccessWrite = true
    await saveAccess.click(); await page.getByText('模拟访问保存失败', { exact: true }).waitFor()
    assert.equal(await publicUrl.inputValue(), 'https://emby.example.com'); assert.equal(access.publicUrl, '')
    failAccessWrite = false
    await saveAccess.click(); await page.getByText('访问设置已保存。', { exact: true }).waitFor()
    assert.equal(proxy.port, 8097); assert.equal(access.publicUrl, 'https://emby.example.com')
    failProxyWrite = ''
    await saveProxy.click(); await page.getByText('反代配置已保存，入口正在监听。请在播放器中验证实际播放。', { exact: true }).waitFor()
    assert.equal(proxy.port, 8098)
    assert.equal(await page.locator('.proxy-entry code').innerText(), 'http://127.0.0.1:8098')
    const before = writes.length
    await port.fill('65536'); await saveProxy.click()
    assert.equal(await port.evaluate(el => el.validity.valid), false); assert.equal(writes.length, before)
    await port.fill('8098')
    await page.getByRole('switch', { name: '启用 302 并接管播放', exact: true }).uncheck({ force: true })
    await page.getByText('待保存后停止入口', { exact: true }).waitFor()
    assert.equal(await page.locator('#proxy-runtime-title').innerText(), '反代正在监听')
    await page.getByRole('switch', { name: '启用 302 并接管播放', exact: true }).check({ force: true })
    const upstream = page.getByLabel('Emby 原服务器地址', { exact: false })
    await upstream.fill(''); await saveProxy.click()
    assert.equal(await upstream.evaluate(el => el.validity.valid), false); assert.equal(writes.length, before)
    await upstream.fill('http://emby:8096')
    await page.locator('.proxy-advanced > summary').click()
    const cacheMinutes = page.getByLabel('直链缓存时间（分钟）', { exact: false })
    await cacheMinutes.fill('31')
    await page.locator('.proxy-advanced > summary').click()
    await saveProxy.click()
    assert.equal(await page.locator('.proxy-advanced').evaluate(el => el.open), true, 'invalid advanced fields must be revealed')
    assert.equal(writes.length, before)
    await cacheMinutes.fill('9')
    await page.locator('.proxy-advanced > summary').click()
    // Each view/theme should retain clear headers, no horizontal overflow, usable dialog.
    fs.mkdirSync(path.join(__dirname, '../test-results'), { recursive: true })
    for (const theme of ['light', 'dark']) {
      await page.evaluate(theme => document.documentElement.classList.toggle('dark', theme === 'dark'), theme)
      for (const [width, height] of [[1440,1000],[1024,900],[768,1024],[390,844],[320,740],[844,390]]) {
        await page.setViewportSize({ width, height })
        await page.evaluate(() => window.scrollTo(0, 0))
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, theme + width)
        assert.ok(await page.locator('.management-heading h1').evaluate(el => el.getBoundingClientRect().width) > 200)
        await page.getByRole('button', { name: '秒传验证', exact: true }).click()
        const dlg = page.getByRole('dialog', { name: '单文件秒传验证', exact: true })
        await page.waitForFunction(() => getComputedStyle(document.querySelector('.proxy-upload-dialog')).opacity === '1')
        assert.equal(await dlg.isVisible(), true)
        assert.ok(await dlg.evaluate(el => el.getBoundingClientRect().width) <= width)
        assert.equal(await dlg.evaluate(el => el.scrollWidth > el.clientWidth), false)
        await page.screenshot({ path: path.join(__dirname, '../test-results/proxy-' + theme + '-' + width + '.png') })
        await page.getByRole('button', { name: '关闭秒传验证', exact: true }).click()
        assert.equal(await page.evaluate(() => document.body.style.overflow), '')
        await page.screenshot({ path: path.join(__dirname, '../test-results/proxy-page-' + theme + '-' + width + '.png'), fullPage: true })
      }
    }
    await page.setViewportSize({ width: 390, height: 844 })
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.getByRole('button', { name: '秒传验证', exact: true }).click()
    assert.equal(await page.locator('dialog').evaluate(el => getComputedStyle(el).animationName), 'none')
    const target = page.getByLabel('目标用户', { exact: true })
    await target.locator('option[value="pool-user"]').waitFor({ state: 'attached' })
    assert.deepEqual(await target.locator('option').evaluateAll(els => els.map(el => el.value)), ['', 'pool-user'])
    await page.locator('#instant-strm').fill('http://nas/api/?share_code=abc&id=123')
    runDelay = 2000
    await page.getByRole('button', { name: '开始单文件秒传', exact: true }).click()
    await page.getByText('验证正在执行。可以关闭飞窗，任务和草稿会保留在当前页面。', { exact: true }).waitFor()
    await page.getByRole('button', { name: '关闭秒传验证', exact: true }).click()
    await page.getByRole('button', { name: '秒传验证中', exact: true }).click()
    assert.equal(await page.locator('#instant-strm').inputValue(), 'http://nas/api/?share_code=abc&id=123')
    await page.getByText('秒传成功', { exact: true }).waitFor()
    assert.equal(runIds.length, 1)
    await page.getByRole('button', { name: '关闭秒传验证', exact: true }).click()
    assert.equal(await page.getByRole('button', { name: '秒传验证', exact: true }).evaluate(el => el === document.activeElement), true)
    await page.getByRole('button', { name: '秒传验证', exact: true }).click()
    const sampleRect = await page.locator('#instant-strm').boundingBox()
    await page.mouse.move(sampleRect.x + 20, sampleRect.y + 20)
    await page.mouse.down()
    await page.mouse.move(2, 2)
    await page.mouse.up()
    assert.equal(await page.locator('dialog').evaluate(el => el.open), true, 'text selection drag must not close dialog')
    await page.keyboard.press('Escape')
    assert.equal(await page.locator('dialog').evaluate(el => el.open), false)
    await page.getByRole('button', { name: '秒传验证', exact: true }).click()
    failRun = true; runDelay = 0
    await page.getByRole('button', { name: '开始单文件秒传', exact: true }).click()
    await page.getByText('模拟秒传结果不确定。', { exact: false }).waitFor()
    await page.getByRole('button', { name: '关闭秒传验证', exact: true }).click()
    await page.getByRole('button', { name: '秒传验证', exact: true }).click()
    failRun = false
    await page.getByRole('button', { name: '开始单文件秒传', exact: true }).click()
    await page.getByText('模拟秒传成功', { exact: true }).first().waitFor()
    assert.equal(runIds[0], runIds[1]); assert.equal(runIds[1], runIds[2])
    await page.getByRole('button', { name: '关闭秒传验证', exact: true }).click()
    failAccountRead = true
    await page.reload(); await page.getByText('网盘状态读取失败：模拟账号读取失败', { exact: false }).waitFor()
    assert.equal(await saveProxy.isDisabled(), false, 'account read failure must not block proxy controls')
    failProxyRead = true; failAccountRead = false
    await page.reload(); await page.getByText('模拟反代读取失败', { exact: false }).waitFor()
    assert.equal(await saveProxy.isDisabled(), true)
    failProxyRead = false; await page.getByRole('button', { name: '重试读取', exact: true }).click()
    await page.locator('#proxy-runtime-title').getByText('反代正在监听', { exact: true }).waitFor()
    assert.equal(await saveProxy.isDisabled(), false)
    assert.deepEqual(errors, [])
    console.log('Proxy mocked UI passed: independent saves, native validity, preserved settings and drafts, runtime saved port, retry, six sizes/light+dark, kept task and idempotent retry')
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
