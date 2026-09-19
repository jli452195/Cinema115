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
    const errors = [], searches = [], submissions = [], bangumiRequests = [], imageRequests = [], externalImageRequests = []
    page.on('pageerror', e => errors.push(e.message))
    let role = 'admin', configured = false, fail = false, failBangumi = false, fallbackUsed = false, coverRecovered = false
    const bangumis = [{ id: '1940', title: '一弦定音！', coverUrl: '', url: 'https://mikanani.me/Home/Bangumi/1940' }, { id: '2077', title: '一弦定音！第2部分', coverUrl: '/api/mikan/image/201910/06ced9ab.jpg', url: 'https://mikanani.me/Home/Bangumi/2077' }, { id: '9999', title: '旧版远程封面不直连', coverUrl: 'https://mikanani.me/images/Bangumi/201904/bb09460c.jpg', url: 'https://mikanani.me/Home/Bangumi/9999' }]
    let genres = [{ id: 16, name: '动画' }], countries = ['JP']
    const items = Array.from({ length: 61 }, (_, i) => ({ id: i.toString(16).padStart(40, '0'), title: `[测试字幕组] Re：从零开始的异世界生活 第四季 - ${i + 1} [${i % 2 ? '2160p' : '1080p'}][简繁内封]`, size: '1.1 GB', updatedAt: '2026/09/17 02:57', resolution: i % 2 ? '2160P' : '1080P', magnet: 'magnet:?xt=urn:btih:' + i.toString(16).padStart(40, '0'), url: 'https://mikanani.me/Home/Episode/' + i.toString(16).padStart(40, '0'), torrentUrl: '' }))
    await page.route('https://mikanani.me/**', route => { externalImageRequests.push(route.request().url()); return route.fulfill({ status: 404, body: '' }) })
    await page.route('**/api/**', async route => {
      const u = new URL(route.request().url())
      if (u.pathname.startsWith('/api/mikan/image/')) {
        imageRequests.push(u.pathname + u.search)
        // 仅用内存图片模拟 NAS 返回，不连接蜜柑或提交真实任务。
        return route.fulfill(coverRecovered ? { status: 200, contentType: 'image/png', body: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=', 'base64') } : { status: 503, body: '' })
      }
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
          if (fallbackUsed) {
            data.query = input.fallback
            data.attempts = [{ query: input.query, count: 0, cached: false }, { query: input.fallback, count: rows.length, cached: false }]
          }
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
    await page.getByRole('dialog', { name: '蜜柑搜索', exact: true }).waitFor()
    // 本地品牌图加载成功，保留弹窗语义；Logo 不增加链接或外站请求。
    await page.waitForFunction(() => {
      const images = [...document.querySelectorAll('[data-mikan-brand] img')]
      return images.length === 2 && images.every(img => img.complete && img.naturalWidth > 0)
    })
    const brand = await page.locator('[data-mikan-brand] img').evaluateAll(images => images.map(img => ({ local: new URL(img.src).origin === location.origin, height: img.getBoundingClientRect().height })))
    assert.deepEqual(brand, [{ local: true, height: 48 }, { local: true, height: 26 }])
    assert.equal(await page.locator('[data-mikan-brand] a').count(), 0)
    const headerLayout = await page.locator('dialog header').evaluate(header => {
      const brand = header.querySelector('[data-mikan-brand]').getBoundingClientRect()
      const title = header.querySelector('[data-mikan-brand-title]').getBoundingClientRect()
      return { height: header.getBoundingClientRect().height, titleAfterLogo: title.left >= brand.right, titleOffset: (title.top + title.bottom) / 2 - (brand.top + brand.bottom) / 2 }
    })
    assert.ok(headerLayout.height <= 74)
    assert.equal(headerLayout.titleAfterLogo, true)
    assert.equal(headerLayout.titleOffset, 4)
    await page.screenshot({ path: path.join(os.tmpdir(), 'cinema-mikan-brand-desktop.png') })
    await count(50)
    assert.equal(searches[0].query, 'Re：从零开始的异世界生活')
    assert.equal(searches[0].fallback, 'Re:ゼロから始める異世界生活')
    await page.getByRole('button', { name: '重试封面', exact: true }).waitFor()
    assert.ok(imageRequests[0].startsWith('/api/mikan/image/'))
    coverRecovered = true
    await page.getByRole('button', { name: '重试封面', exact: true }).click()
    await page.waitForFunction(() => [...document.querySelectorAll('[aria-label="蜜柑相关番剧"] img')].some(img => img.complete && img.naturalWidth > 0))
    assert.ok(imageRequests.at(-1).includes('?retry='))
    assert.equal(await page.getByRole('button', { name: '重试封面', exact: true }).count(), 0)
    // 精简后不再展示过程面板；标题覆盖海报底部，卡片保持统一比例及完整可访问名称。
    assert.equal(await page.locator('[aria-label="蜜柑搜索过程"]').count(), 0)
    assert.equal(await page.getByText(/取得时间：|缓存有效至|不自动匹配 TMDB/).count(), 0)
    assert.equal(await page.getByText('中文标题未找到资源，已尝试原名：', { exact: false }).count(), 0)
    const card = page.getByRole('button', { name: '查看番剧：一弦定音！', exact: true })
    assert.equal(await card.getAttribute('title'), '一弦定音！')
    const cardLayout = await card.evaluate(el => {
      const box = el.getBoundingClientRect(), title = el.querySelector('.line-clamp-3'), titleBox = title.getBoundingClientRect()
      return { ratio: box.width / box.height, clamp: getComputedStyle(title).webkitLineClamp, color: getComputedStyle(title).color, inside: titleBox.top >= box.top && titleBox.bottom <= box.bottom }
    })
    assert.ok(Math.abs(cardLayout.ratio - 5 / 7) < 0.02)
    assert.equal(cardLayout.clamp, '3')
    assert.equal(cardLayout.color, 'rgb(255, 255, 255)')
    assert.equal(cardLayout.inside, true)
    await page.getByRole('button', { name: '加载更多（剩余 11 条）', exact: true }).click()
    await count(61)
    await page.getByRole('combobox', { name: '蜜柑分辨率', exact: true }).click()
    const selectedResolution = page.getByRole('option', { name: '全部分辨率', exact: true })
    assert.equal(await selectedResolution.getAttribute('aria-selected'), 'true')
    const colors = await selectedResolution.evaluate(el => ({ background: getComputedStyle(el).backgroundColor, color: getComputedStyle(el).color }))
    assert.deepEqual(colors, { background: 'rgb(255, 247, 237)', color: 'rgb(194, 65, 12)' })
    await page.getByRole('option', { name: '2160P', exact: true }).click()
    await count(30)
    await page.getByRole('textbox', { name: '筛选蜜柑结果' }).fill('不存在')
    await page.getByText('没有符合筛选条件的资源，请调整关键词或分辨率。').waitFor()
    await page.getByRole('textbox', { name: '筛选蜜柑结果' }).fill('')
    await page.getByRole('combobox', { name: '蜜柑分辨率', exact: true }).click()
    await page.getByRole('option', { name: '全部分辨率', exact: true }).click()
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
    const beforeGroupFilter = searches.length
    await page.getByRole('combobox', { name: '蜜柑字幕组', exact: true }).click()
    assert.equal(await page.locator('.mikan-filter-popup input').count(), 0)
    assert.equal(await page.evaluate(() => document.activeElement.tagName), 'BUTTON')
    assert.equal(searches.length, beforeGroupFilter)
    await page.getByRole('combobox', { name: '蜜柑字幕组', exact: true }).press('ArrowDown')
    await page.getByRole('combobox', { name: '蜜柑字幕组', exact: true }).press('Enter')
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
    assert.equal(await card.getAttribute('aria-pressed'), 'true')
    assert.equal(await card.locator('[aria-hidden="true"]').innerText(), '✓')
    await page.getByRole('combobox', { name: '蜜柑字幕组', exact: true }).click()
    await page.getByRole('option', { name: '测试字幕组', exact: true }).click()
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
    assert.equal(await page.getByRole('combobox', { name: '蜜柑字幕组', exact: true }).getAttribute('title'), '测试字幕组')
    assert.equal(await page.getByRole('textbox', { name: '筛选蜜柑结果' }).inputValue(), '第四季')
    assert.equal(searches.length, searchesBeforeBangumi)
    await page.setViewportSize({ width: 390, height: 844 })
    const resolutionControl = page.getByRole('combobox', { name: '蜜柑分辨率', exact: true })
    await resolutionControl.focus()
    await resolutionControl.press('ArrowDown')
    await page.getByRole('listbox', { name: '蜜柑分辨率', exact: true }).waitFor()
    const menuBox = await page.locator('.mikan-filter-popup:popover-open').boundingBox()
    assert.ok(menuBox.x >= 0 && menuBox.x + menuBox.width <= 390 && menuBox.y >= 0 && menuBox.y + menuBox.height <= 844)
    await page.screenshot({ path: path.join(os.tmpdir(), 'cinema-mikan-dropdown-mobile.png') })
    await resolutionControl.press('ArrowDown')
    await resolutionControl.press('Enter')
    assert.equal(await page.getByRole('listbox').count(), 0)
    await resolutionControl.click()
    await resolutionControl.press('Escape')
    assert.equal(await page.getByRole('listbox').count(), 0)
    assert.equal(await page.getByRole('dialog', { name: '蜜柑搜索', exact: true }).count(), 1)
    await resolutionControl.click()
    await page.getByRole('textbox', { name: '筛选蜜柑结果' }).click()
    assert.equal(await page.getByRole('listbox').count(), 0)
    await page.getByRole('combobox', { name: '蜜柑字幕组', exact: true }).click()
    assert.equal(await page.locator('.mikan-filter-popup input').count(), 0)
    assert.equal(await page.evaluate(() => document.activeElement.tagName), 'BUTTON')
    await page.getByRole('combobox', { name: '蜜柑字幕组', exact: true }).press('Tab')
    assert.equal(await page.getByRole('listbox').count(), 0)
    const mobileBrand = await page.locator('[data-mikan-brand] img').evaluateAll(images => images.map(img => img.getBoundingClientRect().height))
    assert.deepEqual(mobileBrand, [40, 22])
    const mobileTitle = await page.locator('[data-mikan-brand-title]').evaluate(el => ({ width: el.getBoundingClientRect().width, nowrap: getComputedStyle(el).whiteSpace, fullTitle: el.getAttribute('title') }))
    assert.ok(mobileTitle.width > 0)
    assert.equal(mobileTitle.nowrap, 'nowrap')
    assert.ok(mobileTitle.fullTitle)
    await page.getByRole('button', { name: '关闭蜜柑搜索' }).isVisible().then(visible => assert.equal(visible, true))
    const screenshot = path.join(os.tmpdir(), 'cinema-mikan-mobile.png')
    await page.screenshot({ path: screenshot })
    const dimensions = await page.locator('dialog').evaluate(el => ({ width: el.getBoundingClientRect().width, scroll: el.scrollWidth, client: el.clientWidth }))
    assert.ok(dimensions.scroll <= dimensions.client + 1)
    await page.getByRole('textbox', { name: '蜜柑搜索词' }).fill('空结果')
    await page.getByRole('button', { name: '搜索', exact: true }).click()
    await page.getByText('蜜柑未找到资源，可修改片名、尝试其他译名或访问原站。').waitFor()
    assert.equal(await page.locator('[aria-label="蜜柑相关番剧"]').count(), 0)
    assert.equal(searches.at(-1).fallback, '')
    fallbackUsed = true
    await page.getByRole('button', { name: '用中文标题', exact: true }).click()
    await page.getByText('中文标题未找到资源，已尝试原名：Re:ゼロから始める異世界生活', { exact: true }).waitFor()
    await count(50)
    fallbackUsed = false
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
    assert.deepEqual(externalImageRequests, [])
    console.log(JSON.stringify({ passed: true, searches: searches.length, bangumiRequests: bangumiRequests.length, imageRequests: imageRequests.length, externalImageRequests: externalImageRequests.length, cloudSubmissions: submissions.length, adminOnly: true, mobile: dimensions, pageErrors: errors, screenshot }))
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
