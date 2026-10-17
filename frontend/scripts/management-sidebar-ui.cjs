// All service requests are mocked; never operate on actual accounts or settings.
const path = require('node:path'), os = require('node:os'), fs = require('node:fs'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const context = await browser.newContext({ viewport: { width: 1920, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => {
   localStorage.setItem('admin_token', 'mock-sidebar')
   // A previously saved collapsed preference must not hide the navigation labels.
   localStorage.setItem('cinema115-management-sidebar', 'collapsed')
  })
  const errors = [], requests = [], writes = []
  let role = 'admin'
  const primary = { id: 'main', name: '主账号', uid: '123456', primary: true, configured: true, revision: 1, loginClient: 'tv', summary: { vip: 'vip', spaceKnown: true, remainingBytes: 54975581388800 } }
  const pool = [{ id: 'shared', name: '共享小号', uid: '987654', primary: false, configured: true, revision: 1, loginClient: '115ipad', playbackParentCid: '123', pool: { state: 'ready', mode: 'enabled', sessions: 0, preparing: 0, active: 0, residual: 0 } }]
  const member = { userId: 'Reader', embyId: 'a'.repeat(32), embyName: 'Reader', username: 'Reader', source: 'assigned', embyState: 'active', stage: 'ready', childLimit: 0, childUsed: 0, driveStatus: { state: 'ready', message: '播放时自动分配' } }
  await context.route('**/api/**', async route => {
   const request = route.request(), p = new URL(request.url()).pathname
   requests.push(p)
   if (request.method() !== 'GET') writes.push(p)
   let data = { configured: false, connected: false, items: [], total: 0, totalPages: 1, hasMore: false, accounts: [], jobs: [], pending: [], sessions: [], genres: [], bots: [], invites: [] }
   if (p === '/api/auth/me') data = { user: { id: 'admin', username: 'sidebar-review', role } }
   else if (p === '/api/admin/p115/accounts') data = { accounts: [primary, ...pool] }
   else if (p === '/api/admin/member-playback/pool') data = { settings: { maxSessions: 3 }, accounts: pool, metrics: { requests: 0, succeeded: 0, failed: 0 }, events: [], api: { waiting: 0 } }
   else if (p === '/api/admin/member-playback/users') data = { members: [member], settings: { enabled: true, allowDownload: false, libraryIds: [] } }
   else if (p === '/api/admin/member-playback/settings') data = { settings: { enabled: true, allowDownload: false, libraryIds: [] }, proxy: { listening: true } }
   else if (p === '/api/admin/member-playback/libraries') data = { libraries: [] }
   else if (p === '/api/admin/gying/config') data = { configured: false, baseUrl: 'https://example.com', resourceOrder: [], resourceTypes: [] }
   else if (p === '/api/admin/emby/missing') data = { configured: true, ready: true, total: 0, items: [], notices: [], offset: 0, nextOffset: 0, hasMore: false, summary: { series: 0, seasons: 0, episodes: 0 }, excludedCount: 0 }
   else if (p === '/api/admin/emby/proxy') data = { listening: false, config: { enabled: false, debug: false, prewarm: false, prepareFiles: true, directLinkCacheMinutes: 9, port: 8097, url: '', accountId: '', embyPathPrefix: '' } }
   await route.fulfill({ json: data })
  })
  const page = await context.newPage(); page.setDefaultTimeout(10000)
  page.on('pageerror', error => errors.push(error.message))
  const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
  const go = to => page.goto(origin + to, { waitUntil: 'networkidle' })
  const sidebar = () => page.getByRole('complementary', { name: '管理侧栏' })
  const nav = () => page.getByRole('navigation', { name: '管理功能', exact: true })
  const drawer = () => page.getByRole('dialog', { name: '功能导航', exact: true })
  const open = () => page.getByRole('button', { name: '打开功能导航', exact: true })
  const link = to => nav().locator(`a[href="${to}"]`)
  const active = async (to, current = 'location') => {
   assert.equal(await link(to).getAttribute('aria-current'), current)
   assert.equal(await nav().locator(`[data-section="${to}"] .management-nav-row`).evaluate(el => el.classList.contains('is-section-active')), true)
   assert.equal(await nav().locator('[aria-current]').count(), 1)
  }
  const noOverflow = async () => assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
  const screenshot = async name => {
   fs.mkdirSync('test-results', { recursive: true })
   await page.mouse.move(0, 0); await page.waitForTimeout(350)
   await page.screenshot({ path: `test-results/sidebar-${name}.png` })
  }
  await go('/helper115')
  await sidebar().waitFor(); await active('/115')
  assert.equal(await sidebar().evaluate(el => Math.round(el.getBoundingClientRect().width)), 216)
  assert.equal(await page.locator('main.management-content').evaluate(el => el.clientWidth >= 1200), true)
  // Hover keeps the list still, shows the marker and does not fetch or open a duplicate menu.
  const before = requests.length, embyBefore = await link('/emby').boundingBox()
  await link('/115').hover(); await page.waitForTimeout(250)
  assert.equal(await page.locator('.management-nav-flyout').count(), 0)
  assert.equal(await nav().getByRole('button').count(), 0)
  assert.equal(requests.length, before)
  assert.deepEqual(await link('/emby').boundingBox(), embyBefore)
  const hover = await nav().locator('[data-section="/115"]').evaluate(el => ({
   opacity: getComputedStyle(el.querySelector('.management-nav-marker')).opacity,
   text: getComputedStyle(el.querySelector('a > span')).transform,
   icon: getComputedStyle(el.querySelector('a > svg')).transform,
  }))
  assert.equal(hover.opacity, '1'); assert.notEqual(hover.text, 'none'); assert.notEqual(hover.icon, 'none')
  fs.mkdirSync('test-results', { recursive: true })
  await page.screenshot({ path: 'test-results/sidebar-desktop-hover.png' })
  // Native links keep their natural Tab order and Enter navigation.
  await link('/115').focus(); await page.keyboard.press('Tab')
  assert.equal(await link('/resources').evaluate(el => el === document.activeElement), true)
  await page.keyboard.press('Shift+Tab'); await page.keyboard.press('Enter'); await page.waitForURL('**/115')
  await active('/115', 'page')
  assert.equal(await page.locator('main a.navigation-card').count(), 5)
  await page.locator('main a[href="/helper115"]').click(); await page.waitForURL('**/helper115'); await active('/115')
  await screenshot('1920-light-helper')
  await page.evaluate(() => window.scrollTo(0, 500)); await page.waitForTimeout(100)
  const sticky = await sidebar().boundingBox()
  assert.ok(sticky.y >= 88 && sticky.y <= 90 && sticky.y + sticky.height <= 1000)
  await page.evaluate(() => window.scrollTo(0, 0))
  await link('/resources').click(); await page.waitForURL('**/resources'); await active('/resources', 'page')
  await link('/resources').hover(); await page.waitForTimeout(220)
  await page.screenshot({ path: 'test-results/sidebar-resources-hover.png' })
  await page.locator('main a[href="/gying"]').click(); await page.waitForURL('**/gying'); await active('/resources')
  await link('/users').click(); await page.waitForURL('**/users')
  await page.locator('main a[href="/users/list"]').click(); await page.waitForURL('**/users/list'); await active('/users')
  assert.equal(await sidebar().getByRole('button').count(), 0)
  await page.reload(); await sidebar().waitFor()
  assert.equal(await sidebar().evaluate(el => Math.round(el.getBoundingClientRect().width)), 216)
  assert.equal(await link('/115').getAttribute('title'), '115 网盘')
  assert.equal(await link('/115').getAttribute('aria-label'), '115 网盘')
  await link('/115').click(); await page.waitForURL('**/115'); await active('/115', 'page')
  await page.locator('main a[href="/helper115"]').click(); await page.waitForURL('**/helper115'); await active('/115')
  assert.equal(await sidebar().evaluate(el => Math.round(el.getBoundingClientRect().width)), 216)
  // Desktop navigation stays fully labelled at narrower desktop widths as well.
  await page.setViewportSize({ width: 1280, height: 800 }); await page.reload(); await sidebar().waitFor()
  assert.equal(await sidebar().evaluate(el => Math.round(el.getBoundingClientRect().width)), 216)
  await screenshot('1280-expanded')
  await page.setViewportSize({ width: 1440, height: 900 })
  await sidebar().waitFor()
  await noOverflow(); await screenshot('1440-expanded')
  // Focus, Escape, backdrop, focus trap, scroll lock and viewport change.
  await page.setViewportSize({ width: 390, height: 844 }); await open().waitFor()
  assert.equal(await sidebar().count(), 0)
  await open().click(); await drawer().waitFor()
  assert.equal(await drawer().getByRole('button', { name: '关闭功能导航' }).evaluate(el => el === document.activeElement), true)
  assert.equal(await page.evaluate(() => document.body.style.overflow), 'hidden')
  assert.equal(await nav().getByRole('link').count(), 7)
  await screenshot('390-navigation')
  for (let i = 0; i < 35; i++) await page.keyboard.press('Tab')
  assert.equal(await drawer().evaluate(el => el.contains(document.activeElement)), true)
  await drawer().evaluate(el => el.focus()); await page.keyboard.press('Shift+Tab')
  assert.equal(await drawer().evaluate(el => el.contains(document.activeElement)), true)
  await page.keyboard.press('Escape'); await drawer().waitFor({ state: 'detached' })
  assert.equal(await open().evaluate(el => el === document.activeElement), true)
  assert.equal(await page.evaluate(() => document.body.style.overflow), '')
  await open().click(); await drawer().waitFor()
  await page.mouse.click(375, 400); await drawer().waitFor({ state: 'detached' })
  await open().click(); await drawer().waitFor()
  await page.setViewportSize({ width: 1280, height: 800 }); await sidebar().waitFor()
  assert.equal(await drawer().count(), 0)
  assert.equal(await page.evaluate(() => document.body.style.overflow), '')
  assert.equal(await link('/115').evaluate(el => el === document.activeElement), true)
  await page.setViewportSize({ width: 1024, height: 360 }); await sidebar().waitFor()
  assert.equal(await nav().evaluate(el => el.scrollWidth <= el.clientWidth), true)
  await nav().evaluate(el => { el.scrollTop = el.scrollHeight })
  const last = await link('/emby').boundingBox()
  assert.ok(last.y >= 72 && last.y + last.height <= 360)
  // Navigation remembers its own scroll without moving the page.
  const scroll = await nav().evaluate(el => el.scrollTop)
  await link('/emby').click(); await page.waitForURL('**/emby')
  assert.ok(Math.abs(await nav().evaluate(el => el.scrollTop) - scroll) < 2)
  // Mobile route success closes the drawer; existing unsaved-leave rejection keeps it.
  await go('/emby/proxy'); await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole('spinbutton', { name: /反代监听端口/ }).fill('8098')
  await open().click(); await drawer().waitFor()
  page.once('dialog', dialog => dialog.dismiss())
  await link('/115').click()
  assert.equal(new URL(page.url()).pathname, '/emby/proxy')
  assert.equal(await drawer().isVisible(), true)
  page.once('dialog', dialog => dialog.accept())
  await link('/115').click(); await page.waitForURL('**/115')
  await drawer().waitFor({ state: 'detached' })
  assert.equal(await page.evaluate(() => document.body.style.overflow), '')
  // Selecting the current category also dismisses the drawer and restores focus.
  await open().click(); await drawer().waitFor()
  await link('/115').click(); await drawer().waitFor({ state: 'detached' })
  assert.equal(await open().evaluate(el => el === document.activeElement), true)
  assert.equal(await page.evaluate(() => document.body.style.overflow), '')
  for (const width of [1920, 1440, 1280, 1024, 1023, 768, 390, 320]) {
   await page.setViewportSize({ width, height: 900 })
   for (const dark of [false, true]) {
    await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
    await noOverflow()
    if (width < 1024) {
     await open().click(); await drawer().waitFor()
     await drawer().evaluate(el => Promise.all(el.getAnimations().map(animation => animation.finished)))
    }
    const b = await nav().boundingBox(); assert.ok(b.x >= 0 && b.x + b.width <= width)
    if (width >= 1024) {
     assert.equal(await sidebar().evaluate(el => Math.round(el.getBoundingClientRect().width)), 216)
     assert.equal(await sidebar().getByRole('button').count(), 0)
    }
    assert.equal(await nav().locator('a > span:visible').count(), 7)
    await active('/115', 'page')
    await screenshot(`${width}-${dark ? 'dark' : 'light'}`)
    assert.equal(await page.locator('.management-nav-flyout').count(), 0)
    if (width < 1024) { await page.keyboard.press('Escape'); await drawer().waitFor({ state: 'detached' }) }
   }
  }
  await page.setViewportSize({ width: 390, height: 568 }); await page.emulateMedia({ reducedMotion: 'reduce' })
  await open().click(); await drawer().waitFor()
  assert.equal(await drawer().evaluate(el => getComputedStyle(el).animationName), 'none')
  await page.keyboard.press('Escape')
  // Browsing and ordinary-user pages keep their full-width layout.
  await go('/movie'); assert.equal(await sidebar().count(), 0); assert.equal(await open().count(), 0)
  role = 'user'; await go('/follows'); assert.equal(await sidebar().count(), 0); assert.equal(await open().count(), 0)
  assert.deepEqual(writes, []); assert.deepEqual(errors, [])
  console.log('Management sidebar passed: no collapse button, full desktop labels despite old preference, direct category links, no duplicate flyouts or hover requests, preserved marker/scale, native Tab/Enter, card routing, resource aliases, sticky/independent scroll, mobile Escape/focus/backdrop/resize/current-link focus/route guards, no writes, role scope, 8 widths, both themes and reduced motion')
 } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
