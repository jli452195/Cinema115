// All APIs/posters are intercepted. No real subscription, upstream or 115 mutations.
const assert = require('node:assert/strict')
const fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const wait = async fn => { for (let i = 0; i < 150; i++) { if (await fn()) return; await new Promise(r => setTimeout(r, 40)) } throw new Error('condition timed out') }
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const contexts = [], errors = [], passed = []
  await fs.mkdir('test-results', { recursive: true })
  async function session(viewport = { width: 1440, height: 1000 }, role = 'admin', options = {}) {
    const context = await browser.newContext({ viewport, serviceWorkers: 'block', ...options }); contexts.push(context)
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
    const page = await context.newPage(); page.setDefaultTimeout(10000)
    page.on('pageerror', e => errors.push(e.message)); page.on('dialog', d => d.accept())
    const calls = [], state = { role, failList: false, failDetail: false, slow: false, release: undefined }
    const card = id => ({ id: String(id), title: `番剧篇章 ${id}`, originalTitle: `Original ${id}`, coverUrl: `https://anime-poster.test/${id}.svg`, date: '2026-10-01', weekday: 1 })
    const meta = () => ({ fetchedAt: Date.now(), expiresAt: Date.now() + 600000, stale: false, dataVersion: 'metadata-v1' })
    const make = i => ({ id: `sub-${i}`, entryId: `entry-${i}`, status: i % 2 ? 'draft' : 'paused', revision: 1, createdAt: Date.now(), updatedAt: Date.now(), dataVersion: 'local-v1',
      entry: { id: `entry-${i}`, bangumiId: String(1000 + i), editionKey: 'original', title: i === 1 ? '从零开始的异世界生活 第三季 袭击篇' : `番剧订阅 ${i}`, originalTitle: `Original anime ${i}`, revision: 1, metadata: { coverUrl: card(i).coverUrl, airDate: '2026-10-01' } },
      sources: Array.from({ length: 3 }, (_, n) => ({ id: `source-${i}-${n}`, role: n ? 'standby' : 'main', position: n, configuredState: n ? 'draft' : 'paused', runWhenEnabled: true, provider: 'mikan', itemId: '300', groupId: String(10 + n), groupName: `字幕组${n ? `备用${n}` : '甲'}`, revision: 1, checkedAt: null,
        binding: { schemaVersion: 1, confirmed: true, scope: { kind: 'range', first: 25, last: 64 }, chapterCorrespondence: { sourceEpisode: 25, chapterEpisode: 1 } },
        rules: { schemaVersion: 1, resolution: '1080p', include: ['简体'], exclude: [], minimumEpisode: 25, initialMode: 'from_episode' } })),
      form: { formSelections: [{ sourceRef: `source-${i}-0`, selectedReleaseIds: ['release-a', 'release-b'], unresolvedReleaseIds: ['release-a', 'release-c'], dismissedReleaseIds: ['release-old'] }], previewRefs: [] }, readiness: { ready: true, issues: [] } })
    state.items = Array.from({ length: 43 }, (_, i) => make(i + 1))
    await page.route('https://anime-poster.test/**', route => {
      const id = Number(new URL(route.request().url()).pathname.match(/\d+/)?.[0] || 1), hue = id * 47 % 360
      return route.fulfill({ contentType: 'image/svg+xml', body: `<svg xmlns="http://www.w3.org/2000/svg" width="240" height="360"><defs><linearGradient id="g" x2="1" y2="1"><stop stop-color="hsl(${hue},35%,28%)"/><stop offset="1" stop-color="hsl(${hue + 40},48%,70%)"/></linearGradient></defs><path fill="url(#g)" d="M0 0h240v360H0z"/><circle cx="180" cy="100" r="95" fill="white" opacity=".12"/><text x="22" y="300" fill="white" font-size="24" font-family="sans-serif">Bangumi ${id}</text></svg>` }).catch(() => {})
    })
    await page.route('**/api/**', async route => {
      const req = route.request(), url = new URL(req.url()), endpoint = url.pathname, method = req.method(), body = req.postDataJSON()
      calls.push({ endpoint, query: url.search, method, body }); let data = {}, status = 200
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock', username: 'admin', role: state.role, disabled: false } }
      else if (endpoint.endsWith('/catalog-status')) data = { items: body.itemIds.map(itemId => ({ itemId, bangumiId: null, subscriptions: [] })) }
      else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
      else if (endpoint === '/api/admin/anime/subscriptions' && method === 'GET') {
        if (state.slow) { state.slow = false; await new Promise(resolve => { state.release = resolve }) }
        if (state.failList) { status = 503; data = { error: '模拟本地列表读取失败' } }
        else {
          const q = url.searchParams.get('keyword') || '', filter = url.searchParams.get('status'), page = Number(url.searchParams.get('page') || 1)
          const items = state.items.filter(item => (!filter || item.status === filter) && (!q || item.entry.title.includes(q) || item.entry.originalTitle.includes(q)))
          data = { items: items.slice((page - 1) * 20, page * 20), total: items.length, page, pageSize: 20, dataVersion: 'local-v1' }
        }
      } else if (/\/subscriptions\/[^/]+$/.test(endpoint)) {
        const id = endpoint.split('/').pop(), item = state.items.find(s => s.id === id)
        if (!item) { status = 404; data = { error: 'not found' } }
        else if (method === 'PATCH') {
          item.status = body.saveMode; item.revision++; item.updatedAt = Date.now()
          item.form.formSelections = body.config.formSelections
          data = { id, entryId: item.entryId, revision: item.revision, status: item.status, sourceIds: {}, sourceRevisions: {}, requiresRepreview: true }
        } else if (state.failDetail) { status = 503; data = { error: '模拟详情读取失败' } }
        else data = item
      } else if (endpoint.endsWith('/jobs')) data = { items: [], total: 0, page: Number(url.searchParams.get('page') || 1), pageSize: 20, dataVersion: 'jobs-empty' }
      else if (endpoint.endsWith('/settings')) data = { settings: { revision: 1, accountId: 'primary', accountRevision: 2, uid: '10001', downloadCid: '100', downloadPath: '/动漫下载' }, primaryAccount: { id: 'primary', name: '115 主网盘', uid: '10001', revision: 2 }, readiness: { ready: true, issues: [] } }
      else if (endpoint.endsWith('/discovery/calendar')) data = { ...meta(), days: Array.from({ length: 7 }, (_, n) => ({ weekday: n + 1, items: Array.from({ length: 20 }, (_, i) => card(n * 100 + i + 1)) })) }
      else if (endpoint.endsWith('/discovery/search')) data = { ...meta(), total: 60, items: Array.from({ length: 20 }, (_, i) => card(8000 + body.offset + i)), limit: 20, offset: body.offset }
      else if (endpoint.endsWith('/catalog/mikan')) data = { ...meta(), provider:'mikan', days:Array.from({length:9},(_,n)=>({day:n+1,items:Array.from({length:20},(_,i)=>({...card(n*100+i+1),updatedDate:'2026-10-16'}))})),items:Array.from({length:60},(_,i)=>({...card(8000+i),updatedDate:'2026-10-16'})) }
      else if (/\/sources\/mikan\/\d+$/.test(endpoint)) data={entry:{id:endpoint.split('/').pop(),title:'蜜柑来源',groups:[{id:'10',name:'字幕组甲'}],bangumiIds:['9001']}}
      else if (endpoint.includes('/discovery/subjects/')) {
        const id = endpoint.split('/').pop(); data = { subject: { ...card(id), synopsis: '这是独立篇章的简介。'.repeat(70), platform: 'TV', totalEpisodes: 12 }, metadata: meta() }
        if (url.searchParams.has('includeRelations')) data.relations = { ...meta(), items: [{ ...card(9001), title: '续作篇章', relation: '续集' }] }
      } else if (endpoint.includes('/tmdb/')) data = { items: [], results: [], total: 0 }
      else if (endpoint.includes('/emby/')) data = { configured: false, items: [] }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
    })
    const go = async (url = '/anime/subscriptions') => { await page.goto(origin + url, { waitUntil: 'networkidle' }) }
    const navigate = async url => { await page.evaluate(async path => { await document.querySelector('#app').__vue_app__.config.globalProperties.$router.push(path) }, url) }
    const count = suffix => calls.filter(c => c.endpoint.endsWith(suffix)).length
    return { page, state, calls, go, navigate, count }
  }
  try {
    const { page, state, calls, go, navigate, count } = await session()
    await go()
    assert.equal(await page.locator('[data-subscription-id]').count(), 20)
    assert.equal(count('/subscriptions'), 1)
    assert.equal(calls.some(c => /\/subscriptions\//.test(c.endpoint)), false, 'no N+1 detail reads')
    assert.equal(calls.some(c => c.endpoint.includes('/discovery/')), false, 'list is local')
    await page.screenshot({ path: 'test-results/anime-subscriptions-desktop-light.png', fullPage: true })
    await page.getByRole('button', { name: '订阅下一页' }).click()
    await page.locator('[data-subscription-id="sub-21"]').waitFor()
    assert.ok(page.url().endsWith('?page=2'))
    await page.getByRole('searchbox', { name: '订阅名称' }).fill('番剧订阅 4')
    assert.equal(count('/subscriptions'), 2, 'typing does not fetch')
    await page.getByRole('button', { name: '搜索', exact: true }).click()
    await wait(async () => await page.locator('[data-subscription-id]').count() === 5)
    assert.equal(new URL(page.url()).searchParams.has('page'), false)
    await page.getByRole('button', { name: '已暂停', exact: true }).click()
    await wait(async () => await page.locator('[data-subscription-id]').count() === 3)
    assert.ok(page.url().includes('status=paused'))
    passed.push('本地列表每页20项，无逐卡/上游请求；显式搜索、状态筛选与URL分页')

    await navigate('/anime/subscriptions?page=2')
    await page.locator('[data-subscription-id="sub-21"]').waitFor()
    await page.evaluate(() => window.scrollTo(0, 550)); const top = await page.evaluate(() => scrollY)
    const first = page.locator('[data-subscription-id="sub-21"]')
    await first.evaluate(el => { window.__subscriptionCard = el; el.click() })
    const dialog = page.getByRole('dialog')
    await dialog.getByRole('heading', { name: '番剧订阅 21' }).waitFor()
    assert.ok(page.url().includes('/sub-21?page=2'))
    await dialog.getByText('已保存 3 条待核对预选。尚未下载，执行前需要重新预览。', { exact: true }).waitFor()
    assert.equal(await dialog.getByText(/已下载|下载进度/).count(), 0)
    await dialog.getByRole('button', { name: /备用 1/ }).click()
    await dialog.getByRole('button', { name: /备用 2/ }).click()
    await dialog.locator('.anime-floating-body').evaluate(el => { el.scrollTop = 220 })
    await wait(async () => await dialog.locator('.anime-floating-body').evaluate(el => el.scrollTop) > 100)
    const detailTop = await dialog.locator('.anime-floating-body').evaluate(el => el.scrollTop)
    await dialog.getByRole('link', { name: '编辑订阅配置' }).click()
    await page.locator('.ae-chapter').waitFor()
    await page.locator('.ae-chapter').getByRole('link', { name: '返回订阅' }).click()
    await dialog.getByRole('heading', { name: '番剧订阅 21' }).waitFor()
    assert.equal(await dialog.getByRole('button', { name: /备用 2/ }).getAttribute('aria-expanded'), 'true')
    await wait(async () => Math.abs(await dialog.locator('.anime-floating-body').evaluate(el => el.scrollTop) - detailTop) < 4)
    await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'detached' })
    await page.waitForFunction(expected => Math.abs(scrollY - expected) < 4, top)
    assert.equal(await first.evaluate(el => el === document.activeElement), true)
    const before = count('/subscriptions')
    await navigate('/users'); await navigate('/anime/subscriptions')
    await page.locator('[data-subscription-id="sub-21"]').waitFor()
    assert.equal(count('/subscriptions'), before, 'fresh return uses cache')
    assert.ok(page.url().endsWith('?page=2'))
    await page.waitForFunction(expected => Math.abs(scrollY - expected) < 4, top)
    passed.push('编辑往返保留详情展开与内部滚动，ESC恢复卡片焦点；切页返回保留页码/列表滚动且不重读')

    await first.evaluate(el => { window.__subscriptionCard = el })
    state.failList = true
    await page.getByRole('button', { name: '刷新订阅列表' }).click()
    await page.getByText(/模拟本地列表读取失败/).waitFor()
    assert.equal(await first.evaluate(el => el === window.__subscriptionCard), true)
    state.failList = false
    await page.getByRole('button', { name: '重试', exact: true }).click()
    await wait(async () => !await page.getByText(/模拟本地列表读取失败/).count())
    assert.equal(await first.evaluate(el => el === window.__subscriptionCard), true)
    await page.evaluate(() => { window.__nativeNow = Date.now; Date.now = () => window.__nativeNow() + 31000 })
    await navigate('/users'); state.failList = true; await navigate('/anime/subscriptions')
    await first.waitFor(); await page.getByText(/已保留上次结果/).waitFor()
    state.failList = false
    await page.getByRole('button', { name: '重试', exact: true }).click()
    await wait(async () => !await page.getByText(/已保留上次结果/).count())
    await page.evaluate(() => { Date.now = window.__nativeNow })
    passed.push('后台刷新保留相同卡片DOM；过期缓存先显示，失败保留结果与时间并可重试')

    state.slow = true
    await navigate('/anime/subscriptions?q=slow')
    await wait(() => !!state.release)
    await navigate('/anime/subscriptions?status=paused')
    await page.locator('[data-subscription-id="sub-2"]').waitFor()
    state.release(); await page.waitForTimeout(120)
    assert.equal(await page.locator('[data-subscription-id="sub-2"]').count(), 1)
    assert.equal(await page.getByRole('button', { name: '已暂停', exact: true }).getAttribute('aria-pressed'), 'true')
    passed.push('迟到列表响应不覆盖新筛选，新查询不混入旧卡片')

    await navigate('/anime/subscriptions/sub-1?status=draft&page=2')
    await dialog.getByRole('heading', { name: /从零开始/ }).waitFor()
    await dialog.getByRole('link', { name: '编辑订阅配置' }).click()
    await page.locator('.ae-chapter').waitFor()
    await page.getByRole('button', { name: '保存为暂停', exact: true }).click()
    await page.getByRole('heading', { name: '已保存为暂停配置' }).waitFor()
    await page.locator('.ae-receipt').getByRole('link', { name: '返回订阅', exact: true }).click()
    await dialog.getByText('配置版本 2', { exact: false }).waitFor()
    assert.equal(await dialog.locator('.as-state').innerText(), '已暂停')
    await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'detached' })
    assert.equal(await page.locator('[data-subscription-id="sub-1"]').count(), 0)
    const saved = calls.filter(c => c.method === 'PATCH')
    assert.equal(saved.length, 1); assert.equal(saved[0].body.saveMode, 'paused')
    passed.push('确认保存后失效列表和详情缓存，显示新版本/状态，并更新筛选成员')

    await navigate('/anime?view=search&q=Re0&page=3&day=4')
    assert.equal(await page.getByRole('link', { name: '我的订阅', exact: true }).getAttribute('href'), '/anime/subscriptions')
    const discoveryCard = page.locator('[data-mikan-id="8040"]'); await discoveryCard.waitFor()
    await page.evaluate(() => window.scrollTo(0, 400)); const discoveryTop = await page.evaluate(() => scrollY)
    await discoveryCard.evaluate(el => el.click())
    await page.locator('.ae-chapter').waitFor()
    assert.equal(new URL(page.url()).searchParams.get('bangumiId'), '9001')
    await page.locator('.ae-chapter').getByRole('link', { name: '返回选番' }).click()
    await discoveryCard.waitFor()
    assert.equal(new URL(page.url()).searchParams.get('page'), '3')
    assert.equal(await page.getByRole('dialog').count(), 0)
    await page.waitForFunction(expected => Math.abs(scrollY - expected) < 4, discoveryTop)
    await navigate('/anime?source=mikan&view=calendar&day=4')
    await page.locator('[data-mikan-id="302"]').click(); await page.locator('.ae-chapter').waitFor()
    await page.locator('.ae-chapter').getByRole('link', { name: '返回选番' }).click()
    assert.equal(new URL(page.url()).searchParams.get('day'), '4')
    await page.locator('[data-mikan-id="302"]').waitFor()
    assert.equal(await page.getByRole('dialog').count(), 0)
    passed.push('蜜柑单击进入配置，往返恢复搜索页码/星期与滚动，不重开摘要弹窗')

    const direct = await session()
    await direct.go('/anime/subscriptions/sub-1?status=draft&page=2')
    await direct.page.getByRole('dialog').getByRole('heading', { name: /从零开始/ }).waitFor()
    await direct.page.reload({ waitUntil: 'networkidle' })
    await direct.page.getByRole('dialog').getByRole('heading', { name: /从零开始/ }).waitFor()
    await direct.page.getByRole('dialog').getByRole('button', { name: /主 RSS/ }).click()
    assert.equal(await direct.page.getByRole('dialog').getByRole('button', { name: /主 RSS/ }).getAttribute('aria-expanded'), 'false')
    await direct.page.getByRole('link', { name: '编辑订阅配置' }).click(); await direct.page.locator('.ae-chapter').waitFor()
    await direct.page.locator('.ae-chapter').getByRole('link', { name: '返回订阅' }).click()
    await direct.page.getByRole('dialog').getByRole('heading', { name: /从零开始/ }).waitFor()
    assert.equal(await direct.page.getByRole('dialog').getByRole('button', { name: /主 RSS/ }).getAttribute('aria-expanded'), 'false', 'all-collapsed state survives editing')
    await direct.page.keyboard.press('Escape')
    assert.equal(await direct.page.getByRole('searchbox', { name: '订阅名称' }).evaluate(el => el === document.activeElement), true, 'off-page detail has a keyboard fallback')
    assert.ok(direct.page.url().includes('status=draft&page=2'))
    await direct.navigate('/anime/subscriptions/not-found')
    await direct.page.getByText('这条订阅不存在。', { exact: true }).waitFor()
    assert.equal(await direct.page.getByRole('link', { name: '编辑订阅配置' }).count(), 0)
    await direct.page.keyboard.press('Escape')
    direct.state.failDetail = true
    await direct.navigate('/anime/subscriptions/sub-2')
    await direct.page.getByRole('dialog').getByText(/模拟详情读取失败/).waitFor()
    assert.equal(await direct.page.getByRole('dialog').getByRole('heading', { name: '番剧订阅 2' }).count(), 1)
    direct.state.failDetail = false
    await direct.page.getByRole('dialog').getByRole('button', { name: '重试', exact: true }).click()
    await direct.page.getByRole('dialog').getByText(/读取于/).waitFor()
    passed.push('直接详情URL与刷新可恢复，404不展示编辑，读取失败保留列表摘要并可恢复')

    const narrow = await session({ width: 320, height: 760 })
    await narrow.go('/anime/subscriptions')
    assert.equal(await narrow.page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    await narrow.page.screenshot({ path: 'test-results/anime-subscriptions-mobile-320.png', fullPage: true })
    await narrow.page.locator('[data-subscription-id="sub-1"]').click()
    const mobileDialog = narrow.page.getByRole('dialog')
    await mobileDialog.getByRole('heading', { name: /从零开始/ }).waitFor()
    await mobileDialog.getByRole('button', { name: /备用 1/ }).click()
    const bounds = await mobileDialog.boundingBox(); assert.ok(bounds.x >= 0 && bounds.width <= 320)
    assert.equal(await mobileDialog.locator('.anime-floating-body').evaluate(el => el.scrollWidth > el.clientWidth), false)
    await mobileDialog.locator('.anime-floating-body').evaluate(el => { el.scrollTop = 0 })
    await narrow.page.waitForTimeout(80)
    await narrow.page.screenshot({ path: 'test-results/anime-subscription-detail-mobile-320.png' })
    await mobileDialog.locator('.anime-floating-body').evaluate(el => { el.scrollTop = el.scrollHeight })
    await narrow.page.waitForTimeout(80)
    await narrow.page.screenshot({ path: 'test-results/anime-subscription-detail-mobile-bottom.png' })
    assert.equal(await mobileDialog.getByRole('link', { name: '编辑订阅配置' }).isVisible(), true)
    await narrow.page.keyboard.press('Shift+Tab')
    assert.equal(await narrow.page.evaluate(() => document.querySelector('dialog').contains(document.activeElement)), true)
    await narrow.page.keyboard.press('Escape')
    assert.equal(await narrow.page.evaluate(() => document.body.style.overflow), '')
    await narrow.page.emulateMedia({ reducedMotion: 'reduce' })
    await narrow.page.evaluate(() => document.documentElement.classList.add('dark'))
    await narrow.page.locator('[data-subscription-id="sub-1"]').click()
    await narrow.page.screenshot({ path: 'test-results/anime-subscription-detail-mobile-dark.png' })
    await navigate('/anime/subscriptions/sub-1?status=paused')
    await dialog.getByRole('heading', { name: /从零开始/ }).waitFor()
    await dialog.getByRole('button', { name: /备用 1/ }).click()
    await page.screenshot({ path: 'test-results/anime-subscription-detail-desktop-light.png' })
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.screenshot({ path: 'test-results/anime-subscription-detail-desktop-dark.png' })
    passed.push('320px双列/无横向溢出，详情边界、原生键盘焦点圈与ESC滚动锁；桌面/手机/暗色截图')

    const touch = await session({width:320,height:760}, 'admin', {hasTouch:true,isMobile:true})
    await touch.go()
    assert.ok(await touch.page.evaluate(() => navigator.maxTouchPoints > 0 && matchMedia('(pointer: coarse)').matches))
    await touch.page.locator('[data-subscription-id="sub-1"]').tap()
    const touchDialog = touch.page.getByRole('dialog')
    await touchDialog.getByRole('heading', {name:/从零开始/}).waitFor()
    await touchDialog.getByRole('button', {name:/备用 1/}).tap()
    assert.equal(await touchDialog.getByRole('button', {name:/备用 1/}).getAttribute('aria-expanded'), 'true')
    await touchDialog.locator('.anime-floating-body').evaluate(el => { el.scrollTop = 220 })
    const touchTop = await touchDialog.locator('.anime-floating-body').evaluate(el => el.scrollTop)
    assert.ok(touchTop > 100)
    await touchDialog.getByRole('button', {name:'关闭订阅详情',exact:true}).tap()
    await touchDialog.waitFor({state:'detached'})
    assert.equal(await touch.page.evaluate(() => document.body.style.overflow), '')
    await touch.page.locator('[data-subscription-id="sub-1"]').tap()
    await touchDialog.getByRole('heading', {name:/从零开始/}).waitFor()
    assert.equal(await touchDialog.getByRole('button', {name:/备用 1/}).getAttribute('aria-expanded'), 'true')
    await wait(async () => Math.abs(await touchDialog.locator('.anime-floating-body').evaluate(el => el.scrollTop) - touchTop) < 4)
    assert.equal(touch.count('/subscriptions'), 1)
    assert.equal(touch.count('/subscriptions/sub-1'), 1, 'touch reopen restores cached detail without another read')
    assert.equal(await touch.page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    assert.equal(touch.calls.some(c => c.method !== 'GET'), false, 'touch browsing has no mutations')
    passed.push('真实触屏模拟320px打开/展开/关闭/重开详情，恢复展开和滚动，缓存无重复请求，背景滚动解锁')

    const oldCount = direct.count('/subscriptions')
    await direct.page.keyboard.press('Escape')
    await direct.page.evaluate(async () => { const auth = await import('/src/composables/usePortalAuth.ts'); await auth.ensurePortalSession(true) })
    assert.equal(direct.count('/subscriptions'), oldCount)
    await direct.page.evaluate(async () => { const auth = await import('/src/composables/usePortalAuth.ts'); auth.setPortalToken('other-token'); await auth.ensurePortalSession(true) })
    await wait(() => direct.count('/subscriptions') > oldCount)
    await direct.navigate('/anime/subscriptions/sub-1'); await direct.page.getByRole('dialog').waitFor()
    await direct.page.getByRole('dialog').getByRole('button', { name: /主 RSS/ }).click()
    const savedState = await direct.page.evaluate(async () => {
      const url = performance.getEntriesByType('resource').find(e => new URL(e.name).pathname === '/src/composables/useAnimeSubscriptionBrowser.ts')?.name
      if (!url) throw new Error('active cache module not found')
      window.__cacheModuleURL = url
      const cache = await import(url); return cache.subscriptionState('detail:sub-1').saved
    })
    assert.equal(savedState, true, 'inspect the active module, not a second Vite module instance')
    await direct.page.evaluate(async () => { const auth = await import('/src/composables/usePortalAuth.ts'); auth.clearPortalSession() })
    await direct.page.waitForURL(/\/admin\/login/)
    const cleared = await direct.page.evaluate(async () => { const cache = await import(window.__cacheModuleURL); return { cached: !!cache.cachedSubscription('subscriptions/sub-1'), query: cache.animeSubscriptionQuery(), saved: cache.subscriptionState('detail:sub-1').saved } })
    assert.equal(cleared.cached, false); assert.equal(cleared.query, undefined); assert.equal(cleared.saved, false)
    await navigate('/anime?source=mikan&view=calendar&day=4')
    await page.locator('[data-mikan-id="302"]').waitFor()
    const discoveryState = await page.evaluate(async () => {
      const url = performance.getEntriesByType('resource').find(e => new URL(e.name).pathname === '/src/composables/useAnimeCatalog.ts')?.name
      if (!url) throw new Error('active discovery module not found')
      window.__discoveryModuleURL = url
      const cache = await import(url); return !!cache.catalogSnapshot('mikan:calendar:2026-10')
    })
    assert.equal(discoveryState, true)
    await page.evaluate(async () => { const auth = await import('/src/composables/usePortalAuth.ts'); auth.clearPortalSession() })
    await page.waitForURL(/\/admin\/login/)
    assert.equal(await page.evaluate(async () => { const cache = await import(window.__discoveryModuleURL); return !!cache.catalogSnapshot('mikan:calendar:2026-10') }), false)
    const denied = await session({ width: 1000, height: 800 }, 'user'); await denied.go('/anime/subscriptions/sub-1')
    assert.equal(await denied.page.locator('.as-detail').count(), 0)
    assert.equal(denied.calls.some(c => c.endpoint.includes('/admin/anime')), false)
    passed.push('正常鉴权刷新保留缓存，身份切换重读，退出清空详情/查询状态；普通用户守卫拒绝挂载')

    const validation = await page.evaluate(async () => { const { animeReturnPath } = await import('/src/composables/useAnimeSubscriptionBrowser.ts'); return ['https://example.test', '//example.test', '/anime/subscriptions/sub-1/edit', '/anime/new', '/anime?day=4&subject=302', '/anime/subscriptions/sub-1?page=2'].map(animeReturnPath) })
    assert.deepEqual(validation.slice(0, 4), [undefined, undefined, undefined, undefined])
    assert.equal(validation[4], '/anime?day=4&subject=302'); assert.equal(validation[5], '/anime/subscriptions/sub-1?page=2')
    assert.equal(calls.some(c => /\/anime\/.+(enable|check|organize|releases)/.test(c.endpoint)), false)
    assert.equal(calls.filter(c => c.endpoint.includes('/admin/anime/') && c.method !== 'GET' && !c.endpoint.endsWith('/catalog-status') && !c.endpoint.endsWith('/catalog/mikan') && !c.endpoint.endsWith('/discovery/search') && !c.endpoint.endsWith('/naming/search') && !c.endpoint.endsWith('/subscriptions/sub-1')).length, 0)
    assert.deepEqual(errors, [])
    passed.push('回跳仅限已知只读动漫路由，无伪下载/启用请求，未访问真实配置或115')
    console.log(JSON.stringify({ passed, total: passed.length, pageErrors: errors }, null, 2))
  } catch (e) { console.error(e); for (let i = 0; i < contexts.length; i++) await contexts[i].pages()[0]?.screenshot({ path: `test-results/anime-browser-failure-${i}.png`, fullPage: true }).catch(() => {}); process.exitCode = 1 }
  finally { await browser.close() }
})()
