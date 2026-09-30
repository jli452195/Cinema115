const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage(), errors = [], writes = []
    page.on('pageerror', e => errors.push(e.message))
    const settings = { enabled: true, revision: 3, libraryIds: ['movies'], allowDownload: false, publicUrl: 'https://play.example.com' }
    const members = [
      { userId: 'reader', embyId: 'reader', embyName: 'reader', source: 'primary', skipLibrarySync: false, appliedRevision: 3, syncError: '' },
      { userId: 'admin:owner', embyId: 'owner', embyName: 'owner', administrator: true, skipLibrarySync: true }
    ]
    let reject = false
    await page.route('**/api/**', async route => {
      const request = route.request(), p = new URL(request.url()).pathname
      let data = {}
      if (p === '/api/auth/me') data = { user: { id: 'admin', username: 'admin', role: 'admin', disabled: false } }
      else if (p.endsWith('/library-group')) {
        if (reject) return route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: '分组保存失败' }) })
        const body = request.postDataJSON(); writes.push(body); members[0].skipLibrarySync = body.skipLibrarySync; data = { settings, members }
      } else if (p === '/api/admin/member-playback/accounts') data = { accounts: [] }
      else if (p === '/api/admin/member-playback/libraries') data = { libraries: [{ ItemId: 'movies', Name: '电影' }] }
      else if (p.startsWith('/api/admin/member-playback')) data = { settings, members }
      else if (p === '/api/admin/invites') data = { invites: [], hasMore: false }
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
    })
    const base = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    await page.goto(base + '/emby/users')
    const reader = page.locator('article').filter({ has: page.getByRole('heading', { name: 'reader', exact: true }) })
    const owner = page.locator('article').filter({ has: page.getByRole('heading', { name: 'owner', exact: true }) })
    await reader.getByRole('button', { name: '更多' }).click()
    await reader.getByRole('button', { name: '加入不同步组' }).waitFor()
    assert.equal(await owner.getByLabel('播放账号', {exact: true}).count(), 1)
    assert.equal(await owner.getByRole('button', {name: '移回默认组', exact: true}).count(), 0)
    await page.getByLabel('媒体库同步分组').selectOption('excluded')
    assert.equal(await page.locator('article:visible').count(), 1)
    await page.getByLabel('媒体库同步分组').selectOption('default')
    await reader.getByRole('button', { name: '加入不同步组' }).click()
    await reader.waitFor({ state: 'hidden' })
    await page.getByLabel('媒体库同步分组').selectOption('excluded')
    await reader.getByRole('button', { name: '更多' }).click()
    await reader.getByRole('button', { name: '移回默认组' }).waitFor()
    assert.equal(await page.locator('article:visible').count(), 2)
    await page.reload()
    await reader.getByRole('button', { name: '更多' }).click()
    await reader.getByRole('button', { name: '移回默认组' }).waitFor()
    reject = true
    await reader.getByRole('button', { name: '移回默认组' }).click()
    await page.getByRole('alert').filter({ hasText: '分组保存失败' }).waitFor()
    assert.equal(await reader.getByRole('button', { name: '移回默认组' }).count(), 1)
    reject = false
    await reader.getByRole('button', { name: '移回默认组' }).click()
    await reader.getByRole('button', { name: '更多' }).click()
    await reader.getByRole('button', { name: '加入不同步组' }).waitFor()
    assert.deepEqual(writes, [{ skipLibrarySync: true }, { skipLibrarySync: false }])
    await page.setViewportSize({ width: 390, height: 844 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    await page.goto(base + '/users')
    await page.getByRole('button', { name: '播放设置', exact: true }).click()
    await page.getByText('权限同步：已同步 1 · 待同步 0 · 失败 0 · 不同步 1', { exact: true }).waitFor()
    assert.deepEqual(errors, [])
    console.log('Library group UI passed: admin exclusion, filters, join/leave, reload, failed save, counts, mobile layout')
  } finally { await browser.close() }
})().catch(e => { console.error(e); process.exit(1) })
