// 浏览器隔离验证：所有接口均为模拟，不写入 NAS 或 115。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const copy = value => JSON.parse(JSON.stringify(value))
const gate = () => {
  let enter, release
  const ready = new Promise(resolve => { enter = resolve }), pending = new Promise(resolve => { release = resolve })
  return { ready, release, wait: () => { enter(); return pending } }
}
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
  const page = await context.newPage(), calls = [], errors = []
  page.on('pageerror', error => errors.push(error.message)); page.setDefaultTimeout(10000)
  const rule = { id: 'rule1', name: 'NAS 验收', storage: 'local', source: '/media/inbox', target: '/media/library', failure: '/media/failed', classify: true, realtimeEnabled: false, scheduleEnabled: false, intervalSeconds: 300, attachmentCleanupEnabled: true, attachmentGraceSeconds: 60 }
  const entry = (id, mediaType, status, videoId) => ({ file: { id, name: path.basename(id), sourcePath: id, mediaType, season: 1, episode: 1 }, group: '1', videoId, target: '/media/library/' + id, status, message: status === 'error' ? '模拟附件写入失败' : '' })
  const task = (id, status, entries) => ({ runId: 'run1', fileId: id, name: path.basename(id), title: '示例作品', kind: 'tv', mediaType: 'video', season: 1, episode: 1, source: rule.source + '/' + id, target: rule.target + '/' + id, status, at: new Date().toISOString(), attachmentCount: entries.length - 1, canDelete: true, canRetry: entries.some(entry => !['done', 'retained'].includes(entry.status)), entries, rule: copy(rule), group: { id: '1', identity: { name: 'Unknown' }, kind: 'tv', tmdbId: 42, title: '示例作品' } })
  const first = task('Work.S01E01.strm', 'done', [entry('Work.S01E01.strm', 'video', 'done'), entry('Work.S01E01.ass', 'subtitle', 'error', 'Work.S01E01.strm'), entry('Work.S01E01.nfo', 'attachment', 'done', 'Work.S01E01.strm')])
  const second = task('Work.S01E02.mkv', 'failed', [entry('Work.S01E02.mkv', 'video', 'needs_match')])
  second.episode = 2; second.group.tmdbId = 0; second.group.message = '未找到唯一匹配'
  const third = task('another/Work.S01E01.mkv', 'done', [entry('another/Work.S01E01.mkv', 'video', 'done')])
  const tasks = [first, second, third]
  const state = { rules: [{ ...copy(rule), scheduleEnabled: true }], active: true, activeRules: [rule.id], records: tasks, cleanup: [{ runId: 'run1', ruleName: rule.name, files: 6, directories: 2, failed: 0, at: first.at }] }
  let rejectRuleSave = false, rejectRun = false, runStatus = 'queued', heldRun, heldTask, heldState, heldRuleState, heldSchedule
  await page.route('**/api/**', async route => {
    const req = route.request(), u = new URL(req.url()), endpoint = u.pathname, method = req.method(), body = req.postDataJSON()
    calls.push({ endpoint, method, body, query: u.searchParams.toString() })
    let data = {}, status = 200
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
    else if (endpoint === '/api/admin/media-organize') data = { rules: state.rules, active: state.active, activeRules: state.activeRules, monitoring: { [rule.id]: { mode: 'realtime' } } }
    else if (endpoint === '/api/admin/media-organize/history') {
      const q = (u.searchParams.get('q') || '').toLowerCase(), filter = u.searchParams.get('status') || 'all'
      const matches = state.records.filter(item => [item.title, item.name, item.source, item.target].some(value => value?.toLowerCase().includes(q)))
      const category = item => item.status
      const records = matches.filter(item => filter === 'all' || category(item) === filter)
      const counts = { all: matches.length, running: 0, done: 0, failed: 0 }; matches.forEach(item => counts[category(item)]++)
      const pages = Math.max(1, Math.ceil(records.length / 20)), page = Math.max(1, Math.min(Number(u.searchParams.get('page') || 1), pages))
      data = { items: records.slice((page - 1) * 20, page * 20).map(({entries, rule, group, ...item}) => item), page, pages, total: records.length, counts, active: state.active, activity: [], cleanup: state.cleanup }
    } else if (endpoint.endsWith('/run')) {
      if (heldRun) { const pending = heldRun; heldRun = undefined; await pending.wait() }
      if (rejectRun) { rejectRun = false; status = 503; data = { error: '模拟运行提交失败' } }
      else { state.active = true; state.activeRules = [rule.id]; data = { id: 'existing-run', status: runStatus, trigger: 'manual' } }
    }
    else if (endpoint.endsWith('/task')) { data = tasks.find(task => task.fileId === u.searchParams.get('fileId')); assert.ok(data, 'task ID is required') }
    else if (endpoint.endsWith('/retry')) {
      assert.equal('groups' in body, false); assert.equal('files' in body, false)
      const selected = tasks.find(task => task.fileId === body.fileId)
      assert.ok(selected)
      if (selected.entries[0].status === 'done') assert.deepEqual(body, { fileId: first.fileId })
      if (body.tmdbId) { selected.group.tmdbId = body.tmdbId; selected.entries[0].file.season = body.season; selected.entries[0].file.episode = body.episode }
      selected.status = 'done'; selected.canRetry = false; selected.entries.forEach(e => { e.status = 'done'; e.message = '' }); data = selected
    } else if (endpoint.endsWith('/schedule') && method === 'PATCH') {
      if (heldSchedule) { const pending = heldSchedule; heldSchedule = undefined; await pending.wait() }
      if (rejectRuleSave) { status = 503; data = { error: '模拟保存失败' }; rejectRuleSave = false }
      else { Object.assign(rule, body); state.rules = [copy(rule)]; data = rule }
    } else if (endpoint.endsWith('/rules') && method === 'POST') {
      if (rejectRuleSave) { status = 503; data = { error: '模拟保存失败' }; rejectRuleSave = false }
      else { Object.assign(rule, body); state.rules = [copy(rule)]; data = rule }
    } else if (endpoint === '/api/admin/tmdb/browse') data = { items: [{ id: 99, kind: 'tv', title: '修正作品', date: '2024-01-01' }] }
    else if (endpoint === '/api/admin/p115/accounts') data = { accounts: [{ id: 'primary1', primary: true }, { id: 'secondary', primary: false }] }
    else if (endpoint === '/api/admin/p115/cloud-download/paths') { assert.equal(u.searchParams.get('accountId'), 'primary1'); data = { directories: [{ id: '10', name: 'inbox' }], hasMore: false } }
    const response = { status, contentType: 'application/json', body: JSON.stringify(data) }
    if (endpoint.endsWith('/task') && heldTask) { const pending = heldTask; heldTask = undefined; await pending.wait() }
    if (endpoint === '/api/admin/media-organize/history' && heldState) { const pending = heldState; heldState = undefined; await pending.wait() }
    if (endpoint === '/api/admin/media-organize' && heldRuleState) { const pending = heldRuleState; heldRuleState = undefined; await pending.wait() }
    await route.fulfill(response).catch(() => {})
  })
  try {
    await fs.mkdir('test-results', { recursive: true })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/media/organize', { waitUntil: 'networkidle' })
    const history = page.getByRole('region', { name: '整理记录', exact: true })
    const nav = page.getByRole('navigation', { name: '整理功能', exact: true })
    const tab = label => nav.getByRole('link', { name: label, exact: true })
    const detail = page.getByRole('dialog', { name: '整理详情', exact: true })
    const card = history.getByRole('button', { name: '查看Work.S01E01.strm的整理记录', exact: true })
    assert.equal(await history.count(), 0)
    assert.equal(calls.some(call => call.endpoint.endsWith('/history')), false)
    assert.equal(await page.getByRole('button', { name: '扫描预览', exact: true }).count(), 0)
    const runButton = page.getByRole('button', { name: '立即运行', exact: true })
    await page.getByText('本轮结束后按设定间隔扫描', { exact: true }).waitFor()
    assert.equal(await runButton.isEnabled(), true, '已有自动排队批次仍可提交手动优先')
    const runSubmit = gate(); heldRun = runSubmit
    await runButton.click(); await runSubmit.ready
    assert.equal(await page.getByRole('button', { name: '提交中…', exact: true }).isDisabled(), true)
    await page.locator('.mo-rule-scan').evaluate(button => { button.click(); button.click() })
    assert.equal(calls.filter(c => c.endpoint.endsWith('/run')).length, 1, '请求尚未返回时不能重复提交')
    const stateRefresh = gate(); heldRuleState = stateRefresh
    runSubmit.release(); await stateRefresh.ready
    await page.getByRole('status').filter({ hasText: 'NAS 验收已按手动任务排队' }).waitFor()
    assert.equal(await detail.count(), 0)
    assert.equal(await runButton.isEnabled(), true, '提交后的慢状态刷新不能延长按钮锁')
    assert.equal(calls.filter(c => c.endpoint.endsWith('/run')).length, 1)
    assert.equal(state.rules[0].scheduleEnabled, true, '手动运行不得修改定时开关')
    stateRefresh.release()
    for (const phase of ['running', 'scanning']) {
      runStatus = phase
      await runButton.click()
      await page.getByRole('status').filter({ hasText: phase === 'scanning' ? 'NAS 验收正在识别，继续执行现有批次' : 'NAS 验收正在整理，继续执行现有批次' }).waitFor()
      assert.equal(await runButton.isEnabled(), true, '复用执行中的批次后仍可再次查询运行状态')
    }
    rejectRun = true
    await runButton.click()
    await page.getByRole('alert').filter({ hasText: '模拟运行提交失败' }).waitFor()
    assert.equal(await runButton.isEnabled(), true)
    const scheduleSave = gate(); heldSchedule = scheduleSave
    await page.getByRole('button', { name: '暂停NAS 验收的定时扫描', exact: true }).click(); await scheduleSave.ready
    assert.equal(await runButton.isDisabled(), true, '同规则的其他提交期间不能交错提交运行')
    const scheduleRefresh = gate(); heldRuleState = scheduleRefresh
    scheduleSave.release()
    await scheduleRefresh.ready
    await page.getByText('定时扫描已暂停', { exact: true }).waitFor()
    await page.waitForFunction(() => !document.querySelector('.mo-rule-scan')?.disabled)
    assert.equal(await runButton.isEnabled(), true, '开关提交完成后的状态刷新也不能延长运行按钮锁')
    scheduleRefresh.release()
    const submittedRuns = calls.filter(c => c.endpoint.endsWith('/run')).length
    state.active = false; state.activeRules = []
    await tab('整理记录').click()
    await card.waitFor()
    assert.equal(await history.locator('.mo-result').count(), 3)
    await card.getByText('成功', { exact: true }).waitFor()
    assert.equal(await card.getByText('附件 2', { exact: true }).count(), 1)
    await card.click()
    await detail.locator('.mo-file').first().waitFor()
    assert.equal(await detail.locator('.mo-file').count(), 1)
    assert.equal(await detail.locator('.mo-attachment-list>li').count(), 2)
    assert.equal(await detail.getByText('Work.S01E02.mkv', { exact: true }).count(), 0)
    assert.equal(await detail.getByRole('spinbutton').count(), 0)
    await detail.getByText('模拟附件写入失败', { exact: true }).waitFor()
    await page.screenshot({ path: 'test-results/media-organize-task-desktop.png' })
    const before = copy(second)
    // 旧详情回执不能覆盖重试提交后的状态。
    const staleTask = gate(); heldTask = staleTask
    first.at = new Date(Date.now() + 1000).toISOString()
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await staleTask.ready
    const nextState = gate(); heldState = nextState
    await detail.getByRole('button', { name: '重试附件', exact: true }).click()
    staleTask.release()
    await nextState.ready
    assert.deepEqual(copy(second), before)
    assert.equal(await detail.getByRole('button', { name: '重试附件', exact: true }).count(), 0)
    await card.getByText('成功', { exact: true }).waitFor()
    nextState.release()
    await detail.getByRole('button', { name: '关闭', exact: true }).click()
    await history.getByRole('button', { name: '查看Work.S01E02.mkv的整理记录', exact: true }).click()
    await detail.locator('.mo-file').first().waitFor()
    assert.equal(await detail.locator('.mo-file').count(), 1)
    assert.equal(await detail.getByText(/关联文件/).count(), 0)
    await detail.getByRole('spinbutton', { name: '集', exact: true }).fill('13')
    await detail.getByRole('button', { name: '匹配作品', exact: true }).click()
    await detail.getByRole('button', { name: '搜索作品', exact: true }).click()
    await detail.getByRole('button', { name: /修正作品.*2024/ }).click()
    await detail.getByRole('button', { name: '保存并重试此任务', exact: true }).click()
    const retry = calls.filter(c => c.endpoint.endsWith('/retry')).at(-1)
    assert.deepEqual(retry.body, { fileId: second.fileId, season: 1, episode: 13, kind: 'tv', tmdbId: 99 })
    await detail.getByRole('button', { name: '关闭', exact: true }).click()
    await tab('整理规则').click()
    await page.getByRole('button', { name: '启用NAS 验收的定时扫描', exact: true }).click()
    await page.getByText('定时扫描 · 每 5 分钟', { exact: true }).waitFor()
    rejectRuleSave = true
    await page.getByRole('button', { name: '暂停NAS 验收的定时扫描', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '模拟保存失败' }).waitFor()
    assert.equal(await page.getByRole('button', { name: '暂停NAS 验收的定时扫描', exact: true }).count(), 1)
    await page.getByRole('button', { name: '暂停NAS 验收的定时扫描', exact: true }).click()
    await page.getByText('定时扫描已暂停', { exact: true }).waitFor()
    assert.equal(calls.filter(c => c.endpoint.endsWith('/run')).length, submittedRuns)
    await page.locator('.mo-rule-card').hover()
    assert.ok((await page.locator('.mo-rule-card').evaluate(el => getComputedStyle(el, '::before').animationName)).includes('ambient-glow'))
    await page.emulateMedia({ reducedMotion: 'reduce' })
    assert.equal(await page.locator('.mo-rule-card').evaluate(el => getComputedStyle(el, '::before').animationName), 'none')
    await tab('整理记录').click()
    await page.locator('.mo-cleanup summary').click()
    await page.getByText('文件 6 · 空目录 2', { exact: true }).waitFor()
    state.records = Array.from({ length: 45 }, (_, i) => ({ ...copy(first), fileId: 'fixture-' + i, episode: i + 1 }))
    await page.getByRole('button', { name: '刷新记录', exact: true }).click()
    const pagination = page.getByRole('navigation', { name: '整理记录分页', exact: true })
    await pagination.getByRole('button', { name: '下一页', exact: true }).click()
    await history.getByText('S01E21', { exact: true }).waitFor()
    assert.equal(await history.locator('.mo-result').count(), 20)
    state.records = tasks
    await page.getByRole('button', { name: '刷新记录', exact: true }).click()
    await page.waitForFunction(() => document.querySelectorAll('.mo-result').length === 3)
    await page.setViewportSize({ width: 390, height: 844 })
    await page.getByRole('button', { name: '打开功能导航', exact: true }).waitFor()
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1), false, await page.evaluate(() => JSON.stringify([...document.querySelectorAll('main *')].filter(el => el.getBoundingClientRect().right > window.innerWidth + 1).slice(0, 15).map(el => [el.className, el.getBoundingClientRect().width, el.textContent.slice(0, 40)]))))
    assert.equal(await history.locator('.mo-result').evaluateAll(cards => cards.every(card => card.getBoundingClientRect().height < 210)), true)
    await page.screenshot({ path: 'test-results/media-organize-tasks-mobile-dark.png', fullPage: true })
    await card.click()
    assert.equal(await detail.evaluate(el => el.scrollWidth > el.clientWidth + 1), false)
    assert.ok((await detail.boundingBox()).height <= 844)
    await page.screenshot({ path: 'test-results/media-organize-task-mobile-dark.png' })
    await detail.getByRole('button', { name: '关闭整理详情', exact: true }).click()
    second.status = 'failed'; second.canRetry = true; second.entries[0].status = 'needs_match'
    second.entries[0].file.sourcePath = 'long-directory/'.repeat(30) + second.name
    second.entries[0].target = '/media/library/' + 'long-destination/'.repeat(30) + second.name
    second.entries[0].file.name = 'Long.Video.Name.'.repeat(20) + '.mkv'
    await page.getByRole('button', { name: '刷新记录', exact: true }).click()
    await history.getByRole('button', { name: '查看Work.S01E02.mkv的整理记录', exact: true }).click()
    await page.setViewportSize({ width: 320, height: 740 })
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    const retryCount = calls.filter(c => c.endpoint.endsWith('/retry')).length
    await detail.getByRole('spinbutton', { name: '集', exact: true }).fill('')
    await detail.getByRole('button', { name: '保存并重试此任务', exact: true }).click()
    await detail.getByRole('alert').filter({ hasText: '请填写有效的目标季集' }).waitFor()
    assert.equal(calls.filter(c => c.endpoint.endsWith('/retry')).length, retryCount)
    await detail.getByRole('spinbutton', { name: '集', exact: true }).fill('13')
    assert.equal(await detail.evaluate(el => el.scrollWidth > el.clientWidth + 1), false)
    assert.ok((await detail.getByRole('button', { name: '重试此任务', exact: true }).boundingBox()).x >= 0)
    await page.screenshot({ path: 'test-results/media-organize-task-narrow.png' })
    await detail.getByRole('button', { name: '关闭整理详情', exact: true }).click()
    await page.setViewportSize({ width: 390, height: 844 })
    await tab('整理规则').click()
    rule.attachmentGraceSeconds = 1200; state.rules = [copy(rule)]
    await page.reload({ waitUntil: 'networkidle' })
    await page.getByRole('button', { name: '编辑NAS 验收' }).click()
    const editor = page.getByRole('dialog', { name: '整理规则', exact: true })
    await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).waitFor()
    assert.equal(await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).inputValue(), '1200')
    for (const seconds of ['10', '0']) {
      await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).fill(seconds)
      await editor.getByRole('button', { name: '保存规则', exact: true }).click()
      await editor.waitFor({ state: 'hidden' })
      assert.equal(rule.attachmentGraceSeconds, Number(seconds))
      await page.reload({ waitUntil: 'networkidle' })
      await page.getByRole('button', { name: '编辑NAS 验收' }).click()
      assert.equal(await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).inputValue(), seconds)
    }
    await editor.getByRole('combobox', { name: '存储位置' }).click()
    await page.getByRole('option', { name: '115 主账号', exact: true }).click()
    await editor.getByRole('button', { name: '选择来源目录' }).click()
    const picker = page.getByRole('dialog', { name: '选择来源目录', exact: true })
    await picker.getByText('inbox', { exact: true }).waitFor()
    await picker.getByRole('button', { name: '关闭目录选择器', exact: true }).click()
    await editor.getByRole('button', { name: '取消', exact: true }).click()
    await page.getByRole('button', { name: '新增规则', exact: true }).click()
    assert.equal(await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).inputValue(), '10')
    await editor.getByRole('button', { name: '取消', exact: true }).click()
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed: ['自动排队仍可提交手动优先，queued/running/scanning反馈准确', '仅提交期间防重复，同规则操作互斥，慢状态刷新不延长提交锁', '周期提示按本轮结束后的间隔计算', '主文件详情、附件重试、独立修正、监控保存、清理摘要、分页、深色移动端、减少动画及主账号目录选择'], runs: submittedRuns, errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
