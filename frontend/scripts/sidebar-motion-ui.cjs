// All account and service responses are isolated synthetic fixtures; no real writes.
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'

;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 const errors = [], writes = [], loginRequests = []
 try {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, serviceWorkers: 'block' })
  await context.addInitScript(() => {
   if (location.pathname !== '/admin/login') localStorage.setItem('admin_token', 'mock-sidebar-motion')
   localStorage.setItem('cinema115-theme', 'light')
   window.firstNavigationPaint = undefined
   new MutationObserver(() => {
    const aside = document.querySelector('.management-sidebar'), nav = document.querySelector('#management-desktop-navigation')
    if (!aside || !nav || window.firstNavigationPaint) return
    window.firstNavigationPaint = {
     width: aside.getBoundingClientRect().width,
     collapsed: nav.classList.contains('is-collapsed'),
     opacity: getComputedStyle(nav).opacity,
     animations: [aside, nav, document.querySelector('.portal-workspace')].flatMap(el => el.getAnimations()).length,
    }
   }).observe(document, { childList: true, subtree: true })
  })
  await context.route('**/api/**', async route => {
   const request = route.request(), endpoint = new URL(request.url()).pathname
   if (request.method() !== 'GET') {
    if (endpoint === '/api/auth/admin/login') loginRequests.push(endpoint)
    else writes.push(endpoint)
   }
   if (endpoint.startsWith('/api/tmdb/image/')) return route.fulfill({ contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="1600" height="900"><path fill="#7586b8" d="M0 0h1600v900H0z"/></svg>' })
   let data = { configured: false, connected: false, items: [], total: 0, totalPages: 1, hasMore: false, accounts: [], jobs: [], pending: [], sessions: [], genres: [], bots: [], invites: [] }
   if (endpoint === '/api/auth/me') data = { user: { id: 'fixture', username: 'sidebar-motion-fixture', role: 'admin' } }
   else if (endpoint === '/api/auth/admin/login') data = { token: 'mock-sidebar-motion-login' }
   else if (endpoint === '/api/admin/media-organize') data = { rules: [], active: false, activeRules: [] }
   else if (/\/tmdb\/detail\/movie\/\d+$/.test(endpoint)) data = { id: 1452, kind: 'movie', title: '侧栏动画测试', originalTitle: 'Sidebar Motion Fixture', overview: '', backdropUrl: '/api/tmdb/image/w1280/sidebar-motion.svg', date: '2026-01-01', rating: 7, votes: 1, status: 'Released', runtime: 90, countries: [], genres: [], cast: [], seasons: [], collection: null }
   await route.fulfill({ json: data })
  })
  const page = await context.newPage()
  page.setDefaultTimeout(15000)
  page.on('pageerror', error => errors.push(error.message))
  const sidebar = () => page.locator('.management-sidebar')
  const navigation = () => page.locator('#management-desktop-navigation')
  const homeLogo = () => sidebar().locator('.management-sidebar-home')
  const push = to => page.evaluate(async to => {
   await document.querySelector('#app').__vue_app__.config.globalProperties.$router.push(to)
  }, to)
  const settle = collapsed => page.waitForFunction(collapsed => {
   const aside = document.querySelector('.management-sidebar'), nav = document.querySelector('#management-desktop-navigation')
   return aside && nav && Math.abs(aside.getBoundingClientRect().width - (collapsed ? 64 : 240)) < .1
    && nav.classList.contains('is-collapsed') === collapsed
    && !nav.classList.contains('is-transitioning')
    && Number(getComputedStyle(nav).opacity) === 1
    && !nav.getAnimations({ subtree: true }).some(animation => animation.playState === 'running' && Number.isFinite(animation.effect.getComputedTiming().endTime))
  }, collapsed)
  const unique = async () => {
   assert.equal(await sidebar().count(), 1)
   assert.equal(await page.getByRole('navigation', { name: '功能导航', exact: true }).count(), 1)
   assert.equal(await page.locator('[id="management-desktop-navigation"]').count(), 1)
  }
  const rememberDOM = () => page.evaluate(() => {
   window.sidebarIdentity = {
    aside: document.querySelector('.management-sidebar'), nav: document.querySelector('#management-desktop-navigation'),
    links: [...document.querySelectorAll('#management-desktop-navigation a')],
   }
  })
  const sameDOM = async () => {
   assert.equal(await page.evaluate(() => {
    const before = window.sidebarIdentity
    return before.aside === document.querySelector('.management-sidebar')
     && before.nav === document.querySelector('#management-desktop-navigation')
     && before.links.every((link, index) => link === document.querySelectorAll('#management-desktop-navigation a')[index])
   }), true, 'Cross-page navigation must preserve the aside, navigation and all native links')
   await unique()
  }
  const startSampling = () => page.evaluate(() => {
   const samples = []
   window.sidebarSamples = samples
   window.sampleSidebar = true
   const sample = () => {
    const aside = document.querySelector('.management-sidebar'), nav = document.querySelector('#management-desktop-navigation')
    const header = document.querySelector('.workbench-header'), shell = document.querySelector('.portal-workspace')
    const backdrop = document.querySelector('.detail-backdrop')?.getBoundingClientRect()
    if (aside && nav && header && shell) samples.push({
     width: aside.getBoundingClientRect().width, header: header.getBoundingClientRect().x,
     padding: parseFloat(getComputedStyle(shell).paddingLeft), collapsed: nav.classList.contains('is-collapsed'),
     opacity: Number(getComputedStyle(nav).opacity), navCount: document.querySelectorAll('#management-desktop-navigation').length,
     labelOpacity: Number(getComputedStyle(nav.querySelector('.management-nav-link > span')).opacity),
     headingHeight: nav.querySelector('.management-nav-label').getBoundingClientRect().height,
     iconOpacity: Number(getComputedStyle(nav.querySelector('.management-nav-link > svg')).opacity),
     backdrop: backdrop && {
      // Stable scrollbar gutters reserve layout space even when clientWidth still includes it.
      x: backdrop.x, width: backdrop.width, viewport: document.documentElement.getBoundingClientRect().width,
     },
    })
    if (window.sampleSidebar) requestAnimationFrame(sample)
   }
   sample()
  })
  const stopSampling = () => page.evaluate(() => { window.sampleSidebar = false; return window.sidebarSamples })
  const synchronized = samples => {
   const middle = samples.filter(sample => sample.width > 64.5 && sample.width < 239.5)
   assert.ok(middle.length >= 2, 'A width transition must expose intermediate frames')
   for (const sample of samples) {
    assert.ok(Math.abs(sample.header - sample.width) < 1, JSON.stringify(sample))
    assert.ok(Math.abs(sample.padding - sample.width) < 1, JSON.stringify(sample))
    assert.equal(sample.navCount, 1)
    if (sample.backdrop) {
     assert.ok(Math.abs(sample.backdrop.x) < 1, JSON.stringify(sample))
     assert.ok(Math.abs(sample.backdrop.width - sample.backdrop.viewport) < 1, JSON.stringify(sample))
    }
   }
  }
  await page.goto(origin + '/movie', { waitUntil: 'networkidle' })
  await settle(true); await unique()
  assert.deepEqual(await page.evaluate(() => window.firstNavigationPaint), { width: 64, collapsed: true, opacity: '1', animations: 0 }, 'First paint must directly use the route default without a startup transition')
  await rememberDOM()
  // Warm the destination components while checking the global navigation survives each layout.
  for (const [to, collapsed] of [['/resources', false], ['/115', false], ['/media/organize', false], ['/movie/1452', true], ['/movie', true]]) {
   await push(to); await settle(collapsed); await sameDOM()
  }
  // A native link navigates while expansion is running; route resolution never waits for motion.
  await startSampling()
  const routeMotion = await page.evaluate(async () => {
   const router = document.querySelector('#app').__vue_app__.config.globalProperties.$router
   const completed = new Promise(resolve => {
    const remove = router.afterEach(to => {
     if (to.path !== '/115') return
     remove()
     resolve({ width: document.querySelector('.management-sidebar').getBoundingClientRect().width })
    })
   })
   document.querySelector('#management-desktop-navigation a[href="/115"]').click()
   return completed
  })
  await page.waitForURL('**/115')
  assert.ok(routeMotion.width < 239.5, 'Navigation must commit before sidebar expansion finishes')
  await settle(false); await sameDOM()
  const routeSamples = await stopSampling()
  synchronized(routeSamples)
  assert.ok(routeSamples.every(sample => Math.abs(sample.opacity - 1) < .001 && Math.abs(sample.iconOpacity - 1) < .001), 'Navigation icons must remain visible throughout the continuous layout transition')
  assert.ok(routeSamples.some(sample => sample.headingHeight > .5 && sample.headingHeight < 15.5), 'Group headings must pass through intermediate heights')
  assert.ok(routeSamples.some(sample => sample.labelOpacity > .05 && sample.labelOpacity < .95), 'Link text must fade while the existing icons stay visible')
  assert.ok(routeSamples.some(sample => sample.collapsed) && routeSamples.some(sample => !sample.collapsed))
  const panel = await navigation().boundingBox(), rail = await sidebar().boundingBox()
  const headerHeight = await page.locator('.workbench-header .header-inner').evaluate(el => el.getBoundingClientRect().height)
  assert.ok(Math.abs(panel.x) < .5 && Math.abs(panel.y - headerHeight) < .5
   && Math.abs(panel.width - rail.width) <= 1 && Math.abs(panel.y + panel.height - 900) < .5, JSON.stringify({ panel, rail, headerHeight }))
  fs.mkdirSync('test-results', { recursive: true })
  await page.screenshot({ path: 'test-results/sidebar-expanded-panel-light.png' })
  await page.evaluate(() => document.documentElement.classList.add('dark')); await page.waitForTimeout(350)
  await page.screenshot({ path: 'test-results/sidebar-expanded-panel-dark.png' })
  // The column-aligned brand is a native home link with no desktop collapse control.
  const homePath = await homeLogo().getAttribute('href')
  assert.match(homePath, /^\/(?:movie|tv)(?:\?|$)/)
  const mark = await homeLogo().locator('.cinema-logo-mark').boundingBox()
  const wordmark = await homeLogo().locator('.cinema-logo-wordmark').boundingBox()
  const icon = await navigation().locator('.management-nav-link > svg').first().boundingBox()
  const label = await navigation().locator('.management-nav-link > span').first().boundingBox()
  assert.ok(Math.abs(mark.x + mark.width / 2 - icon.x - icon.width / 2) < 1)
  assert.ok(Math.abs(wordmark.x - label.x) < 2)
  assert.equal(await sidebar().locator('.management-brand-toggle, .management-collapse-control').count(), 0)
  await homeLogo().click(); await page.waitForURL(origin + homePath); await settle(true); await sameDOM()
  await homeLogo().click(); await settle(true)
  assert.equal(page.url(), origin + homePath, 'Clicking the home logo on the current home route must not toggle the sidebar')
  await push('/115'); await settle(false); await sameDOM()
  // The expanded navigation scrolls its own links on a short desktop, without moving the page.
  await page.setViewportSize({ width: 1440, height: 360 }); await settle(false)
  assert.equal(await navigation().evaluate(el => el.scrollHeight > el.clientHeight), true)
  const nativeLinks = navigation().getByRole('link')
  for (let index = 0; index < await nativeLinks.count(); index++) {
   await nativeLinks.nth(index).scrollIntoViewIfNeeded()
   const bounds = await nativeLinks.nth(index).boundingBox(), panel = await navigation().boundingBox()
   assert.ok(bounds.y >= panel.y - .5 && bounds.y + bounds.height <= panel.y + panel.height + .5)
  }
  assert.equal(await page.evaluate(() => scrollY), 0)
  assert.equal(await navigation().evaluate(el => el.scrollWidth <= el.clientWidth), true)
  const focusedLink = navigation().locator('a[href="/115"]')
  await focusedLink.focus()
  await push('/movie')
  await settle(true); await sameDOM()
  assert.equal(await focusedLink.evaluate(el => el === document.activeElement), true, 'Changing layout must preserve focus on the native link')
  await push('/115')
  await settle(false)
  assert.equal(await focusedLink.evaluate(el => el === document.activeElement), true)
  await page.setViewportSize({ width: 1440, height: 900 }); await settle(false)
  // Switching into a detail layout keeps its full-viewport backdrop aligned on every frame.
  await startSampling(); await push('/movie/1452'); await settle(true); await sameDOM()
  const detailSamples = await stopSampling()
  synchronized(detailSamples)
  assert.ok(detailSamples.some(sample => sample.backdrop), 'Detail frames must include the viewport-wide background')
  await push('/115'); await settle(false)
  // Repeated route reversals retain the newest target and focus through the continuous animation.
  await homeLogo().focus()
  await page.evaluate(async () => {
   const router = document.querySelector('#app').__vue_app__.config.globalProperties.$router
   await router.push('/movie')
   await new Promise(resolve => setTimeout(resolve, 35))
   await router.push('/115')
   await new Promise(resolve => setTimeout(resolve, 110))
   await router.push('/tv')
  })
  await settle(true); await sameDOM()
  assert.equal(await homeLogo().evaluate(el => el === document.activeElement), true)
  await page.waitForTimeout(320); await settle(true)
  // Resize and hidden-document cleanup finish both outer geometry and local navigation.
  await push('/115'); await page.setViewportSize({ width: 1280, height: 800 }); await settle(false)
  await push('/movie')
  await page.evaluate(() => {
   Object.defineProperty(document, 'hidden', { configurable: true, get: () => true })
   document.dispatchEvent(new Event('visibilitychange'))
  })
  await settle(true)
  await page.evaluate(() => { delete document.hidden; document.dispatchEvent(new Event('visibilitychange')) })
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await navigation().locator('a[href="/resources"]').click(); await page.waitForURL('**/resources'); await settle(false)
  assert.equal(await navigation().evaluate(el => el.getAnimations().length), 0)
  assert.equal(await sidebar().locator('.management-brand-toggle, .management-collapse-control').count(), 0)
  await sameDOM()
  // A drawer opened on mobile returns focus into the one global desktop navigation on resize.
  await page.setViewportSize({ width: 390, height: 844 })
  assert.equal(await sidebar().count(), 0)
  await page.getByRole('button', { name: '打开功能导航', exact: true }).click()
  const drawer = page.getByRole('dialog', { name: '功能导航', exact: true })
  await drawer.waitFor()
  await page.setViewportSize({ width: 1440, height: 900 }); await settle(false)
  await drawer.waitFor({ state: 'detached' }); await unique()
  assert.equal(await navigation().locator('a[href="/resources"]').evaluate(el => el === document.activeElement), true)
  assert.equal(await page.evaluate(() => document.body.style.overflow), '')
  // Entering the authenticated workspace from the public login page paints its route default directly.
  await page.evaluate(() => localStorage.removeItem('admin_token'))
  await page.goto(origin + '/admin/login?redirect=%2F115', { waitUntil: 'networkidle' })
  assert.equal(await sidebar().count(), 0)
  await page.getByLabel('管理员账号', { exact: true }).fill('fixture-admin')
  await page.getByLabel('密码', { exact: true }).fill('fixture-password-only')
  await page.getByRole('button', { name: '登录管理后台', exact: true }).click()
  await page.waitForURL('**/115'); await settle(false)
  assert.deepEqual(await page.evaluate(() => window.firstNavigationPaint), { width: 240, collapsed: false, opacity: '1', animations: 0 })
  assert.deepEqual(loginRequests, ['/api/auth/admin/login'])
  assert.deepEqual(writes, []); assert.deepEqual(errors, [])
  console.log('Sidebar motion passed: persistent unique DOM, static first paint, full-height sidebar, column-aligned home logo without desktop collapse control, immediate native navigation, synchronized frame geometry, continuous icon visibility and heading/text transitions, rapid route reversals, focus, resize/visibility/reduced-motion cleanup, mobile focus restoration and both themes; no service writes')
 } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
