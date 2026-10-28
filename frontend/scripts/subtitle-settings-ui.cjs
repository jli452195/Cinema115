// Mock APIs only: no credentials or 115 tasks are changed.
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage(), calls = [], errors = []
    page.setDefaultTimeout(8000); page.on('pageerror', e => errors.push(e.message))
    let configured = false
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
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    await page.goto(`${origin}/resources`)
    const card = page.locator('main').getByRole('link', { name: '字幕', exact: true })
    await card.waitFor()
    assert.ok((await card.getAttribute('href')).endsWith('/resources/subtitles'))
    await card.click()
    const token = page.getByLabel('API Token', { exact: true }), save = page.getByRole('button', { name: '保存并验证', exact: true })
    await page.getByText('无需配置', { exact: true }).waitFor()
    await page.getByText('未配置', { exact: true }).waitFor()
    assert.equal(await token.getAttribute('type'), 'password')
    await token.fill('a'.repeat(32)); await save.click()
    await page.getByText('已配置', { exact: true }).waitFor()
    assert.equal(await token.inputValue(), '')
    await token.fill('b'.repeat(32)); await save.click()
    await page.getByRole('alert').filter({ hasText: 'Token 无效' }).waitFor()
    await page.getByText('已配置', { exact: true }).waitFor()
    const artifact = path.join(os.tmpdir(), 'cinema115-assrt-ui'); await fs.mkdir(artifact, { recursive: true })
    await token.fill('')
    for (const [width, theme] of [[1440, 'light'], [390, 'light'], [320, 'dark']]) {
      await page.setViewportSize({ width, height: 900 }); await page.evaluate(t => document.documentElement.classList.toggle('dark', t === 'dark'), theme)
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true)
      await page.screenshot({ path: path.join(artifact, `settings-${width}-${theme}.png`) })
    }
    await page.getByRole('button', { name: '清除配置', exact: true }).click()
    await page.getByRole('button', { name: '确认清除', exact: true }).click()
    await page.getByText('未配置', { exact: true }).waitFor()
    assert.ok(calls.every(c => !c.endpoint.endsWith('/search') && !c.endpoint.includes('/download-organize')))
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed: ['shared resource card and route', 'masked token, save verification and failure preserves config', 'explicit clearing; no searches when reading settings', '320px mobile and dark theme'], artifact }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
