// 所有接口使用模拟数据；验证固定玻璃材质，不读取真实凭据或提交业务任务。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const legacyStorageKey = 'cinema115-glass-settings'

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const errors = [], requests = [], passed = []
  let role = 'admin'
  try {
    await fs.mkdir(path.join(__dirname, '../test-results'), { recursive: true })
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, colorScheme: 'light', serviceWorkers: 'block' })
    await context.addInitScript(() => {
      localStorage.setItem('admin_token', 'mock-glass-token')
      if (!localStorage.getItem('cinema115-theme')) localStorage.setItem('cinema115-theme', 'light')
      window.glassGlobalWrites = []
      // 固定材质由 CSS 声明，启动、滚动和动画不应再逐帧改写全局玻璃参数。
      for (const method of ['setProperty', 'removeProperty']) {
        const original = CSSStyleDeclaration.prototype[method]
        CSSStyleDeclaration.prototype[method] = function(name, ...values) {
          if (this === document.documentElement.style && /glass|refraction/i.test(name)) window.glassGlobalWrites.push({ method, name, values })
          return original.call(this, name, ...values)
        }
      }
    })
    await context.route('**/api/**', async route => {
      const endpoint = new URL(route.request().url()).pathname
      requests.push({ endpoint, method: route.request().method() })
      let data = { configured: true, accounts: [], items: [], total: 0, totalPages: 1, hasMore: false }
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock-glass', username: '固定玻璃验证', role } }
      else if (endpoint.endsWith('/telegram-user/config')) data = { configured: true, hasSession: true, botConfigured: true }
      else if (endpoint.endsWith('/telegram-user/channels')) data = { account: { hasSession: true }, channels: [{ id: 'mock-channel', title: '模拟频道' }] }
      else if (endpoint.endsWith('/resources/search-settings')) data = { order: ['re0', 'bot', 'channels', 'gying', 'mikan', 'garden', 'dmhy', 'woniu'] }
      else if (endpoint.includes('/tmdb/detail/')) data = { id: 300146, kind: 'tv', title: '嫁金钗', originalTitle: 'Where the Mask Ends', overview: '固定玻璃材质验证。', posterUrl: '', backdropUrl: '', date: '2026-01-01', rating: 8, votes: 10, status: 'Released', runtime: 45, episodeCount: 30, genres: [], countries: [], cast: [], seasons: [], collection: null, imdbId: '', imdbUrl: '', tmdbUrl: '' }
      else if (endpoint.includes('/emby/media/')) data = { media: { state: 'absent', items: [] } }
      else if (endpoint.includes('recommendations')) data = { items: [], page: 1, totalPages: 1 }
      else if (endpoint.includes('/follows/')) data = { subscribed: false, request: null, libraryState: 'absent', items: [], hasMore: false }
      else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
      await route.fulfill({ json: data })
    })
    const page = await context.newPage(), cdp = await context.newCDPSession(page)
    page.setDefaultTimeout(10000)
    page.on('pageerror', error => errors.push(error.message))
    page.on('console', message => { if (message.text().includes('[Vue warn]')) errors.push(message.text()) })
    const trigger = () => page.locator('button[aria-controls="header-management"]')
    const menu = () => page.getByRole('navigation', { name: '账户菜单', exact: true })
    const detailPills = () => page.locator('.media-detail .detail-search-buttons .search-pill')
    const openMenu = async () => {
      if (await trigger().getAttribute('aria-expanded') !== 'true') await trigger().click()
      await page.waitForFunction(() => {
        const element = document.querySelector('#header-management')
        return element && !element.hasAttribute('inert') && !element.dataset.menuMotion
      })
      assert.equal(await menu().getByRole('button', { name: '外观设置', exact: true }).count(), 0)
      assert.equal(await page.getByRole('dialog', { name: '外观设置', exact: true }).count(), 0)
    }
    const closeMenu = async () => {
      await page.keyboard.press('Escape')
      await page.locator('#header-management').waitFor({ state: 'detached' })
      assert(await trigger().evaluate(element => document.activeElement === element))
    }
    const material = (locator, pseudo = null) => locator.first().evaluate((element, pseudo) => {
      const style = getComputedStyle(element, pseudo)
      const canvas = document.createElement('canvas'); canvas.width = canvas.height = 1
      const drawing = canvas.getContext('2d')
      drawing.fillStyle = style.backgroundColor; drawing.fillRect(0, 0, 1, 1)
      return { filter: style.backdropFilter, background: style.backgroundColor, alpha: drawing.getImageData(0, 0, 1, 1).data[3] / 255, shadow: style.boxShadow, radius: style.borderRadius, textFilter: getComputedStyle(element).filter, textBackdrop: getComputedStyle(element).backdropFilter }
    }, pseudo)
    const pillMaterial = () => material(detailPills(), '::before')
    const assertNativeFilter = style => {
      assert(!/url\(/i.test(style.filter), '固定玻璃只使用原生滤镜')
      const blur = style.filter.match(/blur\(([\d.]+)px\)/)
      assert(blur && Number(blur[1]) > 0 && Number(blur[1]) <= 48, `固定模糊半径保持有界：${style.filter}`)
      assert.equal((style.filter.match(/\bblur\(/g) || []).length, 1, '每个表面只使用一次模糊')
    }
    const assertNoGlobalGlass = async () => {
      assert.deepEqual(await page.evaluate(() => window.glassGlobalWrites), [], '固定材质不写入全局玻璃内联参数')
      assert.equal(await page.evaluate(() => [...document.documentElement.style].some(name => /glass|refraction/i.test(name))), false)
      assert.equal(await page.locator('[data-glass-refraction], #cinema-glass-refraction, svg feDisplacementMap').count(), 0, '不再挂载 SVG 折射或实验性开关状态')
    }
    const screenshot = name => page.screenshot({ path: path.join(__dirname, '../test-results', name + '.png') })
    const useTheme = theme => page.evaluate(async theme => { const { useTheme } = await import('/src/composables/useTheme.ts'); await useTheme().setTheme(theme) }, theme)
    const waitDetail = async () => { await detailPills().last().waitFor(); await page.mouse.move(0, 0) }

    await page.goto(origin + '/tv/300146')
    await waitDetail()
    assert.equal(await detailPills().count(), 6)
    const baseline = await pillMaterial()
    assertNativeFilter(baseline)
    assert.equal(baseline.textFilter, 'none')
    assert.equal(baseline.textBackdrop, 'none', '文字层不重复采样')
    assert(baseline.alpha > 0)
    await openMenu()
    const baselineMenu = await material(menu().locator('.account-surface'))
    assertNativeFilter(baselineMenu)
    for (const selector of ['.account-content', '.account-scrim']) {
      assert.equal((await material(menu().locator(selector))).filter, 'none', `${selector} 不重复采样玻璃`)
    }
    await screenshot('glass-surfaces-desktop-light')
    await closeMenu()
    await assertNoGlobalGlass()
    const glassCSS = await fs.readFile(path.join(__dirname, '../src/styles/floatingSurface.css'), 'utf8')
    assert.doesNotMatch(glassCSS, /--user-glass-|cinema-glass-refraction|data-glass-refraction/, '共享样式中移除用户参数与折射定义')
    passed.push('固定有界原生滤镜、文字与背景分层、菜单单层采样、无外观入口/SVG 折射/全局参数写入')

    // 旧版本的浏览器记录不迁移、不读取，不再改变固定材质。
    for (const value of [JSON.stringify({ blur: 0, saturate: 220, opacity: 25, rim: 0, refraction: true }), JSON.stringify({ blur: 999, saturate: 1, opacity: 100, rim: 100, refraction: false }), '{broken json']) {
      await page.evaluate(({ key, value }) => localStorage.setItem(key, value), { key: legacyStorageKey, value })
      await page.reload(); await waitDetail()
      assert.deepEqual(await pillMaterial(), baseline, '刷新后旧参数不影响固定搜索胶囊')
      await openMenu()
      assert.deepEqual(await material(menu().locator('.account-surface')), baselineMenu, '旧参数不影响固定菜单玻璃')
      await closeMenu()
      await page.evaluate(({ key, value }) => window.dispatchEvent(new StorageEvent('storage', { key, newValue: value, storageArea: localStorage })), { key: legacyStorageKey, value })
      assert.deepEqual(await pillMaterial(), baseline, '旧配置同步事件不再改写材质')
      await assertNoGlobalGlass()
    }
    passed.push('旧 localStorage 配置、损坏 JSON 与跨标签同步事件均不再影响固定材质')

    const themeMaterials = {}
    for (const width of [1440, 390, 320]) for (const theme of ['light', 'dark']) {
      await page.setViewportSize({ width, height: width === 1440 ? 1000 : 860 })
      await useTheme(theme)
      await page.mouse.move(0, 0)
      const style = await pillMaterial()
      assertNativeFilter(style)
      themeMaterials[theme] = style.background
      const boxes = await detailPills().evaluateAll(elements => elements.map(element => { const box = element.getBoundingClientRect(); return { x: box.x, y: box.y, width: box.width, height: box.height } }))
      assert.deepEqual(await detailPills().evaluateAll(elements => elements.map(element => element.dataset.searchSource)), ['re0', 'bot', 'channels', 'gying', 'dmhy', 'woniu'])
      assert.deepEqual(await page.locator('.media-detail .detail-search-buttons').evaluate(element => ({ display: getComputedStyle(element).display, wrap: getComputedStyle(element).flexWrap })), { display: 'flex', wrap: 'wrap' })
      assert(new Set(boxes.map(box => Math.round(box.width))).size > 1, '搜索按钮保持内容宽度')
      assert(boxes.every(box => box.height >= 44 && box.x >= 0 && box.x + box.width <= width + 1))
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
      await detailPills().first().scrollIntoViewIfNeeded()
      await screenshot(`glass-surfaces-search-${width}-${theme}`)
      await openMenu()
      assertNativeFilter(await material(menu().locator('.account-surface')))
      const box = await menu().locator('.account-surface').boundingBox(), viewport = page.viewportSize()
      assert(box.x >= 0 && box.y >= 0 && box.x + box.width <= viewport.width + 1 && box.y + box.height <= viewport.height + 1, JSON.stringify(box))
      await screenshot(`glass-surfaces-menu-${width}-${theme}`)
      await closeMenu()
      await assertNoGlobalGlass()
    }
    assert.notEqual(themeMaterials.light, themeMaterials.dark, '固定材质跟随深浅主题')
    passed.push('1440/390/320 深浅主题、搜索来源顺序与自然宽度换行、44px 热区、菜单视口边界')

    for (const theme of ['light', 'dark']) for (const feature of ['prefers-reduced-transparency', 'prefers-contrast', 'forced-colors']) {
      await useTheme(theme)
      await cdp.send('Emulation.setEmulatedMedia', { features: [{ name: feature, value: feature === 'prefers-reduced-transparency' ? 'reduce' : feature === 'prefers-contrast' ? 'more' : 'active' }] })
      // 胶囊底色保留短过渡，等待无障碍实色真正绘制完成再取样。
      await page.waitForFunction(() => {
        const element = document.querySelector('.media-detail .detail-search-buttons .search-pill')
        if (!element) return false
        const style = getComputedStyle(element, '::before')
        const canvas = document.createElement('canvas'); canvas.width = canvas.height = 1
        const drawing = canvas.getContext('2d')
        drawing.fillStyle = style.backgroundColor; drawing.fillRect(0, 0, 1, 1)
        return style.backdropFilter === 'none' && drawing.getImageData(0, 0, 1, 1).data[3] === 255
      })
      const fallback = await pillMaterial()
      assert.equal(fallback.filter, 'none', `${theme}/${feature} 关闭背景滤镜`)
      assert.equal(fallback.alpha, 1, `${theme}/${feature} 使用不透明底色`)
      await openMenu()
      const fallbackMenu = await material(menu().locator('.account-surface'))
      assert.equal(fallbackMenu.filter, 'none')
      assert.equal(fallbackMenu.alpha, 1)
      assert.equal((await material(menu().locator('.account-scrim'))).filter, 'none')
      await closeMenu()
      await assertNoGlobalGlass()
      await cdp.send('Emulation.setEmulatedMedia', { features: [] })
    }
    assertNativeFilter(await pillMaterial())
    passed.push('深浅主题的减少透明度、高对比与强制颜色模式使用实色降级，遮罩不叠加模糊')

    role = 'user'
    await page.reload(); await trigger().waitFor(); await openMenu()
    assert.equal(await menu().getByText('普通用户', { exact: true }).count(), 1)
    assert.equal(await menu().getByRole('button', { name: '外观设置', exact: true }).count(), 0)
    assert.equal(await page.getByRole('dialog', { name: '外观设置', exact: true }).count(), 0)
    await closeMenu()
    await assertNoGlobalGlass()
    assert.equal(requests.filter(request => !['GET', 'HEAD'].includes(request.method)).length, 0, '验证不发起业务写请求')
    assert.deepEqual(errors, [])
    passed.push('管理员与普通用户均无外观入口，全部接口 mock 且无业务写入/浏览器错误')
    console.log(JSON.stringify({ passed, screenshots: 'test-results/glass-surfaces-*.png' }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
