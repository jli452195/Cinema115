// All API responses are isolated synthetic fixtures; no real account or settings writes.
const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'

;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 const errors = [], writes = []
 let role = 'admin'
 const setup = async context => {
  await context.addInitScript(() => {
   localStorage.setItem('admin_token', 'mock-sidebar-dock')
   localStorage.setItem('cinema115-theme', 'light')
   window.dockRafExecuted = 0
   const request = window.requestAnimationFrame.bind(window)
   window.requestAnimationFrame = callback => request(time => { window.dockRafExecuted++; callback(time) })
  })
  await context.route('**/api/**', async route => {
   const request = route.request(), endpoint = new URL(request.url()).pathname
   if (request.method() !== 'GET') writes.push(endpoint)
   let data = { configured: false, connected: false, items: [], total: 0, totalPages: 1, hasMore: false, accounts: [], jobs: [], pending: [], sessions: [], genres: [], bots: [], invites: [] }
   if (endpoint === '/api/auth/me') data = { user: { id: 'fixture', username: 'sidebar-dock-fixture', role } }
   await route.fulfill({ json: data })
  })
 }
 try {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, serviceWorkers: 'block' })
  await setup(context)
  const page = await context.newPage()
  page.setDefaultTimeout(15000)
  page.on('pageerror', error => errors.push(error.message))
  const sidebar = () => page.getByRole('complementary', { name: '功能侧栏', exact: true })
  const nav = () => sidebar().getByRole('navigation', { name: '功能导航', exact: true })
  const links = () => nav().getByRole('link')
  const toggle = () => sidebar().locator('.management-brand-toggle')
  const boxes = () => links().evaluateAll(elements => elements.map(element => {
   const b = element.getBoundingClientRect()
   return { x: b.x, y: b.y, width: b.width, height: b.height }
  }))
  const close = (actual, expected, tolerance = .5) => assert.ok(Math.abs(actual - expected) < tolerance, `${actual} differs from ${expected}`)
  const baseSizes = async () => {
   await page.waitForFunction(() => [...document.querySelectorAll('.management-navigation.is-collapsed .management-nav-link')]
    .length > 0 && [...document.querySelectorAll('.management-navigation.is-collapsed .management-nav-link')]
    .every(element => Math.abs(element.getBoundingClientRect().height - 36) < .1))
  }
  const resting = async () => { await page.mouse.move(500, 60); await baseSizes() }
  const stationaryLayout = () => page.evaluate(() => {
   const bounds = element => {
    const b = element.getBoundingClientRect()
    return { x: b.x, y: b.y, width: b.width, height: b.height }
   }
   return { content: bounds(document.querySelector('main')), header: bounds(document.querySelector('.workbench-header')), padding: getComputedStyle(document.querySelector('.portal-workspace')).paddingLeft }
  })
  const noOverflow = async () => assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
  const rafStops = async () => {
   await page.waitForTimeout(150)
   const before = await page.evaluate(() => window.dockRafExecuted)
   await page.waitForTimeout(180)
   assert.equal(await page.evaluate(() => window.dockRafExecuted), before, 'Dock must stop scheduling frames when its dimensions settle')
  }
  const screenshot = async name => {
   fs.mkdirSync('test-results', { recursive: true })
   await page.screenshot({ path: `test-results/sidebar-dock-${name}.png` })
  }
  await page.goto(origin + '/115', { waitUntil: 'networkidle' })
  await toggle().click(); await resting()
  await page.waitForFunction(() => Math.abs(document.querySelector('.management-sidebar').getBoundingClientRect().width - 64) < .1)
  assert.equal(await links().count(), 10)
  const baseline = await boxes(), capsule = await nav().boundingBox()
  close(capsule.x, 8)
  assert.ok(capsule.height < 450, 'Folded navigation should be a compact capsule')
  const headerHeight = await page.locator('.workbench-header').evaluate(el => el.getBoundingClientRect().height)
  close(capsule.y + capsule.height / 2, headerHeight + (900 - headerHeight) / 2, 1)
  for (let i = 0; i < baseline.length; i++) {
   close(baseline[i].height, 36); close(baseline[i].width, 36)
   if (i) close(baseline[i].y - baseline[i - 1].y - baseline[i - 1].height, 6)
  }
  assert.equal(await nav().locator('a > span:visible').count(), 0)
  const layout = await stationaryLayout(), hovered = 4
  const label = await links().nth(hovered).getAttribute('aria-label')
  for (const dark of [false, true]) {
   await page.evaluate(value => document.documentElement.classList.toggle('dark', value), dark)
   await page.waitForTimeout(350) // Capture the settled theme, including the body's 300ms surface transition.
   await resting(); await screenshot(`1440-${dark ? 'dark' : 'light'}-rest`)
   const before = await boxes(), target = before[hovered]
   await page.mouse.move(target.x + target.width / 2, target.y + target.height / 2)
   await page.waitForFunction(index => document.querySelectorAll('.management-nav-link')[index].getBoundingClientRect().height > 52, hovered)
   await page.waitForTimeout(650)
   const after = await boxes()
   assert.ok(after[hovered].height <= 56.1)
   assert.ok(after[hovered - 1].height > 38 && after[hovered + 1].height > 38, 'Nearby controls should participate in magnification')
   assert.ok(after[hovered - 1].y < before[hovered - 1].y - 1)
   assert.ok(after[hovered + 1].y > before[hovered + 1].y + 1, 'Real dimensions must push the next control down')
   for (const box of after) close(box.x, before[0].x)
   for (let i = 1; i < after.length; i++) assert.ok(after[i].y >= after[i - 1].y + after[i - 1].height + 5.5, 'Controls must never overlap')
   assert.deepEqual(await stationaryLayout(), layout, 'Magnification must not move the header or page content')
   const tooltip = page.getByRole('tooltip', { name: label, exact: true })
   await tooltip.waitFor()
   const hint = await tooltip.boundingBox(), grownCapsule = await nav().boundingBox()
   assert.ok(hint.x >= Math.max(after[hovered].x + after[hovered].width, grownCapsule.x + grownCapsule.width) + 5)
   close(hint.y + hint.height / 2, after[hovered].y + after[hovered].height / 2, 1)
   await screenshot(`1440-${dark ? 'dark' : 'light'}-hover`)
   await rafStops()
   const still = await boxes()
   for (let i = 0; i < still.length; i++) { close(still[i].y, after[i].y, .15); close(still[i].height, after[i].height, .15) }
   await resting(); await tooltip.waitFor({ state: 'detached' }); await rafStops()
   assert.deepEqual(await stationaryLayout(), layout)
   await noOverflow()
  }
  // Click the enlarged area outside the resting hitbox: mouse focus must not shrink it on mousedown.
  const clickRest = (await boxes())[4]
  await page.mouse.move(clickRest.x + clickRest.width / 2, clickRest.y + clickRest.height / 2)
  await page.waitForFunction(() => document.querySelectorAll('.management-nav-link')[4].getBoundingClientRect().width > 52)
  await page.waitForTimeout(400)
  const clickGrown = (await boxes())[4]
  await page.mouse.click(clickGrown.x + clickGrown.width - 3, clickGrown.y + clickGrown.height / 2)
  await page.waitForURL('**/users')
  assert.equal(await toggle().getAttribute('aria-expanded'), 'true')
  await page.goto(origin + '/movie', { waitUntil: 'networkidle' }); await resting()
  // Keyboard focus shows names without magnification, and native Enter navigation remains available.
  await links().first().focus(); await page.keyboard.press('Tab')
  assert.equal(await links().nth(1).evaluate(el => el === document.activeElement), true)
  await page.getByRole('tooltip', { name: await links().nth(1).getAttribute('aria-label'), exact: true }).waitFor()
  for (const box of await boxes()) close(box.height, 36)
  await page.keyboard.press('Enter'); await page.waitForURL('**/follows/manage')
  assert.equal(await toggle().getAttribute('aria-expanded'), 'true')
  assert.equal(await nav().locator('a > span:visible').count(), 10)
  // Route changes and window blur clear an active effect and its tooltip.
  await page.goto(origin + '/movie', { waitUntil: 'networkidle' }); await resting()
  const hoverAgain = async () => {
   const box = (await boxes())[4]
   await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
   await page.waitForFunction(() => [...document.querySelectorAll('.management-nav-link')].some(el => el.getBoundingClientRect().height > 45))
  }
  await hoverAgain()
  await page.evaluate(() => window.dispatchEvent(new Event('blur')))
  await baseSizes(); await page.getByRole('tooltip').waitFor({ state: 'detached' })
  await hoverAgain()
  await page.evaluate(async () => { await document.querySelector('#app').__vue_app__.config.globalProperties.$router.push('/tv') })
  await page.waitForURL('**/tv'); await baseSizes()
  await page.getByRole('tooltip').waitFor({ state: 'detached' })
  // Scroll events from another overflow panel do not bubble; the capture listener must still reset the Dock.
  await hoverAgain()
  assert.equal(await page.evaluate(() => {
   const scroller = document.createElement('div')
   scroller.id = 'dock-scroll-fixture'
   scroller.style.cssText = 'position:fixed;right:24px;top:100px;width:60px;height:40px;overflow:auto'
   const content = document.createElement('div')
   content.style.height = '300px'
   scroller.append(content); document.body.append(scroller)
   scroller.scrollTop = 100
   return scroller.scrollTop
  }), 100)
  await baseSizes(); await page.getByRole('tooltip').waitFor({ state: 'detached' })
  await page.evaluate(() => document.querySelector('#dock-scroll-fixture').remove())
  await rafStops()
  // Short desktops retain all links through scrolling and use static buttons.
  await hoverAgain(); await page.setViewportSize({ width: 1024, height: 360 }); await resting()
  const shortCapsule = await nav().boundingBox()
  assert.ok(shortCapsule.y >= headerHeight + 4 && shortCapsule.y + shortCapsule.height <= 356)
  assert.equal(await nav().evaluate(el => el.scrollWidth <= el.clientWidth), true)
  for (let i = 0; i < await links().count(); i++) {
   await links().nth(i).scrollIntoViewIfNeeded()
   const box = await links().nth(i).boundingBox(), viewport = await nav().boundingBox()
   assert.ok(box.y >= viewport.y - .5 && box.y + box.height <= viewport.y + viewport.height + .5)
  }
  const last = await links().last().boundingBox()
  await page.mouse.move(last.x + last.width / 2, last.y + last.height / 2)
  await page.waitForTimeout(250)
  for (const box of await boxes()) close(box.height, 36)
  close(await page.evaluate(() => scrollY), 0)
  await screenshot('1024-short-scroll')
  // When the resting capsule fits but full magnification would not, all controls remain visible.
  await page.setViewportSize({ width: 1440, height: 540 }); await resting()
  const mediumTarget = (await boxes())[4]
  await page.mouse.move(mediumTarget.x + 18, mediumTarget.y + 18); await page.waitForTimeout(650)
  const mediumCapsule = await nav().boundingBox(), mediumLinks = await boxes()
  assert.ok(mediumCapsule.y >= headerHeight + 4 && mediumCapsule.y + mediumCapsule.height <= 536)
  assert.equal(await nav().evaluate(el => el.scrollHeight <= el.clientHeight + 1), true)
  assert.ok(mediumLinks[0].y >= mediumCapsule.y && mediumLinks.at(-1).y + mediumLinks.at(-1).height <= mediumCapsule.y + mediumCapsule.height)
  await rafStops()
  // Restoring a tall viewport and enabling reduced motion must leave every button static.
  await page.setViewportSize({ width: 1440, height: 900 }); await resting()
  await page.emulateMedia({ reducedMotion: 'reduce' })
  const reducedTarget = (await boxes())[4]
  await page.mouse.move(reducedTarget.x + 18, reducedTarget.y + 18); await page.waitForTimeout(250)
  for (const box of await boxes()) close(box.height, 36)
  await rafStops()
  await toggle().click()
  assert.equal(await nav().locator('a > span:visible').count(), 10)
  await toggle().click(); await resting()
  // Role scope stays unchanged, and the mobile drawer keeps its ordinary layout.
  role = 'user'
  await page.goto(origin + '/movie', { waitUntil: 'networkidle' }); await resting()
  assert.deepEqual(await links().allTextContents(), ['我的播放', '我的追更'])
  assert.ok((await nav().boundingBox()).height < 110)
  await page.setViewportSize({ width: 390, height: 844 })
  assert.equal(await sidebar().count(), 0)
  await page.locator('.workbench-header .brand-link').click()
  const drawer = page.getByRole('dialog', { name: '功能导航', exact: true })
  await drawer.waitFor()
  assert.deepEqual(await drawer.getByRole('link').allTextContents(), ['我的播放', '我的追更'])
  assert.equal(await drawer.locator('.is-collapsed').count(), 0)
  await page.keyboard.press('Escape'); await drawer.waitFor({ state: 'detached' })
  assert.equal(await page.evaluate(() => document.body.style.overflow), '')
  // A desktop-sized touch device must not start pointer magnification.
  role = 'admin'
  const touchContext = await browser.newContext({ viewport: { width: 1440, height: 900 }, hasTouch: true, serviceWorkers: 'block' })
  await setup(touchContext)
  const touchPage = await touchContext.newPage()
  touchPage.on('pageerror', error => errors.push(error.message))
  await touchPage.goto(origin + '/movie', { waitUntil: 'networkidle' })
  assert.equal(await touchPage.evaluate(() => matchMedia('(hover: hover) and (pointer: fine)').matches), false)
  const touchLink = touchPage.locator('.management-nav-link').nth(4), touchBox = await touchLink.boundingBox()
  await touchPage.mouse.move(touchBox.x + 18, touchBox.y + 18); await touchPage.waitForTimeout(250)
  assert.equal(await touchPage.locator('.management-nav-link').evaluateAll(elements => elements.every(el => Math.abs(el.getBoundingClientRect().height - 36) < .1)), true)
  await noOverflow()
  assert.deepEqual(writes, []); assert.deepEqual(errors, [])
  console.log('Sidebar Dock passed: compact centered capsule, proximity magnification and physical spacing, stable geometry and idle frames, fixed content/header, tooltip, enlarged mouse hitbox and keyboard/native navigation, blur/route/resize/local-scroll cleanup, short viewport scrolling, reduced motion, touch, role scope, mobile drawer and both themes; no service writes')
 } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
