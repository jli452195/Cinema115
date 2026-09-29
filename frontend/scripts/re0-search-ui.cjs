// Mock remote submission: no real unlocks, points consumption or STRM writes.
const path = require('node:path'), os = require('node:os'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock'))
    const page = await context.newPage(), errors = [], searches = [], submissions = []
    page.on('pageerror', error => errors.push(error.message))
    let role = 'admin', configured = true, empty = false, changed = false, multiple = false, missingAccount = false, unlockOnRefresh = false, refreshFails = false, unknownNext = false, dropNext = false, failRecovery = false
    const receipts = {}
    const transfers = {}, transferCalls = []
    let transferState = 'accepted', transferMultiple = false, transferDrop = false
    const resources = [{ slug: 'one', key: 'one', name: '1-192 周更', remark: '', title: '凡人修仙传', pan_type: '115', video_resolution: ['4K'], resource_sources: [], share_size: '402.32GB', publisher: '发布者', publish_time: '2026-09-24', subtitle_language: ['简中'], subtitle_type: ['内封'], unlock_points: 10, is_unlocked: false, unlocked_users_count: 2, is_official: false, validate_status: 'invalid' }, { slug: 'two', key: 'two', name: '高清合集', pan_type: '115', unlock_points: 0, video_resolution: null, subtitle_language: null, subtitle_type: null, is_unlocked: true }]
    await page.route('**/api/**', async route => {
      const req = route.request(), u = new URL(req.url()); let data = {}, status = 200
      if (u.pathname === '/api/auth/me') data = { user: { id: 'mock', username: 'mock', role, disabled: false } }
      else if (u.pathname.endsWith('/nanshare/config')) data = { configured }
      else if (u.pathname.includes('/tmdb/detail/')) data = { id: 106449, kind: u.pathname.includes('/movie/') ? 'movie' : 'tv', title: '凡人修仙传', originalTitle: '', overview: '', posterUrl: '', backdropUrl: '', date: '2020', rating: 8, votes: 1, tagline: '', status: 'Returning Series', runtime: 24, episodeCount: 192, genres: [], countries: [], cast: [], seasons: [], collection: null, imdbId: '', imdbUrl: '', tmdbUrl: '' }
      else if (u.pathname.endsWith('/nanshare/search')) {
        if (failRecovery) return route.abort('failed')
        searches.push(req.postDataJSON())
        if (req.postDataJSON().refresh && refreshFails) { status = 502; data = { error: '刷新暂不可用' } }
        else if (!configured) { status = 400; data = { error: '请先登录 NanShare' } }
        else data = { id: 'search-one', items: empty ? [] : resources, submissions: receipts, transfers, expiresAt: new Date(Date.now()+900000).toISOString() }
        if (req.postDataJSON().refresh && unlockOnRefresh) resources[0].is_unlocked = true
      } else if (u.pathname.includes('/nanshare/transfer-options/')) data = { accountId: 'remote-primary', folders: transferMultiple ? [{ id: '3486868885414610176', name: '电影' }, { id: '3486868885414610177', name: '剧集' }] : [{ id: '3486868885414610176', name: '转存目录' }] }
      else if (u.pathname.endsWith('/nanshare/transfer')) {
        const input = req.postDataJSON(); transferCalls.push(input)
        if (transferDrop) { transferDrop = false; return route.abort('failed') }
        data = transfers[input.slug] = { requestId: input.requestId, state: transferState, message: ({ accepted: '转存成功，共 1 个', points_unknown: '无法确认资源积分，已按积分限制策略跳过', already_saved: '该文件已转存过，请在网盘核查' })[transferState] }
      } else if (u.pathname.includes('/nanshare/share-options/')) data = { version: 'snapshot', accountId: missingAccount ? '' : 'remote-115', modes: multiple ? ['normal', 'reshare'] : ['reshare'], folders: multiple ? [{ index: 0, name: 'strm', path: '/strm/待整理' }, { index: 3, name: 'movie', path: '/strm/电影' }] : [{ index: 0, name: 'strm', path: '/strm/待整理' }] }
      else if (u.pathname.endsWith('/nanshare/submit')) {
        const input = req.postDataJSON(); submissions.push(input)
        if (dropNext) { dropNext = false; failRecovery = true; return route.abort('failed') }
        if (changed) { status = 409; data = { error: 'NanShare 配置已变化，请刷新搜索并重新选择' } }
        else { receipts[input.slug] = { requestId: input.requestId, state: unknownNext ? 'unknown' : 'accepted', message: unknownNext ? '未确认提交结果，请先核对 NanShare 任务' : '已提交给 NanShare，生成进度请在 NanShare 查看' }; unknownNext = false; data = receipts[input.slug] }
      } else if (u.pathname.includes('/emby/media/')) data = { media: { state: 'unknown', items: [] }, baseUrl: '', serverId: '' }
      else if (u.pathname.includes('/recommendations')) data = { items: [], page: 1, totalPages: 1 }
      else if (u.pathname.includes('/follows/')) data = { subscribed: false, libraryState: 'unknown' }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) })
    })
    const base = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    await page.goto(base + '/tv/106449')
    const open = () => page.getByRole('button', { name: 'RE0 搜索', exact: true }).click()
    await open()
    const modal = page.getByRole('dialog', { name: 'RE0 搜索', exact: true })
    const rows = modal.locator('[aria-label="RE0 资源列表"] > li')
    await rows.first().waitFor()
    resources[1].is_unlocked = false
    await modal.getByRole('button', { name: '刷新搜索', exact: true }).click()
    await rows.nth(1).locator('[data-unlock-status]').filter({ hasText: '免费' }).waitFor()
    assert.ok((await rows.nth(1).locator('[data-unlock-status]').getAttribute('class')).includes('text-emerald-700'))
    assert.equal(await rows.count(), 2)
    assert.deepEqual(searches[0], { kind: 'tv', tmdbId: 106449, refresh: false })
    assert.equal(submissions.length, 0)
    assert.equal(await modal.getByRole('button', { name: '一键转存', exact: true }).count(), 2)
    await rows.first().getByRole('button', { name: '一键转存', exact: true }).click()
    await rows.first().getByText('转存成功，共 1 个', { exact: true }).waitFor()
    assert.equal(await rows.first().getByRole('button', { name: '已转存', exact: true }).isDisabled(), true)
    assert.equal(transferCalls[0].folderId, '3486868885414610176')
    assert.equal(transferCalls[0].accountId, 'remote-primary')
    assert.equal(submissions.length, 0, 'transfer must not create STRM')
    transferMultiple = true; transferState = 'points_unknown'
    await rows.nth(1).getByRole('button', { name: '一键转存', exact: true }).click()
    await modal.getByLabel('转存目录', { exact: true }).selectOption('3486868885414610177')
    await modal.getByRole('button', { name: '开始转存', exact: true }).click()
    await rows.nth(1).getByText('无法确认资源积分，已按积分限制策略跳过', { exact: true }).waitFor()
    assert.equal(transferCalls[1].folderId, '3486868885414610177')
    transferMultiple = false; transferState = 'already_saved'; transferDrop = true
    await rows.nth(1).getByRole('button', { name: '一键转存', exact: true }).click()
    await rows.nth(1).getByRole('button', { name: '获取转存结果', exact: true }).click()
    await rows.nth(1).getByText('该文件已转存过，请在网盘核查', { exact: true }).waitFor()
    assert.equal(transferCalls[2].requestId, transferCalls[3].requestId, 'lost response must reuse request ID')
    await modal.getByLabel('筛选当前结果').fill('高清')
    assert.equal(await rows.count(), 1)
    await modal.getByLabel('筛选当前结果').fill('')
    assert.equal(await rows.first().locator('[data-unlock-status]').textContent(), '10 积分')
    await rows.first().getByRole('button', { name: '分享 STRM', exact: true }).click()
    await rows.first().getByRole('button', { name: '再次生成 STRM', exact: true }).waitFor()
    await page.waitForFunction(() => [...document.querySelectorAll('[aria-label="RE0 资源列表"] button')].some(button => button.textContent.trim() === '再次生成 STRM' && !button.disabled))
    assert.equal(await modal.locator('[aria-label="分享 STRM 配置"]').count(), 0)
    assert.equal(await modal.getByLabel('NanShare 已绑定的 115 账号').count(), 0)
    assert.equal(submissions.length, 1)
    assert.match(submissions[0].requestId, /^[0-9a-f]{32}$/)
    assert.deepEqual(submissions[0], { requestId: submissions[0].requestId, searchId: 'search-one', slug: 'one', version: 'snapshot', accountId: 'remote-115', mode: 'reshare', folderIndex: 0 })
    assert.equal(await rows.first().getByRole('button', { name: '再次生成 STRM', exact: true }).isDisabled(), false)
    assert.equal(await rows.first().locator('[data-unlock-status]').textContent(), '10 积分', 'submission alone must not imply unlocked')
    await modal.getByRole('button', { name: '关闭 RE0 搜索' }).click()
    await open()
    await rows.first().getByRole('button', { name: '再次生成 STRM', exact: true }).waitFor()
    changed = true
    await rows.nth(1).getByRole('button', { name: '分享 STRM', exact: true }).click()
    await modal.getByRole('alert').filter({ hasText: '配置已变化' }).waitFor()
    assert.equal(submissions.length, 2)
    changed = false; missingAccount = true
    await rows.nth(1).getByRole('button', { name: '分享 STRM', exact: true }).click()
    await modal.getByRole('alert').filter({ hasText: '请先在 NanShare 配置' }).waitFor()
    assert.equal(submissions.length, 2)
    missingAccount = false; multiple = true
    await rows.nth(1).getByRole('button', { name: '分享 STRM', exact: true }).click()
    await modal.getByLabel('生成模式', { exact: true }).waitFor()
    assert.equal(submissions.length, 2)
    assert.equal(await modal.getByLabel('NanShare 已绑定的 115 账号').count(), 0)
    assert.equal(await modal.getByLabel('生成模式', { exact: true }).inputValue(), 'normal')
    assert.equal(await modal.getByLabel('输出目录', { exact: true }).inputValue(), '0')
    await modal.getByLabel('生成模式', { exact: true }).selectOption('reshare')
    await modal.getByLabel('输出目录', { exact: true }).selectOption('3')
    await modal.getByRole('button', { name: '提交分享 STRM', exact: true }).click()
    await rows.nth(1).getByRole('button', { name: '再次生成 STRM', exact: true }).waitFor()
    assert.equal(submissions.length, 3)
    assert.equal(submissions[2].folderIndex, 3)
    assert.equal(submissions[2].accountId, 'remote-115')
    assert.equal(submissions[2].mode, 'reshare')
    multiple = false; unlockOnRefresh = true
    await rows.first().getByRole('button', { name: '再次生成 STRM', exact: true }).click()
    await rows.first().locator('[data-unlock-status]').filter({ hasText: '已解锁' }).waitFor()
    assert.equal(submissions.length, 4)
    assert.notEqual(submissions[0].requestId, submissions[3].requestId)
    assert.ok((await rows.first().locator('[data-unlock-status]').getAttribute('class')).includes('text-blue-600'))
    refreshFails = true
    await rows.first().getByRole('button', { name: '再次生成 STRM', exact: true }).click()
    await modal.getByRole('status').filter({ hasText: '解锁状态暂未刷新' }).waitFor()
    assert.equal(await rows.first().getByRole('button', { name: '再次生成 STRM', exact: true }).isDisabled(), false)
    refreshFails = false
    unknownNext = true
    await rows.first().getByRole('button', { name: '再次生成 STRM', exact: true }).click()
    await rows.first().getByRole('button', { name: '核对后重新提交', exact: true }).waitFor()
    const beforeRetry = submissions.length
    await rows.first().getByRole('button', { name: '核对后重新提交', exact: true }).click()
    const confirmation = modal.getByRole('checkbox', { name: '我已核对 NanShare 任务，仍需重新提交' })
    await confirmation.waitFor()
    assert.equal(submissions.length, beforeRetry)
    assert.equal(await modal.getByRole('button', { name: '提交分享 STRM', exact: true }).isDisabled(), true)
    await confirmation.check()
    await modal.getByRole('button', { name: '提交分享 STRM', exact: true }).click()
    await rows.first().getByRole('button', { name: '再次生成 STRM', exact: true }).waitFor()
    assert.notEqual(submissions.at(-1).requestId, submissions.at(-2).requestId)
    dropNext = true
    await rows.first().getByRole('button', { name: '再次生成 STRM', exact: true }).click()
    await rows.first().getByRole('button', { name: '重试获取提交结果', exact: true }).waitFor()
    failRecovery = false
    await rows.first().getByRole('button', { name: '重试获取提交结果', exact: true }).click()
    await rows.first().getByRole('button', { name: '再次生成 STRM', exact: true }).waitFor()
    assert.equal(submissions.at(-1).requestId, submissions.at(-2).requestId, 'network retry must reuse request ID')
    await page.setViewportSize({ width: 390, height: 844 })
    assert.equal(await modal.evaluate(el => el.scrollWidth > el.clientWidth), false)
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    await page.keyboard.press('Escape')
    await modal.waitFor({ state: 'hidden' })
    assert.equal(await page.evaluate(() => document.body.style.overflow), '')
    empty = true
    await open()
    await modal.getByText('RE0 没有找到该影视的 115 资源。').waitFor()
    await page.keyboard.press('Escape')
    configured = false
    await open()
    await modal.getByRole('alert').filter({ hasText: '请先登录' }).waitFor()
    assert.equal(await modal.getByRole('link', { name: '登录配置' }).getAttribute('href'), '/nanshare')
    await page.keyboard.press('Escape')
    configured = true; empty = false
    await page.goto(base + '/movie/106449'); await open(); await rows.first().waitFor()
    assert.equal(searches.at(-1).kind, 'movie')
    role = 'user'; await page.reload()
    await page.locator('h1').filter({ hasText: '凡人修仙传' }).waitFor()
    assert.equal(await page.getByRole('button', { name: 'RE0 搜索', exact: true }).count(), 0)
    assert.deepEqual(errors, [])
    console.log('RE0 UI passed: one-click transfer, folder selection, points policy, duplicate receipts, same-ID transfer recovery, STRM repeat generation and recovery, unlock refresh, mobile and access control')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
