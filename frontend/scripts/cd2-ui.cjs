// 所有 API 均使用独立模拟数据，不连接真实 CD2 或网盘，不修改实际文件。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-cd2-admin'); localStorage.setItem('cinema115-theme', 'light') })
  const page = await context.newPage(), calls = [], errors = [], passed = []
  page.setDefaultTimeout(10000); page.on('pageerror', error => errors.push(error.message))
  let config = { id: 'cd2-test', revision: 1, enabled: false, url: 'http://cd2.example:19798', tokenConfigured: true }
  let rejectSave = false, staleDirectory = false, rejectConfigGet = true, rejectDirectory = false, heldTest, heldDirectory, heldSave
  let savedToken = 'initial-isolated-token'
  const rules = []
  await page.route('**/api/**', async route => {
    const req = route.request(), url = new URL(req.url()), endpoint = url.pathname, method = req.method(), body = req.postDataJSON()
    calls.push({ endpoint, method, body, query: url.searchParams.toString() })
    let data = { configured: false, authorized: false, channels: [], items: [], accounts: [], assrt: { configured: false } }, status = 200
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'mock', role: 'admin' } }
    else if (endpoint === '/api/admin/cd2') {
      if (method === 'POST') {
        if (heldSave) { const held = heldSave; heldSave = undefined; held.started(); await held.pending }
        if (rejectSave) { status = 503; data = { error: '模拟保存失败' } }
        else {
          if (body.url !== config.url || body.token !== undefined && body.token !== savedToken) config.revision++
          if (body.token !== undefined) savedToken = body.token
          config = { ...config, enabled: body.enabled, url: body.url, tokenConfigured: body.token === undefined ? config.tokenConfigured : !!body.token }
          data = config
        }
      } else if (rejectConfigGet) { status = 503; data = { error: '模拟配置服务不可达' } }
      else data = config
    } else if (endpoint === '/api/admin/cd2/test') {
      if (heldTest) { const held = heldTest; heldTest = undefined; held.started(); await held.pending }
      data = { reachable: true, authorized: true, listable: true, version: '1.1.2', elapsedMs: 21, message: '模拟只读连接检查完成', permissions: { list: true, read: true, createFolder: true, rename: true, move: true, copy: false, delete: true } }
    } else if (endpoint === '/api/admin/cd2/directories') {
      const folder = url.searchParams.get('path') || '/'
      const children = folder === '/' ? [{ name: '阿里云盘', path: '/Aliyun' }, { name: '其他账号', path: '/Another' }] : folder === '/Aliyun' ? [{ name: '收件箱', path: '/Aliyun/inbox' }, { name: '媒体库', path: '/Aliyun/library' }, { name: '只读目录', path: '/Aliyun/readonly', readOnly: true }] : []
      data = { connectionId: config.id, revision: staleDirectory ? config.revision + 1 : config.revision, path: folder, parentPath: folder === '/' ? null : folder.substring(0, folder.lastIndexOf('/')) || '/', readOnly: folder.endsWith('/readonly'), directories: children.map(child => ({ id: child.path, ...child })), truncated: false }
      if (rejectDirectory) { status = 502; data = { error: '模拟 CD2 暂时不可达' } }
      if (heldDirectory) {
        const held = heldDirectory; heldDirectory = undefined
        data = { ...data, directories: [{ id: '/Stale', name: '陈旧目录', path: '/Stale' }] }
        held.started(); await held.pending
      }
    } else if (endpoint === '/api/admin/media-organize') data = { rules, active: false, activeRules: [], monitoring: Object.fromEntries(rules.filter(rule => rule.scheduleEnabled && rule.storage === 'cd2').map(rule => [rule.id, { mode: 'polling' }])) }
    else if (endpoint === '/api/admin/media-organize/rules') {
      if (!Number.isInteger(body.intervalSeconds) || body.intervalSeconds < 1 || body.intervalSeconds > 86400) { status = 400; data = { error: '扫描间隔须为 1 至 86400 秒' } }
      else if (typeof body.attachmentCleanupEnabled !== 'boolean') { status = 400; data = { error: '附件清理开关须为布尔值' } }
      else if (!Number.isInteger(body.attachmentGraceSeconds) || body.attachmentGraceSeconds < 0 || body.attachmentGraceSeconds > 86400) { status = 400; data = { error: '附件宽限须为 0 至 86400 秒' } }
      else { data = { ...body, id: body.id || 'cd2-rule' }; rules.splice(0, rules.length, data) }
    } else if (endpoint.endsWith('/schedule') && method === 'PATCH') {
      assert.deepEqual(Object.keys(body).sort(), ['intervalSeconds', 'realtimeEnabled', 'scheduleEnabled'])
      const rule = rules.find(rule => rule.id === endpoint.split('/').at(-2)); Object.assign(rule, body); data = rule
    }
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
  })
  const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
  const save = () => page.getByRole('button', { name: '保存配置', exact: true })
  const tests = () => calls.filter(call => call.endpoint === '/api/admin/cd2/test')
  const saves = () => calls.filter(call => call.endpoint === '/api/admin/cd2' && call.method === 'POST')
  const token = () => page.getByLabel('API Token', { exact: true })
  const address = () => page.getByLabel('服务地址', { exact: true })
  const picker = title => page.getByRole('dialog', { name: title, exact: true })
  try {
    await page.goto(origin + '/resources')
    await page.getByRole('region', { name: '服务连接', exact: true }).getByRole('link', { name: 'CloudDrive2', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '模拟配置服务不可达' }).waitFor()
    assert.equal(await address().isDisabled(), true)
    rejectConfigGet = false
    await page.getByRole('button', { name: '重新加载', exact: true }).click()
    await page.waitForFunction(() => !document.querySelector('.cd2-fields')?.disabled)
    assert.equal(tests().length, 0)
    assert.equal(await token().inputValue(), '')
    assert.equal(await page.getByRole('button', { name: '浏览目录', exact: true }).isDisabled(), true)
    await page.getByRole('switch', { name: '启用 CloudDrive2', exact: true }).locator('..').click()
    await save().click(); await page.getByText('CloudDrive2 配置已保存。', { exact: true }).waitFor()
    assert.equal('token' in saves().at(-1).body, false)
    assert.equal(config.revision, 1)
    passed.push('初次配置读取失败可恢复，入口只读加载，Token 不回显，启停保持连接修订')

    await address().fill('https://cd2-new.example:443'); await token().fill('isolated-test-token')
    await page.getByRole('button', { name: '测试连接', exact: true }).click()
    await page.getByText('模拟只读连接检查完成', { exact: true }).waitFor()
    assert.equal(tests().at(-1).body.url, 'https://cd2-new.example:443')
    assert.equal(tests().at(-1).body.token, 'isolated-test-token')
    assert.equal(config.url, 'http://cd2.example:19798')
    assert.equal(await token().inputValue(), 'isolated-test-token')
    assert.equal(await page.getByRole('button', { name: '浏览目录', exact: true }).isDisabled(), true)
    assert.equal(await page.locator('.cd2-permission.is-allowed').count(), 6)
    assert.equal(await page.locator('.cd2-permission').count(), 7)
    assert.equal(await page.getByText('事件推送', { exact: true }).count(), 0)
    await page.getByText('自动整理支持自定义轮询间隔，可开关未匹配附件清理并设置宽限；目录读完立即整理，来源空子目录始终清理。', { exact: true }).waitFor()
    rejectSave = true; await save().click(); await page.getByRole('alert').filter({ hasText: '模拟保存失败' }).waitFor()
    assert.equal(await token().inputValue(), 'isolated-test-token')
    let start, release
    let started = new Promise(resolve => { start = resolve }), pending = new Promise(resolve => { release = resolve })
    heldSave = { started: start, pending }; rejectSave = false
    await save().click(); await started
    assert.equal(await address().isDisabled(), true)
    await page.getByRole('link', { name: '前往自动整理', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '请完成当前操作' }).waitFor()
    assert.equal(new URL(page.url()).pathname, '/resources/clouddrive2')
    release(); await page.getByText('CloudDrive2 配置已保存。', { exact: true }).waitFor()
    assert.equal(await token().inputValue(), '')
    started = new Promise(resolve => { start = resolve }); pending = new Promise(resolve => { release = resolve })
    heldTest = { started: start, pending }
    await page.getByRole('button', { name: '测试连接', exact: true }).click(); await started
    assert.equal(await address().isDisabled(), true)
    await page.getByRole('button', { name: '取消测试', exact: true }).click()
    await page.getByText('测试已取消，当前输入已保留。', { exact: true }).waitFor(); release()
    passed.push('草稿只读测试不保存、失败保留输入、测试可取消')

    rejectDirectory = true
    await page.getByRole('button', { name: '浏览目录', exact: true }).click()
    const browse = picker('浏览 CloudDrive2 目录')
    await browse.getByRole('alert').filter({ hasText: '模拟 CD2 暂时不可达' }).waitFor()
    assert.equal(await browse.getByRole('button', { name: '完成浏览', exact: true }).isDisabled(), true)
    rejectDirectory = false
    await browse.getByRole('button', { name: '重新读取', exact: true }).click()
    await browse.getByRole('button', { name: '打开阿里云盘', exact: true }).click()
    await browse.getByRole('button', { name: '打开收件箱', exact: true }).click()
    await browse.getByRole('button', { name: '完成浏览', exact: true }).waitFor()
    await browse.getByLabel('目录路径', { exact: true }).fill('/Another')
    assert.equal(await browse.getByRole('button', { name: '完成浏览', exact: true }).isDisabled(), true)
    await browse.getByRole('button', { name: '前往', exact: true }).click()
    await page.waitForFunction(() => document.querySelector('.dp-path input')?.value === '/Another' && !document.querySelector('.dp-primary')?.disabled)
    await browse.getByRole('button', { name: '完成浏览', exact: true }).click()
    staleDirectory = true
    await page.getByRole('button', { name: '浏览目录', exact: true }).click()
    await browse.getByRole('alert').filter({ hasText: '连接已变化' }).waitFor()
    assert.equal(await browse.getByRole('button', { name: '完成浏览', exact: true }).isDisabled(), true)
    await browse.getByRole('button', { name: '取消', exact: true }).click(); staleDirectory = false
    started = new Promise(resolve => { start = resolve }); pending = new Promise(resolve => { release = resolve })
    heldDirectory = { started: start, pending }
    await page.getByRole('button', { name: '浏览目录', exact: true }).click(); await started
    await browse.getByRole('button', { name: '取消', exact: true }).click()
    await page.getByRole('button', { name: '浏览目录', exact: true }).click()
    await browse.getByRole('button', { name: '打开阿里云盘', exact: true }).waitFor(); release()
    assert.equal(await browse.getByRole('button', { name: '打开陈旧目录', exact: true }).count(), 0)
    await browse.getByRole('button', { name: '完成浏览', exact: true }).click()
    passed.push('CD2 原生多网盘目录浏览、失败重试、连接修订检查及关闭后的迟到响应隔离')

    await address().fill('http://unsaved.example:19798')
    await page.getByRole('link', { name: '前往自动整理', exact: true }).click()
    const leave = picker('放弃未保存的 CloudDrive2 设置？')
    await leave.getByRole('button', { name: '继续编辑', exact: true }).click()
    assert.equal(await address().inputValue(), 'http://unsaved.example:19798')
    await page.getByRole('link', { name: '前往自动整理', exact: true }).click()
    await leave.getByRole('button', { name: '放弃修改并离开', exact: true }).click()
    await page.getByRole('button', { name: '新增规则', exact: true }).click()
    const editor = picker('整理规则')
    await editor.getByLabel('规则名称', { exact: true }).fill('CD2 阿里云盘整理')
    await editor.getByRole('combobox', { name: '存储位置', exact: true }).click()
    await page.getByRole('option', { name: 'CloudDrive2', exact: true }).getByRole('button').click()
    await editor.getByText('开启后，未匹配的字幕和附件从首次发现起计时，宽限到期后的下一轮扫描仍无对应视频才删除；0 秒表示复核后立即清理。关闭时保留未匹配附件及首次发现记录，再开启不重置计时；已匹配附件始终立即整理。空子目录始终自动清理，保留来源根目录。', { exact: true }).waitFor()
    assert.equal(await editor.getByRole('switch', { name: '清理未匹配附件', exact: true }).isChecked(), true)
    assert.equal(await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).inputValue(), '60')
    await page.screenshot({ path: 'test-results/media-rule-attachment-default-desktop.png' })
    await editor.getByRole('combobox', { name: '附件宽限单位', exact: true }).click()
    await page.getByRole('option', { name: '秒', exact: true }).getByRole('button').click()
    await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).fill('45')
    assert.equal(await editor.getByRole('switch', { name: '自动删除相同文件', exact: true }).isChecked(), false)
    await editor.getByRole('switch', { name: '定时扫描', exact: true }).locator('..').click()
    assert.equal(await editor.getByRole('spinbutton', { name: '扫描间隔', exact: true }).inputValue(), '20')
    await editor.getByRole('spinbutton', { name: '扫描间隔', exact: true }).fill('37')
    assert.doesNotMatch(await editor.innerText(), /稳定 30 秒/)
    await editor.getByRole('switch', { name: '自动删除相同文件', exact: true }).locator('..').click()
    await editor.getByText('目标同名且双方 SHA1 一致时删除来源文件；SHA1 不同或缺失时保留冲突。', { exact: true }).waitFor()
    for (const [which, folder] of [['来源', '收件箱'], ['目标', '媒体库']]) {
      await editor.getByRole('button', { name: `选择${which}目录`, exact: true }).click()
      const dialog = picker(`选择${which}目录`)
      await dialog.getByRole('button', { name: '打开阿里云盘', exact: true }).click()
      if (which === '来源') {
        await dialog.getByRole('button', { name: '打开只读目录', exact: true }).click()
        await dialog.getByText('此目录为只读目录。', { exact: true }).waitFor()
        assert.equal(await dialog.getByRole('button', { name: '选择此目录', exact: true }).isDisabled(), true)
        await dialog.getByRole('button', { name: '返回上级', exact: true }).click()
      }
      await dialog.getByRole('button', { name: `打开${folder}`, exact: true }).click()
      await dialog.getByRole('button', { name: '选择此目录', exact: true }).click()
    }
    await editor.getByRole('button', { name: '保存规则', exact: true }).click()
    await page.getByRole('heading', { name: 'CD2 阿里云盘整理', exact: true }).waitFor()
    await page.getByText('无关文件、空子目录；未匹配附件宽限 45 秒', { exact: true }).waitFor()
    const submitted = calls.filter(call => call.endpoint === '/api/admin/media-organize/rules').at(-1).body
    assert.equal(submitted.storage, 'cd2'); assert.equal(submitted.connectionId, config.id); assert.equal(submitted.connectionRevision, config.revision)
    assert.equal(submitted.source, '/Aliyun/inbox'); assert.equal(submitted.target, '/Aliyun/library'); assert.ok(!submitted.accountId && !submitted.sourceId)
    assert.equal(submitted.intervalSeconds, 37); assert.equal(Object.hasOwn(submitted, 'intervalMinutes'), false); assert.equal(submitted.deleteDuplicates, true)
    assert.equal(submitted.attachmentGraceSeconds, 45)
    assert.equal(submitted.attachmentCleanupEnabled, true)
    assert.equal(calls.some(call => call.endpoint.startsWith('/api/admin/p115/')), false)
    passed.push('自动整理选择 CD2 原生目录，保存连接标识并独立于 115 账号接口')

    const ruleCard = page.getByRole('article', { name: 'CD2 阿里云盘整理整理规则', exact: true })
    await ruleCard.getByText('定时扫描 · 每 37 秒', { exact: true }).waitFor()
    await ruleCard.getByText('同名且 SHA1 相同删除来源', { exact: true }).waitFor()
    assert.equal(await ruleCard.locator('.mo-rule-schedule.is-unavailable').count(), 0)
    await ruleCard.getByRole('button', { name: '暂停CD2 阿里云盘整理的定时扫描', exact: true }).click()
    await ruleCard.getByText('定时扫描已暂停', { exact: true }).waitFor()
    await ruleCard.getByRole('button', { name: '启用CD2 阿里云盘整理的定时扫描', exact: true }).click()
    await ruleCard.getByText('定时扫描 · 每 37 秒', { exact: true }).waitFor()
    await ruleCard.getByRole('button', { name: '编辑CD2 阿里云盘整理', exact: true }).click()
    assert.equal(await editor.getByRole('spinbutton', { name: '扫描间隔', exact: true }).inputValue(), '37')
    assert.equal(await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).inputValue(), '45')
    assert.equal(await editor.getByRole('switch', { name: '自动删除相同文件', exact: true }).isChecked(), true)
    await editor.getByRole('switch', { name: '清理未匹配附件', exact: true }).locator('..').click()
    await editor.getByRole('button', { name: '保存规则', exact: true }).click()
    await ruleCard.getByText('无关文件、空子目录；保留未匹配附件', { exact: true }).waitFor()
    assert.equal(calls.filter(call => call.endpoint === '/api/admin/media-organize/rules').at(-1).body.attachmentGraceSeconds, 45)
    await ruleCard.getByRole('button', { name: '编辑CD2 阿里云盘整理', exact: true }).click()
    assert.equal(await editor.getByRole('switch', { name: '清理未匹配附件', exact: true }).isChecked(), false)
    assert.equal(await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).isDisabled(), true)
    await editor.getByRole('link', { name: '配置 CloudDrive2 连接', exact: true }).click()
    await page.waitForURL('**/resources/clouddrive2')
    assert.equal(await picker('放弃未保存的整理修改？').count(), 0)
    await page.goto(origin + '/media/organize')
    await ruleCard.getByRole('button', { name: '编辑CD2 阿里云盘整理', exact: true }).click()
    await editor.getByRole('combobox', { name: '存储位置', exact: true }).click()
    await page.getByRole('option', { name: 'NAS / 本地挂载', exact: true }).getByRole('button').click()
    assert.equal(await editor.getByRole('spinbutton', { name: '检查间隔（秒）', exact: true }).count(), 0)
    assert.equal(await editor.getByRole('switch', { name: '自动删除相同文件', exact: true }).count(), 0)
    await editor.getByRole('spinbutton', { name: '扫描间隔', exact: true }).waitFor()
    await editor.getByText('选择或填写服务器／容器内已挂载且可写的目录。来源与目标不能重叠。新文件稳定 30 秒后处理。', { exact: true }).waitFor()
    assert.equal(await editor.getByRole('spinbutton', { name: '扫描间隔', exact: true }).inputValue(), '5')
    assert.equal(await editor.getByRole('spinbutton', { name: '附件宽限', exact: true }).inputValue(), '45')
    await editor.getByRole('button', { name: '取消', exact: true }).click()
    await ruleCard.getByRole('button', { name: '暂停CD2 阿里云盘整理的定时扫描', exact: true }).click()
    await ruleCard.getByText('定时扫描已暂停', { exact: true }).waitFor()
    await ruleCard.getByRole('button', { name: '编辑CD2 阿里云盘整理', exact: true }).click()
    await editor.getByRole('combobox', { name: '存储位置', exact: true }).click()
    await page.getByRole('option', { name: 'NAS / 本地挂载', exact: true }).getByRole('button').click()
    assert.equal(await editor.getByRole('spinbutton').count(), 2)
    await editor.getByLabel('来源目录', { exact: true }).fill('/media/test-inbox')
    await editor.getByLabel('目标目录', { exact: true }).fill('/media/test-library')
    await editor.getByRole('button', { name: '保存规则', exact: true }).click()
    await ruleCard.getByText('本地挂载', { exact: true }).waitFor()
    const localSubmitted = calls.filter(call => call.endpoint === '/api/admin/media-organize/rules').at(-1).body
    assert.equal(localSubmitted.intervalSeconds, 300)
    assert.equal(localSubmitted.attachmentGraceSeconds, 45)
    assert.equal(localSubmitted.attachmentCleanupEnabled, false)
    assert.equal(Object.hasOwn(localSubmitted, 'deleteDuplicates'), false)
    assert.equal(Object.hasOwn(localSubmitted, 'intervalMinutes'), false)
    assert.equal(localSubmitted.scheduleEnabled, false)
    passed.push('CD2 自定义轮询、附件清理默认开启60秒、关闭保留设置、独立启停、无修改离开及切换存储保留配置')

    await page.goto(origin + '/resources/clouddrive2')
    await page.waitForFunction(() => !document.querySelector('.cd2-fields')?.disabled)
    await page.getByRole('switch', { name: '启用 CloudDrive2', exact: true }).locator('..').click()
    await page.getByLabel('清除已保存 Token', { exact: true }).check()
    await save().click(); await page.getByText('CloudDrive2 配置已保存。', { exact: true }).waitFor()
    assert.equal(saves().at(-1).body.token, '')
    await page.setViewportSize({ width: 390, height: 844 })
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false)
    assert.notEqual(await page.locator('.cd2-panel').first().evaluate(el => getComputedStyle(el).backgroundColor), 'rgb(255, 255, 255)')
    await fs.mkdir('test-results', { recursive: true })
    await page.screenshot({ path: 'test-results/cd2-mobile-dark.png', fullPage: true })
    assert.deepEqual(errors, [])
    passed.push('显式清除 Token、未保存离开保护和移动端深色布局')
    console.log(JSON.stringify({ passed }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
