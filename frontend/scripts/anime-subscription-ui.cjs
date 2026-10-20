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
    page.on('pageerror', e => errors.push(e.stack))
    page.on('dialog', dialog => dialog.accept())
    const calls = [], state = { role, naming: true, primary: true, ready: true, empty: false, zeroMatch: false, failPreview: false, failSearch: false, expired: false, saveMode: '', settingsMode: '', loseSelection: false, conflict: false, batch: 0, batches: new Map(), saved: undefined,
      settings: { revision: 1, accountId: 'primary', accountRevision: 2, uid: '10001', downloadCid: '100', downloadPath: '/动漫下载', organizeCid: '200', organizePath: '/动漫整理' } }
    const card = id => ({ id, title: '从零开始的异世界生活 第三季', originalTitle: 'Re:ZERO', coverUrl: '', date: '2026-10-01', weekday: 1 })
    function item(ref, index) {
      const review = index === 2, filtered = state.zeroMatch || index === 3, ep = 25 + Math.floor(index / 2)
      const result = { release: { id: `${ref.replace(/^source-/, '')}-release-${index}`, title: `[示例字幕组] 从零开始的异世界生活 [${ep}] [1080p] ${index % 2 ? '简繁内封 v2' : '简体内嵌'}`, publishedAt: Date.UTC(2026,9,20,12,30), parse: { kind: 'episode', episode: ep, rawEpisode: String(ep), resolution: '1080p', versionLabel: index % 2 ? 'v2' : '' } },
        observation: { title: '', sizeRaw: '650 MB' }, projection: { status: 'in_scope', sourceEpisode: ep },
        selectable: !review && !filtered, decision: review ? 'needs_confirmation' : filtered ? 'filtered' : 'matched', reasonCode: review ? 'EPISODE_NUMBER_UNRESOLVED' : filtered ? 'BELOW_SOURCE_MINIMUM' : 'MATCHED_METADATA_ONLY' }
      return state.previewTransform ? state.previewTransform(result, index) : result
    }
    function makeSource(config, take, previous) {
      const items = state.empty ? [] : Array.from({ length: Math.min(take, 120) }, (_, i) => item(config.ref, i))
      const pending = previous && config.rules.minimumEpisode !== previous.config.rules.minimumEpisode ? [{ releaseId: previous.selected[0] || `${config.ref}-old`, reason: 'changed', previousItem: previous.items[0] }] : []
      const selected = previous ? previous.selected.filter(id => items.some(item => item.release.id === id && item.selectable) && !pending.some(item => item.releaseId === id)) : items.filter(item => item.selectable).map(item => item.release.id)
      return { ref: config.ref, config, id: `preview-${config.ref}-${state.batch}`, rev: 1, take, items, selected, pending, expires: Date.now() + (state.expired ? -1000 : 900000) }
    }
    function summary(source) { return { sourceRef: source.ref, sourcePreviewId: source.id, loadRevision: source.rev, generatedAt: Date.now(), expiresAt: source.expires, expired: source.expires < Date.now(), rawCount: source.items.length, loadedCount: source.items.length, matchedCount: source.items.filter(i => i.selectable).length, reviewCount: source.items.filter(i => i.decision === 'needs_confirmation').length, selectedCount: source.selected.length, pendingSelectionCount: source.pending.length, completeness: source.take > 100 || state.empty ? 'visible_end' : 'partial', stopReason: source.config.provider === 'anibt' && source.take >= 100 ? 'source_limit' : source.take > 100 ? '' : 'more_available', loadedFirst: source.items.length ? 25 : null, loadedLast: source.items.length ? 25 + Math.floor((source.items.length - 1) / 2) : null, gapsWithinLoadedMatches: [{ first: 26, last: 26 }], unloadedHistory: source.take <= 100 && !state.empty } }
    function previewPage(source, number = 1) { return { items: source.items.slice((number - 1) * 50, number * 50), localStates: Object.fromEntries(source.items.slice(0,9).map((item,i)=>[item.release.id,['organized','downloading','blocked','filtered','selected','filtered','needs_confirmation','seen','dismissed'][i]])), page: number, pageSize: 50, total: source.items.length, selectedIds: source.selected, pendingSelections: source.pending, summary: summary(source) } }
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
      } else if ((endpoint.endsWith('/subscriptions') && method === 'POST') || (/\/subscriptions\/[^/]+$/.test(endpoint) && method === 'PATCH')) {
        if (state.saveMode === 'conflict') { status = 409; data = { error: '配置版本已变化，输入保留', code: 'REVISION_CONFLICT' } }
        else if (state.saveMode === 'duplicate') { status = 409; data = { error: '该篇章已有订阅配置', code: 'SUBSCRIPTION_EXISTS', details: { existingId: 'saved-subscription' } } }
        else {
          const ids = Object.fromEntries(body.config.sources.map(s => [s.ref, s.id || `source-${s.ref}`]))
          state.saved = { id: 'saved-subscription', entryId: 'entry-123', status: state.saved?.status || 'enabled', firstEnabledAt: Date.now(), revision: body.expectedRevision + 1, entry: { id: 'entry-123', bangumiId: '123', editionKey: 'original', title: card('123').title, originalTitle: '', revision: 1, metadata: { coverUrl: '', airDate: '2026-10-01' } },
            sources: body.config.sources.map(s => ({ ...s, id: ids[s.ref], revision: s.expectedRevision + 1, configuredState: s.status })), form: { schemaVersion: 1 },
            naming: body.config.naming ? {schemaVersion:1,target:structuredClone(state.namingTarget),sources:body.config.naming.sources.map(s=>({...s,sourceRef:ids[s.sourceRef]}))} : null }
          if (state.saveMode === 'unknown') { state.saveMode = ''; await route.abort(); return }
          if (state.saveMode === 'invalid') { state.saveMode = ''; data = {}; }
          else data = { id: state.saved.id, entryId: 'entry-123', revision: state.saved.revision, status: state.saved.status, sourceIds: ids, sourceRevisions: Object.fromEntries(body.config.sources.map(s => [s.ref, s.expectedRevision + 1])), requiresRepreview: false }
        }
      } else if (endpoint.endsWith('/subscriptions') && method === 'GET') { data={items:state.saved?[state.saved]:[],total:state.saved?1:0,page:1,pageSize:20} } else if (endpoint.endsWith('/jobs')) { data={items:[],total:0,page:1,pageSize:20} } else if (/\/subscriptions\/[^/]+$/.test(endpoint) && method === 'GET') {
        if (!state.saved) { status = 404; data = { code: 'NOT_FOUND', error: '配置不存在' } } else data = state.saved
      } else if (endpoint.includes('/tmdb/')) data = { items: [], results: [], total: 0 }
      else if (endpoint.includes('/emby/')) data = { configured: false, items: [] }
      await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) }).catch(() => {})
    })
    async function go(suffix = '/anime/new?bangumiId=123') { await page.goto(origin + suffix, { waitUntil: 'networkidle' }); if (role === 'admin') await page.locator(suffix.startsWith('/anime?') ? '.mikan-anime-card, .anibt-anime-card, .garden-anime-card' : '.ac-identity').first().waitFor() }
    async function source(group = '10', first = '25', last = '64') {
      await page.getByRole('button', { name: '选择资源', exact: true }).click()
      await page.getByRole('button', { name: /从零开始 第三季 字幕资源/ }).click()
      await page.getByRole('button', { name: group === '10' ? /字幕组甲/ : /字幕组乙/ }).click()
      await page.getByRole('checkbox', { name: /^1080p 简 内嵌/ }).check()
      assert.equal(await page.getByLabel('来源范围', { exact: true }).count(), 0)
      assert.equal(await page.getByLabel('发布编号与篇章集号').count(), 0)
      await page.getByRole('button', { name: '使用此来源', exact: true }).click()
      await wait(() => page.locator('dialog[open]').count().then(n => n === 1))
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
    const s = await session()
    await s.go()
    assert.equal(await s.page.locator('dialog[open]').count(), 1)
    await s.source()
    await s.page.getByLabel('发布第几话',{exact:true}).fill('25')
    await s.page.getByLabel('对应第几集',{exact:true}).fill('1')
    assert.equal(s.calls.filter(c => c.endpoint.endsWith('/subscriptions/preview')).length, 0)
    await s.page.getByRole('button', {name:'保存',exact:true}).click()
    await wait(() => s.page.locator('dialog[open]').count().then(n => n === 0), 'save should close modal')
    const saved = s.writes().find(c => c.method === 'POST' && c.endpoint === '/api/admin/anime/subscriptions')
    assert.ok(saved, 'save configuration without preview')
    assert.equal(saved.body.sources, undefined)
    assert.equal("formSelections" in saved.body.config, false)
    assert.equal("previewRefs" in saved.body.config, false)
    assert.equal(s.calls.filter(c => /\/(start|selections)$/.test(c.endpoint)).length, 0)
    passed.push('new definition saves without preview or selected download grants')

    await s.go('/anime/subscriptions/saved-subscription')
    for (const viewport of [{width:1440,height:1080},{width:390,height:844},{width:900,height:450}]) {
      await s.page.setViewportSize(viewport)
      const geometry=()=>s.page.locator('dialog[open]').evaluate(el=>{
        const rect=node=>{const {x,y,width,height}=node.getBoundingClientRect();return {x,y,width,height}}
        return {shell:rect(el),tabs:rect(el.querySelector('.ac-tabs')),footer:rect(el.querySelector('.anime-floating-footer')),bodyWidth:el.querySelector('.anime-floating-body').clientWidth}
      })
      const initial=await geometry()
      assert.ok(initial.shell.y>=0 && initial.shell.y+initial.shell.height<=viewport.height,'configuration fits viewport')
      await s.page.locator('.anime-floating-body').evaluate(el=>{el.scrollTop=el.scrollHeight})
      await wait(()=>s.page.locator('.anime-floating-body').evaluate(el=>el.scrollTop>0),'basic content scrolls internally')
      const scrollTop=await s.page.locator('.anime-floating-body').evaluate(el=>el.scrollTop)
      for (const tab of [/^备用 RSS/,/^下载记录$/,/^基本$/]) {
        await s.page.getByRole('tab',{name:tab}).click()
        const current=await geometry()
        assert.deepEqual(current,initial,`tab switch keeps shell, tabs, footer and content width stable at ${viewport.width}x${viewport.height}`)
      }
      await wait(()=>s.page.locator('.anime-floating-body').evaluate((el,top)=>Math.abs(el.scrollTop-top)<1,scrollTop),'basic tab restores its scroll position')
    }
    await s.page.setViewportSize({width:1440,height:1080})
    passed.push('desktop, phone and short-screen tab switches keep dialog geometry and restore scroll')
    const fixed=await s.page.locator('dialog[open] .anime-floating-footer').boundingBox()
    await s.page.locator('.anime-floating-body').evaluate(el=>{el.scrollTop=el.scrollHeight})
    const afterScroll=await s.page.locator('dialog[open] .anime-floating-footer').boundingBox()
    assert.equal(afterScroll.y,fixed.y,'footer stays fixed while form body scrolls')
    assert.equal(await s.page.locator('dialog[open]').evaluate(el=>getComputedStyle(el).overflow),'clip')
    assert.equal(await s.page.locator('dialog[open]').evaluate(el=>getComputedStyle(el).getPropertyValue('--scrollbar-track').trim()),'transparent')
    await s.page.getByLabel('从第几话开始').fill('7')
    s.state.previewTransform=(item,i)=>i<2?({...item,release:{...item.release,title:`[完全无修版] 与暗黑召唤师秘密恋爱中 - 0${i+1} [简 / 繁] (1080p H.264 AAC2.0+DDP2.0 SRTx2) {ダークサモナーとデキている | I'm Dating a Dark Summoner | 黑暗召唤师在地下恋爱中}`}}):item
    await s.page.getByRole('button',{name:'预览',exact:true}).click()
    await s.page.locator('.ae-preview-stats').waitFor()
    assert.equal(await s.page.locator('dialog[open]').count(), 2)
    assert.equal(await s.page.locator('.ae-preview input[type=checkbox]').count(),0)
    assert.deepEqual(await s.page.locator('.ae-preview-table thead th').allTextContents(),['规则匹配','处理状态','RSS','字幕组','原标题','重命名不含扩展名','发布时间'])
    assert.equal(await s.page.locator('.ae-preview-table tbody tr').first().locator('td').count(),7)
    const firstRow=s.page.locator('.ae-preview-table tbody tr').first()
    assert.equal(await firstRow.locator('td').nth(0).innerText(),'符合')
    assert.equal(await firstRow.locator('td').nth(1).innerText(),'已整理')
    assert.match(await firstRow.locator('time').innerText(),/2026-10-20/)
    assert.deepEqual(await s.page.locator('.ae-preview-table tbody .ae-sticky-state').allTextContents().then(values=>values.slice(3,10)),['已过滤','已选定','已过滤','需确认','待检查','已忽略','尚未处理'],'persisted decisions must not be hidden as missing download records')
    await s.page.getByRole('combobox',{name:'显示',exact:true}).click()
    await s.page.getByRole('option',{name:'有下载记录',exact:true}).click()
    assert.equal(await s.page.locator('.ae-preview-table tbody tr').count(),4,'skip/filter decisions are excluded from download records')
    await s.page.getByRole('combobox',{name:'显示',exact:true}).click()
    await s.page.getByRole('option',{name:'全部发布',exact:true}).click()
    assert.equal(await s.page.locator('.ae-preview-boundary').count(),0)
    assert.match(await firstRow.locator('.ae-release-filename').innerText(),/S02E01/)
    assert.equal(await s.page.locator('.ae-release-meta').count(),0,'default rows omit secondary metadata')
    const rowHeight=await firstRow.evaluate(el=>el.getBoundingClientRect().height)
    assert.ok(rowHeight<=100,`complete multilingual title fits a compact row: ${rowHeight}`)
    const shellGeometry=await s.page.locator('dialog[open]').last().evaluate(el=>({height:el.getBoundingClientRect().height,scroll:el.querySelector('.anime-floating-body').scrollHeight,body:el.querySelector('.anime-floating-body').clientHeight}))
    assert.ok(shellGeometry.height<=760)
    assert.ok(shellGeometry.scroll<=shellGeometry.body+1,'table owns preview scroll; outer body does not scroll')
    await s.page.getByRole('button',{name:'详细信息',exact:true}).click()
    assert.ok(await s.page.locator('.ae-release-meta').count()>0)
    await s.page.getByRole('button',{name:'详细信息',exact:true}).click()
    await s.page.getByRole('button',{name:'说明',exact:true}).click()
    await s.page.locator('.ae-preview-boundary').waitFor()
    await s.page.getByRole('button',{name:'说明',exact:true}).click()
    await s.page.screenshot({path:'test-results/anime-preview-desktop-light.png'})
    await s.page.evaluate(()=>document.documentElement.classList.add('dark'))
    await s.page.screenshot({path:'test-results/anime-preview-desktop-dark.png'})
    await s.page.evaluate(()=>document.documentElement.classList.remove('dark'))
    const displayFilter=s.page.getByRole('combobox',{name:'显示',exact:true})
    await displayFilter.click()
    await s.page.getByRole('listbox',{name:'显示',exact:true}).waitFor()
    assert.equal(await s.page.getByRole('listbox',{name:'显示',exact:true}).evaluate(el=>el.matches(':popover-open')),true,'menu is above native dialog and its clipped body')
    await s.page.screenshot({path:'test-results/anime-preview-filter-light.png'})
    await s.page.keyboard.press('Escape')
    assert.equal(await s.page.getByRole('listbox',{name:'显示',exact:true}).count(),0)
    assert.equal(await s.page.locator('dialog[open]').count(),2,'Escape closes dropdown before preview')
    await displayFilter.press('Enter')
    await displayFilter.press('End')
    assert.equal(await s.page.getByRole('listbox',{name:'显示',exact:true}).evaluate(el=>el.querySelector('.active')?.textContent.trim()),'有下载记录')
    await displayFilter.press('Home')
    await displayFilter.press('ArrowDown')
    await displayFilter.press('Enter')
    assert.equal(await displayFilter.innerText(),'符合规则','keyboard navigation commits selected filter')
    await displayFilter.click()
    await s.page.getByRole('option',{name:'符合规则',exact:true}).click()
    await s.page.locator('.ae-release-list').evaluate(el=>{el.scrollLeft=40;el.dispatchEvent(new Event('scroll'))})
    const sticky=await firstRow.locator('td').first().evaluate(el=>({left:el.getBoundingClientRect().left,list:el.closest('.ae-release-list').getBoundingClientRect().left,bg:getComputedStyle(el).backgroundColor}))
    assert.ok(Math.abs(sticky.left-sticky.list)<1,'rule column stays visible during horizontal scrolling')
    assert.notEqual(sticky.bg,'rgba(0, 0, 0, 0)','sticky status cell is opaque')
    await s.page.getByRole('button',{name:'返回配置',exact:true}).click()
    assert.equal(await s.page.getByLabel('从第几话开始').inputValue(),'7')
    assert.equal(await s.page.evaluate(()=>document.body.style.overflow),'hidden')
    assert.equal(await s.page.evaluate(()=>document.activeElement?.textContent.trim()),'预览')
    await s.page.getByRole('button',{name:'预览',exact:true}).click()
    await s.page.locator('.ae-preview-table').waitFor()
    assert.equal(await s.page.getByRole('combobox',{name:'显示',exact:true}).innerText(),'符合规则')
    await wait(()=>s.page.locator('.ae-release-list').evaluate(el=>el.scrollLeft===40),'preview table scroll is restored')
    await s.page.keyboard.press('Escape')
    await wait(()=>s.page.locator('dialog[open]').count().then(n=>n===1))
    assert.equal(await s.page.evaluate(()=>document.body.style.overflow),'hidden')
    await s.page.getByRole('button',{name:'取消',exact:true}).click()
    await s.page.getByRole('button',{name:'继续编辑',exact:true}).click()
    assert.equal(await s.page.getByLabel('从第几话开始').inputValue(),'7')
    await s.page.getByRole('tab',{name:'下载记录',exact:true}).click()
    assert.equal(await s.page.getByLabel('从第几话开始').inputValue(),'7')
    await s.page.getByRole('tab',{name:'基本',exact:true}).click()
    await s.page.getByRole('button',{name:'保存',exact:true}).click()
    await wait(()=>s.page.locator('dialog[open]').count().then(n=>n===0))
    assert.ok(s.writes().some(c=>c.method==='PATCH'&&c.endpoint.endsWith('/saved-subscription')))
    passed.push('running subscription edits, independent readonly preview, focus, scroll lock, cancel protection')

    s.state.saved.status='paused'
    await s.go('/anime/subscriptions/saved-subscription')
    assert.match(await s.page.locator('#anime-save-hint').innerText(),/暂停/)
    await s.page.getByLabel('从第几话开始').fill('3')
    await s.page.getByRole('button',{name:'保存',exact:true}).click()
    await wait(()=>s.page.locator('dialog[open]').count().then(n=>n===0))
    assert.equal(s.state.saved.status,'paused')
    assert.equal(s.calls.filter(c=>c.endpoint.endsWith('/resume')).length,0)
    passed.push('paused edit remains paused without lifecycle control calls')

    const unknown = await session()
    await unknown.go(); await unknown.source(); unknown.state.saveMode='unknown'
    await unknown.page.getByRole('button',{name:'保存',exact:true}).click()
    await unknown.page.getByRole('button',{name:'核对原保存请求',exact:true}).waitFor()
    const original = unknown.writes().at(-1)
    await unknown.page.getByRole('button',{name:'核对原保存请求',exact:true}).click()
    await wait(()=>unknown.page.locator('dialog[open]').count().then(n=>n===0))
    const retry=unknown.writes().at(-1)
    assert.equal(original.key,retry.key)
    assert.deepEqual(original.body,retry.body)
    passed.push('uncertain save retries exact idempotency key and original body')

    const mobile=await session({width:390,height:844})
    await mobile.go();await mobile.source()
    mobile.state.previewTransform=(item,i)=>({...item,release:{...item.release,title:`${item.release.title} ${'very-long-unbroken-release-name'.repeat(i===0?8:1)}`}})
    await mobile.page.getByRole('button',{name:'预览',exact:true}).click()
    await mobile.page.locator('.ae-preview-table').waitFor()
    const dims=await mobile.page.evaluate(()=>({vw:innerWidth,page:document.documentElement.scrollWidth,dialog:[...document.querySelectorAll('dialog[open]')].at(-1).getBoundingClientRect().toJSON(),list:document.querySelector('.ae-release-list').getBoundingClientRect().toJSON()}))
    assert.ok(dims.page<=dims.vw+1,'no page horizontal overflow')
    assert.ok(dims.dialog.width<=dims.vw,'preview fits phone')
    assert.equal(await mobile.page.locator('dialog[open]').last().evaluate(el=>getComputedStyle(el).overflow),'clip')
    assert.ok(await mobile.page.locator('.ae-release-list').evaluate(el=>el.scrollWidth>el.clientWidth),'names scroll inside table')
    const titleLayout=await mobile.page.locator('.ae-release-title').first().evaluate(el=>({width:el.clientWidth,height:el.clientHeight,scroll:el.scrollWidth,whiteSpace:getComputedStyle(el).whiteSpace,overflow:getComputedStyle(el).textOverflow}))
    assert.ok(titleLayout.width<=600 && titleLayout.height>40,'long full titles wrap inside a bounded column')
    assert.ok(titleLayout.scroll<=titleLayout.width+1,'unbroken title does not widen the table')
    assert.equal(titleLayout.whiteSpace,'normal')
    assert.notEqual(titleLayout.overflow,'ellipsis')
    assert.equal(await mobile.page.locator('.ae-preview-table tbody .ae-sticky-state').first().evaluate(el=>getComputedStyle(el).position),'static','phone leaves room for file names')
    await mobile.page.screenshot({path:'test-results/anime-dialog-mobile.png'})
    await mobile.page.locator('.ae-release-list').evaluate(el=>{el.scrollLeft=340;el.scrollTop=0})
    await mobile.page.screenshot({path:'test-results/anime-preview-mobile-titles.png'})
    await mobile.page.setViewportSize({width:900,height:450})
    const short=await mobile.page.locator('dialog[open]').last().evaluate(el=>({rect:el.getBoundingClientRect().toJSON(),body:el.querySelector('.anime-floating-body').clientHeight,scroll:el.querySelector('.anime-floating-body').scrollHeight,list:el.querySelector('.ae-release-list').clientHeight,vh:innerHeight}))
    assert.ok(short.rect.top>=0&&short.rect.bottom<=short.vh,'short-screen preview fits and keeps close controls in view')
    assert.ok(short.scroll<=short.body+1 && short.list>50,'short screen keeps table as the only main scroll container')
    await mobile.page.setViewportSize({width:390,height:844})
    await mobile.page.getByRole('button',{name:'返回配置',exact:true}).click()
    await mobile.page.getByRole('button',{name:'取消',exact:true}).click()
    await mobile.page.screenshot({path:'test-results/anime-dialog-confirm-light.png'})
    const light=await mobile.page.locator('.ac-confirm').evaluate(el=>({ink:getComputedStyle(el).color,bg:getComputedStyle(el).backgroundColor}))
    assert.deepEqual(light,{ink:'rgb(30, 41, 59)',bg:'rgb(255, 255, 255)'})
    assert.equal(await mobile.page.locator('.ae-target-season > .ae-label').evaluate(el=>getComputedStyle(el).color),'rgb(30, 41, 59)')
    await mobile.page.getByRole('button',{name:'继续编辑',exact:true}).click()
    assert.equal(await mobile.page.locator('input[placeholder="字幕组标题中的编号"]').evaluate(el=>getComputedStyle(el).backgroundColor),'rgb(255, 255, 255)')
    await mobile.page.evaluate(()=>document.documentElement.classList.add('dark'))
    assert.equal(await mobile.page.locator('.ae-target-season > .ae-label').evaluate(el=>getComputedStyle(el).color),'rgb(226, 232, 240)')
    assert.equal(await mobile.page.locator('input[placeholder="字幕组标题中的编号"]').evaluate(el=>getComputedStyle(el).backgroundColor),'rgb(32, 40, 50)')
    await mobile.page.getByRole('button',{name:'预览',exact:true}).click()
    await mobile.page.getByRole('combobox',{name:'显示',exact:true}).click()
    const darkMenu=await mobile.page.getByRole('listbox',{name:'显示',exact:true}).evaluate(el=>({ink:getComputedStyle(el).color,bg:getComputedStyle(el).backgroundColor}))
    assert.deepEqual(darkMenu,{ink:'rgb(226, 232, 240)',bg:'rgb(32, 40, 50)'})
    await mobile.page.screenshot({path:'test-results/anime-preview-filter-dark.png'})
    await mobile.page.keyboard.press('Escape')
    await mobile.page.getByRole('button',{name:'返回配置',exact:true}).click()
    await mobile.page.screenshot({path:'test-results/anime-dialog-config-dark.png'})
    await mobile.page.setViewportSize({width:1440,height:1000})
    await mobile.page.evaluate(()=>document.documentElement.classList.remove('dark'))
    await mobile.page.screenshot({path:'test-results/anime-dialog-desktop.png'})
    passed.push('light/dark field, label and confirmation colors remain legible')
    const clean=await session()
    await clean.go()
    await clean.page.waitForFunction(()=>document.querySelector('.ac-identity p')?.textContent.includes('TMDB'))
    await clean.page.getByRole('button',{name:'取消',exact:true}).click()
    await wait(()=>clean.page.locator('dialog[open]').count().then(n=>n===0))
    passed.push('automatic naming does not mark an untouched form as dirty')
    await s.go('/anime/subscriptions/saved-subscription')
    await s.page.getByLabel('从第几话开始').fill('9')
    s.state.saveMode='conflict'
    await s.page.getByRole('button',{name:'保存',exact:true}).click()
    await s.page.getByRole('button',{name:'重载最新配置',exact:true}).click()
    await s.page.getByRole('button',{name:'放弃修改',exact:true}).click()
    await wait(()=>s.page.getByLabel('从第几话开始').inputValue().then(v=>v==='3'))
    passed.push('conflict reload confirms discard and reads latest configuration')
    passed.push('phone modal fits viewport and long names scroll inside preview')
    const touch=await session({width:390,height:844},'admin',{isMobile:true,hasTouch:true})
    await touch.go();await touch.source()
    await touch.page.getByRole('button',{name:'预览',exact:true}).tap()
    await touch.page.locator('.ae-preview-table').waitFor()
    await touch.page.getByRole('combobox',{name:'显示',exact:true}).tap()
    await touch.page.getByRole('option',{name:'已过滤',exact:true}).tap()
    assert.equal(await touch.page.getByRole('combobox',{name:'显示',exact:true}).innerText(),'已过滤','touch selects an option without losing click activation')
    await touch.page.getByRole('combobox',{name:'来源',exact:true}).tap()
    await touch.page.getByRole('option',{name:'主 RSS · 字幕组甲',exact:true}).tap()
    assert.equal(await touch.page.getByRole('listbox').count(),0)
    passed.push('shared custom dropdown supports keyboard, Escape, top layer, dark theme and touch')
    assert.deepEqual(errors,[])
    console.log(JSON.stringify({passed,errors},null,2))
  } finally { await Promise.all(contexts.map(c=>c.close())); await browser.close() }
})().catch(error=>{console.error(error);process.exitCode=1})
