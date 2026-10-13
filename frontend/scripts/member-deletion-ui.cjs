// Every API call is mocked. This script never deletes real Emby users or files.
const path = require('node:path'), os = require('node:os'), fs = require('node:fs'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block', timezoneId: 'Asia/Shanghai' })
  page.setDefaultTimeout(10000)
  await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
  const errors = [], previews = [], submissions = [], reads = []
  page.on('pageerror', e => errors.push(e.message))
  page.on('console', m => { if (m.type() === 'warning' && m.text().includes('[Vue warn]')) errors.push(m.text()) })
  const target = (key, extra = {}) => ({ key, userId: key, name: key, embyId: key.padEnd(32, 'a'), completed: false, remoteDeleted: false, ...extra })
  const job = (id, name, extra = {}) => ({ id, name, fingerprint: `fp-${id}`, targets: [target(name), target(`${name}-child`, { ownerId: name })], stage: 'pending', message: '部分步骤未确认，请核实后继续原任务', createdAt: 1791849600, ...extra })
  const base = (userId, extra = {}) => ({ userId, embyId: userId.padEnd(32, 'a'), embyName: userId, source: 'primary', embyState: 'active', disabled: false, childLimit: 2, childUsed: 1, stage: 'ready', driveStatus: { state: 'ready', message: '最近检查通过' }, ...extra })
  const members = [base('Reader'), base('Admin', { administrator: true }), base('Child', { playbackOnly: true, source: 'personal_shared', ownerUserId: 'Reader' })]
  let pending = [target('orphan', { embyId: '' })]
  let jobs = [job('original-task', 'old-user', { targets: [target('old-user', { completed: true, remoteDeleted: true }), target('old-user-child')], createdAt: 1791849601 }), ...Array.from({ length: 4 }, (_, i) => job(`done-${i}`, `done-user-${i}`, { stage: 'completed', message: 'Emby 用户及本地业务关联已清理', createdAt: 1791849600 - i }))]
  let failList = false, failPreview = false, failDelete = false, partialDelete = false, delayPreview = false, delayDelete = false, delayList = false
  let resolvePreview, resolveDelete, resolveList
  await page.route('**/api/**', async route => {
   const req = route.request(), p = new URL(req.url()).pathname, body = req.postDataJSON()
   if (p.endsWith('/deletions/preview')) {
    previews.push(body)
    const result = { key: body.key, name: body.key, fingerprint: `fp-preview-${body.key}`, children: body.key === 'orphan' ? 0 : 1, personalDrives: 1, sessions: 1, embyStates: { [body.key]: 'active', Child: 'active' }, targets: [target(body.key), ...(body.key === 'orphan' ? [] : [target('Child', { ownerId: body.key })])] }
    if (delayPreview) await new Promise(r => { resolvePreview = r })
    if (failPreview) return route.fulfill({ status: 503, json: { error: '无法核验 Emby 用户，未开始删除' } })
    return route.fulfill({ json: result })
   }
   if (p.endsWith('/deletions') && req.method() === 'POST') {
    submissions.push(body)
    if (delayDelete) await new Promise(r => { resolveDelete = r })
    const original = jobs.find(j => j.id === body.key)
    const result = original ? { ...original, stage: partialDelete ? 'pending' : 'completed', message: partialDelete ? '清理尚未完成，请继续原任务' : 'Emby 用户及本地业务关联已清理' } : job('new-task', body.confirmName, { fingerprint: body.fingerprint, targets: [target(body.key), target('Child', { ownerId: body.key })], stage: failDelete ? 'pending' : 'completed' })
    jobs = [...jobs.filter(j => j.id !== result.id), result]
    pending = pending.filter(t => !result.targets.some(x => x.key === t.key))
    if (failDelete) return route.fulfill({ status: 503, json: { error: '删除结果未确认' } })
    return route.fulfill({ status: partialDelete ? 202 : 200, json: { job: result } })
   }
   if (p.endsWith('/deletions')) {
    reads.push(p)
    const result = { pending: [...pending], jobs: [...jobs] }
    if (delayList) await new Promise(r => { resolveList = r })
    if (failList) return route.fulfill({ status: 503, json: { error: '清理任务服务暂不可用' } })
    return route.fulfill({ json: result })
   }
   assert.equal(req.method(), 'GET', `Unexpected write to ${p}`)
   const data = p === '/api/auth/me' ? { user: { id: 'admin', role: 'admin', username: 'admin' } } : p.endsWith('/accounts') ? { accounts: [] } : { members, settings: { enabled: true, libraryIds: [], allowDownload: false }, proxy: { listening: true }, sessions: [], devicePolicy: { mode: 'parallel', silenceSeconds: 60 } }
   await route.fulfill({ json: data })
  })
  await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/emby/users')
  const entry = page.locator('.cleanup-trigger'), dialog = page.locator('dialog.deletion-dialog[open]')
  const settle = async () => page.evaluate(async () => { await Promise.all(document.getAnimations().filter(a => Number.isFinite(a.effect?.getComputedTiming().endTime)).map(a => a.finished.catch(() => {}))) })
  const idle = async () => page.waitForFunction(() => document.querySelector('.cleanup-footer button')?.getAttribute('aria-disabled') === 'false')
  const open = async () => { await entry.click(); await dialog.waitFor(); await idle(); await settle() }
  const close = async () => { await dialog.getByRole('button', { name: /^关闭/ }).click(); await dialog.waitFor({ state: 'hidden' }) }
  const refresh = async () => { const response = page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/deletions')); await dialog.getByRole('button', { name: '刷新任务' }).click(); await response; await idle() }
  const back = async () => { await dialog.getByRole('button', { name: '返回任务列表' }).click(); await dialog.getByRole('heading', { name: '账号清理', exact: true }).waitFor(); await idle() }
  const previewReader = async () => {
   await page.locator('article.user-card').filter({ has: page.getByRole('heading', { name: 'Reader', exact: true }) }).getByRole('button', { name: '管理用户 Reader' }).click()
   const management = page.locator('dialog.member-dialog[open]'); await management.locator('summary').click(); await management.getByRole('button', { name: '预览删除影响' }).click()
   await dialog.getByText('Emby 账号仍存在，将永久删除账号及观看记录', { exact: true }).first().waitFor()
  }
  await entry.locator('.cleanup-badge').filter({ hasText: /^2$/ }).waitFor()
  assert.equal(await page.locator('select[aria-label="选择删除用户"]').count(), 0)
  assert.equal(await page.getByText('删除用户与未完成账号清理', { exact: true }).count(), 0)
  assert.equal(await entry.evaluate(e => !!e.closest('.management-heading')), true)
  assert.deepEqual(submissions, [])
  await page.evaluate(() => { document.body.style.overflow = 'auto' })
  await open()
  assert.equal(await page.locator('dialog[open]').count(), 1)
  assert.equal(await dialog.getByRole('button', { name: '关闭账号清理' }).evaluate(e => e === document.activeElement), true)
  assert.equal(await dialog.locator('.cleanup-history').getAttribute('open'), null)
  assert.equal(await dialog.getByRole('region', { name: '未完成创建' }).count(), 1)
  assert.match(await dialog.innerText(), /未完成创建\s+1[\s\S]*待继续清理\s+1/)
  const originalHeight = (await dialog.boundingBox()).height
  await dialog.locator('.cleanup-history > summary').click()
  assert.equal(await dialog.locator('.cleanup-history-row').count(), 4)
  await dialog.locator('.cleanup-history-row').first().locator('summary').click()
  assert.equal(await dialog.locator('.cleanup-history-row').first().getByText('Emby 用户及本地业务关联已清理').isVisible(), true)
  assert.equal((await dialog.boundingBox()).height, originalHeight)
  assert.match(await dialog.locator('.cleanup-history-row time').first().innerText(), /10\/13/)
  // Preview is read-only; closing/back during the request prevents a late preview being reused.
  delayPreview = true; resolvePreview = undefined
  await dialog.getByRole('button', { name: '核查并预览' }).click()
  await dialog.getByText('正在核验删除影响…').waitFor()
  for (let i = 0; !resolvePreview && i < 50; i++) await page.waitForTimeout(20)
  await back(); assert.ok(resolvePreview); delayPreview = false; resolvePreview()
  await page.waitForTimeout(100)
  assert.equal(await dialog.locator('form').count(), 0)
  assert.equal(await dialog.getByRole('heading', { name: '账号清理', exact: true }).isVisible(), true)
  failPreview = true
  await dialog.getByRole('button', { name: '核查并预览' }).click()
  await dialog.getByRole('alert').waitFor()
  assert.equal(await dialog.locator('form').count(), 0)
  assert.equal(submissions.length, 0)
  await close(); failPreview = false
  assert.equal(await entry.evaluate(e => e === document.activeElement), true)
  assert.equal(await page.evaluate(() => document.body.style.overflow), 'auto')
  // Name and child acknowledgement are both mandatory; failed writes cannot be blindly repeated.
  await previewReader()
  assert.equal(await page.locator('dialog[open]').count(), 1)
  const confirm = () => dialog.getByRole('button', { name: /确认永久删除|确认继续清理/ })
  await dialog.getByRole('textbox').fill('Reader ')
  await dialog.getByRole('checkbox').check(); assert.equal(await confirm().isDisabled(), true)
  await dialog.getByRole('textbox').fill('Reader')
  await dialog.getByRole('checkbox').uncheck(); assert.equal(await confirm().isDisabled(), true)
  await dialog.getByRole('checkbox').check(); assert.equal(await confirm().isEnabled(), true)
  failDelete = true
  await confirm().click(); await dialog.getByRole('alert').waitFor()
  assert.equal(submissions.length, 1)
  assert.deepEqual(submissions[0], { key: 'Reader', fingerprint: 'fp-preview-Reader', confirmName: 'Reader', includeChildren: true })
  assert.equal(await confirm().isDisabled(), true)
  assert.equal(await dialog.getByRole('textbox').isDisabled(), true)
  await dialog.locator('form').evaluate(form => form.requestSubmit()); assert.equal(submissions.length, 1)
  await back(); failDelete = false
  const recovered = dialog.locator('[data-cleanup-key="new-task"]')
  await recovered.getByRole('button', { name: '继续原任务' }).click()
  await dialog.getByRole('textbox').fill('Reader'); await dialog.getByRole('checkbox').check()
  delayDelete = true; resolveDelete = undefined
  await confirm().click(); await dialog.getByRole('button', { name: '正在确认删除结果…' }).waitFor()
  await page.keyboard.press('Escape'); assert.equal(await dialog.isVisible(), true)
  assert.equal(await dialog.getByRole('button', { name: '返回任务列表' }).isDisabled(), true)
  assert.equal(await dialog.getByRole('button', { name: '关闭删除确认' }).isDisabled(), true)
  await page.mouse.click(1, 1); assert.equal(await dialog.isVisible(), true)
  for (let i = 0; !resolveDelete && i < 50; i++) await page.waitForTimeout(20)
  assert.ok(resolveDelete); resolveDelete(); delayDelete = false
  await dialog.getByRole('heading', { name: '账号清理', exact: true }).waitFor(); await idle()
  assert.deepEqual(submissions[1], { key: 'new-task', fingerprint: 'fp-preview-Reader', confirmName: 'Reader', includeChildren: true })
  assert.equal((await dialog.boundingBox()).height, originalHeight)
  assert.equal(await dialog.getByRole('button', { name: '关闭账号清理' }).evaluate(e => e === document.activeElement), true)
  // Resume snapshots the original task and keeps the completed-target progress.
  await dialog.locator('[data-cleanup-key="original-task"]').getByRole('button', { name: '继续原任务' }).click()
  assert.equal(await dialog.getByText('已清理', { exact: true }).isVisible(), true)
  await dialog.getByRole('textbox').fill('old-user'); await dialog.getByRole('checkbox').check()
  partialDelete = true; await confirm().click()
  await dialog.getByRole('heading', { name: '账号清理', exact: true }).waitFor(); await idle()
  assert.deepEqual(submissions[2], { key: 'original-task', fingerprint: 'fp-original-task', confirmName: 'old-user', includeChildren: true })
  assert.equal(await dialog.locator('[data-cleanup-key="original-task"]').isVisible(), true)
  partialDelete = false
  // Read failure retains history and pending counts, but stale actions are disabled.
  failList = true; await refresh()
  assert.match(await dialog.getByRole('alert').innerText(), /保留上次读取结果/)
  assert.equal(await dialog.getByRole('button', { name: '核查并预览' }).isDisabled(), true)
  assert.equal(await entry.locator('.cleanup-badge').innerText(), '待核实')
  await close(); await page.reload()
  await entry.locator('.cleanup-badge').filter({ hasText: '待核实' }).waitFor(); await open()
  assert.equal(await dialog.getByText('暂无待处理清理', { exact: true }).count(), 0)
  failList = false; pending = []; jobs = jobs.filter(j => j.stage === 'completed'); await refresh()
  await dialog.getByText('暂无待处理清理', { exact: true }).waitFor()
  assert.equal(await entry.locator('.cleanup-badge').count(), 0)
  assert.equal(await dialog.locator('.cleanup-history').getAttribute('open'), null)
  // Closing a read leaves normal scrolling/focus and does not reopen a dialog later.
  delayList = true; resolveList = undefined
  await dialog.getByRole('button', { name: '刷新任务' }).click()
  for (let i = 0; !resolveList && i < 50; i++) await page.waitForTimeout(20)
  await close(); assert.ok(resolveList); resolveList(); delayList = false
  await page.waitForTimeout(100); assert.equal(await page.locator('dialog[open]').count(), 0)
  // Many tasks and long identifiers: inner scroll, visible footer, compact history and both palettes.
  pending = Array.from({ length: 5 }, (_, i) => target(`pending-${i}`, { name: '很长的未完成用户名'.repeat(10) }))
  jobs = [job('resume', 'long-user'.repeat(12)), ...Array.from({ length: 12 }, (_, i) => job(`history-${i}`, '历史用户名'.repeat(12), { stage: 'completed' }))]
  const artifacts = path.join(__dirname, '../test-results/member-deletion'); fs.mkdirSync(artifacts, { recursive: true })
  for (const [width, height] of [[1440,1000], [1024,768], [768,900], [390,844], [320,568], [667,375]]) {
   await page.setViewportSize({ width, height })
   for (const dark of [false, true]) {
    await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
    await page.evaluate(() => window.scrollTo(0, 0)); await settle()
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    await open()
    const box = await dialog.boundingBox(), footer = await dialog.locator('footer').boundingBox()
    assert.ok(box.x >= 0 && box.y >= 0 && box.x + box.width <= width + 1 && box.y + box.height <= height + 1)
    assert.ok(footer.y + footer.height <= height + 1)
    assert.equal(await dialog.locator('.cleanup-body').evaluate(e => e.scrollWidth > e.clientWidth), false)
    assert.equal(await dialog.evaluate(e => getComputedStyle(e).backgroundColor), dark ? 'rgb(24, 33, 49)' : 'rgb(255, 255, 255)')
    assert.ok(await dialog.locator('.cleanup-body').evaluate(e => e.scrollHeight > e.clientHeight))
    if ([1440,390,320].includes(width)) await page.screenshot({ path: path.join(artifacts, `tasks-${width}-${dark ? 'dark' : 'light'}.png`) })
    await dialog.locator('.cleanup-history > summary').click()
    await dialog.locator('.cleanup-history').scrollIntoViewIfNeeded()
    assert.equal(await dialog.locator('.cleanup-history').evaluate(e => e.scrollWidth > e.clientWidth), false)
    await dialog.locator('[data-cleanup-key="resume"]').getByRole('button', { name: '继续原任务' }).click()
    assert.equal(await dialog.locator('.cleanup-heading p').evaluate(e => getComputedStyle(e).whiteSpace), 'nowrap')
    assert.equal(await dialog.locator('.cleanup-body').evaluate(e => e.scrollWidth > e.clientWidth), false)
    const previewFooter = await dialog.locator('footer').boundingBox()
    assert.ok(previewFooter.y + previewFooter.height <= height + 1)
    if (width < 640) assert.equal(await dialog.getByRole('textbox').evaluate(e => getComputedStyle(e).fontSize), '16px')
    if ([1440,390,320].includes(width)) await page.screenshot({ path: path.join(artifacts, `preview-${width}-${dark ? 'dark' : 'light'}.png`) })
    await close()
   }
  }
  await page.emulateMedia({ reducedMotion: 'reduce' }); await open()
  assert.equal(await dialog.evaluate(e => getComputedStyle(e).animationName), 'none')
  await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'hidden' })
  assert.equal(await entry.evaluate(e => e === document.activeElement), true)
  assert.deepEqual(errors, [])
  assert.equal(submissions.length, 3)
  console.log('Account cleanup: header entry/badge, history, preview cancellation, name/children confirmation, failed-write recovery, original-task resume, busy guard, 6 viewports, light/dark and reduced motion passed')
 } finally { await browser.close() }
})().catch(e => { console.error(e); process.exit(1) })
