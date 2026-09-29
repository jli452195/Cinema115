const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 1000 }, serviceWorkers: 'block' })
    const page = await context.newPage(), errors = [], registrations = [], checks = []
    page.on('pageerror', error => errors.push(error.message))
    const code = '01234567-89abcdef-01234567-89abcdef'
    const invite = { id: 'invite-one', note: '朋友', createdBy: 'admin', createdAt: Date.now()/1000, expiresAt: Date.now()/1000+604800, revokedAt: 0, usedAt: 0, usedBy: '', username: '' }
    let generated = false
    await page.route('**/api/**', async route => {
      const req = route.request(), url = new URL(req.url()); let data = {}
      if (url.pathname === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin', disabled: false } }
      else if (url.pathname === '/api/admin/users') data = { users: [], hasMore: false }
      else if (url.pathname === '/api/admin/invites') {
        if (req.method() === 'POST') { generated = true; assert.equal(req.postDataJSON().days, 7); assert.equal(req.postDataJSON().playbackSource, 'primary'); data = { invites: [{ ...invite, playbackSource: 'primary', code }] } }
        else data = { invites: generated ? [invite] : [], hasMore: false }
      } else if (url.pathname.endsWith('/revoke')) { invite.revokedAt = Date.now()/1000; data = { message: '邀请已撤销' } }
      else if (url.pathname === '/api/auth/invite/check') { checks.push(req.postDataJSON()); data = { valid: true, playbackSource: 'primary' } }
      else if (url.pathname === '/api/auth/register') { registrations.push(req.postDataJSON()); data = { user: { username: 'newreader', role: 'user' } } }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
    })
    const base = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    await page.goto(base + '/register#invite=' + code)
    await page.getByText('朋友邀请有效：注册后可创建 Emby 账号，使用管理员主账号播放，无需扫码。', { exact: true }).waitFor()
    assert.equal(new URL(page.url()).hash, '')
    assert.equal(await page.getByLabel('邀请码', { exact: true }).inputValue(), code)
    assert.equal(checks.length, 1)
    assert.equal(registrations.length, 0)
    await page.getByLabel('用户名', { exact: true }).fill('newreader')
    await page.getByLabel('密码', { exact: true }).fill('test-password')
    await page.getByLabel('确认密码', { exact: true }).fill('different-password')
    await page.getByRole('button', { name: '注册账号', exact: true }).click()
    await page.getByText('两次输入的密码不一致', { exact: true }).waitFor()
    assert.equal(registrations.length, 0)
    await page.getByLabel('确认密码', { exact: true }).fill('test-password')
    await page.getByRole('button', { name: '注册账号', exact: true }).click()
    await page.getByText('注册成功，请使用刚设置的密码登录。').waitFor()
    assert.equal(await page.getByLabel('账号', { exact: true }).inputValue(), 'newreader')
    assert.equal(await page.getByLabel('密码', { exact: true }).inputValue(), '')
    assert.deepEqual(registrations, [{ username: 'newreader', password: 'test-password', code }])
    assert.equal(await page.evaluate(() => JSON.stringify(localStorage).includes('01234567')), false)
    await page.evaluate(() => localStorage.setItem('admin_token', 'mock'))
    await page.goto(base + '/users')
    await page.getByRole('button', { name: '注册邀请', exact: true }).click()
    await page.getByLabel('备注', { exact: true }).fill('朋友')
    assert.equal(await page.getByLabel('播放开通方式').inputValue(), 'personal')
    await page.getByLabel('播放开通方式').selectOption('primary')
    await page.getByRole('button', { name: '生成邀请', exact: true }).click()
    const link = page.getByLabel('邀请链接', { exact: true })
    await link.waitFor()
    assert.equal(await link.inputValue(), base + '/register#invite=' + code)
    await page.getByLabel('邀请链接的站点地址', { exact: false }).fill('https://cinema.example.com')
    assert.equal(await link.inputValue(), 'https://cinema.example.com/register#invite=' + code)
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.setViewportSize({ width: 390, height: 844 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    await page.getByRole('button', { name: '撤销', exact: true }).click()
    await page.getByText('邀请已撤销', { exact: true }).waitFor()
    assert.equal(await link.count(), 0)
    assert.deepEqual(errors, [])
    console.log('Invitation UI passed: hash import/removal, password confirmation, registration/login handoff, generation, custom link address, revocation, dark mobile layout')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
