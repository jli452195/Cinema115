// Isolated UI validation: every API is mocked; no real cloud operations.
const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const naming = require('./media-naming-fixture.cjs').mediaNamingFixture('测试作品', '2026')
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const calls = [], errors = []
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage(); page.setDefaultTimeout(10000); page.on('pageerror', e => errors.push(e.message))
    const files = [
      ['a', 'Show.S01E01.mkv', 1, 1], ['b', 'Show.S01E02.mkv', 1, 2],
      ['c', 'Show.S02E01.mkv', 2, 1], ['d', 'Show.S02E02.mkv', 2, 2],
      ['unknown', 'OVA.mkv', 1, 0], ['duplicate', 'Duplicate.S01E01.mkv', 1, 1],
    ].map(([id, name, season, episode]) => ({ id, name, season, episode, sourcePath: `/flat/${name}`, parentId: 'flat', directory: false, size: 100, mediaType: 'video' }))
    let loseReceipt = true
    await page.route('**/api/**', async route => {
      const req = route.request(), endpoint = new URL(req.url()).pathname
      const multipart = req.headers()['content-type']?.startsWith('multipart/form-data')
      const body = multipart ? undefined : req.postDataJSON()
      calls.push({ endpoint, body }); let status = 200, data = {}
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock', username: 'admin', role: 'admin', disabled: false } }
      else if (endpoint.includes('/tmdb/detail/')) data = { id: 42, kind: 'tv', title: '测试作品', originalTitle: 'Show', overview: '', posterUrl: '', backdropUrl: '', date: '2026-01-01', rating: 7, votes: 1, genres: [{ id: 16, name: '动画' }], countries: [], countryCodes: [], cast: [], seasons: [1, 2].map(number => ({ number, name: `第 ${number} 季`, episodeCount: 2 })), collection: null }
      else if (endpoint.endsWith('/resources/garden/search')) data = { items: [{ provider: 'dmhy', providerId: '1', title: 'Show S1-S2 合集', magnet: `magnet:?xt=urn:btih:${'a'.repeat(40)}`, size: 1000, createdAt: '2026-10-31T00:00:00Z', publisher: { id: 1, name: 'Group' } }], groups: [], query: body.query, page: 1, hasMore: false }
      else if (endpoint.endsWith('/download-organize/files')) data = { files, subtitles: [] }
      else if (endpoint.endsWith('/cloud-download/config')) data = { accountId: 'primary', targetCID: '10', targetPath: '/下载' }
      else if (endpoint.endsWith('/download-organize/config')) data = { accountId: 'primary', targetCID: '20', targetPath: '/整理' }
      else if (endpoint.endsWith('/download-organize/share-config')) data = { ready: true }
      else if (endpoint.endsWith('/naming/media-preview')) data = naming.preview(body)
      else if (endpoint.endsWith('/download-organize/preview')) data = { id: 'plan', ...naming.plan(body.files.map(row => files.find(file => file.id === row.id))) }
      else if (endpoint.endsWith('/download-organize/subtitles/attachments')) { await route.fulfill({ status: 204 }); return }
      else if (endpoint.endsWith('/download-organize/jobs') && req.method() === 'POST') {
        if (loseReceipt) { loseReceipt = false; status = 502; data = { error: '模拟任务回执丢失' } }
        else data = { id: 'plan', status: 'queued', title: '测试作品', items: [] }
      }
      else if (endpoint.endsWith('/subtitles/search')) data = { versions: [] }
      else if (endpoint.endsWith('/subtitles/assrt/search')) data = { configured: false, items: [], hasMore: false }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const count = suffix => calls.filter(call => call.endpoint.endsWith(suffix)).length
    await page.goto(`${origin}/tv/42`)
    await page.getByRole('button', { name: 'Garden 搜索', exact: true }).click()
    await page.getByRole('button', { name: '115 整理', exact: true }).click()
    const dialog = page.locator('dialog.anime-floating-organize'), confirmed = dialog.getByRole('region', { name: '已暂存的整理文件' })
    const row = id => dialog.locator(`[data-organize-file="${id}"]`)
    const confirm = () => dialog.getByRole('button', { name: /^暂存这 \d+ 项$/ })
    const start = () => dialog.getByRole('button', { name: '下载并自动整理', exact: true })
    const returnSeason = n => dialog.getByRole('button', { name: `退回编辑第 ${n} 季`, exact: true })
    await row('a').waitFor(); await dialog.getByText('版本 3', { exact: true }).waitFor()
    assert.equal(await start().isDisabled(), true)
    const size = () => dialog.evaluate(el => { const r = el.getBoundingClientRect(); return [r.x, r.y, r.width, r.height] })
    const initial = await size()
    for (const id of ['a', 'b']) await row(id).getByRole('checkbox').check()
    assert.equal(await start().isEnabled(), true, 'a single season can submit without staging')
    assert.equal(await dialog.getByRole('button', { name: '查找字幕', exact: true }).isEnabled(), true, 'subtitle search uses current selection')
    await confirm().click()
    await returnSeason(1).waitFor()
    assert.equal(await dialog.locator('[data-organize-file]').count(), 4)
    assert.deepEqual(await size(), initial)
    for (const id of ['c', 'd']) await row(id).getByRole('checkbox').check()
    await dialog.getByRole('combobox', { name: '目标季', exact: true }).click()
    await page.getByRole('option', { name: /第 2 季/ }).click()
    await dialog.getByLabel('目标起始集', { exact: true }).fill('1')
    await dialog.getByRole('button', { name: '应用偏移（2 项）', exact: true }).click()
    await confirm().click()
    assert.equal(await confirmed.locator('.confirmed-group').count(), 2)
    assert.equal(await dialog.locator('[data-organize-file]').count(), 2)
    for (const id of ['unknown', 'duplicate']) await row(id).getByRole('checkbox').check()
    await confirm().click()
    await dialog.getByText(/已暂存 0 个视频，2 个文件存在问题/).waitFor()
    assert.equal(await start().isDisabled(), true, 'invalid checked files must be corrected or deselected')
    assert.equal(await confirmed.locator('.confirmed-group').count(), 2, 'bad pending selection does not return prior confirmed files')
    for (const id of ['unknown', 'duplicate']) await row(id).getByRole('checkbox').uncheck()
    await dialog.getByRole('button', { name: '排除目录 flat 及下级', exact: true }).click()
    await returnSeason(1).click()
    assert.equal(await row('a').getByRole('spinbutton', { name: / 季$/ }).inputValue(), '1')
    assert.equal(await row('a').getByRole('checkbox').isChecked(), true)
    assert.equal(await confirmed.locator('.confirmed-group').count(), 1)
    assert.equal(await start().isEnabled(), true, 'returned files remain selected for direct submission')
    assert.equal(count('/download-organize/files'), 1)
    assert.equal(count('/download-organize/preview'), 0)
    assert.equal(count('/subtitles/assrt/search'), 0)
    await dialog.getByRole('button', { name: '查找字幕', exact: true }).click()
    const picker = page.locator('dialog.subtitle-picker')
    await picker.getByLabel('选择本地字幕或压缩包', { exact: true }).setInputFiles({ name: '01.sc.ass', mimeType: 'text/plain', buffer: Buffer.from('[Script Info]') })
    const subtitle = picker.locator('.match-files > li').first()
    await subtitle.waitFor()
    assert.equal(await subtitle.getByRole('checkbox').isChecked(), false)
    await picker.getByRole('combobox', { name: '字幕所属季', exact: true }).click()
    await page.getByRole('option', { name: '第 2 季', exact: true }).click()
    assert.equal(await subtitle.getByRole('checkbox').isChecked(), true)
    assert.equal(await subtitle.getByRole('combobox', { name: /对应视频/ }).inputValue(), 'c')
    await picker.getByRole('combobox', { name: '字幕所属季', exact: true }).click()
    await page.getByRole('option', { name: '第 1 季', exact: true }).click()
    assert.equal(await subtitle.getByRole('combobox', { name: /对应视频/ }).inputValue(), 'c', 'season switch preserves association')
    await picker.getByRole('button', { name: '使用所选字幕', exact: true }).click()
    // Only season 2 remains staged; season 1 is still checked in the editor.
    assert.equal(await confirmed.locator('.confirmed-group').count(), 1)
    await returnSeason(2).click()
    assert.equal(await row('c').getByRole('checkbox').isChecked(), true)
    await row('c').getByRole('spinbutton', { name: / 集$/ }).fill('3')
    await confirm().click()
    for (const id of ['a', 'b']) await row(id).getByRole('checkbox').check()
    assert.equal(await confirmed.locator('.confirmed-group').count(), 1)
    await confirmed.getByText(/外部字幕 1 个/).waitFor()
    const artifact = path.join(os.tmpdir(), 'cinema115-confirmation-ui'); await fs.mkdir(artifact, { recursive: true })
    for (const [width, theme] of [[1440, 'light'], [390, 'light'], [320, 'dark']]) {
      await page.setViewportSize({ width, height: 900 }); await page.evaluate(t => document.documentElement.classList.toggle('dark', t === 'dark'), theme)
      assert.equal(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
      const footer = await dialog.locator('.anime-floating-footer').boundingBox()
      assert.ok(footer.y >= 0 && footer.y + footer.height <= 900)
      await page.screenshot({ path: path.join(artifact, `confirmation-${width}-${theme}.png`) })
    }
    await start().click()
    await dialog.getByRole('button', { name: '确认任务状态', exact: true }).waitFor()
    assert.equal(await confirm().isDisabled(), true)
    await dialog.getByRole('button', { name: '确认任务状态', exact: true }).click()
    await dialog.getByText('后台将下载整条磁力', { exact: false }).waitFor()
    const preview = calls.find(call => call.endpoint.endsWith('/download-organize/preview')).body
    assert.deepEqual(preview.files.map(file => [file.id, file.season, file.episode]), [['a', 1, 1], ['b', 1, 2], ['c', 2, 3], ['d', 2, 2]])
    assert.equal(preview.subtitles[0].videoId, 'c')
    assert.equal(count('/download-organize/preview'), 1)
    assert.equal(count('/download-organize/subtitles/attachments'), 1)
    assert.deepEqual(calls.filter(call => call.endpoint.endsWith('/download-organize/jobs')).map(call => call.body.previewId), ['plan', 'plan'])
    assert.equal(count('/cloud-download/tasks'), 0)
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed: ['single-season direct submission and subtitle access without staging', 'optional multi-season staging and return preserve mappings', 'invalid checked files block submission until corrected or deselected', 'staging is local and does not refetch torrents', 'multi-season subtitle scope and preservation after return', 'staged plus checked files submit once with lost receipt recovery', 'desktop/mobile/dark stable layout'], artifact }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
