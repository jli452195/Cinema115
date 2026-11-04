// 浏览器隔离验证：所有接口均为模拟，不写入 NAS 或网盘。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const copy = value => JSON.parse(JSON.stringify(value))
const gate = () => { let enter, release; const ready = new Promise(resolve => { enter = resolve }), pending = new Promise(resolve => { release = resolve }); return { ready, release, wait: () => { enter(); return pending } } }
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
  const page = await context.newPage(), errors = [], patches = [], stops = [], runs = [], saves = []
  page.on('pageerror', error => errors.push(error.message)); page.setDefaultTimeout(10000)
  const makeRule = (id, name, storage) => ({ id, name, storage, source: '/media/' + id + '/inbox', target: '/media/' + id + '/library', realtimeEnabled: storage !== 'cd2', scheduleEnabled: true, classify: true, intervalSeconds: storage === 'cd2' ? 20 : 1800, attachmentCleanupEnabled: true, attachmentGraceSeconds: 10, nextScanAt: new Date(Date.now() + 90000).toISOString() })
  const state = { rules: [makeRule('local', '本机目录', 'local'), makeRule('cloud', '115 目录', '115'), makeRule('cd2', 'CD2 目录', 'cd2')], active: true, activeRules: ['local'], stoppingRules: [], monitoring: { local: { mode: 'realtime' }, cloud: { mode: 'event' }, cd2: { mode: 'connecting' } } }
  let heldStop, heldState, rejectStop = false, rejectPatch = false, rejectSave = false
  await page.route('**/api/**', async route => {
    const request = route.request(), endpoint = new URL(request.url()).pathname
    let data = {}, status = 200
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
    else if (endpoint === '/api/admin/media-organize') data = copy(state)
    else if (endpoint.endsWith('/schedule')) {
      const id = endpoint.split('/').at(-2), body = request.postDataJSON()
      assert.equal(request.method(), 'PATCH'); assert.deepEqual(Object.keys(body).sort(), ['intervalSeconds', 'realtimeEnabled', 'scheduleEnabled']); patches.push({ id, body })
      if (rejectPatch) { rejectPatch = false; status = 503; data = { error: '模拟监控保存失败' } }
      else { const rule = state.rules.find(rule => rule.id === id); Object.assign(rule, body); data = copy(rule) }
    } else if (endpoint.endsWith('/stop')) {
      const id = endpoint.split('/').at(-2); stops.push(id)
      if (heldStop) { const pending = heldStop; heldStop = undefined; await pending.wait() }
      if (rejectStop) { rejectStop = false; status = 503; data = { error: '模拟停止失败' } }
      else { const rule = state.rules.find(rule => rule.id === id); rule.realtimeEnabled = rule.scheduleEnabled = false; state.stoppingRules = [...new Set([...state.stoppingRules, id])]; data = { rule: copy(rule) } }
    } else if (endpoint.endsWith('/run')) {
      const id = endpoint.split('/').at(-2); runs.push(id); state.active = true; state.activeRules = [...new Set([...state.activeRules, id])]; data = { id: 'run-' + id, status: 'queued' }
    } else if (endpoint === '/api/admin/media-organize/rules') {
      const body = request.postDataJSON(); saves.push(body)
      assert.equal(Object.hasOwn(body, 'intervalMinutes'), false); assert.equal(Object.hasOwn(body, 'mode'), false)
      if (rejectSave) { rejectSave = false; status = 503; data = { error: '模拟规则保存失败' } }
      else { const rule = state.rules.find(rule => rule.id === body.id); Object.assign(rule, body); data = copy(rule) }
    }
    const response = { status, contentType: 'application/json', body: JSON.stringify(data) }
    if (endpoint === '/api/admin/media-organize' && heldState) { const pending = heldState; heldState = undefined; await pending.wait() }
    await route.fulfill(response).catch(() => {})
  })
  try {
    await fs.mkdir('test-results', { recursive: true })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/media/organize', { waitUntil: 'networkidle' })
    const card = name => page.getByRole('article', { name: name + '整理规则', exact: true })
    const label = (name, text) => card(name).getByRole('status', { name: name + '监控状态', exact: true }).getByText(text, { exact: true })
    const editor = page.getByRole('dialog', { name: '整理规则', exact: true })
    await label('本机目录', '事件监控已连接').waitFor(); await label('115 目录', '事件监控已连接').waitFor(); await label('CD2 目录', '目录轮询 · 每 20 秒').waitFor()
    assert.equal(await card('CD2 目录').getByRole('button', { name: /事件监控/ }).count(), 0)
    for (const name of ['本机目录', '115 目录']) {
      assert.equal(await card(name).getByRole('button', { name: '暂停' + name + '的事件监控', exact: true }).isEnabled(), true)
      await label(name, '完整扫描 · 每 30 分钟').waitFor()
    }
    assert.equal(await card('本机目录').getByRole('button', { name: '删除本机目录', exact: true }).isDisabled(), true)
    // 停止请求只锁当前规则，重复点击不重复提交，其他规则保持可操作。
    const stopGate = gate(); heldStop = stopGate
    await card('本机目录').getByRole('button', { name: '停止本机目录的整理', exact: true }).click(); await stopGate.ready
    assert.equal(await card('本机目录').getByRole('button', { name: '停止本机目录的整理', exact: true }).isDisabled(), true)
    await card('本机目录').locator('.mo-rule-stop').evaluate(button => { button.click(); button.click() }); assert.deepEqual(stops, ['local'])
    await card('CD2 目录').getByRole('button', { name: '暂停CD2 目录的目录轮询', exact: true }).click()
    await label('CD2 目录', '目录轮询已暂停').waitFor(); assert.equal(state.rules[2].realtimeEnabled, false)
    stopGate.release()
    await card('本机目录').getByText('停止中…', { exact: true }).waitFor(); await label('本机目录', '事件监控已暂停').waitFor(); await label('本机目录', '定时完整扫描已暂停').waitFor()
    assert.equal(await card('本机目录').getByRole('button', { name: '启用本机目录的事件监控', exact: true }).isDisabled(), true)
    state.active = false; state.activeRules = []; state.stoppingRules = []
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await card('本机目录').getByRole('button', { name: '本机目录立即完整扫描', exact: true }).waitFor()
    // 新的手动扫描可以在自动触发暂停时执行，失败的停止请求不会留下前端假锁。
    await card('115 目录').getByRole('button', { name: '115 目录立即完整扫描', exact: true }).click()
    await card('115 目录').getByRole('button', { name: '停止115 目录的整理', exact: true }).waitFor(); assert.deepEqual(runs, ['cloud'])
    rejectStop = true; await card('115 目录').getByRole('button', { name: '停止115 目录的整理', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '模拟停止失败' }).waitFor()
    assert.equal(await card('115 目录').getByRole('button', { name: '停止115 目录的整理', exact: true }).isEnabled(), true)
    // 晚返回的旧状态不能覆盖已保存的事件开关。
    const oldState = gate(); heldState = oldState
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange'))); await oldState.ready
    await card('115 目录').getByRole('button', { name: '暂停115 目录的事件监控', exact: true }).click()
    await label('115 目录', '事件监控已暂停').waitFor(); oldState.release()
    await page.waitForTimeout(100); await label('115 目录', '事件监控已暂停').waitFor(); assert.equal(state.rules[1].scheduleEnabled, true)
    rejectPatch = true; await card('115 目录').getByRole('button', { name: '启用115 目录的事件监控', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '模拟监控保存失败' }).waitFor(); await label('115 目录', '事件监控已暂停').waitFor()
    await card('115 目录').getByRole('button', { name: '编辑115 目录', exact: true }).click()
    assert.equal(await editor.getByRole('switch', { name: '事件监控', exact: true }).count(), 1)
    const interval = editor.getByRole('spinbutton', { name: '完整扫描间隔', exact: true })
    assert.equal(await interval.inputValue(), '30')
    await interval.fill('1.5'); const beforeInvalid = saves.length
    await editor.getByRole('button', { name: '保存规则', exact: true }).click(); assert.equal(saves.length, beforeInvalid)
    await interval.fill('12'); await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).fill('0')
    rejectSave = true; await editor.getByRole('button', { name: '保存规则', exact: true }).click()
    await editor.getByRole('alert').filter({ hasText: '模拟规则保存失败' }).waitFor(); assert.equal(await interval.inputValue(), '12')
    await editor.getByRole('button', { name: '保存规则', exact: true }).click(); await editor.waitFor({ state: 'hidden' })
    assert.equal(saves.at(-1).intervalSeconds, 720); assert.equal(saves.at(-1).attachmentGraceSeconds, 0)
    await label('115 目录', '完整扫描 · 每 12 分钟').waitFor()
    await page.getByRole('button', { name: '新增规则', exact: true }).click()
    assert.equal(await interval.inputValue(), '30'); assert.equal(await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).inputValue(), '10')
    await editor.getByRole('combobox', { name: '存储位置', exact: true }).click()
    await page.getByRole('option', { name: 'CloudDrive2', exact: true }).getByRole('button').click()
    const cd2Interval = editor.getByRole('spinbutton', { name: '轮询间隔', exact: true })
    assert.equal(await cd2Interval.inputValue(), '20'); assert.equal(await editor.getByRole('switch', { name: '事件监控', exact: true }).count(), 0)
    assert.equal(await editor.getByRole('switch', { name: '目录轮询', exact: true }).count(), 1)
    await editor.getByRole('button', { name: '取消', exact: true }).click()
    state.monitoring.cd2 = { mode: 'unavailable', message: '连接暂时不可用，定时完整扫描继续按配置执行。' }
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await label('CD2 目录', '目录轮询已暂停').waitFor()
    assert.equal(await card('CD2 目录').getByText('连接暂时不可用，定时完整扫描继续按配置执行。', { exact: true }).count(), 0)
    await page.screenshot({ path: 'test-results/media-events-desktop.png', fullPage: true })
    await page.setViewportSize({ width: 320, height: 740 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false)
    await page.screenshot({ path: 'test-results/media-events-mobile.png', fullPage: true })
    await card('CD2 目录').getByRole('button', { name: '编辑CD2 目录', exact: true }).click()
    await cd2Interval.scrollIntoViewIfNeeded(); assert.equal(await cd2Interval.inputValue(), '20')
    await cd2Interval.fill('20'); await editor.getByRole('button', { name: '保存规则', exact: true }).click(); await editor.waitFor({ state: 'hidden' })
    assert.equal(saves.at(-1).intervalSeconds, 20); assert.equal(saves.at(-1).realtimeEnabled, false)
    await card('CD2 目录').getByRole('button', { name: '编辑CD2 目录', exact: true }).click()
    assert.equal(await cd2Interval.inputValue(), '20'); assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false)
    await page.screenshot({ path: 'test-results/media-events-editor-mobile.png', fullPage: true })
    await editor.getByRole('button', { name: '取消', exact: true }).click()
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.screenshot({ path: 'test-results/media-events-dark.png', fullPage: true })
    assert.deepEqual(errors, []); console.log('media monitor UI: event controls, minute schedules, stop lifecycle, stale state and responsive layout passed')
  } finally { heldStop?.release(); heldState?.release(); await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
