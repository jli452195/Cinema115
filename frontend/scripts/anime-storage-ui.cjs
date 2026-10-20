// Isolated browser checks: every API request is intercepted, including 115.
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
  const page = await context.newPage(), calls = [], errors = [], passed = [], receipts = new Map()
  page.setDefaultTimeout(10000); page.on('pageerror', e => errors.push(e.message)); page.on('dialog', d => d.accept())
  const state = { primary: true, stale: false, failRead: false, lose: false, reject: false, gate: null, writes: 0 }
  let settings = { revision: 1, accountId: 'main', accountRevision: 2, uid: '123', downloadCid: '100', downloadPath: '/下载', organizeCid: '900', organizePath: '/整理' }
  await page.route('**/api/**', async route => {
    const req = route.request(), url = new URL(req.url()), endpoint = url.pathname, method = req.method(), body = req.postDataJSON(), key = req.headers()['idempotency-key']
    calls.push({ endpoint, method, body, key, query: url.search }); let data = {}, status = 200
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
    else if (endpoint.endsWith('/catalog/mikan')) data = { provider: 'mikan', days: [], items: [], fetchedAt: Date.now(), expiresAt: Date.now() + 600000 }
    else if (endpoint.endsWith('/subscriptions')) data = { items: [], total: 0, page: 1, pageSize: 20 }
    else if (endpoint.endsWith('/settings') && method === 'GET') {
      if (state.failRead) { status = 503; data = { error: '模拟配置读取失败' } }
      else data = { settings, primaryAccount: state.primary ? { id: 'main', name: '主网盘', uid: '123', revision: 2 } : null, readiness: { ready: state.primary && !state.stale, issues: !state.primary ? ['PRIMARY_ACCOUNT_REQUIRED'] : state.stale ? ['ACCOUNT_SNAPSHOT_CHANGED'] : [] } }
    } else if (method === 'PATCH' && /\/settings(?:\/organize-directory)?$/.test(endpoint)) {
      if (state.gate) await state.gate
      if (state.reject) { state.reject = false; status = 409; data = { error: '主账号已变化，请重读设置', code: 'ACCOUNT_SNAPSHOT_CHANGED' } }
      else if (receipts.has(key)) { assert.deepEqual(body, receipts.get(key).body); data = receipts.get(key).data }
      else {
        assert.equal(body.expectedRevision, settings.revision)
        const organize = endpoint.endsWith('/organize-directory')
        if (!organize) { assert.equal(body.expectedPrimaryAccountId, 'main'); assert.equal(body.expectedPrimaryAccountRevision, 2) }
        state.writes++; settings = { ...settings, revision: settings.revision + 1, ...(organize ? { organizeCid: body.cid, organizePath: '/新整理' } : { downloadCid: body.downloadCid, downloadPath: '/新下载' }) }
        data = organize ? { id: 'anime-settings', revision: settings.revision, settings } : settings
        receipts.set(key, { body, data })
        if (state.lose) { state.lose = false; return route.abort() }
      }
    } else if (endpoint.includes('/cloud-download/paths')) {
      assert.equal(url.searchParams.get('accountId'), 'main')
      data = { directories: url.searchParams.get('cid') === '0' ? [{ id: '101', name: '新下载' }, { id: '901', name: '新整理' }] : [], hasMore: false }
    }
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
  })
  const modal = page.getByRole('dialog', { name: '动漫存储设置', exact: true })
  const open = async () => { await page.getByRole('button', { name: '动漫存储设置', exact: true }).click(); await modal.locator('.storage-directory').first().waitFor() }
  const close = async () => { await modal.getByRole('button', { name: '关闭弹窗' }).click(); await modal.waitFor({ state: 'detached' }) }
  async function choose(kind) {
    await modal.getByRole('button', { name: kind === 'download' ? '选择下载目录' : '选择整理目录', exact: true }).click()
    const picker = page.getByRole('dialog', { name: kind === 'download' ? '动漫订阅下载目录' : '动漫整理目录2', exact: true })
    await picker.getByRole('button', { name: kind === 'download' ? '新下载' : '新整理', exact: true }).click()
    await picker.getByRole('button', { name: kind === 'download' ? '保存此目录' : '保存整理目录', exact: true }).click()
    return picker
  }
  try {
    await fs.mkdir('test-results', { recursive: true })
    await page.goto(origin + '/anime?source=mikan&day=1', { waitUntil: 'networkidle' })
    assert.equal(calls.filter(c => c.endpoint.includes('/settings') || c.endpoint.includes('/cloud-download')).length, 0)
    const trigger = page.getByRole('button', { name: '动漫存储设置', exact: true })
    await trigger.focus(); await page.keyboard.press('Enter'); await modal.locator('.storage-directory').first().waitFor()
    assert.equal(calls.filter(c => c.endpoint.includes('/cloud-download')).length, 0)
    assert.equal(await modal.getByRole('button', { name: /保存|确认/ }).count(), 0)
    await page.screenshot({ path: 'test-results/anime-storage-desktop.png' })
    passed.push('选番页按需读取本地设置，两个目录集中展示，无115预请求或总保存按钮')
    await modal.getByRole('button', { name: '选择下载目录', exact: true }).click()
    await page.getByRole('dialog', { name: '动漫订阅下载目录' }).waitFor()
    await page.keyboard.press('Escape')
    await page.getByRole('dialog', { name: '动漫订阅下载目录' }).waitFor({ state: 'detached' })
    assert.equal(await modal.isVisible(), true)
    assert.equal(await page.evaluate(() => document.body.style.overflow), 'hidden')
    assert.equal(await modal.getByRole('button', { name: '选择下载目录' }).evaluate(el => el === document.activeElement), true)
    await page.keyboard.press('Escape'); await modal.waitFor({ state: 'detached' })
    assert.equal(await trigger.evaluate(el => el === document.activeElement), true)
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    passed.push('嵌套目录选择器Esc只关闭顶层，滚动锁与两级键盘焦点正确恢复')
    await open(); const download = await choose('download'); await download.waitFor({ state: 'detached' })
    await modal.getByText('/新下载', { exact: true }).waitFor()
    const write = calls.find(c => c.method === 'PATCH')
    assert.equal(write.body.organizeCid, '900'); assert.equal(state.writes, 1)
    passed.push('下载目录仍固定主账号，单次选择立即保存且保留整理目标')
    state.lose = true
    const organize = await choose('organize'); await organize.waitFor({ state: 'detached' })
    await modal.getByRole('button', { name: '核对原目录保存', exact: true }).waitFor()
    assert.equal(await modal.getByRole('button', { name: '选择下载目录' }).isDisabled(), true)
    assert.equal(await modal.getByRole('button', { name: '选择整理目录' }).isDisabled(), true)
    const original = structuredClone(calls.filter(c => c.method === 'PATCH').at(-1))
    await close()
    await page.getByRole('link', { name: '我的订阅', exact: true }).click()
    await page.evaluate(async () => document.querySelector('#app').__vue_app__.config.globalProperties.$router.push('/anime?source=mikan&day=1'))
    await open(); await modal.getByRole('button', { name: '核对原目录保存', exact: true }).click()
    await modal.getByText('目录已保存', { exact: true }).waitFor()
    const retry = calls.filter(c => c.method === 'PATCH').at(-1)
    assert.deepEqual(retry, original); assert.equal(state.writes, 2)
    assert.equal(await modal.getByRole('button', { name: '核对原目录保存' }).count(), 0)
    passed.push('未知结果跨弹窗和路由保留，锁定新选择并按原key/body/版本核对，无重复写入')
    await close(); state.primary = false; await open()
    assert.equal(await modal.getByRole('button', { name: '选择下载目录' }).isDisabled(), true)
    assert.equal(await modal.getByRole('button', { name: '选择整理目录' }).isDisabled(), true)
    await modal.getByRole('link', { name: '配置主账号' }).waitFor(); await close()
    state.primary = true; state.stale = true; await open()
    assert.equal(await modal.getByRole('button', { name: '选择下载目录' }).isEnabled(), true)
    assert.equal(await modal.getByRole('button', { name: '选择整理目录' }).isDisabled(), true)
    await close(); state.stale = false; state.failRead = true; await page.getByRole('button', { name: '动漫存储设置' }).click()
    await modal.getByText('模拟配置读取失败', { exact: true }).waitFor(); state.failRead = false
    await modal.getByRole('button', { name: '重新读取设置' }).click(); await modal.locator('.storage-directory').first().waitFor()
    passed.push('缺主账号不回退小号，换号先重选下载目录，本地设置读取失败可恢复')
    await close(); await page.setViewportSize({ width: 320, height: 740 }); await open()
    await page.screenshot({ path: 'test-results/anime-storage-320.png' })
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    assert.equal(await modal.locator('.storage-path').first().evaluate(el => getComputedStyle(el).color), 'rgb(226, 232, 240)')
    await page.screenshot({ path: 'test-results/anime-storage-dark-320.png' })
    settings = { ...settings, downloadPath: '/' + 'UnbrokenDownloadDirectory'.repeat(12), organizePath: '/' + 'UnbrokenOrganizeDirectory'.repeat(12) }
    await close(); await open()
    assert.equal(await modal.evaluate(el => el.scrollWidth <= el.clientWidth), true)
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    await page.screenshot({ path: 'test-results/anime-storage-long-320.png' })
    passed.push('320px及暗色长路径自然折行，弹窗可滚动，无横向溢出')
    await modal.getByRole('button', { name: '选择下载目录' }).click()
    await page.getByRole('dialog', { name: '动漫订阅下载目录' }).waitFor()
    await page.evaluate(async () => document.querySelector('#app').__vue_app__.config.globalProperties.$router.push('/anime/subscriptions'))
    await modal.waitFor({ state: 'detached' })
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    passed.push('浏览器路由离开双层弹窗后正确释放滚动锁')
    await page.evaluate(async () => document.querySelector('#app').__vue_app__.config.globalProperties.$router.push('/anime?source=mikan&day=1'))
    await open()
    let release
    state.gate = new Promise(resolve => { release = resolve }); state.reject = true
    const rejected = await choose('download')
    assert.equal(await rejected.getByRole('button', { name: '关闭目录选择器' }).isDisabled(), true)
    assert.equal(await modal.getByRole('button', { name: '关闭弹窗' }).isDisabled(), true)
    release(); state.gate = null
    await rejected.getByText('主账号已变化，请重读设置', { exact: true }).waitFor()
    await rejected.getByRole('button', { name: '关闭目录选择器' }).click()
    assert.equal(await modal.getByRole('button', { name: '核对原目录保存' }).count(), 0)
    assert.equal(await modal.getByRole('button', { name: '选择下载目录' }).isEnabled(), true)
    passed.push('保存中禁止重复提交和关闭，明确版本拒绝后解除待确认状态')
    assert.deepEqual(errors, []); console.log(JSON.stringify({ passed, apiCalls: calls.length, errors }, null, 2))
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
