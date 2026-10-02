const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 950 }, serviceWorkers: 'block' })
  await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
  const errors=[], writes=[]
  page.on('pageerror', e => errors.push(e.message))
  const settings={enabled:true,libraryIds:[],allowDownload:true}
  const members=[
   {userId:'reader',embyId:'a'.repeat(32),embyName:'已有 Emby 用户',source:'primary',playbackAllowed:true,downloadAllowed:true,stage:'pending',childLimit:0,childUsed:0},
   {userId:'invited',embyId:'b'.repeat(32),embyName:'邀请注册用户',createdByCinema:true,source:'personal',playbackAllowed:false,personalDrive:{connected:false},stage:'pending'},
   {userId:'child',embyId:'c'.repeat(32),embyName:'已撤销子账号',playbackOnly:true,childRevoked:true,disabled:true,source:'personal_shared',playbackAllowed:true,stage:'pending'},
  ]
  let uncertain=false,hold,release
  await page.route('**/api/**',async route=>{
   const req=route.request(),p=new URL(req.url()).pathname
   let data={}
   if(p==='/api/auth/me')data={user:{id:'admin',role:'admin',username:'admin'}}
   else if(p.endsWith('/accounts'))data={accounts:[]}
   else if(p.endsWith('/reader/playback')){
    const body=req.postDataJSON();writes.push(body)
    if(hold)await hold
    members[0].playbackAllowed=body.allowed
    if(uncertain){uncertain=false;return route.fulfill({status:502,contentType:'application/json',body:JSON.stringify({error:'Emby 修改结果未确认，请刷新查看；不会自动重试'})})}
    data={members:[members[0]],settings}
   }else if(p.endsWith('/reader/permissions'))data={members:[members[0]],settings}
   else if(p.includes('/member-playback'))data={members,settings,proxy:{listening:true}}
   await route.fulfill({contentType:'application/json',body:JSON.stringify(data)})
  })
  await page.goto((process.env.CINEMA_URL||'http://127.0.0.1:5173')+'/emby/users')
  const card=page.locator('article').filter({has:page.getByRole('heading',{name:'已有 Emby 用户',exact:true})})
  const toggle=card.getByRole('switch',{name:'允许 Emby 播放'})
  await toggle.waitFor()
  assert.equal(await toggle.getAttribute('aria-checked'),'true')
  await card.getByText('尚未检查',{exact:true}).waitFor()
  assert.equal(await page.getByText('尚未接入播放',{exact:true}).count(),0)
  const revoked=page.locator('article').filter({has:page.getByRole('heading',{name:'已撤销子账号',exact:true})})
  assert.equal(await revoked.getByRole('switch').isDisabled(),true)
  hold=new Promise(resolve=>{release=resolve})
  await toggle.click()
  assert.equal(await toggle.getAttribute('aria-checked'),'true')
  assert.equal(await toggle.isDisabled(),true)
  release();hold=undefined
  await card.getByText('Emby 播放权限已更新。').waitFor()
  assert.equal(await toggle.getAttribute('aria-checked'),'false')
  assert.equal(members[0].downloadAllowed,true)
  assert.deepEqual(writes,[{allowed:false,expected:true}])
  uncertain=true
  await toggle.click()
  await card.getByRole('alert').waitFor()
  assert.equal(await toggle.isDisabled(),true)
  await card.getByText('待确认',{exact:true}).waitFor()
  assert.equal(writes.length,2)
  await card.getByRole('button',{name:'刷新 Emby 权限'}).click()
  await card.locator('[role="switch"][aria-checked="true"]').waitFor()
  assert.equal(writes.length,2)
  // External tools change the authoritative value; list refresh must display it.
  members[0].playbackAllowed=false
  await page.getByRole('button',{name:'刷新状态',exact:true}).click()
  await card.locator('[role="switch"][aria-checked="false"]').waitFor()
  await page.screenshot({path:path.join(os.tmpdir(),'cinema-emby-playback-switches.png'),fullPage:true})
  await page.setViewportSize({width:390,height:844})
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
  await page.evaluate(()=>document.documentElement.classList.add('dark'))
  await page.screenshot({path:path.join(os.tmpdir(),'cinema-emby-playback-switches-mobile.png'),fullPage:true})
  assert.deepEqual(errors,[])
  console.log('Emby playback switch UI passed: live values, pending writes, uncertainty recovery, no retries, revoked child, external refresh, mobile')
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)})
