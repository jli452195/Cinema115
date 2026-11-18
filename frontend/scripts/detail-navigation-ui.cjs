// 所有接口均为模拟数据，验证详情导航和图片降级，不提交真实业务操作。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const errors = [], writes = [], passed = []
  let holdDetail = false, releaseDetail
  let page
  try {
    await fs.mkdir('test-results', { recursive: true })
    const context = await browser.newContext({ viewport: { width: 390, height: 844 }, colorScheme: 'light', serviceWorkers: 'block' })
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-detail-token'); localStorage.setItem('cinema115-theme', 'light') })
    await context.route('**/api/**', async route => {
      const request = route.request(), endpoint = new URL(request.url()).pathname
      if (request.method() !== 'GET') writes.push(endpoint)
      if (endpoint.includes('/tmdb/image/')) {
        if (endpoint.includes('broken')) { await route.fulfill({ status: 404, body: '' }); return }
        await route.fulfill({ contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="400" height="600"><defs><linearGradient id="a" x2="1" y2="1"><stop stop-color="#141c3a"/><stop offset=".5" stop-color="#3855a1"/><stop offset="1" stop-color="#976c69"/></linearGradient></defs><path fill="url(#a)" d="M0 0h400v600H0z"/><circle cx="270" cy="170" r="90" fill="#b9c5dd" opacity=".6"/><path d="m0 400 190-180 210 250v130H0" fill="#0f1932"/><text x="200" y="500" text-anchor="middle" fill="#fff" font-size="42">山海之间</text></svg>' })
        return
      }
      let data = { configured: true, items: [], accounts: [], total: 0, totalPages: 1, genres: [], hasMore: false }
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock-detail', username: '详情导航验证', role: 'admin' } }
      else if (endpoint.endsWith('/telegram-user/config')) data = { configured: true, hasSession: true, botConfigured: true }
      else if (endpoint.endsWith('/telegram-user/channels')) data = { account: { hasSession: true }, channels: [{ id: 'mock', title: '模拟频道' }] }
      else if (endpoint.endsWith('/resources/search-settings')) data = { order: ['re0', 'bot', 'channels', 'gying', 'mikan', 'garden', 'dmhy', 'woniu'] }
      else if (endpoint.includes('/tmdb/detail/')) {
        if (holdDetail) { holdDetail = false; await new Promise(resolve => { releaseDetail = resolve }) }
        const id = Number(endpoint.split('/').pop()), kind = endpoint.includes('/movie/') ? 'movie' : 'tv'
        if (id === 404) { await route.fulfill({ status: 502, json: { error: '模拟详情加载失败' } }); return }
        data = { id, kind, title: id === 2 ? '这是用于验证窄屏与横竖屏切换的特别长的影视标题山海之间' : '山海之间', originalTitle: 'Between Mountains and Seas', date: '2026-11-03', overview: Array(24).fill('风吹过群山，故事从这里开始。不同的旅途在此相遇。').join('\n'), tagline: '走向山海之间。', posterUrl: id === 3 ? '' : '/api/tmdb/image/w500/detail-poster.jpg', backdropUrl: id === 2 ? '/api/tmdb/image/w1280/broken.jpg' : id === 3 ? '' : '/api/tmdb/image/w1280/detail-backdrop.jpg', rating: 8.5, votes: 100, runtime: 45, episodeCount: 27, status: 'Ended', genres: [{ id: 18, name: '剧情' }], countries: ['China'], cast: [], seasons: [], collection: kind === 'movie' ? { id: 10, name: '山海系列' } : null, imdbId: '', imdbUrl: '', tmdbUrl: '' }
      } else if (endpoint.includes('/emby/media/')) data = { media: { state: 'absent', items: [] } }
      else if (endpoint.includes('recommendations')) data = { items: [], page: 1, totalPages: 1 }
      else if (endpoint.includes('/follows/')) data = { subscribed: false, request: null, libraryState: 'absent', items: [] }
      else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
      await route.fulfill({ json: data }).catch(() => {})
    })
    page = await context.newPage()
    const cdp = await context.newCDPSession(page)
    page.setDefaultTimeout(10000)
    page.on('pageerror', error => errors.push(error.message))
    page.on('console', message => { if (message.text().includes('[Vue warn]')) errors.push(message.text()) })
    const title = () => page.locator('.detail-nav-title')
    const trigger = () => page.locator('button[aria-controls="header-management"]')
    const navigate = to => page.evaluate(async to => { await document.querySelector('#app').__vue_app__.config.globalProperties.$router.push(to) }, to)
    const ready = () => page.locator('h1[ref], .detail-content h1 button').first().waitFor()
    const titleVisible = value => page.waitForFunction(value => {
      const element = document.querySelector('.detail-nav-title')
      return element?.classList.contains('is-visible') === value && getComputedStyle(element).visibility === (value ? 'visible' : 'hidden')
    }, value)
    const positionTitle = async (past, gap = 16) => {
      await page.evaluate(({ past, gap }) => {
        const heading = document.querySelector('.detail-content h1'), bar = document.querySelector('.workbench-header')
        scrollTo(0, scrollY + heading.getBoundingClientRect().bottom - bar.getBoundingClientRect().bottom + (past ? gap : -gap))
      }, { past, gap })
      await titleVisible(past)
    }
    const checkBounds = async () => {
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, '页面无横向溢出')
      const controls = await page.locator('.detail-nav-back, .header-actions > a[aria-label="搜索影视"], .header-actions > button[aria-controls]').evaluateAll(elements => elements.map(element => { const r = element.getBoundingClientRect(); return { left: r.left, right: r.right, width: r.width, height: r.height } }))
      assert.equal(controls.length, 3)
      assert(controls.every(r => r.width >= 44 && r.height >= 44 && r.left >= 0 && r.right <= innerWidthForTest + 1), JSON.stringify(controls))
    }
    let innerWidthForTest = 390
    await page.goto(origin + '/tv/1'); await ready()
    await titleVisible(false)
    assert.equal(await page.getByRole('navigation', { name: '影视导航' }).count(), 0)
    assert.equal(await page.getByRole('heading', { level: 1 }).count(), 1)
    assert.equal(await page.getByRole('button', { name: '返回上一页', exact: true }).count(), 1)
    await checkBounds()
    const follow = await page.locator('.follow-button').boundingBox()
    assert(follow.width < 160 && follow.height < 44, '追更按钮保留原有尺寸')
    assert.equal(await page.locator('.detail-overview').evaluate(el => getComputedStyle(el).borderTopWidth), '0px', '剧情大纲不新增分隔线')
    assert.deepEqual(await page.locator('.detail-search-buttons').evaluate(el => ({ display: getComputedStyle(el).display, wrap: getComputedStyle(el).flexWrap })), { display: 'flex', wrap: 'wrap' })
    await page.screenshot({ path: 'test-results/detail-navigation-mobile-light.png', animations: 'disabled' })
    await positionTitle(true)
    const shortTitleBox = await title().boundingBox(), shortTitleSlot = await page.locator('.detail-nav-title-slot').boundingBox()
    assert(Math.abs(shortTitleBox.width - shortTitleSlot.width) <= 1 && Math.abs(shortTitleBox.height - 44) <= .5, '短片名胶囊填满中间区域，与两侧按钮同高')
    assert.equal(await title().textContent(), '山海之间2026')
    assert.equal(await page.locator('.detail-nav-title-slot').getAttribute('aria-hidden'), 'true')
    await page.screenshot({ path: 'test-results/detail-navigation-scrolled.png', animations: 'disabled' })
    await positionTitle(false)
    await page.evaluate(() => scrollTo(0, 0)); await titleVisible(false)
    passed.push('手机悬浮导航、正文标题实际跨界、唯一 h1、原有追更尺寸/搜索自然换行与无额外分隔线')

    await positionTitle(false, 12)
    await page.locator('.workbench-header').evaluate(el => { el.style.paddingTop = '28px' })
    await titleVisible(true)
    await page.locator('.workbench-header').evaluate(el => { el.style.removeProperty('padding-top') })
    await titleVisible(false)
    passed.push('安全区单独改变 padding 时，标题切换边界随顶栏实际高度更新')

    await navigate('/tv/2'); await ready()
    await page.locator('.detail-backdrop.is-poster-backdrop').waitFor()
    for (const width of [320, 390]) {
      innerWidthForTest = width
      await page.setViewportSize({ width, height: 844 })
      await positionTitle(true)
      await checkBounds()
      const titleBox = await title().boundingBox(), slotBox = await page.locator('.detail-nav-title-slot').boundingBox()
      assert(Math.abs(titleBox.width - slotBox.width) <= 1 && Math.abs(titleBox.height - 44) <= .5, '不同屏宽下片名胶囊均填满中间可用空间，不挤压左右按钮')
      const text = await page.locator('.detail-nav-name').evaluate(el => ({ width: el.clientWidth, content: el.scrollWidth, overflow: getComputedStyle(el).textOverflow }))
      assert(text.width > 0 && text.content > text.width && text.overflow === 'ellipsis')
      for (const theme of ['light', 'dark']) {
        await page.evaluate(async theme => { const { useTheme } = await import('/src/composables/useTheme.ts'); await useTheme().setTheme(theme) }, theme)
        await page.screenshot({ path: `test-results/detail-navigation-${width}-${theme}.png`, animations: 'disabled' })
      }
    }
    await page.setViewportSize({ width: 844, height: 390 })
    await title().waitFor({ state: 'detached' })
    assert.equal(await page.getByRole('navigation', { name: '影视导航' }).isVisible(), true)
    assert.equal(await page.getByRole('button', { name: '返回上一页', exact: true }).count(), 1)
    await page.setViewportSize({ width: 390, height: 844 }); innerWidthForTest = 390
    await positionTitle(true)
    await trigger().click()
    await page.getByRole('navigation', { name: '账户菜单' }).waitFor()
    await page.keyboard.press('Escape')
    await page.locator('#header-management').waitFor({ state: 'detached' })
    assert.equal(await trigger().evaluate(el => document.activeElement === el), true)
    passed.push('320/390 深浅主题长片名省略、44px 热区、横屏导航恢复、单一更多菜单及 Escape 焦点')

    await page.evaluate(() => {
      window.detailHeaderWrites = 0
      window.detailHeaderObserver = new MutationObserver(records => { window.detailHeaderWrites += records.length })
      window.detailHeaderObserver.observe(document.querySelector('.workbench-header'), { attributes: true, attributeFilter: ['style'] })
    })
    await page.evaluate(async () => {
      for (let y = 0; y < 300; y += 15) { scrollTo(0, y); await new Promise(resolve => requestAnimationFrame(resolve)) }
    })
    assert.equal(await page.evaluate(() => { detailHeaderObserver.disconnect(); return detailHeaderWrites }), 0, '手机详情滚动不逐帧改写顶栏样式')
    await cdp.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-transparency', value: 'reduce' }] })
    assert.equal(await page.locator('.detail-nav-back').evaluate(el => getComputedStyle(el, '::before').backdropFilter), 'none')
    await cdp.send('Emulation.setEmulatedMedia', { features: [] })
    passed.push('手机详情滚动无逐帧样式写入，小胶囊遵守无透明降级')

    holdDetail = true
    await navigate('/tv/3')
    await page.getByLabel('正在加载影视详情', { exact: true }).waitFor()
    await titleVisible(false)
    assert.equal(await title().innerText(), '')
    assert.equal(await page.locator('.detail-nav-back').isVisible(), true)
    assert(releaseDetail); releaseDetail(); await ready()
    assert.equal(await page.locator('.detail-backdrop').count(), 0)
    await positionTitle(true)
    await navigate('/tv/404')
    await page.getByRole('heading', { name: '暂时无法加载详情' }).waitFor()
    await titleVisible(false)
    assert.equal(await page.getByRole('button', { name: '返回上一页', exact: true }).count(), 1)
    passed.push('复用路由的加载/失败不残留片名，无图降级保留返回入口')

    await navigate('/movie/1'); await ready()
    await page.evaluate(() => history.replaceState({ ...history.state, back: 'https://outside.invalid/path' }, ''))
    await page.locator('.detail-nav-back').click()
    await page.waitForURL('**/movie')
    assert.equal(await page.getByRole('navigation', { name: '影视分类' }).isVisible(), true)
    await navigate('/tv/1'); await ready()
    await navigate('/tv/2'); await ready()
    await page.locator('.detail-nav-back').click()
    await page.waitForURL('**/tv/1'); await ready()
    passed.push('外站历史使用电影回退页，同源详情历史正常返回，可返回新版电影列表')

    await page.evaluate(() => {
      let standalone = false
      const query = new EventTarget(), native = matchMedia.bind(window)
      Object.defineProperty(query, 'matches', { get: () => standalone })
      window.matchMedia = value => value === '(display-mode: standalone)' ? query : native(value)
      window.detailSetStandalone = () => {
        standalone = true
        const collect = rules => { for (const rule of rules) { if (rule instanceof CSSMediaRule && rule.conditionText.includes('display-mode: standalone')) rule.media.mediaText = 'all'; if (rule.cssRules) collect(rule.cssRules) } }
        for (const sheet of document.styleSheets) collect(sheet.cssRules)
        query.dispatchEvent(new Event('change'))
      }
    })
    // 重新挂载顶栏以使用隔离的独立窗口媒体查询，真实浏览器系统栏另由已有专项检查验证。
    await navigate('/movie'); await navigate('/tv/1'); await ready()
    await page.evaluate(() => { detailSetStandalone(); document.documentElement.style.setProperty('--app-safe-top', '28px'); document.documentElement.style.setProperty('--app-safe-left', '28px') })
    await page.waitForFunction(() => getComputedStyle(document.querySelector('.header-surface')).height === '28px')
    const safe = await page.locator('.header-surface').evaluate(el => ({ filter: getComputedStyle(el).backdropFilter, opacity: getComputedStyle(el).opacity, top: el.getBoundingClientRect().top }))
    assert.deepEqual(safe, { filter: 'none', opacity: '1', top: 0 })
    const toolbar = await page.locator('.header-inner').boundingBox()
    assert.equal(toolbar.y, 28)
    await checkBounds()
    passed.push('PWA 独立窗口保留实色安全区，悬浮操作栏单独避让，不重复全宽采样')

    assert.deepEqual(writes, [])
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, errors, writes }, null, 2))
  } catch (error) {
    console.error({ completed: passed, url: page?.url(), state: await page?.evaluate(() => ({ title: document.querySelector('h1')?.textContent, image: document.querySelector('.detail-backdrop')?.outerHTML, text: document.body.innerText.slice(-600) })).catch(() => null) })
    throw error
  } finally { releaseDetail?.(); await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
