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
          assert.match(await menu().locator('.account-glass').evaluate(el => getComputedStyle(el).backdropFilter), /blur\(/, '菜单复用玻璃材质')
          assert.equal(await menu().locator('.account-surface').evaluate(el => getComputedStyle(el).backdropFilter), 'none', '父层不重复采样玻璃')
        }
        if (width === 1440 || width === 390) await screenshot(`account-personal-${width}-${dark ? 'dark' : 'light'}`)
        await close()
      }
    }
    // 按 TG 13.2.2 源码采样：整块胶囊起点、宽高分离弹簧、内容等比缩放与过冲。
    const expansion = await page.evaluate(async () => {
      const trigger = document.querySelector('button[aria-controls="header-management"]'), button = trigger.closest('.header-actions').getBoundingClientRect()
      trigger.click(); await Promise.resolve()
      await new Promise(resolve => requestAnimationFrame(resolve))
      const menu = document.querySelector('#header-management')
      window.accountMotionSamples = menu.getAnimations({ subtree: true })
      for (const animation of accountMotionSamples) { animation.pause(); animation.currentTime = 0 }
      const read = () => {
        const box = menu.querySelector('[data-menu-surface]').getBoundingClientRect()
        const filterBox = menu.querySelector('.account-glass').getBoundingClientRect()
        const icon = menu.querySelector('.account-icon').getBoundingClientRect()
        return { x: box.x, y: box.y, width: box.width, height: box.height, material: { x: filterBox.x, y: filterBox.y, width: filterBox.width, height: filterBox.height }, iconWidth: icon.width, iconHeight: icon.height, contentOpacity: Number(getComputedStyle(menu.querySelector('.account-content')).opacity), glassOpacity: Number(getComputedStyle(menu.querySelector('[data-menu-surface]')).opacity), originOpacity: Number(getComputedStyle(menu.querySelector('.account-origin')).opacity), scrimOpacity: Number(getComputedStyle(menu.querySelector('.account-scrim')).opacity), clip: getComputedStyle(menu.querySelector('.account-clip')).clipPath }
      }
      const start = read()
      for (const animation of accountMotionSamples) animation.currentTime = 80
      const middle = read()
      for (const animation of accountMotionSamples) animation.currentTime = 250
      const overshoot = read()
      for (const animation of accountMotionSamples) animation.currentTime = 700
      return { button: { x: button.x, y: button.y, width: button.width, height: button.height }, start, middle, overshoot, end: read(), animations: accountMotionSamples.length }
    })
    assert(expansion.animations > 0)
    for (const key of ['x', 'y', 'width', 'height']) assert(Math.abs(expansion.button[key] - expansion.start[key]) <= 1, JSON.stringify(expansion))
    assert.equal(expansion.start.contentOpacity, 0)
    assert.equal(expansion.start.glassOpacity, 1, '起点由原胶囊玻璃连续交接')
    assert.equal(expansion.start.originOpacity, 1, '起点保留原胶囊图标')
    assert(expansion.middle.width > expansion.start.width && expansion.middle.width < expansion.end.width)
    assert(expansion.middle.height > expansion.start.height && expansion.middle.height < expansion.end.height)
    for (const state of [expansion.middle, expansion.overshoot, expansion.end]) {
      assert.deepEqual(state.material, expansion.start.material, '0/80/250/700ms 的滤镜采样边界保持固定，仅外框改变几何')
    }
    assert(expansion.middle.contentOpacity > 0 && expansion.middle.contentOpacity < 1, '文字透明度由弹簧几何进度驱动')
    const widthProgress = (expansion.middle.width - expansion.start.width) / (expansion.end.width - expansion.start.width)
    const heightProgress = (expansion.middle.height - expansion.start.height) / (expansion.end.height - expansion.start.height)
    assert(widthProgress > heightProgress, '宽度先行，高度滞后')
    assert(expansion.middle.iconWidth < expansion.end.iconWidth)
    assert(Math.abs(expansion.middle.iconWidth - expansion.middle.iconHeight) < .01, '文字图标统一等比缩放')
    assert(Math.abs(expansion.middle.iconWidth / expansion.end.iconWidth - expansion.middle.width / expansion.end.width) < .005, '内容比例跟随当前窗口宽度')
    assert(expansion.overshoot.width > expansion.end.width && expansion.overshoot.height > expansion.end.height, '双弹簧保留小幅过冲')
    assert.equal(expansion.end.scrimOpacity, 1)
    assert.equal(expansion.end.originOpacity, 0)
    assert.notEqual(expansion.middle.clip, expansion.end.clip, '下方内容由边界逐步露出')
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
        const box = menu.querySelector('[data-menu-surface]').getBoundingClientRect()
        return { x: box.x, y: box.y, width: box.width, height: box.height }
      }
      trigger.click(); await frame()
      const leaving = document.querySelector('#header-management')
      const oldAnimations = leaving.getAnimations({ subtree: true })
      for (const animation of oldAnimations) { animation.pause(); animation.currentTime = 85 }
      const before = read(leaving)
      trigger.click(); await frame()
      const entering = document.querySelector('#header-management')
      for (const animation of entering.getAnimations({ subtree: true })) { animation.pause(); animation.currentTime = 0 }
      const after = read(entering)
      const oldStopped = oldAnimations.every(animation => animation.playState === 'idle')
      for (const animation of entering.getAnimations({ subtree: true })) animation.finish()
      return { before, after, oldStopped }
    })
    assert(Math.abs(reversal.before.y - reversal.after.y) < 1 && Math.abs(reversal.before.height - reversal.after.height) < 1, '反向时按原生较大的宽高进度重新启动双弹簧')
    assert(reversal.after.width >= reversal.before.width && reversal.after.width < expansion.end.width, '重开承接当前进度，不回到起点')
    assert(reversal.oldStopped, '旧节点的动画应立即释放')
    await open(); await close()
    await trigger().focus(); await page.keyboard.press('Enter'); await open()
    assert(await menu().getByRole('link', { name: /站内通知/ }).evaluate(el => el === document.activeElement), '手机键盘打开后焦点落在可见菜单项')
    await close()
    await open()
    const contraction = await page.evaluate(async () => {
      const trigger = document.querySelector('button[aria-controls="header-management"]')
      const source = trigger.closest('.header-actions').getBoundingClientRect()
      trigger.click(); await new Promise(resolve => requestAnimationFrame(resolve))
      const menu = document.querySelector('#header-management'), animations = menu.getAnimations({ subtree: true })
      const read = time => {
        for (const animation of animations) { animation.pause(); animation.currentTime = time }
        const box = menu.querySelector('[data-menu-surface]').getBoundingClientRect()
        return { x: box.x, y: box.y, width: box.width, height: box.height, contentOpacity: Number(getComputedStyle(menu.querySelector('.account-content')).opacity), originOpacity: Number(getComputedStyle(menu.querySelector('.account-origin')).opacity) }
      }
      const pouring = read(150), pill = read(200), settling = read(240)
      for (const animation of animations) animation.finish()
      return { source: { x: source.x, y: source.y, width: source.width, height: source.height }, pouring, pill, settling }
    })
    assert.equal(contraction.pouring.contentOpacity, 0, '回灌前段菜单文字先消失')
    for (const key of ['x', 'y', 'width', 'height']) assert(Math.abs(contraction.pill[key] - contraction.source[key]) < 1, '200ms 时回到原胶囊')
    assert.equal(contraction.pill.originOpacity, 1)
    assert(contraction.settling.width > contraction.pill.width && contraction.settling.height < contraction.pill.height, '回灌后胶囊轻微横向回弹')
    await page.locator('#header-management').waitFor({ state: 'detached' })
    assert.equal(await page.locator('[data-menu-origin-owned]').count(), 0, '收尾后把来源胶囊交还原组件')
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
    assert.equal(await trigger().getAttribute('aria-expanded'), 'false', '300ms 内再次点击来源位置关闭菜单')
    await open()
    await page.evaluate(async () => { document.querySelector('button[aria-controls="header-management"]').click(); await new Promise(resolve => requestAnimationFrame(resolve)); for (const animation of document.querySelector('#header-management').getAnimations({ subtree: true })) animation.pause() })
    await page.setViewportSize({ width: 390, height: 800 })
    await page.locator('#header-management').waitFor({ state: 'detached' })
    assert.equal(await page.locator('[data-menu-origin-owned]').count(), 0, '退场中改变窗口大小立即清理旧几何和来源占用')
    await open(); await page.setViewportSize({ width: 844, height: 390 })
    await page.locator('#header-management').waitFor({ state: 'detached' })
    await page.setViewportSize({ width: 390, height: 844 })
    assert.equal(await page.locator('[data-menu-origin-owned]').count(), 0, '跨断点往返不残留来源占用')
    passed.push('TG 13.2.2 整块胶囊交接、双弹簧过冲、等比内容、几何渐显与快速开关')
    passed.push('200ms 回灌、胶囊回弹、外部点击不穿透、二次点击关闭及退场 resize 清理')

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
    await go('/tv')
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

    role = 'user'
    await go('/movie/123'); await open(); await personalMenu(true)
    assert.equal(await menu().getByText('普通用户', { exact: true }).count(), 1)
    assert.equal(await menu().locator('.account-row:visible').count(), 4)
    await close()
    // 手机详情使用返回入口，回到列表后仍可通过原 Logo 打开功能侧栏。
    await page.locator('.detail-nav-back').click(); await page.waitForURL('**/movie')
    await page.locator('.brand-link').click(); await drawer.waitFor()
    assert.deepEqual(await drawer.getByRole('link').allTextContents(), ['我的播放', '我的追更'])
    await page.keyboard.press('Escape'); await drawer.waitFor({ state: 'detached' })
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
