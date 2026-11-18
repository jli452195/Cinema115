// 从同一影视详情页逐个打开八个来源；拦截全部接口，不连接真实账号或网盘。
const assert = require('node:assert/strict')
const os = require('node:os'), path = require('node:path')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const sources = {
  re0: '.re0-dialog', bot: '.bot-dialog', channels: '.tg-channel-search',
  woniu: '.gy-dialog', gying: '.gy-dialog', dmhy: '.mikan-dialog', mikan: '.mikan-dialog', garden: '.mikan-dialog',
}
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const page = await browser.newPage({ viewport: { width: 390, height: 780 }, hasTouch: true, serviceWorkers: 'block' })
    page.setDefaultTimeout(7000)
    const errors = []
    page.on('pageerror', error => errors.push(error.message))
    page.on('console', message => { if (message.text().includes('[Vue warn]')) errors.push(message.text()) })
    await page.addInitScript(() => {
      localStorage.setItem('admin_token', 'mock')
      localStorage.setItem('cinema115-theme', 'light')
    })
    await page.route('**/api/**', async route => {
      const endpoint = new URL(route.request().url()).pathname
      if (endpoint.endsWith('/search-stream')) {
        await route.fulfill({ contentType: 'text/event-stream', body: 'event: result\ndata: {"complete":true,"result":{"text":"","items":[],"links":[],"empty":true}}\n\n' })
        return
      }
      let data = { query: '百变之星', items: [], resources: [], channels: [], groups: [], bangumis: [], attempts: [], warnings: [], total: 0, page: 1, hasMore: false }
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock', role: 'admin', username: 'mock', disabled: false } }
      else if (endpoint.includes('/tmdb/detail/')) data = { id: 12501, kind: 'tv', title: '百变之星', imdbId: '', date: '2003-04-03', releaseDate: '', backdropUrl: '', posterUrl: '', originalTitle: 'Kaleido Star', genres: [{ id: 16, name: '动画' }], countries: [], cast: [], seasons: [], overview: '' }
      else if (endpoint.endsWith('/resources/garden/search')) data = { ...data, items: [
        { provider: 'dmhy', providerId: '1', title: '[测试字幕组] 百变之星 01 [1080p]', size: 300000000, createdAt: '2026-11-01T00:00:00Z', magnet: '', publisher: { id: 1, name: '测试组' } },
      ] }
      else if (endpoint.endsWith('/resources/search-settings')) data = { order: Object.keys(sources) }
      else if (endpoint.endsWith('/telegram-user/channels')) data = { account: { hasSession: true }, channels: [{ id: '11', title: '测试频道' }] }
      else if (endpoint.endsWith('/config')) data = { configured: true, hasSession: true, botConfigured: true, bot: 'fixture_bot' }
      await route.fulfill({ json: data })
    })
    await page.goto(origin + '/tv/12501')
    const geometry = dialog => dialog.evaluate(el => {
      const rect = el.getBoundingClientRect(), style = getComputedStyle(el)
      return { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom, radius: style.borderTopLeftRadius, border: style.borderTopWidth, scroll: el.scrollWidth, width: el.clientWidth }
    })
    for (const mobile of [true, false]) {
      const width = mobile ? 390 : 1200, height = 780
      await page.setViewportSize({ width, height })
      for (const [source, dialogClass] of Object.entries(sources)) {
        const trigger = page.locator(`.detail-search-buttons button[data-search-source="${source}"]`)
        await trigger.click({ timeout: 8000 })
        const dialog = page.locator(`dialog${dialogClass}[open]`)
        await dialog.waitFor()
        await dialog.evaluate(el => Promise.allSettled(el.getAnimations().map(animation => animation.finished)))
        const handle = dialog.locator('.bottom-sheet-grabber-zone')
        const expand = dialog.getByRole('button', { name: '展开弹窗', exact: true })
        const box = await geometry(dialog)
        assert.equal(await handle.isVisible(), mobile, source)
        assert.equal(await expand.isVisible(), mobile, source)
        if (mobile) {
          assert.ok(Math.abs(box.left) <= 1 && Math.abs(box.right - width) <= 1 && Math.abs(box.bottom - height) <= 1 && box.top >= 23 && box.radius !== '0px' && box.border === '0px' && box.scroll <= box.width + 1, `${source}: ${JSON.stringify(box)}`)
          await expand.click()
          await dialog.evaluate(el => Promise.allSettled(el.getAnimations().map(animation => animation.finished)))
          assert.ok(Math.abs((await geometry(dialog)).top) <= 1, `展开 ${source}`)
          await dialog.getByRole('button', { name: '恢复弹窗高度', exact: true }).click()
          await dialog.evaluate(el => Promise.allSettled(el.getAnimations().map(animation => animation.finished)))
          if (source === 'garden') {
            await dialog.getByText('[测试字幕组] 百变之星 01 [1080p]', { exact: true }).waitFor()
            if (process.env.SHEET_SCREENSHOT) await page.screenshot({ path: process.env.SHEET_SCREENSHOT })
          }
          await page.touchscreen.tap(width / 2, 8)
        } else {
          assert.ok(box.left > 0 && box.right < width && box.top > 0 && box.bottom < height, `${source}: ${JSON.stringify(box)}`)
          await page.keyboard.press('Escape')
        }
        await dialog.waitFor({ state: 'detached' })
        assert.equal(await trigger.evaluate(el => document.activeElement === el), true, `焦点 ${source}`)
      }
    }
    assert.deepEqual(errors, [])
    console.log(`手机与桌面共验证 ${Object.keys(sources).length} 个搜索抽屉：${Object.keys(sources).join('、')}`)
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
