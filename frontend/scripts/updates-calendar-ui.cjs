// Mocked API only: verifies single-day requests and calendar navigation.
const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage(), calls = [], errors = [], layouts = []
    let responseMode = 'normal'
    let simulatedNow = Date.parse('2027-01-01T16:30:00Z')
    await page.clock.setFixedTime(new Date(simulatedNow))
    const expireCache = async () => { simulatedNow += 61000; await page.clock.setFixedTime(new Date(simulatedNow)) }
    page.on('pageerror', error => errors.push(error.message))
    await page.route('**/api/**', async route => {
      const url = new URL(route.request().url())
      let data = {}, status = 200
      if (url.pathname === '/api/auth/me') data = { user: { id: 'mock', username: 'mock', role: 'admin', disabled: false } }
      else if (url.pathname.endsWith('/updates')) {
        const query = Object.fromEntries(url.searchParams)
        calls.push(query)
        const mode = responseMode
        if (mode === 'slow' || mode === 'slow-rich' || (mode === 'race' && query.date === '2027-01-01')) await new Promise(resolve => setTimeout(resolve, 650))
        const item = id => ({ date: query.date, kind: 'movie', tmdbId: id, title: '示例电影 ' + id, posterUrl: '', releaseDate: '2026-01-01', rating: 8, votes: 10 })
        const today = query.date === '2027-01-02'
        data = { ready: true, status: 'ready', items: today ? [item(query.cursor ? 2 : 1)] : [], hasMore: today && !query.cursor, nextCursor: today && !query.cursor ? '1' : '', from: query.date, to: query.date }
        if (mode === 'rich' || mode === 'slow-rich' || (mode === 'race' && query.date === '2026-12-31')) {
          const labels = ['S01 E01', 'S01 E01-E08', 'S01 E01, E03, E05, E07, E09, E11', 'S00 E01, E03, E05, E07, E09, E11', 'S01 更新10集', '']
          data = { ...data, hasMore: false, nextCursor: '', items: Array.from({ length: 23 }, (_, i) => ({ ...item(100 + i), kind: i % 4 === 0 ? 'movie' : 'tv', title: i % 2 ? `示例剧集 ${i}（第 1 季）` : `较长的测试片名用于检查两行标题与下方更新信息的对齐 ${i}`, season: i === 3 ? 0 : 1, episodeLabel: i % 4 === 0 ? '' : labels[i % labels.length], episodeCount: i === 5 ? 0 : i === 4 ? 10 : 6, metadataMissing: i === 5, rating: i === 5 ? 0 : 8, votes: i === 5 ? 0 : 100 })) }
        }
        if (mode === 'building') data = { ...data, ready: false, status: 'building', items: [], hasMore: false, nextCursor: '' }
        if (mode === 'error') { status = 503; data = { error: '模拟更新加载失败' } }
      }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
    })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/updates')
    const strip = page.getByRole('group', { name: '选择更新日期' })
    await page.getByText('已加载 1 项', { exact: false }).waitFor()
    assert.equal(await strip.getByRole('button').count(), 7)
    assert.equal(await strip.getByRole('button').first().getAttribute('aria-label'), '2027-01-02 今天')
    assert.match(await strip.getByRole('button').last().getAttribute('aria-label'), /^2026-12-27 /)
    assert.deepEqual(calls, [{ date: '2027-01-02' }])
    await page.getByRole('button', { name: '加载更多', exact: true }).click()
    await page.getByText('更新 2 项', { exact: false }).waitFor()
    assert.deepEqual(calls.at(-1), { date: '2027-01-02', cursor: '1' })
    await strip.getByRole('button', { name: '2027-01-01 昨天' }).click()
    await page.getByText('这一天暂无更新，试试其他日期。').waitFor()
    assert.equal(await page.getByRole('button', { name: '加载更多', exact: true }).count(), 0)
    assert.deepEqual(calls.at(-1), { date: '2027-01-01' })
    const count = calls.length
    await strip.getByRole('button', { name: '2027-01-01 昨天' }).click()
    assert.equal(calls.length, count)
    await page.goBack()
    await page.getByText('更新 2 项', { exact: false }).waitFor()
    assert.equal(calls.length, count)
    assert.equal(await page.locator('.update-card').count(), 2)
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.setViewportSize({ width: 390, height: 844 })
    await strip.getByRole('button').last().click()
    await page.getByText('这一天暂无更新，试试其他日期。').waitFor()
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    assert.equal(await strip.evaluate(el => el.parentElement.scrollWidth > el.parentElement.clientWidth), false)
    assert.equal(await strip.getByRole('button').last().getAttribute('aria-pressed'), 'true')
    // Failed or pending date changes retain a clearly dated snapshot, with pagination locked.
    await expireCache()
    await strip.getByRole('button').first().click()
    await page.getByText('已加载 1 项', { exact: false }).waitFor()
    const originalLink = await page.locator('.update-card a').first().getAttribute('href')
    responseMode = 'error'
    await strip.getByRole('button', { name: '2027-01-01 昨天' }).click()
    await page.getByRole('alert').waitFor()
    assert.equal(await page.locator('.update-card a').first().getAttribute('href'), originalLink)
    assert.match(await page.locator('.updates-summary [role="status"]').innerText(), /2027-01-02.*结果.*加载失败/)
    assert.equal(await page.getByRole('button', { name: '加载更多', exact: true }).isDisabled(), true)
    responseMode = 'slow'
    await page.getByRole('button', { name: '重新加载', exact: true }).click()
    await page.locator('.updates-results[aria-busy="true"]').waitFor()
    assert.equal(await page.locator('.update-card a').first().getAttribute('href'), originalLink)
    await page.getByText('这一天暂无更新，试试其他日期。').waitFor()
    responseMode = 'normal'
    await strip.getByRole('button').first().click()
    await page.getByText('已加载 1 项', { exact: false }).waitFor()
    responseMode = 'error'
    await page.getByRole('button', { name: '加载更多', exact: true }).click()
    await page.getByRole('button', { name: '重试加载更多', exact: true }).waitFor()
    assert.equal(await page.locator('.update-card').count(), 1)
    responseMode = 'normal'
    await page.getByRole('button', { name: '重试加载更多', exact: true }).click()
    await page.getByText('更新 2 项', { exact: false }).waitFor()
    assert.deepEqual(calls.at(-1), { date: '2027-01-02', cursor: '1' })
    // A superseded slower response cannot replace the newest day's data.
    await expireCache()
    await page.setViewportSize({ width: 1280, height: 900 })
    responseMode = 'race'
    await strip.getByRole('button', { name: '2027-01-01 昨天' }).click()
    await strip.getByRole('button', { name: /^2026-12-31 / }).click()
    await page.getByText('更新 23 项', { exact: false }).waitFor()
    await page.waitForTimeout(750)
    assert.equal(await page.locator('.update-card').count(), 23)
    assert.equal(new URL(page.url()).searchParams.get('date'), '2026-12-31')
    const expectedColumns = { 320: 2, 390: 2, 640: 3, 768: 3, 1024: 4, 1280: 6, 1440: 7, 1600: 7, 1920: 7 }
    for (const [widthValue, columns] of Object.entries(expectedColumns)) for (const dark of [false, true]) {
      const width = Number(widthValue)
      await page.setViewportSize({ width, height: 900 })
      await page.evaluate(value => document.documentElement.classList.toggle('dark', value), dark)
      const layout = await page.evaluate(() => {
        const grid = document.querySelector('.updates-grid'), card = grid.firstElementChild
        const rect = card.getBoundingClientRect()
        const poster = card.querySelector('.update-poster').getBoundingClientRect(), range = card.querySelector('.update-range')?.getBoundingClientRect(), rating = card.querySelector('.update-rating').getBoundingClientRect(), type = card.querySelector('.update-type').getBoundingClientRect()
        const dates = document.querySelector('.updates-dates').getBoundingClientRect(), scroll = document.querySelector('.updates-date-scroll').getBoundingClientRect()
        const dateButtons = [...document.querySelectorAll('.updates-dates button')].map(el => el.getBoundingClientRect())
        return { columns: getComputedStyle(grid).gridTemplateColumns.split(' ').length, card: Math.round(rect.width), gridTop: grid.getBoundingClientRect().top, overflow: document.documentElement.scrollWidth > document.documentElement.clientWidth, titleHeights: [...grid.querySelectorAll('h3')].map(el => el.getBoundingClientRect().height), posterRatio: poster.height / rect.width, dateCenterDelta: Math.abs((dates.left + dates.right) / 2 - (scroll.left + scroll.right) / 2), datesVisible: dateButtons.length === 7 && dateButtons.every(r => r.left >= scroll.left && r.right <= scroll.right && r.height >= 44), summaryAlign: getComputedStyle(document.querySelector('.updates-summary')).textAlign, badgesOnPoster: (!range || (range.top >= poster.top && range.right < type.left)) && rating.bottom < poster.bottom && rating.right < poster.right, badgeSurface: getComputedStyle(card).getPropertyValue('--badge-surface').trim(), titleColor: getComputedStyle(card.querySelector('h3')).color, yearColor: getComputedStyle(card.querySelector('.update-meta')).color }
      })
      assert.equal(layout.columns, columns, JSON.stringify({ width, dark, layout }))
      assert.equal(layout.overflow, false)
      assert.ok(layout.titleHeights.every(height => height === 40))
      assert.ok(Math.abs(layout.posterRatio - 1.5) < 0.01)
      assert.equal(layout.summaryAlign, 'center')
      assert.equal(layout.badgesOnPoster, true)
      assert.match(layout.badgeSurface, dark ? /33 37 41/ : /#fff/)
      assert.equal(layout.titleColor, dark ? 'rgb(225, 227, 229)' : 'rgb(30, 41, 59)')
      assert.equal(layout.yearColor, dark ? 'rgb(156, 163, 170)' : 'rgb(148, 163, 184)')
      assert.ok(layout.dateCenterDelta < 1)
      assert.equal(layout.datesVisible, true)
      if (width >= 1280) assert.ok(layout.gridTop < 320 && layout.card <= 215)
      layouts.push({ width, dark, columns, card: layout.card, gridTop: layout.gridTop })
    }
    // Selection feedback runs while the API waits, without pushing the grid or resetting scroll.
    responseMode = 'slow-rich'
    await expireCache()
    for (const width of [320, 1280]) {
      await page.setViewportSize({ width, height: 900 })
      await page.waitForTimeout(40)
      await page.evaluate(() => window.scrollTo(0, 60))
      const samples = await page.evaluate(async index => {
        const read = () => ({ y: scrollY, top: document.querySelector('.updates-results').getBoundingClientRect().top, statusHeight: document.querySelector('.updates-summary p').getBoundingClientRect().height })
        const samples = [read()], buttons = [...document.querySelectorAll('.updates-dates button')]
        const next = buttons.at(index)
        next.click()
        for (let i = 0; i < 55; i++) { await new Promise(requestAnimationFrame); samples.push(read()) }
        return samples
      }, width === 320 ? -1 : -2)
      assert.ok(samples.every(s => Math.abs(s.y - samples[0].y) < 1 && Math.abs(s.top - samples[0].top) < 1), JSON.stringify({ width, samples }))
      await page.locator('.updates-results[aria-busy="false"]').waitFor()
      const indicator = page.locator('.updates-dates .catalog-segment-indicator')
      assert.match(await indicator.evaluate(el => el.style.transition), /240ms cubic-bezier\(0.2, 0, 0, 1\)/)
    }
    responseMode = 'rich'
    await page.evaluate(() => window.scrollTo(0, 0))
    // Returning to a cached day aborts an in-flight request without losing the cached list.
    responseMode = 'slow-rich'
    await strip.getByRole('button').nth(4).click()
    await page.locator('.updates-results[aria-busy="true"]').waitFor()
    const beforeCachedReturn = calls.length
    await strip.getByRole('button').last().click()
    await page.locator('.updates-results[aria-busy="false"]').waitFor()
    assert.equal(calls.length, beforeCachedReturn)
    await page.waitForTimeout(700)
    assert.equal(new URL(page.url()).searchParams.get('date'), '2026-12-27')
    assert.equal(await page.locator('.update-card').count(), 23)
    responseMode = 'rich'
    const card = page.locator('.update-card').nth(1)
    const resting = await card.evaluate(el => ({poster: el.querySelector('.update-poster').getBoundingClientRect().top, range: el.querySelector('.update-range-row').getBoundingClientRect().top}))
    await card.locator('a').hover(); await page.waitForTimeout(180)
    const hovered = await card.evaluate(el => ({poster: el.querySelector('.update-poster').getBoundingClientRect().top, range: el.querySelector('.update-range-row').getBoundingClientRect().top, title: getComputedStyle(el.querySelector('h3')).color, rating: getComputedStyle(document.querySelector('.update-rating')).color}))
    assert.ok(Math.abs(hovered.poster - resting.poster + 4) < 0.1)
    assert.ok(Math.abs(hovered.range - resting.range + 4) < 0.1)
    assert.equal(hovered.title, 'rgb(245, 181, 68)')
    assert.equal(hovered.rating, 'rgb(245, 181, 68)')
    await page.mouse.move(0, 0); await page.waitForTimeout(180)
    // Overflowing update ranges expand by touch and keyboard without entering a detail route.
    await page.setViewportSize({ width: 320, height: 844 })
    const rangeButton = page.locator('.update-card').nth(2).locator('button.update-range')
    await rangeButton.waitFor()
    const beforeRange = page.url(), beforeRangeCalls = calls.length
    const beforeRangeHeight = await page.locator('.update-card').nth(2).evaluate(el => el.getBoundingClientRect().height)
    await rangeButton.click()
    assert.equal(await rangeButton.getAttribute('aria-expanded'), 'true')
    assert.equal(await rangeButton.locator('span').innerText(), 'S01 E01, E03, E05, E07, E09, E11')
    assert.equal(await rangeButton.locator('span').evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
    assert.equal(page.url(), beforeRange); assert.equal(calls.length, beforeRangeCalls)
    assert.equal(await page.locator('.update-card').nth(2).evaluate(el => el.getBoundingClientRect().height), beforeRangeHeight)
    await rangeButton.focus(); await page.keyboard.press('Enter')
    assert.equal(await rangeButton.getAttribute('aria-expanded'), 'false')
    assert.match(await page.locator('.update-card').nth(3).locator('.update-range').innerText(), /^特别篇 E01, E03/)
    assert.equal(await page.locator('.update-card').nth(5).locator('.update-range').innerText(), '剧集更新')
    assert.equal(await page.locator('.update-card').nth(5).locator('.update-rating').count(), 0)
    assert.equal(await page.locator('.update-card').first().locator('.update-range').count(), 0)
    assert.equal(await page.getByText('收录更新', { exact: true }).count(), 0)
    assert.equal(await page.locator('.update-card').locator('a button').count(), 0)
    responseMode = 'rich'
    await expireCache()
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await strip.getByRole('button').first().click()
    await page.getByText('更新 23 项', { exact: false }).waitFor()
    assert.equal(await page.locator('.updates-dates .catalog-segment-indicator').evaluate(el => getComputedStyle(el).transitionDuration), '0s')
    responseMode = 'building'
    await expireCache()
    await strip.getByRole('button', { name: '2027-01-01 昨天' }).click()
    await page.getByText('最近更新时间线正在后台构建。', { exact: true }).waitFor()
    assert.equal(await page.locator('.update-card').count(), 0)
    responseMode = 'error'
    await page.reload(); await page.getByRole('alert').waitFor()
    assert.match(await page.locator('.updates-summary').innerText(), /加载失败/)
    assert.doesNotMatch(await page.locator('.updates-summary').innerText(), /更新 0 项/)
    assert.ok(calls.every(query => query.date && !('range' in query)))
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed: true, cases: ['Shanghai-calendar-history', 'cursor-pagination-retry', 'date-cache-pagination-expiry', 'cached-return-aborts-request', 'retained-dated-results', 'request-cancellation', 'responsive-light-dark', 'selection-no-layout-jump', 'poster-hover-colors', 'range-touch-keyboard', 'specials-missing-metadata', 'reduced-motion', 'building-initial-failure'], layouts, pageErrors: errors }))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
