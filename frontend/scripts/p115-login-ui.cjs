// Mock all API traffic: no real QR sessions, Cookie changes or 115 requests.
const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage(), errors = [], writes = [], passed = []
    page.setDefaultTimeout(10000); page.on('pageerror', error => errors.push(error.message))
    let scenario = 'waiting', polls = 0, qrCount = 0, detection = '115ipad'
    const main = { id: 'main', name: '主账号', primary: true, configured: true, uid: '123', revision: 1, loginClient: '' }
    const pool = { id: 'pool', name: '小号 A', primary: false, configured: true, uid: '456', revision: 1, loginClient: '115ios', playbackParentCid: '77' }
    await page.route('**/api/**', async route => {
      const req = route.request(), url = new URL(req.url()), p = url.pathname
      let data = {}, status = 200
      if (p === '/api/auth/me') data = { user: { id: 'mock', role: 'admin', username: 'mock', disabled: false } }
      else if (p === '/api/admin/member-playback/accounts') data = { accounts: [pool] }
      else if (p === '/api/admin/p115/accounts') data = { accounts: [main, pool] }
      else if (p.startsWith('/api/admin/p115/accounts/')) {
        const account = p.includes('/pool') ? pool : main
        if (req.method() !== 'GET') writes.push({ path: p, body: req.postData() ? req.postDataJSON() : null })
        if (p.endsWith('/qrcode') && req.method() === 'POST') {
          qrCount++
          if (scenario === 'error') { status = 502; data = { error: '获取二维码失败，请稍后重试' } }
          else data = { qrId: 'qr-' + qrCount, sessionId: 'session-' + qrCount, qrcode: 'mock-qr-' + qrCount, expiresAt: new Date(Date.now() + (scenario === 'expiry' ? 600 : 180000)).toISOString() }
        } else if (p.endsWith('/qrcode/status')) {
          polls++
          if (scenario === 'success') {
            account.loginClient = '115ipad'; account.revision++
            data = { status: 'success', connected: true, loginClient: account.loginClient }
          } else data = { status: scenario === 'scanned' ? 'scanned' : scenario === 'cancelled' ? 'cancelled' : 'waiting', connected: false }
        } else if (p.endsWith('/status')) {
          if (!account.loginClient) account.loginClient = detection
          data = { configured: account.configured, connected: account.configured, loginClient: account.loginClient, clientMessage: account.loginClient ? '' : 'Cookie 有效，暂未识别登录客户端' }
        } else if (p.endsWith('/cookie/test')) data = { valid: true }
        else if (p.endsWith('/cookie')) data = { cookie: 'UID=123_A; SEID=mock' }
        else if (req.method() === 'PUT') {
          const body = req.postDataJSON(); account.revision++; account.name = body.name || account.name
          if (body.cookie) account.loginClient = ''
          data = { status: 'success', configured: true, connected: !!body.cookie, message: '账号配置已保存' }
        } else if (p.endsWith('/login') && req.method() === 'DELETE') {
          account.revision++; account.configured = false; account.loginClient = ''
        }
      }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    await page.goto(origin + '/helper115')
    const mainCard = page.getByRole('region', {name:'主账号配置'})
    const check = () => mainCard.getByRole('button', {name:'检测可用性',exact:true}).click()
    const openQR = () => mainCard.getByRole('button', {name:'扫码登录',exact:true}).click()
    await mainCard.getByText(/登录客户端：待识别/).waitFor()
    assert.equal(await page.getByLabel('Cookie', { exact: true }).count(), 0)
    await check()
    await mainCard.getByText(/登录客户端：115 iPad/).waitFor()
    await page.reload(); await mainCard.getByText(/登录客户端：115 iPad/).waitFor()
    assert.equal(await mainCard.locator('select').count(), 0)
    await openQR(); await page.getByLabel('登录设备', { exact: true }).selectOption('115-ios')
    assert(await mainCard.getByText(/登录客户端：115 iPad/).isVisible())
    await page.keyboard.press('Escape'); await page.getByRole('dialog').waitFor({state:'detached'})
    passed.push('detect-persist-and-selection-isolation')
    await mainCard.getByRole('button', {name:'更多操作'}).click()
    assert.equal(await mainCard.getByRole('button', {name:'修改名称',exact:true}).count(), 0)
    await mainCard.getByRole('button', { name: '手动配置 Cookie' }).click()
    await page.getByLabel('Cookie', { exact: true }).fill('UID=123_B; SEID=draft')
    assert.equal(main.loginClient, '115ipad')
    assert(await mainCard.getByText(/登录客户端：115 iPad/).isVisible())
    await page.getByRole('button', { name: '验证并继续', exact: true }).click()
    await page.getByRole('dialog').waitFor({state:'detached'})
    detection = ''
    await check()
    await mainCard.getByText(/登录客户端：未识别/).waitFor()
    assert(await mainCard.getByText('已连接', { exact: true }).isVisible())
    assert.equal(await mainCard.getByText('账号可用', {exact:true}).count(), 0)
    passed.push('draft-isolation-and-unidentified-valid-cookie')
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 844 })
      for (const dark of [false, true]) {
        await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
        await openQR()
        await page.getByLabel('登录设备', { exact: true }).selectOption('115-ipad')
        await page.getByRole('dialog').getByRole('img', { name: '115 登录二维码' }).waitFor()
        assert.equal(writes.filter(w=>w.path.endsWith('/qrcode')&&w.body?.clientType).at(-1).body.clientType, '115-ipad')
        const box = await page.getByRole('dialog').boundingBox()
        assert(box.x >= 0 && box.x + box.width <= width + 1)
        assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1))
        await page.keyboard.press('Escape'); await page.getByRole('dialog').waitFor({ state: 'detached' })
        assert.equal(await page.evaluate(() => document.body.style.overflow), '')
        const before = polls; await page.waitForTimeout(1700); assert.equal(polls, before)
        passed.push(`${width}:${dark ? 'dark' : 'light'}:close-stops-polling`)
      }
    }
    scenario = 'scanned'
    await openQR()
    await page.getByText('已扫码，请在手机上确认登录', { exact: true }).waitFor()
    scenario = 'success'
    await page.getByRole('dialog').waitFor({ state: 'detached' })
    await mainCard.getByText(/登录客户端：115 iPad/).waitFor()
    passed.push('scan-confirm-save-auto-close')
    scenario = 'expiry'
    await openQR()
    await page.getByText('登录会话已过期，请重新登录', { exact: true }).waitFor()
    assert.equal(await page.getByRole('dialog').getByRole('img').count(), 0)
    scenario = 'error'
    await page.getByRole('button', { name: '重新获取二维码', exact: true }).click()
    await page.getByText('获取二维码失败，请稍后重试', { exact: true }).waitFor()
    scenario = 'cancelled'
    await page.getByRole('button', { name: '重新获取二维码', exact: true }).click()
    await page.getByText('已取消登录，请重新获取二维码', { exact: true }).waitFor()
    await page.getByRole('button', { name: '关闭登录弹窗' }).click()
    passed.push('expiry-error-retry-cancelled')
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, pageErrors: errors }))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exit(1) })
