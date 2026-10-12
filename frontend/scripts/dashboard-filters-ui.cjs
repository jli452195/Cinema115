// Isolated browser context; all APIs are mocked, including library checks.
const path = require('node:path')
const os = require('node:os')
const assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const base = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const genreNames = ['动作冒险', '动画', '喜剧', '犯罪', '纪录', '剧情', '家庭', '儿童', '悬疑', '新闻', '真人秀', '科幻奇幻', '肥皂剧', '脱口秀', '战争政治', '西部']
const genreIDs = [10759, 16, 35, 80, 99, 18, 10751, 10762, 9648, 10763, 10764, 10765, 10766, 10767, 10768, 37]
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage()
    page.setDefaultTimeout(10000)
    const errors = [], calls = [], layouts = []
    page.on('pageerror', err => errors.push(err.message))
    let mode = 'normal', responseNumber = 0
    await page.route('**/api/**', async route => {
      const url = new URL(route.request().url())
      let data = {}, status = 200
      if (url.pathname === '/api/auth/me') data = { user: { id: 'mock', role: 'admin', username: 'mock', disabled: false } }
      else if (url.pathname.endsWith('/tmdb/genres')) data = { genres: genreIDs.map((id, i) => ({ id, name: genreNames[i] })) }
      else if (url.pathname.endsWith('/tmdb/browse')) {
        const input = Object.fromEntries(url.searchParams); calls.push(input)
        const currentMode = mode, number = ++responseNumber
        if (currentMode === 'slow' || input.region === 'JP') await new Promise(resolve => setTimeout(resolve, 650))
        if (currentMode === 'error') { status = 503; data = { error: '模拟网络异常' } }
        else data = { total: 120, totalPages: 6, items: Array.from({ length: 6 }, (_, i) => ({ id: number * 10 + i, kind: input.kind, title: `测试影片 ${number}-${i}`, posterUrl: '', date: '2021-01-01', rating: 8, votes: 200 })) }
      }
      else if (url.pathname.endsWith('/library-check')) data = { items: [] }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
    })
    const query = () => new URL(page.url()).searchParams
    const cards = page.locator('.catalog-media-grid > a')
    const loaded = () => page.locator('[aria-label="影视列表"][aria-busy="false"]').waitFor()
    await page.goto(base + '/?page=3'); await loaded()
    assert.equal(await cards.count(), 6)
    const oldIDs = await cards.evaluateAll(elements => elements.map(e => e.getAttribute('href')))
    mode = 'slow'; await page.getByRole('group', { name: '影视地区', exact: true }).getByRole('button', { name: '日本', exact: true }).click()
    await page.locator('[aria-label="影视列表"][aria-busy="true"]').waitFor()
    assert.deepEqual(await cards.evaluateAll(elements => elements.map(e => e.getAttribute('href'))), oldIDs)
    assert.equal(query().has('page'), false)
    await loaded(); assert.notDeepEqual(await cards.evaluateAll(elements => elements.map(e => e.getAttribute('href'))), oldIDs)
    mode = 'error'; const goodIDs = await cards.evaluateAll(elements => elements.map(e => e.getAttribute('href')))
    await page.getByRole('group', { name: '影视地区', exact: true }).getByRole('button', { name: '韩国', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '模拟网络异常' }).waitFor()
    assert.deepEqual(await cards.evaluateAll(elements => elements.map(e => e.getAttribute('href'))), goodIDs)
    assert.match(await page.locator('.catalog-load-error').innerText(), /日本.*第 1 页/)
    mode = 'normal'; await page.getByRole('button', { name: '重新加载', exact: true }).click(); await loaded()
    await page.getByRole('button', { name: '更多年份', exact: true }).click()
    const years = page.locator('.catalog-year-menu:popover-open')
    await years.getByRole('button', { name: '1990年代', exact: true }).click()
    await years.getByRole('button', { name: '1998', exact: true }).click(); await loaded()
    assert.equal(query().get('year'), '1998')
    const oldYearTrigger = page.getByRole('button', { name: '1998 年', exact: true })
    assert.equal(await oldYearTrigger.evaluate(el => document.activeElement === el), true)
    await oldYearTrigger.click(); await years.waitFor()
    assert.equal(await years.getByRole('button', { name: '1990年代', exact: true }).getAttribute('aria-pressed'), 'true')
    await page.keyboard.press('ArrowRight')
    assert.equal(await page.evaluate(() => document.activeElement.textContent), '1997')
    await page.keyboard.press('Escape')
    assert.equal(await page.locator('.catalog-year-menu:popover-open').count(), 0)
    assert.equal(await oldYearTrigger.evaluate(el => document.activeElement === el), true)
    await oldYearTrigger.click(); await years.waitFor(); await page.getByRole('group', { name: '影视类别' }).getByRole('button', { name: '全部', exact: true }).click()
    assert.equal(await page.locator('.catalog-year-menu:popover-open').count(), 0)
    await page.goBack(); await loaded(); assert.equal(query().has('year'), false)
    // A slower superseded request must never overwrite the latest region.
    await page.getByRole('group', { name: '影视地区', exact: true }).getByRole('button', { name: '日本', exact: true }).click()
    await page.getByRole('group', { name: '影视地区', exact: true }).getByRole('button', { name: '大陆', exact: true }).click()
    await loaded(); const finalIDs = await cards.evaluateAll(elements => elements.map(e => e.getAttribute('href')))
    await page.waitForTimeout(700)
    assert.equal(query().get('region'), 'CN'); assert.deepEqual(await cards.evaluateAll(elements => elements.map(e => e.getAttribute('href'))), finalIDs)
    await page.getByRole('group', { name: '影视题材', exact: true }).getByRole('button', { name: '犯罪', exact: true }).click(); await loaded()
    await page.getByRole('group', { name: '影视类别', exact: true }).getByRole('button', { name: '动漫', exact: true }).click(); await loaded()
    assert.equal(query().has('genre'), false)
    assert.equal(await page.getByRole('group', { name: '影视题材', exact: true }).getByRole('button', { name: '动画', exact: true }).count(), 0)
    for (const width of [1440, 768, 640, 390, 320]) for (const dark of [false, true]) {
      await page.setViewportSize({ width, height: 900 }); await page.evaluate(value => document.documentElement.classList.toggle('dark', value), dark)
      const mobile = width < 640
      if (mobile) await page.getByRole('button', { name: /^筛选/ }).click()
      await page.waitForTimeout(240)
      const layout = await page.evaluate(() => {
        const dialog = document.querySelector('.catalog-filter-dialog[open]'), d = dialog?.getBoundingClientRect()
        return { overflow: document.documentElement.scrollWidth > document.documentElement.clientWidth, filterHeight: document.querySelector('.catalog-filters').getBoundingClientRect().height, dialog: d ? { left: d.left, right: d.right, top: d.top, bottom: d.bottom, horizontalOverflow: dialog.scrollWidth > dialog.clientWidth + 1, footerVisible: dialog.querySelector('footer').getBoundingClientRect().bottom <= d.bottom + 0.5, background: getComputedStyle(dialog).backgroundColor } : null }
      })
      assert.equal(layout.overflow, false, JSON.stringify({ width, dark, layout }))
      if (mobile) {
        assert.ok(layout.dialog.left >= 11 && layout.dialog.right <= width - 11 && layout.dialog.top >= 11 && layout.dialog.bottom <= 889 && !layout.dialog.horizontalOverflow && layout.dialog.footerVisible, JSON.stringify({ width, dark, layout }))
        if (width === 390) await page.screenshot({ path: path.join(os.tmpdir(), `cinema115-catalog-mobile-${dark ? 'dark' : 'light'}.png`) })
        await page.getByRole('button', { name: '关闭筛选', exact: true }).click()
      } else if (width === 1440) await page.screenshot({ path: path.join(os.tmpdir(), `cinema115-catalog-desktop-${dark ? 'dark' : 'light'}.png`) })
      layouts.push({ width, dark, ...layout })
    }
    // Small landscape windows must keep the year menu and modal actions reachable.
    for (const width of [768, 640]) {
      await page.setViewportSize({ width, height: 300 })
      await page.getByRole('button', { name: '更多年份', exact: true }).click()
      await years.waitFor(); await page.waitForTimeout(180)
      const bounds = await years.evaluate(el => {
        const rect = el.getBoundingClientRect()
        return { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom }
      })
      assert.ok(bounds.left >= 11 && bounds.right <= width - 11 && bounds.top >= 11 && bounds.bottom <= 289, JSON.stringify({ width, bounds }))
      await years.getByRole('button', { name: '1900年代', exact: true }).click()
      await years.getByRole('button', { name: '1901', exact: true }).click(); await loaded()
      assert.equal(query().get('year'), '1901')
      await page.getByRole('button', { name: '1901 年', exact: true }).click(); await years.waitFor()
      await page.keyboard.press('Escape')
      assert.equal(await page.locator('.catalog-year-menu:popover-open').count(), 0)
      await page.getByRole('group', { name: '影视年份', exact: true }).getByRole('button', { name: '全部', exact: true }).click(); await loaded()
    }
    for (const dark of [false, true]) {
      await page.setViewportSize({ width: 390, height: 400 })
      await page.evaluate(value => document.documentElement.classList.toggle('dark', value), dark)
      await page.getByRole('button', { name: /^筛选/ }).click()
      const shortDialog = page.getByRole('dialog', { name: '筛选影视', exact: true })
      await page.waitForTimeout(240)
      await shortDialog.locator('.catalog-dialog-body').evaluate(el => { el.scrollTop = el.scrollHeight })
      await shortDialog.getByRole('button', { name: 'TMDB 评分', exact: true }).click()
      const bounds = await shortDialog.evaluate(el => {
        const rect = el.getBoundingClientRect(), footer = el.querySelector('footer').getBoundingClientRect()
        const body = el.querySelector('.catalog-dialog-body')
        return { top: rect.top, bottom: rect.bottom, footerTop: footer.top, footerBottom: footer.bottom, scrollable: body.scrollHeight > body.clientHeight, overflow: el.scrollWidth > el.clientWidth + 1 }
      })
      assert.ok(bounds.top >= 11 && bounds.bottom <= 389 && bounds.footerTop >= bounds.top && bounds.footerBottom <= bounds.bottom + 0.5 && bounds.scrollable && !bounds.overflow, JSON.stringify({ dark, bounds }))
      await page.keyboard.press('Escape')
    }
    await page.setViewportSize({ width: 390, height: 844 })
    const beforeDraft = page.url(), beforeCalls = calls.length
    await page.getByRole('button', { name: /^筛选/ }).click()
    const dialog = page.getByRole('dialog', { name: '筛选影视', exact: true })
    await dialog.getByRole('button', { name: '韩国', exact: true }).click()
    assert.equal(page.url(), beforeDraft); assert.equal(calls.length, beforeCalls)
    await page.keyboard.press('Escape'); assert.equal(page.url(), beforeDraft)
    await page.getByRole('button', { name: /^筛选/ }).click()
    assert.equal(await dialog.getByRole('button', { name: '大陆', exact: true }).getAttribute('aria-pressed'), 'true')
    await dialog.getByRole('button', { name: '重置', exact: true }).click()
    assert.equal(page.url(), beforeDraft)
    await dialog.getByRole('button', { name: '韩国', exact: true }).click()
    await dialog.getByRole('button', { name: '2000年代', exact: true }).click()
    await dialog.getByRole('button', { name: '2005', exact: true }).click()
    await dialog.getByRole('button', { name: 'TMDB 评分', exact: true }).click()
    await dialog.getByRole('button', { name: '应用筛选', exact: true }).click(); await loaded()
    assert.equal(query().get('region'), 'KR'); assert.equal(query().get('year'), '2005'); assert.equal(query().get('sort'), 'rating')
    await page.getByRole('button', { name: /^筛选/ }).click(); await dialog.getByRole('button', { name: '2005', exact: true }).waitFor()
    await page.mouse.click(2, 2)
    assert.equal(await page.locator('dialog[open]').count(), 0)
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.getByRole('button', { name: /^筛选/ }).click()
    assert.equal(await dialog.evaluate(el => getComputedStyle(el).animationName), 'none')
    await page.getByRole('button', { name: '关闭筛选', exact: true }).click()
    await page.goto(base + '/movie'); await loaded(); assert.equal(calls.at(-1).kind, 'movie')
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed: true, cases: ['retained-loading-results', 'failure-keeps-snapshot', 'reset-page', 'dated-year-exact-selection', 'popover-focus-keyboard-dismiss', 'history', 'cancellation', 'category-genre-reset', 'desktop-mobile-light-dark', 'short-screen-menu-modal', 'draft-cancel-reset-apply', 'reduced-motion', 'movie-tv'], layouts, pageErrors: errors }))
  } finally { await browser.close() }
})().catch(err => { console.error(err); process.exitCode = 1 })
