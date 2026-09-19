// 仅使用模拟接口验证下架提示及正常资源展示，不访问真实观影或提交转存任务。
const path = require('node:path')
const os = require('node:os')
const assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext()
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage()
    const errors = []
    page.on('pageerror', err => errors.push(err.message))
    let mode = 'mixed'
    await page.route('**/api/**', async route => {
      const url = new URL(route.request().url())
      let data = {}
      if (url.pathname === '/api/auth/me') data = { user: { id: 'mock', role: 'admin', username: 'mock', disabled: false } }
      else if (url.pathname.includes('/tmdb/detail/')) data = { id: 3570, kind: 'tv', title: '美少女战士', originalTitle: '美少女戦士セーラームーン', imdbId: 'tt0103369', genres: [{ id: 16, name: '动画' }], countries: ['JP'], cast: [], seasons: [], overview: '', posterUrl: '', backdropUrl: '' }
      else if (url.pathname.endsWith('/gying/config')) data = { configured: true, hasSession: true, resourceTypes: [], resourceOrder: ['magnet'] }
      else if (url.pathname.endsWith('/gying/search')) data = {
        query: '美少女戦士セーラームーン', matchedBy: 'title', total: 2, cached: false, unavailableDetails: 1,
        partial: mode === 'partial', failedDetails: mode === 'partial' ? 1 : 0,
        attempts: [{ method: 'title', query: '美少女戦士セーラームーン', total: 2, usable: mode === 'mixed' ? 1 : 0, unavailable: 1, failed: mode === 'partial' ? 1 : 0 }],
        items: mode === 'mixed' ? [{ id: 'ac:one', title: '正常资源条目', mediaType: 'anime', links: [{ type: 'magnet', typeLabel: '磁力', category: 'torrent', url: 'magnet:?xt=urn:btih:' + 'a'.repeat(40) }] }] : [],
      }
      else if (url.pathname.includes('/recommendations')) data = { items: [], page: 1, totalPages: 1 }
      else if (url.pathname.includes('/emby/media/')) data = { media: { state: 'unknown', items: [] } }
      else if (url.pathname.includes('/follows/')) data = { subscribed: false, libraryState: 'unknown' }
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(data) })
    })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/tv/3570')
    await page.getByRole('button', { name: '观影搜索', exact: true }).click()
    await page.getByRole('heading', { name: '正常资源条目', exact: true }).waitFor()
    await page.getByText(/已跳过 1 个下架或不可用条目/).waitFor()
    assert.equal(await page.getByText(/会话失效|重新登录失败/).count(), 0)
    mode = 'all'
    await page.getByRole('button', { name: '重新搜索', exact: true }).click()
    await page.getByText('匹配资源已下架或不可用，暂未取得可用的网盘或磁力资源。', { exact: true }).waitFor()
    mode = 'partial'
    await page.getByRole('button', { name: '重新搜索', exact: true }).click()
    await page.getByText('部分资源详情读取失败，尚不能确认是否有可用资源，请稍后重新搜索。', { exact: true }).waitFor()
    assert.equal(await page.getByText('空结果缓存 2 分钟，也可以立即重新搜索。', { exact: true }).count(), 0)
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed: true, cases: ['mixed', 'all', 'partial'], pageErrors: errors }))
  } finally { await browser.close() }
})().catch(err => { console.error(err); process.exitCode = 1 })
