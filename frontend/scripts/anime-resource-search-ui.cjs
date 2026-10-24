// All APIs are mocked. No real subscriptions, 115 tasks or external resources.
const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const errors = [], passed = []
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light') })
    const page = await context.newPage(); page.setDefaultTimeout(5000)
    page.on('pageerror', e => errors.push(e.message))
    const calls = [], state = { fail: false, hold: false, release: undefined, role: 'admin', large: false }
    const groups = [{ id:'dmhy_f776',name:'测试组',origin:'dmhy',originName:'动漫花园',kind:'fansub' },{ id:'moe_p2',name:'测试组',origin:'moe',originName:'萌番组',kind:'publisher' }]
    const item = (id, provider = 'dmhy') => ({ id, provider, providerId:String(id),title:`[测试组] 百变之星 / Kaleido Star - ${id} [${id % 2 ? '1080p' : '720p'}][简繁字幕]`,size:1024**3,createdAt:'2026-10-23T00:00:00Z',href:provider==='dmhy'?`https://share.dmhy.org/topics/view/${id}`:`https://bangumi.moe/torrent/${id}`,magnet:`magnet:?xt=urn:btih:${id.toString(16).padStart(40,'0')}`,publisher:{id:2,name:'上传者'},...(provider==='dmhy'?{fansub:{id:776,name:'测试组'}}:{}) })
    await page.route('**/api/**', async route => {
      const request = route.request(), endpoint = new URL(request.url()).pathname, body = request.postDataJSON()
      calls.push({endpoint,body})
      let status = 200, data = {}
      if(endpoint==='/api/auth/me') data={user:{id:'mock',username:'admin',role:state.role,disabled:false}}
      else if(endpoint.includes('/tmdb/detail/')) data={id:12501,kind:'tv',title:'百变之星',originalTitle:'カレイドスター',overview:'简介',posterUrl:'',backdropUrl:'',date:'2003-04-03',rating:7,votes:1,genres:[{id:16,name:'动画'}],countries:['JP'],cast:[],seasons:[],collection:null}
      else if(endpoint.endsWith('/resources/garden/search')) {
        if(state.hold){state.hold=false;await new Promise(resolve=>{state.release=resolve})}
        if(state.fail){status=502;data={error:'Garden 暂时无法读取'}}
        else data={query:body.query,page:body.page,hasMore:body.page===1&&!body.group,groups,items:body.page>1?[item(101)]:body.group?[body.group==='moe_p2'?item(2,'moe'):item(1)]:state.large?Array.from({length:100},(_,i)=>({...item(i+1),...(i===2?{title:'[测试组] 百变之星 [1440x1080]'}:{})})):[item(1),item(2,'moe')],cached:false,fetchedAt:new Date().toISOString(),expiresAt:new Date(Date.now()+60000).toISOString()}
      } else if(endpoint.endsWith('/mikan/search')) data={query:body.query,groups:[],bangumis:[],attempts:[],items:[{id:'mikan',title:'蜜柑资源',size:'1 GB',updatedAt:'2026-10-23',magnet:item(8).magnet,url:'https://mikanani.me/',torrentUrl:'',resolution:'1080P'}]}
      else if(endpoint.endsWith('/cloud-download/config')) data={accountId:'primary',targetCID:'123',targetPath:'/下载'}
      else if(endpoint.endsWith('/cloud-download/tasks')) data={accepted:true}
      else if(endpoint.endsWith('/cloud-download/tasks/batch')) {
        const lines = [{type:'ready',total:body.urls.length},...body.urls.flatMap((_,index)=>[{type:'start',index,total:body.urls.length},{type:'result',index,total:body.urls.length,accepted:true}]),{type:'done',total:body.urls.length,stopped:false}]
        return route.fulfill({contentType:'application/x-ndjson',body:lines.map(v=>JSON.stringify(v)).join('\n')+'\n'})
      } else if(endpoint.includes('/notifications')) data={items:[],unread:0}
      else if(endpoint.includes('/emby/media/')) data={media:{state:'unknown',items:[]},baseUrl:'',serverId:''}
      else if(endpoint.includes('/recommendations')) data={items:[],page:1,totalPages:1}
      else if(endpoint.includes('/follows/')) data={subscribed:false,libraryState:'unknown'}
      if(endpoint.endsWith('/resources/garden/search') && data.items) data.specs=Object.fromEntries(data.items.map(resource=>[`${resource.provider}:${resource.providerId}`,{resolution:resource.title.includes('1440x1080')?'1080p':resource.id%2?'1080p':'720p',languages:['zh-Hans','zh-Hant']}]))
      if(endpoint.endsWith('/mikan/search')) data.specs={mikan:{resolution:'1080p'}}
      await route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)}).catch(()=>{})
    })
    const count = () => calls.filter(c=>c.endpoint.endsWith('/resources/garden/search')).length
    const rows = page.getByRole('list',{name:'Garden资源列表'}).locator('> li')
    await page.goto(origin+'/tv/12501',{waitUntil:'networkidle'})
    assert.equal(count(),0)
    state.fail=true
    await page.getByRole('button',{name:'Garden 搜索',exact:true}).click()
    await page.getByText('搜索暂时失败',{exact:true}).waitFor()
    assert.equal(await page.getByRole('textbox',{name:'Garden搜索词'}).getAttribute('readonly'),null)
    state.fail=false
    await page.getByRole('button',{name:'重新搜索',exact:true}).click()
    await rows.first().waitFor();assert.equal(await rows.count(),2);assert.equal(count(),2)
    assert.equal(await page.getByRole('navigation',{name:'Garden 来源站'}).count(),0)
    assert.equal(await page.locator('.mikan-item-picker').count(),0)
    assert.equal(calls.some(c=>/\/sources\/garden|\/api\/garden\/image|\/bgm\//.test(c.endpoint)),false)
    assert.equal(await page.getByRole('link',{name:'下载种子'}).count(),0)
    await page.getByRole('textbox',{name:'筛选Garden结果'}).fill('01 nonexistent')
    assert.equal(await rows.count(),0);assert.equal(count(),2)
    await page.getByRole('textbox',{name:'筛选Garden结果'}).fill('')
    await page.getByRole('navigation',{name:'Garden分辨率'}).getByRole('button',{name:'1080P',exact:true}).click()
    assert.equal(await rows.count(),1);assert.equal(count(),2)
    await page.getByRole('navigation',{name:'Garden分辨率'}).getByRole('button',{name:'全部分辨率'}).click()
    await page.getByRole('button',{name:'读取更多搜索结果'}).click();await rows.nth(2).waitFor()
    assert.equal(calls.filter(c=>c.endpoint.endsWith('/resources/garden/search')).at(-1).body.page,2)
    passed.push('initial failure/retry, lazy resource-only search, no covers/subject requirements, local filters and on-demand append')
    state.fail=true
    await page.getByRole('navigation',{name:'Garden发布方'}).getByRole('button',{name:'测试组 · 萌番组 · 发布者',exact:true}).click()
    await page.getByText('更新失败，已保留原结果',{exact:true}).waitFor()
    assert.equal(await rows.count(),3)
    assert.equal(await page.getByRole('navigation',{name:'Garden发布方'}).getByRole('button',{name:'全部发布方'}).getAttribute('aria-pressed'),'true')
    state.fail=false
    await page.getByRole('navigation',{name:'Garden发布方'}).getByRole('button',{name:'测试组 · 动漫花园 · 字幕组',exact:true}).click()
    await page.waitForFunction(()=>document.querySelectorAll('[aria-label="Garden资源列表"] > li').length===1)
    const selected = calls.filter(c=>c.endpoint.endsWith('/resources/garden/search')).at(-1).body
    assert.equal(selected.group,'dmhy_f776');assert.equal(selected.groupName,'测试组')
    passed.push('aggregated search without source selector; distinct same-name publisher identities; failed filter retains previous rows')
    await page.getByRole('button',{name:'115 云下载',exact:true}).first().click()
    await page.getByText('已提交到 115 云下载：/下载。提交成功不代表下载已完成。',{exact:true}).waitFor()
    assert.equal(calls.filter(c=>c.endpoint.endsWith('/cloud-download/tasks')).length,1)
    await page.keyboard.press('Escape');await page.locator('dialog').waitFor({state:'detached'})
    assert.equal(await page.getByRole('button',{name:'Garden 搜索',exact:true}).evaluate(el=>el===document.activeElement),true)
    await page.getByRole('button',{name:'Mikan 搜索',exact:true}).click()
    await page.getByRole('list',{name:'蜜柑资源列表'}).waitFor()
    assert.equal(await page.getByRole('navigation',{name:'Garden 来源站'}).count(),0)
    await page.getByRole('button',{name:'115 云下载',exact:true}).first().click()
    await page.getByText('已提交到 115 云下载：/下载。提交成功不代表下载已完成。',{exact:true}).waitFor()
    assert.equal(calls.filter(c=>c.endpoint.endsWith('/cloud-download/tasks')).length,2)
    await page.keyboard.press('Escape')
    passed.push('single cloud download uses existing main-account config; Escape restores focus; Mikan uses independent API')
    await page.getByRole('button',{name:'Garden 搜索',exact:true}).click();await rows.nth(1).waitFor()
    await page.getByRole('button',{name:'批量选择',exact:true}).click()
    await page.getByLabel('全选当前筛选').check()
    await page.getByRole('button',{name:'提交到 115（2）',exact:true}).click()
    await page.getByText('已提交 2，失败 0，待核对 0。下载进度请到 115 查看。',{exact:true}).waitFor()
    assert.equal(calls.filter(c=>c.endpoint.endsWith('/cloud-download/tasks/batch')).length,1)
    await page.keyboard.press('Escape')
    passed.push('batch cloud download reuses bounded request/receipt workflow')
    state.large=true
    await page.getByRole('button',{name:'Garden 搜索',exact:true}).click();await rows.nth(49).waitFor()
    const beforeAppend=count()
    assert.equal(await rows.count(),50)
    assert.equal(await page.getByRole('button',{name:'读取更多搜索结果'}).count(),0)
    assert.equal(await rows.filter({hasText:'[1440x1080]'}).locator('.mikan-resolution').textContent(),'1080P')
    await page.getByRole('button',{name:'显示更多资源'}).click();await rows.nth(99).waitFor()
    assert.equal(count(),beforeAppend)
    state.fail=true
    await page.getByRole('button',{name:'读取更多搜索结果'}).click()
    await page.locator('.mikan-list-end').getByText('Garden 暂时无法读取',{exact:true}).waitFor()
    assert.equal(await rows.count(),100)
    state.fail=false
    await page.getByRole('button',{name:'读取更多搜索结果'}).click();await rows.nth(100).waitFor()
    assert.equal(await rows.count(),101)
    await page.keyboard.press('Escape');state.large=false
    passed.push('100-row local display before remote pagination, dimension labels and failed pagination retry')
    await page.setViewportSize({width:320,height:740})
    await page.getByRole('button',{name:'Garden 搜索',exact:true}).click();await rows.first().waitFor()
    await fs.mkdir('test-results',{recursive:true})
    await page.screenshot({path:'test-results/garden-search-mobile-light.png'})
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true)
    assert.equal(await page.locator('dialog').evaluate(el=>el.scrollWidth<=el.clientWidth),true)
    await page.evaluate(()=>document.documentElement.classList.add('dark'))
    await page.screenshot({path:'test-results/garden-search-mobile-dark.png'})
    await page.keyboard.press('Escape')
    state.hold=true
    await page.getByRole('button',{name:'Garden 搜索',exact:true}).click()
    await page.getByText('正在搜索 Garden…',{exact:true}).waitFor()
    await page.keyboard.press('Escape');state.release?.()
    await page.locator('dialog').waitFor({state:'detached'})
    passed.push('320px light/dark no overflow and close during pending search')
    assert.deepEqual(errors,[])
    console.log(JSON.stringify({passed,errors},null,2))
  } finally {await browser.close()}
})().catch(error=>{console.error(error);process.exitCode=1})
