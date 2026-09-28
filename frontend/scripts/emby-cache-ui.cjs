// Exercise pagination and local-record removal with mocked APIs only.
const path = require('node:path')
const os = require('node:os')
const assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1100 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage()
    const errors = [], writes = []
    page.on('pageerror', e => errors.push(e.message))
    let entries = Array.from({ length: 17 }, (_, i) => ({ key: `copy-${i}`, accountId: i % 2 ? 'a' : 'b', uid: '123', directoryId: '10', status: i === 0 ? 'uncertain' : 'ready', message: '网盘文件由用户管理', lastUsed: new Date(Date.UTC(2026, 8, 29, 0, 0, 17 - i)).toISOString(), file: { name: `Movie-${String(i).padStart(2, '0')}.2160p.WEB-DL.DDP5.1.HDR.HEVC.mkv`, sha1: String(i).padStart(40, 'A'), size: 5 * 1024 ** 3, id: `file-${i}` } }))
    let materials = entries.map((e, i) => ({ id: `material-${i}`, name: e.file.name, sha1: e.file.sha1, size: e.file.size, verifiedAt: e.lastUsed, ranges: [{ start: 0, end: 100, sha1: 'B'.repeat(40) }] }))
    const links = entries.map((_, i) => ({ id: `link-${i}`, user: `user-${i}`, mediaSource: `media-${i}`, account: '测试账号', mode: '秒传播放', cdn: 'cdn.example.test', expiresAt: '2099-01-01T00:00:00Z', hits: 1 }))
    await page.route('**/api/**', async route => {
      const request = route.request(), url = new URL(request.url())
      let data = {}
      if (url.pathname === '/api/auth/me') data = { user: { id: 'mock', role: 'admin', username: 'mock', disabled: false } }
      else if (url.pathname.endsWith('/p115/accounts')) data = { accounts: [{ id: 'a', name: '测试账号 A' }, { id: 'b', name: '测试账号 B' }] }
      else if (url.pathname.endsWith('/emby/proxy')) data = { config: { directLinkCacheMinutes: 9 } }
      else if (url.pathname.endsWith('/proxy/direct-links')) data = { entries: links, hits: 3, misses: 17 }
      else if (url.pathname.includes('/playback-cache')) {
        if (request.method() === 'POST') {
          writes.push({ path: url.pathname, body: request.postDataJSON() })
          if (url.pathname.endsWith('/forget')) entries = entries.filter(e => e.key !== request.postDataJSON().key)
          if (url.pathname.endsWith('/forget-material')) materials = materials.filter(m => m.id !== request.postDataJSON().id)
        }
        data = { entries, materials }
      }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
    })
    await page.goto('http://localhost:5173/emby/cache')
    await page.getByRole('button', { name: '网盘文件复用', exact: true }).click()
    const panel = page.getByRole('region', { name: '网盘文件复用记录' })
    await panel.getByText('共 17 条 · 每页 8 条').waitFor()
    assert.equal(await panel.locator('article').count(), 8)
    assert.equal(await page.getByRole('heading', { name: '直链缓存管理' }).isVisible(), false)
    await panel.getByRole('button', { name: '下一页', exact: true }).click()
    await panel.getByRole('button', { name: '下一页', exact: true }).click()
    assert.equal(await panel.locator('article').count(), 1)
    await panel.getByRole('button', { name: /查看文件详情/ }).click()
    await panel.getByRole('button', { name: '移除本地记录', exact: true }).click()
    assert.equal(writes.length, 0, 'removal must require confirmation')
    await panel.getByRole('button', { name: '确认仅移除记录' }).click()
    await panel.getByText('共 16 条 · 每页 8 条').waitFor()
    assert.equal(await panel.locator('article').count(), 8, 'deleting last page must clamp page')
    assert.deepEqual(writes[0].body, { key: 'copy-16' })
    await panel.getByRole('searchbox').fill('Movie-00')
    await panel.getByText('共 1 条 · 每页 8 条').waitFor()
    await panel.getByRole('searchbox').fill('')
    await panel.getByLabel('筛选记录状态').selectOption('attention')
    await panel.getByText('共 1 条 · 每页 8 条').waitFor()
    await panel.getByRole('button', { name: /文件秒传资料/ }).click()
    await panel.getByText('共 17 条 · 每页 8 条').waitFor()
    assert.equal(await panel.locator('article').count(), 8, 'copy-only filters must not affect materials')
    await panel.getByRole('button', { name: /查看资料详情/ }).first().click()
    await panel.getByText('查看校验片段（1）').click()
    await panel.getByRole('button', { name: '移除文件资料', exact: true }).click()
    await panel.getByRole('button', { name: '确认移除资料' }).click()
    await panel.getByText('共 16 条 · 每页 8 条').waitFor()
    assert.deepEqual(writes[1].body, { id: 'material-0' })
    await panel.getByRole('button', { name: /账号文件副本/ }).click()
    await panel.getByLabel('筛选记录状态').selectOption('all')
    for (const dark of [false, true]) {
      await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
      await page.setViewportSize({ width: 390, height: 844 })
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'mobile horizontal overflow')
    }
    await page.setViewportSize({ width: 1440, height: 1100 })
    await page.screenshot({ path: path.join(os.tmpdir(), 'cinema115-cache-dark.png'), fullPage: true })
    await page.getByRole('button', { name: '直链缓存', exact: true }).click()
    await page.getByText('共 17 条 · 每页 8 条').waitFor()
    await page.getByRole('button', { name: '下一页', exact: true }).click()
    await page.getByText('2 / 3', { exact: true }).waitFor()
    assert.deepEqual(errors, [])
    console.log('PASS: categories, pagination, search, status filter, details, confirmed removals, last-page clamp, mobile light/dark, direct-link pagination')
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
