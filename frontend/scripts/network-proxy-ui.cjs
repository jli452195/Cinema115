// 所有接口使用模拟响应，不连接代理、NAS 服务或外部网站。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block', reducedMotion: 'reduce' })
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light') })
    const page = await context.newPage(), calls = [], errors = [], passed = []
    page.setDefaultTimeout(8000); page.on('pageerror', error => errors.push(error.message))
    let config = { enabled: false, url: 'http://proxy.example:7890', username: 'operator', passwordConfigured: true, services: ['tmdb'], availableServices: [{ id: 'tmdb', label: 'TMDB' }, { id: 'bangumi', label: 'Bangumi' }, { id: 'mikan', label: '蜜柑' }, { id: 'anibt', label: 'AniBT' }, { id: 'garden', label: 'AnimeGarden' }, { id: 'assrt', label: 'Assrt' }, { id: 'github', label: '字幕服务' }, { id: 'gying', label: '观影' }, { id: 'woniu', label: '蜗牛' }, { id: 'telegram_bot', label: 'Telegram 机器人' }, { id: 'telegram_user', label: 'Telegram 用户与频道搜索' }] }
    const failureMessage = '连接超时，请检查代理服务和 NAS 网络；这是一段用于验证窄屏换行的完整失败说明。'
    let failSave = false, holdSave = false, releaseSave, nextHeldTest, activeTests = 0, maxActiveTests = 0
    function holdNextTest() {
      let release, start, finish
      const response = new Promise(resolve => { release = resolve }), started = new Promise(resolve => { start = resolve }), completed = new Promise(resolve => { finish = resolve })
      nextHeldTest = { response, start, finish }
      return { release, started, completed }
    }
    await context.route('**/*', async route => {
      const request = route.request(), target = new URL(request.url()), endpoint = target.pathname
      if (!endpoint.startsWith('/api/')) return target.origin === origin ? route.continue() : route.abort('blockedbyclient')
      const method = request.method(), body = request.postDataJSON()
      calls.push({ endpoint, method, body }); let data = { configured: false, authorized: false, channels: [], items: [], accounts: [], assrt: { configured: false } }, status = 200
      if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'mock', role: 'admin' } }
      else if (endpoint === '/api/admin/network-proxy/test') {
        const held = nextHeldTest; nextHeldTest = undefined
        activeTests++; maxActiveTests = Math.max(maxActiveTests, activeTests)
        if (held) { held.start(); await held.response }
        data = { service: body.service, proxied: body.enabled && body.services.includes(body.service), reachable: body.service !== 'gying', ...(!['telegram_user', 'gying'].includes(body.service) ? { status: body.service === 'tmdb' ? 401 : 200 } : {}), elapsedMs: 42, message: body.service === 'gying' ? failureMessage : '' }
        try { await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {}) }
        finally { activeTests--; held?.finish() }
        return
      } else if (endpoint === '/api/admin/network-proxy') {
        if (method === 'POST') {
          if (holdSave) { holdSave = false; await new Promise(resolve => { releaseSave = resolve }) }
          if (failSave) { status = 409; data = { error: '模拟配置校验失败' } }
          else { config = { ...config, enabled: body.enabled, url: body.url, username: body.username, services: body.services, passwordConfigured: body.password === undefined ? config.passwordConfigured : !!body.password }; data = config }
        } else data = config
      }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
    })
    const switcher = () => page.getByRole('switch', { name: '启用网络代理', exact: true })
    const save = () => page.getByRole('button', { name: '保存配置', exact: true })
    const openTest = () => page.getByRole('button', { name: '测试当前表单', exact: true })
    const dialog = () => page.getByRole('dialog', { name: '服务连通性', exact: true })
    const row = id => dialog().locator(`.np-test-row[data-service="${id}"]`)
    const testOne = id => row(id).getByRole('button', { name: `测试 ${config.availableServices.find(service => service.id === id).label}`, exact: true })
    const closeTest = () => dialog().getByRole('button', { name: '关闭弹窗', exact: true }).click()
    const batch = () => dialog().getByRole('button', { name: '重新测试全部', exact: true })
    const testCalls = () => calls.filter(call => call.endpoint.endsWith('/network-proxy/test'))
    const url = () => page.getByLabel('代理地址', { exact: true })
    const password = () => page.getByLabel('代理密码', { exact: true })
    const posts = () => calls.filter(call => call.endpoint === '/api/admin/network-proxy' && call.method === 'POST')
    await page.goto(origin + '/resources')
    await page.locator('main').getByRole('link', { name: '网络代理', exact: true }).click()
    await page.waitForFunction(() => !document.querySelector('.np-fields')?.disabled)
    assert.equal(await switcher().isChecked(), false)
    assert.equal(await page.getByLabel('Telegram 用户与频道搜索', { exact: true }).isChecked(), false)
    assert.equal(await page.getByLabel('Telegram 机器人', { exact: true }).isChecked(), false)
    assert.equal(await page.locator('.np-auth').getAttribute('open'), null)
    assert.equal(calls.some(call => call.endpoint.endsWith('/network-proxy/test')), false)
    await page.locator('.np-auth summary').click(); assert.equal(await password().inputValue(), '')
    await switcher().locator('..').click(); await page.getByLabel('AnimeGarden', { exact: true }).check()
    holdSave = true; await save().click(); await page.waitForFunction(() => document.querySelector('.np-fields').disabled)
    assert.equal(await url().isDisabled(), true); assert.equal(await switcher().isDisabled(), true)
    assert.equal(await openTest().isDisabled(), true)
    releaseSave(); await page.getByText('网络代理配置已保存。', { exact: true }).waitFor()
    assert.equal(posts().at(-1).body.enabled, true); assert.equal(await switcher().isChecked(), true)
    assert.equal('password' in posts().at(-1).body, false); assert.equal(await password().inputValue(), '')
    await switcher().locator('..').click(); await save().click(); await page.getByText('网络代理已关闭，服务使用直连。', { exact: true }).waitFor()
    assert.equal(posts().at(-1).body.enabled, false); assert.equal(config.passwordConfigured, true)
    passed.push('resource entry, read-only loading, save lock, enabling/disabling and omitted password preservation')

    await switcher().locator('..').click(); await url().fill('socks5h://proxy.example:1080'); await password().fill('new-test-secret')
    await page.getByLabel('Telegram 用户与频道搜索', { exact: true }).check()
    const saveCount = posts().length, beforeOpen = testCalls().length, beforeForm = calls.length, first = holdNextTest()
    await openTest().click(); await row('tmdb').locator('.np-test-state').filter({ hasText: '测试中' }).waitFor(); await first.started
    assert.equal(await dialog().locator('.np-test-row').count(), config.availableServices.length)
    assert.match(await dialog().locator('.np-test-top-note').innerText(), /当前表单测试，不保存配置/)
    assert.equal(testCalls().length, beforeOpen + 1)
    assert.equal(await testOne('telegram_user').isDisabled(), true); assert.equal(await batch().isDisabled(), true)
    const tested = testCalls().at(-1).body
    assert.deepEqual(tested, { enabled: true, url: 'socks5h://proxy.example:1080', username: 'operator', password: 'new-test-secret', services: ['garden', 'telegram_user', 'tmdb'], service: 'tmdb' })
    first.release(); await first.completed
    await dialog().getByRole('button', { name: '取消测试', exact: true }).waitFor({ state: 'detached' })
    assert.deepEqual(testCalls().slice(beforeOpen).map(call => call.body.service), config.availableServices.map(service => service.id))
    for (const call of testCalls().slice(beforeOpen)) assert.deepEqual(call.body, { ...tested, service: call.body.service })
    assert.equal(maxActiveTests, 1)
    assert.equal(calls.slice(beforeForm).some(call => call.endpoint === '/api/admin/network-proxy'), false)
    assert.equal(await row('tmdb').locator('.np-test-state').innerText(), '网络可达')
    assert.equal(await row('tmdb').locator('.np-test-http').innerText(), 'HTTP 401')
    assert.equal(await row('gying').locator('.np-test-state').innerText(), '网络不可达')
    assert.equal(await row('gying').locator('.np-test-message').innerText(), failureMessage)
    assert.equal(await row('telegram_user').locator('.np-test-state').innerText(), '网络可达')
    assert.equal(await row('telegram_user').locator('.np-test-latency').innerText(), '42 ms')
    assert.equal(await row('telegram_user').locator('.np-test-http').count(), 0)
    assert.equal(await row('telegram_user').locator('.np-test-route').innerText(), '代理')
    assert.equal(await row('telegram_bot').locator('.np-test-route').innerText(), '直连')
    await closeTest()
    assert.equal(posts().length, saveCount); assert.equal(config.url, 'http://proxy.example:7890')
    assert.equal(await password().inputValue(), 'new-test-secret')
    passed.push('表单快照自动串行测试全部服务，HTTP 401 仍可达，TCP 无 HTTP，错误完整且不保存配置')

    const beforeHeader = calls.length, headerStart = testCalls().length, overflow = await page.evaluate(() => document.body.style.overflow)
    await page.getByRole('button', { name: '服务连通性', exact: true }).click(); await dialog().waitFor()
    await dialog().getByRole('button', { name: '取消测试', exact: true }).waitFor({ state: 'detached' })
    assert.match(await dialog().locator('.np-test-top-note').innerText(), /使用已保存配置/)
    assert.deepEqual(calls.slice(beforeHeader).filter(call => call.endpoint === '/api/admin/network-proxy').map(call => call.method), ['GET'])
    assert.deepEqual(testCalls().slice(headerStart).map(call => call.body.service), config.availableServices.map(service => service.id))
    for (const call of testCalls().slice(headerStart)) assert.deepEqual(call.body, { enabled: config.enabled, url: config.url, username: config.username, services: config.services, service: call.body.service })
    assert.equal(posts().length, saveCount)

    // 用一个窄屏保留真实操作风险，不重复截图、主题矩阵和精确样式断言。
    await page.setViewportSize({ width: 320, height: 677 }); await page.evaluate(() => document.documentElement.classList.add('dark'))
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true)
    assert.equal(await dialog().evaluate(element => { const box = element.getBoundingClientRect(); return box.left >= 0 && box.right <= innerWidth && box.top >= 0 && box.bottom <= innerHeight && element.scrollWidth <= element.clientWidth + 1 }), true)
    assert.equal(await row('gying').locator('.np-test-message').innerText(), failureMessage)
    assert.equal(await row('gying').locator('.np-test-message').evaluate(element => element.scrollWidth <= element.clientWidth + 1 && element.scrollHeight <= element.clientHeight + 1), true)
    for (const button of await dialog().getByRole('button').all()) {
      await button.scrollIntoViewIfNeeded()
      assert.equal(await button.evaluate(element => { const box = element.getBoundingClientRect(); return box.width >= 44 && box.height >= 44 && box.left >= 0 && box.right <= innerWidth && box.top >= 0 && box.bottom <= innerHeight && element.contains(document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2)) }), true)
    }
    const singleStart = testCalls().length
    await testOne('telegram_user').click(); await row('telegram_user').locator('.np-test-state').filter({ hasText: '网络可达' }).waitFor()
    assert.deepEqual(testCalls().slice(singleStart).map(call => call.body.service), ['telegram_user'])
    assert.equal(await row('gying').locator('.np-test-message').innerText(), failureMessage)
    const escapeStart = testCalls().length, escaping = holdNextTest()
    await batch().click(); await row('tmdb').locator('.np-test-state').filter({ hasText: '测试中' }).waitFor(); await escaping.started
    const cancel = dialog().getByRole('button', { name: '取消测试', exact: true })
    assert.equal(await cancel.evaluate(element => { const box = element.getBoundingClientRect(); return box.height >= 44 && box.top >= 0 && box.bottom <= innerHeight && element.contains(document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2)) }), true)
    assert.equal(await page.evaluate(() => document.body.style.overflow), 'hidden')
    await page.keyboard.press('Escape'); await dialog().waitFor({ state: 'detached' })
    assert.equal(await page.evaluate(() => document.body.style.overflow), overflow)
    escaping.release(); await escaping.completed; await page.waitForTimeout(100)
    assert.deepEqual(testCalls().slice(escapeStart).map(call => call.body.service), ['tmdb'])
    await page.setViewportSize({ width: 1440, height: 1000 })
    assert.equal(await password().inputValue(), tested.password); assert.equal(posts().length, saveCount)
    assert.equal(await page.evaluate(() => [...Object.values(localStorage), ...Object.values(sessionStorage)].some(value => value.includes('new-test-secret'))), false)
    passed.push('全局 Header 读取已保存配置且不携带未保存凭据；320px 按钮可操作、单项重试不影响其他结果，Escape 中止队列并释放滚动锁')

    failSave = true; await save().click(); await page.getByRole('alert').filter({ hasText: '模拟配置校验失败' }).waitFor()
    assert.equal(await url().inputValue(), tested.url); assert.equal(await password().inputValue(), tested.password)
    const savesAfterFailure = posts().length
    failSave = false
    const cancelStart = testCalls().length, cancelled = holdNextTest()
    await openTest().click(); await row('tmdb').locator('.np-test-state').filter({ hasText: '测试中' }).waitFor(); await cancelled.started
    await dialog().getByRole('button', { name: '取消测试', exact: true }).click()
    await row('tmdb').locator('.np-test-state').filter({ hasText: '测试已取消' }).waitFor()
    await dialog().getByRole('status').filter({ hasText: '测试已取消，当前输入与结果已保留。' }).waitFor()
    cancelled.release(); await cancelled.completed
    await page.waitForTimeout(100); assert.equal(await row('tmdb').locator('.np-test-state').innerText(), '测试已取消')
    assert.deepEqual(testCalls().slice(cancelStart).map(call => call.body.service), ['tmdb'])
    assert.equal(await testOne('telegram_user').isEnabled(), true); assert.equal(await batch().isEnabled(), true)
    const closed = holdNextTest(), closeStart = testCalls().length
    await batch().click(); await row('tmdb').locator('.np-test-state').filter({ hasText: '测试中' }).waitFor(); await closed.started
    await closeTest(); await dialog().waitFor({ state: 'detached' })
    assert.equal(await password().inputValue(), tested.password)
    await url().fill('socks5h://proxy.example:1081')
    const beforeReopen = testCalls().length, reopened = holdNextTest()
    await openTest().click(); await row('tmdb').locator('.np-test-state').filter({ hasText: '测试中' }).waitFor(); await reopened.started
    closed.release(); await closed.completed; await page.waitForTimeout(100)
    assert.deepEqual(testCalls().slice(closeStart).map(call => call.body.service), ['tmdb', 'tmdb'])
    assert.equal(await row('tmdb').getAttribute('aria-busy'), 'true')
    assert.equal(await row('tmdb').locator('.np-test-state').innerText(), '测试中…')
    assert.equal(await row('tmdb').locator('.np-test-latency').innerText(), '')
    assert.equal(await dialog().locator('.np-test-result').count(), 1)
    reopened.release(); await reopened.completed
    await dialog().getByRole('button', { name: '取消测试', exact: true }).waitFor({ state: 'detached' })
    assert.deepEqual(testCalls().slice(beforeReopen).map(call => call.body.service), config.availableServices.map(service => service.id))
    for (const call of testCalls().slice(beforeReopen)) assert.deepEqual(call.body, { ...tested, url: 'socks5h://proxy.example:1081', service: call.body.service })
    assert.equal(posts().length, savesAfterFailure)
    await closeTest()
    assert.equal(await password().inputValue(), tested.password)
    passed.push('保存失败保留输入；取消阻止余下队列及迟到覆盖，关闭后的旧响应不污染新弹窗，新表单快照清除旧结果')

    await page.locator('.np-auth summary').click(); await page.locator('.np-auth summary').click(); assert.equal(await password().inputValue(), tested.password)
    await page.getByRole('link', { name: '资源配置', exact: true }).click()
    const leave = page.getByRole('dialog', { name: '放弃未保存的代理设置？', exact: true }); await leave.waitFor()
    await leave.getByRole('button', { name: '继续编辑', exact: true }).click(); assert.equal(await password().inputValue(), tested.password)
    await password().fill(''); await page.getByLabel('代理用户名', { exact: true }).fill('')
    await page.getByRole('alert').filter({ hasText: '同时清除密码' }).waitFor(); assert.equal(await save().isDisabled(), true)
    await page.getByLabel('清除已保存密码', { exact: true }).check(); await save().click()
    await page.getByText('网络代理配置已保存。', { exact: true }).waitFor()
    assert.equal(config.enabled, true)
    assert.equal(posts().at(-1).body.password, ''); assert.equal(config.passwordConfigured, false)
    assert.equal(await page.getByLabel('清除已保存密码', { exact: true }).count(), 0)
    await password().fill('transient-secret')
    assert.equal(await page.evaluate(() => [...Object.values(localStorage), ...Object.values(sessionStorage)].some(value => value.includes('transient-secret'))), false)
    await page.getByRole('link', { name: '资源配置', exact: true }).click(); await leave.waitFor()
    await leave.getByRole('button', { name: '放弃修改并离开', exact: true }).click(); await page.waitForURL(origin + '/resources')
    await page.locator('main').getByRole('link', { name: '网络代理', exact: true }).click(); await page.waitForFunction(() => !document.querySelector('.np-fields')?.disabled)
    assert.equal(await password().inputValue(), '')
    assert.equal(await switcher().isChecked(), true)
    passed.push('collapsed auth retains edits, leaving can cancel/discard, explicit password clearing, no secret persistence')

    await switcher().focus(); await page.keyboard.press('Space'); assert.equal(await switcher().isChecked(), false)
    await page.keyboard.press('Space'); assert.equal(await switcher().isChecked(), true)
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
