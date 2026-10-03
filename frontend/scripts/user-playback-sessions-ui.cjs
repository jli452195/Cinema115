const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 }, serviceWorkers: 'block' })
  await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
  const writes = [], errors = []
  page.on('pageerror', e => errors.push(e.message))
  let generation = 7, cleared = false, reject = true
  let devicePolicy = { mode: 'heartbeat_takeover', silenceSeconds: 60 }
  const members = [{ userId: 'reader', embyId: 'a'.repeat(32), embyName: 'reader', source: 'primary' }]
  const sessions = () => cleared ? [] : [{ key: 'slot', userId: 'reader', item: '408398', generation, issued: true, updatedAt: Date.now(), expiresAt: Date.now() + 3600000 }]
  await page.route('**/api/**', async route => {
   const req = route.request(), p = new URL(req.url()).pathname
   let data, status = 200
   if (req.method() === 'POST') {
    writes.push({ path: p, body: req.postDataJSON() })
    assert.equal(p, '/api/admin/member-playback/playback-sessions/slot/release')
    if (reject) { status = 409; data = { error: '播放会话已变化，请刷新后重试' }; reject = false }
    else { cleared = true; data = { sessions: [] } }
   } else {
    data = p === '/api/auth/me' ? { user: { id: 'admin', role: 'admin', username: 'admin' } } : p.endsWith('/playback-sessions') ? { sessions: sessions(), devicePolicy } : p.endsWith('/accounts') ? { accounts: [] } : { members, settings: { enabled: true, libraryIds: [], allowDownload: false }, proxy: { listening: true } }
   }
   await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
  })
  await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/emby/users')
  await page.getByText('用户播放占用（1）', { exact: true }).click()
  await page.getByText('reader：条目 408398，已签发直链', { exact: true }).waitFor()
  await page.getByText('原设备静默 60 秒后', { exact: false }).waitFor()
  await page.getByRole('button', { name: '释放播放占用', exact: true }).click()
  generation = 8
  // Poll updates the visible list, but confirmation must retain generation 7.
  await page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/playback-sessions'), { timeout: 15000 })
  await page.getByRole('button', { name: '确认释放', exact: true }).click()
  await page.getByText('播放会话已变化，请刷新后重试', { exact: true }).waitFor()
  assert.equal(writes[0].body.generation, 7)
  await page.getByRole('button', { name: '取消', exact: true }).click()
  await page.getByRole('button', { name: '释放播放占用', exact: true }).click()
  await page.getByRole('button', { name: '确认释放', exact: true }).click()
  await page.getByText('暂无播放占用', { exact: true }).waitFor()
  assert.equal(writes[1].body.generation, 8)
  devicePolicy = { mode: 'parallel', silenceSeconds: 60 }
  await page.reload()
  await page.getByText('用户播放占用（0）', { exact: true }).click()
  await page.getByText('当前策略：多设备并行。', { exact: false }).waitFor()
  await page.setViewportSize({ width: 390, height: 844 })
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
  assert.deepEqual(errors, [])
  console.log('User playback sessions: list, stale generation protection, release, mobile layout passed')
 } finally { await browser.close() }
})().catch(e => { console.error(e); process.exit(1) })
