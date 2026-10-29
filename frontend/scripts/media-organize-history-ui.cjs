// Isolated record-management fixture: all APIs are mocked; no NAS/115 writes.
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const clone = value => JSON.parse(JSON.stringify(value))
const gate = () => { let enter, release; const ready = new Promise(resolve => { enter = resolve }), pending = new Promise(resolve => { release = resolve }); return { ready, release, wait: () => { enter(); return pending } } }
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
  const rule = { id: 'rule1', name: '目录监控', storage: 'local', source: '/media/inbox', target: '/media/library', enabled: true, classify: true, intervalMinutes: 5 }
  const row = (index, status = 'done', extra = {}) => ({ runId: `run-${index}`, fileId: `video-${index}`, name: `Work.S01E${String(index).padStart(2, '0')}.mkv`, title: `作品 ${index}`, kind: 'tv', mediaType: 'video', season: 1, episode: index,
    source: `/media/inbox/Work.S01E${String(index).padStart(2, '0')}.mkv`, target: `/media/library/作品/Season 01/Work.S01E${String(index).padStart(2, '0')}.mkv`, status, canDelete: true, attachmentCount: 1, at: new Date(Date.UTC(2026, 9, 29, 10, 0, index)).toISOString(), ...extra })
  let records = Array.from({ length: 46 }, (_, index) => row(index + 1, index < 34 ? 'done' : 'error'))
  records[39] = row(40, 'partial', { title: 'needle_% 附件失败', message: '视频已完成，字幕未完成' })
  records[43] = row(44, 'done', { title: 'needle_% 已成功' })
  records.push(row(47, 'running', { title: 'needle_% 正在整理', canDelete: false }), row(48, 'done', { runId: 'active-run', canDelete: false }), row(49, 'running', { runId: 'active-run', canDelete: false }))
  const calls = [], errors = [], passed = []
  let heldHistory, loseDelete = false, activity = [{ runId: 'scanning-run', ruleName: '目录监控', status: 'scanning', message: '正在扫描来源目录', at: records[0].at }]
  const category = item => ['queued', 'scanning', 'running'].includes(item.status) ? 'running' : item.status === 'done' ? 'done' : 'attention'
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
      const counts = { all: matches.length, running: 0, done: 0, attention: 0 }; matches.forEach(item => counts[category(item)]++)
      const filtered = matches.filter(item => filter === 'all' || category(item) === filter), pages = Math.max(1, Math.ceil(filtered.length / 20)), page = Math.min(pages, Math.max(1, Number(url.searchParams.get('page') || 1)))
      data = { items: clone(filtered.slice((page - 1) * 20, page * 20)), page, pages, total: filtered.length, counts, active: records.some(item => category(item) === 'running'), activity, cleanup: [] }
      if (heldHistory) { const held = heldHistory; heldHistory = undefined; await held.wait() }
    } else if (endpoint === '/api/admin/media-organize/history' && method === 'DELETE') {
      const selected = body.completed ? records.filter(item => item.status === 'done' && item.canDelete) : records.filter(item => body.items.some(key => key.runId === item.runId && key.fileId === item.fileId))
      if (selected.some(item => !item.canDelete)) { status = 409; data = { error: '整理任务正在处理，请稍后重试' } }
      else {
        records = records.filter(item => !selected.includes(item)); data = { deleted: selected.length }
        if (loseDelete) { loseDelete = false; records.push(row(100, 'done', { title: '刚成功的任务' })); return route.abort() }
      }
    } else if (endpoint.endsWith('/task')) {
      const item = records.find(item => item.fileId === url.searchParams.get('fileId'))
      assert.ok(item, 'detail must address an existing record')
      data = { ...clone(item), rule, group: { id: '1', kind: 'tv', tmdbId: 42, title: item.title, identity: { name: 'Work' } }, entries: [{ file: { id: item.fileId, name: item.name, sourcePath: item.name, mediaType: 'video', season: 1, episode: item.episode }, status: item.status === 'done' || item.status === 'partial' ? 'done' : 'error', target: item.target }] }
    } else throw new Error(`Unexpected API ${method} ${endpoint}`)
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
  })
  const page = await context.newPage(); page.setDefaultTimeout(10000); page.on('pageerror', error => errors.push(error.message))
  const nav = page.getByRole('navigation', { name: '整理功能', exact: true })
  const section = page.getByRole('region', { name: '整理记录', exact: true })
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
    await search.fill('needle_%'); assert.equal(readCount(), initialReads)
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
    await statusButton('待处理').click(); await page.waitForFunction(() => document.querySelectorAll('.mo-result').length === 1)
    await section.getByText('部分完成', { exact: true }).waitFor()
    assert.equal(new URL(page.url()).searchParams.get('status'), 'attention')
    await nav.getByRole('link', { name: '整理规则', exact: true }).click(); await page.locator('.mo-rule-card').waitFor()
    await nav.getByRole('link', { name: '整理记录', exact: true }).click(); await section.getByText('部分完成', { exact: true }).waitFor()
    assert.equal(await search.inputValue(), 'needle_%'); assert.equal(new URL(page.url()).searchParams.get('status'), 'attention')
    await page.reload(); await section.getByText('部分完成', { exact: true }).waitFor()
    passed.push('独立记录页按需读取；搜索由服务端跨全部记录筛选，回车提交并重置分页；部分完成归待处理；筛选地址支持切回和刷新恢复')

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

    await page.goto(origin + '/media/organize/history?status=attention'); await section.locator('.mo-result').first().waitFor()
    await section.locator('.mo-result').first().click()
    const detail = page.getByRole('dialog', { name: '整理详情', exact: true })
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
    assert.equal(await section.getByRole('button', { name: '选择Work.S01E48.mkv的整理记录', exact: true }).isDisabled(), true)
    await page.getByRole('checkbox', { name: '全选当前页', exact: true }).check()
    assert.equal(await section.locator('.mo-result[aria-pressed=true]').count(), 1)
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
    passed.push('活动批次的运行及已完成子项均不能勾选删除；历史页成功详情不轮询；尚未产生主文件的扫描仍显示状态')

    await page.goto(origin + '/media/organize/history?q=' + encodeURIComponent('needle_%')); await section.locator('.mo-result').first().waitFor()
    loseDelete = true
    const beforeDelete = deleteCalls().length
    await page.getByRole('button', { name: /清理.*成功/ }).click()
    const clear = page.getByRole('dialog', { name: /清理.*成功/ })
    await clear.getByRole('button', { name: '确认删除', exact: true }).click()
    await clear.waitFor({ state: 'detached' })
    await page.getByRole('alert').filter({ hasText: '删除结果尚未确认' }).waitFor()
    assert.equal(deleteCalls().length, beforeDelete + 1)
    assert.deepEqual(deleteCalls().at(-1).body, { completed: true })
    assert.ok(records.some(item => item.fileId === 'video-100'), 'new success after a lost receipt must not be deleted by automatic replay')
    assert.ok(records.some(item => item.fileId === 'video-48'), 'success in an active batch must remain')
    passed.push('清理成功明确作用全部成功记录；丢失删除回执后不自动重放，新增成功和活动批次的已完成子项保留')

    await page.goto(origin + '/media/organize/history'); await section.locator('.mo-result').first().waitFor()
    for (const width of [1440, 320, 390]) {
      await page.setViewportSize({ width, height: 900 })
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

    records = []
    await page.reload(); await page.getByText('暂无整理记录，可在整理规则中立即运行或启用监控', { exact: true }).waitFor()
    assert.equal(await section.locator('.mo-result').count(), 0)
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, reads: readCount(), deletions: deleteCalls().length, errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
