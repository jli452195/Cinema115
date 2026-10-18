// Every API and poster request is mocked. No subscriptions, upstream reads or 115 writes.
const assert = require('node:assert/strict')
const fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const passed = [], errors = [], contexts = []
  await fs.mkdir('test-results', { recursive: true })
  async function session(role = 'admin', viewport = { width: 1440, height: 1000 }) {
    const context = await browser.newContext({ viewport, serviceWorkers: 'block' }); contexts.push(context)
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
    const page = await context.newPage(); page.setDefaultTimeout(10000)
    page.on('pageerror', e => errors.push(e.message))
    const calls = [], state = { role, calendarStatus: 200, stale: false, empty: false, failRelations: false, holdSubject: false, releaseSubject: undefined, slowSearch: undefined }
    const meta = () => ({ fetchedAt: Date.now() - 60000, expiresAt: Date.now() + (state.stale ? -1000 : 600000), stale: state.stale, dataVersion: 'mock-v1', issue: state.stale ? { code: 'UPSTREAM_UNAVAILABLE', retryAfterAt: null } : null })
    const card = (id, title = `番剧篇章 ${id}`) => ({ id: String(id), title, originalTitle: `Original anime ${id}`, coverUrl: `https://anime-poster.test/${id}.svg`, date: '2026-10-01', weekday: 1, url: `https://bgm.tv/subject/${id}` })
    await page.route('https://anime-poster.test/**', async route => {
      const id = Number(new URL(route.request().url()).pathname.match(/\d+/)?.[0] || 1)
      const hue = id * 47 % 360
      await route.fulfill({ contentType: 'image/svg+xml', body: `<svg xmlns="http://www.w3.org/2000/svg" width="240" height="360"><defs><linearGradient id="g" x2="1" y2="1"><stop stop-color="hsl(${hue},40%,30%)"/><stop offset="1" stop-color="hsl(${hue + 40},55%,70%)"/></linearGradient></defs><path fill="url(#g)" d="M0 0h240v360H0z"/><circle cx="180" cy="100" r="95" fill="white" opacity=".12"/><text x="22" y="300" fill="white" font-size="24" font-family="sans-serif">Bangumi ${id}</text></svg>` }).catch(() => {})
    })
    await page.route('**/api/**', async route => {
      const request = route.request(), url = new URL(request.url()), endpoint = url.pathname
      const body = request.postDataJSON()
      calls.push({ endpoint, query: url.search, method: request.method(), body })
      let status = 200, data = {}
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock', username: 'mock', role: state.role, disabled: false } }
      else if (endpoint.endsWith('/discovery/calendar')) {
        status = state.calendarStatus
        if (status !== 200) data = { error: status === 429 ? 'Bangumi 请求冷却中' : '模拟周历读取失败', code: status === 429 ? 'SOURCE_COOLDOWN' : 'UPSTREAM_UNAVAILABLE', details: { retryAfterAt: Date.now() + 2000 } }
        else data = { ...meta(), days: Array.from({ length: 7 }, (_, day) => ({ weekday: day + 1, items: state.empty && day === 0 ? [] : Array.from({ length: day === 0 ? 16 : 4 }, (_, i) => card((day + 1) * 1000 + i + 1, i === 1 ? '这是一部长标题番剧用来核对窄屏两行显示与卡片高度是否保持一致' : `星期${day + 1}篇章 ${i + 1}`)) })) }
      } else if (endpoint.endsWith('/discovery/search')) {
        if (body.keyword === 'slow') await new Promise(resolve => { state.slowSearch = resolve })
        if (body.keyword === 'failure') { status = 502; data = { error: '模拟搜索读取失败', code: 'UPSTREAM_UNAVAILABLE' } }
        else {
          const total = body.keyword === 'none' ? 0 : 43
          data = { ...meta(), total, limit: 20, offset: body.offset, items: Array.from({ length: Math.max(0, Math.min(20, total - body.offset)) }, (_, i) => card(8000 + body.offset + i, `${body.keyword} 搜索篇章 ${body.offset + i + 1}`)) }
        }
      } else if (endpoint.includes('/discovery/subjects/')) {
        const id = endpoint.split('/').pop()
        if (state.holdSubject) { state.holdSubject = false; await new Promise(resolve => { state.releaseSubject = resolve }) }
        if (url.searchParams.has('includeRelations') && state.failRelations) { status = 502; data = { error: '模拟关联读取失败', code: 'UPSTREAM_UNAVAILABLE' } }
        else {
          data = { subject: { ...card(id), synopsis: '这是一段篇章简介，作品资料与资源发布时间应当分别理解。'.repeat(16), platform: 'TV', episodes: 12, totalEpisodes: 12 }, metadata: meta() }
          if (url.searchParams.has('includeRelations')) data.relations = { ...meta(), items: [{ ...card(9001, '前传篇章'), relation: '前传' }, { ...card(9002, '续作篇章'), relation: '续集' }] }
        }
      } else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
      else if (endpoint.includes('/tmdb/')) data = { items: [], results: [], genres: [], total: 0, totalPages: 1, page: 1 }
      else if (endpoint.includes('/emby/')) data = { items: [], matches: [], configured: false }
      await route.fulfill({ status, contentType: 'application/json', headers: { 'Cache-Control': 'no-store' }, body: JSON.stringify(data) }).catch(() => {})
    })
    const go = async (suffix = '/anime?view=calendar&day=1') => {
      await page.goto(origin + suffix, { waitUntil: 'networkidle' })
      if (state.role === 'admin') await page.getByRole('heading', { name: '动漫订阅', exact: true }).waitFor()
    }
    const searchFor = async value => {
      if (!await page.locator('#anime-keyword').count()) await page.getByRole('button', { name: '历史搜索', exact: true }).click()
      await page.getByRole('searchbox', { name: '番剧名称' }).fill(value)
      await page.getByRole('button', { name: '搜索', exact: true }).click()
    }
    return { page, context, state, calls, go, searchFor }
  }
  try {
    const { page, state, calls, go, searchFor } = await session()
    const count = path => calls.filter(item => item.endpoint.endsWith(path)).length
    await go('/anime')
    const today = new Date().getDay() || 7
    assert.equal(await page.locator(`.anime-weekdays button:nth-of-type(${today})`).getAttribute('aria-pressed'), 'true')
    assert.equal(count('/discovery/calendar'), 1)
    await page.getByRole('button', { name: /^周一/ }).click()
    await page.getByRole('button', { name: /^周二/ }).click()
    await page.getByRole('button', { name: /^周一/ }).click()
    assert.equal(count('/discovery/calendar'), 1, 'weekday changes must be local')
    assert.equal(calls.filter(c => c.endpoint.includes('/subjects/')).length, 0, 'no per-card detail requests')
    await page.screenshot({ path: 'test-results/anime-calendar-desktop-light.png', fullPage: true, animations: 'disabled' })
    passed.push('默认今天，整周一次请求，本地切星期，无逐卡详情请求')

    state.holdSubject = true
    const card = page.locator('[data-anime-id="1001"]')
    await card.click()
    const dialog = page.getByRole('dialog')
    await dialog.getByRole('heading', { name: '星期1篇章 1', exact: true }).waitFor()
    await dialog.getByText('正在读取篇章资料…', { exact: true }).waitFor()
    assert.equal(calls.filter(c => c.query.includes('includeRelations')).length, 0)
    await page.waitForFunction(() => !!document.querySelector('dialog[open]'))
    assert.equal(await page.getByRole('button', { name: '关闭篇章详情' }).evaluate(el => el === document.activeElement), true)
    state.releaseSubject()
    await dialog.getByText('资料收录 12 话', { exact: true }).waitFor()
    assert.equal(await page.evaluate(() => document.body.style.overflow), 'hidden')
    await page.keyboard.press('Shift+Tab')
    assert.equal(await page.evaluate(() => document.querySelector('dialog').contains(document.activeElement)), true, 'native modal traps focus')
    state.failRelations = true
    await dialog.getByRole('button', { name: /^关联篇章/ }).click()
    await dialog.getByText('模拟关联读取失败', { exact: false }).waitFor()
    assert.equal(await dialog.getByRole('heading', { name: '番剧篇章 1001', exact: true }).count(), 1, 'relation failure preserves subject')
    state.failRelations = false
    await dialog.getByRole('button', { name: '重试关联', exact: true }).click()
    await dialog.getByRole('button', { name: '前传 前传篇章 2026', exact: true }).click()
    await dialog.getByRole('heading', { name: '番剧篇章 9001', exact: true }).waitFor()
    await dialog.getByRole('button', { name: '返回上一篇章' }).click()
    await dialog.getByRole('heading', { name: '番剧篇章 1001', exact: true }).waitFor()
    await page.keyboard.press('Escape')
    await dialog.waitFor({ state: 'detached' })
    assert.equal(await card.evaluate(el => el === document.activeElement), true, 'close restores trigger focus')
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    passed.push('详情头部即时显示，局部加载，关联按需/失败重试，篇章返回，ESC/焦点与滚动锁恢复')

    await searchFor('Re0')
    await page.locator('[data-anime-id="8000"]').waitFor()
    assert.equal(await page.locator('.anime-card').count(), 20)
    assert.equal(calls.filter(c => c.endpoint.endsWith('/search')).at(-1).body.offset, 0)
    await page.getByRole('button', { name: '搜索下一页' }).click()
    await page.locator('[data-anime-id="8020"]').waitFor()
    assert.equal(calls.filter(c => c.endpoint.endsWith('/search')).at(-1).body.offset, 20)
    const paginationReads = count('/discovery/search')
    await page.goBack(); await page.locator('[data-anime-id="8000"]').waitFor()
    await page.goForward(); await page.locator('[data-anime-id="8020"]').waitFor()
    assert.equal(count('/discovery/search'), paginationReads, 'browser history uses cached pages')
    await page.locator('[data-anime-id="8020"]').click()
    await dialog.getByRole('heading', { name: '番剧篇章 8020', exact: true }).waitFor()
    await page.keyboard.press('Escape')
    assert.match(page.url(), /page=2/)
    await page.evaluate(() => scrollTo(0, 500))
    const top = await page.evaluate(() => scrollY), searchCount = count('/discovery/search')
    await page.locator('.management-sidebar a[href="/users"]').click()
    await page.getByRole('heading', { name: '用户与邀请', exact: true }).waitFor()
    await page.locator('.management-sidebar a[href="/anime"]').click()
    await page.locator('[data-anime-id="8020"]').waitFor()
    assert.match(page.url(), /page=2/)
    assert.equal(count('/discovery/search'), searchCount, 'return uses cached page')
    await page.waitForFunction(expected => Math.abs(scrollY - expected) < 3, top)
    // A normal auth refresh must not invalidate the metadata or remembered page.
    await page.evaluate(async () => { const auth = await import('/src/composables/usePortalAuth.ts'); await auth.ensurePortalSession(true) })
    await page.locator('.management-sidebar a[href="/users"]').click()
    await page.getByRole('heading', { name: '用户与邀请', exact: true }).waitFor()
    await page.locator('.management-sidebar a[href="/anime"]').click()
    await page.locator('[data-anime-id="8020"]').waitFor()
    assert.equal(count('/discovery/search'), searchCount)
    passed.push('搜索分页、详情关闭、跨页返回及正常鉴权刷新保留数据/页码/滚动')

    const slowRequest = page.waitForRequest(request => request.url().endsWith('/discovery/search') && request.postDataJSON()?.keyword === 'slow')
    await searchFor('slow'); await slowRequest
    await searchFor('fast')
    await page.getByRole('button', { name: 'fast 搜索篇章 1，查看篇章', exact: true }).waitFor()
    state.slowSearch()
    await page.waitForLoadState('networkidle')
    assert.equal(await page.getByRole('button', { name: 'fast 搜索篇章 1，查看篇章', exact: true }).count(), 1)
    assert.equal(await page.locator('.anime-skeleton').count(), 0)
    await searchFor('none')
    await page.getByRole('heading', { name: '没有找到相关作品', exact: true }).waitFor()
    await searchFor('failure')
    await page.getByText('模拟搜索读取失败', { exact: true }).waitFor()
    assert.equal(await page.locator('.anime-card').count(), 0, 'new query must never display previous query results')
    await page.getByRole('searchbox', { name: '番剧名称' }).fill('')
    await page.getByRole('button', { name: '搜索', exact: true }).click()
    await page.getByRole('alert').getByText('请输入番剧名称').waitFor()
    passed.push('迟到响应不覆盖新搜索，新查询不混旧结果，零结果/失败/输入校验')

    await page.getByRole('button', { name: '新番日历', exact: true }).click()
    await page.locator('[data-anime-id="1001"]').waitFor()
    const originalCard = await page.locator('[data-anime-id="1001"]').elementHandle()
    await page.evaluate(() => scrollTo(0, 400))
    const refreshTop = await page.evaluate(() => scrollY)
    state.calendarStatus = 502
    await page.getByRole('button', { name: '刷新新番日历' }).evaluate(button => button.click())
    await page.getByText('模拟周历读取失败，已保留上次结果。', { exact: true }).waitFor()
    assert.equal(await originalCard.evaluate(el => el.isConnected), true, 'refresh failure keeps the same card DOM')
    assert.equal(await page.locator('.anime-skeleton').count(), 0)
    assert.equal(await page.evaluate(() => scrollY), refreshTop, 'explicit refresh does not restore old scroll')
    state.calendarStatus = 200; state.stale = true
    await page.getByRole('button', { name: '刷新新番日历' }).click()
    await page.getByText('正在使用缓存资料，暂未取得最新内容。', { exact: true }).waitFor()
    state.stale = false; state.empty = true
    await page.getByRole('button', { name: '刷新新番日历' }).click()
    await page.getByRole('heading', { name: '这一天暂未收录放送作品', exact: true }).waitFor()
    state.empty = false
    await page.getByRole('button', { name: '刷新新番日历' }).click()
    await page.locator('[data-anime-id="1001"]').waitFor()
    passed.push('后台刷新保留卡片DOM，失败不闪卡，真实空态与过期资料提示')

    const beforeAuthRefresh = count('/discovery/calendar')
    await page.evaluate(async () => { const auth = await import('/src/composables/usePortalAuth.ts'); auth.setPortalToken('another-mock-token'); await auth.ensurePortalSession(true) })
    await page.locator('[data-anime-id="1001"]').waitFor()
    assert.equal(count('/discovery/calendar'), beforeAuthRefresh + 1, 'identity change invalidates memory cache')
    assert.equal(await page.locator('main [token]').count(), 0)
    passed.push('令牌/身份切换清缓存，令牌不落页面属性')

    await page.setViewportSize({ width: 360, height: 800 })
    await page.screenshot({ path: 'test-results/anime-calendar-mobile-light.png', fullPage: true, animations: 'disabled' })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    assert.equal(await page.locator('.anime-grid').evaluate(el => getComputedStyle(el).gridTemplateColumns.split(' ').length), 2)
    await page.locator('[data-anime-id="1002"]').click()
    await dialog.getByRole('heading', { name: '番剧篇章 1002', exact: true }).waitFor()
    await dialog.getByRole('button', { name: /^关联篇章/ }).click()
    await dialog.getByText('前传篇章', { exact: true }).waitFor()
    const box = await dialog.boundingBox()
    assert.ok(box.x >= 0 && box.y >= 0 && box.x + box.width <= 361 && box.y + box.height <= 801, JSON.stringify(box))
    assert.equal(await dialog.evaluate(el => el.scrollWidth > el.clientWidth), false)
    await page.screenshot({ path: 'test-results/anime-detail-mobile-light.png', animations: 'disabled' })
    await page.keyboard.press('Escape')
    await page.evaluate(() => { document.documentElement.classList.add('dark') })
    await page.screenshot({ path: 'test-results/anime-calendar-mobile-dark.png', fullPage: true, animations: 'disabled' })
    await page.locator('[data-anime-id="1001"]').click()
    await dialog.getByRole('heading', { name: '番剧篇章 1001', exact: true }).waitFor()
    await page.screenshot({ path: 'test-results/anime-detail-mobile-dark.png', animations: 'disabled' })
    await page.keyboard.press('Escape')
    await page.setViewportSize({ width: 1440, height: 1000 })
    await searchFor('Re0')
    await page.locator('[data-anime-id="8000"]').waitFor()
    await page.screenshot({ path: 'test-results/anime-search-desktop-dark.png', fullPage: true, animations: 'disabled' })
    await page.emulateMedia({ reducedMotion: 'reduce' })
    assert.equal(await page.locator('.anime-poster').first().evaluate(el => getComputedStyle(el).transitionDuration), '0s')
    passed.push('360px双列/无横向溢出，详情边界/长标题，明暗截图，减少动效偏好')

    // Source cooldown is local UI state too; prevent repeated automatic/user retries.
    await page.getByRole('button', { name: '新番日历', exact: true }).click()
    await page.locator('[data-anime-id="1001"]').waitFor()
    state.calendarStatus = 429
    await page.clock.install()
    await page.getByRole('button', { name: '刷新新番日历' }).click()
    await page.getByText(/Bangumi 暂时限流/).waitFor()
    assert.equal(await page.getByRole('button', { name: '刷新新番日历' }).isDisabled(), true)
    await page.clock.fastForward(2500)
    assert.equal(await page.getByRole('button', { name: '刷新新番日历' }).isDisabled(), false)
    assert.ok(calls.filter(c => c.endpoint.includes('/anime/')).every(c => c.method === 'GET' || c.endpoint.endsWith('/discovery/search')))
    assert.equal(calls.filter(c => /mikan|emby|p115|subscriptions/.test(c.endpoint)).length, 0, 'discovery has no resource/player/download coupling')
    passed.push('限流冷却与恢复；动漫仅只读发现接口，无蜜柑/Emby/115请求或订阅写入')

    const fresh = await session()
    fresh.state.calendarStatus = 502
    await fresh.go()
    await fresh.page.getByRole('heading', { name: '暂时无法读取新番日历', exact: true }).waitFor()
    assert.equal(await fresh.page.locator('.anime-card').count(), 0)
    fresh.state.calendarStatus = 200
    await fresh.page.getByRole('button', { name: '重试', exact: true }).click()
    await fresh.page.locator('[data-anime-id="1001"]').waitFor()
    passed.push('首次无缓存读取失败和显式恢复')

    const user = await session('user')
    await user.go('/anime')
    await user.page.waitForURL(origin + '/')
    assert.equal(user.calls.filter(c => c.endpoint.includes('/anime/')).length, 0, 'ordinary user never mounts admin discovery')
    const expired = await session()
    await expired.go()
    await expired.page.evaluate(async () => { const auth = await import('/src/composables/usePortalAuth.ts'); auth.clearPortalSession() })
    await expired.page.waitForURL(/\/admin\/login/)
    const remembered = await expired.page.evaluate(async () => { const module = await import('/src/composables/useAnimeDiscovery.ts'); return { query: module.animeRememberedQuery(), cached: !!module.cachedAnime('calendar') } })
    assert.equal(remembered.query, undefined); assert.equal(remembered.cached, false)
    assert.ok(expired.calls.filter(c => c.endpoint.includes('/anime/')).length === 1, 'logout must not send an empty-token follow-up request')
    passed.push('普通用户不可挂载管理员页面，退出清除查询及数据缓存')
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, pageErrors: errors }, null, 2))
  } finally {
    await Promise.all(contexts.map(context => context.close()))
    await browser.close()
  }
})().catch(error => { console.error(error); process.exitCode = 1 })
