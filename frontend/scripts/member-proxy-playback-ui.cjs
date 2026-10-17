// All API traffic is mocked. Never changes real Emby users or playback sessions.
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
  const member = (userId, extra = {}) => ({ userId, embyId: userId.padEnd(32, 'a'), embyName: userId, username: userId, source: 'assigned', accountId: '', embyState: 'active', disabled: false, childLimit: 0, childUsed: 0, stage: 'ready', driveStatus: { state: 'ready', message: '共享池自动调度，播放时实时核验' }, ...extra })
  let members = [member('Reader'), member('Admin', { administrator: true }), member('Child', { playbackOnly: true, source: 'personal_shared', ownerName: 'Reader', ownerUserId: 'Reader' }), member('Missing', { embyState: 'missing' }), member('LongName', { embyName: '这是一个很长的测试用户名'.repeat(8) }), member('Disabled', { embyState: 'disabled', disabled: true })]
  const settings = { enabled: true, libraryIds: [], allowDownload: false }
  let enabled = true, revision = 0, mode = '', release, playbackGets = 0
  const observation = () => ({ userId: 'Reader', enabled, revision })
  await page.route('**/api/**', async route => {
   const req = route.request(), p = new URL(req.url()).pathname
   if (req.method() === 'POST') writes.push({ p, body: req.postDataJSON() }); else reads.push(p)
   if (p.endsWith('/proxy-playback')) {
    if (req.method() === 'POST') {
     if (mode === 'delay') await new Promise(resolve => { release = resolve })
     if (mode === 'rejected') return route.fulfill({ status: 400, json: { error: '身份已变化，请刷新核实' } })
     if (mode === 'stale') { revision += 2; return route.fulfill({ status: 409, json: { error: 'Settings changed', playback: observation() } }) }
     enabled = req.postDataJSON().enabled; revision++
     members = members.map(m => m.userId === 'Reader' ? { ...m, proxyPlaybackDisabled: !enabled, proxyPlaybackRevision: revision } : m)
     if (mode === 'uncertain') return route.fulfill({ status: 502, json: { unconfirmed: true, error: '提交结果待核实，请重新读取' } })
     if (mode === 'transport') return route.abort('failed')
    } else {
     playbackGets++
     if (mode === 'read-failed') return route.fulfill({ status: 503, json: { error: 'Emby 暂时无法连接' } })
    }
    return route.fulfill({ json: { playback: observation() } })
   }
   if (p === '/api/auth/me') return route.fulfill({ json: { user: { id: 'admin', role: 'admin', username: 'admin' } } })
   return route.fulfill({ json: { members, settings, proxy: { listening: true }, accounts: [], pending: [], jobs: [], sessions: [] } })
  })
  await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/users/list')
  const card = name => page.locator('article.user-card').filter({ has: page.getByRole('heading', { name, exact: true }) })
  const reader = card('Reader'), toggle = () => reader.getByRole('switch'), confirm = page.locator('dialog.account-confirm[open]')
  // Allow the 150–200ms theme/track transitions to settle before color screenshots.
  const settle = () => page.waitForTimeout(250)
  const geometry = () => reader.evaluate(el => ({ h: el.offsetHeight, footer: el.querySelector('footer').offsetTop, summary: el.querySelector('.member-drive-summary').offsetHeight }))
  const switches = () => writes.filter(w => w.p.endsWith('/proxy-playback'))
  const checked = async value => { await page.waitForFunction(value => document.querySelector('article[aria-label="Reader"] [role="switch"]')?.getAttribute('aria-checked') === String(value), value) }
  await toggle().waitFor()
  await reader.evaluate(el => el.dataset.testIdentity = 'same-card')
  const initial = await geometry()
  assert.equal(switches().length, 0)
  assert.equal(await toggle().getAttribute('aria-checked'), 'true')
  assert.equal(await card('Admin').getByRole('switch').count(), 0)
  assert.equal(await card('Child').getByRole('switch').count(), 0)
  assert.equal(await card('Disabled').getByRole('switch').getAttribute('aria-checked'), 'true')
  assert.equal(await card('Disabled').getByText('Emby 已停用', { exact: true }).count(), 1)
  assert.equal(await card('Admin').getByRole('img', { name: '账号已启用' }).count(), 1)
  assert.equal(await toggle().evaluate(el => el.offsetWidth >= 44 && el.offsetHeight >= 44), true)
  await toggle().click(); await confirm.waitFor()
  assert.match(await confirm.innerText(), /Reader/)
  assert.match(await confirm.innerText(), /子账号/)
  assert.match(await confirm.innerText(), /其他工具不受影响/)
  assert.match(await confirm.innerText(), /不修改 Emby 账号及权限/)
  assert.match(await confirm.innerText(), /关联子账号不受此开关影响/)
  assert.match(await confirm.innerText(), /已签发/)
  assert.equal(await confirm.getByRole('button', { name: '取消', exact: true }).evaluate(el => el === document.activeElement), true)
  await page.keyboard.press('Escape'); await confirm.waitFor({ state: 'hidden' })
  assert.equal(switches().length, 0)
  assert.equal(await toggle().evaluate(el => el === document.activeElement), true)
  assert.equal(await page.evaluate(() => document.body.style.overflow), '')
  // Pending leaves the confirmed position intact and suppresses duplicates.
  mode = 'delay'; await toggle().click(); await confirm.getByRole('button', { name: '暂停播放', exact: true }).click()
  await page.waitForFunction(() => document.querySelector('dialog.account-confirm')?.getAttribute('aria-busy') === 'true')
  assert.equal(await toggle().getAttribute('aria-checked'), 'true')
  assert.equal(await confirm.getByRole('button', { name: '提交中…' }).isDisabled(), true)
  await confirm.getByRole('button', { name: '提交中…' }).dispatchEvent('click')
  await page.keyboard.press('Escape'); assert.equal(await confirm.isVisible(), true)
  assert.deepEqual(await geometry(), initial)
  for (let i = 0; !release && i < 30; i++) await page.waitForTimeout(20)
  assert.ok(release); release(); mode = ''
  await confirm.waitFor({ state: 'hidden' }); await checked(false)
  assert.equal(switches().length, 1)
  assert.deepEqual(switches()[0].body, { enabled: false, expectedRevision: 0 })
  assert.equal(members[0].disabled, false)
  assert.equal(members[0].embyState, 'active')
  assert.deepEqual(await geometry(), initial)
  assert.equal(await reader.getAttribute('data-test-identity'), 'same-card')
  // Enable directly, with the native button keyboard activation.
  await toggle().focus(); await page.keyboard.press('Space'); await checked(true)
  assert.equal(await confirm.count(), 0)
  assert.deepEqual(switches().at(-1).body, { enabled: true, expectedRevision: 1 })
  // A confirmed rejection retains the original state and the confirmation dialog.
  mode = 'rejected'; await toggle().click(); await confirm.getByRole('button', { name: '暂停播放', exact: true }).click()
  await confirm.getByRole('alert').waitFor(); assert.equal(await toggle().getAttribute('aria-checked'), 'true')
  await confirm.getByRole('button', { name: '取消', exact: true }).click(); mode = ''
  // A stale tab receives the latest revision instead of overwriting a pause/resume cycle.
  mode = 'stale'; await toggle().click(); await confirm.getByRole('button', { name: '暂停播放', exact: true }).click()
  await confirm.getByRole('alert').filter({ hasText: 'Settings changed' }).waitFor()
  assert.equal(await toggle().getAttribute('aria-checked'), 'true')
  assert.equal(switches().at(-1).body.expectedRevision, 2)
  await confirm.getByRole('button', { name: '取消', exact: true }).click(); mode = ''
  // The editor draft cannot race an account switch.
  await reader.getByRole('button', { name: '管理用户 Reader' }).click()
  const editor = page.locator('dialog.member-dialog[open]')
  await editor.getByRole('spinbutton').fill('2')
  assert.equal(await toggle().isDisabled(), true)
  await editor.getByRole('button', { name: '取消', exact: true }).click()
  await editor.getByRole('button', { name: '放弃并关闭' }).click()
  // Ambiguous write is resolved via a read only operation, including read failure.
  mode = 'uncertain'; await toggle().click(); await confirm.getByRole('button', { name: '暂停播放', exact: true }).click()
  await confirm.getByRole('alert').waitFor()
  const count = switches().length
  assert.equal(await reader.getByRole('switch').count(), 0)
  assert.equal(await reader.getByRole('button', { name: '重新核实反代播放设置 Reader' }).count(), 1)
  mode = 'read-failed'; await confirm.getByRole('button', { name: '重新读取设置' }).click()
  await confirm.getByRole('alert').filter({ hasText: 'Emby 暂时无法连接' }).waitFor()
  assert.equal(switches().length, count)
  mode = ''; await confirm.getByRole('button', { name: '重新读取设置' }).click()
  await confirm.waitFor({ state: 'hidden' }); await checked(false)
  assert.equal(switches().length, count); assert.equal(playbackGets, 2)
  assert.deepEqual(await geometry(), initial)
  assert.equal(await toggle().evaluate(el => el === document.activeElement), true)
  mode = 'transport'; await toggle().click()
  await reader.getByRole('button', { name: '重新核实反代播放设置 Reader' }).waitFor()
  const transportCount = switches().length
  mode = ''; await reader.getByRole('button', { name: '重新核实反代播放设置 Reader' }).click(); await checked(true)
  assert.equal(switches().length, transportCount)
  assert.equal(switches().at(-1).body.expectedRevision, 5)
  assert.equal(members[0].disabled, false)
  assert.equal(members[0].embyState, 'active')
  // Re-entering the page retains the independent local setting without Emby mutation.
  await page.reload(); await checked(true)
  assert.equal(await card('Disabled').getByText('Emby 已停用', { exact: true }).count(), 1)
  await page.getByRole('button', { name: '关闭提示', exact: true }).evaluateAll(buttons => buttons.forEach(button => button.click()))
  await page.getByRole('button', { name: '关闭提示', exact: true }).first().waitFor({ state: 'hidden' })
  // Mobile, dark, long-name, focus and reduced-motion coverage.
  fs.mkdirSync('test-results', { recursive: true })
  for (const width of [1440, 390, 320]) {
   await page.setViewportSize({ width, height: width === 1440 ? 1000 : 844 })
   for (const dark of [false, true]) {
    await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
    await page.waitForFunction(() => !document.querySelector('article[aria-label="Reader"] .member-manage-button')?.disabled)
    await settle()
    await page.screenshot({ path: `test-results/proxy-playback-switch-${width}-${dark ? 'dark' : 'light'}.png`, fullPage: true })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    assert.equal(await card('Disabled').locator('.member-card-identity').evaluate(el => {
     const [identity, native] = el.children
     const a = identity.getBoundingClientRect(), b = native.getBoundingClientRect(), parent = el.getBoundingClientRect()
     return getComputedStyle(el).display === 'flex' && b.left - a.right >= 5 && b.right <= parent.right + 1
    }), true)
    await toggle().click(); await confirm.waitFor(); await settle()
    const b = await confirm.boundingBox()
    assert.ok(b.x >= 0 && b.x + b.width <= width)
    await page.screenshot({ path: `test-results/proxy-playback-confirm-${width}-${dark ? 'dark' : 'light'}.png` })
    await confirm.getByRole('button', { name: '取消', exact: true }).click()
    await confirm.waitFor({ state: 'hidden' })
   }
  }
  await page.emulateMedia({ reducedMotion: 'reduce' })
  assert.equal(await reader.locator('.account-switch-thumb').evaluate(el => getComputedStyle(el).transitionDuration), '0s')
  assert.deepEqual(errors, [])
  console.log('Proxy playback switches: confirmation, keyboard/focus, duplicate suppression, verified state, ambiguity/re-read, protected identities, draft, stable geometry and 320/390/1440 light/dark passed')
 } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
