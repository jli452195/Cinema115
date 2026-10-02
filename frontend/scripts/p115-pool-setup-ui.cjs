// Exercise setup with mocked APIs only; never log in to or modify a real 115 account.
const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    page.setDefaultTimeout(10000)
    const errors = [], passed = [], pool = []
    const main = { id: 'main', name: '主账号', primary: true, configured: true, uid: '1', revision: 1 }
    let qrSuccess = false, failSave = false, lastSave
    page.on('pageerror', e => errors.push(e.message))
    await page.route('**/api/**', async route => {
      const req = route.request(), url = new URL(req.url()), p = url.pathname
      let data = {}, status = 200
      if (p === '/api/auth/me') data = { user: { id: 'admin', role: 'admin', username: 'admin' } }
      else if (p === '/api/admin/member-playback/accounts') data = { accounts: pool }
      else if (p === '/api/admin/p115/accounts') {
        if (req.method() === 'POST') {
          const account = { id: 'pool-' + pool.length, name: req.postDataJSON().name, primary: false, configured: false, uid: '', revision: 1, playbackParentCid: '' }
          pool.push(account); data.id = account.id
        }
        data.accounts = [main, ...pool]
      } else {
        const account = [main, ...pool].find(a => p.startsWith('/api/admin/p115/accounts/' + a.id + '/') || p === '/api/admin/p115/accounts/' + a.id)
        if (account) {
          const login = () => { account.configured = true; account.uid = '123'; account.revision++; return { status: 'success', connected: true } }
          if (p.endsWith('/qrcode')) data = { qrId: 'qr', sessionId: 'session', qrcode: 'mock', expiresAt: new Date(Date.now() + 180000).toISOString() }
          else if (p.endsWith('/qrcode/status')) data = qrSuccess ? login() : { status: 'waiting', connected: false }
          else if (p.endsWith('/playback-directory')) {
            lastSave = req.postDataJSON()
            assert.equal(lastSave.revision, account.revision, 'directory must use the revision refreshed after login')
            if (failSave) { status = 422; data = { error: '目录校验失败，请重新选择' } }
            else { account.playbackParentCid = lastSave.parentCid; account.revision++; data = { accounts: [main, ...pool] } }
          } else if (p.endsWith('/paths')) data = { directories: url.searchParams.get('cid') === '0' ? [{ id: '55', name: '播放文件' }] : [], hasMore: false }
          else if (p.endsWith('/status')) data = { connected: true }
          else if (req.method() === 'PUT') data = login()
        }
      }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/helper115')
    await page.getByRole('button', { name: '添加共享小号', exact: true }).click()
    await page.getByRole('dialog').getByText('先登录，再设置秒传目录。完成目录设置后，才能分配用户使用。', { exact: true }).waitFor()
    await page.getByRole('button', { name: '关闭登录弹窗' }).click()
    const card = page.locator('article').filter({ has: page.getByRole('heading', { name: '共享小号 1', exact: true }) })
    await card.getByText('未设置（必填）', { exact: true }).waitFor()
    assert(await card.getByRole('button', { name: '设置目录', exact: true }).isDisabled())
    passed.push('create-auto-login-and-cancel-resumable')
    await card.getByRole('button', { name: '更多操作' }).click()
    await card.getByRole('button', { name: '手动配置 Cookie' }).click()
    await page.getByLabel('Cookie', { exact: true }).fill('UID=123_A; SEID=mock')
    await page.getByRole('button', { name: '验证并继续', exact: true }).click()
    await page.getByRole('dialog', { name: '设置 共享小号 1 的秒传目录' }).waitFor()
    await page.getByRole('button', { name: '关闭目录选择器' }).click()
    await card.getByText('待设置目录', { exact: true }).waitFor()
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    await card.getByRole('button', { name: '检测可用性', exact: true }).click()
    await card.getByRole('button', { name: '检测可用性', exact: true }).waitFor()
    assert.equal(await card.getByText('已连接', { exact: true }).count(), 0)
    passed.push('cookie-login-next-step-and-missing-directory-status')
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 844 })
      for (const dark of [false, true]) {
        await page.evaluate(d => document.documentElement.classList.toggle('dark', d), dark)
        const warning = card.getByText('未设置（必填）', { exact: true })
        const color = await warning.evaluate(e => getComputedStyle(e).color)
        assert.equal(color, dark ? 'rgb(248, 113, 113)' : 'rgb(220, 38, 38)')
        assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1))
        await card.screenshot({ path: path.join(os.tmpdir(), `cinema115-setup-${width}-${dark ? 'dark' : 'light'}.png`) })
      }
    }
    passed.push('desktop-mobile-light-dark-warning')
    await card.getByRole('button', { name: '设置目录', exact: true }).click()
    await page.getByRole('button', { name: '播放文件', exact: true }).click()
    failSave = true
    await page.getByRole('button', { name: '保存目录并完成配置', exact: true }).click()
    await page.getByRole('dialog').getByRole('alert').getByText('目录校验失败，请重新选择', { exact: true }).waitFor()
    assert.equal(pool[0].playbackParentCid, '')
    failSave = false
    await page.getByRole('button', { name: '保存目录并完成配置', exact: true }).click()
    await page.getByRole('dialog').waitFor({ state: 'detached' })
    await card.getByText('秒传目录：55', { exact: true }).waitFor()
    assert.equal(await card.getByText('未设置（必填）', { exact: true }).count(), 0)
    assert.equal(lastSave.parentCid, '55')
    passed.push('directory-save-failure-retry-and-complete')
    qrSuccess = true
    await card.getByRole('button', { name: '扫码登录', exact: true }).click()
    await page.getByRole('dialog').waitFor({ state: 'visible' })
    await page.getByRole('dialog').waitFor({ state: 'detached' })
    await card.getByText('已连接', { exact: true }).waitFor()
    assert.equal(pool[0].playbackParentCid, '55')
    passed.push('same-account-relogin-keeps-directory')
    await page.getByRole('button', { name: '添加共享小号', exact: true }).click()
    await page.getByRole('dialog', { name: '设置 共享小号 2 的秒传目录' }).waitFor()
    await page.getByRole('button', { name: '保存目录并完成配置', exact: true }).click()
    await page.getByRole('dialog').waitFor({ state: 'detached' })
    const second = page.locator('article').filter({ has: page.getByRole('heading', { name: '共享小号 2', exact: true }) })
    await second.getByText('秒传目录：0', { exact: true }).waitFor()
    assert.equal(pool[1].playbackParentCid, '0')
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    await page.reload()
    await second.getByText('秒传目录：0', { exact: true }).waitFor()
    assert.equal(await page.getByRole('dialog').count(), 0)
    passed.push('qr-login-next-step-explicit-root-and-refresh')
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, pageErrors: errors }))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exit(1) })
