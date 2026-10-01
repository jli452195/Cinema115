const path=require('node:path'),os=require('node:os'),assert=require('node:assert/strict')
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||path.join(os.homedir(),'.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async()=>{
 const browser=await chromium.launch({executablePath:'C:/Program Files/Google/Chrome/Application/chrome.exe',headless:true})
 try{
  const context=await browser.newContext({viewport:{width:1440,height:1000},serviceWorkers:'block'})
  await context.addInitScript(()=>localStorage.setItem('admin_token','mock'))
  const page=await context.newPage(),errors=[],writes=[],passed=[];page.setDefaultTimeout(10000)
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept())
  const account={id:'pool',name:'小号-1',primary:false,uid:'111',revision:1,configured:true,loginClient:'115ipad',playbackParentCid:'123',userCount:6,users:['one','two','three','four','five','six']}
  let session=0,mode='replace',confirmFails=false
  await page.route('**/api/**',async route=>{
   const req=route.request(),url=new URL(req.url()),p=url.pathname;let data={},status=200
   if(req.method()!=='GET')writes.push({path:p,method:req.method(),body:req.postData()?req.postDataJSON():null,query:url.searchParams.toString()})
   const pending=()=>({status:'directory_required',connected:false,replacement:{sessionId:'session-'+session,oldUid:account.uid,newUid:'999',expiresAt:new Date(Date.now()+300000).toISOString()}})
   if(p==='/api/auth/me')data={user:{id:'admin',role:'admin',username:'admin'}}
   else if(p==='/api/admin/member-playback/accounts')data={accounts:[account]}
   else if(p==='/api/admin/p115/accounts')data={accounts:[account]}
   else if(p.endsWith('/qrcode')&&req.method()==='POST'){session++;data={qrId:'qr-'+session,sessionId:'session-'+session,qrcode:'qr-'+session,expiresAt:new Date(Date.now()+180000).toISOString()}}
   else if(p.endsWith('/qrcode/status')){if(mode==='replace')data=pending();else{account.revision++;data={status:'success',connected:true,loginClient:'115ipad'}}}
   else if(p.endsWith('/replacement/paths')){assert.equal(url.searchParams.get('sessionId'),'session-'+session);data={directories:url.searchParams.get('cid')==='0'?[{id:'789',name:'新网盘播放目录'}]:[],hasMore:false}}
   else if(p.endsWith('/replacement/confirm')){
    if(confirmFails){status=400;data={error:'目录校验失败，原配置未修改'}}else{assert.equal(req.postDataJSON().directoryId,'789');account.revision++;account.uid='999';account.playbackParentCid='789';data={status:'success',connected:true,loginClient:'115ipad'}}
   }else if(p.endsWith('/replacement')&&req.method()==='DELETE'){}
   else if(p.endsWith('/playback-directory')){account.revision++;account.playbackParentCid=req.postDataJSON().parentCid;data={accounts:[account]}}
   else if(p.endsWith('/paths'))data={directories:url.searchParams.get('cid')==='0'?[{id:'555',name:'当前账号新目录'}]:[],hasMore:false}
   else if(p.endsWith('/status'))data={connected:true,configured:true,loginClient:'115ipad'}
   else if(p.endsWith('/login')&&req.method()==='DELETE'){account.configured=false;account.loginClient='';account.revision++}
   else if(p.endsWith('/pool')&&req.method()==='PUT'){
    const body=req.postDataJSON();if(body.cookie){session++;data=pending()}else{account.name=body.name;account.revision++;data={message:'名称已保存'}}
   }
   await route.fulfill({status,contentType:'application/json',body:JSON.stringify(data)})
  })
  await page.goto((process.env.CINEMA_URL||'http://127.0.0.1:5173')+'/115/pool')
  const card=page.locator('article');await card.getByRole('heading',{name:'小号-1'}).waitFor()
  assert.equal(await card.locator('input,select').count(),0)
  assert(!(await page.locator('body').innerText()).includes('换号时的新网盘目录编号'))
  const cardHeight=(await card.boundingBox()).height;assert(cardHeight<320)
  await card.getByText('配置 ID：',{exact:true}).waitFor()
  await context.grantPermissions(['clipboard-read','clipboard-write'])
  await card.getByRole('button',{name:'复制配置 ID',exact:true}).click()
  assert.equal(await page.evaluate(()=>navigator.clipboard.readText()),account.id)
  await card.getByRole('button',{name:'6 人使用 ›'}).click();await page.getByRole('dialog').getByText('one',{exact:true}).waitFor()
  assert.equal((await card.boundingBox()).height,cardHeight)
  await page.keyboard.press('Escape');await page.getByRole('dialog').waitFor({state:'detached'})
  await card.getByRole('button',{name:'更多操作'}).click();assert.equal((await card.boundingBox()).height,cardHeight)
  await page.keyboard.press('Escape');assert.equal(await card.getByRole('button',{name:'修改名称',exact:true}).count(),0)
  await card.getByRole('button',{name:'更多操作'}).click()
  await card.getByRole('button',{name:'修改名称',exact:true}).click();await page.getByRole('dialog').getByLabel('名称',{exact:true}).fill('小号 B');assert.equal((await card.boundingBox()).height,cardHeight);await page.getByRole('button',{name:'保存名称'}).click();await card.getByRole('heading',{name:'小号 B'}).waitFor()
  passed.push('compact-card-and-rename')
  await card.getByRole('button',{name:'扫码登录',exact:true}).click()
  await page.getByRole('dialog').getByLabel('登录设备',{exact:true}).selectOption('115-ipad')
  await page.getByText('原 UID：111 → 新 UID：999',{exact:true}).waitFor()
  assert.equal(account.uid,'111');assert.equal(account.playbackParentCid,'123')
  await page.getByRole('button',{name:'选择新网盘中的秒传目录'}).click()
  await page.getByRole('button',{name:'新网盘播放目录'}).click();await page.getByText('当前目录没有子文件夹',{exact:true}).waitFor()
  await page.getByRole('button',{name:'选择此目录',exact:true}).click()
  confirmFails=true
  await page.getByRole('button',{name:'确认更换',exact:true}).click();await page.getByText('目录校验失败，原配置未修改',{exact:true}).waitFor();assert.equal(account.uid,'111')
  confirmFails=false
  await page.getByRole('button',{name:'确认更换',exact:true}).click();await page.getByRole('dialog').waitFor({state:'detached'})
  await card.getByText(/UID：999/).waitFor();await card.getByText('秒传目录：789',{exact:true}).waitFor();assert(await card.getByText('已连接',{exact:true}).isVisible())
  passed.push('qr-replacement-new-directory-validation-and-commit')
  await card.getByRole('button',{name:'更多操作'}).click();await card.getByRole('button',{name:'手动配置 Cookie'}).click()
  await page.getByLabel('Cookie',{exact:true}).fill('UID=999_A; SEID=mock')
  await page.getByRole('button',{name:'验证并继续'}).click();await page.getByRole('button',{name:'选择新网盘中的秒传目录'}).waitFor()
  const before=account.revision;await page.getByRole('button',{name:'关闭登录弹窗'}).click();await page.getByRole('dialog').waitFor({state:'detached'})
  assert.equal(account.revision,before);assert(writes.some(w=>w.method==='DELETE'&&w.query.includes('sessionId=')))
  passed.push('manual-cookie-staging-cancel')
  await card.getByRole('button',{name:'修改',exact:true}).click();await page.getByRole('button',{name:'当前账号新目录'}).click();await page.getByText('当前目录没有子文件夹',{exact:true}).waitFor();await page.getByRole('button',{name:'选择此目录',exact:true}).click();await card.getByText('秒传目录：555',{exact:true}).waitFor()
  mode='same';await card.getByRole('button',{name:'扫码登录',exact:true}).click();await page.getByRole('dialog').waitFor({state:'visible'});await page.getByRole('dialog').waitFor({state:'detached'});assert.equal(account.playbackParentCid,'555')
  passed.push('directory-edit-and-same-uid-login')
  for(const width of [1440,390]){await page.setViewportSize({width,height:844});for(const dark of [false,true]){
   await page.evaluate(d=>document.documentElement.classList.toggle('dark',d),dark)
   const height=(await card.boundingBox()).height
   await card.getByRole('button',{name:'更多操作'}).click();assert.equal((await card.boundingBox()).height,height)
   const menuBounds=await card.getByRole('group',{name:'账号操作'}).boundingBox();assert(menuBounds.x>=0&&menuBounds.x+menuBounds.width<=width)
   await card.getByRole('heading').click();assert.equal(await card.getByRole('group',{name:'账号操作'}).count(),0)
   await card.getByRole('button',{name:'6 人使用 ›'}).click();assert.equal((await card.boundingBox()).height,height)
   const userBounds=await page.getByRole('dialog').boundingBox();assert(userBounds.width<=width&&userBounds.height<=844)
   await page.getByRole('button',{name:'关闭账号详情'}).click()
   mode='replace';await card.getByRole('button',{name:'扫码登录',exact:true}).click();await page.getByRole('button',{name:'选择新网盘中的秒传目录'}).waitFor()
   await page.getByRole('button',{name:'选择新网盘中的秒传目录'}).click();await page.getByRole('button',{name:'新网盘播放目录'}).waitFor()
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1))
   await page.getByRole('button',{name:'关闭目录选择器'}).click();await page.getByRole('button',{name:'关闭登录弹窗'}).click();await page.getByRole('dialog').waitFor({state:'detached'});assert.equal(await page.evaluate(()=>document.body.style.overflow),'')
   passed.push(width+':'+dark+':nested-dialogs')
  }}
  // The LAN HTTP fallback must copy the complete ID without requiring Clipboard API.
  await page.evaluate(()=>{Object.defineProperty(navigator,'clipboard',{configurable:true,value:undefined});document.execCommand=command=>{window.copiedID=document.querySelector('textarea')?.value;return command==='copy'}})
  await card.getByRole('button',{name:'复制配置 ID',exact:true}).click();assert.equal(await page.evaluate(()=>window.copiedID),account.id)
  passed.push('stable-card-menu-user-dialog-copy-and-http-fallback')
  assert.deepEqual(errors,[]);console.log(JSON.stringify({passed,pageErrors:errors}))
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)})
