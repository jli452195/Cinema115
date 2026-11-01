// All service requests are mocked; never operate on actual accounts or settings.
const path = require('node:path'), os = require('node:os'), fs = require('node:fs'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const context = await browser.newContext({ viewport: { width: 1920, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => {
   localStorage.setItem('admin_token', 'mock-sidebar')
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
   if (p.startsWith('/api/tmdb/image/')) return route.fulfill({ contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="1600" height="900"><defs><linearGradient id="bg"><stop stop-color="#9eaff0"/><stop offset=".5" stop-color="#f8d59c"/><stop offset="1" stop-color="#d2c5e7"/></linearGradient></defs><path fill="url(#bg)" d="M0 0h1600v900H0z"/></svg>' })
   let data = { configured: false, connected: false, items: [], total: 0, totalPages: 1, hasMore: false, accounts: [], jobs: [], pending: [], sessions: [], genres: [], bots: [], invites: [] }
   if (p === '/api/auth/me') data = { user: { id: 'admin', username: 'sidebar-review', role } }
   else if (p === '/api/admin/media-organize') data = { rules: [], active: false, activeRules: [] }
   else if (p === '/api/admin/tmdb/detail/movie/1452') data = { id: 1452, kind: 'movie', title: '侧栏布局测试', originalTitle: 'Sidebar Layout Fixture', overview: '仅用于独立界面检查的示例内容。', posterUrl: '/api/tmdb/image/w500/sidebar-fixture.svg', backdropUrl: '/api/tmdb/image/w1280/sidebar-fixture.svg', date: '2026-01-01', rating: 7.9, votes: 100, tagline: '', status: 'Released', runtime: 96, episodeCount: 0, countries: [], genres: [], cast: [], seasons: [], collection: null, imdbUrl: '', tmdbUrl: '' }
   else if (p === '/api/admin/p115/accounts') data = { accounts: [primary, ...pool] }
   else if (p === '/api/admin/member-playback/pool') data = { settings: { maxSessions: 3 }, accounts: pool, metrics: { requests: 0, succeeded: 0, failed: 0 }, events: [], api: { waiting: 0 } }
   else if (p === '/api/admin/member-playback/users') data = { members: [member], settings: { enabled: true, allowDownload: false, libraryIds: [] } }
   else if (p === '/api/admin/member-playback/settings') data = { settings: { enabled: true, allowDownload: false, libraryIds: [] }, proxy: { listening: true } }
   else if (p === '/api/admin/member-playback/libraries') data = { libraries: [] }
   else if (p === '/api/admin/gying/config') data = { configured: false, baseUrl: 'https://example.com', resourceOrder: [], resourceTypes: [] }
   else if (p === '/api/admin/emby/missing') data = { configured: true, ready: true, total: 0, items: [], notices: [], offset: 0, nextOffset: 0, hasMore: false, summary: { series: 0, seasons: 0, episodes: 0 }, excludedCount: 0 }
   else if (p === '/api/admin/emby/proxy') data = { listening: false, config: { enabled: false, debug: false, directLinkCacheMinutes: 9, port: 8097, url: '', accountId: '', embyPathPrefix: '' } }
   await route.fulfill({ json: data })
  })
  const page = await context.newPage(); page.setDefaultTimeout(10000)
  page.on('pageerror', error => errors.push(error.message))
  const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
  const go = to => page.goto(origin + to, { waitUntil: 'networkidle' })
  const sidebar = () => page.getByRole('complementary', { name: '功能侧栏', exact: true })
  const nav = () => page.getByRole('navigation', { name: '功能导航', exact: true })
  const drawer = () => page.getByRole('dialog', { name: '功能导航', exact: true })
  const open = () => page.getByRole('button', { name: '打开功能导航', exact: true })
  const toggle = () => sidebar().locator('.management-brand-toggle')
  const link = to => nav().locator(`a[href="${to}"]`)
  const settleNavigation = async collapsed => page.waitForFunction(collapsed => {
   const navigation = document.querySelector('#management-desktop-navigation')
   return navigation && navigation.classList.contains('is-collapsed') === collapsed
    && !navigation.classList.contains('is-transitioning')
    && Math.abs(Number(getComputedStyle(navigation).opacity) - 1) < .001
    && !navigation.getAnimations({ subtree: true }).some(animation => animation.playState === 'running' && Number.isFinite(animation.effect.getComputedTiming().endTime))
  }, collapsed)
  const active = async (to, current = 'location') => {
   assert.equal(await link(to).getAttribute('aria-current'), current)
   assert.equal(await nav().locator(`[data-section="${to}"] .management-nav-row`).evaluate(el => el.classList.contains('is-section-active')), true)
   assert.equal(await nav().locator('[aria-current]').count(), 1)
  }
  const noOverflow = async () => assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
  const brandState = async () => {
   const brand = page.locator('.management-sidebar .management-sidebar-home'), wordmark = brand.locator('.cinema-logo-wordmark')
   assert.equal(await brand.getAttribute('href'), '/')
   assert.equal(await wordmark.isVisible(), true)
   assert.equal(await wordmark.innerText(), 'Cinema115')
   assert.equal(await wordmark.evaluate(el => {
    const bounds = el.getBoundingClientRect()
    return bounds.width > 70 && el.closest('a') === document.elementFromPoint(bounds.right - 2, bounds.y + bounds.height / 2)?.closest('a')
   }), true, 'The complete wordmark must stay visible and clickable in both desktop states')
   const mark = await brand.locator('.cinema-logo-mark').boundingBox()
   assert.equal(Math.round(mark.width), 36)
   assert.equal(await brand.locator('.cinema-logo-mark').evaluate(el => {
    const bounds = el.getBoundingClientRect()
    return el.closest('a') === document.elementFromPoint(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2)?.closest('a')
   }), true)
   if (!await sidebar().evaluate(el => el.classList.contains('is-collapsed'))) {
    assert.equal(await wordmark.isVisible(), true)
    assert.equal(await wordmark.innerText(), 'Cinema115')
    const panel = await nav().boundingBox(), rail = await sidebar().boundingBox()
    const icon = await nav().locator('.management-nav-link > svg').first().boundingBox()
    const label = await nav().locator('.management-nav-link > span').first().boundingBox()
    assert.ok(Math.abs(mark.x + mark.width / 2 - icon.x - icon.width / 2) < 1, 'Logo mark must align with the menu icon column')
    assert.ok(Math.abs((await wordmark.boundingBox()).x - label.x) < 2, 'Logo wordmark must align with the menu label column')
    const headerHeight = await page.locator('.workbench-header .header-inner').evaluate(el => el.getBoundingClientRect().height)
    assert.ok(Math.abs(panel.x) < .5)
    assert.ok(Math.abs(panel.y - headerHeight) < .5)
    assert.ok(Math.abs(panel.x + panel.width - rail.x - rail.width) <= 1)
    assert.ok(Math.abs(panel.y + panel.height - page.viewportSize().height) < .5)
    const style = await nav().evaluate(el => {
     const css = getComputedStyle(el)
     return { padding: css.paddingTop, horizontal: css.paddingLeft, bottom: css.paddingBottom, border: css.borderTopWidth, radius: css.borderTopLeftRadius }
    })
    assert.deepEqual(style, { padding: '14px', horizontal: '10px', bottom: '16px', border: '0px', radius: '0px' })
   } else {
    assert.ok(Math.abs(mark.x - 14) < .5)
   }
   const headerSurface = await page.locator('.workbench-header .header-background, .workbench-header .header-surface').boundingBox()
   assert.ok(Math.abs(headerSurface.x) < 1)
  }
  const desktopState = async collapsed => {
   // Pointer-driven magnification is checked separately; layout contracts use its resting size.
   await page.mouse.move(500, 60)
   const width = collapsed ? 64 : 240
   await page.waitForFunction(width => {
    const sidebar = document.querySelector('.management-sidebar')?.getBoundingClientRect()
    const header = document.querySelector('.workbench-header')?.getBoundingClientRect()
    return sidebar && header && Math.abs(sidebar.width - width) < 0.5 && Math.abs(header.x - width) < 0.5
   }, width)
   await page.evaluate(() => Promise.all(
    [...document.querySelectorAll('.management-sidebar, .workbench-header, .portal-workspace')]
     .flatMap(element => element.getAnimations()).map(animation => animation.finished.catch(() => {}))))
   await settleNavigation(collapsed)
   const bounds = await sidebar().boundingBox()
   assert.equal(bounds.x, 0); assert.equal(bounds.y, 0)
   assert.equal(Math.round(bounds.width), width); assert.equal(Math.round(bounds.height), page.viewportSize().height)
   assert.equal(await toggle().isVisible(), !collapsed)
   assert.equal(await toggle().getAttribute('aria-label'), '收起侧栏')
   if (!collapsed) assert.equal(await toggle().getAttribute('aria-expanded'), 'true')
   assert.equal(await nav().getAttribute('id'), 'management-desktop-navigation')
   assert.equal(await page.locator('.workbench-header .brand-link').count(), 0)
   assert.equal(await sidebar().getByRole('link', { name: 'Cinema115 首页', exact: true }).count(), 1)
   assert.equal(await toggle().locator('.cinema-logo').count(), 0)
   assert.equal(await toggle().locator('svg').count(), 1)
   assert.equal(await sidebar().locator('.management-collapse-control:visible').count(), collapsed ? 0 : 1)
   await brandState()
   assert.equal(await page.locator('main').evaluate((el, width) => el.getBoundingClientRect().x >= width, width), true)
   await nav().locator('.management-nav-row, .management-nav-link > svg').evaluateAll(elements => Promise.all(elements
    .flatMap(el => el.getAnimations()).filter(animation => Number.isFinite(animation.effect.getComputedTiming().endTime))
    .map(animation => animation.finished.catch(() => {}))))
   if (collapsed) {
    await page.waitForFunction(() => [...document.querySelectorAll('.management-nav-link')]
     .every(el => Math.abs(el.getBoundingClientRect().height - 44) < .1))
    assert.equal(await sidebar().evaluate(el => getComputedStyle(el).backgroundColor.endsWith(', 0)')), true)
    assert.equal(await sidebar().evaluate(el => getComputedStyle(el).borderRightColor.endsWith(', 0)')), true)
    assert.equal(await nav().locator('a > span:visible').count(), 0)
    assert.equal(await nav().locator('svg').count(), 10)
    assert.equal(await nav().locator('.management-nav-row').evaluateAll(rows => rows.every(row => {
     const style = getComputedStyle(row)
     return style.backgroundColor === 'rgba(0, 0, 0, 0)' && style.boxShadow === 'none'
      && getComputedStyle(row, '::before').display === 'none' && getComputedStyle(row, '::after').display === 'none'
      && getComputedStyle(row.querySelector('.management-nav-marker')).display === 'none'
    })), true)
    const icons = await nav().locator('.management-nav-row').evaluateAll(rows => rows.map(row => {
     const svg = row.querySelector('svg'), style = getComputedStyle(svg)
     return { active: row.classList.contains('is-section-active'), color: style.color, stroke: parseFloat(style.strokeWidth) }
    }))
    const activeColors = await page.locator('html').evaluate(el => el.classList.contains('dark'))
     ? ['rgb(96, 165, 250)', 'rgb(147, 197, 253)'] : ['rgb(37, 99, 235)', 'rgb(29, 78, 216)']
    assert.equal(icons.filter(icon => icon.active).length, 1)
    for (const icon of icons) {
     assert.equal(icon.stroke, icon.active ? 2.25 : 1.75)
     assert.equal(activeColors.includes(icon.color), icon.active)
    }
   } else {
    assert.equal(await nav().locator('svg').evaluateAll(icons => icons.every(icon => getComputedStyle(icon).strokeWidth === '1.75px')), true)
   }
   await noOverflow()
  }
  const screenshot = async name => {
   fs.mkdirSync('test-results', { recursive: true })
   await page.mouse.move(0, 0); await page.waitForTimeout(350)
   await page.screenshot({ path: `test-results/sidebar-${name}.png` })
  }
  await go('/helper115')
  await sidebar().waitFor(); await active('/115')
  await desktopState(false)
  const collapseControl = sidebar().locator('.management-collapse-control')
  const initialURL = page.url(), controlRequests = requests.length
  const brandBounds = await sidebar().locator('.management-sidebar-home .cinema-logo').boundingBox(), controlBounds = await collapseControl.boundingBox()
  assert.ok(controlBounds.x > brandBounds.x + brandBounds.width)
  assert.ok(controlBounds.x + controlBounds.width <= 240)
  await collapseControl.focus(); await page.keyboard.press('Enter'); await desktopState(true)
  assert.equal(await sidebar().locator('.management-sidebar-home').evaluate(el => el === document.activeElement), true)
  assert.equal(page.url(), initialURL); assert.equal(requests.length, controlRequests)
  await page.reload({ waitUntil: 'networkidle' }); await desktopState(false)
  // Every page must keep the same groups, destinations, logo and row positions.
  const navigationLayout = () => sidebar().evaluate(el => {
   const bounds = node => { const r = node.getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height } }
   return {
    brand: bounds(el.querySelector('.cinema-logo')),
    groups: [...el.querySelectorAll('.management-nav-group')].map(group => ({
     label: group.getAttribute('aria-label'), bounds: bounds(group),
     links: [...group.querySelectorAll('a')].map(link => ({ label: link.getAttribute('aria-label'), to: link.getAttribute('href'), bounds: bounds(link) })),
    })),
   }
  })
  const sharedNavigation = await navigationLayout()
  assert.deepEqual(sharedNavigation.groups.map(group => group.label), ['个人', '管理', '服务'])
  for (const [to, name] of [['/resources', 'resources'], ['/follows/manage', 'follows-management'], ['/media/organize', 'organize'], ['/115', 'drive']]) {
   await go(to); await sidebar().waitFor()
   await desktopState(false)
   assert.deepEqual(await navigationLayout(), sharedNavigation, `Navigation moved or changed on ${to}`)
   for (const dark of [false, true]) {
    await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
    await screenshot(`consistent-${name}-${dark ? 'dark' : 'light'}`)
   }
  }
  await go('/helper115'); await sidebar().waitFor(); await desktopState(false)
  assert.equal(await page.locator('main.management-content').evaluate(el => el.clientWidth >= 1200), true)
  // Hover keeps the list still and does not fetch or open a duplicate menu.
  const before = requests.length, embyBefore = await link('/emby').boundingBox()
  await link('/115').hover(); await page.waitForTimeout(250)
  assert.equal(await page.locator('.management-nav-flyout').count(), 0)
  assert.equal(await nav().getByRole('button').count(), 0)
  assert.equal(requests.length, before)
  assert.deepEqual(await link('/emby').boundingBox(), embyBefore)
  fs.mkdirSync('test-results', { recursive: true })
  await page.screenshot({ path: 'test-results/sidebar-desktop-hover.png' })
  // Native links keep their natural Tab order and Enter navigation.
  await link('/115').focus(); await page.keyboard.press('Tab')
  assert.equal(await link('/resources').evaluate(el => el === document.activeElement), true)
  await page.keyboard.press('Shift+Tab'); await page.keyboard.press('Enter'); await page.waitForURL('**/115')
  await active('/115', 'page')
  await page.locator('main a[href="/helper115"]').click(); await page.waitForURL('**/helper115'); await active('/115')
  await screenshot('1920-light-helper')
  await page.evaluate(() => window.scrollTo(0, 500)); await page.waitForTimeout(100)
  await desktopState(false)
  await page.evaluate(() => window.scrollTo(0, 0))
  await link('/resources').click(); await page.waitForURL('**/resources'); await active('/resources', 'page')
  await link('/resources').hover(); await page.waitForTimeout(220)
  await page.screenshot({ path: 'test-results/sidebar-resources-hover.png' })
  await page.locator('main a[href="/gying"]').click(); await page.waitForURL('**/gying'); await active('/resources')
  await link('/users').click(); await page.waitForURL('**/users')
  await page.locator('main a[href="/users/list"]').click(); await page.waitForURL('**/users/list'); await active('/users')
  assert.equal(await sidebar().getByRole('button').count(), 1)
  await page.reload(); await sidebar().waitFor()
  await desktopState(false)
  assert.equal(await link('/115').getAttribute('aria-label'), '115 网盘')
  // The icon rail keeps accessible names, keyboard use and visible name hints.
  const expandedContent = await page.locator('main.management-content').boundingBox()
  const collapseRequests = requests.length
  const beforeLogoToggle = page.url()
  // Sample actual geometry while the shared width moves, independently of its animation mechanism.
  await page.evaluate(() => {
   window.sidebarMotionSample = new Promise(resolve => {
    document.querySelector('.management-sidebar .management-brand-toggle').addEventListener('click', () => {
     const sample = () => {
      const sidebar = document.querySelector('.management-sidebar'), header = document.querySelector('.workbench-header'), shell = document.querySelector('.portal-workspace')
      const width = sidebar.getBoundingClientRect().width
      if (width > 90 && width < 220) resolve({ width, header: header.getBoundingClientRect().x, content: parseFloat(getComputedStyle(shell).paddingLeft) })
      else requestAnimationFrame(sample)
     }
     requestAnimationFrame(sample)
    }, { once: true })
   })
  })
  await toggle().click(); await desktopState(true)
  const motion = await page.evaluate(() => window.sidebarMotionSample)
  assert.ok(motion.width > 64 && motion.width < 240)
  assert.ok(Math.abs(motion.header - motion.width) < 1 && Math.abs(motion.content - motion.width) < 1)
  assert.equal(page.url(), beforeLogoToggle)
  assert.equal(await page.evaluate(() => localStorage.getItem('cinema115-management-sidebar')), null)
  const collapsedContent = await page.locator('main.management-content').boundingBox()
  assert.ok(collapsedContent.width >= expandedContent.width)
  assert.equal(requests.length, collapseRequests)
  await link('/115').hover()
  await page.getByRole('tooltip', { name: '115 网盘', exact: true }).waitFor()
  await desktopState(true)
  await page.mouse.move(500, 100)
  await link('/resources').focus()
  await page.getByRole('tooltip', { name: '资源配置', exact: true }).waitFor()
  await page.keyboard.press('Enter'); await page.waitForURL('**/resources'); await active('/resources', 'page')
  await desktopState(false)
  await toggle().click(); await desktopState(true)
  await page.reload(); await sidebar().waitFor(); await desktopState(false)
  await toggle().click(); await desktopState(true)
  await screenshot('1920-collapsed')
  await link('/115').click(); await page.waitForURL('**/115'); await active('/115', 'page')
  await page.locator('main a[href="/helper115"]').click(); await page.waitForURL('**/helper115'); await active('/115')
  await desktopState(false)
  assert.equal(await page.evaluate(() => localStorage.getItem('cinema115-management-sidebar')), null)
  // Both desktop sizes use the management default after refresh.
  await page.setViewportSize({ width: 1280, height: 800 }); await page.reload(); await sidebar().waitFor()
  await desktopState(false)
  await screenshot('1280-expanded')
  await page.setViewportSize({ width: 1440, height: 900 })
  await sidebar().waitFor()
  await noOverflow(); await screenshot('1440-expanded')
  // Focus, Escape, backdrop, focus trap, scroll lock and viewport change.
  await page.setViewportSize({ width: 390, height: 844 }); await open().waitFor()
  assert.equal(await sidebar().count(), 0)
  await open().click(); await drawer().waitFor()
  assert.equal(await drawer().locator('nav svg').evaluateAll(icons => icons.every(icon => getComputedStyle(icon).strokeWidth === '1.75px')), true)
  assert.equal(await drawer().getByRole('button', { name: '关闭功能导航' }).evaluate(el => el === document.activeElement), true)
  assert.equal(await page.evaluate(() => document.body.style.overflow), 'hidden')
  await screenshot('390-navigation')
  for (let i = 0; i < 35; i++) await page.keyboard.press('Tab')
  assert.equal(await drawer().evaluate(el => el.contains(document.activeElement)), true)
  await drawer().evaluate(el => el.focus()); await page.keyboard.press('Shift+Tab')
  assert.equal(await drawer().evaluate(el => el.contains(document.activeElement)), true)
  await page.keyboard.press('Escape'); await drawer().waitFor({ state: 'detached' })
  assert.equal(await open().evaluate(el => el === document.activeElement), true)
  assert.equal(await page.evaluate(() => document.body.style.overflow), '')
  // Both mobile triggers operate the same drawer, and the drawer logo closes it.
  const headerLogo = () => page.locator('.workbench-header .brand-link')
  const mobileURL = page.url()
  await headerLogo().click(); await drawer().waitFor()
  assert.equal(await page.getByRole('dialog').count(), 1)
  assert.equal(await headerLogo().getAttribute('aria-expanded'), 'true')
  assert.equal(await headerLogo().getAttribute('aria-controls'), await drawer().getAttribute('id'))
  await drawer().getByRole('button', { name: '关闭侧栏', exact: true }).click()
  await drawer().waitFor({ state: 'detached' })
  assert.equal(await headerLogo().evaluate(el => el === document.activeElement), true)
  assert.equal(await headerLogo().getAttribute('aria-expanded'), 'false')
  assert.equal(page.url(), mobileURL)
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
  // Short icon rails keep the focused link's hint after Tab scrolls it into view.
  await toggle().click(); await desktopState(true)
  await link('/follows/manage').focus()
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
  const topBeforeTab = await nav().evaluate(el => el.scrollTop)
  for (let i = 0; i < 8; i++) await page.keyboard.press('Tab')
  assert.equal(await link('/emby').evaluate(el => el === document.activeElement), true)
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
  assert.ok(await nav().evaluate(el => el.scrollTop) > topBeforeTab)
  assert.equal(await page.getByRole('tooltip', { name: 'Emby', exact: true }).isVisible(), true)
  await page.reload({ waitUntil: 'networkidle' }); await desktopState(false)
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
    if (width >= 1024) await desktopState(false)
    assert.equal(await nav().locator('a > span:visible').count(), await nav().getByRole('link').count())
    await active('/115', 'page')
    await screenshot(`${width}-${dark ? 'dark' : 'light'}`)
    assert.equal(await page.locator('.management-nav-flyout').count(), 0)
    if (width < 1024) { await page.keyboard.press('Escape'); await drawer().waitFor({ state: 'detached' }) }
    else {
     await toggle().click(); await desktopState(true); await active('/115', 'page')
     assert.equal(await nav().getByRole('link').count(), 10)
     await screenshot(`${width}-${dark ? 'dark' : 'light'}-collapsed`)
     await page.reload({ waitUntil: 'networkidle' }); await desktopState(false)
    }
   }
  }
  await page.setViewportSize({ width: 390, height: 568 }); await page.emulateMedia({ reducedMotion: 'reduce' })
  await open().click(); await drawer().waitFor()
  assert.equal(await drawer().evaluate(el => getComputedStyle(el).animationName), 'none')
  await page.keyboard.press('Escape')
  await page.setViewportSize({ width: 1280, height: 800 }); await sidebar().waitFor()
  await toggle().click(); await desktopState(true)
  assert.equal(await page.evaluate(() => [...document.querySelectorAll('.management-sidebar, .workbench-header, .portal-workspace')].flatMap(element => element.getAnimations()).length), 0)
  await page.reload({ waitUntil: 'networkidle' }); await desktopState(false)
  // Homepage browsing shares the same desktop SVG rail and animation; mobile keeps its drawer.
  await page.emulateMedia({ reducedMotion: 'no-preference' })
  await go('/movie?sort=rating')
  assert.equal(await sidebar().count(), 1); assert.equal(await open().count(), 0)
  const homeSidebar = () => page.getByRole('complementary', { name: '功能侧栏', exact: true })
  const homeToggle = () => homeSidebar().locator('.management-brand-toggle')
  const settleHomeSidebar = async () => {
   const collapsed = await homeSidebar().evaluate(el => el.classList.contains('is-collapsed'))
   await page.waitForFunction(width => Math.abs(document.querySelector('.management-sidebar').getBoundingClientRect().width - width) < .1, collapsed ? 64 : 240)
   await settleNavigation(collapsed)
  }
  for (const width of [2560, 1920, 1280, 1024, 1023, 768, 390, 320]) {
   await page.setViewportSize({ width, height: 900 })
   if (width >= 1024) {
    await settleHomeSidebar()
   }
   await page.evaluate(() => { document.querySelector('main').style.minHeight = '1400px' })
   for (const dark of [false, true]) {
    await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
    await noOverflow()
    const content = await page.locator('main').boundingBox()
    const viewport = await page.evaluate(() => document.documentElement.getBoundingClientRect().width)
    const inset = width >= 1024 ? 64 : 0
    assert.ok(Math.abs(content.width - Math.min(viewport - inset, 1600)) < 1)
    assert.ok(Math.abs(content.x - inset - (viewport - inset - content.width) / 2) < 1)
    const account = await page.locator('button[aria-controls="header-management"]').boundingBox()
    assert.ok(account.x + account.width >= viewport - 25)
    if (width === 1920) await screenshot(`browse-${width}-${dark ? 'dark' : 'light'}`)
    const beforeURL = page.url(), beforeOpen = requests.length
    if (width >= 1024) {
     assert.equal(await headerLogo().count(), 0)
     const bounds = await homeSidebar().boundingBox()
     assert.deepEqual({ x: bounds.x, y: bounds.y, width: bounds.width, height: bounds.height }, { x: 0, y: 0, width: 64, height: 900 })
     assert.equal(await homeSidebar().evaluate(el => getComputedStyle(el).backgroundColor.endsWith(', 0)')), true)
     await brandState()
     assert.equal(await nav().locator('a > span:visible').count(), 0)
     assert.equal(await homeSidebar().locator('nav svg').count(), 10)
     assert.equal(await homeToggle().isVisible(), false)
     assert.equal(await page.getByRole('dialog').count(), 0)
     assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    } else {
     assert.equal(await homeSidebar().count(), 0)
     assert.ok((await headerLogo().boundingBox()).x <= 24)
     await headerLogo().click(); await drawer().waitFor()
     await drawer().evaluate(el => Promise.all(el.getAnimations().map(animation => animation.finished)))
     assert.equal(await headerLogo().getAttribute('aria-expanded'), 'true')
     assert.equal(await headerLogo().getAttribute('aria-controls'), await drawer().getAttribute('id'))
     assert.deepEqual(await page.locator('main').boundingBox(), content)
     assert.equal(await drawer().getByRole('link', { name: '我的追更', exact: true }).count(), 1)
     assert.equal(await drawer().getByRole('link', { name: '115 网盘', exact: true }).count(), 1)
     if (width === 390) await screenshot(`browse-${width}-${dark ? 'dark' : 'light'}-navigation`)
     await drawer().getByRole('button', { name: '关闭侧栏', exact: true }).click()
     await drawer().waitFor({ state: 'detached' })
     assert.equal(await headerLogo().getAttribute('aria-expanded'), 'false')
     assert.equal(await headerLogo().evaluate(el => el === document.activeElement), true)
    }
    assert.equal(page.url(), beforeURL)
    assert.equal(requests.length, beforeOpen)
    assert.deepEqual(await page.locator('main').boundingBox(), content)
   }
  }
  await page.setViewportSize({ width: 1920, height: 900 })
  await settleHomeSidebar()
  // Real route reversals settle correctly; collapsed browsing has no extra expand control.
  await page.evaluate(async () => {
   const router = document.querySelector('#app').__vue_app__.config.globalProperties.$router
   await router.push('/115'); await router.push('/movie')
  })
  await settleHomeSidebar()
  assert.equal(await homeToggle().isVisible(), false)
  await page.getByRole('link', { name: '剧集', exact: true }).click()
  await page.waitForURL(/\/(tv)?$/); await homeSidebar().waitFor()
  assert.equal(await homeToggle().isVisible(), false)
  await homeSidebar().getByRole('link', { name: '115 网盘', exact: true }).click()
  await page.waitForURL('**/115'); await drawer().waitFor({ state: 'detached' })
  await sidebar().waitFor()
  await desktopState(false)
  assert.equal(await page.evaluate(() => document.body.style.overflow), '')
  // The media background reaches under the transparent rail, including below its icons.
  await go('/movie/1452')
  await page.locator('h1').filter({ hasText: '侧栏布局测试' }).waitFor()
  for (const width of [1920, 1024, 390]) {
   await page.setViewportSize({ width, height: 900 })
   for (const dark of [false, true]) {
    await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
    if (width >= 1024) {
     for (const collapsed of [true]) {
      await settleHomeSidebar(); await brandState()
      const backdrop = await page.locator('.detail-backdrop').boundingBox()
      assert.ok(Math.abs(backdrop.x) < 1)
      const backgroundViewport = await page.evaluate(() => document.documentElement.getBoundingClientRect().width)
      assert.ok(Math.abs(backdrop.width - backgroundViewport) < 1, JSON.stringify({ width, dark, collapsed, backdrop, backgroundViewport }))
      assert.equal(await page.locator('.detail-page').evaluate(el => {
       const surface = getComputedStyle(el, '::before'), bounds = el.getBoundingClientRect()
       return Math.abs(bounds.x + parseFloat(surface.left)) < 1 && surface.backgroundColor === getComputedStyle(el).backgroundColor
      }), true)
      if (width === 1920 && collapsed) await screenshot(`detail-${dark ? 'dark' : 'light'}-collapsed`)
     }
    } else {
     assert.equal(await homeSidebar().count(), 0)
     assert.equal((await page.locator('.detail-backdrop').boundingBox()).x, 0)
     assert.equal(await page.locator('.detail-page').evaluate(el => getComputedStyle(el, '::before').content), 'none')
    }
    await noOverflow()
   }
  }
  await page.setViewportSize({ width: 1920, height: 900 })
  role = 'user'; await go('/follows')
  assert.equal(await sidebar().count(), 1); assert.equal(await open().count(), 0)
  assert.deepEqual(await nav().getByRole('link').allTextContents(), ['我的播放', '我的追更'])
  await page.setViewportSize({ width: 390, height: 844 })
  await headerLogo().click(); await drawer().waitFor()
  assert.deepEqual(await drawer().getByRole('link').allTextContents(), ['我的播放', '我的追更'])
  await drawer().getByRole('link', { name: '我的追更', exact: true }).click()
  await drawer().waitFor({ state: 'detached' })
  assert.equal(await headerLogo().evaluate(el => el === document.activeElement), true)
  assert.deepEqual(writes, []); assert.deepEqual(errors, [])
  console.log('Sidebar passed: identical groups, links, brand and row positions on homepage, follows management, organize and drive pages; persistent Cinema115 brand, full-bleed detail background, synchronized motion, route defaults and temporary manual state, reduced motion, mobile drawer, role scope, focus/Escape/backdrop/resize/route guards, both themes and no service writes')
 } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
