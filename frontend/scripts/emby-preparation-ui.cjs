const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1200, height: 950 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage(), errors = [], writes = []
    page.on('pageerror', e => errors.push(e.message))
    let config = { enabled: false, debug: false, prewarm: false, port: 8097, url: '', accountId: '', embyPathPrefix: '', directLinkCacheMinutes: 9 }
    await page.route('**/api/**', async route => {
      const req = route.request(), url = new URL(req.url()); let data = {}
      if (url.pathname === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin' } }
      else if (url.pathname === '/api/admin/p115/accounts') data = { accounts: [] }
      else if (url.pathname === '/api/admin/member-playback/settings' || url.pathname === '/api/admin/member-playback') data = { settings: {enabled:false,libraryIds:[],allowDownload:false,publicUrl:''}, proxy:{listening:false} }
      else if (url.pathname === '/api/admin/emby/playback-cache/users') data = {users:[],members:[],embyUrl:''}
      else if (url.pathname === '/api/admin/emby/instant-upload') data = {config:{userId:'',sourceAccountId:'',accountId:'',targetCid:'',targetPath:''},records:[],running:false}
      else if (url.pathname === '/api/admin/emby/proxy') {
        if (req.method() === 'POST') { config = req.postDataJSON(); writes.push(config) }
        data = { config, listening: false, error: '' }
      }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
    })
    const base = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    await page.goto(base + '/emby/proxy')
    const prepare = page.getByRole('switch', { name: '提前准备秒传文件', exact: true })
    const links = page.getByRole('switch', { name: '提前获取播放直链', exact: true })
    await page.getByRole('button', { name: '保存配置', exact: true }).waitFor()
    assert.equal(await prepare.isChecked(), true)
    assert.equal(await links.isChecked(), false)
    await page.getByRole('button', { name: '保存配置', exact: true }).click()
    await page.getByText('302 已关闭，停止接管新请求；现有 Emby 权限保留。', { exact: true }).waitFor()
    assert.equal(writes.at(-1).prepareFiles, true)
    assert.equal(writes.at(-1).prewarm, false)
    await prepare.uncheck({ force: true })
    await Promise.all([
      page.waitForResponse(r => r.url().endsWith('/api/admin/emby/proxy') && r.request().method() === 'POST'),
      page.getByRole('button', { name: '保存配置', exact: true }).click(),
    ])
    await page.reload()
    await prepare.waitFor()
    assert.equal(await prepare.isChecked(), false)
    assert.equal(await links.isChecked(), false)
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    await page.setViewportSize({ width: 390, height: 844 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    assert.deepEqual(errors, [])
    console.log('Preparation UI passed: default on, independent link switch, persisted off, mobile dark layout')
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exitCode = 1 })
