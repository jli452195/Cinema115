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
  let failCreate = true
  await page.route('**/api/**', async route => {
   const req = route.request(), url = new URL(req.url()), p = url.pathname
   let data = {}
   if (p.startsWith('/api/admin/')) throw new Error('Owner called admin API')
   if (p === '/api/auth/me') data = { user: { id: 'owner', username: 'owner', role: 'user' } }
   else if (p === '/api/me/playback') data = { enabled: true, source: 'primary', canPreparePersonal: true, stage: 'ready', connected: true, uid: '123', directoryId: '456', embyName: 'owner' }
   else if (p === '/api/me/playback/children') {
    if (req.method() === 'POST') {
     const body = req.postDataJSON(); writes.push(body)
     if (!children.items.length) { children.used = 1; children.items.push({ requestId: body.requestId, name: body.name, userId: 'child', completed: false, revision: 1, stage: 'creating' }) }
     assert.equal(body.requestId, children.items[0].requestId)
     if (failCreate) { failCreate = false; return route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: 'Emby 创建结果未确认，请使用原任务重试' }) }) }
     children.items[0].completed = true; children.items[0].stage = 'ready'
    }
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
  await page.getByLabel('子账号用户名', { exact: true }).fill('viewer-one')
  await page.getByLabel('子账号初始密码', { exact: true }).fill('secret-password')
  await page.getByRole('checkbox', { name: /我授权这些子账号/ }).check()
  await page.getByRole('button', { name: '创建子账号', exact: true }).click()
  await page.getByRole('alert').filter({ hasText: '创建结果未确认' }).waitFor()
  assert.equal(await page.getByLabel('子账号初始密码', { exact: true }).inputValue(), '')
  assert.equal(await page.getByLabel('子账号用户名', { exact: true }).isDisabled(), true)
  const first = writes[0].requestId
  await page.reload()
  await page.getByRole('button', { name: '继续此创建任务' }).click()
  await page.getByLabel('子账号初始密码', { exact: true }).fill('secret-password')
  await page.getByRole('checkbox', { name: /我授权这些子账号/ }).check()
  await page.getByRole('button', { name: '继续创建', exact: true }).click()
  await page.getByRole('button', { name: '暂停播放', exact: true }).waitFor()
  assert.equal(writes[1].requestId, first)
  assert.equal(await page.getByLabel('子账号初始密码', { exact: true }).count(), 0)
  await page.getByRole('button', { name: '暂停播放', exact: true }).click()
  await page.getByRole('button', { name: '恢复播放', exact: true }).click()
  await page.getByRole('button', { name: '停用登录', exact: true }).click()
  await page.getByRole('button', { name: '启用登录', exact: true }).click()
  await page.getByRole('button', { name: '重置密码', exact: true }).click()
  await page.getByLabel('子账号新密码', { exact: true }).fill('new-password')
  await page.getByRole('button', { name: '保存新密码', exact: true }).click()
  await page.getByText('密码已更新；已登录设备不保证立即退出。', { exact: true }).waitFor()
  const count = writes.length
  await page.getByRole('button', { name: '移除子账号', exact: true }).click()
  assert.equal(writes.length, count)
  await page.getByRole('button', { name: '确认移除', exact: true }).click()
  await page.getByText('已撤销', { exact: true }).waitFor()
  assert.equal(children.used, 0)
  assert.equal(await page.getByRole('button', { name: '恢复播放', exact: true }).count(), 0)
  assert.equal(await page.evaluate(() => JSON.stringify(localStorage).includes('password')), false)
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
  assert.deepEqual(errors, [])
  console.log('playback children UI passed: durable retry, quota, pause, disable, password reset, revoke, mobile layout')
 } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
