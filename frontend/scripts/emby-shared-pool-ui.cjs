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
    const members = ['reader', 'owner'].map((name, i) => ({ userId: name, embyId: name, embyName: name, username: name, source: 'primary', assignedAccountId: '', administrator: !!i, skipLibrarySync: !!i, personalDrive: { connected: true }, appliedRevision: 1, stage: 'ready', policyEnabled: true }))
    const pool = [{ id: 'pool', uid: '501', name: '共享小号', configured: true, primary: false, revision: 1, playbackParentCid: '123', users: ['reader', 'owner'], userCount: 2 }]
    await page.route('**/api/**', async route => {
      const request = route.request(), p = new URL(request.url()).pathname
      let data = {}
      if (p === '/api/auth/me') data = { user: { id: role, username: role, role, disabled: false } }
      else if (p.endsWith('/source')) {
        const member = members.find(m => p.includes('/' + m.userId + '/'))
        const body = request.postDataJSON(); writes.push(body); member.source = body.source; member.assignedAccountId = body.accountId; data = { settings, members }
      } else if (p === '/api/admin/member-playback/accounts') data = { accounts: pool }
      else if (p.startsWith('/api/admin/member-playback')) data = { settings, members }
      else if (p === '/api/admin/p115/accounts/pool' && request.method() === 'PUT') {
        writes.push(request.postDataJSON());pool[0].revision++;pool[0].uid='502';pool[0].playbackParentCid='456';data={accounts:pool,configured:true,connected:true,message:'账号配置已保存'}
      } else if (p === '/api/admin/p115/accounts/pool/qrcode') {
        writes.push(request.postDataJSON());data={qrId:'pool-qr',qrcode:'mock-qr',expiresAt:new Date(Date.now()+180000).toISOString()}
      } else if (p === '/api/admin/p115/accounts/pool/qrcode/status') {
        pool[0].revision++;data={status:'success',connected:true}
      } else if (p === '/api/admin/p115/accounts') data = { accounts: [{ id: 'main', name: '主网盘', configured: true, primary: true, revision: 1 }, ...pool] }
      else if (p === '/api/me/playback') data = { source: 'primary', enabled: true, stage: 'ready', canPreparePersonal: true, embyName: 'reader', connected: false, needsDirectoryConfirmation: true }
      else if (p === '/api/me/playback/directory') { writes.push(request.postDataJSON()); data = { source: 'primary', enabled: true, stage: 'ready', canPreparePersonal: true, embyName: 'reader', connected: true, directoryId: '123' } }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
    })
    const base = 'http://127.0.0.1:5173'
    await page.goto(base + '/emby/users')
    for (const name of ['reader', 'owner']) {
      const card = page.locator('article').filter({ has: page.getByRole('heading', { name, exact: true }) })
      await card.getByLabel('播放账号', {exact: true}).selectOption('pool:pool')
      await card.getByRole('button', { name: '保存', exact: true }).click()
      await card.locator('header strong').filter({hasText:'共享小号'}).waitFor()
      assert.equal(await page.getByLabel('确认播放目录编号').count(), 0)
    }
    assert.deepEqual(writes.slice(0, 2), [{ source: 'assigned', accountId: 'pool' }, { source: 'assigned', accountId: 'pool' }])
    assert.equal(await page.locator('article').filter({has:page.getByRole('heading',{name:'owner',exact:true})}).getByLabel('播放账号',{exact:true}).locator('option[value="personal"]').count(), 0)
    await page.goto(base + '/115/pool')
    await page.getByText('2 人使用', { exact: false }).waitFor()
    assert.equal(await page.getByRole('heading', { name: '主网盘', exact: true }).count(), 0)
    await page.getByLabel('换号时的新网盘目录编号').fill('456')
    await page.getByLabel('Cookie', {exact:true}).fill('UID=502_A; SEID=mock')
    await page.getByRole('button', {name:'保存配置',exact:true}).click()
    await page.getByText('账号配置已保存',{exact:true}).waitFor()
    assert.equal(writes.at(-1).directoryId,'456')
    await page.getByRole('button', {name:'扫码登录',exact:true}).click()
    await page.getByLabel('换号时的新网盘目录编号').fill('789')
    await page.getByRole('button',{name:'获取二维码',exact:true}).click()
    await page.getByText('等待扫码',{exact:true}).waitFor()
    assert.equal(writes.at(-1).directoryId,'789')
    await page.getByText('扫码登录成功',{exact:true}).waitFor()
    await page.goto(base + '/helper115')
    await page.getByRole('link', { name: '管理共享小号池', exact: true }).waitFor()
    assert.equal(await page.getByRole('heading', { name: '共享小号', exact: true }).count(), 0)
    role = 'user'
    await page.goto(base + '/account/playback')
    await page.getByText('播放方式：主网盘，无需扫码', { exact: true }).waitFor()
    await page.getByLabel('确认个人播放目录编号').fill('123')
    await page.getByRole('button', { name: '确认目录', exact: true }).click()
    await page.getByText('状态已更新', { exact: true }).waitFor()
    assert.deepEqual(writes.at(-1), { directoryId: '123' })
    await page.setViewportSize({ width: 390, height: 844 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    assert.deepEqual(errors, [])
    console.log('Shared pool UI passed: shared assignment, administrator playback, separated pool, personal directory self-service, mobile layout')
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exit(1) })
