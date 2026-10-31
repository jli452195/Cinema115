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
    const calls = [], state = { fail: false, failSeasons: false, duplicate: false, mismatch: false, slowSeason: '', links: ['624085'], empty: false, failEntry: false, slowEntry: '', entryRelease: undefined, statusEnabled: false, knownMikan: {}, saved: undefined, failStatus: false, slowStatus: false, statusRelease: undefined }
    const item = id => ({ id: String(id), title: id === 4058 ? '乱世千金倪亚·利斯顿转生为娇弱千金的弑神武人华丽无双录' : `蜜柑番剧 ${id}`, coverUrl: `/api/mikan/image/202610/${id}.jpg`, updatedDate: '2026-10-16' })
    await page.route('**/api/**', async route => {
      const req = route.request(), endpoint = new URL(req.url()).pathname, body = req.postDataJSON(); calls.push({ endpoint, body })
      let data = {}, status = 200
      if (endpoint === '/api/auth/me') data = { user: { id:'mock',username:'admin',role,disabled:false } }
      else if (/\/catalog\/(mikan|anibt|garden)\/seasons$/.test(endpoint)) {
        if(state.failSeasons){status=502;data={error:'archive unavailable'}}
        else data={provider:endpoint.split('/').at(-2),seasons:['2026-10','2024-01','2012-04'],fetchedAt:Date.now(),expiresAt:Date.now()+600000}
      }
      else if (endpoint.endsWith('/catalog-status')) {
        if (state.slowStatus) { state.slowStatus = false; await new Promise(resolve => { state.statusRelease = resolve }) }
        if (state.failStatus) { status = 503; data = { error: 'status unavailable' } }
        else data = { items: body.itemIds.map(itemId => {
          const bangumiId = state.statusEnabled ? body.provider === 'mikan' ? state.knownMikan[itemId] || null : itemId : null
          return { itemId, bangumiId: state.omitBangumi ? null : bangumiId, subscriptions: state.saved && bangumiId === '630160' ? [{ id: state.saved.id, status: state.saved.status }, ...(state.multiple ? [{id:'other-edition',status:'paused'}] : [])] : [] }
        }) }
      }
      else if (/^\/api\/(mikan|anibt|garden)\/image\//.test(endpoint)) {
        if (state.failImages) { await route.fulfill({status:404,body:''}); return }
        await route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="240" height="336"><defs><linearGradient id="g" x2="1" y2="1"><stop stop-color="#405d85"/><stop offset="1" stop-color="#98b6ad"/></linearGradient></defs><path fill="url(#g)" d="M0 0h240v336H0z"/><circle cx="170" cy="120" r="80" fill="white" opacity=".18"/></svg>'}); return
      } else if (endpoint.endsWith('/catalog/garden')) {
        const native={id:'630160',bangumiId:'630160',title:'Garden 原生卡片',originalTitle:'Garden original',coverUrl:state.cardCovers?'/api/garden/image/630160/poster.jpeg':'',platform:'TV',airDate:'2026-10-18'}
        if(state.fail){status=502;data={error:'Garden unavailable'}}
        else data={provider:'garden',season:body.season||'',days:[...Array.from({length:7},(_,i)=>({day:i+1,items:[{...native,id:String(630160+i),bangumiId:String(630160+i)}]})),{day:8,items:[native,{...native,id:'640000',bangumiId:'640000',title:'网络动画'}]}],items:body.keyword==='none'?[]:[native],truncated:body.keyword==='many',fetchedAt:Date.now(),expiresAt:Date.now()+600000}
      } else if (endpoint.endsWith('/catalog/anibt')) {
        if(state.fail){status=502;data={error:'模拟 AniBT 读取失败'}}
        else {
          const native={id:'630160',bangumiId:'630160',animeId:'native630160',title:'AniBT 原生卡片',originalTitle:'Native anime',coverUrl:state.cardCovers?'/api/anibt/image/mock630160/'+'a'.repeat(64)+'.webp':'',format:'TV',rating:7.8,episodes:12,airingAt:1792407600,releaseCount:3,hasRelease:true}
          data={provider:'anibt',season:body.season||'',days:[{day:1,items:[native]},{day:8,items:[{...native,id:'630161',bangumiId:'630161',title:'AniBT 未排播'}]}],items:body.keyword==='none'?[]:[native],truncated:body.keyword==='many',total:1,fetchedAt:Date.now(),expiresAt:Date.now()+600000}
        }
      } else if (endpoint.endsWith('/catalog/mikan')) {
        if (body.keyword === 'slow') await new Promise(resolve => { state.release = resolve })
        if (body.season === state.slowSeason) await new Promise(resolve => { state.seasonRelease = resolve })
        if (state.fail) { status = 502; data = { error: '模拟蜜柑读取失败' } }
        else data = { provider:'mikan',season:state.mismatch?'2020-01':body.season || '',fetchedAt:Date.now(),expiresAt:Date.now()+600000,days:Array.from({length:9},(_,n)=>({day:n+1,items:state.empty || state.emptyDays?.includes(n+1) ? [] : Array.from({length: n ? 4 : 20},(_,i)=>item(state.duplicate&&n===1&&i===0?4058:4058+n*100+i))})),items:body.keyword==='none'?[]:Array.from({length:43},(_,i)=>item(8000+i)) }
      } else if (/\/sources\/mikan\/\d+$/.test(endpoint)) {
        const id=endpoint.split('/').pop()
        if (state.slowEntry===id) await new Promise(resolve=>{state.entryRelease=resolve})
        if(state.failEntry){status=502;data={error:'模拟条目读取失败'}}
        else { if (state.statusEnabled) state.knownMikan[id] = state.links.length === 1 ? state.links[0] : null; data={entry:{id,title:'蜜柑原站番名',groups:[{id:'10',name:'Kirara Fantasia'},{id:'20',name:'LoliHouse'},{id:'30',name:'三明治摆烂组'},{id:'40',name:'ANi'}],bangumiIds:state.links}} }
      }
      else if (endpoint.endsWith('/subscriptions') && req.method() === 'POST') {
        state.saved = { id:'cross-site-sub',entryId:'cross-site-entry',status:'enabled',revision:2,firstEnabledAt:Date.now(),entry:{id:'cross-site-entry',bangumiId:'630160',title:'原站关联的 Bangumi 篇章',editionKey:'original',metadata:{}},sources:body.config.sources.map((s,i)=>({...s,id:`cross-source-${i}`,revision:2,configuredState:'enabled'})),naming:{schemaVersion:1,target:{tmdbId:'123',title:'测试番剧',year:'2026',groupId:'',groupName:'TMDB默认',season:1,fingerprint:'a'.repeat(64),seasons:[{number:1,name:'第一季',episodeCount:12,episodes:[{id:'101',number:1,name:'第一话',sourceSeason:1,sourceNumber:1}]}]},sources:body.config.naming.sources.map((v,i)=>({...v,sourceRef:'cross-source-'+i}))},form:{schemaVersion:1},readiness:{ready:true,issues:[]} }
        data={id:state.saved.id,revision:2,status:'enabled'}
      }
      else if (endpoint.endsWith('/source-bindings/preview')) data={source:{...body.source,expiresAt:Date.now()+600000},releases:[],profiles:[],profilesTruncated:false,observedAt:Date.now(),specVersion:1}
      else if (endpoint.endsWith('/subscriptions/preview')) {
        const summary={sourceRef:body.previewSourceRef,sourcePreviewId:'cross-preview',loadRevision:1,generatedAt:Date.now(),expiresAt:Date.now()+900000,expired:false,rawCount:0,loadedCount:0,matchedCount:0,reviewCount:0,selectedCount:0,pendingSelectionCount:0,completeness:'visible_end',stopReason:'',loadedFirst:null,loadedLast:null,gapsWithinLoadedMatches:[],unloadedHistory:false}
        data={batchId:'cross-batch',budget:{limit:2000,totalRawCount:0,remaining:2000},sources:[summary],issues:[],page:{items:[],total:0,page:1,pageSize:50,summary,selectedIds:[],pendingSelections:[]}}
      }
      else if (endpoint.endsWith('/subscriptions')) data = {items:state.saved?[state.saved]:[],total:state.saved?1:0,page:1,pageSize:20}
      else if (endpoint.endsWith('/subscriptions/cross-site-sub')) data=state.saved
      else if (endpoint.endsWith('/subscriptions/cross-site-sub/jobs')) data={items:[],total:0,page:1,pageSize:20,dataVersion:'empty-jobs'}
      else if (endpoint.endsWith('/settings')) data = state.statusEnabled ? {settings:{revision:1,accountId:'primary',downloadCid:'100',downloadPath:'/下载',organizeCid:'200',organizePath:'/整理'},primaryAccount:{id:'primary',name:'主账号'},readiness:{ready:true,issues:[]}} : { settings:{revision:1,downloadCid:null},primaryAccount:null,readiness:{ready:false,issues:['PRIMARY_ACCOUNT_REQUIRED']} }
      else if (endpoint.includes('/discovery/subjects/')) {
        const id=endpoint.split('/').pop(); data={subject:{id,title:'原站关联的 Bangumi 篇章',coverUrl:'',date:'2026-10-01'},metadata:{expiresAt:Date.now()+600000}}
      } else if (endpoint.endsWith('/naming/search')) data=state.statusEnabled?{items:[{id:123,title:'测试番剧',date:'2026-10-01'}],recommendedId:'123'}:{items:[]}
      else if (endpoint.endsWith('/naming/target')) data={tmdbId:'123',title:'测试番剧',year:'2026',groupId:'',groupName:'TMDB默认',season:1,fingerprint:'a'.repeat(64),seasons:[{number:1,name:'第一季',episodeCount:12,episodes:[{id:'101',number:1,name:'第一话',sourceSeason:1,sourceNumber:1}]}]}
      else if (endpoint.includes('/notifications')) data={items:[],unread:0}
      await route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)}).catch(()=>{})
    })
    const go = async (url='/anime?source=mikan&season=2026-10&day=1') => { await page.goto(origin+url,{waitUntil:'networkidle'}) }
    const navigate = async url => page.evaluate(async url => document.querySelector('#app').__vue_app__.config.globalProperties.$router.push(url),url)
    const search = async q => { await page.getByRole('searchbox',{name:'蜜柑番剧名称'}).fill(q); await page.getByRole('button',{name:'搜索',exact:true}).click() }
    return {page,state,calls,go,navigate,search,count:()=>calls.filter(c=>c.endpoint.endsWith('/catalog/mikan')).length}
  }
  try {
    const s=await session(),{page,state,calls}=s
    await s.go()
    assert.equal(s.count(),1)
    assert.equal(calls.some(c=>c.endpoint.includes('/sources/')||c.endpoint.includes('/discovery/')),false)
    await page.getByRole('button',{name:'周日',exact:true}).click()
    await page.getByRole('button',{name:/^周一/}).click()
    assert.equal(s.count(),1)
    const card=page.locator('[data-mikan-id="4058"]')
    await card.evaluate(el=>{window.__catalogCard=el;el.click()})
    await page.locator('.ac-identity').waitFor()
    assert.equal(new URL(page.url()).searchParams.get('bangumiId'),'624085')
    assert.equal(await page.locator('dialog[open]').count(),1)
    assert.equal(calls.filter(c=>c.endpoint.endsWith('/sources/mikan/4058')).length,1)
    await page.getByRole('button',{name:'选择资源',exact:true}).click()
    await page.getByRole('button',{name:/Kirara Fantasia/}).waitFor()
    await page.keyboard.press('Escape')
    await page.getByRole('button',{name:'取消',exact:true}).click()
    await page.locator('dialog[open]').waitFor({state:'detached'})
    assert.equal(await card.evaluate(el=>el===window.__catalogCard),true)
    assert.equal(s.count(),1)
    passed.push('single source read, shared modal and retained catalog card/cache')

    await s.search('test')
    await page.locator('[data-mikan-id="8000"]').waitFor()
    await page.getByRole('button',{name:'搜索下一页'}).click()
    await page.locator('[data-mikan-id="8020"]').waitFor()
    assert.equal(s.count(),2)
    await s.navigate('/users');await s.navigate('/anime')
    await page.locator('[data-mikan-id="8020"]').waitFor()
    assert.equal(new URL(page.url()).searchParams.get('page'),'2')
    state.fail=true
    await page.getByRole('button',{name:'刷新当前站点'}).click()
    await page.getByText('模拟蜜柑读取失败，已保留上次结果。',{exact:true}).waitFor()
    assert.equal(await page.locator('[data-mikan-id="8020"]').count(),1)
    passed.push('local pagination, cross-page return and retained results on error')

    const delayed=await session()
    await delayed.go()
    delayed.state.slowEntry='4058'
    await delayed.page.locator('[data-mikan-id="4058"]').click()
    await delayed.page.getByText('正在读取番剧与订阅来源…',{exact:true}).waitFor()
    assert.equal(await delayed.page.locator('.mikan-anime-card').count(),20,'host stays mounted during loading')
    await delayed.page.goBack()
    await delayed.page.locator('dialog[open]').waitFor({state:'detached'})
    delayed.state.entryRelease()
    await delayed.page.waitForTimeout(80)
    assert.equal(new URL(delayed.page.url()).pathname,'/anime')
    passed.push('loading modal, immediate back and late metadata response fenced')

    for(const provider of ['anibt','garden']){
      const native=await session()
      await native.go('/anime?source='+provider+'&day=1')
      const selector=provider==='anibt'?'.anibt-anime-card':'.garden-anime-card'
      await native.page.locator(selector).first().waitFor()
      assert.equal(native.count(),0)
      assert.equal(native.calls.some(c=>c.endpoint.includes('/sources/')||c.endpoint.includes('/discovery/')),false)
      await native.page.locator(selector).first().click()
      await native.page.locator('.ac-identity').waitFor()
      assert.equal(new URL(native.page.url()).searchParams.get('bangumiId'),'630160')
      await native.page.keyboard.press('Escape')
      await native.page.locator('dialog[open]').waitFor({state:'detached'})
      assert.equal(new URL(native.page.url()).searchParams.get('source'),provider)
    }
    passed.push('native AniBT/Garden cards use same configuration modal and preserve provider')

    const cross=await session()
    cross.state.statusEnabled=true;cross.state.links=['630160']
    await cross.go()
    await cross.page.locator('[data-mikan-id="4058"]').click()
    await cross.page.locator('.ac-identity').waitFor()
    await cross.page.getByRole('button',{name:'选择资源',exact:true}).click()
    await cross.page.getByRole('button',{name:/Kirara Fantasia/}).click()
    await cross.page.getByRole('radio',{name:/^全部规格/}).check()
    await cross.page.getByRole('button',{name:'使用此来源',exact:true}).click()
    await cross.page.getByRole('button',{name:'保存',exact:true}).click()
    await cross.page.locator('dialog[open]').waitFor({state:'detached'})
    await cross.page.locator('[data-mikan-id="4058"] .catalog-subscription-badge').waitFor()
    const upstream=cross.calls.filter(c=>c.endpoint.includes('/sources/')||c.endpoint.includes('/discovery/')).length
    for(const provider of ['mikan','anibt','garden']){
      await cross.navigate('/anime?source='+provider+'&day=1')
      const selector=provider==='mikan'?'[data-mikan-id="4058"]':'[data-'+provider+'-id="630160"]'
      await cross.page.locator(selector+' .catalog-subscription-badge').waitFor()
      await cross.page.locator(selector).click()
      await cross.page.locator('.ac-identity').waitFor()
      assert.equal(new URL(cross.page.url()).pathname,'/anime/subscriptions/cross-site-sub')
      await cross.page.keyboard.press('Escape')
      await cross.page.locator('dialog[open]').waitFor({state:'detached'})
      assert.equal(new URL(cross.page.url()).searchParams.get('source'),provider)
    }
    assert.equal(cross.calls.filter(c=>c.endpoint.includes('/sources/')||c.endpoint.includes('/discovery/')).length,upstream,JSON.stringify(cross.calls.filter(c=>c.endpoint.includes('/sources/')||c.endpoint.includes('/discovery/'))))
    assert.equal(cross.calls.filter(c=>c.endpoint.endsWith('/subscriptions')&&c.body?.config).length,1)
    assert.equal(cross.calls.filter(c=>c.endpoint.endsWith('/subscriptions/preview')).length,0)
    passed.push('save without preview updates all three providers and opens existing subscription without upstream search')

    const quarters=await session()
    quarters.state.duplicate=true
    await quarters.go('/anime?source=mikan&season=2024-01')
    await quarters.page.getByRole('heading',{name:'2024 冬季收录51 部'}).waitFor()
    assert.equal(await quarters.page.locator('.mikan-anime-card').count(),20)
    assert.equal(await quarters.page.locator('.catalog-day-overview .catalog-day-number').textContent(),'51','overview count must deduplicate across weekdays')
    assert.equal(await quarters.page.locator('.catalog-day-today').count(),0,'historical quarters must not show today')
    assert.equal(await quarters.page.getByRole('button',{name:'上一季',exact:true}).getAttribute('title'),'切换到 2012 春季','previous quarter must follow verified archive gaps')
    assert.equal(await quarters.page.getByRole('button',{name:'下一季',exact:true}).getAttribute('title'),'切换到 2026 秋季')
    await quarters.page.screenshot({path:'test-results/anime-quarter-desktop.png'})
    await quarters.page.getByRole('combobox',{name:'蜜柑季度'}).click()
    assert.equal(await quarters.page.getByRole('option').count(),3)
    assert.equal(await quarters.page.getByRole('option',{name:'2012 春季'}).count(),1)
    assert.equal(await quarters.page.getByRole('option',{name:'2012 冬季'}).count(),0,'do not invent archive gaps')
    await quarters.page.keyboard.press('Escape')
    await quarters.page.getByRole('button',{name:'季度下一页'}).click()
    await quarters.page.locator('[data-mikan-id="4159"]').waitFor()
    assert.equal(quarters.count(),1)
    await quarters.navigate('/users');await quarters.navigate('/anime')
    await quarters.page.locator('[data-mikan-id="4159"]').waitFor()
    assert.equal(new URL(quarters.page.url()).searchParams.get('page'),'2')
    assert.equal(quarters.count(),1)
    await quarters.page.getByRole('button',{name:'下一季',exact:true}).click()
    await quarters.page.getByRole('heading',{name:'2026 秋季收录51 部'}).waitFor()
    assert.equal(await quarters.page.getByRole('button',{name:'下一季',exact:true}).isDisabled(),true,'latest verified quarter disables forward navigation')
    assert.equal(await quarters.page.locator('.catalog-current-season').textContent(),'当季')
    await quarters.page.locator('.catalog-controls').screenshot({path:'test-results/anime-quarter-controls-desktop.png'})
    await quarters.page.locator('.catalog-days').screenshot({path:'test-results/anime-days-desktop.png'})
    assert.equal(new URL(quarters.page.url()).searchParams.get('day'),'all')
    assert.equal(new URL(quarters.page.url()).searchParams.has('page'),false)
    await quarters.page.getByRole('button',{name:/^周一/}).click()
    assert.equal(quarters.count(),2,'weekday filtering must use the season response')
    await quarters.search('independent')
    const searchCall=quarters.calls.filter(c=>c.endpoint.endsWith('/catalog/mikan')).at(-1)
    assert.equal(searchCall.body.mode,'search');assert.equal('season' in searchCall.body,false)
    passed.push('verified quarter index, archive gaps, unique overview, local pagination and saved return state')

    const extra=await session()
    await extra.go('/anime?source=garden&season=2024-01')
    await extra.page.getByRole('heading',{name:'2024 冬季收录8 部'}).waitFor()
    await extra.page.getByRole('button',{name:'网络动画',exact:true}).click()
    assert.equal(await extra.page.locator('.garden-anime-card').count(),2)
    assert.equal(extra.calls.filter(c=>c.endpoint.endsWith('/catalog/garden')).length,1)
    passed.push('Garden extra categories included in overview with cross-category deduplication')

    const failures=await session()
    failures.state.failSeasons=true
    await failures.go('/anime?source=mikan&season=2024-01')
    await failures.page.locator('[data-mikan-id="4058"]').waitFor()
    await failures.page.getByRole('button',{name:'重试目录'}).waitFor()
    failures.state.failSeasons=false
    await failures.page.getByRole('button',{name:'重试目录'}).click()
    await failures.page.getByRole('button',{name:'重试目录'}).waitFor({state:'detached'})
    failures.state.fail=true
    await failures.navigate('/anime?source=mikan&season=2012-04')
    await failures.page.getByText('模拟蜜柑读取失败',{exact:true}).waitFor()
    assert.equal(await failures.page.locator('.mikan-anime-card').count(),0)
    assert.equal(new URL(failures.page.url()).searchParams.get('season'),'2012-04')
    failures.state.fail=false;failures.state.empty=true
    await failures.page.getByRole('button',{name:'重试',exact:true}).click()
    await failures.page.getByRole('heading',{name:'当前分类暂无条目'}).waitFor()
    assert.equal(new URL(failures.page.url()).searchParams.get('season'),'2012-04')
    failures.state.empty=false;failures.state.mismatch=true
    await failures.navigate('/anime?source=mikan&season=2023-01')
    await failures.page.getByText('返回的季度与请求不符，请重试',{exact:true}).waitFor()
    assert.equal(await failures.page.locator('.mikan-anime-card').count(),0)
    await failures.page.getByRole('combobox',{name:'蜜柑季度'}).click()
    assert.equal(await failures.page.getByRole('option',{name:'2023 冬季'}).count(),0)
    await failures.page.keyboard.press('Escape')
    passed.push('archive index failure independent of cards, empty/error seasons retained and mismatched responses rejected')

    const race=await session()
    race.state.slowSeason='2024-01'
    await race.page.goto(origin+'/anime?source=mikan&season=2024-01',{waitUntil:'domcontentloaded'})
    await race.page.waitForFunction(()=>document.querySelector('.anime-grid[aria-busy="true"]'))
    assert.ok(await race.page.locator('.catalog-day-placeholder').count()>0,'unknown counts must use placeholders instead of zero')
    await race.navigate('/anime?source=garden&season=2024-01')
    await race.page.locator('.garden-anime-card').first().waitFor()
    race.state.seasonRelease()
    await race.page.waitForTimeout(80)
    assert.equal(await race.page.locator('.mikan-anime-card').count(),0)
    passed.push('late quarter response cannot overwrite a different provider')

    const dates=await session({width:1440,height:1000},'admin',{timezoneId:'America/Los_Angeles'})
    await dates.page.clock.install({time:new Date('2026-12-31T23:59:58+08:00')})
    await dates.page.clock.pauseAt(new Date('2026-12-31T23:59:58+08:00'))
    await dates.go('/anime?source=mikan&season=2026-10&day=6')
    assert.deepEqual(await dates.page.locator('.catalog-day-card[data-date]').evaluateAll(els=>els.map(el=>el.dataset.date)),['2026-12-31','2027-01-01','2027-01-02','2027-01-03','2027-01-04','2027-01-05','2027-01-06'])
    assert.equal(await dates.page.locator('.catalog-day-card').first().getAttribute('data-date'),'2026-12-31','today stays first even when another day is selected')
    assert.equal(await dates.page.locator('.catalog-day-card').first().locator('.catalog-day-number').textContent(),'31','large number is date, not collection count')
    await dates.page.clock.runFor(2200)
    assert.equal(await dates.page.locator('.catalog-day-card[data-date], .catalog-day-today').count(),0,'previous quarter must stop displaying current dates at Shanghai midnight')
    assert.equal(dates.count(),1,'local date rollover must not reload an explicitly selected quarter')
    await dates.navigate('/anime?source=mikan&season=2027-01&day=6')
    await dates.page.clock.runFor(100)
    assert.equal(await dates.page.locator('.catalog-day-card').first().getAttribute('data-date'),'2027-01-01')
    assert.equal(await dates.page.locator('.catalog-day-card[aria-pressed="true"]').getAttribute('aria-label'),'周六','date rollover must preserve an explicitly selected weekday')
    passed.push('Shanghai dates, today-first order, full day numbers and year/quarter rollover independent of browser timezone')

    const stable=await session({width:1440,height:900})
    stable.state.emptyDays=[3]
    await stable.go()
    await stable.page.evaluate(()=>window.scrollTo(0,180))
    await stable.navigate('/anime?source=mikan&season=2026-10&day=5')
    await stable.page.waitForFunction(()=>window.scrollY===0)
    const controlsTop=await stable.page.locator('.catalog-controls').evaluate(el=>el.getBoundingClientRect().top)
    await stable.page.getByRole('button',{name:/^周一/}).click()
    await stable.page.waitForTimeout(350)
    assert.equal(await stable.page.evaluate(()=>window.scrollY),0,'filter click must not restore that weekday\'s previous scroll position')
    assert.equal(await stable.page.locator('.catalog-controls').evaluate(el=>el.getBoundingClientRect().top),controlsTop,'upper controls remain in the same viewport position')
    await stable.page.evaluate(()=>window.scrollTo(0,120))
    await stable.page.getByRole('button',{name:/^周三/}).click()
    await stable.page.getByRole('heading',{name:'当前分类暂无条目'}).waitFor()
    assert.ok(Math.abs(await stable.page.evaluate(()=>window.scrollY)-120)<1,'empty filtered results must not clamp the viewport')
    assert.equal(stable.count(),1,'stable filtering still reuses the loaded season')
    await stable.navigate('/users');await stable.navigate('/anime')
    await stable.page.waitForFunction(()=>Math.abs(window.scrollY-120)<1)
    for (const provider of ['AniBT','Anime Garden']) {
      await stable.page.getByRole('button',{name:provider,exact:true}).click()
      await stable.page.locator('.catalog-count').waitFor()
      await stable.page.waitForTimeout(100)
      assert.ok(Math.abs(await stable.page.evaluate(()=>window.scrollY)-120)<1,'provider switch must preserve the current viewport too')
    }
    await stable.page.goBack()
    await stable.page.waitForFunction(()=>Math.abs(window.scrollY-120)<1)
    passed.push('in-page date/provider filtering keeps header/scroll stable with old caches and empty results; external return and browser back restore progress')

    const posters=await session()
    posters.state.cardCovers=true;posters.state.statusEnabled=true;posters.state.saved={id:'poster-example',status:'enabled'};posters.state.multiple=true
    await posters.go('/anime?source=anibt&season=2026-10&day=all')
    const poster=posters.page.locator('.anibt-anime-card').first()
    assert.equal(await poster.locator('.poster-card-sticker').count(),1)
    assert.equal(await poster.locator('button,a').count(),0,'the card remains a single link without nested controls')
    assert.equal(await poster.locator('img').getAttribute('loading'),'eager')
    const dimensions=await poster.locator('.poster-card-cover').evaluate(el=>({w:el.clientWidth,h:el.clientHeight}))
    assert.ok(Math.abs(dimensions.h/dimensions.w-1.5)<.02,'sleeve keeps a 2:3 cover')
    const scrollBefore=await posters.page.evaluate(()=>window.scrollY)
    const imageCallsBefore=posters.calls.filter(c=>c.endpoint.includes('/image/')).length
    await poster.hover()
    await posters.page.waitForTimeout(800)
    const hoverStyle=await poster.evaluate(el=>{const c=getComputedStyle(el),m=new DOMMatrixReadOnly(c.transform),shine=getComputedStyle(el.querySelector('.poster-card-cover'),'::after');return {angle:Math.atan2(m.b,m.a)*180/Math.PI,y:m.f,shineX:new DOMMatrixReadOnly(shine.transform).m41,w:el.querySelector('.poster-card-cover').clientWidth,running:el.getAnimations({subtree:true}).filter(a=>a.playState==='running').length}})
    assert.ok(Math.abs(hoverStyle.angle+.6)<.05 && Math.abs(hoverStyle.y+4)<.05,'hover lifts and gently tilts the whole sleeve')
    assert.ok(hoverStyle.shineX>hoverStyle.w,'one sweep finishes beyond the cover')
    assert.equal(hoverStyle.running,0,'hover has no continuous animation after the sweep')
    assert.equal(await posters.page.evaluate(()=>window.scrollY),scrollBefore)
    assert.equal(posters.calls.filter(c=>c.endpoint.includes('/image/')).length,imageCallsBefore,'hover must not refetch a cover')
    await posters.page.mouse.move(0,0)
    await posters.page.waitForTimeout(350)
    assert.equal(await poster.evaluate(el=>getComputedStyle(el).transform),'none')
    await posters.page.locator('.anime-grid').screenshot({path:'test-results/anime-poster-grid-desktop.png'})
    await posters.page.setViewportSize({width:320,height:760})
    await posters.page.waitForTimeout(100)
    const overlay=await poster.evaluate(el=>{const time=el.querySelector('.poster-card-sticker').getBoundingClientRect(),format=el.querySelector('.poster-card-format').getBoundingClientRect(),badge=el.querySelector('.catalog-subscription-badge').getBoundingClientRect(),cover=el.querySelector('.poster-card-cover').getBoundingClientRect();return {overlap:time.bottom>badge.top || format.right>badge.left,inside:badge.right<=cover.right && format.left>=cover.left,columns:getComputedStyle(el.parentElement).gridTemplateColumns.split(' ').length}})
    assert.equal(overlay.overlap,false,'narrow card must separate its time sticker, format and subscription badge')
    assert.equal(overlay.inside,true)
    assert.equal(overlay.columns,2,'320px keeps two readable columns')
    assert.equal(await posters.page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
    await posters.page.locator('.anime-grid').screenshot({path:'test-results/anime-poster-grid-mobile.png'})
    await posters.page.evaluate(()=>document.documentElement.classList.add('dark'))
    await posters.page.locator('.anime-grid').screenshot({path:'test-results/anime-poster-grid-mobile-dark.png'})
    await posters.page.evaluate(()=>document.documentElement.classList.remove('dark'))
    await posters.page.setViewportSize({width:1440,height:1000})
    await posters.page.emulateMedia({reducedMotion:'reduce'})
    await poster.hover()
    assert.equal(await poster.evaluate(el=>getComputedStyle(el).transform),'none')
    assert.equal(await poster.evaluate(el=>getComputedStyle(el.querySelector('.poster-card-cover'),'::after').transitionDuration),'0s')
    posters.state.saved=undefined
    await posters.navigate('/anime?source=mikan&season=2026-10&day=1')
    const heights=await posters.page.locator('.anime-poster-card').evaluateAll(els=>els.slice(0,6).map(el=>el.getBoundingClientRect().height))
    assert.ok(Math.max(...heights)-Math.min(...heights)<1,'long and short titles keep equal card heights')
    await posters.page.locator('.mikan-anime-card').first().focus()
    await posters.page.keyboard.press('Enter')
    await posters.page.locator('.ac-identity').waitFor()
    await posters.page.keyboard.press('Escape')
    await posters.page.locator('dialog[open]').waitFor({state:'detached'})
    posters.state.failImages=true
    await posters.navigate('/anime?source=garden&season=2026-10&day=1')
    await posters.page.waitForFunction(()=>document.querySelector('.garden-anime-card')&&!document.querySelector('.garden-anime-card img'))
    assert.equal(await posters.page.locator('.garden-anime-card .poster-card-placeholder').count(),1,'failed image keeps the same card and dotted placeholder')
    passed.push('reusable poster sleeves: native fields, fixed geometry, keyboard entry, failed cover, finite hover sweep and reduced motion without extra image requests')

    const mobile=await session({width:320,height:760},'admin',{hasTouch:true,isMobile:true})
    await mobile.go()
    assert.equal(await mobile.page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
    await mobile.page.locator('.catalog-controls').screenshot({path:'test-results/anime-quarter-controls-mobile.png'})
    const rowHeight=await mobile.page.locator('.catalog-days').evaluate(el=>el.getBoundingClientRect().height)
    await mobile.page.getByRole('button',{name:'OVA',exact:true}).focus()
    await mobile.page.keyboard.press('Space')
    assert.equal(mobile.count(),1,'keyboard filtering must not reload the source')
    assert.equal(await mobile.page.locator('.catalog-days').evaluate(el=>{const b=el.querySelector('[aria-pressed="true"]').getBoundingClientRect(),r=el.getBoundingClientRect();return b.left>=r.left&&b.right<=r.right}),true,'selected category stays visible')
    await mobile.page.locator('.catalog-days').screenshot({path:'test-results/anime-days-mobile.png'})
    await mobile.page.evaluate(()=>document.documentElement.classList.add('dark'))
    await mobile.page.locator('.catalog-controls').screenshot({path:'test-results/anime-quarter-controls-mobile-dark.png'})
    await mobile.page.locator('.catalog-days').screenshot({path:'test-results/anime-days-mobile-dark.png'})
    await mobile.page.evaluate(()=>document.documentElement.classList.remove('dark'))
    await mobile.page.getByRole('combobox',{name:'蜜柑季度'}).click()
    await mobile.page.getByRole('option',{name:'2024 冬季'}).tap()
    await mobile.page.getByRole('heading',{name:'2024 冬季收录52 部'}).waitFor()
    assert.equal(await mobile.page.locator('.catalog-days').evaluate(el=>el.getBoundingClientRect().height),rowHeight,'loaded counts and selection must keep a stable row height')
    assert.equal(await mobile.page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
    await mobile.page.screenshot({path:'test-results/anime-quarter-mobile.png'})
    await mobile.page.evaluate(()=>document.documentElement.classList.add('dark'))
    await mobile.page.screenshot({path:'test-results/anime-quarter-mobile-dark.png'})
    await mobile.page.locator('[data-mikan-id="4058"]').tap()
    await mobile.page.locator('.ac-identity').waitFor()
    assert.equal(await mobile.page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
    await mobile.page.evaluate(()=>document.documentElement.classList.add('dark'))
    await mobile.page.screenshot({path:'test-results/anime-catalog-config-mobile-dark.png'})
    await mobile.page.getByRole('button',{name:'关闭订阅配置',exact:true}).tap()
    assert.equal(await mobile.page.evaluate(()=>document.body.style.overflow),'')
    passed.push('320px touch dialog and dark theme with no page overflow')
    assert.deepEqual(errors,[])
    console.log(JSON.stringify({passed,errors},null,2))
  }catch(e){
    console.error(e)
    for(let i=0;i<contexts.length;i++)await contexts[i].pages()[0]?.screenshot({path:'test-results/anime-catalog-failure-'+i+'.png',fullPage:true}).catch(()=>{})
    process.exitCode=1
  }finally{await browser.close()}
})()
