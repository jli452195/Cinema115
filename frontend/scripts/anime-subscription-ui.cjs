// All APIs and posters are intercepted. No real subscriptions, directories or downloads are written.
const assert = require('node:assert/strict')
const fs = require('node:fs/promises'), path = require('node:path'), os = require('node:os')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || path.join(os.homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'))
const origin = process.env.CINEMA_URL || 'http://127.0.0.1:5173'
const wait = async (fn, label) => { for (let i = 0; i < 100; i++) { if (await fn()) return; await new Promise(r => setTimeout(r, 50)) } throw new Error(label || 'condition timed out') }
;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  const contexts = [], errors = [], passed = []
  await fs.mkdir('test-results', { recursive: true })
  async function session(viewport = { width: 1440, height: 1080 }, role = 'admin') {
    const context = await browser.newContext({ viewport, serviceWorkers: 'block' }); contexts.push(context)
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light'); Object.defineProperty(crypto, 'randomUUID', { value: undefined }) })
    const page = await context.newPage(); page.setDefaultTimeout(10000)
    page.on('pageerror', e => errors.push(e.message))
    page.on('dialog', dialog => dialog.accept())
    const calls = [], state = { role, primary: true, ready: true, empty: false, zeroMatch: false, failPreview: false, failSearch: false, expired: false, saveMode: '', settingsMode: '', loseSelection: false, conflict: false, batch: 0, batches: new Map(), saved: undefined,
      settings: { revision: 1, accountId: 'primary', accountRevision: 2, uid: '10001', downloadCid: '100', downloadPath: '/动漫下载', organizeCid: null, organizePath: null } }
    const card = id => ({ id, title: '从零开始的异世界生活 第三季', originalTitle: 'Re:ZERO', coverUrl: '', date: '2026-10-01', weekday: 1 })
    function item(ref, index) {
      const review = index === 2, filtered = state.zeroMatch || index === 3, ep = 25 + Math.floor(index / 2)
      return { release: { id: `${ref.replace(/^source-/, '')}-release-${index}`, title: `[示例字幕组] 从零开始的异世界生活 [${ep}] [1080p] ${index % 2 ? '简繁内封 v2' : '简体内嵌'}`, parse: { episode: ep, rawEpisode: String(ep), resolution: '1080p', versionLabel: index % 2 ? 'v2' : '' } },
        observation: { title: '', sizeRaw: '650 MB' }, projection: { status: 'in_scope', sourceEpisode: ep },
        selectable: !review && !filtered, decision: review ? 'needs_confirmation' : filtered ? 'filtered' : 'matched', reasonCode: review ? 'EPISODE_NUMBER_UNRESOLVED' : filtered ? 'BELOW_SOURCE_MINIMUM' : 'MATCHED_METADATA_ONLY' }
    }
    function makeSource(config, take, previous) {
      const items = state.empty ? [] : Array.from({ length: Math.min(take, 120) }, (_, i) => item(config.ref, i))
      const pending = previous && config.rules.minimumEpisode !== previous.config.rules.minimumEpisode ? [{ releaseId: previous.selected[0] || `${config.ref}-old`, reason: 'changed', previousItem: previous.items[0] }] : []
      const selected = previous ? previous.selected.filter(id => items.some(item => item.release.id === id && item.selectable) && !pending.some(item => item.releaseId === id)) : config.rules.initialMode === 'from_episode' ? items.filter(item => item.selectable).map(item => item.release.id) : []
      return { ref: config.ref, config, id: `preview-${config.ref}-${state.batch}`, rev: 1, take, items, selected, pending, expires: Date.now() + (state.expired ? -1000 : 900000) }
    }
    function summary(source) { return { sourceRef: source.ref, sourcePreviewId: source.id, loadRevision: source.rev, generatedAt: Date.now(), expiresAt: source.expires, expired: source.expires < Date.now(), rawCount: source.items.length, loadedCount: source.items.length, matchedCount: source.items.filter(i => i.selectable).length, reviewCount: source.items.filter(i => i.decision === 'needs_confirmation').length, selectedCount: source.selected.length, pendingSelectionCount: source.pending.length, completeness: source.take > 100 || state.empty ? 'visible_end' : 'partial', stopReason: source.take > 100 ? '' : 'more_available', loadedFirst: source.items.length ? 25 : null, loadedLast: source.items.length ? 25 + Math.floor((source.items.length - 1) / 2) : null, gapsWithinLoadedMatches: [{ first: 26, last: 26 }], unloadedHistory: source.take <= 100 && !state.empty } }
    function previewPage(source, number = 1) { return { items: source.items.slice((number - 1) * 50, number * 50), page: number, pageSize: 50, total: source.items.length, selectedIds: source.selected, pendingSelections: source.pending, summary: summary(source) } }
    function response(batch, target, issues = []) {
      const sources = [...batch.sources.values()], totalRawCount = sources.reduce((n, s) => n + s.items.length, 0)
      return { batchId: batch.id, budget: { totalRawCount, limit: 2000, remaining: 2000 - totalRawCount }, sources: sources.map(summary), issues, ...(batch.sources.has(target) ? { page: previewPage(batch.sources.get(target)) } : {}) }
    }
    await page.route('**/api/**', async route => {
      const request = route.request(), url = new URL(request.url()), endpoint = url.pathname, method = request.method(), body = request.postDataJSON()
      const call = { endpoint, query: url.search, method, body, key: request.headers()['idempotency-key'] }; calls.push(call)
      let status = 200, data = {}
      if (endpoint === '/api/auth/me') data = { user: { id: 'mock', username: 'admin', role: state.role, disabled: false } }
      else if (endpoint.includes('/notifications')) data = { items: [], unread: 0 }
      else if (endpoint.endsWith('/naming/search')) {
        if (state.namingGate) await state.namingGate
        if (state.failNaming) { status = 503; data = { error: 'TMDB 暂不可用' } }
        else data = { items: state.naming ? [{ id: 117061, title: '从零开始的异世界生活', date: '2016-04-04' }] : [], recommendedId: state.naming ? '117061' : '' }
      } else if (endpoint.endsWith('/naming/target')) {
        if (state.targetGate) await state.targetGate
        const season = body.season ?? 2
        data = { tmdbId: body.tmdbId, title: '从零开始的异世界生活', year: '2016', groupId: body.groupId || '', groupName: body.groupId ? '独立编排' : 'TMDB 默认', season, fingerprint: 'a'.repeat(64),
          seasons: [1, 2].map(n => ({ number: n, name: `第 ${n} 季`, date: n === 1 ? '2016-04-04' : '2026-10-01', episodeCount: 13, episodes: [season, ...(body.extraSeasons || [])].includes(n) ? Array.from({length:13}, (_,i) => ({id:String(n * 100+i+1),number:i+(body.groupId ? 9 : 1),name:`目标第 ${i+1} 集`,date:`${n===2?'2026-10':'2016-04'}-${String(i+1).padStart(2,'0')}`,sourceSeason:n,sourceNumber:i+1})) : null })) }
        state.namingTarget = structuredClone(data)
      } else if (endpoint.includes('/naming/groups/')) data = { items: [{id:'group001',name:'独立编排'}] }
      else if (endpoint.endsWith('/catalog/mikan')) data = { provider: 'mikan', days: [{ day: 1, items: [{ id:'300',title:'从零开始 第三季 字幕资源',coverUrl:'',updatedDate:'' }] }], items: [], fetchedAt:Date.now(), expiresAt:Date.now()+600000 }
      else if (/\/discovery\/subjects\/[^/]+\/episodes$/.test(endpoint)) {
        if (state.episodeGate) await state.episodeGate
        if (state.failEpisodes) { status = 502; data = {error:'模拟集数读取失败'} }
        else {
          const offset = Number(url.searchParams.get('offset') || 0), total = state.chapterCount ?? 8
          data = { total, offset, limit:100, maxItems:2000, stale:false, items:Array.from({length:Math.min(100,Math.max(total-offset,0))},(_,i)=>({id:String(1388677+offset+i),number:59+offset+i,chapterNumber:offset+i+1,title:`篇章集 ${offset+i+1}`,originalTitle:'',date:`2026-10-${String(offset+i+1).padStart(2,'0')}`})) }
        }
      } else if (endpoint.includes('/discovery/subjects/')) {
        const id = endpoint.split('/').pop()
        data = { subject: { ...card(id), title: id === '456' ? '独立续篇' : card(id).title, synopsis: '独立的篇章资料。', totalEpisodes: 12, platform: 'TV' }, metadata: { expiresAt: Date.now() + 600000, stale: false } }
        if (url.searchParams.has('includeRelations')) data.relations = { items: [{ ...card('456'), title: '独立续篇', relation: '续集' }], expiresAt: Date.now() + 600000 }
      } else if (endpoint.endsWith('/settings') && method === 'GET') data = { settings: state.settings, primaryAccount: state.primary ? { id: 'primary', name: '115 主网盘', uid: '10001', revision: 2 } : null, readiness: { ready: state.ready && state.primary, issues: state.primary ? state.ready ? [] : ['DOWNLOAD_DIRECTORY_REQUIRED'] : ['PRIMARY_ACCOUNT_REQUIRED'] } }
      else if (endpoint.endsWith('/settings') && method === 'PATCH') {
        state.settings = { ...state.settings, revision: 2, downloadCid: body.downloadCid, downloadPath: '/新动漫目录' }; state.ready = true
        if (state.settingsMode === 'unknown') { state.settingsMode = ''; await route.abort(); return }
        data = state.settings
      } else if (endpoint.includes('/cloud-download/paths')) data = { directories: [{ id: '200', name: '新动漫目录' }], hasMore: false }
      else if (endpoint.endsWith('/sources/mikan/search')) {
        if (state.failSearch) { status = 429; data = { code: 'SOURCE_COOLDOWN', error: '模拟来源冷却', details: { retryAfterAt: Date.now() + 20000 } } }
        else data = { items: body.keyword === 'none' ? [] : [{ id: '300', title: '从零开始 第三季 字幕资源' }], truncated: false }
      } else if (endpoint.endsWith('/sources/mikan/300')) data = { entry: { id: '300', title: '从零开始 第三季 字幕资源', groups: [{ id: '10', name: '字幕组甲' }, { id: '20', name: '字幕组乙' }], bangumiIds: ['123'] } }
      else if (endpoint.endsWith('/source-bindings/preview')) {
        if (state.sampleGate?.group === body.source.groupId) await state.sampleGate.ready
        if (state.failSample) { await route.fulfill({status: 502, contentType: 'application/json', body: JSON.stringify({code:'UPSTREAM_UNAVAILABLE',error:'模拟样本读取失败'})}); return }
        assert.equal(body.binding.confirmed, false, 'samples must not assert confirmation')
        assert.equal(body.binding.scope, null, 'samples precede range configuration')
        const specs = [
          { resolution: '1080p', languages: ['zh-Hans'], subtitleMode: 'hard' },
          { resolution: '1080p', languages: ['zh-Hant'], subtitleMode: 'hard' },
          { resolution: '1080p', languages: ['zh-Hans', 'zh-Hant', 'ja'], subtitleMode: 'soft', codec: 'h265', bitDepth: '10', container: 'mkv' },
        ]
        if (state.distinctGroups && body.source.groupId === '20') specs.splice(0, specs.length, { resolution: '720p' })
        const labels = state.distinctGroups && body.source.groupId === '20' ? ['720p'] : ['1080p 简 内嵌', '1080p 繁 内嵌', '1080p 简繁日 内封 HEVC 10bit MKV']
        data = { source: { ...body.source, expiresAt: Date.now() + 600000 }, observedAt: Date.now(), specVersion: 1, profilesTruncated: false,
          profiles: specs.map((spec, i) => ({ key: `spec-${i}`, label: labels[i], spec, count: 1 })),
          releases: state.empty ? [] : specs.map((spec, i) => ({ postKey: `sample-${i}`, title: `[${body.source.groupId === '10' ? '字幕组甲' : '字幕组乙'}] 从零开始的异世界生活 [${state.sampleEpisode || 25}] [${labels[i]}]`, parse: { episode: state.sampleEpisode || 25, spec }, sizeRaw: '650 MB' })) }
      }
      else if (endpoint.endsWith('/subscriptions/preview')) {
        const ref = body.previewSourceRef
        if (state.failPreview) { status = 502; data = { code: 'UPSTREAM_UNAVAILABLE', error: '模拟 RSS 读取失败，原输入保留' } }
        else {
          state.batch++
          const batch = { id: `batch-${state.batch}`, sources: new Map() }
          for (const reuse of body.previewRefs) {
            const old = state.batches.get(reuse.batchId)?.sources.get(reuse.sourceRef)
            assert.ok(old, 'reuse must refer to an actual snapshot')
            assert.equal(reuse.loadRevision, old.rev)
            batch.sources.set(old.ref, { ...structuredClone(old), id: `preview-${old.ref}-${state.batch}`, rev: 1 })
          }
          const previous = body.previous ? state.batches.get(body.previous.batchId)?.sources.get(ref) : undefined
          if (body.previous) assert.equal(body.previous.loadRevision, previous.rev, 'previous CAS must be recovered before regeneration')
          batch.sources.set(ref, makeSource(body.config.sources.find(s => s.ref === ref), body.take, previous))
          state.batches.set(batch.id, batch); data = response(batch, ref)
        }
      } else if (/\/previews\/[^/]+\/(releases|selections|load)$/.test(endpoint)) {
        const parts = endpoint.split('/'), batch = state.batches.get(parts.at(-2)), action = parts.at(-1), ref = body?.sourceRef || url.searchParams.get('sourceRef'), source = batch?.sources.get(ref)
        assert.ok(source, 'snapshot must exist')
        if (action === 'releases') data = previewPage(source, Number(url.searchParams.get('page') || 1))
        else if (source.rev !== body.expectedLoadRevision) { status = 409; data = { error: '预览版本已变化', code: 'REVISION_CONFLICT' } }
        else if (action === 'selections') {
          source.selected = [...body.selectedReleaseIds]; source.pending = source.pending.filter(item => !body.dismissedReleaseIds.includes(item.releaseId)); source.rev++
          if (state.loseSelection) { state.loseSelection = false; await route.abort(); return }
          data = response(batch, ref)
        } else { const next = makeSource(source.config, body.nextTake, source); next.rev = source.rev + 1; next.id = source.id; batch.sources.set(ref, next); data = response(batch, ref) }
      } else if (endpoint === '/api/admin/anime/subscriptions' && method === 'POST' || /\/subscriptions\/[^/]+$/.test(endpoint) && method === 'PATCH') {
        if (state.saveMode === 'conflict') { status = 409; data = { error: '配置版本已变化，输入保留', code: 'REVISION_CONFLICT' } }
        else if (state.saveMode === 'duplicate') { status = 409; data = { error: '该篇章已有订阅配置', code: 'SUBSCRIPTION_EXISTS', details: { existingId: 'saved-subscription' } } }
        else {
          const ids = Object.fromEntries(body.config.sources.map(s => [s.ref, s.id || `source-${s.ref}`]))
          state.saved = { id: 'saved-subscription', entryId: 'entry-123', status: body.saveMode, revision: body.expectedRevision + 1, entry: { id: 'entry-123', bangumiId: '123', editionKey: 'original', title: card('123').title, originalTitle: '', revision: 1, metadata: { coverUrl: '', airDate: '2026-10-01' } },
            sources: body.config.sources.map(s => ({ ...s, id: ids[s.ref], revision: s.expectedRevision + 1, configuredState: s.status })), form: { formSelections: body.config.formSelections.map(s => ({ ...s, sourceRef: ids[s.sourceRef] })), previewRefs: body.config.previewRefs },
            naming: body.config.naming ? {schemaVersion:1,target:structuredClone(state.namingTarget),sources:body.config.naming.sources.map(s=>({...s,sourceRef:ids[s.sourceRef]}))} : null }
          if (state.saveMode === 'unknown') { state.saveMode = ''; await route.abort(); return }
          if (state.saveMode === 'invalid') { state.saveMode = ''; data = {}; }
          else data = { id: state.saved.id, entryId: 'entry-123', revision: state.saved.revision, status: body.saveMode, sourceIds: ids, sourceRevisions: Object.fromEntries(body.config.sources.map(s => [s.ref, s.expectedRevision + 1])), requiresRepreview: true }
        }
      } else if (/\/subscriptions\/[^/]+$/.test(endpoint) && method === 'GET') {
        if (!state.saved) { status = 404; data = { code: 'NOT_FOUND', error: '配置不存在' } } else data = state.saved
      } else if (endpoint.includes('/tmdb/')) data = { items: [], results: [], total: 0 }
      else if (endpoint.includes('/emby/')) data = { configured: false, items: [] }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
    })
    async function go(suffix = '/anime/new?bangumiId=123') { await page.goto(origin + suffix, { waitUntil: 'networkidle' }); if (role === 'admin') await page.locator(suffix.startsWith('/anime?') ? '.mikan-anime-card' : '.ae-chapter').first().waitFor() }
    async function source(group = '10', first = '25', last = '64') {
      await page.getByRole('button', { name: '选择资源', exact: true }).click()
      await page.getByRole('button', { name: /从零开始 第三季 字幕资源/ }).click()
      await page.getByRole('button', { name: group === '10' ? /字幕组甲/ : /字幕组乙/ }).click()
      await page.getByRole('checkbox', { name: /^1080p 简 内嵌/ }).check()
      assert.equal(await page.getByLabel('来源范围', { exact: true }).count(), 0)
      assert.equal(await page.getByLabel('发布编号与篇章集号').count(), 0)
      await page.getByRole('button', { name: '使用此来源', exact: true }).click()
      await page.getByRole('dialog').waitFor({ state: 'detached' })
      assert.equal(await page.getByLabel('从第几话开始').inputValue(), '1', 'recent samples never change default minimum')
      await page.getByLabel('从第几话开始').fill(first)
      await page.locator('.ae-source-options summary').click()
      await page.getByRole('checkbox', {name:'限定发布编号范围'}).check()
      await page.getByLabel('范围起点', { exact: true }).fill(first); await page.getByLabel('范围终点', { exact: true }).fill(last)
    }
    async function preview() { await page.getByRole('button', { name: /^(生成预览|重新预览)$/ }).click(); await page.locator('.ae-preview-stats').waitFor(); await wait(async () => await page.locator('.ae-preview [aria-busy=true]').count() === 0 && !await page.getByRole('button', { name: '处理中…' }).count()) }
    const writes = () => calls.filter(c => c.key && c.endpoint.includes('/anime/'))
    return { page, context, state, calls, go, source, preview, writes }
  }
  try {
    const first = await session(), { page, state, calls, go, source, preview } = first
    await go()
    assert.equal(calls.some(c => c.endpoint.includes('/sources/mikan')), false, 'no automatic search')
    assert.equal(await page.getByRole('button', { name: '保存为暂停' }).isDisabled(), true)
    await page.getByRole('button', { name: '保存草稿', exact: true }).click()
    await page.getByRole('heading', { name: '草稿已保存' }).waitFor()
    assert.equal(first.writes()[0].body.saveMode, 'draft')
    assert.equal(first.writes()[0].body.config.sources[0].binding.scope, null)
    await page.getByRole('button', { name: '继续编辑', exact: true }).click()
    await page.getByRole('heading', { name: '编辑动漫订阅', exact: true }).waitFor()
    assert.equal(calls.filter(c => c.endpoint.includes('/discovery/subjects') && !c.endpoint.endsWith('/episodes')).length, 1, 'edit loads local metadata')
    passed.push('未选资源可保存草稿，保存回执后本地继续编辑，无自动来源请求')

    await go(); await source()
    await preview()
    assert.equal(await page.locator('.ae-release').count(), 50)
    assert.equal(await page.locator('.ae-release input:checked').count(), 48)
    assert.equal(await page.locator('.ae-release input:disabled').count(), 2)
    assert.match(await page.locator('.ae-release').first().innerText(), /第 25 话/)
    const request = calls.findLast(c => c.endpoint.endsWith('/subscriptions/preview'))
    assert.equal(request.body.config.sources[0].binding.chapterCorrespondence, null)
    assert.equal(request.body.config.sources[0].rules.minimumEpisode, 25)
    passed.push('选择来源不强制范围或篇章编号，下载范围独立，同集两个版本均可选')

    assert.equal(await page.getByRole('button', { name: '资源下一页' }).isDisabled(), true, 'first 50 rows fit a single page')
    await page.getByRole('button', { name: '继续加载历史', exact: true }).click()
    await wait(async () => (await page.locator('.ae-page-buttons').innerText()).includes('1 / 2'))
    await page.getByRole('button', { name: '资源下一页' }).click()
    await wait(async () => (await page.locator('.ae-page-buttons').innerText()).includes('2 / 2'))
    assert.equal(await page.locator('.ae-release input:checked').count(), 0, 'new history is not silently selected')
    await page.locator('.ae-release input').first().check()
    await wait(() => calls.some(c => c.endpoint.endsWith('/selections')))
    await wait(async () => (await page.locator('.ae-page-buttons').innerText()).includes('2 / 2') && !await page.getByRole('button', { name: '处理中…' }).count())
    const selection = calls.findLast(c => c.endpoint.endsWith('/selections')).body
    assert.equal(selection.selectedReleaseIds.length, 49, 'retain full selected IDs across pages')
    assert.ok(selection.selectedReleaseIds.includes('main-release-0'))
    assert.equal(await page.locator('.ae-release input').first().isChecked(), true)
    await page.locator('.ae-release input').first().uncheck()
    await wait(() => calls.filter(c => c.endpoint.endsWith('/selections')).length === 2)
    await wait(async () => !await page.getByRole('button', { name: '处理中…' }).count())
    await page.getByRole('button', { name: '继续加载历史', exact: true }).click()
    await wait(() => calls.some(c => c.endpoint.endsWith('/load')))
    await wait(async () => (await page.locator('.ae-preview-boundary').innerText()).includes('可见历史末尾'))
    assert.equal(calls.findLast(c => c.endpoint.endsWith('/load')).body.nextTake, 150)
    await page.getByRole('button', { name: '添加备用', exact: true }).click()
    assert.equal(await page.getByRole('button', { name: '选择资源', exact: true }).count(), 1, 'standby starts without rules')
    await source('20', '1', '80'); await preview()
    const standbyGenerate = calls.findLast(c => c.endpoint.endsWith('/subscriptions/preview')).body
    assert.equal(standbyGenerate.previewRefs.length, 1)
    const mainReuse = standbyGenerate.previewRefs[0]
    assert.equal(mainReuse.sourceRef, 'main')
    await page.locator('.ae-source-tabs button').first().click()
    assert.equal(await page.getByLabel('从第几话开始').inputValue(), '25')
    await page.getByRole('button', { name: '保存草稿', exact: true }).click()
    await page.getByRole('heading', { name: '草稿已保存' }).waitFor()
    const multiSave = first.writes().at(-1).body
    assert.equal(multiSave.config.previewRefs.length, 2)
    assert.equal(new Set(multiSave.config.previewRefs.map(r => r.batchId)).size, 1, 'update refs for all cloned summaries')
    assert.ok(multiSave.config.formSelections[0].dismissedReleaseIds.includes('main-release-50'))
    passed.push('跨页选择保留全量 ID 与排除记录，继续加载受控，备用独立配置且更新所有克隆引用')

    await go(); await source(); await preview()
    await page.getByLabel('从第几话开始').fill('26')
    assert.equal(await page.locator('.ae-release input').first().isDisabled(), true)
    await page.getByText('规则或目录已变化。', { exact: false }).waitFor()
    await preview()
    await page.getByText('1 条旧选择待核对', { exact: true }).click()
    await page.setViewportSize({ width: 320, height: 800 })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    assert.equal(await page.locator('.ae-pending > div').evaluate(el => { const text = el.querySelector('span').getBoundingClientRect(), button = el.querySelector('button').getBoundingClientRect(); return button.top >= text.bottom }), true, 'pending row action stacks below long title on mobile')
    await page.locator('.ae-pending').screenshot({ path: 'test-results/anime-editor-mobile-pending.png', animations: 'disabled' })
    await page.setViewportSize({ width: 1440, height: 1080 })
    await page.getByRole('button', { name: '放弃旧选择', exact: true }).click()
    await wait(async () => await page.locator('.ae-pending').count() === 0)
    state.loseSelection = true
    await page.locator('.ae-release input:checked').first().uncheck()
    await page.getByText('读取失败或超时，输入已保留', { exact: true }).waitFor()
    await preview()
    assert.equal(await page.locator('.ae-release input').nth(1).isChecked(), false, 'recover selection CAS after lost response')
    passed.push('规则改动锁定旧预览，旧选择待核对，选择回执丢失后先恢复最新 CAS 再预览')

    state.zeroMatch = true; await page.getByRole('button', { name: '重新预览', exact: true }).click()
    await page.getByText('当前已加载内容没有符合规则的资源。', { exact: false }).waitFor()
    state.failPreview = true; await page.getByRole('button', { name: '重新预览', exact: true }).click()
    await page.getByText('模拟 RSS 读取失败，原输入保留', { exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: '保存为暂停' }).isEnabled(), true, 'RSS failure does not block complete paused config')
    await page.getByRole('button', { name: '保存为暂停' }).click()
    await page.getByRole('heading', { name: '已保存为暂停配置' }).waitFor()
    passed.push('零匹配与读取失败保留原规则，完整配置可保存暂停，不依赖 RSS 成功')

    const directory = await session(); directory.state.ready = false; directory.state.settings.downloadCid = null; directory.state.settings.downloadPath = null
    await directory.go(); await directory.page.getByRole('button', { name: '选择目录', exact: true }).click()
    await directory.page.getByRole('button', { name: '新动漫目录', exact: true }).click()
    directory.state.settingsMode = 'unknown'
    await directory.page.getByRole('button', { name: '保存此目录', exact: true }).click()
    await directory.page.getByRole('dialog').waitFor({ state: 'detached' })
    await directory.page.getByRole('button', { name: '重试原目录保存请求', exact: true }).waitFor()
    const settingWrite = structuredClone(directory.writes()[0])
    await directory.page.getByRole('button', { name: '重试原目录保存请求', exact: true }).click()
    await directory.page.getByRole('dialog').waitFor({ state: 'detached' })
    assert.deepEqual(directory.writes()[1], settingWrite, 'directory retries same key and exact body')
    assert.equal(settingWrite.body.expectedPrimaryAccountId, 'primary')
    assert.ok(directory.calls.filter(c => c.endpoint.endsWith('/paths')).every(c => c.query.includes('accountId=primary')))
    assert.equal(directory.calls.some(c => c.endpoint.endsWith('/accounts')), false)
    const noMain = await session(); noMain.state.primary = false; await noMain.go()
    assert.equal(await noMain.page.getByRole('button', { name: '选择目录' }).count(), 0)
    await noMain.page.getByRole('button', { name: '保存草稿', exact: true }).click(); await noMain.page.getByRole('heading', { name: '草稿已保存' }).waitFor()
    passed.push('目录只用固定主账号，未知结果原键原正文重试；未配置主账号仍可存草稿，无小号回退')

    const unknown = await session(); unknown.state.naming=true; await unknown.go(); await unknown.source()
    await unknown.page.getByLabel('发布第几话',{exact:true}).fill('25')
    unknown.state.saveMode = 'unknown'
    await unknown.page.getByRole('button', { name: '保存为暂停' }).click()
    await unknown.page.getByRole('button', { name: '重试原保存请求', exact: true }).waitFor()
    assert.equal(await unknown.page.getByLabel('从第几话开始').isDisabled(), true)
    const savedWrite = structuredClone(unknown.writes()[0])
    assert.equal(savedWrite.body.config.naming.sources[0].segments[0].sourceBase,25,'unknown save freezes naming alongside all other inputs')
    await unknown.page.getByRole('button', { name: '重试原保存请求', exact: true }).click()
    await unknown.page.getByRole('heading', { name: '已保存为暂停配置' }).waitFor()
    assert.deepEqual(unknown.writes()[1], savedWrite)
    await unknown.page.getByRole('button', { name: '继续编辑', exact: true }).click()
    await unknown.page.getByRole('heading', { name: '编辑动漫订阅', exact: true }).waitFor()
    assert.equal(await unknown.page.getByLabel('字幕组', { exact: true }).count(), 0, 'saved group immutable')
    unknown.state.saveMode = 'conflict'; await unknown.page.getByLabel('从第几话开始').fill('26'); await unknown.page.getByRole('button', { name: '保存为暂停' }).click()
    await unknown.page.getByText('配置版本已变化，输入保留', { exact: true }).waitFor()
    assert.equal(await unknown.page.getByLabel('从第几话开始').inputValue(), '26')
    const revision = unknown.writes().at(-1).body.expectedRevision
    await unknown.page.getByRole('button', { name: '保存草稿', exact: true }).click()
    await wait(() => unknown.writes().length === 4)
    assert.equal(unknown.writes().at(-1).body.expectedRevision, revision)
    passed.push('创建回执丢失锁定表单，原键原文重试；编辑固定来源身份，CAS 冲突保留输入且不静默升版本')

    const validation = await session(); await validation.go(); await validation.source()
    await validation.page.getByLabel('范围终点', { exact: true }).fill('')
    await validation.page.getByRole('button', { name: '修改来源规则', exact: true }).click()
    await validation.page.getByRole('checkbox', { name: /^1080p 繁 内嵌/ }).check()
    await validation.page.getByRole('button', { name: '更新来源规则', exact: true }).click()
    assert.equal(await validation.page.getByLabel('范围终点', { exact: true }).inputValue(), '')
    await validation.page.getByRole('button', { name: '保存草稿', exact: true }).click()
    await validation.page.getByText('请完整填写编号范围，起点不能大于终点', { exact: true }).waitFor()
    assert.equal(validation.writes().length, 0)
    await validation.page.getByRole('button', { name: '修改来源规则', exact: true }).click()
    await validation.page.getByRole('button', { name: '返回字幕组', exact: true }).click()
    await validation.page.getByRole('button', { name: /字幕组乙/ }).click()
    assert.equal(await validation.page.getByLabel('来源范围', { exact: true }).count(), 0)
    await validation.page.getByRole('checkbox', { name: /^1080p/ }).first().waitFor()
    assert.equal(await validation.page.getByRole('checkbox', { name: /^1080p/ }).count(), 3)
    assert.equal(await validation.page.locator('.ae-spec-option input:checked').count(), 0, 'group change resets specifications')
    await validation.page.getByRole('checkbox', { name: /^1080p 简 内嵌/ }).check()
    await validation.page.getByRole('button', { name: '使用此来源', exact: true }).click()
    assert.equal(await validation.page.getByLabel('从第几话开始').inputValue(), '1', 'never inherit another group minimum or offset')
    validation.state.expired = true
    await validation.preview()
    assert.equal(await validation.page.locator('.ae-release input').first().isDisabled(), true)
    passed.push('不丢弃不完整的范围输入，过期逐来源预览禁止选择')

    const empty = await session(); await empty.go()
    await empty.page.getByRole('button', { name: '选择资源', exact: true }).click()
    await empty.page.getByRole('button', { name: /从零开始 第三季 字幕资源/ }).waitFor()
    await empty.page.getByRole('searchbox', { name: '搜索蜜柑资源' }).fill('none'); await empty.page.getByRole('button', { name: '搜索', exact: true }).click()
    await empty.page.getByText('没有找到资源，可以换个名称搜索，或先保存草稿。', { exact: true }).waitFor()
    empty.state.failSearch = true
    await empty.page.getByRole('searchbox', { name: '搜索蜜柑资源' }).fill('other'); await empty.page.getByRole('button', { name: '搜索', exact: true }).click()
    await empty.page.getByText('模拟来源冷却', { exact: true }).waitFor()
    assert.equal(await empty.page.getByRole('button', { name: '搜索', exact: true }).isDisabled(), true)
    await empty.page.getByText('上次成功搜索：none', { exact: false }).waitFor()
    await empty.go(); empty.state.failSearch = false; empty.state.empty = true; await empty.source(); await empty.preview()
    await empty.page.getByText('来源读取成功，当前没有发布。可以保存配置，之后再预览。', { exact: true }).waitFor()
    await empty.page.getByRole('button', { name: '添加备用', exact: true }).click()
    await empty.page.getByRole('button', { name: '保存为暂停', exact: true }).click()
    await empty.page.getByRole('heading', { name: '已保存为暂停配置' }).waitFor()
    assert.equal(empty.writes()[0].body.config.sources[1].status, 'draft', 'empty standby does not block main paused save')
    passed.push('搜索零结果/冷却区分旧查询；来源零发布成功，未配置备用保持草稿而不阻断主 RSS')

    const specs = await session(); await specs.go(); await specs.source()
    const sampleReads = specs.calls.filter(c => c.endpoint.endsWith('/source-bindings/preview')).length
    await specs.page.getByRole('button', { name: '修改来源规则', exact: true }).click()
    await specs.page.getByRole('checkbox', { name: /^1080p 繁 内嵌/ }).check()
    await specs.page.getByRole('button', { name: '更新来源规则', exact: true }).click()
    await specs.preview()
    let selectedRules = specs.calls.findLast(c => c.endpoint.endsWith('/subscriptions/preview')).body.config.sources[0].rules
    assert.equal(selectedRules.specMode, 'profiles'); assert.equal(selectedRules.specProfiles.length, 2)
    assert.equal(specs.calls.filter(c => c.endpoint.endsWith('/source-bindings/preview')).length, sampleReads, 'reopening and toggling specs reuse samples')
    await specs.page.getByRole('button', { name: '修改来源规则', exact: true }).click()
    await specs.page.getByRole('checkbox', { name: /^全部规格/ }).check()
    assert.equal(await specs.page.locator('.ae-spec-option input:checked').count(), 1, 'all specifications excludes profiles')
    await specs.page.getByRole('checkbox', { name: /^全部规格/ }).uncheck()
    await specs.page.getByRole('button', { name: '更新来源规则', exact: true }).click()
    await specs.page.getByText('请选择至少一种规格，或明确选择全部规格', { exact: true }).waitFor()
    assert.equal(await specs.page.locator('[data-spec-option]').first().evaluate(el => el === document.activeElement), true)
    assert.equal(await specs.page.getByText('请选择至少一种规格，或明确选择全部规格', { exact: true }).evaluate(el => { const r=el.getBoundingClientRect(); return r.top>=0 && r.bottom<=innerHeight }), true, 'error remains visible beside focused field')
    await specs.page.getByRole('checkbox', { name: /^全部规格/ }).check()
    await specs.page.getByRole('button', { name: '更新来源规则', exact: true }).click()
    await specs.preview()
    selectedRules = specs.calls.findLast(c => c.endpoint.endsWith('/subscriptions/preview')).body.config.sources[0].rules
    assert.equal(selectedRules.specMode, 'any'); assert.deepEqual(selectedRules.specProfiles, [])
    await specs.page.getByRole('button', { name: '修改来源规则', exact: true }).click()
    await specs.page.setViewportSize({ width: 320, height: 800 })
    assert.equal(await specs.page.locator('dialog').evaluate(el => el.scrollWidth <= el.clientWidth), true, 'single modal does not overflow at 320px')
    assert.equal(await specs.page.getByRole('button', { name: '更新来源规则', exact: true }).evaluate(el => { const r=el.getBoundingClientRect(); return r.top >= 0 && r.bottom <= innerHeight && r.left >= 0 && r.right <= innerWidth }), true, 'primary action stays visible on mobile')
    await specs.page.screenshot({ path: 'test-results/anime-picker-mobile-320.png', animations: 'disabled' })
    await specs.page.keyboard.press('Escape')
    assert.equal(await specs.page.getByRole('button', { name: '修改来源规则', exact: true }).evaluate(el => el === document.activeElement), true, 'Escape restores launcher focus')
    passed.push('读取样本先于范围确认，多规格OR与明确全部互斥、零选项阻断确认、缓存复用、320px浮窗与焦点恢复')

    const delayed = await session(); await delayed.go(); delayed.state.distinctGroups = true
    let releaseSample
    delayed.state.sampleGate = { group: '10', ready: new Promise(resolve => { releaseSample = resolve }) }
    await delayed.page.getByRole('button', { name: '选择资源', exact: true }).click()
    await delayed.page.getByRole('button', { name: /从零开始 第三季 字幕资源/ }).click()
    await delayed.page.getByRole('button', { name: /字幕组甲/ }).click()
    await wait(() => delayed.calls.some(c => c.endpoint.endsWith('/source-bindings/preview')))
    assert.equal(await delayed.page.getByRole('checkbox', { name: /^全部规格/ }).isDisabled(), true)
    assert.equal(await delayed.page.getByRole('button', { name: '使用此来源', exact: true }).isDisabled(), true)
    await delayed.page.getByRole('button', { name: '返回字幕组', exact: true }).click()
    await delayed.page.getByRole('button', { name: /字幕组乙/ }).click()
    await delayed.page.getByRole('checkbox', { name: /^720p/ }).waitFor()
    releaseSample()
    assert.equal(await delayed.page.getByRole('checkbox', { name: /^1080p/ }).count(), 0, 'late sample from previous group cannot overwrite current group')
    await delayed.page.getByRole('checkbox', { name: /^720p/ }).check()
    await delayed.page.getByRole('button', { name: '使用此来源', exact: true }).click()
    assert.equal(await delayed.page.getByLabel('从第几话开始').inputValue(), '1')
    delayed.state.failSample = true
    await delayed.page.getByRole('button', { name: '修改来源规则', exact: true }).click()
    await delayed.page.getByRole('button', { name: '刷新样本', exact: true }).click()
    await delayed.page.getByText('模拟样本读取失败，保留上次成功样本。', { exact: true }).waitFor()
    assert.equal(await delayed.page.getByRole('checkbox', { name: /^720p/ }).isChecked(), true)
    await delayed.page.keyboard.press('Escape')
    passed.push('首次样本读取期间不可提前确认，换组取消旧读取，失败保留上次样本及已选规则')

    const malformed = await session(); await malformed.go(); malformed.state.saveMode = 'invalid'
    await malformed.page.getByRole('button', { name: '保存草稿', exact: true }).click()
    await malformed.page.getByRole('button', { name: '重试原保存请求', exact: true }).click()
    await malformed.page.getByRole('heading', { name: '草稿已保存' }).waitFor()
    assert.deepEqual(malformed.writes()[1], malformed.writes()[0], 'malformed success receipt keeps same immutable operation')
    const duplicate = await session(); await duplicate.go(); duplicate.state.saveMode = 'duplicate'
    await duplicate.page.getByRole('button', { name: '保存草稿', exact: true }).click()
    await duplicate.page.getByRole('button', { name: '打开已有配置', exact: true }).waitFor()
    assert.equal(duplicate.writes().length, 1)
    assert.equal(duplicate.calls.some(call => call.endpoint.endsWith('/saved-subscription') && call.method === 'PATCH'), false)
    passed.push('异常成功回执仍保留原操作重试，重复篇章只提供打开已有配置且不自动覆盖')

    const visual = await session(); visual.state.naming = true; await visual.go(); await visual.source(); await visual.page.getByLabel('发布第几话', {exact:true}).fill('25'); await visual.preview()
    await visual.page.getByRole('button', { name: '修改来源规则', exact: true }).click()
    assert.equal(await visual.page.getByRole('button', { name: '更新来源规则', exact: true }).evaluate(el => el.getBoundingClientRect().bottom <= innerHeight), true, 'primary action stays visible on desktop')
    await visual.page.screenshot({ path: 'test-results/anime-picker-desktop-light.png', animations: 'disabled' })
    await visual.page.keyboard.press('Escape')
    await visual.page.locator('.ae-mapping-anchor').scrollIntoViewIfNeeded()
    assert.equal(await visual.page.getByLabel('发布第几话',{exact:true}).evaluate(el=>{const r=el.getBoundingClientRect();const hit=document.elementFromPoint(r.left+r.width/2,r.top+r.height/2);return hit===el}),true,'desktop mapping input is reachable above sticky save bar')
    await visual.page.screenshot({path:'test-results/anime-mapping-desktop-viewport.png',animations:'disabled'})
    await visual.page.evaluate(()=>document.documentElement.classList.add('dark'))
    await visual.page.screenshot({path:'test-results/anime-mapping-desktop-dark-viewport.png',animations:'disabled'})
    await visual.page.evaluate(()=>document.documentElement.classList.remove('dark'))
    await visual.page.evaluate(() => scrollTo(0, 0))
    await visual.page.screenshot({ path: 'test-results/anime-editor-desktop-light.png', fullPage: true, animations: 'disabled' })
    await visual.page.evaluate(() => document.documentElement.classList.add('dark'))
    await visual.page.screenshot({ path: 'test-results/anime-editor-desktop-dark.png', fullPage: true, animations: 'disabled' })
    await visual.page.evaluate(() => document.documentElement.classList.remove('dark'))
    await visual.page.setViewportSize({ width: 320, height: 800 })
    assert.ok(await visual.page.locator('.ae-summary-title h3').evaluate(el=>el.getBoundingClientRect().width)>100,'mobile source title keeps readable width')
    assert.ok(await visual.page.locator('.ae-summary-title').evaluate(el=>el.getBoundingClientRect().height)<200,'mobile source summary does not collapse into a vertical title')
    await visual.page.screenshot({ path: 'test-results/anime-editor-mobile-320.png', fullPage: true, animations: 'disabled' })
    assert.equal(await visual.page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'no mobile horizontal overflow')
    await visual.page.locator('.ae-save-bar').scrollIntoViewIfNeeded()
    assert.equal(await visual.page.getByRole('button', { name: '保存为暂停', exact: true }).isVisible(), true)
    await visual.page.getByRole('button', { name: '保存草稿', exact: true }).focus(); await visual.page.keyboard.press('Tab')
    assert.equal(await visual.page.getByRole('button', { name: '保存为暂停', exact: true }).evaluate(el => el === document.activeElement), true)
    await visual.page.locator('.ae-release input').first().uncheck()
    await wait(async () => !await visual.page.getByRole('button', { name: '处理中…' }).count())
    await visual.page.getByRole('button', { name: '保存草稿', exact: true }).click(); await visual.page.getByRole('heading', { name: '草稿已保存' }).waitFor()
    await visual.page.getByRole('button', { name: '继续编辑' }).click(); await visual.page.getByRole('heading', { name: '编辑动漫订阅', exact: true }).waitFor()
    await visual.page.getByText(/已恢复 \d+ 条历史选择记录/).waitFor()
    assert.equal(await visual.page.locator('.ae-release input:checked').count(), 0)
    await visual.preview()
    assert.equal(await visual.page.locator('.ae-release input').first().isChecked(), false, 'saved explicit exclusion survives fresh preview scope')
    passed.push('桌面明暗及 320px 截图、无横向溢出、键盘操作；历史选择待核对且保留明确排除')

    const naming = await session(); naming.state.naming = true; await naming.go(); await naming.source()
    await naming.page.getByRole('heading', {name:/从零开始的异世界生活.*2016.*117061/}).waitFor()
    assert.equal(await naming.page.getByLabel('目标季', {exact:true}).inputValue(), '2')
    assert.equal(await naming.page.getByLabel('发布第几话', {exact:true}).inputValue(), '', 'continuous RSS cannot establish an anchor from its minimum')
    await naming.page.getByLabel('发布第几话', {exact:true}).fill('25')
    await naming.page.getByLabel('从第几话开始').fill('30')
    await naming.page.getByText('发布第 30 话对应 S02E06 · 目标第 6 集', {exact:true}).waitFor()
    await naming.page.getByRole('button', {name:'添加备用',exact:true}).click(); await naming.source('20','1','80')
    await naming.page.getByLabel('发布第几话', {exact:true}).fill('1')
    await naming.page.getByText('发布第 1 话对应 S02E01 · 目标第 1 集',{exact:true}).waitFor()
    await naming.page.locator('.ae-source-tabs button').first().click()
    assert.equal(await naming.page.getByLabel('发布第几话',{exact:true}).inputValue(),'25')
    await naming.page.getByRole('button',{name:'保存为暂停',exact:true}).click()
    await naming.page.getByRole('heading',{name:'已保存为暂停配置'}).waitFor()
    const namingWrite=naming.writes().at(-1).body.config.naming
    assert.equal(namingWrite.sources[0].segments[0].sourceBase,25)
    assert.equal(namingWrite.sources[1].segments[0].sourceBase,1)
    assert.equal(naming.writes().at(-1).body.config.sources[0].rules.minimumEpisode,30)
    await naming.page.getByRole('button',{name:'继续编辑',exact:true}).click()
    await naming.page.getByLabel('发布第几话',{exact:true}).waitFor()
    assert.equal(await naming.page.getByLabel('发布第几话',{exact:true}).inputValue(),'25')
    await naming.page.getByText('发布第 30 话对应 S02E06 · 目标第 6 集',{exact:true}).waitFor()
    passed.push('进入配置推荐TMDB作品/目标季，25对应S02E01与补番30对应E06，备用独立并稳定回显')

    await naming.page.locator('.ae-source-naming summary').click()
    await naming.page.getByRole('button',{name:'添加分段',exact:true}).click()
    await naming.page.getByText('分段范围重叠，请先限定上一段终点',{exact:true}).waitFor()
    await naming.page.getByRole('button',{name:'保存草稿',exact:true}).click()
    assert.equal(naming.writes().length,1,'invalid mapping blocked before API write')
    await naming.page.getByRole('button',{name:'删除此分段',exact:true}).click()
    await naming.page.getByRole('button',{name:'添加单集例外',exact:true}).click()
    await naming.page.getByLabel('原始编号',{exact:true}).fill('12.5')
    await naming.page.getByLabel('例外目标季').selectOption('1')
    await naming.page.getByText('特殊集 12.5对应 S01E01 · 目标第 1 集',{exact:true}).waitFor()
    await naming.page.getByLabel('例外目标集',{exact:true}).fill('99')
    await naming.page.getByText('特殊集 12.5对应 S01E99 · 目标资料待核对',{exact:true}).waitFor()
    assert.equal(await naming.page.locator('.ae-path-preview').innerText().then(s=>s.includes('S01E99')),false)
    assert.ok(naming.calls.filter(c=>c.endpoint.endsWith('/naming/target')).length<=3,'extra season reads are bounded')
    await naming.page.setViewportSize({width:320,height:800})
    await naming.page.locator('.ae-source-naming').screenshot({path:'test-results/anime-mapping-advanced-mobile-320.png',animations:'disabled'})
    assert.equal(await naming.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'advanced mapping fits320px')
    passed.push('分段重叠阻断保存，SP须显式目标且可预览，缺失目标不生成路径，跨季读取有界')

    const partial=await session(); partial.state.naming=true; partial.state.sampleEpisode=8; await partial.go(); await partial.source('10','1','64')
    assert.equal(await partial.page.getByLabel('发布第几话',{exact:true}).inputValue(),'1','partial feed beginning at8 must not become E01')
    await partial.page.getByLabel('发布第几话',{exact:true}).fill('5')
    await partial.page.getByRole('button',{name:'修改来源规则',exact:true}).click(); await partial.page.getByRole('button',{name:'刷新样本',exact:true}).click()
    await wait(()=>partial.calls.filter(c=>c.endpoint.endsWith('/source-bindings/preview')).length===2)
    await partial.page.keyboard.press('Escape')
    assert.equal(await partial.page.getByLabel('发布第几话',{exact:true}).inputValue(),'5')
    await partial.page.getByLabel('目标季',{exact:true}).selectOption('1')
    await partial.page.getByText(/此来源沿用手动设置的第 2 季/).waitFor()
    assert.equal(await partial.page.getByLabel('发布第几话',{exact:true}).inputValue(),'5')
    passed.push('RSS窗口起于8仍按1对1建议，样本刷新和换季不覆盖手动基准且提示独立季')

    const late=await session(); late.state.naming=true; let releaseNaming
    late.state.namingGate=new Promise(resolve=>{releaseNaming=resolve})
    await late.page.goto(origin+'/anime/new?bangumiId=123',{waitUntil:'domcontentloaded'})
    await late.page.getByRole('button',{name:'关联 TMDB',exact:true}).click()
    await late.page.getByRole('button',{name:'保留原名，不关联 TMDB',exact:true}).click()
    releaseNaming(); await late.page.waitForLoadState('networkidle')
    assert.equal(late.calls.some(c=>c.endpoint.endsWith('/naming/target')),false,'late automatic recommendation cannot override explicit original mode')
    await late.page.getByRole('button',{name:'保存草稿',exact:true}).click(); await late.page.getByRole('heading',{name:'草稿已保存'}).waitFor()
    assert.equal(late.writes()[0].body.config.naming,null)
    const unavailable=await session(); unavailable.state.failNaming=true; await unavailable.go()
    await unavailable.page.getByRole('button',{name:'保存草稿',exact:true}).click(); await unavailable.page.getByRole('heading',{name:'草稿已保存'}).waitFor()
    passed.push('晚到的自动推荐不能覆盖保留原名，TMDB不可用仍可保存且不强制关联')

    const chapter = await session(); chapter.state.naming = true; await chapter.go()
    const chips = chapter.page.locator('.ae-episode-chip')
    assert.equal(await chips.count(),8)
    assert.equal(await chips.first().innerText(),'S02E01')
    assert.match(await chips.first().getAttribute('title'),/Bangumi 原编号 59（篇章内第 1 集）/)
    await chapter.page.locator('.ae-naming-target .ae-advanced summary').click()
    await chapter.page.getByLabel('剧集编排',{exact:true}).selectOption('group001')
    await wait(async()=>await chips.first().innerText()==='S02E09')
    assert.equal(await chips.last().innerText(),'S02E16')
    await chapter.page.getByLabel('目标季',{exact:true}).selectOption('1')
    await wait(async()=>await chips.first().innerText()==='BGM 59')
    assert.match(await chapter.page.locator('.ae-chapter-episodes').innerText(),/8 集待核对/)
    assert.equal(chapter.calls.filter(c=>c.endpoint.endsWith('/episodes')).length,1,'changing target only recomputes chapter reference')
    assert.equal(chapter.writes().length,0)
    await chapter.page.getByLabel('目标季',{exact:true}).selectOption('2')
    await wait(async()=>await chips.first().innerText()==='S02E09')
    await chapter.page.screenshot({path:'test-results/anime-chapter-desktop-light.png',animations:'disabled'})
    await chapter.page.evaluate(()=>document.documentElement.classList.add('dark'))
    await chapter.page.screenshot({path:'test-results/anime-chapter-desktop-dark.png',animations:'disabled'})
    await chapter.page.evaluate(()=>document.documentElement.classList.remove('dark'))
    await chapter.page.setViewportSize({width:320,height:800})
    const mobileChapterLayout = await chapter.page.evaluate(()=>({width:innerWidth,scroll:document.documentElement.scrollWidth,overflow:[...document.querySelectorAll('body *')].filter(el=>el.getBoundingClientRect().right>innerWidth+1).slice(0,10).map(el=>({tag:el.tagName,cls:el.className,right:el.getBoundingClientRect().right,width:el.getBoundingClientRect().width}))}))
    assert.ok(mobileChapterLayout.scroll<=mobileChapterLayout.width,JSON.stringify(mobileChapterLayout))
    assert.ok(await chapter.page.locator('.ae-chapter-identity').evaluate(el=>el.getBoundingClientRect().width)>120)
    await chapter.page.screenshot({path:'test-results/anime-chapter-mobile-320.png',animations:'disabled'})
    passed.push('篇章集数显示真实TMDB季集，分组切换本地重算，原编号可核对，未对应显式待核对，320px无溢出')

    const longChapter = await session(); longChapter.state.chapterCount = 101; await longChapter.go()
    assert.equal(await longChapter.page.locator('.ae-episode-chip').count(),12)
    assert.equal(longChapter.calls.filter(c=>c.endpoint.endsWith('/episodes')).length,1)
    await longChapter.page.getByRole('button',{name:'展开集数（已读 100 集）',exact:true}).click()
    assert.equal(await longChapter.page.locator('.ae-episode-chip').count(),100)
    assert.ok(await longChapter.page.locator('.ae-episode-chips').evaluate(el=>el.getBoundingClientRect().height)<=143)
    await longChapter.page.getByRole('button',{name:'继续读取 1 集',exact:true}).click()
    await wait(async()=>await longChapter.page.locator('.ae-episode-chip').count()===101)
    assert.equal(longChapter.calls.filter(c=>c.endpoint.endsWith('/episodes')).at(-1).query,'?offset=100')
    await longChapter.page.getByRole('button',{name:'收起集数',exact:true}).click()
    assert.equal(await longChapter.page.locator('.ae-episode-chip').count(),12)
    const failedChapter = await session(); failedChapter.state.failEpisodes = true; await failedChapter.go()
    await failedChapter.page.getByText('模拟集数读取失败',{exact:false}).waitFor()
    failedChapter.state.failEpisodes = false
    await failedChapter.page.getByRole('button',{name:'重新读取集数',exact:true}).click()
    await failedChapter.page.locator('.ae-episode-chip').first().waitFor()
    assert.equal(await failedChapter.page.locator('.ae-episode-chip').first().innerText(),'BGM 59')
    await failedChapter.page.getByRole('button',{name:'保存草稿',exact:true}).click()
    await failedChapter.page.getByRole('heading',{name:'草稿已保存',exact:true}).waitFor()
    passed.push('长篇默认仅12项，100集分页按需读取且限制高度，失败可重试，未关联TMDB不阻断草稿保存')

    const cta = await session(); await cta.go('/anime?view=calendar&day=1')
    await cta.page.locator('.mikan-anime-card').first().click()
    await cta.page.getByRole('link', { name: '选择字幕组并配置订阅', exact: true }).click()
    await wait(() => /bangumiId=123/.test(cta.page.url()) && /itemId=300/.test(cta.page.url()))
    await cta.page.getByRole('heading', { name: '从零开始的异世界生活 第三季', exact: true }).waitFor()
    const unlinked = await session(); await unlinked.go('/anime/new?source=mikan&itemId=300')
    await unlinked.page.getByRole('button',{name:'选择资源',exact:true}).click()
    await unlinked.page.getByRole('button',{name:/字幕组甲/}).click()
    await unlinked.page.getByRole('checkbox',{name:/^1080p 简 内嵌/}).check()
    await unlinked.page.getByRole('button',{name:'使用此来源',exact:true}).click()
    await unlinked.page.getByRole('button',{name:'保存草稿',exact:true}).click()
    await unlinked.page.getByRole('heading',{name:'草稿已保存',exact:true}).waitFor()
    assert.equal(unlinked.calls.some(c=>c.endpoint.includes('/discovery/')),false,'unlinked Mikan source must not query Bangumi')
    assert.equal(unlinked.writes().at(-1).body.config.entry.bangumiId,undefined)
    assert.equal(unlinked.writes().at(-1).body.config.sources[0].itemId,'300')
    passed.push('蜜柑条目直接继承到字幕组，无 Bangumi 也可读取样本、选择规格和保存草稿')
    const ordinary = await session(undefined, 'user'); await ordinary.go()
    assert.equal(new URL(ordinary.page.url()).pathname, '/')
    assert.equal(ordinary.calls.some(c => c.endpoint.includes('/admin/anime')), false)
    passed.push('详情关联导航后使用当前篇章建立配置，普通用户前端守卫拒绝管理员页面')

    for (const ctx of contexts) await ctx.close()
    assert.deepEqual(errors, [], 'no browser runtime errors')
    assert.ok([...calls, ...directory.calls, ...unknown.calls].filter(c => c.method !== 'GET').every(c => c.endpoint.startsWith('/api/admin/anime/')), 'all writes confined to anime config/preview APIs')
    console.log(JSON.stringify({ passed, total: passed.length, screenshots: ['anime-editor-desktop-light.png', 'anime-editor-desktop-dark.png', 'anime-editor-mobile-320.png', 'anime-picker-desktop-light.png', 'anime-picker-mobile-320.png', 'anime-chapter-desktop-light.png', 'anime-chapter-desktop-dark.png', 'anime-chapter-mobile-320.png'] }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
