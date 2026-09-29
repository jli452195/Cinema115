const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage(), errors = [], writes = []
    page.on('pageerror', error => errors.push(error.message))
    const state = { accountRevision: 0, suggestedName: 'reader', pendingName: '', accountMessage: '', source: 'personal', provisioned: false, enabled: true, stage: 'unbound', uid: '', connected: false, directoryId: '', embyName: '', embyUrl: 'https://play.example.com', suspended: false, blocked: false }
    await page.route('**/api/**', async route => {
      const req = route.request(), url = new URL(req.url()); let data = {}
      if (url.pathname === '/api/auth/me') data = { user: { id: 'user', username: 'reader', role: 'user', disabled: false } }
      else if (url.pathname === '/api/me/playback') data = state
      else if (url.pathname === '/api/me/playback/qrcode') { writes.push(url.pathname); data = { qrId: 'private-qr', qrcode: 'test-qr', expiresAt: new Date(Date.now()+180000).toISOString() } }
      else if (url.pathname.endsWith('/qrcode/status')) { Object.assign(state, { stage: 'ready', uid: '123', connected: true, directoryId: '123', message: '已开通' }); data = { status: 'success', playback: state } }
      else if (url.pathname.endsWith('/activate')) { writes.push(req.postDataJSON()); Object.assign(state, { accountRevision: 3, provisioned: true, stage: 'pending', embyName: req.postDataJSON().username, message: '可登录浏览，播放待开通' }); data = state }
      else if (url.pathname.endsWith('/username')) { writes.push(req.postDataJSON()); state.embyName = req.postDataJSON().username; state.accountRevision++; state.accountMessage = '用户名已更新'; data = state }
      else if (url.pathname.endsWith('/password')) { writes.push(req.postDataJSON()); state.accountRevision++; state.accountMessage = '密码已更新'; data = state }
      else if (url.pathname.endsWith('/disconnect')) { state.connected = false; if (state.source === 'personal') Object.assign(state, { stage: 'paused', suspended: true }); data = state }
      else if (url.pathname.endsWith('/use-personal')) { writes.push(url.pathname); state.source = 'personal'; data = state }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
    })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/account/playback')
    await page.getByRole('heading', { name: '待注册 Emby', exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: '扫码绑定 115' }).isDisabled(), true)
    assert.equal(await page.getByRole('button', { name: '注册 Emby 账号', exact: true }).isDisabled(), false)
    assert.equal(await page.getByLabel('Emby 用户名', { exact: false }).inputValue(), 'reader')
    await page.getByLabel('Emby 用户名', { exact: false }).fill('Alice')
    await page.getByLabel('Emby 密码', { exact: true }).fill('password123')
    await page.getByLabel('确认密码', { exact: true }).fill('password123')
    await page.getByRole('button', { name: '注册 Emby 账号', exact: true }).click()
    await page.getByRole('heading', { name: '已注册，播放待开通' }).waitFor()
    await page.getByRole('checkbox', { name: '我同意保存网盘登录凭据用于本人播放，并在专用目录保存播放文件。' }).check()
    await page.getByRole('button', { name: '扫码绑定 115' }).click()
    await page.getByAltText('115 登录二维码').waitFor()
    await page.getByText('115 绑定成功', { exact: true }).waitFor()
    await page.getByRole('heading', { name: '播放已开通' }).waitFor()

    assert.equal(await page.getByRole('link', { name: 'https://play.example.com', exact: true }).getAttribute('href'), 'https://play.example.com/')
    assert.deepEqual(writes, [{ username: 'Alice', password: 'password123', revision: 0 }, '/api/me/playback/qrcode'])
    await page.getByLabel('新 Emby 用户名', { exact: true }).fill('Alice2')
    await page.getByRole('button', { name: '保存 Emby 用户名' }).click()
    await page.getByText('用户名已更新', { exact: true }).first().waitFor()
    assert.deepEqual(writes.at(-1), { username: 'Alice2', revision: 3 })
    await page.getByLabel('Emby 密码', { exact: true }).fill('new-password')
    await page.getByLabel('确认密码', { exact: true }).fill('new-password')
    await page.getByRole('button', { name: '保存 Emby 密码' }).click()
    await page.getByText('密码已更新', { exact: true }).first().waitFor()
    assert.deepEqual(writes.at(-1), { password: 'new-password', revision: 4 })
    assert.equal(await page.getByLabel('Emby 密码', { exact: true }).inputValue(), '')
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.setViewportSize({ width: 390, height: 844 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    await page.getByRole('checkbox', { name: '暂停播放并清除保存的 115 登录凭据，网盘文件保留，之后仅能重新绑定原 UID。' }).check()
    await page.getByRole('button', { name: '暂停并退出网盘登录' }).click()
    await page.getByRole('heading', { name: '播放已暂停' }).waitFor()
    assert.equal(await page.getByRole('button', { name: '保存 Emby 密码' }).isDisabled(), false)
    Object.assign(state, { source: 'primary', stage: 'ready', suspended: false, connected: true })
    await page.reload()
    await page.getByRole('heading', { name: '播放已开通' }).waitFor()
    assert.equal(await page.getByRole('button', { name: /扫码/ }).count(), 0)
    assert.equal(await page.getByRole('button', { name: '暂停并退出网盘登录' }).count(), 0)
    await page.getByRole('heading', { name: '个人网盘 · 未用于当前播放' }).waitFor()
    await page.getByRole('checkbox', { name: '清除个人网盘登录凭据，保留 UID 和网盘文件，不影响当前管理员提供的播放账号。' }).check()
    await page.getByRole('button', { name: '清除个人网盘登录凭据', exact: true }).click()
    await page.getByText('UID：123 · 登录凭据已清除', { exact: true }).waitFor()
    await page.getByRole('heading', { name: '播放已开通' }).waitFor()
    assert.deepEqual(errors, [])
    Object.assign(state, { adopted: true, canPreparePersonal: true, source: 'assigned', connected: false, uid: '', directoryId: '' })
    await page.reload()
    await page.getByRole('heading', { name: '准备自己的 115 网盘' }).waitFor()
    assert.equal(await page.getByRole('button', { name: '保存 Emby 密码' }).count(), 0)
    assert.equal(await page.getByRole('button', { name: '保存 Emby 用户名' }).count(), 0)
    assert.equal(await page.getByRole('button', { name: '切换到我的网盘播放' }).isDisabled(), true)
    await page.getByRole('checkbox', { name: '我同意保存网盘登录凭据用于本人播放，并在专用目录保存播放文件。' }).check()
    await page.getByRole('button', { name: '扫码绑定 115' }).click()
    await page.getByText('115 绑定成功', { exact: true }).waitFor()
    assert.equal(state.source, 'assigned', 'preparing a drive must not change playback')
    await page.getByRole('button', { name: '切换到我的网盘播放' }).click()
    await page.getByText('播放方式：绑定自己的 115 网盘', { exact: true }).waitFor()
    assert.equal(writes.at(-1), '/api/me/playback/use-personal')
    console.log('Personal playback UI passed: consent, owner-only payload, QR login, activation, suspend, dark mobile layout')

    const adminPage = await context.newPage(), settingsWrites = []
    adminPage.on('pageerror', error => errors.push(error.message))
    let settings = { enabled: true, libraryIds: ['movies'], allowDownload: false, publicUrl: 'https://play.example.com', revision: 1 }
    let libraries = [{ ItemId: 'movies', Name: '电影', CollectionType: 'movies' }, { ItemId: 'tv', Name: '剧集', CollectionType: 'tvshows' }]
    const members = [{ personalDrive: { uid: '123', connected: true, directoryId: '123' }, source: 'personal', assignedAccountId: '', userId: 'reader', embyName: 'c115_reader', embyId: 'emby-reader', stage: 'ready', message: '已开通', adminBlocked: false, directoryStarted: true, directoryId: '123', appliedRevision: 1, syncError: '' }]
    const sourceWrites = []
    await adminPage.route('**/api/**', async route => {
      const req = route.request(), url = new URL(req.url()); let data = {}
      if (url.pathname === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin', disabled: false } }
      else if (url.pathname === '/api/admin/users') data = { users: [], hasMore: false }
      else if (url.pathname === '/api/admin/member-playback/libraries') data = { libraries }
      else if (url.pathname === '/api/admin/member-playback/accounts') data = { accounts: [{ id: 'secondary', name: '朋友小号', configured: true, playbackParentCid: '123' }] }
      else if (url.pathname === '/api/admin/member-playback/reader/source') { const input = req.postDataJSON(); sourceWrites.push(input); members[0].source = input.source; members[0].assignedAccountId = input.accountId; data = { settings, members } }
      else if (url.pathname === '/api/admin/member-playback') {
        if (req.method() === 'POST') { const input = req.postDataJSON(); settingsWrites.push(input); settings = { ...input, revision: settings.revision + 1 }; members[0].syncError = '模拟权限同步失败' }
        data = { settings, members }
      } else if (url.pathname === '/api/admin/member-playback/sync') { members[0].syncError = ''; members[0].appliedRevision = settings.revision; data = { settings, members } }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
    })
    await adminPage.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/users')
    await adminPage.getByRole('button', { name: '自助注册设置', exact: true }).click()
    await adminPage.getByRole('heading', { name: '自助注册与播放设置' }).waitFor()
    assert.equal(await adminPage.getByRole('checkbox', { name: /允许下载原文件/ }).isChecked(), false)
    assert.equal(await adminPage.getByRole('checkbox', { name: /转码/ }).count(), 0)
    await adminPage.getByRole('checkbox', { name: /剧集/ }).check()
    await adminPage.getByRole('checkbox', { name: /允许下载原文件/ }).check()
    await adminPage.getByRole('button', { name: '保存设置', exact: true }).click()
    await adminPage.getByText(/权限同步：.*失败 1/).waitFor()
    assert.deepEqual(settingsWrites[0].libraryIds, ['movies', 'tv'])
    assert.equal(settingsWrites[0].allowDownload, true)
    await adminPage.getByLabel('Emby 对外反代地址').fill('https://unsaved.example.com')
    await adminPage.getByRole('button', { name: '重试同步', exact: true }).click()
    await adminPage.getByText(/权限同步：已同步 1/).waitFor()
    assert.equal(await adminPage.getByLabel('Emby 对外反代地址').inputValue(), 'https://unsaved.example.com')
    libraries = [...libraries, { ItemId: 'new', Name: '新媒体库', CollectionType: 'movies' }]
    await adminPage.getByRole('button', { name: '刷新媒体库', exact: true }).click()
    await adminPage.getByRole('checkbox', { name: /新媒体库/ }).waitFor()
    assert.equal(await adminPage.getByRole('checkbox', { name: /新媒体库/ }).isChecked(), false)
    await adminPage.evaluate(() => document.documentElement.classList.add('dark'))
    await adminPage.setViewportSize({ width: 390, height: 844 })
    assert.equal(await adminPage.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    assert.deepEqual(errors, [])
    console.log('Admin playback UI passed: library selection, download permission, sync retry, unsaved edits, new libraries excluded, dark mobile layout')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
