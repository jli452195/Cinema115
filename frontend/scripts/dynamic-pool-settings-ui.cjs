const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const context = await browser.newContext({ viewport: { width: 1280, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
  const page = await context.newPage(), errors = [], writes = []
  page.on('pageerror', e => errors.push(e.message))
  const pool = [{id:'pool',name:'小号 A',uid:'501',primary:false,configured:true,revision:1,playbackParentCid:'123',pool:{state:'ready',mode:'enabled',sessions:1,preparing:0,residual:0,retryAt:0}}]
  let settings={maxSessions:3,maxAttempts:3,budgetSeconds:90,leaseMinutes:5,cooldownSeconds:30,uidIntervalMs:250,globalIntervalMs:50,notificationBotId:''}
  let reads=0
  let config={userId:'',sourceAccountId:'main',accountId:'',targetCid:'',targetPath:''}
  await page.route('**/api/**', async route => {
   const req=route.request(), p=new URL(req.url()).pathname; let data={}
   if (p==='/api/auth/me') data={user:{id:'admin',username:'admin',role:'admin'}}
   else if(p==='/api/admin/p115/accounts') data={accounts:[{id:'main',name:'主账号',primary:true,configured:true,revision:1},...pool]}
   else if(p.endsWith('/device-policy')) data={mode:'parallel',silenceSeconds:60}
   else if(p==='/api/admin/member-playback/accounts') data={accounts:pool}
   else if(p==='/api/admin/member-playback/pool') {if(req.method()==='POST'){settings=req.postDataJSON();writes.push(settings)}else reads++;data={settings,accounts:pool,metrics:{requests:10,succeeded:8,failed:2,switches:1,p50Ms:100,p95Ms:900,samples:10,peakUidSessions:2,resolved:8,copyResolutions:7,directLinkHits:5},api:{requests:20,throttled:1,rejected:0,waitMs:50,waiting:0,sourceRequests:3},events:[{id:1,accountId:'pool',state:'invalid_directory',at:Date.now()}]}}
   else if(p==='/api/admin/telegram/bots') data={bots:[{id:'bot',name:'测试通知',configured:true,enabled:true,notificationChatID:'42'}]}
   else if(p==='/api/admin/member-playback/pool/pool'){pool[0].pool.mode=req.postDataJSON().mode;data={accounts:pool}}
   else if(p==='/api/admin/member-playback/pool/pool/check'){data={accounts:pool}}
   else if(p==='/api/admin/member-playback/settings') data={settings:{allowDownload:false,publicUrl:''}}
   else if(p==='/api/admin/emby/proxy') data={config:{enabled:false,debug:false,prewarm:false,prepareFiles:true,port:8097,url:'http://emby',accountId:'main',directLinkCacheMinutes:9},listening:false,error:''}
   else if(p==='/api/admin/emby/playback-cache/users') data={embyUrl:'http://emby',users:[{Id:'reader',Policy:{IsDisabled:false}},{Id:'personal',Policy:{IsDisabled:false}}],members:[{embyId:'reader',embyName:'读者',source:'assigned'},{embyId:'personal',embyName:'个人用户',source:'personal',personalDrive:{connected:true}}]}
   else if(p==='/api/admin/emby/instant-upload') {if(req.method()==='POST'){writes.push(req.postDataJSON());config={...config,userId:req.postDataJSON().userId}}data={config,records:[],running:false}}
   else if(p==='/api/admin/emby/instant-upload/run') {writes.push(req.postDataJSON());data={id:req.postDataJSON().id,accountId:'pool',targetCid:'123',status:'success',message:'模拟秒传成功',startedAt:new Date().toISOString(),durationMs:10}}
   await route.fulfill({contentType:'application/json',body:JSON.stringify(data)})
  })
  await page.goto('http://127.0.0.1:5173/helper115')
  await page.getByText('动态池容量与调度设置',{exact:true}).click()
  await page.getByText('当前只有一个小号 UID，没有备用小号。',{exact:false}).waitFor()
  await page.getByLabel('动态池运行指标').getByText('本次运行成功率 80.0%',{exact:false}).waitFor()
  await page.getByLabel('每 UID 会话上限',{exact:true}).fill('2')
  const before=reads
  await page.waitForTimeout(10500)
  assert.ok(reads>before,'metrics were not refreshed')
  assert.equal(await page.getByLabel('每 UID 会话上限',{exact:true}).inputValue(),'2','polling overwrote unsaved settings')
  await page.getByLabel('池健康通知',{exact:true}).selectOption('bot')
  await page.getByRole('button',{name:'保存池设置',exact:true}).click()
  await page.getByText('池设置已保存',{exact:true}).waitFor()
  assert.equal(writes.at(-1).maxSessions,2)
  assert.equal(writes.at(-1).notificationBotId,'bot')
  await page.getByLabel('小号 A 调度状态',{exact:true}).selectOption('draining')
  await page.getByRole('button',{name:'重新检查',exact:true}).click()
  await page.setViewportSize({width:390,height:844})
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
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
  console.log('Dynamic pool UI passed: settings, modes, health refresh, source-only manual task, personal isolation, mobile layout')
 } finally {await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1})
