// All APIs and posters are mocked; no subscriptions or external downloads.
const assert = require('node:assert/strict')
const fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const contexts = [], errors = [], passed = []
  await fs.mkdir('test-results', { recursive: true })
  async function session(viewport = { width: 1440, height: 1000 }, role = 'admin', options = {}) {
    const context = await browser.newContext({ viewport, serviceWorkers: 'block', ...options }); contexts.push(context)
    await context.addInitScript(() => { localStorage.setItem('admin_token','mock-token'); localStorage.setItem('cinema115-theme','light') })
    const page = await context.newPage(); page.setDefaultTimeout(10000); page.on('pageerror', error => errors.push(error.message))
    const calls = [], state = { fail: false, links: ['624085'], empty: false, failEntry: false, slowEntry: '', entryRelease: undefined, statusEnabled: false, knownMikan: {}, saved: undefined, failStatus: false, slowStatus: false, statusRelease: undefined }
    const item = id => ({ id: String(id), title: id === 4058 ? '乱世千金倪亚·利斯顿转生为娇弱千金的弑神武人华丽无双录' : `蜜柑番剧 ${id}`, coverUrl: `/api/mikan/image/202610/${id}.jpg`, updatedDate: '2026-10-16' })
    await page.route('**/api/**', async route => {
      const req = route.request(), endpoint = new URL(req.url()).pathname, body = req.postDataJSON(); calls.push({ endpoint, body })
      let data = {}, status = 200
      if (endpoint === '/api/auth/me') data = { user: { id:'mock',username:'admin',role,disabled:false } }
      else if (endpoint.endsWith('/catalog-status')) {
        if (state.slowStatus) { state.slowStatus = false; await new Promise(resolve => { state.statusRelease = resolve }) }
        if (state.failStatus) { status = 503; data = { error: 'status unavailable' } }
        else data = { items: body.itemIds.map(itemId => {
          const bangumiId = state.statusEnabled ? body.provider === 'mikan' ? state.knownMikan[itemId] || null : itemId : null
          return { itemId, bangumiId: state.omitBangumi ? null : bangumiId, subscriptions: state.saved && bangumiId === '630160' ? [{ id: state.saved.id, status: state.saved.status }, ...(state.multiple ? [{id:'other-edition',status:'paused'}] : [])] : [] }
        }) }
      }
      else if (endpoint.startsWith('/api/mikan/image/')) {
        await route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="240" height="336"><defs><linearGradient id="g" x2="1" y2="1"><stop stop-color="#405d85"/><stop offset="1" stop-color="#98b6ad"/></linearGradient></defs><path fill="url(#g)" d="M0 0h240v336H0z"/><circle cx="170" cy="120" r="80" fill="white" opacity=".18"/></svg>'}); return
      } else if (endpoint.endsWith('/catalog/garden')) {
        const native={id:'630160',bangumiId:'630160',title:'Garden 原生卡片',originalTitle:'Garden original',coverUrl:'',platform:'TV',airDate:'2026-10-18'}
        if(state.fail){status=502;data={error:'Garden unavailable'}}
        else data={provider:'garden',season:body.season||'',days:Array.from({length:7},(_,i)=>({day:i+1,items:[{...native,id:String(630160+i),bangumiId:String(630160+i)}]})),items:body.keyword==='none'?[]:[native],truncated:body.keyword==='many',fetchedAt:Date.now(),expiresAt:Date.now()+600000}
      } else if (endpoint.endsWith('/catalog/anibt')) {
        if(state.fail){status=502;data={error:'模拟 AniBT 读取失败'}}
        else {
          const native={id:'630160',bangumiId:'630160',animeId:'native630160',title:'AniBT 原生卡片',originalTitle:'Native anime',coverUrl:'',format:'TV',rating:7.8,episodes:12,airingAt:1792407600,releaseCount:3,hasRelease:true}
          data={provider:'anibt',season:body.season||'',days:[{day:1,items:[native]},{day:8,items:[{...native,id:'630161',bangumiId:'630161',title:'AniBT 未排播'}]}],items:body.keyword==='none'?[]:[native],truncated:body.keyword==='many',total:1,fetchedAt:Date.now(),expiresAt:Date.now()+600000}
        }
      } else if (endpoint.endsWith('/catalog/mikan')) {
        if (body.keyword === 'slow') await new Promise(resolve => { state.release = resolve })
        if (state.fail) { status = 502; data = { error: '模拟蜜柑读取失败' } }
        else data = { provider:'mikan',season:body.season || '',fetchedAt:Date.now(),expiresAt:Date.now()+600000,days:Array.from({length:9},(_,n)=>({day:n+1,items:state.empty ? [] : Array.from({length: n ? 4 : 20},(_,i)=>item(4058+n*100+i))})),items:body.keyword==='none'?[]:Array.from({length:43},(_,i)=>item(8000+i)) }
      } else if (/\/sources\/mikan\/\d+$/.test(endpoint)) {
        const id=endpoint.split('/').pop()
        if (state.slowEntry===id) await new Promise(resolve=>{state.entryRelease=resolve})
        if(state.failEntry){status=502;data={error:'模拟条目读取失败'}}
        else { if (state.statusEnabled) state.knownMikan[id] = state.links.length === 1 ? state.links[0] : null; data={entry:{id,title:'蜜柑原站番名',groups:[{id:'10',name:'Kirara Fantasia'},{id:'20',name:'LoliHouse'},{id:'30',name:'三明治摆烂组'},{id:'40',name:'ANi'}],bangumiIds:state.links}} }
      }
      else if (endpoint.endsWith('/subscriptions') && req.method() === 'POST') {
        state.saved = { id:'cross-site-sub',entryId:'cross-site-entry',status:body.saveMode,revision:1,entry:{id:'cross-site-entry',bangumiId:'630160',title:'原站关联的 Bangumi 篇章',editionKey:'original',metadata:{}},sources:body.config.sources.map((s,i)=>({...s,id:`cross-source-${i}`,revision:1})),form:{formSelections:[]},readiness:{ready:false,issues:[]} }
        data={id:state.saved.id,revision:1,status:body.saveMode}
      }
      else if (endpoint.endsWith('/subscriptions')) data = {items:state.saved?[state.saved]:[],total:state.saved?1:0,page:1,pageSize:20}
      else if (endpoint.endsWith('/subscriptions/cross-site-sub')) data=state.saved
      else if (endpoint.endsWith('/subscriptions/cross-site-sub/jobs')) data={items:[],total:0,page:1,pageSize:20,dataVersion:'empty-jobs'}
      else if (endpoint.endsWith('/settings')) data = { settings:{revision:1,downloadCid:null},primaryAccount:null,readiness:{ready:false,issues:['PRIMARY_ACCOUNT_REQUIRED']} }
      else if (endpoint.includes('/discovery/subjects/')) {
        const id=endpoint.split('/').pop(); data={subject:{id,title:'原站关联的 Bangumi 篇章',coverUrl:'',date:'2026-10-01'},metadata:{expiresAt:Date.now()+600000}}
      } else if (endpoint.endsWith('/naming/search')) data={items:[]}
      else if (endpoint.includes('/notifications')) data={items:[],unread:0}
      await route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)}).catch(()=>{})
    })
    const go = async (url='/anime?source=mikan&day=1') => { await page.goto(origin+url,{waitUntil:'networkidle'}) }
    const navigate = async url => page.evaluate(async url => document.querySelector('#app').__vue_app__.config.globalProperties.$router.push(url),url)
    const search = async q => { await page.getByRole('searchbox',{name:'蜜柑番剧名称'}).fill(q); await page.getByRole('button',{name:'搜索',exact:true}).click() }
    return {page,state,calls,go,navigate,search,count:()=>calls.filter(c=>c.endpoint.endsWith('/catalog/mikan')).length}
  }
  try {
    const s=await session(), {page,state,calls}=s; await s.go()
    assert.equal(s.count(),1)
    assert.equal(await page.getByRole('button',{name:'AniBT',exact:true}).isDisabled(),false)
    assert.equal(await page.getByRole('button',{name:'Anime Garden',exact:true}).isDisabled(),false)
    assert.equal(calls.some(c=>c.endpoint.includes('/discovery/')||c.endpoint.includes('/sources/')),false)
    await page.getByRole('button',{name:'周日',exact:true}).click(); await page.getByRole('button',{name:'剧场版',exact:true}).click(); await page.getByRole('button',{name:'OVA',exact:true}).click(); await page.getByRole('button',{name:/^周一/}).click()
    assert.equal(s.count(),1,'categories use one calendar request')
    await page.screenshot({path:'test-results/anime-catalog-desktop-light.png',fullPage:true,animations:'disabled'})
    passed.push('独立蜜柑入口与三站可用状态，整季一次请求，星期/剧场版/OVA本地切换，无逐卡详情')
    await page.locator('[data-mikan-id="4058"]').click()
    await page.locator('.ae-chapter').waitFor()
    assert.equal(new URL(page.url()).search,'?bangumiId=624085','unique source association goes straight to the shared editor')
    assert.equal(await page.getByRole('dialog').count(),0)
    assert.equal(calls.filter(c=>c.endpoint.endsWith('/sources/mikan/4058')).length,1,'editor reuses the clicked entry without another HTTP read')
    await page.screenshot({path:'test-results/mikan-direct-editor-desktop.png',animations:'disabled'})
    await page.getByRole('button',{name:'选择资源',exact:true}).click(); await page.getByRole('button',{name:/Kirara Fantasia/}).waitFor()
    assert.equal(calls.filter(c=>c.endpoint.endsWith('/sources/mikan/search')).length,0,'clicked item goes directly to its own groups')
    await page.keyboard.press('Escape'); await page.locator('.ae-chapter').getByRole('link',{name:'返回选番'}).click(); await page.locator('[data-mikan-id="4058"]').waitFor()
    assert.equal(await page.getByRole('dialog').count(),0)
    assert.equal(new URL(page.url()).searchParams.has('item'),false)
    assert.equal(s.count(),1)
    passed.push('蜜柑单击直达统一 Bangumi 配置，无摘要弹窗；条目只读一次并复用字幕组，返回保留日历与缓存')
    await s.search('test'); await page.locator('[data-mikan-id="8000"]').waitFor(); await page.getByRole('button',{name:'搜索下一页'}).click(); await page.locator('[data-mikan-id="8020"]').waitFor()
    assert.equal(s.count(),2,'pagination is local')
    await s.navigate('/users'); await s.navigate('/anime'); await page.locator('[data-mikan-id="8020"]').waitFor()
    assert.equal(s.count(),2); assert.equal(new URL(page.url()).searchParams.get('page'),'2')
    state.fail=true; await page.getByRole('button',{name:'刷新当前站点'}).click(); await page.getByText('模拟蜜柑读取失败，已保留上次结果。',{exact:true}).waitFor(); assert.equal(await page.locator('[data-mikan-id="8020"]').count(),1); state.fail=false
    passed.push('搜索仅请求蜜柑，本地分页与页面往返恢复，刷新失败保留结果')
    await s.search('slow'); await page.waitForFunction(()=>document.querySelector('.anime-grid[aria-busy="true"]'))
    await s.search('fast'); await page.locator('[data-mikan-id="8000"]').waitFor(); state.release(); await page.waitForTimeout(80)
    assert.equal(new URL(page.url()).searchParams.get('q'),'fast'); assert.equal(await page.getByRole('searchbox').inputValue(),'fast')
    await s.search('none'); await page.getByText('蜜柑未找到相关番剧',{exact:true}).waitFor(); await page.getByRole('button',{name:'清空搜索并返回日历'}).click(); await page.locator('[data-mikan-id="4058"]').waitFor()
    passed.push('取消过期搜索、防止旧响应覆盖，零结果与清空返回日历')
    state.links=['1','2']; await page.locator('[data-mikan-id="4059"]').click(); await page.locator('.ae-chapter').waitFor()
    assert.equal(new URL(page.url()).searchParams.has('bangumiId'),false)
    assert.equal(await page.getByRole('dialog').count(),0)
    await page.locator('.ae-chapter').getByRole('link',{name:'返回选番'}).click(); await page.locator('[data-mikan-id="4060"]').waitFor()
    state.links=[]; await page.locator('[data-mikan-id="4060"]').click(); await page.getByRole('heading',{name:'蜜柑原站番名',exact:true}).waitFor()
    assert.equal(new URL(page.url()).searchParams.has('bangumiId'),false)
    passed.push('关联缺失或异常时直接进入未关联草稿，不猜测关联或增加弹窗')
    await page.locator('.ae-chapter').getByRole('link',{name:'返回选番'}).click();await page.locator('[data-mikan-id="4061"]').waitFor()
    state.failEntry=true;await page.locator('[data-mikan-id="4061"]').click();await page.getByText(/模拟条目读取失败/).waitFor()
    assert.equal(new URL(page.url()).pathname,'/anime/new');assert.equal(await page.getByRole('dialog').count(),0)
    state.failEntry=false;await page.getByRole('button',{name:'重新读取',exact:true}).click();await page.locator('.ae-chapter').waitFor()
    await page.locator('.ae-chapter').getByRole('link',{name:'返回选番'}).click();await page.locator('[data-mikan-id="4062"]').waitFor()
    state.slowEntry='4062';await page.locator('[data-mikan-id="4062"]').evaluate(el=>{el.click();el.click()})
    await page.locator('.ae-entry-loading').waitFor()
    assert.equal(new URL(page.url()).pathname,'/anime/new','route changes before the entry response')
    assert.equal(await page.locator('.mikan-anime-card').count(),0)
    await page.screenshot({path:'test-results/mikan-entry-loading-desktop.png',animations:'disabled'})
    assert.equal(calls.filter(c=>c.endpoint.endsWith('/sources/mikan/4062')).length,1)
    await page.goBack();await page.locator('[data-mikan-id="4062"]').waitFor()
    // Provider switches use today's weekday; the fixture has AniBT entries on Monday only.
    await page.getByRole('button',{name:'AniBT',exact:true}).click();await page.getByRole('button',{name:/^周一/}).click();await page.locator('.anibt-anime-card').waitFor()
    state.entryRelease();await page.waitForTimeout(100);assert.equal(new URL(page.url()).searchParams.get('source'),'anibt')
    await page.getByRole('button',{name:'蜜柑',exact:true}).click();await page.locator('[data-mikan-id="4063"]').waitFor()
    state.slowEntry='4063';await page.locator('[data-mikan-id="4063"]').click();await page.locator('.ae-entry-loading').waitFor()
    await page.goBack();await page.locator('[data-mikan-id="4064"]').waitFor()
    state.links=['624085'];await page.locator('[data-mikan-id="4064"]').click();await page.locator('.ae-chapter').waitFor()
    assert.equal(await page.evaluate(()=>history.state.animeSource.itemId),'4064')
    state.entryRelease();await page.waitForTimeout(100);assert.equal(await page.evaluate(()=>history.state.animeSource.itemId),'4064')
    passed.push('点击立刻进入配置页后才读条目；失败重试、重复点击、返回切站/换卡和迟到响应隔离')
    const mobile=await session({width:390,height:844}); await mobile.go(); assert.equal(await mobile.page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
    assert.equal(await mobile.page.locator('.anime-provider-button').count(),3)
    assert.ok(await mobile.page.locator('.anime-provider-button').evaluateAll(buttons=>buttons.every(button=>button.getBoundingClientRect().height>=44)),'brand buttons keep usable touch targets')
    assert.ok(await mobile.page.locator('.anime-provider-button img').evaluateAll(images=>images.every(image=>image.complete&&image.naturalWidth>0)),'all official brand images loaded locally')
    await mobile.page.screenshot({path:'test-results/anime-catalog-mobile-light.png',fullPage:true,animations:'disabled'})
    await mobile.page.evaluate(()=>document.documentElement.classList.add('dark'))
    assert.equal(await mobile.page.locator('.provider-garden-name').evaluate(e=>getComputedStyle(e).color),'rgb(242, 176, 199)','dark brand label uses its readable theme colour')
    assert.equal(await mobile.page.locator('.mikan-anime-card strong').first().evaluate(e=>getComputedStyle(e).color),'rgb(243, 244, 245)','dark card titles remain readable')
    assert.equal(await mobile.page.locator('.catalog-days button[aria-pressed="true"]').evaluate(e=>getComputedStyle(e).color),'rgb(147, 197, 253)','dark selection styles target the selected control')
    await mobile.page.screenshot({path:'test-results/anime-catalog-mobile-dark.png',fullPage:true,animations:'disabled'})
    await mobile.page.setViewportSize({width:320,height:760}); assert.equal(await mobile.page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
    mobile.state.slowEntry='4058';await mobile.page.locator('[data-mikan-id="4058"]').focus();await mobile.page.keyboard.press('Enter');await mobile.page.locator('.ae-entry-loading').waitFor()
    await mobile.page.screenshot({path:'test-results/mikan-entry-loading-mobile-dark.png',animations:'disabled'})
    mobile.state.entryRelease();await mobile.page.locator('.ae-chapter').waitFor();assert.equal(await mobile.page.getByRole('dialog').count(),0)
    assert.equal(await mobile.page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
    await mobile.page.screenshot({path:'test-results/mikan-direct-editor-mobile-dark.png',animations:'disabled'})
    await mobile.page.locator('.ae-chapter').getByRole('link',{name:'返回选番'}).click();await mobile.page.locator('[data-mikan-id="4058"]').waitFor()
    await mobile.page.locator('.catalog-providers').screenshot({path:'test-results/anime-provider-buttons-mobile-320.png',animations:'disabled'})
    await mobile.navigate('/anime?source=garden&q=test'); await mobile.page.locator('.garden-anime-card').waitFor(); assert.equal(mobile.count(),1,'Garden never falls back to Mikan'); await mobile.page.getByRole('button',{name:'蜜柑',exact:true}).click(); await mobile.page.locator('[data-mikan-id="4058"]').waitFor()
    passed.push('390/320手机明暗和键盘直达配置，无横向溢出，站点读取隔离')
    const native=await session(); await native.go('/anime?source=anibt&day=1')
    await native.page.locator('[data-anibt-id="630160"]').waitFor()
    assert.equal(native.count(),0);assert.equal(await native.page.locator('.mikan-anime-card').count(),0)
    assert.equal(native.calls.some(c=>c.endpoint.includes('/sources/')||c.endpoint.includes('/discovery/')),false)
    await native.page.getByRole('button',{name:'未排播',exact:true}).click();await native.page.locator('[data-anibt-id="630161"]').waitFor()
    assert.equal(native.calls.filter(c=>c.endpoint.endsWith('/catalog/anibt')).length,1)
    await native.page.getByRole('searchbox',{name:'AniBT番剧名称'}).fill('many');await native.page.getByRole('button',{name:'搜索',exact:true}).click()
    await native.page.getByText(/AniBT 最多返回 50/).waitFor()
    await native.page.getByRole('button',{name:'蜜柑',exact:true}).click();await native.page.locator('.mikan-anime-card').first().waitFor()
    await native.page.getByRole('searchbox').fill('mikan draft')
    await native.page.getByRole('button',{name:'AniBT',exact:true}).click();await native.page.locator('.anibt-anime-card').waitFor()
    assert.equal(await native.page.getByRole('searchbox').inputValue(),'many')
    assert.equal(native.calls.filter(c=>c.endpoint.endsWith('/catalog/anibt')).length,2,'return uses provider-specific cache')
    native.state.fail=true;await native.page.getByRole('button',{name:'刷新当前站点',exact:true}).click();await native.page.getByText(/模拟 AniBT/).waitFor();assert.equal(await native.page.locator('.anibt-anime-card').count(),1)
    await native.page.screenshot({path:'test-results/anibt-catalog-desktop.png',animations:'disabled'})
    passed.push('AniBT native cards/categories/search isolated from Mikan; one seasonal read, 50-result boundary, per-site drafts/cache, failed refresh retains cards')
    const garden=await session(); await garden.go('/anime?source=garden&day=1')
    await garden.page.locator('.garden-anime-card').waitFor()
    assert.equal(garden.calls.filter(c=>c.endpoint.endsWith('/catalog/garden')).length,1)
    assert.equal(garden.calls.some(c=>c.endpoint.includes('/discovery/')||c.endpoint.includes('/sources/')),false)
    assert.equal(await garden.page.getByRole('button',{name:'OVA',exact:true}).count(),0)
    assert.equal(await garden.page.locator('.garden-anime-card').getAttribute('href'),'/anime/new?bangumiId=630160')
    assert.equal(await garden.page.locator('.garden-anime-card').innerText().then(t=>t.includes('2026-10-18')),true)
    await garden.page.getByRole('button',{name:/^周日/}).click();await garden.page.locator('[data-garden-id="630166"]').waitFor()
    assert.equal(garden.calls.filter(c=>c.endpoint.endsWith('/catalog/garden')).length,1)
    await garden.page.screenshot({path:'test-results/garden-catalog-desktop.png',animations:'disabled'})
    await garden.page.getByRole('searchbox').fill('many');await garden.page.getByRole('button',{name:'搜索',exact:true}).click();await garden.page.getByText(/Anime Garden.*100/).waitFor()
    await garden.page.getByRole('button',{name:'AniBT',exact:true}).click();await garden.page.getByRole('button',{name:/^周一/}).click();await garden.page.locator('.anibt-anime-card').waitFor()
    await garden.page.getByRole('button',{name:'Anime Garden',exact:true}).click();await garden.page.locator('.garden-anime-card').waitFor()
    assert.equal(await garden.page.getByRole('searchbox').inputValue(),'many')
    assert.equal(garden.calls.filter(c=>c.endpoint.endsWith('/catalog/garden')).length,2)
    garden.state.fail=true;await garden.page.getByRole('button',{name:'刷新当前站点'}).click();await garden.page.getByText(/Garden unavailable/).waitFor();assert.equal(await garden.page.locator('.garden-anime-card').count(),1)
    await garden.page.setViewportSize({width:320,height:800});await garden.page.waitForTimeout(120)
    assert.equal(await garden.page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
    await garden.page.screenshot({path:'test-results/garden-catalog-mobile.png',animations:'disabled'})
    await garden.page.evaluate(()=>document.documentElement.classList.add('dark'));await garden.page.screenshot({path:'test-results/garden-catalog-mobile-dark.png',animations:'disabled'})
    passed.push('Garden native card, one seasonal request, no per-card calls, same Bangumi URL, scoped search/cache/errors and 320px light/dark')

    const cross = await session(); cross.state.statusEnabled = true; cross.state.links = ['630160']
    await cross.go('/anime?source=anibt&day=1');await cross.page.locator('[data-anibt-id="630160"]').waitFor()
    await cross.page.getByRole('button',{name:'Anime Garden',exact:true}).click();await cross.page.getByRole('button',{name:/^周一/}).click();await cross.page.locator('[data-garden-id="630160"]').waitFor()
    await cross.navigate('/anime?source=mikan&day=1');await cross.page.locator('[data-mikan-id="4058"]').click();await cross.page.locator('.ae-chapter').waitFor()
    await cross.page.getByRole('button',{name:'保存草稿',exact:true}).click();await cross.page.getByRole('heading',{name:'草稿已保存'}).waitFor()
    await cross.page.locator('.ae-receipt').getByRole('link',{name:'返回选番'}).click()
    await cross.page.locator('[data-mikan-id="4058"] .catalog-subscription-badge').waitFor()
    assert.equal(await cross.page.locator('[data-mikan-id="4058"] .catalog-subscription-badge').innerText(),'草稿')
    const sourceReads=cross.calls.filter(c=>c.endpoint.includes('/sources/')||c.endpoint.includes('/discovery/')).length
    for (const provider of ['mikan','anibt','garden']) {
      await cross.navigate(`/anime?source=${provider}&day=1`)
      const selector=provider==='mikan'?'[data-mikan-id="4058"]':`[data-${provider}-id="630160"]`
      await cross.page.locator(`${selector} .catalog-subscription-badge`).waitFor()
      assert.match(await cross.page.locator(selector).getAttribute('href'),/^\/anime\/subscriptions\/cross-site-sub\?/)
      assert.match(await cross.page.locator(selector).getAttribute('aria-label'),/草稿，查看订阅配置/)
      await cross.page.locator(selector).click();await cross.page.getByRole('dialog').waitFor()
      assert.equal(new URL(cross.page.url()).pathname,'/anime/subscriptions/cross-site-sub')
      await cross.page.keyboard.press('Escape');await cross.page.locator(selector).waitFor()
      assert.equal(new URL(cross.page.url()).searchParams.get('source'),provider)
    }
    assert.equal(cross.calls.filter(c=>c.endpoint.includes('/sources/')||c.endpoint.includes('/discovery/')).length,sourceReads,'existing subscription never searches a provider or metadata')
    assert.equal(cross.calls.filter(c=>c.endpoint.endsWith('/subscriptions')&&c.body?.saveMode).length,1,'one configuration across all sites')
    const beforeCache=cross.calls.filter(c=>c.endpoint.endsWith('/catalog-status')).length
    await cross.navigate('/anime?source=anibt&day=1');await cross.page.locator('.catalog-subscription-badge').waitFor()
    await cross.navigate('/anime?source=garden&day=1');await cross.page.locator('.catalog-subscription-badge').waitFor()
    assert.equal(cross.calls.filter(c=>c.endpoint.endsWith('/catalog-status')).length,beforeCache,'switching back uses the short local status cache')
    await cross.page.screenshot({path:'test-results/anime-cross-site-status-desktop.png',animations:'disabled'})
    await cross.page.setViewportSize({width:320,height:844});await cross.page.evaluate(()=>document.documentElement.classList.add('dark'))
    assert.equal(await cross.page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
    await cross.page.screenshot({path:'test-results/anime-cross-site-status-mobile-dark.png',animations:'disabled'})
    await cross.page.reload({waitUntil:'networkidle'});await cross.page.locator('.catalog-subscription-badge').waitFor()
    await cross.navigate('/anime?source=mikan&day=1');await cross.page.locator('.catalog-subscription-badge').waitFor()
    assert.equal(cross.calls.filter(c=>c.endpoint.includes('/sources/')||c.endpoint.includes('/discovery/')).length,sourceReads,'persistent mapping works after a document reload')
    passed.push('保存后失效三站状态缓存；同 Bangumi 草稿标记和直达已有订阅，关闭恢复站点；切站/重载无额外上游读取，320px暗色')

    cross.state.saved.status='paused';await cross.page.getByRole('button',{name:'刷新当前站点'}).click();await cross.page.locator('.catalog-subscription-badge[data-status="paused"]').waitFor()
    cross.state.saved.status='enabled';await cross.page.getByRole('button',{name:'刷新当前站点'}).click();await cross.page.locator('.catalog-subscription-badge[data-status="enabled"]').waitFor()
    cross.state.saved.status='archived';await cross.page.getByRole('button',{name:'刷新当前站点'}).click();await cross.page.locator('.catalog-subscription-badge[data-status="archived"]').waitFor()
    cross.state.failStatus=true;await cross.page.getByRole('button',{name:'刷新当前站点'}).click();await cross.page.getByRole('button',{name:'重新核对',exact:true}).waitFor()
    assert.equal(await cross.page.locator('.catalog-subscription-badge').count(),0,'failed status read does not claim subscribed')
    cross.state.failStatus=false;await cross.page.getByRole('button',{name:'重新核对',exact:true}).click();await cross.page.locator('.catalog-subscription-badge').waitFor()
    const previousSaved=cross.state.saved
    cross.state.saved=undefined;await cross.page.getByRole('button',{name:'刷新当前站点'}).click();await cross.page.waitForTimeout(150)
    assert.equal(await cross.page.locator('.catalog-subscription-badge').count(),0,'removed configurations are not marked')
    assert.equal(await cross.page.locator('[data-mikan-id="4058"]').getAttribute('href'),'/anime/new?bangumiId=630160','mapping survives subscription removal')
    passed.push('草稿/暂停/启用/归档准确区分，刷新读取本地真值；状态失败可重试，移除后保留映射但去掉标记')

    cross.state.saved=previousSaved;cross.state.multiple=true
    await cross.page.getByRole('button',{name:'刷新当前站点'}).click();await cross.page.getByText('2 个配置',{exact:true}).waitFor()
    await cross.page.locator('[data-mikan-id="4058"]').click();await cross.page.locator('.as-toolbar').waitFor()
    assert.equal(new URL(cross.page.url()).pathname,'/anime/subscriptions')
    assert.equal(new URL(cross.page.url()).searchParams.get('bangumiId'),'630160')
    assert.equal(await cross.page.getByRole('dialog').count(),0,'multiple editions do not select one arbitrarily')
    await cross.page.locator('[data-subscription-id="cross-site-sub"]').click();await cross.page.getByRole('dialog').waitFor()
    await cross.page.keyboard.press('Escape');await cross.page.locator('[data-subscription-id="cross-site-sub"]').waitFor()
    assert.equal(new URL(cross.page.url()).pathname,'/anime/subscriptions','closing a selected edition returns to its filtered list')
    assert.equal(new URL(cross.page.url()).searchParams.get('bangumiId'),'630160')
    await cross.page.getByRole('link',{name:'返回选番',exact:true}).click();await cross.page.getByText('2 个配置',{exact:true}).waitFor()
    cross.state.multiple=false;cross.state.saved.status='draft'
    await cross.page.evaluate(()=>{const current=Date.now;Date.now=()=>current()+31000;window.dispatchEvent(new Event('focus'))})
    await cross.page.locator('.catalog-subscription-badge[data-status="draft"]').waitFor()
    cross.state.slowStatus=true;await cross.page.getByRole('button',{name:'刷新当前站点'}).click()
    while(!cross.state.statusRelease) await cross.page.waitForTimeout(20)
    await cross.navigate('/anime?source=anibt&day=1');await cross.page.locator('.catalog-subscription-badge[data-status="draft"]').waitFor()
    cross.state.statusRelease();await cross.page.waitForTimeout(80)
    assert.equal(new URL(cross.page.url()).searchParams.get('source'),'anibt')
    assert.equal(await cross.page.locator('[data-anibt-id="630160"] .catalog-subscription-badge').innerText(),'草稿')
    cross.state.multiple=true;cross.state.omitBangumi=true
    await cross.page.getByRole('button',{name:'刷新当前站点'}).click();await cross.page.getByText('2 个配置',{exact:true}).waitFor()
    assert.equal(new URL(await cross.page.locator('[data-anibt-id="630160"]').getAttribute('href'),origin).searchParams.get('bangumiId'),'630160','native catalog identity still filters multiple configurations if status has no external link')
    await cross.page.evaluate(async()=>{const auth=await import('/src/composables/usePortalAuth.ts');auth.clearPortalSession()})
    await cross.page.waitForURL(/\/admin\/login/)
    assert.equal(await cross.page.locator('.catalog-subscription-badge').count(),0)
    passed.push('多个版本进入精确Bangumi筛选列表；30秒后回焦点仅核对本地状态；切站迟到响应隔离，退出清空状态')
    const touch=await session({width:320,height:760},'admin',{hasTouch:true,isMobile:true});await touch.go()
    assert.ok(await touch.page.evaluate(()=>navigator.maxTouchPoints>0&&matchMedia('(pointer: coarse)').matches))
    await touch.page.getByRole('button',{name:'AniBT',exact:true}).tap();await touch.page.getByRole('button',{name:/^周一/}).tap();await touch.page.locator('.anibt-anime-card').first().waitFor()
    await touch.page.getByRole('button',{name:'蜜柑',exact:true}).tap();await touch.page.locator('[data-mikan-id="4058"]').waitFor()
    assert.equal(touch.count(),1,'touch site round-trip uses the existing calendar cache')
    await touch.page.locator('[data-mikan-id="4058"]').tap();await touch.page.locator('.ae-chapter').waitFor()
    await touch.page.getByRole('button',{name:'选择资源',exact:true}).tap();await touch.page.getByRole('button',{name:/Kirara Fantasia/}).waitFor()
    await touch.page.getByRole('button',{name:'关闭来源选择器',exact:true}).tap()
    await touch.page.locator('.ae-chapter').getByRole('link',{name:'返回选番'}).tap();await touch.page.locator('[data-mikan-id="4058"]').waitFor()
    assert.equal(touch.count(),1);assert.equal(touch.calls.filter(c=>c.endpoint.endsWith('/sources/mikan/4058')).length,1)
    assert.equal(await touch.page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
    assert.equal(touch.calls.some(c=>c.endpoint.endsWith('/subscriptions')&&c.method==='POST'),false)
    passed.push('真实触屏模拟320px切换站点、卡片进入/关闭来源/返回；日历和条目复用缓存且不保存订阅')
    const ordinary=await session(undefined,'user'); await ordinary.go(); assert.equal(ordinary.calls.some(c=>c.endpoint.includes('/admin/anime')),false)
    assert.deepEqual(errors,[]); console.log(JSON.stringify({passed,errors},null,2))
  } finally { await Promise.allSettled(contexts.map(c=>c.close())); await browser.close() }
})().catch(e=>{console.error(e);process.exitCode=1})
