// Run against Vite; all API calls are mocked so no real users or settings are changed.
const path = require('node:path'), os = require('node:os'), fs = require('node:fs'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  page.setDefaultTimeout(10000)
  await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
  const errors = [], writes = [], reads = []
  page.on('pageerror', e => errors.push(e.message))
  const base = (userId, source = 'assigned', extra = {}) => ({ userId, embyId: userId.padEnd(32, '0'), embyName: userId, username: userId, source, accountId: '', embyState: 'active', disabled: false, childLimit: 3, childUsed: 2, stage: 'ready', driveStatus: { state: 'ready', message: '共享池自动调度，播放时实时核验' }, ...extra })
  let members = [
   base('PoolUser'), base('MainUser', 'primary', { childLimit: 0, childUsed: 0, driveStatus: { state: 'ready', message: '最近检查通过', checkedAt: '2026-10-12T10:00:00Z' } }),
   base('OverQuota', 'primary', { childLimit: 0, childUsed: 2, driveStatus: { state: 'ready', message: '最近检查通过', checkedAt: '2026-10-12T10:00:00Z' } }),
   base('UnreadQuota', 'primary', { childLimit: 0, childUsed: undefined, driveStatus: { state: 'pending', message: '尚无网盘检查记录' } }),
   base('AdminUser', 'assigned', { administrator: true }),
   // Deliberately inconsistent partial boolean to exercise the authoritative Emby state.
   base('DisabledUser', 'assigned', { embyState: 'disabled', disabled: false }),
   base('ChildUser', 'personal_shared', { playbackOnly: true, ownerUserId: 'PoolUser', ownerName: 'PoolUser', childRevoked: true }),
   base('LongUser', 'personal', { embyName: '很长的用户名'.repeat(14), embyId: 'id'.repeat(80), personalDrive: { connected: false }, driveStatus: { state: 'invalid_login', message: '个人网盘登录已失效，请在个人中心重新扫码并核实目录。', checkedAt: '2026-10-12T10:00:00Z' } }),
   ...['deleted', 'missing', 'other_server'].map(state => base(state, 'assigned', { embyState: state })),
  ]
  const settings = { enabled: true, libraryIds: [], allowDownload: false }
  let failSave = false, failCheck = false, failList = false, delayCheck = false, releaseCheck
  await page.route('**/api/**', async route => {
   const req = route.request(), p = new URL(req.url()).pathname, body = req.postDataJSON()
   if (req.method() === 'POST') writes.push({ p, body }); else reads.push(p)
   if (p.endsWith('/users') && failList) return route.fulfill({ status: 503, json: { error: 'Emby 暂时无法连接' } })
   if (p.endsWith('/sync') && failCheck) return route.fulfill({ status: 503, json: { error: '网盘检查失败，请重试' } })
   if (p.endsWith('/sync') && delayCheck) await new Promise(resolve => { releaseCheck = resolve })
   if (p.endsWith('/settings') && req.method() === 'POST') {
    if (failSave) return route.fulfill({ status: 409, json: { error: '配置已变化，请重新核实' } })
    const id = decodeURIComponent(p.split('/').at(-2))
    members = members.map(m => m.userId === id ? { ...m, source: body.source, childLimit: body.childLimit } : m)
   }
   let data = { members, settings, proxy: { listening: true }, pending: [], jobs: [], leases: [] }
   if (p === '/api/auth/me') data = { user: { id: 'admin', role: 'admin', username: 'admin' } }
   if (p.endsWith('/accounts')) data = { accounts: [] }
   if (p.endsWith('/sync')) data = { settings, members: members.map(({ embyState, embyCheckedAt, ...m }) => ({ ...m, disabled: false })) }
   if (p.endsWith('/deletions/preview')) data = { key: body.key, name: body.key, fingerprint: 'mock', children: 1, personalDrives: 1, sessions: 0, embyStates: { [body.key]: body.key === 'deleted' ? 'deleted' : 'active', ChildUser: 'active' }, targets: [{ key: body.key, name: body.key, embyId: 'a'.repeat(32), remoteDeleted: body.key === 'deleted' }, { key: 'ChildUser', name: 'ChildUser', embyId: 'b'.repeat(32) }] }
   await route.fulfill({ json: data })
  })
  await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/users/list')
  const card = id => page.locator('article.user-card').filter({ has: page.getByRole('heading', { name: id, exact: true }) })
  const pool = card('PoolUser'), modal = page.locator('dialog.member-dialog[open]')
  const settle = async () => page.evaluate(async () => { await Promise.all(document.getAnimations().filter(a => Number.isFinite(a.effect?.getComputedTiming().endTime)).map(a => a.finished.catch(() => {}))) })
  const open = async id => { await card(id).getByRole('button', { name: /^管理用户|^查看记录/ }).click(); await modal.waitFor(); await settle() }
  const choose = async name => modal.locator('.member-source-option').filter({ has: page.getByRole('radio', { name, exact: true }) }).click()
  const close = async () => { await modal.getByRole('button', { name: '关闭用户管理' }).click(); await modal.waitFor({ state: 'hidden' }) }
  const noOverflow = async () => assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
  await pool.waitFor()
  assert.equal(await page.locator('details').filter({ has: page.locator('summary', { hasText: '待核实 / 待清理记录' }) }).getAttribute('open'), null)
  assert.equal(writes.length, 0, 'Initial page load must be read-only')
  assert.equal(await page.locator('article.user-card input, article.user-card select').count(), 0)
  assert.match(await pool.innerText(), /播放时自动分配/)
  assert.equal(await pool.locator('time').count(), 0)
  assert.equal(await pool.getByRole('button').count(), 1)
  assert.equal(await pool.getByRole('switch').count(), 1)
  assert.equal(await pool.getByRole('button', { name: '检查网盘', exact: true }).count(), 0)
  assert.doesNotMatch(await pool.innerText(), /Emby ID/)
  assert.match(await card('MainUser').innerText(), /未开放子账号/)
  assert.match(await card('OverQuota').innerText(), /子账号\s+2 \/ 0/)
  assert.match(await card('UnreadQuota').innerText(), /子账号\s+未读取 \/ 0/)
  assert.equal(await card('MainUser').locator('time').getAttribute('datetime'), '2026-10-12T10:00:00Z')
  const initialHeight = await pool.evaluate(e => e.offsetHeight)
  assert.ok(initialHeight <= 230, `Summary card too tall: ${initialHeight}`)
  assert.equal(await pool.evaluate(e => getComputedStyle(e.parentElement.parentElement).gridTemplateColumns.split(' ').length), 3)
  assert.ok(await pool.locator('.member-card-footer button').evaluate(e => e.offsetHeight) >= 44)
  console.log('Summary layout passed')
  // The dialog retains health checks, full identity and actionable failure feedback.
  await open('PoolUser')
  await modal.locator('summary').click()
  assert.match(await modal.locator('.member-id').innerText(), new RegExp(members[0].embyId))
  await modal.locator('summary').click()
  await modal.getByRole('button', { name: '检查网盘' }).click()
  await modal.getByText('已更新网盘检查结果。', { exact: true }).waitFor()
  failCheck = true
  await modal.getByRole('button', { name: '检查网盘' }).click()
  await modal.getByRole('alert').filter({ hasText: '网盘检查失败' }).waitFor()
  failCheck = false
  await close()
  await page.evaluate(() => { document.body.style.overflow = 'auto' })
  await open('PoolUser')
  console.log('Opened settings')
  assert.equal(await modal.getByRole('button', { name: '关闭用户管理' }).evaluate(e => e === document.activeElement), true)
  await choose('个人网盘')
  await modal.getByRole('spinbutton', { name: '子账号名额' }).fill('5')
  const usersBeforeFocus = reads.filter(p => p.endsWith('/users')).length
  const statusesBeforeFocus = reads.filter(p => p.endsWith('/drive-status')).length
  await page.evaluate(() => window.dispatchEvent(new Event('focus')))
  await page.waitForFunction(() => !!document.querySelector('.member-source-option.is-selected input[value="personal"]'))
  await page.waitForTimeout(200)
  assert.equal(reads.filter(p => p.endsWith('/users')).length, usersBeforeFocus)
  assert.ok(reads.filter(p => p.endsWith('/drive-status')).length > statusesBeforeFocus)
  assert.equal(await modal.getByRole('spinbutton').inputValue(), '5')
  delayCheck = true
  await modal.getByRole('button', { name: '检查网盘' }).click()
  await page.keyboard.press('Escape')
  assert.equal(await modal.isVisible(), true)
  assert.equal(await modal.getByRole('button', { name: '关闭用户管理' }).isDisabled(), true)
  for (let i = 0; !releaseCheck && i < 20; i++) await page.waitForTimeout(20)
  assert.ok(releaseCheck); releaseCheck(); delayCheck = false
  await modal.getByText('已更新网盘检查结果。', { exact: true }).waitFor()
  assert.equal(await modal.getByRole('radio', { name: '个人网盘', exact: true }).isChecked(), true)
  assert.equal(await modal.getByRole('spinbutton').inputValue(), '5')
  // Failed save retains the draft and baseline; a successful save updates the summary.
  failSave = true
  await modal.getByRole('button', { name: '保存更改' }).click()
  await modal.getByRole('alert').waitFor()
  assert.equal(await modal.getByRole('spinbutton').inputValue(), '5')
  const payload = writes.filter(w => w.p.endsWith('/PoolUser/settings')).at(-1).body
  assert.deepEqual(payload, { source: 'personal', accountId: '', childLimit: 5, expected: { source: 'assigned', accountId: '', childLimit: 3 } })
  failSave = false
  await modal.getByRole('button', { name: '保存更改' }).click()
  await modal.getByText('设置已保存。', { exact: true }).waitFor()
  assert.equal(await modal.getByRole('button', { name: '保存更改' }).isDisabled(), true)
  await close()
  assert.equal(await pool.getByRole('button', { name: '管理用户 PoolUser' }).evaluate(e => e === document.activeElement), true)
  assert.equal(await page.evaluate(() => document.body.style.overflow), 'auto')
  assert.equal(await pool.evaluate(e => e.offsetHeight), initialHeight)
  assert.match(await pool.innerText(), /个人网盘/)
  console.log('Save and health draft passed')
  // ESC, backdrop and cancel must protect drafts, restore edit focus and discard explicitly.
  await open('PoolUser'); await choose('主网盘')
  await page.keyboard.press('Escape')
  await modal.getByRole('button', { name: '继续编辑' }).waitFor()
  assert.equal(await modal.getByRole('button', { name: '继续编辑' }).evaluate(e => e === document.activeElement), true)
  await page.keyboard.press('Escape')
  assert.equal(await modal.getByRole('button', { name: '继续编辑' }).count(), 0)
  assert.equal(await modal.getByRole('radio', { name: '主网盘', exact: true }).evaluate(e => e === document.activeElement), true)
  await page.mouse.click(4, 4)
  await modal.getByRole('button', { name: '继续编辑' }).click()
  await modal.getByRole('button', { name: '取消', exact: true }).click()
  await modal.getByRole('button', { name: '放弃并关闭' }).click()
  await modal.waitFor({ state: 'hidden' })
  await open('PoolUser')
  assert.equal(await modal.getByRole('radio', { name: '个人网盘', exact: true }).isChecked(), true)
  await close()
  console.log('Discard controls passed')
  // Different roles expose only their appropriate controls.
  await open('AdminUser')
  assert.equal(await modal.getByRole('radio').count(), 2)
  assert.equal(await modal.getByRole('spinbutton').count(), 0)
  await modal.locator('summary').click()
  assert.equal(await modal.getByRole('button', { name: '预览删除影响' }).count(), 0)
  await close()
  await open('DisabledUser')
  assert.equal(await modal.getByRole('radio', { name: '主网盘', exact: true }).isDisabled(), true)
  assert.equal(await modal.getByRole('spinbutton').isDisabled(), false)
  await modal.getByRole('button', { name: '检查网盘' }).click()
  await modal.getByText('已更新网盘检查结果。', { exact: true }).waitFor()
  assert.equal(await modal.getByRole('radio', { name: '主网盘', exact: true }).isDisabled(), true)
  await close()
  assert.match(await card('DisabledUser').innerText(), /已停用/)
  await open('ChildUser')
  assert.equal(await modal.getByRole('radio').count(), 0)
  assert.equal(await modal.getByRole('spinbutton').count(), 0)
  assert.equal(await modal.getByRole('button', { name: '保存更改' }).count(), 0)
  assert.match(await modal.innerText(), /共享已撤销/)
  await close()
  // Viewing children closes the flyout and moves focus to the visible filter.
  await open('PoolUser'); await modal.getByRole('button', { name: '查看子账号' }).click()
  await modal.waitFor({ state: 'hidden' })
  assert.equal(await page.getByRole('radio', { name: '子账号', exact: true }).isChecked(), true)
  assert.equal(await page.getByRole('radio', { name: '子账号', exact: true }).evaluate(e => e === document.activeElement), true)
  assert.equal(await card('ChildUser').isVisible(), true)
  await page.getByRole('button', { name: '显示全部用户' }).click()
  // Delete remains an impact preview, with exactly one modal and restored focus.
  await open('PoolUser'); await modal.locator('summary').click()
  await modal.getByRole('button', { name: '预览删除影响' }).click()
  const deletion = page.locator('dialog.deletion-dialog[open]')
  await deletion.getByText('Emby 账号仍存在，将永久删除账号及观看记录', { exact: true }).first().waitFor()
  assert.equal(await page.locator('dialog[open]').count(), 1)
  await deletion.getByRole('button', { name: '关闭删除确认' }).click()
  assert.equal(await pool.getByRole('button', { name: '管理用户 PoolUser' }).evaluate(e => e === document.activeElement), true)
  assert.equal(await page.evaluate(() => document.body.style.overflow), 'auto')
  console.log('Role controls and handoff passed')
  const review = page.locator('details').filter({ has: page.locator('summary', { hasText: '待核实 / 待清理记录' }) })
  if (await review.getAttribute('open') === null) await review.locator('summary').click()
  await open('deleted')
  assert.equal(await modal.getByRole('radio').count(), 0)
  assert.equal(await modal.getByRole('button', { name: '检查网盘' }).count(), 0)
  await modal.locator('summary').click()
  await modal.getByRole('button', { name: '预览清理影响' }).click()
  await deletion.getByRole('button', { name: '关闭删除确认' }).click()
  failList = true
  await page.getByRole('button', { name: '刷新状态', exact: true }).click()
  await page.getByText('Emby 暂时无法连接', { exact: true }).waitFor()
  assert.equal(await page.locator('article.user-card .member-account-badge').filter({ hasText: '待核实' }).count(), members.length)
  failList = false
  await page.getByRole('button', { name: '刷新状态', exact: true }).click()
  await pool.getByRole('button', { name: '管理用户 PoolUser' }).waitFor()
  console.log('Refresh recovery passed')
  const screenshots = path.join(__dirname, '../test-results/member-cards')
  fs.mkdirSync(screenshots, { recursive: true })
  // Real responsive geometry, dialog scrolling and both palettes.
  for (const [width, height, columns] of [[1440, 1000, 3], [1024, 768, 3], [768, 900, 2], [390, 844, 1], [320, 568, 1], [667, 375, 1]]) {
   await page.setViewportSize({ width, height }); await noOverflow()
   assert.equal(await pool.evaluate(e => getComputedStyle(e.parentElement.parentElement).gridTemplateColumns.split(' ').length), columns)
   for (const dark of [false, true]) {
    await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark); await settle()
    assert.ok(await pool.getByRole('button', { name: '管理用户 PoolUser' }).evaluate(e => e.getBoundingClientRect().height) >= 44)
    if ([1440, 390, 320].includes(width)) {
     await page.mouse.move(0, 0)
     await page.evaluate(() => window.scrollTo(0, 0))
     await page.screenshot({ path: path.join(screenshots, `cards-${width}-${dark ? 'dark' : 'light'}.png`), fullPage: true })
     await card('MainUser').screenshot({ path: path.join(screenshots, `card-main-${width}-${dark ? 'dark' : 'light'}.png`) })
    }
    await open('很长的用户名'.repeat(14))
    await modal.locator('summary').click()
    const bounds = await modal.boundingBox()
    assert.ok(bounds.x >= 0 && bounds.y >= 0 && bounds.x + bounds.width <= width + 1 && bounds.y + bounds.height <= height + 1)
    assert.equal(await modal.evaluate(e => e.scrollWidth > e.clientWidth), false)
    assert.equal(await modal.locator('.member-dialog-body').evaluate(e => e.scrollWidth > e.clientWidth), false)
    const footer = await modal.locator('footer').boundingBox()
    assert.ok(footer.y + footer.height <= height + 1)
    const surface = await modal.evaluate(e => getComputedStyle(e).backgroundColor.match(/\d+/g).map(Number))
    assert.ok(surface.every(channel => dark ? channel < 80 : channel === 255), 'Dialog must follow the active light/dark palette')
    await modal.locator('.member-dialog-body').evaluate(e => { e.scrollTop = e.scrollHeight })
    if ([1440, 390, 320].includes(width)) await page.screenshot({ path: path.join(screenshots, `dialog-${width}-${dark ? 'dark' : 'light'}.png`) })
    await close(); await noOverflow()
   }
  }
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await open('PoolUser')
  assert.equal(await modal.evaluate(e => getComputedStyle(e).animationName), 'none')
  await close()
  assert.equal(writes.some(w => w.p.endsWith('/deletions')), false, 'Must never perform a deletion in UI review')
  assert.equal(writes.some(w => /policy|permission|authorize/.test(w.p)), false)
  assert.deepEqual(errors, [])
  console.log('User cards: compact layout, quota edge cases, dialog identity/checks, save baseline/failure, health draft, ESC/backdrop/focus, roles, preview handoff, refresh recovery, 44px targets, 6 viewports, light/dark and reduced motion passed')
 } finally { await browser.close() }
})().catch(e => { console.error(e); process.exit(1) })
