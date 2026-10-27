// All requests are mocked; this test never reads real Telegram dialogs or saves real settings.
const path = require('node:path'), os = require('node:os'), fs = require('node:fs'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
 const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
 try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  page.setDefaultTimeout(10000)
  await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
  const account = { apiId: 123, configured: true, botConfigured: false, hasSession: true, authorized: false, name: '', username: '', stage: 'idle', bot: '' }
  const first = { id: '4358697065', title: '115网盘热更频道', username: 'test_channel' }
  const second = { id: '2769844611', title: '爱影热更频道' }
  let saved = [], failSave = false, expired = false, failMore = false, holdList = false, releaseList
  const lists = [], writes = [], errors = []
  page.on('pageerror', e => errors.push(e.message))
  page.on('console', m => { if (m.text().includes('[Vue warn]')) errors.push(m.text()) })
  await page.route('**/api/**', async route => {
   const req = route.request(), p = new URL(req.url()).pathname
   if (p === '/api/auth/me') return route.fulfill({ json: { user: { id: 'admin', role: 'admin', username: 'admin' } } })
   if (p.endsWith('/telegram-user/channels/list')) {
    const body = req.postDataJSON(); lists.push(body)
    if (holdList) await new Promise(resolve => { releaseList = resolve })
    if (body.cursor && failMore) return route.fulfill({ status: 502, json: { error: '读取频道失败，请重试' } })
    return route.fulfill({ json: body.cursor ? { channels: [first, second], selectionToken: 'selection', nextCursor: '' } : { channels: [first], selectionToken: 'selection', nextCursor: 'archive', config: { account, channels: saved } } })
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
   if (p.endsWith('/telegram-user/config')) return route.fulfill({ json: account })
   return route.fulfill({ json: { configured: false } })
  })
  const root = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
  await page.goto(root + '/resources')
  await page.getByRole('link', { name: /TG 频道配置/ }).click()
  const choose = page.getByRole('button', { name: '选择频道', exact: true })
  await choose.waitFor()
  await page.getByText('尚未选择频道', { exact: true }).waitFor()
  assert.equal(lists.length, 0, 'Opening configuration must only read local state')
  const dialog = page.locator('dialog.channel-picker[open]')
  const open = async () => { await choose.click(); await dialog.getByRole('checkbox', { name: /115网盘/ }).waitFor() }
  await open()
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
  assert.equal(await dialog.getByRole('checkbox').isChecked(), false, 'Reload must synchronize with saved config')
  await dialog.getByRole('checkbox', { name: /115网盘/ }).check()
  await dialog.getByRole('button', { name: '加载更多频道' }).click()
  await dialog.getByRole('checkbox', { name: /爱影/ }).check()
  await dialog.getByRole('button', { name: '保存频道' }).click()
  await dialog.waitFor({ state: 'hidden' })
  await page.getByRole('status').filter({ hasText: '已保存 2 个搜索频道' }).waitFor()
  assert.equal(saved.length, 2)
  await page.reload()
  await page.locator('.channel-saved').filter({ hasText: '爱影' }).waitFor()
  // An archived saved channel stays visible and selected before that page is fetched.
  await open()
  assert.equal(await dialog.getByRole('checkbox', { name: /爱影/ }).isChecked(), true)
  if (process.env.CINEMA_UI_SCREENSHOTS) {
   fs.mkdirSync(process.env.CINEMA_UI_SCREENSHOTS, { recursive: true })
   await page.screenshot({ path: path.join(process.env.CINEMA_UI_SCREENSHOTS, 'channels-desktop.png') })
  }
  for (const width of [390, 320]) {
   await page.setViewportSize({ width, height: 780 })
   await page.evaluate(() => document.documentElement.classList.add('dark'))
   const fits = await page.evaluate(() => {
    const el = document.querySelector('dialog.channel-picker'), box = el.getBoundingClientRect()
    return box.left >= 0 && box.right <= innerWidth && el.scrollWidth <= el.clientWidth && document.documentElement.scrollWidth <= innerWidth
   })
   assert.ok(fits, `No horizontal overflow at ${width}px`)
   const save = await dialog.getByRole('button', { name: '保存频道' }).boundingBox()
   assert.ok(save.y >= 0 && save.y + save.height <= 780, 'Footer must remain visible')
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
  releaseList(); holdList = false
  await open()
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  account.hasSession = false
  await page.reload()
  await page.getByText('请先配置 API 并登录 Telegram', { exact: true }).waitFor()
  assert.equal(await choose.isDisabled(), true)
  assert.deepEqual(errors, [])
  console.log('Telegram channels UI passed: local reads, cancel, archive pagination, retries, persistence, clear, mobile/dark and login gating.')
 } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
