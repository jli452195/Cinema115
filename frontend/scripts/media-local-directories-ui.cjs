// Isolated fixture: every API is mocked; no real directory, account, or rule writes.
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
  await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light') })
  const page = await context.newPage(), calls = [], errors = [], saves = []
  page.on('pageerror', error => errors.push(error.message)); page.setDefaultTimeout(10000)
  const rule = { id: 'local1', name: '本地目录验收', storage: 'local', source: '/media/inbox', target: '/media/library', failure: '/media/failed', classify: true, enabled: false, intervalMinutes: 5 }
  const special = '/media/片源 & #? + 中文', failurePath = '/media/整理失败 # 目录', longName = '很长的目录名称'.repeat(25)
  const directory = (name, path) => ({ name, path })
  const directories = {
    '/': { parentPath: null, directories: [directory('media', '/media'), directory('大量目录', '/many')] },
    '/media': { parentPath: '/', directories: [directory('inbox', '/media/inbox'), directory('library', '/media/library'), directory('failed', '/media/failed'), directory('片源 & #? + 中文', special), directory(longName, '/media/' + longName)] },
    '/media/inbox': { parentPath: '/media', directories: [] },
    '/media/library': { parentPath: '/media', directories: [] },
    '/media/failed': { parentPath: '/media', directories: [] },
    [failurePath]: { parentPath: '/media', directories: [] },
    [special]: { parentPath: '/media', directories: [] },
    ['/media/' + longName]: { parentPath: '/media', directories: [] },
    '/many': { parentPath: '/', directories: Array.from({ length: 500 }, (_, i) => directory('文件夹 ' + i, '/many/' + i)), truncated: true },
    'C:\\': { parentPath: '', directories: [directory('media', 'C:\\media')] },
    'C:\\media': { parentPath: 'C:\\', directories: [] },
  }
  let failNext = false, windowsRoots = false, enterSlow, releaseSlow
  const slowEntered = new Promise(resolve => { enterSlow = resolve }), slowReleased = new Promise(resolve => { releaseSlow = resolve })
  await page.route('**/api/**', async route => {
    const req = route.request(), url = new URL(req.url()), endpoint = url.pathname, method = req.method()
    calls.push({ endpoint, method, path: url.searchParams.get('path') })
    let data = {}, status = 200
    if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
    else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
    else if (endpoint === '/api/admin/media-organize') data = { rules: [rule], active: false, activeRules: [] }
    else if (endpoint === '/api/admin/media-organize/directories') {
      const input = url.searchParams.get('path') || '', current = input || (windowsRoots ? '' : '/')
      if (input === '/slow') { enterSlow(); await slowReleased }
      if (failNext) { failNext = false; status = 503; data = { error: '模拟挂载暂时不可用' } }
      else if (!current && windowsRoots) data = { path: '', parentPath: null, directories: [directory('C:\\', 'C:\\'), directory('D:\\', 'D:\\')], truncated: false }
      else if (directories[current]) data = { path: current, truncated: false, ...directories[current] }
      else { status = 400; data = { error: '目录不存在或无法读取' } }
    } else if (endpoint === '/api/admin/media-organize/rules' && method === 'POST') { const body = req.postDataJSON(); saves.push(body); Object.assign(rule, body); data = rule }
    else if (endpoint === '/api/admin/p115/accounts') data = { accounts: [{ id: 'primary1', primary: true }] }
    else if (endpoint === '/api/admin/p115/cloud-download/paths') { assert.equal(url.searchParams.get('accountId'), 'primary1'); data = { directories: [{ id: '10', name: '115来源' }], hasMore: false } }
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
  })
  try {
    await fs.mkdir('test-results', { recursive: true })
    await page.goto((process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/media/organize', { waitUntil: 'networkidle' })
    await page.getByRole('button', { name: '编辑本地目录验收', exact: true }).click()
    const editor = page.getByRole('dialog', { name: '整理规则', exact: true })
    const picker = name => page.getByRole('dialog', { name, exact: true })
    const open = async name => {
      await editor.getByRole('button', { name, exact: true }).click()
      const dialog = picker(name)
      await dialog.waitFor()
      await dialog.locator('.dp-content[aria-busy="false"]').waitFor()
      return dialog
    }
    const choose = dialog => dialog.getByRole('button', { name: '选择此目录', exact: true })
    let dialog = await open('选择来源目录')
    await dialog.getByText('当前目录没有子文件夹，可直接选择此目录。', { exact: true }).waitFor()
    assert.equal(calls.filter(c => c.endpoint.endsWith('/directories')).at(-1).path, '/media/inbox')
    await dialog.getByRole('button', { name: '返回上级', exact: true }).click()
    await dialog.getByRole('button', { name: '打开片源 & #? + 中文', exact: true }).click()
    await choose(dialog).click()
    assert.equal(await editor.getByPlaceholder('/media/inbox', { exact: true }).inputValue(), special)
    dialog = await open('选择目标目录')
    await dialog.getByRole('textbox', { name: '目录路径' }).fill('/missing')
    assert.equal(await choose(dialog).isDisabled(), true, 'typing must not select a stale path')
    await dialog.getByRole('button', { name: '前往', exact: true }).click()
    await dialog.getByRole('alert').getByText('目录不存在或无法读取', { exact: true }).waitFor()
    assert.equal(await choose(dialog).isDisabled(), true)
    await dialog.getByRole('button', { name: '根目录', exact: true }).click()
    await dialog.getByRole('button', { name: '打开media', exact: true }).waitFor()
    assert.equal(await dialog.getByRole('button', { name: '返回上级', exact: true }).isDisabled(), true)
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
    assert.equal(await editor.getByPlaceholder('/media/library', { exact: true }).inputValue(), '/media/library')
    failNext = true
    dialog = await open('选择整理失败目录')
    await dialog.getByRole('alert').getByText('模拟挂载暂时不可用', { exact: true }).waitFor()
    await dialog.getByRole('button', { name: '重新读取', exact: true }).click()
    await dialog.locator('.dp-content[aria-busy="false"]').waitFor()
    await dialog.getByRole('textbox', { name: '目录路径' }).fill(failurePath)
    await dialog.getByRole('button', { name: '前往', exact: true }).click()
    await choose(dialog).click()
    assert.equal(await editor.getByRole('textbox', { name: '整理失败目录', exact: true }).inputValue(), failurePath)
    await editor.getByRole('button', { name: '清空整理失败目录', exact: true }).click()
    assert.equal(await editor.getByRole('textbox', { name: '整理失败目录', exact: true }).inputValue(), '')
    await editor.getByRole('textbox', { name: '整理失败目录', exact: true }).fill(failurePath)
    assert.equal(calls.some(c => c.endpoint.includes('/p115/')), false, 'local browsing must not require a 115 account')
    // Closing a pending nested picker aborts it and returns focus to its trigger.
    dialog = await open('选择来源目录')
    await dialog.getByRole('textbox', { name: '目录路径' }).fill('/slow')
    await dialog.getByRole('button', { name: '前往', exact: true }).click()
    await slowEntered
    assert.equal(await choose(dialog).isDisabled(), true)
    await page.keyboard.press('Escape')
    await dialog.waitFor({ state: 'hidden' })
    assert.equal(await editor.isVisible(), true)
    assert.equal(await editor.getByRole('button', { name: '选择来源目录', exact: true }).evaluate(el => el === document.activeElement), true)
    dialog = await open('选择来源目录')
    await dialog.getByText('当前目录没有子文件夹，可直接选择此目录。', { exact: true }).waitFor()
    releaseSlow()
    assert.equal(await dialog.getByRole('textbox', { name: '目录路径' }).inputValue(), special)
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
    await editor.getByRole('button', { name: '保存规则', exact: true }).click()
    await editor.waitFor({ state: 'hidden' })
    assert.equal(saves.length, 1)
    assert.equal(saves[0].source, special); assert.equal(saves[0].target, '/media/library'); assert.equal(saves[0].failure, failurePath)
    assert.ok(!saves[0].sourceId && !saves[0].targetId && !saves[0].failureId && !saves[0].accountId)
    await page.getByRole('button', { name: '编辑本地目录验收', exact: true }).click()
    await editor.getByRole('combobox', { name: '存储位置' }).click()
    await page.getByRole('option', { name: '115 主账号', exact: true }).click()
    dialog = await open('选择来源目录')
    await dialog.getByRole('button', { name: '115来源', exact: true }).click()
    await choose(dialog).click()
    assert.equal(await editor.getByPlaceholder('选择 115 来源目录', { exact: true }).inputValue(), '/115来源')
    assert.equal(await editor.getByPlaceholder('选择 115 来源目录', { exact: true }).getAttribute('readonly'), '')
    await editor.getByRole('combobox', { name: '存储位置' }).click()
    await page.getByRole('option', { name: 'NAS / 本地挂载', exact: true }).click()
    windowsRoots = true
    dialog = await open('选择来源目录')
    await dialog.getByRole('button', { name: '打开C:\\', exact: true }).waitFor()
    assert.equal(await choose(dialog).isDisabled(), true, 'a virtual drive list is not a directory')
    await dialog.getByRole('button', { name: '打开C:\\', exact: true }).click()
    await dialog.getByRole('button', { name: '打开media', exact: true }).waitFor()
    await dialog.getByRole('button', { name: '返回上级', exact: true }).click()
    await dialog.getByRole('button', { name: '打开C:\\', exact: true }).click()
    await dialog.getByRole('button', { name: '打开media', exact: true }).click()
    await choose(dialog).click()
    assert.equal(await editor.getByPlaceholder('/media/inbox', { exact: true }).inputValue(), 'C:\\media')
    await editor.getByPlaceholder('/media/library', { exact: true }).fill('/media/library')
    dialog = await open('选择目标目录')
    await dialog.getByRole('textbox', { name: '目录路径' }).fill('/many')
    await dialog.getByRole('button', { name: '前往', exact: true }).click()
    await dialog.getByText('目录内容较多，列表仅展示部分文件夹。可输入完整路径前往。', { exact: true }).waitFor()
    assert.equal(await dialog.locator('.anime-floating-body').evaluate(el => el.scrollHeight > el.clientHeight), true)
    assert.equal(Math.round((await dialog.boundingBox()).width), 640)
    await page.screenshot({ path: 'test-results/media-local-directories-desktop.png' })
    await page.setViewportSize({ width: 320, height: 740 })
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await dialog.getByRole('textbox', { name: '目录路径' }).fill('/media')
    await dialog.getByRole('button', { name: '前往', exact: true }).click()
    await dialog.getByRole('button', { name: '打开' + longName, exact: true }).click()
    await dialog.getByText('当前目录没有子文件夹，可直接选择此目录。', { exact: true }).waitFor()
    assert.equal(await dialog.evaluate(el => el.scrollWidth > el.clientWidth + 1), false)
    assert.ok((await dialog.boundingBox()).height <= 740)
    assert.ok((await choose(dialog).boundingBox()).y + (await choose(dialog).boundingBox()).height <= 740)
    await page.screenshot({ path: 'test-results/media-local-directories-mobile-dark.png' })
    await choose(dialog).click()
    assert.equal(await editor.getByPlaceholder('/media/library', { exact: true }).inputValue(), '/media/' + longName)
    await editor.getByRole('button', { name: '取消', exact: true }).click()
    assert.deepEqual(errors, [])
    console.log('PASS: all local fields, initial paths, nested navigation, special characters, errors/retry, cancellation/focus, isolated save, 115 switching, Windows drives, internal scroll, narrow dark layout')
  } finally { releaseSlow(); await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
