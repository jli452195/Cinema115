const { selectOption, inputValue, selectOptions } = require('./select-menu-helpers.cjs')
const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage(), errors = [], writes = []
    page.on('pageerror', e => errors.push(e.message))
    let role = 'admin'
    const settings = { enabled: true, revision: 1, libraryIds: ['movies'], allowDownload: false }
    const members = ['reader', 'owner'].map((name, i) => ({ userId: name, embyId: name, embyName: name, username: name, embyState: 'active', embyCheckedAt: new Date().toISOString(), disabled: false, source: 'primary', administrator: !!i, playbackAllowed: true, personalDrive: { connected: true }, stage: 'ready' }))
    const pool = [{ id: 'pool', uid: '501', name: '共享小号', configured: true, primary: false, revision: 1, playbackParentCid: '123', pool: { state:'ready',mode:'enabled',sessions:2,preparing:0,residual:0,retryAt:0 } }]
    await page.route('**/api/**', async route => {
      const request = route.request(), p = new URL(request.url()).pathname
      let data = {}
      if (p === '/api/auth/me') data = { user: { id: role, username: role, role, disabled: false } }
      else if (p.endsWith('/settings') && request.method() === 'POST') {
        const member = members.find(m => p.includes('/' + m.userId + '/'))
        const body = request.postDataJSON(); writes.push(body); member.source = body.source; data = { settings, members }
      } else if (p === '/api/admin/member-playback/accounts') data = { accounts: pool }
      else if (p === '/api/admin/member-playback/pool') data = { settings:{maxSessions:3,maxAttempts:3,budgetSeconds:90,leaseMinutes:5,cooldownSeconds:30,uidIntervalMs:250,globalIntervalMs:50}, accounts:pool, metrics:{requests:0,succeeded:0,failed:0,switches:0,p50Ms:0,p95Ms:0,samples:0,peakUidSessions:2,resolved:0,copyResolutions:0,directLinkHits:0}, api:{requests:0,throttled:0,rejected:0,waitMs:0,waiting:0,sourceRequests:0} }
      else if (p.startsWith('/api/admin/member-playback')) data = { settings, members, proxy: { listening: true } }
      else if (p === '/api/admin/p115/accounts/pool' && request.method() === 'PUT') {
        const body = request.postDataJSON(); writes.push(body); pool[0].revision++; pool[0].name=body.name; data={accounts:pool}
      } else if (p === '/api/admin/p115/accounts/pool/qrcode' && request.method() === 'POST') {
        writes.push(request.postDataJSON());data={qrId:'pool-qr',sessionId:'pool-session',qrcode:'mock-qr',expiresAt:new Date(Date.now()+180000).toISOString()}
      } else if (p === '/api/admin/p115/accounts/pool/qrcode/status') {
        pool[0].revision++;pool[0].loginClient='115ipad';data={status:'success',connected:true,loginClient:'115ipad'}
      } else if (p === '/api/admin/p115/accounts') data = { accounts: [{ id: 'main', name: '主网盘', configured: true, primary: true, revision: 1 }, ...pool] }
      else if (p === '/api/me/playback') data = { source: 'primary', enabled: true, stage: 'ready', canPreparePersonal: true, embyName: 'reader', connected: false, pendingDrive: true, directoryAccount: { accountId: 'personal', revision: 1, uid: '700', directoryId: '', creationUncertain: false } }
      else if (p === '/api/me/playback/directories') data = { directories: [{ id: '123', name: '个人目录' }], hasMore: false }
      else if (p === '/api/me/playback/children') data = { limit: 0, used: 0, items: [] }
      else if (p === '/api/me/playback/directory') { writes.push(request.postDataJSON()); data = { source: 'primary', enabled: true, stage: 'ready', canPreparePersonal: true, embyName: 'reader', connected: true, directoryId: '123', directoryAccount: { accountId: 'personal', revision: 1, uid: '700', directoryId: '123', creationUncertain: false } } }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
    })
    const base = 'http://127.0.0.1:5173'
    await page.goto(base + '/emby/users')
    for (const name of ['reader', 'owner']) {
      const card = page.locator('article').filter({ has: page.getByRole('heading', { name, exact: true }) })
      await card.getByRole('button', {name:'管理用户 ' + name, exact:true}).click()
      const dialog = page.getByRole('dialog')
      await dialog.locator('.member-source-option').filter({ has: page.getByRole('radio', { name: '动态共享池', exact: true }) }).click()
      if (name === 'owner') assert.equal(await dialog.getByRole('radio', { name: '个人网盘', exact: true }).count(), 0)
      await dialog.getByRole('button', { name: '保存更改', exact: true }).click()
      await dialog.getByText('设置已保存。', {exact:true}).waitFor()
      await dialog.getByRole('button', {name:'关闭用户管理',exact:true}).click()
      assert.equal(await page.getByLabel('确认播放目录编号').count(), 0)
    }
    assert.deepEqual(writes.slice(0, 2).map(({source,accountId})=>({source,accountId})), [{ source: 'assigned', accountId: '' }, { source: 'assigned', accountId: '' }])
    await page.goto(base + '/115/pool')
    await page.locator('#pool .pool-stat-value').filter({ hasText: '2' }).waitFor()
    assert.equal(new URL(page.url()).hash, '#pool')
    assert.equal(await page.getByLabel('换号时的新网盘目录编号').count(), 0)
    await page.locator('#pool').getByRole('button', {name:'更多操作',exact:true}).click()
    await page.getByRole('button', {name:'修改名称',exact:true}).click()
    await page.getByRole('textbox', {name:'名称',exact:true}).fill('共享小号 A')
    await page.getByRole('button', {name:'保存名称',exact:true}).click()
    await page.getByRole('heading', {name:'共享小号 A',exact:true}).waitFor()
    assert.deepEqual(writes.at(-1), {name:'共享小号 A',revision:1})
    await page.locator('#pool').getByRole('button', {name:'扫码登录',exact:true}).click()
    await selectOption(page.getByLabel('登录设备', {exact:true}), '115-ipad')
    await page.getByText('等待扫码',{exact:true}).waitFor()
    assert.deepEqual(writes.at(-1), {clientType:'115-ipad'})
    await page.getByText('登录信息已保存',{exact:true}).waitFor()
    assert.equal(pool[0].playbackParentCid, '123')
    await page.goto(base + '/helper115')
    await page.getByRole('heading', { name: '主账号', exact: true }).waitFor()
    await page.getByRole('heading', { name: '共享小号 A', exact: true }).waitFor()
    role = 'user'
    await page.goto(base + '/account/playback')
    await page.getByRole('heading', { name: '管理员主网盘', exact: true }).waitFor()
    await page.getByRole('button', { name: '继续选择目录', exact: true }).click()
    await page.getByRole('button', { name: '选择秒传目录', exact: true }).click()
    await page.getByRole('button', { name: /个人目录/ }).click()
    await page.getByRole('button', { name: '选择此目录', exact: true }).click()
    await page.getByRole('button', { name: '确认目录并完成绑定', exact: true }).click()
    await page.getByText('秒传保存目录已确认', { exact: true }).waitFor()
    assert.deepEqual(writes.at(-1), { accountId: 'personal', revision: 1, directoryId: '123', create: false })
    await page.setViewportSize({ width: 390, height: 844 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    assert.deepEqual(errors, [])
    console.log('Shared pool UI passed: dynamic source, administrator playback, separated pool, personal directory self-service, mobile layout')
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exit(1) })
