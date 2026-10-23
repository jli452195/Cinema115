// All network APIs are mocked. This test never submits a real 115 download.
const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const calls = [], errors = [], passed = []
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light') })
    const page = await context.newPage(); page.setDefaultTimeout(8000)
    page.on('pageerror', e => errors.push(e.message))
    let loseResponse = true, conflict = false, holdJob = false, releaseJob
    const files = [
      { id: 'show/E027-E28.mkv', name: 'E027-E28.mkv', sourcePath: '/show/E027-E28.mkv', episode: 27 },
      { id: 'show/E030.mkv', name: 'E030.mkv', sourcePath: '/show/E030.mkv', episode: 30 },
      { id: 'show/OVA.mkv', name: 'OVA.mkv', sourcePath: '/show/OVA.mkv', episode: 0 },
    ].map(f => ({ ...f, directory: false, parentId: '', size: 0, mediaType: 'video', season: 1, suffix: '' }))
    const resource = n => ({ provider: 'dmhy', providerId: String(n), title: `测试合集 ${n} [1080p]`, size: 1024 ** 3, createdAt: '2026-10-23T00:00:00Z', magnet: `magnet:?xt=urn:btih:${n.toString(16).padStart(40, '0')}`, publisher: { id: 2, name: '测试组' } })
    await page.route('**/api/**', async route => {
      const req = route.request(), endpoint = new URL(req.url()).pathname, body = req.postDataJSON()
      calls.push({ endpoint, body }); let status = 200, data = {}
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock', username: 'admin', role: 'admin', disabled: false } }
      else if (endpoint.endsWith('/accounts')) data = { accounts: [] }
      else if (endpoint.includes('/tmdb/detail/')) data = { id: 12501, kind: 'tv', title: '百变之星', originalTitle: 'カレイドスター', overview: '', posterUrl: '', backdropUrl: '', date: '2003-04-03', rating: 7, votes: 1, genres: [{ id: 16, name: '动画' }], countries: ['JP'], cast: [], seasons: [{ number: 1, name: '第 1 季', episodeCount: 26 }, { number: 2, name: '第 2 季', episodeCount: 25 }], collection: null }
      else if (endpoint.endsWith('/resources/garden/search')) data = { items: Array.from({ length: 15 }, (_, i) => resource(i + 1)), groups: [], query: body.query, page: 1, hasMore: false, cached: false }
      else if (endpoint.endsWith('/garden-organize/files')) data = body.providerId === '2' ? { files: [], endEpisodes: {}, reason: '清单中没有可整理的视频文件，可使用普通离线（不支持 ISO）' } : { files, endEpisodes: { [files[0].id]: 28 } }
      else if (endpoint.endsWith('/cloud-download/config')) data = { accountId: 'primary', targetCID: '10', targetPath: '/下载' }
      else if (endpoint.endsWith('/manual-organize/config')) data = { accountId: 'primary', targetCID: '20', targetPath: '/动漫' }
      else if (/manual-organize\/(jobs|cleanup|subtitles)$/.test(endpoint)) data = { jobs: [] }
      else if (endpoint.endsWith('/cloud-download/paths')) data = { directories: [{ id: '30', name: '自选目录' }], hasMore: false }
      else if (endpoint.endsWith('/garden-organize/preview')) data = { id: 'one-plan', title: '百变之星', status: 'preview', items: body.files.map(f => ({ file: files.find(x => x.id === f.id), name: 'mapped.mkv', conflict: conflict ? '目标同名' : '' })) }
      else if (endpoint.endsWith('/garden-organize/jobs')) {
        if (holdJob) await new Promise(resolve => { releaseJob = resolve })
        if (loseResponse) { loseResponse = false; status = 502; data = { error: '模拟回执丢失' } }
        else data = { id: 'one-plan', title: '百变之星', status: 'queued', items: [{ file: files[0] }, { file: files[1] }] }
      } else if (endpoint.endsWith('/cloud-download/tasks')) data = { accepted: true }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const count = suffix => calls.filter(c => c.endpoint.endsWith(suffix)).length
    await page.goto(`${origin}/tv/12501`)
    await page.getByRole('button', { name: 'Garden 搜索', exact: true }).click()
    const list = page.getByRole('list', { name: 'Garden资源列表' })
    await list.waitFor()
    assert.equal(count('/garden-organize/files'), 0)
    const row = list.locator(':scope > li').first()
    await row.locator('summary').click()
    await row.getByRole('button', { name: '下载并整理', exact: true }).click()
    const panel = page.getByRole('region', { name: '下载并整理映射' })
    const organizeDialog = page.locator('dialog.anime-floating-organize')
    const searchDialog = page.locator('dialog.mikan-dialog')
    await panel.waitFor()
    assert.equal(await page.locator('dialog[open]').count(), 2)
    assert.equal(await list.isVisible(), true)
    assert.equal(await searchDialog.locator('.mikan-search-form').isVisible(), true)
    for (let i = 0; i < 6; i++) {
      await page.keyboard.press('Tab')
      assert.equal(await organizeDialog.evaluate(el => el.contains(document.activeElement)), true)
    }
    assert.equal(count('/garden-organize/files'), 1)
    assert.equal(count('/garden-organize/jobs'), 0)
    await page.getByRole('button', { name: '下载并自动整理', exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: '下载并自动整理', exact: true }).isDisabled(), true)
    const first = page.locator('[data-organize-file]').nth(0), second = page.locator('[data-organize-file]').nth(1)
    await first.getByRole('checkbox').click(); await second.getByRole('checkbox').click()
    await page.getByRole('combobox', { name: '目标季', exact: true }).click()
    await page.getByRole('option').filter({ hasText: '第 2 季' }).click()
    await page.getByRole('spinbutton', { name: '发布起始集', exact: true }).fill('27')
    await page.getByRole('button', { name: '应用偏移（2 项）', exact: true }).click()
    assert.equal(await first.getByRole('spinbutton', { name: 'E027-E28.mkv 集', exact: true }).inputValue(), '1')
    assert.equal(await first.getByRole('spinbutton', { name: 'E027-E28.mkv 结束集', exact: true }).inputValue(), '2')
    assert.equal(await second.getByRole('spinbutton', { name: 'E030.mkv 集', exact: true }).inputValue(), '4')
    await first.getByText('整理后：百变之星 - S02E01-E02 - 测试组.mkv', { exact: true }).waitFor()
    if (process.env.CINEMA_UI_SHOTS) { await fs.mkdir(process.env.CINEMA_UI_SHOTS, { recursive: true }); await page.screenshot({ path: path.join(process.env.CINEMA_UI_SHOTS, 'garden-organize-desktop.png') }) }
    await page.getByRole('button', { name: '撤销', exact: true }).click()
    assert.equal(await first.getByRole('spinbutton', { name: 'E027-E28.mkv 集', exact: true }).inputValue(), '27')
    await page.getByRole('button', { name: '应用偏移（2 项）', exact: true }).click()
    assert.equal(await page.locator('[data-organize-file]').nth(2).getByRole('checkbox').isChecked(), false)
    passed.push('lazy metadata, no downloads during preparation, range/gap offset and undo, specials unselected')
    await page.getByRole('button', { name: '选择目录', exact: true }).click()
    await page.getByRole('dialog', { name: '选择本次整理目录', exact: true }).waitFor()
    assert.equal(await page.locator('dialog[open]').count(), 3)
    await page.keyboard.press('Escape')
    await page.getByRole('dialog', { name: '选择本次整理目录', exact: true }).waitFor({ state: 'detached' })
    assert.equal(await page.locator('dialog[open]').count(), 2)
    assert.equal(await page.getByRole('button', { name: '选择目录', exact: true }).evaluate(el => el === document.activeElement), true)
    assert.equal(await page.evaluate(() => document.body.style.overflow), 'hidden')
    await page.getByRole('button', { name: '选择目录', exact: true }).click()
    await page.getByRole('button', { name: '选择此目录', exact: true }).click()
    conflict = true
    await page.getByRole('button', { name: '下载并自动整理', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '命名或目标目录存在冲突' }).waitFor()
    assert.equal(count('/garden-organize/jobs'), 0)
    conflict = false
    holdJob = true
    const jobRequest = page.waitForRequest('**/garden-organize/jobs')
    await page.getByRole('button', { name: '下载并自动整理', exact: true }).click()
    await jobRequest
    assert.equal(await page.getByRole('button', { name: '关闭下载并整理', exact: true }).isDisabled(), true)
    await page.keyboard.press('Escape')
    await page.mouse.click(8, 8)
    assert.equal(await page.locator('dialog[open]').count(), 2)
    holdJob = false
    releaseJob()
    await page.getByRole('button', { name: '确认任务状态', exact: true }).waitFor()
    assert.equal(await first.getByRole('spinbutton', { name: 'E027-E28.mkv 集', exact: true }).isDisabled(), true)
    const previews = count('/garden-organize/preview')
    await page.getByRole('button', { name: '确认任务状态', exact: true }).click()
    await page.getByText('任务已创建', { exact: true }).waitFor()
    assert.equal(count('/garden-organize/preview'), previews)
    assert.deepEqual(calls.filter(c => c.endpoint.endsWith('/garden-organize/jobs')).map(c => c.body.previewId), ['one-plan', 'one-plan'])
    assert.equal(count('/cloud-download/tasks'), 0)
    passed.push('independent dialog and focus trap, nested directory Escape/focus, busy close guard, conflicts and idempotent retry')
    await page.getByRole('button', { name: '关闭下载并整理', exact: true }).click()
    await organizeDialog.waitFor({ state: 'detached' })
    assert.equal(await page.locator('dialog[open]').count(), 1)
    assert.equal(await list.isVisible(), true)
    assert.equal(count('/resources/garden/search'), 1)
    const iso = list.locator(':scope > li').nth(1)
    await iso.locator('summary').click(); await iso.getByRole('button', { name: '下载并整理', exact: true }).click()
    await iso.getByRole('button', { name: '普通离线', exact: true }).waitFor()
    assert.equal(await iso.locator('summary').evaluate(el => el === document.activeElement), true)
    const ordinaryDownload = page.waitForResponse(response => new URL(response.url()).pathname.endsWith('/cloud-download/tasks'))
    await iso.getByRole('button', { name: '普通离线', exact: true }).click()
    await ordinaryDownload
    assert.equal(count('/cloud-download/tasks'), 1)
    passed.push('return keeps search, ISO/no-list falls back to ordinary download')
    // Mobile light/dark editor: no horizontal overflow, footer reachable.
    for (const theme of ['light', 'dark']) {
      await page.setViewportSize({ width: 320, height: 740 })
      await page.evaluate(t => document.documentElement.classList.toggle('dark', t === 'dark'), theme)
      const third = list.locator(':scope > li').nth(2)
      await third.locator('summary').click(); await third.getByRole('button', { name: '下载并整理', exact: true }).click()
      await panel.waitFor()
      assert.equal(await panel.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
      assert.equal(await organizeDialog.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
      if (process.env.CINEMA_UI_SHOTS) await page.screenshot({ path: path.join(process.env.CINEMA_UI_SHOTS, `garden-organize-mobile-${theme}.png`) })
      const button = page.getByRole('button', { name: '下载并自动整理', exact: true })
      assert.equal(await button.evaluate(el => { const rect = el.getBoundingClientRect(); return rect.top >= 0 && rect.bottom <= innerHeight }), true)
      await organizeDialog.locator('.anime-floating-body').evaluate(el => { el.scrollTop = el.scrollHeight })
      assert.equal(await button.evaluate(el => { const rect = el.getBoundingClientRect(); return rect.top >= 0 && rect.bottom <= innerHeight }), true)
      await page.keyboard.press('Escape')
      await organizeDialog.waitFor({ state: 'detached' })
    }
    passed.push('320px light/dark editor no horizontal overflow')
    await page.setViewportSize({ width: 1440, height: 1000 })
    const last = list.locator(':scope > li').last()
    await last.locator('summary').click()
    const scrollBefore = await searchDialog.locator('.mikan-body').evaluate(el => el.scrollTop)
    await last.getByRole('button', { name: '下载并整理', exact: true }).click()
    await panel.waitFor()
    assert.equal(await searchDialog.locator('.mikan-body').evaluate(el => el.scrollTop), scrollBefore)
    await page.mouse.click(8, 8)
    await organizeDialog.waitFor({ state: 'detached' })
    assert.equal(await searchDialog.locator('.mikan-body').evaluate(el => el.scrollTop), scrollBefore)
    assert.equal(await last.locator('summary').evaluate(el => el === document.activeElement), true)
    assert.equal(count('/resources/garden/search'), 1)
    assert.equal(await page.evaluate(() => document.body.style.overflow), 'hidden')
    await page.keyboard.press('Escape')
    await searchDialog.waitFor({ state: 'detached' })
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    passed.push('Escape/backdrop close only top dialog; search scroll/focus preserved, no refetch or body scroll leak')
    // Leaving both dialogs together must release their shared body scroll lock.
    await page.getByRole('button', { name: 'Garden 搜索', exact: true }).click()
    const fourth = list.locator(':scope > li').nth(3)
    await fourth.locator('summary').click(); await fourth.getByRole('button', { name: '下载并整理', exact: true }).click()
    await panel.waitFor()
    await page.locator('[data-organize-file]').first().getByRole('checkbox').click()
    await page.getByRole('button', { name: '下载并自动整理', exact: true }).click()
    await page.getByRole('link', { name: '查看整理任务记录', exact: true }).click()
    await page.waitForURL('**/115/manual-organize')
    await searchDialog.waitFor({ state: 'detached' })
    assert.equal(await page.locator('dialog[open]').count(), 0)
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    passed.push('navigation from completed task closes both dialogs and releases body scroll')
    await page.goBack()
    await page.getByRole('button', { name: 'Garden 搜索', exact: true }).click()
    await list.waitFor()
    await row.locator('summary').click(); await row.getByRole('button', { name: '下载并整理', exact: true }).click()
    await panel.waitFor()
    await page.getByRole('button', { name: '选择目录', exact: true }).click()
    await page.getByRole('dialog', { name: '选择本次整理目录', exact: true }).waitFor()
    assert.equal(await page.locator('dialog[open]').count(), 3)
    await page.goForward()
    await page.waitForURL('**/115/manual-organize')
    await searchDialog.waitFor({ state: 'detached' })
    assert.equal(await page.locator('dialog[open]').count(), 0)
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    passed.push('browser history navigation with directory picker open releases all three dialogs and scroll locks')
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, errors }, null, 2))
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
