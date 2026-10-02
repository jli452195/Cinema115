const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 }, serviceWorkers: 'block' })
  await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
  const errors = [], writes = []
  page.on('pageerror', e => errors.push(e.message))
  const settings = { enabled: true, libraryIds: [], allowDownload: true }
  const ready = { state: 'ready', message: '最近检查通过', checkedAt: '2026-10-02T08:00:00Z' }
  let health = ready, userReads = 0, finishChecks = false
  const members = ['reader', 'second'].map((name, i) => ({ userId: name, embyId: (i ? 'b' : 'a').repeat(32), embyName: name, source: 'primary', assignedAccountId: '', playbackAllowed: true, childLimit: 0, childUsed: 0, stage: 'pending', checkError: '过期检查错误' }))
  const statuses = () => members.map(m => ({ userId: m.userId, driveStatus: health }))
  await page.route('**/api/**', async route => {
   const req = route.request(), p = new URL(req.url()).pathname
   if (req.method() === 'POST') writes.push(p)
   let data = {}
   if (p === '/api/auth/me') data = { user: { id: 'admin', role: 'admin', username: 'admin' } }
   else if (p.endsWith('/accounts')) data = { accounts: [] }
   else if (p.endsWith('/users')) { userReads++; data = { members: members.map(m => ({ ...m, driveStatus: health })), settings } }
   else if (p.endsWith('/refresh')) { health = { state: 'pending', message: '等待检查' }; data = { members: statuses() } }
   else if (p.endsWith('/drive-status')) { if (finishChecks) health = ready; data = { members: statuses() } }
   else data = { settings, proxy: { listening: true } }
   await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
  })
  await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/emby/users')
  const card = page.locator('article').filter({ has: page.getByRole('heading', { name: 'reader', exact: true }) })
  await card.getByText('最近检查通过', { exact: true }).waitFor()
  assert.equal(await card.locator('time').getAttribute('datetime'), ready.checkedAt)
  assert.deepEqual(writes, [])
  assert.equal(await page.getByRole('button', { name: '刷新 Emby 用户', exact: true }).count(), 0)
  members[0].playbackAllowed = false
  await page.getByRole('button', { name: '刷新状态', exact: true }).click()
  await page.getByRole('button', { name: '正在检查网盘…', exact: true }).waitFor()
  assert.equal(await page.getByRole('button', { name: '正在检查网盘…', exact: true }).isDisabled(), true)
  assert.equal(await card.getByRole('switch').getAttribute('aria-checked'), 'false')
  finishChecks = true
  await card.getByText('最近检查通过', { exact: true }).waitFor()
  assert.equal(await page.locator('article time').count(), 2)
  await card.getByRole('button', { name: '管理用户' }).click()
  const dialog = page.getByRole('dialog')
  assert.equal(await dialog.getByText('过期检查错误', { exact: true }).count(), 0)
  await dialog.getByRole('switch', { name: '允许创建子账号' }).click()
  await dialog.getByLabel('子账号名额', { exact: true }).fill('4')
  const before = userReads
  health = { state: 'invalid_login', message: '登录已失效，请重新登录', checkedAt: ready.checkedAt }
  finishChecks = false
  await page.evaluate(() => window.dispatchEvent(new Event('focus')))
  await card.getByText('登录已失效，请重新登录', { exact: true }).waitFor()
  assert.equal(userReads, before)
  assert.equal(await dialog.getByLabel('子账号名额', { exact: true }).inputValue(), '4')
  await dialog.getByRole('button', { name: '关闭用户设置' }).click()
  await dialog.getByRole('button', { name: '放弃修改并关闭' }).click()
  health = { state: 'unavailable', message: '主网盘未配置' }
  await page.evaluate(() => window.dispatchEvent(new Event('focus')))
  await card.getByText('主网盘未配置', { exact: true }).waitFor()
  assert.ok(userReads > before)
  await page.setViewportSize({ width: 390, height: 844 })
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
  assert.deepEqual(writes, ['/api/admin/member-playback/refresh'])
  assert.deepEqual(errors, [])
  console.log('Drive health UI passed: unified refresh, read-only permissions, shared results, check time, focus refresh, edit preservation and mobile')
 } finally { await browser.close() }
})().catch(e => { console.error(e); process.exit(1) })
