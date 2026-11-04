// 整理记录隔离验证：所有接口均为模拟，不写入 NAS 或 115。
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const clone = value => JSON.parse(JSON.stringify(value))
const gate = () => { let enter, release; const ready = new Promise(resolve => { enter = resolve }), pending = new Promise(resolve => { release = resolve }); return { ready, release, wait: () => { enter(); return pending } } }
// 慢请求使用独立浏览器上下文与全模拟接口，不改变真实页面或后端耗时。
async function loadingModels(browser) {
  const results = []
  const fixture = { items: Array.from({ length: 20 }, (_, index) => ({ runId: `slow-${index}`, fileId: `slow-file-${index}`, name: `Work.${index}.mkv`, title: `作品 ${index}`, kind: 'tv', mediaType: 'video', season: 1, episode: index + 1, source: `/inbox/${index}`, target: `/library/${index}`, status: 'failed', at: '2026-11-03T12:00:00Z', canDelete: true, canRetry: true, attachmentCount: 0 })), page: 1, pages: 75, total: 1500, counts: { all: 1500, running: 0, done: 1000, failed: 500 }, active: false, activity: [], cleanup: [] }
  for (const kind of ['slow', 'timeout', 'unmount', 'hidden', 'poll']) {
    const context = await browser.newContext({ serviceWorkers: 'block' }), held = gate()
    let requests = 0
    await context.addInitScript(({ kind }) => {
      localStorage.setItem('admin_token', 'mock-token')
      window.__historyAborts = 0
      const originalFetch = window.fetch
      window.fetch = (input, init) => {
        let settled = false
        if (String(input).includes('/media-organize/history')) init?.signal?.addEventListener('abort', () => { if (!settled) window.__historyAborts++ })
        return originalFetch(input, init).finally(() => { settled = true })
      }
      if (kind === 'timeout') { const timeout = AbortSignal.timeout.bind(AbortSignal); AbortSignal.timeout = ms => timeout(ms === 75000 ? 200 : ms) }
      if (kind === 'poll') { const timeout = window.setTimeout.bind(window); window.setTimeout = (fn, ms, ...args) => timeout(fn, ms === 5000 ? 150 : ms, ...args) }
    }, { kind })
    await context.route('**/api/**', async route => {
      const endpoint = new URL(route.request().url()).pathname
      let data = {}
      if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
      else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
      else if (endpoint === '/api/admin/media-organize') data = { rules: [], active: false, activeRules: [] }
      else if (endpoint === '/api/admin/media-organize/history') { requests++; data = { ...fixture, active: kind === 'poll' }; if (kind !== 'poll' || requests > 1) await held.wait() }
      else throw new Error(`Unexpected loading model API ${endpoint}`)
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
    })
    const page = await context.newPage(); page.setDefaultTimeout(10000)
    try {
      await page.goto(origin + '/media/organize/history', { waitUntil: 'domcontentloaded' })
      if (kind === 'slow') {
        await held.ready; await page.getByText('正在读取整理记录…', { exact: true }).waitFor()
        await page.waitForTimeout(5300); assert.equal(requests, 1, '初次慢读取不能被轮询中断')
        held.release(); await page.locator('.mo-result').nth(19).waitFor()
        assert.equal(await page.getByText('正在读取整理记录…', { exact: true }).count(), 0)
      } else if (kind === 'timeout') {
        await held.ready; await page.getByRole('alert').filter({ hasText: /timed out|TimeoutError/ }).waitFor()
        assert.equal(await page.getByText('正在读取整理记录…', { exact: true }).count(), 0)
        assert.equal(requests, 1); assert.equal(await page.evaluate(() => window.__historyAborts), 1)
      } else if (kind === 'unmount') {
        await held.ready
        await page.getByRole('navigation', { name: '整理功能', exact: true }).getByRole('link', { name: '整理规则', exact: true }).click()
        await page.waitForFunction(() => window.__historyAborts === 1)
        assert.equal(requests, 1)
      } else if (kind === 'hidden') {
        await held.ready
        await page.evaluate(() => { window.__historyHidden = true; Object.defineProperty(document, 'hidden', { configurable: true, get: () => window.__historyHidden }); document.dispatchEvent(new Event('visibilitychange')) })
        await page.waitForFunction(() => window.__historyAborts === 1)
        assert.equal(await page.getByText('正在读取整理记录…', { exact: true }).count(), 1, '取消初次读取不能假定已经加载')
        held.release()
        await page.evaluate(() => { window.__historyHidden = false; document.dispatchEvent(new Event('visibilitychange')) })
        await page.locator('.mo-result').nth(19).waitFor(); assert.equal(requests, 2)
        assert.equal(await page.getByRole('alert').count(), 0)
      } else {
        await page.locator('.mo-result').first().waitFor()
        await page.getByRole('button', { name: '刷新记录', exact: true }).click(); await held.ready
        await page.waitForTimeout(300)
        assert.equal(requests, 2, '旧轮询定时器不得打断新慢读取')
        assert.equal(await page.evaluate(() => window.__historyAborts), 0)
      }
      results.push(kind)
    } finally { held.release(); await context.close() }
  }
  return results
}
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
  const rule = { id: 'rule1', name: '目录监控', storage: 'local', source: '/media/inbox', target: '/media/library', realtimeEnabled: true, scheduleEnabled: true, classify: true, intervalSeconds: 300, attachmentCleanupEnabled: true, attachmentGraceSeconds: 60 }
  const row = (index, status = 'done', extra = {}) => ({ runId: `run-${index}`, fileId: `video-${index}`, name: `Work.S01E${String(index).padStart(2, '0')}.mkv`, title: `作品 ${index}`, kind: 'tv', mediaType: 'video', season: 1, episode: index,
    source: `/media/inbox/Work.S01E${String(index).padStart(2, '0')}.mkv`, target: `/media/library/作品/Season 01/Work.S01E${String(index).padStart(2, '0')}.mkv`, status, canDelete: true, canRetry: status === 'failed', attachmentCount: 1, at: new Date(Date.UTC(2026, 9, 29, 10, 0, index)).toISOString(), ...extra })
  let records = Array.from({ length: 46 }, (_, index) => row(index + 1, index < 34 ? 'done' : 'failed'))
  records[39] = row(40, 'done', { title: 'needle_% 附件失败', canRetry: true, attachmentFailed: true })
  records[43] = row(44, 'done', { title: 'needle_% 已成功' })
  records.push(row(47, 'running', { title: 'needle_% 正在整理', canDelete: false }), row(48, 'done', { runId: 'active-run' }), row(49, 'running', { runId: 'active-run', canDelete: false }))
  const calls = [], errors = [], passed = [], taskResponses = new Map(), missingTasks = new Set()
  let heldHistory, heldTask, detailFailure, queueRetry = false, loseDelete = false, compactDetails = false, attachmentStateCases = false, completedMain = false, retrySucceeds = false, activity = [{ runId: 'scanning-run', ruleName: '目录监控', status: 'scanning', message: '正在扫描来源目录', at: records[0].at }]
  const category = item => item.status
  await context.route('**/api/**', async route => {
    const request = route.request(), url = new URL(request.url()), endpoint = url.pathname, method = request.method(), body = request.postDataJSON()
    calls.push({ endpoint, method, body, query: url.searchParams.toString() })
    let data = {}, status = 200
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
    else if (endpoint === '/api/admin/media-organize') data = { rules: [rule], active: true, activeRules: [] }
    else if (endpoint === '/api/admin/media-organize/history' && method === 'GET') {
      const q = (url.searchParams.get('q') || '').toLowerCase(), filter = url.searchParams.get('status') || 'all'
      const matches = records.filter(item => [item.title, item.name, item.source, item.target].some(value => value?.toLowerCase().includes(q)))
      const counts = { all: matches.length, running: 0, done: 0, failed: 0 }; matches.forEach(item => counts[category(item)]++)
      const filtered = matches.filter(item => filter === 'all' || category(item) === filter), pages = Math.max(1, Math.ceil(filtered.length / 20)), page = Math.min(pages, Math.max(1, Number(url.searchParams.get('page') || 1)))
      data = { items: clone(filtered.slice((page - 1) * 20, page * 20)), page, pages, total: filtered.length, counts, active: records.some(item => category(item) === 'running'), activity, cleanup: [] }
      if (heldHistory) { const held = heldHistory; heldHistory = undefined; await held.wait() }
    } else if (endpoint === '/api/admin/media-organize/history' && method === 'DELETE') {
      const selected = body.status ? records.filter(item => item.status === body.status && item.canDelete) : records.filter(item => body.items.some(key => key.runId === item.runId && key.fileId === item.fileId))
      if (selected.some(item => !item.canDelete)) { status = 409; data = { error: '整理任务正在处理，请稍后重试' } }
      else {
        records = records.filter(item => !selected.includes(item)); data = { deleted: selected.length }
        if (loseDelete) { loseDelete = false; records.push(row(100, 'done', { title: '刚成功的任务' })); return route.abort() }
      }
    } else if (endpoint === '/api/admin/tmdb/browse') {
      data = { items: [{ id: 4242, kind: 'tv', title: 'Corrected Work', date: '2024-01-01' }] }
    } else if (endpoint.endsWith('/retry') && method === 'POST') {
      const item = records.find(item => item.fileId === body.fileId), task = clone(taskResponses.get(body.fileId))
      assert.ok(item, 'retry must address the selected fixture')
      if (body.tmdbId) Object.assign(task.group, { kind: body.kind, tmdbId: body.tmdbId, title: 'Corrected Work', year: '2024' })
      if (body.season !== undefined) task.entries[0].file.season = body.season
      if (body.episode !== undefined) task.entries[0].file.episode = body.episode
      Object.assign(item, { title: task.group.title, kind: task.group.kind, season: task.entries[0].file.season, episode: task.entries[0].file.episode, ...(item.status === 'done' ? { canRetry: false, canDelete: false } : { status: retrySucceeds ? 'done' : 'failed', canRetry: !retrySucceeds, canDelete: true, at: new Date().toISOString() }) })
      if (queueRetry) Object.assign(item, { status: 'running', canRetry: false, canDelete: false })
      if (retrySucceeds) task.entries.forEach(entry => { entry.status = 'done'; entry.message = '' })
      data = { ...task, ...clone(item) }
      taskResponses.set(item.fileId, clone(data))
    } else if (endpoint.endsWith('/task')) {
      if (missingTasks.has(url.searchParams.get('fileId'))) return route.fulfill({ status: 404, contentType: 'application/json', body: JSON.stringify({ error: '整理记录不存在' }) })
      if (detailFailure) {
        const failure = detailFailure; detailFailure = undefined
        if (failure === 'network') return route.abort()
        return route.fulfill({ status: failure, contentType: 'application/json', body: JSON.stringify({ error: '模拟详情服务错误' }) })
      }
      const item = records.find(item => item.fileId === url.searchParams.get('fileId'))
      assert.ok(item, 'detail must address an existing record')
      data = { ...clone(item), rule, group: { id: '1', kind: 'tv', tmdbId: 42, title: item.title, identity: { name: 'Work' } }, entries: [{ file: { id: item.fileId, name: item.name, sourcePath: item.name, mediaType: 'video', season: 1, episode: item.episode }, status: item.status === 'done' ? 'done' : 'error', target: item.target }] }
      if (item.prewrite && item.status === 'running') Object.assign(data.entries[0], { status: 'ready', p115Move: { phase: 'prepared' }, message: '模拟旧失败消息：文件移动结果尚未确认，请重试' })
      if (item.attachmentFailed) data.entries.push({ file: { id: `subtitle-${item.fileId}`, name: 'Work.ass', sourcePath: 'Work.ass', mediaType: 'subtitle' }, videoId: item.fileId, status: item.attachmentFinished ? 'done' : 'error', message: item.attachmentFinished ? '' : '字幕整理失败' })
      if (compactDetails) {
        data.group.year = '2026'
        data.group.tmdbId = 318902
        data.entries[0].file.season = 0
        data.entries[0].file.episode = 1
        data.entries[0].file.sourcePath = 'long-source-directory/'.repeat(12) + item.name
        data.entries[0].file.namingValues = { resolution: '2160p', source: 'WEB-DL', platform: '', videoCodec: 'HEVC', audioCodec: 'DDP', channels: '2.0', edition: ' ' }
        data.entries[0].p115Move = { phase: 'moving' }
        data.entries[0].message = 'Position requires confirmation'
        if (completedMain) { data.entries[0].status = 'done'; delete data.entries[0].p115Move; data.entries[0].message = '' }
        data.entries.push({ file: { id: 'fixture-subtitle', name: item.name.replace(/\.strm$/, '.ass'), sourcePath: 'fixture.ass', mediaType: 'subtitle', season: 0, episode: 1 }, group: '1', videoId: item.fileId, target: '/library/fixture.ass', status: 'error', resultCode: 'WAITING_MAIN_FILE', message: 'Waiting for main video' })
        if (attachmentStateCases) {
          data.entries.push({ file: { id: 'fixture-conflict', name: 'Fixture.conflict.ass', sourcePath: 'Fixture.conflict.ass', mediaType: 'subtitle' }, videoId: item.fileId, target: '/library/Fixture.conflict.ass', status: 'conflict', message: 'Target already exists' })
          data.entries.push({ file: { id: 'fixture-retained', name: 'Fixture.retained.nfo', sourcePath: 'Fixture.retained.nfo', mediaType: 'attachment' }, videoId: item.fileId, status: 'retained' })
          data.entries.push({ file: { id: 'fixture-recycling', name: 'Fixture.recycling.nfo', sourcePath: 'Fixture.recycling.nfo', mediaType: 'attachment' }, videoId: item.fileId, target: '/library/Fixture.recycling.nfo', status: 'error', attachmentKeep: { phase: 'recycling' }, message: 'Recycle receipt requires confirmation' })
        }
      }
      taskResponses.set(item.fileId, clone(data))
      if (heldTask) { const held = heldTask; heldTask = undefined; await held.wait() }
    } else throw new Error(`Unexpected API ${method} ${endpoint}`)
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
  })
  const page = await context.newPage(); page.setDefaultTimeout(10000); page.on('pageerror', error => errors.push(error.message))
  const nav = page.getByRole('navigation', { name: '整理功能', exact: true })
  const section = page.getByRole('region', { name: '整理记录', exact: true })
  const detail = page.getByRole('dialog', { name: '整理详情', exact: true })
  const search = page.getByRole('searchbox', { name: '搜索整理记录', exact: true })
  const statusButton = label => page.getByRole('group', { name: '记录状态', exact: true }).getByRole('button', { name: label, exact: true })
  const readCount = () => calls.filter(call => call.endpoint.endsWith('/history') && call.method === 'GET').length
  const deleteCalls = () => calls.filter(call => call.endpoint.endsWith('/history') && call.method === 'DELETE')
  try {
    await fs.mkdir('test-results', { recursive: true })
    await page.goto(origin + '/media/organize/history?page=2'); await section.locator('.mo-result').first().waitFor()
    assert.equal(await section.locator('.mo-result').count(), 20)
    assert.equal(calls.some(call => call.endpoint === '/api/admin/media-organize'), false)
    assert.equal(calls.some(call => call.endpoint.endsWith('/task')), false)
    assert.equal(new URL(page.url()).searchParams.get('page'), '2')
    const initialReads = readCount()
    await page.waitForTimeout(5300)
    assert.equal(readCount(), initialReads, 'browsing historical pages must not poll and reorder the page')
    const slowDetail = gate(); heldTask = slowDetail
    await section.locator('.mo-result').first().click(); await slowDetail.ready
    assert.equal(await statusButton('失败').isEnabled(), true)
    assert.equal(await search.isEnabled(), true)
    await statusButton('失败').click()
    await page.waitForFunction(count => document.querySelectorAll('.mo-result').length === count, records.filter(item => item.status === 'failed').length)
    await section.locator('.mo-result').first().click(); await detail.waitFor()
    const freshTitle = await detail.locator('h3').first().innerText()
    slowDetail.release(); await page.waitForTimeout(100)
    assert.equal(await detail.locator('h3').first().innerText(), freshTitle, '过期详情回执不能覆盖新选择')
    await detail.getByRole('button', { name: '关闭', exact: true }).click()
    await statusButton('全部').click(); await section.locator('.mo-result').first().waitFor()
    passed.push('详情读取独立于页面操作；慢请求不锁筛选或搜索，切换后过期详情不会覆盖新记录')
    const readsBeforeSearch = readCount()
    await search.fill('needle_%'); assert.equal(readCount(), readsBeforeSearch)
    const stale = gate(); heldHistory = stale
    await search.fill('过期搜索'); await search.press('Enter'); await stale.ready
    await page.evaluate(() => { void document.querySelector('#app').__vue_app__.config.globalProperties.$router.push({ path: '/media/organize/history', query: { q: 'needle_%' } }) })
    await page.waitForFunction(() => document.querySelectorAll('.mo-result').length === 3)
    stale.release()
    await page.waitForTimeout(100)
    assert.equal(await section.locator('.mo-result').count(), 3)
    await search.fill('needle_%')
    await search.press('Enter'); await page.waitForFunction(() => document.querySelectorAll('.mo-result').length === 3)
    assert.equal(new URL(page.url()).searchParams.get('q'), 'needle_%')
    assert.ok(!new URL(page.url()).searchParams.has('page') || new URL(page.url()).searchParams.get('page') === '1')
    assert.equal(new URLSearchParams(calls.filter(call => call.endpoint.endsWith('/history')).at(-1).query).get('q'), 'needle_%')
    assert.match(await statusButton('全部').innerText(), /3/)
    await statusButton('成功').click(); await page.waitForFunction(() => document.querySelectorAll('.mo-result').length === 2)
    assert.equal(await section.getByText('成功', { exact: true }).count(), 2)
    assert.equal(new URL(page.url()).searchParams.get('status'), 'done')
    await nav.getByRole('link', { name: '整理规则', exact: true }).click(); await page.locator('.mo-rule-card').waitFor()
    await nav.getByRole('link', { name: '整理记录', exact: true }).click(); await page.waitForFunction(() => document.querySelectorAll('.mo-result').length === 2)
    assert.equal(await search.inputValue(), 'needle_%'); assert.equal(new URL(page.url()).searchParams.get('status'), 'done')
    await page.reload(); await page.waitForFunction(() => document.querySelectorAll('.mo-result').length === 2)
    passed.push('四种历史筛选；搜索由服务端跨全部记录筛选，回车重置分页；附件失败的主文件仍归成功；筛选地址支持切回和刷新恢复')

    await search.fill('不存在的资源'); await search.press('Enter')
    await page.getByText('未找到符合条件的整理记录', { exact: true }).waitFor()
    await page.getByRole('button', { name: '清空筛选', exact: true }).click()
    await page.waitForFunction(() => document.querySelectorAll('.mo-result').length === 20)
    await page.getByRole('button', { name: '批量管理', exact: true }).click()
    await page.getByRole('checkbox', { name: '全选当前页', exact: true }).check()
    await page.getByRole('navigation', { name: '整理记录分页', exact: true }).getByRole('button', { name: '下一页', exact: true }).click()
    assert.equal(await page.getByRole('button', { name: /删除选中/ }).count(), 0)
    await page.getByRole('button', { name: '批量管理', exact: true }).click()
    await page.getByRole('checkbox', { name: '全选当前页', exact: true }).check()
    await page.getByRole('button', { name: /删除选中/ }).click()
    const deletion = page.getByRole('dialog', { name: /删除.*记录/ })
    await deletion.getByRole('button', { name: /确认删除|删除记录/ }).click()
    await deletion.waitFor({ state: 'detached' })
    assert.equal(deleteCalls().at(-1).body.items.length, 20)
    assert.ok(deleteCalls().at(-1).body.items.every(item => Number(item.fileId.slice(6)) > 20))
    assert.equal(calls.some(call => /\/run$|\/retry$/.test(call.endpoint)), false)
    passed.push('批量只选择当前页，翻页清空选中；删除正文只含可见选中任务，不扫描、不重试、不接触实际文件')

    await page.goto(origin + '/media/organize/history?status=failed'); await section.locator('.mo-result').first().waitFor()
    await section.locator('.mo-result').first().click()
    assert.equal(await detail.locator('.mo-recognition').count(), 0)
    assert.equal(await detail.locator('a[href^="/logs"]').count(), 0)
    await detail.getByRole('spinbutton', { name: '集', exact: true }).fill('55')
    await detail.getByRole('button', { name: '关闭', exact: true }).click()
    const leave = page.getByRole('dialog', { name: '放弃未保存的整理修改？', exact: true })
    await leave.getByRole('button', { name: '继续编辑', exact: true }).click()
    assert.equal(await detail.getByRole('spinbutton', { name: '集', exact: true }).inputValue(), '55')
    await detail.getByRole('button', { name: '关闭整理详情', exact: true }).click()
    await leave.getByRole('button', { name: '放弃修改并离开', exact: true }).click()
    await detail.waitFor({ state: 'detached' })
    await section.locator('.mo-result').first().click()
    assert.notEqual(await detail.getByRole('spinbutton', { name: '集', exact: true }).inputValue(), '55')
    await detail.getByRole('button', { name: '删除此记录', exact: true }).click()
    await deletion.getByRole('button', { name: '确认删除', exact: true }).click()
    await deletion.waitFor({ state: 'detached' }); await detail.waitFor({ state: 'detached' })
    assert.equal(deleteCalls().at(-1).body.items.length, 1)
    passed.push('详情关闭保护未保存修改；继续编辑保留、放弃后丢弃；单条删除复用同一接口并关闭当前详情')

    await page.goto(origin + '/media/organize/history?status=done&page=2'); await section.locator('.mo-result').first().waitFor()
    await page.getByRole('button', { name: '批量管理', exact: true }).click()
    assert.equal(await section.getByRole('button', { name: '选择Work.S01E48.mkv的整理记录', exact: true }).isEnabled(), true)
    await page.getByRole('checkbox', { name: '全选当前页', exact: true }).check()
    assert.equal(await section.locator('.mo-result[aria-pressed=true]').count(), 2)
    await page.getByRole('button', { name: '退出管理', exact: true }).click()
    await section.getByRole('button', { name: '查看Work.S01E44.mkv的整理记录', exact: true }).click()
    const settledReads = readCount()
    await page.waitForTimeout(5300)
    assert.equal(readCount(), settledReads, 'a successful detail must not activate polling on historical pages')
    await detail.getByRole('button', { name: '关闭', exact: true }).click()

    await page.goto(origin + '/media/organize/history?status=running'); await section.locator('.mo-result').first().waitFor()
    await page.getByRole('button', { name: '批量管理', exact: true }).click()
    assert.equal(await section.locator('.mo-result').count(), 2)
    assert.equal(await section.locator('.mo-result').evaluateAll(items => items.every(item => item.disabled)), true)
    await page.getByRole('button', { name: /退出管理|取消管理/ }).click()
    await page.getByText('正在扫描来源目录', { exact: false }).waitFor()
    passed.push('同批其他文件运行不锁定已完成记录；实际在途文件不可删除；历史页成功详情不轮询；扫描状态独立显示')

    await page.goto(origin + '/media/organize/history?q=' + encodeURIComponent('needle_%')); await section.locator('.mo-result').first().waitFor()
    loseDelete = true
    const beforeDelete = deleteCalls().length
    await page.getByRole('button', { name: /清理.*成功/ }).click()
    const clear = page.getByRole('dialog', { name: /清理.*成功/ })
    await clear.getByRole('button', { name: '确认删除', exact: true }).click()
    await clear.waitFor({ state: 'detached' })
    await page.getByRole('alert').filter({ hasText: '删除结果尚未确认' }).waitFor()
    assert.equal(deleteCalls().length, beforeDelete + 1)
    assert.deepEqual(deleteCalls().at(-1).body, { status: 'done' })
    assert.ok(records.some(item => item.fileId === 'video-100'), 'new success after a lost receipt must not be deleted by automatic replay')
    assert.equal(records.some(item => item.fileId === 'video-48'), false, '同批其他文件运行不应阻止清理已完成记录')
    passed.push('清理成功覆盖筛选以外和同批已完成记录；丢失删除回执后不自动重放，保留此后新增成功')

    await page.goto(origin + '/media/organize/history'); await section.locator('.mo-result').first().waitFor()
    for (const width of [1440, 320, 390]) {
      await page.setViewportSize({ width, height: 900 })
      await page.waitForTimeout(350)
      for (const dark of [false, true]) {
        await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
        await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true)
        await page.screenshot({ path: `test-results/media-organize-history-${width}-${dark ? 'dark' : 'light'}.png`, fullPage: true, animations: 'disabled' })
      }
    }
    records = Array.from({ length: 21 }, (_, index) => row(index + 1)); activity = []
    await page.goto(origin + '/media/organize/history?page=2')
    await page.waitForFunction(() => document.querySelectorAll('.mo-result').length === 1)
    const readsBeforeLastDeletion = readCount()
    await page.getByRole('button', { name: '批量管理', exact: true }).click()
    await page.getByRole('checkbox', { name: '全选当前页', exact: true }).check()
    await page.getByRole('button', { name: /删除选中/ }).click()
    await deletion.getByRole('button', { name: '确认删除', exact: true }).click()
    await deletion.waitFor({ state: 'detached' })
    await page.waitForURL(url => !url.searchParams.has('page'))
    await page.waitForFunction(() => document.querySelectorAll('.mo-result').length === 20)
    await page.getByRole('status').filter({ hasText: '已删除 1 条整理记录，实际文件保持不变' }).waitFor()
    assert.equal(readCount(), readsBeforeLastDeletion + 1, 'normalizing a deleted last page must reuse the refreshed result')
    passed.push('删除末页最后记录后回落首页，保留删除结果提示且只刷新一次列表')

    compactDetails = true
    records = [row(61, 'failed', { name: 'Northern Story (2026) - S00E01 - 2160p.WEB-DL.H265.DDP 2.0.strm', title: 'Northern Story', season: 0, episode: 1 })]
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.goto(origin + '/media/organize/history'); await section.locator('.mo-result').first().click()
    await detail.locator('.mo-specifications').waitFor()
    assert.equal(await detail.locator('.mo-recognition').count(), 0)
    assert.equal(await detail.locator('details').count(), 0)
    assert.equal(await detail.locator('a[href^="/logs"]').count(), 0)
    assert.match(await detail.innerText(), /2026/)
    assert.match(await detail.innerText(), /318902/)
    assert.match(await detail.innerText(), /S00E01/)
    assert.match(await detail.locator('.mo-specifications').innerText(), /WEB-DL/)
    assert.match(await detail.locator('.mo-specifications').innerText(), /HEVC/)
    assert.match(await detail.locator('.mo-specifications').innerText(), /DDP/)
    assert.equal(await detail.locator('.mo-specifications>div').count(), 5)
    assert.equal(await detail.locator('.mo-attachment-list>li').count(), 1)
    await detail.locator('.mo-attachment-list').getByText('等待主文件', { exact: true }).waitFor()
    assert.equal(await detail.locator('.mo-attachment-list .mo-red').count(), 0)
    assert.equal(await detail.locator('.mo-file .mo-red').count(), 1)
    await detail.getByText('文件移动结果尚未确认，重试时将先核对文件位置。', { exact: true }).waitFor()
    assert.equal(calls.some(call => call.endpoint.includes('/tmdb/')), false)
    for (const width of [1440, 320, 390]) {
      await page.setViewportSize({ width, height: 900 })
      await page.waitForTimeout(350)
      for (const dark of [false, true]) {
        await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true)
        assert.equal(await detail.locator('.anime-floating-body').evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
        await page.screenshot({ path: `test-results/media-result-${width}-${dark ? 'dark' : 'light'}.png`, animations: 'disabled' })
      }
    }
    passed.push('compact result details, nonempty specifications, pending move and attachment states, no diagnostics or repeated log links, responsive light/dark layout')

    attachmentStateCases = true
    await detail.getByRole('button', { name: '关闭', exact: true }).click()
    await section.locator('.mo-result').first().click()
    await detail.locator('.mo-attachment-list>li').nth(3).waitFor()
    assert.equal(await detail.locator('.mo-attachment-list>li').count(), 4)
    await detail.locator('.mo-attachment-list>li').filter({ hasText: 'Fixture.conflict.ass' }).getByText('冲突', { exact: true }).waitFor()
    assert.equal(await detail.locator('.mo-attachment-list>li').filter({ hasText: 'Fixture.conflict.ass' }).locator('.mo-status.mo-red').count(), 1)
    await detail.locator('.mo-attachment-list>li').filter({ hasText: 'Fixture.retained.nfo' }).getByText('已保留', { exact: true }).waitFor()
    assert.equal(await detail.locator('.mo-attachment-list>li').filter({ hasText: 'Fixture.retained.nfo' }).locator('.mo-red').count(), 0)
    const recycling = detail.locator('.mo-attachment-list>li').filter({ hasText: 'Fixture.recycling.nfo' })
    await recycling.getByText('失败', { exact: true }).waitFor()
    assert.equal(await recycling.locator('.mo-status.mo-red').count(), 1)
    assert.equal(await recycling.getByText('附件回收结果尚未确认，重试时将先核对文件位置。', { exact: true }).count(), 0)
    assert.equal(await recycling.getByText('Recycle receipt requires confirmation', { exact: true }).getAttribute('class'), 'mo-red')
    completedMain = true
    Object.assign(records[0], { status: 'done', canRetry: true })
    await detail.getByRole('button', { name: '关闭', exact: true }).click()
    await section.locator('.mo-result').first().click()
    await detail.locator('.mo-attachment-list>li').first().getByText('失败', { exact: true }).waitFor()
    assert.equal(await detail.locator('.mo-attachment-list>li').first().locator('.mo-status.mo-red').count(), 1)
    assert.equal(await detail.locator('.mo-attachment-list').getByText('等待主文件', { exact: true }).count(), 0)
    await recycling.getByText('失败', { exact: true }).waitFor()
    assert.equal(await recycling.locator('.mo-status.mo-red').count(), 1)
    assert.equal(await detail.getByRole('button', { name: '删除此记录', exact: true }).isEnabled(), true)
    assert.equal(await detail.getByText('正在处理附件，主文件已完成。', { exact: true }).count(), 0)
    await detail.getByRole('button', { name: '删除此记录', exact: true }).click()
    await deletion.getByText(/同时结束对应的重试与恢复，不删除实际文件/).waitFor()
    assert.equal(await deletion.getByText(/待核实|需要重新整理时/).count(), 0)
    await deletion.getByRole('button', { name: '确认删除', exact: true }).click()
    await deletion.waitFor({ state: 'detached' }); await detail.waitFor({ state: 'detached' })
    assert.equal(records.length, 0)
    passed.push('主文件未知移动保留重试核对说明；成功视频带旧附件回收标记仍可删除，删除结束对应恢复且不操作实际文件')

    compactDetails = false
    records = [row(67, 'running', { canRetry: false, canDelete: false, prewrite: true })]
    await page.goto(origin + '/media/organize/history')
    const prewriteCard = section.getByRole('button', { name: '查看Work.S01E67.mkv的整理记录', exact: true })
    await prewriteCard.getByText('整理中', { exact: true }).waitFor(); await prewriteCard.click()
    await detail.locator('.mo-file .mo-status').getByText('整理中', { exact: true }).waitFor()
    assert.equal(await detail.locator('.mo-file .mo-red').count(), 0)
    assert.equal(await detail.getByText('失败', { exact: true }).count(), 0)
    assert.equal(await detail.getByText(/移动结果尚未确认|模拟旧失败消息/).count(), 0)
    assert.equal(await detail.getByRole('button', { name: '删除此记录', exact: true }).isDisabled(), true)
    assert.equal(await detail.getByRole('button', { name: '重试此任务', exact: true }).count(), 0)
    Object.assign(records[0], { status: 'done', canDelete: true, at: new Date().toISOString() })
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await prewriteCard.getByText('成功', { exact: true }).waitFor()
    await detail.locator('.mo-file .mo-status').getByText('已完成', { exact: true }).waitFor()
    assert.equal(await detail.locator('.mo-file .mo-red').count(), 0)
    assert.equal(await detail.getByText(/移动结果尚未确认|模拟旧失败消息/).count(), 0)
    assert.equal(await detail.getByRole('button', { name: '删除此记录', exact: true }).isEnabled(), true)
    await detail.getByRole('button', { name: '关闭', exact: true }).click()
    passed.push('主视频预写意图在顶层运行期间显示整理中、不标红、不提示失败重试；完成后详情与列表一致，真实失败意图仍保留恢复说明')

    records = [row(62, 'failed'), row(64, 'failed')]
    await page.goto(origin + '/media/organize/history?status=failed'); await section.locator('.mo-result').first().click()
    await detail.getByRole('button', { name: '更换作品', exact: true }).click()
    await detail.getByRole('textbox', { name: '搜索 TMDB 作品', exact: true }).fill('Corrected Work')
    await detail.getByRole('button', { name: '搜索作品', exact: true }).click()
    await detail.locator('.mo-candidates').getByRole('button').click()
    assert.match(await detail.innerText(), /2024.*TMDB 4242/)
    await detail.getByRole('spinbutton', { name: '季', exact: true }).fill('0')
    await detail.getByRole('spinbutton', { name: '集', exact: true }).fill('0')
    const retries = () => calls.filter(call => call.endpoint.endsWith('/retry'))
    await detail.getByRole('button', { name: '保存并重试此任务', exact: true }).click()
    await detail.getByRole('alert').filter({ hasText: '请填写有效的目标季集' }).waitFor()
    assert.equal(retries().length, 0)
    await detail.getByRole('spinbutton', { name: '集', exact: true }).fill('7')
    await detail.getByRole('button', { name: '保存并重试此任务', exact: true }).click()
    await detail.getByRole('button', { name: '重试此任务', exact: true }).waitFor()
    assert.deepEqual(retries().at(-1).body, { fileId: 'video-62', season: 0, episode: 7, kind: 'tv', tmdbId: 4242 })
    assert.equal(new URLSearchParams(calls.find(call => call.endpoint === '/api/admin/tmdb/browse').query).get('query'), 'Corrected Work')
    await detail.getByRole('button', { name: '重试此任务', exact: true }).click()
    await page.waitForFunction(() => !document.querySelector('.mo-footer .mo-primary')?.disabled)
    assert.deepEqual(retries().at(-1).body, { fileId: 'video-62' })
    retrySucceeds = true
    await detail.getByRole('button', { name: '重试此任务', exact: true }).click()
    await detail.getByText('成功', { exact: true }).waitFor()
    await page.waitForFunction(() => document.querySelectorAll('.mo-result').length === 1)
    assert.equal(await section.getByRole('button', { name: '查看Work.S01E62.mkv的整理记录', exact: true }).count(), 0)
    await section.getByRole('button', { name: '查看Work.S01E64.mkv的整理记录', exact: true }).waitFor()
    assert.deepEqual(records.map(item => [item.fileId, item.status]), [['video-62', 'done'], ['video-64', 'failed']])
    retrySucceeds = false
    await detail.getByRole('button', { name: '关闭', exact: true }).click()
    await detail.waitFor({ state: 'detached' })
    const completedTime = records[0].at
    await statusButton('成功').click()
    await section.getByRole('button', { name: '查看Work.S01E62.mkv的整理记录', exact: true }).waitFor()
    await page.reload()
    await section.getByRole('button', { name: '查看Work.S01E62.mkv的整理记录', exact: true }).waitFor()
    assert.equal(records[0].at, completedTime)
    records[0].at = '0001-01-01T00:00:00Z'
    await page.reload()
    const recoveredCard = section.getByRole('button', { name: '查看Work.S01E62.mkv的整理记录', exact: true })
    await recoveredCard.getByText('完成时间未知', { exact: true }).waitFor()
    assert.equal(await recoveredCard.locator('time').getAttribute('datetime'), null)
    await recoveredCard.click()
    await detail.getByText('目录监控 · 完成时间未知', { exact: true }).waitFor()
    await detail.getByRole('button', { name: '关闭', exact: true }).click()
    passed.push('仅提交当前任务修正；成功后从失败转入成功，刷新后仍可查询且完成时间不变；旧成功未知时间在列表和详情明确显示')

    records = [row(63, 'done', { canRetry: true, attachmentFailed: true })]
    const primaryTime = records[0].at
    await page.goto(origin + '/media/organize/history'); await section.locator('.mo-result').first().click()
    await detail.getByRole('button', { name: '重试附件', exact: true }).waitFor()
    assert.equal(await detail.getByRole('spinbutton').count(), 0)
    assert.equal(await detail.getByRole('button', { name: '删除此记录', exact: true }).isEnabled(), true)
    const attachmentRefresh = gate(); heldHistory = attachmentRefresh
    const retriesBeforeAttachment = retries().length
    await detail.getByRole('button', { name: '重试附件', exact: true }).click()
    await attachmentRefresh.ready
    await detail.getByText('正在处理附件，主文件已完成。', { exact: true }).waitFor()
    assert.equal(await detail.getByRole('button', { name: '删除此记录', exact: true }).isDisabled(), true)
    assert.deepEqual(retries().at(-1).body, { fileId: 'video-63' })
    assert.equal(records[0].status, 'done'); assert.equal(records[0].at, primaryTime)
    assert.equal(await detail.getByRole('button', { name: '重试附件', exact: true }).count(), 0)
    assert.equal(await detail.getByRole('button', { name: '关闭', exact: true }).isEnabled(), true)
    assert.equal(await statusButton('失败').isEnabled(), true)
    await detail.getByRole('button', { name: '关闭', exact: true }).click()
    await detail.waitFor({ state: 'detached' })
    await statusButton('失败').click()
    await page.getByText('未找到符合条件的整理记录', { exact: true }).waitFor()
    assert.equal(new URL(page.url()).searchParams.get('status'), 'failed')
    await statusButton('成功').click(); await section.locator('.mo-result').first().click()
    await detail.getByText('正在处理附件，主文件已完成。', { exact: true }).waitFor()
    assert.equal(await detail.getByRole('button', { name: '重试附件', exact: true }).count(), 0)
    attachmentRefresh.release(); await page.waitForTimeout(100)
    assert.equal(retries().length, retriesBeforeAttachment + 1)
    passed.push('重试提交成功即解除提交锁；列表刷新延迟时仍可关闭详情和切换筛选，排队状态不会重复提交')
    Object.assign(records[0], { canDelete: true, attachmentFinished: true })
    await detail.locator('.mo-attachment-list').getByText('已完成', { exact: true }).waitFor()
    assert.equal(await detail.getByRole('button', { name: '重试附件', exact: true }).count(), 0)
    assert.equal(await detail.getByRole('button', { name: '删除此记录', exact: true }).isEnabled(), true)
    assert.equal(records[0].at, primaryTime)
    await detail.getByRole('button', { name: '关闭', exact: true }).click()
    passed.push('成功主文件保持只读；失败附件不锁历史；仅实际附件操作短暂禁删，完成后恢复删除且不修改主文件状态和完成时间')

    records = [row(65, 'failed'), row(66, 'failed')]
    await page.goto(origin + '/media/organize/history?status=failed'); await section.locator('.mo-result').first().click()
    queueRetry = true
    await detail.getByRole('button', { name: '重试此任务', exact: true }).click()
    await detail.locator('.mo-title .mo-chip').filter({ hasText: /^整理中$/ }).waitFor()
    queueRetry = false
    await section.getByRole('button', { name: '查看Work.S01E66.mkv的整理记录', exact: true }).waitFor()
    assert.equal(await section.locator('.mo-result').count(), 1)
    assert.equal(await detail.count(), 1, '任务离开当前失败筛选不代表记录不存在')
    detailFailure = 500
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await detail.getByRole('alert').filter({ hasText: '模拟详情服务错误' }).waitFor()
    assert.equal(await detail.count(), 1, '服务错误应保留当前详情')
    detailFailure = 'network'
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await detail.getByRole('alert').filter({ hasText: /Failed to fetch|NetworkError/ }).waitFor()
    assert.equal(await detail.getByRole('button', { name: '关闭', exact: true }).isEnabled(), true)
    records = records.filter(item => item.fileId !== 'video-65'); missingTasks.add('video-65')
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await detail.waitFor({ state: 'detached' })
    await page.getByRole('status').filter({ hasText: '当前任务已结束或记录已移除' }).waitFor()
    assert.equal(await page.getByRole('alert').count(), 0)
    assert.equal(await page.getByText(/整理参数无效|重试成功/).count(), 0)
    assert.equal(await section.locator('.mo-result').count(), 1)
    const endedReads = calls.filter(call => call.endpoint.endsWith('/task') && new URLSearchParams(call.query).get('fileId') === 'video-65').length
    const endedHistoryReads = readCount()
    await page.waitForTimeout(5300)
    assert.equal(calls.filter(call => call.endpoint.endsWith('/task') && new URLSearchParams(call.query).get('fileId') === 'video-65').length, endedReads, '404结束旧详情后不得继续轮询')
    assert.equal(readCount(), endedHistoryReads, '只剩空闲失败时不应无限刷新列表')
    passed.push('排队重试离开筛选仍保留详情；500及网络错误保留真实错误；任务移除后的404关闭过时详情、保留其他失败且停止轮询，不宣称成功')

    records = [...Array.from({ length: 45 }, (_, index) => row(200 + index, 'failed')), row(300, 'done', { attachmentFailed: true }), row(301, 'done'), row(302, 'running', { canDelete: false }), row(303, 'failed', { canDelete: false })]
    Object.assign(records[44], { title: 'Hidden failure', name: 'Hidden.failure.mkv', source: '/hidden', target: '/hidden' })
    await page.goto(origin + '/media/organize/history?status=failed&page=2&q=Work'); await section.locator('.mo-result').first().waitFor()
    assert.equal(await section.locator('.mo-result').count(), 20)
    const beforeClearFailed = deleteCalls().length
    await page.getByRole('button', { name: '清理全部失败', exact: true }).click()
    const clearFailed = page.getByRole('dialog', { name: '清理全部失败记录？', exact: true })
    await clearFailed.getByText(/包括当前搜索和筛选以外的记录/).waitFor()
    await clearFailed.getByRole('button', { name: '取消', exact: true }).click()
    assert.equal(deleteCalls().length, beforeClearFailed)
    await page.getByRole('button', { name: '清理全部失败', exact: true }).click()
    await clearFailed.getByRole('button', { name: '确认删除', exact: true }).click()
    await clearFailed.waitFor({ state: 'detached' })
    await page.getByRole('status').filter({ hasText: '已删除 45 条整理记录' }).waitFor()
    assert.deepEqual(deleteCalls().at(-1).body, { status: 'failed' })
    assert.equal(records.filter(item => item.status === 'failed').length, 1)
    assert.equal(records.filter(item => item.status === 'done').length, 2)
    assert.equal(records.filter(item => item.status === 'running').length, 1)
    assert.ok(records.some(item => item.fileId === 'video-303'))
    assert.equal(new URL(page.url()).searchParams.get('page') || '1', '1')
    passed.push('清理全部失败覆盖分页和搜索以外的记录；取消不提交，确认只按失败状态清理，保留成功、整理中及正在操作的记录')

    records = []
    await page.goto(origin + '/media/organize/history')
    await page.reload(); await page.getByText('暂无整理记录，可在整理规则中立即运行或启用监控', { exact: true }).waitFor()
    assert.equal(await section.locator('.mo-result').count(), 0)
    const models = await loadingModels(browser)
    passed.push(`1500条计数的慢响应、超时、切页取消、隐藏恢复和轮询竞争模型：${models.join('、')}`)
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, reads: readCount(), deletions: deleteCalls().length, errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
