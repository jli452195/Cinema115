// Both providers run the same workflow. Every API is mocked; no real 115 writes.
const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const source = process.argv[2] || 'garden', isMikan = source === 'mikan'
const hash = n => n.toString(16).padStart(40, '0')
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const passed = [], errors = [], calls = []
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light') })
    const page = await context.newPage(); page.setDefaultTimeout(8000)
    page.on('pageerror', e => errors.push(e.message))
    let total = 4, held = 2, release, fail = 3, lost = true, titleMode = false
    const resource = n => ({ provider: 'dmhy', providerId: String(n), title: `[测试字幕组] 作品 - ${titleMode && n === 2 ? '06-12' : n < 4 ? '01' : String(n).padStart(2, '0')} [${n === 2 ? '720p' : '1080p'}][${n === 2 ? '繁体' : '简体'}]`, magnet: `magnet:?xt=urn:btih:${hash(n)}`, size: 1024, createdAt: '2026-10-24T00:00:00Z', publisher: { id: 2, name: '测试字幕组' }, fansub: { id: 2, name: '测试字幕组' } })
    const file = n => ({ id: 'same/Show.mkv', name: 'Show.mkv', sourcePath: '/same/Show.mkv', parentId: 'same', directory: false, size: 1200, mediaType: 'video', season: 1, episode: n < 4 ? 1 : n, suffix: '' })
    await page.route('**/api/**', async route => {
      const req = route.request(), endpoint = new URL(req.url()).pathname, body = req.postDataJSON()
      calls.push({ endpoint, body })
      let status = 200, data = {}
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock', username: 'admin', role: 'admin' } }
      else if (endpoint.endsWith('/accounts')) data = { accounts: [] }
      else if (endpoint.includes('/tmdb/detail/')) data = { id: 12501, kind: 'tv', title: '测试作品', originalTitle: 'Test', date: '2024-01-01', genres: [{ id: 16, name: '动画' }], countries: ['JP'], seasons: [{ number: 1, name: '第 1 季', episodeCount: 12 }], cast: [], collection: null }
      else if (endpoint.endsWith('/resources/garden/search')) data = { items: Array.from({ length: total }, (_, i) => resource(i + 1)), groups: [], query: body.query, page: body.page, hasMore: false }
      else if (endpoint.endsWith('/mikan/search')) data = { items: Array.from({ length: total }, (_, i) => { const r = resource(i + 1); return { ...r, fansub: undefined, publisher: undefined, id: hash(i + 1), torrentUrl: `https://mikanani.me/Download/20261024/${hash(i + 1)}.torrent`, groupId: '2', size: '1 MB', updatedAt: '2026-10-24', resolution: i === 1 ? '720P' : '1080P', url: `https://mikanani.me/Home/Episode/${hash(i + 1)}` } }), groups: [{ id: '2', name: '测试字幕组' }], bangumis: [], attempts: [], query: body.query }
      else if (endpoint.endsWith('/download-organize/files')) {
        const n = parseInt(body.providerId, isMikan ? 16 : 10)
        if (n === held) await new Promise(resolve => { release = resolve })
        if (n === fail) { status = 502; data = { error: '模拟种子读取失败' } }
        else {
          const f = titleMode && n !== 2 ? { ...file(n), id: 'title', name: body.title, sourcePath: '/title', parentId: '.', titleOnly: true, size: 0 } : file(n)
          data = { manifestId: `manifest-${n}`, hash: hash(n), files: [f], subtitles: [], specs: { [f.id]: { resolution: n === 2 ? '720p' : '1080p', languages: [n === 2 ? 'zh-Hant' : 'zh-Hans'] } } }
        }
      } else if (endpoint.endsWith('/cloud-download/config')) data = { accountId: 'primary', targetCID: '10', targetPath: '/下载' }
      else if (endpoint.endsWith('/download-organize/config')) data = { accountId: 'primary', targetCID: '20', targetPath: '/目标' }
      else if (endpoint.endsWith('/download-organize/batch/preview')) data = { id: 'batch-plan', jobs: body.resources.map((r, i) => ({ id: `job-${i}`, download: { hash: r.magnet.slice(-40) }, items: r.files.map(f => ({ file: { id: f.id }, name: 'mapped.mkv' })) })) }
      else if (endpoint.endsWith('/download-organize/batch/jobs')) {
        if (lost) { lost = false; status = 502; data = { error: '回执丢失' } }
        else data = { id: 'batch-plan', jobs: [{ id: 'one', items: [], status: 'queued' }] }
      } else if (endpoint.endsWith('/download-organize/preview')) data = { id: 'single-plan', download: { hash: body.magnet.slice(-40) }, items: body.files.map(f => ({ file: { id: f.id }, name: 'expected' })) }
      else if (endpoint.endsWith('/download-organize/jobs')) data = req.method() === 'POST' ? { id: 'single-job', items: [], status: 'queued' } : { jobs: [] }
      if (endpoint.endsWith('/mikan/search') || endpoint.endsWith('/resources/garden/search')) data.specs = Object.fromEntries(Array.from({ length: total }, (_, i) => [isMikan ? hash(i + 1) : `dmhy:${i + 1}`, { codec: i === 1 ? 'h264' : 'h265', bitDepth: i === 1 ? '8' : '10', bitrate: i === 2 ? '' : '5 Mbps', languages: [i === 1 ? 'zh-Hant' : 'zh-Hans'], subtitleMode: i === 1 ? 'hard' : 'soft' }]))
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
    })
    const count = endpoint => calls.filter(c => c.endpoint.endsWith(endpoint)).length
    const openSearch = async () => {
      await page.getByRole('button', { name: isMikan ? 'Mikan 搜索' : 'Garden 搜索', exact: true }).click()
      await page.getByRole('list', { name: isMikan ? '蜜柑资源列表' : 'Garden资源列表' }).waitFor()
    }
    const search = page.locator('dialog.mikan-dialog'), editor = page.locator('dialog.anime-floating-organize')
    const submit = () => page.getByRole('button', { name: '下载并自动整理', exact: true })
    const closeEditor = () => page.getByRole('button', { name: '关闭下载并整理', exact: true }).click()
    await page.goto(`${origin}/tv/12501`)
    await openSearch()
    await page.getByRole('button', { name: '批量选择', exact: true }).click()
    const choose = async (label, option) => {
      const trigger = page.getByRole('combobox', { name: `批量${label}`, exact: true })
      await trigger.scrollIntoViewIfNeeded()
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
      await trigger.click()
      await page.getByRole('listbox', { name: `批量${label}`, exact: true }).getByRole('option', { name: option, exact: true }).click()
    }
    const available = n => page.locator('.mikan-select-all small').filter({ hasText: `${n} 项` }).waitFor()
    const searches = count(isMikan ? '/mikan/search' : '/resources/garden/search')
    await choose('编码', 'H.265 / HEVC'); await available(3)
    await choose('字幕', '繁体'); await available(0)
    await choose('字幕', '全部字幕')
    await choose('位深', '10bit'); await available(3)
    await choose('码率', '未标注'); await available(1)
    await choose('码率', '全部码率')
    await choose('字幕形式', '内封'); await available(3)
    for (const label of ['编码', '位深', '字幕形式']) await choose(label, `全部${label}`)
    await choose('分辨率', '720P'); await available(1)
    await page.getByLabel('全选当前筛选').check()
    await choose('分辨率', '全部分辨率')
    assert.equal(count(isMikan ? '/mikan/search' : '/resources/garden/search'), searches)
    assert.equal(count('/download-organize/files'), 0)
    assert.equal(await page.getByRole('navigation', { name: isMikan ? '蜜柑分辨率' : 'Garden分辨率', exact: true }).count(), 0)
    passed.push('local resolution/codec/bit-depth/bitrate/subtitle filters compose; filtered select-all uses no network and keeps previous selections')
    await page.getByRole('button', { name: '清空', exact: true }).click()
    await page.getByLabel('全选当前筛选').check()
    await page.getByRole('textbox', { name: isMikan ? '筛选蜜柑结果' : '筛选Garden结果' }).fill('1080p')
    await page.getByRole('button', { name: '批量整理（4）', exact: true }).click()
    await editor.waitFor()
    await editor.getByText('批量整理 4 项资源，包含外层筛选隐藏的 1 项', { exact: true }).waitFor()
    await page.waitForFunction(() => document.querySelectorAll('[data-organize-file]').length === 1)
    await editor.locator('[data-organize-file]').first().getByRole('textbox').fill('人工修正')
    // Editing already prepared files is possible while the next request waits.
    await page.getByRole('button', { name: '停止解析', exact: true }).click()
    await page.getByRole('button', { name: '继续解析', exact: true }).waitFor()
    held = 0; release()
    await page.getByRole('button', { name: '继续解析', exact: true }).click()
    await editor.getByRole('alert').filter({ hasText: '模拟种子读取失败' }).waitFor()
    await page.waitForFunction(() => document.querySelectorAll('[data-organize-file]').length === 3)
    assert.equal(await submit().isDisabled(), true)
    assert.equal(await editor.locator('[data-organize-file]').first().getByRole('textbox').inputValue(), '人工修正')
    fail = 0
    await page.getByRole('button', { name: '重试解析', exact: true }).click()
    await page.waitForFunction(() => document.querySelectorAll('[data-organize-file]').length === 4)
    await editor.getByLabel('字幕语言', { exact: true }).uncheck()
    await editor.getByLabel('字幕语言', { exact: true }).check()
    const names = await editor.locator('[data-organize-file]').getByRole('textbox').evaluateAll(inputs => inputs.map(input => input.value))
    assert.deepEqual(names, ['测试字幕组 - 简体 - 1080p', '测试字幕组 - 繁体 - 720p', '测试字幕组 - 简体 - 1080p', '测试字幕组 - 简体 - 1080p'])
    // Resource 3 is the same version of episode 1; remove only from this editor.
    await editor.locator('.resource-status > summary').click()
    await editor.locator('.resource-status-list > div').nth(2).getByRole('button', { name: /^移除资源/ }).click()
    assert.equal(await editor.locator('[data-organize-file]').count(), 3)
    await editor.getByLabel('字幕语言', { exact: true }).uncheck()
    await editor.getByLabel('分辨率', { exact: true }).uncheck()
    await editor.getByRole('alert').filter({ hasText: '2 个文件目标重名' }).first().waitFor()
    assert.equal(await submit().isDisabled(), true)
    await editor.getByLabel('字幕语言', { exact: true }).check()
    await editor.getByLabel('分辨率', { exact: true }).check()
    assert.equal(await submit().isEnabled(), true)
    const requests = count('/download-organize/files')
    await submit().click()
    await page.getByRole('button', { name: '确认任务状态', exact: true }).click()
    await page.getByText('任务已创建', { exact: true }).waitFor()
    const previews = calls.filter(c => c.endpoint.endsWith('/download-organize/batch/preview'))
    assert.equal(previews.length, 1)
    assert.deepEqual(previews[0].body.resources.map(r => r.manifestId), ['manifest-1', 'manifest-2', 'manifest-4'])
    assert.deepEqual(previews[0].body.resources.map(r => r.files[0].id), ['same/Show.mkv', 'same/Show.mkv', 'same/Show.mkv'])
    assert.equal(count('/download-organize/files'), requests)
    assert.deepEqual(calls.filter(c => c.endpoint.endsWith('/download-organize/batch/jobs')).map(c => c.body.previewId), ['batch-plan', 'batch-plan'])
    assert.equal(count('/cloud-download/tasks'), 0)
    assert.equal(count('/cloud-download/tasks/batch'), 0)
    await closeEditor()
    await page.getByRole('textbox', { name: isMikan ? '筛选蜜柑结果' : '筛选Garden结果' }).fill('')
    await page.getByRole('button', { name: '批量整理（1）', exact: true }).waitFor()
    passed.push('hidden selections, progressive edit, pause/continue, failed parse retry, per-file tags, collisions, editor-only removal and atomic idempotent submit')
    // A larger batch must be parsed exactly once, then sent in one request.
    await page.keyboard.press('Escape'); await search.waitFor({ state: 'detached' })
    total = 61; titleMode = true
    await openSearch()
    await page.getByRole('button', { name: '批量选择', exact: true }).click()
    await page.getByLabel('全选当前筛选').check()
    const before = count('/download-organize/files')
    await page.getByRole('button', { name: '批量整理（61）', exact: true }).click()
    await editor.getByText('已解析 61 / 61 项资源', { exact: true }).waitFor()
    assert.equal(count('/download-organize/files') - before, 61)
    assert.equal(await editor.locator('[data-organize-file]').count(), 61)
    // Mixed title intents/file manifests share one list. Expected names never
    // invent a filename extension or expose the internal intent ID as a path.
    assert.equal(await editor.getByRole('button', { name: /^展开或折叠/ }).count(), 1)
    const titleRow = editor.locator('[data-organize-file]').first()
    assert.equal(await titleRow.getByRole('button', { name: /^查看完整路径/ }).count(), 0)
    assert.ok((await titleRow.textContent()).includes('预计命名：测试作品 - S01E01'))
    assert.equal((await titleRow.textContent()).includes('.mkv'), false)
    await editor.getByLabel('筛选整理文件名', { exact: true }).fill('[测试字幕组]')
    assert.equal(await editor.locator('[data-organize-file]').count(), 60)
    await editor.getByLabel('筛选整理文件名', { exact: true }).fill('')
    await editor.locator('[data-organize-file]').nth(1).getByRole('button', { name: /^查看完整路径/ }).click()
    await editor.locator('[data-organize-file]').nth(1).getByText('/same/Show.mkv', { exact: true }).waitFor()
    await editor.locator('[data-organize-file]').nth(2).getByRole('checkbox').uncheck()
      await page.setViewportSize({ width: 320, height: 740 })
      await page.evaluate(() => document.documentElement.classList.remove('dark'))
      assert.equal(await editor.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
      const footer = await editor.locator('.anime-floating-footer').boundingBox()
      assert.ok(footer.y >= 0 && footer.y + footer.height <= 740)
      await editor.locator('.anime-floating-body').evaluate(el => { el.scrollTop = el.scrollHeight })
      await submit().click()
      await page.getByText('任务已创建', { exact: true }).waitFor()
    assert.equal(count('/download-organize/files') - before, 61)
    assert.equal(calls.filter(c => c.endpoint.endsWith('/download-organize/batch/preview')).at(-1).body.resources.length, 60)
    await closeEditor()
    assert.equal(await search.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
    const actions = await search.locator('.mikan-batch-actions').boundingBox()
    assert.ok(actions.x >= 0 && actions.x + actions.width <= 320)
    const specBar = await search.getByRole('group', { name: '批量资源规格筛选' }).boundingBox()
    assert.ok(specBar.x >= 0 && specBar.x + specBar.width <= 320)
    await choose('码率', '5 Mbps')
    await choose('码率', '全部码率')
    if (process.env.SCREENSHOT_DIR) await page.screenshot({ path: path.join(process.env.SCREENSHOT_DIR, `batch-${source}-light.png`) })
    passed.push('61 mixed title/file resources prepare once, expected names omit extensions/paths, titles remain searchable, single batch request and 320px layout')
    // Escape cancels preparation and restores focus; navigation releases locks.
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await choose('字幕', '简体')
    await choose('字幕', '全部字幕')
    if (process.env.SCREENSHOT_DIR) await page.screenshot({ path: path.join(process.env.SCREENSHOT_DIR, `batch-${source}-dark.png`) })
    for (const theme of ['light', 'dark']) {
      await page.evaluate(t => document.documentElement.classList.toggle('dark', t === 'dark'), theme)
      await page.getByRole('combobox', { name: '批量分辨率', exact: true }).click()
      const popup = page.getByRole('listbox', { name: '批量分辨率', exact: true })
      const background = await popup.evaluate(el => getComputedStyle(el).backgroundColor)
      assert.ok(!background.startsWith('rgba') && background !== 'transparent', `popup must be opaque: ${background}`)
      assert.equal(background, theme === 'dark' ? 'rgb(30, 41, 59)' : 'rgb(248, 250, 252)')
      if (process.env.SCREENSHOT_DIR) await page.screenshot({ path: path.join(process.env.SCREENSHOT_DIR, `popup-${source}-${theme}.png`) })
      await search.getByRole('group', { name: '批量资源规格筛选' }).evaluate(el => { el.scrollLeft += 40 })
      await popup.waitFor({ state: 'detached' })
      await choose('分辨率', '全部分辨率')
    }
    await page.getByRole('button', { name: '批量整理（1）', exact: true }).click()
    await editor.getByText('已解析 1 / 1 项资源', { exact: true }).waitFor()
    assert.equal(await editor.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
    await page.keyboard.press('Escape'); await editor.waitFor({ state: 'detached' })
    assert.equal(await page.getByRole('button', { name: '批量整理（1）', exact: true }).evaluate(el => el === document.activeElement), true)
    await page.getByRole('button', { name: '批量整理（1）', exact: true }).click()
    await editor.waitFor()
    await page.goto(`${origin}/115/download-organize`)
    assert.equal(await page.locator('dialog[open]').count(), 0)
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    passed.push('320px dark theme, Escape focus restore and navigation cancellation/scroll cleanup')
    // A single clear episode is preselected and reuses its prepared manifest.
    total = 4
    await page.goto(`${origin}/tv/12501`)
    await openSearch()
    const beforeSingle = count('/download-organize/files')
    await search.getByRole('button', { name: '115 整理', exact: true }).first().click()
    await editor.waitFor()
    assert.equal(await editor.locator('[data-organize-file]').getByRole('checkbox').isChecked(), true)
    await editor.getByText(/1 项单集按标题预览命名/).waitFor()
    assert.equal(await editor.getByRole('button', { name: /^查看完整路径/ }).count(), 0)
    await submit().click()
    await page.getByText('任务已创建', { exact: true }).waitFor()
    assert.equal(count('/download-organize/files'), beforeSingle + 1)
    assert.equal(calls.filter(c => c.endpoint.endsWith('/download-organize/preview')).at(-1).body.files[0].id, 'title')
    passed.push('single title intent preselected, deferred subtitle/extension explanation and no preparation refetch on submit')
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ source, passed, errors }, null, 2))
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
