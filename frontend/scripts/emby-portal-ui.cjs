const path=require('node:path'),os=require('node:os'),assert=require('node:assert/strict')
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async()=>{
 const browser=await chromium.launch({executablePath:process.env.CHROME_PATH||'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true})
 try{
 const context=await browser.newContext({viewport:{width:1280,height:1000},serviceWorkers:'block'})
 const page=await context.newPage(),errors=[],writes=[]
 page.on('pageerror',e=>errors.push(e.message))
 let role='user'
 const state={source:'assigned',canPreparePersonal:true,enabled:true,stage:'pending',message:'等待分配',uid:'',connected:false,directoryId:'',embyName:'reader',embyUrl:'https://play.example.com',blocked:false,pendingDrive:false}
 await page.route('**/api/**',async route=>{
  const req=route.request(),p=new URL(req.url()).pathname;let data={}
  if(p==='/api/auth/me')data={user:{id:'reader',username:'reader',role,disabled:false}}
  else if(p.includes('/auth/')&&p.endsWith('/login')){writes.push(p);data={token:'mock',user:{role}}}
  else if(p==='/api/auth/invite/check')data={valid:true,playbackSource:'assigned'}
  else if(p==='/api/auth/register'){writes.push(req.postDataJSON());data={user:{username:'newreader'}}}
  else if(p==='/api/me/playback')data=state
  else if(p==='/api/me/playback/qrcode'){writes.push(req.postDataJSON());data={qrId:'qr',qrcode:'test',expiresAt:new Date(Date.now()+180000).toISOString()}}
  else if(p==='/api/me/playback/qrcode/status'){Object.assign(state,{uid:'123',connected:true,directoryId:'456'});data={status:'success',playback:state}}
  else if(p==='/api/me/playback/use-personal'){state.source='personal';state.stage='ready';data=state}
  else if(p==='/api/me/playback/disconnect'){state.connected=false;data=state}
  else if(p==='/api/admin/invites'){if(req.method()==='POST'){writes.push(req.postDataJSON());data={invites:[{id:'i',code:'abcdef',playbackSource:'assigned',createdAt:1,expiresAt:9999999999}]}}else data={invites:[],hasMore:false}}
  await route.fulfill({contentType:'application/json',body:JSON.stringify(data)})
 })
 const base=process.env.CINEMA_URL||'http://127.0.0.1:5173'
 await page.goto(base+'/account/playback')
 await page.getByText('使用 Emby 账号和密码登录个人中心').waitFor()
 await page.getByRole('button',{name:'管理员登录',exact:true}).click()
 await page.getByText('使用独立管理账号登录').waitFor()
 await page.getByRole('button',{name:'返回 Emby 用户登录'}).click()
 await page.getByLabel('账号',{exact:true}).fill('reader');await page.getByLabel('密码',{exact:true}).fill('password123');await page.getByRole('button',{name:'登录',exact:true}).click()
 await page.getByRole('heading',{name:'Emby 账号',exact:true}).waitFor()
 assert.equal(writes[0],'/api/auth/login')
 assert.equal(await page.getByRole('button',{name:'注册 Emby 账号',exact:true}).count(),0)
 await page.getByRole('checkbox',{name:'我同意保存网盘登录凭据用于本人播放，并在专用目录保存播放文件。'}).check()
 await page.getByRole('button',{name:'扫码绑定 115',exact:true}).click();await page.getByText('115 绑定成功',{exact:true}).waitFor()
 assert.equal(state.source,'assigned')
 await page.getByRole('button',{name:'切换到我的网盘播放'}).click();await page.getByText('播放方式：绑定自己的 115 网盘',{exact:true}).waitFor()
 await page.getByRole('checkbox',{name:'更换为另一个 115 账号，新账号校验完成后切换，旧网盘文件保留。'}).check()
 await page.getByRole('button',{name:'更换 115 账号',exact:true}).click()
 assert.deepEqual(writes.at(-1),{replace:true})
 await page.setViewportSize({width:390,height:844});await page.evaluate(()=>document.documentElement.classList.add('dark'))
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
 await page.goto(base+'/register#invite=abcdef')
 await page.getByText('邀请码有效：注册 Emby 后分配管理员小号，无可用账号时等待分配。').waitFor()
 assert.equal(new URL(page.url()).hash,'')
 await page.getByLabel('用户名',{exact:true}).fill('newreader');await page.getByLabel('密码',{exact:true}).fill('password123');await page.getByLabel('确认密码',{exact:true}).fill('password123');await page.getByRole('button',{name:'注册账号',exact:true}).click()
 role='admin';await page.evaluate(()=>localStorage.setItem('admin_token','mock'));await page.goto(base+'/users')
 await page.getByRole('heading',{name:'生成注册邀请'}).waitFor();await page.getByLabel('播放开通方式').selectOption('assigned');await page.getByRole('button',{name:'生成邀请',exact:true}).click();await page.getByRole('heading',{name:'请保存新生成的邀请'}).waitFor()
 assert.equal(writes.at(-1).playbackSource,'assigned');assert.deepEqual(errors,[])
 console.log('Emby portal UI passed: login separation, single registration, QR preparation, drive replacement, two invitations, mobile dark layout')
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)})
