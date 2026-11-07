// 使用隔离的模拟接口验证排序和持久化交互，不执行真实来源搜索或修改服务器配置。
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const defaults = ['re0', 'bot', 'channels', 'gying', 'mikan', 'garden', 'woniu']
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const errors = [], passed = [], calls = []
  const state = { order: [...defaults], failLoad: false, failSave: false, role: 'admin', animation: true }
  const mock = async route => {
    const request = route.request(), endpoint = new URL(request.url()).pathname, method = request.method()
    calls.push({ endpoint, method, body: request.postDataJSON() })
    let status = 200, data = { items: [], unread: 0 }
    if (endpoint === '/api/auth/me') data = { user: { id: 'mock', username: '测试用户', role: state.role } }
    else if (endpoint === '/api/admin/resources/search-settings') {
      if ((method === 'PUT' && state.failSave) || (method === 'GET' && state.failLoad)) { status = 503; data = { error: method === 'PUT' ? '模拟保存失败，当前排序未写入' : '模拟读取失败，请重试' } }
      else { if (method === 'PUT') state.order = [...request.postDataJSON().order]; data = { order: [...state.order] } }
    } else if (endpoint.endsWith('/nanshare/config') || endpoint.endsWith('/gying/config')) data = { configured: true }
    else if (endpoint.endsWith('/woniu/config')) data = { configured: false }
    else if (endpoint.endsWith('/telegram-user/config')) data = { configured: true, botConfigured: true }
    else if (endpoint.endsWith('/telegram-user/channels')) data = { account: { configured: true, hasSession: true, botConfigured: true }, channels: [{ id: 'mock-channel' }] }
    else if (endpoint.endsWith('/subtitles/config')) data = { assrt: { configured: false } }
    else if (endpoint.includes('/tmdb/detail/')) data = { id: 42, kind: 'tv', title: '排序测试影片', originalTitle: 'Order Test', overview: '隔离界面测试数据', posterUrl: '', backdropUrl: '', date: '2026-11-07', rating: 8, votes: 1, genres: state.animation ? [{ id: 16, name: '动画' }] : [{ id: 18, name: '剧情' }], countries: [], cast: [], seasons: [], collection: null, runtime: 24, episodeCount: 1 }
    else if (endpoint.includes('/emby/media/')) data = { media: { state: 'unknown', items: [] }, baseUrl: '', serverId: '' }
    else if (endpoint.includes('/follows/')) data = { subscribed: false, libraryState: 'unknown' }
    else if (endpoint.includes('/recommendations')) data = { items: [], page: 1, totalPages: 1 }
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
  }
  const init = async context => {
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light') })
    await context.route('**/api/**', mock)
  }
  const orderOf = page => page.locator('[data-source-id]').evaluateAll(rows => rows.map(row => row.dataset.sourceId))
  const pillsOf = page => page.locator('[data-search-source]').evaluateAll(rows => rows.map(row => row.dataset.searchSource))
  const puts = () => calls.filter(call => call.endpoint.endsWith('/resources/search-settings') && call.method === 'PUT')
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await init(context)
    const page = await context.newPage(); page.setDefaultTimeout(8000); page.on('pageerror', error => errors.push(error.message))
    await page.goto(origin + '/resources')
    await page.locator('main').getByRole('link', { name: '搜索设置', exact: true }).click()
    await page.locator('[data-source-id]').nth(6).waitFor()
    assert.deepEqual(await orderOf(page), defaults)
    const save = () => page.getByRole('button', { name: '保存排序', exact: true })
    assert.equal(await save().isDisabled(), true)
    const keyboardHandle = page.locator('[data-source-id="re0"] [data-action="handle"]')
    await keyboardHandle.focus(); await keyboardHandle.press('Alt+ArrowDown')
    assert.deepEqual(await orderOf(page), ['bot', 're0', 'channels', 'gying', 'mikan', 'garden', 'woniu'])
    assert.equal(await keyboardHandle.evaluate(element => element === document.activeElement), true)
    await keyboardHandle.press('Alt+ArrowUp')
    assert.deepEqual(await orderOf(page), defaults)
    assert.equal(await save().isDisabled(), true)
    passed.push('资源配置入口、七个来源和默认顺序读取正确')
    passed.push('键盘调整顺序并保持原来源焦点，回到已保存顺序后清除未保存状态')

    // 后续交互基于页面可访问名称验证，不依赖 Vue 内部状态。
    await page.getByRole('button', { name: '动画作品', exact: true }).click()
    await page.waitForFunction(() => document.querySelectorAll('[data-search-source]').length === 6)
    assert.deepEqual(await pillsOf(page), defaults.filter(id => id !== 'woniu'))
    await page.getByRole('button', { name: '普通影视', exact: true }).click()
    assert.deepEqual(await pillsOf(page), ['re0', 'bot', 'channels', 'gying'])
    assert.equal(puts().length, 0)
    passed.push('实时预览保留未配置隐藏和仅动画来源过滤，编辑不触发后台搜索')

    const row = id => page.locator(`[data-source-id="${id}"]`)
    await row('garden').getByRole('button', { name: /上移/ }).click()
    const moved = ['re0', 'bot', 'channels', 'gying', 'garden', 'mikan', 'woniu']
    assert.deepEqual(await orderOf(page), moved)
    assert.equal(await save().isEnabled(), true)
    await page.locator('main').getByRole('link', { name: '返回 资源配置', exact: true }).click()
    await page.getByRole('dialog', { name: '放弃未保存的搜索顺序？', exact: true }).waitFor()
    await page.getByRole('button', { name: '继续编辑', exact: true }).click()
    await page.getByRole('dialog').waitFor({ state: 'detached' })
    assert.deepEqual(await orderOf(page), moved)
    state.failSave = true
    await save().click()
    await page.getByText('模拟保存失败，当前排序未写入', { exact: true }).waitFor()
    assert.deepEqual(await orderOf(page), moved)
    assert.deepEqual(state.order, defaults)
    state.failSave = false
    await save().click()
    await page.waitForFunction(() => [...document.querySelectorAll('button')].some(button => button.textContent.includes('保存排序') && button.disabled))
    assert.deepEqual(state.order, moved)
    passed.push('上移调整、失败保留草稿、重试保存及未保存状态正确')

    // 使用真实鼠标拖动，确认排序只在明确保存后写入接口。
    const beforeDrag = puts().length
    const grip = row('gying').locator('[data-drag-handle]').first()
    const from = await grip.boundingBox(), to = await row('re0').boundingBox()
    await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2); await page.mouse.down()
    await page.mouse.move(from.x + from.width / 2 + 12, from.y + from.height / 2, { steps: 3 })
    await page.locator('.drag-list-fallback').waitFor()
    await page.mouse.move(to.x + to.width / 2, to.y + 2, { steps: 15 }); await page.waitForTimeout(200)
    await page.mouse.up(); await page.locator('.drag-list-fallback').waitFor({ state: 'detached' })
    const dragged = ['gying', 're0', 'bot', 'channels', 'garden', 'mikan', 'woniu']
    assert.deepEqual(await orderOf(page), dragged)
    assert.equal(puts().length, beforeDrag)
    await save().click()
    await page.waitForFunction(() => [...document.querySelectorAll('button')].some(button => button.textContent.includes('保存排序') && button.disabled))
    passed.push('桌面拖动排序与插入位置正确，拖动过程零保存请求')

    await page.goto(origin + '/tv/42')
    await page.waitForFunction(() => document.querySelectorAll('[data-search-source]').length === 6)
    assert.deepEqual(await pillsOf(page), dragged.filter(id => id !== 'woniu'))
    state.animation = false
    await page.reload()
    await page.waitForFunction(() => document.querySelectorAll('[data-search-source]').length === 4)
    assert.deepEqual(await pillsOf(page), ['gying', 're0', 'bot', 'channels'])
    assert.equal(calls.some(call => /\/(?:search|resource-order)$/.test(call.endpoint)), false)
    passed.push('实际详情页应用服务端顺序，普通影视与动画过滤一致，不执行真实搜索')

    const mobile = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, serviceWorkers: 'block', reducedMotion: 'reduce' })
    await init(mobile)
    const phone = await mobile.newPage(); phone.setDefaultTimeout(8000); phone.on('pageerror', error => errors.push(error.message))
    await phone.goto(origin + '/resources/search')
    await phone.locator('[data-source-id]').nth(6).waitFor()
    assert.deepEqual(await orderOf(phone), dragged)
    const mobileFrom = phone.locator('[data-source-id="re0"] [data-drag-handle]').first()
    await mobileFrom.scrollIntoViewIfNeeded()
    const a = await mobileFrom.boundingBox(), b = await phone.locator('[data-source-id="gying"]').boundingBox()
    const touch = await mobile.newCDPSession(phone)
    const at = (type, point) => touch.send('Input.dispatchTouchEvent', { type, touchPoints: point ? [{ ...point, id: 1 }] : [] })
    const beforeTouch = puts().length
    await at('touchStart', { x: a.x + a.width / 2, y: a.y + a.height / 2 }); await phone.waitForTimeout(350)
    await at('touchMove', { x: a.x + a.width / 2 + 20, y: a.y + a.height / 2 }); await phone.locator('.drag-list-fallback').waitFor()
    for (let i = 1; i <= 8; i++) await at('touchMove', { x: b.x + 25, y: a.y + a.height / 2 + (b.y + 2 - a.y - a.height / 2) * i / 8 })
    await at('touchEnd'); await phone.locator('.drag-list-fallback').waitFor({ state: 'detached' })
    assert.equal((await orderOf(phone))[0], 're0')
    assert.equal(puts().length, beforeTouch)
    await phone.getByRole('button', { name: '保存排序', exact: true }).click()
    await phone.waitForFunction(() => [...document.querySelectorAll('button')].some(button => button.textContent.includes('保存排序') && button.disabled))
    assert.equal(state.order[0], 're0')
    assert.equal(await phone.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    passed.push('手机读取同一服务端顺序，真实触摸长按拖动、保存和窄屏布局通过')

    await fs.mkdir(path.join(__dirname, '../test-results'), { recursive: true })
    for (const [target, name] of [[phone, 'mobile'], [page, 'desktop']]) {
      await target.goto(origin + '/resources/search')
      await target.locator('[data-source-id]').nth(6).waitFor()
      for (const theme of ['light', 'dark']) {
        await target.evaluate(theme => { document.documentElement.classList.toggle('dark', theme === 'dark') }, theme)
        await target.screenshot({ path: path.join(__dirname, `../test-results/search-settings-${name}-${theme}.png`), fullPage: true })
        assert.equal(await target.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
      }
    }
    await mobile.close()
    state.failLoad = true
    await page.reload()
    await page.getByRole('alert').filter({ hasText: '模拟读取失败，请重试' }).waitFor()
    assert.equal(await save().isDisabled(), true)
    state.failLoad = false
    await page.getByRole('button', { name: '重新加载', exact: true }).click()
    await page.locator('[data-source-id]').nth(6).waitFor()
    assert.deepEqual(await orderOf(page), state.order)
    await page.getByRole('button', { name: '恢复默认', exact: true }).click()
    assert.deepEqual(await orderOf(page), defaults)
    assert.notDeepEqual(state.order, defaults)
    await save().click()
    await page.waitForFunction(() => [...document.querySelectorAll('button')].some(button => button.textContent.includes('保存排序') && button.disabled))
    assert.deepEqual(state.order, defaults)
    await page.setViewportSize({ width: 320, height: 844 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    passed.push('深浅主题截图、窄屏无溢出、读取失败阻止覆盖并可重试')
    passed.push('恢复默认先更新草稿，保存后生效；320px无横向溢出')
    assert.deepEqual(errors, [])
    console.log(JSON.stringify({ passed, pageErrors: errors }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
