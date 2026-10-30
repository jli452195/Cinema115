// Both providers run the same workflow. Every API is mocked; no real 115 writes.
const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const source = process.argv[2] || 'garden', isMikan = source === 'mikan'
const hash = n => n.toString(16).padStart(40, '0')
const naming = require('./media-naming-fixture.cjs').mediaNamingFixture('测试作品', '2024')
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const passed = [], errors = [], calls = []
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light') })
    const page = await context.newPage(); page.setDefaultTimeout(8000)
    page.on('pageerror', e => errors.push(e.message))
    let total = 4, held = 2, release, fail = 3, lost = true, titleMode = false, duplicate = false
    const resource = n => ({ provider: 'dmhy', providerId: String(n), title: `[测试字幕组] 作品 - ${titleMode && n === 2 ? '06-12' : n < 4 ? '01' : String(n).padStart(2, '0')} [${n === 2 ? '720p' : '1080p'}][${n === 2 ? '繁体' : '简体'}]`, magnet: `magnet:?xt=urn:btih:${hash(duplicate && n === 4 ? 3 : n)}`, size: 1024, createdAt: '2026-10-24T00:00:00Z', publisher: { id: 2, name: '测试字幕组' }, fansub: { id: 2, name: '测试字幕组' } })
    const file = n => ({ id: 'same/Show.mkv', name: 'Show.mkv', sourcePath: '/same/Show.mkv', parentId: 'same', directory: false, size: 1200, mediaType: 'video', season: 1, episode: n < 4 ? 1 : n })
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
          f.namingValues = { resolution: n === 2 ? '720p' : '1080p', languages: n === 2 ? '繁体' : '简体', releaseGroup: '测试字幕组' }
          data = { manifestId: `manifest-${n}`, hash: hash(n), files: [f], subtitles: [], specs: { [f.id]: { resolution: n === 2 ? '720p' : '1080p', languages: [n === 2 ? 'zh-Hant' : 'zh-Hans'] } } }
        }
      } else if (endpoint.endsWith('/cloud-download/config')) data = { accountId: 'primary', targetCID: '10', targetPath: '/下载' }
      else if (endpoint.endsWith('/cloud-download/tasks/batch')) {
        const events = [{ type: 'ready', total: body.urls.length }, ...body.urls.flatMap((_, index) => [{ type: 'start', index, total: body.urls.length }, { type: 'result', index, total: body.urls.length, accepted: index > 0, outcomeUncertain: index === 0, error: index === 0 ? '模拟回执待核对' : '' }]), { type: 'done', total: body.urls.length, stopped: false }]
        return route.fulfill({ contentType: 'application/x-ndjson', body: events.map(event => JSON.stringify(event)).join('\n') + '\n' })
      }
      else if (endpoint.endsWith('/download-organize/config')) data = { accountId: 'primary', targetCID: '20', targetPath: '/目标' }
      else if (endpoint.endsWith('/naming/media-preview')) data = naming.preview(body)
      else if (endpoint.endsWith('/download-organize/batch/preview')) data = { id: 'batch-plan', jobs: body.resources.map((r, i) => ({ id: `job-${i}`, download: { hash: r.magnet.slice(-40) }, ...naming.plan(r.files, `${r.magnet.slice(-40)}:`) })) }
      else if (endpoint.endsWith('/download-organize/batch/jobs')) {
        if (lost) { lost = false; status = 502; data = { error: '回执丢失' } }
        else data = { id: 'batch-plan', jobs: [{ id: 'one', items: [], status: 'queued' }] }
      } else if (endpoint.endsWith('/download-organize/preview')) data = { id: 'single-plan', download: { hash: body.magnet.slice(-40) }, ...naming.plan(body.files) }
      else if (endpoint.endsWith('/download-organize/jobs')) data = req.method() === 'POST' ? { id: 'single-job', items: [], status: 'queued' } : { jobs: [] }
      if (endpoint.endsWith('/mikan/search') || endpoint.endsWith('/resources/garden/search')) data.specs = Object.fromEntries(Array.from({ length: total }, (_, i) => [isMikan ? hash(i + 1) : `dmhy:${i + 1}`, { resolution: i === 1 ? '720p' : '1080p', languages: i === 2 ? ['zh-Hans', 'zh-Hant', 'ja'] : [i === 1 ? 'zh-Hant' : 'zh-Hans'], subtitleMode: i === 2 ? 'external' : 'soft' }]))
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
    const spec = label => search.getByRole('checkbox', { name: `筛选${label}`, exact: true })
    const checks = search.locator('.mikan-resource-checkbox input')
    const selected = async indices => page.waitForFunction(expected => JSON.stringify([...document.querySelectorAll('.mikan-resource-checkbox input')].flatMap((input, i) => input.checked ? [i + 1] : [])) === JSON.stringify(expected), indices)
    const rendered = async indices => page.waitForFunction(expected => JSON.stringify([...document.querySelectorAll('.mikan-resource-title h4')].map(el => el.textContent)) === JSON.stringify(expected), indices.map(n => resource(n).title))
    const searches = count(isMikan ? '/mikan/search' : '/resources/garden/search')
    assert.deepEqual(await search.getByRole('group', { name: '资源规格筛选' }).getByRole('checkbox').evaluateAll(inputs => inputs.map(input => input.getAttribute('aria-label'))), ['筛选1080P', '筛选720P', '筛选简体', '筛选繁体', '筛选简繁', '筛选外挂字幕'])
    await checks.nth(1).check()
    await spec('1080P').check(); await rendered([1, 3, 4]); await selected([])
    await page.getByRole('button', { name: '批量整理（1）', exact: true }).waitFor()
    await spec('简体').check(); await rendered([1, 4])
    await spec('简繁').check(); await rendered([1, 3, 4])
    await spec('720P').check(); await rendered([1, 3, 4])
    await spec('繁体').check(); await rendered([1, 2, 3, 4])
    await spec('1080P').uncheck(); await rendered([2]); await selected([1])
    await spec('繁体').uncheck(); await rendered([])
    await spec('720P').uncheck(); await rendered([1, 3, 4])
    await spec('外挂字幕').check(); await rendered([3]); await selected([])
    await spec('简体').uncheck(); await rendered([3])
    await spec('简繁').uncheck(); await rendered([3])
    await page.getByRole('button', { name: '重置规格筛选', exact: true }).click()
    await rendered([1, 2, 3, 4]); await selected([2])
    await spec('简体').check(); await rendered([1, 4])
    await page.getByLabel('全选当前筛选').check(); await selected([1, 2])
    await page.getByRole('button', { name: '批量整理（3）', exact: true }).waitFor()
    await page.getByRole('button', { name: '清空选择', exact: true }).click(); await selected([])
    assert.equal(await spec('简体').isChecked(), true)
    await page.getByRole('button', { name: '重置规格筛选', exact: true }).click()
    await rendered([1, 2, 3, 4])
    assert.equal(count(isMikan ? '/mikan/search' : '/resources/garden/search'), searches)
    assert.equal(count('/download-organize/files'), 0)
    passed.push('checkboxes only filter visible resources; OR/AND groups, exclusive Chinese categories, reset and selection are independent without network')
    await checks.nth(1).check()
    await spec('1080P').check()
    total = 5
    await page.getByRole('button', { name: '强制刷新', exact: true }).click()
    await rendered([1, 2, 3, 4, 5]); await selected([2])
    assert.equal(await spec('1080P').isChecked(), false)
    total = 4
    await page.getByRole('button', { name: '强制刷新', exact: true }).click()
    await rendered([1, 2, 3, 4]); await selected([2])
    passed.push('refresh resets specification filters, preserves existing selections and does not select new results')
    await page.getByRole('button', { name: '清空选择', exact: true }).click()
    await page.getByLabel('全选当前筛选').check()
    await page.getByRole('textbox', { name: isMikan ? '筛选蜜柑结果' : '筛选Garden结果' }).fill('1080p')
    await page.getByRole('button', { name: '批量整理（4）', exact: true }).click()
    await editor.waitFor()
    await editor.getByText('批量整理 4 项资源，包含外层筛选隐藏的 1 项', { exact: true }).waitFor()
    await page.waitForFunction(() => document.querySelectorAll('[data-organize-file]').length === 1)
    await editor.locator('[data-organize-file]').first().getByRole('spinbutton', { name: 'Show.mkv 集', exact: true }).fill('9')
    // Editing already prepared files is possible while the next request waits.
    await page.getByRole('button', { name: '停止解析', exact: true }).click()
    await page.getByRole('button', { name: '继续解析', exact: true }).waitFor()
    await editor.getByRole('button', { name: /^暂存这 \d+ 项$/ }).click()
    assert.equal(await submit().isEnabled(), true, 'waiting resources do not block confirmed files')
    await editor.getByText('文件已全部暂存，可继续核对整理清单。', { exact: true }).waitFor()
    await editor.getByRole('button', { name: '退回编辑第 1 季', exact: true }).click()
    held = 0; release()
    await page.getByRole('button', { name: '继续解析', exact: true }).click()
    await editor.getByRole('alert').filter({ hasText: '模拟种子读取失败' }).waitFor()
    await page.waitForFunction(() => document.querySelectorAll('[data-organize-file]').length === 3)
    await page.waitForFunction(() => [...document.querySelectorAll('dialog.anime-floating-organize button')].some(button => /^暂存这 \d+ 项$/.test(button.textContent) && !button.disabled))
    assert.equal(await submit().isEnabled(), true, 'ready checked files submit without staging despite another resource failure')
    assert.equal(await editor.locator('[data-organize-file]').first().getByRole('spinbutton', { name: 'Show.mkv 集', exact: true }).inputValue(), '9')
    await editor.getByRole('button', { name: /^暂存这 \d+ 项$/ }).click()
    assert.equal(await submit().isEnabled(), true, 'failed resources do not block confirmed files')
    await editor.getByRole('button', { name: '退回编辑第 1 季', exact: true }).click()
    fail = 0
    await page.getByRole('button', { name: '重试解析', exact: true }).click()
    await page.waitForFunction(() => document.querySelectorAll('[data-organize-file]').length === 4)
    const episodes = await editor.locator('[data-organize-file]').getByRole('spinbutton', { name: 'Show.mkv 集', exact: true }).evaluateAll(inputs => inputs.map(input => input.value))
    assert.deepEqual(episodes, ['9', '1', '4', '1'])
    assert.equal(await editor.getByRole('textbox', { name: /自定义标记/ }).count(), 0)
    await editor.getByRole('link', { name: '公共命名模板', exact: true }).waitFor()
    assert.equal(await editor.getByLabel('字幕语言', { exact: true }).count(), 0)
    await editor.locator('[data-organize-file]').first().getByRole('spinbutton', { name: 'Show.mkv 集', exact: true }).fill('1')
    await editor.getByRole('alert').filter({ hasText: '2 个文件目标重名' }).first().waitFor()
    // Resource 3 is the same version of episode 1; remove only from this editor.
    await editor.locator('.resource-status > summary').click()
    await editor.locator('.resource-status-list > div').nth(2).getByRole('button', { name: /^移除资源/ }).click()
    assert.equal(await editor.locator('[data-organize-file]').count(), 3)
    await editor.locator('[data-organize-file]').first().getByText('整理后：测试作品.S01E01.1080p.[简体]-测试字幕组.mkv', { exact: true }).waitFor()
    const requests = count('/download-organize/files')
    await editor.getByRole('button', { name: /^暂存这 \d+ 项$/ }).click()
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
    passed.push('hidden selections, progressive edit, pause/continue, failed parse retry, per-file numbering, collisions, editor-only removal and atomic idempotent submit')
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
    await editor.locator('.anime-floating-body').evaluate(el => { el.scrollTop = 0 })
    await page.waitForFunction(() => {
      const bar = document.querySelector('dialog.anime-floating-organize .numbering-bar')
      return !bar.classList.contains('is-stuck') && getComputedStyle(bar).backgroundColor === 'rgba(0, 0, 0, 0)'
    })
    if (process.env.SCREENSHOT_DIR) await page.screenshot({ path: path.join(process.env.SCREENSHOT_DIR, `batch-${source}-numbering-in-flow.png`) })
    // Mixed title intents/file manifests share one list. Expected names never
    // invent a filename extension or expose the internal intent ID as a path.
    assert.equal(await editor.getByRole('button', { name: /^展开或折叠/ }).count(), 1)
    const titleRow = editor.locator('[data-organize-file]').first()
    assert.equal(await titleRow.getByRole('button', { name: /^查看完整路径/ }).count(), 0)
    await titleRow.getByText('预计命名：测试作品.S01E01.1080p.[简体]-测试字幕组', { exact: true }).waitFor()
    assert.equal((await titleRow.textContent()).includes('.mkv'), false)
    assert.equal(await editor.getByRole('searchbox').count(), 0)
    await editor.locator('[data-organize-file]').nth(1).getByRole('button', { name: /^查看完整路径/ }).click()
    await editor.locator('[data-organize-file]').nth(1).getByText('/same/Show.mkv', { exact: true }).waitFor()
    await editor.locator('[data-organize-file]').nth(2).getByRole('checkbox').uncheck()
    const assertStickyNumbering = async () => {
      await editor.locator('.anime-floating-body').evaluate(el => { el.scrollTop = el.scrollHeight })
      await page.waitForFunction(() => {
        const dialog = document.querySelector('dialog.anime-floating-organize')
        const body = dialog.querySelector('.anime-floating-body'), bar = dialog.querySelector('.numbering-bar'), folder = dialog.querySelector('.directory-heading-sticky')
        const bounds = bar.getBoundingClientRect(), viewport = body.getBoundingClientRect()
        const stage = bar.querySelector('.stage-action')
        if (!stage || stage.disabled) return false
        const action = stage.getBoundingClientRect(), folderBounds = folder.getBoundingClientRect()
        const hit = document.elementFromPoint(action.x + action.width / 2, action.y + action.height / 2)
        return bar.classList.contains('is-stuck') && getComputedStyle(bar).backdropFilter === 'none'
          && Math.abs(bounds.top - viewport.top) <= 1
          && Math.abs(bounds.left - viewport.left) <= 1
          && Math.abs(bounds.right - viewport.left - body.clientWidth) <= 1
          && Math.abs(folderBounds.top - bounds.bottom) <= 1
          // Staging stays usable while scrolling, including when the folder
          // header and mobile numbering controls are pinned alongside it.
          && action.width > 0 && action.height > 0
          && action.top >= viewport.top - 1 && action.bottom <= viewport.bottom + 1
          && action.left >= viewport.left - 1 && action.right <= viewport.left + body.clientWidth + 1
          && action.bottom <= folderBounds.top + 1 && stage.contains(hit)
      })
    }
    await assertStickyNumbering()
    if (process.env.SCREENSHOT_DIR) await page.screenshot({ path: path.join(process.env.SCREENSHOT_DIR, `batch-${source}-numbering-pinned-desktop.png`) })
      await page.setViewportSize({ width: 320, height: 740 })
      await page.evaluate(() => document.documentElement.classList.remove('dark'))
      assert.equal(await editor.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
      const footer = await editor.locator('.anime-floating-footer').boundingBox()
      assert.ok(footer.y >= 0 && footer.y + footer.height <= 740)
      await assertStickyNumbering()
      const numberingHeight = (await editor.locator('.numbering-bar').boundingBox()).height
      if (process.env.SCREENSHOT_DIR) await page.screenshot({ path: path.join(process.env.SCREENSHOT_DIR, `batch-${source}-numbering-mobile.png`) })
      assert.ok(numberingHeight <= 220, `expanded mobile numbering must leave room for the file list (${numberingHeight}px)`)
      await editor.getByRole('button', { name: '批量编号', exact: true }).click()
      await assertStickyNumbering()
      await editor.getByRole('button', { name: '批量编号', exact: true }).click()
      await assertStickyNumbering()
      if (process.env.SCREENSHOT_DIR) await page.screenshot({ path: path.join(process.env.SCREENSHOT_DIR, `batch-${source}-sticky-numbering.png`) })
      assert.equal(await editor.locator('.confirmed-group').count(), 0)
      await submit().click()
      await page.getByText('任务已创建', { exact: true }).waitFor()
    assert.equal(count('/download-organize/files') - before, 61)
    assert.equal(calls.filter(c => c.endpoint.endsWith('/download-organize/batch/preview')).at(-1).body.resources.length, 60)
    await closeEditor()
    assert.equal(await search.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
    const actions = await search.locator('.mikan-batch-actions').boundingBox()
    assert.ok(actions.x >= 0 && actions.x + actions.width <= 320)
    const specBar = await search.getByRole('group', { name: '资源规格筛选' }).boundingBox()
    assert.ok(specBar.x >= 0 && specBar.x + specBar.width <= 320)
    if (process.env.SCREENSHOT_DIR) await page.screenshot({ path: path.join(process.env.SCREENSHOT_DIR, `batch-${source}-light.png`) })
    passed.push('61 mixed resources prepare once; numbering pins without gaps or extra blur and staging stays visible and hit-testable through desktop/mobile wrapping and collapse; single batch request')
    // Escape cancels preparation and restores focus; navigation releases locks.
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    if (process.env.SCREENSHOT_DIR) await page.screenshot({ path: path.join(process.env.SCREENSHOT_DIR, `batch-${source}-dark.png`) })
    for (const theme of ['light', 'dark']) {
      await page.evaluate(t => document.documentElement.classList.toggle('dark', t === 'dark'), theme)
      const option = spec('720P')
      await option.focus(); await page.keyboard.press('Space')
      assert.equal(await option.isChecked(), true)
      const chip = option.locator('..')
      assert.ok((await chip.boundingBox()).height >= 44)
      const style = await chip.evaluate(el => ({ color: getComputedStyle(el).color, background: getComputedStyle(el).backgroundColor, outline: getComputedStyle(el).outlineStyle }))
      assert.notEqual(style.color, style.background)
      assert.equal(style.outline, 'solid')
      await page.keyboard.press('Space')
      assert.equal(await option.isChecked(), false)
      assert.equal(await search.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
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
    // Filters do not select; explicit select-all still reuses the 100-item limit.
    total = 105
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.goto(`${origin}/tv/12501`)
    await openSearch()
    await page.getByRole('button', { name: '批量选择', exact: true }).click()
    const beforeLimit = count('/download-organize/files')
    await spec('1080P').check(); await selected([])
    await page.getByLabel('全选当前筛选').check()
    await page.getByRole('button', { name: '批量整理（100）', exact: true }).waitFor()
    await search.getByText('每批最多选择 100 项，已保留当前选择。', { exact: true }).waitFor()
    await page.setViewportSize({ width: 320, height: 740 })
    assert.equal(await search.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
    const limitActions = await search.locator('.mikan-batch-actions').boundingBox()
    assert.ok(limitActions.x >= 0 && limitActions.x + limitActions.width <= 320)
    if (process.env.SCREENSHOT_DIR) await page.screenshot({ path: path.join(process.env.SCREENSHOT_DIR, `batch-${source}-limit.png`) })
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.getByRole('button', { name: '显示更多资源', exact: true }).click()
    await page.getByRole('button', { name: '显示更多资源', exact: true }).click()
    assert.equal(await checks.count(), 104)
    assert.equal(await checks.evaluateAll(inputs => inputs.filter(input => input.checked).length), 100)
    assert.equal(await checks.last().isChecked(), false)
    await spec('1080P').uncheck()
    await page.getByRole('button', { name: '批量整理（100）', exact: true }).waitFor()
    await page.getByRole('button', { name: '清空选择', exact: true }).click(); await selected([])
    assert.equal(await search.getByText('每批最多选择 100 项，已保留当前选择。', { exact: true }).count(), 0)
    assert.equal(count('/download-organize/files'), beforeLimit)
    await page.keyboard.press('Escape')
    passed.push('explicit select-all obeys 100-item limit across undisplayed rows; filter changes preserve selection; clear removes stale limit notice')
    total = 4; duplicate = true
    await openSearch()
    await page.getByRole('button', { name: '批量选择', exact: true }).click()
    await spec('简繁').check(); await rendered([3]); await selected([])
    await spec('简体').check(); await rendered([1, 3, 4])
    await page.getByLabel('全选当前筛选').check(); await selected([1, 2, 3])
    await page.getByRole('button', { name: '批量整理（2）', exact: true }).waitFor()
    await spec('简繁').uncheck(); await rendered([1, 4]); await selected([1, 2])
    await page.keyboard.press('Escape'); duplicate = false
    passed.push('duplicate titles filter independently and explicit select-all deduplicates magnets')
    // A single clear episode is preselected and reuses its prepared manifest.
    total = 4
    await page.goto(`${origin}/tv/12501`)
    await openSearch()
    const beforeSingle = count('/download-organize/files')
    await search.getByRole('button', { name: '115 整理', exact: true }).first().click()
    await editor.waitFor()
    assert.equal(await editor.locator('[data-organize-file]').getByRole('checkbox').isChecked(), true)
    await editor.locator('.organize-help > summary').click()
    await editor.getByText(/1 项单集按标题预览命名/).waitFor()
    assert.equal(await editor.getByRole('button', { name: /^查看完整路径/ }).count(), 0)
    assert.equal(await editor.locator('.confirmed-group').count(), 0)
    await submit().click()
    await page.getByText('任务已创建', { exact: true }).waitFor()
    assert.equal(count('/download-organize/files'), beforeSingle + 1)
    assert.equal(calls.filter(c => c.endpoint.endsWith('/download-organize/preview')).at(-1).body.files[0].id, 'title')
    passed.push('single title intent preselected, deferred subtitle/extension explanation and no preparation refetch on submit')
    await closeEditor()
    await page.getByRole('button', { name: '批量选择', exact: true }).click()
    await spec('1080P').check(); await rendered([1, 3, 4]); await selected([])
    assert.equal(await checks.first().isDisabled(), true)
    await page.getByLabel('全选当前筛选').check()
    await page.getByRole('button', { name: '提交到 115（2）', exact: true }).click()
    await page.getByText('已提交 1，失败 0，待核对 1。下载进度请到 115 查看。', { exact: true }).waitFor()
    await selected([])
    assert.equal(await checks.nth(1).isDisabled(), true)
    assert.equal(await checks.nth(2).isDisabled(), true)
    await spec('1080P').uncheck(); await spec('720P').check(); await rendered([2]); await selected([])
    await page.getByLabel('全选当前筛选').check(); await selected([1])
    passed.push('filtered successful and uncertain resources stay visible but disabled; available resources can still be selected')
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ source, passed, errors }, null, 2))
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
