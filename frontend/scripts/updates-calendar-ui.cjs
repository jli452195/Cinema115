// Mocked API only: verifies single-day requests and calendar navigation.
const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage(), calls = [], errors = []
    await page.clock.setFixedTime(new Date('2027-01-01T16:30:00Z'))
    page.on('pageerror', error => errors.push(error.message))
    await page.route('**/api/**', async route => {
      const url = new URL(route.request().url())
      let data = {}
      if (url.pathname === '/api/auth/me') data = { user: { id: 'mock', username: 'mock', role: 'admin', disabled: false } }
      else if (url.pathname.endsWith('/updates')) {
        const query = Object.fromEntries(url.searchParams)
        calls.push(query)
        const item = id => ({ date: query.date, kind: 'movie', tmdbId: id, title: '示例电影 ' + id, posterUrl: '', releaseDate: '2026-01-01', rating: 8, votes: 10 })
        const today = query.date === '2027-01-02'
        data = { ready: true, status: 'ready', items: today ? [item(query.cursor ? 2 : 1)] : [], hasMore: today && !query.cursor, nextCursor: today && !query.cursor ? '1' : '', from: query.date, to: query.date }
      }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
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
    await page.getByText('已加载 1 项', { exact: false }).waitFor()
    assert.deepEqual(calls.at(-1), { date: '2027-01-02' })
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.setViewportSize({ width: 390, height: 844 })
    await strip.getByRole('button').last().click()
    await page.getByText('这一天暂无更新，试试其他日期。').waitFor()
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    assert.equal(await strip.evaluate(el => el.scrollWidth > el.clientWidth), true)
    assert.equal(await strip.getByRole('button').last().getAttribute('aria-pressed'), 'true')
    assert.ok(calls.every(query => query.date && !('range' in query)))
    assert.deepEqual(errors, [])
    console.log('Updates calendar passed: seven dates, Shanghai year boundary, single-day requests, pagination, empty state, history, dark mobile layout')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
