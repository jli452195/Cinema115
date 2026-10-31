// Every API request is intercepted. No Telegram code, check or logout reaches a real service.
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    const context = await browser.newContext({ viewport: { width: 1440, height: 950 }, serviceWorkers: 'block' })
    await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin })
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light') })
    const page = await context.newPage(), calls = [], errors = [], passed = [], held = new Map()
    page.setDefaultTimeout(8000); page.on('pageerror', error => errors.push(error.message))
    const avatar = await page.evaluate(() => {
      const canvas = document.createElement('canvas'); canvas.width = canvas.height = 160
      const brush = canvas.getContext('2d'); brush.fillStyle = '#dbeafe'; brush.fillRect(0, 0, 160, 160)
      brush.fillStyle = '#2563eb'; brush.beginPath(); brush.arc(80, 59, 28, 0, Math.PI * 2); brush.fill(); brush.fillRect(35, 104, 90, 56)
      return canvas.toDataURL('image/png')
    })
    const avatarBytes = Buffer.from(avatar.split(',')[1], 'base64')
    const bot = 'keep_resource_bot'
    let state = { apiId: 0, configured: false, botConfigured: false, hasSession: false, authorized: false, name: '', username: '', stage: 'idle', bot }
    let failRead = true, failConfig = false, failSend = false, failSign = false, failCheck = false, failDisconnect = false
    let needPassword = true, restoreSession = false
    const holdNext = key => {
      let release, started
      const item = { wait: new Promise(resolve => { release = resolve }), started: new Promise(resolve => { started = resolve }), release: () => release(), signal: () => started() }
      held.set(key, item); return item
    }
    const pause = async key => { const item = held.get(key); if (item) { held.delete(key); item.signal(); await item.wait } }
    await page.route('**/api/**', async route => {
      const request = route.request(), endpoint = new URL(request.url()).pathname, method = request.method()
      const body = request.postDataJSON?.() || {}; calls.push({ endpoint, method, body })
      if (endpoint.includes('/telegram-relay/avatars/')) {
        await route.fulfill({ status: 200, contentType: 'image/png', body: avatarBytes }).catch(() => {}); return
      }
      let data = { items: [], channels: [], accounts: [], configured: false }, status = 200, waitKey = ''
      if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'mock', role: 'admin' } }
      else if (endpoint.startsWith('/api/admin/telegram-user/')) {
        const action = endpoint.split('/').at(-1); waitKey = action === 'config' && method === 'GET' ? 'read' : action
        if (action === 'config' && method === 'GET') {
          if (failRead) { status = 502; data = { error: '模拟配置读取失败' } }
          else data = { ...state }
        } else if (action === 'config') {
          if (failConfig) { status = 400; data = { error: '模拟凭据保存失败' } }
          else if (state.hasSession && body.apiId !== state.apiId) { status = 409; data = { error: '更换 API 凭据前，请先退出当前 Telegram 用户账号' } }
          else { state = { ...state, apiId: body.apiId, configured: true, bot: body.bot, botConfigured: !!body.bot }; data = { ...state } }
        } else if (action === 'send-code') {
          if (failSend) { status = 429; data = { error: '模拟发送失败，请稍后再试' } }
          else if (state.authorized) { status = 409; data = { error: '当前已有登录账号，请先退出再更换账号' } }
          else { state = { ...state, hasSession: true, stage: 'code' }; data = { ...state } }
        } else if (action === 'sign-in') {
          if (failSign) { status = 400; data = { error: state.stage === 'password' ? '模拟二步验证密码错误' : '模拟验证码错误' } }
          else if (state.stage === 'code' && needPassword) { state = { ...state, stage: 'password' }; data = { ...state } }
          else { state = { ...state, authorized: true, hasSession: true, stage: 'idle', name: '测试账号', username: 'cinema_test', avatarId: '888' }; data = { ...state } }
        } else if (action === 'check') {
          if (failCheck) { status = 502; data = { error: '模拟连接失败' } }
          else { if (restoreSession) state = { ...state, authorized: true, hasSession: true, stage: 'idle', name: '测试账号', username: 'cinema_test', avatarId: '888' }; data = { ...state } }
        } else if (action === 'disconnect') {
          if (failDisconnect) { status = 502; data = { error: '模拟退出失败' } }
          else { state = { ...state, authorized: false, hasSession: false, stage: 'idle', name: '', username: '', avatarId: undefined }; data = { ...state } }
        } else throw new Error(`Unexpected Telegram endpoint: ${method} ${endpoint}`)
      }
      await pause(waitKey)
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
    })
    const dialog = () => page.getByRole('dialog', { name: '登录 Telegram', exact: true })
    const disconnect = () => page.getByRole('dialog', { name: '退出 Telegram 用户账号？', exact: true })
    const requests = (action, method = 'POST') => calls.filter(call => call.endpoint === `/api/admin/telegram-user/${action}` && call.method === method)
    const openLogin = async () => { await page.getByRole('button', { name: /^(登录账号|继续登录)$/ }).click(); await dialog().locator('.tg-login-form').waitFor() }
    const closeLogin = async () => { await dialog().getByRole('button', { name: '关闭 Telegram 登录', exact: true }).click(); await dialog().waitFor({ state: 'detached' }) }
    const paste = async (locator, text) => { await page.evaluate(text => navigator.clipboard.writeText(text), text); await locator.focus(); await locator.press('Control+A'); await locator.press('Control+V'); assert.equal(await locator.inputValue(), text) }
    const noOverflow = async () => assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true)
    await page.goto(origin + '/telegram/user')
    await page.getByRole('alert').filter({ hasText: '模拟配置读取失败' }).waitFor()
    assert.equal(await page.getByRole('button', { name: '保存 API', exact: true }).isDisabled(), true)
    failRead = false; await page.getByRole('button', { name: '重新读取', exact: true }).click()
    await page.getByText('先保存下方 API 凭据，再登录用户账号。', { exact: true }).waitFor()
    assert.equal(await page.getByRole('heading', { name: 'Telegram 用户账号', exact: true }).count(), 1)
    assert.equal(await page.getByRole('link', { name: 'Telegram 配置', exact: true }).count(), 1)
    assert.equal(await page.getByRole('button', { name: '登录账号', exact: true }).isDisabled(), true)
    assert.equal(await page.getByRole('textbox', { name: /Bot/ }).count(), 0)
    assert.equal(requests('send-code').length + requests('check').length + requests('disconnect').length, 0)
    passed.push('API load failure retries; missing credentials guide setup; no Bot input and no automatic login side effects')

    await page.getByLabel('API ID', { exact: true }).fill('1234')
    await page.getByLabel('API Hash', { exact: true }).fill('a'.repeat(32))
    failConfig = true; await page.getByRole('button', { name: '保存 API', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '模拟凭据保存失败' }).waitFor()
    assert.equal(await page.getByLabel('API Hash', { exact: true }).inputValue(), 'a'.repeat(32))
    failConfig = false; const saveHeld = holdNext('config'); await page.getByRole('button', { name: '保存 API', exact: true }).click(); await saveHeld.started
    assert.equal(await page.getByLabel('API ID', { exact: true }).isDisabled(), true)
    saveHeld.release(); await page.getByText('Telegram API 凭据已保存。', { exact: true }).waitFor()
    assert.equal(await page.getByLabel('API Hash', { exact: true }).inputValue(), '')
    assert.deepEqual(requests('config').at(-1).body, { apiId: 1234, apiHash: 'a'.repeat(32), bot })
    await page.getByRole('button', { name: '保存 API', exact: true }).click()
    await page.waitForFunction(() => !document.querySelector('.tg-api-card fieldset').disabled)
    assert.equal(requests('config').at(-1).body.apiHash, ''); assert.equal(state.bot, bot)
    passed.push('API saves preserve the existing Bot, keep edits on error, lock while saving and clear the hash after success')

    await openLogin()
    assert.equal(await dialog().locator('input').count(), 1); assert.equal(requests('send-code').length, 0)
    await dialog().getByLabel('手机号', { exact: true }).fill('+123')
    assert.equal(await dialog().getByRole('button', { name: '发送验证码', exact: true }).isDisabled(), true)
    const firstPhone = '+8613800000000', editedPhone = '+8613900000000'
    await paste(dialog().getByLabel('手机号', { exact: true }), firstPhone)
    failSend = true; await dialog().getByRole('button', { name: '发送验证码', exact: true }).click()
    await dialog().getByRole('alert').filter({ hasText: '模拟发送失败' }).waitFor()
    assert.equal(await dialog().getByLabel('手机号', { exact: true }).inputValue(), firstPhone)
    failSend = false; await dialog().getByRole('button', { name: '发送验证码', exact: true }).click()
    await dialog().getByLabel('验证码', { exact: true }).waitFor()
    assert.equal(await dialog().locator('input').count(), 1)
    const sendsBeforeWait = requests('send-code').length; await page.waitForTimeout(180); assert.equal(requests('send-code').length, sendsBeforeWait)
    await dialog().getByRole('button', { name: '修改手机号', exact: true }).click()
    await dialog().getByLabel('手机号', { exact: true }).fill(editedPhone)
    await dialog().getByRole('button', { name: '继续上次验证', exact: true }).click()
    await dialog().getByRole('button', { name: '重新发送验证码', exact: true }).click()
    await dialog().getByRole('button', { name: '重新发送验证码', exact: true }).waitFor()
    assert.equal(requests('send-code').at(-1).body.phone, firstPhone)
    const signCount = requests('sign-in').length
    await paste(dialog().getByLabel('验证码', { exact: true }), '1234567')
    assert.equal(await dialog().getByLabel('验证码', { exact: true }).getAttribute('maxlength'), null)
    assert.equal(requests('sign-in').length, signCount)
    failSign = true; await dialog().getByRole('button', { name: '验证并继续', exact: true }).click()
    await dialog().getByRole('alert').filter({ hasText: '模拟验证码错误' }).waitFor()
    assert.equal(await dialog().getByLabel('验证码', { exact: true }).inputValue(), '')
    failSign = false; await paste(dialog().getByLabel('验证码', { exact: true }), '12345')
    await dialog().getByRole('button', { name: '验证并继续', exact: true }).click()
    await dialog().getByLabel('二步验证密码', { exact: true }).waitFor()
    assert.deepEqual(requests('sign-in').at(-1).body, { code: '12345', password: '' })
    assert.equal(await dialog().locator('input').count(), 1)
    failSign = true; await dialog().getByLabel('二步验证密码', { exact: true }).fill('mock-password')
    await dialog().getByRole('button', { name: '完成登录', exact: true }).click()
    await dialog().getByRole('alert').filter({ hasText: '模拟二步验证密码错误' }).waitFor()
    assert.equal(await dialog().getByLabel('二步验证密码', { exact: true }).inputValue(), '')
    await dialog().getByLabel('二步验证密码', { exact: true }).fill('clear-on-close')
    await closeLogin(); await openLogin()
    assert.equal(await dialog().getByLabel('二步验证密码', { exact: true }).inputValue(), '')
    assert.equal(await dialog().locator('input').count(), 1)
    failSign = false; await dialog().getByLabel('二步验证密码', { exact: true }).fill('mock-password')
    await dialog().getByRole('button', { name: '完成登录', exact: true }).click(); await dialog().waitFor({ state: 'detached' })
    await page.getByText('Telegram 用户账号登录成功。', { exact: true }).waitFor()
    assert.deepEqual(requests('sign-in').at(-1).body, { code: '', password: 'mock-password' })
    await page.locator('.tg-avatar img').evaluate(image => image.decode())
    assert.equal(await page.locator('.tg-avatar').evaluate(element => element.offsetWidth === 40 && element.offsetHeight === 40 && getComputedStyle(element).borderRadius === '50%'), true)
    passed.push('phone, flexible single pasteable code and 2FA each show one field; explicit resend uses the verified phone; errors and closing clear secrets; no auto-submit')

    await page.getByLabel('API ID', { exact: true }).fill('1235')
    await page.getByRole('button', { name: '保存 API', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '更换 API 凭据前' }).waitFor()
    assert.equal(state.authorized, true); assert.equal(requests('disconnect').length, 0)
    await page.getByLabel('API ID', { exact: true }).fill('1234')
    failCheck = true; await page.getByRole('button', { name: '检查连接', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: '模拟连接失败' }).waitFor()
    failCheck = false; await page.getByRole('button', { name: '检查连接', exact: true }).click()
    await page.getByText('Telegram 连接正常。', { exact: true }).waitFor()
    await page.getByRole('button', { name: '退出账号', exact: true }).click()
    await disconnect().getByRole('button', { name: '保留登录', exact: true }).click()
    assert.equal(requests('disconnect').length, 0)
    await page.getByRole('button', { name: '退出账号', exact: true }).click()
    failDisconnect = true; await disconnect().getByRole('button', { name: '确认退出', exact: true }).click()
    await disconnect().getByRole('alert').filter({ hasText: '模拟退出失败' }).waitFor()
    failDisconnect = false; await disconnect().getByRole('button', { name: '确认退出', exact: true }).click()
    await disconnect().waitFor({ state: 'detached' }); assert.equal(state.bot, bot); assert.equal(state.configured, true)
    passed.push('logged-in API changes remain refused without implicit logout; connection errors retry; logout requires confirmation, keeps config and reports errors')

    state = { ...state, authorized: false, hasSession: true, stage: 'code' }
    await page.reload(); await openLogin()
    assert.equal(await dialog().getByLabel('验证码', { exact: true }).inputValue(), '')
    assert.equal(await dialog().getByRole('button', { name: '重新发送验证码', exact: true }).count(), 0)
    await dialog().getByRole('button', { name: '修改手机号', exact: true }).click()
    assert.equal(await dialog().getByLabel('手机号', { exact: true }).inputValue(), '')
    await paste(dialog().getByLabel('手机号', { exact: true }), firstPhone)
    const sendHeld = holdNext('send-code'); await dialog().getByRole('button', { name: '发送验证码', exact: true }).click(); await sendHeld.started
    await closeLogin(); state = { ...state, stage: 'password' }; await openLogin()
    await dialog().getByLabel('二步验证密码', { exact: true }).waitFor(); sendHeld.release(); await page.waitForTimeout(120)
    assert.equal(await dialog().getByLabel('二步验证密码', { exact: true }).inputValue(), '')
    assert.equal(await dialog().getByLabel('验证码', { exact: true }).count(), 0)
    await closeLogin()
    const readHeld = holdNext('read'); await page.getByRole('button', { name: '继续登录', exact: true }).click(); await readHeld.started
    await closeLogin(); state = { ...state, stage: 'code' }; await openLogin(); readHeld.release(); await page.waitForTimeout(120)
    await dialog().getByLabel('验证码', { exact: true }).waitFor()
    needPassword = false; await dialog().getByLabel('验证码', { exact: true }).fill('7654321')
    const signHeld = holdNext('sign-in'); await dialog().getByRole('button', { name: '验证并继续', exact: true }).click(); await signHeld.started
    await closeLogin(); await page.getByRole('button', { name: '继续登录', exact: true }).click()
    await page.getByText('Telegram 会话已连接。', { exact: true }).waitFor(); signHeld.release(); await page.waitForTimeout(120)
    assert.equal(await page.getByRole('dialog').count(), 0)
    const beforeRestore = { sends: requests('send-code').length, disconnects: requests('disconnect').length }
    state = { ...state, authorized: false, hasSession: true, stage: 'idle' }; await page.reload()
    restoreSession = true; await page.getByRole('button', { name: '检查连接', exact: true }).click()
    await page.getByText('Telegram 连接正常。', { exact: true }).waitFor()
    assert.equal(requests('send-code').length, beforeRestore.sends); assert.equal(requests('disconnect').length, beforeRestore.disconnects)
    restoreSession = false; needPassword = true
    passed.push('server stages resume without knowing the phone; late reads, send-code and sign-in cannot update a closed/new dialog; saved sessions restore without a new code or logout')

    const artifact = path.resolve(__dirname, '../test-results'); await fs.mkdir(artifact, { recursive: true })
    for (const [width, theme] of [[1440, 'light'], [375, 'light'], [375, 'dark']]) {
      await page.setViewportSize({ width, height: 950 }); await page.reload()
      await page.getByText('已连接', { exact: true }).waitFor()
      await page.evaluate(theme => document.documentElement.classList.toggle('dark', theme === 'dark'), theme)
      await noOverflow()
      const fields = await page.locator('.tg-api-fields input').evaluateAll(elements => elements.map(element => element.getBoundingClientRect().top))
      assert.equal(fields[0], fields[1])
      await page.screenshot({ path: path.join(artifact, `telegram-account-${width}-${theme}.png`), fullPage: true })
      state = { ...state, authorized: false, hasSession: false, stage: 'idle', name: '', username: '', avatarId: undefined }
      await page.reload(); await page.getByRole('button', { name: '登录账号', exact: true }).waitFor()
      await page.evaluate(theme => document.documentElement.classList.toggle('dark', theme === 'dark'), theme)
      await openLogin()
      for (const stage of ['phone', 'code', 'password']) {
        await noOverflow()
        assert.equal(await dialog().evaluate(element => { const box = element.getBoundingClientRect(); return box.width <= 471 && box.left >= -1 && box.right <= innerWidth + 1 && box.top >= -1 && box.bottom <= innerHeight + 1 && element.scrollWidth <= element.clientWidth + 1 }), true)
        await page.screenshot({ path: path.join(artifact, `telegram-account-${stage}-${width}-${theme}.png`), fullPage: true })
        if (stage === 'phone') { await dialog().getByLabel('手机号', { exact: true }).fill(firstPhone); await dialog().getByRole('button', { name: '发送验证码', exact: true }).click(); await dialog().getByLabel('验证码', { exact: true }).waitFor() }
        else if (stage === 'code') { await dialog().getByLabel('验证码', { exact: true }).fill('12345'); await dialog().getByRole('button', { name: '验证并继续', exact: true }).click(); await dialog().getByLabel('二步验证密码', { exact: true }).waitFor() }
      }
      await closeLogin(); state = { ...state, authorized: true, hasSession: true, stage: 'idle', name: '测试账号', username: 'cinema_test', avatarId: '888' }
    }
    assert.deepEqual(errors, [])
    assert.equal(requests('config').every(call => call.body.bot === bot), true)
    assert.equal(requests('disconnect').every(call => Object.keys(call.body).length === 0), true)
    passed.push('desktop and 375px light/dark screenshots; API fields stay on one row and all three dialog stages fit without horizontal overflow')
    console.log(JSON.stringify({ passed, artifact, mockedTelegramRequests: calls.filter(call => call.endpoint.includes('/telegram-user/')).length }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
