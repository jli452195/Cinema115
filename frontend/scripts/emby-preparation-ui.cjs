const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict'), fs = require('node:fs')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage(), errors = [], writes = [], memberRequests = []
    page.on('pageerror', e => errors.push(e.message))
    page.on('dialog', d => d.accept())
    let proxy = { enabled: true, debug: false, port: 8097, url: 'http://emby:8096', accountId: 'main', directLinkCacheMinutes: 9 }
    const savedPublicUrl = 'https://emby.example.com', savedLibraryIds = ['movies']
    const playbackSettings = { allowDownload: false, publicUrl: savedPublicUrl, libraryIds: [...savedLibraryIds] }
    let policy = { mode: 'parallel', silenceSeconds: 60 }
    let failProxyRead = false, failAccountRead = false, failProxyWrite = false
    await page.route('**/api/**', async route => {
      const req = route.request(), p = new URL(req.url()).pathname; let data = {}, status = 200
      if (req.method() !== 'GET') writes.push({ p, body: req.postDataJSON() })
      if (p === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
      else if (p === '/api/admin/p115/accounts') {
        if (failAccountRead) { status = 500; data = { error: '模拟账号读取失败' } }
        else data = { accounts: [{ id: 'main', primary: true, configured: true, name: '主账号' }, { id: 'pool', primary: false, configured: true, name: '小号 A' }] }
      } else if (p.endsWith('/device-policy')) { if (req.method() === 'POST') policy = req.postDataJSON(); data = policy }
      else if (p.startsWith('/api/admin/member-playback')) {
        memberRequests.push({ p, method: req.method() })
        status = 404
        data = { error: '下载设置应通过反代接口加载和保存' }
      } else if (p.endsWith('/proxy')) {
        if (req.method() === 'POST') {
          const { allowDownload, ...draft } = req.postDataJSON()
          if (failProxyWrite) {
            status = 500
            data = { error: '模拟反代配置保存失败' }
          } else {
            proxy = draft
            if (typeof allowDownload === 'boolean') playbackSettings.allowDownload = allowDownload
          }
        } else if (failProxyRead) { status = 500; data = { error: '模拟反代读取失败' } }
        if (status === 200) data = { config: proxy, listening: proxy.enabled, error: '', allowDownload: playbackSettings.allowDownload }
      }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const base = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    await page.goto(base + '/emby/proxy')
    const proxySettings = page.getByRole('region', { name: '反代设置', exact: true })
    const saveProxy = proxySettings.getByRole('button', { name: '保存反代配置', exact: true }), port = page.getByLabel('反代监听端口', { exact: false })
    const downloadSwitch = proxySettings.getByRole('switch', { name: '允许下载原文件', exact: true })
    const runtimeStatus = page.locator('#proxy-runtime-status')
    await saveProxy.waitFor()
    await page.getByText('反代正在监听', { exact: true }).and(runtimeStatus).waitFor()
    assert.equal(await downloadSwitch.isChecked(), false)
    assert.equal(await page.getByText('部署与工具', { exact: false }).count(), 0)
    assert.equal(await page.getByRole('heading', { name: '访问设置', exact: true }).count(), 0)
    assert.equal(await page.getByLabel('提供给用户的 Emby 连接地址', { exact: false }).count(), 0)
    assert.equal(await proxySettings.getByRole('button', { name: '保存下载设置', exact: true }).count(), 0)
    const switchLabels = await proxySettings.getByRole('switch').evaluateAll(switches => switches.map(toggle => toggle.getAttribute('aria-label')))
    assert.deepEqual(switchLabels.slice(0, 2), ['启用 302 并接管播放', '允许下载原文件'])
    const savedProxy = { ...proxy }
    await port.fill('8098')
    await downloadSwitch.press('Space')
    await page.getByRole('button', { name: '刷新状态', exact: true }).click()
    await page.getByRole('button', { name: '刷新状态', exact: true }).and(page.locator(':enabled')).waitFor()
    assert.equal(await port.inputValue(), '8098')
    assert.equal(await downloadSwitch.isChecked(), true)
    failProxyWrite = true
    await saveProxy.click()
    await proxySettings.getByRole('alert').filter({ hasText: '模拟反代配置保存失败' }).waitFor()
    const combinedWrite = { p: '/api/admin/emby/proxy', body: { ...savedProxy, port: 8098, allowDownload: true } }
    assert.deepEqual(writes, [combinedWrite])
    assert.equal(await port.inputValue(), '8098')
    assert.equal(await downloadSwitch.isChecked(), true)
    assert.deepEqual(proxy, savedProxy)
    assert.deepEqual(playbackSettings, { allowDownload: false, publicUrl: savedPublicUrl, libraryIds: savedLibraryIds })
    failProxyWrite = false
    await saveProxy.click(); await proxySettings.getByText('反代配置已保存。', { exact: true }).waitFor()
    assert.deepEqual(writes, [combinedWrite, combinedWrite])
    assert.deepEqual(playbackSettings, { allowDownload: true, publicUrl: savedPublicUrl, libraryIds: savedLibraryIds })
    assert.deepEqual(proxy, { ...savedProxy, port: 8098 })
    assert.equal(await port.inputValue(), '8098')
    const before = writes.length
    await port.fill('65536'); await saveProxy.click()
    assert.equal(await port.evaluate(el => el.validity.valid), false); assert.equal(writes.length, before)
    await port.fill('8098')
    await page.getByRole('switch', { name: '启用 302 并接管播放', exact: true }).uncheck({ force: true })
    await page.getByText('待保存后停止入口', { exact: true }).waitFor()
    assert.equal(await runtimeStatus.innerText(), '反代正在监听')
    await page.getByRole('switch', { name: '启用 302 并接管播放', exact: true }).check({ force: true })
    const upstream = page.getByLabel('Emby 原服务器地址', { exact: false })
    await upstream.fill(''); await saveProxy.click()
    assert.equal(await upstream.evaluate(el => el.validity.valid), false); assert.equal(writes.length, before)
    await upstream.fill('http://emby:8096')
    await page.locator('.proxy-advanced > summary').click()
    const cacheMinutes = page.getByLabel('直链缓存时间（分钟）', { exact: false })
    assert.equal(await page.getByLabel('Emby 中对应 STRM 根目录的路径', { exact: false }).count(), 0)
    await cacheMinutes.fill('31')
    await page.locator('.proxy-advanced > summary').click()
    await saveProxy.click()
    assert.equal(await page.locator('.proxy-advanced').evaluate(el => el.open), true, 'invalid advanced fields must be revealed')
    assert.equal(writes.length, before)
    await cacheMinutes.fill('9')
    await page.locator('.proxy-advanced > summary').click()
    // 检查不同尺寸和主题下的标题和横向溢出。
    fs.mkdirSync(path.join(__dirname, '../test-results'), { recursive: true })
    for (const theme of ['light', 'dark']) {
      await page.evaluate(theme => document.documentElement.classList.toggle('dark', theme === 'dark'), theme)
      for (const [width, height] of [[1440,1000],[1024,900],[768,1024],[390,844],[320,740],[844,390]]) {
        await page.setViewportSize({ width, height })
        await page.evaluate(() => window.scrollTo(0, 0))
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, theme + width)
        const title = page.locator(width < 640 ? '.workbench-header .mobile-centered-title' : '.management-heading h1')
        assert.equal(await title.innerText(), 'Emby 302 与媒体访问')
        assert.ok(await title.evaluate(el => el.getBoundingClientRect().width) > 1)
        await page.screenshot({ path: path.join(__dirname, '../test-results/proxy-page-' + theme + '-' + width + '.png'), fullPage: true })
      }
    }
    failAccountRead = true
    await page.reload(); await page.getByText('网盘状态读取失败：模拟账号读取失败', { exact: false }).waitFor()
    assert.equal(await saveProxy.isDisabled(), false, 'account read failure must not block proxy controls')
    failProxyRead = true; failAccountRead = false
    await page.reload(); await page.getByText('模拟反代读取失败', { exact: false }).waitFor()
    assert.equal(await saveProxy.isDisabled(), true)
    failProxyRead = false; await proxySettings.getByRole('button', { name: '重试读取', exact: true }).click()
    await page.getByText('反代正在监听', { exact: true }).and(runtimeStatus).waitFor()
    assert.equal(await saveProxy.isDisabled(), false)
    assert.equal(await downloadSwitch.isChecked(), true)
    assert.deepEqual(memberRequests, [])
    assert.deepEqual(errors, [])
    console.log('Proxy mocked UI passed: combined saves and retry, native validity, preserved settings and drafts, runtime status, six sizes/light+dark')
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
