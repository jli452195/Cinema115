// All API requests are mocked: this check never creates real Emby accounts.
const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const passed = [], pageErrors = []
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    const page = await context.newPage()
    page.setDefaultTimeout(10000)
    page.on('pageerror', error => pageErrors.push(error.message))
    const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    const invite = '01234567-89abcdef-01234567-89abcdef'
    const requests = []
    let inviteMode = 'personal', registerMode = 'success', loginMode = 'reject', delayInvite = false, releaseInvite, retryHeaders = { 'Retry-After': '2' }
    await page.route('**/api/**', async route => {
      const req = route.request(), endpoint = new URL(req.url()).pathname
      const input = req.postData() ? req.postDataJSON() : null
      requests.push({ endpoint, input })
      let status = 200, data = {}, headers = {}
      if (endpoint === '/api/auth/invite/check') {
        if (delayInvite) await new Promise(resolve => { releaseInvite = resolve })
        if (inviteMode === 'invalid') { status = 400; data = { error: '邀请码无效、已过期、已撤销或已使用；如果刚刚注册过，请尝试登录' } }
        else if (inviteMode === 'network') { await route.abort('failed'); return }
        else if (inviteMode === 'rate') { status = 429; data = { error: '注册尝试过于频繁，请稍后重试' }; headers = retryHeaders }
        else data = { valid: true, playbackSource: inviteMode }
      } else if (endpoint === '/api/auth/register') {
        if (registerMode === 'network') { await route.abort('failed'); return }
        if (registerMode === 'pending') { status = 400; data = { error: 'Emby 创建结果待确认，请使用原邀请重试；不会重复创建，持续未确认请联系管理员' } }
        else if (registerMode === 'exists') { status = 400; data = { error: 'Emby 用户名已存在，请直接登录或使用其他名称' } }
        else if (registerMode === 'invalid') { status = 400; data = { error: '邀请码无效、已过期、已撤销或已使用；如果刚刚注册过，请尝试登录' } }
        else if (registerMode === 'rate') { status = 429; data = { error: '注册尝试过于频繁，请稍后重试' }; headers = retryHeaders }
        else if (registerMode === 'malformed') data = null
        else { status = 201; data = { user: { username: input.username } } }
      } else if (['/api/auth/login', '/api/auth/admin/login'].includes(endpoint)) {
        if (loginMode === 'rate') { status = 429; data = { error: '登录尝试过于频繁，请稍后重试' }; headers = retryHeaders }
        else if (loginMode === 'success') data = { token: 'mock-auth-token' }
        else { status = 401; data = { error: 'Emby 账号或密码不正确，或账号已停用' } }
      } else if (endpoint === '/api/auth/me') data = { user: { id: 'mock-user', role: 'user', username: 'testuser' } }
      else if (endpoint.startsWith('/api/follows')) data = { items: [], hasMore: false }
      await route.fulfill({ status, headers, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const go = url => page.goto(origin + url, { waitUntil: 'networkidle' })
    const count = endpoint => requests.filter(x => x.endpoint === endpoint).length
    const verify = async (mode = 'personal') => {
      inviteMode = mode
      await go('/register')
      await page.getByLabel('邀请码', { exact: true }).fill(invite)
      await page.getByRole('button', { name: '验证邀请码', exact: true }).click()
      await page.getByLabel('Emby 用户名', { exact: true }).waitFor()
    }
    const fillAccount = async (name = 'testuser') => {
      await page.getByLabel('Emby 用户名', { exact: true }).fill(name)
      await page.getByLabel('设置密码', { exact: true }).fill('password123')
      await page.getByLabel('确认密码', { exact: true }).fill('password123')
    }
    const create = () => page.getByRole('button', { name: '创建 Emby 账号', exact: true }).click()
    const waitPending = () => page.getByText('尚未确认创建结果', { exact: true }).waitFor()

    for (const width of [1440, 390, 320]) {
      await page.setViewportSize({ width, height: width === 1440 ? 1000 : 844 })
      for (const dark of [false, true]) {
        for (const url of ['/login', '/register', '/admin/login']) {
          await go(url)
          await page.evaluate(dark => document.documentElement.classList.toggle('dark', dark), dark)
          const layout = await page.evaluate(() => {
            const card = document.querySelector('.auth-card').getBoundingClientRect()
            const input = document.querySelector('input')
            return { width: innerWidth, scroll: document.documentElement.scrollWidth, left: card.left, right: card.right, top: card.top, font: parseFloat(getComputedStyle(input).fontSize), svg: !!document.querySelector('.auth-brand-logo path') }
          })
          assert(layout.scroll <= layout.width && layout.left >= 0 && layout.right <= width && layout.top >= 0)
          assert(layout.font >= 16 && layout.svg)
        }
      }
    }
    passed.push('desktop-mobile-themes-no-overflow-svg-and-16px-inputs')

    await go('/login?redirect=%2Ffollows')
    await page.getByLabel('Emby 用户名', { exact: true }).fill('testuser')
    await page.getByLabel('密码', { exact: true }).fill('password123')
    await page.getByRole('button', { name: '显示密码', exact: true }).click()
    assert.equal(await page.getByLabel('密码', { exact: true }).getAttribute('type'), 'text')
    await page.getByRole('button', { name: '隐藏密码', exact: true }).click()
    await page.getByLabel('密码', { exact: true }).press('Enter')
    await page.getByRole('alert').waitFor()
    assert.equal(requests.at(-1).endpoint, '/api/auth/login')
    await page.getByRole('link', { name: '有邀请码？创建 Emby 账号' }).click()
    assert.equal(new URL(page.url()).searchParams.get('redirect'), '/follows')
    assert.equal(await page.getByLabel('Emby 用户名', { exact: true }).count(), 0)
    passed.push('keyboard-login-password-toggle-and-registration-redirect')

    inviteMode = 'assigned'
    await go('/register?redirect=%2Ffollows#invite=' + invite)
    await page.getByText('邀请码有效：无需自备网盘').waitFor()
    assert.equal(new URL(page.url()).hash, '')
    assert.equal(new URL(page.url()).searchParams.get('redirect'), '/follows')
    assert.equal(await page.evaluate(() => document.activeElement.tagName), 'BODY')
    const checkCount = count('/api/auth/invite/check')
    await page.getByLabel('Emby 用户名', { exact: true }).fill('preserved')
    await page.getByRole('button', { name: '更换邀请码' }).click()
    assert.equal(await page.getByLabel('Emby 用户名', { exact: true }).count(), 0)
    inviteMode = 'personal'
    await page.getByLabel('邀请码', { exact: true }).fill('abcdef01-23456789-abcdef01-23456789')
    await page.getByRole('button', { name: '验证邀请码', exact: true }).click()
    await page.getByText('邀请码有效：自备个人网盘').waitFor()
    assert.equal(await page.getByLabel('Emby 用户名', { exact: true }).inputValue(), 'preserved')
    assert.equal(count('/api/auth/invite/check'), checkCount + 1)
    passed.push('hash-cleared-auto-check-source-summary-no-keyboard-and-change-invalidates')

    await fillAccount()
    await page.getByLabel('确认密码', { exact: true }).fill('different123')
    const beforeMismatch = count('/api/auth/register')
    await create()
    await page.getByText('两次输入的密码不一致，请重新确认').waitFor()
    assert.equal(count('/api/auth/register'), beforeMismatch)
    await page.getByLabel('确认密码', { exact: true }).fill('password123')
    await page.getByRole('button', { name: '显示设置密码' }).click()
    assert.equal(await page.getByLabel('设置密码', { exact: true }).getAttribute('type'), 'text')
    await create()
    await page.waitForURL(url => url.pathname === '/login' && url.searchParams.get('redirect') === '/follows')
    await page.getByText('Emby 账号已创建，请使用刚设置的密码登录。').waitFor()
    assert.equal(await page.getByLabel('Emby 用户名', { exact: true }).inputValue(), 'testuser')
    assert.equal(await page.getByLabel('密码', { exact: true }).inputValue(), '')
    assert.equal(new URL(page.url()).searchParams.get('redirect'), '/follows')
    const saved = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }))
    assert(!saved.includes('password123') && !saved.includes(invite))
    assert.deepEqual(Object.keys(requests.findLast(x => x.endpoint === '/api/auth/register').input).sort(), ['code', 'password', 'username'])
    passed.push('mismatch-local-validation-success-prefill-no-password-storage-no-source-selection')

    for (const mode of ['pending', 'network', 'malformed']) {
      await verify()
      await fillAccount()
      registerMode = mode
      await create(); await waitPending()
      const original = requests.findLast(x => x.endpoint === '/api/auth/register').input
      assert.equal(await page.getByLabel('Emby 用户名', { exact: true }).getAttribute('readonly'), '')
      const originalCount = count('/api/auth/register')
      await page.getByRole('button', { name: '尝试登录', exact: true }).click()
      assert.equal(await page.getByLabel('密码', { exact: true }).inputValue(), original.password)
      assert.equal(await page.getByLabel('密码', { exact: true }).getAttribute('readonly'), '')
      await page.getByRole('button', { name: '登录个人中心', exact: true }).click()
      await page.getByRole('alert').waitFor()
      await page.getByRole('button', { name: '返回继续确认注册' }).click()
      await waitPending()
      assert.equal(count('/api/auth/register'), originalCount)
      registerMode = 'rate'
      await page.getByRole('button', { name: '继续确认注册', exact: true }).click()
      await page.getByText(/请在 .* 秒后重试/).waitFor()
      assert.equal(await page.getByLabel('Emby 用户名', { exact: true }).getAttribute('readonly'), '')
      await page.getByRole('button', { name: '继续确认注册', exact: true }).waitFor({ state: 'visible' })
      await page.waitForFunction(() => !document.querySelector('button[type="submit"]').disabled)
      registerMode = 'invalid'
      await page.getByRole('button', { name: '继续确认注册', exact: true }).click()
      await page.getByText(/邀请码无效、已过期、已撤销或已使用/).waitFor()
      assert.equal(await page.getByLabel('Emby 用户名', { exact: true }).getAttribute('readonly'), '')
      registerMode = 'success'
      await page.getByRole('button', { name: '继续确认注册', exact: true }).click()
      await page.waitForURL('**/login')
      assert.deepEqual(requests.findLast(x => x.endpoint === '/api/auth/register').input, original)
    }
    passed.push('uncertain-result-in-place-login-keeps-input-no-auto-retry-429-keeps-reservation')

    await verify(); await fillAccount()
    registerMode = 'exists'; await create()
    await page.getByText('Emby 用户名已存在，请直接登录或使用其他名称').waitFor()
    assert.equal(await page.getByLabel('Emby 用户名', { exact: true }).getAttribute('readonly'), null)
    registerMode = 'invalid'; await create()
    await page.getByText(/邀请码无效、已过期、已撤销或已使用/).waitFor()
    assert.equal(await page.getByRole('button', { name: '验证邀请码', exact: true }).count(), 1)
    passed.push('known-preflight-errors-remain-editable-expired-invite-rechecks')

    await verify(); await fillAccount()
    registerMode = 'rate'; await create()
    await page.getByText(/请在 .* 秒后重试/).waitFor()
    assert.equal(await page.getByLabel('Emby 用户名', { exact: true }).getAttribute('readonly'), null)
    assert.equal(await page.getByText('尚未确认创建结果', { exact: true }).count(), 0)
    await page.waitForFunction(() => !document.querySelector('button[type="submit"]').disabled)
    passed.push('first-429-preflight-does-not-lock-unused-registration')

    for (const mode of ['invalid', 'network', 'unknown']) {
      inviteMode = mode
      await go('/register'); await page.getByLabel('邀请码', { exact: true }).fill(invite)
      await page.getByRole('button', { name: '验证邀请码', exact: true }).click()
      await page.getByRole('alert').waitFor()
      assert.equal(await page.getByLabel('Emby 用户名', { exact: true }).count(), 0)
      if (mode === 'network') assert((await page.getByRole('alert').innerText()).includes('网络'))
    }
    inviteMode = 'rate'; await go('/register'); await page.getByLabel('邀请码', { exact: true }).fill(invite)
    await page.getByRole('button', { name: '验证邀请码', exact: true }).click()
    await page.getByText(/请在 .* 秒后重试/).waitFor()
    assert(await page.getByRole('button', { name: '验证邀请码', exact: true }).isDisabled())
    await page.waitForFunction(() => !document.querySelector('button[type="submit"]').disabled)
    inviteMode = 'personal'; delayInvite = true
    await page.getByRole('button', { name: '验证邀请码', exact: true }).click()
    await page.waitForFunction(() => document.querySelector('input[name="invite"]').disabled)
    await page.evaluate(() => {
      const input = document.querySelector('input[name="invite"]')
      input.value = 'different-invitation'
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    delayInvite = false; releaseInvite()
    await page.waitForFunction(() => !document.querySelector('input[name="invite"]').disabled)
    assert.equal(await page.getByLabel('Emby 用户名', { exact: true }).count(), 0)
    passed.push('invalid-network-unknown-source-rate-limit-and-stale-invite-response')

    inviteMode = 'personal'; delayInvite = true
    await go('/register'); await page.getByLabel('邀请码', { exact: true }).fill(invite)
    await page.getByRole('button', { name: '验证邀请码', exact: true }).click()
    await page.waitForFunction(() => document.querySelector('input[name="invite"]').disabled)
    await page.getByRole('link', { name: '已有 Emby 账号？返回登录' }).click()
    delayInvite = false; releaseInvite()
    await page.getByRole('heading', { name: '欢迎回来' }).waitFor()
    assert.equal(await page.getByText('邀请码有效：自备个人网盘').count(), 0)
    passed.push('leaving-page-cancels-request-without-stale-state')

    loginMode = 'rate'
    retryHeaders = { Date: 'Wed, 21 Oct 2026 07:28:00 GMT', 'Retry-After': 'Wed, 21 Oct 2026 07:28:02 GMT' }
    await page.getByLabel('Emby 用户名', { exact: true }).fill('testuser')
    await page.getByLabel('密码', { exact: true }).fill('password123')
    await page.getByRole('button', { name: '登录个人中心', exact: true }).click()
    await page.getByText(/请在 [12] 秒后重试/).waitFor()
    assert(await page.getByRole('button', { name: '登录个人中心', exact: true }).isDisabled())
    await page.waitForFunction(() => !document.querySelector('button[type="submit"]').disabled)
    retryHeaders = {}
    await page.getByRole('button', { name: '登录个人中心', exact: true }).click()
    await page.getByText(/请在 (59|60) 秒后重试/).waitFor()
    passed.push('login-rate-limit-http-date-uses-server-clock-and-missing-header-defaults')
    loginMode = 'reject'; retryHeaders = { 'Retry-After': '2' }

    await go('/admin/login')
    await page.getByLabel('管理员账号', { exact: true }).fill('administrator')
    await page.getByLabel('密码', { exact: true }).fill('password123')
    await page.getByRole('button', { name: '登录管理后台', exact: true }).click()
    await page.getByRole('alert').waitFor()
    assert.equal(requests.at(-1).endpoint, '/api/auth/admin/login')
    inviteMode = 'personal'; registerMode = 'pending'
    await go('/register?redirect=%2Ffollows#invite=' + invite)
    await page.getByLabel('Emby 用户名', { exact: true }).waitFor()
    await fillAccount(); await create(); await waitPending()
    loginMode = 'success'
    await page.getByRole('button', { name: '尝试登录', exact: true }).click()
    await page.getByRole('button', { name: '登录个人中心', exact: true }).click()
    await page.waitForURL(url => url.pathname === '/follows')
    await page.getByRole('heading', { name: '我的追更', exact: true }).waitFor()
    passed.push('successful-recovery-login-emits-session-and-returns-to-original-page')
    assert.deepEqual(pageErrors, [])
    passed.push('independent-admin-endpoint-and-no-page-errors')
    console.log(JSON.stringify({ passed, pageErrors }))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
