const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 1000 }, serviceWorkers: 'block' })
  await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
  let policy = { mode: 'heartbeat_takeover', silenceSeconds: 60 }, failRead = true, failWrite = false
  const writes = [], errors = []
  page.on('pageerror', e => errors.push(e.message))
  await page.route('**/api/**', async route => {
   const req = route.request(), p = new URL(req.url()).pathname
   let data = {}, status = 200
   if (p === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
   else if (p.endsWith('/device-policy')) {
    if (req.method() === 'POST') { writes.push(req.postDataJSON()); if (failWrite) { status = 500; data = { error: '模拟保存失败' } } else { policy = req.postDataJSON(); data = policy } }
    else if (failRead) { status = 500; data = { error: '模拟读取失败' } } else data = policy
   } else if (p.endsWith('/proxy')) data = { config: { enabled: false, port: 8097, url: 'http://emby', accountId: '', directLinkCacheMinutes: 9 }, listening: false }
   else if (p.endsWith('/instant-upload')) data = { config: {}, records: [], running: false }
   else if (p.endsWith('/playback-cache/users')) data = { users: [], members: [] }
   else if (p.endsWith('/playback-sessions')) data = { sessions: [], devicePolicy: policy }
   else if (p.endsWith('/accounts')) data = { accounts: [] }
   else data = { members: [], settings: { enabled: true, libraryIds: [], allowDownload: false }, proxy: { listening: true } }
   await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
  })
  const url = (process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/emby/proxy'
  await page.goto(url)
  const save = page.getByRole('button', { name: '保存设备策略', exact: true })
  await page.getByText('模拟读取失败', { exact: false }).waitFor()
  assert.equal(await save.isDisabled(), true)
  failRead = false; await page.getByRole('button', { name: '重试读取', exact: true }).click()
  const seconds = page.getByLabel('心跳静默时间（秒）', { exact: true })
  await seconds.waitFor(); assert.equal(await seconds.inputValue(), '60')
  await page.getByText('默认 60 秒，可设为 30 至 180 秒。', { exact: false }).waitFor()
  for (const invalid of ['29', '181']) { await seconds.fill(invalid); await save.click(); assert.equal(await seconds.evaluate(el => el.validity.valid), false) }
  assert.equal(writes.length, 0)
  failWrite = true; await seconds.fill('30'); await save.click()
  await page.getByText('模拟保存失败', { exact: true }).waitFor()
  assert.equal(await seconds.inputValue(), '30'); assert.equal(policy.silenceSeconds, 60)
  failWrite = false
  for (const value of ['30', '180', '60']) {
   await seconds.fill(value)
   await Promise.all([page.waitForResponse(r => r.url().endsWith('/device-policy') && r.request().method() === 'POST'), save.click()])
   await page.reload(); await seconds.waitFor(); assert.equal(await seconds.inputValue(), value)
  }
  await page.locator('.proxy-segment label').filter({ hasText: '多设备并行' }).click()
  await save.click(); await page.getByText('设备策略已保存，对后续取链请求生效。', { exact: true }).waitFor()
  await page.reload(); await save.waitFor(); assert.equal(await seconds.count(), 0)
  assert.equal(policy.mode, 'parallel')
  await page.setViewportSize({ width: 390, height: 844 })
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
  assert.deepEqual(errors, [])
  console.log('Device policy mocked UI passed: read/write failures, 30/60/180, invalid bounds, mode switch, reload persistence, mobile')
 } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
