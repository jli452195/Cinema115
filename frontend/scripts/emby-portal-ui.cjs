const path=require('node:path'),os=require('node:os'),assert=require('node:assert/strict')
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async()=>{
 const browser=await chromium.launch({executablePath:process.env.CHROME_PATH||'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true})
 try{
 const context=await browser.newContext({viewport:{width:1280,height:1000},serviceWorkers:'block'})
 const page=await context.newPage(),errors=[],writes=[]
 page.on('pageerror',e=>errors.push(e.message))
 let role='user',expired=false,rejectLogin=false
 await page.clock.install()
 const state={source:'assigned',canPreparePersonal:true,enabled:true,stage:'pending',message:'等待分配',uid:'',connected:false,directoryId:'',embyName:'reader',embyUrl:'https://play.example.com',blocked:false,pendingDrive:false}
 await page.route('**/api/**',async route=>{
  const req=route.request(),p=new URL(req.url()).pathname;let data={}
  if(p==='/api/auth/me'){
   if(expired)return route.fulfill({status:401,contentType:'application/json',body:JSON.stringify({error:'登录已过期'})})
   data={user:{id:'reader',username:'reader',role,disabled:false}}
  }
  else if(p.includes('/auth/')&&p.endsWith('/login')){
   writes.push(p)
   if(rejectLogin)return route.fulfill({status:401,contentType:'application/json',body:JSON.stringify({error:'账号或密码错误'})})
   data={token:'mock',user:{role}}}
  else if(p==='/api/auth/invite/check')data={valid:true,playbackSource:'assigned'}
  else if(p==='/api/auth/register'){writes.push(req.postDataJSON());data={user:{username:'newreader'}}}
  else if(p==='/api/me/playback')data=state
  else if(p==='/api/me/playback/children')data={limit:0,used:0,items:[]}
  else if(p==='/api/me/playback/qrcode'){writes.push(req.postDataJSON());data={qrId:'qr',qrcode:'test',expiresAt:new Date(Date.now()+180000).toISOString()}}
  else if(p==='/api/me/playback/qrcode/status'){Object.assign(state,{uid:'123',connected:true,directoryId:'456'});data={status:'success',playback:state}}
  else if(p==='/api/me/playback/use-personal'){state.source='personal';state.stage='ready';data=state}
  else if(p==='/api/me/playback/disconnect'){Object.assign(state,{connected:false,uid:'',directoryId:'',pendingDrive:false,stage:'paused'});data=state}
  else if(p==='/api/admin/invites'){if(req.method()==='POST'){writes.push(req.postDataJSON());data={invites:[{id:'i',code:'abcdef',playbackSource:'assigned',createdAt:1,expiresAt:9999999999}]}}else data={invites:[],hasMore:false}}
  await route.fulfill({contentType:'application/json',body:JSON.stringify(data)})
 })
 const base=process.env.CINEMA_URL||'http://127.0.0.1:5173'
 await page.goto(base+'/account/playback')
 await page.getByText('使用 Emby 账号和密码登录个人中心').waitFor()
 assert.equal(new URL(page.url()).pathname,'/login')
 assert.equal(new URL(page.url()).searchParams.get('redirect'),'/account/playback')
 assert.equal(await page.locator('form').getByRole('link',{name:'管理入口'}).count(),0)
 await page.getByLabel('Emby 用户名',{exact:true}).fill('reader')
 await page.getByLabel('密码',{exact:true}).fill('password123')
 await page.getByRole('link',{name:'管理入口',exact:true}).click()
 await page.getByText('使用独立的后台管理员账号登录').waitFor()
 assert.equal(new URL(page.url()).pathname,'/admin/login')
 assert.equal(await page.getByLabel('管理员账号',{exact:true}).inputValue(),'')
 assert.equal(await page.getByLabel('密码',{exact:true}).inputValue(),'')
 assert.equal(await page.getByRole('link',{name:/邀请码/}).count(),0)
 await page.reload();await page.getByRole('heading',{name:'管理员登录',exact:true}).waitFor()
 await page.getByRole('link',{name:'返回 Emby 用户登录'}).click()
 await page.getByLabel('Emby 用户名',{exact:true}).fill('reader');await page.getByLabel('密码',{exact:true}).fill('password123');await page.getByRole('button',{name:'登录个人中心',exact:true}).click()
 await page.getByRole('heading',{name:'Emby 账号',exact:true}).waitFor()
 assert.equal(writes[0],'/api/auth/login')
 assert.equal(await page.getByRole('button',{name:'注册 Emby 账号',exact:true}).count(),0)
 await page.getByRole('checkbox',{name:'我同意保存凭据用于本人播放和只读秒传校验，并在专用目录保存播放文件。'}).check()
 await page.getByRole('button',{name:'扫码绑定 115',exact:true}).click();await page.getByText('115 登录成功，已保留原秒传目录',{exact:true}).waitFor()
 assert.equal(state.source,'assigned')
 await page.getByRole('button',{name:'切换到我的网盘播放'}).click();await page.getByText('播放方式：绑定自己的 115 网盘',{exact:true}).waitFor()
 await page.getByRole('button',{name:'扫码登录或换号',exact:true}).click()
 assert.deepEqual(writes.at(-1),{replace:true,clientType:'alipay-miniapp',accountId:'',revision:0})
 await page.getByRole('button',{name:'关闭二维码'}).click()
 await page.getByRole('checkbox',{name:/解除个人网盘绑定并暂停播放/}).check()
 await page.getByRole('button',{name:'解绑个人网盘',exact:true}).click()
 await page.getByRole('button',{name:'扫码绑定 115',exact:true}).waitFor()
 assert.equal(state.uid,'');assert.equal(state.source,'personal')
 await page.setViewportSize({width:390,height:844});await page.evaluate(()=>document.documentElement.classList.add('dark'))
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
 await page.goto(base+'/register#invite=abcdef')
 await page.getByText('邀请码有效：注册 Emby 后分配管理员共享小号，无可用小号时使用主网盘。').waitFor()
 assert.equal(new URL(page.url()).hash,'')
 await page.getByLabel('用户名',{exact:true}).fill('newreader');await page.getByLabel('密码',{exact:true}).fill('password123');await page.getByLabel('确认密码',{exact:true}).fill('password123');await page.getByRole('button',{name:'注册账号',exact:true}).click()
 await page.getByText('Emby 账号创建成功，请使用刚设置的密码登录。').waitFor()
 assert.equal(new URL(page.url()).pathname,'/login')
 assert.equal(await page.getByLabel('Emby 用户名',{exact:true}).inputValue(),'newreader')
 role='admin';await page.evaluate(()=>localStorage.setItem('admin_token','mock'));await page.goto(base+'/users/invites')
 await page.getByRole('heading',{name:'生成注册邀请'}).waitFor();await page.getByLabel('播放开通方式').selectOption('assigned');await page.getByRole('button',{name:'生成邀请',exact:true}).click();await page.getByRole('heading',{name:'请保存新生成的邀请'}).waitFor()
 assert.equal(writes.at(-1).playbackSource,'assigned')
 await page.getByRole('button',{name:'管理菜单',exact:true}).click()
 await page.getByRole('button',{name:'退出登录',exact:true}).click()
 await page.waitForURL(base+'/admin/login')
 await page.goto(base+'/115')
 await page.getByRole('heading',{name:'管理员登录',exact:true}).waitFor()
 assert.equal(new URL(page.url()).searchParams.get('redirect'),'/115')
 await page.getByLabel('管理员账号',{exact:true}).fill('manager');await page.getByLabel('密码',{exact:true}).fill('password123')
 await page.getByRole('button',{name:'登录管理后台',exact:true}).click()
 await page.waitForURL(base+'/115')
 assert.equal(writes.at(-1),'/api/auth/admin/login')
 expired=true;await page.clock.fastForward(60000)
 await page.waitForURL(url=>url.pathname==='/admin/login')
 assert.equal(new URL(page.url()).searchParams.get('redirect'),'/115')
 expired=false
 await page.getByLabel('管理员账号',{exact:true}).fill('manager');await page.getByLabel('密码',{exact:true}).fill('password123')
 await page.getByRole('button',{name:'登录管理后台',exact:true}).click();await page.waitForURL(base+'/115')
 await page.getByRole('button',{name:'管理菜单',exact:true}).click()
 await page.getByRole('button',{name:'退出登录',exact:true}).click()
 await page.waitForURL(base+'/admin/login')
 role='user'
 await page.goto(base+'/login?redirect=%2F115')
 await page.getByLabel('Emby 用户名',{exact:true}).fill('reader');await page.getByLabel('密码',{exact:true}).fill('password123');await page.getByRole('button',{name:'登录个人中心',exact:true}).click()
 await page.waitForURL(base+'/')
 await page.getByRole('button',{name:'管理菜单',exact:true}).click()
 await page.getByRole('button',{name:'退出登录',exact:true}).click()
 await page.waitForURL(base+'/login')
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
 rejectLogin=true
 const beforeFailure=writes.length
 await page.getByLabel('Emby 用户名',{exact:true}).fill('reader');await page.getByLabel('密码',{exact:true}).fill('wrongpassword')
 await page.getByRole('button',{name:'登录个人中心',exact:true}).click();await page.getByRole('alert').waitFor()
 assert.deepEqual(writes.slice(beforeFailure),['/api/auth/login'])
 rejectLogin=false
 await page.goto(base+'/login?redirect=https%3A%2F%2Fexample.com')
 await page.getByLabel('Emby 用户名',{exact:true}).fill('reader');await page.getByLabel('密码',{exact:true}).fill('password123')
 await page.getByRole('button',{name:'登录个人中心',exact:true}).click();await page.waitForURL(base+'/')
 expired=true;await page.clock.fastForward(60000)
 await page.waitForURL(url=>url.pathname==='/login')
 await page.screenshot({path:path.join(os.tmpdir(),'cinema115-user-login.png'),fullPage:true})
 await page.getByRole('link',{name:'管理入口',exact:true}).click()
 await page.screenshot({path:path.join(os.tmpdir(),'cinema115-admin-login.png'),fullPage:true})
 assert.deepEqual(errors,[])
 console.log('Emby portal UI passed: separate login routes and endpoints, form reset, registration return, role guards, logout, session expiry, safe redirects, QR preparation, drive replacement, invitations, mobile layout')
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)})
