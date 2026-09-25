// Mock endpoints only: never log in to NanShare or submit remote jobs.
const path = require('node:path')
const os = require('node:os')
const assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage()
    const errors = [], logins = [], searches = []
    page.on('pageerror', e => errors.push(e.message))
    let role = 'admin', expired = false, failLogin = false
    const blank = () => ({ baseUrl: '', username: '', configured: false, checkedAt: '', session: { ok: false, message: '' }, re0: { ok: false, message: '' }, share: { ok: false, message: '' } })
    let config = blank()
    await page.route('**/api/**', async route => {
      const request = route.request(), u = new URL(request.url())
      let data = {}, status = 200
      if (u.pathname === '/api/auth/me') data = { user: { id: 'mock', username: 'admin', role, disabled: false } }
      else if (u.pathname === '/api/admin/nanshare/config') {
        if (request.method() === 'POST') {
          const input = request.postDataJSON(); logins.push(input)
          if (failLogin) { status = 409; data = { error: 'NanShare 登录失败或会话已过期，请重新登录' } }
          else {
            config = { baseUrl: input.baseUrl, username: input.username, autoLogin: true, configured: true, checkedAt: new Date().toISOString(), session: { ok: true, message: '登录验证通过' }, re0: { ok: true, message: 'RE0 搜索配置读取成功' }, share: { ok: true, message: '分享 STRM 配置读取成功（尚未验证任务执行）' } }
            data = config
          }
        } else { if (request.method() === 'DELETE') config = blank(); data = config }
      } else if (u.pathname === '/api/admin/nanshare/verify') data = expired ? { ...config, session: { ok: false, message: 'NanShare 登录失败或会话已过期，请重新登录' }, re0: { ok: false, message: '' }, share: { ok: false, message: '' } } : config
      else if (u.pathname === '/api/admin/nanshare/search-test') { searches.push(request.postDataJSON()); data = { total: 139, p115: 100 } }
      else if (u.pathname.includes('/notifications')) data = { items: [], unread: 0 }
      else data = { items: [], total: 0, page: 1, totalPages: 1 }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const base = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    await page.goto(base + '/nanshare')
    await page.getByLabel('用户名', { exact: true }).fill('test-user')
    await page.getByLabel('密码', { exact: true }).fill('mock-password')
    await page.getByRole('button', { name: '保存并登录' }).click()
    await page.getByText('登录验证通过', { exact: true }).waitFor()
    assert.equal(await page.getByLabel('密码', { exact: true }).inputValue(), '')
    assert.equal(logins[0].password, 'mock-password')
    assert.equal(logins[0].token, undefined)
    assert.equal(await page.getByLabel('登录方式').count(), 0)
    assert.equal(await page.getByLabel('会话令牌', { exact: true }).count(), 0)
    await page.getByText('已启用自动登录：密码已加密保存。', { exact: true }).waitFor()
    await page.getByRole('button', { name: '测试搜索', exact: true }).click()
    await page.getByRole('status').filter({ hasText: '139' }).waitFor()
    assert.deepEqual(searches, [{ kind: 'tv', tmdbId: 106449 }])
    await page.getByLabel('NanShare 地址').fill('http://another-host:8115')
    assert.equal(await page.getByRole('button', { name: '测试搜索', exact: true }).isDisabled(), true)
    await page.getByLabel('NanShare 地址').fill(config.baseUrl)
    expired = true
    await page.getByRole('button', { name: '重新检查', exact: true }).click()
    await page.getByText('NanShare 登录失败或会话已过期，请重新登录', { exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: '测试搜索', exact: true }).isDisabled(), true)
    expired = false
    await page.getByLabel('密码', { exact: true }).fill('updated-password')
    await page.getByRole('button', { name: '保存并登录' }).click()
    await page.getByText('登录验证通过', { exact: true }).waitFor()
    assert.equal(await page.getByLabel('密码', { exact: true }).inputValue(), '')
    assert.equal(await page.getByLabel('用户名', { exact: true }).inputValue(), 'test-user')
    assert.equal(logins[1].password, 'updated-password')
    assert.ok(!(await page.evaluate(() => JSON.stringify(localStorage))).includes('updated-password'))
    failLogin = true
    await page.getByLabel('密码', { exact: true }).fill('invalid-password')
    await page.getByRole('button', { name: '保存并登录' }).click()
    await page.getByRole('alert').waitFor()
    assert.equal(await page.getByLabel('密码', { exact: true }).inputValue(), '')
    await page.setViewportSize({ width: 390, height: 844 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    await page.getByRole('button', { name: '清除登录信息', exact: true }).click()
    await page.getByRole('button', { name: '确认清除', exact: true }).click()
    await page.getByText('NanShare 登录信息已清除。', { exact: true }).waitFor()
    assert.equal(await page.getByRole('heading', { name: '测试 RE0 搜索' }).count(), 0)
    role = 'user'
    await page.reload()
    await page.waitForURL(base + '/')
    assert.deepEqual(errors, [])
    console.log('NanShare UI passed: password-only login, automatic-login status, secret clearing, search, expiry, clear, mobile, access control')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
