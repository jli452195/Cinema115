const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const page = await browser.newPage({ viewport: { width: 390, height: 844 }, serviceWorkers: 'block' })
    const errors = [], writes = [], reads = []
    page.on('pageerror', e => errors.push(e.message))
    await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const state = { accountId: '', revision: 0, source: 'primary', canPreparePersonal: true, enabled: true, stage: 'ready', message: '主网盘播放', uid: '', connected: false, directoryId: '', pendingDrive: false, embyName: 'reader', embyUrl: 'https://emby.example.com' }
    let qr = 0, pollStatus = 'waiting', rejectSave = false, sameLogin = false, uncertainCreate = false, failSwitch = false, failRecovery = false, failChildDirectory = true
    await page.route('**/api/**', async route => {
      const request = route.request(), url = new URL(request.url()), p = url.pathname
      let data = {}
      if (p.startsWith('/api/admin/')) throw new Error('Personal flow called admin API')
      if (p === '/api/auth/me') data = { user: { id: 'reader', username: 'reader', role: 'user' } }
      else if (p === '/api/me/playback') { if (failRecovery) return route.fulfill({ status: 502, contentType: 'application/json', body: JSON.stringify({ error: '状态读取暂不可用' }) }); data = state }
      else if (p === '/api/me/playback/children') data = { limit: 0, used: 0, items: [] }
      else if (p.endsWith('/qrcode')) { writes.push(request.postDataJSON()); data = { qrId: 'qr-' + ++qr, qrcode: 'mock-' + qr, expiresAt: new Date(Date.now() + 180000).toISOString() } }
      else if (p.endsWith('/qrcode/status')) {
        assert.ok(url.searchParams.has('accountId'))
        if (pollStatus === 'success' && !sameLogin) Object.assign(state, { pendingDrive: true, directoryAccount: { accountId: 'personal-new', revision: 1, uid: '123', directoryId: '', creationUncertain: false } })
        data = { status: pollStatus, playback: state }
      } else if (p.endsWith('/directories')) {
        reads.push(Object.fromEntries(url.searchParams))
        assert.equal(url.searchParams.get('accountId'), 'personal-new')
        if (url.searchParams.get('cid') === '123' && failChildDirectory) { failChildDirectory = false; return route.fulfill({ status: 502, contentType: 'application/json', body: JSON.stringify({ error: '子目录暂时读取失败' }) }) }
        data = url.searchParams.get('cid') === '0' ? { directories: [{ id: '123', name: '我的影片' }], nextOffset: 2, hasMore: false } : { directories: [], nextOffset: 0, hasMore: false }
      } else if (p.endsWith('/directory')) {
        const body = request.postDataJSON(); writes.push(body)
        if (rejectSave) return route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: '目录校验失败，请重新选择' }) })
        if (body.create && uncertainCreate) { state.directoryAccount.creationUncertain = true; return route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: '目录创建结果未确认，请选择已有目录' }) }) }
        const cid = body.create ? '789' : body.directoryId
        Object.assign(state, { accountId: 'personal-new', revision: state.directoryAccount.revision + 1, pendingDrive: false, uid: '123', connected: true, directoryId: cid })
        state.directoryAccount = { ...state.directoryAccount, revision: state.revision, directoryId: cid }
        data = state
      } else if (p.endsWith('/use-personal')) {
        writes.push({ action: 'use-personal' })
        if (failSwitch) return route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: '个人网盘核验失败，来源保留' }) })
        state.source = 'personal'; data = state
      } else if (p.endsWith('/drive-change')) { state.pendingDrive = false; data = state }
      else if (p.endsWith('/disconnect')) { writes.push({ action: 'disconnect' }); state.uid = ''; state.connected = false; state.directoryId = ''; state.directoryAccount = undefined; data = state }
      else if (p.endsWith('/refresh')) data = state
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
    })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/account/playback')
    assert.equal(await page.locator('main input').count(), 0)
    await page.getByRole('button', { name: '开始绑定' }).click()
    await page.getByLabel('登录客户端').selectOption('wechat-miniapp')
    await page.getByRole('checkbox', { name: /我同意保存凭据/ }).check()
    await page.getByRole('button', { name: '获取二维码', exact: true }).click()
    await page.getByAltText('115 登录二维码').waitFor()
    assert.deepEqual(writes[0], { replace: true, clientType: 'wechat-miniapp', accountId: '', revision: 0 })
    await page.getByLabel('登录客户端').selectOption('115-ios')
    assert.equal(await page.getByAltText('115 登录二维码').count(), 0)
    pollStatus = 'success'
    await page.getByRole('button', { name: '获取二维码', exact: true }).click()
    await page.getByText('115 登录成功，请选择秒传保存目录完成绑定', { exact: true }).waitFor()
    assert.equal(writes[1].clientType, '115-ios')
    assert.equal(state.source, 'primary')
    await page.getByRole('dialog').getByRole('button', { name: '关闭', exact: true }).click()
    await page.reload()
    await page.getByRole('button', { name: '继续选择目录', exact: true }).click()
    await page.getByLabel('秒传保存目录 ID').pressSequentially('321')
    assert.equal(await page.getByLabel('秒传保存目录 ID').inputValue(), '321')
    assert.equal(await page.getByLabel('秒传保存目录 ID').isVisible(), true, 'manual directory input must remain expanded while typing')
    await page.getByRole('button', { name: '选择秒传目录', exact: true }).click()
    let dialog = page.getByRole('dialog')
    await dialog.getByText('我的影片', { exact: true }).waitFor()
    assert.equal(await dialog.getByRole('button', { name: '选择此目录', exact: true }).isDisabled(), true)
    await dialog.getByText('我的影片', { exact: true }).click()
    await dialog.getByRole('alert').filter({ hasText: '子目录暂时读取失败' }).waitFor()
    const beforeRetry = reads.length
    await dialog.getByRole('button', { name: '重新读取' }).click()
    await dialog.getByText('当前目录没有子文件夹').waitFor()
    assert.equal(reads[beforeRetry].cid, '123')
    await dialog.getByRole('button', { name: '选择此目录', exact: true }).click()
    assert.equal(await page.getByRole('dialog').count(), 1)
    assert.equal(writes.length, 2)
    await page.getByRole('button', { name: '确认目录并完成绑定' }).click()
    await page.getByRole('heading', { name: '个人网盘绑定已完成' }).waitFor()
    await page.getByRole('button', { name: '保持当前来源' }).click()
    assert.equal(state.source, 'primary')
    assert.deepEqual(writes.at(-1), { accountId: 'personal-new', revision: 1, directoryId: '123', create: false })
    rejectSave = true
    await page.getByRole('button', { name: '更换秒传目录', exact: true }).click()
    await page.getByText('手动填写目录编号', { exact: true }).click()
    await page.getByLabel('秒传保存目录 ID').fill('456')
    await page.getByRole('button', { name: '保存秒传目录', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '目录校验失败' }).waitFor()
    assert.equal(state.directoryId, '123')
    assert.equal(await page.getByLabel('秒传保存目录 ID').inputValue(), '456')
    await page.getByRole('dialog').getByRole('button', { name: '关闭', exact: true }).click()
    rejectSave = false
    await page.getByRole('button', { name: '更换秒传目录', exact: true }).click()
    await page.getByRole('button', { name: '选择秒传目录', exact: true }).click()
    dialog = page.getByRole('dialog')
    await dialog.getByText('我的影片', { exact: true }).waitFor()
    await dialog.getByRole('button', { name: '在此新建 Cinema115 并使用' }).click()
    await page.getByRole('heading', { name: '管理个人网盘', exact: true }).waitFor()
    assert.deepEqual(writes.at(-1), { accountId: 'personal-new', revision: 2, directoryId: '0', create: true })
    assert.ok(reads.some(r => r.cid === '123'))
    await page.getByRole('dialog').getByRole('button', { name: '关闭', exact: true }).click()
    uncertainCreate = true
    await page.getByRole('button', { name: '更换秒传目录', exact: true }).click()
    await page.getByRole('button', { name: '选择秒传目录', exact: true }).click()
    await page.getByRole('dialog').getByText('我的影片', { exact: true }).waitFor()
    failRecovery = true
    await page.getByRole('button', { name: '在此新建 Cinema115 并使用' }).click()
    await page.getByRole('alert').filter({ hasText: '目录创建结果未确认' }).waitFor()
    assert.equal(await page.getByRole('button', { name: '在此新建 Cinema115 并使用' }).count(), 0)
    failRecovery = false
    await page.getByRole('button', { name: '取消', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: '关闭', exact: true }).click()
    failSwitch = true
    const before = writes.length
    await page.getByRole('button', { name: '切换到我的网盘播放', exact: true }).click()
    assert.equal(writes.length, before)
    await page.getByRole('button', { name: '确认切换', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '来源保留' }).waitFor()
    assert.equal(state.source, 'primary')
    failSwitch = false
    await page.getByRole('button', { name: '确认切换', exact: true }).click()
    await page.getByRole('heading', { name: '我的 115 网盘', exact: true }).waitFor()
    sameLogin = true
    await page.getByRole('button', { name: '重新扫码或换号', exact: true }).click()
    await page.getByRole('checkbox', { name: /我同意保存凭据/ }).check()
    await page.getByRole('button', { name: '获取二维码', exact: true }).click()
    await page.getByRole('heading', { name: '个人网盘绑定已完成' }).waitFor()
    assert.equal(state.directoryId, '789')
    assert.equal(await page.getByRole('button', { name: '选择秒传目录', exact: true }).count(), 0)
    await page.getByRole('dialog').getByRole('button', { name: '关闭', exact: true }).click()
    await page.getByRole('button', { name: '管理网盘', exact: true }).click()
    await page.getByRole('button', { name: '解除个人网盘绑定', exact: true }).click()
    assert.equal(await page.getByRole('button', { name: '确认解绑' }).isDisabled(), true)
    await page.getByRole('dialog').getByText(/共享此个人网盘的子账号/).waitFor()
    await page.getByRole('checkbox', { name: /我已了解上述影响/ }).check()
    await page.getByRole('button', { name: '确认解绑' }).click()
    await page.getByRole('button', { name: '开始绑定', exact: true }).waitFor()
    state.enabled = false; state.source = 'assigned'; state.canPreparePersonal = false
    await page.reload()
    await page.getByText('直链服务暂未运行', { exact: true }).waitFor()
    assert.equal(await page.getByRole('link', { name: '打开 Emby' }).getAttribute('href'), 'https://emby.example.com/')
    assert.equal(await page.getByRole('button', { name: '开始绑定' }).count(), 0)
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    assert.deepEqual(errors, [])
    console.log('personal drive UI passed')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
