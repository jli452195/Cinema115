// 所有接口均使用隔离模拟数据，不连接实际网盘或修改挂载目录。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const gate = () => {
  let enter, release
  const ready = new Promise(resolve => { enter = resolve }), pending = new Promise(resolve => { release = resolve })
  return { ready, release, wait: () => { enter(); return pending } }
}
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
  const page = await context.newPage(), errors = [], saves = [], patches = []
  page.on('pageerror', error => errors.push(error.message)); page.setDefaultTimeout(10000)
  const makeRule = (id, name, storage = 'local', grace = 60) => ({ id, name, storage, source: '/media/inbox', target: '/media/library', realtimeEnabled: storage === 'local', scheduleEnabled: true, classify: true, intervalSeconds: 300, attachmentCleanupEnabled: true, attachmentGraceSeconds: grace, nextScanAt: new Date(Date.now() + 90000).toISOString() })
  const state = { rules: [makeRule('live', '本机目录'), makeRule('fallback', '远程挂载'), makeRule('starting', '准备中的目录'), makeRule('cloud', '115 目录', '115', 600)], active: true, activeRules: ['live'], monitoring: { live: { mode: 'realtime' }, fallback: { mode: 'unavailable', message: '目录监听暂时不可用。' + '/很长的挂载路径'.repeat(25) } } }
  let heldState, pendingState, heldPatch, pendingPatch, rejectPatch = false, rejectRuleSave = false, stateReads = 0
  await page.route('**/api/**', async route => {
    const request = route.request(), endpoint = new URL(request.url()).pathname
    let data = {}, status = 200
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
    else if (endpoint === '/api/admin/media-organize') data = state
    else if (endpoint.endsWith('/schedule')) {
      const id = endpoint.split('/').at(-2), body = request.postDataJSON()
      assert.equal(request.method(), 'PATCH'); assert.deepEqual(Object.keys(body).sort(), ['intervalSeconds', 'realtimeEnabled', 'scheduleEnabled'])
      patches.push({ id, body })
      if (heldPatch?.id === id) { const held = heldPatch; heldPatch = undefined; await held.gate.wait() }
      if (rejectPatch) { rejectPatch = false; status = 503; data = { error: '模拟开关保存失败' } }
      else {
        const rule = state.rules.find(rule => rule.id === id)
        if (rule.realtimeEnabled !== body.realtimeEnabled) { if (body.realtimeEnabled) state.monitoring[id] = { mode: 'starting' }; else delete state.monitoring[id] }
        Object.assign(rule, body); data = rule
      }
    } else if (endpoint === '/api/admin/media-organize/rules') {
      const body = request.postDataJSON(); saves.push(body)
      assert.equal(Object.hasOwn(body, 'mode'), false)
      assert.equal(Object.hasOwn(body, 'enabled'), false); assert.equal(Object.hasOwn(body, 'intervalMinutes'), false)
      assert.equal(typeof body.attachmentCleanupEnabled, 'boolean')
      assert.equal(Number.isInteger(body.attachmentGraceSeconds) && body.attachmentGraceSeconds >= 0 && body.attachmentGraceSeconds <= 86400, true)
      if (rejectRuleSave) { rejectRuleSave = false; status = 503; data = { error: '模拟宽限保存失败' } }
      else { const rule = state.rules.find(rule => rule.id === body.id); Object.assign(rule, body); data = rule }
    }
    const response = { status, contentType: 'application/json', body: JSON.stringify(data) }
    if (endpoint === '/api/admin/media-organize') {
      response.headers = { 'x-fixture-read': String(++stateReads) }
      if (heldState) { const held = heldState; heldState = undefined; await held.wait() }
    }
    await route.fulfill(response).catch(() => {})
  })
  try {
    await fs.mkdir('test-results', { recursive: true })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/media/organize', { waitUntil: 'networkidle' })
    const card = name => page.getByRole('article', { name: name + '整理规则', exact: true })
    const label = (name, text) => card(name).getByRole('status', { name: name + '监控状态', exact: true }).getByText(text, { exact: true })
    const editor = page.getByRole('dialog', { name: '整理规则', exact: true })
    await label('本机目录', '实时监控运行中').waitFor()
    await label('本机目录', '定时扫描 · 每 5 分钟').waitFor()
    await label('远程挂载', '实时监听不可用').waitFor()
    await label('准备中的目录', '正在建立监听').waitFor()
    await label('115 目录', '定时扫描 · 每 5 分钟').waitFor()
    assert.equal(await card('115 目录').getByRole('button', { name: /实时监控/ }).count(), 0)
    assert.equal(await card('本机目录').getByRole('button', { name: '编辑本机目录', exact: true }).isEnabled(), true)
    assert.equal(await card('本机目录').getByRole('button', { name: '删除本机目录', exact: true }).isDisabled(), true)
    assert.equal(await card('远程挂载').getByRole('button', { name: '删除远程挂载', exact: true }).isEnabled(), true)
    await card('本机目录').getByRole('button', { name: '编辑本机目录', exact: true }).click()
    await editor.getByText('当前批次继续使用启动时的配置，保存的修改从后续任务生效。', { exact: true }).waitFor()
    const desktopRow = await editor.locator('.mo-schedule-line').evaluate(el => [...el.children].map(child => { const rect = child.getBoundingClientRect(); return { center: rect.top + rect.height / 2, width: rect.width } }))
    assert.ok(Math.abs(desktopRow[0].center - desktopRow[1].center) <= 2, '桌面定时开关与间隔应位于同一行')
    assert.equal(await editor.getByRole('spinbutton', { name: '扫描间隔', exact: true }).evaluate(el => Math.round(el.getBoundingClientRect().width)), 100)
    const desktopAttachmentRow = await editor.locator('.mo-attachment-line').evaluate(el => [...el.children].map(child => { const rect = child.getBoundingClientRect(); return rect.top + rect.height / 2 }))
    assert.ok(Math.abs(desktopAttachmentRow[0] - desktopAttachmentRow[1]) <= 2, '桌面附件清理开关与宽限应位于同一行')
    assert.equal(await editor.getByRole('switch', { name: '清理未匹配附件', exact: true }).isChecked(), true)
    assert.equal(await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).inputValue(), '1')
    assert.equal(await editor.getByRole('switch', { name: '自动删除相同文件', exact: true }).count(), 0)
    await editor.getByRole('combobox', { name: '扫描间隔单位', exact: true }).click()
    assert.deepEqual(await page.getByRole('option').allTextContents(), ['秒', '分钟'], '单位菜单只显示秒和分钟')
    await page.getByRole('option', { name: '分钟', exact: true }).getByRole('button').click()
    await page.screenshot({ path: 'test-results/media-rule-compact-desktop.png' })
    await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).fill('0')
    await editor.getByRole('spinbutton', { name: '扫描间隔', exact: true }).fill('12')
    await editor.getByRole('button', { name: '保存规则', exact: true }).click()
    await label('本机目录', '定时扫描 · 每 12 分钟').waitFor()
    assert.equal(saves.at(-1).intervalSeconds, 720)
    assert.equal(saves.at(-1).attachmentGraceSeconds, 0)
    assert.equal(saves.at(-1).attachmentCleanupEnabled, true)
    await card('本机目录').getByText('无关文件、空子目录；未匹配附件复核后立即清理', { exact: true }).waitFor()

    // 旧刷新响应晚于开关提交时，不能覆盖已保存的新状态。
    pendingState = gate(); heldState = pendingState
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await pendingState.ready
    const nextRead = stateReads + 1
    await card('远程挂载').getByRole('button', { name: '暂停远程挂载的实时监控', exact: true }).click()
    await label('远程挂载', '实时监控已关闭').waitFor()
    await label('远程挂载', '定时扫描 · 每 5 分钟').waitFor()
    const refreshed = page.waitForResponse(response => response.headers()['x-fixture-read'] === String(nextRead))
    pendingState.release(); await refreshed
    await label('远程挂载', '实时监控已关闭').waitFor()
    assert.equal(state.rules.find(rule => rule.id === 'fallback').scheduleEnabled, true)
    await card('远程挂载').getByRole('button', { name: '启用远程挂载的实时监控', exact: true }).click()
    await label('远程挂载', '正在建立监听').waitFor()

    // 一个规则的控制请求尚未返回时，另一个规则仍能独立操作。
    pendingPatch = gate(); heldPatch = { id: 'live', gate: pendingPatch }
    await card('本机目录').getByRole('button', { name: '暂停本机目录的定时扫描', exact: true }).click()
    await pendingPatch.ready
    assert.equal(await card('本机目录').getByRole('button', { name: '暂停本机目录的实时监控', exact: true }).isDisabled(), true)
    await card('115 目录').getByRole('button', { name: '暂停115 目录的定时扫描', exact: true }).click()
    await label('115 目录', '定时扫描已暂停').waitFor()
    pendingPatch.release()
    await label('本机目录', '定时扫描已暂停').waitFor()
    await label('本机目录', '实时监控运行中').waitFor()
    assert.equal(state.rules.find(rule => rule.id === 'live').realtimeEnabled, true)
    rejectPatch = true
    await card('115 目录').getByRole('button', { name: '启用115 目录的定时扫描', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '模拟开关保存失败' }).waitFor()
    await label('115 目录', '定时扫描已暂停').waitFor()

    await card('115 目录').getByRole('button', { name: '编辑115 目录', exact: true }).click()
    assert.equal(await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).inputValue(), '10', '明确保存过的 600 秒不得覆盖为新默认值')
    assert.equal(await editor.getByRole('switch', { name: '实时监控', exact: true }).count(), 0)
    assert.equal(await editor.getByRole('switch', { name: '自动删除相同文件', exact: true }).isChecked(), false)
    await editor.getByRole('switch', { name: '自动删除相同文件', exact: true }).locator('..').click()
    assert.equal(await editor.getByRole('spinbutton', { name: '扫描间隔', exact: true }).isEnabled(), true, '暂停定时后仍能预设间隔')
    await editor.getByRole('combobox', { name: '附件宽限单位', exact: true }).click()
    assert.deepEqual(await page.getByRole('option').allTextContents(), ['秒', '分钟'])
    await page.getByRole('option', { name: '秒', exact: true }).getByRole('button').click()
    await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).fill('1.5')
    const invalidGraceCount = saves.length
    await editor.getByRole('button', { name: '保存规则', exact: true }).click()
    assert.equal(saves.length, invalidGraceCount, '不能提交小数秒宽限')
    await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).fill('73')
    await editor.getByRole('combobox', { name: '扫描间隔单位', exact: true }).click()
    await page.getByRole('option', { name: '秒', exact: true }).getByRole('button').click()
    const saveCount = saves.length
    await editor.getByRole('spinbutton', { name: '扫描间隔', exact: true }).fill('1.5')
    await editor.getByRole('button', { name: '保存规则', exact: true }).click()
    assert.equal(saves.length, saveCount)
    await editor.getByRole('spinbutton', { name: '扫描间隔', exact: true }).fill('17')
    rejectRuleSave = true
    await editor.getByRole('button', { name: '保存规则', exact: true }).click()
    await editor.getByRole('alert').filter({ hasText: '模拟宽限保存失败' }).waitFor()
    assert.equal(await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).inputValue(), '73')
    assert.equal(state.rules.find(rule => rule.id === 'cloud').attachmentGraceSeconds, 600)
    await editor.getByRole('switch', { name: '清理未匹配附件', exact: true }).locator('..').click()
    assert.equal(await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).isDisabled(), true)
    assert.equal(await editor.getByRole('combobox', { name: '附件宽限单位', exact: true }).isDisabled(), true)
    await editor.getByRole('button', { name: '保存规则', exact: true }).click()
    await editor.waitFor({ state: 'hidden' })
    assert.equal(saves.at(-1).intervalSeconds, 17)
    assert.equal(saves.at(-1).attachmentGraceSeconds, 73)
    assert.equal(saves.at(-1).attachmentCleanupEnabled, false)
    assert.equal(saves.at(-1).scheduleEnabled, false)
    assert.equal(saves.at(-1).deleteDuplicates, true)
    await card('115 目录').getByText('同名且 SHA1 相同删除来源', { exact: true }).waitFor()
    await card('115 目录').getByText('无关文件、空子目录；保留未匹配附件', { exact: true }).waitFor()
    await card('115 目录').getByRole('button', { name: '编辑115 目录', exact: true }).click()
    assert.equal(await editor.getByRole('switch', { name: '清理未匹配附件', exact: true }).isChecked(), false)
    assert.equal(await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).inputValue(), '73')
    assert.equal(await editor.getByRole('switch', { name: '自动删除相同文件', exact: true }).isChecked(), true)
    await editor.getByRole('combobox', { name: '存储位置', exact: true }).click()
    await page.getByRole('option', { name: 'CloudDrive2', exact: true }).getByRole('button').click()
    assert.equal(await editor.getByRole('switch', { name: '自动删除相同文件', exact: true }).isChecked(), true, '115 与 CD2 共用去重偏好')
    assert.equal(await editor.getByRole('switch', { name: '清理未匹配附件', exact: true }).isChecked(), false)
    await editor.getByRole('combobox', { name: '存储位置', exact: true }).click()
    await page.getByRole('option', { name: 'NAS / 本地挂载', exact: true }).getByRole('button').click()
    assert.equal(await editor.getByRole('switch', { name: '自动删除相同文件', exact: true }).count(), 0)
    assert.equal(await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).inputValue(), '73')
    assert.equal(await editor.getByRole('switch', { name: '清理未匹配附件', exact: true }).isChecked(), false)
    await editor.getByRole('button', { name: '取消', exact: true }).click()
    await card('115 目录').getByRole('button', { name: '启用115 目录的定时扫描', exact: true }).click()
    await label('115 目录', '定时扫描 · 每 17 秒').waitFor()
    state.rules[0].scheduleEnabled = true; state.rules[0].nextScanAt = new Date(Date.now() - 1000).toISOString()
    state.monitoring.fallback = { mode: 'unavailable', message: '远端变更不保证提供通知。' + '/很长的挂载路径'.repeat(25) }
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await card('本机目录').getByText('完成后补扫一次', { exact: true }).waitFor()
    await label('远程挂载', '实时监听不可用').waitFor()
    await page.screenshot({ path: 'test-results/media-monitor-desktop.png', fullPage: true })
    await page.setViewportSize({ width: 320, height: 740 })
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false)
    assert.equal(await card('远程挂载').locator('.mo-rule-monitor-message').first().evaluate(el => el.scrollWidth > el.clientWidth + 1), false)
    const colors = await card('远程挂载').locator('.mo-rule-schedule').first().evaluate(el => ({ background: getComputedStyle(el).backgroundColor, color: getComputedStyle(el).color }))
    assert.equal(colors.background, 'rgb(59, 41, 29)')
    await card('115 目录').getByRole('button', { name: '编辑115 目录', exact: true }).click()
    const mobileRow = await editor.locator('.mo-schedule-line').evaluate(el => [...el.children].map(child => { const rect = child.getBoundingClientRect(); return { top: rect.top, bottom: rect.bottom } }))
    assert.ok(mobileRow[1].top >= mobileRow[0].bottom, '窄屏参数应自动换到下一行')
    const mobileAttachmentRow = await editor.locator('.mo-attachment-line').evaluate(el => [...el.children].map(child => { const rect = child.getBoundingClientRect(); return { top: rect.top, bottom: rect.bottom } }))
    assert.ok(mobileAttachmentRow[1].top >= mobileAttachmentRow[0].bottom, '窄屏附件宽限应自动换到下一行')
    await editor.getByRole('combobox', { name: '扫描间隔单位', exact: true }).scrollIntoViewIfNeeded()
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    await editor.getByRole('combobox', { name: '扫描间隔单位', exact: true }).click()
    await page.getByRole('option', { name: '秒', exact: true }).getByRole('button').click()
    await editor.getByRole('spinbutton', { name: '扫描间隔', exact: true }).fill('30')
    assert.equal(await editor.getByRole('spinbutton', { name: '扫描间隔', exact: true }).inputValue(), '30')
    assert.equal(await editor.evaluate(el => el.scrollWidth > el.clientWidth + 1), false)
    await page.screenshot({ path: 'test-results/media-monitor-mobile-dark.png', fullPage: true })
    await editor.getByRole('combobox', { name: '扫描间隔单位', exact: true }).click()
    await page.getByRole('option', { name: '分钟', exact: true }).getByRole('button').click()
    assert.equal(await editor.getByRole('spinbutton', { name: '扫描间隔', exact: true }).inputValue(), '0.5')
    await editor.getByRole('spinbutton', { name: '扫描间隔', exact: true }).fill('1.5')
    await editor.getByRole('switch', { name: '清理未匹配附件', exact: true }).locator('..').click()
    assert.equal(await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).inputValue(), '73')
    await editor.getByRole('button', { name: '保存规则', exact: true }).click()
    await label('115 目录', '定时扫描 · 每 90 秒').waitFor()
    assert.equal(saves.at(-1).intervalSeconds, 90)
    assert.equal(saves.at(-1).attachmentCleanupEnabled, true)
    assert.equal(saves.at(-1).attachmentGraceSeconds, 73)
    assert.deepEqual(errors, [])
    console.log('PASS: compact shared duration inputs, independent switches, attachment cleanup toggle/default60/zero/custom/failure, seconds/minutes, preserved explicit values, active editing, per-rule concurrent requests, stale refresh, pending scan, mobile wrap and dark layout')
  } finally { pendingState?.release(); pendingPatch?.release(); await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
