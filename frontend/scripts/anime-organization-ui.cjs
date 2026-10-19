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
  const now = Date.now(), state = { failPreview: false, loseDirectory: true, writes: 0, phase: 'pending', jobRevision: 1 }
  let settings = { revision: 3, accountId: 'main', accountRevision: 2, uid: '123', downloadCid: '100', downloadPath: '/下载', organizeCid: '900', organizePath: '/整理' }
  const naming = { schemaVersion: 1, target: { tmdbId: '335790', title: '黑暗召唤师在地下恋爱中', year: '2026', season: 1, groupId: '', groupName: 'TMDB 默认', fingerprint: 'a'.repeat(64), seasons: [{ number: 1, name: '第1季', episodeCount: 12, episodes: [{ id: '101', number: 1, name: '开始' }] }] }, sources: [{ sourceRef: 'source-a', segments: [{ first: 25, sourceBase: 25, targetSeason: 1, targetBase: 1 }], overrides: [] }] }
  const source = { id: 'source-a', role: 'main', provider: 'mikan', position: 0, configuredState: 'paused', revision: 1, runWhenEnabled: true, itemId: '300', groupId: '10', groupName: '银色子弹', binding: { schemaVersion: 1, confirmed: true, scope: { kind: 'feed' }, review: { method: 'source_review', itemId: '300', groupId: '10' } }, rules: { schemaVersion: 1, initialMode: 'from_episode', minimumEpisode: 25, include: [], exclude: [] } }
  const sub = { id: 'sub-a', entryId: 'entry-a', status: 'paused', revision: 2, firstEnabledAt: now, updatedAt: now, naming, entry: { id: 'entry-a', title: naming.target.title, editionKey: 'original', revision: 1, metadata: { schemaVersion: 1 } }, sources: [source], form: { formSelections: [], previewRefs: [] }, readiness: { ready: true, issues: [] }, permittedActions: ['resume', 'archive', 'remove'].map(name => ({ name, allowed: true })) }
  const plan = job => ({ mode: 'standard', revision: 2, example: !job, cloudConflictChecked: false, items: job ? [
    { id: '300', kind: 'video', originalPath: '/下载/anime-job/video.mkv', targetPath: '/旧整理/作品 (2026) {tmdb-335790}/Season 01/作品.S01E01.银色子弹.mkv', status: 'ready', reason: 'mapped' },
    { id: '301', kind: 'subtitle', originalPath: '/下载/anime-job/video.chs.ass', targetPath: '/旧整理/作品 (2026) {tmdb-335790}/Season 01/作品.S01E01.银色子弹.chs.ass', status: 'ready', reason: 'subtitle_matched', videoId: '300' },
    { id: '302', kind: 'video', originalPath: '/下载/anime-job/other.mkv', targetPath: '/旧整理/重复.mkv', status: 'conflict', reason: 'duplicate_target' },
    { id: '303', kind: 'attachment', originalPath: '/下载/anime-job/readme.txt', targetPath: '', status: 'preserved', reason: 'attachment_preserved' }
  ] : [{ id: 'source-a', kind: 'example', originalPath: '银色子弹', targetPath: `${settings.organizePath}/黑暗召唤师在地下恋爱中 (2026) {tmdb-335790}/Season 01/黑暗召唤师在地下恋爱中.S01E01.银色子弹`, status: 'ready', reason: 'mapped' }] })
  await page.route('**/api/**', async route => {
    const req = route.request(), url = new URL(req.url()), endpoint = url.pathname, body = req.postDataJSON(), key = req.headers()['idempotency-key']
    calls.push({ endpoint, body, key, method: req.method() }); let data = {}, status = 200
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
    else if (endpoint.endsWith('/subscriptions')) data = { items: [sub], total: 1, page: 1, pageSize: 20 }
    else if (endpoint.endsWith('/subscriptions/sub-a')) data = sub
    else if (endpoint.endsWith('/settings')) data = { settings, primaryAccount: { id: 'main', name: '主网盘', uid: '123', revision: 2 }, readiness: { ready: true, issues: [] } }
    else if (endpoint.endsWith('/subscriptions/sub-a/jobs')) data = { items: [{ id: 'job-a', title: '[银色子弹] 作品 - 25', groupName: '银色子弹', revision: state.jobRevision, downloadState: 'completed', fileState: 'verified', workflowState: state.phase === 'done' ? 'organized' : 'active', organizationPlanned: true, organization: { state: state.phase, total: 2, renamed: state.phase === 'pending' ? 0 : 2, moved: state.phase === 'done' ? 2 : state.phase === 'needs_review' ? 1 : 0, targetPath: '/旧整理/这是一个很长的目录用来确认手机上自然折行', updatedAt: now }, blockCode: state.phase === 'unknown' ? 'ORGANIZE_UNCERTAIN' : state.phase === 'needs_review' ? 'ORGANIZE_RESULT_UNRESOLVED' : null, executionPath: '/下载/anime-job', directoryCid: '200', files: [{ id: '300', kind: 'file', name: 'video.mkv', size: '100' }], updatedAt: now, observedAt: now, permittedActions: { reconcile: ['unknown', 'needs_review'].includes(state.phase), retry: false } }], total: 1, page: 1, pageSize: 20 }
    else if (endpoint.endsWith('/organization-preview')) {
      if (state.failPreview) { status = 503; data = { error: '模拟预览失败' } }
      else data = plan(endpoint.includes('/jobs/'))
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
    const detail = page.locator('.as-detail'), organization = page.locator('.as-organization')
    await organization.locator('summary').waitFor()
    assert.equal(calls.filter(c => c.endpoint.endsWith('/organization-preview')).length, 0)
    await organization.locator('summary').focus(); await page.keyboard.press('Enter')
    await organization.locator('dd').nth(1).filter({ hasText: '/整理' }).waitFor()
    assert.equal(await organization.getByRole('radio').count(), 0)
    assert.equal(await page.getByText('保存整理方式', { exact: true }).count(), 0)
    passed.push('默认标准命名整理，无处理方式选项，预览仅主动点击读取')
    await organization.getByRole('button', { name: '查看整理预览', exact: true }).click()
    await organization.getByText('映射示例，未包含实际文件扩展名。').waitFor()
    assert.ok((await organization.locator('.as-plan-item').innerText()).includes('S01E01.银色子弹'))
    assert.equal(calls.some(c => /cloud-download/.test(c.endpoint)), false)
    state.failPreview = true
    await organization.getByRole('button', { name: '重新读取整理预览' }).click()
    await organization.getByText(/模拟预览失败.*上次预览已保留/).waitFor()
    assert.equal(await organization.locator('.as-plan-item').count(), 1); state.failPreview = false
    passed.push('示例标明无实际扩展名，读取失败保留上次结果且不查询115')
    await organization.getByRole('button', { name: '选择整理目录' }).click()
    const picker = page.getByRole('dialog', { name: '动漫整理目录2' })
    await picker.getByRole('button', { name: '新整理', exact: true }).click()
    await picker.getByRole('button', { name: '保存整理目录', exact: true }).click()
    await picker.getByRole('button', { name: '核对原目录保存', exact: true }).waitFor()
    await picker.getByRole('button', { name: '核对原目录保存', exact: true }).click()
    await picker.waitFor({ state: 'detached' }); await organization.locator('dd').nth(1).filter({ hasText: '/新整理' }).waitFor()
    const saves = calls.filter(c => c.endpoint.endsWith('/settings/organize-directory'))
    assert.equal(saves.length, 2); assert.equal(saves[0].key, saves[1].key); assert.deepEqual(saves[0].body, saves[1].body); assert.equal(state.writes, 1)
    passed.push('目录保存回执丢失时在选择器核对原PATCH，不替换key、版本或目标')
    await organization.getByRole('button', { name: '查看整理预览', exact: true }).click()
    await organization.getByText(/新整理\/黑暗召唤师/).waitFor()
    passed.push('整理目录保存后旧预览失效，新预览显示新目标')
    await page.locator('[data-job-id="job-a"] > button').click()
    const job = page.locator('[data-job-id="job-a"]')
    await job.getByRole('button', { name: '查看整理预览', exact: true }).click()
    await job.getByText('字幕已配对', { exact: true }).waitFor(); await job.getByText('目标路径重复，不覆盖', { exact: true }).waitFor(); await job.getByText('附件保留原处', { exact: true }).waitFor()
    assert.ok((await job.locator('.as-plan-item').first().innerText()).includes('/旧整理/'))
    passed.push('实际任务保持冻结目录，展示视频、字幕、冲突与附件，不追踪当前位置')
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
    await job.getByText('已改名 2/2，已移动 2/2', { exact: false }).waitFor()
    passed.push('真实整理进度区分工作、未知、部分成功与完成，未知仅核对，完成无执行按钮且保留预览DOM')
    await page.setViewportSize({ width: 320, height: 740 }); await job.scrollIntoViewIfNeeded()
    assert.equal(await job.evaluate(el => el.scrollWidth <= el.clientWidth), true)
    await page.screenshot({ path: 'test-results/anime-organization-job-320.png' })
    await page.evaluate(() => document.documentElement.classList.add('dark')); await page.screenshot({ path: 'test-results/anime-organization-job-dark.png' })
    await page.evaluate(() => document.documentElement.classList.remove('dark')); await page.setViewportSize({ width: 1440, height: 1000 })
    await organization.scrollIntoViewIfNeeded(); await page.screenshot({ path: 'test-results/anime-organization-desktop.png' })
    await page.setViewportSize({ width: 320, height: 740 }); await organization.scrollIntoViewIfNeeded(); await page.screenshot({ path: 'test-results/anime-organization-320.png' })
    assert.equal(await organization.evaluate(el => el.scrollWidth <= el.clientWidth), true)
    await page.evaluate(() => document.documentElement.classList.add('dark')); await page.screenshot({ path: 'test-results/anime-organization-dark.png' })
    passed.push('320px和暗色目录与路径自然折行，无横向溢出')
    await page.goto(origin + '/anime/subscriptions/sub-a/edit', { waitUntil: 'networkidle' })
    await page.getByRole('region', { name: 'TMDB 命名目标' }).waitFor()
    assert.equal(await page.getByText('下载后的处理方式', { exact: true }).count(), 0)
    assert.equal(await page.locator('.ae-organize-modes').count(), 0)
    await page.getByText('整理目录2', { exact: false }).first().waitFor()
    assert.equal(await page.locator('.anime-editor').evaluate(el => el.scrollWidth <= el.clientWidth), true)
    await page.screenshot({ path: 'test-results/anime-organization-editor-320.png' })
    passed.push('编辑页直接配置下载目录1与整理目录2，无多余处理方式单选项')
    assert.equal(calls.some(c => /\/organization$|\/organize\/apply|\/rename|\/move/.test(c.endpoint)), false)
    assert.deepEqual(errors, []); console.log(JSON.stringify({ passed, apiCalls: calls.length, errors }, null, 2))
  } finally { await browser.close() }
})().catch(err => { console.error(err); process.exitCode = 1 })
