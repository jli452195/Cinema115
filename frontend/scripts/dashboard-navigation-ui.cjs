// Isolated synthetic browser fixture. Every API request is mocked; no service writes.
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const errors = [], mutations = [], requests = [], passed = []
  let browseDelay = 0
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block', reducedMotion: 'reduce' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock-dashboard-navigation'))
    await context.addInitScript(() => {
      const timeout = AbortSignal.timeout.bind(AbortSignal), fetch = window.fetch.bind(window)
      window.__dashboardNetworkFixture = { mode: 'normal', timeoutMs: 25000, started: 0, aborted: 0 }
      AbortSignal.timeout = milliseconds => timeout(milliseconds === 25000 ? window.__dashboardNetworkFixture.timeoutMs : milliseconds)
      window.fetch = (input, init) => {
        const fixture = window.__dashboardNetworkFixture
        const address = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
        if (!new URL(address, location.href).pathname.endsWith('/tmdb/browse') || fixture.mode === 'normal') return fetch(input, init)
        const signal = init?.signal
        fixture.started++
        // 模拟请求无响应，以及收到响应头后正文一直未结束，两者均需总超时收尾。
        if (fixture.mode === 'body') {
          const body = new ReadableStream({
            start(controller) {
              const abort = () => { fixture.aborted++; controller.error(signal.reason) }
              if (signal.aborted) abort()
              else signal.addEventListener('abort', abort, { once: true })
            },
          })
          return Promise.resolve(new Response(body, { headers: { 'Content-Type': 'application/json' } }))
        }
        return new Promise((_resolve, reject) => {
          const abort = () => { fixture.aborted++; reject(signal.reason) }
          if (signal.aborted) abort()
          else signal.addEventListener('abort', abort, { once: true })
        })
      }
    })
    await context.route('**/api/**', async route => {
      const request = route.request(), url = new URL(request.url()), endpoint = url.pathname
      requests.push({ endpoint, search: url.search, method: request.method() })
      if (!['GET', 'HEAD'].includes(request.method()) && !endpoint.endsWith('/emby/library-check')) mutations.push(endpoint)
      let data = { configured: false, connected: false, items: [], total: 0, totalPages: 1, hasMore: false, accounts: [], jobs: [], sessions: [], genres: [], unread: 0 }
      if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'dashboard-navigation-fixture', role: 'admin' } }
      else if (endpoint.endsWith('/tmdb/genres')) data = { genres: [{ id: 16, name: '动画' }, { id: 99, name: '纪录' }, { id: 18, name: '剧情' }] }
      else if (endpoint.endsWith('/tmdb/browse')) {
        if (browseDelay) await new Promise(resolve => setTimeout(resolve, browseDelay))
        const kind = url.searchParams.get('kind') === 'movie' ? 'movie' : 'tv', page = Number(url.searchParams.get('page') || 1)
        data = { items: Array.from({ length: 42 }, (_, index) => ({ id: (kind === 'movie' ? 200000 : 100000) + page * 100 + index + 1, kind, title: `${kind === 'movie' ? '电影' : '剧集'}第${page}页测试${index + 1}`, originalTitle: 'Navigation fixture', overview: '', posterUrl: '', date: '2024-01-01', rating: 7.5, votes: 100 })), total: 1000, totalPages: 25 }
      } else if (/\/tmdb\/detail\/(movie|tv)\/\d+$/.test(endpoint)) {
        const parts = endpoint.split('/'), kind = parts.at(-2), id = Number(parts.at(-1))
        data = { id, kind, title: '浏览恢复测试详情', originalTitle: 'Navigation fixture detail', overview: '', posterUrl: '', backdropUrl: '', date: '2024-01-01', rating: 7.5, votes: 100, tagline: '', status: 'Released', runtime: 90, episodeCount: 0, genres: [], countries: [], cast: [], seasons: [], collection: null, imdbId: '', imdbUrl: '', tmdbUrl: '' }
      } else if (endpoint.includes('/emby/media/')) data = { media: { state: 'absent', items: [] }, baseUrl: '', serverId: '' }
      else if (endpoint.endsWith('/telegram/user/channels')) data = { account: { hasSession: false }, channels: [] }
      else if (endpoint === '/api/admin/media-organize/history') data = { items: [], page: 1, pages: 1, total: 0, counts: { all: 0, running: 0, done: 0, attention: 0 }, active: false, activity: [], cleanup: [] }
      await route.fulfill({ json: data }).catch(() => {})
    })
    const page = await context.newPage()
    page.setDefaultTimeout(15000)
    page.on('pageerror', error => errors.push(error.message))
    const loaded = async () => {
      try {
        await page.locator('.catalog-media-grid > a').first().waitFor()
        await page.waitForFunction(() => document.querySelector('.catalog-results')?.getAttribute('aria-busy') === 'false')
      } catch (reason) {
        throw new Error(`${reason.message}\n${JSON.stringify({ url: page.url(), errors, content: await page.locator('body').innerText() })}`)
      }
    }
    const currentPath = () => { const url = new URL(page.url()); return url.pathname + url.search }
    const push = to => page.evaluate(async to => { await document.querySelector('#app').__vue_app__.config.globalProperties.$router.push(to) }, to)
    const home = () => page.getByRole('link', { name: 'Cinema115 首页', exact: true })
    const topNav = name => page.getByRole('navigation', { name: '影视导航', exact: true }).getByRole('link', { name, exact: true })
    const atScroll = y => page.waitForFunction(y => Math.abs(window.scrollY - y) <= 4, y)
    const scroll = y => page.evaluate(async y => {
      window.scrollTo({ top: y, behavior: 'instant' })
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))
      return window.scrollY
    }, y)
    const openVisibleDetail = async () => {
      const href = await page.locator('.catalog-media-grid > a').evaluateAll(links => links.find(link => {
        const bounds = link.getBoundingClientRect()
        return bounds.top >= 110 && bounds.bottom <= innerHeight - 30
      })?.getAttribute('href'))
      assert.ok(href, 'Fixture must have a fully visible card')
      await page.locator(`.catalog-media-grid > a[href="${href}"]`).click()
      await page.waitForURL(url => /^\/(movie|tv)\/\d+$/.test(url.pathname))
      await page.locator('h1').filter({ hasText: '浏览恢复测试详情' }).waitFor()
    }

    await page.goto(origin + '/')
    await page.waitForURL('**/tv'); await loaded()
    assert.equal(await page.evaluate(() => window.scrollY), 0)
    passed.push('首次首页进入默认剧集列表')

    const moviePath = '/movie?region=JP&year=2024&sort=rating&page=5'
    await push(moviePath); await loaded()
    const movieY = await scroll(900)
    assert.ok(movieY >= 800)
    await home().click()
    assert.equal(currentPath(), moviePath); await atScroll(movieY)
    passed.push('列表内点击 Logo 保持筛选、排序、页码和滚动')

    await openVisibleDetail()
    await home().click(); await loaded()
    assert.equal(currentPath(), moviePath); await atScroll(movieY)
    await openVisibleDetail()
    await page.goBack(); await loaded()
    assert.equal(currentPath(), moviePath); await atScroll(movieY)
    passed.push('详情点击 Logo 与浏览器后退均回到原电影页及原滚动位置')

    const tvPath = '/tv?category=animation&region=CN&page=3'
    await push(tvPath); await loaded()
    const tvY = await scroll(650)
    await topNav('电影').click(); await loaded()
    assert.equal(currentPath(), moviePath); await atScroll(movieY)
    await topNav('剧集').click(); await loaded()
    assert.equal(currentPath(), tvPath); await atScroll(tvY)
    passed.push('电影与剧集分别保留各自筛选、页码和滚动')

    await push('/media/organize/history')
    await page.getByRole('link', { name: '整理记录', exact: true }).waitFor()
    await home().click(); await loaded()
    assert.equal(currentPath(), tvPath); await atScroll(tvY)
    passed.push('从管理页面点击 Logo 恢复最近浏览的剧集列表')

    await page.getByRole('navigation', { name: '剧集分页', exact: true }).getByRole('button', { name: '下一页', exact: true }).click()
    await loaded(); await atScroll(0)
    assert.equal(new URL(page.url()).searchParams.get('page'), '4')
    await scroll(700)
    await page.getByRole('group', { name: '影视地区', exact: true }).getByRole('button', { name: '日本', exact: true }).click()
    await loaded(); await atScroll(0)
    assert.equal(new URL(page.url()).searchParams.get('region'), 'JP')
    assert.equal(new URL(page.url()).searchParams.has('page'), false)
    passed.push('主动翻页回到顶部，修改筛选清页码并回到顶部')

    await topNav('电影').click(); await loaded(); await atScroll(movieY)
    const refreshedY = await scroll(1040)
    browseDelay = 250
    await page.reload(); await loaded(); await atScroll(refreshedY)
    assert.equal(currentPath(), moviePath)
    await page.goto(origin + '/'); await loaded(); await atScroll(refreshedY)
    assert.equal(currentPath(), moviePath)
    browseDelay = 0
    passed.push('刷新及重新访问首页恢复电影第5页，延迟加载后正确恢复位置')

    const explicitPath = '/tv?region=KR&page=2'
    await page.goto(origin + explicitPath); await loaded()
    assert.equal(currentPath(), explicitPath); await atScroll(0)
    await push('/media/organize/history'); await home().click(); await loaded()
    assert.equal(currentPath(), explicitPath)
    passed.push('显式列表地址按传入参数打开，并成为下一次首页的恢复目标')

    const networkFixture = (mode, timeoutMs = 200) => page.evaluate(({ mode, timeoutMs }) => {
      Object.assign(window.__dashboardNetworkFixture, { mode, timeoutMs, started: 0, aborted: 0 })
    }, { mode, timeoutMs })
    const timeoutMessage = page.getByRole('alert').getByText('TMDB 查询超时，请重新加载', { exact: true })
    await page.goto(origin + '/tv?region=JP&year=2023')
    await loaded()
    await networkFixture('pending')
    // 电影页面清空已展示的剧集结果，确保检查的是首次加载的骨架屏退出。
    await push('/movie?region=JP&year=2023')
    await timeoutMessage.waitFor()
    assert.equal(await page.locator('.catalog-results').getAttribute('aria-busy'), 'false')
    assert.equal(await page.getByLabel('正在加载影视', { exact: true }).count(), 0)
    assert.equal(await page.evaluate(() => window.__dashboardNetworkFixture.aborted), 1)
    await networkFixture('normal', 25000)
    await page.getByRole('button', { name: '重新加载', exact: true }).click()
    await loaded()
    assert.equal(await page.getByRole('alert').count(), 0)
    passed.push('无响应请求超时退出骨架屏，显示中文错误且重新加载成功')

    const previousTitle = await page.locator('.catalog-media-grid h2').first().innerText()
    await networkFixture('body')
    await push('/movie?region=JP&year=2022')
    await timeoutMessage.waitFor()
    assert.equal(await page.locator('.catalog-results').getAttribute('aria-busy'), 'false')
    assert.equal(await page.locator('.catalog-media-grid h2').first().innerText(), previousTitle)
    assert.equal(await page.evaluate(() => window.__dashboardNetworkFixture.aborted), 1)
    assert.ok((await page.getByRole('alert').innerText()).includes('当前保留上次结果'))
    passed.push('响应头已返回但正文悬挂也会超时，并保留上次列表')

    await networkFixture('pending', 2000)
    await push('/movie?region=JP&year=2021')
    await page.waitForFunction(() => window.__dashboardNetworkFixture.started === 1)
    await page.evaluate(() => { window.__dashboardNetworkFixture.mode = 'normal' })
    await push('/movie?region=JP&year=2020')
    await loaded()
    assert.equal(await page.evaluate(() => window.__dashboardNetworkFixture.aborted), 1)
    await page.waitForTimeout(2100)
    assert.equal(await page.getByRole('alert').count(), 0)
    assert.equal(await page.locator('.catalog-results').getAttribute('aria-busy'), 'false')
    passed.push('筛选切换取消旧请求，旧请求超时不会污染新结果')
    await networkFixture('normal', 25000)

    await page.setViewportSize({ width: 390, height: 844 })
    const beforeDrawer = currentPath()
    await page.locator('.workbench-header .brand-link').click()
    await page.getByRole('dialog', { name: '功能导航', exact: true }).waitFor()
    assert.equal(currentPath(), beforeDrawer)
    await page.keyboard.press('Escape')
    await page.getByRole('dialog', { name: '功能导航', exact: true }).waitFor({ state: 'detached' })
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    passed.push('手机 Logo 保持打开侧栏，关闭后恢复页面滚动且无横向溢出')

    assert.deepEqual(mutations, [])
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, browseRequests: requests.filter(request => request.endpoint.endsWith('/tmdb/browse')).length, errors, serviceWrites: mutations }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
