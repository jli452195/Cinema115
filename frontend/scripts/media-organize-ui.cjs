// Isolated browser fixture: all APIs mocked, no NAS or 115 writes.
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const copy = value => JSON.parse(JSON.stringify(value))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
  const page = await context.newPage(), calls = [], errors = []
  page.on('pageerror', error => errors.push(error.message)); page.setDefaultTimeout(10000)
  const rule = { id: 'rule1', name: 'NAS 验收', storage: 'local', source: '/media/inbox', target: '/media/library', failure: '/media/failed', classify: true, enabled: false, intervalMinutes: 5 }
  const run = { id: 'run1', rule, status: 'preview', readyCount: 1, failureCount: 0, createdAt: new Date().toISOString(), updatedAt: new Date().toISOString(), message: '核对预览', groups: [{ id: '1', identity: { name: 'Example' }, kind: 'tv', tmdbId: 42, title: '示例作品', category: '剧集/动漫' }], entries: [{ file: { id: 'file1', name: 'Example.S01E01.strm', sourcePath: 'Example.S01E01.strm', mediaType: 'video', season: 1, episode: 1 }, group: '1', relative: '剧集/动漫/示例作品 (2024) {tmdb-42}/Season 01/Example.S01E01.strm', status: 'ready' }] }
  const state = { rules: [], runs: [], records: [] }
  let emptyScan = false
  await page.route('**/api/**', async route => {
    const req = route.request(), u = new URL(req.url()), endpoint = u.pathname, method = req.method(), body = req.postDataJSON()
    calls.push({ endpoint, method, body, query: u.searchParams.toString() }); let data = {}
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
    else if (endpoint === '/api/admin/media-organize') {
      const pages = Math.max(1, Math.ceil(state.records.length / 20)), page = Math.max(1, Math.min(Number(u.searchParams.get('page') || 1), pages))
      data = { rules: copy(state.rules), runs: state.runs.map(r => ({ ...copy(r), entries: null, groups: null, entryCount: r.entries.length, pendingCount: r.entries.filter(e => e.status !== 'done').length })), history: { items: copy(state.records.slice((page - 1) * 20, page * 20)), page, pages, total: state.records.length } }
    }
    else if (endpoint === '/api/admin/media-organize/rules' && method === 'POST') { assert.equal('scope' in body, false); Object.assign(rule, body, { id: 'rule1' }); state.rules = [copy(rule)]; data = rule }
    else if (endpoint.endsWith('/scan')) { state.runs = emptyScan ? [] : [copy(run)]; data = emptyScan ? { ...copy(run), id: 'empty', status: 'queued', entries: [], groups: [] } : run }
    else if (endpoint === '/api/admin/media-organize/runs/empty') data = { ...copy(run), id: 'empty', status: 'completed', entries: [], groups: [], message: '没有新的可处理文件' }
    else if (endpoint === '/api/admin/media-organize/runs/run1') data = run
    else if (endpoint.endsWith('/preview')) { run.entries[0].file.episode = body.files[0].episode; run.entries[0].relative = run.entries[0].relative.replace('S01E01.strm', `S01E${String(body.files[0].episode).padStart(2, '0')}.strm`); data = run }
    else if (endpoint.endsWith('/apply') && run.failureCount) { run.status = 'partial'; run.failureCount = 0; run.entries[0].failure = { state: 'stored', reason: '请选择对应作品' }; state.runs = [copy(run)]; data = run }
    else if (endpoint.endsWith('/apply')) {
      run.status = 'completed'; run.readyCount = 0; run.entries[0].status = 'done'; state.runs = []; data = run
      const e = run.entries[0]
      state.records = [{ runId: run.id, fileId: e.file.id, name: e.file.name, title: '示例作品', kind: 'tv', mediaType: 'video', season: e.file.season, episode: e.file.episode, source: `${rule.source}/${e.file.sourcePath}`, target: `${rule.target}/${e.relative}`, status: 'done', at: run.updatedAt }]
    }
    else if (endpoint === '/api/admin/tmdb/browse') { assert.equal(u.searchParams.get('kind'), 'multi'); assert.equal(u.searchParams.get('query'), 'Example'); data = { items: [{ id: 42, kind: 'tv', title: '示例作品', date: '2024-01-01' }] } }
    else if (endpoint === '/api/admin/p115/accounts') data = { accounts: [{ id: 'primary1', primary: true }, { id: 'secondary', primary: false }] }
    else if (endpoint === '/api/admin/p115/cloud-download/paths') { assert.equal(u.searchParams.get('accountId'), 'primary1'); data = { directories: [{ id: '10', name: 'inbox' }], hasMore: false } }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
  })
  try {
    await fs.mkdir('test-results', { recursive: true })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/media/organize', { waitUntil: 'networkidle' })
    await page.getByRole('heading', { name: '自动整理', exact: true }).waitFor()
    assert.equal(await page.getByRole('region', { name: '整理记录', exact: true }).count(), 0)
    assert.equal(await page.getByRole('region', { name: '待处理任务', exact: true }).count(), 0)
    await page.getByRole('button', { name: '新增规则' }).click()
    const editor = page.getByRole('dialog', { name: '整理规则', exact: true })
    assert.equal(await editor.getByText('文件范围', { exact: true }).count(), 0)
    await editor.getByRole('textbox', { name: '规则名称' }).fill(rule.name)
    await editor.getByRole('textbox', { name: '来源目录' }).fill(rule.source)
    await editor.getByRole('textbox', { name: '目标目录' }).fill(rule.target)
    await editor.getByRole('textbox', { name: '整理失败目录', exact: true }).fill('/media/temp-failed')
    await editor.getByRole('button', { name: '清空整理失败目录', exact: true }).click()
    assert.equal(await editor.getByRole('textbox', { name: '整理失败目录', exact: true }).inputValue(), '')
    await editor.getByRole('textbox', { name: '整理失败目录', exact: true }).fill(rule.failure)
    assert.equal(calls.some(call => call.endpoint.startsWith('/api/admin/p115')), false)
    await editor.getByRole('button', { name: '保存规则', exact: true }).click()
    assert.equal(calls.find(call => call.endpoint.endsWith('/rules') && call.method === 'POST').body.failure, rule.failure)
    await page.getByRole('button', { name: '扫描预览', exact: true }).click()
    const preview = page.getByRole('dialog', { name: '整理预览与记录', exact: true })
    await preview.getByText('Example.S01E01.strm', { exact: true }).waitFor()
    assert.equal(await page.getByRole('region', { name: '整理记录', exact: true }).count(), 0)
    await preview.getByRole('button', { name: '关闭整理预览', exact: true }).click()
    await page.locator('.mo-pending-open').click()
    await preview.getByText('Example.S01E01.strm', { exact: true }).waitFor()
    await preview.getByRole('spinbutton', { name: '集', exact: true }).fill('13')
    assert.equal(await preview.getByRole('button', { name: '确认整理并移动', exact: true }).isDisabled(), true)
    await preview.getByRole('button', { name: '更新预览', exact: true }).click()
    await preview.locator('.mo-target').filter({ hasText: 'S01E13.strm' }).waitFor()
    await preview.getByRole('button', { name: '更换作品', exact: true }).click()
    await preview.getByRole('button', { name: '搜索作品', exact: true }).click()
    await preview.getByRole('button', { name: /示例作品.*2024/ }).click()
    assert.equal(await preview.getByRole('button', { name: '确认整理并移动', exact: true }).isDisabled(), true)
    await preview.getByRole('button', { name: '更新预览', exact: true }).click()
    await preview.getByRole('button', { name: '确认整理并移动', exact: true }).click()
    assert.equal(await preview.getByRole('button', { name: '更换作品', exact: true }).isDisabled(), true)
    assert.ok((await preview.boundingBox()).height < 600, await preview.evaluate(el => JSON.stringify({ classes: el.className, height: getComputedStyle(el).height, rules: [...document.styleSheets].flatMap(s => [...s.cssRules]).filter(r => r.selectorText && el.matches(r.selectorText) && r.style?.height).map(r => r.cssText), bodyHeight: getComputedStyle(el.querySelector('.anime-floating-body')).height })))
    await page.screenshot({ path: 'test-results/media-organize-desktop.png' })
    await preview.getByRole('button', { name: '关闭整理预览', exact: true }).click()
    const history = page.getByRole('region', { name: '整理记录', exact: true })
    await history.getByText('S01E13', { exact: true }).waitFor()
    assert.equal(await page.getByRole('region', { name: '待处理任务', exact: true }).count(), 0)
    await history.getByText('成功', { exact: true }).waitFor()
    await history.getByTitle(`${rule.source}/Example.S01E01.strm`, { exact: true }).waitFor()
    await history.getByTitle(`${rule.target}/${run.entries[0].relative}`, { exact: true }).waitFor()
    assert.equal(await history.locator('.mo-result-path code').last().textContent(), 'Example.S01E13.strm')
    await history.getByRole('button', { name: /查看.*整理记录/ }).click()
    await preview.getByRole('button', { name: '关闭整理预览', exact: true }).click()
    state.records[0].title = '用于验证较长片名不会撑高卡片的示例作品名称'.repeat(3)
    state.records[0].source = `/media/inbox/${'很长的目录名称/'.repeat(10)}Example.S01E01.strm`
    state.records[0].target = `/media/library/${'很长的目标目录/'.repeat(10)}Example.S01E13.strm`
    state.records.unshift({ ...copy(state.records[0]), fileId: 'failed', name: 'Example.S01E14.strm', episode: 14, status: 'error', message: '来源文件已变化，请重新扫描', source: '/media/inbox/Example.S01E14.strm', target: '/media/library/Example.S01E14.strm' })
    await page.getByRole('button', { name: '刷新记录', exact: true }).click()
    await history.getByText('来源文件已变化，请重新扫描', { exact: true }).waitFor()
    assert.equal(await history.locator('.mo-result').evaluateAll(cards => cards.every(card => card.getBoundingClientRect().height < 190)), true)
    assert.equal(await history.locator('.mo-result-path code').last().getAttribute('title'), state.records[1].target)
    await page.screenshot({ path: 'test-results/media-organize-history-desktop.png' })
    await page.getByRole('button', { name: '编辑NAS 验收' }).click()
    await editor.getByRole('combobox', { name: '存储位置' }).click()
    await page.getByRole('option', { name: '115 主账号', exact: true }).click()
    await editor.getByRole('button', { name: '选择来源目录' }).click()
    const picker = page.getByRole('dialog', { name: '选择来源目录', exact: true })
    await picker.getByText('inbox', { exact: true }).waitFor()
    await picker.getByRole('button', { name: '关闭' }).click()
    await editor.getByRole('button', { name: '选择整理失败目录', exact: true }).click()
    const failurePicker = page.getByRole('dialog', { name: '选择整理失败目录', exact: true })
    await failurePicker.getByText('inbox', { exact: true }).waitFor()
    await failurePicker.getByRole('button', { name: '关闭' }).click()
    await editor.getByRole('button', { name: '取消', exact: true }).click()
    emptyScan = true
    await page.getByRole('button', { name: '扫描预览', exact: true }).click()
    await page.getByText('没有新的可处理文件', { exact: true }).waitFor()
    assert.equal(await preview.count(), 0)
    assert.equal(await page.getByRole('region', { name: '待处理任务', exact: true }).count(), 0)
    assert.equal(await history.locator('.mo-result').count(), 2)
    const actual = copy(state.records)
    state.records = Array.from({ length: 45 }, (_, i) => ({ ...copy(actual[0]), fileId: `fixture-${i}`, name: `Example.S01E${i + 1}.strm`, episode: i + 1 }))
    await page.getByRole('button', { name: '刷新记录', exact: true }).click()
    const pagination = page.getByRole('navigation', { name: '整理记录分页', exact: true })
    await pagination.getByRole('button', { name: '下一页', exact: true }).click()
    await history.getByText('S01E21', { exact: true }).waitFor()
    assert.equal(await history.locator('.mo-result').count(), 20)
    await pagination.getByRole('button', { name: '下一页', exact: true }).click()
    await history.getByText('S01E41', { exact: true }).waitFor()
    assert.equal(await history.locator('.mo-result').count(), 5)
    assert.equal(await pagination.getByRole('button', { name: '下一页', exact: true }).isDisabled(), true)
    assert.ok(calls.some(call => call.endpoint === '/api/admin/media-organize' && call.query === 'page=3'))
    state.records = actual
    await page.getByRole('button', { name: '刷新记录', exact: true }).click()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await history.getByText('S01E13', { exact: true }).waitFor()
    assert.equal(await history.locator('.mo-result-heading').first().evaluate(el => getComputedStyle(el).color), 'rgb(226, 232, 240)')
    assert.equal(await page.getByRole('heading', { name: '整理规则', exact: true }).evaluate(el => getComputedStyle(el).color), 'rgb(226, 232, 240)')
    assert.equal(await history.locator('.mo-result').evaluateAll(cards => cards.every(card => card.getBoundingClientRect().height < 190)), true)
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1), false)
    await page.screenshot({ path: 'test-results/media-organize-history-mobile-dark.png', fullPage: true })
    await page.getByRole('button', { name: '新增规则' }).click()
    await editor.getByText('自动扫描并整理', { exact: true }).click()
    await editor.getByRole('spinbutton', { name: '扫描间隔（分钟）', exact: true }).waitFor()
    assert.equal(await editor.getByRole('textbox', { name: '规则名称', exact: true }).evaluate(el => getComputedStyle(el).backgroundColor), 'rgb(30, 41, 59)')
    await page.waitForTimeout(200)
    const overflow = await page.evaluate(() => [...document.querySelectorAll('dialog[open]')].some(el => el.scrollWidth > el.clientWidth + 1))
    assert.equal(overflow, false)
    await page.screenshot({ path: 'test-results/media-organize-mobile-dark.png' })
    await editor.getByRole('button', { name: '取消', exact: true }).click()
    await history.getByRole('button', { name: /查看Example.S01E01.*整理记录/ }).click()
    await preview.getByText('Example.S01E01.strm', { exact: true }).waitFor()
    assert.equal(await preview.evaluate(el => el.scrollWidth > el.clientWidth + 1), false)
    assert.ok((await preview.boundingBox()).height <= 844)
    await page.screenshot({ path: 'test-results/media-organize-preview-mobile-dark.png' })
    await preview.getByRole('button', { name: '关闭整理预览', exact: true }).click()
    run.status = 'preview'; run.readyCount = 0; run.failureCount = 1; run.entries[0].status = 'needs_match'; run.entries[0].message = '请选择对应作品'
    run.groups[0].tmdbId = 0
    await history.getByRole('button', { name: /查看Example.S01E01.*整理记录/ }).click()
    assert.equal(await preview.getByRole('button', { name: '移入失败目录', exact: true }).count(), 0)
    assert.equal(await preview.getByRole('button', { name: '确认整理并移动', exact: true }).isDisabled(), true)
    await preview.getByRole('button', { name: '关闭整理预览', exact: true }).click()
    run.status = 'partial'
    await history.getByRole('button', { name: /查看Example.S01E01.*整理记录/ }).click()
    await preview.getByRole('button', { name: '继续整理', exact: true }).click()
    await preview.getByText(/已移入失败目录.*在此修正匹配/).waitFor()
    await preview.getByText(`${rule.failure}/${run.entries[0].file.sourcePath}`, { exact: true }).waitFor()
    await page.screenshot({ path: 'test-results/media-organize-quarantined-mobile-dark.png' })
    assert.deepEqual(errors, [])
    console.log('PASS: conditional per-file history, pending previews, empty scans, failure reasons, pagination, reopening; STRM corrections, TMDB search, primary-only picker, mobile dark without overflow')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
