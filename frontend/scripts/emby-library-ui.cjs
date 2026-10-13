const path = require('node:path'), os = require('node:os'), fs = require('node:fs'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => {
      localStorage.setItem('admin_token', 'mock')
      window.__copies = []
      Object.defineProperty(navigator, 'clipboard', { value: { writeText: async text => window.__copies.push(text) }, configurable: true })
    })
    const page = await context.newPage(), errors = [], writes = []
    page.on('pageerror', error => errors.push(error.message))
    let config = { url: 'http://emby:8096', configured: true, libraryIds: ['movies', 'tv'], serverId: 'server', serverName: '家庭媒体库', webhookPath: '/api/webhook/emby?token=mock-secret', webhookBaseUrl: 'http://cinema:3002' }
    let status = { status: 'idle', configured: true, count: 134674, scanned: 0, syncedAt: '2026-10-12T13:56:25Z', lastError: '', webhookAt: '2026-10-07T08:43:11Z', webhookEvent: 'library.new', webhookError: '', pending: 0, failed: 8, connected: true, connectionError: '', eventError: '缺少季号或集号', metadataPending: 0, metadataFailed: 8, updatesReady: true, updatesError: '', updatesStatus: 'ready' }
    let libraries = [{ ItemId: 'movies', Name: '电影', CollectionType: 'movies' }, { ItemId: 'tv', Name: '电视剧', CollectionType: 'tvshows' }, { ItemId: 'music', Name: '音乐', CollectionType: 'music' }]
    let failConfig = false, failLibraries = false, failStatus = false, failSave = false, failWebhook = false, failSync = false, statusReads = 0
    let testDelay = 0, saveDelay = 0, statusDelay = 0
    await page.route('**/api/**', async route => {
      const req = route.request(), p = new URL(req.url()).pathname
      let data = {}, code = 200
      if (req.method() === 'POST') writes.push({ p, body: req.postDataJSON() })
      if (p === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
      else if (p.endsWith('/emby/config')) {
        if (req.method() === 'POST') {
          await new Promise(resolve => setTimeout(resolve, saveDelay))
          if (failSave) { code = 400; data = { error: '模拟保存失败' } }
          else { const input = req.postDataJSON(); config = { ...config, url: input.url, configured: true, libraryIds: input.libraryIds }; data = config }
        } else if (failConfig) { code = 500; data = { error: '模拟配置读取失败' } } else data = config
      } else if (p.endsWith('/emby/test')) {
        await new Promise(resolve => setTimeout(resolve, testDelay))
        if (failLibraries) { code = 400; data = { error: '模拟媒体库读取失败' } }
        else data = { server: { Id: 'server', ServerName: '家庭媒体库', Version: '4.9' }, libraries }
      } else if (p.endsWith('/emby/status')) {
        statusReads++
        const snapshot = { ...status }
        await new Promise(resolve => setTimeout(resolve, statusDelay))
        if (failStatus) { code = 500; data = { error: '模拟状态读取失败' } } else data = status
        if (!failStatus) data = snapshot
      } else if (p.endsWith('/emby/webhook-config')) {
        if (failWebhook) { code = 400; data = { error: '模拟地址保存失败' } }
        else { config = { ...config, webhookBaseUrl: req.postDataJSON().baseUrl.replace(/\/+$/, '') }; data = config }
      } else if (p.endsWith('/emby/sync')) {
        if (failSync) { code = 400; data = { error: '模拟同步提交失败' } }
        else { status = { ...status, status: 'queued' }; data = { message: '已排队' }; code = 202 }
      }
      await route.fulfill({ status: code, contentType: 'application/json', body: JSON.stringify(data) })
    })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/emby/library')
    const save = page.locator('.library-save-row button[type=submit]'), sync = page.getByRole('button', { name: '开始全量同步', exact: true })
    const options = page.locator('.library-option'), savedURL = page.getByLabel('已保存的 Webhook 地址', { exact: true })
    await options.last().waitFor()
    assert.equal(await options.count(), 3)
    assert.equal(await options.locator('input:checked').count(), 2)
    assert.equal(await page.locator('#library-connection-fields').count(), 0)
    assert.equal(await savedURL.getAttribute('type'), 'password')
    assert.equal(await save.isDisabled(), true)
    const search = page.getByRole('searchbox', { name: '搜索同步媒体库', exact: true })
    await search.fill('电影'); await page.getByRole('button', { name: '清除搜索结果', exact: true }).click()
    await search.fill(''); assert.equal(await options.locator('input:checked').count(), 1)
    assert.equal(await sync.isDisabled(), true)
    await page.locator('.library-save-row').getByRole('button', { name: '撤销修改', exact: true }).click()
    assert.equal(await options.locator('input:checked').count(), 2)
    await page.locator('.library-option').filter({ hasText: '音乐' }).locator('input').check()
    await page.locator('.library-webhook-edit summary').click()
    const base = page.getByLabel('Emby 能访问的 Cinema115 基础地址', { exact: true })
    await base.fill('http://new-cinema:3002')
    await page.getByRole('button', { name: '复制地址', exact: true }).click()
    assert.equal((await page.evaluate(() => window.__copies)).at(-1), 'http://cinema:3002/api/webhook/emby?token=mock-secret')
    await save.click(); await page.getByText('连接与范围已保存。首次使用或调整范围后，请执行全量同步。', { exact: true }).waitFor()
    assert.equal(await page.locator('#library-connection-fields').count(), 0, 'scope saves do not expand credentials')
    assert.equal(await base.inputValue(), 'http://new-cinema:3002')
    assert.equal(config.webhookBaseUrl, 'http://cinema:3002')
    assert.equal('webhookBaseUrl' in writes.find(w => w.p.endsWith('/config')).body, false)
    await page.locator('.library-option').filter({ hasText: '电视剧' }).locator('input').uncheck()
    const draftedSelected = await options.locator('input:checked').count()
    await page.getByRole('button', { name: '保存地址', exact: true }).click()
    await page.getByText('地址已保存。请复制完整地址，并更新 Emby 中的 Webhook 配置。', { exact: true }).waitFor()
    assert.equal(await options.locator('input:checked').count(), draftedSelected)
    assert.equal(await save.isDisabled(), false)
    await page.getByRole('button', { name: '复制地址', exact: true }).click()
    assert.equal((await page.evaluate(() => window.__copies)).at(-1), 'http://new-cinema:3002/api/webhook/emby?token=mock-secret')
    await page.getByRole('button', { name: '编辑连接', exact: true }).click()
    const url = page.getByLabel('服务器地址', { exact: true }), key = page.getByLabel('API Key', { exact: true })
    await key.fill('new-key')
    assert.equal(await options.count(), 0, 'credential changes invalidate old library identity')
    await page.getByRole('button', { name: '测试并读取', exact: true }).click()
    await page.getByText('连接成功：家庭媒体库，已读取 3 个媒体库。', { exact: true }).waitFor()
    await key.fill('changed-key'); assert.equal(await page.getByText('连接成功：家庭媒体库，已读取 3 个媒体库。', { exact: true }).count(), 0)
    await page.getByRole('button', { name: '刷新运行状态', exact: true }).click()
    assert.equal(await key.inputValue(), 'changed-key')
    failSave = true; await save.click(); await page.getByText('模拟保存失败', { exact: true }).waitFor()
    assert.equal(await key.inputValue(), 'changed-key')
    failSave = false; await page.locator('.library-save-row').getByRole('button', { name: '撤销修改', exact: true }).click()
    await options.last().waitFor()
    libraries = libraries.filter(l => l.ItemId !== 'tv')
    await page.getByRole('button', { name: '刷新列表', exact: true }).click()
    await page.getByRole('button', { name: '移除失效选择', exact: true }).waitFor()
    assert.equal(await page.getByText('已选 3 个', { exact: true }).isVisible(), true)
    assert.equal(await save.isDisabled(), true)
    await page.getByRole('button', { name: '清空', exact: true }).click()
    assert.equal(await page.getByText('已选 0 个', { exact: true }).isVisible(), true, 'clear all includes missing IDs')
    await page.locator('.library-save-row').getByRole('button', { name: '撤销修改', exact: true }).click()
    await page.getByRole('button', { name: '移除失效选择', exact: true }).waitFor()
    await page.getByRole('button', { name: '移除失效选择', exact: true }).click()
    assert.equal(await save.isDisabled(), false)
    saveDelay = 300
    await save.dblclick(); await page.getByText('连接与范围已保存。首次使用或调整范围后，请执行全量同步。', { exact: true }).waitFor()
    assert.equal(writes.filter(w => w.p.endsWith('/config')).length, 3, 'double clicks must submit once')
    saveDelay = 0
    failStatus = true; await page.getByRole('button', { name: '刷新运行状态', exact: true }).click()
    await page.getByText('状态刷新失败，当前显示上次结果：模拟状态读取失败', { exact: true }).waitFor()
    assert.equal(await page.getByText('134,674', { exact: true }).innerText(), '134,674')
    failStatus = false; await page.getByRole('button', { name: '刷新运行状态', exact: true }).click()
    await page.getByText('连接正常', { exact: true }).waitFor()
    // Invalid URL is rejected by the native form without issuing a test request.
    if (!(await url.isVisible())) await page.getByRole('button', { name: '编辑连接', exact: true }).click()
    const testsBefore = writes.filter(w => w.p.endsWith('/test')).length
    await url.fill('invalid'); await page.getByRole('button', { name: '测试并读取', exact: true }).click()
    assert.equal(writes.filter(w => w.p.endsWith('/test')).length, testsBefore)
    await page.locator('.library-save-row').getByRole('button', { name: '撤销修改', exact: true }).click()
    failWebhook = true; await base.fill('http://retry:3002'); await page.getByRole('button', { name: '保存地址', exact: true }).click()
    await page.getByText('模拟地址保存失败', { exact: true }).waitFor(); assert.equal(await base.inputValue(), 'http://retry:3002')
    failWebhook = false; await page.getByRole('button', { name: '保存地址', exact: true }).click()
    await page.getByText('地址已保存。请复制完整地址，并更新 Emby 中的 Webhook 配置。', { exact: true }).waitFor()
    failSync = true; await sync.click(); await page.getByText('模拟同步提交失败', { exact: true }).waitFor()
    assert.equal(await sync.isDisabled(), false)
    failSync = false; statusDelay = 250
    await page.getByRole('button', { name: '刷新运行状态', exact: true }).click()
    await sync.dblclick(); await page.getByRole('button', { name: '同步进行中', exact: true }).waitFor()
    await page.getByText('等待后台开始', { exact: true }).waitFor(); statusDelay = 0
    assert.equal(writes.filter(w => w.p.endsWith('/sync')).length, 2)
    status = { ...status, status: 'running', scanned: 2350 }
    await page.getByRole('button', { name: '刷新运行状态', exact: true }).click(); await page.getByText('已读取 2,350 条', { exact: true }).waitFor()
    assert.equal(await page.getByRole('progressbar').count(), 0)
    status = { ...status, status: 'idle' }
    await page.getByRole('button', { name: '刷新运行状态', exact: true }).click()
    await page.locator('.library-warning-link').click(); assert.equal(await page.locator('.library-metadata').evaluate(el => el.open), true)
    await page.reload(); await options.last().waitFor()
    fs.mkdirSync(path.join(__dirname, '../test-results'), { recursive: true })
    for (const theme of ['light', 'dark']) {
      await page.evaluate(theme => document.documentElement.classList.toggle('dark', theme === 'dark'), theme)
      for (const [width, height] of [[1440,1000],[1024,900],[768,1024],[390,844],[320,780],[844,390]]) {
        await page.setViewportSize({ width, height }); await page.evaluate(() => window.scrollTo(0,0))
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, theme + width)
        assert.equal(await page.locator('h1').evaluate(el => el.getBoundingClientRect().height < 40), true)
        if (width < 900) assert.ok((await page.locator('.library-sync').boundingBox()).y < (await page.locator('.library-settings').boundingBox()).y)
        if (theme === 'dark') assert.equal(await page.locator('.library-metric-value').first().evaluate(el => getComputedStyle(el).color), 'rgb(243, 244, 245)')
        await page.screenshot({ path: path.join(__dirname, `../test-results/library-${theme}-${width}.png`), fullPage: true })
        await page.locator('.library-webhook-edit summary').click(); await page.locator('.library-webhook-guide summary').click()
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
        await page.locator('.library-webhook-edit summary').click(); await page.locator('.library-webhook-guide summary').click()
      }
    }
    // The initial unconfigured flow must not automatically select any returned library.
    config = { ...config, configured: false, libraryIds: [], serverName: '', webhookPath: '', webhookBaseUrl: '' }
    await page.reload(); await page.getByLabel('API Key', { exact: true }).waitFor()
    assert.equal(await options.count(), 0)
    await page.getByLabel('API Key', { exact: true }).fill('initial-key')
    await page.getByRole('button', { name: '测试并读取', exact: true }).click(); await options.last().waitFor()
    assert.equal(await options.locator('input:checked').count(), 0)
    assert.equal(await save.isDisabled(), true)
    await page.getByRole('button', { name: '全选', exact: true }).click(); await save.click()
    await page.getByText('连接与范围已保存。首次使用或调整范围后，请执行全量同步。', { exact: true }).waitFor()
    // Failure scopes and retry remain independent.
    config = { ...config, configured: true, webhookPath: '/api/webhook/emby?token=mock-secret' }
    failLibraries = true; await page.reload(); await page.getByText('模拟媒体库读取失败', { exact: true }).waitFor()
    assert.equal(await sync.isDisabled(), false)
    failLibraries = false; await page.getByRole('button', { name: '重新读取', exact: true }).click(); await options.last().waitFor()
    // Background status polling pauses while hidden and never overwrites field drafts.
    if (await page.getByRole('button', { name: '编辑连接', exact: true }).count()) await page.getByRole('button', { name: '编辑连接', exact: true }).click()
    await page.getByLabel('API Key', { exact: true }).fill('keep-draft')
    await page.clock.install()
    await page.evaluate(() => { window.__hidden = true; Object.defineProperty(document, 'hidden', { get: () => window.__hidden, configurable: true }); document.dispatchEvent(new Event('visibilitychange')) })
    const readsWhileHidden = statusReads
    await page.clock.runFor(20000)
    assert.equal(statusReads, readsWhileHidden)
    await page.evaluate(() => { window.__hidden = false; document.dispatchEvent(new Event('visibilitychange')) })
    await page.waitForResponse(response => response.url().endsWith('/emby/status'))
    assert.equal(await page.getByLabel('API Key', { exact: true }).inputValue(), 'keep-draft')
    await page.clock.resume()
    failConfig = true; await page.reload(); await page.getByText('模拟配置读取失败', { exact: true }).waitFor()
    assert.equal(await save.isDisabled(), true)
    failConfig = false; await page.getByRole('button', { name: '重试读取', exact: true }).click(); await options.last().waitFor()
    assert.ok(statusReads > 5); assert.deepEqual(errors, [])
    console.log('Library mocked UI passed: automatic library loading, scoped selection, independent saves/copy, invalidation, retained missing IDs, validation, retries, no duplicate submits, real sync counts, six viewports and both themes.')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
