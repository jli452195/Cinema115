// Every API request is mocked; this check never reads or changes real service data.
const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
  const requests = [], errors = [], passed = []
  let role = 'admin'
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
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
        return element && !element.hasAttribute('inert') && !element.classList.contains('account-menu-motion-enter-active')
      })
    }
    const close = async () => {
      await page.keyboard.press('Escape')
      await menu().waitFor({ state: 'detached' })
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
    await menu().waitFor({ state: 'detached' })
    await open()
    assert.equal(await menu().getByRole('link', { name: '我的追更', exact: true }).getAttribute('aria-current'), 'page')
    await page.locator('.header-inner').click({ position: { x: 1, y: 1 } })
    await menu().waitFor({ state: 'detached' })
    await open()
    await page.getByRole('link', { name: '电影', exact: true }).focus()
    await menu().waitFor({ state: 'detached' })
    passed.push('personal-menu-only-with-keyboard-navigation-escape-outside-and-focus-close')

    // Management destinations remain reachable through the left navigation and category cards.
    await go('/movie/123'); await open()
    const desktopSidebar = page.getByRole('complementary', { name: '功能侧栏', exact: true })
    const drawer = page.getByRole('dialog', { name: '功能导航', exact: true })
    for (const to of adminDestinations) assert.equal(await desktopSidebar.locator(`a[href="${to}"]`).count(), 1)
    await desktopSidebar.getByRole('link', { name: '用户与邀请', exact: true }).click()
    await page.waitForURL('**/users'); await drawer.waitFor({ state: 'detached' })
    await menu().waitFor({ state: 'detached' })
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
    assert.equal(await page.locator('.navigation-card').count(), 4)
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
        if (width === 1440 || width === 390) await screenshot(`account-personal-${width}-${dark ? 'dark' : 'light'}`)
        await close()
      }
    }
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
      await menu().waitFor({ state: 'detached' })
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
    await page.waitForURL('**/notifications'); await menu().waitFor({ state: 'detached' })
    await page.emulateMedia({ reducedMotion: 'reduce' }); await open(); await close()
    passed.push('desktop-mobile-themes-short-screen-notifications-and-reduced-motion')

    role = 'user'
    await go('/movie/123'); await open(); await personalMenu(true)
    assert.equal(await menu().getByText('普通用户', { exact: true }).count(), 1)
    assert.equal(await menu().locator('.account-row:visible').count(), 4)
    await close()
    await page.locator('.brand-link').click(); await drawer.waitFor()
    assert.deepEqual(await drawer.getByRole('link').allTextContents(), ['我的播放', '我的追更'])
    await page.keyboard.press('Escape'); await drawer.waitFor({ state: 'detached' })
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
