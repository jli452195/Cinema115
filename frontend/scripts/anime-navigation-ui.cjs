// All APIs are mocked in isolated browsers. No external reads or 115 tasks.
const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const target = '/anime?source=anibt&season=2026-10&day=1'
const waitUntil = async predicate => {
  const until = Date.now() + 3000
  while (!predicate()) { if (Date.now() > until) throw new Error('Mock request was not observed'); await new Promise(resolve => setTimeout(resolve, 10)) }
}

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const passed = [], errors = []
  async function session(mode = 'normal') {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => {
      localStorage.setItem('admin_token', 'navigation-review-token')
      const now = Date.now, timeout = AbortSignal.timeout.bind(AbortSignal)
      window.__navigationOffset = 0
      Date.now = () => now() + window.__navigationOffset
      AbortSignal.timeout = ms => timeout(window.__shortAuthTimeout && ms === 10000 ? 80 : ms)
    })
    const page = await context.newPage(); page.setDefaultTimeout(5000)
    page.on('pageerror', error => errors.push(error.message))
    const modules = {}
    // Vite may append an HMR timestamp. Import the same module instance as App.
    page.on('request', request => {
      const url = new URL(request.url())
      if (url.pathname === '/src/composables/usePortalAuth.ts') modules.auth = request.url()
      if (url.pathname === '/src/composables/useAnimeCatalogStatus.ts') modules.status = request.url()
    })
    const calls = [], state = { mode, role: 'admin', releaseAuth: undefined }
    await page.route('**/api/**', async route => {
      const request = route.request(), endpoint = new URL(request.url()).pathname, body = request.postDataJSON()
      calls.push({ endpoint, body })
      let status = 200, data = {}
      if (endpoint === '/api/auth/me') {
        if (state.mode === 'network') { await route.abort('failed'); return }
        if (state.mode === 'hold') await new Promise(resolve => { state.releaseAuth = resolve })
        if (state.mode === 'server') { status = 503; data = { error: '暂时不可用' } }
        else if (state.mode === 'unauthorized') { status = 401; data = { error: '登录失效' } }
        else if (state.mode === 'forbidden') { status = 403; data = { error: '权限不足' } }
        else data = { user: { id: 'navigation-review', username: 'review', role: state.role, disabled: false } }
      } else if (endpoint.endsWith('/catalog/anibt/seasons')) data = { provider: 'anibt', seasons: ['2026-10', '2026-07', '2026-04'], fetchedAt: Date.now(), expiresAt: Date.now() + 3600000 }
      else if (endpoint.endsWith('/catalog/anibt')) {
        const month = Number(body.season.slice(5))
        const days = Array.from({ length: 7 }, (_, i) => ({ day: i + 1, items: Array.from({ length: 2 }, (_, j) => {
          const id = String(month * 10000 + (i + 1) * 100 + j)
          return { id, bangumiId: id, title: body.season + ' 周' + (i + 1) + ' 番剧' + j, originalTitle: '', coverUrl: '', format: 'TV', episodes: 12, releaseCount: 4, rating: 7, airingAt: null }
        }) }))
        data = { provider: 'anibt', season: body.season, days, items: [], fetchedAt: Date.now(), expiresAt: Date.now() + 3600000 }
      } else if (endpoint.endsWith('/catalog-status')) data = { items: body.itemIds.map(itemId => ({ itemId, bangumiId: itemId, subscriptions: [] })) }
      else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
      else if (endpoint.includes('/tmdb/')) data = { items: [], results: [], genres: [], page: 1, total: 0, totalPages: 1 }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
    })
    await page.goto(origin + target, { waitUntil: 'networkidle' })
    assert.ok(modules.auth && modules.status, 'application modules must be captured before testing their state')
    await page.evaluate(modules => { window.__navigationModules = modules }, modules)
    const navigate = url => page.evaluate(url => document.querySelector('#app').__vue_app__.config.globalProperties.$router.push(url), url)
    const authenticate = () => page.evaluate(async () => {
      const auth = await import(window.__navigationModules.auth)
      await auth.ensurePortalSession(true, { background: true })
      return { error: auth.portalAuthError.value, role: auth.portalUser.value?.role, tokenPresent: !!auth.portalToken.value }
    })
    const count = endpoint => calls.filter(call => call.endpoint.endsWith(endpoint)).length
    return { context, page, state, calls, navigate, authenticate, count }
  }
  try {
    const s = await session()
    await s.page.locator('.anibt-anime-card').first().waitFor()
    s.state.mode = 'hold'
    await s.page.evaluate(async () => {
      window.__navigationOffset += 31000
      const auth = await import(window.__navigationModules.auth)
      window.__heldAuth = auth.ensurePortalSession(true, { background: true })
    })
    await waitUntil(() => s.state.releaseAuth)
    const authenticationCount = s.count('/auth/me')
    await s.page.getByRole('button', { name: /^周二/ }).click()
    await s.page.waitForFunction(() => new URL(location.href).searchParams.get('day') === '2', undefined, { timeout: 1000 })
    await s.page.getByRole('button', { name: '上一季', exact: true }).click()
    await s.page.getByRole('heading', { name: '2026 夏季收录14 部' }).waitFor()
    const catalogCount = s.count('/catalog/anibt')
    await s.page.getByRole('button', { name: '下一季', exact: true }).click()
    await s.page.getByRole('heading', { name: '2026 秋季收录14 部' }).waitFor()
    assert.equal(s.count('/auth/me'), authenticationCount, 'filters must not wait on or add authentication requests')
    assert.equal(s.count('/catalog/anibt'), catalogCount, 'returning to a cached season must stay local')
    await s.page.evaluate(() => {
      window.__outsideNavigation = document.querySelector('#app').__vue_app__.config.globalProperties.$router.push('/users')
    })
    s.state.mode = 'server'; s.state.releaseAuth()
    await s.page.evaluate(async () => { await window.__heldAuth; await window.__outsideNavigation })
    assert.equal(new URL(s.page.url()).pathname, '/anime', 'a failed background verification must not authorize a different page')
    s.state.mode = 'normal'; await s.authenticate()
    passed.push('星期和季度在慢鉴权期间立即切换，缓存复用；跨页面仍等待校验且失败不跳转')
    await s.context.close()

    const rapid = await session()
    const before = rapid.count('/catalog-status'), catalogsBefore = rapid.count('/catalog/anibt')
    await rapid.page.evaluate(async () => {
      const router = document.querySelector('#app').__vue_app__.config.globalProperties.$router
      for (const day of ['3', '4', '5']) await router.push({ path: '/anime', query: { source: 'anibt', season: '2026-10', day } })
    })
    await waitUntil(() => rapid.count('/catalog-status') > before)
    assert.equal(rapid.count('/catalog-status'), before + 1)
    assert.deepEqual(rapid.calls.filter(call => call.endpoint.endsWith('/catalog-status')).at(-1).body.itemIds, ['100500', '100501'])
    await rapid.page.waitForFunction(async () => {
      const statuses = await import(window.__navigationModules.status)
      return !!statuses.animeCatalogStatusSnapshot('anibt', ['100500'])['100500']
    })
    await rapid.page.getByRole('heading', { name: '周五2 部' }).waitFor()
    await rapid.navigate(target)
    await rapid.page.waitForTimeout(200)
    assert.equal(rapid.count('/catalog-status'), before + 1, 'fresh status cache must be reused immediately')
    assert.equal(rapid.count('/catalog/anibt'), catalogsBefore, 'weekdays must never refetch the catalog')
    await rapid.navigate('/anime?source=anibt&season=2026-10&day=6')
    await rapid.navigate('/users')
    await rapid.page.waitForTimeout(200)
    assert.equal(rapid.count('/catalog-status'), before + 1, 'leaving the catalog must clear its delayed status request')
    passed.push('连续切星期只核对最终卡片，缓存复用且离开页面取消排队请求')
    await rapid.context.close()

    const quiet = await session()
    await quiet.page.locator('.anibt-anime-card').first().evaluate(el => { window.__retainedCard = el })
    for (const mode of ['network', 'server', 'hold']) {
      quiet.state.mode = mode
      if (mode === 'hold') await quiet.page.evaluate(() => { window.__shortAuthTimeout = true })
      const result = await quiet.authenticate()
      assert.equal(result.error, '')
      assert.equal(result.role, 'admin')
      assert.equal(await quiet.page.evaluate(() => window.__retainedCard.isConnected), true, 'temporary background failure must keep the same page and DOM')
      if (mode === 'hold') { quiet.state.mode = 'normal'; quiet.state.releaseAuth(); await quiet.page.evaluate(() => { window.__shortAuthTimeout = false }) }
    }
    quiet.state.mode = 'normal'; await quiet.authenticate()
    quiet.state.mode = 'unauthorized'; await quiet.authenticate()
    await quiet.page.waitForURL('**/admin/login?**')
    assert.equal(await quiet.page.evaluate(() => !!localStorage.getItem('admin_token')), false)
    assert.equal(await quiet.page.locator('.anibt-anime-card').count(), 0)
    passed.push('后台断网、503和超时保留页面；恢复后可校验，401仍退出登录')
    await quiet.context.close()

    const denied = await session()
    denied.state.mode = 'forbidden'
    assert.notEqual((await denied.authenticate()).error, '')
    assert.equal(await denied.page.locator('.anibt-anime-card').count(), 0)
    await denied.context.close()
    const downgraded = await session()
    downgraded.state.role = 'user'; await downgraded.authenticate()
    await downgraded.page.locator('.anibt-anime-card').first().waitFor({ state: 'detached' })
    const reads = downgraded.count('/catalog/anibt')
    await downgraded.navigate('/anime?source=anibt&season=2026-07&day=1')
    assert.equal(downgraded.count('/catalog/anibt'), reads)
    assert.equal(new URL(downgraded.page.url()).pathname, '/')
    await downgraded.context.close()
    const initial = await session('server')
    assert.equal(await initial.page.locator('.anibt-anime-card').count(), 0)
    assert.equal(initial.count('/catalog/anibt'), 0)
    await initial.page.getByText('登录状态验证失败，请重试', { exact: true }).waitFor()
    passed.push('403、管理员降权及首次未验证失败仍拦截管理页面')
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
