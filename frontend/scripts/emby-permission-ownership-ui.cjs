const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 1000 }, serviceWorkers: 'block' })
  await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
  const errors = [], writes = []
  page.on('pageerror', e => errors.push(e.message))
  const publicUrl = 'https://emby.example.com'
  const settings = { enabled: true, libraryIds: [], allowDownload: false, publicUrl }
  const proxy = { listening: true, error: '', config: { enabled: true, debug: false, directLinkCacheMinutes: 9, url: 'http://emby:8096', port: 8097, accountId: 'main' } }
  const members = [{ userId: 'reader', embyId: 'a'.repeat(32), embyName: 'reader', embyState: 'active', source: 'primary', stage: 'ready', driveStatus: { state: 'error', message: '模拟网盘检查失败' } }]
  await page.route('**/api/**', async route => {
   const req = route.request(), p = new URL(req.url()).pathname
   let data = {}
   if (p === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
   else if (p === '/api/admin/member-playback/accounts') data = { accounts: [] }
   else if (p === '/api/admin/member-playback/libraries') data = { libraries: [{ ItemId: 'movies', Name: '电影', CollectionType: 'movies' }] }
   else if (p.startsWith('/api/admin/member-playback')) {
    if (req.method() === 'POST') {
     const body = req.postDataJSON(); writes.push({ path: p, body })
     if (p.endsWith('/registration-settings')) settings.libraryIds = body.libraryIds
     else if (p.endsWith('/reader/sync')) members[0].driveStatus = { state: 'ready', message: '网盘检查通过' }
     else Object.assign(settings, body)
    }
    data = { settings, members, proxy }
   } else if (p === '/api/admin/emby/proxy') {
    if (req.method() === 'POST') {
     const body = req.postDataJSON(); writes.push({ path: p, body })
     const { allowDownload, ...config } = body
     proxy.config = config
     if (typeof allowDownload === 'boolean') settings.allowDownload = allowDownload
    }
    data = { ...proxy, allowDownload: settings.allowDownload }
   }
   else if (p.endsWith('/invites')) data = { invites: [], hasMore: false }
   else if (p.endsWith('/logs')) data = { entries: [] }
   else data = { accounts: [], config: {}, settings: {}, items: [], records: [], stats: {} }
   await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
  })
  const base = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
  await page.goto(base + '/users/list')
  await page.getByRole('button', { name: '管理用户 reader', exact: true }).click()
  const userDialog = page.getByRole('dialog', { name: '管理用户', exact: true })
  await userDialog.getByText(members[0].driveStatus.message, { exact: true }).waitFor()
  assert.equal(await page.getByLabel('媒体库同步分组').count(), 0)
  assert.equal(await userDialog.getByLabel('媒体库权限', { exact: true }).count(), 0)
  await userDialog.getByRole('button', { name: '检查网盘', exact: true }).click()
  await userDialog.getByText('已更新网盘检查结果。', { exact: true }).waitFor()
  await userDialog.getByRole('button', { name: '关闭用户管理', exact: true }).click()
  await page.goto(base + '/users/invites')
  const librarySummary = page.getByRole('region', { name: '默认媒体库', exact: true })
  const libraryDialog = page.getByRole('dialog', { name: '新账号的默认媒体库', exact: true })
  await librarySummary.getByRole('button', { name: '编辑', exact: true }).click()
  await libraryDialog.locator('.invite-library-option').filter({ hasText: '电影' }).click()
  await libraryDialog.getByRole('button', { name: '保存更改', exact: true }).click()
  await librarySummary.getByText('已保存，仅用于之后创建的新账号。', { exact: true }).waitFor()
  assert.deepEqual(writes.at(-1), { path: '/api/admin/member-playback/registration-settings', body: { libraryIds: ['movies'] } })
  await librarySummary.getByRole('button', { name: '编辑', exact: true }).click()
  await libraryDialog.getByRole('button', { name: '清空', exact: true }).click()
  await libraryDialog.getByRole('button', { name: '保存并暂停注册', exact: true }).click()
  await librarySummary.getByText('已保存，仅用于之后创建的新账号。', { exact: true }).waitFor()
  assert.deepEqual(writes.at(-1), { path: '/api/admin/member-playback/registration-settings', body: { libraryIds: [] } })
  const writesBeforeProxySave = writes.length
  await page.goto(base + '/emby/proxy')
  const proxySettings = page.getByRole('region', { name: '反代设置', exact: true })
  const downloadSwitch = proxySettings.getByRole('switch', { name: '允许下载原文件', exact: true })
  const saveProxy = proxySettings.getByRole('button', { name: '保存反代配置', exact: true })
  await saveProxy.and(page.locator(':enabled')).waitFor()
  assert.equal(await downloadSwitch.isChecked(), false)
  assert.equal(await page.getByRole('heading', { name: '访问设置', exact: true }).count(), 0)
  assert.equal(await page.getByRole('heading', { name: '媒体库权限同步' }).count(), 0)
  assert.equal(await page.getByRole('button', { name: '刷新媒体库' }).count(), 0)
  const savedProxyConfig = { ...proxy.config }
  await downloadSwitch.press('Space')
  await saveProxy.click()
  await proxySettings.getByText('反代配置已保存。', { exact: true }).waitFor()
  assert.deepEqual(writes.slice(writesBeforeProxySave), [{ path: '/api/admin/emby/proxy', body: { ...savedProxyConfig, allowDownload: true } }])
  assert.equal(settings.allowDownload, true)
  assert.deepEqual(settings.libraryIds, [])
  assert.equal(settings.publicUrl, publicUrl)
  await page.setViewportSize({ width: 390, height: 844 })
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
  assert.deepEqual(errors, [])
  console.log('Permission ownership UI passed: template isolation, empty template, existing permissions, playback retry, mobile layout')
 } finally { await browser.close() }
})().catch(e => { console.error(e); process.exit(1) })
