const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage(), errors = [], writes = []
    page.on('pageerror', e => errors.push(e.message))
    const accounts = [{ id: 'main', name: 'Main', primary: true, configured: true, revision: 1, uid: '1' }, { id: 'new', name: 'Friend', primary: false, configured: true, revision: 1, uid: '2', playbackParentCid: '123' }]
    const config = { embyUrl: 'http://emby', bindings: [] }, members = [
      { userId: 'linked', embyId: 'already-linked', embyName: 'Linked', stage: 'ready', source: 'primary', appliedRevision: 1 },
      { userId: 'drive', embyId: '', accountId: 'personal', stage: 'unbound', source: 'personal', websiteName: 'Drive' },
      { userId: 'creating', embyId: '', stage: 'creating', source: 'personal', websiteName: 'Creating' },
      { userId: 'website', embyId: '', stage: 'unbound', source: 'personal', websiteName: 'Reader' },
    ]
    const settings = { enabled: true, revision: 1, libraryIds: ['movies'], allowDownload: false }
    await page.route('**/api/**', async route => {
      const req = route.request(), url = new URL(req.url()); let data = {}
      if (req.method() !== 'GET') writes.push({ path: url.pathname, body: req.postDataJSON() })
      if (url.pathname === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
      else if (url.pathname === '/api/admin/emby/proxy') data = { config: { url: 'http://emby' } }
      else if (url.pathname === '/api/admin/emby/playback-cache') data = { config }
      else if (url.pathname === '/api/admin/emby/playback-cache/users') data = { embyUrl: 'http://emby', users: [{ Id: 'existing', Name: 'Existing', Policy: { IsDisabled: false, IsAdministrator: false } }], members }
      else if (url.pathname === '/api/admin/member-playback/users') data = { settings, members }
      else if (url.pathname === '/api/admin/p115/accounts') data = { accounts }
      else if (url.pathname === '/api/admin/emby/playback-cache/bindings/existing') { config.bindings = [{ userId: 'existing', accountId: req.postDataJSON().accountId }]; data = { config } }
      else if (url.pathname === '/api/admin/users') data = { users: [
        { id: 'website', username: 'Reader', disabled: false },
        { id: 'disabled', username: 'Disabled', disabled: true },
        { id: 'linked', username: 'Linked', disabled: false },
        { id: 'drive', username: 'Drive', disabled: false },
        { id: 'creating', username: 'Creating', disabled: false },
      ], hasMore: false }
      else if (url.pathname === '/api/admin/member-playback/adoption/existing') {
        if (req.method() === 'GET') data = { embyName: 'Existing', fingerprint: 'fixed-preview', source: 'assigned', accountName: 'New friend', currentLibraries: ['Movies', 'TV'], newLibraries: ['Movies'], currentDownload: true, newDownload: false }
        else if (req.postDataJSON().invite) data = { invite: { code: 'private-code' } }
        else { Object.assign(members.find(m => m.userId === 'website'), { userId: 'website', websiteName: 'Reader', embyId: 'existing', embyName: 'Existing', embyUrl: 'http://emby', source: 'assigned', assignedAccountId: 'new', adopted: true, stage: 'pending', appliedRevision: 1, personalDrive: {} }); data = { linked: true } }
      }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
    })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/emby/users')
    await page.getByRole('article', { name: 'Existing 的播放配置' }).getByRole('button', { name: '管理', exact: true }).click()
    const dialog = page.getByRole('dialog')
    await dialog.getByRole('link', { name: '去 115 账号管理新增小号 →' }).waitFor()
    assert.equal(await dialog.getByRole('button', { name: '创建小号配置' }).count(), 0)
    await dialog.getByRole('button', { name: '关联网站账号', exact: true }).click()
    await dialog.getByText('原媒体库：Movies、TV', { exact: true }).waitFor()
    assert.deepEqual(await dialog.getByLabel('关联已有网站账号').locator('option').evaluateAll(items => items.map(i => i.value)), ['', 'website'])
    await dialog.getByRole('checkbox').check()
    await dialog.getByLabel('选择播放账号').selectOption('new')
    await dialog.getByText('请先保存或还原播放来源，再关联网站账号。').waitFor()
    assert.equal(await dialog.getByRole('button', { name: '关联网站账号', exact: true }).isDisabled(), true)
    assert.equal(await dialog.getByRole('checkbox').count(), 0, 'editing the source invalidates the permission preview')
    await dialog.getByRole('button', { name: '保存', exact: true }).click()
    await dialog.getByText('已保存，新播放请求生效。', { exact: true }).waitFor()
    await dialog.getByRole('button', { name: '关联网站账号', exact: true }).click()
    await dialog.getByLabel('邀请新用户注册', { exact: true }).check()
    assert.equal(await dialog.getByRole('button', { name: '生成专属注册邀请（一天有效）' }).isDisabled(), true)
    await dialog.getByRole('checkbox').check()
    await dialog.getByRole('button', { name: '关闭', exact: true }).click()
    await dialog.getByText('当前用户有未保存的修改。').waitFor()
    await dialog.getByRole('button', { name: '继续编辑', exact: true }).click()
    await dialog.getByRole('button', { name: '生成专属注册邀请（一天有效）' }).click()
    await dialog.getByLabel('专属邀请链接').waitFor()
    assert.match(await dialog.getByLabel('专属邀请链接').inputValue(), /\/register#invite=private-code$/)
    assert.deepEqual(writes.at(-1).body, { fingerprint: 'fixed-preview', websiteUserId: '', invite: true })
    assert.equal(await dialog.getByRole('button', { name: '生成专属注册邀请（一天有效）' }).isDisabled(), true)
    await dialog.getByLabel('已有网站账号', { exact: true }).check()
    await dialog.getByLabel('关联已有网站账号').selectOption('website')
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.setViewportSize({ width: 390, height: 844 })
    assert.equal(await dialog.evaluate(el => el.scrollWidth > el.clientWidth), false)
    await dialog.getByRole('button', { name: '关联此网站用户' }).click()
    await page.getByRole('article', { name: 'Existing 的播放配置' }).getByText('网站用户：Reader', { exact: true }).waitFor()
    assert.deepEqual(writes.at(-1).body, { fingerprint: 'fixed-preview', websiteUserId: 'website', invite: false })
    assert.deepEqual(errors, [])
    assert.equal(writes.some(w => w.path.startsWith('/api/admin/p115/accounts')), false)
    console.log('Adoption UI passed: centralized account management, eligible owners, stale preview reset, unsaved confirmation, private invitation, fixed website selection, mobile dark layout')
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
