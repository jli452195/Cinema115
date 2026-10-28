// Isolated browser context: every API is mocked; no real login or account changes.
const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const passed = [], pageErrors = []
  try {
    const context = await browser.newContext({ serviceWorkers: 'block' })
    const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    let renewal = false, unavailable = false, probes = 0
    await context.route('**/api/**', async route => {
      const request = route.request(), endpoint = new URL(request.url()).pathname
      const token = (request.headers().authorization || '').replace('Bearer ', '')
      let data = { items: [], total: 0, count: 0, hasMore: false }, status = 200
      if (endpoint === '/api/auth/admin/login') data = { token: 'admin-original' }
      else if (endpoint === '/api/auth/me') {
        probes++
        if (unavailable) { status = 503; data = { error: '登录状态暂时无法验证，请稍后重试' } }
        else if (!token) { status = 401; data = { error: '请先登录' } }
        else {
          data = { user: token === 'reader' ? { id: 'reader', role: 'user', username: 'reader' } : { id: 'admin', role: 'admin', username: 'admin' } }
          if (renewal) { data.token = 'admin-renewed'; renewal = false }
        }
      }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const a = await context.newPage(), b = await context.newPage()
    for (const page of [a, b]) { page.setDefaultTimeout(10000); page.on('pageerror', e => pageErrors.push(e.message)) }
    const login = async page => {
      await page.getByLabel('管理员账号', { exact: true }).fill('admin')
      await page.getByLabel('密码', { exact: true }).fill('mock-password')
      await page.getByRole('button', { name: '登录管理后台', exact: true }).click()
      await page.waitForURL('**/115')
    }
    const state = page => page.evaluate(() => {
      const s = document.getElementById('app').__vue_app__._instance.setupState
      return { token: s.token, role: s.portalUser?.role, error: s.portalAuthError }
    })
    await a.goto(origin + '/admin/login?redirect=/115'); await login(a)
    await b.goto(origin + '/115'); await b.waitForURL('**/115')
    await b.waitForFunction(() => document.getElementById('app').__vue_app__._instance.setupState.portalUser?.role === 'admin')
    for (const page of [a, b]) await page.evaluate(() => { document.querySelector('main').dataset.authContinuity = 'kept' })
    renewal = true
    const before = probes
    await a.evaluate(() => document.getElementById('app').__vue_app__._instance.setupState.ensurePortalSession(true))
    await b.waitForFunction(() => document.getElementById('app').__vue_app__._instance.setupState.token === 'admin-renewed' && document.getElementById('app').__vue_app__._instance.setupState.portalUser?.role === 'admin')
    assert.equal((await state(a)).token, 'admin-renewed')
    assert.equal(new URL(a.url()).pathname, '/115'); assert.equal(new URL(b.url()).pathname, '/115')
    for (const page of [a, b]) assert.equal(await page.locator('main').getAttribute('data-auth-continuity'), 'kept')
    assert(probes - before <= 3)
    passed.push('renewal-synchronizes-tabs-without-login-redirect-or-request-loop')

    await b.evaluate(() => document.getElementById('app').__vue_app__._instance.setupState.logout())
    for (const page of [a, b]) await page.waitForURL('**/admin/login**')
    assert.equal((await state(a)).token, '')
    passed.push('explicit-logout-synchronizes-other-tab-and-admin-login-entry')

    await b.goto(origin + '/admin/login?redirect=/115'); await login(b)
    await a.waitForURL('**/115')
    passed.push('login-in-another-tab-restores-original-return-page')

    await b.evaluate(() => localStorage.setItem('admin_token', 'reader'))
    await a.waitForURL(origin + '/')
    assert.equal((await state(a)).role, 'user')
    passed.push('cross-tab-role-change-leaves-management-page-after-server-validation')
    await b.evaluate(() => localStorage.clear())
    await a.waitForURL('**/login**')
    assert.equal((await state(a)).token, '')
    passed.push('storage-clear-removes-verified-session')
    await b.goto(origin + '/admin/login?redirect=/115')
    unavailable = true
    await login(b)
    await b.getByRole('button', { name: '重新验证', exact: true }).waitFor()
    assert.equal((await state(b)).token, 'admin-original')
    unavailable = false
    await b.getByRole('button', { name: '重新验证', exact: true }).click()
    await b.waitForFunction(() => document.getElementById('app').__vue_app__._instance.setupState.portalUser?.role === 'admin')
    passed.push('post-login-verification-failure-keeps-token-and-retry-screen')
    assert.deepEqual(pageErrors, [])
    console.log(JSON.stringify({ passed, pageErrors }))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
