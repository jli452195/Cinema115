const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 }, serviceWorkers: 'block' })
  await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
  const errors = [], writes = []
  page.on('pageerror', e => errors.push(e.message))
  const members = ['active', 'disabled', 'deleted', 'missing', 'other_server'].map((state, index) => ({
   userId: state, embyId: String(index + 1).repeat(32), embyName: state === 'deleted' ? '21111111' : state,
   source: 'assigned', embyState: state, disabled: state === 'disabled', childLimit: 0, childUsed: 0,
   embyCheckedAt: '2026-10-05T00:00:00Z', driveStatus: { state: 'ready', message: '共享池自动调度，播放时实时校验' },
  }))
  const settings = { enabled: true, libraryIds: [], allowDownload: false }
  let failList = false, withChild = false
  await page.route('**/api/**', async route => {
   const req = route.request(), p = new URL(req.url()).pathname
   if (req.method() === 'POST') writes.push(p)
   if (p.endsWith('/users') && failList) return route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: 'Emby 暂时无法连接' }) })
   let data = { members, settings, proxy: { listening: true }, pending: [], jobs: [], leases: [] }
   if (p === '/api/auth/me') data = { user: { id: 'admin', role: 'admin', username: 'admin' } }
   if (p.endsWith('/accounts')) data = { accounts: [] }
   if (p.endsWith('/sync')) data = { settings, members: members.map(({ embyState, embyCheckedAt, ...m }) => ({ ...m, disabled: false })) }
   if (p.endsWith('/deletions/preview')) data = {
    key: 'deleted', name: '21111111', fingerprint: 'mock', children: withChild ? 1 : 0,
    targets: [{ key: 'deleted', name: '21111111', embyId: members[2].embyId }, ...(withChild ? [{ key: 'child', name: 'still-existing-child', embyId: 'a'.repeat(32) }] : [])],
    embyStates: { deleted: 'deleted', child: 'active' }, personalDrives: 1, sessions: 0,
   }
   await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
  })
  await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/emby/users')
  const active = page.locator('article.user-card').filter({ has: page.getByRole('heading', { name: 'active', exact: true }) })
  await active.waitFor()
  assert.match(await page.getByRole('heading', { name: /^Emby 用户 \d/ }).innerText(), /2$/)
  const review = page.locator('details').filter({ has: page.locator('summary', { hasText: '待核实 / 待清理记录' }) })
  assert.equal(await review.getAttribute('open'), null)
  await review.locator('summary').click()
  const deleted = review.locator('article.user-card').filter({ has: page.getByRole('heading', { name: '21111111', exact: true }) })
  assert.equal(await deleted.getByText('Emby 已删除', { exact: true }).isVisible(), true)
  assert.equal(await deleted.getByRole('button', { name: '检查网盘' }).count(), 0)
  assert.equal(await deleted.getByLabel('播放来源').count(), 0)
  assert.equal(await deleted.getByText('Emby 已停用，恢复后可修改播放来源。').count(), 0)
  const disabled = page.locator('article.user-card').filter({ has: page.getByRole('heading', { name: 'disabled', exact: true }) })
  assert.equal(await disabled.getByLabel('播放来源').isDisabled(), true)
  await disabled.getByRole('button', { name: '检查网盘' }).click()
  await disabled.getByText('已更新网盘检查结果。').waitFor()
  assert.equal(await disabled.getByLabel('播放来源').isDisabled(), true)
  assert.equal(await disabled.getByText('Emby 已停用', { exact: true }).isVisible(), true)
  await deleted.getByRole('button', { name: '预览清理影响' }).click()
  await page.getByRole('heading', { name: '清理已删除用户的关联', exact: true }).waitFor()
  await page.getByRole('button', { name: '关闭删除确认' }).click()
  withChild = true
  await deleted.getByRole('button', { name: '预览清理影响' }).click()
  await page.getByText('Emby 账号仍存在，将永久删除账号及观看记录', { exact: true }).waitFor()
  assert.equal(await page.getByRole('checkbox').isChecked(), false)
  await page.getByRole('button', { name: '关闭删除确认' }).click()
  await page.getByLabel('搜索 Emby 用户').fill('21111111')
  assert.equal(await deleted.isVisible(), true)
  await page.getByLabel('搜索 Emby 用户').fill('')
  failList = true
  await page.getByRole('button', { name: '刷新状态', exact: true }).click()
  await page.getByText('Emby 暂时无法连接', { exact: true }).waitFor()
  assert.equal(await page.getByText('Emby 已删除', { exact: true }).count(), 0)
  assert.equal(await page.locator('article.user-card').getByText('Emby 状态暂不可用', { exact: true }).count(), 5)
  assert.equal(await page.locator('article.user-card').getByLabel('播放来源').count(), 0)
  failList = false
  await page.getByRole('button', { name: '刷新状态', exact: true }).click()
  await active.getByLabel('播放来源').waitFor()
  if (process.env.CINEMA_SCREENSHOT) {
   if (await review.getAttribute('open') === null) await review.locator('summary').click()
   await review.screenshot({ path: process.env.CINEMA_SCREENSHOT })
  }
  await page.setViewportSize({ width: 390, height: 844 })
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
  assert.equal(writes.some(p => p.endsWith('/deletions') || p.endsWith('/settings')), false)
  assert.deepEqual(errors, [])
  console.log('User states: classification, count, collapsed cleanup, partial response, cascade preview, refresh failure/recovery, mobile layout passed')
 } finally { await browser.close() }
})().catch(e => { console.error(e); process.exit(1) })
