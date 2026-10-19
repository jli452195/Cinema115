// All APIs and posters are mocked; no subscriptions or external downloads.
const assert = require('node:assert/strict')
const fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const contexts = [], errors = [], passed = []
  await fs.mkdir('test-results', { recursive: true })
  async function session(viewport = { width: 1440, height: 1000 }, role = 'admin') {
    const context = await browser.newContext({ viewport, serviceWorkers: 'block' }); contexts.push(context)
    await context.addInitScript(() => { localStorage.setItem('admin_token','mock-token'); localStorage.setItem('cinema115-theme','light') })
    const page = await context.newPage(); page.setDefaultTimeout(10000); page.on('pageerror', error => errors.push(error.message))
    const calls = [], state = { fail: false, links: ['624085'], empty: false }
    const item = id => ({ id: String(id), title: id === 4058 ? '乱世千金倪亚·利斯顿转生为娇弱千金的弑神武人华丽无双录' : `蜜柑番剧 ${id}`, coverUrl: `/api/mikan/image/202610/${id}.jpg`, updatedDate: '2026-10-16' })
    await page.route('**/api/**', async route => {
      const req = route.request(), endpoint = new URL(req.url()).pathname, body = req.postDataJSON(); calls.push({ endpoint, body })
      let data = {}, status = 200
      if (endpoint === '/api/auth/me') data = { user: { id:'mock',username:'admin',role,disabled:false } }
      else if (endpoint.startsWith('/api/mikan/image/')) {
        await route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="240" height="336"><defs><linearGradient id="g" x2="1" y2="1"><stop stop-color="#405d85"/><stop offset="1" stop-color="#98b6ad"/></linearGradient></defs><path fill="url(#g)" d="M0 0h240v336H0z"/><circle cx="170" cy="120" r="80" fill="white" opacity=".18"/></svg>'}); return
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
      } else if (/\/sources\/mikan\/\d+$/.test(endpoint)) data = { entry:{id:endpoint.split('/').pop(),title:'蜜柑原站番名',groups:[{id:'10',name:'Kirara Fantasia'},{id:'20',name:'LoliHouse'},{id:'30',name:'三明治摆烂组'},{id:'40',name:'ANi'}],bangumiIds:state.links} }
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
    assert.equal(await page.getByRole('button',{name:'Anime Garden 待接入',exact:true}).isDisabled(),true)
    assert.equal(calls.some(c=>c.endpoint.includes('/discovery/')||c.endpoint.includes('/sources/')),false)
    await page.getByRole('button',{name:'周日',exact:true}).click(); await page.getByRole('button',{name:'剧场版',exact:true}).click(); await page.getByRole('button',{name:'OVA',exact:true}).click(); await page.getByRole('button',{name:/^周一/}).click()
    assert.equal(s.count(),1,'categories use one calendar request')
    await page.screenshot({path:'test-results/anime-catalog-desktop-light.png',fullPage:true,animations:'disabled'})
    passed.push('独立蜜柑入口与真实待接入状态，整季一次请求，星期/剧场版/OVA本地切换，无逐卡详情')
    await page.locator('[data-mikan-id="4058"]').click()
    const dialog=page.getByRole('dialog'); await dialog.getByRole('link',{name:'选择字幕组并配置订阅',exact:true}).waitFor()
    let href=await dialog.getByRole('link',{name:'选择字幕组并配置订阅',exact:true}).getAttribute('href')
    assert.ok(!href.includes('itemId=')&&!href.includes('source=')&&href.includes('bangumiId=624085'),'unique explicit source link is associated by default')
    await dialog.locator('summary').click(); await dialog.getByLabel('暂不关联',{exact:true}).check()
    href=await dialog.getByRole('link',{name:'选择字幕组并配置订阅',exact:true}).getAttribute('href'); assert.equal(href.includes('bangumiId='),false)
    await dialog.getByRole('button',{name:'手动填写其他篇章',exact:true}).click(); await dialog.getByLabel('Bangumi 篇章 ID',{exact:true}).fill('624085')
    await page.screenshot({path:'test-results/anime-catalog-dialog-desktop.png',animations:'disabled'})
    await dialog.getByRole('link',{name:'选择字幕组并配置订阅',exact:true}).click(); await page.locator('.ae-chapter').waitFor()
    await page.getByRole('button',{name:'选择资源',exact:true}).click(); await page.getByRole('button',{name:/Kirara Fantasia/}).waitFor()
    assert.equal(calls.filter(c=>c.endpoint.endsWith('/sources/mikan/search')).length,0,'clicked item goes directly to its own groups')
    await page.keyboard.press('Escape'); await page.locator('.ae-chapter').getByRole('link',{name:'返回选番'}).click(); await page.getByRole('dialog').waitFor(); await page.keyboard.press('Escape')
    assert.equal(s.count(),1)
    passed.push('原站唯一 Bangumi 默认关联，可取消/修改，条目直接带入本站字幕组，返回保留日历和缓存')
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
    state.links=['1','2']; await page.locator('[data-mikan-id="4058"]').click(); await page.getByRole('dialog').getByRole('link',{name:'选择字幕组并配置订阅'}).waitFor()
    assert.equal((await page.getByRole('dialog').getByRole('link',{name:'选择字幕组并配置订阅'}).getAttribute('href')).includes('bangumiId='),false)
    await page.keyboard.press('Escape'); state.links=[]; await page.locator('[data-mikan-id="4058"]').click(); await page.getByRole('dialog').getByRole('link',{name:'选择字幕组并配置订阅'}).click(); await page.getByRole('heading',{name:'蜜柑原站番名',exact:true}).waitFor()
    passed.push('多个原站篇章不猜测，无 Bangumi 可进入配置')
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
    await mobile.page.locator('[data-mikan-id="4058"]').click(); await mobile.page.getByRole('dialog').getByRole('link',{name:'选择字幕组并配置订阅'}).waitFor()
    assert.equal(await mobile.page.getByRole('dialog').locator('.anime-floating-body').evaluate(e=>e.scrollWidth>e.clientWidth),false)
    await mobile.page.screenshot({path:'test-results/anime-catalog-dialog-mobile.png',animations:'disabled'})
    await mobile.page.keyboard.press('Escape'); await mobile.page.setViewportSize({width:320,height:760}); assert.equal(await mobile.page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
    await mobile.page.locator('.catalog-providers').screenshot({path:'test-results/anime-provider-buttons-mobile-320.png',animations:'disabled'})
    await mobile.navigate('/anime?source=garden&q=test'); await mobile.page.getByText('该站点暂未接入',{exact:true}).waitFor(); assert.equal(mobile.count(),1,'unsupported site must never fall back to mikan'); await mobile.page.getByRole('button',{name:'蜜柑',exact:true}).click(); await mobile.page.locator('[data-mikan-id="4058"]').waitFor()
    passed.push('390/320手机布局与暗色，无横向溢出，未接入站点直达不回退蜜柑')
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
    const ordinary=await session(undefined,'user'); await ordinary.go(); assert.equal(ordinary.calls.some(c=>c.endpoint.includes('/admin/anime')),false)
    assert.deepEqual(errors,[]); console.log(JSON.stringify({passed,errors},null,2))
  } finally { await Promise.allSettled(contexts.map(c=>c.close())); await browser.close() }
})().catch(e=>{console.error(e);process.exitCode=1})
