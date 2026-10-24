// All network APIs are mocked. This test never submits a real 115 download.
const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const source = process.argv[2] || 'garden', isMikan = source === 'mikan', isGying = source === 'gying'
const searchLabel = isGying ? /观影\s*搜索/ : isMikan ? 'Mikan 搜索' : 'Garden 搜索'
const searchSuffix = isGying ? '/gying/search' : isMikan ? '/mikan/search' : '/resources/garden/search'
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const calls = [], errors = [], passed = []
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light') })
    const page = await context.newPage(); page.setDefaultTimeout(8000)
    page.on('pageerror', e => errors.push(e.message))
    let loseResponse = true, conflict = false, holdJob = false, releaseJob, failFiles = false
    const files = [
      { id: 'show/E027-E28.mkv', name: 'E027-E28.mkv', sourcePath: '/show/E027-E28.mkv', episode: 27, endEpisode: 28 },
      { id: 'show/E030.mkv', name: 'E030.mkv', sourcePath: '/show/E030.mkv', episode: 30 },
      { id: 'show/OVA.mkv', name: 'OVA.mkv', sourcePath: '/show/OVA.mkv', episode: 0 },
    ].map(f => ({ ...f, directory: false, parentId: '', size: 0, mediaType: 'video', season: 1, suffix: '' }))
    const subtitles = [
      { file: { id: 'show/E027-E28.zh-CN.ass', name: 'E027-E28.zh-CN.ass', sourcePath: '/show/E027-E28.zh-CN.ass' }, videoId: files[0].id, tail: '.zh-CN' },
      { file: { id: 'show/E027-E28.zh-TW.default.srt', name: 'E027-E28.zh-TW.default.srt', sourcePath: '/show/E027-E28.zh-TW.default.srt' }, videoId: files[0].id, tail: '.zh-TW.default' },
      { file: { id: 'subtitles/unmatched.ass', name: 'unmatched.ass', sourcePath: '/subtitles/unmatched.ass' } },
    ]
    const resource = n => ({ provider: 'dmhy', providerId: String(n), title: `测试合集 ${n} [1080p]`, size: 1024 ** 3, createdAt: '2026-10-23T00:00:00Z', magnet: `magnet:?xt=urn:btih:${n.toString(16).padStart(40, '0')}`, publisher: { id: 2, name: '测试组' } })
    await page.route('**/api/**', async route => {
      const req = route.request(), endpoint = new URL(req.url()).pathname, body = req.postDataJSON()
      calls.push({ endpoint, body }); let status = 200, data = {}
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock', username: 'admin', role: 'admin', disabled: false } }
      else if (endpoint.endsWith('/accounts')) data = { accounts: [] }
      else if (endpoint.includes('/tmdb/detail/')) data = { id: 12501, kind: endpoint.includes('/detail/movie/') ? 'movie' : 'tv', title: '百变之星', originalTitle: 'カレイドスター', overview: '', posterUrl: '', backdropUrl: '', date: '2003-04-03', rating: 7, votes: 1, genres: [{ id: 16, name: '动画' }], countries: ['JP'], cast: [], seasons: [{ number: 1, name: '第 1 季', episodeCount: 26 }, { number: 2, name: '第 2 季', episodeCount: 25 }], collection: null }
      else if (endpoint.endsWith('/resources/garden/search')) data = { items: Array.from({ length: 15 }, (_, i) => resource(i + 1)), groups: [], query: body.query, page: 1, hasMore: false, cached: false }
      else if (endpoint.endsWith('/mikan/search')) data = { items: Array.from({ length: 15 }, (_, i) => {
        const r = resource(i + 1), id = (i + 1).toString(16).padStart(40, '0')
        return { id, title: r.title, magnet: r.magnet, url: `https://mikanani.me/Home/Episode/${id}`, torrentUrl: `https://mikanani.me/Download/20261024/${id}.torrent`, groupId: '2', size: '1 GB', updatedAt: '2026-10-24', resolution: '1080P' }
      }), groups: [{ id: '2', name: '测试组' }], bangumis: [], attempts: [], query: body.query, group: '' }
      else if (endpoint.endsWith('/gying/config')) data = { configured: true, hasSession: true, resourceOrder: [] }
      else if (endpoint.endsWith('/gying/search')) data = { query: 'カレイドスター', matchedBy: 'title', total: 1, cached: false, attempts: [], items: [{ id: 'tv:Show', title: '百变之星', year: 2003, mediaType: 'tv', links: Array.from({ length: 15 }, (_, i) => ({ category: 'torrent', type: 'magnet', typeLabel: '磁力', workTitle: resource(i + 1).title, url: resource(i === 13 ? 1 : i + 1).magnet, hasTorrent: i !== 12 })) }] }
      else if (endpoint.endsWith('/download-organize/files')) {
        if (failFiles) { status = 502; data = { error: '种子读取暂时失败' } }
        else data = Number.parseInt(isGying ? body.magnet.split(':').at(-1) : body.providerId, 16) === 2 ? { files: [], reason: '清单中没有可整理的视频文件，可使用普通离线（不支持 ISO）' } : { files, subtitles }
      }
      else if (endpoint.endsWith('/cloud-download/config')) data = { accountId: 'primary', targetCID: '10', targetPath: '/下载' }
      else if (endpoint.endsWith('/download-organize/config')) data = { accountId: 'primary', targetCID: '20', targetPath: '/动漫' }
      else if (endpoint.endsWith('/download-organize/jobs') && req.method() === 'GET') data = { jobs: [] }
      else if (endpoint.endsWith('/cloud-download/paths')) data = { directories: [{ id: '30', name: '自选目录' }], hasMore: false }
      else if (endpoint.endsWith('/download-organize/preview')) data = { id: 'one-plan', title: '百变之星', status: 'preview', items: [...body.files.map(f => ({ file: files.find(x => x.id === f.id), name: 'mapped.mkv' })), ...subtitles.filter(sub => body.files.some(f => f.id === sub.videoId)).map(sub => ({ file: sub.file, name: 'mapped.ass', conflict: conflict ? '字幕目标同名' : '' }))] }
      else if (endpoint.endsWith('/download-organize/jobs')) {
        if (holdJob) await new Promise(resolve => { releaseJob = resolve })
        if (loseResponse) { loseResponse = false; status = 502; data = { error: '模拟回执丢失' } }
        else data = { id: 'one-plan', title: '百变之星', status: 'queued', items: [{ file: files[0] }, { file: files[1] }] }
      } else if (endpoint.endsWith('/cloud-download/tasks')) data = { accepted: true }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const count = suffix => calls.filter(c => c.endpoint.endsWith(suffix)).length
    const trigger = row => row.getByRole('button', { name: '115 整理', exact: true })
    const open = row => trigger(row).click()
    await page.goto(`${origin}/tv/12501`)
    await page.getByRole('button', { name: searchLabel, exact: true }).click()
    const list = isGying ? page.locator('.gy-resource-list') : page.getByRole('list', { name: isMikan ? '蜜柑资源列表' : 'Garden资源列表' })
    await list.waitFor()
    assert.equal(count('/download-organize/files'), 0)
    const row = list.locator(':scope > li').first()
    assert.equal(await page.getByRole('link', { name: '下载种子', exact: true }).count(), 0)
    await open(row)
    const panel = page.getByRole('region', { name: '下载并整理映射' })
    const organizeDialog = page.locator('dialog.anime-floating-organize')
    const searchDialog = page.locator(isGying ? 'dialog.gy-dialog' : 'dialog.mikan-dialog')
    await panel.waitFor()
    assert.equal(await page.locator('dialog[open]').count(), 2)
    assert.equal(await list.isVisible(), true)
    assert.equal(await searchDialog.locator(isGying ? '.gy-resource-list' : '.mikan-search-form').isVisible(), true)
    for (let i = 0; i < 6; i++) {
      await page.keyboard.press('Tab')
      assert.equal(await organizeDialog.evaluate(el => el.contains(document.activeElement)), true)
    }
    assert.equal(count('/download-organize/files'), 1)
    const input = calls.find(c => c.endpoint.endsWith('/download-organize/files')).body
    assert.equal(input.source, source)
    if (isMikan) { assert.equal(input.providerId, '1'.padStart(40, '0')); assert.ok(input.torrentUrl.startsWith('https://mikanani.me/Download/')) }
    if (isGying) { assert.equal(input.provider, 'tv'); assert.equal(input.providerId, 'Show'); assert.equal(input.torrentUrl, undefined) }
    assert.equal(count('/download-organize/jobs'), 0)
    await page.getByRole('button', { name: '下载并自动整理', exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: '下载并自动整理', exact: true }).isDisabled(), true)
    const first = page.locator('[data-organize-file]').nth(0), second = page.locator('[data-organize-file]').nth(1)
    if (isGying) {
      await first.getByRole('textbox', { name: 'E027-E28.mkv 版本标记', exact: true }).fill('测试组')
      await second.getByRole('textbox', { name: 'E030.mkv 版本标记', exact: true }).fill('测试组')
    }
    const subtitleToggle = first.locator('.subtitle-details > summary')
    await subtitleToggle.focus(); await page.keyboard.press('Enter')
    assert.equal(await first.getByRole('checkbox').isChecked(), false)
    await first.getByText('随视频选择，当前保留原位').first().waitFor()
    await first.getByRole('checkbox').click(); await second.getByRole('checkbox').click()
    await page.getByText('已选 2 个视频，关联 2 个字幕', { exact: true }).waitFor()
    await first.getByRole('checkbox').click()
    await page.getByText('已选 1 个视频', { exact: true }).waitFor()
    await first.getByRole('checkbox').click()
    assert.equal(await page.getByRole('button', { name: '下载并自动整理', exact: true }).isEnabled(), true)
    await page.getByRole('combobox', { name: '目标季', exact: true }).click()
    await page.getByRole('option').filter({ hasText: '第 2 季' }).click()
    await page.getByRole('spinbutton', { name: '发布起始集', exact: true }).fill('27')
    await page.getByRole('button', { name: '应用偏移（2 项）', exact: true }).click()
    assert.equal(await first.getByRole('spinbutton', { name: 'E027-E28.mkv 集', exact: true }).inputValue(), '1')
    assert.equal(await first.getByRole('spinbutton', { name: 'E027-E28.mkv 结束集', exact: true }).inputValue(), '2')
    assert.equal(await second.getByRole('spinbutton', { name: 'E030.mkv 集', exact: true }).inputValue(), '4')
    await first.getByText('整理后：百变之星 - S02E01-E02 - 测试组.mkv', { exact: true }).waitFor()
    await first.getByText('整理后：百变之星 - S02E01-E02 - 测试组.zh-TW.default.srt', { exact: true }).waitFor()
    await first.getByRole('textbox', { name: 'E027-E28.mkv 版本标记', exact: true }).fill('BD')
    await first.getByText('整理后：百变之星 - S02E01-E02 - BD.zh-CN.ass', { exact: true }).waitFor()
    await first.getByRole('textbox', { name: 'E027-E28.mkv 版本标记', exact: true }).fill('测试组')
    assert.equal(await first.locator('.subtitle-details').getAttribute('open'), '')
    assert.equal(count('/download-organize/preview'), 0)
    await page.getByText('1 个字幕未能唯一匹配，保留原位', { exact: true }).click()
    await page.getByText('/subtitles/unmatched.ass', { exact: true }).waitFor()
    if (process.env.CINEMA_UI_SHOTS) { await fs.mkdir(process.env.CINEMA_UI_SHOTS, { recursive: true }); await page.screenshot({ path: path.join(process.env.CINEMA_UI_SHOTS, 'download-organize-desktop.png') }) }
    await page.getByRole('button', { name: '撤销', exact: true }).click()
    assert.equal(await first.getByRole('spinbutton', { name: 'E027-E28.mkv 集', exact: true }).inputValue(), '27')
    await page.getByRole('button', { name: '应用偏移（2 项）', exact: true }).click()
    assert.equal(await page.locator('[data-organize-file]').nth(2).getByRole('checkbox').isChecked(), false)
    passed.push('lazy metadata, range/gap offset and undo, companion selection/naming and keyboard expansion without requests')
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
    await subtitleToggle.click()
    await page.getByRole('button', { name: '下载并自动整理', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '命名或目标目录存在冲突' }).waitFor()
    await first.getByRole('alert').filter({ hasText: '字幕目标同名' }).first().waitFor()
    assert.equal(await first.locator('.subtitle-details').getAttribute('open'), '')
    assert.equal(count('/download-organize/jobs'), 0)
    await subtitleToggle.click()
    await first.getByRole('textbox', { name: 'E027-E28.mkv 版本标记', exact: true }).fill('BD')
    assert.equal(await first.locator('.subtitle-details').getAttribute('open'), null)
    await first.getByRole('textbox', { name: 'E027-E28.mkv 版本标记', exact: true }).fill('测试组')
    conflict = false
    holdJob = true
    const jobRequest = page.waitForRequest('**/download-organize/jobs')
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
    const previews = count('/download-organize/preview')
    await page.getByRole('button', { name: '确认任务状态', exact: true }).click()
    await page.getByText('任务已创建', { exact: true }).waitFor()
    assert.equal(count('/download-organize/preview'), previews)
    assert.deepEqual(calls.filter(c => c.endpoint.endsWith('/download-organize/jobs')).map(c => c.body.previewId), ['one-plan', 'one-plan'])
    assert.equal(count('/cloud-download/tasks'), 0)
    passed.push('independent dialog and focus trap, nested directory Escape/focus, busy close guard, conflicts and idempotent retry')
    await page.getByRole('button', { name: '关闭下载并整理', exact: true }).click()
    await organizeDialog.waitFor({ state: 'detached' })
    assert.equal(await page.locator('dialog[open]').count(), 1)
    assert.equal(await list.isVisible(), true)
    assert.equal(count(searchSuffix), 1)
    if (isGying) {
      const duplicate = list.locator(':scope > li').nth(13)
      assert.equal(await trigger(duplicate).isDisabled(), true)
      assert.equal(await duplicate.locator('[data-gy-action="cloud"]').isDisabled(), true)
      assert.equal(await list.locator(':scope > li').nth(12).locator('[data-gy-action="organize"]').count(), 0)
      passed.push('same magnet shares submitted state; resources without torrents keep ordinary download only')
    }
    const iso = list.locator(':scope > li').nth(1)
    await open(iso)
    await iso.getByRole('button', { name: '普通离线', exact: true }).waitFor()
    assert.equal(await trigger(iso).evaluate(el => el === document.activeElement), true)
    const ordinaryDownload = page.waitForResponse(response => new URL(response.url()).pathname.endsWith('/cloud-download/tasks'))
    await iso.getByRole('button', { name: '普通离线', exact: true }).click()
    await ordinaryDownload
    assert.equal(count('/cloud-download/tasks'), 1)
    if (isGying) assert.equal(await iso.getByText(/清单中没有可整理的视频/).count(), 0)
    passed.push('return keeps search, ISO/no-list falls back to ordinary download')
    // Mobile light/dark editor: no horizontal overflow, footer reachable.
    for (const theme of ['light', 'dark']) {
      await page.setViewportSize({ width: 320, height: 740 })
      await page.evaluate(t => document.documentElement.classList.toggle('dark', t === 'dark'), theme)
      const third = list.locator(':scope > li').nth(2)
      const actionWidths = await third.locator(isGying ? '.gy-actions > *' : '.mikan-resource-actions > *').evaluateAll(elements => elements.map(el => el.getBoundingClientRect().width))
      assert.ok(Math.max(...actionWidths) - Math.min(...actionWidths) <= 1, `mobile action buttons should share the available width: ${actionWidths}`)
      assert.equal(await searchDialog.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
      await open(third)
      await panel.waitFor()
      const mobileFirst = page.locator('[data-organize-file]').first()
      if (isGying) await mobileFirst.getByRole('textbox', { name: 'E027-E28.mkv 版本标记', exact: true }).fill('测试组')
      await mobileFirst.getByRole('checkbox').click()
      await mobileFirst.locator('.subtitle-details > summary').click()
      await mobileFirst.getByText('整理后：百变之星 - S01E27-E28 - 测试组.zh-TW.default.srt', { exact: true }).waitFor()
      assert.equal(await panel.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
      assert.equal(await organizeDialog.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
      if (process.env.CINEMA_UI_SHOTS) await page.screenshot({ path: path.join(process.env.CINEMA_UI_SHOTS, `${source}-organize-mobile-${theme}.png`) })
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
    await trigger(last).scrollIntoViewIfNeeded()
    const scrollBody = searchDialog.locator(isGying ? '.gy-body' : '.mikan-body')
    const scrollBefore = await scrollBody.evaluate(el => el.scrollTop)
    await open(last)
    await panel.waitFor()
    assert.equal(await scrollBody.evaluate(el => el.scrollTop), scrollBefore)
    await page.mouse.click(8, 8)
    await organizeDialog.waitFor({ state: 'detached' })
    assert.equal(await scrollBody.evaluate(el => el.scrollTop), scrollBefore)
    assert.equal(await trigger(last).evaluate(el => el === document.activeElement), true)
    assert.equal(count(searchSuffix), 1)
    assert.equal(await page.evaluate(() => document.body.style.overflow), 'hidden')
    await page.keyboard.press('Escape')
    await searchDialog.waitFor({ state: 'detached' })
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    passed.push('Escape/backdrop close only top dialog; search scroll/focus preserved, no refetch or body scroll leak')
    // Leaving both dialogs together must release their shared body scroll lock.
    await page.getByRole('button', { name: searchLabel, exact: true }).click()
    const fourth = list.locator(':scope > li').nth(3)
    await open(fourth)
    await panel.waitFor()
    await page.locator('[data-organize-file]').first().getByRole('checkbox').click()
    await page.getByRole('button', { name: '下载并自动整理', exact: true }).click()
    await page.getByRole('link', { name: '查看下载整理任务', exact: true }).click()
    await page.waitForURL('**/115/download-organize')
    await searchDialog.waitFor({ state: 'detached' })
    assert.equal(await page.locator('dialog[open]').count(), 0)
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    passed.push('navigation from completed task closes both dialogs and releases body scroll')
    await page.goBack()
    await page.getByRole('button', { name: searchLabel, exact: true }).click()
    await list.waitFor()
    await open(row)
    await panel.waitFor()
    await page.getByRole('button', { name: '选择目录', exact: true }).click()
    await page.getByRole('dialog', { name: '选择本次整理目录', exact: true }).waitFor()
    assert.equal(await page.locator('dialog[open]').count(), 3)
    await page.goForward()
    await page.waitForURL('**/115/download-organize')
    await searchDialog.waitFor({ state: 'detached' })
    assert.equal(await page.locator('dialog[open]').count(), 0)
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    passed.push('browser history navigation with directory picker open releases all three dialogs and scroll locks')
    if (isGying) {
      await page.goto(`${origin}/movie/12501`)
      await page.getByRole('button', { name: searchLabel, exact: true }).click()
      failFiles = true
      await open(row)
      await row.getByText(/种子读取暂时失败/).waitFor()
      assert.equal(await page.locator('dialog[open]').count(), 1)
      assert.equal(await row.getByRole('button', { name: '普通离线', exact: true }).isEnabled(), true)
      assert.equal(await trigger(row).isEnabled(), true)
      failFiles = false
      await open(row)
      await panel.waitFor()
      const movieFile = page.locator('[data-organize-file]').first()
      await movieFile.getByRole('checkbox').click()
      await movieFile.getByText('整理后：百变之星 (2003).mkv', { exact: true }).waitFor()
      assert.equal(await page.getByRole('combobox', { name: '目标季', exact: true }).count(), 0)
      await page.getByRole('button', { name: '下载并自动整理', exact: true }).click()
      await page.getByText('任务已创建', { exact: true }).waitFor()
      const preview = calls.filter(c => c.endpoint.endsWith('/download-organize/preview')).at(-1).body
      assert.equal(preview.kind, 'movie')
      assert.equal(preview.source, 'gying')
      passed.push('torrent failure can retry or download normally; movie uses year naming without TV episode controls')
    }
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, errors }, null, 2))
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
