// Isolated UI gray test: every API request is intercepted; no NAS/115 writes.
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'fixture'); localStorage.setItem('cinema115-theme', 'light') })
  const page = await context.newPage(), errors = [], calls = []
  page.on('pageerror', e => errors.push(e.message)); page.setDefaultTimeout(10000)
  const state = { mappings: [], primaryAccountId: 'main', accountReady: true, defaults: { baseUrl: 'http://localhost:8080', videoExtensions: 'mkv,mp4' }, events: {} }
  let rejectSave = false
  const endpoint = '/api/admin/p115/drive-strm'
  await page.route('**/api/**', async route => {
    const req = route.request(), url = new URL(req.url()), method = req.method(), body = req.postDataJSON()
    calls.push({ path: url.pathname, method, body, query: url.search })
    let data = {}, status = 200
    if (url.pathname === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (url.pathname.includes('/notifications')) data = { items: [], unread: 0 }
    else if (url.pathname === '/api/admin/p115/accounts') data = { accounts: [{ id: 'main', primary: true, configured: true }, { id: 'pool', primary: false, configured: true }] }
    else if (url.pathname === '/api/admin/p115/cloud-download/paths') {
      assert.equal(url.searchParams.get('accountId'), 'main')
      data = { directories: [{ id: '10', name: '动画' }], hasMore: false }
    } else if (url.pathname === '/api/admin/p115/share/output-directories') data = { path: '.', displayPath: '/strm', directories: [{ name: '动漫', path: '动漫', displayPath: '/strm/动漫' }], truncated: false }
    else if (url.pathname === endpoint && method === 'GET') data = state
    else if ((url.pathname === endpoint && method === 'POST') || (url.pathname === endpoint + '/one' && method === 'PUT')) {
      if (rejectSave) { status = 409; data = { error: '模拟输出目录冲突' }; rejectSave = false }
      else { data = { ...body, id: 'one', status: 'idle', queued: false, lastScanAt: new Date().toISOString(), stats: { videos: 12, written: 12, unchanged: 0, metadataWritten: 2, failed: 0 } }; state.mappings = [data] }
    } else if (url.pathname === endpoint + '/one/monitor') { Object.assign(state.mappings[0], body); data = state.mappings[0] }
    else if (url.pathname === endpoint + '/one/scan') { Object.assign(state.mappings[0], { status: 'running', mode: 'full', queued: false, lastError: '' }); data = { accepted: true } }
    else if (url.pathname === endpoint + '/one/check') data = { checked: true }
    else if (url.pathname === endpoint + '/one/stop') { state.mappings[0].status = 'stopped'; data = { stopped: true } }
    else if (url.pathname === endpoint + '/one' && method === 'DELETE') { state.mappings = []; status = 204 }
    await route.fulfill({ status, contentType: 'application/json', body: status === 204 ? undefined : JSON.stringify(data) })
  })
  try {
    await fs.mkdir('test-results/drive-strm', { recursive: true })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/115/drive-strm', { waitUntil: 'networkidle' })
    await page.getByText('选择云端来源与本地输出目录，开始生成 STRM。', { exact: true }).waitFor()
    await page.getByRole('button', { name: '新增映射', exact: true }).click()
    let editor = page.getByRole('dialog', { name: '新增 STRM 映射', exact: true })
    assert.equal(await editor.getByRole('button', { name: '保存映射', exact: true }).isDisabled(), true)
    await editor.getByRole('button', { name: /选择云端目录/ }).click()
    const directory = page.getByRole('dialog', { name: '选择主网盘来源目录', exact: true })
    await directory.getByRole('button', { name: '动画', exact: true }).click()
    await directory.getByRole('button', { name: '选择此目录', exact: true }).click()
    await editor.getByLabel('本地输出相对目录', { exact: true }).fill('动漫')
    await editor.getByText('附加文件', { exact: false }).click()
    await editor.getByText('下载外挂字幕', { exact: true }).click()
    await editor.getByText('下载 NFO', { exact: true }).click()
    rejectSave = true
    await editor.getByRole('button', { name: '保存映射', exact: true }).click()
    await editor.getByText('模拟输出目录冲突', { exact: true }).waitFor()
    await editor.getByRole('button', { name: '保存映射', exact: true }).click()
    const card = page.getByRole('article', { name: '动画', exact: true })
    await card.waitFor()
    const saved = calls.findLast(c => c.path === endpoint && c.method === 'POST').body
    assert.equal(saved.accountId, 'main'); assert.equal(saved.sourceCID, '10'); assert.ok(saved.metadataExtensions.includes('ass')); assert.ok(saved.metadataExtensions.includes('nfo')); assert.equal(saved.metadataExtensions.includes('jpg'), false)
    assert.equal('status' in saved, false); assert.equal('stats' in saved, false)
    await page.screenshot({ path: 'test-results/drive-strm/desktop.png' })
    await card.getByRole('button', { name: '暂停动画监控', exact: true }).click()
    await card.getByText('已暂停', { exact: true }).waitFor()
    assert.equal(await card.getByRole('button', { name: '检查更新', exact: true }).isDisabled(), true)
    await card.getByRole('button', { name: '完整扫描补齐', exact: true }).click()
    await card.getByText('正在完整扫描', { exact: true }).waitFor()
    assert.equal(await card.getByRole('button', { name: '编辑动画', exact: true }).isDisabled(), true)
    await card.getByRole('button', { name: '停止动画本次扫描', exact: true }).click()
    await card.getByText('已停止', { exact: true }).waitFor()
    await card.getByRole('button', { name: '开启动画监控', exact: true }).click()
    await card.getByRole('button', { name: '检查更新', exact: true }).click()
    assert.equal(calls.filter(c => c.path.endsWith('/check')).length, 1)
    Object.assign(state.mappings[0], { status: 'queued', queued: true, lastError: '模拟请求超时，稍后重试', nextScanAt: new Date(Date.now() + 60000).toISOString() })
    await page.reload({ waitUntil: 'networkidle' })
    await card.getByText('需检查', { exact: true }).waitFor()
    assert.equal(await card.getByRole('button', { name: '编辑动画', exact: true }).isEnabled(), true, 'backoff must allow editing')
    assert.equal(await card.getByRole('button', { name: '完整扫描补齐', exact: true }).isEnabled(), true, 'backoff must allow manual retry')
    await card.getByRole('button', { name: '完整扫描补齐', exact: true }).click()
    await card.getByText('正在完整扫描', { exact: true }).waitFor()
    await card.getByRole('button', { name: '停止动画本次扫描', exact: true }).click()
    await card.getByText('已停止', { exact: true }).waitFor()
    await card.getByRole('button', { name: '编辑动画', exact: true }).click()
    editor = page.getByRole('dialog', { name: '编辑 STRM 映射', exact: true })
    await editor.getByLabel('名称', { exact: true }).fill('新映射名称')
    page.once('dialog', d => d.dismiss())
    await editor.getByRole('button', { name: '取消', exact: true }).click()
    assert.equal(await editor.count(), 1)
    page.once('dialog', d => d.accept())
    await editor.getByRole('button', { name: '取消', exact: true }).click()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.screenshot({ path: 'test-results/drive-strm/mobile.png' })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'mobile page overflows')
    await card.getByRole('button', { name: '编辑动画', exact: true }).click()
    await page.getByRole('dialog', { name: '编辑 STRM 映射', exact: true }).waitFor()
    await page.screenshot({ path: 'test-results/drive-strm/mobile-editor.png' })
    await page.getByRole('dialog', { name: '编辑 STRM 映射', exact: true }).getByRole('button', { name: '取消', exact: true }).click()
    await page.setViewportSize({ width: 320, height: 740 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, '320px page overflows')
    await card.getByRole('button', { name: '编辑动画', exact: true }).click()
    const narrowEditor = page.getByRole('dialog', { name: '编辑 STRM 映射', exact: true })
    await narrowEditor.waitFor()
    assert.equal(await narrowEditor.evaluate(el => el.scrollWidth > el.clientWidth), false, '320px editor overflows')
    await narrowEditor.getByRole('button', { name: '取消', exact: true }).click()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.screenshot({ path: 'test-results/drive-strm/mobile-dark.png' })
    await card.getByRole('button', { name: '删除动画', exact: true }).click()
    const deletion = page.getByRole('dialog', { name: '删除 STRM 映射', exact: true })
    await deletion.getByText(/本地文件和网盘文件都会保留/).waitFor()
    await deletion.getByRole('button', { name: '删除配置', exact: true }).click()
    await page.getByRole('button', { name: '添加第一个映射', exact: true }).waitFor()
    assert.deepEqual(errors, [])
    assert.equal(calls.some(c => /share\/generate|folder-monitors|anime\//.test(c.path)), false)
    console.log('Directory STRM UI passed: create/error recovery, primary directory, attachments, pause, scan/stop, backoff retry, edit/discard, 320px/mobile/dark, delete.')
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
