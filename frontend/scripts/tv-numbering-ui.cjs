// All API requests are intercepted. This suite never changes a real media library.
const path = require('node:path'), os = require('node:os'), fs = require('node:fs/promises'), assert = require('node:assert/strict')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const errors = [], passed = []
  try {
    await fs.mkdir('test-results', { recursive: true })
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, serviceWorkers: 'block' })
    await context.addInitScript(() => localStorage.setItem('cinema115-theme', 'light'))
    await context.addInitScript(() => localStorage.setItem('admin_token', 'mock-token'))
    const page = await context.newPage()
    page.setDefaultTimeout(12000)
    page.on('pageerror', e => errors.push(e.message))
    const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
    const episodes = Array.from({ length: 29 }, (_, i) => ({ id: i + 1, number: i + 1, sourceSeason: 1, sourceNumber: i + 1, name: `分集 ${i + 1}`, date: '2018-07-06', overview: '', runtime: 24, votes: 0, rating: 0, stillUrl: '' }))
    const group = { id: 'group12', name: 'Season', description: '电视版十二集', type: 6, groupCount: 1, episodeCount: 12, seasons: [{ id: 1, number: 1, name: 'Season 1', overview: '', episodes: episodes.slice(0, 12) }] }
    let current = { groupId: '', name: 'TMDB 默认', type: 0, revision: 0 }, snapshot = null, role = 'admin'
    let upstream = structuredClone(group), failSave = false, holdSave = false, releaseSave, writes = 0
    let holdPreview = false, releasePreview, choicesFailure = false, listingOffline = false, extendedChoices = false, loseResponse = false
    const previewReads = []
    const manySeasons = Array.from({ length: 19 }, (_, i) => ({ id: i + 1, number: i, name: '编排分组 ' + i, overview: '', episodes: episodes.slice(0, 7).map(e => ({ ...e, id: i * 100 + e.id })) }))
    const longGroup = { ...group, id: 'long-group', name: 'Order by initial air date', groupCount: 19, episodeCount: 133, seasons: manySeasons }
    const extraGroups = [longGroup, ...Array.from({ length: 15 }, (_, i) => ({ ...group, id: 'option-' + i, name: i < 2 ? '同名方案' : '候选方案 ' + i })), { ...group, id: 'zero-summary', name: '统计待核对', episodeCount: 0 }, { ...group, id: 'empty-group', name: '空分集方案', episodeCount: 0, seasons: [] }]
    const summary = { id: 1, number: 1, name: '第 1 季', episodeCount: 29, date: '2018-07-06', posterUrl: '' }
    await page.route('**/api/**', async route => {
      const request = route.request(), url = new URL(request.url()), endpoint = url.pathname
      let status = 200, data = {}
      if (endpoint === '/api/auth/me') data = { user: { id: 'test', username: 'test', role } }
      else if (endpoint.endsWith('/episode-groups')) {
        if (choicesFailure) { status = 502; data = { error: '暂时无法读取方案' } }
        else data = { current, snapshot, defaultSeasons: [summary], defaultEpisodeCount: 29, groups: listingOffline ? [] : extendedChoices ? [group, ...extraGroups] : [group], groupsError: listingOffline ? 'TMDB 剧集组列表暂时不可用，已保存编排仍可查看或恢复默认。' : '' }
      }
      else if (endpoint.endsWith('/episode-groups/group12')) {
        previewReads.push('group12')
        if (holdPreview) await new Promise(resolve => { releasePreview = resolve })
        data = { group: upstream, fingerprint: 'a'.repeat(64), changed: JSON.stringify(snapshot) !== JSON.stringify(upstream) }
      }
      else if (endpoint.includes('/episode-groups/')) {
        const id = decodeURIComponent(endpoint.split('/').pop())
        previewReads.push(id)
        const selected = extraGroups.find(item => item.id === id)
        assert.ok(selected)
        data = { group: { ...selected, episodeCount: selected.seasons.flatMap(s => s.episodes).length }, fingerprint: 'b'.repeat(64), changed: true }
      }
      else if (endpoint.endsWith('/numbering') && request.method() === 'PUT') {
        writes++
        if (loseResponse) { await route.abort('failed'); return }
        if (holdSave) await new Promise(resolve => { releaseSave = resolve })
        if (failSave) { status = 409; data = { error: '季集方案已变化，请重新读取并预览' } }
        else {
          const body = request.postDataJSON()
          assert.equal(body.revision, current.revision)
          current = { groupId: body.groupId, name: body.groupId ? 'Season' : 'TMDB 默认', type: body.groupId ? 6 : 0, revision: current.revision + 1, savedAt: 1791936000 }
          snapshot = body.groupId ? structuredClone(upstream) : null
          data = current
        }
      } else if (endpoint.endsWith('/detail/tv/80462')) data = { id: 80462, kind: 'tv', title: '千绪的上学路', originalTitle: '', overview: '十二集电视版', posterUrl: '', backdropUrl: '', date: '2018-07-06', rating: 7.1, votes: 39, tagline: '', status: 'Ended', runtime: 24, episodeCount: snapshot?.episodeCount || 29, genres: [], countries: [], cast: [], seasons: snapshot ? snapshot.seasons.map(s => ({ ...summary, number: s.number, name: s.name, episodeCount: s.episodes.length })) : [summary], collection: null, imdbId: '', imdbUrl: '', tmdbUrl: '', numbering: current }
      else if (endpoint.endsWith('/schedules')) data = { seasons: [{ number: 1, episodes: snapshot?.seasons[0].episodes || episodes, numberingRevision: current.revision }] }
      else if (endpoint.includes('/schedule/')) data = { number: 1, episodes: snapshot?.seasons[0].episodes || episodes, numberingRevision: current.revision }
      else if (endpoint.includes('/season/')) data = { id: 1, number: 1, name: 'Season 1', overview: '', episodes: snapshot?.seasons[0].episodes || episodes, numberingRevision: current.revision }
      else if (endpoint.includes('/emby/media/')) data = { media: { state: 'present', kind: 'tv', tmdbId: '80462', episodeCount: 12, items: episodes.slice(0, 12).map(e => ({ id: String(e.id), type: 'Episode', season: 1, episode: e.number, endEpisode: -1, name: e.name })) }, baseUrl: '', serverId: '' }
      else if (endpoint.includes('/recommendations/')) data = { items: [], page: 1, totalPages: 1 }
      else if (endpoint.includes('/follows/')) data = { subscribed: false, request: null, libraryState: 'present' }
      else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
      else data = { configured: false, accounts: [] }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
    })
    await page.goto(origin + '/tv/80462', { waitUntil: 'networkidle' })
    const open = () => page.getByRole('button', { name: '设置季集方案', exact: true }).click()
    await open()
    const dialog = page.getByRole('dialog'), save = dialog.locator('.numbering-primary')
    await dialog.getByRole('group', { name: /^选择方案/ }).waitFor()
    await dialog.getByText('当前方案未更改。', { exact: true }).waitFor()
    assert.equal(await save.isDisabled(), true)
    assert.equal(await dialog.getByRole('radio').count(), 2)
    assert.equal(await dialog.getByRole('radio').first().evaluate(el => el === document.activeElement), true)
    holdPreview = true
    await dialog.getByRole('radio').nth(1).check()
    await dialog.getByText('正在读取 TMDB 分集…', { exact: true }).waitFor()
    await dialog.getByRole('radio').first().check()
    releasePreview(); holdPreview = false
    await dialog.getByText('使用 TMDB 默认季集，后续随常规资料刷新更新。', { exact: true }).waitFor()
    assert.equal(await save.isDisabled(), true)
    passed.push('delayed group preview cannot replace a newer default selection; initial radio receives focus')
    await dialog.getByRole('radio').nth(1).check()
    await dialog.getByText('TMDB 原编号 S01E12', { exact: true }).waitFor()
    assert.equal(await save.isEnabled(), true)
    holdSave = true
    await save.click()
    await dialog.getByRole('button', { name: '正在保存…' }).waitFor()
    await page.keyboard.press('Escape')
    assert.equal(await dialog.isVisible(), true)
    assert.equal(await dialog.getByRole('button', { name: '关闭季集方案' }).isDisabled(), true)
    releaseSave(); holdSave = false
    await dialog.waitFor({ state: 'detached' })
    await page.waitForFunction(() => { const bar = document.querySelector('[role="progressbar"]'); return bar?.getAttribute('aria-valuenow') === '12' && bar?.getAttribute('aria-valuemax') === '12' })
    await page.getByText('共 12 集', { exact: true }).waitFor()
    assert.equal(current.groupId, 'group12')
    passed.push('explicit selection and save; busy dismissal blocked; 12/12 library progress')

    await open()
    await dialog.getByText('以下为已保存编排。', { exact: false }).waitFor()
    const textBounds = await dialog.getByText('以下为已保存编排。', { exact: false }).boundingBox()
    await page.mouse.move(textBounds.x + 8, textBounds.y + 8)
    await page.mouse.down()
    await page.mouse.move(5, 5, { steps: 6 })
    await page.mouse.up()
    assert.equal(await dialog.isVisible(), true)
    await page.mouse.click(5, 5)
    await dialog.waitFor({ state: 'detached' })
    await open()
    await dialog.getByText('以下为已保存编排。', { exact: false }).waitFor()
    passed.push('dragging selected text outside preserves the draft; direct backdrop click closes')
    upstream = structuredClone(group)
    upstream.seasons[0].episodes[0].name = '上游调整后的标题'
    await dialog.getByRole('button', { name: '查看 TMDB 更新' }).click()
    await dialog.getByText('新增 0 集，移除 0 集，调整 1 集。', { exact: false }).waitFor()
    assert.equal(snapshot.seasons[0].episodes[0].name, '分集 1')
    await dialog.getByText('查看变化', { exact: true }).click()
    await dialog.getByText(/调整：S01E01 分集 1 改为 S01E01 上游调整后的标题/).waitFor()
    await dialog.getByRole('button', { name: '更新本剧快照' }).click()
    await dialog.waitFor({ state: 'detached' })
    assert.equal(snapshot.seasons[0].episodes[0].name, '上游调整后的标题')
    passed.push('upstream changes require preview and explicit snapshot update')

    await open()
    await dialog.getByRole('radio').first().check()
    failSave = true
    await dialog.getByRole('button', { name: '恢复默认方案' }).click()
    await dialog.getByRole('alert').waitFor()
    assert.equal(current.groupId, 'group12')
    assert.equal(await dialog.getByRole('button', { name: '恢复默认方案' }).isDisabled(), true)
    failSave = false
    choicesFailure = true
    await dialog.getByRole('button', { name: '重新读取', exact: true }).click()
    await dialog.getByText('暂时无法读取方案', { exact: false }).waitFor()
    assert.equal(await save.isDisabled(), true)
    choicesFailure = false
    await dialog.getByRole('button', { name: '重新读取', exact: true }).click()
    await dialog.getByText('以下为已保存编排。', { exact: false }).waitFor()
    await page.keyboard.press('Escape')
    await dialog.waitFor({ state: 'detached' })
    assert.equal(await page.getByRole('button', { name: '设置季集方案', exact: true }).evaluate(el => el === document.activeElement), true)
    passed.push('conflict preserves saved scheme and requires reload; Escape restores focus')

    await page.setViewportSize({ width: 375, height: 812 })
    listingOffline = true
    await open()
    await dialog.getByRole('button', { name: '查看预览', exact: true }).click()
    await dialog.getByText('以下为已保存编排。', { exact: false }).waitFor()
    await dialog.getByText('TMDB 剧集组列表暂时不可用，已保存编排仍可查看或恢复默认。', { exact: false }).waitFor()
    assert.equal(await dialog.getByRole('radio', { includeHidden: true }).count(), 2)
    const dimensions = await dialog.evaluate(el => ({ width: el.getBoundingClientRect().width, height: el.getBoundingClientRect().height, overflow: el.scrollWidth > el.clientWidth, footer: el.querySelector('footer').getBoundingClientRect().bottom, windowHeight: window.innerHeight }))
    assert.ok(dimensions.width <= 375 && dimensions.height <= 812 && !dimensions.overflow && dimensions.footer <= dimensions.windowHeight)
    await page.screenshot({ path: 'test-results/tv-numbering-mobile.png' })
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.screenshot({ path: 'test-results/tv-numbering-desktop.png' })
    await dialog.getByRole('radio').first().check()
    await dialog.getByRole('button', { name: '恢复默认方案' }).click()
    await dialog.waitFor({ state: 'detached' })
    await page.getByText('已入库 12/29', { exact: true }).waitFor()
    passed.push('375px mobile layout and fixed footer; offline snapshot remains accessible and default can be restored')

    listingOffline = false; extendedChoices = true
    const previewReadCount = previewReads.length
    await open()
    await dialog.getByRole('radio').last().waitFor()
    assert.equal(previewReads.length, previewReadCount, 'listing must not prefetch candidate details')
    assert.equal(await dialog.getByText(/ID 尾号/).count(), 2)
    await dialog.locator('input[value="long-group"]').check()
    await dialog.getByText('编排分组 18', { exact: true }).waitFor({ state: 'attached' })
    const leftScroll = dialog.locator('.numbering-options'), rightScroll = dialog.locator('.numbering-preview-content')
    const independentScroll = await dialog.evaluate(el => {
      const left = el.querySelector('.numbering-options'), right = el.querySelector('.numbering-preview-content'), body = el.querySelector('.numbering-body')
      left.scrollTop = 180; right.scrollTop = 200
      return { left: left.scrollTop, right: right.scrollTop, body: body.scrollTop, bodyOverflow: body.scrollHeight > body.clientHeight + 1 }
    })
    assert.ok(independentScroll.left > 0 && independentScroll.right > 0 && !independentScroll.bodyOverflow && independentScroll.body === 0)
    await dialog.locator('input[value="option-1"]').check()
    await dialog.getByText('TMDB 原编号 S01E12', { exact: true }).waitFor()
    assert.equal(await rightScroll.evaluate(el => el.scrollTop), 0)
    assert.ok(await leftScroll.evaluate(el => el.scrollTop > 0))
    assert.equal(await dialog.locator('input[value="option-1"]').evaluate(el => el.matches(':focus-visible')), false, 'mouse selection should not add a second focus ring')
    await page.keyboard.press('ArrowDown')
    assert.equal(await dialog.locator('input[value="option-2"]').isChecked(), true)
    assert.equal(await dialog.locator('input[value="option-2"]').evaluate(el => el.matches(':focus-visible')), true)
    await page.screenshot({ path: 'test-results/tv-numbering-desktop.png' })
    passed.push('long desktop columns scroll independently; selection resets only preview; radio mouse and keyboard focus differ; no eager preview fetches')

    await dialog.locator('input[value="zero-summary"]').check()
    await dialog.getByText('TMDB 原编号 S01E12', { exact: true }).waitFor()
    assert.equal(await save.isEnabled(), true, 'zero summary may have valid detailed episodes')
    await dialog.locator('input[value="empty-group"]').check()
    await dialog.locator('.numbering-preview-content').getByText('该方案详情无有效分集数据，无法应用。', { exact: true }).waitFor()
    assert.equal(await save.isDisabled(), true)
    passed.push('duplicate summaries receive ID disambiguation; zero summary is previewable; confirmed empty detail cannot be applied')

    await dialog.locator('input[value="long-group"]').check()
    await dialog.getByText('编排分组 18', { exact: true }).waitFor({ state: 'attached' })
    await page.setViewportSize({ width: 375, height: 812 })
    const choiceTab = dialog.getByRole('tab', { name: '选择方案', exact: true }), previewTab = dialog.getByRole('tab', { name: '分集预览', exact: true })
    await choiceTab.waitFor()
    assert.equal(await dialog.locator('.numbering-preview').isVisible(), false)
    await leftScroll.evaluate(el => { el.scrollTop = 240 })
    const choicePosition = await leftScroll.evaluate(el => el.scrollTop)
    await dialog.getByRole('button', { name: '查看预览', exact: true }).click()
    assert.equal(await previewTab.evaluate(el => el === document.activeElement), true)
    await rightScroll.evaluate(el => { el.scrollTop = 160 })
    const previewPosition = await rightScroll.evaluate(el => el.scrollTop)
    await choiceTab.click()
    assert.equal(await leftScroll.evaluate(el => el.scrollTop), choicePosition)
    await dialog.locator('input[value="long-group"]').click()
    assert.equal(await previewTab.getAttribute('aria-selected'), 'false', 'radio must not automatically switch tabs')
    await choiceTab.focus()
    await page.keyboard.press('ArrowRight')
    assert.equal(await previewTab.getAttribute('aria-selected'), 'true')
    assert.equal(await previewTab.evaluate(el => el === document.activeElement), true)
    assert.equal(await rightScroll.evaluate(el => el.scrollTop), previewPosition)
    const mobileBounds = await dialog.evaluate(el => { const r = el.getBoundingClientRect(), content = el.querySelector('.numbering-preview-content').getBoundingClientRect(), footer = el.querySelector('footer').getBoundingClientRect(); return { width: r.width, height: r.height, contentTop: content.top, contentBottom: content.bottom, footerTop: footer.top, footerBottom: footer.bottom, overflow: el.scrollWidth > el.clientWidth } })
    assert.ok(mobileBounds.width <= 375 && mobileBounds.height <= 780 && !mobileBounds.overflow && mobileBounds.contentTop < 500 && mobileBounds.contentBottom <= mobileBounds.footerTop + 1 && mobileBounds.footerBottom <= 812)
    await page.screenshot({ path: 'test-results/tv-numbering-mobile.png' })
    await page.emulateMedia({ colorScheme: 'dark' })
    await page.evaluate(() => document.documentElement.classList.add('dark'))
    assert.ok((await dialog.evaluate(el => getComputedStyle(el).backgroundColor)).includes('18, 22, 30, 0.84'))
    await page.waitForFunction(() => getComputedStyle(document.querySelector('.numbering-primary')).color === 'rgb(173, 199, 236)')
    await page.screenshot({ path: 'test-results/tv-numbering-mobile-dark.png' })
    await page.setViewportSize({ width: 1440, height: 1000 })
    await dialog.getByRole('radio').last().waitFor()
    assert.equal(await rightScroll.evaluate(el => el === document.activeElement), true, 'removing mobile tabs must transfer focus into the desktop preview')
    await page.screenshot({ path: 'test-results/tv-numbering-desktop-dark.png' })
    await page.emulateMedia({ contrast: 'more' })
    assert.equal(await dialog.evaluate(el => getComputedStyle(el).backdropFilter), 'none')
    await page.emulateMedia({ colorScheme: 'light', contrast: 'no-preference' })
    await page.evaluate(() => document.documentElement.classList.remove('dark'))
    passed.push('mobile tabs preserve both scroll positions and move focus explicitly; preview remains above footer; dark and high-contrast surfaces')

    await dialog.locator('input[value="group12"]').check()
    await dialog.getByText('TMDB 原编号 S01E12', { exact: true }).waitFor()
    loseResponse = true
    await save.click()
    await dialog.getByRole('alert').filter({ hasText: '状态待核实' }).waitFor()
    assert.equal(await save.isDisabled(), true)
    await save.evaluate(el => el.click())
    assert.equal(writes, 5, 'lost responses must block a second PUT')
    loseResponse = false
    await dialog.getByRole('button', { name: '重新读取', exact: true }).click()
    await dialog.getByText('当前方案未更改。', { exact: true }).waitFor()
    await page.keyboard.press('Escape')
    await dialog.waitFor({ state: 'detached' })
    passed.push('unconfirmed save response exposes pending verification and blocks repeat writes until reload')

    role = 'user'
    await page.reload({ waitUntil: 'networkidle' })
    assert.equal(await page.getByRole('button', { name: '设置季集方案', exact: true }).count(), 0)
    assert.deepEqual(errors, [])
    assert.equal(writes, 5)
    passed.push('ordinary users cannot see administrator controls; no browser errors')
    console.log(JSON.stringify({ passed, screenshots: ['test-results/tv-numbering-mobile.png', 'test-results/tv-numbering-desktop.png', 'test-results/tv-numbering-mobile-dark.png', 'test-results/tv-numbering-desktop-dark.png'] }, null, 2))
    await context.close()
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
