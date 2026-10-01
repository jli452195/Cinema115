const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 }, serviceWorkers: 'block' })
  const errors = [], writes = []
  let reject = true
  page.on('pageerror', e => errors.push(e.message))
  await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
  const settings = { enabled: true, revision: 1, libraryIds: ['library'], allowDownload: false }
  const members = [
   { userId: 'owner', embyId: 'a'.repeat(32), embyName: 'jlihome', source: 'personal', assignedAccountId: '', childLimit: 0, childUsed: 1, skipLibrarySync: false, policyEnabled: true, stage: 'ready', appliedRevision: 1 },
   { userId: 'child', embyId: 'b'.repeat(32), embyName: 'viewer', source: 'personal_shared', ownerUserId: 'owner', ownerName: 'jlihome', playbackOnly: true, policyEnabled: true, stage: 'ready', appliedRevision: 1 },
   { userId: 'admin', embyId: 'c'.repeat(32), embyName: 'jli452195', administrator: true, skipLibrarySync: true, source: 'primary', policyEnabled: true, stage: 'ready', appliedRevision: 1 },
  ]
  await page.route('**/api/**', async route => {
   const req = route.request(), p = new URL(req.url()).pathname
   let data = {}
   if (p === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
   else if (p.endsWith('/member-playback/accounts')) data = { accounts: [] }
   else if (p.includes('/member-playback')) {
    if (req.method() === 'POST') {
     const body = req.postDataJSON(); writes.push({ path: p, body })
     if (p.endsWith('/settings')) {
      if (reject) { reject = false; return route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: '保存失败，请重试' }) }) }
      Object.assign(members[0], { childLimit: body.childLimit, source: body.source, assignedAccountId: body.accountId, skipLibrarySync: body.skipLibrarySync, appliedRevision: 0, policyEnabled: false })
     } else if (p.endsWith('/owner')) members[0].adminBlocked = body.blocked
    }
    data = { members, settings }
   }
   await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
  })
  await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/emby/users')
  const owner = page.locator('article').filter({ has: page.getByRole('heading', { name: 'jlihome', exact: true }) })
  const child = page.locator('article').filter({ has: page.getByRole('heading', { name: 'viewer', exact: true }) })
  const admin = page.locator('article').filter({ has: page.getByRole('heading', { name: 'jli452195', exact: true }) })
  await owner.getByRole('button', { name: '管理用户' }).waitFor()
  assert.equal(await page.locator('article input, article select').count(), 0)
  const heights = await page.locator('article').evaluateAll(nodes => nodes.map(node => node.getBoundingClientRect().height))
  assert.ok(Math.max(...heights) - Math.min(...heights) < 2)
  await page.screenshot({ path: path.join(os.tmpdir(), 'cinema-users-cards.png'), fullPage: true })
  await owner.getByRole('button', { name: '管理用户' }).click()
  const dialog = page.getByRole('dialog')
  assert.equal(await dialog.getByRole('button', { name: '保存更改' }).isDisabled(), true)
  assert.equal(await dialog.getByLabel('子账号名额', { exact: true }).count(), 0)
  await dialog.getByRole('switch', { name: '允许创建子账号' }).click()
  await dialog.getByLabel('子账号名额', { exact: true }).fill('3')
  await dialog.getByLabel('播放来源', { exact: true }).selectOption('primary')
  await dialog.getByLabel('媒体库权限', { exact: true }).selectOption({ label: '不同步组' })
  await page.keyboard.press('Escape')
  await dialog.getByText('有未保存的修改，是否放弃？').waitFor()
  await dialog.getByRole('button', { name: '继续编辑' }).click()
  await dialog.getByRole('button', { name: '保存更改' }).click()
  await dialog.getByRole('alert').filter({ hasText: '保存失败' }).waitFor()
  assert.equal(await dialog.getByLabel('子账号名额', { exact: true }).inputValue(), '3')
  assert.equal(members[0].childLimit, 0)
  await dialog.getByRole('button', { name: '保存更改' }).click()
  await dialog.getByText('设置已保存，播放权限正在同步。', { exact: true }).waitFor()
  assert.equal(writes.length, 2)
  assert.deepEqual(writes[1], { path: '/api/admin/member-playback/owner/settings', body: { source: 'primary', accountId: '', childLimit: 3, skipLibrarySync: true, expected: { source: 'personal', accountId: '', childLimit: 0, skipLibrarySync: false } } })
  await page.screenshot({ path: path.join(os.tmpdir(), 'cinema-user-dialog.png'), fullPage: true })
  await dialog.getByRole('button', { name: '关闭', exact: true }).click()
  assert.equal(await owner.getByRole('button', { name: '管理用户' }).evaluate(el => el === document.activeElement), true)
  await owner.getByRole('button', { name: '管理用户' }).click()
  await dialog.getByRole('switch').click()
  await dialog.getByRole('button', { name: '关闭用户设置' }).click()
  await dialog.getByRole('button', { name: '放弃修改并关闭' }).click()
  assert.equal(members[0].childLimit, 3)
  await owner.getByRole('button', { name: '管理用户' }).click()
  await dialog.getByRole('button', { name: '查看子账号' }).click()
  assert.equal(await page.getByRole('dialog').count(), 0)
  assert.equal(await child.isVisible(), true); assert.equal(await owner.isVisible(), false)
  await page.getByRole('button', { name: '显示全部用户' }).click()
  await child.getByRole('button', { name: '管理用户' }).click()
  assert.equal(await dialog.getByRole('switch').count(), 0)
  assert.equal(await dialog.getByLabel('播放来源', { exact: true }).count(), 0)
  await dialog.getByText('仅允许 Emby 播放，不允许网站登录。').waitFor()
  await page.keyboard.press('Escape')
  await admin.getByRole('button', { name: '管理用户' }).click()
  assert.equal(await dialog.getByLabel('媒体库权限', { exact: true }).isDisabled(), true)
  assert.equal(await dialog.getByRole('switch').count(), 0)
  await page.keyboard.press('Escape')
  await page.setViewportSize({ width: 390, height: 700 })
  await owner.getByRole('button', { name: '管理用户' }).click()
  const bounds = await dialog.boundingBox(); assert.ok(bounds.height <= 700 && bounds.width <= 390)
  const footer = await dialog.locator('footer').boundingBox(); assert.ok(footer.y + footer.height <= 700)
  await dialog.getByText('更多操作', { exact: true }).click()
  await dialog.getByRole('button', { name: '停用播放', exact: true }).click()
  assert.equal(writes.length, 2)
  await dialog.getByRole('button', { name: '确认停用', exact: true }).click()
  await dialog.getByText('操作已完成。').waitFor()
  assert.equal(members[0].adminBlocked, true)
  await page.evaluate(() => document.documentElement.classList.add('dark'))
  const palette = await dialog.evaluate(el => {
   const form = el.querySelector('select'), footer = el.querySelector('footer')
   return { background: getComputedStyle(el).backgroundColor, color: getComputedStyle(el).color, field: getComputedStyle(form).backgroundColor, scheme: getComputedStyle(form).colorScheme, footer: getComputedStyle(footer).backgroundColor }
  })
  assert.deepEqual(palette, { background: 'rgb(33, 37, 41)', color: 'rgb(225, 227, 229)', field: 'rgb(24, 25, 28)', scheme: 'dark', footer: 'rgb(33, 37, 41)' })
  await page.screenshot({ path: path.join(os.tmpdir(), 'cinema-user-dialog-mobile.png') })
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.screenshot({ path: path.join(os.tmpdir(), 'cinema-user-dialog-dark.png') })
  await page.evaluate(() => document.documentElement.classList.remove('dark'))
  assert.equal(await dialog.evaluate(el => getComputedStyle(el).backgroundColor), 'rgb(255, 255, 255)')
  await page.setViewportSize({ width: 390, height: 700 })
  await dialog.getByRole('button', { name: '关闭', exact: true }).click()
  assert.equal(await page.evaluate(() => document.body.style.overflow), '')
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
  assert.deepEqual(errors, [])
  console.log('user modal UI passed: equal cards, unified save, failure recovery, discard, focus return, role restrictions, child filter, confirmation, mobile and dark mode')
 } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
