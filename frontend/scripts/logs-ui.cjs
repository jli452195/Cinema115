// Every API request is mocked; this regression check never reads real service data.
const path = require('node:path'), os = require('node:os'), fs = require('node:fs'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
  const output = path.join(__dirname, '../test-results')
  fs.mkdirSync(output, { recursive: true })
  const entry = (id, overrides = {}) => ({ id, time: '2026-10-17T10:15:30.123Z', level: 'INFO', module: 'Emby 302', source: 'playback.go:324', taskID: 'task-example', message: 'Original file direct link ready', ...overrides })
  const rows = [
    entry(10, { details: { method: 'GET', path: '/Videos/123/stream', status: 302, durationMs: 128, url: 'https://mock.invalid/' + 'file'.repeat(40), requestHeaders: 'X-Mock: request', lookupHeaders: 'X-Mock: lookup', responseHeaders: 'X-Mock: response', steps: 'Verified playback permission\nResolved original file\nReturned CDN location', lookup: 'Mock lookup ' + 'result'.repeat(60) } }),
    entry(11, { level: 'WARNING', module: 'Telegram', source: '', taskID: '', message: '\u8bf7\u6c42\u7b49\u5f85\u91cd\u8bd5\uff0c\u539f\u8bb0\u5f55\u4fdd\u7559\u3002' }),
    entry(12, { level: 'DEBUG', module: 'STRM', taskID: 'task-debug', message: 'Diagnostic fixture', details: { steps: 'Unique diagnostic needle' } }),
    entry(13, { level: 'ERROR', module: '\u641c\u7d22', source: '\u8717\u725b' + 'source'.repeat(15), taskID: 'task-'.repeat(15), message: '\u6a21\u62df\u7f51\u7edc\u8bf7\u6c42\u5931\u8d25\uff0c\u8bf7\u7a0d\u540e\u91cd\u8bd5\u3002' }),
  ]
  const files = [{ name: 'cinema115.log', size: 2048, modifiedAt: '2026-10-30T10:15:30Z', active: true }, { name: 'cinema115-2026-10-29T10-15-30.000.log', size: 1048576, modifiedAt: '2026-10-29T10:15:30Z', active: false }]
  const state = { hold: true, status: 200, entries: rows, requests: 0, inFlight: 0, maxInFlight: 0, pending: [], reset: false, instanceID: 'instance-a', persistError: '', failedWrites: 0, persistEnabled: true, fileRequests: 0, filesStatus: 200, downloadStatus: 200 }
  const errors = [], requests = [], passed = []
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => {
      localStorage.setItem('admin_token', 'mock-logs-token')
      localStorage.setItem('cinema115-theme', 'light')
      window.__logsAborts = 0
      window.__logURLs = { created: 0, revoked: 0 }
      const createURL = URL.createObjectURL, revokeURL = URL.revokeObjectURL
      URL.createObjectURL = blob => { window.__logURLs.created++; return createURL.call(URL, blob) }
      URL.revokeObjectURL = url => { window.__logURLs.revoked++; return revokeURL.call(URL, url) }
      const original = window.fetch
      window.fetch = (input, init) => {
        if (String(input).includes('/api/admin/logs')) {
          let pending = true
          init?.signal?.addEventListener('abort', () => { if (pending) window.__logsAborts++ }, { once: true })
          return original(input, init).finally(() => { pending = false })
        }
        return original(input, init)
      }
    })
    await context.route('**/api/**', async route => {
      const requestURL = new URL(route.request().url()), endpoint = requestURL.pathname
      requests.push({ endpoint, after: requestURL.searchParams.get('after'), instanceID: requestURL.searchParams.get('instanceID'), method: route.request().method(), authorization: route.request().headers().authorization })
      if (endpoint === '/api/admin/logs') {
        state.requests++; state.inFlight++; state.maxInFlight = Math.max(state.maxInFlight, state.inFlight)
        const status = state.status, snapshot = structuredClone(state.entries), cursor = Math.max(0, ...snapshot.map(entry => entry.id))
        const after = requestURL.searchParams.get('after'), instanceID = state.instanceID, reset = state.reset || after !== null && (Number(after) > cursor || requestURL.searchParams.get('instanceID') !== instanceID)
        const entries = after === null || reset ? snapshot : snapshot.filter(entry => entry.id > Number(after))
        try {
          if (state.hold) await new Promise(resolve => state.pending.push(resolve))
          await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(status === 200 ? { entries, capacity: 2000, cursor, reset, instanceID, persistence: { enabled: state.persistEnabled, lastError: state.persistError, failedWrites: state.failedWrites } } : { error: 'Mock read failure' }) })
        } catch (error) {
          if (!/closed|intercepted|invalid.*state/i.test(error.message)) throw error
        } finally { state.inFlight-- }
        return
      }
      if (endpoint === '/api/admin/logs/files') {
        state.fileRequests++
        await route.fulfill({ status: state.filesStatus, contentType: 'application/json', body: JSON.stringify(state.filesStatus === 200 ? { files, maxSizeMB: 20, maxBackups: 10, maxAgeDays: 14 } : { error: 'Mock file list failure' }) })
        return
      }
      if (endpoint.startsWith('/api/admin/logs/files/')) {
        await route.fulfill({ status: state.downloadStatus, contentType: state.downloadStatus === 200 ? 'application/x-ndjson' : 'application/json', body: state.downloadStatus === 200 ? '{"level":"ERROR","message":"Historical fixture"}\n' : JSON.stringify({ error: 'Mock download failure' }) })
        return
      }
      const body = endpoint === '/api/auth/me' ? { user: { id: 'mock', username: 'logs-review', role: 'admin' } } : { items: [], unread: 0, accounts: [], configured: false }
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
    })
    const page = await context.newPage()
    page.setDefaultTimeout(8000)
    page.on('pageerror', error => errors.push(error.message))
    const retry = page.locator('button.logs-retry')
    const search = page.getByRole('searchbox'), nav = page.locator('.logs-levels')
    const waitFor = async predicate => {
      const until = Date.now() + 8000
      while (!predicate()) { assert(Date.now() < until, 'Timed out waiting for mocked request'); await new Promise(resolve => setTimeout(resolve, 30)) }
    }
    const release = () => { state.hold = false; state.pending.splice(0).forEach(resolve => resolve()) }
    const idle = () => page.waitForFunction(() => !document.querySelector('.logs-error') && !!document.querySelector('.logs-updated'))
    const choose = async name => { await nav.getByRole('button', { name, exact: true }).click(); await page.waitForTimeout(330) }
    const go = async suffix => { await page.goto(origin + suffix); await idle() }
    const screenshot = name => page.screenshot({ path: path.join(output, name + '.png'), fullPage: true })

    await page.goto(origin + '/logs')
    await waitFor(() => state.pending.length === 1)
    assert.match(await page.locator('.logs-empty').innerText(), /\u6b63\u5728\u8bfb\u53d6/)
    release(); await idle()
    assert.equal(await nav.locator('[aria-pressed="true"]').innerText(), 'INFO')
    assert.equal(await page.locator('.logs-entry').count(), 1)
    assert.equal(await page.locator('.logs-controls button, .logs-controls input[type="checkbox"]').count(), 0)
    assert.equal(await retry.count(), 0)
    await page.locator('.logs-details summary').click()
    const stable = await page.locator('.logs-controls').boundingBox()
    state.hold = true
    await waitFor(() => state.pending.length === 1)
    assert.equal(await page.locator('.is-refreshing').count(), 0)
    assert.deepEqual(await page.locator('.logs-controls').boundingBox(), stable)
    assert.equal(await page.locator('.logs-toolbar > [role="status"]').innerText(), '')
    release(); await idle()
    assert.equal(await page.locator('.logs-details').getAttribute('open'), '')
    passed.push('no permanent update controls, silent automatic polling, stable toolbar, preserved expanded details')

    state.hold = true; state.status = 500
    await waitFor(() => state.pending.length === 1)
    release(); await page.getByRole('alert').waitFor()
    assert.equal(await page.locator('.logs-entry').count(), 1)
    assert.match(await page.getByRole('alert').innerText(), /\u4fdd\u7559\u4e0a\u6b21/)
    const stoppedCount = state.requests
    await page.waitForTimeout(2150)
    assert.equal(state.requests, stoppedCount)
    state.status = 200; state.hold = true
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await retry.focus(); await page.keyboard.press('Enter')
    await waitFor(() => state.pending.length === 1)
    assert.equal(await retry.getAttribute('aria-busy'), 'true')
    assert.equal(await retry.isDisabled(), true)
    assert.equal(await retry.locator('svg').evaluate(el => getComputedStyle(el).animationName), 'none')
    await retry.evaluate(el => el.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await page.waitForTimeout(150)
    assert.equal(state.requests, stoppedCount + 1)
    release(); await page.getByRole('alert').waitFor({ state: 'detached' })
    assert.equal(await search.evaluate(el => el === document.activeElement), true)
    assert.equal(await retry.count(), 0)
    await page.emulateMedia({ reducedMotion: 'no-preference' })
    state.hold = true
    await waitFor(() => state.pending.length === 1)
    assert.equal(state.requests, stoppedCount + 2)
    release(); await waitFor(() => state.inFlight === 0)
    assert.equal(state.maxInFlight, 1)
    passed.push('failure preserves logs and pauses polling, keyboard retry, request guard, focus restored, polling resumes, reduced motion')

    const localCount = state.requests
    await choose('ALL')
    assert.equal(await page.locator('.logs-entry').count(), 4)
    await page.getByRole('combobox', { name: '\u65e5\u5fd7\u6a21\u5757' }).selectOption('Telegram')
    assert.equal(await page.locator('.logs-entry').count(), 1)
    assert.equal(await page.locator('.logs-entry-source, .logs-entry-task').count(), 0)
    await search.fill('does-not-exist')
    assert.equal(await page.locator('.logs-entry').count(), 0)
    await page.getByRole('button', { name: '\u6e05\u7a7a\u641c\u7d22' }).focus()
    await page.keyboard.press('Enter')
    assert.equal(await search.evaluate(el => el === document.activeElement), true)
    await page.getByRole('combobox').selectOption('ALL')
    await search.fill('UNIQUE DIAGNOSTIC NEEDLE')
    assert.equal(await page.locator('.logs-entry').count(), 1)
    assert.equal(await page.locator('.logs-level-badge').innerText(), 'DEBUG')
    assert.equal(state.requests, localCount)
    await search.fill('')
    await page.getByRole('combobox').selectOption('\u641c\u7d22')
    assert.equal(await page.locator('.logs-entry').count(), 1)
    await search.fill('\u8717\u725b')
    assert.equal(await page.locator('.logs-entry').count(), 1)
    assert.equal(await page.getByRole('combobox').locator('option').filter({ hasText: /^\u89c2\u5f71$|^\u8717\u725b$|^\u871c\u67d1$/ }).count(), 0)
    await go('/logs?module=' + encodeURIComponent('\u641c\u7d22') + '&level=ALL')
    assert.equal(await page.getByRole('combobox').inputValue(), '\u641c\u7d22')
    assert.equal(await page.locator('.logs-entry').count(), 1)
    passed.push('unified search module, provider text filtering and direct search-log route')
    await go('/logs?level=DEBUG&module=STRM&taskID=task-debug&keep=example')
    assert.equal(await page.locator('.logs-entry').count(), 1)
    await page.getByRole('button', { name: '\u53d6\u6d88\u4efb\u52a1\u7b5b\u9009' }).click()
    await page.waitForURL('**/logs?level=DEBUG&module=STRM&keep=example')
    assert.equal(await nav.locator('[aria-pressed="true"]').innerText(), 'DEBUG')
    assert.equal(await page.getByRole('combobox').inputValue(), 'STRM')
    state.entries = Array.from({ length: 230 }, (_, index) => entry(index + 100, { message: 'Pagination fixture ' + index }))
    await go('/logs')
    assert.equal(await page.locator('.logs-entry').count(), 100)
    await page.locator('.logs-more button').click()
    assert.equal(await page.locator('.logs-entry').count(), 200)
    await search.fill('Pagination')
    assert.equal(await page.locator('.logs-entry').count(), 100)
    passed.push('local level/module/search/details filtering, clear restores focus, task query preserved, pagination resets')

    state.entries = rows
    await go('/logs?level=ALL')
    const saturated = { INFO: 'rgb(16, 185, 129)', DEBUG: 'rgb(14, 165, 233)', ERROR: 'rgb(239, 68, 68)', WARNING: 'rgb(251, 191, 36)' }
    for (const theme of ['light', 'dark']) {
      await page.evaluate(theme => document.documentElement.classList.toggle('dark', theme === 'dark'), theme)
      await page.waitForTimeout(350)
      for (const [level, color] of Object.entries(saturated)) {
        assert.equal(await page.locator(`.logs-level-badge[data-level="${level}"]`).evaluate(el => getComputedStyle(el).backgroundColor), color)
      }
      for (const width of [1440, 1024, 768, 640, 390, 320]) {
        await page.setViewportSize({ width, height: width < 640 ? 844 : 1000 })
        await page.mouse.move(0, 0)
        await page.waitForTimeout(350)
        assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${theme} ${width}: page overflow`)
        for (const selector of ['.logs-search', '.logs-module']) assert((await page.locator(selector).boundingBox()).height >= 44)
        if (width >= 380) {
          const boxes = await Promise.all(['.logs-search', '.logs-module'].map(selector => page.locator(selector).boundingBox()))
          assert.equal(boxes[0].y, boxes[1].y)
        }
        if (width < 640) {
          for (const button of await nav.getByRole('button').all()) assert((await button.boundingBox()).height >= 44)
        }
        await screenshot(`logs-compact-${width}-${theme}`)
      }
      for (const level of Object.keys(saturated)) {
        await choose(level)
        const geometry = await nav.evaluate(el => {
          const button = el.querySelector('[aria-pressed="true"]'), indicator = el.firstElementChild
          return { b: [button.offsetLeft, button.offsetTop, button.offsetWidth, button.offsetHeight], i: ['left', 'top', 'width', 'height'].map(key => parseFloat(indicator.style[key])), color: getComputedStyle(indicator).backgroundColor }
        })
        assert.deepEqual(geometry.b, geometry.i)
        assert.equal(geometry.color, saturated[level])
      }
      await choose('ALL')
      await page.locator('.logs-details summary').first().click()
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${theme} mobile details overflow`)
      const outOfBounds = await page.locator('.logs-details[open] button').evaluateAll(buttons => buttons.filter(button => button.getBoundingClientRect().right > innerWidth))
      assert.equal(outOfBounds.length, 0)
      const clippedText = await page.locator('.logs-details[open] p').evaluateAll(elements => elements.some(element => element.scrollWidth > element.clientWidth + 1))
      assert.equal(clippedText, false, `${theme} mobile diagnostic text clipped`)
      await screenshot(`logs-compact-details-320-${theme}`)
      await page.locator('.logs-details summary').first().click()
    }
    passed.push('saturated selector and badges, indicator alignment, 320–1440px light/dark layout and touch targets')

    assert.equal(state.fileRequests, 0, 'history files were read before opening history')
    const history = page.locator('.logs-history'), historySummary = history.locator(':scope > summary')
    await historySummary.click()
    await history.locator('.logs-history-files li').first().waitFor()
    assert.equal(await history.locator('.logs-history-files li').count(), 2)
    assert((await historySummary.boundingBox()).height >= 44)
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'history list overflows at 320px')
    for (const button of await history.getByRole('button').all()) assert((await button.boundingBox()).height >= 44)
    assert.match(await history.innerText(), /20 MB/)
    await page.waitForTimeout(2150)
    assert.equal(state.fileRequests, 1, 'normal polling refreshed the history file list')
    await historySummary.click(); await historySummary.click()
    await page.waitForTimeout(100)
    assert.equal(state.fileRequests, 1, 'reopening history unexpectedly reread metadata')
    await history.getByRole('button', { name: '\u5237\u65b0\u5217\u8868' }).click()
    await waitFor(() => state.fileRequests === 2)
    const downloadEvent = page.waitForEvent('download')
    await history.getByRole('button', { name: '\u4e0b\u8f7d cinema115.log', exact: true }).click()
    const download = await downloadEvent
    assert.equal(download.suggestedFilename(), 'cinema115.log')
    assert.match(fs.readFileSync(await download.path(), 'utf8'), /Historical fixture/)
    await page.waitForFunction(() => window.__logURLs.created === 1 && window.__logURLs.revoked === 1)
    assert(requests.filter(request => request.endpoint.startsWith('/api/admin/logs/files')).every(request => request.authorization === 'Bearer mock-logs-token'))
    state.downloadStatus = 500
    await history.getByRole('button', { name: '\u4e0b\u8f7d cinema115.log', exact: true }).click()
    await history.locator('.logs-history-error').waitFor()
    assert.match(await history.locator('.logs-history-error').innerText(), /Mock download failure/)
    state.downloadStatus = 200; state.filesStatus = 500
    await history.getByRole('button', { name: '\u5237\u65b0\u5217\u8868' }).click()
    await page.waitForFunction(() => document.querySelector('.logs-history-error')?.textContent.includes('Mock file list failure'))
    assert.equal(await history.locator('.logs-history-files li').count(), 2, 'history read failure discarded prior metadata')
    state.filesStatus = 200
    await history.getByRole('button', { name: '\u5237\u65b0\u5217\u8868' }).click()
    await history.locator('.logs-history-error').waitFor({ state: 'detached' })
    await screenshot('logs-history-320-dark')
    await historySummary.click()
    passed.push('history reads only on demand, manual refresh, authenticated JSONL download, Blob cleanup, file errors and 320px touch layout')

    await go('/logs')
    const incrementalStart = state.requests
    state.entries = [entry(20, { message: 'Incremental fixture' }), ...rows]
    await page.getByText('Incremental fixture', { exact: true }).waitFor()
    assert.equal(await page.locator('.logs-entry').count(), 2)
    assert(requests.some(request => request.endpoint === '/api/admin/logs' && request.after === '13'))
    const incrementalDone = state.requests
    await waitFor(() => state.requests > incrementalDone)
    assert.equal(await page.getByText('Incremental fixture', { exact: true }).count(), 1, 'incremental poll duplicated a record')
    state.entries = [entry(1, { message: 'Restarted fixture' })]; state.reset = true
    await page.getByText('Restarted fixture', { exact: true }).waitFor()
    assert.equal(await page.locator('.logs-entry').count(), 1, 'cursor reset did not replace old cache')
    state.reset = false; state.entries = [entry(2, { level: 'DEBUG', message: 'Old instance debug fixture' }), entry(1, { message: 'Persisted info fixture' })]
    await go('/logs?level=ALL')
    await page.getByText('Old instance debug fixture', { exact: true }).waitFor()
    state.entries = [entry(2, { message: 'Same cursor new instance fixture' }), entry(1, { message: 'Persisted info fixture' })]; state.instanceID = 'instance-b'
    await page.getByText('Same cursor new instance fixture', { exact: true }).waitFor()
    assert.equal(await page.getByText('Old instance debug fixture', { exact: true }).count(), 0, 'instance reset retained the old DEBUG event with a reused ID')
    assert(requests.some(request => request.endpoint === '/api/admin/logs' && request.after === '2' && request.instanceID === 'instance-a'), 'incremental request omitted the instance identity')
    state.reset = false; state.persistError = 'Mock disk full'; state.failedWrites = 2
    await page.locator('.logs-persistence').waitFor()
    assert.match(await page.locator('.logs-persistence').innerText(), /2/)
    assert.match(await page.locator('.logs-persistence').innerText(), /Mock disk full/)
    state.persistError = ''
    await page.waitForFunction(() => document.querySelector('.logs-persistence')?.textContent.includes('\u65e5\u5fd7\u5199\u5165\u5df2\u6062\u590d'))
    assert.match(await page.locator('.logs-persistence').innerText(), /2/)
    assert.doesNotMatch(await page.locator('.logs-persistence').innerText(), /Mock disk full/)
    state.persistEnabled = false
    await page.waitForFunction(() => document.querySelector('.logs-persistence')?.textContent.includes('\u6587\u4ef6\u6301\u4e45\u5316\u672a\u542f\u7528'))
    assert.doesNotMatch(await page.locator('.logs-persistence').innerText(), /\u5df2\u6062\u590d/)
    state.persistEnabled = true; state.failedWrites = 0
    await page.locator('.logs-persistence').waitFor({ state: 'detached' })
    assert(state.requests > incrementalStart)
    passed.push('cursor incremental merge, empty delta preservation, restart and same-cursor instance reset, persistence failure/recovery and retained failed-write count')

    await page.setViewportSize({ width: 1440, height: 1000 })
    state.entries = []; await go('/logs')
    assert.match(await page.locator('.logs-empty').innerText(), /\u6682\u65e0\u65e5\u5fd7/)
    state.status = 500
    await page.goto(origin + '/logs')
    await page.getByRole('alert').waitFor()
    assert.match(await page.locator('.logs-empty').innerText(), /\u6682\u65f6\u65e0\u6cd5\u8bfb\u53d6/)
    await page.setViewportSize({ width: 320, height: 844 })
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
    assert((await retry.boundingBox()).height >= 44)
    await screenshot('logs-compact-error-320')
    state.hold = true
    await retry.click(); await waitFor(() => state.pending.length === 1)
    assert.match(await retry.locator('svg').evaluate(el => getComputedStyle(el).animationName), /^logs-refresh-spin(?:-|$)/)
    release(); await retry.waitFor({ state: 'visible' })
    await page.waitForFunction(() => document.querySelector('button.logs-retry')?.getAttribute('aria-busy') === 'false')
    const repeatedFailureCount = state.requests
    await page.waitForTimeout(2150)
    assert.equal(state.requests, repeatedFailureCount)
    passed.push('initial empty/error, compact mobile error action, repeated failure stays paused')

    state.status = 401
    await page.goto(origin + '/logs?level=DEBUG&module=STRM&taskID=task-debug')
    await page.getByRole('alert').waitFor()
    assert.equal(await retry.count(), 0)
    const login = page.locator('a.logs-retry'), loginURL = new URL(await login.getAttribute('href'), origin)
    assert.equal(loginURL.pathname, '/admin/login')
    assert.equal(loginURL.searchParams.get('redirect'), '/logs?level=DEBUG&module=STRM&taskID=task-debug')
    const expiredCount = state.requests
    await page.waitForTimeout(2150)
    assert.equal(state.requests, expiredCount)
    await login.click(); await page.waitForURL('**/admin/login?**')
    passed.push('401 stops polling, offers login instead of retry, preserves filtered return route')

    state.status = 200; state.entries = rows
    await page.setViewportSize({ width: 1440, height: 1000 })
    await go('/logs')
    state.hold = true
    await waitFor(() => state.pending.length === 1)
    const unmountedCount = state.requests
    const aborts = await page.evaluate(() => window.__logsAborts)
    await page.locator('a[href="/115"]').first().click()
    await page.waitForURL('**/115')
    assert(await page.evaluate(() => window.__logsAborts) > aborts)
    release()
    await page.waitForTimeout(2150)
    assert.equal(state.requests, unmountedCount)
    passed.push('initial loading/empty/error, unmount aborts in-flight read and stops polling')
    assert.deepEqual(errors, [])
    assert(requests.every(request => request.method === 'GET'))
    console.log(JSON.stringify({ passed, maxInFlight: state.maxInFlight, pageErrors: errors, mockedRequests: requests.length }, null, 2))
    await context.close()
  } finally { state.pending.splice(0).forEach(resolve => resolve()); await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
