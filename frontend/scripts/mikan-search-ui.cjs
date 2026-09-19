// 仅使用模拟接口检查界面，不会向真实 115 提交任务。
const path = require('node:path')
const os = require('node:os')
const assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 } })
    await context.addInitScript(() => {
      localStorage.setItem('admin_token', 'mock')
      Object.defineProperty(navigator, 'clipboard', { value: { writeText: async () => { throw new Error('denied') } } })
    })
    const page = await context.newPage()
    const errors = [], searches = [], submissions = [], bangumiRequests = []
    page.on('pageerror', e => errors.push(e.message))
    let role = 'admin', configured = false, fail = false, failBangumi = false
    const bangumis = [{ id: '1940', title: '一弦定音！', coverUrl: '', url: 'https://mikanani.me/Home/Bangumi/1940' }, { id: '2077', title: '一弦定音！第2部分', coverUrl: 'https://mikanani.me/images/Bangumi/missing.jpg', url: 'https://mikanani.me/Home/Bangumi/2077' }]
    let genres = [{ id: 16, name: '动画' }], countries = ['JP']
    const items = Array.from({ length: 61 }, (_, i) => ({ id: i.toString(16).padStart(40, '0'), title: `[测试字幕组] Re：从零开始的异世界生活 第四季 - ${i + 1} [${i % 2 ? '2160p' : '1080p'}][简繁内封]`, size: '1.1 GB', updatedAt: '2026/09/17 02:57', resolution: i % 2 ? '2160P' : '1080P', magnet: 'magnet:?xt=urn:btih:' + i.toString(16).padStart(40, '0'), url: 'https://mikanani.me/Home/Episode/' + i.toString(16).padStart(40, '0'), torrentUrl: '' }))
    await page.route('https://mikanani.me/**', route => route.fulfill({ status: 404, body: '' }))
    await page.route('**/api/**', async route => {
      const u = new URL(route.request().url())
      let data = {}, status = 200
      if (u.pathname === '/api/auth/me') data = { user: { id: 'mock', username: 'mock', role, disabled: false } }
      else if (u.pathname.includes('/tmdb/detail/')) data = { id: 65942, kind: u.pathname.includes('/movie/') ? 'movie' : 'tv', title: 'Re：从零开始的异世界生活', originalTitle: 'Re:ゼロから始める異世界生活', overview: '测试简介', posterUrl: '', backdropUrl: '', date: '2016-04-04', rating: 8, votes: 1, tagline: '', status: 'Returning Series', runtime: 24, episodeCount: 50, genres, countries, cast: [], seasons: [], collection: null, imdbId: '', imdbUrl: '', tmdbUrl: '' }
      else if (u.pathname.endsWith('/mikan/search')) {
        const input = route.request().postDataJSON()
        searches.push(input)
        if (fail) { status = 502; data = { error: '蜜柑访问受限，请稍后重试' } }
        else {
          const rows = input.query === '空结果' ? [] : input.group ? items.slice(0, 2) : items
          data = { items: rows, bangumis: input.query === '空结果' ? [] : bangumis, groups: [{ id: '615', name: '测试字幕组' }], query: input.query, group: input.group, cached: !input.refresh, fetchedAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 600000).toISOString(), attempts: [{ query: input.query, count: rows.length, cached: !input.refresh }] }
        }
      } else if (u.pathname.endsWith('/mikan/bangumi')) {
        const input = route.request().postDataJSON()
        bangumiRequests.push(input)
        if (failBangumi) { status = 502; data = { error: '番剧读取暂时失败' } }
        else {
          const rows = input.group ? items.slice(0, 4).map(item => ({ ...item, groupId: '615' })) : items.slice(0, 2).map((item, i) => ({ ...item, groupId: i ? '19' : '615' }))
          data = { items: rows, bangumi: bangumis.find(item => item.id === input.id), groups: [{ id: '615', name: '测试字幕组' }, { id: '19', name: '其他字幕组' }], more: input.group ? [] : [{ groupId: '615', take: 65 }], attempts: [], fetchedAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 600000).toISOString() }
        }
      } else if (u.pathname.endsWith('/cloud-download/config')) data = { accountId: configured ? 'account-mikan-test' : '', targetCID: '987', targetPath: '/测试目录' }
      else if (u.pathname.endsWith('/cloud-download/tasks')) { submissions.push({ account: u.searchParams.get('accountId'), ...route.request().postDataJSON() }); data = { accepted: true } }
      else if (u.pathname.includes('/emby/media/')) data = { media: { state: 'unknown', items: [] }, baseUrl: '', serverId: '' }
      else if (u.pathname.includes('/recommendations')) data = { items: [], page: 1, totalPages: 1 }
      else if (u.pathname.includes('/follows/')) data = { subscribed: false, libraryState: 'unknown' }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const rows = page.locator('[aria-label="蜜柑资源列表"] > li')
    const count = async n => { await page.waitForFunction(n => document.querySelectorAll('[aria-label="蜜柑资源列表"] > li').length === n, n) }
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/tv/65942')
    await page.getByRole('button', { name: '蜜柑搜索', exact: true }).click()
    await count(50)
    assert.equal(searches[0].query, 'Re：从零开始的异世界生活')
    assert.equal(searches[0].fallback, 'Re:ゼロから始める異世界生活')
    await page.getByRole('button', { name: '加载更多（剩余 11 条）', exact: true }).click()
    await count(61)
    await page.getByRole('combobox', { name: '蜜柑分辨率' }).selectOption('2160P')
    await count(30)
    await page.getByRole('textbox', { name: '筛选蜜柑结果' }).fill('不存在')
    await page.getByText('没有符合筛选条件的资源，请调整关键词或分辨率。').waitFor()
    await page.getByRole('textbox', { name: '筛选蜜柑结果' }).fill('')
    await page.getByRole('combobox', { name: '蜜柑分辨率' }).selectOption('全部')
    await page.getByRole('button', { name: '复制磁力', exact: true }).first().click()
    await page.getByRole('textbox', { name: '手动复制磁力' }).waitFor()
    await page.getByRole('button', { name: '115 转存', exact: true }).first().click()
    await page.getByRole('link', { name: '前往配置', exact: true }).waitFor()
    assert.equal(submissions.length, 0)
    configured = true
    await page.getByRole('button', { name: '115 转存', exact: true }).first().click()
    await page.getByRole('button', { name: '已提交', exact: true }).first().waitFor()
    assert.equal(submissions[0].account, 'account-mikan-test')
    assert.equal(submissions[0].targetCID, '987')
    assert.match(submissions[0].url, /^magnet:/)
    await page.getByRole('combobox', { name: '蜜柑字幕组' }).selectOption('615')
    await count(2)
    assert.equal(searches.at(-1).group, '615')
    assert.equal(searches.at(-1).fallback, '')
    await page.getByRole('button', { name: '强制刷新', exact: true }).click()
    await count(2)
    assert.equal(searches.at(-1).refresh, true)
    // 番剧按编号读取；失败保留已加载内容，返回恢复搜索筛选且不额外请求。
    await page.getByRole('textbox', { name: '筛选蜜柑结果' }).fill('第四季')
    const searchesBeforeBangumi = searches.length
    await page.getByRole('button', { name: '查看番剧：一弦定音！', exact: true }).click()
    await page.getByText('番剧资源：一弦定音！', { exact: true }).waitFor()
    await page.getByRole('button', { name: '加载更多：测试字幕组', exact: true }).waitFor()
    await count(2)
    assert.equal(bangumiRequests.at(-1).id, '1940')
    await page.getByRole('combobox', { name: '蜜柑字幕组' }).selectOption('615')
    await count(1)
    failBangumi = true
    await page.getByRole('button', { name: '加载更多：测试字幕组', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '番剧读取暂时失败' }).waitFor()
    await count(1)
    failBangumi = false
    await page.getByRole('button', { name: '加载更多：测试字幕组', exact: true }).click()
    await count(4)
    assert.equal(bangumiRequests.at(-1).group, '615')
    assert.equal(bangumiRequests.at(-1).take, 65)
    assert.equal(await page.getByRole('button', { name: '加载更多：测试字幕组', exact: true }).count(), 0)
    await page.getByRole('button', { name: '查看番剧：一弦定音！第2部分', exact: true }).click()
    await count(2)
    assert.equal(bangumiRequests.at(-1).id, '2077')
    await page.getByRole('button', { name: '强制刷新', exact: true }).click()
    await page.waitForFunction(() => !document.querySelector('[aria-label="蜜柑搜索词"]').disabled)
    assert.equal(bangumiRequests.at(-1).refresh, true)
    await page.getByRole('button', { name: '返回搜索结果', exact: true }).click()
    await count(2)
    assert.equal(await page.getByRole('combobox', { name: '蜜柑字幕组' }).inputValue(), '615')
    assert.equal(await page.getByRole('textbox', { name: '筛选蜜柑结果' }).inputValue(), '第四季')
    assert.equal(searches.length, searchesBeforeBangumi)
    await page.setViewportSize({ width: 390, height: 844 })
    const screenshot = path.join(os.tmpdir(), 'cinema-mikan-mobile.png')
    await page.screenshot({ path: screenshot })
    const dimensions = await page.locator('dialog').evaluate(el => ({ width: el.getBoundingClientRect().width, scroll: el.scrollWidth, client: el.clientWidth }))
    assert.ok(dimensions.scroll <= dimensions.client + 1)
    await page.getByRole('textbox', { name: '蜜柑搜索词' }).fill('空结果')
    await page.getByRole('button', { name: '搜索', exact: true }).click()
    await page.getByText('蜜柑未找到资源，可修改片名、尝试其他译名或访问原站。').waitFor()
    assert.equal(await page.locator('[aria-label="蜜柑相关番剧"]').count(), 0)
    assert.equal(searches.at(-1).fallback, '')
    fail = true
    await page.getByRole('button', { name: '强制刷新', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '蜜柑访问受限' }).waitFor()
    assert.equal(await rows.count(), 0)
    await page.getByRole('button', { name: '关闭蜜柑搜索' }).click()
    assert.equal(await page.locator('dialog').count(), 0)
    assert.notEqual(await page.evaluate(() => document.body.style.overflow), 'hidden')
    // 类型而非国家决定入口；动画不是第一个类型时也应显示，其他搜索按钮保持不变。
    const visibilityCases = [
      { kind: 'tv', country: 'CN', ids: [18, 16], visible: true },
      { kind: 'movie', country: 'JP', ids: [16], visible: true },
      { kind: 'movie', country: 'CN', ids: [16], visible: true },
      { kind: 'tv', country: 'US', ids: [16], visible: true },
      { kind: 'tv', country: 'JP', ids: [18], visible: false },
      { kind: 'movie', country: 'CN', ids: [28], visible: false },
      { kind: 'tv', country: 'CN', ids: [], visible: false },
    ]
    for (const scenario of visibilityCases) {
      genres = scenario.ids.map(id => ({ id, name: id === 16 ? '动画' : '剧情' }))
      countries = [scenario.country]
      await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + `/${scenario.kind}/65942`)
      await page.getByRole('heading', { name: 'Re：从零开始的异世界生活', exact: true }).waitFor()
      assert.equal(await page.getByRole('button', { name: '蜜柑搜索', exact: true }).count(), scenario.visible ? 1 : 0, JSON.stringify(scenario))
      assert.equal(await page.getByRole('button', { name: 'Bot 搜索', exact: true }).count(), 1)
      assert.equal(await page.getByRole('button', { name: '观影搜索', exact: true }).count(), 1)
    }
    genres = [{ id: 16, name: '动画' }]
    role = 'user'
    await page.reload()
    await page.getByRole('heading', { name: 'Re：从零开始的异世界生活', exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: '蜜柑搜索', exact: true }).count(), 0)
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed: true, searches: searches.length, bangumiRequests: bangumiRequests.length, cloudSubmissions: submissions.length, adminOnly: true, mobile: dimensions, pageErrors: errors, screenshot }))
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
