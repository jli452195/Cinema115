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
    const make = i => ({ id: `sub-${i}`, entryId: `entry-${i}`, status: i % 2 ? 'enabled' : 'paused', revision: 1, firstEnabledAt: Date.now()-60000, createdAt: Date.now(), updatedAt: Date.now(), dataVersion: 'local-v1',
      entry: { id: `entry-${i}`, bangumiId: String(1000 + i), editionKey: 'original', title: i === 1 ? '从零开始的异世界生活 第三季 袭击篇' : `番剧订阅 ${i}`, originalTitle: `Original anime ${i}`, revision: 1, metadata: { coverUrl: card(i).coverUrl, airDate: '2026-10-01' } },
      sources: Array.from({ length: 5 }, (_, n) => ({ id: `source-${i}-${n}`, role: n ? 'standby' : 'main', position: n, configuredState: 'enabled', runWhenEnabled: true, provider: 'mikan', itemId: '300', groupId: String(10 + n), groupName: n === 4 ? '很长很长的字幕组名称测试联合制作' : `字幕组${n ? `备用${n}` : '甲'}`, revision: 1, checkedAt: null,
        progress: { state: ['missing','complete','ongoing','unknown','ongoing'][n], firstEpisode: 1, publishedThrough: 10, organizedCount: n === 0 ? 9 : 10, missingCount: n === 0 ? 1 : 0, ...(n === 0 ? { missing: [{ first: 7, last: 7 }] } : {}) },
        binding: { schemaVersion: 1, confirmed: true, scope: { kind: 'range', first: 25, last: 64 }, chapterCorrespondence: { sourceEpisode: 25, chapterEpisode: 1 } },
        rules: { schemaVersion: 1, match: ['(?i)1080p', '简体'], exclude: [], minimumEpisode: 25 } })),
      form: { schemaVersion: 1 }, readiness: { ready: true, issues: [] } })
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
          data = { items: items.slice((page - 1) * 20, page * 20).map(({ id, status, entry, sources }) => ({ id, status, entry, sources: sources.map(({ id, groupName, position, configuredState, runWhenEnabled, progress: { missing, ...progress } }) => ({ id, groupName, position, configuredState, runWhenEnabled, progress })) })), total: items.length, page, pageSize: 20, dataVersion: 'local-v1' }
        }
      } else if (/\/subscriptions\/[^/]+$/.test(endpoint)) {
        const id = endpoint.split('/').pop(), item = state.items.find(s => s.id === id)
        if (!item) { status = 404; data = { error: 'not found' } }
        else if (method === 'PATCH') {
          item.status = body.saveMode; item.revision++; item.updatedAt = Date.now()

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
    assert.equal(await page.locator('[data-subscription-id]').count(),20)
    assert.equal(count('/subscriptions'),1)
    assert.equal(calls.some(c=>/\/subscriptions\//.test(c.endpoint)),false)
    const pills = page.locator('[data-subscription-id="sub-1"] .as-source-pill')
    assert.equal(await pills.count(), 5)
    assert.deepEqual(await pills.evaluateAll(elements => elements.map(el => el.dataset.state)), ['missing','complete','ongoing','unknown','ongoing'])
    assert.match(await pills.nth(3).getAttribute('title'), /状态待更新/)
    assert.match(await page.locator('[data-subscription-id="sub-1"]').getAttribute('aria-label'), /字幕组甲：缺 1 集/)
    const hostCard = page.locator('[data-subscription-id="sub-1"]')
    for (const dark of [false, true]) {
      await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
      await page.mouse.move(0, 0)
      const color = await hostCard.locator('.anime-card-title').evaluate(el => getComputedStyle(el).color)
      await hostCard.hover()
      assert.equal(await hostCard.locator('.anime-card-title').evaluate(el => getComputedStyle(el).color), color, 'subscription title keeps its neutral color on hover')
      assert.equal(await hostCard.locator('.ambient-glow').count(), 0, 'subscription poster has no blue ambient animation')
    }
    await page.evaluate(()=>document.documentElement.classList.remove('dark'))
    await page.mouse.move(0,0)
    await page.screenshot({ path:'test-results/anime-subscription-pills-desktop.png' })
    await page.getByRole('button',{name:'订阅下一页'}).click()
    await page.locator('[data-subscription-id="sub-21"]').waitFor()
    assert.ok(page.url().endsWith('?page=2'))
    await page.getByRole('searchbox',{name:'订阅名称'}).fill('番剧订阅 4')
    assert.equal(count('/subscriptions'),2)
    await page.getByRole('button',{name:'搜索',exact:true}).click()
    await wait(()=>page.locator('[data-subscription-id]').count().then(n=>n===5))
    await page.getByRole('button',{name:'已暂停',exact:true}).click()
    await wait(()=>page.locator('[data-subscription-id]').count().then(n=>n===3))
    passed.push('local paginated list, explicit search and status filter, no per-card reads')

    await navigate('/anime/subscriptions?page=2')
    const first=page.locator('[data-subscription-id="sub-21"]')
    await first.waitFor()
    await page.evaluate(()=>scrollTo(0,350))
    const top=await page.evaluate(()=>scrollY), before=count('/subscriptions')
    await first.evaluate(el=>{window.__subscriptionCard=el;el.click()})
    const dialog=page.locator('dialog[open]')
    await dialog.getByRole('heading',{name:'番剧订阅 21',exact:true}).waitFor()
    assert.equal(count('/subscriptions'),before)
    await dialog.getByRole('tab',{name:'下载记录',exact:true}).click()
    await dialog.getByText('字幕组甲：缺 1 集（第 7 集）',{exact:true}).waitFor()
    await dialog.getByRole('tab',{name:/备用 RSS/}).click()
    await dialog.getByRole('button',{name:/字幕组备用1/}).click()
    assert.equal(await dialog.getByRole('button',{name:/字幕组备用1/}).getAttribute('aria-expanded'),'true')
    await page.keyboard.press('Escape')
    await dialog.waitFor({state:'detached'})
    assert.equal(await first.evaluate(el=>el===window.__subscriptionCard),true,'same host card DOM survives modal')
    assert.equal(await first.evaluate(el=>el===document.activeElement),true)
    assert.ok(page.url().endsWith('?page=2'))
    await page.waitForFunction(t=>Math.abs(scrollY-t)<4,top)
    await first.evaluate(el=>el.click())
    await dialog.getByRole('heading',{name:'番剧订阅 21',exact:true}).waitFor()
    await page.locator('.anime-source-form:visible').getByText('自定义',{exact:true}).click()
    await page.getByLabel('从第几话开始').fill('7')
    await page.evaluate(()=>history.back())
    await page.getByRole('button',{name:'继续编辑',exact:true}).waitFor()
    await page.getByRole('button',{name:'继续编辑',exact:true}).click()
    await page.waitForURL('**/anime/subscriptions/sub-21?page=2')
    assert.equal(await page.getByLabel('从第几话开始').inputValue(),'7')
    await page.evaluate(()=>history.back())
    await page.getByRole('button',{name:'放弃修改',exact:true}).click()
    await dialog.waitFor({state:'detached'})
    assert.ok(page.url().endsWith('?page=2'))
    assert.equal(count('/subscriptions'),before)
    passed.push('retained host DOM/page/scroll/focus and real history-back dirty guard')

    await first.evaluate(el=>{window.__subscriptionCard=el})
    state.failList=true
    await page.getByRole('button',{name:'刷新订阅列表'}).click()
    await page.getByText(/模拟本地列表读取失败/).waitFor()
    assert.equal(await first.evaluate(el=>el===window.__subscriptionCard),true)
    state.failList=false
    await page.getByRole('button',{name:'重试',exact:true}).click()
    await wait(async()=>!await page.getByText(/模拟本地列表读取失败/).count())
    const reads=count('/subscriptions')
    await navigate('/users');await navigate('/anime/subscriptions')
    await first.waitFor()
    assert.equal(count('/subscriptions'),reads)
    assert.ok(page.url().endsWith('?page=2'))
    state.slow=true
    await navigate('/anime/subscriptions?q=slow')
    await wait(()=>!!state.release)
    await navigate('/anime/subscriptions?status=paused')
    await page.locator('[data-subscription-id="sub-2"]').waitFor()
    state.release();await page.waitForTimeout(80)
    assert.equal(await page.locator('[data-subscription-id="sub-2"]').count(),1)
    passed.push('cached list and late-response fencing survive navigation and failed refresh')

    const direct=await session()
    await direct.go('/anime/subscriptions/sub-1?status=enabled&page=2')
    await direct.page.locator('.ac-identity').waitFor()
    await direct.page.reload({waitUntil:'networkidle'})
    await direct.page.locator('.ac-identity').waitFor()
    await direct.page.keyboard.press('Escape')
    assert.ok(direct.page.url().includes('status=enabled&page=2'))
    await direct.navigate('/anime/subscriptions/not-found')
    await direct.page.getByText('not found',{exact:true}).waitFor()
    await direct.page.keyboard.press('Escape')
    direct.state.failDetail=true
    await direct.navigate('/anime/subscriptions/sub-2')
    await direct.page.getByText(/模拟详情读取失败/).waitFor()
    direct.state.failDetail=false
    await direct.page.getByRole('button',{name:'重新读取',exact:true}).click()
    await direct.page.locator('.ac-identity').waitFor()
    passed.push('direct URL, reload, missing subscription and retryable local read failure')

    const touch=await session({width:320,height:760},'admin',{hasTouch:true,isMobile:true})
    await touch.go()
    const layout = await touch.page.locator('[data-subscription-id="sub-1"]').evaluate(el => {
      const poster=el.querySelector('.anime-poster').getBoundingClientRect(), badge=el.querySelector('.as-badge').getBoundingClientRect()
      return [...el.querySelectorAll('.as-source-pill')].every(p => { const b=p.getBoundingClientRect(); return b.x>=poster.x && b.right<=poster.right && b.top>badge.bottom && b.bottom<=poster.bottom })
    })
    assert.ok(layout,'five pills fit within a 320px poster without overlapping the scheduling badge')
    await touch.page.screenshot({path:'test-results/anime-subscription-pills-mobile-light.png'})
    await touch.page.emulateMedia({reducedMotion:'reduce'})
    assert.equal(await touch.page.locator('.as-source-pill').first().evaluate(el=>getComputedStyle(el).transform),'none')
    await touch.page.evaluate(()=>document.documentElement.classList.add('dark'))
    await touch.page.screenshot({path:'test-results/anime-subscription-pills-mobile-dark.png'})
    await touch.page.evaluate(()=>document.documentElement.classList.remove('dark'))
    await touch.page.locator('[data-subscription-id="sub-1"]').tap()
    const td=touch.page.locator('dialog[open]')
    await touch.page.locator('.ac-identity').waitFor()
    const b=await td.boundingBox()
    assert.ok(b.x>=0&&b.width<=320)
    assert.equal(await td.locator('.anime-floating-body').evaluate(el=>el.scrollWidth>el.clientWidth),false)
    await touch.page.screenshot({path:'test-results/anime-subscription-mobile-light.png'})
    await touch.page.evaluate(()=>document.documentElement.classList.add('dark'))
    await touch.page.screenshot({path:'test-results/anime-subscription-mobile-dark.png'})
    await td.getByRole('button',{name:'关闭订阅配置',exact:true}).tap()
    assert.equal(await touch.page.evaluate(()=>document.body.style.overflow),'')
    assert.equal(touch.calls.some(c=>c.method!=='GET'&&!c.endpoint.endsWith('/catalog-status')),false)
    const denied=await session({width:1000,height:800},'user')
    await denied.go('/anime/subscriptions/sub-1')
    assert.equal(denied.calls.some(c=>c.endpoint.includes('/admin/anime')),false)
    passed.push('320px touch modal, dark/light layout, scroll unlock and role guard')
    assert.deepEqual(errors,[])
    console.log(JSON.stringify({passed,errors},null,2))
  } catch(e) {
    console.error(e)
    for(let i=0;i<contexts.length;i++) await contexts[i].pages()[0]?.screenshot({path:'test-results/anime-browser-failure-'+i+'.png',fullPage:true}).catch(()=>{})
    process.exitCode=1
  } finally {await browser.close()}
})()
