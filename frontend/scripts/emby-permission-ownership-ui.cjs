const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 1000 }, serviceWorkers: 'block' })
  await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
  const errors = [], writes = []
  page.on('pageerror', e => errors.push(e.message))
  const settings = { enabled: true, libraryIds: [], allowDownload: false, publicUrl: '' }
  const proxy = { listening: true, config: { enabled: true, url: 'http://emby:8096', port: 8097 } }
  const members = [{ userId: 'reader', embyId: 'a'.repeat(32), embyName: 'reader', source: 'primary', playbackAllowed: true, checkedAt: "2026-10-02T00:00:00Z", stage: 'ready', checkError: 'Emby 播放限制应用失败，请检查 Emby 连接后重试' }]
  await page.route('**/api/**', async route => {
   const req = route.request(), p = new URL(req.url()).pathname
   let data = {}
   if (p === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
   else if (p === '/api/admin/member-playback/accounts') data = { accounts: [] }
   else if (p === '/api/admin/member-playback/libraries') data = { libraries: [{ ItemId: 'movies', Name: '电影' }] }
   else if (p.startsWith('/api/admin/member-playback')) {
    if (req.method() === 'POST') {
     const body = req.postDataJSON(); writes.push({ path: p, body })
     if (p.endsWith('/registration-settings')) settings.libraryIds = body.libraryIds
     else if (p.endsWith('/reader/sync')) members[0].checkError = ''
     else Object.assign(settings, body)
    }
    data = { settings, members, proxy }
   } else if (p.endsWith('/proxy')) data = proxy
   else if (p.endsWith('/invites')) data = { invites: [], hasMore: false }
   else if (p.endsWith('/logs')) data = { entries: [] }
   else data = { accounts: [], config: {}, settings: {}, items: [], records: [], stats: {} }
   await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
  })
  const base = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
  await page.goto(base + '/emby/users')
  await page.getByRole('button', { name: '管理用户' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByText(members[0].checkError, { exact: true }).waitFor()
  assert.equal(await page.getByLabel('媒体库同步分组').count(), 0)
  assert.equal(await dialog.getByLabel('媒体库权限', { exact: true }).count(), 0)
  await dialog.getByText('更多操作', { exact: true }).click()
  await dialog.getByRole('button', { name: '检查网盘' }).click()
  await dialog.getByText('操作已完成。').waitFor()
  await dialog.getByRole('button', { name: '关闭', exact: true }).click()
  await page.goto(base + '/users/invites')
  await page.getByRole('heading', { name: '新注册用户的默认媒体库' }).waitFor()
  await page.getByRole('checkbox', { name: '电影', exact: true }).check()
  await page.getByRole('button', { name: '保存注册媒体库' }).click()
  await page.getByText('已保存，仅用于之后创建的新账号。已有用户权限保持不变。').waitFor()
  assert.deepEqual(writes.at(-1), { path: '/api/admin/member-playback/registration-settings', body: { libraryIds: ['movies'] } })
  await page.getByRole('button', { name: '清空选择' }).click()
  await page.getByRole('button', { name: '保存注册媒体库' }).click()
  await page.waitForFunction(() => document.querySelector('[role="status"]')?.textContent?.includes('已保存'))
  await page.goto(base + '/emby/proxy')
  await page.getByRole('heading', { name: '播放与原文件下载' }).waitFor()
  assert.equal(await page.getByRole('heading', { name: '媒体库权限同步' }).count(), 0)
  assert.equal(await page.getByRole('button', { name: '刷新媒体库' }).count(), 0)
  await page.getByRole('button', { name: '保存播放设置', exact: true }).click()
  await page.getByText('播放设置已保存。', { exact: true }).waitFor()
  assert.deepEqual(writes.at(-1), { path: '/api/admin/member-playback', body: { allowDownload: false, publicUrl: '' } })
  assert.deepEqual(settings.libraryIds, [])
  await page.setViewportSize({ width: 390, height: 844 })
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
  assert.deepEqual(errors, [])
  console.log('Permission ownership UI passed: template isolation, empty template, existing permissions, playback retry, mobile layout')
 } finally { await browser.close() }
})().catch(e => { console.error(e); process.exit(1) })
