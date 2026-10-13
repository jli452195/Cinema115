// All API calls are mocked; release requests never reach real playback sessions.
const path = require('node:path'), os = require('node:os'), fs = require('node:fs'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  page.setDefaultTimeout(10000)
  await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
  const writes = [], reads = [], errors = []
  page.on('pageerror', e => errors.push(e.message))
  page.on('console', message => { if (message.type() === 'warning' && message.text().includes('[Vue warn]')) errors.push(message.text()) })
  const base = (userId, source, extra = {}) => ({ userId, embyId: userId.padEnd(32, 'a'), embyName: userId, source, accountId: '', embyState: 'active', disabled: false, childLimit: 3, childUsed: 0, stage: 'ready', driveStatus: { state: 'ready', message: '共享池动态调度，播放时核验' }, ...extra })
  const members = [base('reader', 'primary'), base('pool', 'assigned'), base('personal', 'personal'), base('child', 'personal_shared', { playbackOnly: true }), base('disabled', 'assigned', { embyState: 'disabled', disabled: true }), base('deleted', 'assigned', { embyState: 'deleted' }), base('very-long-user-name'.repeat(8), 'primary')]
  const session = (key, extra = {}) => ({ key, userId: 'reader', item: '408398', generation: 7, issued: true, updatedAt: Date.now(), expiresAt: Date.now() + 3600000, ...extra })
  let sessions = [session('slot')], devicePolicy = { mode: 'heartbeat_takeover', silenceSeconds: 60 }
  let failRead = false, reject = true, delayRead = false, delayRelease = false, resolveRead, resolveRelease
  await page.route('**/api/**', async route => {
   const req = route.request(), p = new URL(req.url()).pathname
   if (req.method() === 'POST') {
    writes.push({ path: p, body: req.postDataJSON() })
    assert.match(p, /\/playback-sessions\/[^/]+\/release$/)
    if (delayRelease) await new Promise(resolve => { resolveRelease = resolve })
    if (reject) return route.fulfill({ status: 409, json: { error: '播放会话已变化，请重新读取后重试' } })
    sessions = sessions.filter(s => s.key !== decodeURIComponent(p.split('/').at(-2)))
    return route.fulfill({ json: { sessions } })
   }
   reads.push(p)
   if (p.endsWith('/playback-sessions')) {
    const result = { sessions: [...sessions], devicePolicy }
    if (delayRead) await new Promise(resolve => { resolveRead = resolve })
    if (failRead) return route.fulfill({ status: 503, json: { error: '播放状态服务暂不可用' } })
    return route.fulfill({ json: result })
   }
   const data = p === '/api/auth/me' ? { user: { id: 'admin', role: 'admin', username: 'admin' } } : p.endsWith('/accounts') ? { accounts: [] } : { members, settings: { enabled: true, libraryIds: [], allowDownload: false }, proxy: { listening: true }, pending: [], jobs: [], leases: [] }
   await route.fulfill({ json: data })
  })
  const url = (process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/emby/users'
  await page.goto(url)
  const summary = page.getByRole('region', { name: '播放会话摘要' }), dialog = page.locator('dialog.playback-dialog[open]')
  const settle = async () => page.evaluate(async () => { await Promise.all(document.getAnimations().filter(a => Number.isFinite(a.effect?.getComputedTiming().endTime)).map(a => a.finished.catch(() => {}))) })
  const open = async () => { await summary.getByRole('button', { name: '查看会话' }).click(); await dialog.waitFor(); await settle() }
  const close = async () => { await dialog.getByRole('button', { name: '关闭播放会话' }).click(); await dialog.waitFor({ state: 'hidden' }) }
  const firstRow = () => dialog.locator('.playback-session-row').first()
  const refresh = async () => {
   const response = page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/playback-sessions'))
   await dialog.getByRole('button', { name: '重新读取', exact: true }).click(); await response
   await page.waitForFunction(() => document.querySelector('.playback-dialog-footer button')?.getAttribute('aria-disabled') === 'false')
  }
  await summary.locator('.playback-count').filter({ hasText: /^1$/ }).waitFor()
  assert.equal(writes.length, 0)
  const toolbar = page.getByRole('region', { name: '用户筛选' }), filters = toolbar.getByRole('group', { name: '筛选用户' })
  assert.equal(await filters.getByRole('radio').count(), 6)
  assert.equal(await filters.getByRole('radio', { name: '全部', exact: true }).isChecked(), true)
  const chooseFilter = async name => toolbar.locator('.users-filters label').filter({ has: page.getByRole('radio', { name, exact: true }) }).click()
  const assertIndicator = async () => {
   await settle()
   const indicator = await filters.locator('.users-filter-indicator').boundingBox(), selected = await filters.locator('label.is-selected').boundingBox()
   for (const key of ['x', 'y', 'width', 'height']) assert.ok(Math.abs(indicator[key] - selected[key]) < 1, `Indicator ${key} must match selected filter`)
  }
  await chooseFilter('主网盘')
  assert.ok(await filters.locator('.users-filter-indicator').evaluate(e => e.style.transition.includes('260ms')), 'Filter change reuses sliding transition')
  await assertIndicator()
  assert.equal(await page.locator('article.user-card:visible').count(), 2)
  await filters.getByRole('radio', { name: '主网盘', exact: true }).focus()
  await page.keyboard.press('ArrowRight')
  assert.equal(await filters.getByRole('radio', { name: '共享池', exact: true }).isChecked(), true)
  assert.equal(await page.locator('article.user-card:visible').count(), 2)
  await chooseFilter('全部')
  const toolbarHeight = await toolbar.evaluate(e => e.offsetHeight)
  await toolbar.getByLabel('用户管理说明').click()
  assert.equal(await toolbar.evaluate(e => e.offsetHeight), toolbarHeight)
  await toolbar.getByLabel('用户管理说明').click()
  await open()
  assert.equal(await dialog.getByRole('button', { name: '关闭播放会话' }).evaluate(e => e === document.activeElement), true)
  const boundsBefore = await dialog.boundingBox()
  await dialog.locator('summary').click()
  await dialog.getByText('原设备静默 60 秒后', { exact: false }).waitFor()
  assert.equal((await dialog.boundingBox()).height, boundsBefore.height)
  assert.equal(await firstRow().getByText('已签发直链', { exact: true }).isVisible(), true)
  assert.equal(await firstRow().getByText('正在播放', { exact: true }).count(), 0)
  await firstRow().getByRole('button', { name: '释放占用', exact: true }).click()
  await dialog.getByRole('button', { name: '取消', exact: true }).waitFor()
  assert.equal(await dialog.getByRole('button', { name: '取消', exact: true }).evaluate(e => e === document.activeElement), true)
  assert.equal(writes.length, 0, 'Selecting release must not submit')
  await page.keyboard.press('Escape')
  assert.equal(await dialog.isVisible(), true)
  assert.equal(await dialog.locator('.playback-release-confirm').count(), 0)
  // Poll changes the generation, invalidating the old confirmation instead of transferring it.
  await firstRow().getByRole('button', { name: '释放占用', exact: true }).click()
  sessions = [session('slot', { generation: 8 })]
  await page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/playback-sessions'), { timeout: 15000 })
  await dialog.getByText('所选会话已更新或结束，请重新选择。', { exact: true }).waitFor()
  assert.equal(await dialog.locator('.playback-release-confirm').count(), 0)
  assert.equal(writes.length, 0)
  assert.equal(await dialog.getByRole('button', { name: '重新读取' }).evaluate(e => e === document.activeElement), true)
  await firstRow().getByRole('button', { name: '释放占用', exact: true }).click()
  await dialog.getByRole('button', { name: '确认释放', exact: true }).click()
  await dialog.getByText('播放会话已变化，请重新读取后重试', { exact: true }).waitFor()
  assert.equal(writes[0].body.generation, 8)
  assert.equal(await dialog.getByRole('button', { name: '重新读取' }).evaluate(e => e === document.activeElement), true)
  // A regular refresh keeps the selection, error, focused cancel button and dialog dimensions.
  await dialog.getByRole('button', { name: '取消', exact: true }).focus()
  const focusedBefore = await page.evaluate(() => document.activeElement.outerHTML)
  await page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/playback-sessions'), { timeout: 15000 })
  assert.equal(await page.evaluate(() => document.activeElement.outerHTML), focusedBefore)
  assert.equal((await dialog.boundingBox()).height, boundsBefore.height)
  assert.equal(await dialog.getByText('播放会话已变化，请重新读取后重试', { exact: true }).isVisible(), true)
  // Keep a poll in flight while releasing: no focus loss, and its stale result must not resurrect the session.
  delayRead = true; resolveRead = undefined
  await page.waitForRequest(r => new URL(r.url()).pathname.endsWith('/playback-sessions'), { timeout: 15000 })
  for (let i = 0; !resolveRead && i < 50; i++) await page.waitForTimeout(20)
  assert.ok(resolveRead)
  assert.equal(await dialog.getByRole('button', { name: '取消', exact: true }).evaluate(e => e === document.activeElement), true)
  assert.equal(await dialog.getByRole('button', { name: '确认释放', exact: true }).isEnabled(), true)
  reject = false; delayRelease = true
  await dialog.getByRole('button', { name: '确认释放', exact: true }).click()
  await dialog.getByRole('button', { name: '正在释放…', exact: true }).waitFor()
  await page.keyboard.press('Escape')
  assert.equal(await dialog.isVisible(), true)
  assert.equal(await dialog.getByRole('button', { name: '关闭播放会话' }).isDisabled(), true)
  assert.equal(await dialog.getByRole('button', { name: '取消', exact: true }).isDisabled(), true)
  for (let i = 0; !resolveRelease && i < 50; i++) await page.waitForTimeout(20)
  assert.ok(resolveRelease); resolveRelease(); delayRelease = false
  await dialog.getByText('暂无播放会话', { exact: true }).waitFor()
  const oldRead = resolveRead; delayRead = false; resolveRead = undefined; oldRead()
  await page.waitForTimeout(100)
  assert.equal(await dialog.locator('.playback-session-row').count(), 0, 'Late read cannot restore released session')
  assert.equal(writes[1].body.generation, 8)
  assert.equal((await dialog.boundingBox()).height, boundsBefore.height)
  await close()
  assert.equal(await summary.getByRole('button', { name: '查看会话' }).evaluate(e => e === document.activeElement), true)
  assert.equal(await toolbar.evaluate(e => e.offsetHeight), toolbarHeight)
  // Zero remains accessible; loading errors are never represented as a fresh zero.
  await open(); assert.equal(await dialog.getByText('暂无播放会话', { exact: true }).isVisible(), true)
  delayRead = true; resolveRead = undefined
  await dialog.getByRole('button', { name: '重新读取', exact: true }).click()
  for (let i = 0; !resolveRead && i < 50; i++) await page.waitForTimeout(20)
  await close(); assert.ok(resolveRead); resolveRead(); delayRead = false
  sessions = [session('slot')]; failRead = true
  await page.reload()
  await summary.locator('.playback-count').filter({ hasText: '待核实' }).waitFor()
  await open(); await dialog.getByRole('alert').waitFor()
  assert.equal(await dialog.getByText('暂无播放会话', { exact: true }).count(), 0)
  failRead = false; devicePolicy = { mode: 'parallel', silenceSeconds: 60 }
  await refresh(); await dialog.getByRole('heading', { name: '播放会话 1', exact: true }).waitFor()
  // An unsuccessful poll disables release safely and recovers focus to refresh.
  failRead = true
  await firstRow().getByRole('button', { name: '释放占用', exact: true }).focus()
  await page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/playback-sessions'), { timeout: 15000 })
  await dialog.getByRole('alert').waitFor()
  assert.equal(await dialog.getByRole('button', { name: '重新读取' }).evaluate(e => e === document.activeElement), true)
  assert.match(await dialog.getByRole('alert').innerText(), /保留上次读取结果/)
  assert.equal(await firstRow().getByRole('button', { name: '释放占用', exact: true }).isDisabled(), true)
  await close(); assert.equal(await summary.locator('.playback-count').innerText(), '1')
  assert.equal(await summary.locator('.playback-stale').innerText(), '待核实')
  assert.equal(await toolbar.evaluate(e => e.offsetHeight), toolbarHeight)
  failRead = false
  await open(); await refresh()
  // A session disappearing restores focus instead of leaving it on the document.
  await firstRow().getByRole('button', { name: '释放占用', exact: true }).focus()
  sessions = []
  await page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/playback-sessions'), { timeout: 15000 })
  await dialog.getByText('暂无播放会话', { exact: true }).waitFor()
  assert.equal(await dialog.getByRole('button', { name: '重新读取' }).evaluate(e => e === document.activeElement), true)
  await close()
  // Long values, many sessions, stable scroll/focus, desktop/mobile palettes and short landscape.
  sessions = Array.from({ length: 12 }, (_, i) => session(`key-${i}`, { userId: members.at(-1).userId, item: '1234567890'.repeat(12), issued: i % 2 === 0 }))
  const screenshots = path.join(__dirname, '../test-results/users-toolbar')
  fs.mkdirSync(screenshots, { recursive: true })
  for (const [width, height] of [[1440,1000], [1024,768], [768,900], [390,844], [320,568], [667,375]]) {
   await page.setViewportSize({ width, height })
   for (const dark of [false,true]) {
    await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
    await settle()
    await assertIndicator()
    await chooseFilter('需关注'); await assertIndicator()
    await chooseFilter('全部'); await assertIndicator()
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    if (width < 768) {
     assert.equal(await filters.evaluate(e => getComputedStyle(e).gridTemplateColumns.split(' ').length), 3)
     assert.ok(await filters.locator('label').first().evaluate(e => e.offsetHeight) >= 44)
     assert.equal(await toolbar.getByRole('searchbox').evaluate(e => getComputedStyle(e).fontSize), '16px')
    }
    await toolbar.scrollIntoViewIfNeeded()
    if ([1440,390,320].includes(width)) await page.screenshot({ path: path.join(screenshots, `toolbar-${width}-${dark ? 'dark' : 'light'}.png`) })
    await open(); await refresh()
    const bounds = await dialog.boundingBox()
    assert.ok(bounds.x >= 0 && bounds.y >= 0 && bounds.x + bounds.width <= width + 1 && bounds.y + bounds.height <= height + 1)
    assert.equal(await dialog.evaluate(e => e.scrollWidth > e.clientWidth), false)
    assert.equal(await dialog.locator('.playback-dialog-body').evaluate(e => e.scrollWidth > e.clientWidth), false)
    assert.equal(await dialog.evaluate(e => getComputedStyle(e).backgroundColor), dark ? 'rgb(24, 33, 49)' : 'rgb(255, 255, 255)')
    assert.ok(await dialog.locator('.playback-dialog-body').evaluate(e => e.scrollHeight > e.clientHeight))
    const footer = await dialog.locator('footer').boundingBox()
    assert.ok(footer.y + footer.height <= height + 1)
    if ([1440,390,320].includes(width)) await page.screenshot({ path: path.join(screenshots, `sessions-${width}-${dark ? 'dark' : 'light'}.png`) })
    await close()
   }
  }
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await chooseFilter('共享池'); await assertIndicator()
  assert.equal(await filters.locator('.users-filter-indicator').evaluate(e => e.style.transition), 'none')
  await open()
  assert.equal(await dialog.evaluate(e => getComputedStyle(e).animationName), 'none')
  await close()
  assert.deepEqual(errors, [])
  assert.equal(writes.length, 2)
  console.log('Users toolbar/sessions: filters/keyboard, compact stable layout, 0/error/stale states, generation confirmation, polling/focus, mocked release, 6 viewports, light/dark and reduced motion passed')
 } finally { await browser.close() }
})().catch(e => { console.error(e); process.exit(1) })
