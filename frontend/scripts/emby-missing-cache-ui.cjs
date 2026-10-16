const path = require('node:path'), os = require('node:os'), fs = require('node:fs'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => {
      localStorage.setItem('admin_token', 'mock')
      window.posterObjects = new Set()
      const create = URL.createObjectURL.bind(URL), revoke = URL.revokeObjectURL.bind(URL)
      URL.createObjectURL = blob => { const url = create(blob); window.posterObjects.add(url); return url }
      URL.revokeObjectURL = url => { window.posterObjects.delete(url); revoke(url) }
    })
    const page = await context.newPage(), errors = [], requests = []
    page.on('pageerror', error => errors.push(error.message))
    const items = Array.from({ length: 216 }, (_, i) => ({ key: `tmdb:${i + 1}`, tmdbId: String(i + 1), title: `缓存剧${String(i + 1).padStart(3, '0')}`, posterUrl: `https://image.tmdb.org/t/p/w500/cache-${i + 1}.jpg`, missingCount: 1, seasons: [{ season: 1, lastEpisode: 8, ownedCount: 7, missingCount: 1, ranges: [{ first: 5, last: 5 }] }] }))
    const exclusions = Array.from({ length: 49 }, (_, i) => ({ id: `excluded-${i}`, seriesId: `series-${i}`, tmdbId: String(300 + i), title: `排除剧${String(i + 1).padStart(3, '0')}`, libraryId: 'tv', episodeCount: 3, excludedAt: '2026-10-16T05:00:00Z' }))
    const service = 's'.repeat(64)
    let failOffset = -1, listDelay = 0, excludedDelay = 0, gate, statusSyncedAt = '2026-10-16T02:00:00Z'
    let config = { url: 'http://emby:8096', configured: true, libraryIds: ['tv', 'movies'], serverId: 'server', serverName: '家庭媒体库', webhookPath: '/api/webhook/emby?token=mock', webhookBaseUrl: 'http://cinema:3002' }
    await page.route('**/api/**', async route => {
      const req = route.request(), url = new URL(req.url()), p = url.pathname
      requests.push({ p, method: req.method(), q: url.searchParams.get('q') || '', offset: Number(url.searchParams.get('offset') || 0) })
      if (p.includes('/tmdb/image/')) return route.fulfill({ contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="160" height="240"><rect width="160" height="240" fill="#85714b"/></svg>' })
      let data = {}, code = 200
      if (p === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
      else if (p === '/api/admin/emby/missing' || p === '/api/admin/emby/missing/exclusions') {
        const isExcluded = p.endsWith('/exclusions'), q = url.searchParams.get('q') || '', offset = Number(url.searchParams.get('offset') || 0)
        const filtered = (isExcluded ? exclusions : items).filter(item => !q || item.title.includes(q) || item.tmdbId.includes(q))
        const start = filtered.length ? Math.min(offset, Math.floor((filtered.length - 1) / 24) * 24) : 0
        data = { total: filtered.length, offset: start, nextOffset: start + 24, hasMore: filtered.length > start + 24, items: filtered.slice(start, start + 24), ...(isExcluded ? { service, count: exclusions.length } : { ready: true, configured: true, syncedAt: statusSyncedAt, notices: [], checkedSeries: items.length, excludedCount: exclusions.length, summary: { series: filtered.length, seasons: filtered.length, episodes: filtered.length } }) }
        if (!isExcluded && offset === failOffset) { code = 500; data = { error: '模拟目标页失败' } }
        if (!isExcluded && gate) await gate
        await new Promise(resolve => setTimeout(resolve, isExcluded ? excludedDelay : listDelay))
      } else if (p.endsWith('/emby/config')) {
        if (req.method() === 'POST') config = { ...config, libraryIds: req.postDataJSON().libraryIds }
        data = config
      } else if (p.endsWith('/emby/test')) data = { server: { Id: 'server', ServerName: '家庭媒体库', Version: '4.9' }, libraries: [{ ItemId: 'tv', Name: '电视剧', CollectionType: 'tvshows' }, { ItemId: 'movies', Name: '电影', CollectionType: 'movies' }] }
      else if (p.endsWith('/emby/status')) data = { status: 'idle', configured: true, connected: true, count: 216, scanned: 0, syncedAt: statusSyncedAt, pending: 0, failed: 0, metadataPending: 0, metadataFailed: 0, updatesStatus: 'ready' }
      await route.fulfill({ status: code, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const base = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    const cards = page.locator('[data-missing-card]'), rows = page.locator('[data-excluded-item]')
    const pager = () => page.getByRole('navigation', { name: '缺集结果分页', exact: true })
    const pageInput = () => pager().getByRole('textbox')
    const resultSearch = () => page.getByRole('searchbox', { name: '搜索缺集剧集' })
    const excludedSearch = () => page.getByRole('searchbox', { name: '搜索已排除条目' })
    const listReads = () => requests.filter(req => req.p === '/api/admin/emby/missing' || req.p === '/api/admin/emby/missing/exclusions').length
    const imageReads = () => requests.filter(req => req.p.includes('/tmdb/image/')).length
    const idle = () => page.waitForFunction(() => document.querySelector('[aria-label="刷新检测结果"]')?.disabled === false)
    async function jump(value) {
      await pageInput().fill(String(value)); await pageInput().press('Enter'); await idle()
      assert.equal(await pageInput().inputValue(), String(value))
      await cards.first().getByRole('heading', { name: items[(value - 1) * 24].title, exact: true }).waitFor()
    }
    async function excludedTab() { await page.getByRole('button', { name: /^已排除 \d+ 项$/ }).evaluate(el => el.click()); await idle() }
    async function resultTab() { await page.getByRole('button', { name: '缺集结果', exact: true }).evaluate(el => el.click()); await idle() }
    async function leave() { await page.getByRole('link', { name: 'Emby', exact: true }).click(); await page.getByRole('link', { name: /缺集检测 按季检查/ }).waitFor() }
    async function enter() { await page.getByRole('link', { name: /缺集检测 按季检查/ }).click(); await idle() }
    await page.goto(base + '/emby/missing'); await idle(); await cards.nth(23).waitFor()
    await jump(2)
    await resultSearch().fill('结果草稿')
    await page.evaluate(() => window.scrollTo(0, 500))
    const resultTop = await page.evaluate(() => scrollY), resultReads = listReads()
    await excludedTab(); await rows.nth(23).waitFor()
    await page.getByRole('button', { name: '下一页', exact: true }).click(); await idle()
    assert.equal(new URL(page.url()).searchParams.get('offset'), '24')
    await excludedSearch().fill('排除草稿')
    await page.evaluate(() => window.scrollTo(0, 650))
    const excludedTop = await page.evaluate(() => scrollY), warmReads = listReads()
    assert.equal(warmReads, resultReads + 2)
    await resultTab()
    assert.equal(await pageInput().inputValue(), '2')
    assert.equal(await resultSearch().inputValue(), '结果草稿')
    assert.equal(await page.evaluate(() => scrollY), resultTop)
    await excludedTab()
    assert.equal(await excludedSearch().inputValue(), '排除草稿')
    assert.equal(await page.evaluate(() => scrollY), excludedTop)
    assert.equal(listReads(), warmReads, 'switching back to either fresh view must not refetch')
    await resultTab()
    await resultSearch().fill('缓存剧001'); await resultSearch().press('Enter'); await idle()
    assert.equal(await cards.count(), 1)
    assert.equal(await pager().count(), 0, 'one-page searches hide pagination')
    await excludedTab()
    assert.equal(await excludedSearch().inputValue(), '排除草稿', 'search state belongs to each view')
    await resultTab()
    assert.equal(await resultSearch().inputValue(), '缓存剧001')
    await page.goBack(); await idle()
    assert.equal(new URL(page.url()).searchParams.get('view'), 'excluded', 'browser history remains authoritative')
    await page.goForward(); await idle()
    assert.equal(await resultSearch().inputValue(), '缓存剧001')
    await page.getByRole('button', { name: '清除搜索', exact: true }).click(); await idle(); await jump(2)
    // Leaving the route retains page, scroll, data and decoded posters.
    await cards.first().locator('img[alt]:not([alt=""])').waitFor()
    const blob = await cards.first().locator('img[alt]:not([alt=""])').getAttribute('src')
    await page.evaluate(() => window.scrollTo(0, 480))
    const routeTop = await page.evaluate(() => scrollY), beforeLeave = listReads()
    // Use programmatic click to avoid scrolling the breadcrumb into view before it captures scrollY.
    await page.getByRole('link', { name: 'Emby', exact: true }).evaluate(el => el.click())
    await page.getByRole('link', { name: /缺集检测 按季检查/ }).waitFor()
    const beforeImages = imageReads()
    assert.ok(await page.evaluate(url => window.posterObjects.has(url), blob))
    await enter()
    assert.equal(await pageInput().inputValue(), '2')
    assert.equal(await page.evaluate(() => scrollY), routeTop)
    assert.equal(listReads(), beforeLeave)
    assert.equal(imageReads(), beforeImages)
    assert.equal(await cards.first().locator('img[alt]:not([alt=""])').getAttribute('src'), blob)
    // Enter-only commit, input validation, same-page no-op, and blur/arrow ordering.
    const beforeInputs = listReads()
    for (const value of ['2', '', '0', '-1', '1.5', 'abc']) {
      await pageInput().fill(value); await pageInput().press('Enter')
      assert.equal(await pageInput().inputValue(), '2')
    }
    await pageInput().fill('7'); await pageInput().press('Escape')
    assert.equal(await pageInput().inputValue(), '2')
    await pageInput().fill('7'); await pageInput().blur()
    assert.equal(await pageInput().inputValue(), '2')
    assert.equal(listReads(), beforeInputs)
    await pageInput().fill('7'); await pager().getByRole('button', { name: '下一页' }).click(); await idle()
    assert.equal(await pageInput().inputValue(), '3')
    assert.equal(listReads(), beforeInputs + 1, 'blur must not submit an extra page before the arrow')
    await pageInput().fill('99999999'); await pageInput().press('Enter'); await idle()
    assert.equal(await pageInput().inputValue(), '9', 'valid integers above the last page clamp to it')
    await jump(4)
    failOffset = 96
    await pager().getByRole('button', { name: '下一页' }).click(); await page.getByRole('alert').waitFor(); await idle()
    assert.equal(await pageInput().inputValue(), '4', 'failed navigation keeps the actual displayed page number')
    assert.equal(await cards.first().getByRole('heading', { name: items[72].title, exact: true }).count(), 1)
    failOffset = -1
    await page.getByRole('button', { name: '重新读取列表', exact: true }).click(); await idle()
    assert.equal(await pageInput().inputValue(), '5')
    // Expired cache displays previous content during one background refresh.
    await excludedTab()
    await page.clock.install(); await page.clock.fastForward(120_001)
    let releaseGate
    gate = new Promise(resolve => { releaseGate = resolve })
    const staleRequest = page.waitForRequest(req => new URL(req.url()).pathname === '/api/admin/emby/missing')
    await page.getByRole('button', { name: '缺集结果', exact: true }).evaluate(el => el.click()); await staleRequest
    assert.equal(await cards.count(), 24)
    assert.equal(await pageInput().inputValue(), '5')
    assert.equal(await page.getByRole('button', { name: '刷新检测结果' }).isDisabled(), true)
    releaseGate(); gate = undefined; await idle()
    const beforeRefresh = listReads()
    await page.getByRole('button', { name: '刷新检测结果' }).click(); await idle()
    assert.equal(listReads(), beforeRefresh + 1)
    // Pending view loads must not overwrite the other view's search draft.
    await resultSearch().fill('保留结果草稿')
    excludedDelay = 400
    const pendingExcluded = page.waitForRequest(req => new URL(req.url()).pathname.endsWith('/missing/exclusions'))
    await page.getByRole('button', { name: /^已排除 \d+ 项$/ }).evaluate(el => el.click()); await pendingExcluded
    await resultTab()
    assert.equal(await resultSearch().inputValue(), '保留结果草稿')
    excludedDelay = 0
    // Explicit synchronization and saved scope changes invalidate both views.
    await page.getByRole('link', { name: '媒体库同步', exact: true }).click()
    await page.locator('.library-option').last().waitFor()
    await page.getByRole('button', { name: '开始全量同步', exact: true }).click()
    await page.getByText('已加入后台队列，离开页面也会继续。', { exact: true }).waitFor()
    const beforeSyncReturn = listReads()
    await page.goBack(); await idle()
    assert.equal(listReads(), beforeSyncReturn + 1)
    await page.getByRole('link', { name: '媒体库同步', exact: true }).click()
    await page.locator('.library-option').last().waitFor()
    await page.locator('.library-option').filter({ hasText: '电影' }).locator('input').uncheck()
    await page.locator('.library-save-row button[type=submit]').click()
    await page.getByText('连接与范围已保存。首次使用或调整范围后，请执行全量同步。', { exact: true }).waitFor()
    const beforeScopeReturn = listReads()
    await page.goBack(); await idle()
    assert.equal(listReads(), beforeScopeReturn + 1)
    statusSyncedAt = '2026-10-16T04:00:00Z'
    await page.getByRole('link', { name: '媒体库同步', exact: true }).click()
    await page.getByText('已建立索引', { exact: true }).first().waitFor()
    const beforeCompletedSync = listReads()
    await page.goBack(); await idle()
    assert.equal(listReads(), beforeCompletedSync + 1, 'observing a completed snapshot also invalidates cached pages')
    // Nine distinct pages evict the least recently visited of the eight-page data cache.
    await page.getByRole('button', { name: '刷新检测结果' }).click(); await idle()
    for (const value of [1, 2, 3, 4, 6, 7, 8, 9]) await jump(value)
    const beforeEvictedPage = listReads()
    await jump(8)
    assert.equal(listReads(), beforeEvictedPage)
    await jump(5)
    assert.equal(listReads(), beforeEvictedPage + 1)
    assert.ok(await page.evaluate(() => window.posterObjects.size <= 48))
    fs.mkdirSync(path.join(__dirname, '../test-results'), { recursive: true })
    for (const theme of ['light', 'dark']) {
      await page.evaluate(theme => document.documentElement.classList.toggle('dark', theme === 'dark'), theme)
      for (const width of [1440, 390, 320]) {
        await page.setViewportSize({ width, height: 844 }); await pager().scrollIntoViewIfNeeded()
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
        for (const button of await pager().getByRole('button').all()) {
          const bounds = await button.boundingBox(); assert.ok(bounds.width >= 44 && bounds.height >= 44)
        }
        await page.screenshot({ path: path.join(__dirname, `../test-results/missing-pagination-${theme}-${width}.png`) })
      }
    }
    // Unmounted page expires after two minutes; returning still remembers its location.
    await leave(); assert.ok(await page.evaluate(() => window.posterObjects.size > 0))
    await page.clock.fastForward(120_001)
    assert.equal(await page.evaluate(() => window.posterObjects.size), 0)
    const beforeExpiredReturn = listReads()
    await enter()
    assert.equal(await pageInput().inputValue(), '5')
    assert.equal(listReads(), beforeExpiredReturn + 1)
    await cards.first().locator('img[alt]:not([alt=""])').waitFor()
    await leave()
    // Log out through the actual account menu while the detection view is unmounted.
    await page.getByRole('button', { name: '更多功能', exact: true }).click()
    await page.getByRole('button', { name: '退出登录', exact: true }).click()
    await page.waitForURL('**/admin/login')
    assert.equal(await page.evaluate(() => window.posterObjects.size), 0)
    assert.deepEqual(errors, [])
    console.log('Emby missing cache UI: independent pages/search/drafts/scroll, SPA/history reuse without list or image requests, strict pager input, failed target retention, stale refresh, refresh/config/sync invalidation, LRU bounds, idle/logout cleanup and mobile 44px controls passed')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
