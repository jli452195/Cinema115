// 仅使用模拟接口，不修改真实凭据或 115 任务。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage(), calls = [], errors = []
    page.setDefaultTimeout(8000); page.on('pageerror', e => errors.push(e.message))
    let configured = false, githubConfigured = false
    await page.route('**/api/**', async route => {
      const req = route.request(), endpoint = new URL(req.url()).pathname
      calls.push({ endpoint, method: req.method(), body: req.postDataJSON() })
      let data = { configured: false, authorized: false, channels: [], items: [], accounts: [], genres: [], totalPages: 1 }, status = 200
      if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'mock', role: 'admin', disabled: false } }
      else if (endpoint === '/api/admin/subtitles/config') data = { assrt: { configured } }
      else if (endpoint === '/api/admin/subtitles/assrt/config') {
        if (req.method() === 'DELETE') configured = false
        else if (req.postDataJSON().token === 'b'.repeat(32)) { status = 409; data = { error: '射手网 API Token 无效，请在字幕设置中重新配置' } }
        else configured = true
        if (status === 200) data = { assrt: { configured } }
      }
      else if (endpoint === '/api/admin/subtitles/github/config') {
        if (req.method() === 'DELETE') githubConfigured = false
        else if (req.method() === 'POST') {
          if (req.postDataJSON().token === 'github-invalid') { status = 400; data = { error: 'GitHub Token 无效，请重新配置' } }
          else githubConfigured = true
        }
        if (status === 200) data = { configured: githubConfigured }
      }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    await page.goto(`${origin}/resources`)
    const card = page.locator('main').getByRole('link', { name: '字幕', exact: true })
    await card.waitFor()
    assert.ok((await card.getAttribute('href')).endsWith('/resources/subtitles'))
    await card.click()
    const assrt = page.locator('form').filter({ has: page.getByRole('heading', { name: '射手网 Assrt', exact: true }) })
    const github = page.locator('form').filter({ has: page.getByRole('heading', { name: '字幕库', exact: true }) })
    const token = page.getByLabel('API Token', { exact: true }), save = assrt.getByRole('button', { name: '保存并验证', exact: true })
    const githubToken = page.getByLabel('GitHub Token（可选）', { exact: true }), githubSave = github.getByRole('button', { name: '保存并验证', exact: true })
    await page.getByText('匿名访问', { exact: true }).waitFor()
    await page.getByText('未配置', { exact: true }).waitFor()
    assert.equal(await token.getAttribute('type'), 'password')
    assert.equal(await githubToken.getAttribute('type'), 'password')
    await token.fill('a'.repeat(32)); await save.click()
    await page.getByText('已配置', { exact: true }).waitFor()
    assert.equal(await token.inputValue(), '')
    await token.fill('b'.repeat(32)); await save.click()
    await page.getByRole('alert').filter({ hasText: 'Token 无效' }).waitFor()
    await page.getByText('已配置', { exact: true }).waitFor()
    await token.fill('')
    await githubToken.fill('github-valid'); await githubSave.click()
    await github.getByText('已配置 Token', { exact: true }).waitFor()
    assert.equal(await githubToken.inputValue(), '')
    assert.equal(configured, true)
    await githubToken.fill('github-invalid'); await githubSave.click()
    await github.getByRole('alert').filter({ hasText: 'Token 无效' }).waitFor()
    await github.getByText('已配置 Token', { exact: true }).waitFor()
    assert.equal(githubConfigured, true)
    assert.equal(configured, true)
    assert.equal(await githubToken.inputValue(), 'github-invalid')
    await githubToken.fill('')
    await page.setViewportSize({ width: 320, height: 900 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true)
    await github.getByRole('button', { name: '清除配置', exact: true }).click()
    assert.equal(githubConfigured, true)
    await github.getByRole('button', { name: '确认清除', exact: true }).click()
    await github.getByText('匿名访问', { exact: true }).waitFor()
    assert.equal(configured, true)
    await assrt.getByRole('button', { name: '清除配置', exact: true }).click()
    await assrt.getByRole('button', { name: '确认清除', exact: true }).click()
    await page.getByText('未配置', { exact: true }).waitFor()
    const githubCalls = calls.filter(c => c.endpoint === '/api/admin/subtitles/github/config')
    assert.deepEqual(githubCalls.map(c => c.method), ['GET', 'POST', 'POST', 'DELETE'])
    assert.deepEqual(githubCalls[1].body, { token: 'github-valid' })
    assert.equal(await page.evaluate(() => [...Object.values(localStorage), ...Object.values(sessionStorage)].some(value => value.includes('github-valid') || value.includes('github-invalid'))), false)
    assert.ok(calls.every(c => !c.endpoint.endsWith('/search') && !c.endpoint.includes('/download-organize')))
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed: ['shared resource card and route', 'masked tokens; save and failure preserve config', 'GitHub and Assrt configure and clear independently', 'GitHub token stays out of browser storage', '320px mobile without overflow; no searches when reading settings'] }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
