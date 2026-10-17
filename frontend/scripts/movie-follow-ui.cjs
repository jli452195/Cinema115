// All API requests are mocked; this suite never creates real follows or changes a media library.
const path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const errors = [], passed = [], requests = []
  try {
    await fs.mkdir('test-results', { recursive: true })
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
    const page = await context.newPage()
    page.setDefaultTimeout(10000)
    page.on('pageerror', e => errors.push(e.message))
    await page.clock.install()
    let library = 'absent', followLibrary = 'absent', subscribed = false, status = 'pending', note = ''
    let failState = false, failMedia = false, failCancel = false, loseSubmit = false
    let holdRead = false, releaseRead, holdMedia = false, releaseMedia, holdSubmit = false, releaseSubmit
    const current = () => ({ subscribed, libraryState: followLibrary, request: subscribed ? { kind: 'movie', tmdbId: 1452, title: '超人归来', status, effectiveStatus: status, note, libraryState: followLibrary, subscribedAt: '2026-10-17', progress: null } : null })
    await page.route('**/api/**', async route => {
      const request = route.request(), endpoint = new URL(request.url()).pathname, method = request.method()
      requests.push({ endpoint, method })
      let code = 200, data = { configured: true, accounts: [] }
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock', username: 'mock', role: 'admin', disabled: false } }
      else if (endpoint.includes('/tmdb/detail/')) {
        const kind = endpoint.includes('/movie/') ? 'movie' : 'tv', id = Number(endpoint.split('/').pop())
        data = { id, kind, title: kind === 'movie' ? id === 1452 ? '超人归来' : '另一部电影' : '测试剧集', originalTitle: 'Superman Returns', overview: '当超人重新回到熟悉的世界，发现一切已经改变。', posterUrl: '', backdropUrl: '', date: '2006-06-28', rating: 5.8, votes: 4736, tagline: '', status: kind === 'movie' ? 'Released' : 'Returning Series', runtime: 154, episodeCount: 0, countries: ['United States of America'], genres: [{ id: 878, name: '科幻' }], cast: [], seasons: [], collection: null, imdbUrl: '', tmdbUrl: '' }
      } else if (endpoint.includes('/emby/media/')) {
        if (holdMedia) { holdMedia = false; await new Promise(resolve => { releaseMedia = resolve }) }
        if (failMedia) { code = 502; data = { error: '模拟媒体库读取失败' } }
        else data = { media: { kind: endpoint.includes('/movie/') ? 'movie' : 'tv', tmdbId: endpoint.split('/').pop(), state: library, episodeCount: 0, items: library === 'present' ? [{ id: 'movie-mock', type: 'Movie', name: '超人归来', season: -1, episode: -1, endEpisode: -1 }] : [] }, baseUrl: '', serverId: '' }
      } else if (endpoint.includes('/emby/versions/')) data = { versions: [], hasMore: false, nextOffset: 0, total: 0 }
      else if (endpoint.includes('/recommendations/')) data = { items: [], page: 1, totalPages: 1 }
      else if (endpoint.endsWith('/schedules')) data = { seasons: [] }
      else if (/\/follows\/(movie|tv)\/\d+(\/cancel)?$/.test(endpoint)) {
        if (method === 'POST') {
          if (endpoint.endsWith('/cancel')) {
            if (failCancel) { code = 502; data = { error: '模拟取消失败，请重试' } }
            else { subscribed = false; data = { ok: true } }
          } else {
            if (holdSubmit) { holdSubmit = false; await new Promise(resolve => { releaseSubmit = resolve }) }
            subscribed = true
            if (loseSubmit) { loseSubmit = false; await route.abort('failed'); return }
            data = current()
          }
        } else {
          data = current()
          if (holdRead) { holdRead = false; await new Promise(resolve => { releaseRead = resolve }) }
          if (failState) { code = 502; data = { error: '模拟申请状态读取失败' } }
        }
      } else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
      await route.fulfill({ status: code, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
    })
    const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    const go = async (url = '/movie/1452') => { await page.goto(origin + url, { waitUntil: 'networkidle' }); await page.locator('.detail-content').waitFor() }
    const action = () => page.locator('.movie-follow-action')
    const panel = () => page.getByRole('dialog', { name: '电影申请', exact: true })
    const waitIdle = () => page.waitForFunction(() => document.querySelector('.movie-follow-action')?.getAttribute('aria-disabled') !== 'true')
    const writes = () => requests.filter(r => r.method === 'POST').length
    const assertInside = async () => {
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
      const box = await panel().boundingBox(), size = page.viewportSize()
      assert.ok(box.x >= 0 && box.y >= 0 && box.x + box.width <= size.width + 1 && box.y + box.height <= size.height + 1, JSON.stringify(box))
    }

    await go()
    await action().getByText('申请入库', { exact: true }).waitFor()
    assert.equal(await action().locator('.tabler-icon-heart').count(), 1)
    const search = page.locator('.search-pill').first()
    const [a, b] = await Promise.all([action().boundingBox(), search.boundingBox()])
    assert.ok(b.y >= a.y + a.height + 8, 'application stays on a separate row above search pills')
    assert.equal(a.height, 36, 'movie capsule matches the TV visual height')
    await page.screenshot({ path: 'test-results/movie-follow-idle.png', fullPage: true, animations: 'disabled' })
    await action().click()
    await action().getByText('已申请', { exact: true }).waitFor()
    assert.equal(writes(), 1)
    await page.clock.runFor(1100)
    await action().hover()
    assert.equal(await action().innerText(), '已申请', 'hover must not turn into cancel')
    await action().focus(); await page.keyboard.press('Enter')
    await panel().waitFor()
    await panel().getByText('等待处理', { exact: true }).waitFor()
    assert.equal(writes(), 1, 'opening details must never cancel')
    assert.equal(await panel().getByRole('button', { name: '关闭申请详情' }).evaluate(el => el === document.activeElement), true)
    await page.keyboard.press('Escape')
    await panel().waitFor({ state: 'hidden' })
    assert.equal(await action().evaluate(el => el === document.activeElement), true)
    passed.push('standalone row, TV sizing, heart icon, apply once, stable hover and keyboard open/Escape focus')

    holdRead = true
    await action().click(); await page.waitForFunction(() => document.querySelector('.movie-follow-panel .animate-spin'))
    await panel().getByRole('button', { name: '取消申请', exact: true }).click()
    await action().getByText('申请入库', { exact: true }).waitFor()
    await panel().waitFor({ state: 'hidden' })
    assert.equal(await action().evaluate(el => el === document.activeElement), true, 'cancel restores focus during cooldown')
    assert.ok(releaseRead); releaseRead(); releaseRead = null
    await page.waitForLoadState('networkidle')
    assert.equal(await action().innerText(), '申请入库', 'a late read must not restore the cancelled subscription')
    passed.push('cancel closes details and restores focus; stale GET cannot overwrite successful cancellation')

    subscribed = true; status = 'processing'; note = '正在核对资源与字幕。'
    await go(); await action().click()
    await panel().getByText('正在处理', { exact: true }).waitFor()
    await panel().getByText(note, { exact: true }).waitFor()
    failCancel = true
    await panel().getByRole('button', { name: '取消申请', exact: true }).click()
    await panel().getByRole('alert').waitFor()
    assert.equal(await action().innerText(), '已申请')
    assert.equal(await panel().isVisible(), true)
    await page.clock.runFor(1100); failCancel = false
    await panel().getByRole('button', { name: '关闭申请详情' }).click()
    passed.push('processing note and cancellation failure preserve the subscription and a usable details panel')

    status = 'closed'; note = '当前未找到可用资源。'
    await go(); await action().getByText('申请已关闭', { exact: true }).waitFor(); await action().click()
    await panel().getByText(note, { exact: true }).waitFor()
    assert.equal(await panel().getByText('正在处理', { exact: true }).count(), 0)
    await page.keyboard.press('Escape')
    passed.push('closed requests show the actual closure reason without offering a false new request')

    library = 'present'; followLibrary = 'absent'; status = 'processing'
    await go()
    await page.locator('.movie-follow-available').waitFor()
    assert.equal(await action().count(), 0)
    assert.equal(await page.locator('.movie-follow-available').locator('.tabler-icon-heart-filled').count(), 1)
    assert.equal(await page.locator('.movie-follow-available').evaluate(el => getComputedStyle(el).opacity), '1')
    assert.equal(await page.getByRole('region', { name: '电影媒体版本' }).count(), 1)
    await page.screenshot({ path: 'test-results/movie-follow-available.png', fullPage: true, animations: 'disabled' })
    const readsBeforePoll = requests.filter(r => r.endpoint.includes('/follows/')).length
    library = 'absent'; status = 'completed'; followLibrary = 'present'
    await page.clock.runFor(30010)
    await action().getByText('已申请', { exact: true }).waitFor()
    assert.equal(requests.filter(r => r.endpoint.includes('/follows/')).length, readsBeforePoll, 'library polling does not add follow polling')
    await action().click()
    await panel().getByText('等待入库复核', { exact: true }).waitFor()
    await page.keyboard.press('Escape')
    passed.push('confirmed library overrides personal/historical state, uses a solid heart badge and follows existing polling')

    failMedia = true
    await page.clock.runFor(30010)
    await page.getByText('入库待核实', { exact: true }).waitFor()
    subscribed = false; failMedia = false; library = 'present'
    await go(); await page.locator('.movie-follow-available').waitFor()
    failMedia = true; await page.clock.runFor(30010)
    await page.getByText('状态待核实', { exact: true }).waitFor()
    holdMedia = true
    await page.getByRole('button', { name: '重新核对', exact: true }).click()
    await page.waitForLoadState('domcontentloaded')
    assert.equal(await page.locator('.movie-follow-available').count(), 0, 'retry cannot briefly revive a failed cached present result')
    failMedia = false; assert.ok(releaseMedia); releaseMedia(); releaseMedia = null
    await page.locator('.movie-follow-available').waitFor()
    passed.push('unknown library blocks new applications; retries do not revive stale cached availability')

    library = 'absent'; followLibrary = 'absent'; failState = true
    await go(); await page.getByText('状态读取失败', { exact: true }).waitFor()
    assert.equal(await action().count(), 0)
    failState = false
    await page.getByRole('button', { name: '重新核对', exact: true }).click()
    await action().getByText('申请入库', { exact: true }).waitFor()
    loseSubmit = true
    await action().click()
    await action().getByText('已申请', { exact: true }).waitFor()
    assert.equal(writes(), 4, 'lost response must reconcile through GET without a second POST')
    passed.push('failed reads retry safely; a lost submit response reconciles without duplicate submission')

    note = '超长处理说明'.repeat(160); status = 'processing'; subscribed = true
    await page.setViewportSize({ width: 320, height: 568 }); await go()
    await action().click(); await panel().getByText(note, { exact: true }).waitFor()
    await assertInside()
    await panel().getByRole('button', { name: '取消申请', exact: true }).scrollIntoViewIfNeeded()
    assert.equal(await panel().isVisible(), true, 'scrolling within details must not dismiss it')
    await page.keyboard.press('Escape')
    note = '正在核对资源与字幕。'
    await action().click(); await panel().getByText(note, { exact: true }).waitFor()
    await assertInside()
    await page.screenshot({ path: 'test-results/movie-follow-mobile.png', animations: 'disabled' })
    await page.keyboard.press('Tab'); await page.keyboard.press('Tab')
    await panel().waitFor({ state: 'hidden' })
    await action().click(); await page.locator('h1').click()
    await panel().waitFor({ state: 'hidden' })
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await action().click(); await panel().waitFor(); await assertInside()
    await page.screenshot({ path: 'test-results/movie-follow-dark.png', animations: 'disabled' })
    await page.keyboard.press('Escape')
    passed.push('320px layout, long notes, internal scrolling, Tab exit, outside click and dark mode')

    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.evaluate(() => document.documentElement.classList.remove('dark'))
    subscribed = false; holdSubmit = true
    await go(); await action().click()
    await page.getByText('提交中', { exact: true }).waitFor()
    await page.evaluate(() => document.querySelector('.media-detail').__vueParentComponent.appContext.config.globalProperties.$router.push('/movie/9999'))
    await page.locator('h1').filter({ hasText: '另一部电影' }).waitFor()
    assert.ok(releaseSubmit); releaseSubmit(); releaseSubmit = null
    await page.waitForLoadState('networkidle')
    assert.equal(await action().innerText(), '申请入库', 'old route response must not become a subscription for the next movie')
    passed.push('route changes discard late mutation responses')

    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, writes: writes(), errors }, null, 2))
    await context.close()
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
