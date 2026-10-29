// Isolated browser fixture. Every API request is mocked; real rules and cloud files are untouched.
const assert = require('node:assert/strict'), fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os'), { createHash } = require('node:crypto')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const hash = text => createHash('sha256').update(text).digest('hex')
;(async () => {
  const { parse } = await import('yaml')
  const defaults = await fs.readFile('../backend/internal/mediacategory/default.yaml', 'utf8')
  const initial = defaults.replace('name: 动画电影', 'name: 动画电影 # keep movie comment').replace('name: 动漫', 'name: 动漫 # keep tv comment')
  let record = { yaml: initial, revision: hash(initial), activeRevision: hash(initial) }
  const state = { conflict: false, loseSave: false, reject: false, writes: 0 }, calls = [], errors = [], passed = []
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
  const mock = async route => {
    const request = route.request(), endpoint = new URL(request.url()).pathname, method = request.method(), body = request.postDataJSON()
    calls.push({ endpoint, method, body }); let status = 200, data = {}
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
    else if (endpoint === '/api/admin/classification' && method === 'GET') data = { record, defaultYAML: defaults }
    else if (endpoint === '/api/admin/classification' && method === 'PUT') {
      if (state.conflict) { state.conflict = false; status = 409; data = { error: '模拟冲突' } }
      else if (state.reject) { state.reject = false; status = 400; data = { error: '模拟字段校验失败', diagnostic: { field: 'tv.rules[0].path', line: 25, column: 7, message: '目录不合法' } } }
      else {
        if (record.yaml !== body.yaml) { assert.equal(body.revision, record.revision); state.writes++ }
        record = { yaml: body.yaml, revision: hash(body.yaml), activeRevision: hash(body.yaml) }; data = record
        if (state.loseSave) { state.loseSave = false; return route.abort() }
      }
    } else if (endpoint === '/api/admin/classification/reload') { record.activeRevision = record.revision; data = record }
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
  }
  await context.route('**/api/**', mock)
  const page = await context.newPage(); page.setDefaultTimeout(10000); page.on('pageerror', error => errors.push(error.message))
  const save = page.getByRole('button', { name: '保存规则', exact: true })
  const movie = page.locator('.cs-kind button').nth(0), tv = page.locator('.cs-kind button').nth(1)
  const getCount = () => calls.filter(call => call.endpoint === '/api/admin/classification' && call.method === 'GET').length
  const puts = () => calls.filter(call => call.method === 'PUT')
  const push = url => page.evaluate(url => { void document.querySelector('#app').__vue_app__.config.globalProperties.$router.push(url) }, url)
  const closeEditor = async () => { const button = page.getByRole('button', { name: '完成编辑', exact: true }); if (await button.count()) await button.click() }
  const saveAll = async () => {
    const button = page.getByRole('button', { name: '保存全部规则', exact: true })
    if (await button.count()) await button.click(); else await save.click()
  }
  const reset = async () => {
    await closeEditor()
    await page.locator('.cs-actions').getByRole('button', { name: '恢复默认', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: '恢复默认', exact: true }).click()
  }
  const choose = async (label, value) => {
    await page.getByRole('combobox', { name: label, exact: true }).click()
    await page.getByRole('option', { name: value, exact: true }).click()
  }
  try {
    await fs.mkdir('test-results', { recursive: true })
    await page.goto(`${origin}/settings/classification`); await page.locator('.cr-rule').first().waitFor()
    assert.equal(await page.locator('.cr-rule').count(), 2); assert.equal(await save.isDisabled(), true)
    const reads = getCount()
    const heights = await page.locator('.cr-rule').evaluateAll(items => items.map(item => item.getBoundingClientRect().height))
    await page.locator('.cr-toggle').first().click()
    assert.equal(await page.getByRole('dialog', { name: '电影分类规则', exact: true }).evaluate(el => el.getBoundingClientRect().width), 420)
    assert.deepEqual(await page.locator('.cr-rule').evaluateAll(items => items.map(item => item.getBoundingClientRect().height)), heights)
    await page.getByRole('textbox', { name: '规则名称', exact: true }).fill('日本动画电影')
    await choose('添加匹配条件', '原产地')
    await choose('添加原产地', '日本 JP')
    await choose('切换编辑规则', '2. 华语电影')
    assert.equal(await page.getByRole('textbox', { name: '规则名称', exact: true }).inputValue(), '华语电影')
    await choose('切换编辑规则', '1. 日本动画电影')
    assert.equal(await page.getByRole('button', { name: '移除日本 JP', exact: true }).count(), 1)
    await page.screenshot({ path: 'test-results/classification-desktop-panel.png', fullPage: true })
    await closeEditor(); await tv.click(); await page.locator('.cr-toggle').first().click()
    await page.getByRole('textbox', { name: '目标分类目录', exact: true }).fill('剧集/日漫')
    await closeEditor(); await movie.click(); assert.ok((await page.locator('.cr-name').first().innerText()).includes('日本动画电影'))
    assert.equal(getCount(), reads)
    await save.click(); await page.getByText('电影和剧集分类规则已保存并生效', { exact: true }).waitFor()
    const saved = parse(record.yaml)
    assert.deepEqual(saved.movie.rules[0].match.origin_countries, ['JP']); assert.equal(saved.tv.rules[0].path, '剧集/日漫')
    assert.ok(record.yaml.includes('# keep movie comment')); assert.ok(record.yaml.includes('# keep tv comment'))
    passed.push('跨类型保留编辑、零额外读取；整体保存且保留 YAML 注释')
    passed.push('420px 共用侧窗，窗口内直接切换规则并保留编辑，主列表高度不变')

    await page.getByRole('button', { name: '下移日本动画电影', exact: true }).click()
    assert.equal(await page.locator('.cr-name').last().innerText(), '日本动画电影')
    await page.locator('.cr-toggle').last().press('Alt+ArrowUp')
    assert.equal(await page.locator('.cr-name').first().innerText(), '日本动画电影')
    await page.getByRole('button', { name: '添加规则', exact: true }).click()
    await page.getByRole('textbox', { name: '规则名称', exact: true }).fill('其他类型')
    await page.getByRole('textbox', { name: '目标分类目录', exact: true }).fill('电影/其他类型')
    await choose('添加匹配条件', '类型')
    await page.getByRole('textbox', { name: '自定义类型 ID', exact: true }).fill('12345')
    await page.getByRole('button', { name: '添加自定义类型', exact: true }).click()
    await choose('添加匹配条件', '原始语言')
    const beforeInvalid = puts().length
    await saveAll(); await page.getByRole('alert').filter({ hasText: '每项条件至少选择一个匹配值，或移除该条件' }).waitFor()
    assert.equal(puts().length, beforeInvalid)
    await page.getByRole('button', { name: '移除原始语言条件', exact: true }).click()
    await saveAll(); await page.getByText('电影和剧集分类规则已保存并生效', { exact: true }).waitFor()
    assert.deepEqual(parse(record.yaml).movie.rules.at(-1).match, { genres: [12345] })
    passed.push('规则按钮/键盘排序、自定义匹配值；空条件阻止保存，避免静默扩大匹配范围')

    const grip = page.locator('.cr-grip').last(), first = page.locator('.cr-row').first()
    const a = await grip.boundingBox(), b = await first.boundingBox()
    await page.mouse.move(a.x + a.width / 2, a.y + a.height / 2); await page.mouse.down()
    await page.mouse.move(a.x + a.width / 2 + 12, a.y + a.height / 2, { steps: 3 })
    await page.locator('.drag-list-fallback').waitFor()
    await page.mouse.move(b.x + b.width / 2, b.y + 4, { steps: 15 }); await page.waitForTimeout(250)
    assert.equal(await page.locator('.cr-list > .drag-list-placeholder').count(), 1)
    await page.mouse.up(); await page.locator('.drag-list-fallback').waitFor({ state: 'detached' })
    await page.waitForTimeout(300); assert.equal(await page.locator('.cr-name').first().innerText(), '其他类型')
    assert.equal(getCount(), reads)
    await page.screenshot({ path: 'test-results/classification-desktop.png', fullPage: true })
    passed.push('复用拖动组件，具备插入预览，排序过程不请求后端')

    state.conflict = true; await save.click(); await page.getByText('文件已被其他页面或外部修改。本地编辑已保留，请重新读取后再编辑。', { exact: true }).waitFor()
    assert.equal(await page.locator('.cr-name').first().innerText(), '其他类型')
    await push('/logs'); const leave = page.getByRole('dialog', { name: '放弃未保存的修改？', exact: true }); await leave.waitFor()
    await leave.getByRole('button', { name: '取消', exact: true }).click(); assert.equal(new URL(page.url()).pathname, '/settings/classification')
    state.loseSave = true; await save.click(); const confirm = page.getByRole('button', { name: '确认保存结果', exact: true }); await confirm.waitFor()
    const frozen = puts().at(-1).body, writes = state.writes
    assert.equal(await movie.isDisabled(), true); await push('/logs')
    await page.getByRole('dialog', { name: '放弃确认并离开？', exact: true }).getByRole('button', { name: '取消', exact: true }).click()
    assert.equal(new URL(page.url()).pathname, '/settings/classification')
    await confirm.click(); await confirm.waitFor({ state: 'detached' }); assert.deepEqual(puts().at(-1).body, frozen); assert.equal(state.writes, writes)
    passed.push('409 保留编辑，离开须确认；保存丢回执冻结原请求，重放不重复写入')

    await tv.click(); await page.locator('.cr-toggle').first().click(); await page.getByRole('textbox', { name: '规则名称', exact: true }).fill('日漫')
    await closeEditor(); await movie.click(); state.reject = true; await save.click(); await page.getByRole('alert').filter({ hasText: '模拟字段校验失败' }).waitFor()
    assert.equal(await tv.getAttribute('aria-pressed'), 'true'); assert.equal(await page.locator('.cr-rule.is-invalid').count(), 1)
    assert.equal(await page.locator('.cr-detail input').first().evaluate(el => el === document.activeElement), true)
    await reset(); await save.click(); await page.getByText('电影和剧集分类规则已保存并生效', { exact: true }).waitFor()
    passed.push('后端错误定位到对应类型和规则，恢复全部默认需确认')

    await page.getByRole('textbox', { name: '剧集兜底目录', exact: true }).fill('剧集/新兜底')
    state.loseSave = true; await save.click(); await confirm.waitFor()
    await page.setViewportSize({ width: 375, height: 812 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    await page.getByRole('button', { name: '放弃等待并重读', exact: true }).click()
    await page.getByRole('dialog', { name: '重新读取分类文件？', exact: true }).getByRole('button', { name: '重新读取', exact: true }).click()
    await confirm.waitFor({ state: 'detached' }); assert.equal(await page.getByRole('textbox', { name: '剧集兜底目录', exact: true }).inputValue(), '剧集/新兜底')
    await page.waitForFunction(() => !document.querySelector('.cs-kind button')?.disabled); assert.equal(await tv.isEnabled(), true); await page.setViewportSize({ width: 1440, height: 1000 })
    passed.push('回执长期无法确认可明确放弃等待并重读，手机提示无溢出')

    for (let remaining = 6; remaining > 0; remaining--) {
      await page.locator('.cr-toggle').first().click(); await page.getByRole('button', { name: '删除规则', exact: true }).click()
      await page.getByRole('dialog', { name: '删除分类规则？', exact: true }).getByRole('button', { name: '删除规则', exact: true }).click()
    }
    await save.click(); await page.getByText('电影和剧集分类规则已保存并生效', { exact: true }).waitFor()
    assert.deepEqual(parse(record.yaml).tv.rules, []); assert.ok(await page.getByRole('textbox', { name: '剧集兜底目录', exact: true }).isVisible())
    passed.push('允许空规则列表，兜底独立保留；删除需要确认')

    record = { yaml: defaults, revision: hash(defaults), activeRevision: 'old-active' }
    await page.getByRole('button', { name: '重新读取文件', exact: true }).click()
    await page.getByRole('button', { name: '应用文件修改', exact: true }).click()
    await page.getByText('文件中的分类规则已生效', { exact: true }).waitFor(); assert.equal(record.activeRevision, record.revision)
    record = { yaml: 'version: [bad', revision: hash('version: [bad'), activeRevision: 'last-valid', loadError: '无效 YAML' }
    await page.getByRole('button', { name: '重新读取文件', exact: true }).click(); await page.getByRole('alert').waitFor()
    assert.equal(await page.locator('.cr-rule').count(), 0); await reset(); await save.click()
    await page.getByText('电影和剧集分类规则已保存并生效', { exact: true }).waitFor()
    passed.push('外部文件需明确应用；坏文件不静默覆盖，可明确恢复默认并保存')

    await page.setViewportSize({ width: 375, height: 812 }); await page.locator('.cr-toggle').first().click()
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    await page.screenshot({ path: 'test-results/classification-mobile.png', fullPage: true })
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.getByRole('combobox', { name: '添加类型', exact: true }).click()
    const colors = await page.locator('.select-menu-panel:popover-open').evaluate(el => ({ bg: getComputedStyle(el).backgroundColor, color: getComputedStyle(el).color }))
    assert.notEqual(colors.bg, 'rgb(255, 255, 255)'); assert.notEqual(colors.color, 'rgb(30, 41, 59)')
    await page.screenshot({ path: 'test-results/classification-mobile-dark.png', fullPage: true }); await page.keyboard.press('Escape')
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    passed.push('375px 手机布局无横向溢出，深色选择菜单保持清晰')
    await page.keyboard.press('Escape')
    assert.equal(await page.getByRole('dialog', { name: '剧集分类规则', exact: true }).count(), 0)
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    assert.equal(await page.locator('.cr-toggle').first().evaluate(el => el === document.activeElement), true)
    passed.push('Esc 关闭菜单后可关闭侧窗并归还焦点，滚动锁正常释放')
    assert.equal(calls.some(call => /115|cloud-download|tmdb|anime/.test(call.endpoint)), false)
    assert.deepEqual(errors, []); console.log(JSON.stringify({ passed, classificationReads: getCount(), writes: state.writes, errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
