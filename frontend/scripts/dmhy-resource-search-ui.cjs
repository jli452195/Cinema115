// 所有 API 均使用模拟响应；不访问原站，不创建真实 115 任务。
const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const calls = [], errors = [], passed = []
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light') })
    const state = { role: 'admin', animation: false, emptyFilm: false, fail: false, magnetCases: false, magnetFail: false, magnetHold: false, releaseMagnet: undefined }
    const item = (id, categoryScope = 'film', magnet = true) => ({ id: String(id), title: `幽游白书 资源 ${id} [1080p]`, magnet: magnet ? `magnet:?xt=urn:btih:${id.toString(16).padStart(40, '0')}` : '', url: `https://share.dmhy.org/topics/view/${id}_.html`, torrentUrl: '', size: '1 GB', updatedAt: '2026-11-16 10:00', categoryId: categoryScope === 'film' ? 6 : 1, categoryName: categoryScope === 'film' ? '日剧' : categoryScope === 'supplement' ? '音乐' : '游戏', categoryScope, groupId: '7', fansub: '测试组', publisher: '发布者', seeders: 12, leechers: 3 })
    await context.route('**/api/**', async route => {
      const request = route.request(), endpoint = new URL(request.url()).pathname, body = request.postDataJSON()
      calls.push({ endpoint, body })
      let status = 200, data = { items: [], unread: 0 }
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock', username: 'admin', role: state.role } }
      else if (endpoint.endsWith('/resources/search-settings')) data = { order: ['re0', 'bot', 'channels', 'gying', 'mikan', 'garden', 'dmhy', 'woniu'] }
      else if (endpoint.includes('/tmdb/detail/')) data = { id: 30669, kind: 'tv', title: '幽游白书', originalTitle: 'Yu Yu Hakusho', overview: '模拟详情', posterUrl: '', backdropUrl: '', date: '2023-12-14', rating: 7, votes: 1, genres: state.animation ? [{ id: 16, name: '动画' }] : [{ id: 18, name: '剧情' }], countries: ['JP'], cast: [], seasons: [], collection: null }
      else if (endpoint.endsWith('/dmhy/search')) {
        if (state.fail) { status = 502; data = { error: '模拟原站读取失败' } }
        else {
          const items = state.magnetCases ? [{ ...item(160765, 'film', false), magnetIssue: 'missing' }, { ...item(162636, 'film', false), magnetIssue: 'invalid' }, { ...item(160766, 'film', false), magnetIssue: 'conflict' }] : body.page === 2 ? [item(1), item(6)] : body.group ? [item(1)] : state.emptyFilm ? [item(2, 'supplement')] : [item(1), item(2, 'supplement'), item(3, 'other'), item(4, 'future'), { ...item(5, 'film', false), magnetIssue: 'missing', seeders: -1, leechers: -1 }]
          data = { items, groups: [{ id: '7', name: '测试组' }], specs: Object.fromEntries(items.map(row => [row.id, { resolution: '1080p', languages: ['zh-Hans'] }])), query: body.query, page: body.page, hasMore: body.page === 1 && !body.group, attempts: [], cached: false }
        }
      } else if (endpoint.endsWith('/dmhy/magnet')) {
        if (state.magnetHold) { state.magnetHold = false; await new Promise(resolve => { state.releaseMagnet = resolve }) }
        if (state.magnetFail) { status = 502; data = { error: '模拟原帖磁力读取失败' } }
        else data = { id: body.id, magnet: item(Number(body.id)).magnet, cached: false }
      } else if (endpoint.endsWith('/download-organize/files')) data = { files: [], reason: '模拟空种子清单' }
      else if (endpoint.endsWith('/cloud-download/config')) data = { accountId: 'primary', targetCID: '123', targetPath: '/下载' }
      else if (endpoint.endsWith('/cloud-download/tasks')) data = { accepted: true }
      else if (endpoint.includes('/emby/media/')) data = { media: { state: 'unknown', items: [] }, baseUrl: '', serverId: '' }
      else if (endpoint.includes('/recommendations')) data = { items: [], page: 1, totalPages: 1 }
      else if (endpoint.includes('/follows/')) data = { subscribed: false, libraryState: 'unknown' }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
    })
    const page = await context.newPage(); page.setDefaultTimeout(8000)
    page.on('pageerror', error => errors.push(error.message))
    const searchCalls = () => calls.filter(call => call.endpoint.endsWith('/dmhy/search'))
    const magnetCalls = () => calls.filter(call => call.endpoint.endsWith('/dmhy/magnet'))
    const writeCalls = () => calls.filter(call => call.endpoint.endsWith('/cloud-download/tasks') || call.endpoint.endsWith('/download-organize/files'))
    const rows = page.getByRole('list', { name: '动漫花园资源列表' }).locator('> li')
    const scope = name => page.getByRole('navigation', { name: '动漫花园结果范围' }).getByRole('button', { name, exact: true })
    const open = async () => { await page.getByRole('button', { name: '动漫花园 搜索', exact: true }).click(); await page.getByRole('list', { name: '动漫花园资源列表' }).waitFor({ state: 'attached' }) }
    await page.goto(origin + '/tv/30669', { waitUntil: 'networkidle' })
    assert.equal(searchCalls().length, 0)
    await open(); await rows.nth(1).waitFor()
    assert.deepEqual(searchCalls()[0].body, { query: '幽游白书', fallback: 'Yu Yu Hakusho', group: '', page: 1, refresh: false })
    assert.equal(await scope('影视').getAttribute('aria-pressed'), 'true')
    assert.match(await scope('影视').textContent(), /2/)
    assert.match(await scope('补充').textContent(), /2/)
    assert.match(await scope('全部').textContent(), /5/)
    const noMagnet = rows.filter({ hasText: '资源 5' })
    for (const name of ['复制磁力', '115 云下载', '115 整理']) assert.equal(await noMagnet.getByRole('button', { name, exact: true }).isDisabled(), true)
    assert.equal(await noMagnet.locator('.mikan-resource-meta').getByText(/^(做种|下载) -1$/).count(), 0)
    assert.equal(await rows.first().getByText('做种 12', { exact: true }).count(), 1)
    assert.equal(await noMagnet.getByRole('link', { name: '查看动漫花园原帖', exact: true }).getAttribute('href'), 'https://share.dmhy.org/topics/view/5_.html')
    await scope('补充').click(); assert.equal(await rows.count(), 2)
    assert.equal(await rows.filter({ hasText: '资源 4' }).count(), 1)
    await scope('全部').click(); assert.equal(await rows.count(), 5)
    assert.equal(searchCalls().length, 1)
    passed.push('普通剧集管理员可见；默认影视、补充含未知分类、全部保留游戏；范围切换零网络请求')
    passed.push('列表未提供磁力时保留原帖入口，复制/下载/整理均禁用，不自动读取详情')
    assert.equal(magnetCalls().length, 0)

    await page.getByRole('button', { name: '批量选择', exact: true }).click()
    await rows.first().getByRole('checkbox').check()
    await page.getByRole('button', { name: '读取更多搜索结果', exact: true }).click()
    await rows.nth(5).waitFor()
    assert.equal(await rows.count(), 6)
    assert.equal(await rows.first().getByRole('checkbox').isChecked(), true)
    assert.equal(searchCalls().at(-1).body.page, 2)
    assert.equal(await rows.filter({ hasText: '资源 1' }).count(), 1)
    await page.getByRole('button', { name: '退出选择', exact: true }).click()
    await rows.first().getByRole('button', { name: '115 整理', exact: true }).click()
    await rows.first().locator('.garden-prepare-notice').filter({ hasText: '模拟空种子清单' }).waitFor()
    const organize = calls.find(call => call.endpoint.endsWith('/download-organize/files')).body
    assert.deepEqual(organize, { source: 'dmhy', title: '幽游白书 资源 1 [1080p]', fansub: '测试组', providerId: '1', magnet: item(1).magnet })
    await rows.first().getByRole('button', { name: '115 云下载', exact: true }).click()
    await page.getByText('已提交到 115 云下载：/下载。提交成功不代表下载已完成。', { exact: true }).waitFor()
    assert.equal(calls.filter(call => call.endpoint.endsWith('/cloud-download/tasks')).length, 1)
    passed.push('下一页去重并保留批量选择；整理 source 为 dmhy；普通离线复用现有主账号流程')

    await page.getByRole('button', { name: '用原名', exact: true }).click()
    await page.waitForFunction(() => document.querySelector('[aria-label="动漫花园搜索词"]').value === 'Yu Yu Hakusho')
    await rows.nth(4).waitFor()
    assert.equal(searchCalls().at(-1).body.fallback, '')
    await page.getByRole('navigation', { name: '动漫花园字幕组' }).getByRole('button', { name: '测试组', exact: true }).click()
    await page.waitForFunction(() => document.querySelectorAll('[aria-label="动漫花园资源列表"] > li').length === 1)
    assert.equal(searchCalls().at(-1).body.group, '7')
    await Promise.all([page.waitForResponse(response => response.url().endsWith('/dmhy/search')), page.getByRole('button', { name: '强制刷新', exact: true }).click()])
    assert.equal(searchCalls().at(-1).body.refresh, true)
    assert.equal(searchCalls().at(-1).body.group, '7')
    await page.keyboard.press('Escape'); await page.locator('dialog').waitFor({ state: 'detached' })
    assert.equal(await page.getByRole('button', { name: '动漫花园 搜索', exact: true }).evaluate(element => element === document.activeElement), true)
    passed.push('原名/字幕组/刷新请求正确，Escape 关闭并恢复焦点')

    state.emptyFilm = true
    await open()
    await page.getByText('已加载结果中没有符合当前范围和筛选的资源，可切换范围或继续读取下一页。', { exact: true }).waitFor()
    assert.equal(await rows.count(), 0)
    await page.getByRole('button', { name: '读取更多搜索结果', exact: true }).click(); await rows.nth(1).waitFor()
    passed.push('当前页只有补充分类时，影视范围仍能继续分页读取')
    await page.keyboard.press('Escape'); state.emptyFilm = false; state.magnetCases = true
    await page.reload(); await open(); await rows.nth(2).waitFor()
    const missing = rows.filter({ hasText: '资源 160765' }), invalid = rows.filter({ hasText: '资源 162636' }), conflict = rows.filter({ hasText: '资源 160766' })
    await missing.getByText('列表未提供磁力，可读取原帖', { exact: true }).waitFor()
    await invalid.getByText('列表磁力无法核验，可读取原帖', { exact: true }).waitFor()
    await conflict.getByText('磁力信息冲突，请查看原帖核对', { exact: true }).waitFor()
    assert.equal(await conflict.getByRole('button', { name: '读取磁力', exact: true }).count(), 0)
    for (const name of ['复制磁力', '115 云下载', '115 整理']) assert.equal(await conflict.getByRole('button', { name, exact: true }).isDisabled(), true)
    assert.equal(magnetCalls().length, 0)
    await page.setViewportSize({ width: 320, height: 1000 })
    await missing.scrollIntoViewIfNeeded()
    await fs.mkdir('test-results', { recursive: true })
    await page.screenshot({ path: 'test-results/dmhy-magnet-missing-320.png' })
    assert.equal(await missing.evaluate(element => element.scrollWidth <= element.clientWidth), true)
    assert.ok((await missing.getByRole('button', { name: '读取磁力', exact: true }).boundingBox()).height <= 52)
    await page.setViewportSize({ width: 1440, height: 1000 })
    const writesBeforeResolve = writeCalls().length
    state.magnetHold = true
    await missing.getByRole('button', { name: '读取磁力', exact: true }).click()
    await missing.getByText('正在读取原帖磁力…', { exact: true }).waitFor()
    assert.equal(await missing.getByRole('button', { name: '读取磁力', exact: true }).isDisabled(), true)
    assert.equal(await page.getByRole('button', { name: '批量选择', exact: true }).isDisabled(), true)
    assert.equal(magnetCalls().length, 1)
    assert.deepEqual(magnetCalls()[0].body, { id: '160765' })
    await missing.getByRole('button', { name: '读取磁力', exact: true }).evaluate(button => button.click())
    assert.equal(magnetCalls().length, 1)
    state.releaseMagnet()
    await missing.getByRole('button', { name: '读取磁力', exact: true }).waitFor({ state: 'detached' })
    for (const name of ['复制磁力', '115 云下载', '115 整理']) assert.equal(await missing.getByRole('button', { name, exact: true }).isDisabled(), false)
    assert.equal(await missing.getByRole('button', { name: '复制磁力', exact: true }).evaluate(button => button === document.activeElement), true)
    await page.getByRole('button', { name: '读取更多搜索结果', exact: true }).click()
    await page.getByRole('button', { name: '读取更多搜索结果', exact: true }).waitFor({ state: 'detached' })
    assert.equal(await missing.getByRole('button', { name: '复制磁力', exact: true }).isDisabled(), false)
    assert.equal(await missing.getByRole('button', { name: '读取磁力', exact: true }).count(), 0)
    await page.getByRole('button', { name: '批量选择', exact: true }).click()
    assert.equal(await missing.getByRole('checkbox').isDisabled(), false)
    assert.equal(await conflict.getByRole('checkbox').isDisabled(), true)
    await page.getByRole('button', { name: '退出选择', exact: true }).click()
    assert.equal(writeCalls().length, writesBeforeResolve)
    passed.push('点击才读取单条原帖，防重复与共用busy锁，成功响应式恢复复制/整理/下载/批选，不自动提交115')

    state.magnetFail = true
    await invalid.getByRole('button', { name: '读取磁力', exact: true }).click()
    await invalid.getByText('模拟原帖磁力读取失败', { exact: true }).waitFor()
    assert.equal(await invalid.getByRole('button', { name: '普通离线', exact: true }).count(), 0)
    state.magnetFail = false; state.magnetHold = true
    await invalid.getByRole('button', { name: '读取磁力', exact: true }).click()
    await invalid.getByText('正在读取原帖磁力…', { exact: true }).waitFor()
    const cancelledRequest = page.waitForEvent('requestfailed', request => request.url().endsWith('/dmhy/magnet'))
    await page.keyboard.press('Escape'); await cancelledRequest
    state.releaseMagnet()
    await open(); await rows.nth(2).waitFor()
    assert.equal(await rows.filter({ hasText: '资源 162636' }).getByRole('button', { name: '读取磁力', exact: true }).isDisabled(), false)
    assert.equal(magnetCalls().length, 3)
    assert.equal(writeCalls().length, writesBeforeResolve)
    passed.push('磁力冲突不自动选身份；失败保留原帖与重试；关闭取消请求，迟到结果不写入新弹窗')

    await page.keyboard.press('Escape'); state.magnetCases = false; state.animation = true
    await page.reload(); await open(); await rows.nth(1).waitFor()
    await fs.mkdir('test-results', { recursive: true })
    for (const width of [1440, 320]) {
      await page.setViewportSize({ width, height: width === 320 ? 740 : 1000 })
      for (const theme of ['light', 'dark']) {
        await page.evaluate(theme => document.documentElement.classList.toggle('dark', theme === 'dark'), theme)
        await page.screenshot({ path: `test-results/dmhy-search-${width}-${theme}.png` })
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
        assert.equal(await page.locator('dialog').evaluate(element => element.scrollWidth <= element.clientWidth), true)
      }
    }
    await page.keyboard.press('Escape')
    state.role = 'user'; await page.reload(); await page.locator('h1').filter({ hasText: '幽游白书' }).waitFor()
    assert.equal(await page.getByRole('button', { name: '动漫花园 搜索', exact: true }).count(), 0)
    assert.equal(calls.some(call => call.endpoint.endsWith('/resources/garden/search')), false)
    assert.deepEqual(errors, [])
    passed.push('动画入口、桌面/320px深浅主题无溢出；普通用户无入口；没有 Garden 搜索请求')
    console.log(JSON.stringify({ passed, errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
