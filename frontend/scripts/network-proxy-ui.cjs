// All APIs are mocked. No connection is made to a proxy, NAS service or external site.
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light') })
    const page = await context.newPage(), calls = [], errors = [], passed = []
    page.setDefaultTimeout(8000); page.on('pageerror', error => errors.push(error.message))
    let config = { enabled: false, url: 'http://proxy.example:7890', username: 'operator', passwordConfigured: true, services: ['tmdb'], availableServices: [{ id: 'tmdb', label: 'TMDB' }, { id: 'bangumi', label: 'Bangumi' }, { id: 'mikan', label: '蜜柑' }, { id: 'anibt', label: 'AniBT' }, { id: 'garden', label: 'AnimeGarden' }, { id: 'assrt', label: 'Assrt' }, { id: 'github', label: '字幕服务' }, { id: 'gying', label: '观影' }, { id: 'woniu', label: '蜗牛' }, { id: 'telegram_bot', label: 'Telegram 机器人' }, { id: 'telegram_user', label: 'Telegram 用户与频道搜索' }] }
    let failSave = false, failTest = false, holdSave = false, releaseSave, nextHeldTest, activeTests = 0, maxActiveTests = 0
    function holdNextTest() {
      let release, start, finish
      const response = new Promise(resolve => { release = resolve }), started = new Promise(resolve => { start = resolve }), completed = new Promise(resolve => { finish = resolve })
      nextHeldTest = { response, start, finish }
      return { release, started, completed }
    }
    await page.route('**/api/**', async route => {
      const request = route.request(), endpoint = new URL(request.url()).pathname, method = request.method(), body = request.postDataJSON()
      calls.push({ endpoint, method, body }); let data = { configured: false, authorized: false, channels: [], items: [], accounts: [], assrt: { configured: false } }, status = 200
      if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'mock', role: 'admin' } }
      else if (endpoint === '/api/admin/network-proxy/test') {
        const held = nextHeldTest; nextHeldTest = undefined
        activeTests++; maxActiveTests = Math.max(maxActiveTests, activeTests)
        if (held) { held.start(); await held.response }
        data = { service: body.service, proxied: body.enabled && body.services.includes(body.service), reachable: !failTest, ...(body.service !== 'telegram_user' && !failTest ? { status: 200 } : {}), elapsedMs: 42, message: failTest ? '模拟连接超时' : body.service === 'telegram_user' ? '模拟 Telegram 数据中心连接成功，仅验证网络连通。' : '模拟连接成功' }
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
    const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    const switcher = () => page.getByRole('switch', { name: '启用网络代理', exact: true })
    const save = () => page.getByRole('button', { name: '保存配置', exact: true })
    const openTest = () => page.getByRole('button', { name: '测试连接', exact: true })
    const dialog = () => page.getByRole('dialog', { name: '连接测试', exact: true })
    const row = id => dialog().locator(`.np-test-row[data-service="${id}"]`)
    const testOne = id => row(id).getByRole('button', { name: `测试 ${config.availableServices.find(service => service.id === id).label}`, exact: true })
    const closeTest = () => dialog().getByRole('button', { name: '关闭弹窗', exact: true }).click()
    const batch = () => dialog().getByRole('button', { name: '测试已选服务', exact: true })
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
    const saveCount = posts().length, beforeOpen = testCalls().length
    await openTest().click(); await dialog().waitFor()
    assert.equal(await dialog().locator('.np-test-row').count(), config.availableServices.length)
    assert.equal(await page.getByRole('combobox', { name: '测试服务' }).count(), 0)
    assert.equal(testCalls().length, beforeOpen)
    await testOne('telegram_user').click(); await row('telegram_user').locator('.np-test-result').filter({ hasText: '成功' }).waitFor()
    const tested = testCalls().at(-1).body
    assert.equal(tested.url, 'socks5h://proxy.example:1080'); assert.equal(tested.password, 'new-test-secret'); assert.equal(tested.service, 'telegram_user')
    assert.equal(tested.services.includes('telegram_user'), true); assert.equal(tested.services.includes('telegram_bot'), false)
    assert.equal(await row('telegram_user').locator('.np-test-latency').innerText(), '42 ms')
    assert.equal(await row('telegram_user').locator('.np-test-result').innerText().then(text => text.includes('HTTP')), false)
    await testOne('telegram_bot').click(); await row('telegram_bot').locator('.np-test-result').filter({ hasText: '成功' }).waitFor()
    assert.equal(testCalls().at(-1).body.services.includes('telegram_bot'), false)
    assert.match(await row('telegram_bot').innerText(), /直连/)
    await closeTest()
    assert.equal(posts().length, saveCount); assert.equal(config.url, 'http://proxy.example:7890')
    assert.equal(await password().inputValue(), 'new-test-secret')
    failSave = true; await save().click(); await page.getByRole('alert').filter({ hasText: '模拟配置校验失败' }).waitFor()
    assert.equal(await url().inputValue(), tested.url); assert.equal(await password().inputValue(), tested.password)
    const savesAfterFailure = posts().length
    failSave = false; failTest = true; await openTest().click(); await testOne('telegram_user').click()
    await row('telegram_user').locator('.np-test-result').filter({ hasText: '失败' }).waitFor(); failTest = false
    const cancelled = holdNextTest(); await testOne('telegram_user').click(); await cancelled.started
    assert.equal(await testOne('tmdb').isDisabled(), true); assert.equal(await batch().isDisabled(), true)
    await dialog().getByRole('button', { name: '取消测试', exact: true }).click()
    await row('telegram_user').locator('.np-test-result').filter({ hasText: '取消' }).waitFor()
    cancelled.release(); await cancelled.completed
    await page.waitForTimeout(100); assert.doesNotMatch(await row('telegram_user').locator('.np-test-result').innerText(), /成功/)
    const closed = holdNextTest(); await testOne('telegram_user').click(); await closed.started
    await closeTest(); await dialog().waitFor({ state: 'detached' })
    assert.equal(await password().inputValue(), tested.password)
    const beforeReopen = testCalls().length; await openTest().click(); assert.equal(testCalls().length, beforeReopen)
    closed.release(); await closed.completed; await page.waitForTimeout(100)
    assert.doesNotMatch(await row('telegram_user').locator('.np-test-result').innerText(), /成功/)
    await closeTest(); await url().fill('socks5h://proxy.example:1081'); await openTest().click()
    assert.doesNotMatch(await dialog().locator('.np-test-result').allTextContents().then(values => values.join(' ')), /成功|失败|取消/)
    const batchStart = testCalls().length, first = holdNextTest(); maxActiveTests = 0
    await batch().click(); await first.started
    assert.equal(testCalls().length, batchStart + 1)
    assert.equal(await testOne('telegram_user').isDisabled(), true); assert.equal(await batch().isDisabled(), true)
    first.release(); await first.completed
    await row('telegram_user').locator('.np-test-result').filter({ hasText: '成功' }).waitFor()
    await dialog().getByRole('button', { name: '取消测试', exact: true }).waitFor({ state: 'detached' })
    assert.deepEqual(testCalls().slice(batchStart).map(call => call.body.service), ['garden', 'tmdb', 'telegram_user'])
    assert.equal(maxActiveTests, 1); assert.equal(posts().length, savesAfterFailure)
    const cancelBatchStart = testCalls().length, cancelFirst = holdNextTest()
    await batch().click(); await cancelFirst.started
    await dialog().getByRole('button', { name: '取消测试', exact: true }).click()
    await row('garden').locator('.np-test-result').filter({ hasText: '取消' }).waitFor()
    cancelFirst.release(); await cancelFirst.completed; await page.waitForTimeout(100)
    assert.deepEqual(testCalls().slice(cancelBatchStart).map(call => call.body.service), ['garden'])
    await closeTest()
    assert.equal(await password().inputValue(), tested.password)
    passed.push('test modal opens without requests; scoped per-service results, cancellation and closed late responses preserve unsaved input')
    passed.push('batch tests only selected services sequentially; cancellation stops the remaining queue; form edits invalidate results')

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
    assert.equal(await page.evaluate(() => Object.values(localStorage).some(value => value.includes('transient-secret'))), false)
    await page.getByRole('link', { name: '资源配置', exact: true }).click(); await leave.waitFor()
    await leave.getByRole('button', { name: '放弃修改并离开', exact: true }).click(); await page.waitForURL(origin + '/resources')
    await page.locator('main').getByRole('link', { name: '网络代理', exact: true }).click(); await page.waitForFunction(() => !document.querySelector('.np-fields')?.disabled)
    assert.equal(await password().inputValue(), '')
    assert.equal(await switcher().isChecked(), true)
    passed.push('collapsed auth retains edits, leaving can cancel/discard, explicit password clearing, no secret persistence')

    const artifact = path.join(os.tmpdir(), 'cinema115-network-proxy-ui'); await fs.mkdir(artifact, { recursive: true })
    for (const [width, theme] of [[1440, 'light'], [390, 'light'], [320, 'dark']]) {
      await page.setViewportSize({ width, height: 1000 }); await page.evaluate(theme => document.documentElement.classList.toggle('dark', theme === 'dark'), theme)
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true)
      await page.screenshot({ path: path.join(artifact, `network-${width}-${theme}.png`), fullPage: true })
      const before = testCalls().length; await openTest().click(); await dialog().waitFor()
      assert.equal(testCalls().length, before)
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true)
      assert.equal(await dialog().evaluate(element => { const box = element.getBoundingClientRect(); return box.left >= -1 && box.right <= innerWidth + 1 && box.top >= -1 && box.bottom <= innerHeight + 1 }), true)
      await page.screenshot({ path: path.join(artifact, `network-test-${width}-${theme}.png`), fullPage: true })
      const running = holdNextTest(); await testOne('tmdb').click(); await running.started
      for (const name of ['关闭弹窗', '取消测试']) {
        const button = dialog().getByRole('button', { name, exact: true })
        assert.equal(await button.isVisible(), true)
        assert.equal(await button.evaluate(element => { const box = element.getBoundingClientRect(); return box.left >= -1 && box.right <= innerWidth + 1 && box.top >= -1 && box.bottom <= innerHeight + 1 }), true)
      }
      await page.screenshot({ path: path.join(artifact, `network-test-running-${width}-${theme}.png`), fullPage: true })
      await dialog().getByRole('button', { name: '取消测试', exact: true }).click()
      await row('tmdb').locator('.np-test-result').filter({ hasText: '取消' }).waitFor()
      running.release(); await running.completed
      await closeTest()
    }
    assert.deepEqual(errors, [])
    passed.push('320/390px and desktop light/dark page and test-modal layouts without viewport overflow; close/cancel remain reachable')
    console.log(JSON.stringify({ passed, artifact }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
