// 独立页面回归拦截所有接口，不登录网盘、不提交真实离线任务。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1100 }, serviceWorkers: 'block' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'fixture'); localStorage.setItem('cinema115-theme', 'light') })
  const page = await context.newPage(), errors = [], calls = [], endpoint = '/api/admin/p115/ed2k-strm'
  page.on('pageerror', error => errors.push(error.message)); page.setDefaultTimeout(10000)
  const hash = '0123456789abcdef0123456789abcdef', sha1 = 'A'.repeat(40)
  const link = `ed2k://|file|影片.S01E01.mkv|104857600|${hash}|/`
  const state = { settings: { baseUrl: 'http://localhost:8080', outputDir: 'ed2k' }, settingsSaved: false, accountReady: true, primaryAccountId: 'main', cacheCID: '', cachePath: '', resources: [] }
  let rejectSettings = false
  await page.route('**/api/**', async route => {
    const request = route.request(), url = new URL(request.url()), method = request.method(), body = request.postDataJSON()
    calls.push({ path: url.pathname, method, body })
    let data = {}, status = 200
    if (url.pathname === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (url.pathname.includes('/notifications')) data = { items: [], unread: 0 }
    else if (url.pathname === endpoint && method === 'GET') data = state
    else if (url.pathname === endpoint + '/settings' && method === 'PUT') {
      if (rejectSettings) { rejectSettings = false; status = 409; data = { error: '模拟设置保存失败' } }
      else { state.settings = { ...body, baseUrl: body.baseUrl.replace(/\/+$/, '') }; state.settingsSaved = true; data = state.settings }
    } else if (url.pathname === endpoint + '/imports' && method === 'POST') {
      assert.deepEqual(body, { links: [link] })
      state.resources = [{ hash, size: 104857600, name: '影片.S01E01.mkv', status: 'queued', cacheState: 'unknown', message: '等待后台处理', updatedAt: new Date().toISOString() }]
      status = 202; data = { resources: state.resources }
    } else if (url.pathname === endpoint + `/resources/${hash}/check`) {
      assert.deepEqual(body, { size: 104857600 }); state.resources[0].cacheState = 'missing'; state.resources[0].message = '缓存文件已删除，STRM 仍保留'; data = { accepted: true }
    } else if (url.pathname === endpoint + `/resources/${hash}/restore`) {
      assert.deepEqual(body, { size: 104857600 }); Object.assign(state.resources[0], { status: 'recovering', message: '正在利用已知副本恢复缓存' }); status = 202; data = { accepted: true }
    } else if (url.pathname === '/api/admin/p115/share/output-directories') data = { path: '.', displayPath: '/strm', directories: [], truncated: false }
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
  })
  try {
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5193') + '/115/ed2k-strm', { waitUntil: 'networkidle' })
    const settings = page.getByRole('form', { name: 'ED2K STRM 基础设置' }), imports = page.getByRole('form', { name: '导入 ED2K' })
    const address = settings.getByLabel('STRM 播放地址'), output = settings.getByLabel('本地输出目录'), submit = imports.getByRole('button', { name: '导入并生成 STRM' })
    await settings.waitFor()
    await imports.getByLabel('ED2K 链接').fill(link)
    assert.equal(await submit.isDisabled(), true, '未保存设置时不能启动导入')
    assert.equal(calls.some(call => call.method === 'POST'), false, '页面读取不能启动恢复或离线')
    await address.fill('http://cinema.example:8080/')
    await output.fill('ed2k/电影')
    await page.getByRole('button', { name: '刷新状态', exact: true }).click()
    assert.equal(await address.inputValue(), 'http://cinema.example:8080/', '后台刷新不能覆盖设置草稿')
    rejectSettings = true
    await settings.getByRole('button', { name: '保存设置', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '模拟设置保存失败' }).waitFor()
    assert.equal(await output.inputValue(), 'ed2k/电影', '保存失败时保留草稿')
    await settings.getByRole('button', { name: '保存设置', exact: true }).click()
    await settings.getByRole('button', { name: '已保存', exact: true }).waitFor()
    assert.equal(await address.inputValue(), 'http://cinema.example:8080')
    assert.equal(calls.some(call => call.path.endsWith('/restore')), false, '保存设置不能触发恢复')
    await output.fill('未保存目录')
    assert.equal(await submit.isDisabled(), true, '导入不能静默采用与草稿不同的旧输出目录')
    await settings.getByRole('button', { name: '还原修改', exact: true }).click()
    await submit.click()
    const resource = page.getByRole('article', { name: '影片.S01E01.mkv' })
    await resource.waitFor()
    assert.equal(await imports.getByLabel('ED2K 链接').inputValue(), '')
    assert.equal(await resource.getByRole('button', { name: '恢复缓存', exact: true }).isDisabled(), true, '活跃任务不应重复提交')
    Object.assign(state.resources[0], { sha1, status: 'ready', cacheState: 'ready', strmPath: 'ed2k/电影/影片.S01E01.strm', message: 'STRM 已生成' })
    state.cacheCID = '12345'; state.cachePath = '/Cinema115-ED2K'
    await page.getByRole('button', { name: '刷新状态', exact: true }).click()
    await resource.getByText('STRM 已生成', { exact: true }).waitFor()
    await resource.getByRole('button', { name: '核验缓存', exact: true }).click()
    await resource.getByText('缓存文件已删除，STRM 仍保留', { exact: true }).waitFor()
    assert.equal(await resource.getByText('ed2k/电影/影片.S01E01.strm', { exact: true }).count(), 1)
    await resource.getByRole('button', { name: '恢复缓存', exact: true }).click()
    await resource.getByText('恢复缓存中', { exact: true }).waitFor()
    assert.equal(calls.filter(call => call.path.endsWith('/restore')).length, 1)
    await page.getByRole('searchbox', { name: '筛选 ED2K 资源' }).fill('不存在')
    await page.getByText('没有匹配的资源', { exact: true }).waitFor()
    await page.getByRole('button', { name: '清除筛选', exact: true }).click()
    const screenshotDir = path.resolve('test-results/ed2k-strm')
    await fs.mkdir(screenshotDir, { recursive: true })
    await page.screenshot({ path: path.join(screenshotDir, 'desktop.png'), fullPage: true })
    for (const width of [390, 320]) {
      await page.setViewportSize({ width, height: 844 })
      await page.evaluate(() => document.documentElement.classList.add('dark'))
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true, `${width}px 页面不能横向溢出`)
      await page.screenshot({ path: path.join(screenshotDir, `dark-${width}.png`), fullPage: true })
    }
    assert.deepEqual(errors, [])
    console.log('ED2K STRM UI：草稿保留、失败重试、导入、核验、恢复、只读刷新及手机深色布局通过。')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
