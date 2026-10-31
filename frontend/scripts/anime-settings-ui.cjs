// All API requests, including directory operations, stay inside this fixture.
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
  const page = await context.newPage(), calls = [], errors = [], passed = [], receipts = new Map()
  page.setDefaultTimeout(10000); page.on('pageerror', e => errors.push(e.message))
  const state = { primary: true, stale: false, failRead: false, loseRSS: false, loseDirectory: false, conflict: false, gate: null, rssWrites: 0, directoryWrites: 0 }
  let rss = { revision: 1, pollMinutes: 15, autoArchive: true, inactiveArchiveDays: 60 }
  let settings = { revision: 1, accountId: 'main', downloadCid: '100', downloadPath: '/下载', organizeCid: '900', organizePath: '/整理' }
  await page.route('**/api/**', async route => {
    const req = route.request(), url = new URL(req.url()), endpoint = url.pathname, method = req.method(), body = req.postDataJSON(), key = req.headers()['idempotency-key']
    calls.push({ endpoint, method, body, key, query: url.search }); let data = {}, status = 200
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
    else if (endpoint.endsWith('/catalog/mikan')) data = { provider: 'mikan', days: [], items: [], fetchedAt: Date.now(), expiresAt: Date.now() + 600000 }
    else if (endpoint.endsWith('/subscriptions')) data = { items: [], total: 0, page: 1, pageSize: 20 }
    else if (endpoint.endsWith('/rss-settings')) {
      if (method === 'GET') data = rss
      else {
        if (state.gate) await state.gate
        if (state.conflict) { state.conflict = false; status = 409; data = { error: '配置已变化，请重新读取', code: 'REVISION_CONFLICT' } }
        else if (receipts.has(key)) { assert.deepEqual(body, receipts.get(key).body); data = receipts.get(key).data }
        else {
          assert.equal(body.revision, rss.revision); state.rssWrites++
          rss = { ...body, revision: rss.revision + 1 }; data = rss; receipts.set(key, { body, data })
          if (state.loseRSS) { state.loseRSS = false; return route.abort() }
        }
      }
    } else if (endpoint.endsWith('/settings') && method === 'GET') {
      if (state.failRead) { status = 503; data = { error: '模拟配置读取失败' } }
      else data = { settings, primaryAccount: state.primary ? { id: state.stale ? 'other-main' : 'main', name: '主网盘' } : null, readiness: { ready: state.primary && !state.stale, issues: !state.primary ? ['PRIMARY_ACCOUNT_REQUIRED'] : state.stale ? ['ACCOUNT_SNAPSHOT_CHANGED'] : [] } }
    } else if (method === 'PATCH' && /\/settings(?:\/organize-directory)?$/.test(endpoint)) {
      if (receipts.has(key)) { assert.deepEqual(body, receipts.get(key).body); data = receipts.get(key).data }
      else {
        assert.equal(body.expectedRevision, settings.revision)
        const organize = endpoint.endsWith('/organize-directory')
        if (!organize) { assert.equal(body.expectedPrimaryAccountId, 'main'); assert.equal('expectedPrimaryAccountRevision' in body, false) }
        state.directoryWrites++; settings = { ...settings, revision: settings.revision + 1, ...(organize ? { organizeCid: body.cid, organizePath: '/新整理' } : { downloadCid: body.downloadCid, downloadPath: '/新下载' }) }
        data = organize ? { id: 'anime-settings', revision: settings.revision, settings } : settings
        receipts.set(key, { body, data })
        if (state.loseDirectory) { state.loseDirectory = false; return route.abort() }
      }
    } else if (endpoint.includes('/cloud-download/paths')) {
      assert.equal(url.searchParams.get('accountId'), 'main')
      data = { directories: url.searchParams.get('cid') === '0' ? [{ id: '101', name: '新下载' }, { id: '901', name: '新整理' }] : [], hasMore: false }
    }
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
  })
  const rssPanel = page.getByRole('region', { name: 'RSS 设置', exact: true })
  const storage = page.getByRole('region', { name: '下载与整理目录', exact: true })
  const minutes = rssPanel.getByRole('textbox', { name: '更新检查间隔' })
  const save = rssPanel.getByRole('button', { name: '保存 RSS 设置', exact: true })
  const toRSS = () => page.getByRole('button', { name: 'RSS 设置', exact: true }).click()
  const toStorage = async () => { await page.getByRole('button', { name: '下载与整理', exact: true }).click(); await storage.locator('.storage-directory').first().waitFor() }
  const push = url => page.evaluate(async url => document.querySelector('#app').__vue_app__.config.globalProperties.$router.push(url), url)
  const discard = page.getByRole('dialog', { name: '放弃未保存的 RSS 修改？', exact: true })
  async function choose(kind) {
    await storage.getByRole('button', { name: kind === 'download' ? '选择下载目录' : '选择整理目录', exact: true }).click()
    const picker = page.getByRole('dialog', { name: kind === 'download' ? '动漫订阅下载目录' : '动漫整理目录2', exact: true })
    await picker.getByRole('button', { name: kind === 'download' ? '新下载' : '新整理', exact: true }).click()
    await picker.getByRole('button', { name: kind === 'download' ? '保存此目录' : '保存整理目录', exact: true }).click()
    await picker.waitFor({ state: 'detached' })
  }
  try {
    await fs.mkdir('test-results', { recursive: true })
    const returnTo = '/anime?source=mikan&day=1&page=3'
    await page.goto(origin + '/anime/settings?returnTo=' + encodeURIComponent(returnTo), { waitUntil: 'networkidle' })
    assert.equal(await minutes.inputValue(), '15'); assert.equal(await save.isDisabled(), true)
    assert.equal(await page.getByRole('link', { name: '返回选番', exact: true }).getAttribute('href'), returnTo)
    assert.equal(calls.filter(c => c.endpoint.includes('/cloud-download')).length, 0)
    await page.screenshot({ path: 'test-results/anime-settings-desktop.png' })
    passed.push('独立设置页按需读取本地配置，保留选番返回位置，初始零115请求')
    await minutes.fill('9'); assert.equal(await save.isDisabled(), true)
    await minutes.fill('1441'); assert.equal(await save.isDisabled(), true)
    await minutes.fill('abc'); assert.equal(await save.isDisabled(), true)
    await minutes.fill('30'); await rssPanel.getByRole('radio', { name: '90 天', exact: true }).check()
    await toStorage(); await toRSS(); assert.equal(await minutes.inputValue(), '30')
    await page.getByRole('link', { name: '我的订阅', exact: true }).click(); await discard.waitFor()
    assert.equal(await discard.getByRole('button', { name: '继续编辑' }).evaluate(el => getComputedStyle(el).backgroundColor), 'rgb(237, 241, 246)')
    await discard.getByRole('button', { name: '继续编辑' }).click(); assert.equal(await minutes.inputValue(), '30')
    await save.click(); await rssPanel.getByText('RSS 设置已保存', { exact: true }).waitFor()
    assert.equal(state.rssWrites, 1); assert.equal(settings.revision, 1)
    passed.push('非法间隔阻止保存，跨分类保留输入，离开保护与按钮主题正确，RSS保存不改变目录版本')
    await minutes.fill('45'); state.loseRSS = true; await save.click()
    const confirm = rssPanel.getByRole('button', { name: '核对原保存请求', exact: true }); await confirm.waitFor()
    assert.equal(await minutes.isDisabled(), true)
    assert.equal(await page.getByRole('button', { name: '下载与整理', exact: true }).isDisabled(), true)
    await push('/anime/settings?tab=storage'); assert.equal(new URL(page.url()).searchParams.get('tab'), null)
    await page.getByRole('link', { name: '我的订阅', exact: true }).click(); assert.equal(new URL(page.url()).pathname, '/anime/settings')
    const original = structuredClone(calls.filter(c => c.endpoint.endsWith('/rss-settings') && c.method === 'PATCH').at(-1))
    await confirm.click(); await confirm.waitFor({ state: 'detached' })
    assert.deepEqual(calls.filter(c => c.endpoint.endsWith('/rss-settings') && c.method === 'PATCH').at(-1), original)
    assert.equal(state.rssWrites, 2)
    passed.push('RSS保存丢回执后锁定输入与导航，原key和正文核对且不重复写入')
    await minutes.fill('60'); state.conflict = true; await save.click()
    await rssPanel.getByText('配置已变化，请重新读取', { exact: true }).waitFor()
    assert.equal(await minutes.inputValue(), '60')
    await rssPanel.getByRole('button', { name: '重新读取设置', exact: true }).click(); await discard.waitFor()
    await discard.getByRole('button', { name: '继续编辑' }).click(); assert.equal(await minutes.inputValue(), '60')
    await rssPanel.getByRole('button', { name: '重新读取设置', exact: true }).click()
    await discard.getByRole('button', { name: '放弃修改并重新读取' }).click()
    await page.waitForFunction(() => document.querySelector('#anime-rss-minutes')?.value === '45')
    passed.push('版本冲突保留输入，重载前可取消或确认放弃，无需离开页面')
    await toStorage()
    const trigger = storage.getByRole('button', { name: '选择下载目录', exact: true })
    await trigger.click(); await page.getByRole('dialog', { name: '动漫订阅下载目录' }).waitFor()
    await page.keyboard.press('Escape'); await page.getByRole('dialog', { name: '动漫订阅下载目录' }).waitFor({ state: 'detached' })
    assert.equal(await trigger.evaluate(el => el === document.activeElement), true)
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    await choose('download'); await storage.getByText('/新下载', { exact: true }).waitFor(); assert.equal(settings.organizeCid, '900')
    state.loseDirectory = true; await choose('organize')
    const directoryConfirm = storage.getByRole('button', { name: '核对原目录保存', exact: true }); await directoryConfirm.waitFor()
    assert.equal(await page.getByRole('button', { name: 'RSS 设置', exact: true }).isDisabled(), true)
    await push('/anime/settings'); assert.equal(new URL(page.url()).searchParams.get('tab'), 'storage')
    const originalDirectory = structuredClone(calls.filter(c => c.method === 'PATCH').at(-1))
    await directoryConfirm.click(); await directoryConfirm.waitFor({ state: 'detached' })
    assert.deepEqual(calls.filter(c => c.method === 'PATCH').at(-1), originalDirectory); assert.equal(state.directoryWrites, 2)
    passed.push('目录选择即保存并保留另一个目录，未知结果禁止切换分类，原请求核对无重复操作')
    await trigger.click(); await page.getByRole('dialog', { name: '动漫订阅下载目录' }).waitFor()
    await push('/anime/subscriptions'); await storage.waitFor({ state: 'detached' })
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    passed.push('目录弹窗Esc恢复焦点，路由离开时释放滚动锁')
    state.primary = false; await push('/anime/settings?tab=storage'); await storage.getByRole('link', { name: '配置主账号' }).waitFor()
    assert.equal(await storage.getByRole('button', { name: '选择下载目录' }).isDisabled(), true)
    assert.equal(await storage.getByRole('button', { name: '选择整理目录' }).isDisabled(), true)
    state.primary = true; state.stale = true; await toRSS(); await toStorage()
    assert.equal(await storage.getByRole('button', { name: '选择下载目录' }).isEnabled(), true)
    assert.equal(await storage.getByRole('button', { name: '选择整理目录' }).isDisabled(), true)
    state.stale = false; state.failRead = true; await toRSS(); await page.getByRole('button', { name: '下载与整理', exact: true }).click()
    await storage.getByText('模拟配置读取失败', { exact: true }).waitFor(); state.failRead = false
    await storage.getByRole('button', { name: '重新读取设置' }).click(); await storage.locator('.storage-directory').first().waitFor()
    passed.push('缺主账号不回退，换号要求先选下载目录，本地目录配置读取失败可重试')
    for (const width of [320, 390]) {
      await page.setViewportSize({ width, height: 844 })
      for (const dark of [false, true]) {
        await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
        await toRSS(); assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
        assert.equal(await minutes.evaluate(el => el.getBoundingClientRect().width), 82)
        assert.equal(await page.getByRole('heading', { level: 1, name: '动漫订阅设置' }).evaluate(el => el.getBoundingClientRect().height < 40), true)
        await page.screenshot({ path: `test-results/anime-settings-${width}-${dark ? 'dark' : 'light'}.png`, fullPage: true })
        await toStorage(); assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
      }
    }
    settings = { ...settings, downloadPath: '/' + 'LongDownloadDirectory'.repeat(12), organizePath: '/' + 'LongOrganizeDirectory'.repeat(12) }
    await toRSS(); await toStorage()
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    assert.equal(await storage.locator('.storage-path').first().evaluate(el => getComputedStyle(el).color), 'rgb(226, 232, 240)')
    await page.screenshot({ path: 'test-results/anime-settings-storage-dark.png', fullPage: true })
    passed.push('320px与390px深浅主题无横向溢出，输入尺寸固定，长目录折行且文字可读')
    await toRSS(); await minutes.fill('90')
    let release; state.gate = new Promise(resolve => { release = resolve }); await save.click()
    assert.equal(await rssPanel.getByRole('button', { name: '保存中…', exact: true }).isDisabled(), true); assert.equal(await minutes.isDisabled(), true)
    await page.getByRole('link', { name: '我的订阅', exact: true }).click(); assert.equal(new URL(page.url()).pathname, '/anime/settings')
    release(); state.gate = null; await rssPanel.getByText('RSS 设置已保存', { exact: true }).waitFor()
    await minutes.fill('120'); await page.getByRole('link', { name: '我的订阅', exact: true }).click()
    await discard.getByRole('button', { name: '放弃修改并离开' }).click(); await page.waitForURL('**/anime/subscriptions')
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    passed.push('保存中阻止重复提交和离开；确认放弃后可以离开且释放弹窗锁')
    assert.deepEqual(errors, []); console.log(JSON.stringify({ passed, apiCalls: calls.length, errors }, null, 2))
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
