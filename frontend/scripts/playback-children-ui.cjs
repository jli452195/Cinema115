const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 390, height: 844 }, serviceWorkers: 'block' })
  const errors = [], writes = []
  page.on('pageerror', e => errors.push(e.message))
  await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
  const children = { limit: 1, used: 0, items: [] }
  let failCreate = true, unknownCreate = false, recoveryReads = 0
  await page.route('**/api/**', async route => {
   const req = route.request(), url = new URL(req.url()), p = url.pathname
   let data = {}
   if (p.startsWith('/api/admin/')) throw new Error('Owner called admin API')
   if (p === '/api/auth/me') data = { user: { id: 'owner', username: 'owner', role: 'user' } }
   else if (p === '/api/me/playback') data = { enabled: true, source: 'primary', canPreparePersonal: true, stage: 'ready', connected: true, uid: '123', directoryId: '456', embyName: 'owner' }
   else if (p === '/api/me/playback/children') {
    if (req.method() === 'POST') {
     const body = req.postDataJSON(); writes.push(body)
     if (unknownCreate) { unknownCreate = false; recoveryReads = 1; return route.fulfill({ status: 502, contentType: 'application/json', body: JSON.stringify({ error: '创建响应丢失，结果未确认' }) }) }
     if (!children.items.length) { children.used = 1; children.items.push({ requestId: body.requestId, name: body.name, userId: 'child', completed: false, revision: 1, stage: 'creating' }) }
     assert.equal(body.requestId, children.items[0].requestId)
     if (failCreate) { failCreate = false; return route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: 'Emby 创建结果未确认，请使用原任务重试' }) }) }
     children.items[0].completed = true; children.items[0].stage = 'ready'
    }
    if (req.method() === 'GET' && recoveryReads > 0) { recoveryReads--; return route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: '恢复读取失败' }) }) }
    data = children
   } else if (p === '/api/me/playback/children/child') {
    const body = req.postDataJSON(); writes.push(body); const child = children.items[0]
    assert.equal(body.revision, child.revision)
    child.revision++
    if (body.action === 'pause') { child.paused = true; child.stage = 'paused' }
    if (body.action === 'resume') { child.paused = false; child.stage = 'ready' }
    if (body.action === 'disable') { child.disabled = true; child.stage = 'paused' }
    if (body.action === 'enable') { child.disabled = false; child.stage = 'ready' }
    if (body.action === 'revoke') { child.revoked = true; children.used = 0 }
    data = children
   }
   await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
  })
  await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/account/playback')
  await page.getByRole('heading', { name: '播放子账号', exact: true }).waitFor()
  assert.equal(await page.locator('main input').count(), 0)
  await page.getByRole('button', { name: '新增子账号', exact: true }).click()
  await page.getByLabel('子账号用户名', { exact: true }).fill('viewer-one')
  await page.getByLabel('子账号初始密码', { exact: true }).fill('secret-password')
  await page.getByRole('checkbox', { name: /我授权这些子账号/ }).check()
  await page.getByRole('button', { name: '创建子账号', exact: true }).click()
  await page.getByRole('alert').filter({ hasText: '创建结果未确认' }).waitFor()
  assert.equal(await page.getByLabel('子账号初始密码', { exact: true }).inputValue(), '')
  assert.equal(await page.getByLabel('子账号用户名', { exact: true }).isDisabled(), true)
  const first = writes[0].requestId
  await page.reload()
  await page.getByRole('button', { name: '继续完成' }).click()
  await page.getByLabel('子账号初始密码', { exact: true }).fill('secret-password')
  await page.getByRole('checkbox', { name: /我授权这些子账号/ }).check()
  await page.getByRole('button', { name: '继续创建', exact: true }).click()
  await page.getByRole('button', { name: '管理', exact: true }).waitFor()
  await page.getByRole('button', { name: '管理', exact: true }).click()
  assert.equal(writes[1].requestId, first)
  assert.equal(await page.getByLabel('子账号初始密码', { exact: true }).count(), 0)
  await page.getByRole('button', { name: /^暂停共享/ }).click()
  await page.getByRole('button', { name: /^停用 Emby 登录/ }).click()
  await page.getByRole('dialog').getByText('共享已暂停。登录已停用。', { exact: true }).waitFor()
  await page.getByRole('button', { name: /^恢复共享/ }).click()
  await page.getByRole('button', { name: /^启用 Emby 登录/ }).click()
  await page.getByRole('button', { name: /^重置密码/ }).click()
  await page.getByLabel('子账号新密码', { exact: true }).fill('new-password')
  await page.getByRole('button', { name: '保存新密码', exact: true }).click()
  await page.getByText('密码已更新；已登录设备不保证立即退出。', { exact: true }).waitFor()
  const count = writes.length
  await page.getByRole('button', { name: '永久撤销子账号', exact: true }).click()
  assert.equal(writes.length, count)
  await page.getByRole('button', { name: '确认永久撤销', exact: true }).click()
  await page.getByText('已永久撤销', { exact: true }).waitFor()
  assert.equal(children.used, 0)
  assert.equal(await page.getByRole('button', { name: /^恢复共享/ }).count(), 0)
  children.items.splice(0); children.used = 0; unknownCreate = true
  await page.getByRole('button', { name: '刷新子账号', exact: true }).click()
  await page.getByRole('button', { name: '新增子账号', exact: true }).click()
  await page.getByLabel('子账号用户名', { exact: true }).fill('viewer-two')
  await page.getByLabel('子账号初始密码', { exact: true }).fill('secret-password')
  await page.getByRole('checkbox', { name: /我授权这些子账号/ }).check()
  await page.getByRole('button', { name: '创建子账号', exact: true }).click()
  await page.getByRole('alert').filter({ hasText: '结果未确认' }).waitFor()
  const unknownID = writes.at(-1).requestId
  await page.getByRole('dialog').getByRole('button', { name: '关闭', exact: true }).click()
  await page.getByRole('button', { name: '继续原创建任务' }).click()
  assert.equal(await page.getByLabel('子账号用户名', { exact: true }).isDisabled(), true)
  await page.getByLabel('子账号初始密码', { exact: true }).fill('secret-password')
  await page.getByRole('checkbox', { name: /我授权这些子账号/ }).check()
  await page.getByRole('button', { name: '继续创建', exact: true }).click()
  await page.getByRole('button', { name: '管理', exact: true }).waitFor()
  assert.equal(writes.at(-1).requestId, unknownID, 'missing record after unknown response must retain the same request ID')
  children.items.splice(0); children.used = 0; failCreate = true
  await page.getByRole('button', { name: '刷新子账号', exact: true }).click()
  await page.getByRole('button', { name: '新增子账号', exact: true }).click()
  await page.getByLabel('子账号用户名', { exact: true }).fill('viewer-three')
  await page.getByLabel('子账号初始密码', { exact: true }).fill('secret-password')
  await page.getByRole('checkbox', { name: /我授权这些子账号/ }).check()
  await page.getByRole('button', { name: '创建子账号', exact: true }).click()
  await page.getByRole('alert').filter({ hasText: '创建结果未确认' }).waitFor()
  await page.getByRole('dialog').getByRole('button', { name: '关闭', exact: true }).click()
  children.items[0].completed = true; children.limit = 2
  await page.getByRole('button', { name: '刷新子账号', exact: true }).click()
  await page.getByRole('button', { name: '新增子账号', exact: true }).waitFor({ state: 'visible' })
  assert.equal(await page.getByRole('button', { name: '新增子账号', exact: true }).isDisabled(), false, 'authoritative completion must release the pending request lock')
  assert.equal(await page.evaluate(() => JSON.stringify(localStorage).includes('password')), false)
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
  assert.deepEqual(errors, [])
  console.log('playback children UI passed: durable retry, quota, pause, disable, password reset, revoke, mobile layout')
 } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
