// 模拟搜索及图片接口，不连接真实观影或提交 115 任务。
const path = require('node:path')
const os = require('node:os')
const assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 } })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage()
    const errors = [], external = [], images = []
    let searches = 0
    page.on('pageerror', e => errors.push(e.message))
    await page.route('https://s.tutu.pm/**', route => { external.push(route.request().url()); return route.abort() })
    const items = [
      { id: 'ac:P8KR', title: '第一季', year: 2016, coverUrl: '/api/gying/image/ac/P8KR.webp' },
      { id: 'ac:jdb6', title: '第三季', year: 2024, coverUrl: '/api/gying/image/ac/jdb6.webp' },
      { id: 'mv:meJB', title: '没有封面的剧场版', year: 2019 },
      { id: 'ac:Mgnk', title: '拒绝外站图片', year: 2026, coverUrl: 'https://s.tutu.pm/img/ac/Mgnk/256.webp' },
    ].map((item, i) => ({ ...item, mediaType: 'anime', links: [{ type: 'magnet', typeLabel: '磁力', category: 'torrent', subTypeLabel: '1080P', url: 'magnet:?xt=urn:btih:' + String(i + 1).repeat(40) }] }))
    await page.route('**/api/**', async route => {
      const url = new URL(route.request().url())
      if (url.pathname.startsWith('/api/gying/image/')) {
        images.push(url.pathname)
        return route.fulfill(url.pathname.includes('jdb6') ? { status: 502, body: '' } : { status: 200, contentType: 'image/png', body: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=', 'base64') })
      }
      let data = {}
      if (url.pathname === '/api/auth/me') data = { user: { id: 'mock', role: 'admin', username: 'mock', disabled: false } }
      else if (url.pathname.includes('/tmdb/detail/')) data = { id: 65942, kind: 'tv', title: 'Re：从零开始的异世界生活', originalTitle: 'Re:ゼロから始める異世界生活', imdbId: '', genres: [{ id: 16, name: '动画' }], countries: ['JP'], cast: [], seasons: [], overview: '', posterUrl: '', backdropUrl: '' }
      else if (url.pathname.endsWith('/gying/config')) data = { configured: true, hasSession: true, resourceTypes: [], resourceOrder: ['magnet'] }
      else if (url.pathname.endsWith('/gying/search')) { searches++; data = { query: 'Re', matchedBy: 'title', total: items.length, cached: false, attempts: [{ method: 'title', query: 'Re', total: items.length, usable: items.length }], items } }
      else if (url.pathname.includes('/recommendations')) data = { items: [], page: 1, totalPages: 1 }
      else if (url.pathname.includes('/emby/media/')) data = { media: { state: 'unknown', items: [] } }
      else if (url.pathname.includes('/follows/')) data = { subscribed: false, libraryState: 'unknown' }
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(data) })
    })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/tv/65942')
    await page.getByRole('button', { name: '观影搜索', exact: true }).click()
    const dialog = page.getByRole('dialog')
    const nav = dialog.getByRole('navigation', { name: '影视条目', exact: true })
    await nav.getByText('封面暂不可用', { exact: true }).waitFor()
    assert.equal(await nav.getByText('暂无封面', { exact: true }).count(), 2)
    assert.equal(await dialog.locator('article').count(), 4)
    await page.waitForFunction(() => document.querySelector('img[src="/api/gying/image/ac/P8KR.webp"]')?.naturalWidth > 0)
    await nav.getByRole('button', { name: '第一季 (2016)', exact: true }).click()
    assert.equal(await nav.getByRole('button', { name: '第一季 (2016)', exact: true }).getAttribute('aria-pressed'), 'true')
    assert.equal(await dialog.locator('article').count(), 1)
    assert.match(await dialog.locator('article').innerText(), /第一季/)
    assert.equal(await dialog.getByRole('button', { name: '115 转存', exact: true }).count(), 1)
    await dialog.getByRole('navigation', { name: '资源网盘' }).getByRole('button', { name: /磁力/ }).click()
    assert.equal(searches, 1)
    await dialog.getByRole('button', { name: /全部条目/ }).click()
    assert.equal(await dialog.locator('article').count(), 4)
    assert.equal(searches, 1)
    await page.setViewportSize({ width: 390, height: 844 })
    assert.equal(await nav.evaluate(el => el.scrollWidth > el.clientWidth), true)
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    await nav.getByRole('button', { name: '拒绝外站图片 (2026)', exact: true }).click()
    assert.equal(await dialog.locator('article').count(), 1)
    assert.deepEqual(external, [])
    assert.deepEqual(errors, [])
    assert.equal(images.length, 2)
    console.log(JSON.stringify({ passed: true, searches, images, externalRequests: external.length, cases: ['poster', 'failed', 'missing', 'remote-rejected', 'local-filter', 'mobile'] }))
  } finally { await browser.close() }
})().catch(err => { console.error(err); process.exitCode = 1 })
