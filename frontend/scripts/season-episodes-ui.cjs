// All API requests are intercepted; this suite never reads or changes a real library.
const path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const passed = [], errors = [], reads = []
  const state = { role: 'admin', library: 'present', fail: '', hold: '', held: new Map(), stamp: 1 }
  try {
    await fs.mkdir('test-results', { recursive: true })
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block', reducedMotion: 'reduce' })
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
    const page = await context.newPage()
    page.setDefaultTimeout(10000)
    page.on('pageerror', e => errors.push(e.message))
    const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    const episodeData = Array.from({ length: 29 }, (_, i) => ({
      id: i + 1, number: i + 1, sourceSeason: 1, sourceNumber: i + 1,
      name: i === 2 ? '这是一集很长很长很长很长的中文标题与UnbrokenLongEnglishTitleTestingOverflowHandling的分集名称' : `黑仪重蟹 其之${i + 1}`,
      date: i === 27 ? '2099-01-01' : i === 28 ? '' : '2009-07-03',
      overview: '在放学后的楼梯间，阿良良木历接住了从天而降的战场原黑仪。看似普通的相遇，却揭开了一段与怪异有关的往事。'.repeat(i === 2 ? 14 : 3),
      runtime: 25, rating: 8.6, votes: 125,
      stillUrl: i === 3 ? '' : `https://image.tmdb.org/t/p/w300/${i === 4 ? 'broken' : 'still'}.jpg`,
    }))
    const episodesFor = season => episodeData.map(e => ({ ...e, id: season * 100 + e.id, sourceSeason: season }))
    const versionPage = (season, episode, offset, stamp) => ({
      versions: episode === 6 ? [] : Array.from({ length: offset ? 1 : 2 }, (_, i) => ({
        itemId: `item-${season}-${episode}-${offset + i}`, name: `物语系列 S${season}E${episode} v${stamp}`, url: 'https://example.invalid/item',
        sources: [{ Id: `source-${offset + i}`, Container: 'mkv', Size: 1530000000, MediaStreams: [
          { Type: 'Video', Codec: 'hevc', Width: 3840, Height: 2160, VideoRange: 'SDR', AverageFrameRate: 23.976, BitDepth: 10, BitRate: 15000000 },
          { Type: 'Audio', Codec: 'flac', Language: 'jpn', IsDefault: true, Channels: 2, SampleRate: 48000 },
          { Type: 'Audio', Codec: 'aac', Language: 'chi', IsDefault: false, Channels: 2 },
        ] }],
      })), total: episode === 6 ? 0 : 3, hasMore: episode !== 6 && offset === 0, nextOffset: offset ? 3 : 2,
    })
    await context.route('**/api/**', async route => {
      const request = route.request(), url = new URL(request.url()), endpoint = url.pathname
      assert.equal(request.method(), 'GET', 'the episode list must never write business data')
      if (endpoint.includes('/tmdb/image/')) {
        await route.fulfill(endpoint.includes('broken') ? { status: 404, body: '' } : {
          contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="320" height="180"><defs><linearGradient id="a"><stop stop-color="#384d68"/><stop offset="1" stop-color="#ad8396"/></linearGradient></defs><path fill="url(#a)" d="M0 0h320v180H0z"/><circle cx="220" cy="60" r="38" fill="#dacbb9"/><path fill="#313646" d="m0 160 90-70 50 35 70-25 110 80H0z"/></svg>',
        }).catch(() => {})
        return
      }
      let status = 200, data = { configured: false, accounts: [] }
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock', username: 'mock', role: state.role, disabled: false } }
      else if (endpoint.endsWith('/detail/tv/46195') || endpoint.endsWith('/detail/movie/123')) {
        const movie = endpoint.includes('/movie/')
        data = { id: movie ? 123 : 46195, kind: movie ? 'movie' : 'tv', title: '物语系列', originalTitle: '化物語', overview: '以怪异为线索的一系列故事。', posterUrl: '', backdropUrl: '', date: '2009-07-03', rating: 8.1, votes: 534, tagline: '', status: 'Ended', runtime: 24, episodeCount: 58, genres: [{ id: 16, name: '动画' }], countries: ['JP'], cast: [], seasons: movie ? [] : [1, 2].map(s => ({ id: s, number: s, name: `第${s}季`, episodeCount: 29, date: '2009-07-03' })), collection: null, imdbId: '', imdbUrl: '', tmdbUrl: '', numbering: { groupId: '', revision: 0, name: 'TMDB 默认', type: 0 } }
      } else if (endpoint.endsWith('/schedules')) data = { seasons: [1, 2].map(s => ({ number: s, episodes: episodesFor(s), numberingRevision: 0 })) }
      else if (/\/(season|schedule)\/\d+$/.test(endpoint)) {
        const s = Number(endpoint.split('/').pop())
        data = { id: s, number: s, name: `第${s}季`, overview: '', episodes: episodesFor(s), numberingRevision: 0 }
      } else if (endpoint.includes('/emby/media/')) {
        const movie = endpoint.includes('/movie/')
        data = { media: { state: state.library, kind: movie ? 'movie' : 'tv', tmdbId: movie ? '123' : '46195', episodeCount: 50,
          items: movie ? [{ id: 'movie', type: 'Movie' }] : [1, 2].flatMap(s => episodesFor(s).filter(e => e.number !== 5 && (e.number < 27 || e.number === 28)).map(e => ({ id: `emby-${s}-${e.number}`, type: 'Episode', season: s, episode: e.number, endEpisode: -1, name: e.name, libraryId: 'tv' }))),
        }, baseUrl: '', serverId: '' }
      } else if (endpoint.includes('/emby/versions/')) {
        const season = Number(url.searchParams.get('season') || 0), episode = Number(url.searchParams.get('episode') || 0), offset = Number(url.searchParams.get('offset'))
        const key = `${season}/${episode}/${offset}`, stamp = state.stamp
        reads.push(key)
        if (state.hold === key) { state.hold = ''; await new Promise(resolve => state.held.set(key, resolve)); state.held.delete(key) }
        if (state.fail === key) { status = 502; data = { error: '模拟读取失败' } }
        else data = versionPage(season, episode, offset, stamp)
      } else if (endpoint.includes('/recommendations/')) data = { items: [], page: 1, totalPages: 1 }
      else if (endpoint.includes('/follows/')) data = { subscribed: false, request: null, libraryState: 'present' }
      else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
    })
    const button = n => page.locator(`#episode-46195-1-${n}-button`)
    const summaries = () => page.locator('.version-summary')
    const openEpisode = async n => { await button(n).click(); await summaries().first().waitFor() }
    const closeEpisode = () => page.locator('.episode-close').click()
    const waitForHeld = async key => {
      for (let i = 0; i < 250 && !state.held.has(key); i++) await new Promise(resolve => setTimeout(resolve, 20))
      assert.ok(state.held.has(key), `expected held request: ${key}`)
    }
    const scrollList = () => button(1).evaluate(el => window.scrollTo(0, el.getBoundingClientRect().top + scrollY - 100))
    const noOverflow = async () => assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)

    await page.goto(origin + '/tv/46195', { waitUntil: 'networkidle' })
    await page.locator('#season-button-1').click()
    await button(1).waitFor()
    assert.equal(reads.length, 0)
    assert.equal(await page.locator('.episode-row').count(), 20)
    assert.ok((await button(1).boundingBox()).height <= 110)
    passed.push('compact initial rows; first 20 episodes; no version prefetch on season expansion')

    await button(1).focus(); await page.keyboard.press('Enter'); await summaries().first().waitFor()
    assert.equal(reads.length, 1)
    await page.keyboard.press('Space')
    assert.equal(await page.locator('.episode-detail').count(), 0)
    await openEpisode(2); await openEpisode(1)
    assert.equal(reads.length, 2)
    assert.equal(await page.locator('.episode-detail').count(), 1)
    assert.equal(await button(2).getAttribute('aria-expanded'), 'false')
    passed.push('native keyboard toggle; one expanded episode; repeated openings reuse the page cache')

    state.fail = '1/1/2'
    await page.getByRole('button', { name: '加载更多版本', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '模拟读取失败' }).waitFor()
    state.fail = ''
    await page.getByRole('button', { name: '重新加载', exact: true }).click()
    await summaries().nth(2).waitFor()
    assert.deepEqual(reads.slice(-2), ['1/1/2', '1/1/2'])
    await summaries().first().click()
    await page.getByRole('combobox', { name: '选择音轨' }).selectOption('1')
    await page.getByRole('region', { name: /版本 1/ }).getByText('chi', { exact: true }).waitFor()
    await closeEpisode()
    assert.equal(await button(1).evaluate(el => el === document.activeElement), true)
    const cachedReads = reads.length
    await openEpisode(1)
    assert.equal(await summaries().count(), 3)
    assert.equal(reads.length, cachedReads)
    passed.push('failed pagination retries the same offset without dropping rows; audio selection; cached pagination; collapse restores focus')

    await page.locator('#season-button-2').click()
    await page.locator('#episode-46195-2-1-button').click(); await summaries().first().waitFor()
    await page.locator('#season-button-1').click(); await openEpisode(1)
    assert.equal(reads.length, cachedReads + 1)
    await page.getByRole('button', { name: '刷新播出信息', exact: true }).click()
    await page.waitForFunction(() => document.querySelectorAll('.version-summary').length === 2)
    assert.equal(reads.length, cachedReads + 2)
    passed.push('season switches reuse cached responses; explicit refresh invalidates them')

    state.fail = '1/4/0'
    await button(4).click(); await page.getByRole('alert').filter({ hasText: '模拟读取失败' }).waitFor()
    state.fail = ''
    await page.getByRole('button', { name: '重新加载', exact: true }).click(); await summaries().first().waitFor()
    state.hold = '1/7/0'
    await button(7).click(); await page.waitForFunction(() => !!document.querySelector('.episode-media [role="status"]'))
    await waitForHeld('1/7/0')
    await openEpisode(2)
    state.held.get('1/7/0')()
    await page.waitForLoadState('networkidle')
    assert.equal(await button(2).getAttribute('aria-expanded'), 'true')
    const afterAbort = reads.length
    await openEpisode(7)
    assert.equal(reads.length, afterAbort + 1, 'aborted response must not populate the cache')
    state.hold = '1/8/0'
    await button(8).click()
    await waitForHeld('1/8/0')
    state.stamp = 2
    await page.getByRole('button', { name: '刷新播出信息', exact: true }).click(); await summaries().first().waitFor()
    state.held.get('1/8/0')()
    await page.waitForLoadState('networkidle')
    await summaries().first().click()
    assert.ok((await page.locator('.episode-media').innerText()).includes('S1E8 v2'))
    assert.ok(!(await page.locator('.episode-media').innerText()).includes('S1E8 v1'))
    passed.push('first-page retry; aborted requests cannot fill another episode or cache; refresh rejects an old generation response')

    await closeEpisode(); await button(6).click()
    await page.getByText('Emby 暂未返回可用媒体版本，可刷新入库状态后重试。', { exact: true }).waitFor()
    const emptyReads = reads.length
    await closeEpisode(); await button(6).click()
    assert.equal(reads.length, emptyReads)
    await closeEpisode()
    await page.evaluate(() => { window.originalDateNow = Date.now; const now = Date.now(); Date.now = () => now + 6 * 60 * 1000 })
    await openEpisode(2)
    assert.equal(reads.length, emptyReads + 1)
    await page.evaluate(() => { Date.now = window.originalDateNow })
    await closeEpisode()
    const beforeMore = reads.length
    await page.locator('[data-infinite-load-more]').scrollIntoViewIfNeeded()
    await page.locator('[data-episode-number="29"]').waitFor()
    assert.equal(reads.length, beforeMore)
    await page.locator('[data-episode-number="28"] .episode-status').getByText('已入库', { exact: true }).waitFor()
    assert.equal(await page.locator('[data-episode-number="29"]').getByText('播出日期待定', { exact: true }).count(), 1)
    passed.push('empty responses are cached; expired entries reload; infinite append makes no version requests; future owned episode and undated status remain correct')

    await scrollList(); await page.mouse.move(0, 0)
    await page.screenshot({ path: 'test-results/season-episodes-desktop.png' })
    await openEpisode(3)
    await closeEpisode()
    const headerBottom = await page.locator('.workbench-header').evaluate(el => el.getBoundingClientRect().bottom)
    assert.ok((await button(3).boundingBox()).y >= headerBottom)
    assert.equal(await button(3).evaluate(el => el === document.activeElement), true)
    await page.locator('[data-episode-number="5"] .episode-still > svg').waitFor()
    for (const width of [390, 320]) {
      await page.setViewportSize({ width, height: 844 }); await scrollList(); await noOverflow()
      assert.ok((await button(1).boundingBox()).height <= 110)
      assert.equal(await page.locator('[data-episode-number="1"] .episode-number').isVisible(), true)
      await page.screenshot({ path: `test-results/season-episodes-mobile-${width}.png` })
      await openEpisode(1); await summaries().first().click(); await noOverflow()
      await summaries().first().evaluate(el => window.scrollTo(0, el.getBoundingClientRect().top + scrollY - 90))
      await page.evaluate(() => document.documentElement.classList.add('dark'))
      await page.waitForTimeout(250)
      await page.screenshot({ path: `test-results/season-episodes-mobile-${width}-dark.png` })
      await page.evaluate(() => document.documentElement.classList.remove('dark'))
      await closeEpisode()
    }
    passed.push('deep collapse returns to episode heading; failed images use SVG; desktop/390px/320px layouts and dark expanded parameters have no horizontal overflow')

    state.role = 'user'
    await page.reload({ waitUntil: 'networkidle' })
    await page.locator('#season-button-1').click()
    const beforeOrdinary = reads.length
    await button(1).click()
    assert.equal(await page.locator('.episode-media').count(), 0)
    assert.equal(reads.length, beforeOrdinary)
    state.role = 'admin'; state.library = 'stale'
    await page.reload({ waitUntil: 'networkidle' })
    await page.locator('#season-button-1').click()
    await page.locator('[data-episode-number="1"] .episode-status').getByText('已入库 · 待更新', { exact: true }).waitFor()
    state.library = 'present'
    await page.goto(origin + '/movie/123', { waitUntil: 'networkidle' })
    await page.locator('.media-version-panel').first().waitFor()
    assert.equal(await page.locator('.media-version-compact').count(), 0)
    assert.equal(await summaries().count(), 0)
    assert.equal(await page.locator('.movie-version-toggle').count(), 2)
    await page.getByRole('heading', { name: '媒体版本', exact: true }).waitFor()
    assert.deepEqual(errors, [])
    passed.push('ordinary users never request media versions; stale index label is preserved; movie media uses its own summary layout; no browser errors')
    console.log(JSON.stringify({ passed, screenshots: 'test-results/season-episodes-*.png' }, null, 2))
  } finally {
    for (const resolve of state.held.values()) resolve()
    await browser.close()
  }
})().catch(e => { console.error(e); process.exitCode = 1 })
