// Mock APIs only; no real subscription or 115 requests are made.
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const errors = [], passed = []
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block', timezoneId: 'Asia/Shanghai' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
  const page = await context.newPage(); page.setDefaultTimeout(10000); page.on('pageerror', error => errors.push(error.message))
  const now = Date.now(); await page.clock.install({ time: new Date(now) });
  const calls = [], receipts = new Map(), changes = {}, state = { status: 'enabled', revision: 2, failJobs: false, loseJob: true, losePause: true, empty: false }
  const source = { id: 'source-a', role: 'main', position: 0, configuredState: 'enabled', runWhenEnabled: true, provider: 'mikan', itemId: '300', groupId: '10', groupName: '银色子弹字幕组', revision: 1, health: 'ok', checkedAt: now, binding: { schemaVersion: 1, confirmed: true, scope: { kind: 'range', first: 1, last: 12 }, chapterCorrespondence: { sourceEpisode: 1, chapterEpisode: 1 } }, rules: { schemaVersion: 1, resolution: '1080p', include: [], exclude: [], minimumEpisode: 1, initialMode: 'from_episode' } }
  const sub = () => ({ id: 'sub-a', entryId: 'entry-a', status: state.status, revision: state.revision, firstEnabledAt: state.status === 'draft' ? null : now - 300000, updatedAt: now, entry: { id: 'entry-a', bangumiId: '123', editionKey: 'original', title: '从零开始的异世界生活 第三季', originalTitle: 'Re:ZERO', revision: 1, metadata: { coverUrl: '', airDate: '2026-10-01' } }, sources: [{ ...source, configuredState: state.status === 'draft' ? 'paused' : 'enabled' }], form: { formSelections: [], previewRefs: [] }, readiness: { ready: true, issues: [] }, permittedActions: ['enable', 'pause', 'resume', 'archive', 'unarchive', 'remove'].map(name => ({ name, allowed: name === 'enable' ? state.status === 'draft' : name === 'pause' ? state.status === 'enabled' : name === 'resume' ? state.status === 'paused' : name === 'unarchive' ? state.status === 'archived' : state.status !== 'removed' })) })
  const jobs = Array.from({ length: 23 }, (_, i) => ({ id: `job-${i}`, title: `[银色子弹字幕组] 从零开始的异世界生活 - ${String(i + 1).padStart(2, '0')} [1080p HEVC 简繁内封]`, groupName: '银色子弹字幕组', revision: 1, downloadState: i === 0 ? 'unknown' : i === 1 || i === 2 || i === 3 ? 'completed' : 'accepted', fileState: i === 2 ? 'verified' : i === 3 ? 'needs_review' : 'pending', workflowState: i === 2 ? 'original_saved' : 'active', blockCode: i === 0 ? 'SUBMIT_UNCERTAIN' : i === 3 ? 'FILES_INCOMPLETE' : null, executionPath: `/动漫下载/anime-job-${i}`, directoryCid: '200', createdAt: now - 600000, updatedAt: now, observedAt: now - 100000, attemptedAt: now - 50000, nextRunAt: now + 200000, progress: null, files: i === 2 ? [{ id: '400', name: '03.mkv', kind: 'file', size: '12345' }] : [], permittedActions: { reconcile: i !== 2, retry: i === 3 } }))
  const items = () => state.empty ? [] : Array.from({ length: 13 }, (_, i) => ({ release: { id: `release-${i}`, title: `[银色子弹字幕组] 番剧 - ${i + 1} [1080p]`, parse: { episode: i + 1 } }, observation: {}, projection: { sourceEpisode: i + 1, chapterEpisode: i + 1 }, selectable: true, decision: 'matched', reasonCode: 'MATCHED_METADATA_ONLY' }))
  const previewPage = (number = 1, size = 50) => ({ items: items().slice((number - 1) * size, number * size), total: items().length, page: number, pageSize: size, selectedIds: items().map(item => item.release.id), pendingSelections: [], summary: { sourceRef: 'source-a', sourcePreviewId: 'preview-a', loadRevision: 1, matchedCount: items().length, reviewCount: 0, selectedCount: items().length, pendingSelectionCount: 0, loadedCount: items().length, rawCount: items().length, unloadedHistory: false, generatedAt: now, expiresAt: now + 900000 } })
  await page.route('**/api/**', async route => {
    const req = route.request(), url = new URL(req.url()), endpoint = url.pathname, body = req.postDataJSON(), key = req.headers()['idempotency-key']
    calls.push({ endpoint, query: url.search, method: req.method(), body, key }); let status = 200, data = {}
    if (endpoint === '/api/auth/me') data = { user: { id: 'mock-admin', username: 'admin', role: 'admin' } }
    else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
    else if (endpoint.endsWith('/subscriptions')) data = { items: [sub()], total: 1, page: 1, pageSize: 20, dataVersion: 'list' }
    else if (endpoint.endsWith('/subscriptions/sub-a')) data = sub()
    else if (endpoint.endsWith('/subscriptions/sub-a/jobs')) {
      if (state.failJobs) { status = 503; data = { error: '模拟本地读取失败' } }
      else { const number = Number(url.searchParams.get('page') || 1); data = { items: state.status === 'draft' ? [] : jobs.slice((number - 1) * 20, number * 20), total: state.status === 'draft' ? 0 : jobs.length, page: number, pageSize: 20, dataVersion: 'jobs' } }
    } else if (endpoint.endsWith('/settings')) data = { settings: { revision: 3 }, readiness: { ready: true } }
    else if (endpoint.endsWith('/enable-preview')) data = { batchId: 'batch-a', sources: [previewPage().summary], issues: [], page: previewPage(), budget: { limit: 2000, remaining: 1987 } }
    else if (endpoint.endsWith('/previews/batch-a/releases')) data = previewPage(Number(url.searchParams.get('page')), Number(url.searchParams.get('pageSize')))
    else if (/\/(pause|resume|archive|unarchive|remove|enable|reconcile|retry)$/.test(endpoint)) {
      if (receipts.has(key)) { assert.deepEqual(receipts.get(key).body, body); data = receipts.get(key).receipt }
      else if (!endpoint.includes('/jobs/') && body.expectedRevision !== state.revision) { status = 409; data = { error: '配置版本已变化，请重载', code: 'REVISION_CONFLICT' } }
      else {
        const action = endpoint.split('/').pop(); changes[action] = (changes[action] || 0) + 1
        if (!endpoint.includes('/jobs/')) { state.status = ({ pause: 'paused', resume: 'enabled', archive: 'archived', unarchive: 'paused', remove: 'removed', enable: 'enabled' })[action]; state.revision++ }
        data = { id: endpoint.includes('/jobs/') ? 'job-0' : 'sub-a', revision: endpoint.includes('/jobs/') ? 2 : state.revision, status: state.status }
        receipts.set(key, { body, receipt: data })
        if (action === 'reconcile' && state.loseJob) { state.loseJob = false; return route.abort() }
        if (action === 'pause' && state.losePause) { state.losePause = false; return route.abort() }
      }
    }
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
  })
  const dialog = page.getByRole('dialog'), go = async () => page.goto(origin + '/anime/subscriptions/sub-a', { waitUntil: 'networkidle' })
  try {
    await fs.mkdir('test-results', { recursive: true }); await go()
    await dialog.locator('[data-job-id="job-0"]').waitFor(); assert.equal(await dialog.locator('.as-job').count(), 20)
    await dialog.locator('[data-job-id="job-0"] > button').click()
    await dialog.getByRole('button', { name: '核对原任务', exact: true }).first().click()
    await dialog.getByText('安排结果待确认，已保留原请求。').waitFor()
    assert.equal(await dialog.getByRole('button', { name: '暂停订阅', exact: true }).isEnabled(), true)
    await dialog.getByRole('button', { name: '核对原安排', exact: true }).click()
    await dialog.getByText(/安排核对已记录/).waitFor(); assert.equal(changes.reconcile, 1)
    passed.push('任务核对未知回执沿用原请求，单行处理不锁订阅，未知任务无重提按钮')
    await dialog.getByRole('button', { name: '暂停订阅', exact: true }).click()
    await dialog.getByText(/结果尚未确认，已保留原请求/).waitFor()
    await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'detached' })
    await page.locator('[data-subscription-id="sub-a"]').click(); await dialog.getByRole('button', { name: '核对原操作', exact: true }).click()
    await dialog.getByRole('button', { name: '恢复订阅', exact: true }).waitFor(); assert.equal(changes.pause, 1)
    passed.push('暂停回执丢失后关闭重开仍能核对原操作，不增加提交次数')
    await dialog.getByRole('button', { name: '恢复订阅', exact: true }).click(); await dialog.getByRole('button', { name: '暂停订阅', exact: true }).waitFor()
    const next = dialog.getByRole('button', { name: '任务下一页', exact: true }); await next.focus(); await page.keyboard.press('Enter')
    await dialog.locator('[data-job-id="job-20"]').waitFor(); assert.equal(await next.evaluate(el => el === document.activeElement), true)
    await dialog.locator('[data-job-id="job-20"] > button').click()
    await page.keyboard.press('Escape'); await dialog.waitFor({ state: 'detached' }); await page.locator('[data-subscription-id="sub-a"]').click()
    await dialog.locator('[data-job-id="job-20"]').waitFor(); assert.equal(await dialog.locator('[data-job-id="job-20"] > button').getAttribute('aria-expanded'), 'true')
    passed.push('任务分页与展开状态往返保留，末页键盘焦点不丢失')
    await dialog.getByRole('button', { name: '任务上一页' }).click(); await dialog.locator('[data-job-id="job-0"]').waitFor()
    await page.evaluate(() => { window.pinnedJob = document.querySelector('[data-job-id="job-0"]') })
    state.failJobs = true; await dialog.getByRole('button', { name: '刷新本地任务记录' }).click(); await dialog.getByText(/模拟本地读取失败/).waitFor()
    assert.equal(await page.evaluate(() => document.querySelector('[data-job-id="job-0"]') === window.pinnedJob), true)
    state.failJobs = false; await dialog.getByRole('button', { name: '刷新本地任务记录' }).click()
    await dialog.getByText(/模拟本地读取失败/).waitFor({ state: 'detached' })
    assert.equal(await dialog.getByText('文件清单已保存', { exact: true }).count(), 1)
    passed.push('刷新失败保留任务DOM与展开，网盘完成和文件清单状态分开')
    const countJobs = () => calls.filter(call => call.endpoint.endsWith('/subscriptions/sub-a/jobs')).length
    const readsBefore = countJobs()
    await page.clock.fastForward(31000)
    await page.waitForFunction(() => document.querySelector('[aria-label="刷新本地任务记录"]')?.disabled === false)
    assert.ok(countJobs() > readsBefore)
    assert.equal(await page.evaluate(() => document.querySelector('[data-job-id="job-0"]') === window.pinnedJob), true)
    await page.evaluate(() => { Object.defineProperty(document, 'hidden', { configurable: true, get: () => window.mockHidden }); window.mockHidden = true; document.dispatchEvent(new Event('visibilitychange')) })
    const hiddenReads = countJobs(); await page.clock.fastForward(61000); assert.equal(countJobs(), hiddenReads)
    await page.evaluate(() => { window.mockHidden = false; document.dispatchEvent(new Event('visibilitychange')) })
    await dialog.getByRole('button', { name: '刷新本地任务记录' }).click()
    passed.push('30秒可见轮询保留DOM且不切换加载态，隐藏时停止本地读取')
    await dialog.locator('[data-job-id="job-0"]').scrollIntoViewIfNeeded(); await page.screenshot({ path: 'test-results/anime-download-desktop.png' })
    await page.setViewportSize({ width: 320, height: 740 }); await page.screenshot({ path: 'test-results/anime-download-320.png' })
    assert.equal(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth), true)
    await page.evaluate(() => document.documentElement.classList.add('dark')); await page.screenshot({ path: 'test-results/anime-download-dark.png' })
    await dialog.getByRole('button', { name: '移除订阅', exact: true }).click(); await dialog.getByRole('button', { name: '确认移除', exact: true }).waitFor()
    await page.keyboard.press('Escape'); assert.equal(await dialog.count(), 1); assert.equal(await dialog.getByRole('button', { name: '确认移除', exact: true }).count(), 0)
    passed.push('320px和暗色无横向溢出，移除原位确认支持Esc返回')
    state.status = 'draft'; state.revision++; await go()
    await dialog.getByRole('button', { name: '预览并启用订阅', exact: true }).click()
    await dialog.getByRole('button', { name: '启用并下载已选 13 条', exact: true }).waitFor()
    assert.equal(await dialog.locator('.as-preview-choice').count(), 10)
    await dialog.locator('.as-preview-choice input').first().uncheck()
    await dialog.getByRole('button', { name: '历史下一页' }).click(); await page.waitForFunction(() => document.querySelectorAll('.as-preview-choice').length === 3)
    assert.equal(await dialog.locator('.as-preview-choice').count(), 3); await dialog.locator('.as-preview-choice input').first().uncheck()
    await dialog.getByRole('button', { name: '历史上一页' }).click(); await page.waitForFunction(() => document.querySelectorAll('.as-preview-choice').length === 10); assert.equal(await dialog.locator('.as-preview-choice input').first().isChecked(), false)
    await page.screenshot({ path: 'test-results/anime-enable-320.png' }); await dialog.getByRole('button', { name: '启用并下载已选 11 条', exact: true }).scrollIntoViewIfNeeded(); await page.screenshot({ path: 'test-results/anime-enable-action-320.png' })
    await dialog.getByRole('button', { name: '启用并下载已选 11 条', exact: true }).click(); await dialog.getByRole('button', { name: '暂停订阅', exact: true }).waitFor()
    const enable = calls.find(call => call.endpoint.endsWith('/enable')); assert.equal(enable.body.sources[0].selectedReleaseIds.length, 11); assert.equal(enable.body.expectedRevision, state.revision - 1)
    passed.push('首次启用每页10条，跨页保留勾选，明确提交选择与配置版本')
    state.status = 'draft'; state.revision++; state.empty = true; await go()
    await dialog.getByRole('button', { name: '预览并启用订阅', exact: true }).click(); await dialog.getByRole('button', { name: '启用并等待更新', exact: true }).waitFor()
    state.revision++; await dialog.getByRole('button', { name: '启用并等待更新', exact: true }).click(); await dialog.getByText(/配置版本已变化，请重载/).waitFor()
    assert.equal(changes.enable, 1); assert.equal(await dialog.getByRole('button', { name: '核对原操作', exact: true }).count(), 0)
    passed.push('零资源准确显示等待更新，版本冲突不套新版本重提')
    assert.equal(calls.some(call => /p115|\/cloud|\/drive/.test(call.endpoint)), false)
    assert.deepEqual(errors, []); console.log(JSON.stringify({ passed, apiCalls: calls.length, errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
