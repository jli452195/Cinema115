// 所有接口均使用模拟数据；不发送 Telegram 消息，也不向 115 写入真实任务。
const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict'), fs = require('node:fs')
const { selectOption, inputValue, selectOptions } = require('./select-menu-helpers.cjs')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  page.setDefaultTimeout(10000)
  await page.addInitScript(() => { localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light') })
  const errors = [], searches = [], writes = [], ed2kRequests = [], resolves = [], abortedResolves = [], apiRequests = []
  page.on('pageerror', error => errors.push(error.message))
  page.on('console', message => { if (message.text().includes('[Vue warn]')) errors.push(message.text()) })
  const channels = [{ id: '11', title: '115网盘热更频道' }, { id: '12', title: '爱影热更频道' }]
  const makeLink = (episode, serial, resolution = '2160p', releaseGroup = 'ADWeb', platform = 'YK') => {
   const name = `Where.the.Mask.Ends.S01E${episode}.2026.${resolution}.${platform}.WEB-DL.H265.AAC-${releaseGroup}.mkv`
   const hash = serial.toString(16).padStart(32, '0'), sizeBytes = String(1000000000 + serial)
   return { type: 'ed2k', url: `ed2k://|file|${encodeURIComponent(name)}|${sizeBytes}|${hash}|/`, file: { name, hash, sizeBytes }, metadata: { episode: `S01E${episode}`, season: 1, episodeNumber: Number(episode), resolution, dynamicRange: 'SDR', source: 'WEB-DL', platform, releaseGroup, videoCodec: 'HEVC', audio: 'AAC', size: '953.67 MiB', specs: [resolution, 'SDR', 'WEB-DL', 'HEVC', 'AAC'] } }
  }
  const links = ['13', '14', '22', '24'].map((episode, index) => makeLink(episode, index + 1))
  const anotherVersion = makeLink('13', 5, '1080p', 'HHWEB', 'NF')
  const pageLinks = [makeLink('01', 6, '1080p', 'HHWEB', 'NF'), makeLink('02', 7, '1080p', 'HHWEB', 'NF')]
  const message = (id, channel, fileLinks, extra = {}) => ({ id, title: '测试电影', text: '原消息的广告文字及同帖说明 S01E99', channel, sourceUrl: `https://t.me/c/${channel.id}/${id.split(':')[1]}`, postedAt: '2026-11-17T01:00:00Z', links: fileLinks, ...extra })
  const share = message('11:10', channels[0], [{ type: '115', url: 'https://115.com/s/example?password=abcd' }], { metadata: { episode: 'S01E13', specs: ['2160p', 'HDR', 'HEVC'], size: '18.1 GB' } })
  const multi = message('11:11', channels[0], links)
  const duplicate = message('12:12', channels[1], [links[0]])
  const pageURL = 'https://telegra.ph/tmdbid-42-11-17'
  const candidate = message('11:13', channels[0], [], { sources: [{ id: 'source-one', type: 'telegraph', url: pageURL, label: 'ED2K链接' }] })
  const duplicateCandidate = message('12:14', channels[1], [], { sources: [{ id: 'source-two', type: 'telegraph', url: pageURL, label: 'ED2K链接' }] })
  const queueCandidates = [1, 2, 3].map(index => message(`11:${30 + index}`, channels[0], [], { title: '相同片名的不同页面', sources: [{ id: `queue-${index}`, type: 'telegraph', url: `${pageURL}-${index}`, label: 'ED2K链接' }] }))
  const extraVersion = message('12:20', channels[1], [anotherVersion])
  const selectionLinks = Array.from({ length: 545 }, (_, index) => makeLink(String(index + 1).padStart(3, '0'), 1000 + index, index < 500 ? '2160p' : '1080p'))
  const appendedSelectionLinks = [makeLink('546', 1545), makeLink('547', 1546, '1080p')]
  const selectionMessage = message('11:50', channels[0], selectionLinks)
  const selectionCandidate = message('11:51', channels[0], [], { sources: [{ id: 'selection-page', type: 'telegraph', url: `${pageURL}-selection`, label: 'ED2K链接' }] })
  const makeFilterLink = (index, keyword, season, episode, resolution, dynamicRange, platform, releaseGroup) => {
   const coordinate = `S${String(season).padStart(2, '0')}E${String(episode).padStart(2, '0')}`
   const name = ['Filter', keyword, coordinate, resolution, dynamicRange, platform, releaseGroup].filter(Boolean).join('.') + '.mkv'
   const hash = (2000 + index).toString(16).padStart(32, '0'), sizeBytes = String(1000002000 + index)
   return { type: 'ed2k', url: `ed2k://|file|${encodeURIComponent(name)}|${sizeBytes}|${hash}|/`, file: { name, hash, sizeBytes }, metadata: { episode: coordinate, season, episodeNumber: episode, resolution, dynamicRange, platform, releaseGroup, specs: [resolution, dynamicRange].filter(Boolean) } }
  }
  const filterLinks = [
   makeFilterLink(0, 'River', 1, 1, '2160p', 'HDR', 'NF', 'ALPHA'),
   makeFilterLink(1, 'River', 1, 2, '2160p', 'HDR', 'NF', 'ALPHA'),
   makeFilterLink(2, 'River', 1, 3, '2160p', 'SDR', 'NF', 'BETA'),
   makeFilterLink(3, 'Lake', 1, 4, '1080p', 'SDR', 'NF', 'ALPHA'),
   makeFilterLink(4, 'Lake', 1, 5, '1080p', 'SDR', 'AMZN', 'GAMMA'),
   makeFilterLink(5, 'River', 2, 1, '2160p', 'HDR', 'NF', 'ALPHA'),
   makeFilterLink(6, 'Lake', 2, 2, '1080p', 'SDR', 'AMZN', 'GAMMA'),
   makeFilterLink(7, 'Bare', 3, 1),
  ]
  const appendedFilterLinks = [makeFilterLink(8, 'River', 1, 6, '2160p', 'HDR', 'NF', 'ALPHA'), makeFilterLink(9, 'Future', 4, 1, '4320p', 'DV', 'DSNP', 'DELTA')]
  const filterMessage = message('11:60', channels[0], filterLinks)
  const filterCandidate = message('11:61', channels[0], [], { sources: [{ id: 'filter-page', type: 'telegraph', url: `${pageURL}-filters`, label: 'ED2K链接' }] })
  let ed2kTargets = [{ id: 'ed2k-movie', name: '电影', outputDir: 'ed2k/电影' }, { id: 'ed2k-tv', name: '剧集', outputDir: 'ed2k/剧集' }]
  let failMore = false, expired = false, holdSearch = false, releaseSearch, rejectED2K = true, resolveFailure = true, uncertainSTRM = false, moreLoaded = false, queueMode = false, selectionMode = false, filterMode = false, holdResolves = false, maximumResolves = 0
  const resolvedPages = new Set(), heldResolves = new Map(), activeResolves = new Set()
  page.on('requestfailed', request => {
   if (new URL(request.url()).pathname.endsWith('/telegram-user/channels/resolve')) { abortedResolves.push(request.postDataJSON()); activeResolves.delete(request) }
  })
  const releaseHeldResolves = () => { for (const [request, release] of heldResolves) { heldResolves.delete(request); release() } }
  const waitUntil = async (condition, message) => {
   const deadline = Date.now() + 10000
   while (!condition()) { assert.ok(Date.now() < deadline, message); await page.waitForTimeout(25) }
  }
  let run
  const baseResult = () => {
   const standalone = queueMode || selectionMode || filterMode
   const items = filterMode ? [filterMessage, filterCandidate] : selectionMode ? [selectionMessage, selectionCandidate] : queueMode ? [multi, ...queueCandidates] : [share, multi, duplicate, candidate]
   return { items, warnings: standalone ? [] : [{ channel: channels[1], error: '频道读取超时，可加载更多重试' }], hasMore: !standalone, cursor: standalone ? '' : 'page2', partial: !standalone, cached: false, expiresAt: '2099-11-17T02:00:00Z' }
  }
  await page.route('**/api/**', async route => {
   const req = route.request(), url = new URL(req.url()), endpoint = url.pathname
   apiRequests.push({ endpoint, method: req.method() })
   if (endpoint === '/api/auth/me') return route.fulfill({ json: { user: { id: 'admin', role: 'admin', username: 'admin' } } })
   if (endpoint.endsWith('/telegram-user/channels')) return route.fulfill({ json: { account: { configured: true, hasSession: true, botConfigured: false }, channels } })
   if (endpoint.endsWith('/telegram-user/config')) return route.fulfill({ json: { configured: true, hasSession: true, botConfigured: false, bot: '' } })
   if (endpoint.endsWith('/telegram-user/channels/search')) {
    const body = req.postDataJSON(); searches.push(body)
    assert.deepEqual(Object.keys(body).sort(), (body.cursor ? ['cursor', 'kind', 'tmdbId'] : ['kind', 'refresh', 'tmdbId']).sort())
    if (holdSearch) await new Promise(resolve => { releaseSearch = resolve })
    if (body.cursor && expired) return route.fulfill({ status: 409, json: { error: '频道搜索已过期，请重新搜索', code: 'channel_search_expired' } })
    if (body.cursor && failMore) return route.fulfill({ status: 502, json: { error: '网络暂时不可用' } })
    if (body.refresh) moreLoaded = false
    if (body.cursor) moreLoaded = true
    return route.fulfill({ json: moreLoaded ? { ...baseResult(), items: [...baseResult().items, duplicateCandidate, extraVersion], warnings: [], partial: false, hasMore: false, cursor: '', cached: !body.cursor && !body.refresh } : baseResult() })
   }
   if (endpoint.endsWith('/telegram-user/channels/resolve')) {
    const body = req.postDataJSON(); resolves.push(body)
    const record = [candidate, duplicateCandidate, selectionCandidate, filterCandidate, ...queueCandidates].find(item => item.id === body.resourceId && item.sources.some(source => source.id === body.sourceId))
    assert.ok(record, '解析只接受搜索结果中的已有候选')
    assert.deepEqual(Object.keys(body).sort(), ['kind', 'resourceId', 'sourceId', 'tmdbId'])
    assert.equal(body.kind, 'movie'); assert.equal(body.tmdbId, '42')
    if (!queueMode) {
     const expected = filterMode ? filterCandidate : selectionMode ? selectionCandidate : candidate
     assert.deepEqual(body, { resourceId: expected.id, sourceId: expected.sources[0].id, kind: 'movie', tmdbId: '42' }, '同URL固定首个服务端候选，并携带作品身份')
    }
    const sourceURL = record.sources[0].url
    activeResolves.add(req); maximumResolves = Math.max(maximumResolves, activeResolves.size)
    try {
     if (holdResolves) await new Promise(resolve => { heldResolves.set(req, resolve) })
     if (req.failure()) return
     if (resolveFailure) return await route.fulfill({ status: 502, json: { error: '清单读取暂时失败' } })
     const cached = resolvedPages.has(sourceURL)
     resolvedPages.add(sourceURL)
     const queueIndex = queueCandidates.indexOf(record)
     const groups = record === filterCandidate ? [{ id: 'filter-extra', title: '异步追加的筛选样本', links: appendedFilterLinks }] : record === selectionCandidate ? [{ id: 'selection-extra', title: '自动追加文件', links: appendedSelectionLinks }] : queueIndex < 0 ? [{ id: 'nf', title: 'Where.the.Mask.Ends.S01.1080p.NF.WEB-DL.H265.AAC-HHWEB', links: pageLinks }, { id: 'same', title: 'Where.the.Mask.Ends.S01.2160p.YK.WEB-DL.H265.AAC-ADWeb', links: [links[0]] }] : [{ id: 'same-title', title: '相同版本标题', links: [makeLink(String(31 + queueIndex), 31 + queueIndex)] }]
     return await route.fulfill({ json: { sourceId: record.sources[0].id, pageTitle: '测试电影 (2026) {tmdbid-42}', groups, cached, expiresAt: '2099-11-17T02:00:00Z' } })
    } finally { activeResolves.delete(req) }
   }
   if (endpoint === '/api/admin/p115/cloud-download/config') return route.fulfill({ json: { accountId: 'main', targetCID: '123', targetPath: '/Downloads' } })
   if (endpoint === '/api/admin/p115/cloud-download/tasks') {
    const body = req.postDataJSON(); writes.push({ path: endpoint, body })
    assert.equal(url.searchParams.get('accountId'), 'main'); assert.equal(body.targetCID, '123')
    if (body.url === links[1].url) return route.fulfill({ status: 504, body: '', contentType: 'text/plain' })
    return route.fulfill({ json: { accepted: true } })
   }
   if (endpoint === '/api/admin/p115/cloud-download/tasks/batch') {
    const body = req.postDataJSON(); writes.push({ path: endpoint, body })
    assert.equal(url.searchParams.get('accountId'), 'main'); assert.equal(body.targetCID, '123')
    const events = [{ type: 'ready', index: 0, total: body.urls.length }]
    body.urls.forEach((_value, index) => events.push({ type: 'start', index, total: body.urls.length }, { type: 'result', index, total: body.urls.length, accepted: true }))
    events.push({ type: 'done', index: body.urls.length, total: body.urls.length })
    return route.fulfill({ contentType: 'application/x-ndjson', body: events.map(event => JSON.stringify(event)).join('\n') + '\n' })
   }
   if (endpoint === '/api/admin/p115/ed2k-strm') return route.fulfill({ json: { settings: { baseUrl: 'http://nas' }, settingsSaved: true, accountReady: true, primaryAccountId: 'main', targets: ed2kTargets, tasks: [], resources: [] } })
   if (endpoint === '/api/admin/p115/ed2k-strm/imports') {
    const body = req.postDataJSON(); ed2kRequests.push(body)
    assert.ok(body.links.length > 0); assert.ok(ed2kTargets.some(target => target.id === body.targetId)); assert.match(body.requestId, /^[a-f0-9-]{36}$/)
    if (rejectED2K) return route.fulfill({ status: 409, json: { error: '请先保存 ED2K STRM 基础设置' } })
    if (uncertainSTRM) return route.fulfill({ status: 504, contentType: 'text/plain', body: '' })
    writes.push({ path: endpoint, body })
    return route.fulfill({ status: 202, json: { resources: [], tasks: [] } })
   }
   if (endpoint === '/api/admin/p115/share/automation-config') return route.fulfill({ json: { settings: { accountId: 'main', generation: { baseUrl: 'http://nas', outputDir: '/strm', videoExtensions: '.mkv', recursive: true } } } })
   if (endpoint === '/api/admin/p115/share/generation-runs' && req.method() === 'POST') {
    const body = req.postDataJSON(); writes.push({ path: endpoint, body })
    run = { id: body.id, status: 'success', items: [{ taskId: 'task', status: 'success', message: '', result: { count: 1, directory: '/strm/Test' } }] }
    return route.fulfill({ json: run })
   }
   if (endpoint.startsWith('/api/admin/p115/share/generation-runs/')) return route.fulfill({ json: run })
   if (endpoint.includes('/tmdb/detail/')) return route.fulfill({ json: { id: 42, kind: 'movie', title: '测试电影', originalTitle: 'Test Movie', overview: '测试影片', date: '2026-01-01', genres: [], countries: [], cast: [], seasons: [], rating: 0, votes: 0, runtime: 100, episodeCount: 0, status: 'Released', tmdbUrl: '', imdbUrl: '', posterUrl: '', backdropUrl: '' } })
   if (endpoint.includes('/emby/media/')) return route.fulfill({ json: { media: { state: 'absent', items: [] } } })
   if (endpoint.includes('/recommendations/')) return route.fulfill({ json: { items: [], page: 1, totalPages: 1 } })
   if (endpoint.includes('/follows/')) return route.fulfill({ json: { subscribed: false, request: null, libraryState: 'absent' } })
   if (endpoint.includes('/notifications')) return route.fulfill({ json: { items: [], unread: 0 } })
   return route.fulfill({ json: { configured: false, accounts: [] } })
  })
  await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/movie/42')
  const trigger = page.getByRole('button', { name: 'TG 频道搜索', exact: true })
  await trigger.waitFor()
  assert.equal(searches.length, 0, '打开影视详情不发起 Telegram 搜索')
  const dialog = page.locator('dialog.tg-channel-search[open]')
  const open = async () => { await trigger.click(); await dialog.locator('.ed-file').first().waitFor() }
  const row = (episode, resolution = '2160p') => dialog.locator('.ed-file').filter({ has: page.locator('.ed-episode', { hasText: new RegExp(`^S01E${episode}$`) }) }).filter({ has: page.locator('.ed-specs', { hasText: resolution }) })
  const query = dialog.getByRole('searchbox', { name: '筛选频道资源' })
  const sourceProgress = dialog.locator('.tg-load-status').getByRole('status')
  const resourceControl = dialog.getByRole('navigation', { name: '资源类型', exact: true })
  const viewControl = dialog.getByRole('navigation', { name: 'ED2K 展示方式', exact: true })
  const assertMobileToolbar = async reason => {
   const layout = await dialog.evaluate(element => {
    const toolbar = element.querySelector('.ed-toolbar'), parts = [toolbar.querySelector('.ed-filter-toggle'), toolbar.querySelector('[role="combobox"]'), toolbar.querySelector('.segmented-control')]
    const rectangles = parts.map(part => part.getBoundingClientRect()), buttons = [...toolbar.querySelectorAll('.ed-filter-toggle, [role="combobox"], .segmented-control button'), ...element.querySelectorAll('.tg-tabs .segmented-control button')]
    return { sameRow: Math.max(...rectangles.map(rect => rect.bottom)) - Math.min(...rectangles.map(rect => rect.top)) <= Math.max(...rectangles.map(rect => rect.height)) + 1, fits: toolbar.scrollWidth <= toolbar.clientWidth + 1, controls: rectangles.map(rect => ({ left: rect.left, top: rect.top, width: rect.width, height: rect.height })), touchTargets: buttons.map(button => { const rect = button.getBoundingClientRect(); return { label: button.getAttribute('aria-label'), width: rect.width, height: rect.height } }) }
   })
   assert.equal(layout.sameRow, true, `${reason}：筛选、排序和展示方式保持同排，${JSON.stringify(layout)}`)
   assert.equal(layout.fits, true, `${reason}：工具栏无横向溢出`)
   assert.ok(layout.touchTargets.every(target => target.height >= 44 && target.width >= 44), `${reason}：所有切换和工具按钮至少44px点击区，${JSON.stringify(layout.touchTargets)}`)
  }
  await open()
  await sourceProgress.filter({ hasText: /^清单已载入 0 \/ 1 页，失败 1 页$/ }).waitFor()
  assert.equal(await dialog.getByRole('button', { name: '重试失败清单', exact: true }).isEnabled(), true, '失败清单在列表上方提供明确重试入口')
  await dialog.locator('.tg-candidates > summary').click()
  await dialog.getByText('清单读取暂时失败', { exact: true }).waitFor()
  assert.equal(await dialog.getByRole('button', { name: '重试读取', exact: true }).count(), 1)
  await dialog.locator('.tg-candidates > summary').click()
  assert.equal(writes.length, 0); assert.equal(resolves.length, 1, '打开弹窗自动读取唯一清单，失败后等待用户重试')
  assert.equal(await dialog.locator('.ed-file').count(), 4, '同帖四个文件独立成行，跨来源重复文件合并')
  await dialog.getByText('显示 4 / 4 个文件', { exact: true }).waitFor()
  assert.equal(await dialog.getByText('消息内容', { exact: true }).count(), 0)
  await dialog.getByText('部分频道暂不可用（1）', { exact: true }).click()
  await dialog.getByText('爱影热更频道：频道读取超时', { exact: false }).waitFor()
  await query.fill('E13'); assert.equal(await dialog.locator('.ed-file').count(), 1)
  assert.equal(await row('14').count(), 0)
  await query.fill('广告'); await dialog.getByText('没有匹配的文件', { exact: true }).waitFor()
  await query.fill('')
  await row('13').locator('.ed-information').click()
  await row('13').getByText('来源（2）', { exact: true }).waitFor()
  await row('13').getByText(links[0].file.name, { exact: true }).waitFor()
  await row('13').locator('.ed-information').click()
  assert.ok((await row('13').boundingBox()).height <= 72, '桌面文件行保持紧凑')

  assert.equal(await resourceControl.evaluate(element => element.classList.contains('segmented-control-underline')), true, '资源分类采用一级下划线外观')
  const beforeResourceKeys = apiRequests.length
  await resourceControl.getByRole('button', { name: 'ED2K 文件', exact: true }).focus()
  await page.keyboard.press('ArrowRight')
  assert.equal(await resourceControl.getByRole('button', { name: '115 分享', exact: true }).evaluate(element => document.activeElement === element), true)
  assert.equal(await resourceControl.getByRole('button', { name: 'ED2K 文件', exact: true }).getAttribute('aria-pressed'), 'true', '方向键只移动资源分类焦点，不立即切换')
  await page.keyboard.press('Home'); await page.keyboard.press('End'); await page.keyboard.press('Enter')
  const shareLink = dialog.getByRole('link', { name: '打开分享', exact: true })
  await shareLink.waitFor()
  assert.equal(apiRequests.length, beforeResourceKeys, '资源分类方向键、Home/End和Enter切换不发起API请求')
  assert.equal(await shareLink.getAttribute('href'), share.links[0].url)
  await dialog.getByRole('button', { name: '分享 STRM', exact: true }).click()
  await dialog.getByRole('button', { name: '已生成', exact: true }).waitFor()
  assert.equal(writes.at(-1).body.inputs[0].url, share.links[0].url.replace('115.com', '115cdn.com'))
  const beforeReturnKeys = apiRequests.length
  await resourceControl.getByRole('button', { name: '115 分享', exact: true }).focus()
  await page.keyboard.press('Home'); await page.keyboard.press('Enter')
  await row('13').waitFor()
  assert.equal(apiRequests.length, beforeReturnKeys, '键盘返回ED2K资源不重建文件状态或重新请求')

  failMore = true
  await dialog.getByRole('button', { name: '加载更多资源' }).click()
  await dialog.getByRole('alert').filter({ hasText: '网络暂时不可用' }).waitFor()
  assert.equal(await dialog.locator('.ed-file').count(), 4)
  failMore = false
  await dialog.getByRole('button', { name: '重试', exact: true }).click()
  await row('13', '1080p').waitFor()
  assert.equal(await dialog.locator('.ed-file').count(), 5)
  assert.equal(resolves.length, 1, '分页补入同URL的另一来源，不重试已失败的清单')
  await dialog.locator('.tg-merged-hint').filter({ hasText: /2.*1 页/ }).waitFor()
  assert.equal(await dialog.locator('.tg-warnings').count(), 0)
  await query.fill('E13'); assert.equal(await dialog.locator('.ed-file').count(), 2, '同集不同版本保留')
  await query.fill('E13 1080p'); assert.equal(await dialog.locator('.ed-file').count(), 1)
  await query.fill('')
  await dialog.getByRole('button', { name: '规格筛选', exact: true }).click()
  await selectOption(dialog.getByRole('combobox', { name: '筛选清晰度', exact: true }), '1080p')
  assert.equal(await dialog.locator('.ed-file').count(), 1)
  await selectOption(dialog.getByRole('combobox', { name: '筛选清晰度', exact: true }), 'all')
  await selectOption(dialog.getByRole('combobox', { name: '筛选发布组', exact: true }), 'HHWEB')
  assert.equal(await dialog.locator('.ed-file').count(), 1)
  await selectOption(dialog.getByRole('combobox', { name: '筛选发布组', exact: true }), 'all')
  await dialog.getByRole('button', { name: '规格筛选', exact: true }).click()
  assert.equal(await viewControl.evaluate(element => element.classList.contains('segmented-control-subtle')), true, '文件/版本采用二级轻量外观')
  const beforeViewKeys = apiRequests.length
  await viewControl.getByRole('button', { name: '文件列表', exact: true }).focus()
  await page.keyboard.press('ArrowRight')
  assert.equal(await viewControl.getByRole('button', { name: '按版本', exact: true }).evaluate(element => document.activeElement === element), true)
  assert.equal(await viewControl.getByRole('button', { name: '文件列表', exact: true }).getAttribute('aria-pressed'), 'true', '展示方式方向键只移动焦点')
  await page.keyboard.press('Home'); await page.keyboard.press('End'); await page.keyboard.press('Enter')
  assert.equal(await dialog.locator('.ed-group-title').count(), 2)
  await page.keyboard.press('Home'); await page.keyboard.press('Enter')
  assert.equal(await dialog.locator('.ed-group-title').count(), 0)
  assert.equal(apiRequests.length, beforeViewKeys, '文件/版本键盘切换只调整本地展示，不发起API请求')

  await row('13').getByRole('button', { name: 'ED2K STRM', exact: true }).click()
  await dialog.getByText('请选择上方的 STRM 输出目录，然后提交。', { exact: false }).waitFor()
  assert.equal(ed2kRequests.length, 0, '多个输出目录不得静默选择')
  assert.equal(await dialog.getByRole('combobox', { name: 'ED2K STRM 输出目录', exact: true }).count(), 1, '输出目录集中在工具栏')
  await selectOption(dialog.getByRole('combobox', { name: 'ED2K STRM 输出目录', exact: true }), 'ed2k-tv')
  await row('13').getByRole('button', { name: 'ED2K STRM', exact: true }).click()
  await dialog.locator('.ed-notice').getByText('请先保存 ED2K STRM 基础设置', { exact: false }).waitFor()
  rejectED2K = false
  await row('13').getByRole('button', { name: 'ED2K STRM', exact: true }).click()
  await row('13').getByRole('button', { name: '已接收', exact: true }).waitFor()
  assert.equal(await row('13').getByRole('button', { name: '已接收', exact: true }).isDisabled(), true)
  assert.deepEqual(ed2kRequests[0].links, [links[0].url]); assert.equal(ed2kRequests[0].targetId, 'ed2k-tv')
  assert.equal(ed2kRequests[0].requestId, ed2kRequests[1].requestId, '失败重试复用请求编号')

  await row('14').getByRole('button', { name: '115 云下载', exact: true }).click()
  await row('14').getByRole('button', { name: '待核对', exact: true }).waitFor()
  assert.equal(await row('14').getByRole('button', { name: '待核对', exact: true }).isDisabled(), true)
  await row('22').getByRole('button', { name: '115 云下载', exact: true }).click()
  await row('22').getByRole('button', { name: '已提交', exact: true }).waitFor()
  const singles = writes.filter(write => write.path.endsWith('/cloud-download/tasks'))
  assert.deepEqual(singles.map(write => write.body.url), [links[1].url, links[2].url])
  await row('13').getByRole('checkbox').check(); await row('24').getByRole('checkbox').check()
  await dialog.getByRole('button', { name: '云下载所选', exact: true }).click()
  await row('24').getByRole('button', { name: '已提交', exact: true }).waitFor()
  assert.deepEqual(writes.find(write => write.path.endsWith('/cloud-download/tasks/batch')).body.urls, [links[0].url, links[3].url])
  await dialog.getByRole('button', { name: '清空选择', exact: true }).click()
  await row('14').getByRole('checkbox').check(); await row('22').getByRole('checkbox').check()
  await dialog.getByRole('button', { name: 'STRM 所选', exact: true }).click()
  await row('22').getByRole('button', { name: '已接收', exact: true }).waitFor()
  assert.deepEqual(ed2kRequests.at(-1).links, [links[1].url, links[2].url])
  await dialog.getByRole('button', { name: '清空选择', exact: true }).click()

  assert.equal(await dialog.locator('.ed-file').count(), 5, '清单失败保留已加载文件')
  resolveFailure = false
  await dialog.getByRole('button', { name: '重试失败清单', exact: true }).click()
  await row('01', '1080p').waitFor()
  await sourceProgress.filter({ hasText: /^清单已载入 1 \/ 1 页$/ }).waitFor()
  assert.equal(await dialog.locator('.ed-file').count(), 7, '清单版本注入相同文件模型，已有文件继续去重')
  assert.equal(resolves.length, 2, '同页两个来源仅首次失败和主动重试各请求一次')
  await row('01', '1080p').locator('.ed-information').click()
  await row('01', '1080p').getByText('来源（2）', { exact: true }).waitFor()
  assert.deepEqual((await row('01', '1080p').locator('.ed-sources a').evaluateAll(elements => elements.map(element => element.href))).sort(), [candidate.sourceUrl, duplicateCandidate.sourceUrl].sort(), '解析文件保留跨帖子与分页的全部TG来源')
  await row('01', '1080p').locator('.ed-information').click()

  await row('01', '1080p').getByRole('checkbox').check(); await row('02', '1080p').getByRole('checkbox').check()
  uncertainSTRM = true
  await dialog.getByRole('button', { name: 'STRM 所选', exact: true }).click()
  await row('01', '1080p').getByRole('button', { name: '待核对', exact: true }).waitFor()
  const originalPacket = ed2kRequests.at(-1), beforeSubset = ed2kRequests.length
  await row('02', '1080p').getByRole('checkbox').uncheck(); await row('24').getByRole('checkbox').check()
  await dialog.getByRole('button', { name: 'STRM 所选', exact: true }).click()
  await dialog.getByText('上一批 STRM 接收情况尚未确认，请重试原批次或先查看 STRM 任务。', { exact: false }).waitFor()
  assert.equal(ed2kRequests.length, beforeSubset, '回执丢失后不允许按新子集创建请求')
  uncertainSTRM = false
  await dialog.getByRole('button', { name: /重试原 STRM 批次/ }).click()
  await row('02', '1080p').getByRole('button', { name: '已接收', exact: true }).waitFor()
  assert.deepEqual(ed2kRequests.at(-1), originalPacket, '只能重放相同请求编号、目标和完整原文件集合')
  await dialog.getByRole('button', { name: '清空选择', exact: true }).click()

  const screenshotDir = path.resolve(__dirname, '../../tests/browser')
  fs.mkdirSync(screenshotDir, { recursive: true })
  // 重新进入默认文件视图，验证用户首次查看结果时的常规信息密度。
  const beforeReopen = resolves.length
  await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' }); await open()
  await row('01', '1080p').waitFor()
  assert.equal(await dialog.locator('.ed-file').count(), 7, '重开自动恢复搜索累积结果与后端缓存清单')
  assert.equal(resolves.length, beforeReopen + 1, '重新打开仍只为同URL发起一次缓存恢复请求')
  assert.equal(await dialog.locator('.ed-filterbar').count(), 0, '规格筛选默认收起')
  assert.equal(await dialog.locator('.ed-output').count(), 0, '输出目录默认收起')
  assert.equal(await dialog.locator('.ed-batch-actions').count(), 0, '未选择文件时不占用批量操作空间')
  assert.equal(await dialog.locator('.ed-notice').count(), 0)
  for (const close of await page.locator('[data-sonner-toast] [data-close-button]').all()) await close.click()
  await dialog.locator('.anime-floating-body').evaluate(element => { element.scrollTop = 0 })
  for (const width of [1440, 390, 320]) for (const dark of [false, true]) {
   await page.setViewportSize({ width, height: width === 1440 ? 1000 : 860 })
   await page.evaluate(value => document.documentElement.classList.toggle('dark', value), dark)
   // 等待主题颜色和滑块过渡稳定，避免把切换过程误当成最终配色。
   await page.waitForTimeout(350)
   await page.evaluate(async () => { const { useToast } = await import('/src/composables/useToast.ts'); useToast().clear() })
   if (width !== 1440) await assertMobileToolbar(`${width}px ${dark ? '深色' : '浅色'}无筛选计数`)
   const layout = await dialog.evaluate(element => {
    const box = element.getBoundingClientRect()
    const children = [...element.querySelectorAll('.anime-floating-body, .ed-file, .ed-workspace, .ed-filterbar, .ed-output, .ed-selection')]
    const body = element.querySelector('.anime-floating-body').getBoundingClientRect(), rowRects = [...element.querySelectorAll('.ed-file')].map(row => row.getBoundingClientRect())
    return { fits: box.left >= 0 && box.right <= innerWidth + 1 && element.scrollWidth <= element.clientWidth + 1 && children.every(child => child.scrollWidth <= child.clientWidth + 1), fullRows: rowRects.filter(row => row.top >= body.top - 1 && row.bottom <= body.bottom + 1).length, maxRowHeight: Math.max(...rowRects.map(row => row.height)), dialog: { left: box.left, right: box.right, width: element.clientWidth, scrollWidth: element.scrollWidth }, overflow: children.filter(child => child.scrollWidth > child.clientWidth + 1).map(child => ({ class: child.className, width: child.clientWidth, scrollWidth: child.scrollWidth, children: [...child.children].map(entry => ({ class: entry.className, width: entry.getBoundingClientRect().width, text: entry.textContent.slice(0, 100) })) })) }
   })
   await page.screenshot({ path: path.join(screenshotDir, `telegram-files-${width}-${dark ? 'dark' : 'light'}.png`), animations: 'disabled' })
   assert.equal(layout.fits, true, `${width}px ${dark ? '深色' : '浅色'}无横向溢出：${JSON.stringify(layout)}`)
   if (width === 1440) {
    assert.ok(layout.dialog.width >= 1050 && layout.dialog.width <= 1150, `桌面弹窗宽约1100px：${layout.dialog.width}`)
    assert.ok(layout.maxRowHeight <= 72, `桌面文件行紧凑：${layout.maxRowHeight}`)
    assert.ok(layout.fullRows >= 7, `桌面首屏至少7个完整文件：${layout.fullRows}`)
   } else assert.ok(layout.fullRows >= 3, `${width}px 首屏至少3个完整文件：${layout.fullRows}`)
   console.log(`密度 ${width}px ${dark ? 'dark' : 'light'}：弹窗${layout.dialog.width}px，最大行高${Math.round(layout.maxRowHeight)}px，首屏完整${layout.fullRows}行`)
  }
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' })
  assert.equal(await trigger.evaluate(element => document.activeElement === element), true)
  ed2kTargets = [ed2kTargets[0]]
  await open()
  await row('13').getByRole('button', { name: 'ED2K STRM', exact: true }).click()
  await row('13').getByRole('button', { name: '已接收', exact: true }).waitFor()
  assert.equal(ed2kRequests.at(-1).targetId, 'ed2k-movie', '单目录自动选中')
  assert.notEqual(ed2kRequests.at(-1).requestId, ed2kRequests[0].requestId)
  await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' }); moreLoaded = false; await open()
  expired = true
  await dialog.getByRole('button', { name: '加载更多资源' }).click()
  await dialog.getByRole('button', { name: '重新搜索', exact: true }).waitFor()
  assert.equal(await dialog.getByRole('button', { name: '加载更多资源' }).count(), 0)
  expired = false
  await dialog.getByRole('button', { name: '重新搜索', exact: true }).click()
  await dialog.getByRole('button', { name: '加载更多资源' }).waitFor()
  await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' })

  // 三个不同网址即使共用片名和版本标题，也必须分别读取，实际并发不超过两路。
  queueMode = true; moreLoaded = false; holdResolves = true; maximumResolves = 0
  const queueStart = resolves.length
  await open()
  await waitUntil(() => resolves.length === queueStart + 2, '自动队列应先启动两个清单请求')
  await sourceProgress.filter({ hasText: /清单已载入 0 \/ 3 页，读取中 2 页/ }).waitFor()
  await dialog.getByRole('button', { name: '暂停后续读取', exact: true }).click()
  releaseHeldResolves()
  await row('31').waitFor(); await row('32').waitFor()
  await sourceProgress.filter({ hasText: /清单已载入 2 \/ 3 页，已暂停/ }).waitFor()
  assert.equal(resolves.length, queueStart + 2, '暂停只允许在途请求收尾，第三页继续等待')
  await dialog.getByRole('button', { name: '继续读取清单', exact: true }).click()
  await waitUntil(() => resolves.length === queueStart + 3, '继续后应读取余下的不同网址')
  holdResolves = false; releaseHeldResolves()
  await row('33').waitFor()
  await sourceProgress.filter({ hasText: /^清单已载入 3 \/ 3 页$/ }).waitFor()
  assert.equal(await dialog.locator('.ed-file').count(), 7)
  assert.equal(maximumResolves, 2, '清单读取并发上限为两路')
  assert.equal(new Set(resolves.slice(queueStart).map(body => body.sourceId)).size, 3, '不同URL不能按相同片名或版本标题合并')

  await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' })
  holdResolves = true
  const closeStart = resolves.length, closeAborted = abortedResolves.length
  await open()
  await waitUntil(() => resolves.length === closeStart + 2, '关闭取消场景应有两路在途读取')
  await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' })
  await waitUntil(() => abortedResolves.length === closeAborted + 2, '关闭弹窗必须取消两路在途清单请求')
  holdResolves = false; releaseHeldResolves()
  assert.equal(resolves.length, closeStart + 2, '关闭后不能继续调度第三页')
  await open(); await row('33').waitFor()
  assert.equal(await dialog.locator('.ed-file').count(), 7, '关闭取消后重开仍能自动恢复所有页面文件')

  holdResolves = true
  const refreshStart = resolves.length
  await dialog.getByRole('button', { name: '刷新搜索', exact: true }).click()
  await waitUntil(() => resolves.length === refreshStart + 2, '刷新取消场景应有两路在途读取')
  holdSearch = true
  const refreshAborted = abortedResolves.length
  await dialog.getByRole('button', { name: '刷新搜索', exact: true }).click()
  await dialog.getByText('正在搜索频道资源…', { exact: true }).waitFor()
  await waitUntil(() => abortedResolves.length === refreshAborted + 2, '刷新开始就应取消旧清单，不能等新频道搜索完成')
  releaseHeldResolves(); holdResolves = false; releaseSearch(); holdSearch = false
  await row('33').waitFor()
  await sourceProgress.filter({ hasText: /^清单已载入 3 \/ 3 页$/ }).waitFor()
  await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' })

  // 真实点击原生复选框，验证超过旧上限的大列表及异步追加时的最终选择状态。
  queueMode = false; selectionMode = true; moreLoaded = false; holdResolves = true
  const selectionStart = resolves.length, selectionWrites = writes.length, selectionImports = ed2kRequests.length
  const headerSelection = dialog.getByRole('checkbox', { name: '选择当前筛选文件', exact: true })
  const assertSelection = async (expected, reason) => {
   await dialog.getByText(`显示 ${expected.visible} / ${expected.total} 个文件`, { exact: true }).waitFor()
   if (expected.selected) await dialog.locator('.ed-selected-count').filter({ hasText: new RegExp(`^已选 ${expected.selected}(?:（|$)`) }).waitFor()
   else await dialog.locator('.ed-selected-count').waitFor({ state: 'hidden' })
   const actual = await dialog.evaluate(element => {
    const header = element.querySelector('input[aria-label="选择当前筛选文件"]')
    const rows = [...element.querySelectorAll('.ed-file input[type="checkbox"]')]
    const selectedText = element.querySelector('.ed-selected-count')?.textContent || ''
    const displayed = element.querySelector('.ed-selection label')?.textContent.match(/显示 (\d+) \/ (\d+) 个文件/)
    return { checked: header.checked, indeterminate: header.indeterminate, visible: rows.length, total: Number(displayed?.[2]), selectedVisible: rows.filter(input => input.checked).length, selected: Number(selectedText.match(/^已选 (\d+)/)?.[1] || 0), hidden: Number(selectedText.match(/（(\d+) 项在其他筛选中）/)?.[1] || 0) }
   })
   assert.deepEqual(actual, expected, reason)
  }
  const allSelection = { checked: true, indeterminate: false, visible: 545, total: 545, selectedVisible: 545, selected: 545, hidden: 0 }
  await open()
  await waitUntil(() => resolves.length === selectionStart + 1, '大样本的自动清单读取应保持在途，稍后追加文件')
  await assertSelection({ ...allSelection, checked: false, selectedVisible: 0, selected: 0 }, '545个文件初始均未选择')
  await headerSelection.click()
  await assertSelection(allSelection, '一次真实点击完整选中545个文件，原生复选框应勾选且不是混合状态')
  await dialog.locator('.ed-file').first().getByRole('checkbox').click()
  await assertSelection({ ...allSelection, checked: false, indeterminate: true, selectedVisible: 544, selected: 544 }, '取消一行后表头原生复选框进入混合状态')
  await headerSelection.click()
  await assertSelection(allSelection, '混合状态点击表头应补全当前筛选，而不是取消全部')
  await headerSelection.click()
  await assertSelection({ ...allSelection, checked: false, selectedVisible: 0, selected: 0 }, '再次点击已全选的表头应清空当前筛选')

  await query.fill('2160p')
  await headerSelection.click()
  await assertSelection({ checked: true, indeterminate: false, visible: 500, total: 545, selectedVisible: 500, selected: 500, hidden: 0 }, '筛选后全选只选择当前500个文件')
  await query.fill('1080p')
  await assertSelection({ checked: false, indeterminate: false, visible: 45, total: 545, selectedVisible: 0, selected: 500, hidden: 500 }, '切换筛选保留隐藏选择，但隐藏项不能让当前筛选呈现全选或混合状态')
  await headerSelection.click()
  await assertSelection({ checked: true, indeterminate: false, visible: 45, total: 545, selectedVisible: 45, selected: 545, hidden: 500 }, '全选新筛选时应保留隐藏的500个选择')
  await headerSelection.click()
  await assertSelection({ checked: false, indeterminate: false, visible: 45, total: 545, selectedVisible: 0, selected: 500, hidden: 500 }, '清空当前筛选不能清空其他筛选的隐藏选择')
  await query.fill('')
  await assertSelection({ ...allSelection, checked: false, indeterminate: true, selectedVisible: 500, selected: 500 }, '恢复全部文件后显示500/545的混合状态')
  await headerSelection.click()
  await assertSelection(allSelection, '全选追加剩余45个文件后应恢复545个完整选择')

  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await dialog.getByRole('button', { name: '复制所选', exact: true }).click()
  await page.getByText('已复制 545 条 ED2K 链接', { exact: true }).waitFor()
  assert.deepEqual((await page.evaluate(() => navigator.clipboard.readText())).split(/\r?\n/).sort(), selectionLinks.map(link => link.url).sort(), '复制所选必须包含545条完整链接，不能截断为旧上限')
  assert.equal(writes.length, selectionWrites); assert.equal(ed2kRequests.length, selectionImports, '选择及复制不触发任何写入请求')

  await dialog.getByRole('button', { name: 'STRM 所选', exact: true }).click()
  await dialog.locator('.ed-notice').filter({ hasText: '已接收 545 个文件的 STRM 生成任务。' }).waitFor()
  const bulkImport = ed2kRequests.slice(selectionImports)
  assert.equal(bulkImport.length, 1, '545个文件通过单次STRM请求完整提交')
  assert.deepEqual([...bulkImport[0].links].sort(), selectionLinks.map(link => link.url).sort())
  assert.equal(await dialog.locator('.ed-file').getByRole('button', { name: '已接收', exact: true }).count(), 545, '整包STRM成功后每个文件显示已接收')
  await dialog.getByRole('button', { name: '云下载所选', exact: true }).click()
  await dialog.locator('.ed-notice').filter({ hasText: '已提交 545 / 545 个文件' }).waitFor()
  const bulkCloud = writes.slice(selectionWrites).filter(write => write.path.endsWith('/cloud-download/tasks/batch'))
  assert.equal(bulkCloud.length, 1, '545个文件通过单次云下载请求完整提交')
  assert.deepEqual([...bulkCloud[0].body.urls].sort(), selectionLinks.map(link => link.url).sort())
  assert.equal(await dialog.locator('.ed-file').getByRole('button', { name: '已提交', exact: true }).count(), 545, '整包云下载成功后每个文件显示已提交')

  holdResolves = false; releaseHeldResolves()
  await assertSelection({ ...allSelection, checked: false, indeterminate: true, visible: 547, total: 547 }, '自动追加文件后保留545个已选项并变为混合状态，新增项不能自动选中')
  for (const link of appendedSelectionLinks) assert.equal(await row(String(link.metadata.episodeNumber), link.metadata.resolution).getByRole('checkbox').isChecked(), false, '清单新增文件应保持未选')
  await headerSelection.click()
  await assertSelection({ checked: true, indeterminate: false, visible: 547, total: 547, selectedVisible: 547, selected: 547, hidden: 0 }, '点击混合状态表头应把两条自动追加文件一起选中')
  await dialog.getByRole('button', { name: '复制所选', exact: true }).click()
  await page.getByText('已复制 547 条 ED2K 链接', { exact: true }).waitFor()
  assert.deepEqual((await page.evaluate(() => navigator.clipboard.readText())).split(/\r?\n/).sort(), [...selectionLinks, ...appendedSelectionLinks].map(link => link.url).sort(), '追加后复制全部547个文件')
  await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' })

  // 用独立样本验证关键词和五级规格联动，避免改变既有大列表及密度用例。
  selectionMode = false; filterMode = true; moreLoaded = false; holdResolves = true
  const filterStart = resolves.length
  const filterLabels = { season: '筛选季', resolution: '筛选清晰度', dynamicRange: '筛选动态范围', platform: '筛选平台', releaseGroup: '筛选发布组' }
  const filterAllLabels = { season: '全部季', resolution: '全部清晰度', dynamicRange: '全部动态范围', platform: '全部平台', releaseGroup: '全部发布组' }
  const filterControl = field => dialog.getByRole('combobox', { name: filterLabels[field], exact: true })
  const filterRow = link => dialog.locator('.ed-file').filter({ has: page.getByTitle(link.file.name, { exact: true }) })
  const assertFilterOptions = async (field, entries, reason) => {
   const normalize = options => options.map(option => ({ value: option.value, label: option.label.replace(/\s/g, '') })).sort((left, right) => left.value.localeCompare(right.value))
   const expected = [{ value: 'all', label: filterAllLabels[field] }, ...entries.map(([value, count]) => ({ value, label: `${field === 'season' ? `第 ${value} 季` : value}（${count}）` }))]
   assert.deepEqual(normalize(await selectOptions(filterControl(field))), normalize(expected), reason)
  }
  const assertFilterValues = async expected => {
   for (const [field, value] of Object.entries(expected)) {
    assert.equal(await inputValue(filterControl(field)), value, `${filterLabels[field]}应保留或恢复为${value}`)
    assert.doesNotMatch(await filterControl(field).innerText(), /[（(]\d+[）)]/, '触发按钮保持短标签，数量只展示在展开选项中')
   }
  }
  const clearFilterValues = Object.fromEntries(Object.keys(filterLabels).map(field => [field, 'all']))
  const assertVisibleFiles = async (visible, total = 8) => {
   await dialog.getByText(`显示 ${visible} / ${total} 个文件`, { exact: true }).waitFor()
   assert.equal(await dialog.locator('.ed-file').count(), visible)
  }
  await open()
  await waitUntil(() => resolves.length === filterStart + 1, '联动场景先保留一条在途清单，稍后异步追加规格')
  await page.waitForFunction(() => !document.querySelector('dialog[open] input[aria-label="选择当前筛选文件"]').disabled)
  const requestsBeforeFilters = apiRequests.length
  await dialog.getByRole('button', { name: '规格筛选', exact: true }).click()
  assert.deepEqual(await dialog.locator('.ed-filterbar [role="combobox"]').evaluateAll(elements => elements.map(element => element.getAttribute('aria-label'))), Object.values(filterLabels), '规格从左到右按季、清晰度、动态范围、平台、发布组排列')
  await assertVisibleFiles(8)
  await assertFilterValues(clearFilterValues)
  await assertFilterOptions('season', [['1', 5], ['2', 2], ['3', 1]], '季选项统计关键词匹配的所有文件')
  await assertFilterOptions('resolution', [['1080p', 3], ['2160p', 4]], '无清晰度的文件不生成空选项')
  await assertFilterOptions('dynamicRange', [['HDR', 3], ['SDR', 4]], '动态范围选项显示真实数量')
  await assertFilterOptions('platform', [['AMZN', 2], ['NF', 5]], '平台选项显示真实数量')
  await assertFilterOptions('releaseGroup', [['ALPHA', 4], ['BETA', 1], ['GAMMA', 2]], '发布组选项显示真实数量')

  await selectOption(filterControl('season'), '1')
  await assertFilterOptions('resolution', [['1080p', 2], ['2160p', 3]], '选择季后清晰度数量只统计该季')
  await selectOption(filterControl('resolution'), '2160p')
  await assertFilterOptions('dynamicRange', [['HDR', 2], ['SDR', 1]], '选择清晰度后动态范围只保留上游有文件的值')
  const filterScreenshotDark = await page.evaluate(() => document.documentElement.classList.contains('dark'))
  await page.evaluate(() => document.documentElement.classList.remove('dark'))
  await page.evaluate(async () => { const { useToast } = await import('/src/composables/useToast.ts'); useToast().clear() })
  for (const width of [1440, 390]) {
   await page.setViewportSize({ width, height: width === 1440 ? 1000 : 860 })
   await filterControl('dynamicRange').click()
   const panel = page.locator(`[id=${JSON.stringify(await filterControl('dynamicRange').getAttribute('aria-controls'))}]`)
   await panel.waitFor()
   await page.waitForTimeout(350)
   const fits = await panel.evaluate(element => { const rect = element.getBoundingClientRect(); return rect.left >= 0 && rect.right <= innerWidth + 1 && element.scrollWidth <= element.clientWidth + 1 })
   assert.equal(fits, true, `${width}px联动下拉菜单不超出视口`)
   await page.screenshot({ path: path.join(screenshotDir, `telegram-filters-${width}-light.png`), animations: 'disabled' })
   await filterControl('dynamicRange').press('Escape')
  }
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), filterScreenshotDark)
  await selectOption(filterControl('dynamicRange'), 'HDR')
  await assertFilterOptions('platform', [['NF', 2]], 'HDR下不显示零文件的AMZN组合')
  await selectOption(filterControl('platform'), 'NF')
  await assertFilterOptions('releaseGroup', [['ALPHA', 2]], '发布组根据全部上游条件排除零文件组合')
  await selectOption(filterControl('releaseGroup'), 'ALPHA')
  await assertVisibleFiles(2)
  for (const width of [390, 320]) for (const dark of [false, true]) {
   await page.setViewportSize({ width, height: 860 })
   await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
   await assertMobileToolbar(`${width}px ${dark ? '深色' : '浅色'}存在5个已选规格计数`)
  }
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), filterScreenshotDark)
  await assertFilterOptions('resolution', [['1080p', 2], ['2160p', 3]], '下游HDR、平台和发布组不能反向缩小上游清晰度选项')
  await assertFilterOptions('platform', [['NF', 2]], '唯一有效且已选的平台仍保留全部平台取消入口')
  await assertFilterOptions('releaseGroup', [['ALPHA', 2]], '唯一有效且已选的发布组仍保留全部发布组取消入口')
  await selectOption(filterControl('season'), '2')
  await assertFilterValues({ season: '2', resolution: '2160p', dynamicRange: 'HDR', platform: 'NF', releaseGroup: 'ALPHA' })
  await assertVisibleFiles(1)
  await selectOption(filterControl('resolution'), '1080p')
  await assertFilterValues({ season: '2', resolution: '1080p', dynamicRange: 'all', platform: 'all', releaseGroup: 'all' })
  await assertVisibleFiles(1)
  await assertFilterOptions('platform', [['AMZN', 1]], '上游变化后不显示失效的平台，同时不自动选中唯一平台')
  await assertFilterOptions('releaseGroup', [['GAMMA', 1]], '无效的多级下游条件逐级恢复all')

  await selectOption(filterControl('season'), '1')
  await selectOption(filterControl('dynamicRange'), 'SDR')
  await selectOption(filterControl('platform'), 'NF')
  await selectOption(filterControl('releaseGroup'), 'ALPHA')
  await selectOption(filterControl('platform'), 'AMZN')
  await assertFilterValues({ season: '1', resolution: '1080p', dynamicRange: 'SDR', platform: 'AMZN', releaseGroup: 'all' })
  await assertFilterOptions('releaseGroup', [['GAMMA', 1]], '切换平台只清除不兼容的发布组')
  await selectOption(filterControl('platform'), 'NF')
  await selectOption(filterControl('releaseGroup'), 'ALPHA')
  await selectOption(filterControl('resolution'), '2160p')
  await assertFilterValues({ season: '1', resolution: '2160p', dynamicRange: 'SDR', platform: 'NF', releaseGroup: 'all' })
  await assertFilterOptions('releaseGroup', [['BETA', 1]], '切换清晰度时保留兼容的SDR和NF，只清除不兼容的ALPHA')
  await assertVisibleFiles(1)
  await selectOption(filterControl('releaseGroup'), 'BETA')
  await selectOption(filterControl('dynamicRange'), 'HDR')
  await assertFilterValues({ season: '1', resolution: '2160p', dynamicRange: 'HDR', platform: 'NF', releaseGroup: 'all' })
  await assertFilterOptions('releaseGroup', [['ALPHA', 2]], '切换动态范围后保留兼容平台并清除失效发布组')
  await assertVisibleFiles(2)
  await dialog.getByRole('button', { name: '重置', exact: true }).click()
  await query.fill('River')
  await assertVisibleFiles(4)
  await assertFilterValues(clearFilterValues)
  await assertFilterOptions('season', [['1', 3], ['2', 1]], '关键词先约束季选项和数量')
  await assertFilterOptions('resolution', [['2160p', 4]], '关键词只剩一种清晰度时仍保持all，不自动选中')
  await assertFilterOptions('dynamicRange', [['HDR', 3], ['SDR', 1]], '关键词先约束动态范围计数')
  await assertFilterOptions('platform', [['NF', 4]], '关键词不显示无匹配文件的平台')
  await assertFilterOptions('releaseGroup', [['ALPHA', 3], ['BETA', 1]], '关键词不显示零文件发布组')

  await query.fill('Bare')
  await assertVisibleFiles(1)
  for (const field of ['resolution', 'dynamicRange', 'platform', 'releaseGroup']) assert.equal(await filterControl(field).count(), 0, '没有有效规格值时隐藏整个下拉项')
  await assertFilterOptions('season', [['3', 1]], '只有一个有效季仍可显示其数量')
  await selectOption(filterControl('season'), '3')
  await assertFilterValues({ season: '3' })
  await assertFilterOptions('season', [['3', 1]], '唯一且已选的季仍保留全部季取消入口')
  await selectOption(filterControl('season'), 'all')
  await filterRow(filterLinks[7]).getByRole('checkbox').click()
  await query.fill('nothing-matches-this-fixture')
  await assertSelection({ checked: false, indeterminate: false, visible: 0, total: 8, selectedVisible: 0, selected: 1, hidden: 1 }, '关键词无结果时仍保留隐藏文件的选择')
  for (const field of Object.keys(filterLabels)) assert.equal(await filterControl(field).count(), 0, '关键词无结果时不保留没有有效值的规格下拉')
  assert.equal(await headerSelection.isDisabled(), true)
  await query.fill('River')
  await assertSelection({ checked: false, indeterminate: false, visible: 4, total: 8, selectedVisible: 0, selected: 1, hidden: 1 }, '切换关键词不会清除隐藏的文件选择或自动勾选新结果')
  await assertFilterValues(clearFilterValues)
  await query.fill('')
  await assertSelection({ checked: false, indeterminate: true, visible: 8, total: 8, selectedVisible: 1, selected: 1, hidden: 0 }, '清空关键词后恢复原文件的勾选')
  for (const [field, value] of Object.entries({ season: '1', resolution: '2160p', dynamicRange: 'HDR', platform: 'NF', releaseGroup: 'ALPHA' })) await selectOption(filterControl(field), value)
  await assertSelection({ checked: false, indeterminate: false, visible: 2, total: 8, selectedVisible: 0, selected: 1, hidden: 1 }, '规格筛选不把隐藏选择计入当前全选')
  await headerSelection.click()
  await assertSelection({ checked: true, indeterminate: false, visible: 2, total: 8, selectedVisible: 2, selected: 3, hidden: 1 }, '全选当前规格时保留其他规格中已选的文件')
  assert.equal(apiRequests.length, requestsBeforeFilters, '关键词、联动选项、规格切换及勾选只在本地处理，不发起API请求')

  holdResolves = false; releaseHeldResolves()
  await assertSelection({ checked: false, indeterminate: true, visible: 3, total: 10, selectedVisible: 2, selected: 3, hidden: 1 }, '异步追加后保留隐藏和可见勾选，新匹配文件不自动选中')
  await assertFilterValues({ season: '1', resolution: '2160p', dynamicRange: 'HDR', platform: 'NF', releaseGroup: 'ALPHA' })
  await assertFilterOptions('season', [['1', 6], ['2', 2], ['3', 1], ['4', 1]], '异步追加只更新上游选项和数量，不自动切换到新季')
  await assertFilterOptions('resolution', [['1080p', 2], ['2160p', 4]], '新季的4320p不混入当前第1季的清晰度选项')
  await assertFilterOptions('dynamicRange', [['HDR', 3], ['SDR', 1]], '追加后动态范围计数按当前上游更新')
  await assertFilterOptions('releaseGroup', [['ALPHA', 3]], '追加后发布组计数按全部上游更新')
  assert.equal(await filterRow(appendedFilterLinks[0]).getByRole('checkbox').isChecked(), false)
  assert.equal(await filterRow(appendedFilterLinks[1]).count(), 0, '新增不兼容规格的文件保持在当前筛选之外')
  await dialog.getByRole('button', { name: '重置', exact: true }).click()
  await assertFilterValues(clearFilterValues)
  await assertSelection({ checked: false, indeterminate: true, visible: 10, total: 10, selectedVisible: 3, selected: 3, hidden: 0 }, '重置规格只恢复可见文件，保留原勾选')
  for (const link of appendedFilterLinks) assert.equal(await filterRow(link).getByRole('checkbox').isChecked(), false, '所有新加载文件保持未选中')
  for (const link of [filterLinks[0], filterLinks[1], filterLinks[7]]) assert.equal(await filterRow(link).getByRole('checkbox').isChecked(), true, '重置规格后原有可见及隐藏勾选仍然保留')
  assert.equal(apiRequests.length, requestsBeforeFilters, '异步追加后的筛选恢复和重置不触发额外API请求')
  await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' })

  filterMode = false; moreLoaded = true
  await open(); await row('01', '1080p').waitFor()
  assert.equal(await dialog.locator('.ed-file').count(), 7, '关闭大样本并重开后恢复小样本，不残留大列表或选择')
  assert.equal(await dialog.locator('.ed-selected-count').count(), 0)
  await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' }); moreLoaded = false
  holdSearch = true
  await trigger.click(); await dialog.getByText('正在搜索频道资源…', { exact: true }).waitFor()
  await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' })
  releaseSearch(); holdSearch = false
  assert.deepEqual(errors, [])
  assert.ok(searches.every(search => search.tmdbId === '42' && search.kind === 'movie'))
  console.log('TG 文件列表 UI 通过：自动清单恢复、2路队列/暂停继续、失败手动重试、跨帖分页去重来源、关闭/刷新取消、五级规格联动/计数/关键词/隐藏维度/异步追加、545文件全选/混合/隐藏选择/整包STRM和云下载、追加547文件复制、单文件/批量/504原包重放、分页失败/过期、分享STRM及320/390/1440深浅主题。')
 } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
