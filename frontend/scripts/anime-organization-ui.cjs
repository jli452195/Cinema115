// All API calls are intercepted; this script never accesses real 115 data.
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
  const page = await context.newPage(), passed = [], errors = [], calls = [], receipts = new Map()
  page.setDefaultTimeout(10000); page.on('pageerror', err => errors.push(err.message)); page.on('dialog', dialog => dialog.accept())
  const now = Date.now(), state = { failPreview: false, failJobs: false, loseDirectory: true, loseReconcile: true, writes: 0, reconcileWrites: 0, phase: 'pending', jobRevision: 1, longPath: false }
  await page.clock.install({ time: new Date(now) })
  let settings = { revision: 3, accountId: 'main', accountRevision: 2, uid: '123', downloadCid: '100', downloadPath: '/下载', organizeCid: '900', organizePath: '/整理' }
  const naming = { schemaVersion: 1, target: { tmdbId: '335790', title: '黑暗召唤师在地下恋爱中', year: '2026', season: 1, groupId: '', groupName: 'TMDB 默认', fingerprint: 'a'.repeat(64), seasons: [{ number: 1, name: '第1季', episodeCount: 12, episodes: [{ id: '101', number: 1, name: '开始' }] }] }, sources: [{ sourceRef: 'source-a', segments: [{ first: 25, sourceBase: 25, targetSeason: 1, targetBase: 1 }], overrides: [] }] }
  const source = { id: 'source-a', role: 'main', provider: 'mikan', position: 0, configuredState: 'paused', revision: 1, runWhenEnabled: true, itemId: '300', groupId: '10', groupName: '银色子弹', binding: { schemaVersion: 1, confirmed: true, scope: { kind: 'feed' }, review: { method: 'source_review', itemId: '300', groupId: '10' } }, rules: { schemaVersion: 1, minimumEpisode: 25, match: [], exclude: [] } }
  const sub = { id: 'sub-a', entryId: 'entry-a', status: 'paused', revision: 2, firstEnabledAt: now, updatedAt: now, naming, entry: { id: 'entry-a', title: naming.target.title, editionKey: 'original', revision: 1, metadata: { schemaVersion: 1 } }, sources: [source], form: { schemaVersion: 1 }, readiness: { ready: true, issues: [] }, permittedActions: ['resume', 'archive', 'remove'].map(name => ({ name, allowed: true })) }
  const plan = () => ({ mode: 'standard', revision: 2, cloudConflictChecked: false, items: [
    { id: '300', kind: 'video', originalPath: `/下载/anime-job/${state.longPath ? 'UnbrokenRelease'.repeat(14) : 'video'}.mkv`, targetPath: `/旧整理/作品 (2026) {tmdb-335790}/Season 01/${state.longPath ? 'UnbrokenTitle'.repeat(14) : '作品'}.S01E01.银色子弹.mkv`, status: state.skipped ? 'skipped' : 'ready', reason: state.skipped ? 'existing_target' : 'mapped' },
    { id: '301', kind: 'subtitle', originalPath: '/下载/anime-job/video.chs.ass', targetPath: '/旧整理/作品 (2026) {tmdb-335790}/Season 01/作品.S01E01.银色子弹.chs.ass', status: 'ready', reason: 'subtitle_matched', videoId: '300' },
    { id: '302', kind: 'video', originalPath: '/下载/anime-job/other.mkv', targetPath: '/旧整理/重复.mkv', status: 'conflict', reason: 'duplicate_target' },
    { id: '303', kind: 'attachment', originalPath: '/下载/anime-job/readme.txt', targetPath: '', status: 'preserved', reason: 'attachment_preserved' }
  ] })
  await page.route('**/api/**', async route => {
    const req = route.request(), url = new URL(req.url()), endpoint = url.pathname, body = req.postDataJSON(), key = req.headers()['idempotency-key']
    calls.push({ endpoint, body, key, method: req.method() }); let data = {}, status = 200
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (endpoint.endsWith('/discovery/subjects/123')) data={subject:{id:'123',title:naming.target.title,coverUrl:'',date:'2026-10-01'},metadata:{expiresAt:Date.now()+600000}}
    else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
    else if (endpoint.endsWith('/subscriptions')) data = { items: [sub], total: 1, page: 1, pageSize: 20 }
    else if (endpoint.endsWith('/subscriptions/sub-a')) data = sub
    else if (endpoint.endsWith('/settings')) data = { settings, primaryAccount: { id: 'main', name: '主网盘', uid: '123', revision: 2 }, readiness: { ready: true, issues: [] } }
    else if (endpoint.endsWith('/subscriptions/sub-a/jobs')) {
      if (state.failJobs) { status = 503; data = { error: '模拟本地任务读取失败' } }
      else data = { items: [{ id: 'job-a', title: '[银色子弹] 作品 - 25', groupName: '银色子弹', revision: state.jobRevision, downloadState: 'completed', fileState: 'verified', workflowState: state.phase === 'done' ? 'organized' : 'active', organizationPlanned: true, organization: { state: state.phase, total: 2, renamed: state.mappingReview || state.phase === 'pending' ? 0 : 2, moved: state.mappingReview ? 0 : state.phase === 'done' ? 2 - (state.skipped || 0) : state.phase === 'needs_review' ? 1 : 0, skipped: state.skipped || 0, targetPath: '/旧整理/这是一个很长的目录用来确认手机上自然折行', updatedAt: now }, blockCode: state.conflictReview ? 'ORGANIZE_TARGET_CONFLICT' : state.mappingReview ? 'ORGANIZE_MAPPING_REQUIRED' : state.phase === 'unknown' ? 'ORGANIZE_UNCERTAIN' : state.phase === 'needs_review' ? 'ORGANIZE_RESULT_UNRESOLVED' : null, executionPath: '/下载/anime-job', directoryCid: '200', files: [{ id: '300', kind: 'file', name: 'video.mkv', size: '100' }], updatedAt: now, observedAt: now, permittedActions: { reconcile: !state.mappingReview && !state.conflictReview && ['unknown', 'needs_review'].includes(state.phase), retry: !!(state.mappingReview || state.conflictReview) } }], total: 1, page: 1, pageSize: 20, sourceProgress: { 'source-a': { state: state.phase === 'done' ? 'complete' : 'missing', firstEpisode: 25, publishedThrough: 25, organizedCount: state.phase === 'done' ? 1 : 0, missingCount: state.phase === 'done' ? 0 : 1 } } }
    }
    else if (endpoint.endsWith('/jobs/job-a/retry')) {
      assert.equal(req.method(), 'POST'); assert.deepEqual(body, { expectedRevision: state.jobRevision })
      state.retryWrites = (state.retryWrites || 0) + 1
      state.mappingReview = false; state.conflictReview = false; state.phase = 'working'
      data = { id: 'job-a', revision: ++state.jobRevision }
    }
    else if (endpoint.endsWith('/jobs/job-a/reconcile')) {
      assert.equal(req.method(), 'POST')
      assert.deepEqual(Object.keys(body), ['expectedRevision'])
      if (state.reconcileGate) await state.reconcileGate
      if (receipts.has(key)) { assert.deepEqual(receipts.get(key).body, body); data = receipts.get(key).data; state.phase = 'working' }
      else {
        assert.equal(body.expectedRevision, state.jobRevision)
        state.reconcileWrites++; data = { id: 'job-a', revision: ++state.jobRevision }
        receipts.set(key, { body, data })
        if (state.loseReconcile) { state.loseReconcile = false; return route.abort() }
        state.phase = 'working'
      }
    }
    else if (endpoint.endsWith('/organization-preview')) {
      if (state.failPreview) { status = 503; data = { error: '模拟预览失败' } }
      else data = plan()
    } else if (endpoint.includes('/cloud-download/paths')) data = { directories: url.searchParams.get('cid') === '0' ? [{ id: '901', name: '新整理' }] : [], hasMore: false }
    else if (endpoint.endsWith('/settings/organize-directory')) {
      assert.equal(req.method(), 'PATCH')
      if (receipts.has(key)) { assert.deepEqual(receipts.get(key).body, body); data = receipts.get(key).data }
      else {
        assert.equal(body.expectedRevision, settings.revision)
        state.writes++; settings = { ...settings, revision: settings.revision + 1, organizeCid: body.cid, organizePath: '/新整理' }
        data = { id: 'anime-settings', revision: settings.revision, settings }; receipts.set(key, { body, data })
        if (state.loseDirectory) { state.loseDirectory = false; return route.abort() }
      }
    }
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
  })
  try {
    await fs.mkdir('test-results', { recursive: true })
    await page.goto(origin + '/anime/subscriptions/sub-a', { waitUntil: 'networkidle' })
    const rule = page.getByRole('textbox', { name: '匹配正则（全部满足）', exact: true, includeHidden: true })
    await rule.fill('keep-my-unsaved-rule')
    const configReads = calls.filter(c => c.endpoint.endsWith('/subscriptions/sub-a') && c.method === 'GET').length
    await page.getByRole('tab').nth(2).click()
    await page.locator('[data-job-id="job-a"] > button').click()
    const job = page.locator('[data-job-id="job-a"]')
    await job.getByRole('button', { name: '查看整理预览', exact: true }).click()
    await job.getByText('字幕已配对', { exact: true }).waitFor(); await job.getByText('目标路径重复，不覆盖', { exact: true }).waitFor(); await job.getByText('附件保留原处', { exact: true }).waitFor()
    assert.ok((await job.locator('.as-plan-item').first().innerText()).includes('/旧整理/'))
    passed.push('实际任务保持冻结目录，展示视频、字幕、冲突与附件，不追踪当前位置')
    const refresh = page.locator('.as-jobs').getByRole('button', { name: '刷新本地任务记录' })
    const previewReads = () => calls.filter(c => c.endpoint.includes('/jobs/') && c.endpoint.endsWith('/organization-preview')).length
    await job.evaluate(el => { window.pinnedPlan = el.querySelector('.as-plan-items'); window.pinnedJob = el })
    state.failJobs = true; await refresh.click(); await page.getByText(/模拟本地任务读取失败/).waitFor()
    assert.equal(await job.evaluate(el => el === window.pinnedJob && el.querySelector('.as-plan-items') === window.pinnedPlan), true)
    state.failJobs = false; await refresh.click(); await page.getByText(/模拟本地任务读取失败/).waitFor({ state: 'detached' })
    state.failPreview = true; await job.getByRole('button', { name: '重新读取整理预览' }).click()
    await job.getByText(/模拟预览失败.*上次预览已保留/).waitFor()
    assert.equal(await job.locator('.as-plan-item').count(), 4); state.failPreview = false
    passed.push('任务记录或实际路径预览读取失败保留原DOM、展开与旧结果')
    const jobsReads = () => calls.filter(c => c.endpoint.endsWith('/subscriptions/sub-a/jobs')).length
    await job.scrollIntoViewIfNeeded()
    const pinnedReads = previewReads()
    const scrollTop = () => job.evaluate(el => {
      for (let parent = el.parentElement; parent; parent = parent.parentElement) {
        if (parent.scrollHeight > parent.clientHeight && /auto|scroll/.test(getComputedStyle(parent).overflowY)) return parent.scrollTop
      }
      return window.scrollY
    })
    const initialTop = await scrollTop()
    state.phase = 'working'
    for (let i = 0; i < 3; i++) {
      const before = jobsReads(); state.jobRevision++
      await page.clock.fastForward(31000)
      await page.waitForFunction(() => document.querySelector('.as-job-phase .as-state')?.textContent === '正在整理')
      await page.waitForFunction(() => document.querySelector('[aria-label="刷新本地任务记录"]')?.disabled === false)
      assert.ok(jobsReads() > before)
      assert.equal(await job.evaluate(el => el === window.pinnedJob && el.querySelector('.as-plan-items') === window.pinnedPlan), true)
      assert.ok(Math.abs(await scrollTop() - initialTop) <= 2, '本地轮询保持滚动位置')
    }
    assert.equal(previewReads(), pinnedReads, '轮询不能重复读取冻结命名计划')
    passed.push('三轮30秒静默轮询保持展开、预览DOM与滚动位置，不重复读取命名计划')
    state.phase = 'unknown'; state.jobRevision++; await refresh.click()
    const reconcile = job.getByRole('button', { name: '核对整理结果', exact: true })
    await reconcile.focus(); await page.keyboard.press('Enter')
    await job.getByText('安排结果待确认，已保留原请求。').waitFor()
    assert.equal(await reconcile.isDisabled(), true)
    state.jobRevision++; await refresh.click()
    const confirm = job.getByRole('button', { name: '核对原安排', exact: true })
    await confirm.focus(); await page.keyboard.press('Enter')
    await job.getByText('安排核对已完成，后台会沿用原任务处理。', { exact: true }).waitFor()
    const reconciles = calls.filter(c => c.endpoint.endsWith('/jobs/job-a/reconcile'))
    assert.equal(reconciles.length, 2); assert.equal(reconciles[0].key, reconciles[1].key)
    assert.deepEqual(reconciles[0].body, reconciles[1].body); assert.equal(state.reconcileWrites, 1)
    assert.equal(await job.locator('.as-operation-feedback').evaluate(el => el === document.activeElement), true, '核对按钮消失后焦点留在操作反馈')
    passed.push('整理核对回执丢失保留原版本与幂等请求，键盘确认后焦点留在反馈区域')
    state.phase = 'unknown'; state.jobRevision++; await refresh.click()
    let releaseReconcile
    state.reconcileGate = new Promise(resolve => { releaseReconcile = resolve })
    await reconcile.focus(); await page.keyboard.press('Enter')
    await job.getByText('正在安排…', { exact: true }).waitFor()
    await refresh.focus(); releaseReconcile()
    await job.getByText('安排核对已完成，后台会沿用原任务处理。', { exact: true }).waitFor(); state.reconcileGate = undefined
    assert.equal(await refresh.evaluate(el => el === document.activeElement), true, '异步反馈不抢走用户已移动的焦点')
    passed.push('等待核对回执时用户可继续键盘导航，异步完成不抢回焦点')
    for (const [phase, label] of [['working', '正在整理'], ['unknown', '整理结果待核对'], ['needs_review', '整理需要处理'], ['done', '已整理']]) {
      state.phase = phase; state.jobRevision++
      await page.locator('.as-jobs').getByRole('button', { name: '刷新本地任务记录' }).click()
      await job.locator('.as-job-phase .as-state').filter({ hasText: label }).waitFor()
      assert.equal(await job.locator('.as-plan-item').count(), 4, '轮询不能重置展开的命名计划')
      if (['unknown', 'needs_review'].includes(phase)) {
        await job.getByRole('button', { name: '核对整理结果', exact: true }).waitFor()
        assert.equal(await job.getByRole('button', { name: /继续准备下载|重试读取/ }).count(), 0)
        if (phase === 'unknown') { await job.scrollIntoViewIfNeeded(); await page.screenshot({ path: 'test-results/anime-organization-unknown.png' }) }
      } else assert.equal(await job.getByRole('button', { name: '核对整理结果', exact: true }).count(), 0)
    }
    state.phase = 'needs_review'; state.mappingReview = true; state.jobRevision++
    await refresh.click()
    const retry = job.getByRole('button', { name: '重新整理', exact: true })
    await retry.focus(); await page.keyboard.press('Enter')
    await job.getByText('安排重新整理已完成，后台会沿用原任务处理。', { exact: true }).waitFor()
    assert.equal(state.retryWrites, 1)
    assert.equal(await job.getByRole('button', { name: '重新整理', exact: true }).count(), 0)
    assert.equal(calls.some(c=>c.endpoint.endsWith('/redownload')), false, 'replanning never schedules another download')
    passed.push('未写入的映射失败复用重试接口显示重新整理，键盘提交沿用原任务，不重新下载')
    state.phase = 'done'; state.jobRevision++; await refresh.click()
    await job.getByText('已移动 2/2', { exact: false }).waitFor()
    passed.push('真实整理进度区分工作、未知、部分成功与完成，未知仅核对，完成无执行按钮且保留预览DOM')
    state.phase = 'needs_review'; state.conflictReview = true; state.jobRevision++; await refresh.click()
    await job.getByRole('button', { name: '继续整理', exact: true }).click()
    await job.getByText('安排继续整理已完成，后台会沿用原任务处理。', { exact: true }).waitFor()
    assert.equal(state.retryWrites, 2)
    state.phase = 'done'; state.skipped = 1; state.jobRevision++; await refresh.click()
    await job.getByText('已移动 1/2', { exact: false }).waitFor()
    await job.getByText('已有跳过 1 个', { exact: false }).waitFor()
    await job.getByRole('button', { name: '重新读取整理预览' }).click()
    await job.getByText('已有同名文件，已跳过', { exact: true }).waitFor()
    await page.getByText('银色子弹：已完成', { exact: true }).waitFor()
    assert.equal(await rule.inputValue(), 'keep-my-unsaved-rule')
    assert.equal(calls.filter(c => c.endpoint.endsWith('/subscriptions/sub-a') && c.method === 'GET').length, configReads)
    assert.equal(calls.some(c => c.endpoint.endsWith('/redownload')), false)
    passed.push('冲突沿用原任务继续整理，已有跳过单独统计并显示；同一轮询同步缺集摘要，保留未保存规则且不重读配置或重新下载')
    await page.setViewportSize({ width: 320, height: 740 }); await job.scrollIntoViewIfNeeded()
    state.longPath = true; await job.getByRole('button', { name: '重新读取整理预览' }).click()
    await job.getByText(/UnbrokenRelease/).waitFor()
    assert.equal(await page.getByRole('dialog').evaluate(el => el.scrollWidth <= el.clientWidth), true)
    assert.equal(await job.evaluate(el => el.scrollWidth <= el.clientWidth), true)
    await page.screenshot({ path: 'test-results/anime-organization-job-320.png' })
    await page.evaluate(() => document.documentElement.classList.add('dark')); await page.screenshot({ path: 'test-results/anime-organization-job-dark.png' })
    await page.evaluate(() => document.documentElement.classList.remove('dark')); await page.setViewportSize({ width: 1440, height: 1000 })
    assert.equal(calls.some(c => /\/organization$|\/organize\/apply|\/rename|\/move/.test(c.endpoint)), false)
    assert.deepEqual(errors, []); console.log(JSON.stringify({ passed, apiCalls: calls.length, errors }, null, 2))
  } finally { await browser.close() }
})().catch(err => { console.error(err); process.exitCode = 1 })
