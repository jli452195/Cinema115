// All account operations are mocked; never modify actual 115 credentials.
const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await page.addInitScript(() => {
      localStorage.setItem('admin_token', 'mock')
      const schedule = window.setInterval.bind(window), cancel = window.clearInterval.bind(window)
      const polls = new Set()
      window.p115Polls = polls
      window.setInterval = (handler, delay, ...args) => {
        const timer = schedule(handler, delay, ...args)
        if (delay === 10000) polls.add(timer)
        return timer
      }
      window.clearInterval = timer => { polls.delete(timer); cancel(timer) }
    })
    page.setDefaultTimeout(10000)
    const errors = [], writes = [], reads = [], passed = []
    page.on('pageerror', e => errors.push(e.message))
    let primary = { id: 'main', name: '历史名称', uid: '2461229', primary: true, configured: true, revision: 1, loginClient: 'tv', summary: {vip:'vip',spaceKnown:true,remainingBytes:1099511627776,checkedAt:'2026-10-01T00:00:00Z'} }
    const pool = Array.from({ length: 6 }, (_, i) => ({ id: 'pool-' + i, name: '小号-' + (i + 1), uid: '121843' + i, primary: false, configured: true, revision: 1, loginClient: '115ipad', playbackParentCid: '3526857162909576292', users: ['reader', 'owner'], userCount: 2 }))
    const accountsPath = '/api/admin/p115/accounts', poolPath = '/api/admin/member-playback/pool', drivesPath = '/api/admin/member-playback/personal-drives'
    const sectionReads = () => reads.filter(p => [accountsPath, poolPath, drivesPath].includes(p))
    const drives = [{ userId: 'viewer', username: '测试用户', binding: 'test-id', driveStatus: { state: 'ready', message: '最近检查通过' }, vip: 'vip', issuedLinks: 2 }]
    let rejectCookie = true, rejectDelete = false, rejectPool = false, rejectStatus = false
    await page.route('**/api/**', async route => {
      const req = route.request(), p = new URL(req.url()).pathname, method = req.method()
      const body = req.postData() ? req.postDataJSON() : null
      if (method === 'GET') reads.push(p); else writes.push({ path: p, method, body })
      let data = {}, status = 200
      if (p === '/api/auth/me') data = { user: { id: 'admin', role: 'admin', username: 'admin' } }
      else if (p === poolPath) {
        if (rejectPool) { status = 502; data = { error: '小号池读取失败' } }
        else data = { accounts: pool, settings: { maxSessions: 3 }, metrics: { requests: 0, samples: 0, succeeded: 0, failed: 0 }, api: { waiting: 0 } }
      } else if (p === drivesPath || p === drivesPath + '/viewer/check') data = { drives }
      else if (p === accountsPath) {
        if (method === 'POST') {
          if (body.primary) primary = { id: 'created-main', name: '主账号', primary: true, configured: false, uid: '', revision: 1, loginClient: '' }
          else pool.push({ ...pool[0], id: 'added-pool', name: body.name, configured: false, uid: '', playbackParentCid: '', users: [], userCount: 0 })
        }
        data = { accounts: [...(primary ? [primary] : []), ...pool] }
      } else if (p.endsWith('/status')) {
        if (rejectStatus) { status = 502; data = { error: '暂时无法验证 Cookie，请重试' } }
        else data = { connected: true, configured: true, loginClient: primary.loginClient }
      }
      else if (method === 'PUT') {
        if (rejectCookie) { status = 422; data = { error: 'Cookie 无效，原配置未修改' } }
        else {
          primary = { ...primary, id: 'rotated-main', uid: '999', revision: primary.revision + 1, loginClient: '', configured: true, summary: {vip:'normal',spaceKnown:true,remainingBytes:0,checkedAt:'2026-10-01T00:00:00Z'} }
          data = { status: 'success', connected: true, configured: true }
        }
      } else if (method === 'DELETE' && p.endsWith('/login')) { primary.configured = false; primary.revision++; primary.loginClient = ''; status = 204 }
      else if (method === 'DELETE') {
        if (rejectDelete) { status = 500; data = { error: '删除失败，请重试' } } else primary = undefined
      }
      await route.fulfill({ status, contentType: 'application/json', body: status === 204 ? undefined : JSON.stringify(data) })
    })
    const base = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    await page.goto(base + '/helper115')
    const main = page.getByRole('region', { name: '主账号配置' }), shared = page.getByRole('region', { name: '共享小号池', exact: true })
    const personal = page.getByRole('region', { name: '个人网盘状态', exact: true })
    const navigation = page.getByRole('navigation', { name: '115 助手导航' })
    const tab = name => navigation.getByRole('link', { name, exact: true })
    const refresh = page.getByRole('button', { name: '刷新状态', exact: true })
    async function active(name) {
      await page.locator('.p115-tabs [aria-current="page"]').filter({ hasText: name }).waitFor()
      assert.equal(await navigation.locator('[aria-current="page"]').count(), 1)
      assert.equal(await tab(name).getAttribute('aria-current'), 'page')
      assert.equal(await page.evaluate(() => window.p115Polls.size), name === '主账号' ? 0 : 1)
    }
    async function refreshOnly(pathname) {
      const before = sectionReads().length
      const response = page.waitForResponse(response => new URL(response.url()).pathname === pathname)
      await refresh.click()
      await response
      await refresh.waitFor()
      assert.deepEqual(sectionReads().slice(before), [pathname])
    }
    await main.getByRole('heading', { name: '主账号', exact: true }).waitFor()
    await active('主账号')
    assert.deepEqual(sectionReads(), [accountsPath])
    await refreshOnly(accountsPath)
    await main.getByText('VIP', {exact:true}).waitFor()
    await main.locator('.primary-space').getByText('1 TB', { exact: true }).waitFor()
    assert.equal(reads.filter(p=>p.endsWith('/status')).length,0)
    assert.equal(await shared.count(), 0)
    assert.equal(await personal.count(), 0)
    assert.equal(await main.locator('select,input').count(), 0)
    assert.equal(await main.getByText(/秒传目录/).count(), 0)
    assert.equal(await main.getByRole('button', { name: /人使用/ }).count(), 0)
    await main.getByRole('button', { name: '检测可用性' }).click()
    await main.getByText('最近检查通过', { exact: true }).waitFor()
    assert.equal(await main.getByText('账号可用').count(), 0)
    rejectStatus = true
    await main.getByRole('button', { name: '检测可用性' }).click()
    await main.getByRole('alert').getByText('暂时无法验证 Cookie，请重试').waitFor()
    assert.equal(await main.getByText('最近检查通过', { exact: true }).count(), 0)
    await main.getByText('登录信息已保存', { exact: true }).waitFor()
    rejectStatus = false
    await main.getByRole('button', { name: '检测可用性' }).click()
    await main.getByText('最近检查通过', { exact: true }).waitFor()
    passed.push('failed-recheck-does-not-retain-connected-status')
    for (const width of [1440, 390, 320]) {
      await page.setViewportSize({ width, height: 900 })
      for (const dark of [false, true]) {
        await page.evaluate(d => document.documentElement.classList.toggle('dark', d), dark)
        assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
        assert(await navigation.evaluate(el => el.scrollWidth <= el.clientWidth + 1), '三个标签应同时可见')
        const icon = tab('主账号').locator('svg')
        assert.equal(await icon.evaluate(el => getComputedStyle(el).width), '16px')
        await page.screenshot({ path: path.join(os.tmpdir(), `cinema115-unified-${width}-${dark}.png`), fullPage: true })
      }
    }
    await page.setViewportSize({ width: 1440, height: 900 })
    await tab('小号池').click()
    await shared.getByRole('heading', { name: '小号-1', exact: true }).waitFor()
    await active('小号池')
    assert.equal(new URL(page.url()).hash, '#pool')
    assert.equal(await main.count(), 0)
    assert.equal(await shared.getByText('会员未知',{exact:true}).count(), 6)
    assert.equal(await shared.locator('article').count(), 6)
    await refreshOnly(poolPath)
    await shared.getByRole('button', { name: '共享池更多操作' }).click()
    await shared.getByRole('button', { name: '分配说明', exact: true }).click()
    await page.getByRole('dialog').getByText(/不回退主网盘/).waitFor()
    await page.getByRole('button', { name: '知道了' }).click()
    await tab('个人网盘').click()
    await active('个人网盘')
    assert.equal(new URL(page.url()).hash, '#personal-drives')
    assert.equal(await shared.count(), 0)
    await personal.getByText('测试用户', { exact: true }).waitFor()
    await personal.getByText('115 VIP', { exact: true }).waitFor()
    assert.equal(await personal.getByRole('cell', { name: '2', exact: true }).count(), 1)
    await page.setViewportSize({ width: 320, height: 900 })
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
    assert(await navigation.evaluate(el => el.scrollWidth <= el.clientWidth + 1))
    assert(await personal.getByRole('table').evaluate(table => table.parentElement.scrollWidth > table.parentElement.clientWidth))
    await page.setViewportSize({ width: 1440, height: 900 })
    await refreshOnly(drivesPath)
    const check = page.waitForResponse(response => new URL(response.url()).pathname === drivesPath + '/viewer/check')
    await personal.getByRole('button', { name: '检查 测试用户 的个人网盘' }).click()
    await check
    assert.equal(writes.at(-1).path, drivesPath + '/viewer/check')
    await page.goBack()
    await active('小号池')
    await tab('主账号').click()
    await active('主账号')
    passed.push('three-visible-tabs-and-scoped-refresh')
    await main.getByRole('button', { name: '更多操作' }).click()
    await main.getByRole('button', { name: '手动配置 Cookie' }).click()
    await page.getByRole('dialog').getByText(/重新登录同一 UID/).waitFor()
    await page.getByLabel('Cookie', { exact: true }).fill('UID=999_A; SEID=mock')
    await page.getByRole('button', { name: '验证并继续' }).click()
    await page.getByRole('alert').getByText('Cookie 无效，原配置未修改').waitFor()
    assert.equal(primary.uid, '2461229')
    rejectCookie = false
    await page.getByRole('button', { name: '验证并继续' }).click()
    await page.getByRole('dialog').waitFor({ state: 'detached' })
    await main.getByText(/UID：999/).waitFor()
    await main.getByText('普通用户',{exact:true}).waitFor()
    await main.locator('.primary-space').getByText('0 B', { exact: true }).waitFor()
    await main.getByRole('button', { name: '检测可用性' }).click()
    await main.getByText('最近检查通过', { exact: true }).waitFor()
    assert(reads.includes('/api/admin/p115/accounts/rotated-main/status'))
    assert.equal(pool[0].uid, '1218430')
    passed.push('cookie-failure-preserves-account-and-uid-change-refreshes-id')
    await main.getByRole('button', { name: '更多操作' }).click()
    await main.getByRole('button', { name: '清除登录信息' }).click()
    const before = writes.length
    await page.getByRole('dialog').getByText(/资源管理与使用主网盘/).waitFor()
    await page.getByRole('button', { name: '取消', exact: true }).click()
    assert.equal(writes.length, before)
    await main.getByRole('button', { name: '更多操作' }).click()
    await main.getByRole('button', { name: '清除登录信息' }).click()
    await page.getByRole('button', { name: '确认清除' }).click()
    await main.locator('.primary-status').getByText('未登录', { exact: true }).waitFor()
    await main.getByText('会员未知',{exact:true}).waitFor()
    await main.getByText('剩余空间未知',{exact:true}).waitFor()
    assert(pool.every(a => a.configured))
    await main.getByRole('button', { name: '更多操作' }).click()
    await main.getByRole('button', { name: '删除配置' }).click()
    rejectDelete = true
    await page.getByRole('button', { name: '确认删除' }).click()
    await page.getByRole('dialog').getByText('删除失败，请重试', { exact: true }).waitFor()
    rejectDelete = false
    await page.getByRole('button', { name: '确认删除' }).click()
    await main.getByRole('heading', { name: '主账号未配置' }).waitFor()
    assert.equal(await shared.count(), 0)
    await page.getByRole('button', { name: '配置主账号', exact: true }).click()
    await main.getByRole('heading', { name: '主账号', exact: true }).waitFor()
    assert.equal(writes.at(-1).body.primary, true)
    await tab('小号池').click()
    await shared.getByRole('button', { name: '添加共享小号', exact: true }).click()
    await shared.getByRole('heading', { name: '共享小号 7', exact: true }).waitFor()
    assert.equal(writes.at(-1).body.primary, false)
    passed.push('confirm-cancel-error-retry-and-explicit-account-creation')
    await page.goto(base + '/115/pool')
    await shared.getByRole('heading', { name: '共享小号 7', exact: true }).waitFor()
    assert.equal(new URL(page.url()).pathname, '/helper115'); assert.equal(new URL(page.url()).hash, '#pool')
    await active('小号池')
    assert(await navigation.evaluate(el => el.getBoundingClientRect().top >= 0))
    await page.goto(base + '/helper115#personal-drives')
    await active('个人网盘')
    await personal.getByText('115 VIP', { exact: true }).waitFor()
    await tab('小号池').click()
    rejectPool = true
    await page.reload()
    await shared.getByRole('alert').filter({ hasText: '小号池读取失败' }).waitFor()
    assert.equal(await main.count(), 0)
    assert.equal(await page.getByText(/尚未添加共享小号/).count(), 0)
    assert.equal(await page.getByRole('button', { name: '添加共享小号' }).isDisabled(), true)
    await tab('主账号').click()
    await main.getByRole('heading', { name: '主账号', exact: true }).waitFor()
    passed.push('direct-links-and-independent-load-errors')
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, pageErrors: errors }))
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exit(1) })
