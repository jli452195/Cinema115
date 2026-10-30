const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const context = await browser.newContext({ viewport: { width: 1280, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
  const page = await context.newPage(), errors = [], writes = [], modeWrites = [], checkWrites = []
  page.setDefaultTimeout(10000)
  page.on('pageerror', e => errors.push(e.message))
  const pool = [{id:'pool',name:'小号 A',uid:'501',primary:false,configured:true,revision:1,playbackParentCid:'123',pool:{state:'ready',mode:'enabled',sessions:3,active:2,preparing:0,residual:1,retryAt:0,checkedAt:Date.now()}}, {id:'duplicate',name:'同 UID 配置',uid:'501',primary:false,configured:true,revision:1,playbackParentCid:'456',pool:{state:'ready',mode:'enabled',sessions:3,active:2,preparing:0,residual:1,retryAt:0,checkedAt:Date.now()}}]
  let settings={maxSessions:3,maxAttempts:3,budgetSeconds:90,leaseMinutes:5,cooldownSeconds:30,uidIntervalMs:250,globalIntervalMs:50,notificationBotId:''}
  let reads=0, failRead=false, failSave=false, failMode=false, zeroSamples=false, checkState='invalid_directory'
  let config={userId:'',sourceAccountId:'main',accountId:'',targetCid:'',targetPath:''}
  await page.route('**/api/**', async route => {
   const req=route.request(), p=new URL(req.url()).pathname; let data={},status=200
   if (p==='/api/auth/me') data={user:{id:'admin',username:'admin',role:'admin'}}
   else if(p==='/api/admin/p115/accounts') data={accounts:[{id:'main',name:'主账号',primary:true,configured:true,revision:1},...pool]}
   else if(p.endsWith('/device-policy')) data={mode:'parallel',silenceSeconds:60}
   else if(p==='/api/admin/member-playback/accounts') data={accounts:pool}
   else if(p==='/api/admin/member-playback/pool') {
    if(req.method()==='POST'){writes.push(req.postDataJSON());if(failSave){status=422;data={error:'保存失败，设置未修改'}}else {settings=req.postDataJSON();data={settings}}}
    else {reads++;if(failRead){status=502;data={error:'模拟刷新失败'}}else data={settings,accounts:pool,metrics:{requests:zeroSamples?0:10,succeeded:zeroSamples?0:8,failed:zeroSamples?0:2,switches:1,p50Ms:100,p95Ms:900,samples:zeroSamples?0:10,peakUidSessions:2,resolved:8,copyResolutions:7,directLinkHits:5},api:{requests:20,throttled:1,rejected:0,waitMs:50,waiting:0,sourceRequests:3},events:[{id:1,accountId:'pool',state:'invalid_directory',at:Date.now()}]}}
   }
   else if(p==='/api/admin/telegram/bots') data={bots:[{id:'bot',name:'测试通知',configured:true,enabled:true,notificationChatID:'42'}]}
   else if(p==='/api/admin/member-playback/pool/pool'){modeWrites.push(req.postDataJSON());if(failMode){status=500;data={error:'模式保存失败'}}else {pool[0].pool.mode=req.postDataJSON().mode;data={accounts:pool}}}
   else if(p==='/api/admin/member-playback/pool/pool/check'){checkWrites.push(p);pool[0].pool.state=checkState;data={accounts:pool}}
   else if(p==='/api/admin/member-playback/settings') data={settings:{allowDownload:false,publicUrl:''}}
   else if(p==='/api/admin/emby/proxy') data={config:{enabled:false,debug:false,port:8097,url:'http://emby',accountId:'main',directLinkCacheMinutes:9},listening:false,error:''}
   else if(p==='/api/admin/emby/playback-cache/users') data={embyUrl:'http://emby',users:[{Id:'reader',Policy:{IsDisabled:false}},{Id:'personal',Policy:{IsDisabled:false}}],members:[{embyId:'reader',embyName:'读者',source:'assigned'},{embyId:'personal',embyName:'个人用户',source:'personal',personalDrive:{connected:true}}]}
   else if(p==='/api/admin/emby/instant-upload') {if(req.method()==='POST'){writes.push(req.postDataJSON());config={...config,userId:req.postDataJSON().userId}}data={config,records:[],running:false}}
   else if(p==='/api/admin/emby/instant-upload/run') {writes.push(req.postDataJSON());data={id:req.postDataJSON().id,accountId:'pool',targetCid:'123',status:'success',message:'模拟秒传成功',startedAt:new Date().toISOString(),durationMs:10}}
   await route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)})
  })
  await page.goto('http://127.0.0.1:5173/helper115')
  await page.getByText('仅单 UID 调度中',{exact:true}).waitFor()
  const overview=page.getByLabel('动态池运行概览'), card=page.locator('article').filter({has:page.getByRole('heading',{name:'小号 A',exact:true})})
  await overview.getByText('80.0%',{exact:true}).waitFor()
  assert.equal(await overview.getByText('1 个检查通过',{exact:true}).count(),1)
  assert.equal(await page.getByText('2 份配置共用此 UID 名额，容量不累加',{exact:true}).count(),2)
  await page.getByRole('button',{name:'调度设置',exact:true}).click()
  await page.getByLabel('每 UID 名额上限',{exact:true}).fill('2')
  const before=reads
  await page.waitForTimeout(10500)
  assert.ok(reads>before,'metrics were not refreshed')
  assert.equal(await page.getByLabel('每 UID 名额上限',{exact:true}).inputValue(),'2','polling overwrote unsaved settings')
  await page.getByLabel('全局 API 请求间隔（毫秒）',{exact:true}).fill('500')
  await page.getByRole('button',{name:'保存池设置',exact:true}).click()
  await page.getByText('全局 API 请求间隔不能大于每 UID 间隔',{exact:true}).waitFor()
  assert.equal(writes.length,0)
  await page.getByLabel('全局 API 请求间隔（毫秒）',{exact:true}).fill('50')
  await page.getByLabel('池健康通知',{exact:true}).selectOption('bot')
  failSave=true
  await page.getByRole('button',{name:'保存池设置',exact:true}).click()
  await page.getByRole('alert').getByText('保存失败，设置未修改',{exact:true}).waitFor()
  assert.equal(await page.getByLabel('每 UID 名额上限',{exact:true}).inputValue(),'2')
  failSave=false
  await page.getByRole('button',{name:'保存池设置',exact:true}).click()
  await page.getByText('池设置已保存',{exact:true}).waitFor()
  assert.equal(writes.at(-1).maxSessions,2)
  assert.equal(writes.at(-1).notificationBotId,'bot')
  await card.getByText('超过当前上限，已有会话保留',{exact:true}).waitFor()
  await page.getByRole('button',{name:'调度设置',exact:true}).click()
  assert(await page.getByRole('button',{name:'保存池设置',exact:true}).isDisabled())
  await page.getByLabel('取链总预算（秒）',{exact:true}).fill('60')
  await page.getByRole('button',{name:'关闭调度设置',exact:true}).click()
  await page.getByRole('dialog',{name:'放弃未保存的修改？',exact:true}).waitFor()
  await page.getByRole('button',{name:'继续编辑',exact:true}).click()
  assert.equal(await page.getByLabel('取链总预算（秒）',{exact:true}).inputValue(),'60')
  await page.getByRole('button',{name:'关闭调度设置',exact:true}).click()
  await page.getByRole('button',{name:'放弃修改',exact:true}).click()
  await page.getByRole('dialog').waitFor({state:'detached'})
  assert.equal(await page.evaluate(()=>document.body.style.overflow),'')
  await page.getByLabel('小号 A 调度状态',{exact:true}).selectOption('draining')
  await card.getByText('已停止为此配置分配新播放，已有会话保留。',{exact:true}).waitFor()
  const count=modeWrites.length
  await page.getByLabel('小号 A 调度状态',{exact:true}).selectOption('disabled')
  await page.getByRole('dialog').getByText('禁用后拒绝使用此配置的后续取链。已签发的直链无法立即撤回。',{exact:true}).waitFor()
  assert.equal(modeWrites.length,count)
  await page.getByRole('button',{name:'取消',exact:true}).click()
  assert.equal(await page.getByLabel('小号 A 调度状态',{exact:true}).inputValue(),'draining')
  failMode=true
  await page.getByLabel('小号 A 调度状态',{exact:true}).selectOption('disabled')
  await page.getByRole('button',{name:'确认禁用',exact:true}).click()
  await card.getByRole('alert').getByText('模式保存失败',{exact:true}).waitFor()
  assert.equal(await page.getByLabel('小号 A 调度状态',{exact:true}).inputValue(),'draining')
  failMode=false
  await page.getByLabel('小号 A 调度状态',{exact:true}).selectOption('enabled')
  await card.getByRole('status').getByText('已恢复参与调度，仍需满足健康与容量条件。',{exact:true}).waitFor()
  await card.getByRole('button',{name:'重新检查',exact:true}).click()
  await card.getByText('检查结果：目录失效',{exact:true}).waitFor()
  assert.equal(await card.getByText('最近检查通过',{exact:true}).count(),0)
  assert.equal(checkWrites.length,1)
  // Unknown health does not imply failure or unusability.
  pool.forEach(a=>a.pool.state='unknown');zeroSamples=true
  await page.getByRole('button',{name:'刷新状态',exact:true}).click()
  await overview.getByText('等待请求完成',{exact:true}).waitFor()
  await overview.locator('.metric-cell').filter({hasText:'取链成功率'}).locator('.metric-number').getByText('--',{exact:true}).waitFor()
  assert.equal(await page.getByText('参与调度的小号目前均处于故障或冷却状态',{exact:true}).count(),0)
  await page.getByRole('button',{name:'运行详情',exact:true}).click()
  await page.getByRole('dialog').getByText('暂无样本',{exact:true}).first().waitFor()
  await page.getByRole('button',{name:'关闭运行详情',exact:true}).click()
  failRead=true
  await page.getByRole('button',{name:'刷新状态',exact:true}).click()
  await page.getByRole('alert').getByText(/状态刷新失败，显示上次成功更新的数据/).waitFor()
  assert.equal(await page.locator('article').count(),3)
  failRead=false
  await page.getByRole('button',{name:'重试',exact:true}).click()
  await page.getByRole('alert').filter({hasText:'状态刷新失败'}).waitFor({state:'detached'})
  for(const width of [1440,390,320]) for(const dark of [false,true]) {
   await page.setViewportSize({width,height:844})
   await page.evaluate(d=>document.documentElement.classList.toggle('dark',d),dark)
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),JSON.stringify(await page.evaluate(()=>({width:innerWidth,scrollWidth:document.documentElement.scrollWidth,overflow:[...document.querySelectorAll('main *')].map(e=>({tag:e.tagName,class:e.className,x:e.getBoundingClientRect().x,right:e.getBoundingClientRect().right})).filter(e=>e.right>innerWidth+1).slice(0,8)}))))
   await page.getByRole('button',{name:'调度设置',exact:true}).click()
   const panel=page.getByRole('dialog',{name:'调度设置',exact:true}), bounds=await panel.boundingBox()
   assert(bounds.x>=-1 && bounds.x+bounds.width<=width+1)
   assert(await panel.evaluate(e=>e.scrollWidth<=e.clientWidth+1))
   const footer=await panel.locator('footer').boundingBox();assert(footer.y+footer.height<=844+1)
   await page.keyboard.press('Escape');await panel.waitFor({state:'detached'})
   assert.equal(await page.evaluate(()=>document.body.style.overflow),'')
   assert.equal(await page.evaluate(()=>document.activeElement.textContent.trim()),'调度设置')
   await page.screenshot({path:path.join(os.tmpdir(),`cinema115-helper-pool-${width}-${dark?'dark':'light'}.png`),fullPage:true})
  }
  pool.splice(0,pool.length)
  await page.getByRole('button',{name:'刷新状态',exact:true}).click()
  await page.getByText('共享池还没有小号',{exact:true}).waitFor()
  assert(await page.getByRole('button',{name:'添加第一个小号',exact:true}).isEnabled())
  assert.equal(checkWrites.length,1,'passive refresh must not probe 115')
  await page.goto('http://127.0.0.1:5173/emby/proxy')
  await page.getByRole('button',{name:'秒传验证',exact:true}).click()
  await page.getByLabel('目标用户').selectOption('reader')
  assert.equal(await page.getByLabel('目标用户').locator('option[value="personal"]').count(),0)
  await page.getByRole('button',{name:'保存秒传配置',exact:true}).click()
  await page.getByText('秒传配置已保存。',{exact:true}).waitFor()
  assert.deepEqual(writes.at(-1),{userId:'reader'})
  await page.locator('#instant-strm').fill('http://nas/api/?share_code=abc&id=123')
  await page.getByRole('button',{name:'开始单文件秒传',exact:true}).click()
  await page.getByText('秒传成功',{exact:true}).waitFor()
  assert.equal(writes.at(-1).strm,'http://nas/api/?share_code=abc&id=123')
  assert.equal('accountId' in writes.at(-1),false)
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
  assert.deepEqual(errors,[])
  console.log('Dynamic pool UI passed: UID deduplication, draft polling, save failures, interval validation, nested discard, mode confirmation and failures, check health result, unknown health, zero samples, stale-data recovery, light/dark 320/390/1440 layouts, focus restore, empty pool, source-only manual task')
 } finally {await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1})
