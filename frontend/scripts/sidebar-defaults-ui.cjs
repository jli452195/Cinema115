// Isolated synthetic UI checks: every API response is mocked; no service writes.
const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 const errors = [], writes = []
 try {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, serviceWorkers: 'block' })
  await context.addInitScript(() => {
   localStorage.setItem('admin_token', 'mock-sidebar-defaults')
   if (!localStorage.getItem('cinema115-management-sidebar')) localStorage.setItem('cinema115-management-sidebar', 'collapsed')
   window.firstSidebarState = undefined
   new MutationObserver(() => {
    const element = document.querySelector('.management-sidebar')
    if (element && window.firstSidebarState === undefined) window.firstSidebarState = element.classList.contains('is-collapsed')
   }).observe(document, { childList: true, subtree: true })
  })
  await context.route('**/api/**', async route => {
   const request = route.request(), p = new URL(request.url()).pathname
   if (request.method() !== 'GET') writes.push(p)
   let data = { configured: false, connected: false, items: [], total: 0, totalPages: 1, hasMore: false, accounts: [], jobs: [], pending: [], sessions: [], genres: [], bots: [], invites: [] }
   if (p === '/api/admin/member-playback/pool') data = { settings: { maxSessions: 3 }, accounts: [], metrics: { requests: 0, succeeded: 0, failed: 0 }, events: [], api: { waiting: 0 } }
   else if (p === '/api/admin/media-organize/history') data = { items: [], page: 1, pages: 1, total: 0, counts: { all: 0, running: 0, done: 0, attention: 0 }, active: false, activity: [], cleanup: [] }
   else if (p === '/api/admin/media-organize') data = { rules: [], active: false, activeRules: [] }
   else if (p === '/api/auth/me') data = { user: { id: 'admin', username: 'sidebar-defaults-fixture', role: 'admin' } }
   else if (p === '/api/admin/emby/proxy') data = { listening: false, config: { enabled: false, debug: false, directLinkCacheMinutes: 9, port: 8097, url: '', accountId: '', embyPathPrefix: '' } }
   else if (/\/tmdb\/detail\/movie\/\d+$/.test(p)) {
    const id = Number(p.split('/').at(-1))
    data = { id, kind: 'movie', title: '侧栏默认状态测试 ' + id, originalTitle: 'Sidebar Default Fixture', overview: '', date: '2026-01-01', rating: 7, votes: 1, status: 'Released', runtime: 90, countries: [], genres: [], cast: [], seasons: [], collection: null }
   }
   await route.fulfill({ json: data })
  })
  const page = await context.newPage()
  page.setDefaultTimeout(15000)
  page.on('pageerror', e => errors.push(e.message))
  const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
  const rail = () => page.getByRole('complementary', { name: '功能侧栏', exact: true })
  const toggle = () => rail().locator('.management-brand-toggle')
  const state = async collapsed => {
   await page.waitForFunction(collapsed => document.querySelector('.management-sidebar')?.classList.contains('is-collapsed') === collapsed, collapsed)
   await page.waitForFunction(collapsed => {
    const sidebar = document.querySelector('.management-sidebar'), navigation = document.querySelector('#management-desktop-navigation')
    return sidebar && Math.abs(sidebar.getBoundingClientRect().width - (collapsed ? 64 : 240)) < .1
     && navigation && navigation.classList.contains('is-collapsed') === collapsed
     && !navigation.classList.contains('is-transitioning')
     && Math.abs(Number(getComputedStyle(navigation).opacity) - 1) < .001
     && !navigation.getAnimations({ subtree: true }).some(animation => animation.playState === 'running' && Number.isFinite(animation.effect.getComputedTiming().endTime))
   }, collapsed)
   assert.equal(await toggle().isVisible(), !collapsed)
   if (!collapsed) assert.equal(await toggle().getAttribute('aria-expanded'), 'true')
   assert.equal(Math.round((await rail().boundingBox()).width), collapsed ? 64 : 240)
   assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
  }
  const direct = async (to, collapsed) => {
   await page.goto(origin + to, { waitUntil: 'networkidle' }); await state(collapsed)
   assert.equal(await page.evaluate(() => window.firstSidebarState), collapsed, 'First rendered rail uses the target default')
  }
  const push = async to => {
   // Use the mounted router: Vite may serve timestamped modules after hot updates.
   await page.evaluate(async to => {
    await document.querySelector('#app').__vue_app__.config.globalProperties.$router.push(to)
   }, to)
  }
  // Stale collapsed preference cannot shrink management pages, even on first paint/reload.
  await direct('/helper115', false)
  await toggle().click(); await state(true)
  await page.reload({ waitUntil: 'networkidle' }); await state(false)
  await toggle().click(); await state(true)
  await push('/resources'); await state(false) // Same meta value, new path resets.
  await toggle().click(); await state(true)
  await push('/resources?sort=desc#details'); await state(true)
  await page.setViewportSize({ width: 390, height: 844 })
  assert.equal(await rail().count(), 0)
  await page.locator('.workbench-header .brand-link').click()
  await page.getByRole('dialog', { name: '功能导航', exact: true }).waitFor()
  await page.keyboard.press('Escape')
  await page.getByRole('dialog', { name: '功能导航', exact: true }).waitFor({ state: 'detached' })
  await page.setViewportSize({ width: 1024, height: 800 }); await state(true)
  assert.equal(await page.evaluate(() => document.body.style.overflow), '')
  // Stale expanded preference cannot enlarge a detail page.
  await page.evaluate(() => localStorage.setItem('cinema115-management-sidebar', 'expanded'))
  await direct('/movie/1452', true)
  await push('/movie/1453'); await state(true) // Reused detail component and identical meta.
  await push('/movie/1453?tab=cast#cast'); await state(true)
  await page.reload({ waitUntil: 'networkidle' }); await state(true)
  await push('/media/organize/history'); await state(false) // Inherited parent meta.
  await toggle().click(); await state(true)
  await push('/media/organize'); await state(false)
  await toggle().click(); await state(true)
  await push('/115/pool?source=test'); await state(false) // Final redirect target.
  assert.equal(new URL(page.url()).pathname, '/helper115')
  await push('/emby/proxy'); await state(false)
  await page.getByRole('spinbutton', { name: /反代监听端口/ }).fill('8098')
  await toggle().click(); await state(true)
  page.once('dialog', dialog => dialog.dismiss())
  await rail().getByRole('link', { name: '115 网盘', exact: true }).click()
  assert.equal(new URL(page.url()).pathname, '/emby/proxy'); await state(true)
  page.once('dialog', dialog => dialog.accept())
  await rail().getByRole('link', { name: '115 网盘', exact: true }).click()
  await page.waitForURL('**/115'); await state(false)
  // Browse routes, including notifications/updates, all start folded.
  for (const to of ['/movie', '/tv', '/notifications', '/updates']) { await push(to); await state(true) }
  assert.equal(await page.evaluate(() => localStorage.getItem('cinema115-management-sidebar')), 'expanded', 'Manual actions do not persist a global preference')
  assert.deepEqual(writes, [])
  assert.deepEqual(errors, [])
  console.log('Sidebar defaults passed: initial paint, stale storage, refresh, same-meta/new-path, reused detail, query/hash, nested meta, redirects, rejected navigation, mobile drawer and viewport; no service writes')
 } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
