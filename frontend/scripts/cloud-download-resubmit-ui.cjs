// 所有请求均由模拟接口接管，验证三个网页入口，不提交或删除真实云下载任务。
const path = require('node:path')
const os = require('node:os')
const assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext()
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage()
    page.setDefaultTimeout(10000)
    const errors = [], calls = [], passed = []
    page.on('pageerror', err => errors.push(err.message))
    const magnet = 'magnet:?xt=urn:btih:' + 'a'.repeat(40)
    let scenario = 'new', accountId = 'selected-account'
    const failures = {
      running: '相同任务仍在 115 中运行或等待，未删除或重新提交',
      failed: '旧任务记录已移除，但重新提交失败；原有网盘文件已保留。提交被拒绝',
      uncertain: '旧任务记录已移除，但重新提交结果尚未确认；原有网盘文件已保留。请先检查 115 任务列表再决定是否重试',
    }
    await page.route('**/api/**', async route => {
      const url = new URL(route.request().url())
      let data = {}, status = 200
      if (url.pathname === '/api/auth/me') data = { user: { id: 'mock', role: 'admin', username: 'mock', disabled: false } }
      else if (url.pathname.endsWith('/p115/accounts')) data = { accounts: [{ id: 'selected-account', name: '主账号', primary: true, configured: true, revision: 1 }] }
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
    for (const source of ['web', 'gying', 'mikan']) {
      for (const mode of ['new', 'redo', 'running', 'failed', 'uncertain', 'gateway', 'unconfigured']) {
        scenario = mode
        accountId = mode === 'unconfigured' ? '' : 'selected-account'
        const before = calls.length
        await page.goto(origin + (source === 'web' ? '/115/cloud-download' : '/tv/65942'))
        if (source === 'web') {
          await page.getByRole('textbox').fill(magnet)
          if (mode === 'unconfigured') {
            assert.equal(await page.getByRole('button', { name: '开始磁力转存', exact: true }).isDisabled(), true)
            assert.equal(calls.length, before)
            passed.push(source + ':' + mode)
            continue
          }
          await page.getByRole('button', { name: '开始磁力转存', exact: true }).click()
        } else {
          await page.getByRole('button', { name: source === 'gying' ? '观影搜索' : '蜜柑搜索', exact: true }).click()
          await page.getByRole('button', { name: '115 转存', exact: true }).click()
        }
        if (mode === 'unconfigured') {
          await page.getByText(/请先.*(?:选择|配置).*账号|请先在.*选择账号/).first().waitFor()
          assert.equal(calls.length, before)
        } else {
          const text = mode === 'new' ? /已提交到 115 云下载：\/测试目标/ : mode === 'redo' ? /已重新提交到 115 云下载：\/测试目标.*原有网盘文件已保留/ : mode === 'gateway' ? '处理结果尚未确认，请先到 115 云下载核对；不要立即重复提交' : failures[mode]
          try { await page.getByText(text, { exact: typeof text === 'string' }).waitFor() }
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
