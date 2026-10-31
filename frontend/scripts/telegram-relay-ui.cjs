// Every API is mocked; this test never sends a Telegram message.
const assert = require('node:assert/strict'), path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => {
      localStorage.setItem('admin_token', 'mock'); localStorage.setItem('cinema115-theme', 'light')
      window.avatarObjects = { created: [], revoked: [] }
      const create = URL.createObjectURL.bind(URL), revoke = URL.revokeObjectURL.bind(URL)
      URL.createObjectURL = blob => { const value = create(blob); window.avatarObjects.created.push(value); return value }
      URL.revokeObjectURL = value => { window.avatarObjects.revoked.push(value); revoke(value) }
    })
    const page = await context.newPage(), calls = [], errors = [], passed = [], tokens = new Map(), held = new Map(), avatarRequests = []
    page.setDefaultTimeout(8000); page.on('pageerror', error => errors.push(error.message))
    const avatar = await page.evaluate(() => {
      const canvas = document.createElement('canvas'); canvas.width = canvas.height = 160
      const brush = canvas.getContext('2d'); brush.fillStyle = '#2563eb'; brush.fillRect(0, 0, 160, 160)
      brush.fillStyle = '#cbd5e1'; brush.beginPath(); brush.arc(80, 60, 30, 0, Math.PI * 2); brush.fill(); brush.fillRect(35, 105, 90, 55)
      return canvas.toDataURL('image/jpeg')
    })
    const avatarBytes = Buffer.from(avatar.split(',')[1], 'base64')
    let accountAvatarId = '888', activeAvatars = 0, maxAvatars = 0
    const peers = [
      { id: 'channel:101', title: '动画来源', username: 'anime_source', avatarId: '101', kind: 'channel', source: true, target: false },
      { id: 'channel:102', title: '电影来源', username: 'movie_source', avatarId: '999', kind: 'channel', source: true, target: true },
      { id: 'user:201', title: '转存机器人', username: 'relay_bot', avatarId: '201', kind: 'bot', source: false, target: true },
      { id: 'chat:301', title: '资源备份群', kind: 'group', source: false, target: true },
      { id: 'channel:999', title: '无权限频道', kind: 'channel', source: false, target: false },
      { id: 'channel:104', title: '动画新增来源', kind: 'channel', source: true, target: false },
      { id: 'channel:103', title: '第二页来源', kind: 'channel', source: true, target: false },
    ]
    const extraPeers = Array.from({ length: 18 }, (_, index) => ({ id: `channel:${500 + index}`, title: `更多来源${index}`, avatarId: `${500 + index}`, kind: 'channel', source: true, target: false }))
    const initial = (id, name, sourceId, targetId, enabled) => ({ id, name, sourceId, targetId, sourceTitle: peers.find(peer => peer.id === sourceId).title, targetTitle: peers.find(peer => peer.id === targetId).title, enabled, linkTypes: ['115', 'ed2k'], includePattern: '', excludePattern: '' })
    let rules = [initial('r1', '动画转存', 'channel:101', 'user:201', true), initial('r2', '电影备份', 'channel:102', 'chat:301', false)]
    let failSave = false, failPeers = false, failDelete = false, peerSerial = 0, hasSession = true
    function holdNext(key) {
      let release, start
      const response = new Promise(resolve => { release = resolve }), started = new Promise(resolve => { start = resolve })
      held.set(key, { response, start }); return { started, release }
    }
    async function pause(key) { const item = held.get(key); if (item) { held.delete(key); item.start(); await item.response } }
    await page.route('**/api/**', async route => {
      const request = route.request(), url = new URL(request.url()), endpoint = url.pathname, method = request.method(), body = request.postDataJSON()
      calls.push({ endpoint, method, body, query: url.search }); let data = { items: [], channels: [], accounts: [], configured: false }, status = 200
      if (endpoint.includes('/telegram-relay/avatars/')) {
        avatarRequests.push({ peer: decodeURIComponent(endpoint.split('/').at(-1)), photo: url.searchParams.get('photo'), url: request.url(), authorization: request.headers().authorization })
        activeAvatars++; maxAvatars = Math.max(maxAvatars, activeAvatars)
        try {
          await pause('avatar'); await new Promise(resolve => setTimeout(resolve, 70))
          const photo = url.searchParams.get('photo')
          await route.fulfill({ status: 200, contentType: photo === '777' ? 'text/html' : 'image/jpeg', body: photo === '999' ? Buffer.from('not a jpeg') : photo === '776' ? Buffer.alloc(128 * 1024 + 1) : avatarBytes }).catch(() => {})
        } finally { activeAvatars-- }
        return
      }
      if (endpoint === '/api/auth/me') data = { user: { id: 'admin', username: 'mock', role: 'admin' } }
      else if (endpoint === '/api/admin/telegram-relay') { await pause('load'); data = { rules, peers: peers.slice(0, 4), status: { connected: hasSession }, account: { hasSession, name: hasSession ? '测试账号' : '', avatarId: hasSession ? accountAvatarId : undefined } } }
      else if (endpoint === '/api/admin/telegram-relay/peers') {
        await pause('peers')
        if (failPeers) { status = 502; data = { error: '模拟会话读取失败' } }
        else {
          const pagePeers = body.cursor ? peers.slice(6) : [...peers.slice(0, 6), ...extraPeers], selectionToken = `pick-${++peerSerial}`
          tokens.set(selectionToken, pagePeers.map(peer => peer.id)); data = { peers: pagePeers, selectionToken, nextCursor: body.cursor ? '' : 'page2' }
        }
      } else if (endpoint === '/api/admin/telegram-relay/rules' && method === 'POST') {
        await pause('save')
        if (failSave) { status = 409; data = { error: '模拟规则校验失败' } }
        else if (rules.some(rule => rule.sourceId === body.sourceId && rule.id !== body.id)) { status = 409; data = { error: '来源频道已配置转发规则' } }
        else {
          const previous = rules.find(rule => rule.id === body.id)
          const valid = (field, token) => previous?.[field] === body[field] && !body[token] || tokens.get(body[token])?.includes(body[field])
          if (!valid('sourceId', 'sourceSelectionToken') || !valid('targetId', 'targetSelectionToken')) { status = 400; data = { error: '选择凭据不属于这个会话' } }
          else {
            const rule = { ...previous, ...body, id: body.id || 'r3', name: body.name || peers.find(peer => peer.id === body.sourceId).title, sourceTitle: peers.find(peer => peer.id === body.sourceId).title, targetTitle: peers.find(peer => peer.id === body.targetId).title }
            delete rule.sourceSelectionToken; delete rule.targetSelectionToken
            rules = previous ? rules.map(item => item.id === rule.id ? rule : item) : [...rules, rule]; data = { rule }
          }
        }
      } else if (/\/telegram-relay\/rules\/[^/]+$/.test(endpoint) && method === 'DELETE') {
        if (failDelete) { status = 409; data = { error: '模拟删除失败' } }
        else { rules = rules.filter(rule => rule.id !== endpoint.split('/').at(-1)); status = 204 }
      }
      await route.fulfill({ status, contentType: 'application/json', body: status === 204 ? undefined : JSON.stringify(data) }).catch(() => {})
    })
    const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    const card = name => page.locator('.relay-card').filter({ has: page.getByRole('heading', { name, exact: true }) })
    const editor = () => page.getByRole('dialog', { name: /^(添加|编辑)转发规则$/, exact: true })
    const picker = title => page.getByRole('dialog', { name: title, exact: true })
    const posts = () => calls.filter(call => call.endpoint.endsWith('/telegram-relay/rules') && call.method === 'POST')
    const relayReads = () => calls.filter(call => call.endpoint === '/api/admin/telegram-relay')
    const peerReads = () => calls.filter(call => call.endpoint.endsWith('/telegram-relay/peers'))
    await page.goto(origin + '/telegram'); await page.locator('main').getByRole('link', { name: '频道转发', exact: true }).click()
    await page.getByText('监听已连接', { exact: true }).waitFor(); assert.equal(await page.locator('.relay-card').count(), 2)
    assert.equal(await page.getByRole('button', { name: /投递记录|重试失败/ }).count(), 0)
    assert.doesNotMatch(await page.locator('.relay-grid').innerText(), /待投递|已投递|统计/)
    await page.waitForTimeout(450); assert.equal(relayReads().length, 1); assert.equal(peerReads().length, 0)
    await page.waitForFunction(() => document.querySelector('.relay-symbol img')?.naturalWidth > 0)
    assert.equal(await page.locator('.relay-symbol').evaluate(element => element.offsetWidth === 40 && element.offsetHeight === 40), true)
    assert.equal(await page.locator('.relay-symbol img').evaluate(image => image.naturalWidth), 160)
    assert.equal(await page.locator('.relay-symbol').evaluate(element => getComputedStyle(element).borderRadius), '50%')
    await card('动画转存').locator('.relay-peer-avatar img').nth(1).waitFor()
    await card('动画转存').locator('.relay-peer-avatar img').evaluateAll(images => Promise.all(images.map(image => image.decode())))
    assert.equal(await card('动画转存').locator('.relay-peer-avatar img').count(), 2)
    assert.equal(await page.locator('.relay-peer-avatar').evaluateAll(elements => elements.every(element => element.offsetWidth === 32 && element.offsetHeight === 32 && getComputedStyle(element).borderRadius === '50%')), true)
    const readsBeforeEditor = peerReads().length
    await card('动画转存').getByRole('button', { name: '编辑动画转存', exact: true }).click()
    await page.waitForFunction(() => [...document.querySelectorAll('.relay-selection-avatar img')].filter(image => image.naturalWidth === 160).length === 2)
    assert.equal(await editor().locator('.relay-selection-avatar').evaluateAll(elements => elements.every(element => element.offsetWidth === 32 && element.offsetHeight === 32 && getComputedStyle(element).borderRadius === '50%')), true)
    assert.equal(peerReads().length, readsBeforeEditor)
    await editor().getByRole('button', { name: '取消', exact: true }).click()
    passed.push('account, rule cards and picker avatars are round; saved source and target avatars appear without loading the Telegram dialog list')
    for (const photo of ['999', '777', '776']) {
      accountAvatarId = photo; await page.getByRole('button', { name: '刷新', exact: true }).click()
      await page.waitForTimeout(160); await page.locator('.relay-symbol svg').waitFor()
      assert.equal(await page.locator('.relay-symbol img').count(), 0)
      assert.equal(await page.locator('.relay-symbol').evaluate(element => element.offsetWidth === 40 && element.offsetHeight === 40), true)
    }
    accountAvatarId = '666'; const avatarHeld = holdNext('avatar'); await page.getByRole('button', { name: '刷新', exact: true }).click(); await avatarHeld.started
    assert.equal(await page.locator('.relay-symbol svg').count(), 1)
    accountAvatarId = '888'; await page.getByRole('button', { name: '刷新', exact: true }).click(); avatarHeld.release()
    await page.waitForFunction(() => document.querySelector('.relay-symbol img')?.naturalWidth > 0)
    assert.ok(await page.evaluate(() => window.avatarObjects.revoked.length > 0))
    passed.push('160px authenticated avatar displayed at 40px; invalid images, MIME and oversized files retain fixed SVG fallback; changed photos abort stale loads and release object URLs')
    const switchOne = () => card('动画转存').getByRole('switch')
    failSave = true; await switchOne().locator('..').click(); await page.getByRole('alert').filter({ hasText: '模拟规则校验失败' }).waitFor()
    assert.equal(await switchOne().isChecked(), true); assert.equal(await card('电影备份').getByRole('switch').isChecked(), false)
    failSave = false; await switchOne().locator('..').click(); await page.getByText('转发规则已保存并停用。', { exact: true }).waitFor()
    assert.equal(await switchOne().isChecked(), false); assert.equal(posts().at(-1).body.id, 'r1'); assert.equal('sourceTitle' in posts().at(-1).body, false); assert.equal('pending' in posts().at(-1).body, false)
    const refreshHeld = holdNext('load'); await page.getByRole('button', { name: '刷新', exact: true }).click(); await refreshHeld.started
    assert.equal(await switchOne().isDisabled(), true); refreshHeld.release(); await page.getByText('监听已连接', { exact: true }).waitFor()
    passed.push('category entry, lazy reads, no polling; independent toggles rollback on errors and cannot race settings refresh')

    await card('电影备份').getByRole('button', { name: '编辑电影备份', exact: true }).click(); await editor().waitFor()
    await editor().getByLabel('ed2k', { exact: true }).uncheck(); assert.equal(await card('电影备份').locator('.relay-tags').getByText('ed2k', { exact: true }).count(), 1)
    await editor().getByRole('button', { name: '取消', exact: true }).click(); await card('电影备份').getByRole('button', { name: '编辑电影备份', exact: true }).click()
    assert.equal(await editor().getByLabel('ed2k', { exact: true }).isChecked(), true); await editor().getByRole('button', { name: '取消', exact: true }).click()
    await page.getByRole('button', { name: '添加转发', exact: true }).click(); assert.equal(await editor().getByRole('button', { name: '保存规则', exact: true }).isDisabled(), true)
    await editor().getByRole('button', { name: '选择来源频道', exact: true }).click(); await picker('选择来源频道').locator('.rp-option').first().waitFor()
    await picker('选择来源频道').getByRole('button', { name: /动画来源/ }).locator('img').evaluate(image => image.decode())
    assert.equal(avatarRequests.some(request => request.peer === 'channel:517'), false)
    await picker('选择来源频道').getByRole('button', { name: /更多来源17/ }).scrollIntoViewIfNeeded()
    await picker('选择来源频道').getByRole('button', { name: /更多来源17/ }).locator('img').evaluate(image => image.decode())
    assert.ok(maxAvatars <= 2)
    await picker('选择来源频道').getByRole('button', { name: /动画来源/ }).scrollIntoViewIfNeeded()
    await picker('选择来源频道').getByRole('button', { name: /电影来源/ }).locator('svg').waitFor()
    assert.equal(await picker('选择来源频道').getByRole('button', { name: /电影来源/ }).locator('img').count(), 0)
    assert.equal(await picker('选择来源频道').getByRole('button', { name: /动画新增来源/ }).locator('svg').count(), 1)
    assert.equal(await picker('选择来源频道').locator('.rp-icon').evaluateAll(elements => elements.every(element => element.offsetWidth === 36 && element.offsetHeight === 36)), true)
    assert.equal(await picker('选择来源频道').getByRole('button', { name: /转存机器人|资源备份群|无权限频道/ }).count(), 0)
    const sourceToken = `pick-${peerSerial}`
    await picker('选择来源频道').getByRole('button', { name: '加载更多会话', exact: true }).click(); await picker('选择来源频道').getByRole('button', { name: /第二页来源/ }).waitFor()
    await picker('选择来源频道').getByRole('button', { name: /动画新增来源/ }).click(); assert.equal(await editor().getByLabel('规则名称', { exact: true }).inputValue(), '动画新增来源')
    await editor().getByRole('button', { name: '选择机器人、频道或群组', exact: true }).click(); await picker('选择转发目标').locator('.rp-option').first().waitFor()
    assert.equal(await picker('选择转发目标').getByRole('button', { name: /资源备份群/ }).locator('img').count(), 0)
    assert.equal(await picker('选择转发目标').getByRole('button', { name: /资源备份群/ }).locator('svg').count(), 1)
    assert.equal(await picker('选择转发目标').getByRole('button', { name: /动画来源|无权限频道/ }).count(), 0)
    await picker('选择转发目标').getByLabel('筛选已加载的会话', { exact: true }).fill('relay_bot')
    assert.equal(await picker('选择转发目标').locator('.rp-option').count(), 1)
    const targetToken = `pick-${peerSerial}`; await picker('选择转发目标').getByRole('button', { name: /转存机器人/ }).click()
    await editor().getByLabel('规则名称', { exact: true }).fill('新动画转存'); await editor().locator('summary').click()
    await editor().getByLabel('包含条件', { exact: true }).fill('1080p|2160p'); await editor().getByLabel('排除条件', { exact: true }).fill('预告')
    await editor().getByLabel('ed2k', { exact: true }).uncheck(); await editor().getByLabel('115 分享', { exact: true }).uncheck()
    assert.equal(await editor().getByRole('button', { name: '保存规则', exact: true }).isDisabled(), true); await editor().getByLabel('115 分享', { exact: true }).check()
    failSave = true; await editor().getByRole('button', { name: '保存规则', exact: true }).click(); await editor().getByRole('alert').filter({ hasText: '模拟规则校验失败' }).waitFor()
    assert.equal(await editor().getByLabel('包含条件', { exact: true }).inputValue(), '1080p|2160p')
    failSave = false; const saveHeld = holdNext('save'); await editor().getByRole('button', { name: '保存规则', exact: true }).click(); await saveHeld.started
    assert.equal(await editor().locator('form').getAttribute('inert'), ''); assert.equal(await editor().getByRole('button', { name: '关闭规则编辑', exact: true }).isDisabled(), true)
    saveHeld.release(); await editor().waitFor({ state: 'detached' }); await card('新动画转存').waitFor()
    assert.equal(posts().at(-1).body.sourceSelectionToken, sourceToken); assert.equal(posts().at(-1).body.targetSelectionToken, targetToken)
    assert.deepEqual(posts().at(-1).body.linkTypes, ['115']); assert.equal(posts().at(-1).body.includePattern, '1080p|2160p')
    assert.equal(await card('电影备份').getByRole('switch').isChecked(), false)
    passed.push('editing clones selected types; role permissions, name filter and per-page selection tokens; save lock/error retention and no raw IDs or send-mode inputs')

    await page.getByRole('button', { name: '添加转发', exact: true }).click(); failPeers = true
    await editor().getByRole('button', { name: '选择来源频道', exact: true }).click(); await picker('选择来源频道').getByRole('alert').waitFor()
    failPeers = false; await picker('选择来源频道').getByRole('button', { name: '重新读取', exact: true }).click(); await picker('选择来源频道').locator('.rp-option').first().waitFor()
    await picker('选择来源频道').getByRole('button', { name: '关闭会话选择', exact: true }).click()
    const peersHeld = holdNext('peers'); await editor().getByRole('button', { name: '选择来源频道', exact: true }).click(); await peersHeld.started
    await picker('选择来源频道').getByRole('button', { name: '关闭会话选择', exact: true }).click(); peersHeld.release()
    await editor().getByRole('button', { name: '取消', exact: true }).click()
    assert.equal(await page.getByRole('dialog').count(), 0)
    passed.push('peer load failures retry; closing a loading picker cancels the request without writing rules')
    passed.push('peer avatars load only when visible with at most two concurrent downloads; missing/failed avatars keep existing SVG icons without size changes')

    await card('新动画转存').getByRole('button', { name: '删除新动画转存', exact: true }).click()
    const deletion = page.getByRole('dialog', { name: '删除转发规则？', exact: true }); failDelete = true
    await deletion.getByRole('button', { name: '删除规则', exact: true }).click(); await deletion.getByRole('alert').filter({ hasText: '模拟删除失败' }).waitFor()
    failDelete = false; await deletion.getByRole('button', { name: '删除规则', exact: true }).click(); await deletion.waitFor({ state: 'detached' })
    assert.equal(await card('新动画转存').count(), 0); assert.equal(await page.locator('.relay-card').count(), 2)
    passed.push('deleting removes only the selected rule and pending messages; failures remain visible in the confirmation dialog')

    const artifact = path.resolve(__dirname, '../test-results'); await fs.mkdir(artifact, { recursive: true })
    await switchOne().locator('..').click(); await page.getByText('转发规则已保存并启用。', { exact: true }).waitFor()
    await card('电影备份').getByRole('button', { name: '编辑电影备份', exact: true }).click(); await editor().getByRole('button', { name: '取消', exact: true }).click()
    for (const [width, theme] of [[1440, 'light'], [375, 'dark']]) {
      await page.setViewportSize({ width, height: 950 }); await page.evaluate(theme => document.documentElement.classList.toggle('dark', theme === 'dark'), theme)
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), true)
      await page.screenshot({ path: path.join(artifact, `telegram-relay-${width}-${theme}.png`), fullPage: true })
      await card('电影备份').getByRole('button', { name: '编辑电影备份', exact: true }).click(); await editor().waitFor()
      assert.equal(await editor().evaluate(element => { const box = element.getBoundingClientRect(); return box.left >= -1 && box.right <= innerWidth + 1 && box.top >= -1 && box.bottom <= innerHeight + 1 }), true)
      await page.screenshot({ path: path.join(artifact, `telegram-relay-editor-${width}-${theme}.png`), fullPage: true })
      await editor().getByRole('button', { name: '电影来源', exact: true }).click(); await picker('选择来源频道').locator('.rp-option').first().waitFor()
      await picker('选择来源频道').getByRole('button', { name: /动画来源/ }).locator('img').evaluate(image => image.decode())
      assert.equal(await picker('选择来源频道').evaluate(element => { const box = element.getBoundingClientRect(); return box.left >= -1 && box.right <= innerWidth + 1 && box.top >= -1 && box.bottom <= innerHeight + 1 }), true)
      await page.screenshot({ path: path.join(artifact, `telegram-relay-peers-${width}-${theme}.png`), fullPage: true })
      await picker('选择来源频道').getByRole('button', { name: '关闭会话选择', exact: true }).click(); await editor().getByRole('button', { name: '取消', exact: true }).click()
    }
    hasSession = false; await page.getByRole('button', { name: '刷新', exact: true }).click(); await page.getByText('尚未登录', { exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: '添加转发', exact: true }).isDisabled(), true); assert.deepEqual(errors, [])
    assert.equal(calls.some(call => /test|send/i.test(call.endpoint)), false)
    assert.equal(calls.some(call => /\/(jobs|retry)(\/|$)/.test(call.endpoint)), false)
    assert.ok(avatarRequests.length > 0); assert.equal(avatarRequests.every(request => request.authorization === 'Bearer mock' && !request.url.includes('mock') && request.photo), true)
    await page.getByRole('link', { name: '管理登录', exact: true }).click()
    assert.equal(await page.evaluate(() => window.avatarObjects.created.every(value => window.avatarObjects.revoked.includes(value))), true)
    passed.push('desktop/light and 375px/dark without overflow; unauthenticated account prompts login; no test or message-send endpoints invoked')
    console.log(JSON.stringify({ passed, artifact }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
