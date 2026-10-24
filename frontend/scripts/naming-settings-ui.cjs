// Isolated UI contract fixture: no requests reach the real configuration or cloud APIs.
// Preview strings are canned; Go HTTP/engine tests verify actual path rendering.
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const copy = value => JSON.parse(JSON.stringify(value))
const fields = [
  ['title', '作品名称', 'work'], ['originalTitle', '原语种名称', 'work'], ['englishTitle', '英文名称', 'work'], ['originalName', '原文件名', 'work'], ['year', '年份', 'work'], ['tmdbId', 'TMDB ID', 'work'],
  ['season', '季号', 'episode', true], ['episode', '集号', 'episode', true], ['seasonEpisode', '季集编号', 'episode'], ['episodeTitle', '集名', 'episode'],
  ['resolution', '分辨率', 'video'], ['source', '资源来源', 'video'], ['platform', '发布平台', 'video'], ['videoCodec', '视频编码', 'video'], ['bitDepth', '位深', 'video'], ['dynamicRange', '动态范围', 'video'], ['frameRate', '帧率', 'video'],
  ['audioCodec', '音频编码', 'audio'], ['channels', '声道', 'audio'], ['audioEffect', '音频制式', 'audio'], ['languages', '字幕语言', 'release'], ['subtitleMode', '字幕形式', 'release'], ['releaseGroup', '字幕组或发布组', 'release'], ['edition', '版本', 'release'], ['custom', '自定义标记', 'release'],
].map(([id, label, group, numeric]) => ({ id, label, group, ...(numeric ? { numeric } : {}) }))
const defaults = Object.fromEntries(['movie', 'tv'].map(kind => {
  const year = { field: 'year', separator: ' ', prefix: '(', suffix: ')' }
  return [kind, { kind, directories: [[{ field: 'title' }, year, { field: 'tmdbId', separator: ' ', prefix: '{tmdb-', suffix: '}' }], ...(kind === 'tv' ? [[{ field: 'season', prefix: 'Season ', pad: 2 }]] : [])], filename: [
    { field: 'title' }, kind === 'tv' ? { field: 'seasonEpisode', separator: '.' } : year,
    ...['resolution', 'source', 'platform', 'videoCodec', 'bitDepth', 'dynamicRange', 'frameRate', 'audioCodec', 'channels', 'audioEffect', 'languages', 'subtitleMode', 'edition'].map(field => ({ field, separator: '.' })),
    { field: 'releaseGroup', separator: '-' }, { field: 'custom', separator: '-' },
  ] }]
}))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
  const page = await context.newPage(), calls = [], errors = [], passed = []
  const records = Object.fromEntries(['movie', 'tv'].map(kind => [kind, { revision: 0, template: copy(defaults[kind]) }]))
  const state = { failRead: false, conflict: false, loseSave: false, rejectPreview: false, previewGate: null, saveGate: null, writes: 0, preview: 0 }
  page.setDefaultTimeout(10000); page.on('pageerror', error => errors.push(error.message))
  await page.route('**/api/**', async route => {
    const req = route.request(), endpoint = new URL(req.url()).pathname, method = req.method(), body = req.postDataJSON()
    calls.push({ endpoint, method, body }); let data = {}, status = 200
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
    else if (endpoint === '/api/admin/naming') {
      if (state.failRead) { status = 503; data = { error: '模拟读取失败' } }
      else data = { records: copy(records), defaults, catalog: { movie: fields.filter(field => field.group !== 'episode'), tv: fields } }
    } else if (endpoint === '/api/admin/naming/preview') {
      const number = ++state.preview, gate = state.previewGate, rejected = state.rejectPreview
      if (gate) await gate
      if (rejected) { status = 400; data = { error: '无效命名模板：文件名缺少作品名称' } }
      else {
        const tv = body.template.kind === 'tv', stem = `示例${tv ? '剧集.S01E13-E14' : '电影 (2026)'}.2160p.WEB-DL.NF.HEVC.10bit.DV HDR10.24fps.DDP.5.1.Atmos.简繁.外挂.特别版-字幕组-收藏版`
        data = { items: [{ result: { directory: '示例作品 (2026) {tmdb-123456}', stem, extension: '.mkv', name: `${stem}.mkv`, relativePath: `示例作品 (2026) {tmdb-123456}/${tv ? 'Season 01/' : ''}${stem}.mkv [预览${number}]` } }] }
      }
    } else if (/^\/api\/admin\/naming\/(movie|tv)$/.test(endpoint)) {
      if (state.saveGate) await state.saveGate
      const kind = endpoint.split('/').at(-1), current = records[kind]
      if (state.conflict) { state.conflict = false; status = 409; data = { error: '模板已在其他页面修改，请重新读取后保存', code: 'REVISION_CONFLICT' } }
      else if (current.revision === body.revision + 1 && JSON.stringify(current.template) === JSON.stringify(body.template)) data = current
      else {
        assert.equal(current.revision, body.revision)
        records[kind] = { revision: body.revision + 1, template: copy(body.template) }; state.writes++; data = records[kind]
        if (state.loseSave) { state.loseSave = false; return route.abort() }
      }
    }
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
  })
  const save = page.getByRole('button', { name: '保存模板', exact: true })
  const movie = page.locator('.ns-kind button').nth(0), tv = page.locator('.ns-kind button').nth(1)
  const filename = page.getByRole('region', { name: '文件名', exact: true })
  const title = () => filename.getByRole('button', { name: '编辑文件名的作品名称', exact: true })
  const prefix = () => page.getByRole('textbox', { name: '前缀', exact: true })
  const previewCalls = () => calls.filter(call => call.endpoint.endsWith('/preview'))
  const putCalls = () => calls.filter(call => call.method === 'PUT')
  const settled = () => page.waitForFunction(() => document.querySelector('.ns-preview')?.getAttribute('aria-busy') === 'false' && !!document.querySelector('.ns-preview-content code'))
  const push = url => page.evaluate(url => { void document.querySelector('#app').__vue_app__.config.globalProperties.$router.push(url) }, url)
  const editPrefix = async text => { await title().click(); await prefix().fill(text); await settled() }
  try {
    await fs.mkdir('test-results', { recursive: true })
    await page.goto(origin + '/settings/naming', { waitUntil: 'networkidle' }); await settled()
    assert.equal(await save.isDisabled(), true)
    assert.equal(calls.filter(call => call.endpoint === '/api/admin/naming').length, 1)
    assert.equal(await page.locator('a[href="/settings/naming"]').count() > 0, true)
    await page.screenshot({ path: 'test-results/naming-desktop-light.png', fullPage: true })
    passed.push('独立命名模板入口，读取一次本地配置，初始无修改禁止保存')

    await title().click(); const composing = previewCalls().length
    await prefix().evaluate(input => {
      input.dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true }))
      input.value = 'di'; input.dispatchEvent(new InputEvent('input', { bubbles: true, data: 'di', isComposing: true }))
    })
    await page.waitForTimeout(350); assert.equal(previewCalls().length, composing)
    await prefix().evaluate(input => { input.value = '第'; input.dispatchEvent(new CompositionEvent('compositionend', { bubbles: true, data: '第' })) })
    await settled(); assert.equal(previewCalls().at(-1).body.template.filename[0].prefix, '第')
    const before = previewCalls().length
    await prefix().fill('电'); await prefix().fill('电影'); await prefix().fill('电影 '); await settled()
    assert.equal(previewCalls().length, before + 1)
    await tv.click(); await settled(); await editPrefix('剧集 ')
    await movie.click(); await settled(); await title().click(); assert.equal(await prefix().inputValue(), '电影 ')
    await page.getByRole('button', { name: '重新读取当前模板', exact: true }).click()
    const reload = page.getByRole('dialog', { name: '重新读取电影模板？', exact: true })
    await reload.getByRole('button', { name: '重新读取', exact: true }).click(); await settled()
    await tv.click(); await settled(); await title().click(); assert.equal(await prefix().inputValue(), '剧集 ')
    passed.push('快速输入合并一次预览；电影/剧集保留各自编辑，重读仅替换当前类型')

    await page.getByRole('button', { name: '添加原语种名称到文件名', exact: true }).click(); await settled()
    const added = filename.getByRole('button', { name: '编辑文件名的原语种名称', exact: true })
    await added.click(); const moveBack = page.getByRole('button', { name: '前移', exact: true })
    await moveBack.focus(); await moveBack.press('Enter'); assert.equal(await moveBack.evaluate(el => el === document.activeElement), true)
    await added.press('Alt+ArrowLeft'); await settled()
    let body = previewCalls().at(-1).body.template.filename
    assert.equal(body.at(-3).field, 'originalTitle')
    await filename.getByRole('button', { name: '编辑文件名的分辨率', exact: true }).dragTo(title()); await settled()
    assert.equal(previewCalls().at(-1).body.template.filename[0].field, 'resolution')
    await filename.getByRole('button', { name: '编辑文件名的分辨率', exact: true }).dragTo(title()); await settled()
    assert.equal(previewCalls().at(-1).body.template.filename[1].field, 'resolution')
    await added.press('Delete'); await settled(); assert.equal(await added.count(), 0)
    assert.equal(await filename.getByText('原扩展名', { exact: true }).count(), 1)
    await save.click(); await page.getByText('剧集模板已保存', { exact: true }).waitFor(); assert.equal(state.writes, 1)
    passed.push('字段点击添加、桌面拖动、按钮和键盘排序与删除，扩展名固定，保存仅更新当前类型')

    await title().click(); state.rejectPreview = true; await page.getByRole('button', { name: '移除字段', exact: true }).click()
    await page.getByText('无效命名模板：文件名缺少作品名称', { exact: true }).waitFor(); assert.equal(await save.isDisabled(), true)
    state.rejectPreview = false; await page.getByRole('button', { name: '添加作品名称到文件名', exact: true }).click(); await settled()
    state.conflict = true; await save.click(); await page.getByText('模板已在其他页面修改，请重新读取后保存', { exact: true }).waitFor()
    assert.equal(await save.isEnabled(), true)
    await page.locator('.ns-actions').getByRole('button', { name: '恢复默认', exact: true }).click()
    const reset = page.getByRole('dialog', { name: '恢复剧集默认模板？', exact: true })
    await page.keyboard.press('Escape'); await reset.waitFor({ state: 'detached' }); assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    await page.locator('.ns-actions').getByRole('button', { name: '恢复默认', exact: true }).click()
    await reset.getByRole('button', { name: '恢复默认', exact: true }).click(); await settled()
    passed.push('模板校验失败阻止保存，冲突保留编辑；恢复默认须确认，Esc关闭释放滚动锁')

    let releaseSave; state.saveGate = new Promise(resolve => { releaseSave = resolve })
    state.loseSave = true; await save.click()
    await page.getByRole('button', { name: '保存中…', exact: true }).waitFor()
    assert.equal(await title().isDisabled(), true); await push('/logs'); assert.equal(new URL(page.url()).pathname, '/settings/naming')
    releaseSave(); state.saveGate = null
    const confirm = page.getByRole('button', { name: '确认保存结果', exact: true }); await confirm.waitFor()
    const frozen = copy(putCalls().at(-1)); assert.equal(await movie.isDisabled(), true); assert.equal(await title().isDisabled(), true)
    await push('/settings/naming'); assert.equal(new URL(page.url()).searchParams.get('kind'), 'tv')
    await push('/logs'); assert.equal(new URL(page.url()).pathname, '/settings/naming')
    await confirm.click(); await confirm.waitFor({ state: 'detached' }); assert.deepEqual(putCalls().at(-1), frozen); assert.equal(state.writes, 2)
    passed.push('保存丢回执锁定编辑与导航，原正文核对，不重复写入')

    let release; state.previewGate = new Promise(resolve => { release = resolve })
    const oldCount = previewCalls().length
    await title().click(); await prefix().fill('旧预览')
    assert.equal(await page.locator('.ns-preview-content code').count(), 1)
    await page.waitForTimeout(350); assert.equal(previewCalls().length, oldCount + 1)
    state.previewGate = null; await prefix().fill('新预览'); await settled()
    const lastPreview = await page.locator('.ns-preview-content code').innerText(); release()
    await page.waitForTimeout(100); assert.equal(await page.locator('.ns-preview-content code').innerText(), lastPreview)
    passed.push('取消并忽略过期预览响应')

    for (const width of [1440, 320, 390]) {
      await page.setViewportSize({ width, height: 900 })
      for (const dark of [false, true]) {
        await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
        assert.equal(await page.locator('.nt-layer').first().evaluate(el => getComputedStyle(el).backgroundColor === 'rgb(255, 255, 255)'), !dark)
        assert.equal(await page.locator('.ns-preview').evaluate(el => getComputedStyle(el).backgroundColor === 'rgb(255, 255, 255)'), !dark)
        if (width < 600) assert.equal(await page.locator('.ns-preview').evaluate(el => getComputedStyle(el).position), 'static')
        if (dark && width === 1440) {
          const action = page.getByRole('button', { name: '移除字段', exact: true }); await action.hover()
          assert.equal(await action.evaluate(el => getComputedStyle(el).color !== getComputedStyle(el).backgroundColor), true)
        }
        if (width < 600) {
          assert.equal(await page.locator('.ns-preview-content').evaluate(el => el.scrollWidth > el.clientWidth), true)
          await page.locator('.ns-preview-content').evaluate(el => { el.scrollLeft = el.scrollWidth }); assert.equal(await page.locator('.ns-preview-content').evaluate(el => el.scrollLeft > 0), true)
        }
        await page.evaluate(() => scrollTo(0, 0))
        await page.screenshot({ path: `test-results/naming-${width}-${dark ? 'dark' : 'light'}.png`, fullPage: true })
      }
    }
    passed.push('桌面及320/390px深浅主题无页面横向溢出，完整路径内部滚动')
    await page.setViewportSize({ width: 1440, height: 1000 })
    await push('/logs')
    const leave = page.getByRole('dialog', { name: '放弃未保存的模板修改？', exact: true })
    await leave.getByRole('button', { name: '继续编辑', exact: true }).click(); assert.equal(new URL(page.url()).pathname, '/settings/naming')
    await push('/logs'); await leave.getByRole('button', { name: '放弃修改并离开', exact: true }).click()
    await page.waitForURL('**/logs'); assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    state.failRead = true; await push('/settings/naming'); await page.getByText('模拟读取失败', { exact: true }).waitFor()
    records.movie.template.directories = null // Valid Go representation of a template with no directory levels.
    state.failRead = false; await page.getByRole('button', { name: '重新读取设置', exact: true }).click(); await settled()
    assert.equal(await page.locator('.nt-layer').count(), 1); assert.equal(await save.isDisabled(), true)
    passed.push('离开前保护未保存修改，失败重读可恢复')
    assert.equal(calls.some(call => /115|cloud-download|tmdb|anime/.test(call.endpoint)), false)
    assert.deepEqual(errors, []); console.log(JSON.stringify({ passed, apiCalls: calls.length, previewCalls: state.preview, writes: state.writes, errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
