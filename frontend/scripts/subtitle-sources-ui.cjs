// Every API request is mocked; tests never read or write a real drive.
const path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises'), assert = require('node:assert/strict')
const { zipSync, strToU8 } = require('fflate')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const passed = [], errors = [], writes = []
  let page
  try {
    await fs.mkdir('test-results', { recursive: true })
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'subtitle-test'))
    page = await context.newPage()
    page.setDefaultTimeout(15000)
    page.on('pageerror', error => errors.push(error.message))
    const normal = await fs.readFile('tests/fixtures/subtitle-archives/normal.7z')
    const encrypted = await fs.readFile('tests/fixtures/subtitle-archives/encrypted-header.7z')
    const rar = await fs.readFile('tests/fixtures/subtitle-archives/normal-v4.rar')
    const zip = Buffer.from(zipSync({ 'S01E02.ass': strToU8('subtitle two'), 'font.ttf': strToU8('font') }))
    const config = { accountId: 'main', sourceCID: '10', sourcePath: '/来源', targetCID: '20', targetPath: '/影视库' }
    const videos = [1, 2].map(i => ({ id: String(100 + i), name: `Show.S01E0${i}.mkv`, directory: false, size: 100, parentId: '20', path: `/影视库/Show.S01E0${i}.mkv` }))
    const job = { id: 'job', title: '网盘字幕测试', config, status: 'completed', createdAt: '2026-10-15T01:00:00Z', items: videos.map(video => ({ file: { ...video, mediaType: 'video' }, targetPath: video.path, targetCID: '20', status: 'done' })) }
    const entries = [
      { id: '600', name: 'normal.7z', size: normal.length, directory: false },
      { id: '601', name: 'plain.zip', size: zip.length, directory: false },
      { id: '602', name: 'encrypted.7z', size: encrypted.length, directory: false },
      { id: '603', name: 'normal.rar', size: rar.length, directory: false },
      { id: '604', name: 'subtitles.part1.rar', size: 1, directory: false },
      { id: '605', name: 'too-large.zip', size: 65 * 1024 * 1024, directory: false },
    ]
    const snapshots = new Map(), releases = [], downloads = []
    let serial = 0, slow = false, preview, uploadCount = 0
    await page.route('**/api/**', async route => {
      const request = route.request(), url = new URL(request.url()), endpoint = url.pathname
      if (endpoint.includes('/subtitles/sources')) assert.equal(request.headers().authorization, 'Bearer subtitle-test')
      if (request.method() !== 'GET') writes.push(`${request.method()} ${endpoint}`)
      let data = {}, status = 200
      if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
      else if (endpoint.endsWith('/accounts')) data = { accounts: [{ id: 'main', name: '主账号', primary: true, configured: true }] }
      else if (endpoint.endsWith('/manual-organize/config')) data = config
      else if (endpoint.endsWith('/manual-organize/jobs')) data = { jobs: [job] }
      else if (endpoint.endsWith('/manual-organize/cleanup') || endpoint.endsWith('/manual-organize/subtitles')) data = { jobs: [] }
      else if (endpoint.endsWith('/manual-organize/files')) {
        const cid = url.searchParams.get('cid'), offset = Number(url.searchParams.get('offset'))
        if (cid === '0') data = { files: [{ id: '10', name: '字幕来源', directory: true, size: 0 }], count: 1, offset: 0 }
        else { assert.equal(cid, '10'); data = { files: entries.slice(offset, offset + 2), count: entries.length, offset } }
      }
      else if (endpoint.endsWith('/subtitles/videos')) data = { videos }
      else if (endpoint.endsWith('/subtitles/sources') && request.method() === 'POST') {
        const input = request.postDataJSON(); assert.equal(input.accountId, 'main'); assert.equal(input.jobId, 'job')
        const id = `source-${++serial}`; snapshots.set(id, input.files); data = { id, files: input.files }
      }
      else if (/\/subtitles\/sources\/[^/]+$/.test(endpoint) && request.method() === 'DELETE') {
        releases.push(endpoint.split('/').pop()); status = 204
      }
      else if (/\/subtitles\/sources\/[^/]+\/files\//.test(endpoint)) {
        const id = endpoint.split('/').pop(); downloads.push(id)
        if (slow) await new Promise(resolve => setTimeout(resolve, 1500))
        const body = { '600': normal, '601': zip, '602': encrypted, '603': rar }[id]
        await route.fulfill({ status: 200, contentType: 'application/octet-stream', headers: { 'Content-Length': String(body.length) }, body }).catch(() => {})
        return
      }
      else if (endpoint.endsWith('/subtitles/preview')) {
        const input = request.postDataJSON()
        preview = { id: 'preview', status: 'preview', accountId: 'main', jobId: 'job', createdAt: new Date().toISOString(), items: input.files.map(file => ({ ...file, video: videos.find(v => v.id === file.videoId), name: 'Show.S01E01.ass', targetPath: '/影视库/Show.S01E01.ass', status: 'pending' })) }
        data = preview
      }
      else if (endpoint.endsWith('/subtitles/import')) {
        uploadCount++
        const body = request.postDataBuffer().toString()
        assert.match(body, /filename="Show.S01E01.ass"/)
        assert.doesNotMatch(body, /filename="[^\"]*\.(rar|7z|zip)"/i)
        data = { ...preview, status: 'completed', message: '字幕导入完成', items: preview.items.map(item => ({ ...item, status: 'done' })) }
      }
      await route.fulfill({ status, contentType: 'application/json', body: status === 204 ? '' : JSON.stringify(data) })
    })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/115/manual-organize', { waitUntil: 'networkidle' })
    await page.getByRole('button', { name: '查看网盘字幕测试的详情', exact: true }).click()
    await page.getByRole('button', { name: '补充字幕', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '补充字幕 · 网盘字幕测试', exact: true })
    await page.waitForFunction(() => { const input = document.querySelector('dialog[aria-labelledby="subtitle-title"] input[type=file]'); return input && !input.matches(':disabled') })
    const open = async () => {
      await dialog.getByRole('button', { name: '从 115 网盘选择', exact: true }).click()
      const picker = page.getByRole('dialog', { name: '从 115 网盘选择字幕', exact: true })
      await picker.getByRole('button', { name: '字幕来源', exact: true }).click()
      await picker.getByRole('checkbox', { name: '选择 normal.7z', exact: true }).waitFor()
      return picker
    }
    await dialog.locator('input[type=file]').setInputFiles({ name: 'S01E01.srt', mimeType: 'text/plain', buffer: Buffer.from('local subtitle') })
    await dialog.getByRole('combobox', { name: 'S01E01.srt 对应视频', exact: true }).selectOption('101')
    await dialog.getByRole('combobox', { name: 'S01E01.srt 语言', exact: true }).selectOption('en')
    await dialog.getByRole('textbox', { name: 'S01E01.srt 版本', exact: true }).fill('keep-edit')
    let picker = await open()
    await page.keyboard.press('Escape')
    await picker.waitFor({ state: 'detached' })
    assert.equal(await dialog.isVisible(), true)
    assert.equal(await page.evaluate(() => document.body.style.overflow), 'hidden')
    assert.equal(await dialog.getByRole('button', { name: '从 115 网盘选择', exact: true }).evaluate(el => document.activeElement === el), true)
    passed.push('nested picker Escape closes only the picker, restores focus and retains scroll lock')

    picker = await open()
    assert.equal(await picker.getByRole('checkbox', { name: '选择 subtitles.part1.rar', exact: true }).isDisabled(), true)
    assert.equal(await picker.getByRole('checkbox', { name: '选择 too-large.zip', exact: true }).isDisabled(), true)
    await picker.getByRole('checkbox', { name: '选择 normal.7z', exact: true }).check()
    await picker.getByRole('checkbox', { name: '选择 plain.zip', exact: true }).check()
    await picker.getByRole('button', { name: '返回上级', exact: true }).click()
    await picker.getByRole('button', { name: '字幕来源', exact: true }).click()
    await picker.getByRole('checkbox', { name: '选择 plain.zip', exact: true }).waitFor()
    assert.equal(await picker.getByRole('checkbox', { name: '选择 plain.zip', exact: true }).isChecked(), true)
    assert.equal(await picker.getByRole('checkbox', { name: '选择 normal.7z', exact: true }).isChecked(), true)
    await picker.getByRole('button', { name: '下载并解析 2 个文件', exact: true }).click()
    await dialog.getByRole('checkbox', { name: '导入 episode01.chs.srt', exact: true }).waitFor()
    await dialog.getByRole('checkbox', { name: '导入 S01E02.ass', exact: true }).waitFor()
    assert.equal(await dialog.getByRole('checkbox', { name: /^导入 / }).count(), 3)
    assert.equal(await dialog.getByRole('combobox', { name: 'S01E01.srt 语言', exact: true }).inputValue(), 'en')
    assert.equal(await dialog.getByRole('textbox', { name: 'S01E01.srt 版本', exact: true }).inputValue(), 'keep-edit')
    assert.equal(uploadCount, 0)
    passed.push('paginated directory selection, cross-directory selection retention, ZIP and real 7z Worker extraction, append preserves edits')

    picker = await open()
    await picker.getByRole('checkbox', { name: '选择 normal.7z', exact: true }).check()
    await picker.getByRole('checkbox', { name: '选择 plain.zip', exact: true }).check()
    await picker.getByRole('button', { name: '下载并解析 2 个文件', exact: true }).click()
    await dialog.getByText(/跳过 2 个重复字幕/).waitFor()
    assert.equal(await dialog.getByRole('checkbox', { name: /^导入 / }).count(), 3)
    await dialog.getByRole('radio', { name: '替换当前列表', exact: true }).check()
    picker = await open()
    await picker.getByRole('checkbox', { name: '选择 encrypted.7z', exact: true }).check()
    await picker.getByRole('button', { name: '下载并解析 1 个文件', exact: true }).click()
    await dialog.getByRole('alert').filter({ hasText: /加密/ }).waitFor()
    assert.equal(await dialog.getByRole('checkbox', { name: /^导入 / }).count(), 3)
    assert.equal(await dialog.getByRole('textbox', { name: 'S01E01.srt 版本', exact: true }).inputValue(), 'keep-edit')
    passed.push('duplicate archives do not add rows; encrypted archive rejection keeps old rows even in replace mode')

    slow = true
    picker = await open()
    await picker.getByRole('checkbox', { name: '选择 normal.7z', exact: true }).check()
    await picker.getByRole('button', { name: '下载并解析 1 个文件', exact: true }).click()
    await dialog.getByRole('status').filter({ hasText: /正在下载/ }).waitFor()
    await dialog.getByRole('button', { name: '取消读取', exact: true }).click()
    await dialog.getByText('已取消读取，原有字幕列表和人工匹配已保留。', { exact: true }).waitFor()
    assert.equal(await dialog.getByRole('checkbox', { name: /^导入 / }).count(), 3)
    slow = false
    passed.push('download cancellation restores controls, releases snapshot and retains manual edits')

    for (const width of [390, 320]) {
      await page.setViewportSize({ width, height: 844 })
      picker = await open()
      await picker.getByRole('checkbox', { name: '选择 normal.7z', exact: true }).check()
      await picker.getByText('查看已选文件（切换目录保留勾选）', { exact: true }).click()
      const bounds = await picker.boundingBox()
      assert(bounds.x >= -1 && bounds.x + bounds.width <= width + 1)
      assert.equal(await picker.evaluate(el => el.scrollWidth <= el.clientWidth), true)
      const button = await picker.getByRole('button', { name: '下载并解析 1 个文件', exact: true }).boundingBox()
      assert(button.y >= 0 && button.y + button.height <= 845)
      await picker.screenshot({ path: `test-results/subtitle-sources-${width}.png` })
      await page.keyboard.press('Escape')
    }
    await page.setViewportSize({ width: 1440, height: 1000 })
    passed.push('390px and 320px picker fits viewport, expanded selection and confirmation remain reachable')

    picker = await open()
    await picker.getByRole('checkbox', { name: '选择 normal.rar', exact: true }).check()
    await picker.getByRole('button', { name: '下载并解析 1 个文件', exact: true }).click()
    await dialog.getByRole('checkbox', { name: '导入 example/readm.ass', exact: true }).waitFor()
    assert.equal(await dialog.getByRole('checkbox', { name: /^导入 / }).count(), 1)
    await dialog.getByRole('combobox', { name: 'example/readm.ass 对应视频', exact: true }).selectOption('101')
    await dialog.getByRole('button', { name: '预览字幕导入', exact: true }).click()
    await dialog.getByText('/影视库/Show.S01E01.ass', { exact: false }).waitFor()
    assert.equal(uploadCount, 0)
    await dialog.getByRole('button', { name: '确认上传 1 个字幕', exact: true }).click()
    await dialog.getByText(/字幕导入完成/).waitFor()
    assert.equal(uploadCount, 1)
    await dialog.getByRole('button', { name: '关闭字幕导入', exact: true }).click()
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    assert.equal(releases.length, snapshots.size)
    assert.equal(errors.length, 0, errors.join('\n'))
    passed.push('real RAR Worker extraction, successful explicit replacement, preview and only confirmed subtitle bytes upload')
    console.log(JSON.stringify({ passed, errors, uploads: uploadCount, snapshots: snapshots.size, released: releases.length, downloads: downloads.length }, null, 2))
  } catch (error) { console.error({ errors, dialogs: await page?.locator('dialog').allInnerTexts() }); await page?.screenshot({ path: 'test-results/subtitle-sources-failure.png' }); throw error }
  finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
