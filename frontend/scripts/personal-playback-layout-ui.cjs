const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    page.setDefaultTimeout(12000)
    const errors = [], writes = []
    page.on('pageerror', e => errors.push(e.message))
    await page.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const state = { accountId: 'personal', revision: 1, source: 'personal', canPreparePersonal: true, enabled: true, stage: 'ready', message: '网盘最近检查通过', uid: '123', connected: true, directoryId: '456', pendingDrive: false, suspended: false, embyName: 'owner', embyUrl: 'https://emby.example.com', directoryAccount: { accountId: 'personal', revision: 1, uid: '123', directoryId: '456', creationUncertain: false } }
    const children = { limit: 3, used: 1, items: [{ requestId: 'child-task', userId: 'child', name: 'family_room', completed: true, stage: 'ready', revision: 1, paused: false, disabled: false, blocked: false }] }
    let expiry = 180000, pollStarted = false, releasePoll
    await page.route('**/api/**', async route => {
      const req = route.request(), url = new URL(req.url()), p = url.pathname
      let data = {}
      if (p.startsWith('/api/admin/')) throw new Error('Personal layout called admin API')
      if (p === '/api/auth/me') data = { user: { id: 'owner', username: 'owner', role: 'user' } }
      else if (p === '/api/me/playback') data = state
      else if (p === '/api/me/playback/children') data = children
      else if (p.endsWith('/qrcode')) { writes.push(req.postDataJSON()); data = { qrId: 'test-qr', qrcode: 'not-a-real-login-code', expiresAt: new Date(Date.now() + expiry).toISOString() } }
      else if (p.endsWith('/qrcode/status')) {
        pollStarted = true
        await new Promise(resolve => { releasePoll = resolve })
        data = { status: 'success', playback: { ...state, uid: 'stale-uid', pendingDrive: true } }
      } else if (p.endsWith('/refresh')) data = state
      try { await route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) }) } catch { /* An aborted QR poll has no receiver. */ }
    })
    const url = (process.env.CINEMA_URL || 'http://127.0.0.1:5173') + '/account/playback'
    await page.goto(url)
    await page.getByRole('heading', { name: '我的个人网盘' }).waitFor()
    for (const width of [1440, 390, 320]) for (const dark of [false, true]) {
      await page.setViewportSize({ width, height: 1000 })
      await page.evaluate(d => document.documentElement.classList.toggle('dark', d), dark)
      await page.waitForFunction(d => getComputedStyle(document.querySelector('main header .pp-button')).backgroundColor === (d ? 'rgb(33, 37, 41)' : 'rgb(255, 255, 255)'), dark)
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), `page overflow ${width}/${dark}`)
      assert.equal(await page.locator('main input').count(), 0)
      const aligned = await page.evaluate(() => Math.abs(document.querySelector('h1').getBoundingClientRect().left - document.querySelector('[aria-label="播放概览"]').getBoundingClientRect().left) < 2)
      assert(aligned, 'title and summary must align')
      await page.screenshot({ path: path.join(os.tmpdir(), `cinema115-personal-playback-${width}-${dark ? 'dark' : 'light'}.png`), fullPage: true, animations: 'disabled' })
      await page.getByRole('button', { name: '重新扫码或换号', exact: true }).click()
      const dialog = page.getByRole('dialog')
      assert.equal(await dialog.getByRole('button', { name: '获取二维码' }).isDisabled(), true)
      assert(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth + 1), `dialog overflow ${width}/${dark}`)
      assert(await dialog.locator('footer').evaluate(el => el.getBoundingClientRect().bottom <= innerHeight + 1))
      await page.keyboard.press('Escape')
      assert.equal(await page.getByRole('dialog').count(), 0)
      assert.equal(await page.evaluate(() => document.body.style.overflow), '')
      assert.equal(await page.getByRole('button', { name: '重新扫码或换号', exact: true }).evaluate(el => el === document.activeElement), true)
    }
    await page.getByRole('button', { name: '更换秒传目录', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: '关闭', exact: true }).click()
    // A slow successful response from a closed QR must never install its state.
    await page.getByRole('button', { name: '重新扫码或换号', exact: true }).click()
    await page.getByRole('checkbox', { name: /我同意保存凭据/ }).check()
    await page.getByRole('button', { name: '获取二维码', exact: true }).click()
    await page.getByAltText('115 登录二维码').waitFor()
    for (let i = 0; i < 40 && !pollStarted; i++) await new Promise(resolve => setTimeout(resolve, 100))
    assert(pollStarted)
    await page.getByRole('dialog').getByRole('button', { name: '关闭', exact: true }).click()
    releasePoll()
    await page.getByRole('button', { name: '管理网盘', exact: true }).click()
    await page.getByRole('dialog').getByText('已绑定 UID：123', { exact: true }).waitFor()
    await page.getByRole('dialog').getByRole('button', { name: '关闭', exact: true }).click()
    expiry = 150
    await page.getByRole('button', { name: '重新扫码或换号', exact: true }).click()
    await page.getByRole('checkbox', { name: /我同意保存凭据/ }).check()
    await page.getByRole('button', { name: '获取二维码', exact: true }).click()
    await page.getByText('二维码已过期，请重新获取', { exact: true }).waitFor()
    assert.equal(await page.getByAltText('115 登录二维码').count(), 0)
    await page.keyboard.press('Escape')
    // Read-only personal ownership and external Emby browsing remain independent of source health.
    Object.assign(state, { source: 'assigned', canPreparePersonal: false, enabled: false })
    Object.assign(children.items[0], { blocked: true })
    await page.reload()
    await page.getByText('直链服务暂未运行', { exact: true }).waitFor()
    assert.equal(await page.getByRole('link', { name: '打开 Emby' }).getAttribute('href'), 'https://emby.example.com/')
    assert.equal(await page.getByRole('button', { name: '更换秒传目录', exact: true }).count(), 0)
    assert.equal(await page.getByRole('button', { name: '切换到我的网盘播放', exact: true }).count(), 0)
    assert.equal(await page.getByRole('button', { name: '管理', exact: true }).isDisabled(), true)
    await page.getByRole('button', { name: '管理网盘', exact: true }).click()
    await page.getByRole('button', { name: '解除个人网盘绑定', exact: true }).click()
    await page.getByText('本人当前使用的管理员播放来源不受影响。', { exact: true }).waitFor()
    await page.keyboard.press('Escape')
    Object.assign(state, { source: 'personal', enabled: true, canPreparePersonal: true, stage: 'pending', message: '暂时无法检查网盘登录，请稍后重试' })
    await page.reload()
    await page.getByText('网盘检查需要处理', { exact: true }).waitFor()
    await page.getByRole('button', { name: '检查状态', exact: true }).click()
    await page.getByText('暂时无法检查网盘登录，请稍后重试', { exact: true }).waitFor()
    assert.equal(await page.getByText('检查已完成，请查看当前检查结果', { exact: true }).count(), 0, 'HTTP success must not show a success notice for a failed health result')
    state.embyUrl = 'javascript:alert(1)'
    await page.reload()
    await page.getByText('Emby 入口尚未配置，请联系管理员。', { exact: true }).waitFor()
    assert.equal(await page.getByRole('link', { name: '打开 Emby' }).count(), 0)
    assert.deepEqual(errors, [])
    console.log('Personal playback layout UI passed: 320/390/1440 light/dark, alignment, modal focus and scroll, QR expiry and stale result rejection, read-only ownership, child blocked state, safe Emby links')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
