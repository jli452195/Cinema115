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
  const state = { rules: [], records: [] }
  let emptyScan = false, rejectRuleSave = false, delayedState
  await page.route('**/api/**', async route => {
    const req = route.request(), u = new URL(req.url()), endpoint = u.pathname, method = req.method(), body = req.postDataJSON()
    calls.push({ endpoint, method, body, query: u.searchParams.toString() }); let data = {}, status = 200
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
    else if (endpoint === '/api/admin/media-organize') {
      const pages = Math.max(1, Math.ceil(state.records.length / 20)), page = Math.max(1, Math.min(Number(u.searchParams.get('page') || 1), pages))
      data = { rules: copy(state.rules), active: ['queued', 'scanning', 'running'].includes(run.status), history: { items: copy(state.records.slice((page - 1) * 20, page * 20)), page, pages, total: state.records.length } }
      if (delayedState) {
        const held = delayedState; delayedState = undefined
        held.started(); await held.release
        if (held.reject) { status = 503; data = { error: '过期读取失败' } }
      }
    }
    else if (endpoint === '/api/admin/media-organize/rules' && method === 'POST') {
      assert.equal('scope' in body, false)
      if (rejectRuleSave) { rejectRuleSave = false; status = 503; data = { error: '模拟保存失败' } }
      else { Object.assign(rule, body, { id: 'rule1' }); state.rules = [copy(rule)]; data = rule }
    }
    else if (endpoint === '/api/admin/media-organize/rules/rule1' && method === 'DELETE') { state.rules = []; state.records = []; data = { ok: true } }
    else if (endpoint.endsWith('/scan')) { data = emptyScan ? { ...copy(run), id: 'empty', status: 'queued', entries: [], groups: [] } : run }
    else if (endpoint === '/api/admin/media-organize/runs/empty') data = { ...copy(run), id: 'empty', status: 'completed', entries: [], groups: [], message: '没有新的可处理文件' }
    else if (endpoint === '/api/admin/media-organize/runs/run1') data = run
    else if (endpoint.endsWith('/preview')) { run.entries[0].file.episode = body.files[0].episode; run.entries[0].relative = run.entries[0].relative.replace('S01E01.strm', `S01E${String(body.files[0].episode).padStart(2, '0')}.strm`); data = run }
    else if (endpoint.endsWith('/apply') && run.failureCount) { run.status = 'partial'; run.failureCount = 0; run.entries[0].failure = { state: 'stored', reason: '请选择对应作品' }; data = run }
    else if (endpoint.endsWith('/apply')) {
      run.status = 'completed'; run.readyCount = 0; run.entries[0].status = 'done'; data = run
      const e = run.entries[0]
      state.records = [{ runId: run.id, fileId: e.file.id, name: e.file.name, title: '示例作品', kind: 'tv', mediaType: 'video', season: e.file.season, episode: e.file.episode, source: `${rule.source}/${e.file.sourcePath}`, target: `${rule.target}/${e.relative}`, status: 'done', at: run.updatedAt }]
    }
    else if (endpoint === '/api/admin/tmdb/browse') { assert.equal(u.searchParams.get('kind'), 'multi'); assert.equal(u.searchParams.get('query'), 'Example'); data = { items: [{ id: 42, kind: 'tv', title: '示例作品', date: '2024-01-01' }] } }
    else if (endpoint === '/api/admin/p115/accounts') data = { accounts: [{ id: 'primary1', primary: true }, { id: 'secondary', primary: false }] }
    else if (endpoint === '/api/admin/p115/cloud-download/paths') { assert.equal(u.searchParams.get('accountId'), 'primary1'); data = { directories: [{ id: '10', name: 'inbox' }], hasMore: false } }
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
  })
  async function holdRefresh(reject = false) {
    let started, release
    const ready = new Promise(resolve => { started = resolve })
    delayedState = { started, release: new Promise(resolve => { release = resolve }), reject }
    await page.getByRole('button', { name: '刷新记录', exact: true }).click()
    await ready
    return release
  }
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
    assert.equal(await page.getByRole('region', { name: '待处理任务', exact: true }).count(), 0)
    await page.getByRole('button', { name: '扫描预览', exact: true }).click()
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
    await editor.getByText('启用监控', { exact: true }).click()
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
    await preview.getByRole('button', { name: '关闭整理预览', exact: true }).click()
    state.records.unshift({ runId: run.id, fileId: 'cleanup', name: 'ads.zip', title: '', kind: '', mediaType: 'cleanup', season: 0, episode: 0, source: '/media/inbox/ads.zip', status: 'done', message: '已从来源删除无关文件', at: run.updatedAt })
    await page.getByRole('button', { name: '刷新记录', exact: true }).click()
    const cleaned = history.getByRole('button', { name: '查看ads.zip的整理记录', exact: true })
    await cleaned.getByText('已删除', { exact: true }).waitFor()
    assert.equal(await cleaned.getByText('无关文件', { exact: true }).count(), 1)
    assert.equal(await cleaned.locator('.mo-red, .mo-episode').count(), 0)
    assert.equal(await cleaned.locator('.mo-result-path').count(), 1)
    const scansBeforeToggle = calls.filter(call => call.endpoint.endsWith('/scan')).length
    await page.getByRole('button', { name: '启用NAS 验收的目录监控', exact: true }).click()
    await page.getByText('定时监控 · 每 5 分钟', { exact: true }).waitFor()
    const enabledRequest = calls.filter(call => call.endpoint.endsWith('/rules') && call.method === 'POST').at(-1)
    assert.equal(enabledRequest.body.enabled, true)
    assert.equal(enabledRequest.body.failure, rule.failure)
    assert.equal(enabledRequest.body.source, rule.source)
    rejectRuleSave = true
    await page.getByRole('button', { name: '暂停NAS 验收的目录监控', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '模拟保存失败' }).waitFor()
    assert.equal(await page.getByRole('button', { name: '暂停NAS 验收的目录监控', exact: true }).count(), 1)
    await page.getByRole('button', { name: '暂停NAS 验收的目录监控', exact: true }).click()
    await page.getByText('监控已暂停', { exact: true }).waitFor()
    assert.equal(calls.filter(call => call.endpoint.endsWith('/scan')).length, scansBeforeToggle)
    const releaseSave = await holdRefresh()
    await page.getByRole('button', { name: '编辑NAS 验收', exact: true }).click()
    await editor.getByRole('textbox', { name: '规则名称', exact: true }).fill('NAS 更新')
    await editor.getByRole('button', { name: '保存规则', exact: true }).click()
    await editor.waitFor({ state: 'hidden' })
    releaseSave()
    await page.getByRole('heading', { name: 'NAS 更新', exact: true }).waitFor()
    const releaseToggle = await holdRefresh(true)
    await page.getByRole('button', { name: '启用NAS 更新的目录监控', exact: true }).click()
    await page.getByRole('button', { name: '暂停NAS 更新的目录监控', exact: true }).waitFor()
    releaseToggle()
    await page.waitForResponse(response => response.url().includes('/api/admin/media-organize?page=') && response.status() === 200)
    assert.equal(await page.getByRole('alert').count(), 0)
    const recordsBeforeDelete = copy(state.records)
    const releaseDelete = await holdRefresh()
    await page.getByRole('button', { name: '删除NAS 更新', exact: true }).click()
    const deletion = page.getByRole('dialog', { name: '移除记录', exact: true })
    await deletion.getByRole('button', { name: '确认移除', exact: true }).click()
    await deletion.waitFor({ state: 'hidden' })
    releaseDelete()
    await page.getByText('添加一个来源目录，先扫描预览，再启用监控。', { exact: true }).waitFor()
    assert.equal(await history.count(), 0)
    state.records = recordsBeforeDelete
    const longPath = '/Cinema115/' + '用于验证目录换行和省略的较长文件夹名称/'.repeat(8)
    state.rules = [
      { ...copy(rule), source: longPath + '待整理', target: longPath + '已整理', failure: longPath + '整理失败' },
      { ...copy(rule), id: 'cloud', name: '115 下载整理', storage: '115', source: '/Cinema115/云下载', target: '/Cinema115/媒体库', failure: '', enabled: true, intervalMinutes: 10, classify: false },
      { ...copy(rule), id: 'strm', name: '名称很长时也不会撑开卡片的 STRM 自动整理规则'.repeat(2), source: '/strm/inbox', target: '/strm/library', failure: '/strm/failed' },
    ]
    await page.setViewportSize({ width: 1680, height: 1080 })
    await page.evaluate(() => document.documentElement.classList.remove('dark'))
    await page.reload({ waitUntil: 'networkidle' })
    const ruleCards = page.locator('.mo-rule-card')
    await ruleCards.nth(2).waitFor()
    const desktopCards = await ruleCards.evaluateAll(cards => cards.map(card => ({ x: card.getBoundingClientRect().x, y: card.getBoundingClientRect().y, width: card.getBoundingClientRect().width, height: card.getBoundingClientRect().height, footer: card.querySelector('.mo-rule-actions').getBoundingClientRect().y })))
    assert.equal(new Set(desktopCards.map(card => Math.round(card.y))).size, 1)
    assert.equal(new Set(desktopCards.map(card => Math.round(card.footer))).size, 1)
    assert.ok(desktopCards.every(card => card.width >= 320 && card.height < 420), JSON.stringify(desktopCards))
    assert.equal(await ruleCards.first().getByTitle(state.rules[0].source, { exact: true }).count(), 1)
    assert.equal(await ruleCards.nth(1).getByText('未设置，识别失败的视频留在来源', { exact: true }).count(), 1)
    await ruleCards.first().hover()
    assert.ok((await ruleCards.first().evaluate(el => getComputedStyle(el, '::before').animationName)).includes('ambient-glow'))
    await page.emulateMedia({ reducedMotion: 'reduce' })
    assert.equal(await ruleCards.first().evaluate(el => getComputedStyle(el, '::before').animationName), 'none')
    await page.screenshot({ path: 'test-results/media-organize-rules-desktop.png', fullPage: true })
    await page.setViewportSize({ width: 390, height: 844 })
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1), false)
    assert.equal(await ruleCards.first().locator('h3').evaluate(el => getComputedStyle(el).color), 'rgb(241, 245, 249)')
    assert.ok((await ruleCards.first().getByRole('button', { name: '扫描预览', exact: true }).boundingBox()).height >= 44)
    await page.screenshot({ path: 'test-results/media-organize-rules-mobile-dark.png', fullPage: true })
    assert.deepEqual(errors, [])
    console.log('PASS: rule cards, monitoring toggle and failed saves, stale reads after save/toggle/delete, three-column and mobile layouts, reduced motion; history, previews, STRM corrections, TMDB search, primary-only picker')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
