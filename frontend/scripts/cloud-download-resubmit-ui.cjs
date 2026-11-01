// 所有请求均由模拟接口接管，验证三个网页入口，不提交或删除真实云下载任务。
const path = require('node:path')
const os = require('node:os')
const assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage()
    page.setDefaultTimeout(10000)
    const errors = [], calls = [], passed = []
    page.on('pageerror', err => errors.push(err.message))
    page.on('console', message => { if (message.text().includes('[Vue warn]')) errors.push(message.text()) })
    const magnet = 'magnet:?xt=urn:btih:' + 'a'.repeat(40)
    let scenario = 'new', accountId = 'selected-account', profileID = 0
    const targets = { 'cloud-download': [], 'share-transfer': [] }
    let rejectSave = false, rejectDelete = false
    const failures = {
      running: '相同任务仍在 115 中运行或等待，未删除或重新提交',
      failed: '旧任务记录已移除，但重新提交失败；原有网盘文件已保留。提交被拒绝',
      uncertain: '旧任务记录已移除，但重新提交结果尚未确认；原有网盘文件已保留。请先检查 115 任务列表再决定是否重试',
    }
    await page.route('**/api/**', async route => {
      const url = new URL(route.request().url())
      const profileRoute = url.pathname.match(/\/p115\/(cloud-download|share-transfer)\/profiles(?:\/([^/]+))?$/)
      if (profileRoute) {
        const profiles = targets[profileRoute[1]], id = profileRoute[2], method = route.request().method()
        if (method === 'GET') return route.fulfill({ json: { profiles } })
        if (method === 'DELETE') {
          if (rejectDelete) { rejectDelete = false; return route.fulfill({ status: 409, json: { error: '删除失败，请重试' } }) }
          const index = profiles.findIndex(profile => profile.id === id)
          if (index >= 0) profiles.splice(index, 1)
          return route.fulfill({ status: 204, body: '' })
        }
        if (rejectSave) { rejectSave = false; return route.fulfill({ status: 409, json: { error: '目录名称重复，请修改后重试' } }) }
        const body = route.request().postDataJSON()
        const profile = { id: id || `profile-${++profileID}`, ...body }
        if (method === 'POST') profiles.push(profile)
        else profiles[profiles.findIndex(value => value.id === id)] = profile
        return route.fulfill({ status: method === 'POST' ? 201 : 200, json: { profile } })
      }
      let data = {}, status = 200
      if (url.pathname === '/api/auth/me') data = { user: { id: 'mock', role: 'admin', username: 'mock', disabled: false } }
      else if (url.pathname.endsWith('/p115/accounts')) data = { accounts: [{ id: 'selected-account', name: '主账号', primary: true, configured: true, revision: 1 }] }
      else if (url.pathname.endsWith('/cloud-download/paths')) data = { cid: '0', directories: [], count: 0, hasMore: false }
      else if (url.pathname.endsWith('/cloud-download/config')) data = { accountId, targetCID: '987', targetPath: '/测试目标' }
      else if (url.pathname.endsWith('/cloud-download/tasks')) {
        calls.push({ accountId: url.searchParams.get('accountId'), ...route.request().postDataJSON() })
        if (scenario === 'gateway') return route.fulfill({ status: 504, contentType: 'text/html', body: '<html>Gateway Timeout</html>' })
        if (failures[scenario]) { status = scenario === 'running' ? 409 : scenario === 'uncertain' ? 504 : 502; data = { accepted: false, removedOldTask: scenario !== 'running', outcomeUncertain: scenario === 'uncertain', error: failures[scenario] } }
        else { status = 202; data = { accepted: true, removedOldTask: scenario === 'redo' } }
      }
      else if (url.pathname.includes('/tmdb/detail/')) data = { id: 65942, kind: 'tv', title: '测试动漫', originalTitle: 'テスト', imdbId: '', genres: [{ id: 16, name: '动画' }], countries: ['JP'], cast: [], seasons: [], overview: '', posterUrl: '', backdropUrl: '' }
      else if (url.pathname.endsWith('/gying/config')) data = { configured: true, hasSession: true, resourceTypes: [], resourceOrder: ['magnet'] }
      else if (url.pathname.endsWith('/gying/search')) data = { query: 'テスト', matchedBy: 'title', total: 1, cached: false, attempts: [], items: [{ id: 'ac:one', title: '测试条目', mediaType: 'anime', links: [{ type: 'magnet', typeLabel: '磁力', category: 'torrent', url: magnet }] }] }
      else if (url.pathname.endsWith('/mikan/search')) data = { query: '测试动漫', items: [{ id: 'one', title: '测试资源', magnet, size: '1 GB', updatedAt: '2026/09/19', resolution: '1080P', url: '', torrentUrl: '' }], groups: [], bangumis: [], attempts: [], fetchedAt: new Date().toISOString(), expiresAt: new Date().toISOString() }
      else if (url.pathname.includes('/recommendations')) data = { items: [], page: 1, totalPages: 1 }
      else if (url.pathname.includes('/emby/media/')) data = { media: { state: 'unknown', items: [] } }
      else if (url.pathname.includes('/follows/')) data = { subscribed: false, libraryState: 'unknown' }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    await page.goto(origin + '/115/cloud-download')
    for (const [kind, tab] of [['cloud-download', '磁力/ED2K 转存'], ['share-transfer', '115 链接转存']]) {
      await page.getByRole('tab', { name: tab, exact: true }).click()
      const profiles = targets[kind]
      await page.getByRole('button', { name: '新增目录', exact: true }).click()
      const profileEditor = page.getByRole('dialog', { name: '新增接收目录', exact: true })
      await profileEditor.getByLabel('配置名称').fill(tab + ' XX')
      assert.equal(await profileEditor.getByRole('button', { name: '保存目录' }).isDisabled(), true)
      await profileEditor.getByRole('button', { name: '选择目录' }).click()
      await page.getByRole('dialog', { name: '选择下载目录' }).getByRole('button', { name: '选择此目录' }).click()
      rejectSave = true
      await profileEditor.getByRole('button', { name: '保存目录' }).click()
      await profileEditor.getByRole('alert').filter({ hasText: '目录名称重复' }).waitFor()
      assert.equal(profiles.length, 0)
      assert.equal(await profileEditor.getByLabel('配置名称').inputValue(), tab + ' XX')
      await profileEditor.getByRole('button', { name: '保存目录' }).click()
      await profileEditor.waitFor({ state: 'hidden' })
      assert.deepEqual(profiles.map(({ accountId, targetCID, targetPath }) => ({ accountId, targetCID, targetPath })), [{ accountId: 'selected-account', targetCID: '0', targetPath: '/' }])
      await page.getByRole('button', { name: '编辑 ' + tab + ' XX', exact: true }).click()
      const editor = page.getByRole('dialog', { name: '编辑接收目录', exact: true })
      await editor.getByLabel('配置名称').fill(tab + ' XC')
      await editor.getByRole('button', { name: '保存目录' }).click()
      await editor.waitFor({ state: 'hidden' })
      assert.equal(profiles[0].name, tab + ' XC')
      passed.push(kind + ':profiles')
    }
    // Switching tabs must reload the matching endpoint rather than reuse the other list.
    for (const [kind, tab, other] of [['cloud-download', '磁力/ED2K 转存', '115 链接转存'], ['share-transfer', '115 链接转存', '磁力/ED2K 转存']]) {
      await page.getByRole('tab', { name: tab, exact: true }).click()
      await page.getByRole('heading', { name: tab + ' XC', exact: true }).waitFor()
      assert.equal(await page.getByRole('heading', { name: other + ' XC', exact: true }).count(), 0)
      await page.getByRole('button', { name: '删除 ' + tab + ' XC', exact: true }).click()
      const confirm = page.getByRole('dialog', { name: '删除接收目录？', exact: true })
      rejectDelete = true
      await confirm.getByRole('button', { name: '删除目录', exact: true }).click()
      await confirm.getByRole('alert').filter({ hasText: '删除失败' }).waitFor()
      assert.equal(targets[kind].length, 1)
      await confirm.getByRole('button', { name: '删除目录', exact: true }).click()
      await confirm.waitFor({ state: 'hidden' })
      assert.equal(targets[kind].length, 0)
    }
    if (process.argv.includes('--profiles-only')) {
      assert.deepEqual(errors, [])
      console.log('PASS: magnet/share profile CRUD, root directory selection, tab isolation, and save/delete failure recovery.')
      await context.close()
      return
    }
    for (const source of ['web', 'gying', 'mikan']) {
      for (const mode of ['new', 'redo', 'running', 'failed', 'uncertain', 'gateway', 'unconfigured']) {
        scenario = mode
        accountId = mode === 'unconfigured' ? '' : 'selected-account'
        const before = calls.length
        await page.goto(origin + (source === 'web' ? '/115/cloud-download' : '/tv/65942'))
        if (source === 'web') {
          await page.getByRole('textbox').fill(magnet)
          if (mode === 'unconfigured') {
            assert.equal(await page.getByRole('button', { name: '开始云下载', exact: true }).isDisabled(), true)
            assert.equal(calls.length, before)
            passed.push(source + ':' + mode)
            continue
          }
          await page.getByRole('button', { name: '开始云下载', exact: true }).click()
        } else {
          await page.getByRole('button', { name: source === 'gying' ? /观影\s*搜索/ : /Mikan\s*搜索/ }).click()
          await page.getByRole('button', { name: '115 云下载', exact: true }).click()
        }
        if (mode === 'unconfigured') {
          await page.getByText(/请先.*(?:选择|配置).*账号|请先在.*选择账号/).first().waitFor()
          assert.equal(calls.length, before)
        } else {
          const text = mode === 'new' ? /已提交到 115 云下载：\/测试目标/ : mode === 'redo' ? /已重新提交到 115 云下载：\/测试目标.*原有网盘文件已保留/ : mode === 'gateway' ? '处理结果尚未确认，请先到 115 云下载核对；不要立即重复提交' : failures[mode]
          try { await page.getByText(text, { exact: false }).first().waitFor() }
          catch (error) { throw new Error(`${source}:${mode}: ${await page.locator('[role="alert"], [role="status"]').allTextContents()}\n${error.message}`) }
          assert.equal(calls.length, before + 1)
          assert.deepEqual(calls.at(-1), { accountId: 'selected-account', url: magnet, targetCID: '987' })
          if (mode === 'new' || mode === 'redo') await page.getByText(/提交成功不代表下载已完成/).waitFor()
          if (mode === 'uncertain') assert.equal(await page.getByRole('link', { name: '前往配置' }).count(), 0)
        }
        passed.push(source + ':' + mode)
      }
    }
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, submissions: calls.length, pageErrors: errors }))
  } finally { await browser.close() }
})().catch(err => { console.error(err); process.exitCode = 1 })
