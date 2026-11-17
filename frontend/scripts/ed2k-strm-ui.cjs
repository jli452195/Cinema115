// 所有 API 都在隔离浏览器中模拟，不读取真实登录凭据，也不连接网盘。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { selectOption, inputValue } = require('./select-menu-helpers.cjs')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1100 }, serviceWorkers: 'block' })
  await context.addInitScript(() => {
    if (!['http:', 'https:'].includes(location.protocol)) return
    localStorage.setItem('admin_token', 'fixture'); localStorage.setItem('cinema115-theme', 'light')
    Object.defineProperty(crypto, 'randomUUID', { value: undefined })
  })
  const errors = [], calls = [], endpoint = '/api/admin/p115/ed2k-strm'
  context.on('page', page => {
    page.on('pageerror', error => errors.push(error.message))
    page.on('console', message => { if (message.text().includes('[Vue warn]')) errors.push(message.text()) })
  })
  const page = await context.newPage(); page.setDefaultTimeout(10000)
  const hash = '0123456789abcdef0123456789abcdef', sha1 = 'A'.repeat(40), filename = '影片.S01E01.mkv'
  const link = `ed2k://|file|${filename}|104857600|${hash}|/`
  const state = { settings: { baseUrl: 'http://localhost:8080' }, settingsSaved: false, accountReady: true, primaryAccountId: 'main', cacheReady: true, cacheSetupHint: '', targets: [], tasks: [], resources: [] }
  function updateCacheReadiness() {
    state.cacheReady = state.accountReady && (!state.settings.cacheCID || state.settings.cacheAccountId === state.primaryAccountId)
    state.cacheSetupHint = !state.accountReady ? '请先登录主账号' : !state.cacheReady ? '主账号已变化，请重新选择或清除缓存目录。' : ''
  }
  let rejectSettings = false, rejectImport = true, nextTarget = 0, nextTask = 0, holdSettings = false, releaseSettings, settingsHeld
  await context.route('**/api/**', async route => {
    const request = route.request(), url = new URL(request.url()), method = request.method(), body = request.postDataJSON()
    calls.push({ path: url.pathname, method, body, query: Object.fromEntries(url.searchParams) })
    let data = {}, status = 200
    if (url.pathname === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (url.pathname.includes('/notifications')) data = { items: [], unread: 0 }
    else if (url.pathname === endpoint && method === 'GET') { updateCacheReadiness(); data = state }
    else if (url.pathname === endpoint + '/settings' && method === 'PUT') {
      if (holdSettings) await new Promise(resolve => { releaseSettings = resolve; settingsHeld?.() })
      assert.deepEqual(Object.keys(body).sort(), ['baseUrl', 'cacheAccountId', 'cacheCID', 'cachePath'])
      if (rejectSettings) { rejectSettings = false; status = 409; data = { error: '模拟设置保存失败' } }
      else {
        if (body.cacheCID) { assert.equal(body.cacheAccountId, state.primaryAccountId); assert.equal(state.accountReady, true) }
        else { assert.equal(body.cacheAccountId, ''); assert.equal(body.cachePath, '') }
        state.settings = { ...body, baseUrl: body.baseUrl.replace(/\/+$/, '') }; state.settingsSaved = true
        updateCacheReadiness(); data = state.settings
      }
    } else if (url.pathname === endpoint + '/targets' && method === 'POST') {
      data = { id: `target-${++nextTarget}`, ...body }; state.targets.push(data)
    } else if (url.pathname.startsWith(endpoint + '/targets/')) {
      const id = url.pathname.split('/').at(-1), index = state.targets.findIndex(target => target.id === id)
      if (method === 'PUT') { Object.assign(state.targets[index], body); data = state.targets[index] }
      else { state.targets.splice(index, 1); status = 204 }
    } else if (url.pathname === endpoint + '/imports' && method === 'POST') {
      assert.deepEqual(Object.keys(body).sort(), ['links', 'requestId', 'targetId'])
      assert.match(body.requestId, /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/)
      assert.ok(state.targets.some(target => target.id === body.targetId))
      if (rejectImport) { rejectImport = false; status = 504; data = { error: '模拟响应丢失，请重试核对' } }
      else {
        if (!state.resources.length) state.resources.push({ hash, size: 104857600, name: filename, status: 'queued', cacheState: 'unknown', message: '等待缓存资料', updatedAt: new Date().toISOString() })
        const target = state.targets.find(value => value.id === body.targetId)
        state.tasks.push({ id: `task-${++nextTask}`, hash, size: 104857600, name: filename, targetName: target.name, status: 'waiting', message: '等待文件资料就绪', createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() })
        status = 202; data = { resources: state.resources, tasks: state.tasks }
      }
    } else if (url.pathname.endsWith('/retry') && url.pathname.includes('/tasks/')) {
      state.tasks.find(task => url.pathname.endsWith(`/tasks/${task.id}/retry`)).status = 'queued'; data = { accepted: true }
    } else if (url.pathname.includes('/tasks/') && method === 'DELETE') {
      state.tasks = state.tasks.filter(task => !url.pathname.endsWith('/' + task.id)); status = 204
    } else if (url.pathname === endpoint + `/resources/${hash}/check`) {
      assert.deepEqual(body, { size: 104857600 }); state.resources[0].cacheState = 'missing'; state.resources[0].message = '缓存文件已删除'; data = { accepted: true }
    } else if (url.pathname === endpoint + `/resources/${hash}/restore`) {
      assert.deepEqual(body, { size: 104857600 }); Object.assign(state.resources[0], { status: 'recovering', message: '正在利用已知副本恢复缓存' }); status = 202; data = { accepted: true }
    } else if (url.pathname === '/api/admin/p115/cloud-download/paths') {
      assert.equal(method, 'GET', '缓存目录选择只能读取主账号目录')
      assert.equal(url.searchParams.get('accountId'), state.primaryAccountId)
      data = { directories: url.searchParams.get('cid') === '0' ? [{ id: 'cache-selected', name: '媒体缓存' }] : [], hasMore: false }
    } else if (url.pathname === '/api/admin/p115/share/output-directories') data = { path: '.', displayPath: '/strm', directories: [], truncated: false }
    if (status === 204) return route.fulfill({ status, body: '' })
    await route.fulfill({ status, json: data })
  })
  const base = (process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/115/ed2k-strm'
  const writeCalls = () => calls.filter(call => !['GET', 'HEAD', 'OPTIONS'].includes(call.method))
  const importCalls = () => calls.filter(call => call.path === endpoint + '/imports')
  const tabNames = { generate: '生成 STRM', resources: '资源缓存', settings: '基础设置' }
  const tabNav = targetPage => targetPage.getByRole('navigation', { name: 'ED2K STRM 功能视图', exact: true })
  const tab = (key, targetPage = page) => tabNav(targetPage).getByRole('button', { name: tabNames[key], exact: true })
  const expectedTab = key => key === 'generate' ? null : key
  async function selectedTab(key, targetPage = page) {
    await targetPage.waitForFunction(value => new URL(location.href).searchParams.get('tab') === value, expectedTab(key))
    await targetPage.waitForFunction(name => [...document.querySelectorAll('nav[aria-label="ED2K STRM 功能视图"] button')].some(button => button.getAttribute('aria-label') === name && button.getAttribute('aria-current') === 'page'), tabNames[key])
  }
  async function openTab(key, targetPage = page) { await tab(key, targetPage).click(); await selectedTab(key, targetPage) }
  const settings = page.getByRole('form', { name: 'ED2K STRM 基础设置' }), imports = page.getByRole('form', { name: '导入 ED2K' })
  const tasks = page.getByRole('region', { name: 'STRM 生成任务' }), resources = page.getByRole('region', { name: 'ED2K 资源缓存' })
  const directories = page.getByRole('region', { name: '本地输出目录', exact: true }), cards = directories.locator('.es-target-card')
  const address = settings.getByLabel('STRM 播放地址'), textarea = imports.getByLabel('ED2K 链接')
  const cache = settings.getByRole('group', { name: '主网盘缓存目录', exact: true })
  const cacheLocation = settings.locator('output#es-cache-directory')
  const cachePicker = page.getByRole('dialog', { name: '选择主网盘缓存目录', exact: true })
  const chooseCache = () => cache.getByRole('button', { name: '选择目录', exact: true }).click()
  async function selectCache(root = false) {
    await chooseCache(); await cachePicker.locator('.dp-content[aria-busy="false"]').waitFor()
    if (!root) { await cachePicker.getByRole('button', { name: '媒体缓存', exact: true }).click(); await cachePicker.locator('.dp-content[aria-busy="false"]').waitFor() }
    await cachePicker.getByRole('button', { name: '选择此目录', exact: true }).click(); await cachePicker.waitFor({ state: 'hidden' })
  }
  const targetSelect = imports.getByRole('combobox', { name: '导入输出目录', exact: true })
  const submit = imports.getByRole('button', { name: /^(导入并生成 STRM|生成 STRM|开始生成)$/ })
  const emptyTasks = () => page.getByRole('status', { name: 'STRM 生成任务状态', exact: true })
  async function refresh() {
    const response = page.waitForResponse(value => new URL(value.url()).pathname === endpoint && value.request().method() === 'GET')
    await page.getByRole('button', { name: '刷新状态', exact: true }).click()
    await response; await page.getByRole('button', { name: '刷新状态', exact: true }).waitFor()
  }
  const screenshotDir = process.env.ED2K_STRM_UI_ARTIFACTS && path.resolve(process.env.ED2K_STRM_UI_ARTIFACTS)
  if (screenshotDir) await fs.mkdir(screenshotDir, { recursive: true })
  async function screenshot(name) {
    if (screenshotDir) await page.screenshot({ path: path.join(screenshotDir, name), fullPage: true, animations: 'disabled' })
  }
  try {
    await page.goto(base, { waitUntil: 'networkidle' }); await imports.waitFor(); await selectedTab('generate')
    assert.equal(await settings.isVisible(), false); assert.equal(await resources.isVisible(), false)
    const idle = emptyTasks(); await idle.waitFor()
    const idleHeight = await idle.evaluate(element => element.getBoundingClientRect().height)
    assert.ok(idleHeight <= 96, `零任务仅保留紧凑提示：${idleHeight}px`)
    await textarea.fill(link); assert.equal(await submit.isDisabled(), true)
    assert.equal(writeCalls().length, 0, '读取默认页签不能创建离线或输出任务')

    // 页签和历史切换共用一个页面实例，链接与基础设置草稿都必须保留。
    await page.getByRole('button', { name: '前往基础设置', exact: true }).click(); await selectedTab('settings')
    assert.equal(await cacheLocation.textContent(), '/Cinema115-ED2K缓存目录')
    assert.equal(await cache.getByRole('button', { name: '清除选择', exact: true }).count(), 0, '空配置直接使用固定目录，无需选择操作')
    assert.equal(await cards.count(), 0)
    assert.equal(await directories.getByRole('button', { name: '添加目录', exact: true }).count(), 0)
    const emptyAdd = directories.getByRole('button', { name: '添加输出目录', exact: true })
    await emptyAdd.focus(); await emptyAdd.press('Enter')
    const emptyEditor = page.getByRole('dialog', { name: '添加输出目录', exact: true })
    await emptyEditor.waitFor(); await page.keyboard.press('Escape'); await emptyEditor.waitFor({ state: 'hidden' })
    await screenshot('settings-0-1440-light.png')
    await address.fill('http://cinema.example:8080/')
    await openTab('resources'); assert.equal(await imports.isVisible(), false); assert.equal(await settings.isVisible(), false)
    await page.goBack(); await selectedTab('settings'); assert.equal(await address.inputValue(), 'http://cinema.example:8080/')
    await page.goBack(); await selectedTab('generate'); assert.equal(await textarea.inputValue(), link)
    await page.goForward(); await selectedTab('settings'); assert.equal(await address.inputValue(), 'http://cinema.example:8080/')
    await page.goForward(); await selectedTab('resources')
    assert.equal(writeCalls().length, 0, '页签、浏览器前进后退不能触发写请求')
    await openTab('settings'); await refresh(); assert.equal(await address.inputValue(), 'http://cinema.example:8080/', '刷新保留地址草稿')
    rejectSettings = true
    await settings.getByRole('button', { name: '保存设置', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '模拟设置保存失败' }).waitFor()
    await settings.getByRole('button', { name: '保存设置', exact: true }).click(); await settings.getByRole('status', { name: '基础设置保存状态', exact: true }).waitFor()
    assert.equal(await settings.getByRole('button', { name: '保存设置', exact: true }).count(), 0, '未修改时使用状态文字，不保留禁用主按钮')

    // 缓存目录使用主账号浏览，选择只改草稿，统一保存且允许显式根目录。
    const beforeCacheDraft = writeCalls().length
    state.accountReady = false; await refresh()
    assert.equal(await cache.getByRole('button', { name: '选择目录', exact: true }).isDisabled(), true, '未登录主账号不能浏览缓存目录')
    state.accountReady = true; await refresh()
    await selectCache()
    assert.equal(await cacheLocation.textContent(), '/媒体缓存')
    await openTab('resources'); await openTab('settings'); await refresh()
    assert.equal(await cacheLocation.textContent(), '/媒体缓存', '切页及刷新保留缓存目录草稿')
    assert.equal(writeCalls().length, beforeCacheDraft, '浏览和选择缓存目录不能自动保存或提交任务')
    await settings.getByRole('button', { name: '还原修改', exact: true }).click()
    assert.match(await cacheLocation.textContent(), /Cinema115-ED2K缓存目录/)
    await selectCache()
    await settings.getByRole('button', { name: '保存设置', exact: true }).click(); await settings.getByRole('status', { name: '基础设置保存状态', exact: true }).waitFor()
    assert.deepEqual(calls.filter(call => call.path === endpoint + '/settings').at(-1).body, { baseUrl: 'http://cinema.example:8080', cacheAccountId: 'main', cacheCID: 'cache-selected', cachePath: '/媒体缓存' })
    await selectCache(true)
    assert.equal(await cacheLocation.textContent(), '/', 'CID 0 明确表示主网盘根目录')
    await settings.getByRole('button', { name: '保存设置', exact: true }).click(); await settings.getByRole('status', { name: '基础设置保存状态', exact: true }).waitFor()
    assert.equal(state.settings.cacheCID, '0')

    await openTab('generate'); assert.equal(await submit.isDisabled(), true, '没有输出目录不能导入')
    await page.getByRole('button', { name: '新建目录', exact: true }).click()
    const quickEditor = page.getByRole('dialog', { name: '添加输出目录', exact: true })
    await quickEditor.getByLabel('目录名称').fill('电影'); await quickEditor.getByLabel('本地输出目录').fill('ed2k/电影')
    await quickEditor.getByRole('button', { name: '保存目录', exact: true }).click(); await quickEditor.waitFor({ state: 'hidden' })
    await selectedTab('generate'); assert.equal(await textarea.inputValue(), link, '快捷添加目录不能丢失链接')
    await openTab('settings')
    assert.equal(await cards.count(), 1)
    assert.equal(await cards.first().getByText(/默认/).count(), 0, '首个目录不代表默认目录')
    assert.equal(await cards.first().getByText('未分配 Bot', { exact: true }).count(), 1)
    await screenshot('settings-1-1440-light.png')
    await page.getByRole('button', { name: '添加目录', exact: true }).click()
    const editor = page.getByRole('dialog', { name: '添加输出目录', exact: true })
    await editor.getByLabel('目录名称').fill('剧集'); await editor.getByLabel('本地输出目录').fill('ed2k/剧集')
    await editor.getByRole('button', { name: '保存目录', exact: true }).click(); await editor.waitFor({ state: 'hidden' })
    await cards.nth(1).waitFor()
    assert.equal(await cards.count(), 2)
    const editTarget = page.getByRole('button', { name: '编辑目录 剧集', exact: true })
    await editTarget.focus(); await editTarget.press('Enter')
    const editing = page.getByRole('dialog', { name: '编辑输出目录', exact: true })
    await editing.getByLabel('目录名称').fill('电视剧'); await editing.getByRole('button', { name: '保存目录', exact: true }).click(); await editing.waitFor({ state: 'hidden' })
    await screenshot('settings-2-1440-light.png')
    await openTab('generate'); await selectOption(targetSelect, 'target-1')
    await submit.click(); await page.getByRole('alert').filter({ hasText: '模拟响应丢失' }).waitFor()
    assert.equal(await textarea.inputValue(), link)
    const writesBeforeDrafts = writeCalls().length
    await openTab('settings'); await address.fill('http://draft.example:8080')
    await openTab('resources'); await openTab('generate')
    assert.equal(await textarea.inputValue(), link); assert.equal(await inputValue(targetSelect), 'target-1')
    assert.equal(await submit.isDisabled(), true, '跨页保留的未保存地址必须阻止提交')
    assert.equal(writeCalls().length, writesBeforeDrafts)
    await page.getByRole('button', { name: '前往基础设置', exact: true }).click(); await selectedTab('settings')
    assert.equal(await address.inputValue(), 'http://draft.example:8080')
    await settings.getByRole('button', { name: '还原修改', exact: true }).click(); await openTab('generate')
    await submit.click(); await tasks.getByText('等待缓存', { exact: true }).waitFor()
    assert.equal(importCalls()[0].body.requestId, importCalls()[1].body.requestId, '跨页后重试仍使用原请求编号')
    assert.equal(importCalls()[1].body.targetId, 'target-1')

    Object.assign(state.resources[0], { sha1, status: 'ready', cacheState: 'ready', message: 'STRM 已生成，缓存可用' }); state.tasks = []
    await refresh(); await openTab('settings')

    // 账号变化使旧目录失效，并关闭已打开的选择器；只读核验仍可执行。
    const beforeAccountChange = writeCalls().length
    await chooseCache(); await cachePicker.locator('.dp-content[aria-busy="false"]').waitFor()
    state.primaryAccountId = 'main-replaced'
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await cachePicker.waitFor({ state: 'hidden' })
    await cache.getByText('主账号已变化，请重新选择或清除缓存目录。', { exact: true }).waitFor()
    await address.fill('http://cannot-save-old-directory.example')
    assert.equal(await settings.getByRole('button', { name: '保存设置', exact: true }).isDisabled(), true, '旧账号目录不能随地址修改沿用')
    await settings.getByRole('button', { name: '还原修改', exact: true }).click()
    await openTab('generate'); await textarea.fill(link)
    assert.equal(await submit.isDisabled(), true, '缓存目录不就绪时禁止新导入')
    await openTab('resources')
    const originalResource = resources.getByRole('row', { name: filename, exact: true })
    assert.equal(await originalResource.getByRole('button', { name: '恢复缓存', exact: true }).isDisabled(), true, '缓存目录不就绪时禁止手动恢复')
    assert.equal(await originalResource.getByRole('button', { name: '核验缓存', exact: true }).isEnabled(), true, '主账号可用时保留只读缓存核验')
    assert.equal(writeCalls().length, beforeAccountChange, '账号变化不会自动改投目录或提交恢复')
    await openTab('settings'); await selectCache(true)
    assert.equal(await settings.getByRole('button', { name: '保存设置', exact: true }).isEnabled(), true, '重新选择目录可修复旧配置的不可用状态')
    await settings.getByRole('button', { name: '保存设置', exact: true }).click(); await settings.getByRole('status', { name: '基础设置保存状态', exact: true }).waitFor()
    assert.equal(state.settings.cacheAccountId, 'main-replaced')
    state.primaryAccountId = 'main'; await refresh()
    const clearCache = cache.getByRole('button', { name: '清除选择', exact: true })
    for (const width of [375, 320]) {
      await page.setViewportSize({ width, height: 844 })
      assert.equal(await clearCache.evaluate(element => {
        const bounds = element.getBoundingClientRect(), group = element.closest('.es-cache').getBoundingClientRect()
        return bounds.left >= group.left && bounds.right <= group.right && bounds.height >= 44 && element.scrollWidth <= element.clientWidth + 1
      }), true, `${width}px 清除选择按钮完整显示且具备触控区域`)
    }
    await page.setViewportSize({ width: 1440, height: 1100 })
    const beforeClear = writeCalls().length
    await clearCache.click()
    assert.equal(await cacheLocation.textContent(), '/Cinema115-ED2K缓存目录')
    assert.equal(await clearCache.count(), 0, '清除后立即显示固定目录，无需再次选择')
    assert.equal(writeCalls().length, beforeClear, '清除只修改草稿，保存后生效')
    await settings.getByRole('button', { name: '保存设置', exact: true }).click(); await settings.getByRole('status', { name: '基础设置保存状态', exact: true }).waitFor()
    assert.equal(state.settings.cacheCID, ''); assert.equal(state.settings.cacheAccountId, ''); assert.equal(state.settings.cachePath, '')
    await refresh(); assert.equal(await cacheLocation.textContent(), '/Cinema115-ED2K缓存目录')
    await address.fill('http://cache-tested.example:8080')
    await settings.getByRole('button', { name: '保存设置', exact: true }).click(); await settings.getByRole('status', { name: '基础设置保存状态', exact: true }).waitFor()
    assert.equal(state.settings.cacheCID, '', '保存其他设置时继续保留空配置，使用固定目录')
    assert.equal(state.resources.length, 1, '目录和账号配置变化不删除已有资源')
    await openTab('generate')

    const extra = (digit, name, status) => ({ hash: digit.repeat(32), size: 104857600, name, status, cacheState: status === 'ready' ? 'ready' : 'unknown', updatedAt: new Date().toISOString() })
    state.resources.push({ ...extra('1', '缓存处理中.mkv', 'recovering'), cacheState: 'missing' }, extra('2', '缓存需关注.mkv', 'failed'), extra('3', '缓存待确认.mkv', 'uncertain'), extra('4', '缓存身份冲突.mkv', 'blocked'), { ...extra('5', '缓存已缺失.mkv', 'ready'), cacheState: 'missing' })
    await refresh(); await emptyTasks().waitFor(); await selectOption(targetSelect, 'target-2')
    await openTab('resources'); await resources.waitFor()
    const resource = resources.getByRole('row', { name: filename, exact: true })
    await resource.getByText('缓存可用', { exact: true }).waitFor()
    assert.equal(await resources.getByText(/已生成/).count(), 0, '缓存页不展示旧的永久生成说明')
    const filter = resources.getByRole('navigation', { name: '按缓存状态筛选', exact: true })
    const beforeFiltering = writeCalls().length
    await filter.getByRole('button', { name: '处理中', exact: true }).click()
    assert.equal(await filter.getByRole('button', { name: '处理中', exact: true }).getAttribute('aria-pressed'), 'true')
    assert.equal(await resources.getByRole('row', { name: '缓存处理中.mkv', exact: true }).count(), 1)
    assert.equal(await resource.count(), 0); assert.equal(await resources.getByRole('row', { name: '缓存需关注.mkv', exact: true }).count(), 0)
    await filter.getByRole('button', { name: '需关注', exact: true }).click()
    for (const name of ['缓存需关注.mkv', '缓存待确认.mkv', '缓存身份冲突.mkv', '缓存已缺失.mkv']) assert.equal(await resources.getByRole('row', { name, exact: true }).count(), 1)
    assert.equal(await resources.getByRole('row', { name: '缓存处理中.mkv', exact: true }).count(), 0)
    const search = resources.getByRole('searchbox', { name: '筛选 ED2K 资源', exact: true })
    await search.fill('不存在'); await resources.getByText('没有匹配当前筛选条件的资源', { exact: true }).waitFor()
    await resources.getByRole('button', { name: '清除筛选条件', exact: true }).click()
    assert.equal(await search.inputValue(), ''); assert.equal(await filter.getByRole('button', { name: '全部', exact: true }).getAttribute('aria-pressed'), 'true')
    await filter.getByRole('button', { name: '需关注', exact: true }).click()
    await search.fill('需关注'); assert.equal(await resources.getByRole('row', { name: '缓存需关注.mkv', exact: true }).count(), 1)
    assert.equal(await resources.getByRole('row', { name: '缓存待确认.mkv', exact: true }).count(), 0)
    await search.fill(''); await filter.getByRole('button', { name: '全部', exact: true }).click()
    assert.equal(writeCalls().length, beforeFiltering, '状态筛选和搜索不能触发云端写入')
    const beforeGenerate = writeCalls().length
    await resource.getByRole('button', { name: '生成 STRM', exact: true }).click(); await selectedTab('generate')
    assert.equal(await textarea.inputValue(), `ed2k://|file|${encodeURIComponent(filename)}|104857600|${hash}|/`)
    assert.equal(await inputValue(targetSelect), 'target-2', '跨页生成保留已选目录')
    assert.equal(await textarea.evaluate(element => document.activeElement === element), true, '跨页生成聚焦链接输入')
    assert.equal(writeCalls().length, beforeGenerate, '资源生成按钮只预填，不自动提交')
    await submit.click(); await tasks.getByText('电视剧', { exact: false }).waitFor()
    assert.notEqual(importCalls()[2].body.requestId, importCalls()[1].body.requestId); assert.equal(importCalls()[2].body.targetId, 'target-2')
    state.tasks[0].status = 'failed'; state.tasks[0].message = '输出目录暂时不可写'
    await refresh(); await tasks.getByRole('button', { name: '重试', exact: true }).click(); await tasks.getByText('等待生成', { exact: true }).waitFor()
    state.tasks[0].status = 'failed'; await refresh()
    await tasks.getByRole('button', { name: '清理失败任务 ' + filename, exact: true }).click(); await emptyTasks().waitFor()
    await openTab('resources')
    await resource.getByRole('button', { name: '核验缓存', exact: true }).click(); await resource.getByText('缓存缺失', { exact: true }).waitFor()
    await resource.getByRole('button', { name: '恢复缓存', exact: true }).click(); await resource.getByText('恢复缓存中', { exact: true }).waitFor()
    await openTab('settings')
    const deleteTarget = page.getByRole('button', { name: '删除目录 电视剧', exact: true })
    await deleteTarget.focus(); await deleteTarget.press('Enter')
    const deleting = page.getByRole('dialog', { name: '删除输出目录', exact: true })
    await deleting.getByRole('button', { name: '删除配置', exact: true }).click(); await deleting.waitFor({ state: 'hidden' })
    assert.equal(state.resources.length, 6, '删除目录不清除资源缓存')

    // 新页面直接打开 query 深链接，全部请求仍由隔离上下文拦截。
    const beforeDeepLinks = writeCalls().length
    for (const key of ['settings', 'resources', 'generate']) {
      const deep = await context.newPage(); deep.setDefaultTimeout(10000)
      await deep.goto(base + (expectedTab(key) ? '?tab=' + key : ''), { waitUntil: 'networkidle' }); await selectedTab(key, deep)
      assert.equal(await deep.getByRole('form', { name: '导入 ED2K' }).isVisible(), key === 'generate')
      assert.equal(await deep.getByRole('form', { name: 'ED2K STRM 基础设置' }).isVisible(), key === 'settings')
      assert.equal(await deep.getByRole('region', { name: 'ED2K 资源缓存' }).isVisible(), key === 'resources')
      await deep.close()
    }
    assert.equal(writeCalls().length, beforeDeepLinks, '深链接读取不提交任务或恢复缓存')
    const longName = '完整目录名' + 'A'.repeat(45) + '季度归档'
    const longPath = 'ed2k/长路径/' + 'abcdefghijklmnopqrstuvwxyz'.repeat(5) + '/2026/全季原始文件'
    state.targets.push(
      { id: 'review-target', name: '电视剧', outputDir: 'ed2k/电视剧', assignedBotId: 'bot-one' },
      { id: 'long-target', name: longName, outputDir: longPath },
      { id: 'documentary-target', name: '纪录片', outputDir: 'ed2k/纪录片' },
      { id: 'animation-target', name: '动画', outputDir: 'ed2k/动画' },
    )
    state.tasks.push({ id: 'review-task', hash, size: 104857600, name: '影片.S01E02.mkv', targetName: '电视剧', status: 'failed', message: '输出目录暂时不可写，可检查后重试', createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() })
    await refresh()
    await cards.nth(4).waitFor()
    assert.equal(await cards.count(), 5)
    assert.equal(await directories.getByText('已分配 Bot', { exact: true }).count(), 1)
    assert.equal(await directories.getByText('bot-one', { exact: true }).count(), 0, '不能从绑定 ID 推导 Bot 名称')
    assert.equal(await cards.first().getByText(/默认/).count(), 0, '首张卡片无默认目录语义')

    // 保存过程中锁住目录操作，避免同时修改同一份配置。
    await address.fill('http://busy.example:8080')
    holdSettings = true
    const held = new Promise(resolve => { settingsHeld = resolve })
    try {
      await settings.getByRole('button', { name: '保存设置', exact: true }).click()
      await held
      const buttons = directories.getByRole('button')
      for (const button of await buttons.all()) assert.equal(await button.isDisabled(), true, '保存过程中添加、编辑、删除均禁用')
    } finally { holdSettings = false; releaseSettings?.(); settingsHeld = undefined }
    await settings.getByRole('status', { name: '基础设置保存状态', exact: true }).waitFor()
    assert.equal(await directories.getByRole('button', { name: '添加目录', exact: true }).isEnabled(), true)
    Object.assign(state.settings, { cacheAccountId: 'main', cacheCID: 'long-cache', cachePath: '/媒体缓存/' + '缓存完整路径'.repeat(12) })
    await refresh()

    const beforeScreenshots = writeCalls().length
    for (const key of ['generate', 'resources', 'settings']) {
      await openTab(key)
      for (const width of [1440, 390, 320]) {
        await page.setViewportSize({ width, height: width === 1440 ? 1100 : 844 })
        await page.waitForFunction(() => {
          const nav = document.querySelector('nav[aria-label="ED2K STRM 功能视图"]')
          if (!nav) return false
          const bounds = nav.getBoundingClientRect()
          return [...nav.querySelectorAll('button')].every(button => {
            const item = button.getBoundingClientRect(), label = button.querySelector('span').getBoundingClientRect()
            return item.left >= bounds.left - 1 && item.right <= bounds.right + 1 && label.left >= item.left && label.right <= item.right
          })
        }, undefined, { timeout: 2000 })
        for (const dark of [false, true]) {
          await page.evaluate(value => { document.documentElement.classList.toggle('dark', value); window.scrollTo({ top: 0, behavior: 'instant' }) }, dark)
          assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true, `${key} 页签在 ${width}px 不能横向溢出`)
          if (key === 'settings') {
            assert.equal(await cacheLocation.evaluate(element => element.scrollWidth <= element.clientWidth + 1), true, '缓存长路径完整换行，无横向溢出')
            assert.equal(await directories.getByRole('button', { name: '添加目录', exact: true }).isVisible(), true)
            assert.equal(await directories.locator('.es-target-add-slot').isVisible(), width >= 640)
            const longCard = cards.filter({ has: page.locator('strong', { hasText: longName }) })
            assert.equal(await longCard.locator('strong').textContent(), longName)
            assert.equal(await longCard.locator('.es-target-path').textContent(), '/strm/' + longPath)
            for (const text of [longCard.locator('strong'), longCard.locator('.es-target-path')]) {
              assert.equal(await text.evaluate(element => element.scrollWidth <= element.clientWidth + 1), true, '长名称与路径完整换行，无横向截断')
              assert.equal(await text.evaluate(element => element.scrollHeight <= element.clientHeight + 1), true, '长文本未被固定高度裁切')
            }
          }
          await screenshot(`${key}-${width}-${dark ? 'dark' : 'light'}.png`)
        }
      }
    }
    // 用实际卡片坐标检查列数，覆盖超宽、平板与小屏容器。
    await openTab('settings')
    for (const [width, columns] of [[2139, 3], [960, 2], [320, 1]]) {
      await page.setViewportSize({ width, height: width > 1000 ? 1100 : 900 })
      const count = await cards.evaluateAll(elements => {
        const firstTop = elements[0].getBoundingClientRect().top
        return elements.filter(element => Math.abs(element.getBoundingClientRect().top - firstTop) < 1).length
      })
      assert.equal(count, columns, `${width}px 设置页应有 ${columns} 列目录卡片`)
      for (const dark of [false, true]) {
        await page.evaluate(value => document.documentElement.classList.toggle('dark', value), dark)
        await screenshot(`settings-5-${width}-${dark ? 'dark' : 'light'}.png`)
      }
    }
    assert.equal(writeCalls().length, beforeScreenshots, '响应式布局与主题切换不产生写请求')
    assert.deepEqual(errors, [])
    console.log('ED2K STRM UI：三页签/深链接/历史、固定缓存目录与自定义目录选择/保存/清除/账号变化、只读核验与恢复边界、幂等保留、0/1/2/5目录卡片及键盘CRUD、处理中禁用、任务与缓存操作、跨页预填、筛选、长文本换行及深浅色2139/960/390/320布局通过。')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
