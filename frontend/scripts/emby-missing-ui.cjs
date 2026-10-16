const path = require('node:path'), os = require('node:os'), fs = require('node:fs'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    await context.addInitScript(() => {
      window.posterObjects = new Set()
      const create = URL.createObjectURL.bind(URL), revoke = URL.revokeObjectURL.bind(URL)
      URL.createObjectURL = blob => { const url = create(blob); window.posterObjects.add(url); return url }
      URL.revokeObjectURL = url => { window.posterObjects.delete(url); revoke(url) }
    })
    const page = await context.newPage(), errors = [], requests = []
    page.on('pageerror', error => errors.push(error.message))
    const season = (number = 1) => ({ season: number, lastEpisode: 8, ownedCount: 7, missingCount: 1, ranges: [{ first: 5, last: 5 }] })
    const items = Array.from({ length: 26 }, (_, i) => ({ key: `tmdb:${i + 1}`, tmdbId: String(i + 1), title: `示例剧${String(i + 1).padStart(2, '0')}`, posterUrl: `https://image.tmdb.org/t/p/w500/test-${i + 1}.jpg`, missingCount: 1, seasons: [season()] }))
    const embyPoster = id => `/api/admin/emby/posters/${'a'.repeat(64)}/${id}`
    items[1] = { ...items[1], title: '多季与长缺号测试', missingCount: 8, seasons: [{ ...season(), lastEpisode: 20, missingCount: 5, ranges: [2, 4, 6, 8, 10].map(first => ({ first, last: first })) }, season(2), season(3), season(4)] }
    items[2] = { ...items[2], key: 'emby:local', tmdbId: '', title: '本地剧集没有TMDB编号也应保留检测结果', posterUrl: embyPoster('local') }
    items[3] = { ...items[3], title: '很长的剧名也不能撑高卡片'.repeat(8), missingCount: 30, seasons: Array.from({ length: 30 }, (_, i) => season(i + 1)) }
    items[4].posterUrl = embyPoster('missing')
    items[20].posterUrl = embyPoster('offscreen')
    let fail = false, ready = true, configured = true, empty = false, checkedSeries = 100, notices = [], delay = 0, posterDelay = 0
    let exclusions = [], previewFail = false, excludeFail = false, restoreFail = false, writeDelay = 0
    let posterGate
    const service = 's'.repeat(64), scope = 'a'.repeat(64), revision = 'r'.repeat(64)
    const previewFor = key => {
      const item = items.find(item => item.key === key)
      return { scope, key, revision, title: item.title, missingCount: item.missingCount, sources: Array.from({ length: key === 'tmdb:2' ? 2 : 1 }, (_, i) => ({ seriesId: `series-${key}-${i}`, tmdbId: item.tmdbId, title: item.title, libraryId: 'tv-library', episodeCount: 3 })) }
    }
    await page.route('**/api/**', async route => {
      const req = route.request(), url = new URL(req.url()), p = url.pathname
      requests.push({ p, method: req.method(), auth: req.headers().authorization })
      if (p.includes('/tmdb/image/') || p.includes('/emby/posters/')) {
        if (p === embyPoster('missing')) return route.fulfill({ status: 404 })
        if (p === embyPoster('blocked') && posterGate) await posterGate
        if (/test-(25|26)\.jpg$/.test(p)) await new Promise(resolve => setTimeout(resolve, posterDelay))
        return route.fulfill({ status: 200, contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="160" height="240"><rect width="160" height="240" fill="#85714b"/></svg>' })
      }
      let data = {}, code = 200
      if (p === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
      else if (p === '/api/admin/emby/missing/preview') {
        data = previewFor(url.searchParams.get('key'))
        if (previewFail) { code = 500; data = { error: '模拟来源读取失败' } }
      } else if (p === '/api/admin/emby/missing/exclusions' && req.method() === 'POST') {
        const input = req.postDataJSON()
        assert.equal(input.scope, scope); assert.equal(input.revision, revision)
        await new Promise(resolve => setTimeout(resolve, writeDelay))
        if (excludeFail) { code = 409; data = { error: '条目或检测范围已变化，请返回列表刷新后重新操作' } }
        else {
          const preview = previewFor(input.key)
          for (const [i, source] of preview.sources.entries()) exclusions.push({ ...source, key: input.key, id: `${input.key}-${i}`, excludedAt: '2026-10-16T05:00:00Z' })
          data = { excluded: preview.sources.length }
        }
      } else if (p === '/api/admin/emby/missing/restore') {
        const input = req.postDataJSON(); assert.equal(input.service, service)
        await new Promise(resolve => setTimeout(resolve, writeDelay))
        if (restoreFail) { code = 500; data = { error: '模拟恢复失败' } }
        else { exclusions = exclusions.filter(item => item.id !== input.id); data = { restored: true } }
      } else if (p === '/api/admin/emby/missing/exclusions') {
        const q = url.searchParams.get('q') || ''
        const filtered = exclusions.filter(item => !q || item.title.includes(q) || item.tmdbId.includes(q))
        const offset = filtered.length ? Math.min(Number(url.searchParams.get('offset') || 0), Math.floor((filtered.length - 1) / 24) * 24) : 0
        data = { service, count: exclusions.length, total: filtered.length, offset, nextOffset: offset + 24, hasMore: filtered.length > offset + 24, items: filtered.slice(offset, offset + 24) }
      } else if (p === '/api/admin/emby/missing') {
        const q = url.searchParams.get('q') || ''
        let filtered = empty ? [] : items.filter(item => !exclusions.some(excluded => excluded.key === item.key) && (!q || item.title.includes(q) || item.tmdbId.includes(q)))
        if (!ready) filtered = []
        const offset = filtered.length ? Math.min(Number(url.searchParams.get('offset') || 0), Math.floor((filtered.length - 1) / 24) * 24) : 0
        data = { configured, ready, notices, excludedCount: exclusions.length, syncedAt: '2026-10-16T02:00:00Z', checkedSeries, total: filtered.length, offset, nextOffset: offset + 24, hasMore: filtered.length > offset + 24, summary: { series: filtered.length, seasons: filtered.reduce((n, x) => n + x.seasons.length, 0), episodes: filtered.reduce((n, x) => n + x.missingCount, 0) }, items: filtered.slice(offset, offset + 24) }
        if (fail) { code = 500; data = { error: '模拟读取失败' } }
        await new Promise(resolve => setTimeout(resolve, q === '慢请求' ? 500 : delay))
      }
      await route.fulfill({ status: code, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const base = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    await page.goto(base + '/emby')
    await page.getByRole('link', { name: /缺集检测 按季检查/ }).click()
    const cards = page.locator('[data-missing-card]')
    await cards.nth(23).waitFor()
    assert.equal(await cards.count(), 24)
    await cards.first().locator('img[alt="示例剧01"]').waitFor()
    assert.equal(await cards.first().locator('img[alt="示例剧01"]').evaluate(img => img.complete && img.naturalWidth > 0), true)
    assert.equal(requests.filter(r => r.p === '/api/tmdb/image/w185/test-1.jpg').length, 1, 'poster and ambient background must download one thumbnail')
    assert.equal(requests.some(r => r.p.includes('/tmdb/image/w500/')), false, 'compact cards must not download w500 posters')
    assert.equal(await cards.first().locator('img').evaluateAll(images => images.length === 2 && images.every(img => img.src === images[0].src && img.src.startsWith('blob:'))), true, 'both image layers share one blob')
    const firstBlob = await cards.first().locator('img[alt="示例剧01"]').getAttribute('src')
    await cards.nth(2).locator('img[alt]:not([alt=""])').waitFor()
    assert.equal(requests.filter(r => r.p === embyPoster('local')).length, 1)
    assert.equal(requests.find(r => r.p === embyPoster('local')).auth, 'Bearer mock', 'only authenticated requests may load Emby thumbnails')
    assert.equal(requests.filter(r => r.p === embyPoster('offscreen')).length, 0, 'offscreen posters must not be prefetched')
    await cards.nth(20).scrollIntoViewIfNeeded()
    await cards.nth(20).locator('img[alt]:not([alt=""])').waitFor()
    assert.equal(requests.filter(r => r.p === embyPoster('offscreen')).length, 1, 'scrolling into view loads the poster')
    await page.evaluate(() => window.scrollTo(0, 0))
    assert.match(await cards.first().innerText(), /截至第 8 集/)
    assert.match(await cards.first().innerText(), /缺第5集/)
    assert.equal(await cards.first().getByRole('link', { name: '搜索资源', exact: true }).getAttribute('href'), '/tv/1?botSearch=1')
    assert.equal(await cards.nth(2).getByRole('link').count(), 0)
    const multi = cards.nth(1)
    assert.equal(await multi.getByRole('heading', { name: '第 3 季', exact: true }).count(), 0)
    const originalHeight = (await multi.boundingBox()).height
    const dialog = page.getByRole('dialog', { name: '缺集明细', exact: true })
    const details = multi.getByRole('button', { name: '缺集明细', exact: true })
    await details.click()
    await dialog.waitFor()
    assert.equal(await dialog.getByRole('heading', { name: '第 3 季', exact: true }).count(), 1)
    assert.equal(await dialog.getByText('缺第2集、第4集、第6集、第8集、第10集', { exact: true }).count(), 1)
    assert.equal((await multi.boundingBox()).height, originalHeight, 'opening details must not enlarge the grid row')
    for (let i = 0; i < 5; i++) {
      await page.keyboard.press('Tab')
      assert.equal(await dialog.evaluate(el => el.contains(document.activeElement) || document.activeElement === document.body), true, 'tabbing must not focus background page controls')
    }
    await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' })
    assert.equal(await details.evaluate(el => el === document.activeElement), true, 'closing details restores focus to its trigger')
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    await cards.nth(2).getByRole('button', { name: '缺集明细', exact: true }).click()
    await dialog.waitFor()
    assert.equal(await dialog.getByText(items[2].title, { exact: true }).count(), 1, 'local shows retain full details')
    await dialog.getByRole('button', { name: '关闭缺集明细' }).click()
    await dialog.waitFor({ state: 'hidden' })
    delay = 100; posterDelay = 250
    await page.evaluate(() => {
      window.paginationFrames = []; window.probingPagination = true
      const sample = () => {
        const grid = document.querySelector('[data-missing-grid]'), card = grid?.querySelector('[data-missing-card]')
        const image = card?.querySelector('img[alt]:not([alt=""])')
        window.paginationFrames.push({ title: card?.querySelector('h2')?.textContent?.trim(), top: Math.round(grid?.getBoundingClientRect().top || 0), ready: !!image?.complete && image.naturalWidth > 0 })
        if (window.probingPagination) requestAnimationFrame(sample)
      }
      sample()
    })
    await page.getByRole('button', { name: '下一页', exact: true }).click()
    await cards.filter({ hasText: '示例剧25' }).waitFor()
    await page.evaluate(() => new Promise(requestAnimationFrame))
    const paginationFrames = await page.evaluate(() => { window.probingPagination = false; return window.paginationFrames })
    assert.equal(new Set(paginationFrames.map(frame => frame.top)).size, 1, 'loading must never push the grid down and back up')
    assert.equal(paginationFrames.filter(frame => frame.title === '示例剧25').every(frame => frame.ready), true, 'the incoming page must show decoded visible posters on its first painted frame')
    assert.equal(await page.evaluate(url => window.posterObjects.has(url), firstBlob), true, 'recent pages retain their decoded poster for flicker-free back navigation')
    assert.equal(await cards.count(), 2)
    assert.equal(new URL(page.url()).searchParams.get('offset'), '24')
    await page.getByRole('button', { name: '上一页', exact: true }).click()
    await cards.nth(23).waitFor()
    assert.equal(await cards.first().locator('img[alt="示例剧01"]').getAttribute('src'), firstBlob, 'back navigation must reuse the decoded image URL')
    assert.equal(requests.filter(r => r.p === '/api/tmdb/image/w185/test-1.jpg').length, 1, 'back navigation must not fetch the same thumbnail again')
    assert.equal(requests.filter(r => r.p === '/api/tmdb/image/w185/test-25.jpg').length, 1, 'preparing the incoming page and mounting it must share one fetch')
    assert.equal(requests.filter(r => r.p === embyPoster('missing')).length, 1, 'known missing posters must not retry on pagination')
    delay = 0; posterDelay = 0
    // Slow or cancelled image preparation must not block text or overwrite a later search.
    const originalPoster = items[24].posterUrl
    items[24].posterUrl = embyPoster('blocked')
    let unblockPoster
    posterGate = new Promise(resolve => { unblockPoster = resolve })
    const slowStart = Date.now()
    await page.getByRole('button', { name: '下一页', exact: true }).click()
    await cards.filter({ hasText: '示例剧25' }).waitFor()
    assert.ok(Date.now() - slowStart < 2400, 'slow posters must not delay pagination beyond its 1500ms preparation budget')
    assert.equal(requests.filter(r => r.p === embyPoster('blocked')).length, 1, 'a slow prepared image and its card must share the in-flight request')
    await page.getByRole('button', { name: '上一页', exact: true }).click()
    await cards.nth(23).waitFor()
    await page.getByRole('button', { name: '下一页', exact: true }).click()
    await page.waitForRequest(req => new URL(req.url()).pathname === embyPoster('blocked'))
    const changingSearch = page.getByRole('searchbox', { name: '搜索缺集剧集' })
    await changingSearch.fill('示例剧01'); await changingSearch.press('Enter')
    await page.getByText('当前结果：示例剧01', { exact: false }).waitFor()
    unblockPoster(); posterGate = undefined; items[24].posterUrl = originalPoster
    await page.evaluate(() => new Promise(requestAnimationFrame))
    assert.equal(await cards.count(), 1, 'cancelled preparation must not replace a newer search')
    assert.equal(await cards.first().locator('img[alt="示例剧01"]').getAttribute('src'), firstBlob)
    await page.getByRole('button', { name: '清除搜索', exact: true }).click()
    await cards.nth(23).waitFor()
    const search = page.getByRole('searchbox', { name: '搜索缺集剧集' })
    await search.fill('示例剧01'); await search.press('Enter')
    await page.getByText('当前结果：示例剧01', { exact: false }).waitFor()
    assert.equal(await cards.count(), 1)
    await page.reload(); await cards.first().waitFor()
    assert.equal(await search.inputValue(), '示例剧01')
    fail = true; await page.getByRole('button', { name: '刷新检测结果' }).click()
    await page.getByRole('alert').waitFor()
    assert.match(await page.getByRole('alert').innerText(), /当前保留上次结果/)
    assert.equal(await cards.count(), 1)
    fail = false; await page.getByRole('button', { name: '重新读取列表', exact: true }).click()
    await page.getByRole('alert').waitFor({ state: 'hidden' })
    await search.fill('慢请求'); await search.press('Enter')
    await search.fill('示例剧01'); await search.press('Enter')
    await page.getByRole('button', { name: '刷新检测结果' }).waitFor({ state: 'visible' })
    await new Promise(resolve => setTimeout(resolve, 650))
    assert.equal(await cards.count(), 1, 'a late response must not overwrite the newer search')
    await search.fill('不存在'); await search.press('Enter')
    await page.getByRole('heading', { name: '未找到匹配的缺集剧集' }).waitFor()
    await page.getByRole('button', { name: '清除搜索条件' }).click()
    await cards.nth(23).waitFor()
    await details.click(); await dialog.waitFor()
    await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' })
    await page.getByRole('button', { name: '刷新检测结果' }).click()
    await page.getByRole('button', { name: '刷新检测结果' }).waitFor({ state: 'visible' })
    assert.equal(await dialog.count(), 0)
    await search.fill('示例剧01'); await search.press('Enter')
    await page.getByText('当前结果：示例剧01', { exact: false }).waitFor()
    await page.getByRole('button', { name: '清除搜索', exact: true }).click()
    await cards.nth(23).waitFor()
    assert.equal(new URL(page.url()).searchParams.has('q'), false, 'clearing the input immediately clears the active filter')
    // Manual exclusion: preview only, cancel/focus, stale failure, busy submit and restore.
    const confirmDialog = page.getByRole('dialog', { name: '确认排除该剧集？', exact: true })
    await cards.first().getByRole('button', { name: '缺集明细', exact: true }).click()
    previewFail = true
    await dialog.getByRole('button', { name: '从缺集检测移除', exact: true }).click()
    await confirmDialog.getByRole('alert').waitFor()
    assert.match(await confirmDialog.getByRole('alert').innerText(), /模拟来源读取失败/)
    assert.equal(await confirmDialog.getByRole('button', { name: '确认排除', exact: true }).isDisabled(), true)
    previewFail = false
    await confirmDialog.getByRole('button', { name: '重试读取来源' }).click()
    await confirmDialog.getByText('已入库正式分集记录：3 条').waitFor()
    assert.equal(await confirmDialog.getByText('Emby Series ID：series-tmdb:1-0').count(), 1)
    assert.match(await confirmDialog.innerText(), /不是待删除文件数/)
    assert.equal(requests.some(r => r.method === 'POST'), false, 'opening a preview never changes any data')
    await confirmDialog.getByRole('button', { name: '取消，返回明细' }).click()
    await dialog.waitFor()
    assert.equal(await dialog.getByRole('button', { name: '从缺集检测移除' }).evaluate(el => el === document.activeElement), true)
    await dialog.getByRole('button', { name: '从缺集检测移除' }).click()
    await confirmDialog.getByText('已入库正式分集记录：3 条').waitFor()
    assert.equal(await confirmDialog.getByRole('button', { name: '取消，返回明细' }).evaluate(el => el === document.activeElement), true, 'confirm view must focus the safe cancel action')
    excludeFail = true
    await confirmDialog.getByRole('button', { name: '确认排除', exact: true }).click()
    await confirmDialog.getByRole('alert').waitFor()
    assert.match(await confirmDialog.getByRole('alert').innerText(), /条目或检测范围已变化/)
    assert.equal(await confirmDialog.getByRole('button', { name: '确认排除', exact: true }).isDisabled(), true, 'a failed write requires a fresh source preview')
    excludeFail = false; writeDelay = 250
    await confirmDialog.getByRole('button', { name: '重试读取来源' }).click()
    await confirmDialog.getByText('已入库正式分集记录：3 条').waitFor()
    const beforeWrites = requests.filter(r => r.method === 'POST').length
    await confirmDialog.getByRole('button', { name: '确认排除', exact: true }).evaluate(el => { el.click(); el.click() })
    await confirmDialog.getByRole('button', { name: '正在排除' }).waitFor()
    assert.equal(await confirmDialog.getByRole('button', { name: '关闭确认排除该剧集？' }).isDisabled(), true)
    await page.keyboard.press('Escape')
    assert.equal(await confirmDialog.isVisible(), true, 'busy confirmation must stay open')
    await confirmDialog.waitFor({ state: 'hidden' })
    await page.getByRole('button', { name: '已排除 1 项', exact: true }).waitFor()
    assert.equal(requests.filter(r => r.method === 'POST').length, beforeWrites + 1, 'double submit must produce one write')
    assert.equal(await cards.filter({ hasText: '示例剧01' }).count(), 0)
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    assert.match(await page.locator(':focus').innerText(), /已从缺集检测排除/)
    await page.getByRole('button', { name: '已排除 1 项', exact: true }).click()
    const excludedRows = page.locator('[data-excluded-item]')
    await excludedRows.first().waitFor()
    assert.match(await excludedRows.first().innerText(), /tv-library/)
    restoreFail = true
    await excludedRows.first().getByRole('button', { name: '恢复示例剧01的检测' }).click()
    await page.getByRole('alert').waitFor()
    assert.match(await page.getByRole('alert').innerText(), /模拟恢复失败/)
    assert.equal(await excludedRows.count(), 1)
    restoreFail = false
    await excludedRows.first().getByRole('button', { name: '恢复示例剧01的检测' }).evaluate(el => { el.click(); el.click() })
    await page.getByRole('heading', { name: '暂无手动排除记录' }).waitFor()
    assert.equal(await page.getByRole('button', { name: '返回缺集检测' }).isVisible(), true)
    await page.getByRole('button', { name: '返回缺集检测' }).click()
    await cards.nth(23).waitFor()
    assert.equal(await cards.filter({ hasText: '示例剧01' }).count(), 1)
    // Search persists between views; excluded pagination clamps after restoring its final row.
    exclusions = Array.from({ length: 25 }, (_, i) => ({ ...previewFor('tmdb:1').sources[0], id: `seed-${i}`, title: `排除示例${String(i).padStart(2, '0')}`, tmdbId: String(300 + i), excludedAt: '2026-10-16T05:00:00Z' }))
    await page.getByRole('button', { name: '已排除 0 项', exact: true }).click()
    await excludedRows.nth(23).waitFor()
    assert.equal(await excludedRows.count(), 24)
    const excludedSearch = page.getByRole('searchbox', { name: '搜索已排除条目' })
    await excludedSearch.fill('排除示例00'); await excludedSearch.press('Enter')
    await page.getByText('当前搜索：排除示例00', { exact: false }).waitFor()
    assert.equal(await excludedRows.count(), 1)
    await page.getByRole('button', { name: '缺集结果', exact: true }).click()
    await page.getByRole('heading', { name: '未找到匹配的缺集剧集' }).waitFor()
    assert.equal(await search.inputValue(), '排除示例00')
    await page.getByRole('button', { name: '已排除 25 项', exact: true }).click()
    await excludedRows.first().waitFor()
    assert.equal(await excludedSearch.inputValue(), '排除示例00')
    await page.getByRole('button', { name: '清除搜索', exact: true }).click()
    await excludedRows.nth(23).waitFor()
    await page.getByRole('button', { name: '下一页', exact: true }).click()
    await excludedRows.filter({ hasText: '排除示例24' }).waitFor()
    assert.equal(await excludedRows.count(), 1)
    await excludedRows.first().getByRole('button').click()
    await excludedRows.nth(23).waitFor()
    assert.equal(await page.getByText('第 2 页', { exact: true }).count(), 0)
    assert.equal(new URL(page.url()).searchParams.has('offset'), false, 'restore must normalize the route when the final page disappears')
    assert.equal(await page.getByRole('navigation', { name: '排除记录分页' }).count(), 0, 'pagination disappears when restoring leaves one full page')
    exclusions = []; writeDelay = 0
    await page.getByRole('button', { name: '缺集结果', exact: true }).click()
    await cards.nth(23).waitFor()
    // Metadata is an optional neutral disclosure; actual index failures stay visible.
    notices = [{ code: 'metadata_incomplete', level: 'info', count: 8, message: '8 条资料补查未完成，可能涉及 TMDB 编号或分集信息；检测依据当前本地索引。' }, { code: 'episodes_incomplete', level: 'warning', count: 6, message: '6 条分集记录缺少剧集归属、季号或集号，未参与缺集检测。' }]
    await page.getByRole('button', { name: '刷新检测结果' }).click()
    const metadata = page.getByRole('button', { name: '资料待补充', exact: true })
    await metadata.waitFor()
    assert.equal(await page.getByRole('status').getByText(notices[0].message, { exact: true }).count(), 0, 'metadata must not become a global warning')
    assert.equal(await page.getByRole('status').getByText(notices[1].message, { exact: true }).count(), 0, 'incomplete episode information must not occupy a banner')
    const statusPanel = page.getByRole('dialog', { name: '索引状态', exact: true })
    const gridBeforeStatus = await page.locator('[data-missing-grid]').boundingBox(), requestsBeforeStatus = requests.length
    await metadata.click(); await statusPanel.waitFor()
    assert.equal(await statusPanel.getByText(notices[0].message, { exact: true }).isVisible(), true)
    assert.equal(await statusPanel.getByText(notices[1].message, { exact: true }).isVisible(), true, 'skipped episode records must remain discoverable')
    assert.equal((await page.locator('[data-missing-grid]').boundingBox()).y, gridBeforeStatus.y, 'status details must not move the cards')
    assert.equal(requests.length, requestsBeforeStatus, 'opening status must not make extra requests')
    await page.keyboard.press('Escape'); await statusPanel.waitFor({ state: 'hidden' })
    assert.equal(await metadata.evaluate(el => el === document.activeElement), true, 'closing status restores focus')
    await metadata.click(); await statusPanel.waitFor()
    await page.keyboard.press('Tab'); await page.keyboard.press('Tab')
    await statusPanel.waitFor({ state: 'hidden' })
    assert.equal(await page.evaluate(() => !!document.activeElement?.closest('[popover]')), false, 'tabbing out of a nonmodal disclosure closes it without trapping focus')
    await metadata.click(); await statusPanel.waitFor()
    await page.getByRole('heading', { name: '缺集检测', exact: true }).click()
    await statusPanel.waitFor({ state: 'hidden' })
    notices = [{ code: 'sync_failed', level: 'warning', message: '最近同步失败，当前显示上次完整索引。' }, ...notices]
    await page.getByRole('button', { name: '刷新检测结果' }).click()
    await page.getByRole('status').getByText(notices[0].message, { exact: true }).waitFor()
    assert.equal(await cards.count(), 24, 'sync failure must not discard the published results')
    notices = notices.slice(1)
    await page.getByRole('button', { name: '刷新检测结果' }).click()
    await page.getByRole('status').getByText('最近同步失败，当前显示上次完整索引。', { exact: true }).waitFor({ state: 'hidden' })
    fs.mkdirSync(path.join(__dirname, '../test-results'), { recursive: true })
    for (const theme of ['light', 'dark']) {
      await page.evaluate(theme => document.documentElement.classList.toggle('dark', theme === 'dark'), theme)
      for (const [width, height] of [[1440, 1000], [768, 1024], [390, 844], [320, 780]]) {
        await page.setViewportSize({ width, height }); await page.evaluate(() => window.scrollTo(0, 0))
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${theme} ${width}`)
        await metadata.click(); await statusPanel.waitFor()
        const statusBounds = await statusPanel.boundingBox()
        assert.ok(statusBounds.x >= 0 && statusBounds.x + statusBounds.width <= width && statusBounds.y >= 0 && statusBounds.y + statusBounds.height <= height, 'status disclosure must fit every viewport')
        assert.equal(await statusPanel.evaluate(el => el.scrollWidth <= el.clientWidth), true)
        if (width === 320) await page.screenshot({ path: path.join(__dirname, `../test-results/missing-status-${theme}-${width}.png`) })
        await statusPanel.getByRole('button', { name: '关闭索引状态' }).click(); await statusPanel.waitFor({ state: 'hidden' })
        const heights = await cards.evaluateAll(elements => elements.map(el => Math.round(el.getBoundingClientRect().height)))
        assert.ok(Math.max(...heights) <= 150, `${theme} ${width}: compact cards must stay near 144px, got ${heights}`)
        const links = cards.first().getByRole('link')
        assert.ok(await links.first().isVisible())
        await cards.nth(3).getByRole('button', { name: '缺集明细', exact: true }).click()
        await dialog.waitFor()
        assert.equal(await dialog.getByRole('heading', { name: '第 30 季', exact: true }).count(), 1)
        assert.equal(await dialog.evaluate(el => el.scrollHeight > el.clientHeight && el.scrollWidth <= el.clientWidth), true, 'long details scroll vertically without horizontal overflow')
        const bounds = await dialog.boundingBox()
        assert.ok(bounds.x >= 0 && bounds.x + bounds.width <= width && bounds.y >= 0 && bounds.y + bounds.height <= height)
        await dialog.getByRole('button', { name: '从缺集检测移除', exact: true }).click()
        await confirmDialog.getByText('已入库正式分集记录：3 条').waitFor()
        assert.equal(await confirmDialog.evaluate(el => el.scrollWidth <= el.clientWidth), true, 'confirmation must wrap long source titles on narrow screens')
        if (width === 320) await page.screenshot({ path: path.join(__dirname, `../test-results/missing-confirm-${theme}-${width}.png`) })
        await page.keyboard.press('Escape'); await confirmDialog.waitFor({ state: 'hidden' })
        await page.evaluate(() => window.scrollTo(0, 0))
        await page.screenshot({ path: path.join(__dirname, `../test-results/missing-${theme}-${width}.png`) })
      }
    }
    // Exercise eviction over several full pages, then check actual SPA unmount cleanup.
    await page.setViewportSize({ width: 1440, height: 5000 })
    for (let i = 27; i <= 90; i++) items.push({ key: `tmdb:${i}`, tmdbId: String(i), title: `缓存测试${i}`, posterUrl: `https://image.tmdb.org/t/p/w500/cache-${i}.jpg`, missingCount: 1, seasons: [season()] })
    await page.getByRole('button', { name: '刷新检测结果' }).click()
    await page.getByRole('button', { name: '刷新检测结果' }).waitFor()
    for (let index = 1; index <= 3; index++) {
      await page.getByRole('button', { name: '下一页', exact: true }).click()
      await cards.first().getByRole('heading', { name: items[index * 24].title, exact: true }).waitFor()
      assert.ok(await page.evaluate(() => window.posterObjects.size <= 48), 'LRU retention must not exceed two full pages')
      assert.equal(await cards.evaluateAll(elements => elements.every(el => { const img = el.querySelector('img[alt]:not([alt=""])'); return img?.complete && img.naturalWidth > 0 })), true, 'eviction must never revoke visible posters')
    }
    assert.equal(await page.evaluate(url => window.posterObjects.has(url), firstBlob), false, 'least recently used unmounted posters must be revoked')
    assert.ok(await page.evaluate(() => window.posterObjects.size > 0))
    await page.getByRole('link', { name: 'Emby', exact: true }).click()
    assert.equal(await page.evaluate(() => window.posterObjects.size), 0, 'SPA navigation must release every cached poster')
    await page.getByRole('link', { name: /缺集检测 按季检查/ }).click()
    await cards.nth(23).waitFor()
    ready = false; configured = false; await page.reload()
    await page.getByRole('heading', { name: '尚未配置需要同步的媒体库' }).waitFor()
    assert.equal(await page.getByText('当前索引未检测到缺集', { exact: true }).count(), 0)
    configured = true; await page.getByRole('button', { name: '刷新检测结果' }).click()
    await page.getByRole('heading', { name: '当前媒体库索引尚未建立' }).waitFor()
    ready = true; empty = true; notices = [{ code: 'sync_running', level: 'info', message: '媒体库同步中，当前显示上次完整索引的检测结果。' }]
    await page.getByRole('button', { name: '刷新检测结果' }).click()
    await page.getByRole('heading', { name: '当前索引未检测到缺集' }).waitFor()
    await page.getByRole('button', { name: '媒体库同步中', exact: true }).click()
    await statusPanel.waitFor()
    assert.equal(await statusPanel.getByText(notices[0].message, { exact: true }).isVisible(), true)
    await page.keyboard.press('Escape')
    checkedSeries = 0; await page.getByRole('button', { name: '刷新检测结果' }).click()
    await page.getByRole('heading', { name: '当前索引没有可检测的正式分集' }).waitFor()
    fail = true; await page.reload(); await page.getByRole('alert').waitFor()
    assert.equal(await cards.count(), 0)
    assert.ok(await page.evaluate(() => window.posterObjects.size <= 48), 'recent-page poster retention must remain bounded')
    await page.getByRole('link', { name: 'Emby', exact: true }).click()
    assert.equal(await page.evaluate(() => window.posterObjects.size), 0, 'leaving detection must release every cached poster')
    assert.equal(requests.some(r => r.method !== 'GET' && !['/api/admin/emby/missing/exclusions', '/api/admin/emby/missing/restore'].includes(r.p)), false, 'manual exclusions must never submit a scan or remote deletion')
    assert.deepEqual(errors, [])
    console.log('Emby missing UI: neutral metadata disclosure, visible sync failures, keyboard/outside dismissal, viewport bounds, no extra status requests, flicker-free pagination, bounded poster cache/SPA cleanup, exclusions, light/dark and 320/390/768/1440 widths passed')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
