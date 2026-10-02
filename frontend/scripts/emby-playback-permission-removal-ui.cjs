const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 }, serviceWorkers: 'block' })
  await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
  const writes = [], errors = []
  page.on('pageerror', e => errors.push(e.message))
  const members = [
   { userId: 'reader', embyId: 'a'.repeat(32), embyName: 'reader', source: 'primary', playbackAllowed: true },
   { userId: 'admin', embyId: 'b'.repeat(32), embyName: 'admin', source: 'primary', administrator: true, playbackAllowed: false },
   { userId: 'child', embyId: 'c'.repeat(32), embyName: 'child', source: 'personal_shared', playbackOnly: true, childRevoked: true, disabled: true },
  ]
  await page.route('**/api/**', async route => {
   const req = route.request(), p = new URL(req.url()).pathname
   if (req.method() === 'POST') writes.push(p)
   const data = p === '/api/auth/me' ? { user: { id: 'admin', role: 'admin', username: 'admin' } } : p.endsWith('/accounts') ? { accounts: [] } : { members, settings: { enabled: true, libraryIds: [], allowDownload: false }, proxy: { listening: true } }
   await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
  })
  await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/emby/users')
  await page.getByRole('heading', { name: 'reader', exact: true }).waitFor()
  assert.equal(await page.locator('article [role="switch"]').count(), 0)
  assert.equal(await page.getByText('Emby 播放权限', { exact: true }).count(), 0)
  assert.equal(await page.getByText('权限由 Emby 管理，此处仅显示状态。', { exact: true }).count(), 0)
  await page.getByRole('button', { name: '刷新状态', exact: true }).click()
  await page.getByRole('button', { name: '刷新状态', exact: true }).waitFor()
  assert.deepEqual(writes, ['/api/admin/member-playback/refresh'])
  await page.setViewportSize({ width: 390, height: 844 })
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
  assert.deepEqual(errors, [])
  console.log('Playback permission UI removed: no switches, no permission display, no policy writes, mobile layout')
 } finally { await browser.close() }
})().catch(e => { console.error(e); process.exit(1) })
