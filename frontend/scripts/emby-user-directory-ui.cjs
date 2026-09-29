const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage(), errors = [], writes = []
    page.on('pageerror', e => errors.push(e.message))
    const settings = { enabled: true, revision: 1, libraryIds: ['movies'], allowDownload: false }
    const baseMember = { websiteDisabled: false, source: 'personal', embyUrl: 'http://emby', accountId: '', assignedAccountId: '', policyEnabled: false, stage: 'pending', message: '等待播放授权', adminBlocked: false, directoryStarted: false, directoryId: '', appliedRevision: 1, syncError: '', personalDrive: { uid: '', connected: false, directoryId: '' } }
    const members = [
      { ...baseMember, userId: 'web-reader', websiteName: 'reader', embyId: 'member', embyName: 'Alice', accountId: 'personal-reader', personalDrive: { uid: '123', connected: true, directoryId: '123' } },
      { ...baseMember, userId: 'web-pending', websiteName: 'pending', embyId: '', embyName: 'reserved-name', stage: 'creating' },
      { ...baseMember, userId: 'web-missing', websiteName: 'missing', embyId: 'deleted', embyName: 'Gone' },
    ]
    let offline = false, failSave = false, primaryState = 'configured', config = { embyUrl: 'http://emby', bindings: [] }
    await page.route('**/api/**', async route => {
      const req = route.request(), url = new URL(req.url()); let data = {}, status = 200
      if (url.pathname === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin', disabled: false } }
      else if (url.pathname === '/api/admin/p115/accounts') data = { accounts: [...(primaryState === 'missing' ? [] : [{ id: 'main', name: 'Main', primary: true, configured: primaryState === 'configured' }]), { id: 'secondary', name: 'Friend', primary: false, configured: true, playbackParentCid: '456' }] }
      else if (url.pathname === '/api/admin/emby/proxy') data = { config: { url: 'http://emby' } }
      else if (url.pathname === '/api/admin/emby/playback-cache') data = { config }
      else if (url.pathname === '/api/admin/emby/playback-cache/users') {
        if (offline) { data = { error: 'Emby 暂时离线' }; status = 400 }
        else data = { embyUrl: 'http://emby', users: [{ Id: 'member', Name: 'Alice', Policy: { IsDisabled: false } }, { Id: 'ordinary', Name: 'Existing', Policy: { IsDisabled: false } }], members }
      } else if (url.pathname === '/api/admin/member-playback/users') data = { settings, members }
      else if (url.pathname === '/api/admin/member-playback/web-reader/sync') { writes.push({ path: url.pathname, input: req.postDataJSON() }); data = { settings, members } }
      else if (url.pathname === '/api/admin/member-playback/web-reader/source') {
        const input = req.postDataJSON(); writes.push({ path: url.pathname, input })
        if (failSave) { status = 400; data = { error: '模拟保存失败' } }
        else { Object.assign(members[0], { source: input.source, assignedAccountId: input.accountId, policyEnabled: true, stage: 'ready' }); data = { settings, members } }
      } else if (url.pathname === '/api/admin/member-playback/web-reader') {
        writes.push({ path: url.pathname, input: req.postDataJSON() }); Object.assign(members[0], { adminBlocked: req.postDataJSON().blocked }); data = { settings, members }
      } else if (url.pathname === '/api/admin/emby/playback-cache/bindings/ordinary') {
        const input = req.postDataJSON(); writes.push({ path: url.pathname, input }); config.bindings = input.accountId ? [{ userId: 'ordinary', userName: 'Existing', accountId: input.accountId }] : []; data = { config }
      }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const base = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    await page.goto(base + '/emby/users')
    const card = name => page.getByRole('article', { name: name + ' 的播放配置' })
    await card('Alice').getByText('网站用户：reader', { exact: true }).waitFor()
    assert.equal(await page.getByRole('article').count(), 4)
    assert.equal(await page.getByRole('combobox').count(), 1, 'edit forms should be hidden initially')
    await card('pending').getByText('注册待确认', { exact: true }).waitFor()
    await card('Gone').getByText('Emby 用户不存在 · 保留关联', { exact: true }).waitFor()
    primaryState = 'missing'
    await page.getByRole('button', { name: '刷新用户与状态', exact: true }).click()
    await card('Existing').getByText('主账号未配置，请前往 115 账号管理', { exact: true }).waitFor()
    await page.getByLabel('筛选 Emby 用户').selectOption('issue')
    await card('Existing').waitFor()
    await page.getByLabel('筛选 Emby 用户').selectOption('pending')
    await card('Existing').waitFor()
    await page.getByLabel('筛选 Emby 用户').selectOption('all')
    primaryState = 'logged-out'
    await page.getByRole('button', { name: '刷新用户与状态', exact: true }).click()
    await card('Existing').getByText('主账号未登录，请更新凭据', { exact: true }).waitFor()
    primaryState = 'configured'
    await page.getByRole('button', { name: '刷新用户与状态', exact: true }).click()
    await card('Existing').getByText('已保存播放配置 · 凭据有效性在播放时校验', { exact: true }).waitFor()
    await card('Alice').getByRole('button', { name: '管理', exact: true }).click()
    const drawer = page.getByRole('dialog')
    await drawer.getByRole('heading', { name: '管理 Alice' }).waitFor()
    await drawer.getByLabel('播放账号来源').selectOption('assigned')
    await drawer.getByLabel('选择小号').selectOption('secondary')
    failSave = true
    await drawer.getByRole('button', { name: '保存播放来源' }).click()
    await drawer.getByText('模拟保存失败').waitFor()
    assert.equal(await drawer.getByLabel('播放账号来源').inputValue(), 'assigned')
    assert.equal(members[0].source, 'personal')
    failSave = false
    await drawer.getByRole('button', { name: '保存播放来源' }).click()
    await drawer.getByText('闲置保留', { exact: true }).waitFor()
    await drawer.getByRole('button', { name: '停用播放', exact: true }).click()
    await drawer.getByRole('button', { name: '解除播放停用', exact: true }).waitFor()
    await drawer.getByRole('button', { name: '关闭', exact: true }).click()
    await card('Alice').getByText('播放来源：管理员小号 · Friend', { exact: true }).waitFor()
    await card('Alice').getByText('播放已停用', { exact: true }).waitFor()
    assert.deepEqual(writes[1], { path: '/api/admin/member-playback/web-reader/source', input: { source: 'assigned', accountId: 'secondary' } })
    assert.equal(writes.some(w => w.path.endsWith('/bindings/member')), false)
    await card('Alice').getByRole('button', { name: '管理', exact: true }).click()
    await drawer.getByRole('button', { name: '检查并同步此用户' }).click()
    await drawer.getByText('已保存', { exact: true }).waitFor()
    assert.equal(writes.at(-1).path, '/api/admin/member-playback/web-reader/sync')
    await drawer.getByLabel('播放账号来源').selectOption('primary')
    await page.keyboard.press('Escape')
    await drawer.getByText('当前用户有未保存的修改。').waitFor()
    await drawer.getByRole('button', { name: '继续编辑' }).click()
    assert.equal(await drawer.getByLabel('播放账号来源').inputValue(), 'primary')
    await drawer.getByRole('button', { name: '关闭', exact: true }).click()
    await drawer.getByRole('button', { name: '放弃修改并关闭' }).click()
    assert.equal(members[0].source, 'assigned')
    await card('Existing').getByRole('button', { name: '管理', exact: true }).click()
    await drawer.getByLabel('选择播放账号').selectOption('secondary')
    await drawer.getByRole('button', { name: '保存', exact: true }).click()
    await drawer.getByText('已保存，新播放请求生效。', { exact: true }).waitFor()
    assert.equal(writes.at(-1).path, '/api/admin/emby/playback-cache/bindings/ordinary')
    assert.equal(writes.at(-1).input.expectedAccountId, '')
    await drawer.getByRole('button', { name: '关闭', exact: true }).click()
    await page.getByLabel('筛选 Emby 用户').selectOption('website')
    assert.equal(await page.getByRole('article').count(), 3)
    await page.getByLabel('筛选 Emby 用户').selectOption('all')
    await page.getByLabel('搜索 Emby 用户').fill('reader')
    assert.equal(await page.getByRole('article').count(), 1)
    await page.getByLabel('搜索 Emby 用户').fill('')
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.setViewportSize({ width: 390, height: 844 })
    await card('Alice').getByRole('button', { name: '管理', exact: true }).click()
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    assert.equal(await drawer.evaluate(el => el.scrollWidth > el.clientWidth), false)
    await drawer.getByRole('button', { name: '关闭', exact: true }).click()
    offline = true
    await page.getByRole('button', { name: '刷新用户与状态', exact: true }).click()
    await page.getByText('Emby 暂时离线', { exact: true }).waitFor()
    await card('Alice').getByText('Emby 状态未确认 · 保留关联', { exact: true }).waitFor()
    assert.equal(await page.getByRole('article').count(), 4, 'local members and ordinary saved binding must survive offline')
    await card('pending').getByRole('button', { name: '管理', exact: true }).click()
    await drawer.getByRole('button', { name: '检查 Emby，未创建时允许重试' }).waitFor()
    await drawer.getByRole('button', { name: '关闭', exact: true }).click()
    await page.goto(base + '/emby/users?websiteUser=web-reader')
    await card('Alice').waitFor()
    assert.equal(await page.getByRole('article').count(), 1)
    assert.deepEqual(errors, [])
    console.log('Unified Emby directory passed: fixed-ID ownership, pending/missing/offline records, member and independent mutations, failure retention, discard confirmation, filters, dark mobile drawer')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
