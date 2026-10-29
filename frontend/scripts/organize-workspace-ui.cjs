// Isolated navigation fixture. All business requests are mocked; no NAS or 115 operations.
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os'), { createHash } = require('node:crypto')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const hash = text => createHash('sha256').update(text).digest('hex')
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
  const defaults = await fs.readFile('../backend/internal/mediacategory/default.yaml', 'utf8')
  let classification = { yaml: defaults, revision: hash(defaults), activeRevision: hash(defaults) }, loseSave = false
  const templates = Object.fromEntries(['movie', 'tv'].map(kind => [kind, { kind, directories: [[{ field: 'title' }]], filename: [{ field: 'title' }, ...(kind === 'tv' ? [{ field: 'seasonEpisode', separator: '.' }] : [])] }]))
  const fields = [{ id: 'title', label: '作品名称', group: 'work' }, { id: 'seasonEpisode', label: '季集编号', group: 'episode' }]
  const rule = { id: 'rule1', name: '目录监控', storage: 'local', source: '/media/inbox', target: '/media/library', classify: true, enabled: true, intervalMinutes: 5 }
  const calls = [], errors = [], passed = []
  let holdPreview, previewStarted, holdSave, saveStarted
  await context.route('**/api/**', async route => {
    const req = route.request(), endpoint = new URL(req.url()).pathname, method = req.method(), body = req.postDataJSON()
    calls.push({ endpoint, method, body }); let data = {}
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
    else if (endpoint === '/api/admin/media-organize') data = { rules: [rule], active: true, activeRules: [], cleanup: [], history: { items: [], page: 1, pages: 1, total: 0 } }
    else if (endpoint === '/api/admin/media-organize/rules') { if (holdSave) { saveStarted(); await holdSave }; Object.assign(rule, body); data = rule }
    else if (endpoint === '/api/admin/classification' && method === 'GET') data = { record: classification, defaultYAML: defaults }
    else if (endpoint === '/api/admin/classification' && method === 'PUT') {
      classification = { yaml: body.yaml, revision: hash(body.yaml), activeRevision: hash(body.yaml) }; data = classification
      if (loseSave) { loseSave = false; return route.abort() }
    } else if (endpoint === '/api/admin/naming') data = { records: Object.fromEntries(['movie', 'tv'].map(kind => [kind, { revision: 1, template: templates[kind] }])), defaults: templates, catalog: { movie: [fields[0]], tv: fields } }
    else if (endpoint === '/api/admin/naming/preview') {
      if (holdPreview) { previewStarted(); await holdPreview }
      data = { items: [{ result: { directory: '示例作品', stem: '示例作品', extension: '.mkv', name: '示例作品.mkv', relativePath: '示例作品/示例作品.mkv' } }] }
    }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
  })
  const page = await context.newPage(); page.setDefaultTimeout(10000); page.on('pageerror', error => errors.push(error.message))
  const nav = page.getByRole('navigation', { name: '整理功能', exact: true })
  const tab = label => nav.getByRole('link', { name: label, exact: true })
  const count = endpoint => calls.filter(call => call.endpoint === endpoint).length
  const push = url => page.evaluate(url => { void document.querySelector('#app').__vue_app__.config.globalProperties.$router.push(url) }, url)
  const settledNaming = () => page.waitForFunction(() => document.querySelector('.ns-preview')?.getAttribute('aria-busy') === 'false')
  const shell = () => page.evaluate(() => document.querySelector('.management-shell') === window.organizeShell)
  try {
    await fs.mkdir('test-results', { recursive: true })
    await page.goto(origin + '/media/organize'); await page.locator('.mo-rule-card').waitFor()
    assert.equal(await page.getByRole('heading', { level: 1 }).innerText(), '自动整理')
    assert.deepEqual(await nav.getByRole('link').allTextContents(), ['整理管理', '命名模板', '分类规则'])
    assert.equal(count('/api/admin/naming'), 0); assert.equal(count('/api/admin/classification'), 0)
    assert.equal(await page.locator('.management-navigation a[href="/settings/naming"], .management-navigation a[href="/settings/classification"]').count(), 0)
    assert.equal(await page.locator('.mo-section a').count(), 0)
    await page.evaluate(() => { window.organizeShell = document.querySelector('.management-shell') })
    const bounds = await nav.boundingBox()
    await page.screenshot({ path: 'test-results/organize-workspace-management.png', fullPage: true })

    const mediaReads = count('/api/admin/media-organize')
    await tab('命名模板').click(); await settledNaming()
    assert.equal(await shell(), true); assert.equal(await page.locator('.mo-page').count(), 0)
    assert.equal(count('/api/admin/classification'), 0)
    await page.waitForTimeout(2800); assert.equal(count('/api/admin/media-organize'), mediaReads)
    await page.locator('.ns-kind button').nth(1).click(); await settledNaming()
    assert.equal(new URL(page.url()).searchParams.get('kind'), 'tv')
    await tab('命名模板').click(); assert.equal(new URL(page.url()).searchParams.get('kind'), 'tv')
    assert.equal(count('/api/admin/naming'), 1)
    assert.equal((await nav.boundingBox()).y, bounds.y)
    passed.push('共用标题和侧栏；只挂载当前标签，切换停止整理轮询；模板类型切换不重读，点击当前标签保留query')

    await page.getByRole('button', { name: '编辑文件名的作品名称', exact: true }).click()
    await page.getByRole('textbox', { name: '前缀', exact: true }).fill('未保存'); await settledNaming()
    await page.evaluate(() => {
      window.organizeNavigationSettled = false
      void document.querySelector('#app').__vue_app__.config.globalProperties.$router.push('/media/organize/classification').then(() => { window.organizeNavigationSettled = true })
    })
    const namingLeave = page.getByRole('dialog', { name: '放弃未保存的模板修改？', exact: true })
    await namingLeave.waitFor()
    await push('/media/organize')
    await namingLeave.getByRole('button', { name: '继续编辑', exact: true }).click()
    await page.waitForFunction(() => window.organizeNavigationSettled)
    assert.equal(new URL(page.url()).pathname, '/media/organize/naming'); assert.equal(count('/api/admin/classification'), 0)
    await tab('分类规则').click(); await namingLeave.getByRole('button', { name: '放弃修改并离开', exact: true }).click()
    await page.locator('.cr-rule').first().waitFor(); assert.equal(await shell(), true)
    assert.equal(await page.locator('.management-nav-link[aria-current]').getAttribute('href'), '/media/organize')
    await page.getByRole('textbox', { name: '规则名称', exact: true }).fill('未保存分类')
    await tab('整理管理').click()
    const classificationLeave = page.getByRole('dialog', { name: '放弃未保存的修改？', exact: true })
    await classificationLeave.getByRole('button', { name: '取消', exact: true }).click()
    assert.equal(await tab('分类规则').getAttribute('aria-current'), 'page')
    assert.equal(await page.getByRole('textbox', { name: '规则名称', exact: true }).inputValue(), '未保存分类')
    loseSave = true; await page.getByRole('button', { name: '保存规则', exact: true }).click()
    await page.getByRole('button', { name: '确认保存结果', exact: true }).waitFor()
    await tab('整理管理').click()
    await page.getByRole('dialog', { name: '放弃确认并离开？', exact: true }).getByRole('button', { name: '取消', exact: true }).click()
    await page.getByRole('button', { name: '确认保存结果', exact: true }).click()
    await page.getByText('电影和剧集分类规则已保存并生效', { exact: true }).waitFor()
    passed.push('标签切换复用命名/分类未保存确认；取消保留当前页及表单，未知保存回执保持原请求')

    await tab('整理管理').click(); await page.locator('.mo-rule-card').waitFor()
    await page.goBack(); await page.locator('.cr-rule').first().waitFor()
    await page.goForward(); await page.locator('.mo-rule-card').waitFor()
    assert.equal(await shell(), true); assert.equal(await page.getByRole('heading', { level: 1 }).count(), 1)
    await page.reload(); await page.locator('.mo-rule-card').waitFor()
    await page.evaluate(() => { window.organizeShell = document.querySelector('.management-shell') })

    await page.getByRole('button', { name: '编辑目录监控', exact: true }).click()
    const ruleEditor = page.getByRole('dialog', { name: '整理规则', exact: true })
    await ruleEditor.getByRole('textbox', { name: '规则名称', exact: true }).fill('目录监控修改')
    await push('/media/organize/naming')
    const mediaLeave = page.getByRole('dialog', { name: '放弃未保存的整理修改？', exact: true })
    await mediaLeave.getByRole('button', { name: '继续编辑', exact: true }).click()
    assert.equal(await ruleEditor.getByRole('textbox', { name: '规则名称', exact: true }).inputValue(), '目录监控修改')
    let releaseSave
    holdSave = new Promise(resolve => { releaseSave = resolve }); const saving = new Promise(resolve => { saveStarted = resolve })
    await ruleEditor.getByRole('button', { name: '保存规则', exact: true }).click(); await saving
    await push('/media/organize/naming'); assert.equal(new URL(page.url()).pathname, '/media/organize')
    releaseSave(); holdSave = undefined; await ruleEditor.waitFor({ state: 'detached' })
    await page.getByRole('button', { name: '编辑目录监控修改', exact: true }).click()
    await ruleEditor.getByRole('textbox', { name: '规则名称', exact: true }).fill('放弃本次修改')
    await push('/media/organize/classification')
    await mediaLeave.getByRole('button', { name: '放弃修改并离开', exact: true }).click()
    await page.locator('.cr-rule').first().waitFor()
    assert.notEqual(await page.evaluate(() => document.body.style.overflow), 'hidden')
    await tab('整理管理').click(); await page.locator('.mo-rule-card').waitFor()
    passed.push('规则编辑离开可取消；提交操作中不切页；后台任务运行不阻止正常切页，前进后退与刷新正确')

    let releasePreview
    holdPreview = new Promise(resolve => { releasePreview = resolve }); const previewReady = new Promise(resolve => { previewStarted = resolve })
    await tab('命名模板').click(); await previewReady
    await tab('分类规则').click(); await page.locator('.cr-rule').first().waitFor()
    releasePreview(); holdPreview = undefined
    assert.equal(await page.locator('.naming-settings').count(), 0)
    assert.equal(await shell(), true)
    passed.push('离开模板页取消未完成预览，过期响应不覆盖新标签')

    for (const width of [1440, 320, 390]) {
      await page.setViewportSize({ width, height: 900 })
      for (const dark of [false, true]) {
        await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
        await tab('命名模板').click(); await settledNaming(); const first = await nav.boundingBox()
        await tab('分类规则').click(); await page.locator('.cr-rule').first().waitFor()
        assert.deepEqual(await nav.boundingBox(), first)
        assert.equal(await nav.locator('[aria-current="page"]').count(), 1)
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
        assert.ok((await tab('分类规则').boundingBox()).height >= 44)
        await page.screenshot({ path: `test-results/organize-workspace-${width}-${dark ? 'dark' : 'light'}.png`, fullPage: true })
      }
    }
    await page.goto(origin + '/settings/naming?kind=tv'); await settledNaming()
    assert.equal(new URL(page.url()).pathname, '/media/organize/naming'); assert.equal(new URL(page.url()).searchParams.get('kind'), 'tv')
    await page.goto(origin + '/settings/classification'); await page.locator('.cr-rule').first().waitFor()
    assert.equal(new URL(page.url()).pathname, '/media/organize/classification')
    passed.push('旧地址重定向保留类型参数；320/390px深浅主题无溢出、导航位置稳定、选中唯一且点击范围达标')
    assert.deepEqual(errors, []); assert.equal(calls.some(call => /p115|cloud-download|anime/.test(call.endpoint)), false)
    console.log(JSON.stringify({ passed, errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
