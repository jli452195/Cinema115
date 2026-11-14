// 全部接口使用模拟数据，不处理真实追更申请。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 950 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock-pending'))
    let pending = 3, status = 200, role = 'admin', countRequests = 0, listRequests = 0
    const errors = [], writes = []
    await context.route('**/api/**', async route => {
      const req = route.request(), url = new URL(req.url()), p = url.pathname
      if (req.method() !== 'GET') writes.push(p)
      let data = { items: [], hasMore: false, counts: {}, genres: [], total: 0, totalPages: 1 }
      if (p === '/api/auth/me') data = { user: { id: 'fixture', username: 'fixture', role } }
      if (p === '/api/admin/follows') {
        if (url.searchParams.get('countsOnly') === '1') {
          countRequests++
          assert.equal(url.searchParams.size, 1)
          return route.fulfill({ status, json: status === 200 ? { counts: { pending } } : { error: '模拟读取失败' } })
        }
        listRequests++
        data = { items: [], hasMore: false, counts: { pending: 0 } }
      }
      if (p === '/api/admin/follows/settings') data = { baseUrl: '', autoEnabled: false, telegramEnabled: false, botId: '', notificationsConfigured: false }
      await route.fulfill({ json: data })
    })
    const page = await context.newPage()
    page.on('pageerror', e => errors.push(e.message))
    const nav = page.locator('#management-desktop-navigation')
    const link = nav.locator('[data-section="/follows/manage"] a')
    const badge = link.locator('[data-follow-pending-badge]')
    const refresh = async () => {
      await page.evaluate(async () => {
        // 开发服务器可能带 HMR 时间参数，使用 App 实际加载的模块实例。
        const loaded = performance.getEntriesByType('resource').find(entry => new URL(entry.name).pathname === '/src/composables/useFollowPendingCount.ts')
        await (await import(loaded.name)).refreshFollowPendingCount(true)
      })
      if (pending === 0) await badge.waitFor({ state: 'detached' })
      else await badge.getByText(pending > 99 ? '99+' : String(pending), { exact: true }).waitFor()
    }
    await page.goto(origin + '/follows/manage')
    await badge.getByText('3', { exact: true }).waitFor()
    assert.equal(countRequests, 1, '首次挂载只有一条数量查询')
    assert.match(await link.getAttribute('aria-label'), /3.*待处理/)
    assert.equal(await link.getAttribute('href'), '/follows/manage?status=pending')
    await link.click()
    await page.waitForURL('**/follows/manage?status=pending')
    assert.equal(await badge.innerText(), '3', '查看页面不清零')
    pending = 128; await refresh()
    assert.equal(await badge.innerText(), '99+')
    assert.match(await link.getAttribute('aria-label'), /128/)
    assert.equal(listRequests >= 1, true)
    status = 503; await refresh()
    assert.equal(await badge.innerText(), '99+', '网络失败保留上次数值')
    status = 200

    await page.evaluate(async () => { await document.querySelector('#app').__vue_app__.config.globalProperties.$router.push('/movie') })
    await page.waitForFunction(() => document.querySelector('#management-desktop-navigation')?.classList.contains('is-collapsed') && !document.querySelector('#management-desktop-navigation')?.classList.contains('is-transitioning'))
    for (const dark of [false, true]) {
      await page.evaluate(value => document.documentElement.classList.toggle('dark', value), dark)
      await link.hover()
      await page.waitForTimeout(400)
      const bounds = await badge.evaluate(el => {
        const b = el.getBoundingClientRect(), n = el.closest('nav').getBoundingClientRect(), row = el.closest('a').getBoundingClientRect(), svg = el.closest('a').querySelector('svg').getBoundingClientRect()
        return { b: { x: b.x, right: b.right, width: b.width }, n: { x: n.x, right: n.right }, rowHeight: row.height, svgWidth: svg.width, opacity: getComputedStyle(el).opacity }
      })
      assert.equal(bounds.opacity, '1')
      assert(bounds.b.x >= bounds.n.x && bounds.b.right + 2 <= bounds.n.right, JSON.stringify(bounds))
      assert(bounds.rowHeight > 60 && bounds.svgWidth > 24)
      await page.getByRole('tooltip').getByText(/128/).waitFor()
      await fs.mkdir(path.join(__dirname, '../test-results'), { recursive: true })
      await page.screenshot({ path: path.join(__dirname, `../test-results/follow-pending-${dark ? 'dark' : 'light'}.png`) })
    }
    await page.mouse.move(800, 700)
    pending = 0; await refresh()
    assert.equal(await badge.count(), 0)
    pending = 12; await refresh()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.getByRole('button', { name: '打开侧栏', exact: true }).click()
    const drawer = page.getByRole('dialog', { name: '功能导航', exact: true })
    await drawer.locator('[data-follow-pending-badge]').getByText('12', { exact: true }).waitFor()
    const before = countRequests
    await page.getByRole('button', { name: '关闭功能导航', exact: true }).click()
    await page.getByRole('button', { name: '打开侧栏', exact: true }).click()
    assert.equal(countRequests, before, '打开抽屉不新增轮询')
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)

    role = 'user'
    await page.goto(origin + '/movie')
    const currentRequests = countRequests
    await page.waitForTimeout(300)
    assert.equal(countRequests, currentRequests)
    assert.equal(await page.locator('[data-follow-pending-badge]').count(), 0)
    assert.deepEqual(errors, [])
    assert.deepEqual(writes, [])
    console.log(JSON.stringify({ passed: ['全局数量独立于筛选，查看不清零，失败保留', '99+完整数字辅助说明，深浅主题Dock放大不裁切', '零值隐藏，手机抽屉复用状态与轮询，普通用户无数量请求'], countRequests, errors }, null, 2))
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
