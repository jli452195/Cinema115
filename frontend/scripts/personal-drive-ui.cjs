const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const page = await browser.newPage({ viewport: { width: 390, height: 844 }, serviceWorkers: 'block' })
    const errors = [], writes = [], reads = []
    page.on('pageerror', e => errors.push(e.message))
    await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const state = { accountId: '', revision: 0, source: 'primary', canPreparePersonal: true, enabled: true, stage: 'ready', message: '主网盘播放', uid: '', connected: false, directoryId: '', pendingDrive: false, embyName: 'reader' }
    let qr = 0, pollStatus = 'waiting', rejectSave = false
    await page.route('**/api/**', async route => {
      const request = route.request(), url = new URL(request.url()), p = url.pathname
      let data = {}
      if (p.startsWith('/api/admin/')) throw new Error('Personal flow called admin API')
      if (p === '/api/auth/me') data = { user: { id: 'reader', username: 'reader', role: 'user' } }
      else if (p === '/api/me/playback') data = state
      else if (p === '/api/me/playback/children') data = { limit: 0, used: 0, items: [] }
      else if (p.endsWith('/qrcode')) { writes.push(request.postDataJSON()); data = { qrId: 'qr-' + ++qr, qrcode: 'mock-' + qr, expiresAt: new Date(Date.now() + 180000).toISOString() } }
      else if (p.endsWith('/qrcode/status')) {
        assert.ok(url.searchParams.has('accountId'))
        if (pollStatus === 'success') Object.assign(state, { pendingDrive: true, directoryAccount: { accountId: 'personal-new', revision: 1, uid: '123', directoryId: '', creationUncertain: false } })
        data = { status: pollStatus, playback: state }
      } else if (p.endsWith('/directories')) {
        reads.push(Object.fromEntries(url.searchParams))
        assert.equal(url.searchParams.get('accountId'), 'personal-new')
        data = url.searchParams.get('cid') === '0' ? { directories: [{ id: '123', name: '我的影片' }], nextOffset: 2, hasMore: false } : { directories: [], nextOffset: 0, hasMore: false }
      } else if (p.endsWith('/directory')) {
        const body = request.postDataJSON(); writes.push(body)
        if (rejectSave) return route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: '目录校验失败，请重新选择' }) })
        const cid = body.create ? '789' : body.directoryId
        Object.assign(state, { accountId: 'personal-new', revision: state.directoryAccount.revision + 1, pendingDrive: false, uid: '123', connected: true, directoryId: cid })
        state.directoryAccount = { ...state.directoryAccount, revision: state.revision, directoryId: cid }
        data = state
      }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
    })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/account/playback')
    await page.getByLabel('登录客户端').selectOption('wechat-miniapp')
    await page.getByRole('checkbox', { name: /我同意保存凭据/ }).check()
    await page.getByRole('button', { name: '扫码绑定 115', exact: true }).click()
    await page.getByAltText('115 登录二维码').waitFor()
    assert.deepEqual(writes[0], { replace: true, clientType: 'wechat-miniapp', accountId: '', revision: 0 })
    await page.getByLabel('登录客户端').selectOption('115-ios')
    assert.equal(await page.getByAltText('115 登录二维码').count(), 0)
    pollStatus = 'success'
    await page.getByRole('button', { name: '扫码绑定 115', exact: true }).click()
    await page.getByText('115 登录成功，请选择秒传保存目录完成绑定', { exact: true }).waitFor()
    assert.equal(writes[1].clientType, '115-ios')
    assert.equal(await page.getByRole('button', { name: '切换到我的网盘播放' }).isDisabled(), true)
    await page.getByRole('button', { name: '选择秒传目录', exact: true }).click()
    let dialog = page.getByRole('dialog')
    await dialog.getByText('我的影片', { exact: true }).waitFor()
    assert.equal(await dialog.getByRole('button', { name: '选择此目录', exact: true }).isDisabled(), true)
    await dialog.getByText('我的影片', { exact: true }).click()
    await dialog.getByText('当前目录没有子文件夹').waitFor()
    await dialog.getByRole('button', { name: '选择此目录', exact: true }).click()
    assert.equal(await page.getByLabel('秒传保存目录 ID').inputValue(), '123')
    assert.equal(writes.length, 2)
    await page.getByRole('button', { name: '确认目录并完成绑定' }).click()
    await page.getByRole('button', { name: '更换秒传目录', exact: true }).waitFor()
    assert.equal(state.source, 'primary')
    assert.deepEqual(writes.at(-1), { accountId: 'personal-new', revision: 1, directoryId: '123', create: false })
    rejectSave = true
    await page.getByLabel('秒传保存目录 ID').fill('456')
    await page.getByRole('button', { name: '保存秒传目录', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '目录校验失败' }).waitFor()
    assert.equal(state.directoryId, '123')
    rejectSave = false
    await page.getByRole('button', { name: '更换秒传目录', exact: true }).click()
    dialog = page.getByRole('dialog')
    await dialog.getByText('我的影片', { exact: true }).waitFor()
    await dialog.getByRole('button', { name: '在此新建 Cinema115 并使用' }).click()
    await page.getByLabel('秒传保存目录 ID').filter({ visible: true }).waitFor()
    await page.waitForFunction(() => document.querySelector('input[placeholder="选择文件夹，也可以填写目录编号"]')?.value === '789')
    assert.deepEqual(writes.at(-1), { accountId: 'personal-new', revision: 2, directoryId: '0', create: true })
    assert.ok(reads.some(r => r.cid === '123'))
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    assert.deepEqual(errors, [])
    console.log('personal drive UI passed')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
