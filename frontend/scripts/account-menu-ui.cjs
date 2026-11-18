// 所有 API 请求均使用模拟数据，不读取或修改实际业务数据。
const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
  const requests = [], errors = [], passed = []
  let role = 'admin'
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, colorScheme: 'light', serviceWorkers: 'block' })
    await context.addInitScript(() => {
      if (!sessionStorage.getItem('account-menu-anonymous')) localStorage.setItem('admin_token', 'mock-account-menu-token')
      localStorage.setItem('cinema115-theme', 'light')
    })
    await context.route('**/api/**', async route => {
      const endpoint = new URL(route.request().url()).pathname
      requests.push(endpoint)
      let data = { configured: false, accounts: [], items: [], hasMore: false, total: 0, totalPages: 1, genres: [] }
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock', username: 'account-menu-test', role, disabled: false } }
      else if (endpoint.includes('/notifications')) data = { items: [], unread: 3 }
      else if (endpoint === '/api/admin/member-playback/users') data = { members: [], settings: { enabled: true, libraryIds: ['movies'], allowDownload: false } }
      else if (endpoint === '/api/admin/member-playback/settings') data = { settings: { enabled: true, libraryIds: ['movies'], allowDownload: false }, proxy: { listening: true } }
      else if (endpoint === '/api/admin/member-playback/libraries') data = { libraries: [{ ItemId: 'movies', Name: '电影', CollectionType: 'movies' }] }
      else if (endpoint === '/api/admin/invites') data = { invites: [], hasMore: false }
      else if (endpoint.endsWith('/tmdb/browse')) data = {
        items: Array.from({ length: 16 }, (_, index) => ({ id: index + 123, kind: 'movie', title: '测试海报 ' + (index + 1), overview: '', posterUrl: '', date: '2026-01-01', rating: 8.5, votes: 100 })),
        total: 16, totalPages: 1,
      }
      else if (endpoint.includes('/tmdb/detail/')) data = {
        id: 123, kind: 'movie', title: '示例电影', originalTitle: 'Example Film', overview: '账户菜单交互检查。', posterUrl: '', backdropUrl: '',
        date: '2016-07-01', rating: 8.5, votes: 100, tagline: '', status: 'Released', runtime: 106, episodeCount: 0,
        genres: [], countries: [], cast: [], seasons: [], collection: null, imdbId: '', imdbUrl: '', tmdbUrl: '',
      }
      else if (endpoint.includes('/emby/media/')) data = { media: { state: 'absent', kind: 'movie', tmdbId: '123', items: [], episodeCount: 0 }, baseUrl: '', serverId: '' }
      else if (endpoint.includes('recommendations')) data = { items: [], page: 1, totalPages: 1 }
      else if (endpoint.includes('/follows/')) data = { subscribed: false, request: null, libraryState: 'absent', items: [], hasMore: false }
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const page = await context.newPage()
    page.setDefaultTimeout(8000)
    page.on('pageerror', error => errors.push(error.message))
    const trigger = () => page.locator('button[aria-controls="header-management"]')
    const menu = () => page.getByRole('navigation', { name: '账户菜单', exact: true })
    const open = async () => {
      if (await trigger().getAttribute('aria-expanded') !== 'true') await trigger().click()
      await page.waitForFunction(() => {
        const element = document.querySelector('#header-management')
        return element && !element.hasAttribute('inert') && !element.dataset.menuMotion
      })
    }
    const close = async () => {
      await page.keyboard.press('Escape')
      await page.locator('#header-management').waitFor({ state: 'detached' })
      assert(await trigger().evaluate(el => document.activeElement === el))
    }
    const go = to => page.goto(origin + to, { waitUntil: 'networkidle' })
    const screenshot = name => page.screenshot({ path: path.join(__dirname, '../test-results', name + '.png') })
    const personalLinks = () => menu().locator('[aria-label="个人"] a')
    const adminDestinations = ['/follows/manage', '/anime', '/media/organize', '/users', '/logs', '/115', '/resources', '/telegram', '/emby']
    const personalMenu = async ordinary => {
      assert.deepEqual(await personalLinks().evaluateAll(links => links.map(link => link.getAttribute('href'))), ordinary ? ['/account/playback', '/follows'] : ['/follows'])
      for (const to of adminDestinations) assert.equal(await menu().locator(`a[href="${to}"]`).count(), 0)
      assert.equal(await menu().locator('.account-category, .account-flyout, .account-expand').count(), 0)
      assert.equal(await menu().getByRole('button', { name: '外观设置', exact: true }).count(), 0, '账户菜单不再提供外观设置入口')
      assert.equal(await menu().getByRole('button', { name: '退出登录', exact: true }).count(), 1)
    }

    await go('/movie/123'); await open()
    await personalMenu(false)
    assert.equal(await menu().getByText('account-menu-test', { exact: true }).count(), 1)
    assert.equal(await menu().getByText('管理员', { exact: true }).count(), 1)
    const requestStart = requests.length
    await menu().getByRole('link', { name: '我的追更', exact: true }).hover()
    assert(requests.slice(requestStart).every(endpoint => endpoint === '/api/me/notifications/unread'))
    await screenshot('account-personal-desktop-light')
    await close()
    // Personal links remain native keyboard links; Escape, outside click and focus leaving close the panel.
    await trigger().focus(); await page.keyboard.press('Enter'); await menu().waitFor()
    await page.keyboard.press('Tab')
    assert(await menu().getByRole('link', { name: '我的追更', exact: true }).evaluate(el => el === document.activeElement))
    await page.keyboard.press('Enter'); await page.waitForURL('**/follows')
    await page.locator('#header-management').waitFor({ state: 'detached' })
    await open()
    assert.equal(await menu().getByRole('link', { name: '我的追更', exact: true }).getAttribute('aria-current'), 'page')
    await page.locator('.header-inner').click({ position: { x: 1, y: 1 } })
    await page.locator('#header-management').waitFor({ state: 'detached' })
    await open()
    await page.getByRole('link', { name: '电影', exact: true }).focus()
    await page.locator('#header-management').waitFor({ state: 'detached' })
    passed.push('personal-menu-only-with-keyboard-navigation-escape-outside-and-focus-close')

    // 管理入口按路径核验，追更入口可以携带默认筛选参数。
    await go('/movie/123'); await open()
    const desktopSidebar = page.getByRole('complementary', { name: '功能侧栏', exact: true })
    const drawer = page.getByRole('dialog', { name: '功能导航', exact: true })
    const sidebarPaths = await desktopSidebar.locator('a').evaluateAll(links => links.map(link => new URL(link.href).pathname))
    for (const to of adminDestinations) assert.equal(sidebarPaths.filter(path => path === to).length, 1)
    await desktopSidebar.getByRole('link', { name: '用户与邀请', exact: true }).click()
    await page.waitForURL('**/users'); await drawer.waitFor({ state: 'detached' })
    await page.locator('#header-management').waitFor({ state: 'detached' })
    assert.deepEqual(await page.locator('.navigation-card').evaluateAll(cards => cards.map(card => card.getAttribute('href'))), ['/users/list', '/users/invites'])
    assert.equal(await trigger().evaluate(el => el.classList.contains('selected')), false)
    await open(); await personalMenu(false); await close()
    await page.locator('.navigation-card[href="/users/list"]').click()
    await page.waitForURL('**/users/list')
    assert.equal(await page.getByRole('navigation', { name: '上级导航' }).getByRole('link', { name: '返回 用户与邀请', exact: true }).getAttribute('href'), '/users')
    await page.getByRole('complementary', { name: '功能侧栏', exact: true }).getByRole('link', { name: '用户与邀请', exact: true }).click()
    await page.waitForURL('**/users')
    await page.locator('.navigation-card[href="/users/invites"]').click()
    await page.waitForURL('**/users/invites')
    assert.equal(await page.locator('.management-heading').getByRole('link', { name: '用户管理', exact: true }).getAttribute('href'), '/users/list')
    await go('/emby/users?from=bookmark#users')
    assert.equal(new URL(page.url()).pathname, '/users/list')
    assert.equal(new URL(page.url()).search, '?from=bookmark')
    assert.equal(new URL(page.url()).hash, '#users')
    await go('/emby')
    assert.deepEqual(await page.locator('.navigation-card').evaluateAll(cards => cards.map(card => card.getAttribute('href'))), [
      '/emby/library', '/emby/refresh', '/emby/deep-delete', '/emby/missing', '/emby/proxy', '/emby/cache',
    ])
    assert.equal(await page.locator('.navigation-card[href="/users/list"]').count(), 0)
    await go('/follows/manage'); await open()
    assert.equal(await menu().getByRole('link', { name: '我的追更', exact: true }).getAttribute('aria-current'), null)
    await close()
    passed.push('management-links-in-left-sidebar-category-cards-breadcrumbs-and-bookmark-redirect')

    // Responsive menus retain notifications/theme controls on mobile and both themes fit the viewport.
    await go('/movie/123')
    for (const width of [1440, 640, 390, 320]) {
      await page.setViewportSize({ width, height: width === 640 ? 360 : 844 })
      for (const dark of [false, true]) {
        await page.evaluate(async dark => {
          const code = await (await fetch('/src/components/ThemeSwitcher.vue')).text()
          const moduleURL = code.match(/["']([^"']*\/composables\/useTheme\.ts[^"']*)["']/)[1]
          const { useTheme } = await import(moduleURL)
          await useTheme().setTheme(dark ? 'dark' : 'light')
        }, dark)
        await open(); await personalMenu(false)
        const bounds = await menu().locator('.account-surface').boundingBox()
        assert(bounds.x >= 0 && bounds.x + bounds.width <= width)
        assert(bounds.y >= 0 && bounds.y + bounds.height <= page.viewportSize().height)
        assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
        assert.equal(await menu().getByRole('link', { name: /站内通知/ }).count(), width < 640 ? 1 : 0)
        if (width < 640) {
          const button = await trigger().boundingBox()
          const inner = await page.locator('.header-inner').boundingBox()
          assert(Math.abs(bounds.y - Math.max(inner.y + 12, button.y - 8)) <= 1 && Math.abs(bounds.x + bounds.width - button.x - button.width) <= 1, '手机菜单保留安全区内 12px 顶距并对齐胶囊右边')
          assert.equal(await trigger().evaluate(el => document.querySelector('#header-management').contains(document.elementFromPoint(el.getBoundingClientRect().x + el.offsetWidth / 2, el.getBoundingClientRect().y + el.offsetHeight / 2))), true)
          assert.match(await menu().locator('.account-surface').evaluate(el => getComputedStyle(el).backdropFilter), /blur\(/, '菜单复用玻璃材质')
          assert.equal(await menu().locator('.account-content').evaluate(el => getComputedStyle(el).backdropFilter), 'none', '菜单内容不重复采样玻璃')
        }
        if (width === 1440 || width === 390) await screenshot(`account-personal-${width}-${dark ? 'dark' : 'light'}`)
        await close()
      }
    }
    // 确认手机面板按右上角展开，动画只写入透明度和变换属性。
    const expansion = await page.evaluate(async () => {
      document.querySelector('[aria-controls="header-management"]').click()
      await new Promise(requestAnimationFrame)
      const menu = document.querySelector('#header-management'), panel = menu.querySelector('[data-menu-panel]')
      window.accountMotionSamples = menu.getAnimations({ subtree: true })
      const [animation] = accountMotionSamples
      animation.pause()
      const duration = animation.effect.getTiming().duration
      const read = time => {
        animation.currentTime = time
        const { x, y, width, height } = panel.getBoundingClientRect()
        return { x, y, width, height, layoutWidth: panel.offsetWidth, layoutHeight: panel.offsetHeight, opacity: Number(getComputedStyle(panel).opacity) }
      }
      const frames = animation.effect.getKeyframes()
      return { duration, count: accountMotionSamples.length, properties: Object.keys(frames[0]), start: read(0), middle: read(80), end: read(duration) }
    })
    assert.equal(expansion.count, 1)
    assert(expansion.duration <= 310)
    assert.deepEqual(expansion.properties.filter(key => !['offset', 'computedOffset', 'easing', 'composite'].includes(key)).sort(), ['opacity', 'transform'])
    assert.equal(expansion.start.opacity, 0)
    assert(expansion.middle.opacity > .75 && expansion.end.opacity === 1, '文字在动画前段就已可读')
    assert(expansion.start.width < expansion.end.width && expansion.start.y < expansion.end.y, '从右上角平移并放大')
    assert(Math.abs(expansion.start.width / expansion.end.width - expansion.start.height / expansion.end.height) < .005, '面板等比缩放')
    assert.equal(expansion.start.layoutWidth, expansion.end.layoutWidth)
    assert.equal(expansion.start.layoutHeight, expansion.end.layoutHeight)
    await page.evaluate(() => { for (const animation of accountMotionSamples) { try { animation.finish() } catch {} } })
    await open(); await close()
    // 退场中重新打开，旧节点的完成事件不能使新菜单变为 inert 或遗留旧 Promise。
    await trigger().click()
    await page.evaluate(async () => {
      const trigger = document.querySelector('button[aria-controls="header-management"]')
      trigger.click(); await Promise.resolve(); trigger.click()
    })
    await open()
    assert.equal(await menu().getAttribute('inert'), null)
    await close()
    await open()
    const reversal = await page.evaluate(async () => {
      const trigger = document.querySelector('button[aria-controls="header-management"]')
      const frame = () => new Promise(resolve => requestAnimationFrame(resolve))
      const read = menu => {
        const box = menu.querySelector('[data-menu-panel]').getBoundingClientRect()
        return { x: box.x, y: box.y, width: box.width, height: box.height, opacity: Number(getComputedStyle(menu.querySelector('[data-menu-panel]')).opacity) }
      }
      trigger.click(); await frame()
      const leaving = document.querySelector('#header-management')
      const oldAnimations = leaving.getAnimations({ subtree: true })
      for (const animation of oldAnimations) { animation.pause(); animation.currentTime = 100 }
      const before = read(leaving)
      trigger.click(); await frame()
      const entering = document.querySelector('#header-management')
      for (const animation of entering.getAnimations({ subtree: true })) { animation.pause(); animation.currentTime = 0 }
      const after = read(entering)
      const oldStopped = oldAnimations.every(animation => animation.playState === 'idle')
      for (const animation of entering.getAnimations({ subtree: true })) animation.finish()
      return { before, after, oldStopped }
    })
    for (const key of ['x', 'y', 'width', 'height', 'opacity']) assert(Math.abs(reversal.before[key] - reversal.after[key]) < .5, '反向打开承接之前的视觉状态')
    assert(reversal.oldStopped, '旧节点的动画应立即释放')
    await open(); await close()
    await trigger().focus(); await page.keyboard.press('Enter'); await open()
    assert(await menu().getByRole('link', { name: /站内通知/ }).evaluate(el => el === document.activeElement), '手机键盘打开后焦点落在可见菜单项')
    await close()
    await open()
    const contraction = await page.evaluate(async () => {
      document.querySelector('[aria-controls="header-management"]').click()
      await new Promise(requestAnimationFrame)
      const menu = document.querySelector('#header-management')
      const [animation] = menu.getAnimations({ subtree: true })
      const duration = animation.effect.getTiming().duration
      animation.pause(); animation.currentTime = duration
      const opacity = getComputedStyle(menu.querySelector('[data-menu-panel]')).opacity
      animation.finish()
      return { duration, opacity }
    })
    assert(contraction.duration <= 170 && contraction.opacity === '0', '收起快速淡出')
    await page.locator('#header-management').waitFor({ state: 'detached' })
    await open()
    const currentURL = page.url(), back = await page.locator('.detail-nav-back').boundingBox()
    await page.mouse.click(back.x + back.width / 2, back.y + back.height / 2)
    await page.locator('#header-management').waitFor({ state: 'detached' })
    assert.equal(page.url(), currentURL, '打开菜单时外部点击只关闭菜单，不触发下方返回入口')
    await trigger().click()
    await page.waitForFunction(() => document.querySelector('#header-management')?.dataset.menuMotion === 'entering')
    const source = await trigger().boundingBox()
    await page.mouse.click(source.x + source.width / 2, source.y + source.height / 2)
    await page.locator('#header-management').waitFor({ state: 'detached' })
    assert.equal(await trigger().getAttribute('aria-expanded'), 'false', '展开时再次点击来源位置关闭菜单')
    await open()
    await page.evaluate(async () => { document.querySelector('button[aria-controls="header-management"]').click(); await new Promise(resolve => requestAnimationFrame(resolve)); for (const animation of document.querySelector('#header-management').getAnimations({ subtree: true })) animation.pause() })
    await page.setViewportSize({ width: 390, height: 800 })
    await page.locator('#header-management').waitFor({ state: 'detached' })
    await open(); await page.setViewportSize({ width: 844, height: 390 })
    await page.locator('#header-management').waitFor({ state: 'detached' })
    await page.setViewportSize({ width: 390, height: 844 })
    passed.push('手机面板只动画透明度和变换，保留快速反向交接')
    passed.push('快速淡出、外部点击不穿透、二次点击关闭及退场 resize 清理')

    await page.setViewportSize({ width: 390, height: 320 })
    await page.evaluate(() => { document.documentElement.style.setProperty('--app-safe-top', '28px'); document.documentElement.style.setProperty('--app-safe-left', '28px'); document.documentElement.style.setProperty('--app-safe-right', '22px'); document.documentElement.style.setProperty('--app-safe-bottom', '24px') })
    await open()
    const shortBounds = await menu().locator('.account-surface').boundingBox(), safeTrigger = await trigger().boundingBox()
    assert(Math.abs(shortBounds.y - Math.max(40, safeTrigger.y - 8)) <= 1 && shortBounds.y >= 40, '菜单避开顶部安全区并保留 12px 顶距')
    assert(shortBounds.y + shortBounds.height <= 296 + 1, '短屏面板避开底部安全区')
    assert.equal(await menu().locator('.account-content').evaluate(el => el.scrollHeight > el.clientHeight), true)
    await screenshot('account-motion-short-safe-area')
    await close()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.evaluate(() => { for (const side of ['top', 'left', 'right', 'bottom']) document.documentElement.style.removeProperty(`--app-safe-${side}`) })
    await go('/notifications')
    const ordinaryTrigger = await trigger().boundingBox()
    assert.equal(ordinaryTrigger.width, 32, '普通手机页保留原按钮尺寸')
    await open()
    assert(Math.abs((await menu().boundingBox()).y - Math.max(12, ordinaryTrigger.y - 8)) <= 1, '32px 按钮同样保留 12px 顶距')
    await close(); await go('/movie/123')
    passed.push('短屏内部滚动、非零四侧安全区与普通手机页 32px 锚点')
    await open()
    // 主题切换前菜单应彻底移除，快照不能包含退场中的菜单。
    await page.evaluate(async () => {
      const code = await (await fetch('/src/components/ThemeSwitcher.vue')).text()
      const moduleURL = code.match(/["']([^"']*\/composables\/useTheme\.ts[^"']*)["']/)[1]
      const { useTheme } = await import(moduleURL)
      await useTheme().setTheme('light')
      const start = document.startViewTransition.bind(document)
      window.themeSnapshotMenus = []
      window.themeSnapshotOrigins = []
      document.startViewTransition = callback => {
        window.themeSnapshotMenus.push(!!document.querySelector('#header-management'))
        window.themeSnapshotOrigins.push(document.documentElement.style.getPropertyValue('--theme-reveal-origin'))
        return start(callback)
      }
    })
    const touchSession = await context.newCDPSession(page)
    for (const [index, activation] of ['mouse', 'touch', 'keyboard'].entries()) {
      // 第三次从跟随系统切到浅色，先设为系统深色，确保本次确实发生视觉转场。
      if (activation === 'keyboard') await page.emulateMedia({ colorScheme: 'dark' })
      await open()
      const themeButton = menu().locator('.theme-toggle')
      const bounds = await themeButton.boundingBox()
      const offset = activation === 'mouse' ? { x: 20, y: 12 } : activation === 'touch' ? { x: bounds.width - 18, y: bounds.height - 10 } : { x: bounds.width / 2, y: bounds.height / 2 }
      const expected = { x: bounds.x + offset.x, y: bounds.y + offset.y }
      if (activation === 'mouse') await themeButton.click({ position: offset })
      else if (activation === 'touch') {
        // 仅在手机尺寸的触摸用例中开启真实触摸，不改变前面桌面布局检查的输入环境。
        await touchSession.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 })
        await touchSession.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: expected.x, y: expected.y }] })
        await touchSession.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
      }
      else { await themeButton.focus(); await themeButton.press('Enter') }
      await page.locator('#header-management').waitFor({ state: 'detached' })
      await page.waitForFunction(count => themeSnapshotMenus.length === count, index + 1)
      assert.deepEqual(await page.evaluate(() => themeSnapshotMenus), Array(index + 1).fill(false))
      assert(await trigger().evaluate(el => document.activeElement === el))
      const [x, y] = (await page.evaluate(() => themeSnapshotOrigins.at(-1))).split(' ').map(parseFloat)
      assert(Math.abs(x - expected.x) < 1 && Math.abs(y - expected.y) < 1, `${activation} 应保留菜单关闭前的触发坐标或按钮中心`)
      await page.waitForFunction(() => !document.documentElement.classList.contains('theme-reveal'))
    }
    await touchSession.send('Emulation.setTouchEmulationEnabled', { enabled: false })
    await touchSession.detach()
    passed.push('theme-menu-mouse-touch-origins-and-keyboard-center-survive-menu-unmount')
    await open()
    await menu().getByRole('link', { name: /站内通知/ }).click()
    await page.waitForURL('**/notifications'); await page.locator('#header-management').waitFor({ state: 'detached' })
    await page.emulateMedia({ reducedMotion: 'reduce' }); await open()
    assert.equal(await menu().evaluate(el => el.getAnimations({ subtree: true }).length), 0)
    await close()
    passed.push('desktop-mobile-themes-short-screen-notifications-and-reduced-motion')

    await page.emulateMedia({ reducedMotion: 'no-preference' })
    await go('/movie')
    const posters = page.locator('.media-poster-card')
    await posters.first().waitFor()
    const touch = await context.newCDPSession(page)
    await touch.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 })
    const tap = async ({ x, y }) => {
      await touch.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] })
      await touch.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
    }
    const center = ({ x, y, width, height }) => ({ x: x + width / 2, y: y + height / 2 })
    const logo = center(await page.locator('.workspace-brand').boundingBox())
    await posters.first().evaluate((poster, y) => window.scrollBy({ top: poster.getBoundingClientRect().top - y + 12, behavior: 'instant' }), logo.y)
    const hasPosterBehind = point => page.evaluate(({ x, y }) => document.elementsFromPoint(x, y).some(el => el.closest('.media-poster-card')), point)
    const movieURL = page.url()
    assert(await hasPosterBehind(logo), '徽标下方应有一张可点击的海报')
    await tap(logo)
    assert.equal(page.url(), movieURL, '点击徽标不会打开下层海报')
    await open()
    const themePoint = center(await menu().locator('.theme-toggle').boundingBox())
    const logoutPoint = center(await menu().getByRole('button', { name: '退出登录', exact: true }).boundingBox())
    assert(await hasPosterBehind(themePoint), '主题按钮下方应有一张可点击的海报')
    await close()

    const pauseMotion = (phase, time) => page.evaluate(async ({ phase, time }) => {
      await new Promise(requestAnimationFrame)
      const menu = document.querySelector('#header-management')
      if (menu?.dataset.menuMotion !== phase) throw Error('账户菜单动画阶段不符：' + phase)
      window.accountMotionSamples = menu.getAnimations({ subtree: true })
      for (const animation of accountMotionSamples) { animation.pause(); animation.currentTime = time }
    }, { phase, time })
    const finishMotion = () => page.evaluate(() => { for (const animation of accountMotionSamples) animation.finish() })
    await trigger().evaluate(el => el.click())
    await pauseMotion('entering', 0)
    await tap(logoutPoint)
    await page.waitForFunction(() => !document.querySelector('#header-management')?.dataset.menuMotion)
    assert.equal(await trigger().getAttribute('aria-expanded'), 'true', '提前点到尚未显示的退出登录，只展开菜单')
    assert.equal(await page.evaluate(() => localStorage.getItem('admin_token')), 'mock-account-menu-token')
    await open()
    await page.keyboard.press('Escape')
    await pauseMotion('leaving', 75)
    assert(await page.evaluate(({ x, y }) => document.elementFromPoint(x, y)?.hasAttribute('data-menu-scrim'), themePoint), '退场中由遮罩接住原主题按钮位置的点击')
    await tap(themePoint)
    assert.equal(page.url(), movieURL, '收起动画时点按不会打开海报')
    await finishMotion()
    await page.locator('#header-management').waitFor({ state: 'detached' })

    await trigger().evaluate(el => el.click())
    await pauseMotion('entering', 0)
    assert.equal(await menu().locator('[data-menu-panel]').evaluate(el => getComputedStyle(el).opacity), '0', '验证主题按钮在尚未露出时的点按')
    await tap(themePoint)
    await page.waitForFunction(() => document.documentElement.classList.contains('theme-reveal'))
    await page.waitForFunction(() => localStorage.getItem('cinema115-theme') === 'dark', null, { timeout: 2000 })
    assert.equal(await trigger().getAttribute('aria-expanded'), 'false')
    const [themeX, themeY] = (await page.evaluate(() => document.documentElement.style.getPropertyValue('--theme-reveal-origin'))).split(' ').map(parseFloat)
    assert(Math.abs(themeX - themePoint.x) < 1 && Math.abs(themeY - themePoint.y) < 1, '提前点按仍用真实触点播放主题动效')
    await tap(themePoint)
    await page.waitForFunction(() => !document.documentElement.classList.contains('theme-reveal'))
    assert.equal(page.url(), movieURL, '主题转场中再次点按不会打开海报')
    assert.equal(await page.locator('#header-management').count(), 0)
    const posterHref = await page.evaluate(({ x, y }) => document.elementFromPoint(x, y)?.closest('.media-poster-card')?.href, themePoint)
    assert(posterHref, '转场结束后应恢复下层海报的点击')
    await tap(themePoint)
    await page.waitForURL(posterHref)
    await touch.send('Emulation.setTouchEmulationEnabled', { enabled: false })
    await touch.detach()
    await page.emulateMedia({ reducedMotion: 'reduce' })
    passed.push('手机首页徽标和菜单退场不穿透，展开早期一次点按即切换主题，转场结束后恢复海报点击')

    role = 'user'
    await go('/movie/123'); await open(); await personalMenu(true)
    assert.equal(await menu().getByText('普通用户', { exact: true }).count(), 1)
    assert.equal(await menu().locator('.account-row:visible').count(), 4)
    await close()
    // 手机详情回到首页后，订阅由底栏进入，账户菜单继续提供个人播放。
    await page.locator('.detail-nav-back').click(); await page.waitForURL('**/movie')
    const mobileNav = page.getByRole('navigation', { name: '手机主导航', exact: true })
    await mobileNav.getByRole('link', { name: '订阅', exact: true }).click(); await page.waitForURL('**/follows')
    assert.equal(await mobileNav.getByRole('link', { name: '订阅', exact: true }).getAttribute('aria-current'), 'page')
    await open()
    assert.deepEqual(await personalLinks().evaluateAll(links => links.map(link => link.getAttribute('href'))), ['/account/playback'])
    await close()
    await go('/tv')
    const ordinaryStart = requests.length
    for (const destination of ['/users', '/users/list', '/users/invites', '/emby/users']) {
      await go(destination)
      assert.equal(new URL(page.url()).pathname, '/tv')
      assert.equal(await page.getByRole('heading', { name: '用户与邀请', exact: true }).count(), 0)
    }
    assert(requests.slice(ordinaryStart).every(endpoint => !endpoint.startsWith('/api/admin/')))
    await open(); await menu().getByRole('button', { name: '退出登录', exact: true }).click()
    await page.waitForURL('**/login')
    assert.equal(await page.evaluate(() => localStorage.getItem('admin_token')), null)
    role = 'admin'; await go('/movie/123'); await open()
    await menu().getByRole('button', { name: '退出登录', exact: true }).click()
    await page.waitForURL('**/admin/login')
    assert.equal(await page.evaluate(() => localStorage.getItem('admin_token')), null)
    await page.evaluate(() => sessionStorage.setItem('account-menu-anonymous', 'true'))
    for (const destination of ['/users', '/users/list', '/users/invites']) {
      await go(destination)
      assert.equal(new URL(page.url()).pathname, '/admin/login')
      assert.equal(new URL(page.url()).searchParams.get('redirect'), destination)
    }
    assert.deepEqual(errors, [])
    passed.push('ordinary-user-role-scope-access-denied-and-both-role-logout-destinations')
    console.log(JSON.stringify({ passed, pageErrors: errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
