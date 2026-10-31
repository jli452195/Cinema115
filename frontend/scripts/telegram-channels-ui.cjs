// All requests are mocked; this test never reads real Telegram dialogs or saves real settings.
const path = require('node:path'), os = require('node:os'), fs = require('node:fs'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  page.setDefaultTimeout(10000)
  await page.addInitScript(() => { localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light') })
  const checkTheme = async dark => {
   const colors = await page.evaluate(dark => {
    document.documentElement.classList.toggle('dark', dark)
    const root = getComputedStyle(document.documentElement)
    const rgb = shade => 'rgb(' + root.getPropertyValue('--color-slate-' + shade).trim().split(/\s+/).join(', ') + ')'
    const background = rgb(dark ? 950 : 50)
    return { root: root.backgroundColor, background, scrollbar: root.scrollbarColor, expectedScrollbar: rgb(dark ? 600 : 300) + ' ' + background,
     panels: [...document.querySelectorAll('.channel-panel')].map(panel => getComputedStyle(panel).backgroundColor), panel: dark ? rgb(900) : 'rgb(255, 255, 255)',
     text: getComputedStyle(document.querySelector('.channel-settings')).color, expectedText: rgb(dark ? 200 : 800) }
   }, dark)
   assert.equal(colors.root, colors.background, 'Root and reserved scrollbar gutter must match the page theme')
   assert.equal(colors.scrollbar, colors.expectedScrollbar, 'Dialog styles must not override the root scrollbar')
   assert.equal(colors.panels.length, 3)
   assert(colors.panels.every(color => color === colors.panel), 'All settings panels must follow the active theme')
   assert.equal(colors.text, colors.expectedText)
  }
  const account = { apiId: 123, configured: true, botConfigured: false, hasSession: true, authorized: false, name: '', username: '', stage: 'idle', bot: '' }
  const avatar = await page.evaluate(() => { const canvas = document.createElement('canvas'); canvas.width = canvas.height = 160; const ctx = canvas.getContext('2d'); ctx.fillStyle = '#2563eb'; ctx.fillRect(0, 0, 160, 160); ctx.fillStyle = '#dbeafe'; ctx.beginPath(); ctx.arc(80, 80, 45, 0, Math.PI * 2); ctx.fill(); return canvas.toDataURL('image/jpeg') })
  const avatarBytes = Buffer.from(avatar.split(',')[1], 'base64')
  const first = { id: '4358697065', title: '115网盘热更频道', username: 'test_channel', avatarId: '123456' }
  const second = { id: '2769844611', title: '爱影热更频道' }
  let saved = [], failSave = false, expired = false, failMore = false, holdList = false, releaseList, avatarPause, releaseAvatars, avatarStarted
  const lists = [], writes = [], botWrites = [], errors = [], avatars = []
  page.on('pageerror', e => errors.push(e.message))
  page.on('console', m => { if (m.text().includes('[Vue warn]')) errors.push(m.text()) })
  await page.route('**/api/**', async route => {
   const req = route.request(), p = new URL(req.url()).pathname
   if (p.startsWith('/api/admin/telegram-relay/avatars/')) {
    const url = new URL(req.url()); avatars.push({ peer: p.split('/').at(-1), photo: url.searchParams.get('photo'), authorization: req.headers().authorization, query: url.search })
    if (avatarPause) { avatarStarted?.(); await avatarPause }
    return route.fulfill({ contentType: 'image/jpeg', body: avatarBytes }).catch(() => {})
   }
   if (p === '/api/auth/me') return route.fulfill({ json: { user: { id: 'admin', role: 'admin', username: 'admin' } } })
   if (p.endsWith('/telegram-user/channels/list')) {
    const body = req.postDataJSON(); lists.push(body)
    const response = body.cursor ? { channels: [first, second], selectionToken: 'selection', nextCursor: '' } : { channels: [first], selectionToken: 'selection', nextCursor: 'archive', config: { account: { ...account }, channels: [...saved] } }
    if (holdList) await new Promise(resolve => { releaseList = resolve })
    if (body.cursor && failMore) return route.fulfill({ status: 502, json: { error: '读取频道失败，请重试' } })
    return route.fulfill({ json: response }).catch(() => {})
   }
   if (p.endsWith('/telegram-user/channels')) {
    if (req.method() === 'POST') {
     const body = req.postDataJSON(); writes.push(body)
     if (expired) return route.fulfill({ status: 409, json: { error: '频道列表已失效，请重新打开选择频道', code: 'channel_selection_expired' } })
     if (failSave) return route.fulfill({ status: 502, json: { error: '保存失败，请重试' } })
     assert.equal(body.selectionToken, 'selection')
     saved = [first, second].filter(channel => body.channelIds.includes(channel.id))
    }
    return route.fulfill({ json: { account, channels: saved } })
   }
   if (p.endsWith('/telegram-user/config')) {
    if (req.method() === 'POST') { const body = req.postDataJSON(); botWrites.push(body); account.bot = body.bot.replace(/^@/, ''); account.botConfigured = !!account.bot }
    return route.fulfill({ json: account })
   }
   return route.fulfill({ json: { configured: false } })
  })
  const root = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
  await page.goto(root + '/resources')
  assert.equal(await page.locator('.resource-card[href="/resources/telegram"]').count(), 1)
  assert.equal(await page.locator('.resource-card[href="/telegram/user"]').count(), 0)
  await page.getByRole('link', { name: /Telegram 搜索/ }).last().click()
  const choose = page.getByRole('button', { name: '选择频道', exact: true })
  await choose.waitFor()
  await page.getByText('尚未选择频道', { exact: true }).waitFor()
  assert.equal(lists.length, 0, 'Opening configuration must only read local state')
  assert.equal(avatars.length, 0, 'No avatar request without saved or visible channels')
  assert.equal(await page.getByLabel('API ID', { exact: true }).count(), 0)
  await page.getByLabel('Bot 用户名', { exact: true }).fill('@resource_bot')
  await page.getByRole('button', { name: '保存 Bot', exact: true }).click()
  await page.getByRole('status').filter({ hasText: '资源 Bot 已保存' }).waitFor()
  assert.deepEqual(botWrites, [{ apiId: 123, apiHash: '', bot: '@resource_bot' }])
  assert.equal(saved.length, 0, 'Saving Bot must not change search channels')
  const dialog = page.locator('dialog.channel-picker[open]')
  const open = async () => { await choose.click(); await dialog.getByRole('checkbox', { name: /115网盘/ }).waitFor() }
  await open()
  const firstAvatar = dialog.locator('.channel-option').filter({ hasText: first.title }).locator('.channel-avatar')
  await firstAvatar.locator('img').evaluate(image => image.decode())
  assert.equal(await firstAvatar.locator('img').evaluate(image => image.naturalWidth), 160)
  assert.equal(await firstAvatar.evaluate(el => el.offsetWidth === 36 && el.offsetHeight === 36 && getComputedStyle(el).borderRadius === '50%'), true)
  assert.equal(lists.length, 1, 'Avatar loading must not enumerate channels again')
  await dialog.getByRole('checkbox', { name: /115网盘/ }).check()
  await page.keyboard.press('Escape')
  await dialog.waitFor({ state: 'hidden' })
  assert.equal(writes.length, 0, 'Cancel must not save')
  assert.equal(await choose.evaluate(el => document.activeElement === el), true, 'Return focus after closing')
  await open()
  assert.equal(await dialog.getByRole('checkbox').isChecked(), false)
  failMore = true
  await dialog.getByRole('button', { name: '加载更多频道' }).click()
  await dialog.getByRole('alert').filter({ hasText: '读取频道失败' }).waitFor()
  await dialog.getByRole('checkbox', { name: /115网盘/ }).check()
  failMore = false
  await dialog.getByRole('button', { name: '重试读取' }).click()
  await dialog.getByRole('checkbox', { name: /爱影/ }).waitFor()
  assert.equal(await dialog.getByRole('checkbox').count(), 2, 'Pagination must deduplicate')
  assert.equal(await dialog.locator('.channel-option').filter({ hasText: second.title }).locator('.channel-avatar svg').count(), 1, 'A channel without a photo retains the SVG fallback')
  assert.equal(avatars.some(avatar => avatar.peer === second.id), false)
  assert.equal(await dialog.getByRole('checkbox', { name: /115网盘/ }).isChecked(), true)
  await dialog.getByRole('checkbox', { name: /爱影/ }).check()
  await dialog.getByRole('searchbox', { name: '筛选已加载的频道' }).fill('爱影')
  assert.equal(await dialog.getByRole('checkbox').count(), 1)
  await dialog.getByRole('searchbox').fill('')
  failSave = true
  await dialog.getByRole('button', { name: '保存频道' }).click()
  await dialog.getByRole('alert').filter({ hasText: '保存失败' }).waitFor()
  assert.equal(await dialog.getByRole('checkbox', { name: /爱影/ }).isChecked(), true)
  assert.equal(saved.length, 0, 'Save failure must leave persistent config unchanged')
  failSave = false
  expired = true
  await dialog.getByRole('button', { name: '保存频道' }).click()
  await dialog.getByRole('alert').filter({ hasText: '频道列表已失效' }).waitFor()
  assert.equal(await dialog.getByRole('button', { name: '保存频道' }).isDisabled(), true)
  expired = false
  await dialog.getByRole('button', { name: '重新加载频道', exact: true }).click()
  await dialog.getByRole('alert').waitFor({ state: 'hidden' })
  await dialog.locator('.channel-options:enabled').waitFor()
  assert.equal(await dialog.getByRole('checkbox').isChecked(), false, 'Reload must synchronize with saved config')
  await dialog.getByRole('checkbox', { name: /115网盘/ }).check()
  await dialog.getByRole('button', { name: '加载更多频道' }).click()
  await dialog.getByRole('checkbox', { name: /爱影/ }).check()
  await dialog.getByRole('button', { name: '保存频道' }).click()
  await dialog.waitFor({ state: 'hidden' })
  await page.getByRole('status').filter({ hasText: '已保存 2 个搜索频道' }).waitFor()
  assert.equal(saved.length, 2)
  avatarPause = new Promise(resolve => { releaseAvatars = resolve })
  const downloading = new Promise(resolve => { avatarStarted = resolve })
  await page.reload()
  await page.locator('.channel-saved').filter({ hasText: '爱影' }).waitFor()
  await downloading
  await open()
  assert.equal(await dialog.getByRole('alert').count(), 0, 'A pending avatar must not disable or reject channel selection')
  assert.equal(await page.locator('.channel-saved img').count(), 0, 'Channel selection must finish before the held avatar')
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  avatarPause = undefined; avatarStarted = undefined; releaseAvatars()
  const savedAvatar = page.locator('.channel-saved').filter({ hasText: first.title }).locator('.channel-avatar')
  await savedAvatar.locator('img').evaluate(image => image.decode())
  assert.equal(await savedAvatar.evaluate(el => el.offsetWidth === 36 && el.offsetHeight === 36 && getComputedStyle(el).borderRadius === '50%'), true)
  assert.equal(await page.locator('.channel-saved').filter({ hasText: second.title }).locator('.channel-avatar svg').count(), 1)
  for (const dark of [false, true]) {
   await checkTheme(dark)
   if (process.env.CINEMA_UI_SCREENSHOTS) {
    fs.mkdirSync(process.env.CINEMA_UI_SCREENSHOTS, { recursive: true })
    await page.screenshot({ path: path.join(process.env.CINEMA_UI_SCREENSHOTS, `channels-settings-${dark ? 'dark' : 'light'}.png`) })
   }
  }
  await checkTheme(false)
  // An archived saved channel stays visible and selected before that page is fetched.
  await open()
  assert.equal(await dialog.getByRole('checkbox', { name: /爱影/ }).isChecked(), true)
  if (process.env.CINEMA_UI_SCREENSHOTS) {
   fs.mkdirSync(process.env.CINEMA_UI_SCREENSHOTS, { recursive: true })
   await page.screenshot({ path: path.join(process.env.CINEMA_UI_SCREENSHOTS, 'channels-desktop.png') })
  }
  for (const width of [390, 375, 320]) {
   await page.setViewportSize({ width, height: 780 })
   await checkTheme(true)
   const fits = await page.evaluate(() => {
    const el = document.querySelector('dialog.channel-picker'), box = el.getBoundingClientRect()
    return box.left >= 0 && box.right <= innerWidth && el.scrollWidth <= el.clientWidth && document.documentElement.scrollWidth <= innerWidth
   })
   assert.ok(fits, `No horizontal overflow at ${width}px`)
   const save = await dialog.getByRole('button', { name: '保存频道' }).boundingBox()
   assert.ok(save.y >= 0 && save.y + save.height <= 780, 'Footer must remain visible')
   if (width === 375) { await checkTheme(false); await checkTheme(true) }
   if (width === 390 && process.env.CINEMA_UI_SCREENSHOTS) await page.screenshot({ path: path.join(process.env.CINEMA_UI_SCREENSHOTS, 'channels-mobile-dark.png') })
  }
  await dialog.getByRole('checkbox', { name: /115网盘/ }).uncheck()
  await dialog.getByRole('checkbox', { name: /爱影/ }).uncheck()
  await dialog.getByRole('button', { name: '保存频道' }).click()
  await page.getByText('尚未选择频道', { exact: true }).waitFor()
  assert.equal(saved.length, 0)
  // Closing during a slow read cancels it and ignores its late result.
  holdList = true
  await choose.click()
  await dialog.getByRole('status').filter({ hasText: '正在读取频道' }).waitFor()
  await page.keyboard.press('Escape')
  await dialog.waitFor({ state: 'hidden' })
  const finishOldList = releaseList; holdList = false
  await open()
  await dialog.getByRole('checkbox', { name: /115网盘/ }).check()
  finishOldList()
  await page.waitForTimeout(100)
  assert.equal(await dialog.getByRole('checkbox', { name: /115网盘/ }).isChecked(), true, 'The previous picker response must not replace the reopened selection')
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  account.hasSession = false
  await page.reload()
  await page.getByText('请先配置 API 并登录 Telegram', { exact: true }).waitFor()
  assert.equal(await choose.isDisabled(), true)
  assert.deepEqual(errors, [])
  assert.ok(avatars.length > 0)
  assert.equal(avatars.every(avatar => avatar.peer === first.id && avatar.photo === first.avatarId && avatar.authorization === 'Bearer mock' && !avatar.query.includes('mock')), true)
  console.log('Telegram search UI passed: pending avatar does not block channel selection; reopened picker ignores old response; round channel avatars, independent Bot save, pagination, retries, persistence and mobile themes.')
 } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
