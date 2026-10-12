// All API calls are intercepted. This script never searches a live provider
// or creates real STRM, transfer or cloud-download tasks.
const path = require('node:path')
const os = require('node:os')
const assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const base = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const share = { category: 'pan', type: '115', typeLabel: '115', url: 'https://115cdn.com/s/fixture?password=AbCD', password: 'AbCD', workTitle: '影片 4K HDR [12.73G]' }
const baidu = { category: 'pan', type: 'baidu', typeLabel: '百度', url: 'https://pan.baidu.com/s/fixture?pwd=X1yZ', password: 'X1yZ', workTitle: '影片 4K S01E01-E23' }
const item = (id, links, status = 'ready') => ({ id, title: id === '35487' ? '大刑伺候' : id === '35724' ? '卖冰淇淋的人' : '另一个季度', year: 2026, mediaType: 'tv', links, detailStatus: status, coverUrl: '/api/woniu/image/20260621-1/8c1b8e2da15ab048447c27af382f141b.jpg' })
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ permissions: ['clipboard-read', 'clipboard-write'] })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage()
    page.setDefaultTimeout(10000)
    const errors = [], searches = [], writes = [], submissions = []
    const mockRuns = new Map()
    page.on('pageerror', error => errors.push(error.message))
    let mode = 'normal', role = 'admin', configured = true
    await page.route('**/api/**', async route => {
      const request = route.request(), url = new URL(request.url())
      let data = {}, status = 200
      if (url.pathname.startsWith('/api/woniu/image/')) {
        await route.fulfill({ status: 200, contentType: 'image/png', body: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aHo8AAAAASUVORK5CYII=', 'base64') })
        return
      }
      if (request.method() !== 'GET') writes.push(url.pathname)
      if (url.pathname === '/api/auth/me') data = { user: { id: 'mock', role, username: 'mock', disabled: false } }
      else if (url.pathname.includes('/tmdb/detail/')) data = { id: 65942, kind: 'tv', title: '测试影片', originalTitle: 'Original', imdbId: '', genres: [], countries: [], cast: [], seasons: [], overview: '', posterUrl: '', backdropUrl: '' }
      else if (url.pathname.endsWith('/share/automation-config')) data = { settings: { accountId: 'mock-main', generation: { outputDir: 'mock-output', baseUrl: 'http://mock.invalid', videoExtensions: 'mkv', metadataExtensions: 'srt', minSizeMB: 0, recursive: true } } }
      else if (url.pathname.endsWith('/share/generation-runs') && request.method() === 'POST') {
        const input = request.postDataJSON(); submissions.push(input)
        data = { id: input.id, status: 'success', selectedTaskIds: ['mock-task'], items: [{ taskId: 'mock-task', status: 'success', message: '完成', result: { count: 1, directory: 'mock-output' } }] }
        mockRuns.set(input.id, data)
      }
      else if (url.pathname.includes('/share/generation-runs/')) data = mockRuns.get(url.pathname.split('/').pop()) || { items: [], status: 'failed' }
      else if (url.pathname.endsWith('/woniu/config')) {
        if (request.method() === 'DELETE') configured = false
        if (request.method() === 'POST') {
          const input = request.postDataJSON()
          assert.deepEqual(input, { username: 'fixture', password: 'fixture-password' })
          configured = true
        }
        data = { configured, username: configured ? 'fixture' : '', hasSession: configured, resourceOrder: ['115', 'baidu', 'magnet'] }
      }
      else if (url.pathname.endsWith('/woniu/search')) {
        const input = request.postDataJSON(); searches.push(input)
        if (mode === 'session' || mode === 'rate') { status = mode === 'rate' ? 429 : 409; data = { code: mode === 'rate' ? 'RATE_LIMITED' : 'SESSION_EXPIRED', error: mode === 'rate' ? '蜗牛站搜索过于频繁，请稍后重试' : '蜗牛登录状态已失效，请重新登录' } }
        else if (mode === 'delay') { await new Promise(resolve => setTimeout(resolve, 800)); data = { items: [], attempts: [], query: '测试影片', matchedBy: 'title', page: 1, cached: false } }
        else data = {
          query: '测试影片', matchedBy: 'title', page: input.page || 1, nextPage: input.page ? undefined : 2, hasMore: !input.page, cached: false,
          partial: mode === 'partial', failedDetails: mode === 'partial' ? 1 : 0,
          items: mode === 'empty' ? [] : mode === 'partial' ? [item('35487', [baidu]), item('35724', [], 'failed')] : input.page ? [item('35724', [share]), item('third', [baidu])] : [item('35487', [baidu]), item('35724', [share])],
          attempts: [{ method: 'title', query: '测试影片', total: 2, usable: 2 }],
        }
      } else if (url.pathname.endsWith('/gying/config')) data = { configured: true, hasSession: true, resourceOrder: [] }
      else if (url.pathname.endsWith('/gying/search')) data = { query: 'Original', matchedBy: 'title', total: 1, cached: false, items: [item('gying', [baidu])], attempts: [{ method: 'title', query: 'Original', total: 1, usable: 1 }] }
      else if (url.pathname.includes('/recommendations')) data = { items: [], page: 1, totalPages: 1 }
      else if (url.pathname.includes('/emby/media/')) data = { media: { state: 'unknown', items: [] } }
      else if (url.pathname.includes('/follows/')) data = { subscribed: false, libraryState: 'unknown' }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
    })
    async function open() { await page.goto(base + '/tv/65942'); await page.getByRole('button', { name: /蜗牛\s*搜索/ }).click(); await page.locator('[data-search-source="woniu"] .gy-film').first().waitFor() }
    await open()
    await page.locator('.gy-cover img').first().waitFor()
    await page.waitForFunction(() => Array.from(document.querySelectorAll('.gy-cover img')).every(img => img.complete && img.naturalWidth > 0))
    assert.equal(await page.getByRole('heading', { name: '蜗牛搜索', exact: true }).count(), 1)
    assert.equal(await page.getByRole('button', { name: '115 云下载', exact: true }).count(), 0)
    assert.equal(await page.getByRole('button', { name: '分享 STRM', exact: true }).count(), 1)
    const baiduFilm = page.locator('[data-film-id="35487"]')
    assert.equal(await baiduFilm.getByRole('button', { name: '分享 STRM', exact: true }).count(), 0)
    await baiduFilm.locator('[data-gy-action="copy"]').click()
    await baiduFilm.locator('[data-gy-action="copy"]').filter({ hasText: '已复制' }).waitFor()
    assert.match(await page.evaluate(() => navigator.clipboard.readText()), /X1yZ/)
    mode = 'rate'; await page.getByRole('button', { name: '加载更多条目', exact: true }).click()
    await page.getByText('下一页未加载，已加载条目仍可使用。', { exact: true }).waitFor()
    assert.equal(await page.locator('.gy-film').count(), 2)
    assert.equal(await page.getByText('上次结果', { exact: true }).count(), 0)
    mode = 'normal'; await page.getByRole('button', { name: '重试加载更多', exact: true }).click()
    await page.locator('[data-film-id="third"]').waitFor()
    assert.equal(await page.locator('.gy-film').count(), 3)
    assert.deepEqual(searches[1], { kind: 'tv', tmdbId: '65942', page: 2, method: 'title' })
    mode = 'rate'; await page.getByRole('button', { name: '重新搜索', exact: true }).click()
    await page.locator('[role="alert"]').filter({ hasText: '蜗牛站搜索过于频繁，请稍后重试' }).waitFor()
    assert.equal(await page.locator('.gy-film').count(), 3)
    mode = 'session'; await page.getByRole('button', { name: '重新搜索', exact: true }).click()
    await page.getByRole('link', { name: '前往配置', exact: true }).waitFor()
    mode = 'partial'; await page.getByRole('button', { name: '重新搜索', exact: true }).click()
    await page.getByText('此条目详情读取失败，可重新搜索补充。', { exact: true }).waitFor()
    mode = 'normal'; await page.getByRole('button', { name: '重新搜索', exact: true }).click()
    await page.getByRole('button', { name: '分享 STRM', exact: true }).waitFor()
    await page.getByRole('button', { name: '分享 STRM', exact: true }).click()
    await page.getByRole('button', { name: '已生成', exact: true }).waitFor()
    assert.equal(submissions.length, 1)
    assert.equal(submissions[0].inputs.length, 1)
    assert.equal(submissions[0].inputs[0].url, share.url)
    assert.equal(submissions[0].inputs[0].password, 'AbCD')
    assert.equal(submissions[0].inputs[0].cid, '0')
    assert.match(submissions[0].collectionScope, /^woniu\/tv\/65942\//)
    const layouts = []
    for (const width of [1440, 390, 320]) for (const dark of [false, true]) {
      await page.setViewportSize({ width, height: 900 })
      await page.evaluate(value => document.documentElement.classList.toggle('dark', value), dark)
      await page.waitForTimeout(100)
      const layout = await page.evaluate(() => {
        const d = document.querySelector('dialog[open]'), rect = d.getBoundingClientRect()
        const surface = getComputedStyle(d).backgroundColor
        return { left: rect.left, right: rect.right, width: innerWidth, overflow: d.scrollWidth > d.clientWidth + 1, background: surface, films: d.querySelectorAll('.gy-film').length, collapsedPicker: d.querySelector('.gy-item-strip')?.getBoundingClientRect().height === 0 }
      })
      assert.ok(layout.left >= -1 && layout.right <= width + 1 && !layout.overflow && !layout.collapsedPicker, JSON.stringify(layout))
      layouts.push({ width, dark, ...layout })
    }
    await page.screenshot({ path: path.join(os.tmpdir(), 'cinema115-woniu-mobile-dark.png') })
    await page.getByRole('button', { name: '关闭蜗牛搜索', exact: true }).click()
    await page.getByRole('button', { name: /观影\s*搜索/ }).click()
    await page.getByRole('heading', { name: '观影搜索', exact: true }).waitFor()
    await page.locator('[data-film-id="gying"]').waitFor()
    await page.keyboard.press('Escape')
    assert.equal(await page.locator('dialog[open]').count(), 0)
    mode = 'delay'; await page.getByRole('button', { name: /蜗牛\s*搜索/ }).click(); await page.getByRole('button', { name: '关闭蜗牛搜索', exact: true }).click()
    await page.waitForTimeout(900)
    assert.equal(await page.locator('dialog[open]').count(), 0)
    await page.goto(base + '/resources')
    for (const target of ['/gying', '/woniu', '/nanshare', '/telegram/user']) {
      const logo = page.locator(`a[href="${target}"] img`).first()
      await logo.waitFor()
      assert.ok(await logo.evaluate(img => img.complete && img.naturalWidth > 0))
    }
    await page.goto(base + '/woniu')
    assert.equal(await page.getByRole('checkbox').count(), 0)
    assert.equal(await page.getByRole('button', { name: /启用蜗牛搜索|暂停蜗牛搜索/ }).count(), 0)
    await page.getByRole('button', { name: '清除蜗牛配置', exact: true }).click()
    await page.getByRole('button', { name: '确认清除', exact: true }).click()
    await page.getByText('本项目保存的蜗牛登录信息已清除。', { exact: true }).waitFor()
    await page.goto(base + '/tv/65942'); await page.locator('h1').filter({ hasText: '测试影片' }).waitFor(); assert.equal(await page.getByRole('button', { name: /蜗牛\s*搜索/ }).count(), 0)
    await page.goto(base + '/woniu')
    await page.getByLabel('用户名', { exact: true }).fill('fixture')
    await page.getByLabel('密码', { exact: true }).fill('fixture-password')
    await page.getByRole('button', { name: '保存并登录', exact: true }).click()
    await page.getByText('蜗牛账号登录成功，详情页搜索入口已开启。', { exact: true }).waitFor()
    await page.goto(base + '/tv/65942'); await page.getByRole('button', { name: /蜗牛\s*搜索/ }).waitFor()
    role = 'user'; await page.reload(); await page.locator('h1').filter({ hasText: '测试影片' }).waitFor(); assert.equal(await page.getByRole('button', { name: /蜗牛\s*搜索/ }).count(), 0)
    assert.deepEqual(errors, [])
    assert.equal(writes.filter(url => !url.endsWith('/search') && !url.endsWith('/woniu/config') && !url.endsWith('/share/generation-runs')).length, 0, JSON.stringify(writes))
    console.log(JSON.stringify({ passed: true, cases: ['dated-poster', 'share-types', 'copy', 'pagination-retry', 'rate-keeps-results', 'session-config-link', 'partial-details', 'mock-strm-submit', 'desktop-mobile-light-dark', 'gying-regression', 'close-cancels', 'resource-brand-logos', 'credentials-auto-enable', 'ordinary-user-hidden'], layouts, pageErrors: errors, realBusinessWrites: 0 }))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
