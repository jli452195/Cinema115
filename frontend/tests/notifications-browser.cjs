// API 全部使用隔离数据，不改动真实用户的通知或追更。
const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1100, height: 680 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'notification-test'))
    const notifications = Array.from({ length: 25 }, (_, index) => ({ id: 25 - index, kind: 'tv', tmdbId: 94664, title: `通知 ${25 - index}`, message: '已追平，继续追踪后续更新', createdAt: 1791100000, readAt: 0 }))
    const requests = [], errors = []
    let failNext = true
    const unread = () => notifications.filter(item => !item.readAt).length
    await context.route('**/api/**', async route => {
      const req = route.request(), url = new URL(req.url())
      let data = {}, status = 200
      if (url.pathname === '/api/auth/me') data = { user: { id: 'test', username: 'test', role: 'user', disabled: false } }
      else if (url.pathname === '/api/me/notifications') {
        const before = Number(url.searchParams.get('before'))
        const page = notifications.filter(item => !before || item.id < before)
        data = { items: page.slice(0, 20), hasMore: page.length > 20, unread: unread() }
      } else if (url.pathname === '/api/me/notifications/unread') data = { unread: unread() }
      else if (url.pathname === '/api/me/notifications/read') {
        const body = req.postDataJSON()
        requests.push(body)
        if (failNext) { failNext = false; status = 503; data = { error: '测试临时失败' } }
        else {
          for (const item of notifications) if (body.ids ? body.ids.includes(item.id) : body.all ? item.id <= body.id : item.id === body.id) item.readAt ||= 1791101000
          data = { ok: true, unread: unread() }
        }
      }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const page = await context.newPage()
    page.on('pageerror', error => errors.push(error.message))
    const base = process.env.TEST_BASE_URL || 'http://192.168.0.24:5173'
    await page.goto(`${base}/notifications`)
    await page.locator('article').first().waitFor()
    await page.waitForTimeout(600)
    assert.equal(requests.length, 0, '不足一秒不能自动已读')
    await page.getByRole('alert').filter({ hasText: '测试临时失败' }).waitFor()
    assert.equal(unread(), 25, '失败不能本地伪装已读')
    assert.equal(await page.locator('article').first().getByLabel('未读').count(), 1)
    await page.waitForFunction(() => document.querySelector('article')?.textContent.includes('已读') && !document.querySelector('article')?.querySelector('[aria-label="未读"]'), null, { timeout: 10000 })
    assert(requests.length >= 2, '失败没有重试')
    assert(requests[0].ids.length > 1, '同屏通知应该合并提交')
    assert(requests[0].ids.length < 20, '不应把整页未看过的通知已读')
    assert.equal(notifications.at(-1).readAt, 0, '未加载的通知被标记')
    assert.equal(await page.getByRole('link', { name: `站内通知，${unread()} 条未读`, exact: true }).count(), 1)
    await page.locator('article').last().scrollIntoViewIfNeeded()
    await page.waitForFunction(() => !document.querySelector('article:last-of-type [aria-label="未读"]'))
    await page.getByRole('button', { name: '加载更多', exact: true }).click()
    await page.waitForFunction(() => document.querySelectorAll('article').length === 25)
    await page.locator('article').last().scrollIntoViewIfNeeded()
    await page.waitForFunction(() => !document.querySelector('article:last-of-type [aria-label="未读"]'))
    assert.equal(notifications.at(-1).readAt > 0, true, '分页新卡片没有自动已读')

    // 另一标签页手动全部已读，原标签页通过广播刷新。
    const other = await context.newPage()
    other.on('pageerror', error => errors.push(error.message))
    await other.goto(`${base}/notifications`)
    await other.getByRole('button', { name: '全部已读', exact: true }).click()
    await other.getByRole('link', { name: '站内通知', exact: true }).waitFor()
    await page.getByRole('link', { name: '站内通知', exact: true }).waitFor()
    assert.equal(unread(), 0)
    assert.equal(await page.getByLabel('未读', { exact: true }).count(), 0)
    assert.deepEqual(errors, [])
    console.log('PASS: 可见停留、批量已读、失败重试、分页、角标与跨标签页同步')
    await context.close()
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
