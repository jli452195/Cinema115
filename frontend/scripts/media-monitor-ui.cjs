// Isolated UI fixture: all APIs mocked; no real rule, mount, or account changes.
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
  const page = await context.newPage(), errors = [], saves = []
  page.on('pageerror', error => errors.push(error.message)); page.setDefaultTimeout(10000)
  const makeRule = (id, name, storage = 'local') => ({ id, name, storage, source: '/media/inbox', target: '/media/library', enabled: true, classify: true, intervalMinutes: 5 })
  const state = { rules: [makeRule('live', '本机目录'), makeRule('fallback', '远程挂载'), makeRule('starting', '准备中的目录'), makeRule('cloud', '115 目录', '115')], active: false, activeRules: [], monitoring: { live: { mode: 'realtime' }, fallback: { mode: 'polling', message: '目录监听暂时不可用，使用定时扫描。' + '/很长的挂载路径'.repeat(25) } } }
  let heldState, pendingState, stateReads = 0
  await page.route('**/api/**', async route => {
    const request = route.request(), endpoint = new URL(request.url()).pathname
    let data = {}
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
    else if (endpoint === '/api/admin/media-organize') data = state
    else if (endpoint === '/api/admin/media-organize/rules') {
      const body = request.postDataJSON(); saves.push(body)
      assert.equal(Object.hasOwn(body, 'monitoring'), false, 'runtime status must not be saved as rule settings')
      assert.equal(Object.hasOwn(body, 'mode'), false)
      const rule = state.rules.find(rule => rule.id === body.id)
      if (rule.enabled !== body.enabled) { if (body.enabled) state.monitoring[body.id] = { mode: 'starting' }; else delete state.monitoring[body.id] }
      Object.assign(rule, body); data = rule
    }
    const response = { contentType: 'application/json', body: JSON.stringify(data) }
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
    await label('本机目录', '实时监控 · 兜底每 5 分钟').waitFor()
    await label('远程挂载', '轮询监控 · 每 5 分钟').waitFor()
    await label('准备中的目录', '正在建立监听 · 兜底每 5 分钟').waitFor()
    await label('115 目录', '定时监控 · 每 5 分钟').waitFor()
    assert.equal(await card('准备中的目录').getByText('实时监控', { exact: true }).count(), 0)
    pendingState = gate(); heldState = pendingState
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await pendingState.ready
    const nextRead = stateReads + 1
    await card('远程挂载').getByRole('button', { name: '暂停远程挂载的目录监控', exact: true }).click()
    await label('远程挂载', '监控已暂停').waitFor()
    const refreshed = page.waitForResponse(response => response.headers()['x-fixture-read'] === String(nextRead))
    pendingState.release()
    await refreshed
    await label('远程挂载', '监控已暂停').waitFor()
    assert.equal(await card('远程挂载').locator('.mo-rule-monitor-message').count(), 0)
    await card('远程挂载').getByRole('button', { name: '启用远程挂载的目录监控', exact: true }).click()
    await label('远程挂载', '正在建立监听 · 兜底每 5 分钟').waitFor()
    state.monitoring.fallback = { mode: 'realtime' }
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await label('远程挂载', '实时监控 · 兜底每 5 分钟').waitFor()
    assert.equal(await card('远程挂载').locator('.mo-rule-monitor-message').count(), 0)
    await card('本机目录').getByRole('button', { name: '编辑本机目录', exact: true }).click()
    const editor = page.getByRole('dialog', { name: '整理规则', exact: true })
    await editor.getByRole('spinbutton', { name: '兜底扫描间隔（分钟）', exact: true }).fill('12')
    await editor.getByText('网络挂载的远端变更可能无法即时通知，将由定时扫描补齐。', { exact: true }).waitFor()
    await editor.getByRole('button', { name: '保存规则', exact: true }).click()
    await label('本机目录', '实时监控 · 兜底每 12 分钟').waitFor()
    assert.equal(saves.at(-1).intervalMinutes, 12)
    await card('115 目录').getByRole('button', { name: '编辑115 目录', exact: true }).click()
    await editor.getByRole('spinbutton', { name: '扫描间隔（分钟）', exact: true }).waitFor()
    assert.equal(await editor.getByRole('spinbutton', { name: '兜底扫描间隔（分钟）', exact: true }).count(), 0)
    await editor.getByRole('button', { name: '取消', exact: true }).click()
    state.monitoring.fallback = { mode: 'polling', message: '远端变更不保证提供通知，使用定时扫描。' + '/很长的挂载路径'.repeat(25) }
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await label('远程挂载', '轮询监控 · 每 5 分钟').waitFor()
    await page.screenshot({ path: 'test-results/media-monitor-desktop.png', fullPage: true })
    await page.setViewportSize({ width: 320, height: 740 })
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false)
    assert.equal(await card('远程挂载').locator('.mo-rule-monitor-message').evaluate(el => el.scrollWidth > el.clientWidth + 1), false)
    const colors = await card('远程挂载').locator('.mo-rule-schedule').evaluate(el => ({ background: getComputedStyle(el).backgroundColor, color: getComputedStyle(el).color }))
    assert.equal(colors.background, 'rgb(59, 41, 29)')
    await page.screenshot({ path: 'test-results/media-monitor-mobile-dark.png', fullPage: true })
    assert.deepEqual(errors, [])
    console.log('PASS: actual monitoring modes, startup state, degradation/recovery, pause/re-enable, fallback interval, 115 scheduling, runtime/settings separation, narrow dark layout')
  } finally { pendingState?.release(); await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
