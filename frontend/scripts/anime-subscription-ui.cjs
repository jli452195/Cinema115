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
  async function session(viewport = { width: 1440, height: 1080 }, role = 'admin', options = {}) {
    const context = await browser.newContext({ viewport, serviceWorkers: 'block', ...options }); contexts.push(context)
    await context.addInitScript(() => { localStorage.setItem('admin_token', 'mock-token'); localStorage.setItem('cinema115-theme', 'light'); Object.defineProperty(crypto, 'randomUUID', { value: undefined }) })
    const page = await context.newPage(); page.setDefaultTimeout(10000)
    page.on('pageerror', e => errors.push(e.message))
    page.on('dialog', dialog => dialog.accept())
    const calls = [], state = { role, naming: true, primary: true, ready: true, empty: false, zeroMatch: false, failPreview: false, failSearch: false, expired: false, saveMode: '', settingsMode: '', loseSelection: false, conflict: false, batch: 0, batches: new Map(), saved: undefined,
      settings: { revision: 1, accountId: 'primary', accountRevision: 2, uid: '10001', downloadCid: '100', downloadPath: '/动漫下载', organizeCid: '200', organizePath: '/动漫整理' } }
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
    function summary(source) { return { sourceRef: source.ref, sourcePreviewId: source.id, loadRevision: source.rev, generatedAt: Date.now(), expiresAt: source.expires, expired: source.expires < Date.now(), rawCount: source.items.length, loadedCount: source.items.length, matchedCount: source.items.filter(i => i.selectable).length, reviewCount: source.items.filter(i => i.decision === 'needs_confirmation').length, selectedCount: source.selected.length, pendingSelectionCount: source.pending.length, completeness: source.take > 100 || state.empty ? 'visible_end' : 'partial', stopReason: source.config.provider === 'anibt' && source.take >= 100 ? 'source_limit' : source.take > 100 ? '' : 'more_available', loadedFirst: source.items.length ? 25 : null, loadedLast: source.items.length ? 25 + Math.floor((source.items.length - 1) / 2) : null, gapsWithinLoadedMatches: [{ first: 26, last: 26 }], unloadedHistory: source.take <= 100 && !state.empty } }
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
      else if (endpoint.endsWith('/catalog-status')) data = { items: body.itemIds.map(itemId => ({ itemId, bangumiId: null, subscriptions: [] })) }
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
      } else if (endpoint.endsWith('/settings') && method === 'GET') {
        if (state.settingsReadFailure) { status = 503; data = { error: '模拟目录设置读取失败' } }
        else data = { settings: state.settings, primaryAccount: state.primary ? { id: 'primary', name: '115 主网盘', uid: '10001', revision: 2 } : null, readiness: { ready: state.ready && state.primary, issues: state.primary ? state.ready ? [] : ['DOWNLOAD_DIRECTORY_REQUIRED'] : ['PRIMARY_ACCOUNT_REQUIRED'] } }
      }
      else if (endpoint.endsWith('/settings') && method === 'PATCH') {
        state.settings = { ...state.settings, revision: 2, downloadCid: body.downloadCid, downloadPath: '/新动漫目录' }; state.ready = true
        if (state.settingsMode === 'unknown') { state.settingsMode = ''; await route.abort(); return }
        data = state.settings
      } else if (endpoint.includes('/cloud-download/paths')) data = { directories: [{ id: '200', name: '新动漫目录' }], hasMore: false }
      else if (endpoint.endsWith('/catalog/anibt')) data = {provider:'anibt', season:'2026-10', days:[{day:1,items:[{id:'123',bangumiId:'123',animeId:'native-anibt',title:'AniBT 原站番剧',originalTitle:'Re:ZERO',coverUrl:'',format:'TV',rating:7.8,episodes:12,airingAt:null,releaseCount:120,hasRelease:true}]}],items:[],fetchedAt:Date.now(),expiresAt:Date.now()+600000}
      else if (endpoint.endsWith('/catalog/garden')) data={provider:'garden',season:'2026-10',days:[{day:1,items:[{id:'123',bangumiId:'123',title:'Garden 原站番剧',originalTitle:'Re:ZERO',coverUrl:'',platform:'TV',airDate:'2026-10-01'}]}],items:[],fetchedAt:Date.now(),expiresAt:Date.now()+600000}
      else if (endpoint.endsWith('/sources/garden/search')) data={items:[{id:'123',title:'Garden 原站番剧'}],truncated:false}
      else if (endpoint.endsWith('/sources/garden/123')) data={entry:{id:'123',title:'Garden 原站番剧',groupsPartial:true,groups:[{id:'dmhy_f9',name:'桜都字幕组',origin:'dmhy',originName:'动漫花园',kind:'fansub'},{id:'mikan_f9',name:'桜都字幕组',origin:'mikan',originName:'蜜柑计划',kind:'fansub'},{id:'moe_p99',name:'独立发布者',origin:'moe',originName:'萌番组',kind:'publisher'}],bangumiIds:['123']}}
      else if (endpoint.endsWith('/sources/anibt/search')) {
        if (state.anibtGate) await state.anibtGate
        data={items:[{id:'123',title:'AniBT 原站番剧'}],truncated:false}
      }
      else if (endpoint.endsWith('/sources/anibt/123')) data={entry:{id:'123',title:'AniBT 原站番剧',groups:state.noAnibtGroups?[]:[{id:'lolihouse',name:'LoliHouse'},{id:'toonshub',name:'ToonsHub'}],bangumiIds:['123']}}
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
        let labels = state.distinctGroups && body.source.groupId === '20' ? ['720p'] : ['1080p 简 内嵌', '1080p 繁 内嵌', '1080p 简繁日 内封 HEVC 10bit MKV']
        if (state.manySpecs) {
          specs.splice(0, specs.length, ...Array.from({length:13}, (_, i) => ({resolution:['720p','1080p','2160p'][i%3],languages:[i%2 ? 'zh-Hans' : 'zh-Hant'],subtitleMode:i%3 ? 'soft' : 'hard',codec:i<6 ? 'h264' : 'h265',container:i<12 ? 'mkv' : 'mp4'})))
          labels = specs.map((_, i) => `规格组合 ${i+1}`)
        }
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
      } else if (endpoint.endsWith('/start') && method === 'POST') {
        for (const ref of body.sources) { const source = state.batches.get(ref.batchId)?.sources.get(ref.sourceRef); assert.ok(source); assert.equal(ref.loadRevision, source.rev); assert.deepEqual(ref.selectedReleaseIds, source.selected) }
        if (state.saveMode === 'conflict') { status = 409; data = { error: '配置版本已变化，输入保留', code: 'REVISION_CONFLICT' } }
        else if (state.saveMode === 'duplicate') { status = 409; data = { error: '该篇章已有订阅配置', code: 'SUBSCRIPTION_EXISTS', details: { existingId: 'saved-subscription' } } }
        else {
          const ids = Object.fromEntries(body.config.sources.map(s => [s.ref, s.id || `source-${s.ref}`]))
          state.saved = { id: 'saved-subscription', entryId: 'entry-123', status: 'enabled', firstEnabledAt: Date.now(), revision: body.expectedRevision + 2, entry: { id: 'entry-123', bangumiId: '123', editionKey: 'original', title: card('123').title, originalTitle: '', revision: 1, metadata: { coverUrl: '', airDate: '2026-10-01' } },
            sources: body.config.sources.map(s => ({ ...s, id: ids[s.ref], revision: s.expectedRevision + 1, configuredState: s.status })), form: { formSelections: body.config.formSelections.map(s => ({ ...s, sourceRef: ids[s.sourceRef] })), previewRefs: body.config.previewRefs },
            naming: body.config.naming ? {schemaVersion:1,target:structuredClone(state.namingTarget),sources:body.config.naming.sources.map(s=>({...s,sourceRef:ids[s.sourceRef]}))} : null }
          if (state.saveMode === 'unknown') { state.saveMode = ''; await route.abort(); return }
          if (state.saveMode === 'invalid') { state.saveMode = ''; data = {}; }
          else data = { id: state.saved.id, entryId: 'entry-123', revision: state.saved.revision, status: 'enabled', sourceIds: ids, sourceRevisions: Object.fromEntries(body.config.sources.map(s => [s.ref, s.expectedRevision + 1])), requiresRepreview: false }
        }
      } else if (/\/subscriptions\/[^/]+$/.test(endpoint) && method === 'GET') {
        if (!state.saved) { status = 404; data = { code: 'NOT_FOUND', error: '配置不存在' } } else data = state.saved
      } else if (endpoint.includes('/tmdb/')) data = { items: [], results: [], total: 0 }
      else if (endpoint.includes('/emby/')) data = { configured: false, items: [] }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
    })
    async function go(suffix = '/anime/new?bangumiId=123') { await page.goto(origin + suffix, { waitUntil: 'networkidle' }); if (role === 'admin') await page.locator(suffix.startsWith('/anime?') ? '.mikan-anime-card, .anibt-anime-card, .garden-anime-card' : '.ae-chapter').first().waitFor() }
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

  async function start(s) {
    const button = s.page.locator('.ae-save-bar button')
    if ((await button.innerText()) === '预览并核对') {
      await button.click()
      await s.page.getByRole('button', { name: '开始订阅', exact: true }).waitFor()
    }
    await s.page.getByRole('button', { name: '开始订阅', exact: true }).click()
  }
  try {
    const first = await session(), { page, calls, go, source, preview } = first
    await go()
    assert.equal(calls.some(c => c.endpoint.includes('/sources/mikan')), false)
    assert.equal(await page.getByRole('button', { name: /保存草稿|保存为暂停/ }).count(), 0)
    assert.equal(await page.locator('.ae-save-bar button').isDisabled(), true)
    await source()
    const before = first.writes().length
    await page.getByRole('button', { name: '预览并核对', exact: true }).click()
    await page.getByRole('button', { name: '开始订阅', exact: true }).waitFor()
    assert.equal(first.writes().length, before, 'preview does not persist a subscription')
    assert.equal(await page.locator('.ae-release input:checked').count(), 48)
    assert.match(await page.locator('.ae-save-bar').innerText(), /已选 48 条资源/)
    passed.push('无草稿入口，配置完整后先核对预览，不创建暂停中间态')

    await page.getByRole('button', { name: '继续加载历史', exact: true }).click()
    await wait(async () => (await page.locator('.ae-page-buttons').innerText()).includes('1 / 2'))
    await page.getByRole('button', { name: '资源下一页' }).click()
    await wait(async () => (await page.locator('.ae-page-buttons').innerText()).includes('2 / 2'))
    assert.equal(await page.locator('.ae-release input:checked').count(), 0)
    await page.locator('.ae-release input').first().check()
    await wait(() => calls.some(c => c.endpoint.endsWith('/selections')))
    await wait(async () => !await page.getByRole('button', { name: '处理中…' }).count())
    assert.equal(calls.findLast(c => c.endpoint.endsWith('/selections')).body.selectedReleaseIds.length, 49)
    await page.getByRole('button', { name: '添加备用', exact: true }).click()
    await source('20', '1', '80'); await preview()
    assert.equal(calls.findLast(c => c.endpoint.endsWith('/subscriptions/preview')).body.previewRefs.length, 1)
    const reads = calls.filter(c => c.endpoint.endsWith('/subscriptions/preview')).length
    await start(first)
    await page.getByRole('heading', { name: '已开始订阅', exact: true }).waitFor()
    const write = first.writes().at(-1)
    assert.equal(write.endpoint, '/api/admin/anime/subscriptions/start')
    assert.equal(write.body.saveMode, undefined)
    assert.equal(write.body.sources.length, 2)
    assert.equal(write.body.sources.find(s => s.sourceRef === 'main').selectedReleaseIds.length, 49)
    assert.equal(calls.filter(c => c.endpoint.endsWith('/subscriptions/preview')).length, reads, 'start reuses reviewed snapshots')
    assert.equal(calls.some(c => /enable-preview|\/enable$/.test(c.endpoint)), false)
    passed.push('跨页选择、多版本与备用独立提交，直接开始不重复读取 RSS')

    const unknown = await session(); await unknown.go(); await unknown.source(); await unknown.preview()
    unknown.state.saveMode = 'unknown'
    await start(unknown)
    await unknown.page.getByRole('button', { name: '核对原订阅请求', exact: true }).waitFor()
    assert.equal(await unknown.page.getByLabel('从第几话开始').isDisabled(), true)
    const frozen = structuredClone(unknown.writes()[0])
    await unknown.page.getByRole('button', { name: '核对原订阅请求', exact: true }).click()
    await unknown.page.getByRole('heading', { name: '已开始订阅', exact: true }).waitFor()
    assert.deepEqual(unknown.writes()[1], frozen)
    passed.push('未知结果锁定表单，以原键和原正文核对，成功后无二次启用')

    const conflict = await session(); await conflict.go(); await conflict.source(); await conflict.preview()
    conflict.state.saveMode = 'conflict'; await start(conflict)
    await conflict.page.getByText('配置版本已变化，输入保留', { exact: true }).waitFor()
    assert.equal(await conflict.page.getByLabel('从第几话开始').inputValue(), '25')
    assert.equal(await conflict.page.getByLabel('从第几话开始').isEnabled(), true)
    assert.equal(conflict.state.saved, undefined)
    conflict.state.saveMode = ''; await start(conflict)
    await conflict.page.getByRole('heading', { name: '已开始订阅', exact: true }).waitFor()
    assert.equal(conflict.writes()[1].body.expectedRevision, 0)
    passed.push('明确失败保留输入、不保存半成品、不静默套用新版本')

    const malformed = await session(); await malformed.go(); await malformed.source(); await malformed.preview()
    malformed.state.saveMode = 'invalid'; await start(malformed)
    await malformed.page.getByRole('button', { name: '核对原订阅请求', exact: true }).click()
    await malformed.page.getByRole('heading', { name: '已开始订阅', exact: true }).waitFor()
    assert.deepEqual(malformed.writes()[1], malformed.writes()[0])
    const duplicate = await session(); await duplicate.go(); await duplicate.source(); await duplicate.preview()
    duplicate.state.saveMode = 'duplicate'; await start(duplicate)
    await duplicate.page.getByRole('button', { name: '打开已有订阅', exact: true }).waitFor()
    assert.equal(duplicate.writes().length, 1)
    passed.push('残缺成功回执继续核对原请求，重复篇章只提供打开已有订阅')

    const empty = await session(); empty.state.empty = true; await empty.go(); await empty.source(); await empty.preview()
    await empty.page.getByRole('button', { name: '添加备用', exact: true }).click()
    await start(empty)
    await empty.page.getByRole('heading', { name: '已开始订阅', exact: true }).waitFor()
    assert.equal(empty.writes()[0].body.config.sources.length, 1, 'blank standby omitted')
    assert.deepEqual(empty.writes()[0].body.sources[0].selectedReleaseIds, [])
    const zero = await session(); zero.state.zeroMatch = true; await zero.go(); await zero.source(); await zero.preview()
    assert.match(await zero.page.locator('.ae-save-bar').innerText(), /已选 0 条资源/)
    await start(zero); await zero.page.getByRole('heading', { name: '已开始订阅', exact: true }).waitFor()
    passed.push('零发布和零匹配可开始等待更新，完全空白备用不入库')

    const partial = await session(); await partial.go(); await partial.source(); await partial.preview()
    await partial.page.getByRole('button', { name: '添加备用', exact: true }).click()
    await partial.page.getByRole('button', { name: '选择资源', exact: true }).click()
    await partial.page.getByRole('button', { name: /从零开始 第三季 字幕资源/ }).click()
    await partial.page.keyboard.press('Escape')
    // An unconfirmed modal edit is discarded and remains a blank source.
    assert.equal(await partial.page.getByRole('button', { name: '开始订阅', exact: true }).isEnabled(), true)
    await partial.source('20', '1', '80')
    await partial.page.getByLabel('范围终点', { exact: true }).fill('')
    assert.equal(await partial.page.locator('.ae-save-bar button').isDisabled(), true)
    assert.match(await partial.page.locator('.ae-save-bar').innerText(), /请完成备用 RSS/)
    assert.equal(partial.writes().length, 0)
    passed.push('取消来源选择丢弃临时输入，已填但不完整的备用必须修正或移除')

    const stale = await session(); await stale.go(); await stale.source(); await stale.preview()
    await stale.page.getByLabel('从第几话开始').fill('26')
    assert.equal(await stale.page.locator('.ae-release input').first().isDisabled(), true)
    await stale.preview()
    assert.equal(await stale.page.locator('.ae-save-bar button').isDisabled(), true)
    await stale.page.getByText('1 条旧选择待核对', { exact: true }).click()
    await stale.page.getByRole('button', { name: '放弃旧选择', exact: true }).click()
    await wait(async () => await stale.page.locator('.ae-pending').count() === 0)
    stale.state.loseSelection = true
    await stale.page.locator('.ae-release input:checked').first().uncheck()
    await stale.page.getByText('读取失败或超时，输入已保留', { exact: true }).waitFor()
    await stale.preview()
    assert.equal(await stale.page.locator('.ae-release input').nth(1).isChecked(), false)
    passed.push('规则修改使预览失效，旧选择须核对，选择回执丢失后恢复 CAS')

    const failed = await session(); await failed.go(); await failed.source(); failed.state.failPreview = true
    await failed.page.getByRole('button', { name: '预览并核对', exact: true }).click()
    await failed.page.getByText('模拟 RSS 读取失败，原输入保留', { exact: true }).waitFor()
    assert.equal(failed.writes().length, 0)
    assert.equal(await failed.page.getByLabel('从第几话开始').inputValue(), '25')
    failed.state.failPreview = false; await start(failed)
    await failed.page.getByRole('heading', { name: '已开始订阅', exact: true }).waitFor()
    passed.push('RSS 读取失败只保留本页输入，恢复后直接开始')

    const dirs = await session(); dirs.state.settingsReadFailure = true; await dirs.go(); await dirs.source()
    assert.equal(await dirs.page.locator('.ae-save-bar button').isDisabled(), true)
    dirs.state.settingsReadFailure = false
    await dirs.page.getByRole('button', { name: '动漫存储设置', exact: true }).click()
    await dirs.page.getByRole('dialog', { name: '动漫存储设置' }).locator('.storage-directory').first().waitFor()
    await dirs.page.getByRole('button', { name: '关闭弹窗', exact: true }).click()
    assert.equal(await dirs.page.getByLabel('从第几话开始').inputValue(), '25')
    assert.equal(await dirs.page.getByRole('button', { name: '预览并核对', exact: true }).isEnabled(), true)
    const noMain = await session(); noMain.state.primary = false; await noMain.go(); await noMain.source()
    assert.equal(await noMain.page.locator('.ae-save-bar button').isDisabled(), true)
    assert.equal(noMain.writes().length, 0)
    assert.equal(noMain.calls.some(c => c.endpoint.endsWith('/accounts')), false)
    const noTarget = await session(); noTarget.state.naming = false; await noTarget.go(); await noTarget.source()
    assert.equal(await noTarget.page.locator('.ae-save-bar button').isDisabled(), true)
    assert.match(await noTarget.page.locator('.ae-save-bar').innerText(), /请选择 TMDB/)
    passed.push('缺目录、账号或命名目标明确阻断，设置读取恢复不重置表单')

    const specs = await session(); await specs.go(); await specs.source()
    const samples = specs.calls.filter(c => c.endpoint.endsWith('/source-bindings/preview')).length
    await specs.page.getByRole('button', { name: '修改来源规则', exact: true }).click()
    await specs.page.getByRole('checkbox', { name: /^1080p 繁 内嵌/ }).check()
    await specs.page.getByRole('button', { name: '更新来源规则', exact: true }).click()
    await specs.preview()
    assert.equal(specs.calls.findLast(c => c.endpoint.endsWith('/subscriptions/preview')).body.config.sources[0].rules.specProfiles.length, 2)
    assert.equal(specs.calls.filter(c => c.endpoint.endsWith('/source-bindings/preview')).length, samples)
    await specs.page.getByRole('button', { name: '修改来源规则', exact: true }).click()
    await specs.page.getByRole('checkbox', { name: /^全部规格/ }).check()
    assert.equal(await specs.page.locator('.ae-spec-option input:checked').count(), 1)
    await specs.page.keyboard.press('Escape')
    assert.equal(await specs.page.getByRole('button', { name: '修改来源规则', exact: true }).evaluate(el => el === document.activeElement), true)
    passed.push('多规格 OR、全部规格互斥、取消弹窗与焦点恢复，复用样本减少请求')

    const limit = await session(); limit.state.manySpecs = true; await limit.go()
    await limit.page.getByRole('button', { name: '选择资源', exact: true }).click()
    await limit.page.getByRole('button', { name: /从零开始 第三季 字幕资源/ }).click()
    await limit.page.getByRole('button', { name: /字幕组甲/ }).click()
    const options = limit.page.locator('.ae-spec-option input')
    await wait(async () => await options.count() === 14)
    for (let i=0;i<12;i++) await options.nth(i).check()
    await options.nth(12).click()
    await limit.page.getByRole('alert').filter({ hasText: '每个来源最多选择 12 种规格' }).waitFor()
    assert.equal(await options.nth(12).isChecked(), false)
    passed.push('规格上限反馈与被拒绝勾选还原保持有效')

    const naming = await session(); await naming.go(); await naming.source()
    await naming.page.getByLabel('发布第几话', { exact: true }).fill('25')
    await naming.preview(); await start(naming)
    await naming.page.getByRole('heading', { name: '已开始订阅', exact: true }).waitFor()
    const mapping = naming.writes()[0].body.config.naming
    assert.equal(mapping.tmdbId, '117061')
    assert.equal(mapping.sources[0].segments[0].sourceBase, 25)
    assert.equal(mapping.sources[0].segments[0].targetSeason, 2)
    passed.push('开始请求同时冻结 TMDB 命名与字幕组季集偏移')

    for (const provider of ['anibt', 'garden']) {
      const native = await session(); await native.go()
      await native.page.getByRole('button', { name: provider === 'anibt' ? 'AniBT' : 'Anime Garden', exact: true }).click()
      await native.page.getByRole('button', { name: new RegExp(provider === 'anibt' ? 'AniBT 原站番剧' : 'Garden 原站番剧') }).click()
      await native.page.getByRole('button', { name: provider === 'anibt' ? /LoliHouse/ : /桜都字幕组.*动漫花园/ }).click()
      await native.page.getByRole('checkbox', { name: /^1080p 简 内嵌/ }).check()
      await native.page.getByRole('button', { name: '使用此来源', exact: true }).click()
      await native.preview(); await start(native)
      await native.page.getByRole('heading', { name: '已开始订阅', exact: true }).waitFor()
      assert.equal(native.writes()[0].body.config.sources[0].provider, provider)
      passed.push(`${provider} 共用页面直接开始，来源身份独立`)
    }

    const visual = await session({ width: 320, height: 800 }); await visual.go(); await visual.source(); await visual.preview()
    assert.equal(await visual.page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    await visual.page.locator('.ae-save-bar button').focus()
    assert.equal(await visual.page.locator('.ae-save-bar button').evaluate(el => el === document.activeElement), true)
    await visual.page.locator('.ae-save-bar').scrollIntoViewIfNeeded()
    await visual.page.screenshot({ path: 'test-results/anime-start-mobile-light.png', animations: 'disabled' })
    await visual.page.evaluate(() => document.documentElement.classList.add('dark'))
    await visual.page.screenshot({ path: 'test-results/anime-start-mobile-dark.png', animations: 'disabled' })
    await visual.page.setViewportSize({ width: 1440, height: 1080 })
    await visual.page.screenshot({ path: 'test-results/anime-start-desktop-dark.png', animations: 'disabled' })
    await visual.page.evaluate(() => document.documentElement.classList.remove('dark'))
    await visual.page.screenshot({ path: 'test-results/anime-start-desktop-light.png', animations: 'disabled' })
    await visual.page.locator('.ae-save-bar button').focus(); await visual.page.keyboard.press('Enter')
    await visual.page.getByRole('heading', { name: '已开始订阅', exact: true }).waitFor()
    passed.push('320px、明暗样式与键盘提交正常，无横向溢出')

    const ordinary = await session(undefined, 'user'); await ordinary.go()
    await ordinary.page.waitForURL(origin + '/')
    assert.equal(ordinary.calls.some(c => c.endpoint.includes('/admin/anime')), false)
    passed.push('普通用户路由守卫继续拒绝管理操作')

    assert.deepEqual(errors, [], 'no browser runtime errors')
    for (const context of contexts) await context.close()
    console.log(JSON.stringify({ passed, total: passed.length }, null, 2))
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
