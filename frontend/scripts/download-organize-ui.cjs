// All network APIs are mocked. This test never submits a real 115 download.
const assert = require('node:assert/strict')
const path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const source = process.argv[2] || 'garden', isMikan = source === 'mikan', isGying = source === 'gying'
const shareMode = process.argv[3] === 'share'
const subtitleMode = process.argv[3] === 'subtitles'
const localSubtitleMode = process.argv[3] === 'local-subtitles'
const assrtMode = process.argv[3] === 'assrt'
const searchLabel = isGying ? /观影\s*搜索/ : isMikan ? 'Mikan 搜索' : 'Garden 搜索'
const searchSuffix = isGying ? '/gying/search' : isMikan ? '/mikan/search' : '/resources/garden/search'
const naming = require('./media-naming-fixture.cjs').mediaNamingFixture('百变之星', '2003')
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const calls = [], errors = [], passed = []
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light') })
    const page = await context.newPage(); page.setDefaultTimeout(8000)
    page.on('pageerror', e => errors.push(e.message))
    let loseResponse = true, conflict = false, holdJob = false, releaseJob, failFiles = false, releaseNaming, namingRevision = 3, staleTemplate = false, renamed = false, subtitleError = false, nestedMode = false
    let shareConfigured = false, omitOrganizeDirectory = false, loseAttachmentResponse = localSubtitleMode
    let japanese = true, animation = true, emptySubtitles = localSubtitleMode, assrtConfigured = assrtMode
    let assrtFail = false, assrtDownloadFail = false, releaseLibrary
    const files = [
      { id: 'show/E027-E28.mkv', name: 'E027-E28.mkv', sourcePath: '/show/E027-E28.mkv', episode: 27, endEpisode: 28 },
      { id: 'show/E030.mkv', name: 'E030.mkv', sourcePath: '/show/E030.mkv', episode: 30 },
      { id: 'show/OVA.mkv', name: 'OVA.mkv', sourcePath: '/show/OVA.mkv', episode: 0 },
    ].map(f => ({ ...f, namingValues: { releaseGroup: '测试组' }, directory: false, parentId: '', size: 0, mediaType: 'video', season: 1 }))
    const nestedFiles = [
      { id: 'pack/Root.mkv', season: 3, episode: 1 },
      { id: 'pack/Season 01/E01.mkv', season: 1, episode: 1 },
      { id: 'pack/Season 01/Extras/Featurettes/Bonus-A.mkv', season: 1, episode: 0 },
      { id: 'pack/Season 02/Extras/Featurettes/Bonus-B.mkv', season: 2, episode: 0 },
    ].map(file => ({ ...file, name: file.id.split('/').at(-1), sourcePath: `/${file.id}`, directory: false, parentId: '', size: 0, mediaType: 'video' }))
    const subtitles = [
      { file: { id: 'show/E027-E28.zh-CN.ass', name: 'E027-E28.zh-CN.ass', sourcePath: '/show/E027-E28.zh-CN.ass' }, videoId: files[0].id, tail: '.zh-CN' },
      { file: { id: 'show/E027-E28.zh-TW.default.srt', name: 'E027-E28.zh-TW.default.srt', sourcePath: '/show/E027-E28.zh-TW.default.srt' }, videoId: files[0].id, tail: '.zh-TW.default' },
      { file: { id: 'subtitles/unmatched.ass', name: 'unmatched.ass', sourcePath: '/subtitles/unmatched.ass' } },
    ]
    const resource = n => ({ provider: 'dmhy', providerId: String(n), title: `测试合集 ${n} [1080p]`, size: 1024 ** 3, createdAt: '2026-10-23T00:00:00Z', magnet: `magnet:?xt=urn:btih:${n.toString(16).padStart(40, '0')}`, publisher: { id: 2, name: '测试组' } })
    await page.route('**/api/**', async route => {
      const req = route.request(), endpoint = new URL(req.url()).pathname
      const multipart = req.headers()['content-type']?.startsWith('multipart/form-data')
      const body = multipart ? req.postData() : req.postDataJSON()
      calls.push({ endpoint, body }); let status = 200, data = {}
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock', username: 'admin', role: 'admin', disabled: false } }
      else if (endpoint.endsWith('/accounts')) data = { accounts: [] }
      else if (endpoint.includes('/tmdb/detail/')) data = { id: 12501, kind: endpoint.includes('/detail/movie/') ? 'movie' : 'tv', title: '百变之星', originalTitle: 'カレイドスター', overview: '', posterUrl: '', backdropUrl: '', date: '2003-04-03', rating: 7, votes: 1, genres: animation ? [{ id: 16, name: '动画' }] : [], countries: ['Japan'], countryCodes: japanese ? ['JP'] : ['CN'], cast: [], seasons: [{ number: 1, name: 'The very long season name with an unbroken subtitle ABCDEFGHIJKLMNOPQRSTUVWXYZABCDEFGHIJKLMNOPQRSTUVWXYZ', episodeCount: 26 }, { number: 2, name: '新的篇章与很长的季度副标题', episodeCount: 25 }], collection: null }
      else if (endpoint === '/api/admin/subtitles/search') {
        if (assrtMode && new URL(req.url()).searchParams.getAll('q').includes('慢字幕库')) await new Promise(resolve => { releaseLibrary = resolve })
        if (new URL(req.url()).searchParams.getAll('q').includes('故障测试')) { status = 502; data = { error: '模拟字幕库异常' } }
        else data = { versions: emptySubtitles ? [] : [{ id: 'v1', title: '百变之星', originalTitle: 'カレイドスター', year: '2003', source: 'BD', name: '测试字幕组', url: 'https://github.com/foxofice/sub_share' }] }
      }
      else if (endpoint === '/api/admin/subtitles/assrt/search') {
        if (assrtFail) { status = 429; data = { error: '射手网请求已达配额，请稍后重试' } }
        else data = { configured: assrtConfigured, items: assrtConfigured ? [{ id: 123, title: '百变之星 BD 中文字幕', language: '简体 / 繁体', group: '射手测试组', format: 'ASS', videoName: 'Show.1080p.BluRay' }] : [], hasMore: false }
      }
      else if (endpoint === '/api/admin/subtitles/assrt/123/download') {
        if (assrtDownloadFail) { await route.fulfill({ status: 429, contentType: 'application/json', body: JSON.stringify({ error: '射手下载配额不足，请稍后重试' }) }); return }
        const { zipSync, strToU8 } = require('fflate')
        const bytes = Buffer.from(zipSync({ '01.chs.ass': strToU8('[Script Info]\nTitle: Assrt'), '01.cht.ass': strToU8('[Script Info]\nTitle: Assrt CHT'), 'font.ttf': strToU8('font') }))
        await route.fulfill({ status: 200, contentType: 'application/octet-stream', headers: { 'Content-Length': String(bytes.length), 'X-Subtitle-Filename': encodeURIComponent('测试字幕.zip') }, body: bytes }); return
      }
      else if (endpoint === '/api/admin/subtitles/versions/v1/files') data = { files: ['01.sc.ass', '01.tc.ass', 'Show [SP01].chs.ass'].map((name, i) => ({ path: `subs_list/animation/2003/Show/BD/Group/${name}`, name, sha: String(i + 1).repeat(40), revision: 'a'.repeat(40), size: 100 })) }
      else if (endpoint.endsWith('/resources/garden/search')) data = { items: Array.from({ length: 15 }, (_, i) => resource(i + 1)), groups: [], query: body.query, page: 1, hasMore: false, cached: false }
      else if (endpoint.endsWith('/mikan/search')) data = { items: Array.from({ length: 15 }, (_, i) => {
        const r = resource(i + 1), id = (i + 1).toString(16).padStart(40, '0')
        return { id, title: r.title, magnet: r.magnet, url: `https://mikanani.me/Home/Episode/${id}`, torrentUrl: `https://mikanani.me/Download/20261024/${id}.torrent`, groupId: '2', size: '1 GB', updatedAt: '2026-10-24', resolution: '1080P' }
      }), groups: [{ id: '2', name: '测试组' }], bangumis: [], attempts: [], query: body.query, group: '' }
      else if (endpoint.endsWith('/gying/config')) data = { configured: true, hasSession: true, resourceOrder: [] }
      else if (endpoint.endsWith('/gying/search')) data = { query: 'カレイドスター', matchedBy: 'title', total: 1, cached: false, attempts: [], items: [{ id: 'tv:Show', title: '百变之星', year: 2003, mediaType: 'tv', links: Array.from({ length: 15 }, (_, i) => ({ category: 'torrent', type: 'magnet', typeLabel: '磁力', workTitle: resource(i + 1).title, url: resource(i === 13 ? 1 : i + 1).magnet, hasTorrent: i !== 12 })) }] }
      else if (endpoint.endsWith('/download-organize/files')) {
        if (failFiles) { status = 502; data = { error: '种子读取暂时失败' } }
        else data = nestedMode ? { files: nestedFiles, subtitles: [] } : Number.parseInt(isGying ? body.magnet.split(':').at(-1) : body.providerId, 16) === 2 ? { files: [], reason: '清单中没有可整理的视频文件，可使用普通离线（不支持 ISO）' } : { files, subtitles }
      }
      else if (endpoint.endsWith('/cloud-download/config')) data = { accountId: 'primary', targetCID: '10', targetPath: '/下载' }
      else if (endpoint.endsWith('/download-organize/config')) data = { accountId: 'primary', targetCID: omitOrganizeDirectory ? '' : '20', targetPath: omitOrganizeDirectory ? '' : '/动漫' }
      else if (endpoint.endsWith('/download-organize/share-config')) data = { ready: shareConfigured, reason: '请先完善分享 STRM 设置' }
      else if (endpoint.endsWith('/download-organize/jobs') && req.method() === 'GET') data = { jobs: [] }
      else if (endpoint.endsWith('/cloud-download/paths')) data = { directories: [{ id: '30', name: '自选目录' }], hasMore: false }
      else if (endpoint.endsWith('/naming/media-preview')) {
        data = naming.preview(body, namingRevision)
        if (subtitleError) { const sub = data.items.find(item => item.id === subtitles[0].file.id); if (sub) { delete sub.result; sub.error = '字幕名称超过长度限制' } }
        if (body.files[0].episode === 26) await new Promise(resolve => { releaseNaming = resolve })
      }
      else if (endpoint.endsWith('/download-organize/preview')) {
        data = { id: 'one-plan', title: '百变之星', status: 'preview', ...naming.plan([...body.files.map(f => files.find(x => x.id === f.id)), ...subtitles.filter(sub => body.files.some(f => f.id === sub.videoId)).map(sub => sub.file)]) }
        if (conflict) data.items.filter(item => item.file.name.endsWith('.ass')).forEach(item => { item.conflict = '字幕目标同名' })
        if (staleTemplate) { staleTemplate = false; status = 409; data = { error: '命名模板已更新，请刷新命名预览并重新确认' } }
        else if (renamed) data.items.forEach(item => { item.name = item.name.replace('百变之星', '新名称'); item.stem = item.stem.replace('百变之星', '新名称') })
        if (body.shareSTRM) {
          data.id = 'share-plan'; data.download = { hash: '1'.padStart(40, '0'), share: { directory: 'Cinema115-isolated' } }
          data.items.forEach(item => { item.directory = `Cinema115-isolated/${item.directory}` })
        }
      }
      else if (endpoint.endsWith('/download-organize/subtitles/attachments')) {
        assert.ok(multipart)
        if (loseAttachmentResponse) { loseAttachmentResponse = false; status = 502; data = { error: '模拟附件回执丢失' } }
        else { await route.fulfill({ status: 204 }); return }
      }
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
    const fileRow = index => organizeDialog.locator(`[data-organize-file=${JSON.stringify(files[index].id)}]`)
    const stagedRow = index => organizeDialog.locator('.confirmation-zone li').filter({ has: page.getByText(files[index].name, { exact: true }) })
    async function stashSelection() {
      await organizeDialog.getByRole('button', { name: /^暂存这 [1-9]\d* 项$/ }).click()
    }
    async function editStaged() {
      const buttons = organizeDialog.getByRole('button', { name: /^退回编辑/ })
      while (await buttons.count()) await buttons.first().click()
    }
    async function showStaged(index) {
      const details = stagedRow(index).locator('xpath=ancestor::details')
      if (!await details.evaluate(el => el.open)) await details.locator('summary').click()
    }
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
    if (isMikan) { assert.equal(input.providerId, '1'.padStart(40, '0')); assert.equal(input.fansub, '测试组'); assert.ok(input.torrentUrl.startsWith('https://mikanani.me/Download/')) }
    if (isGying) { assert.equal(input.provider, 'tv'); assert.equal(input.providerId, 'Show'); assert.equal(input.torrentUrl, undefined) }
    assert.equal(count('/download-organize/jobs'), 0)
    const stableLayout = () => organizeDialog.evaluate(el => {
      const rect = el.getBoundingClientRect()
      return [rect.x, rect.y, rect.width, rect.height, el.querySelector('.anime-floating-body').clientWidth]
    })
    const expandedLayout = await stableLayout()
    const groupToggle = organizeDialog.getByRole('button', { name: /^展开或折叠/ }).first()
    await groupToggle.click()
    assert.deepEqual(await stableLayout(), expandedLayout)
    await groupToggle.click()
    assert.equal(await organizeDialog.getByRole('searchbox').count(), 0)
    await page.getByText('版本 3', { exact: true }).waitFor()
    const selectionCalls = calls.length
    await organizeDialog.getByRole('checkbox', { name: '选择目录 show 及下级', exact: true }).check()
    await organizeDialog.getByRole('button', { name: '排除目录 show 及下级', exact: true }).click()
    assert.equal(await organizeDialog.locator('[data-organize-file]').count(), 0)
    await organizeDialog.getByText('所有目录均已排除，可在上方恢复。', { exact: true }).waitFor()
    assert.deepEqual(await stableLayout(), expandedLayout)
    const excludedSummary = organizeDialog.locator('summary').filter({ hasText: '查看已排除目录（1）' })
    await excludedSummary.focus(); await page.keyboard.press('Enter')
    await organizeDialog.getByRole('button', { name: '恢复目录 /show', exact: true }).click()
    assert.equal(await excludedSummary.count(), 0)
    assert.equal(await organizeDialog.locator('[data-organize-file]').count(), 3)
    assert.deepEqual(await organizeDialog.locator('[data-organize-file] input[type="checkbox"]').evaluateAll(inputs => inputs.map(input => input.checked)), [false, false, false])
    assert.equal(calls.length, selectionCalls)
    passed.push('no filter toolbar; excluding all directories keeps recovery accessible; keyboard restore remains unselected without requests or dialog resize')
    await page.getByRole('button', { name: '下载并自动整理', exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: '下载并自动整理', exact: true }).isDisabled(), true)
    assert.equal(await organizeDialog.getByRole('button', { name: '查找字幕', exact: true }).isDisabled(), true, 'subtitle mapping still requires selected videos')
    if (assrtMode) {
      const second = fileRow(1)
      await second.getByRole('checkbox').check()
      await second.getByRole('spinbutton', { name: 'E030.mkv 集', exact: true }).fill('1')
      assert.equal(await organizeDialog.locator('.confirmed-group').count(), 0)
      await organizeDialog.getByRole('button', { name: '查找字幕', exact: true }).click()
      const picker = page.locator('dialog.subtitle-picker'), result = picker.getByRole('button', { name: /百变之星 BD 中文字幕/ })
      await result.waitFor()
      await picker.getByRole('button', { name: /百变之星.*2003/ }).waitFor()
      assert.equal(count('/subtitles/assrt/123/download'), 0)
      assrtDownloadFail = true
      await result.click()
      await picker.getByRole('alert').filter({ hasText: '射手下载配额不足' }).waitFor()
      assert.equal(await picker.locator('.match-files > li').count(), 0)
      assrtDownloadFail = false
      await result.click()
      await picker.getByText(/^已选 2 个字幕/).waitFor()
      assert.equal(await picker.locator('.match-files > li').count(), 2)
      await picker.getByText('射手网 · 射手测试组', { exact: true }).first().waitFor()
      await result.click()
      assert.equal(count('/subtitles/assrt/123/download'), 2, 'loaded Assrt result must not download again')
      await picker.getByLabel('选择本地字幕或压缩包', { exact: true }).setInputFiles({ name: '01.en.srt', mimeType: 'text/plain', buffer: Buffer.from('english') })
      await picker.getByText(/^已选 3 个字幕/).waitFor()
      await picker.getByRole('button', { name: '移除本地字幕', exact: true }).click()
      await picker.getByText(/^已选 2 个字幕/).waitFor()
      assrtFail = true
      await picker.getByRole('textbox', { name: '字幕作品搜索词', exact: true }).fill('故障测试')
      await picker.getByRole('button', { name: '搜索', exact: true }).click()
      await picker.getByText(/射手网：.*配额/).waitFor()
      await picker.getByText(/^已选 2 个字幕/).waitFor()
      assrtFail = false
      await picker.getByRole('textbox', { name: '字幕作品搜索词', exact: true }).fill('慢字幕库')
      await picker.getByRole('button', { name: '搜索', exact: true }).click()
      await result.waitFor()
      await result.click()
      assert.equal(count('/subtitles/assrt/123/download'), 2, 'slow library must not block Assrt result')
      releaseLibrary()
      await picker.getByRole('button', { name: /百变之星.*2003/ }).waitFor()
      const artifact = path.join(os.tmpdir(), 'cinema115-assrt-ui'); await fs.mkdir(artifact, { recursive: true })
      for (const [width, theme] of [[1440, 'light'], [390, 'light'], [320, 'dark']]) {
        await page.setViewportSize({ width, height: 900 })
        await page.evaluate(t => document.documentElement.classList.toggle('dark', t === 'dark'), theme)
        assert.equal(await picker.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
        await page.screenshot({ path: path.join(artifact, `picker-${width}-${theme}.png`) })
      }
      assert.equal(count('/download-organize/subtitles/attachments'), 0)
      await picker.getByRole('checkbox', { name: '选择字幕 01.cht.ass', exact: true }).uncheck()
      await picker.getByRole('button', { name: '使用所选字幕', exact: true }).click()
      await organizeDialog.getByText('外部字幕 1 个 · 射手网 1 个', { exact: true }).waitFor()

      await organizeDialog.getByRole('button', { name: '查找字幕', exact: true }).click()
      await picker.getByText(/^已选 1 个字幕/).waitFor()
      await result.click()
      await picker.getByText(/^已选 2 个字幕/).waitFor()
      assert.equal(count('/subtitles/assrt/123/download'), 3, 'saved subset must not prevent restoring the complete package')
      await picker.getByRole('button', { name: '使用所选字幕', exact: true }).click()
      await page.setViewportSize({ width: 1440, height: 1000 })

      await organizeDialog.getByRole('button', { name: '查找字幕', exact: true }).click()
      await picker.getByRole('button', { name: /百变之星.*2003/ }).click()
      await picker.getByRole('button', { name: '移除全部', exact: true }).click()
      await picker.getByRole('button', { name: /百变之星.*2003/ }).waitFor()
      assert.equal(await picker.locator('.match-files > li').count(), 0, 'clear returns to results instead of a blank body')
      await page.keyboard.press('Escape')

      await organizeDialog.getByRole('button', { name: '下载并自动整理', exact: true }).click()
      await organizeDialog.getByRole('button', { name: '确认任务状态', exact: true }).waitFor()
      await organizeDialog.getByRole('button', { name: '确认任务状态', exact: true }).click()
      await organizeDialog.getByText('任务已创建', { exact: true }).waitFor()
      assert.equal(count('/download-organize/subtitles/attachments'), 1)
      const preview = calls.find(c => c.endpoint.endsWith('/download-organize/preview')).body
      assert.equal(preview.subtitles.length, 2)
      assert.equal(preview.subtitles[0].videoId, files[1].id)
      assert.ok(preview.subtitles.every(sub => sub.localId && sub.sha256 && !sub.url))
      assert.deepEqual(errors, [])
      console.log(JSON.stringify({ passed: ['selection required; merged search; independent slow/error source', 'real ZIP extraction; local removal retains Assrt; repeat clicks reuse files; saved subset can restore full package', 'source labels and mobile/dark layout; clear returns to results', 'same attachment submission and lost receipt recovery'], artifact }, null, 2)); return
    }
    if (localSubtitleMode) {
      const { zipSync, strToU8 } = require('fflate')
      const first = fileRow(0)
      const second = fileRow(1)
      await first.getByRole('checkbox').check()
      await second.getByRole('checkbox').check()
      const imported = page.locator('dialog.subtitle-picker')
      const picker = () => imported.getByLabel('选择本地字幕或压缩包', { exact: true })
      const subtitleRow = name => imported.locator('.match-files > li').filter({ has: page.getByRole('checkbox', { name: `选择字幕 ${name}`, exact: true }) })
      // The original file is E030; only the user-confirmed editor number is 1.
      await second.getByRole('spinbutton', { name: 'E030.mkv 集', exact: true }).fill('1')
      assert.equal(await organizeDialog.getByRole('button', { name: '导入字幕', exact: true }).count(), 0, 'one outer subtitle entry')
      await stashSelection()
      await organizeDialog.getByRole('button', { name: '查找字幕', exact: true }).click()
      await imported.getByText('没有找到匹配字幕', { exact: true }).waitFor()
      assert.equal(await imported.getByRole('button', { name: '导入字幕', exact: true }).isEnabled(), true)
      const artifact = path.join(os.tmpdir(), 'cinema115-local-subtitles-ui')
      await fs.mkdir(artifact, { recursive: true })
      for (const [width, theme] of [[1440, 'light'], [390, 'light'], [320, 'dark']]) {
        await page.setViewportSize({ width, height: 900 })
        await page.evaluate(t => document.documentElement.classList.toggle('dark', t === 'dark'), theme)
        assert.equal(await imported.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
        await page.screenshot({ path: path.join(artifact, `empty-${width}-${theme}.png`) })
      }
      await page.setViewportSize({ width: 1440, height: 1000 })
      await picker().setInputFiles(['01.sc.ass', '01.tc.ass'].map(name => ({ name, mimeType: 'text/plain', buffer: Buffer.from('subtitle') })))
      await imported.getByText(/共 2 个字幕，0 个未匹配/).waitFor()
      for (const [name, language] of [['01.sc.ass', 'zh-CN'], ['01.tc.ass', 'zh-TW']]) {
        const item = subtitleRow(name)
        assert.equal(await item.getByRole('checkbox').isChecked(), true)
        assert.equal(await item.getByRole('combobox', { name: /对应视频/ }).inputValue(), files[1].id)
        assert.equal(await item.getByRole('combobox', { name: /语言/ }).inputValue(), language)
        await item.getByText(/整理后：.*S01E01/).waitFor()
      }
      await imported.getByRole('textbox', { name: '字幕作品搜索词', exact: true }).fill('故障测试')
      await imported.getByRole('button', { name: '搜索', exact: true }).click()
      await imported.getByText('模拟字幕库异常', { exact: true }).waitFor()
      assert.equal(await imported.getByRole('button', { name: '导入字幕', exact: true }).isEnabled(), true)
      assert.equal(await imported.locator('.match-files > li').count(), 2, 'search failure preserves local mappings')
      await imported.getByRole('textbox', { name: '字幕作品搜索词', exact: true }).fill('百变之星')
      emptySubtitles = false
      await imported.getByRole('button', { name: '搜索', exact: true }).click()
      await imported.getByRole('button', { name: /百变之星.*2003/ }).waitFor()
      assert.equal(await imported.getByRole('button', { name: '导入字幕', exact: true }).isEnabled(), true)
      assert.equal(await imported.locator('.match-files > li').count(), 2, 'search preserves imported mappings')
      // Manual correction must survive reopening, including after target edits.
      await subtitleRow('01.tc.ass').getByRole('combobox', { name: /对应视频/ }).selectOption(files[0].id)
      await imported.getByRole('button', { name: '使用所选字幕', exact: true }).click()
      await editStaged()
      await second.getByRole('spinbutton', { name: 'E030.mkv 集', exact: true }).fill('2')
      await stashSelection()
      await organizeDialog.getByRole('button', { name: '查找字幕', exact: true }).click()
      assert.equal(await subtitleRow('01.tc.ass').getByRole('combobox', { name: /对应视频/ }).inputValue(), files[0].id)
      assert.equal(await subtitleRow('01.sc.ass').getByRole('combobox', { name: /对应视频/ }).inputValue(), files[1].id)
      await subtitleRow('01.sc.ass').getByText(/整理后：.*S01E02/).waitFor()
      await imported.getByRole('button', { name: '移除全部', exact: true }).click()
      await imported.getByRole('button', { name: '使用所选字幕', exact: true }).click()
      await editStaged()
      await second.getByRole('spinbutton', { name: 'E030.mkv 集', exact: true }).fill('30')
      await stashSelection()
      await organizeDialog.getByRole('button', { name: '查找字幕', exact: true }).click()
      assert.equal(await page.locator('dialog[open]').count(), 3)
      const archive = zipSync({
        '字幕组/Show [30].chs.ass': strToU8('[Script Info]\nTitle: first'),
        '字幕组/Show [30].cht.ass': strToU8('[Script Info]\nTitle: traditional'),
        '字幕组/Show [SP01].ass': strToU8('[Script Info]\nTitle: special'),
        '字幕组/readme.txt': strToU8('ignored'),
      })
      const zip = { name: 'Group.zip', mimeType: 'application/zip', buffer: Buffer.from(archive) }
      await picker().setInputFiles(zip)
      await imported.getByText(/共 3 个字幕，1 个未匹配/).waitFor()
      assert.equal(await imported.getByRole('checkbox', { name: '选择字幕 字幕组/Show [30].chs.ass', exact: true }).isChecked(), true)
      const special = subtitleRow('字幕组/Show [SP01].ass')
      assert.equal(await special.getByRole('checkbox').isChecked(), false)
      await special.getByRole('combobox', { name: /对应视频/ }).selectOption(files[0].id)
      await special.getByRole('checkbox').check()
      await picker().setInputFiles(zip)
      await imported.getByText(/已跳过 3 个重复字幕/).waitFor()
      // Invalid archives leave all mappings intact; RAR/7z use the real WASM worker.
      await picker().setInputFiles({ name: 'broken.zip', mimeType: 'application/zip', buffer: Buffer.from('broken') })
      await imported.getByRole('alert').waitFor()
      assert.equal(await imported.locator('.match-files > li').count(), 3)
      for (const [index, name] of ['normal.7z', 'normal-v5.rar'].entries()) {
        await picker().setInputFiles(path.join(__dirname, '../tests/fixtures/subtitle-archives', name))
        await imported.getByText(new RegExp(`共 ${index + 4} 个字幕`)).waitFor()
      }
      // A second local version must carry an explicit marker to avoid overwrite.
      await picker().setInputFiles({ name: 'Another [30].chs.ass', mimeType: 'text/plain', buffer: Buffer.from('another version') })
      await imported.getByText(/共 6 个字幕/).waitFor()
      await imported.getByText(/多个字幕的目标名称相同/).waitFor()
      const another = subtitleRow('Another [30].chs.ass')
      await another.getByRole('textbox', { name: /版本/ }).fill('Alt')
      await imported.getByText(/多个字幕的目标名称相同/).waitFor({ state: 'detached' })
      for (const [width, theme] of [[1440, 'light'], [390, 'light'], [320, 'dark']]) {
        await page.setViewportSize({ width, height: 900 })
        await page.evaluate(t => document.documentElement.classList.toggle('dark', t === 'dark'), theme)
        assert.equal(await imported.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
        const apply = imported.getByRole('button', { name: '使用所选字幕', exact: true })
        await apply.scrollIntoViewIfNeeded()
        const bounds = await apply.boundingBox()
        assert.ok(bounds.y >= 0 && bounds.y + bounds.height <= 900, 'footer remains accessible')
        await page.screenshot({ path: path.join(artifact, `local-${width}-${theme}.png`) })
      }
      assert.equal(count('/download-organize/subtitles/attachments'), 0)
      assert.equal(count('/download-organize/jobs'), 0)
      await imported.getByRole('button', { name: '使用所选字幕', exact: true }).click()
      await imported.waitFor({ state: 'detached' })
      await editStaged()
      const external = second.locator('details.subtitle-details')
      await external.locator('summary').click()
      const beforeName = await external.locator('.subtitle-target').first().innerText()
      await second.getByRole('spinbutton', { name: 'E030.mkv 集', exact: true }).fill('31')
      await page.waitForTimeout(700)
      assert.notEqual(await external.locator('.subtitle-target').first().innerText(), beforeName)
      await stashSelection()
      await organizeDialog.getByRole('button', { name: '查找字幕', exact: true }).click()
      assert.equal(await imported.locator('.match-files > li').count(), 4, 'only selected local files are retained')
      await page.keyboard.press('Escape')
      await imported.waitFor({ state: 'detached' })
      assert.equal(await organizeDialog.isVisible(), true)
      assert.equal(await organizeDialog.getByRole('button', { name: '查找字幕', exact: true }).evaluate(el => el === document.activeElement), true)
      await page.setViewportSize({ width: 1440, height: 1000 })
      const start = organizeDialog.getByRole('button', { name: '下载并自动整理', exact: true })

      await start.click()
      await organizeDialog.getByText('模拟附件回执丢失', { exact: true }).waitFor()
      assert.equal(count('/download-organize/jobs'), 0)
      await organizeDialog.getByRole('button', { name: '确认下载并整理', exact: true }).click()
      await organizeDialog.getByRole('button', { name: '确认任务状态', exact: true }).click()
      await organizeDialog.getByText('任务已创建', { exact: true }).waitFor()
      assert.equal(count('/download-organize/subtitles/attachments'), 2, 'creation receipt retry must not upload again')
      assert.equal(count('/download-organize/preview'), 1)
      const preview = calls.find(c => c.endpoint.endsWith('/download-organize/preview')).body
      assert.equal(preview.subtitles.length, 4)
      for (const sub of preview.subtitles) { assert.match(sub.localId, /^[a-f0-9]{32}$/); assert.match(sub.sha256, /^[a-f0-9]{64}$/); assert.equal(sub.data, undefined) }
      assert.equal(preview.subtitles.find(sub => sub.version === 'Alt').videoId, files[1].id)
      assert.deepEqual(errors, [])
      console.log(JSON.stringify({ passed: ['single search entry; empty/error states offer import; searching preserves local mappings', 'confirmed episodes map sc/tc subtitles despite original filename; manual correction survives edits/reopen', 'ZIP/RAR/7z worker extraction', 'unmatched manual mapping', 'append deduplication and invalid archive preserve mappings', 'duplicate target requires version', 'desktop/mobile/dark no overflow', 'nested Escape preserves parent and focus', 'final subtitle name follows video edits', 'metadata-only preview, upload only at Start', 'attachment and creation lost receipts retry same task'], artifact }, null, 2))
      return
    }
    if (subtitleMode) {
      assert.equal(count('/subtitles/search'), 0, 'opening organizer must not fetch catalogue')
      const second = fileRow(1)
      await second.getByRole('checkbox').check()
      await second.getByRole('spinbutton', { name: 'E030.mkv 集', exact: true }).fill('1')
      await stashSelection()
      await organizeDialog.getByRole('button', { name: '查找字幕', exact: true }).click()
      const library = page.locator('dialog.subtitle-picker')
      await library.getByRole('button', { name: /百变之星.*2003/ }).click()
      await library.getByText(/^已选 1 个字幕/).waitFor()
      assert.equal(await library.getByRole('checkbox').count(), 3)
      assert.equal(await library.getByRole('button', { name: '导入字幕', exact: true }).isEnabled(), true)
      const input = library.getByLabel('选择本地字幕或压缩包', { exact: true })
      await input.setInputFiles(['01.chs.ass', '01.en.srt'].map(name => ({ name, mimeType: 'text/plain', buffer: Buffer.from('local subtitle') })))
      await library.getByText(/多个字幕的目标名称相同/).waitFor()
      const localRow = name => library.locator('.match-files > li').filter({ has: page.getByRole('checkbox', { name: `选择字幕 ${name}`, exact: true }) })
      await localRow('01.chs.ass').getByRole('textbox', { name: /版本/ }).fill('Local')
      await library.getByText(/多个字幕的目标名称相同/).waitFor({ state: 'detached' })
      await library.getByText(/^已选 3 个字幕/).waitFor()
      await library.getByRole('button', { name: '返回搜索结果', exact: true }).click()
      const versionCalls = count('/subtitles/versions/v1/files')
      assert.equal(await library.locator('.match-files > li').count(), 5, 'returning to results keeps both sources')
      await library.getByRole('button', { name: /百变之星.*2003/ }).click()
      assert.equal(count('/subtitles/versions/v1/files'), versionCalls, 'reselecting loaded version reuses files')
      assert.equal(await localRow('01.chs.ass').getByRole('textbox', { name: /版本/ }).inputValue(), 'Local')
      await library.getByRole('button', { name: '使用所选字幕', exact: true }).click()
      await organizeDialog.getByText('，外部字幕 3 个', { exact: true }).waitFor()

      await organizeDialog.getByRole('button', { name: '查找字幕', exact: true }).click()
      await library.getByText(/^已选 3 个字幕/).waitFor()
      assert.equal(await localRow('01.chs.ass').getByRole('textbox', { name: /版本/ }).inputValue(), 'Local')
      await localRow('01.chs.ass').getByRole('textbox', { name: /版本/ }).fill('Unsaved')
      await library.getByRole('checkbox', { name: '选择字幕 01.sc.ass', exact: true }).uncheck()
      await page.keyboard.press('Escape')
      await organizeDialog.getByText('，外部字幕 3 个', { exact: true }).waitFor()

      await organizeDialog.getByRole('button', { name: '查找字幕', exact: true }).click()
      await library.getByText(/^已选 3 个字幕/).waitFor()
      assert.equal(await localRow('01.chs.ass').getByRole('textbox', { name: /版本/ }).inputValue(), 'Local', 'cancel does not change saved selections')
      await library.getByRole('button', { name: '移除本地字幕', exact: true }).click()
      await library.getByText(/^已选 1 个字幕/).waitFor()
      await library.getByRole('button', { name: '使用所选字幕', exact: true }).click()
      assert.equal(await library.count(), 0)
      await organizeDialog.getByText('，外部字幕 1 个', { exact: true }).waitFor()
      await editStaged()
      const external = second.locator('details.subtitle-details')
      await external.locator('summary').click()
      await external.getByText(/整理后：.*zh-CN.ass/).waitFor()
      const beforeName = await external.locator('.subtitle-target').innerText()
      await second.getByRole('spinbutton', { name: 'E030.mkv 集', exact: true }).fill('31')
      await page.waitForTimeout(700)
      assert.notEqual(await external.locator('.subtitle-target').innerText(), beforeName)
      const afterCalls = count('/subtitles/versions/v1/files')
      await second.getByRole('checkbox').uncheck()
      assert.equal(await organizeDialog.getByText('，外部字幕 1 个', { exact: true }).count(), 0)
      await second.getByRole('checkbox').check()
      assert.equal(count('/subtitles/versions/v1/files'), afterCalls)
      await stashSelection()
      const artifact = path.join(os.tmpdir(), 'cinema115-subtitle-library-ui')
      await fs.mkdir(artifact, { recursive: true })
      for (const [width, theme] of [[1440, 'light'], [390, 'light'], [390, 'dark']]) {
        await page.setViewportSize({ width, height: 900 })
        await page.evaluate(t => document.documentElement.classList.toggle('dark', t === 'dark'), theme)
        await page.screenshot({ path: path.join(artifact, `organize-${width}-${theme}.png`) })

        await organizeDialog.getByRole('button', { name: '查找字幕', exact: true }).click()
        await library.getByRole('button', { name: /百变之星.*2003/ }).click()
        // Saved associations survive target edits; reopening must not rematch them.
        await library.getByRole('button', { name: '关闭字幕选择', exact: true }).waitFor()
        await page.screenshot({ path: path.join(artifact, `library-${width}-${theme}.png`) })
        const layout = await library.evaluate(el => ({ overflow: el.scrollWidth > el.clientWidth + 1, width: el.getBoundingClientRect().width, screen: innerWidth }))
        assert.equal(layout.overflow, false)
        assert.ok(layout.width <= layout.screen)
        await library.getByRole('button', { name: '关闭字幕选择', exact: true }).click()
      }
      await page.setViewportSize({ width: 1440, height: 1000 })

      await organizeDialog.getByRole('button', { name: '下载并自动整理', exact: true }).click()
      const payload = calls.find(c => c.endpoint.endsWith('/download-organize/preview')).body
      assert.equal(payload.subtitles.length, 1)
      assert.equal(payload.subtitles[0].videoId, files[1].id)
      assert.equal(payload.subtitles[0].sha, '1'.repeat(40))
      const libraryCalls = count('/subtitles/search')
      japanese = false
      await page.reload()
      await page.getByRole('button', { name: searchLabel, exact: true }).click()
      await open(row)
      await panel.waitFor()
      assert.equal(await organizeDialog.getByRole('button', { name: '查找字幕', exact: true }).isDisabled(), true, 'all works keep selection required')
      assert.equal(count('/subtitles/search'), libraryCalls)
      await fileRow(1).getByRole('checkbox').check()
      await stashSelection()
      await organizeDialog.getByRole('button', { name: '查找字幕', exact: true }).click()
      await library.getByRole('button', { name: /百变之星.*2003/ }).waitFor()
      assert.equal(count('/subtitles/search'), libraryCalls + 1, 'non-Japanese animation also searches the animation catalogue')
      if (isGying) {
        animation = false; assrtConfigured = true
        await page.reload()
        await page.getByRole('button', { name: searchLabel, exact: true }).click()
        await open(row)
        await panel.waitFor()
        const beforeLibrary = count('/subtitles/search'), beforeAssrt = count('/subtitles/assrt/search')
        const seriesVideo = fileRow(1)
        await seriesVideo.getByRole('checkbox').check()
        await seriesVideo.getByRole('spinbutton', { name: 'E030.mkv 集', exact: true }).fill('1')
        assert.equal(count('/subtitles/assrt/search'), beforeAssrt, 'selecting videos does not search subtitles')
        await stashSelection()
        await organizeDialog.getByRole('button', { name: '查找字幕', exact: true }).click()
        const assrtResult = library.getByRole('button', { name: /百变之星 BD 中文字幕/ })
        await assrtResult.click()
        await library.getByText(/^已选 2 个字幕/).waitFor()
        assert.equal(count('/subtitles/search'), beforeLibrary, 'live-action TV skips animation catalogue')
        assert.equal(await library.getByLabel('01.chs.ass 对应视频', { exact: true }).inputValue(), files[1].id)
        await library.getByRole('button', { name: '使用所选字幕', exact: true }).click()
        await organizeDialog.locator('.confirmation-subtitles').getByText(/外部字幕 2 个/).waitFor()

        // Movie numbering fields are zero; sequel-like subtitle numbers must not
        // be interpreted as episodes. Keep two releases for version selection.
        files.splice(0, files.length, ...['1080p', '2160p'].map(resolution => ({ id: `movie/1917.2019.${resolution}.mkv`, name: `1917.2019.${resolution}.mkv`, sourcePath: `/movie/1917.2019.${resolution}.mkv`, directory: false, parentId: '', size: 0, mediaType: 'video', season: 0, episode: 0, namingValues: { resolution } })))
        subtitles.length = 0
        await page.goto(`${origin}/movie/12501`)
        await page.getByRole('button', { name: searchLabel, exact: true }).click()
        await open(row)
        await panel.waitFor()
        const find = organizeDialog.getByRole('button', { name: '查找字幕', exact: true })
        assert.equal(await find.isDisabled(), true)
        await fileRow(0).getByRole('checkbox').check()
        await stashSelection()
        await find.click()
        await assrtResult.click()
        await library.getByText('字幕已关联当前电影，请勾选要使用的版本。', { exact: true }).waitFor()
        await library.getByText(/^已选 0 个字幕/).waitFor()
        assert.equal(await library.getByLabel('01.chs.ass 对应视频', { exact: true }).inputValue(), files[0].id)
        const subrow = library.locator('.match-files > li').filter({ has: page.getByRole('checkbox', { name: '选择字幕 01.chs.ass', exact: true }) })
        assert.doesNotMatch(await subrow.locator('.match-target').innerText(), /S\d+E\d+/)
        await library.getByRole('checkbox', { name: '选择字幕 01.chs.ass', exact: true }).check()
        await library.getByRole('button', { name: '使用所选字幕', exact: true }).click()

        await find.click()
        await library.getByText(/^已选 1 个字幕/).waitFor()
        assert.equal(await library.getByLabel('01.chs.ass 对应视频', { exact: true }).inputValue(), files[0].id, 'reopening keeps confirmed movie selection')
        await library.getByRole('button', { name: '关闭字幕选择', exact: true }).click()
        await fileRow(1).getByRole('checkbox').check()
        await stashSelection()
        await find.click()
        await library.getByRole('button', { name: '移除全部', exact: true }).click()
        await assrtResult.click()
        await library.getByText('存在多个视频版本，请指定对应视频并勾选字幕。', { exact: true }).waitFor()
        assert.equal(await library.getByLabel('01.chs.ass 对应视频', { exact: true }).inputValue(), '')
        await library.getByText(/^已选 0 个字幕/).waitFor()
        await library.getByLabel('01.chs.ass 对应视频', { exact: true }).selectOption(files[1].id)
        await library.getByRole('checkbox', { name: '选择字幕 01.chs.ass', exact: true }).check()
        for (const width of [390, 1440]) {
          await page.setViewportSize({ width, height: 1000 })
          await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), width === 390)
          assert.equal(await library.evaluate(el => el.scrollWidth <= el.clientWidth), true)
        }
        await library.getByRole('button', { name: '使用所选字幕', exact: true }).click()
        await organizeDialog.locator('.confirmation-subtitles').getByText(/外部字幕 1 个/).waitFor()

        await organizeDialog.getByRole('button', { name: '下载并自动整理', exact: true }).click()
        await page.getByText('任务已创建', { exact: true }).waitFor()
        const moviePayload = calls.filter(c => c.endpoint.endsWith('/download-organize/preview')).at(-1).body
        assert.equal(moviePayload.kind, 'movie')
        assert.equal(moviePayload.subtitles[0].videoId, files[1].id)
        assert.ok(moviePayload.subtitles[0].localId)
        assert.equal(count('/subtitles/search'), beforeLibrary, 'movies never search animation catalogue')
        passed.push('live-action TV and movies use Assrt with no animation requests; movies bypass numbering; single-video confirmation and multi-version manual selection; saved selection, responsive layout and shared attachment submission')
      }
      assert.deepEqual(errors, [])
      console.log(JSON.stringify({ source, passed: ['lazy catalogue', 'shared library/local selection, cross-source collision validation and one apply', 'reselect loaded version without refetch; reopen retains local version; cancel discards edits', 'unique episode mapping; specials stay manual', 'selection follows video', 'renamed subtitle follows final video', 'desktop/mobile/dark no overflow', 'original video identity submitted', 'non-Japanese animation keeps subtitle entry and catalogue', ...passed], artifact }, null, 2))
      return
    }
    const first = fileRow(0), second = fileRow(1)
    assert.equal(await second.getByRole('spinbutton', { name: 'E030.mkv 结束集', exact: true }).count(), 0)
    assert.equal(await first.getByRole('textbox', { name: 'E027-E28.mkv 自定义标记', exact: true }).count(), 0)
    assert.equal(await first.getByRole('spinbutton').count(), 2)
    assert.equal(await first.getByRole('spinbutton', { name: /结束集/ }).count(), 0)
    assert.equal(await first.getByRole('button', { name: /^更多编辑/ }).count(), 0)
    const subtitleToggle = first.locator('.subtitle-details > summary')
    await subtitleToggle.focus(); await page.keyboard.press('Enter')
    assert.equal(await first.getByRole('checkbox').isChecked(), false)
    await first.getByText('随视频选择，未选字幕整理成功后清理').first().waitFor()
    await first.getByRole('checkbox').click(); await second.getByRole('checkbox').click()
    await page.getByText('待处理 3 个视频 · 已勾选 2 个', { exact: true }).waitFor()
    await first.getByRole('checkbox').click()
    await page.getByText('待处理 3 个视频 · 已勾选 1 个', { exact: true }).waitFor()
    await first.getByRole('checkbox').click()
    await first.getByText('整理后：百变之星.S01E27-测试组.mkv', { exact: true }).waitFor()
    assert.equal(await organizeDialog.getByRole('button', { name: /^暂存这 [1-9]\d* 项$/ }).isEnabled(), true)
    assert.equal(await page.getByRole('button', { name: '下载并自动整理', exact: true }).isEnabled(), true, 'checked files can be submitted without staging')
    assert.equal(await organizeDialog.getByRole('button', { name: '查找字幕', exact: true }).isEnabled(), true, 'checked files can find subtitles without staging')
    if (shareMode) {
      assert.equal(await organizeDialog.locator('.confirmed-group').count(), 0)
      const shareButton = page.getByRole('button', { name: '115 分享 STRM', exact: true })
      const original = page.getByRole('button', { name: '下载并自动整理', exact: true })
      assert.equal(await shareButton.isDisabled(), true)
      shareConfigured = true; omitOrganizeDirectory = true
      await page.getByRole('button', { name: '重新读取配置', exact: true }).click()
      await shareButton.waitFor()
      await page.waitForFunction(() => ![...document.querySelectorAll('button')].find(b => b.textContent.trim() === '115 分享 STRM').disabled)
      assert.equal(await original.isDisabled(), true, 'only ordinary organize requires a target directory')
      const a = await shareButton.boundingBox(), b = await original.boundingBox()
      assert.ok(a.x < b.x, 'secondary share action must precede original action')
      for (const theme of ['light', 'dark']) {
        await page.setViewportSize({ width: 320, height: 740 })
        await page.evaluate(theme => document.documentElement.classList.toggle('dark', theme === 'dark'), theme)
        assert.equal(await organizeDialog.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
        assert.ok((await shareButton.boundingBox()).height >= 40)
        if (process.env.CINEMA_UI_SHOTS) await page.screenshot({ path: path.join(process.env.CINEMA_UI_SHOTS, `share-organize-mobile-${theme}.png`) })
      }

      await shareButton.click()
      const confirm = page.getByRole('button', { name: '确认任务状态', exact: true })
      await confirm.waitFor()
      assert.equal(await original.isDisabled(), true)
      holdJob = true
      await confirm.click()
      await page.getByRole('button', { name: '正在创建分享任务…', exact: true }).waitFor()
      assert.equal(await original.isDisabled(), true)
      releaseJob()
      await page.getByText('任务已创建', { exact: true }).waitFor()
      assert.equal(await page.getByRole('link', { name: '查看分享 STRM 日志', exact: true }).getAttribute('href'), '/115/share/logs')
      const previews = calls.filter(c => c.endpoint.endsWith('/download-organize/preview'))
      assert.equal(previews.length, 1)
      assert.equal(previews[0].body.shareSTRM, true)
      assert.equal(previews[0].body.targetCID, undefined)
      assert.equal(previews[0].body.targetPath, undefined)
      assert.deepEqual(calls.filter(c => c.endpoint.endsWith('/download-organize/jobs')).map(c => c.body.previewId), ['share-plan', 'share-plan'])
      assert.deepEqual(errors, [])
      console.log(JSON.stringify({ passed: ['share and ordinary directory settings are independent; share works without organize directory; canonical names stay unchanged; mobile light/dark fit; shared lock and lost response preserve the same plan; logs link'], errors }, null, 2))
      return
    }
    const namingCalls = count('/naming/media-preview')
    const badRow = fileRow(2)
    await badRow.getByRole('checkbox').check()
    await badRow.getByRole('alert').waitFor()
    await badRow.getByRole('checkbox').uncheck()
    assert.equal(count('/naming/media-preview'), namingCalls)
    await badRow.getByRole('spinbutton', { name: 'OVA.mkv 集', exact: true }).fill('1.5')
    await page.waitForRequest(req => req.url().endsWith('/naming/media-preview') && req.postDataJSON().files[2].episode === -1)
    await page.getByText('版本 3', { exact: true }).waitFor()
    assert.equal(await organizeDialog.getByRole('button', { name: /^暂存这 [1-9]\d* 项$/ }).isEnabled(), true)
    await badRow.getByRole('spinbutton', { name: 'OVA.mkv 集', exact: true }).fill('0')
    await page.getByText('版本 3', { exact: true }).waitFor()
    await subtitleToggle.click()
    subtitleError = true
    await page.getByRole('button', { name: '刷新命名', exact: true }).click()
    await first.getByRole('alert').filter({ hasText: '字幕名称超过长度限制' }).waitFor()
    assert.equal(await first.locator('.subtitle-details').getAttribute('open'), '')
    assert.equal(await page.getByRole('button', { name: '下载并自动整理', exact: true }).isDisabled(), true)
    subtitleError = false
    await page.getByRole('button', { name: '刷新命名', exact: true }).click()
    await first.getByRole('alert').filter({ hasText: '字幕名称超过长度限制' }).waitFor({ state: 'detached' })
    await subtitleToggle.click()
    const episode = first.getByRole('spinbutton', { name: 'E027-E28.mkv 集', exact: true })
    await episode.fill('26')
    await page.waitForRequest(req => req.url().endsWith('/naming/media-preview') && req.postDataJSON().files[0].episode === 26)
    await episode.fill('25')
    await first.getByText('整理后：百变之星.S01E25-测试组.mkv', { exact: true }).waitFor()
    releaseNaming()
    await episode.fill('27')
    await first.getByText('整理后：百变之星.S01E27-测试组.mkv', { exact: true }).waitFor()
    assert.equal(count('/download-organize/files'), 1)
    passed.push('selection performs no naming requests, unselected invalid rows do not block, stale naming responses are discarded without cloud reads')
    await page.getByRole('combobox', { name: '目标季', exact: true }).click()
    assert.equal(await page.getByRole('listbox', { name: '目标季', exact: true }).evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
    await page.getByRole('option').filter({ hasText: '第 2 季' }).click()
    await page.getByRole('spinbutton', { name: '发布起始集', exact: true }).fill('27')
    await page.getByRole('button', { name: '应用偏移（2 项）', exact: true }).click()
    assert.equal(await first.getByRole('spinbutton', { name: 'E027-E28.mkv 集', exact: true }).inputValue(), '1')
    assert.equal(await second.getByRole('spinbutton', { name: 'E030.mkv 集', exact: true }).inputValue(), '4')
    await first.getByText('整理后：百变之星.S02E01-测试组.mkv', { exact: true }).waitFor()
    await first.getByText('整理后：百变之星.S02E01-测试组.zh-TW.default.srt', { exact: true }).waitFor()
    await episode.fill('2')
    await first.getByText('整理后：百变之星.S02E02-测试组.zh-CN.ass', { exact: true }).waitFor()
    await episode.fill('1')
    assert.equal(await first.locator('.subtitle-details').getAttribute('open'), '')
    await first.getByText('整理后：百变之星.S02E01-测试组.mkv', { exact: true }).waitFor()
    assert.equal(count('/download-organize/preview'), 0)
    await page.getByText('1 个字幕未能唯一匹配，整理成功后清理', { exact: true }).click()
    await page.getByText('/subtitles/unmatched.ass', { exact: true }).waitFor()
    if (process.env.CINEMA_UI_SHOTS) { await fs.mkdir(process.env.CINEMA_UI_SHOTS, { recursive: true }); await page.screenshot({ path: path.join(process.env.CINEMA_UI_SHOTS, 'download-organize-desktop.png') }) }
    await page.getByRole('button', { name: '撤销', exact: true }).click()
    assert.equal(await first.getByRole('spinbutton', { name: 'E027-E28.mkv 集', exact: true }).inputValue(), '27')
    await page.getByRole('button', { name: '应用偏移（2 项）', exact: true }).click()
    assert.equal(await fileRow(2).getByRole('checkbox').isChecked(), false)
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
    await stashSelection()
    await page.getByRole('button', { name: '下载并自动整理', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '命名或目标目录存在冲突' }).waitFor()
    await showStaged(0)
    await stagedRow(0).getByRole('alert').waitFor()
    await editStaged()
    assert.equal(count('/download-organize/jobs'), 0)
    await episode.fill('2')
    assert.equal(await first.locator('.subtitle-details').getAttribute('open'), null)
    await episode.fill('1')
    conflict = false
    await first.getByText('整理后：百变之星.S02E01-测试组.mkv', { exact: true }).waitFor()
    namingRevision = 4; staleTemplate = true
    await stashSelection()
    await page.getByRole('button', { name: '下载并自动整理', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '命名模板已更新' }).waitFor()
    await page.getByText('版本 4', { exact: true }).waitFor()
    assert.equal(count('/download-organize/jobs'), 0)
    renamed = true

    await page.getByRole('button', { name: '下载并自动整理', exact: true }).click()
    await page.getByRole('status').filter({ hasText: '最终命名有变化' }).waitFor()
    await showStaged(0)
    await stagedRow(0).getByText('整理后：新名称.S02E01-测试组.mkv', { exact: true }).waitFor()
    assert.equal(count('/download-organize/jobs'), 0)
    passed.push('template revision conflicts refresh preview; changed final names require a second explicit confirmation')
    renamed = false
    holdJob = true
    const jobRequest = page.waitForRequest('**/download-organize/jobs')
    await page.getByRole('button', { name: '确认下载并整理', exact: true }).click()
    await jobRequest
    assert.equal(await page.getByRole('button', { name: '关闭下载并整理', exact: true }).isDisabled(), true)
    await page.keyboard.press('Escape')
    await page.mouse.click(8, 8)
    assert.equal(await page.locator('dialog[open]').count(), 2)
    holdJob = false
    releaseJob()
    await page.getByRole('button', { name: '确认任务状态', exact: true }).waitFor()
    assert.equal(await organizeDialog.getByRole('button', { name: '退回编辑第 2 季', exact: true }).isDisabled(), true)
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
      const mobileLayout = await stableLayout()
      await organizeDialog.getByRole('button', { name: /^展开或折叠/ }).first().click()
      assert.deepEqual(await stableLayout(), mobileLayout)
      await organizeDialog.getByRole('button', { name: /^展开或折叠/ }).first().click()
      const mobileFirst = fileRow(0)
      await mobileFirst.getByRole('checkbox').click()
      await mobileFirst.locator('.subtitle-details > summary').click()
      await mobileFirst.getByText('整理后：百变之星.S01E27-测试组.zh-TW.default.srt', { exact: true }).waitFor()
      assert.equal(await organizeDialog.locator('.anime-floating-body').evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
      assert.equal(await organizeDialog.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
      await organizeDialog.getByRole('button', { name: '批量编号', exact: true }).click()
      await organizeDialog.getByRole('combobox', { name: '目标季', exact: true }).click()
      assert.equal(await organizeDialog.getByRole('listbox', { name: '目标季', exact: true }).evaluate(el => el.scrollWidth <= el.clientWidth + 1 && el.getBoundingClientRect().left >= 0 && el.getBoundingClientRect().right <= innerWidth), true)
      await page.keyboard.press('Escape')
      assert.equal(await organizeDialog.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
      const controls = await organizeDialog.locator('.mapping-bar input, .mapping-bar [role="combobox"]').evaluateAll(elements => elements.map(el => el.getBoundingClientRect().height))
      assert.ok(controls.every(height => height >= 36), 'compact mobile numbering controls stay usable')
      await organizeDialog.getByRole('button', { name: '批量编号', exact: true }).click()
      assert.deepEqual(await organizeDialog.evaluate(el => [...el.querySelectorAll('*')].filter(node => ['auto', 'scroll'].includes(getComputedStyle(node).overflowY) && node.scrollHeight > node.clientHeight).map(node => node.className)), ['anime-floating-body'])
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
    await fileRow(0).getByRole('checkbox').click()
    assert.equal(await organizeDialog.locator('.confirmed-group').count(), 0)
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
    if (source === 'garden' && !process.argv[3]) {
      const baseCleanupJob = {
        id: 'cleanup-recovery', title: '任务目录清理回归', status: 'completed', resumable: true,
        createdAt: '2026-10-31T00:00:00Z', message: '所选文件已整理，临时目录清理失败，可重试清理',
        config: { accountId: 'primary', sourceCID: '310', sourcePath: '/下载/garden-cleanup-recovery', targetCID: '20', targetPath: '/动漫' },
        download: { hash: '1'.repeat(40), directoryId: '310', cleanupDirs: ['310'] },
        items: [{ file: { id: '901', name: 'Show.mkv', sourcePath: '/下载/garden-cleanup-recovery/Show.mkv', mediaType: 'video', size: 1200 }, name: 'Show.S01E01.mkv', targetPath: '/动漫/Show.S01E01.mkv', targetCID: '20', status: 'done' }],
      }
      let cleanupJob = structuredClone(baseCleanupJob)
      const cleanupEndpoints = /\/api\/admin\/p115\/(?:accounts|download-organize\/(?:jobs|resume))$/
      const cleanupWrites = []
      const recordCleanupWrite = request => {
        if (request.method() !== 'GET' && new URL(request.url()).pathname.startsWith('/api/')) cleanupWrites.push({ endpoint: new URL(request.url()).pathname, body: request.postDataJSON() })
      }
      const cleanupRoute = async route => {
        const req = route.request(), endpoint = new URL(req.url()).pathname, body = req.postDataJSON()
        calls.push({ endpoint, body })
        let data
        if (endpoint.endsWith('/accounts')) data = { accounts: [{ id: 'primary', name: '主账号', uid: '123', configured: true, primary: true, revision: 1 }] }
        else if (endpoint.endsWith('/download-organize/jobs')) data = { jobs: [cleanupJob] }
        else {
          assert.equal(req.method(), 'POST')
          assert.deepEqual(body, { jobId: cleanupJob.id })
          cleanupJob.download.cleanupDirs = []; cleanupJob.resumable = false; cleanupJob.message = '所选文件已整理，本任务临时目录已回收'
          data = cleanupJob
        }
        await route.fulfill({ status: endpoint.endsWith('/resume') ? 202 : 200, contentType: 'application/json', body: JSON.stringify(data) })
      }
      await page.route(cleanupEndpoints, cleanupRoute)
      page.on('request', recordCleanupWrite)
      try {
        for (const pending of ['subtitle', 'share', 'cleanup']) {
          cleanupJob = structuredClone(baseCleanupJob)
          if (pending === 'subtitle') cleanupJob.download.subtitles = [{ file: { name: 'Show.ass', path: 'subs/Show.ass' }, name: 'Show.zh-CN.ass', targetPath: '/动漫/Show.zh-CN.ass', language: 'zh-CN', status: 'uncertain', videoIndex: 0 }]
          if (pending === 'share') cleanupJob.download.share = { directory: 'Cinema115-cleanup-recovery', handedOff: false }
          await page.reload()
          await page.getByRole('button', { name: `查看${cleanupJob.title}的详情`, exact: true }).click()
          const details = page.getByRole('dialog', { name: '整理历史详情', exact: true })
          if (pending !== 'cleanup') {
            assert.equal(await details.getByRole('button', { name: '检查并继续', exact: true }).isEnabled(), true)
            assert.equal(await details.getByRole('button', { name: '重试清理', exact: true }).count(), 0)
          } else {
            await details.getByText('文件已整理，临时目录尚未清理完成', { exact: true }).waitFor()
            const resumed = page.waitForResponse('**/download-organize/resume')
            await details.getByRole('button', { name: '重试清理', exact: true }).click()
            await resumed
            await details.getByText('所选文件已整理，本任务临时目录已回收', { exact: true }).waitFor()
            await details.getByRole('button', { name: '重试清理', exact: true }).waitFor({ state: 'detached' })
            assert.equal(await details.getByRole('button', { name: '检查并继续', exact: true }).count(), 0)
          }
          await details.getByRole('button', { name: '关闭详情', exact: true }).click()
        }
        assert.deepEqual(cleanupWrites, [{ endpoint: '/api/admin/p115/download-organize/resume', body: { jobId: baseCleanupJob.id } }])
        passed.push('completed cleanup retries use the original resume endpoint only; completed retry removes the action; pending subtitles/share retain normal recovery')
      } finally {
        page.off('request', recordCleanupWrite)
        await page.unroute(cleanupEndpoints, cleanupRoute)
      }
    }
    if (source === 'garden') {
      nestedMode = true
      await page.goto(`${origin}/tv/12501`)
      await page.getByRole('button', { name: searchLabel, exact: true }).click()
      await open(row)
      await panel.waitFor()
      assert.deepEqual(await organizeDialog.locator('[data-organize-file]').evaluateAll(rows => rows.map(row => row.dataset.organizeFile)), [nestedFiles[2].id, nestedFiles[1].id, nestedFiles[3].id, nestedFiles[0].id])
      const fold = path => organizeDialog.getByRole('button', { name: `展开或折叠 ${path}`, exact: true })
      await organizeDialog.getByRole('checkbox', { name: '选择目录 pack 及下级', exact: true }).check()
      const locate = organizeDialog.getByRole('button', { name: /项待处理 · 定位问题/ })
      await locate.filter({ hasText: '2 项待处理' }).waitFor()
      assert.equal(await fold('pack').locator('.directory-issues').textContent(), '2 项待处理')
      for (const path of ['pack/Season 01/Extras/Featurettes', 'pack/Season 01/Extras', 'pack/Season 01', 'pack/Season 02/Extras/Featurettes', 'pack/Season 02/Extras', 'pack/Season 02', 'pack']) await fold(path).click()
      assert.equal(await organizeDialog.locator('[data-organize-file]').count(), 0)
      const requests = calls.length
      await locate.click()
      const issueA = organizeDialog.locator(`[data-organize-file="${nestedFiles[2].id}"]`)
      const issueB = organizeDialog.locator(`[data-organize-file="${nestedFiles[3].id}"]`)
      await issueA.waitFor()
      assert.equal(await issueA.getByRole('spinbutton', { name: 'Bonus-A.mkv 集', exact: true }).evaluate(el => el === document.activeElement), true)
      assert.equal(await issueB.count(), 0)
      await locate.click()
      await issueB.waitFor()
      assert.equal(await issueB.getByRole('spinbutton', { name: 'Bonus-B.mkv 集', exact: true }).evaluate(el => el === document.activeElement), true)
      assert.equal(calls.length, requests)
      if (process.env.CINEMA_UI_SHOTS) await page.screenshot({ path: path.join(process.env.CINEMA_UI_SHOTS, 'organize-nested-issues-desktop.png') })
      await issueA.getByRole('spinbutton', { name: 'Bonus-A.mkv 集', exact: true }).fill('2')
      await locate.filter({ hasText: '1 项待处理' }).waitFor()
      assert.equal(await fold('pack/Season 01').locator('.directory-issues').count(), 0)
      await issueB.getByRole('checkbox').uncheck()
      await locate.waitFor({ state: 'detached' })
      assert.equal(await organizeDialog.getByRole('button', { name: /^暂存这 [1-9]\d* 项$/ }).isEnabled(), true)
      for (const theme of ['light', 'dark']) {
        await page.setViewportSize({ width: 320, height: 740 })
        await page.evaluate(theme => document.documentElement.classList.toggle('dark', theme === 'dark'), theme)
        await organizeDialog.getByRole('button', { name: '排除目录 pack/Season 01/Extras/Featurettes 及下级', exact: true }).click()
        await organizeDialog.locator('summary').filter({ hasText: '查看已排除目录（1）' }).click()
        assert.equal(await organizeDialog.evaluate(el => el.scrollWidth <= el.clientWidth + 1), true)
        if (process.env.CINEMA_UI_SHOTS) await page.screenshot({ path: path.join(process.env.CINEMA_UI_SHOTS, `organize-nested-mobile-${theme}.png`) })
        const beforeRestore = calls.length
        await organizeDialog.getByRole('button', { name: '恢复目录 /pack/Season 01/Extras/Featurettes', exact: true }).click()
        assert.equal(await issueA.getByRole('checkbox').isChecked(), false)
        assert.equal(calls.length, beforeRestore)
      }
      await page.keyboard.press('Escape')
      nestedMode = false
      passed.push('folders precede local files; deep collapsed errors locate/focus cyclically without requests; corrections clear badges; nested exclude/restore fits 320px light/dark')
    }
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
      const movieFile = fileRow(0)
      await movieFile.getByRole('checkbox').click()
      await movieFile.getByText('整理后：百变之星 (2003)-测试组.mkv', { exact: true }).waitFor()
      assert.equal(await page.getByRole('combobox', { name: '目标季', exact: true }).count(), 0)
      assert.equal(await organizeDialog.locator('.confirmed-group').count(), 0)
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
