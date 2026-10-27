// End-to-end interaction with mocked APIs: never sends Telegram messages or writes to 115.
const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  page.setDefaultTimeout(10000)
  await page.addInitScript(() => { localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light') })
  const errors = [], searches = [], writes = []
  page.on('pageerror', e => errors.push(e.message))
  page.on('console', m => { if (m.text().includes('[Vue warn]')) errors.push(m.text()) })
  const channels = [{ id: '11', title: '115网盘热更频道' }, { id: '12', title: '爱影热更频道' }]
  const ed2k = 'ed2k://|file|Movie.mkv|123|0123456789abcdef0123456789abcdef|/'
  const first = { id: '11:10', title: '测试电影', metadata: { episode: 'S01E13', specs: ['2160p', 'HDR', 'HEVC'], size: '18.1 GB' }, text: '测试电影 2160p\n4K HDR\nhttps://115.com/s/example?password=abcd', channel: channels[0], sourceUrl: 'https://t.me/c/11/10', postedAt: '2026-10-27T01:00:00Z', links: [{ type: '115', url: 'https://115.com/s/example?password=abcd' }] }
  const second = { id: '12:20', title: '测试电影', text: '测试电影 1080p\n' + ed2k, channel: channels[1], sourceUrl: 'https://t.me/c/12/20', postedAt: '2026-10-26T01:00:00Z', links: [{ type: 'ed2k', url: ed2k, metadata: { specs: ['1080p', 'HEVC'], size: '1.2 GiB' } }] }
  let failMore = false, expired = false, holdSearch = false, releaseSearch, uncertainDownload = true, configReady = true
  let run
  const baseResult = () => ({ items: [first], warnings: [{ channel: channels[1], error: '频道读取超时，可加载更多重试' }], hasMore: true, cursor: 'page2', partial: true, cached: false, expiresAt: '2026-10-27T02:00:00Z' })
  await page.route('**/api/**', async route => {
   const req = route.request(), p = new URL(req.url()).pathname
   if (p === '/api/auth/me') return route.fulfill({ json: { user: { id: 'admin', role: 'admin', username: 'admin' } } })
   if (p.endsWith('/telegram-user/channels')) return route.fulfill({ json: { account: { configured: true, hasSession: true, botConfigured: false }, channels: configReady ? channels : [] } })
   if (p.endsWith('/telegram-user/config')) return route.fulfill({ json: { configured: true, hasSession: true, botConfigured: false, bot: '' } })
   if (p.endsWith('/telegram-user/channels/search')) {
    const body = req.postDataJSON(); searches.push(body)
    assert.deepEqual(Object.keys(body).sort(), (body.cursor ? ['cursor', 'kind', 'tmdbId'] : ['kind', 'refresh', 'tmdbId']).sort())
    if (holdSearch) await new Promise(resolve => { releaseSearch = resolve })
    if (body.cursor && expired) return route.fulfill({ status: 409, json: { error: '频道搜索已过期，请重新搜索', code: 'channel_search_expired' } })
    if (body.cursor && failMore) return route.fulfill({ status: 502, json: { error: '网络暂时不可用' } })
    return route.fulfill({ json: body.cursor ? { ...baseResult(), items: [first, second], warnings: [], partial: false, hasMore: false, cursor: '' } : baseResult() })
   }
   if (p === '/api/admin/p115/cloud-download/config') return route.fulfill({ json: { accountId: 'main', targetCID: '123', targetPath: '/Downloads' } })
   if (p === '/api/admin/p115/cloud-download/tasks') {
    writes.push({ path: p, body: req.postDataJSON() })
    if (uncertainDownload) return route.fulfill({ status: 504, body: '', contentType: 'text/plain' })
    return route.fulfill({ json: { accepted: true } })
   }
   if (p === '/api/admin/p115/share/automation-config') return route.fulfill({ json: { settings: { accountId: 'main', generation: { baseUrl: 'http://nas', outputDir: '/strm', videoExtensions: '.mkv', recursive: true } } } })
   if (p === '/api/admin/p115/share/generation-runs' && req.method() === 'POST') {
    const body = req.postDataJSON(); writes.push({ path: p, body })
    run = { id: body.id, status: 'success', items: [{ taskId: 'task', status: 'success', message: '', result: { count: 1, directory: '/strm/Test' } }] }
    return route.fulfill({ json: run })
   }
   if (p.startsWith('/api/admin/p115/share/generation-runs/')) return route.fulfill({ json: run })
   if (p.includes('/tmdb/detail/')) return route.fulfill({ json: { id: 42, kind: 'movie', title: '测试电影', originalTitle: 'Test Movie', overview: '测试影片', date: '2026-01-01', genres: [], countries: [], cast: [], seasons: [], rating: 0, votes: 0, runtime: 100, episodeCount: 0, status: 'Released', tmdbUrl: '', imdbUrl: '', posterUrl: '', backdropUrl: '' } })
   if (p.includes('/emby/media/')) return route.fulfill({ json: { media: { state: 'absent', items: [] } } })
   if (p.includes('/recommendations/')) return route.fulfill({ json: { items: [], page: 1, totalPages: 1 } })
   if (p.includes('/follows/')) return route.fulfill({ json: { subscribed: false, request: null, libraryState: 'absent' } })
   if (p.includes('/notifications')) return route.fulfill({ json: { items: [], unread: 0 } })
   return route.fulfill({ json: { configured: false, accounts: [] } })
  })
  await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/movie/42')
  const trigger = page.getByRole('button', { name: 'TG 频道搜索', exact: true })
  try { await trigger.waitFor() } catch (error) { console.log(await page.locator('body').innerText()); console.log('Page errors:', errors); throw error }
  assert.equal(searches.length, 0, 'Detail load must not start Telegram search')
  const dialog = page.locator('dialog.tg-channel-search[open]')
  const open = async () => { await trigger.click(); await dialog.locator('.tg-resource').waitFor() }
  await open()
  assert.equal(writes.length, 0)
  await dialog.getByText('部分频道暂不可用（1）', { exact: true }).click()
  await dialog.getByText('爱影热更频道：频道读取超时', { exact: false }).waitFor()
  const shareLink = dialog.getByRole('link', { name: '打开分享', exact: true })
  assert.equal(await shareLink.getAttribute('href'), first.links[0].url)
  await dialog.getByText('S01E13', { exact: true }).waitFor()
  await dialog.getByText('2160p', { exact: true }).waitFor()
  await dialog.getByText('消息内容', { exact: true }).click()
  await dialog.getByText('测试电影 2160p\n4K HDR', { exact: false }).waitFor()
  failMore = true
  await dialog.getByRole('button', { name: '加载更多资源' }).click()
  await dialog.getByRole('alert').filter({ hasText: '网络暂时不可用' }).waitFor()
  assert.equal(await dialog.locator('.tg-resource').count(), 1)
  failMore = false
  await dialog.getByRole('button', { name: '重试', exact: true }).click()
  await dialog.locator('.tg-resource').filter({ hasText: '1080p' }).waitFor()
  assert.equal(await dialog.locator('.tg-resource').count(), 2)
  assert.equal(await dialog.locator('.tg-warnings').count(), 0)
  const ed = dialog.locator('.tg-resource').filter({ hasText: '1080p' })
  await ed.getByRole('button', { name: '115 云下载', exact: true }).click()
  await ed.getByRole('button', { name: '待核对', exact: true }).waitFor()
  assert.equal(await ed.getByRole('button', { name: '待核对', exact: true }).isDisabled(), true)
  assert.equal(writes[0].body.url, ed2k)
  await dialog.getByRole('button', { name: '分享 STRM', exact: true }).click()
  await dialog.getByRole('button', { name: '已生成', exact: true }).waitFor()
  assert.equal(writes[1].body.inputs[0].url, first.links[0].url.replace('115.com', '115cdn.com'))
  await dialog.getByRole('searchbox', { name: '筛选频道资源' }).fill('1080p')
  assert.equal(await dialog.locator('.tg-resource').count(), 1)
  await dialog.getByRole('searchbox').fill('')
  for (const width of [390, 320]) {
   await page.setViewportSize({ width, height: 780 })
   await page.evaluate(() => document.documentElement.classList.add('dark'))
   assert.equal(await dialog.evaluate(el => { const box = el.getBoundingClientRect(); return box.left >= 0 && box.right <= innerWidth + 1 && el.scrollWidth <= el.clientWidth }), true)
  }
  await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' })
  assert.equal(await trigger.evaluate(el => document.activeElement === el), true)
  await open()
  expired = true
  await dialog.getByRole('button', { name: '加载更多资源' }).click()
  await dialog.getByRole('button', { name: '重新搜索', exact: true }).waitFor()
  assert.equal(await dialog.getByRole('button', { name: '加载更多资源' }).count(), 0)
  expired = false
  await dialog.getByRole('button', { name: '重新搜索', exact: true }).click()
  await dialog.getByRole('button', { name: '加载更多资源' }).waitFor()
  await page.keyboard.press('Escape')
  holdSearch = true
  await trigger.click()
  await dialog.getByText('正在搜索频道资源…', { exact: true }).waitFor()
  await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' })
  releaseSearch(); holdSearch = false
  assert.deepEqual(errors, [])
  assert.ok(searches.every(s => s.tmdbId === '42' && s.kind === 'movie'))
  console.log('TG channel search UI passed: detail entry, partial results, pagination retry/expiry, share STRM, uncertain ED2K, mobile/dark and close cancellation.')
 } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
