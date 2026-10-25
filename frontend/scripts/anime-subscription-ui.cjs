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
    const calls = [], state = { role, naming: true, primary: true, ready: true, empty: false, zeroMatch: false, failPreview: false, failSearch: false, expired: false, saveMode: '', settingsMode: '', loseSelection: false, conflict: false, batch: 0, batches: new Map(), saved: undefined, regularEpisodes: 12,
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
      const items = state.empty ? [] : Array.from({ length: Math.min(take, state.rowCount ?? 120) }, (_, i) => item(config.ref, i))
      const pending = previous && config.rules.minimumEpisode !== previous.config.rules.minimumEpisode ? [{ releaseId: previous.selected[0] || `${config.ref}-old`, reason: 'changed', previousItem: previous.items[0] }] : []
      const selected = previous ? previous.selected.filter(id => items.some(item => item.release.id === id && item.selectable) && !pending.some(item => item.releaseId === id)) : items.filter(item => item.selectable).map(item => item.release.id)
      return { ref: config.ref, config, id: `preview-${config.ref}-${state.batch}`, rev: 1, take, items, selected, pending, expires: Date.now() + (state.expired ? -1000 : 900000) }
    }
    function summary(source) { return { sourceRef: source.ref, sourcePreviewId: source.id, loadRevision: source.rev, generatedAt: Date.now(), expiresAt: source.expires, expired: source.expires < Date.now(), rawCount: source.items.length, loadedCount: source.items.length, matchedCount: source.items.filter(i => i.selectable).length, reviewCount: source.items.filter(i => i.decision === 'needs_confirmation').length, selectedCount: source.selected.length, pendingSelectionCount: source.pending.length, completeness: source.take > 100 || state.empty ? 'visible_end' : 'partial', stopReason: source.config.provider === 'anibt' && source.take >= 100 ? 'source_limit' : source.take > 100 ? '' : 'more_available', loadedFirst: source.items.length ? 25 : null, loadedLast: source.items.length ? 25 + Math.floor((source.items.length - 1) / 2) : null, gapsWithinLoadedMatches: [{ first: 26, last: 26 }], unloadedHistory: source.take <= 100 && !state.empty } }
    function previewPage(source) {
      const localStates = { ...Object.fromEntries(source.items.slice(0,9).map((item,i)=>[item.release.id,['organized','downloading','blocked','filtered','selected','filtered','needs_confirmation','seen','dismissed'][i]])), ...state.redownloadStates }
      const localJobs = Object.fromEntries(source.items.slice(0,3).map((item,i)=>[item.release.id,{id:`job-${item.release.id}`,revision:1,canRedownload:[0,2].includes(i) && localStates[item.release.id]!=='queued'}]))
      return { items:source.items, localStates, localJobs, total:source.items.length, selectedIds:source.selected, pendingSelections:source.pending, summary:summary(source) }
    }
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
      else if (endpoint.endsWith('/naming/preview')) {
        if (state.namingPreviewGate) await state.namingPreviewGate
        if (state.failNamingPreview) { status=503; data={error:'模拟命名读取失败'} }
        else data={naming:{revision:3,template:{kind:'tv',directories:[],filename:[]}},items:body.items.map((item,index)=>{
          const rule=body.mapping.segments[0], ep=item.parse.episode
          if(item.parse.kind!=='episode'||ep==null||ep<rule.first) return {id:item.id,status:'unmapped'}
          if(state.namingRowError&&index===0) return {id:item.id,status:'mapped',error:'文件名超过 240 字节'}
          const stem=`${body.target.title}.S${String(rule.targetSeason).padStart(2,'0')}E${String(ep-rule.sourceBase+rule.targetBase).padStart(2,'0')}-[简繁]-${body.group}`
          const directory=state.namingRoot?'':`公共目录/第 ${rule.targetSeason} 季`
          return {id:item.id,status:'mapped',episodeName:'目标集名',result:{directory,stem,name:stem,extension:'',relativePath:directory?`${directory}/${stem}`:stem}}
        })}
      }
      else if (endpoint.endsWith('/naming/search')) {
        if (state.namingGate) await state.namingGate
        if (state.failNaming) { status = 503; data = { error: 'TMDB 暂不可用' } }
        else data = { items: state.naming ? [{ id: 117061, title: '从零开始的异世界生活', date: '2016-04-04' }] : [], recommendedId: state.naming ? '117061' : '' }
      } else if (endpoint.endsWith('/naming/target')) {
        if (state.targetGate) await state.targetGate
        const season = body.season ?? 2
        data = { tmdbId: body.tmdbId, title: '从零开始的异世界生活', year: '2016', groupId: body.groupId || '', groupName: body.groupId ? '独立编排' : 'TMDB 默认', season, fingerprint: 'a'.repeat(64),
          seasons: [1, 2].map(n => ({ number: n, name: `第 ${n} 季`, date: n === 1 ? '2016-04-04' : '2026-10-01', episodeCount: 13, episodes: n === season ? Array.from({length:13}, (_,i) => ({id:String(n * 100+i+1),number:i+(body.groupId ? 9 : 1),name:`目标第 ${i+1} 集`,date:`${n===2?'2026-10':'2016-04'}-${String(i+1).padStart(2,'0')}`,sourceSeason:n,sourceNumber:i+1})) : null })) }
        state.namingTarget = structuredClone(data)
      } else if (endpoint.includes('/naming/groups/')) data = { items: [{id:'group001',name:'独立编排'}] }
      else if (endpoint.endsWith('/catalog/mikan')) data = { provider: 'mikan', days: [{ day: 1, items: [{ id:'300',title:'从零开始 第三季 字幕资源',coverUrl:'',updatedDate:'' }] }], items: [], fetchedAt:Date.now(), expiresAt:Date.now()+600000 }
      else if (/\/discovery\/subjects\/[^/]+\/episodes$/.test(endpoint)) {
        if (state.episodeGate) await state.episodeGate
        if (state.failEpisodes) { status = 502; data = {error:'模拟集数读取失败'} }
        else {
          const offset = Number(url.searchParams.get('offset') || 0), total = state.chapterCount ?? 8
          data = { total, offset, limit:100, maxItems:2000, stale:false, fetchedAt:Date.now(), expiresAt:Date.now()+600000, items:Array.from({length:Math.min(100,Math.max(total-offset,0))},(_,i)=>({id:String(1388677+offset+i),number:59+offset+i,chapterNumber:offset+i+1,title:`篇章集 ${offset+i+1}`,originalTitle:'',date:`2026-10-${String(offset+i+1).padStart(2,'0')}`})) }
        }
      } else if (endpoint.includes('/discovery/subjects/') && state.subjectFailure) {
        status = state.subjectFailure.status; data = { code: state.subjectFailure.code, error: '模拟 Bangumi 读取失败' }
      } else if (endpoint.includes('/discovery/subjects/')) {
        const id = endpoint.split('/').pop()
        data = { subject: { ...card(id), title: id === '456' ? '独立续篇' : card(id).title, synopsis: '独立的篇章资料。', episodes: state.regularEpisodes, totalEpisodes: 14, platform: 'TV' }, metadata: { expiresAt: Date.now() + 600000, stale: false } }
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
      } else if (endpoint.endsWith('/sources/mikan/300')) data = { entry: { id: '300', title: '从零开始 第三季 字幕资源', coverUrl: '/api/mikan/image/202601/poster.jpg', groups: [{ id: '10', name: '字幕组甲' }, { id: '20', name: '字幕组乙' }], bangumiIds: [state.sourceBangumiID || '123'] } }
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
        data = { source: { ...body.source, expiresAt: Date.now() + 600000 }, observedAt: Date.now(), completeness: state.sampleCompleteness || 'visible_end', specVersion: 1, profilesTruncated: false,
          profiles: specs.map((spec, i) => ({ match: [spec.resolution, ...(spec.languages || []), spec.subtitleMode || ''].filter(Boolean).map(v => '(?i)' + v), key: `spec-${i}`, label: labels[i], spec, count: 1 })),
          releases: state.empty ? [] : specs.map((spec, i) => {
            const episode = state.sampleEpisodes?.[i] ?? state.sampleEpisode ?? 25
            return { postKey: `sample-${i}`, title: `[${body.source.groupId === '10' ? '字幕组甲' : '字幕组乙'}] 从零开始的异世界生活 [${episode}] [${labels[i]}]`, parse: { kind: 'episode', episode, spec }, sizeRaw: '650 MB' }
          }) }
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
          batch.sources.set(ref, makeSource(body.config.sources.find(s => s.ref === ref), ({mikan:2000,anibt:100,garden:1000})[body.config.sources.find(s => s.ref === ref).provider], previous))
          state.batches.set(batch.id, batch); data = response(batch, ref)
        }
      } else if (endpoint.endsWith('/release-states')) {
        const source=[...state.batches.values()].reverse().flatMap(batch=>[...batch.sources.values()]).find(source=>source.items.some(item=>body.releaseIds.includes(item.release.id)))
        data=source ? previewPage(source) : {localStates:{},localJobs:{}}
      } else if (endpoint.endsWith('/redownload')) {
        if(state.redownloadGate) await state.redownloadGate
        state.redownloadReceipts ||= new Map()
        if(state.redownloadReceipts.has(call.key)) data=state.redownloadReceipts.get(call.key)
        else {
          data={id:state.saved.id,revision:state.saved.revision,results:body.items.map((item,i)=>state.redownloadMode==='all'||state.redownloadMode==='partial'&&i===1?{id:item.id,error:'模拟任务仍在处理'}:{id:item.id,jobId:`next-${item.id}`})}
          state.redownloadReceipts.set(call.key,structuredClone(data))
          state.redownloadStates ||= {}
          for(const result of data.results) if(result.jobId) state.redownloadStates[result.id.replace(/^job-/,'')]='queued'
          if(state.redownloadMode==='unknown') {state.redownloadMode='';await route.abort();return}
        }
      } else if (/\/subscriptions\/[^/]+\/(remove|resume|pause|archive)$/.test(endpoint) && method === 'POST') {
        state.actionReceipts ||= new Map()
        if (state.actionMode === 'busy') { state.actionMode = ''; status = 409; data = { code: 'TASK_BUSY', error: '任务正在处理，请稍后重试删除' } }
        else if (state.actionReceipts.has(call.key)) data = state.actionReceipts.get(call.key)
        else {
          assert.equal(body.expectedRevision, state.saved.revision)
          const action = endpoint.split('/').at(-1)
          data = { id: state.saved.id, revision: state.saved.revision + 1, status: ({ remove: 'removed', resume: 'enabled', pause: 'paused', archive: 'archived' })[action] }
          state.actionReceipts.set(call.key, data)
          if (action === 'remove') state.saved = undefined
          else Object.assign(state.saved, { status: data.status, revision: data.revision })
          if (state.actionMode === 'unknown') { state.actionMode = ''; await route.abort(); return }
        }
      } else if ((endpoint.endsWith('/subscriptions') && method === 'POST') || (/\/subscriptions\/[^/]+$/.test(endpoint) && method === 'PATCH')) {
        if (state.ruleFailure) { status = 400; data = { error: '匹配正则第 1 条语法无效，请使用 Go/RE2 支持的表达式', code: 'SOURCE_RULE_INVALID', details: { sourceRef: body.config.sources[0].ref, field: state.ruleFailure, line: 1 } } }
        else if (state.saveMode === 'conflict') { status = 409; data = { error: '配置版本已变化，输入保留', code: 'REVISION_CONFLICT' } }
        else if (state.saveMode === 'duplicate') { status = 409; data = { error: '该篇章已有订阅配置', code: 'SUBSCRIPTION_EXISTS', details: { existingId: 'saved-subscription' } } }
        else {
          const ids = Object.fromEntries(body.config.sources.map(s => [s.ref, s.id || `source-${s.ref}`]))
          const resumes = state.saved?.status === 'archived' && body.config.sources.some(source => source.runWhenEnabled && !state.saved.sources.some(prior => prior.id === source.id && prior.runWhenEnabled))
          state.saved = { id: 'saved-subscription', entryId: 'entry-123', status: resumes ? 'enabled' : state.saved?.status || 'enabled', permittedActions: ['pause', 'resume', 'archive', 'remove'].map(name => ({ name, allowed: true })), firstEnabledAt: Date.now(), revision: body.expectedRevision + 1, entry: { id: 'entry-123', bangumiId: '123', editionKey: 'original', title: card('123').title, originalTitle: '', revision: 1, metadata: { coverUrl: '', airDate: '2026-10-01', totalEpisodes: state.regularEpisodes || null } },
            sources: body.config.sources.map(s => ({ ...s, id: ids[s.ref], revision: s.expectedRevision + 1, configuredState: s.status })), form: structuredClone(body.config.form || { schemaVersion: 1 }),
            naming: body.config.naming ? {schemaVersion:1,target:structuredClone(state.namingTarget),sources:body.config.naming.sources.map(s=>({...s,sourceRef:ids[s.sourceRef]}))} : null }
          if (state.subjectFailure?.status === 404) Object.assign(state.saved.entry, { bangumiId: body.config.entry.bangumiId || null, title: '从零开始 第三季 字幕资源', metadata: { bangumiUnavailable: true, coverUrl: '/api/mikan/image/202601/poster.jpg' } })
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
    async function source(group = '10', minimum = '25') {
      const chooseSource = page.getByRole('button', { name: '选择资源', exact: true })
      if (await chooseSource.isVisible()) await chooseSource.click()
      await page.getByRole('button', { name: /从零开始 第三季 字幕资源/ }).click()
      await page.getByRole('button', { name: group === '10' ? /字幕组甲/ : /字幕组乙/ }).click()
      await page.getByRole('radio', { name: /^1080p 简 内嵌/ }).check()
      assert.equal(await page.getByLabel('来源范围', { exact: true }).count(), 0)
      assert.equal(await page.getByLabel('发布编号与篇章集号').count(), 0)
      await page.getByRole('button', { name: '使用此来源', exact: true }).click()
      await wait(() => page.locator('dialog[open]').count().then(n => n === 1))
      const form = page.locator('.anime-source-form:visible')
      await form.getByText('自定义', { exact: true }).click()
      assert.equal(await form.getByLabel('从第几话开始').inputValue(), '0')
      if (minimum !== null) await form.getByLabel('从第几话开始').fill(minimum)
      assert.equal(await form.getByLabel('订阅首话', { exact: true }).count(), 0)
    }
    async function minimum(value) {
      const input=page.getByLabel('从第几话开始')
      if (!await input.isVisible()) await page.locator('.anime-source-form:visible').getByText('自定义',{exact:true}).click()
      await input.fill(value)
    }
    async function preview() { await page.getByRole('button', { name: /^(生成预览|重新预览)$/ }).click(); await page.locator('.ae-preview-stats').waitFor(); await wait(async () => await page.locator('.ae-preview [aria-busy=true]').count() === 0 && !await page.getByRole('button', { name: '处理中…' }).count()) }
    const writes = () => calls.filter(c => c.key && c.endpoint.includes('/anime/'))
    return { page, context, state, calls, go, source, minimum, preview, writes }
  }


  try {
    for (const count of [0, null]) {
      const unknown = await session()
      unknown.state.regularEpisodes = count
      await unknown.go()
      const total = unknown.page.getByLabel('总集数', { exact: true })
      assert.equal(await total.inputValue(), '')
      assert.equal(await total.getAttribute('placeholder'), '未定', 'all-chapter count never replaces an unknown regular count')
      assert.match(await unknown.page.locator('.ac-total-note').innerText(), /集数未定/)
      await total.fill('12')
      await unknown.page.getByRole('button', { name: '清空手动总集数，恢复 Bangumi 数据', exact: true }).click()
      assert.equal(await total.getAttribute('placeholder'), '未定')
      assert.equal(unknown.calls.filter(call => call.endpoint.endsWith('/episodes')).length, 0, 'opening a form does not fetch episode lists to count chapters')
    }
    passed.push('unknown regular episode count stays undetermined despite 14 all-type chapters; manual clearing never falls back to OP/ED totals')

    const partial=await session()
    partial.state.sampleCompleteness='partial'
    await partial.go();await partial.source('10',null)
    assert.equal(await partial.page.getByRole('button',{name:'保存',exact:true}).isEnabled(),true)
    await partial.page.getByRole('button',{name:'预览',exact:true}).click()
    await partial.page.locator('.ae-preview-table').waitFor()
    await partial.page.getByRole('button',{name:'返回配置',exact:true}).click()
    assert.equal(await partial.page.getByRole('button',{name:'保存',exact:true}).isEnabled(),true)
    assert.equal(partial.writes().length,0)
    passed.push('partial feed saves and previews without any first-episode confirmation')
    const noTarget=await session()
    noTarget.state.failNaming=true
    await noTarget.go();await noTarget.source()
    assert.equal(await noTarget.page.getByLabel('集数偏移',{exact:true}).count(),0)
    await noTarget.page.getByLabel('集号正则',{exact:true}).fill('第(\\d+)话')
    assert.equal(await noTarget.page.getByLabel('从第几话开始').isVisible(),true)
    assert.equal(noTarget.writes().length,0)
    passed.push('download and parsing controls stay available when TMDB is unavailable')
    const missing = await session()
    missing.state.subjectFailure = { status: 404, code: 'UPSTREAM_NOT_FOUND' }; missing.state.sourceBangumiID = '583313'
    await missing.go('/anime/new?bangumiId=583313&source=mikan&itemId=300')
    await missing.page.locator('.ac-metadata-note').waitFor()
    assert.match(await missing.page.locator('.ac-identity h3').innerText(), /字幕资源/)
    assert.equal(await missing.page.getByText('篇章集号', { exact: true }).count(), 0)
    await wait(() => missing.page.locator('.ac-identity p').innerText().then(text => text.includes('TMDB')))
    assert.ok(missing.calls.some(c => c.endpoint.endsWith('/naming/search') && c.body.keyword === '从零开始 第三季 字幕资源' && !c.body.bangumiId))
    assert.ok(missing.calls.filter(c => c.endpoint.endsWith('/naming/target')).every(c => !c.body.bangumiId))
    const archiveTotal = missing.page.getByRole('textbox', { name: '总集数', exact: true })
    assert.equal(await archiveTotal.getAttribute('placeholder'), '未定')
    assert.equal(await archiveTotal.inputValue(), '', 'TMDB counts never silently become overrides')
    await missing.page.setViewportSize({ width: 390, height: 844 })
    const sourceBefore = await missing.page.getByRole('heading', { name: '主 RSS', exact: true }).boundingBox()
    const readsBeforeTotal = missing.calls.length
    assert.equal(await missing.page.getByRole('button', { name: /填入 TMDB/ }).count(), 0)
    await archiveTotal.fill('13')
    const sourceAfter = await missing.page.getByRole('heading', { name: '主 RSS', exact: true }).boundingBox()
    assert.ok(Math.abs(sourceBefore.y-sourceAfter.y)<1)
    assert.equal(missing.calls.length, readsBeforeTotal, 'editing totals does not request metadata')
    await missing.page.getByRole('button', { name: '清空手动总集数，恢复 Bangumi 数据', exact: true }).click()
    assert.equal(await archiveTotal.inputValue(),'')
    await missing.page.getByRole('button', { name: '选择资源', exact: true }).click()
    await missing.page.getByRole('button', { name: /字幕组甲/ }).click()
    await missing.page.getByRole('radio', { name: /^1080p 简 内嵌/ }).check()
    await missing.page.getByRole('button', { name: '使用此来源', exact: true }).click()
    await missing.page.getByLabel('集数偏移', {exact:true}).fill('-24')
    await archiveTotal.fill('0')
    await missing.page.getByRole('button', { name: '保存', exact: true }).click()
    assert.match(await missing.page.locator('.ac-feedback').innerText(), /总集数/)
    assert.equal(missing.writes().length, 0, 'invalid totals never reach the save API')
    await archiveTotal.fill('12')
    await missing.page.getByRole('button', { name: '预览', exact: true }).click()
    await missing.page.locator('.ae-preview-table').waitFor()
    assert.equal(missing.calls.find(c => c.endpoint.endsWith('/subscriptions/preview')).body.config.chapter.bangumiId, '583313')
    await missing.page.getByRole('button', { name: '返回配置', exact: true }).click()
    await missing.page.getByRole('button', { name: '保存', exact: true }).click()
    await wait(() => missing.page.locator('dialog[open]').count().then(n => n === 0))
    assert.equal(missing.writes().at(-1).body.config.entry.bangumiId, '583313')
    assert.equal(missing.writes().at(-1).body.config.form.totalEpisodes, 12)
    const externalReads = () => missing.calls.filter(c => c.endpoint.includes('/discovery/subjects/')).length
    const beforeReopen = externalReads()
    await missing.go('/anime/subscriptions/saved-subscription')
    assert.equal(externalReads(), beforeReopen)
    await missing.page.locator('.ac-metadata-note').waitFor()
    assert.equal(await archiveTotal.inputValue(), '12', 'manual total survives saving and reopening without Bangumi')
    await missing.page.getByRole('button', { name: '清空手动总集数，恢复 Bangumi 数据', exact: true }).click()
    await missing.page.getByRole('button', { name: '保存', exact: true }).click()
    await wait(() => missing.page.locator('dialog[open]').count().then(n => n === 0))
    assert.equal(missing.writes().at(-1).body.config.form.totalEpisodes, null, 'clearing explicitly removes the stored override')
    await missing.go('/anime/subscriptions/saved-subscription')
    assert.equal(await archiveTotal.inputValue(), '')
    passed.push('subscription totals are independent of TMDB, persist and clear without extra reads')
    passed.push('missing Bangumi uses verified source metadata, keyword naming, preview and save while retaining external identity; local reopen is independent')

    const unseeded = await session()
    unseeded.state.subjectFailure = { status: 404, code: 'UPSTREAM_NOT_FOUND' }; unseeded.state.sourceBangumiID = '583313'
    await unseeded.go('/anime/new?bangumiId=583313')
    assert.equal(await unseeded.page.locator('.ae-target').count(), 0)
    assert.equal(unseeded.calls.filter(c => c.endpoint.endsWith('/naming/search')).length, 0)
    await unseeded.page.getByRole('button', { name: '选择资源', exact: true }).click()
    await unseeded.page.getByRole('searchbox', { name: '搜索蜜柑资源' }).fill('从零开始')
    await unseeded.page.locator('dialog[open]').last().getByRole('button', { name: '搜索', exact: true }).click()
    assert.ok(unseeded.calls.filter(c => c.endpoint.endsWith('/sources/mikan/search')).every(c => !c.body.bangumiId))
    await unseeded.page.getByRole('button', { name: /从零开始 第三季 字幕资源/ }).click()
    await unseeded.page.getByRole('button', { name: /字幕组甲/ }).click()
    await unseeded.page.getByRole('radio', { name: /^1080p 简 内嵌/ }).check()
    await unseeded.page.getByRole('button', { name: '使用此来源', exact: true }).click()
    await wait(() => unseeded.page.locator('.ac-identity p').innerText().then(text => text.includes('TMDB')))
    assert.match(await unseeded.page.locator('.ac-identity h3').innerText(), /字幕资源/)
    passed.push('direct missing-Bangumi URL recovers through existing source picker before naming')

    const unrelated = await session()
    unrelated.state.subjectFailure = { status: 404, code: 'UPSTREAM_NOT_FOUND' }; unrelated.state.sourceBangumiID = '999999'
    await unrelated.go('/anime/new?bangumiId=583313&source=mikan&itemId=300')
    assert.doesNotMatch(await unrelated.page.locator('.ac-identity p').innerText(), /Bangumi/)
    await unrelated.page.getByRole('button', { name: '选择资源', exact: true }).click()
    await unrelated.page.getByRole('button', { name: /字幕组甲/ }).click()
    await unrelated.page.getByRole('radio', { name: /^1080p 简 内嵌/ }).check()
    await unrelated.page.getByRole('button', { name: '使用此来源', exact: true }).click()
    await unrelated.page.getByRole('button', { name: '保存', exact: true }).click()
    await wait(() => unrelated.page.locator('dialog[open]').count().then(n => n === 0))
    assert.equal(unrelated.writes().at(-1).body.config.entry.bangumiId, undefined)
    assert.equal(unrelated.calls.find(c => c.endpoint.endsWith('/source-bindings/preview')).body.entry.bangumiId, undefined)
    passed.push('unrelated source cannot claim the missing URL Bangumi identity in UI, samples or save')

    for (const failure of [{ status: 429, code: 'SOURCE_COOLDOWN' }, { status: 403, code: 'FORBIDDEN' }, { status: 404, code: 'NOT_FOUND' }]) {
      const transient = await session(); transient.state.subjectFailure = failure
      await transient.page.goto(origin + '/anime/new?bangumiId=583313&source=mikan&itemId=300', { waitUntil: 'networkidle' })
      await transient.page.locator('.ae-load-error').waitFor()
      assert.equal(await transient.page.locator('.ac-metadata-note').count(), 0)
      assert.equal(transient.calls.filter(c => c.endpoint.endsWith('/naming/search')).length, 0)
    }
    passed.push('rate limit, access denial and unrelated 404 do not activate metadata fallback')

    const regex = await session()
    await regex.go(); await regex.source()
    const match = regex.page.getByLabel('匹配正则（全部满足）', {exact:true})
    const exclude = regex.page.getByLabel('排除正则（任一命中）', {exact:true})
    const expression = '(?i)1080p\n第\\d{1,3}话'
    await match.fill(expression)
    await exclude.fill('(?i)sample\n(?i)720p')
    await regex.page.getByRole('button',{name:'更换来源',exact:true}).click()
    const picker = regex.page.locator('dialog[open]').last()
    await picker.getByRole('radio',{name:/保留当前匹配正则/}).waitFor()
    assert.equal(await picker.locator('input[type=radio]:checked').count(),1)
    for (const theme of ['light','dark']) {
      await regex.page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme)
      const geometry = await picker.locator('input[type=radio]').first().boundingBox()
      assert.equal(geometry.width,17,'radio must not inherit text input width')
      assert.equal(geometry.height,17,'radio must not inherit text input minimum height')
      await regex.page.screenshot({path:`test-results/anime-regex-picker-${theme}.png`})
    }
    await picker.getByRole('radio',{name:/全部规格/}).check()
    await picker.getByRole('button',{name:'恢复打开时的匹配规则',exact:true}).click()
    assert.equal(await picker.getByRole('radio',{name:/保留当前匹配正则/}).isChecked(),true)
    await picker.getByRole('button',{name:'更新来源规则',exact:true}).click()
    assert.equal(await match.inputValue(),expression)
    assert.equal(await exclude.inputValue(),'(?i)sample\n(?i)720p','template selection preserves exclusion rules')
    if (!await regex.page.locator('.ae-advanced').evaluate(el => el.open)) await regex.page.locator('.ae-advanced > summary').click()
    await regex.page.getByLabel('集号正则',{exact:true}).fill('第(\\d{1,3})话')
    await regex.page.getByLabel('捕获组序号',{exact:true}).fill('1')
    await regex.page.setViewportSize({width:390,height:844})
    await match.scrollIntoViewIfNeeded()
    const matchBox = await match.boundingBox(), excludeBox = await exclude.boundingBox()
    assert.equal(matchBox.x,excludeBox.x,'phone regex fields use the same column')
    assert.ok(excludeBox.y>=matchBox.y+matchBox.height,'phone exclusion field follows the match field')
    await regex.page.screenshot({path:'test-results/anime-regex-form-mobile-dark.png'})
    assert.ok(await regex.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'regex fields fit phone viewport')
    await regex.page.locator('.ae-advanced > summary').click()
    regex.state.ruleFailure='episodePattern'
    await regex.page.getByRole('button',{name:'保存',exact:true}).click()
    const episodePattern = regex.page.getByLabel('集号正则',{exact:true})
    await wait(()=>episodePattern.getAttribute('aria-invalid').then(value=>value==='true'))
    assert.equal(await regex.page.locator('.ae-advanced').evaluate(el=>el.open),true,'capture error opens custom settings')
    await episodePattern.fill('第(\\d{1,3})话')
    assert.equal(await regex.page.locator('.ae-advanced').evaluate(el=>el.open),true,'correcting an error keeps custom settings open')
    regex.state.ruleFailure='match'
    await regex.page.getByRole('button',{name:'保存',exact:true}).click()
    await wait(()=>match.getAttribute('aria-invalid').then(value=>value==='true'))
    assert.equal(await match.evaluate(el=>el===document.activeElement),true,'server rule error focuses its source field')
    await match.fill(expression)
    regex.state.ruleFailure=''
    await regex.page.getByRole('button',{name:'保存',exact:true}).click()
    await wait(()=>regex.page.locator('dialog[open]').count().then(n=>n===0))
    const rules=regex.state.saved.sources[0].rules
    assert.deepEqual(rules.match,['(?i)1080p','第\\d{1,3}话'],'commas inside quantifiers never split expressions')
    assert.deepEqual(rules.exclude,['(?i)sample','(?i)720p'])
    assert.equal(rules.episodePattern,'第(\\d{1,3})话')
    assert.equal(rules.episodeGroup,1)
    assert.equal('specMode' in rules || 'specProfiles' in rules || 'include' in rules || 'resolution' in rules,false)
    passed.push('per-RSS regex editing, comma preservation, custom capture, template restore, native radios, mobile/dark layout and server field errors')

    const s = await session()
    await s.go()
    assert.equal(await s.page.locator('dialog[open]').count(), 1)
    const episodeReads = () => s.calls.filter(call => /\/episodes(\?|$)/.test(call.endpoint + call.query)).length
    assert.equal(await episodeReads(), 0, 'Bangumi episodes load only after opening the list')
    const chapterButton = s.page.getByRole('button', { name: '查看 Bangumi 逐集列表', exact: true })
    const targetBefore = await s.page.getByRole('region', { name: 'TMDB 命名目标', exact: true }).boundingBox()
    await chapterButton.click()
    const chapterPanel = s.page.getByRole('dialog', { name: 'Bangumi 逐集列表', exact: true })
    const chapterList = chapterPanel.getByRole('list', { name: 'Bangumi 逐集信息', exact: true })
    await chapterList.waitFor()
    assert.equal(await chapterList.getByRole('listitem').count(), 8)
    assert.equal(await chapterList.getByRole('listitem').first().innerText(), '59')
    assert.match(await chapterList.getByRole('listitem').first().getAttribute('title'), /篇章集 1/)
    assert.doesNotMatch(await chapterPanel.innerText(), /S\d+E\d+|TMDB/, 'Bangumi list never infers TMDB episode mappings')
    assert.equal(await episodeReads(), 1)
    assert.equal((await s.page.getByRole('region', { name: 'TMDB 命名目标', exact: true }).boundingBox()).y, targetBefore.y)
    await s.page.screenshot({ path: 'test-results/anime-chapter-popover-desktop.png' })
    await s.page.keyboard.press('Escape')
    await chapterPanel.waitFor({ state: 'detached' })
    assert.equal(await chapterButton.evaluate(el => document.activeElement === el), true)
    assert.equal(await s.page.locator('dialog[open]').count(), 1)
    await chapterButton.click()
    await chapterList.waitFor()
    assert.equal(await episodeReads(), 1, 'reopening uses the existing cache')
    await chapterPanel.getByRole('button', { name: '关闭 Bangumi 集数', exact: true }).click()
    const longEpisodes = await session()
    longEpisodes.state.chapterCount = 220
    await longEpisodes.go()
    const longButton = longEpisodes.page.getByRole('button', { name: '查看 Bangumi 逐集列表', exact: true })
    await longButton.click()
    const longPanel = longEpisodes.page.getByRole('dialog', { name: 'Bangumi 逐集列表', exact: true })
    const longList = longPanel.getByRole('list', { name: 'Bangumi 逐集信息', exact: true })
    await wait(() => longList.getByRole('listitem').count().then(n => n === 100))
    await longPanel.locator('.ac-chapter-popover-body').evaluate(el => { el.scrollTop = el.scrollHeight; el.dispatchEvent(new Event('scroll')) })
    assert.equal(await longPanel.evaluate(el => el.matches(':popover-open')), true)
    await longPanel.getByRole('button', { name: '继续读取 100 集', exact: true }).click()
    await wait(() => longList.getByRole('listitem').count().then(n => n === 200))
    assert.equal(longEpisodes.calls.filter(call => call.endpoint.endsWith('/episodes')).length, 2)
    for (const viewport of [{ width: 320, height: 700 }, { width: 900, height: 450 }]) {
      await longEpisodes.page.setViewportSize(viewport)
      await longPanel.waitFor({ state: 'detached' })
      await longButton.click()
      await longList.waitFor()
      const rect = await longPanel.boundingBox()
      assert.ok(rect.x >= 0 && rect.y >= 0 && rect.x + rect.width <= viewport.width && rect.y + rect.height <= viewport.height)
      assert.equal(await longList.getByRole('listitem').count(), 200)
      await longEpisodes.page.screenshot({ path: `test-results/anime-chapter-popover-${viewport.width}.png` })
    }
    assert.equal(longEpisodes.calls.filter(call => call.endpoint.endsWith('/episodes')).length, 2)
    await longEpisodes.page.keyboard.press('Escape')
    passed.push('Bangumi original episode list opens on demand, caches, pages long series, restores focus and fits phone/short screens without inferred TMDB mappings')
    const knownTotal = s.page.getByRole('textbox', { name: '总集数', exact: true })
    assert.equal(await knownTotal.inputValue(), '')
    assert.equal(await knownTotal.getAttribute('placeholder'), '12', 'known Bangumi counts remain defaults rather than manual overrides')
    assert.match(await s.page.locator('.ac-total-note').innerText(), /12 集/)
    assert.equal(await s.page.getByRole('button', { name: /填入 TMDB 的/ }).count(), 0)
    for (const width of [1440, 390]) {
      await s.page.setViewportSize({ width, height: 1080 })
      await knownTotal.fill('99999')
      const space = await knownTotal.evaluate(el => {
        const style = getComputedStyle(el), button = el.parentElement.querySelector('button')
        const canvas = document.createElement('canvas').getContext('2d')
        canvas.font = `${style.fontSize} ${style.fontFamily}`
        return { padding: parseFloat(style.paddingRight), buttonWidth: button.getBoundingClientRect().width,
          available: el.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight), text: canvas.measureText(el.value).width }
      })
      assert.ok(space.padding >= space.buttonWidth, 'total reserves room for its clear button')
      assert.ok(space.available >= space.text, 'five-digit total stays fully readable')
    }
    await s.page.setViewportSize({ width: 1440, height: 1080 })
    await s.page.getByRole('button', { name: '清空手动总集数，恢复 Bangumi 数据', exact: true }).click()
    assert.equal(await knownTotal.inputValue(), '')
    assert.equal(await knownTotal.getAttribute('placeholder'), '12')
    s.state.sampleEpisodes = [25, 27, 25]
    await s.source()
    assert.equal(await s.page.locator('.ae-mapping-examples, .ac-editor .ae-source-sites').count(), 0, 'no synthetic mappings or duplicate source entry points')
    const seasonBounds = await s.page.getByRole('combobox', { name: '目标季', exact: true }).boundingBox()
    const totalBounds = await knownTotal.boundingBox()
    assert.ok(Math.abs(seasonBounds.y - totalBounds.y) < 1, 'season and total share a compact desktop row')
    assert.ok(totalBounds.height <= 36 && seasonBounds.height <= 36, 'desktop fields use compact heights')
    const rulesBeforeSwitch = await s.page.getByRole('textbox', { name: '匹配正则（全部满足）', exact: true }).inputValue()
    await s.page.getByRole('button', { name: '更换来源', exact: true }).click()
    const providerTabs = s.page.getByRole('group', { name: '选择 RSS 站点', exact: true })
    await providerTabs.getByRole('button', { name: 'AniBT', exact: true }).click()
    await s.page.getByRole('dialog', { name: '选择AniBT来源', exact: true }).waitFor()
    await providerTabs.getByRole('button', { name: 'Anime Garden', exact: true }).click()
    await s.page.getByRole('dialog', { name: '选择Anime Garden来源', exact: true }).waitFor()
    await s.page.getByRole('button', { name: '关闭来源选择器', exact: true }).click()
    assert.equal(await s.page.getByRole('textbox', { name: '匹配正则（全部满足）', exact: true }).inputValue(), rulesBeforeSwitch, 'cancelling source switching preserves the saved source rules')
    assert.equal(await s.page.getByRole('button', { name: '更换来源', exact: true }).evaluate(el => el === document.activeElement), true, 'closing switched picker restores focus to its entry point')
    assert.equal(await s.page.getByRole('button',{name:/添加分段|添加单集例外/}).count(),0)
    assert.equal(await s.page.getByLabel('普通偏移到第几话结束（可留空）',{exact:true}).count(),0)
    const targetReads=s.calls.filter(c=>c.endpoint.endsWith('/naming/target')).length
    await s.page.locator('.anime-floating-body').evaluate(el=>{el.scrollTop=0})
    await s.page.screenshot({path:'test-results/anime-no-first-episode-desktop.png'})
    await s.page.getByLabel('集数偏移',{exact:true}).fill('')
    await s.page.getByLabel('集数偏移',{exact:true}).fill('+1')
    await s.page.getByLabel('集数偏移',{exact:true}).fill('-24')
    assert.equal(s.calls.filter(c=>c.endpoint.endsWith('/naming/target')).length,targetReads,'offset edits never fetch other seasons')
    await s.page.locator('.anime-floating-body').evaluate(el=>{el.scrollTop=el.scrollHeight})
    await s.page.screenshot({path:'test-results/anime-offset-custom-desktop.png'})
    await s.page.setViewportSize({width:390,height:844})
    await s.page.evaluate(()=>document.documentElement.classList.add('dark'))
    await s.page.locator('.anime-floating-body').evaluate(el=>{el.scrollTop=el.scrollHeight})
    await s.page.screenshot({path:'test-results/anime-offset-custom-mobile-dark.png'})
    await s.page.setViewportSize({width:1440,height:1080})
    await s.page.evaluate(()=>document.documentElement.classList.remove('dark'))
    assert.equal(s.calls.filter(c => c.endpoint.endsWith('/subscriptions/preview')).length, 0)
    await s.page.getByRole('button', {name:'保存',exact:true}).click()
    await wait(() => s.page.locator('dialog[open]').count().then(n => n === 0), 'save should close modal')
    const saved = s.writes().find(c => c.method === 'POST' && c.endpoint === '/api/admin/anime/subscriptions')
    assert.ok(saved, 'save configuration without preview')
    assert.equal(saved.body.sources, undefined)
    assert.equal(saved.body.config.form.totalEpisodes, null, 'regular count is a default, not a manual override')
    assert.deepEqual(saved.body.config.naming.sources[0],{sourceRef:'main',segments:[{first:24,last:null,sourceBase:24,targetBase:0,targetSeason:2}],overrides:[]})
    assert.equal("formSelections" in saved.body.config, false)
    assert.equal("previewRefs" in saved.body.config, false)
    assert.equal(s.calls.filter(c => /\/(start|selections)$/.test(c.endpoint)).length, 0)
    passed.push('new definition saves without preview or selected download grants')

    await s.go('/anime/subscriptions/saved-subscription')
    assert.equal(await s.page.getByLabel('集数偏移',{exact:true}).inputValue(),'-24','saved offset restores without hidden mapping controls')
    assert.equal(await s.page.getByLabel('总集数', { exact: true }).getAttribute('placeholder'), '12', 'reopening uses the persisted regular count')
    for (const viewport of [{width:1440,height:1080},{width:390,height:844},{width:320,height:700},{width:900,height:450}]) {
      await s.page.setViewportSize(viewport)
      const geometry=()=>s.page.locator('dialog[open]').evaluate(el=>{
        const rect=node=>{const {x,y,width,height}=node.getBoundingClientRect();return {x,y,width,height}}
        return {shell:rect(el),tabs:rect(el.querySelector('.ac-tabs')),footer:rect(el.querySelector('.anime-floating-footer')),bodyWidth:el.querySelector('.anime-floating-body').clientWidth}
      })
      const initial=await geometry()
      assert.ok(initial.shell.y>=0 && initial.shell.y+initial.shell.height<=viewport.height,'configuration fits viewport')
      const referenceBounds = await s.page.getByRole('button', { name: '查看 Bangumi 逐集列表', exact: true }).boundingBox()
      assert.ok(referenceBounds.x >= initial.tabs.x + initial.tabs.width && referenceBounds.x + referenceBounds.width <= initial.shell.x + initial.shell.width, 'episode reference fits beside all tabs')
      await s.page.locator('.anime-floating-body').evaluate(el=>{el.scrollTop=el.scrollHeight})
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
    await s.minimum('7')
    s.state.previewTransform=(item,i)=>i<2?({...item,release:{...item.release,title:`[完全无修版] 与暗黑召唤师秘密恋爱中 - 0${i+1} [简 / 繁] (1080p H.264 AAC2.0+DDP2.0 SRTx2) {ダークサモナーとデキている | I'm Dating a Dark Summoner | 黑暗召唤师在地下恋爱中}`}}):item
    await s.page.getByRole('button',{name:'预览',exact:true}).click()
    await s.page.locator('.ae-preview-stats').waitFor()
    assert.equal(await s.page.locator('dialog[open]').count(), 2)
    assert.equal(await s.page.locator('.ae-preview-table tbody tr').count(),120,'all loaded history is visible without paging')
    assert.equal(await s.page.getByRole('button',{name:/继续加载历史|资源上一页|资源下一页/}).count(),0)
    assert.equal(s.calls.filter(c=>/\/previews\/.*\/(load|releases)$/.test(c.endpoint)).length,0,'no incremental or page requests')
    assert.equal(await s.page.locator('.ae-preview input[type=checkbox]').count(),121)
    assert.ok(await s.page.locator('.ae-preview-table tbody input[type=checkbox]').first().isDisabled(),'unsaved edits cannot redownload')
    assert.deepEqual(await s.page.locator('.ae-preview-table thead th').allTextContents(),['','规则匹配','处理状态','RSS','字幕组','原标题','重命名不含扩展名','发布时间'])
    assert.equal(await s.page.locator('.ae-preview-table tbody tr').first().locator('td').count(),8)
    const firstRow=s.page.locator('.ae-preview-table tbody tr').first()
    assert.equal(await firstRow.locator('.ae-sticky-rule').innerText(),'符合')
    assert.equal(await firstRow.locator('.ae-sticky-state').innerText(),'已整理')
    assert.match(await firstRow.locator('time').innerText(),/2026-10-20/)
    assert.deepEqual(await s.page.locator('.ae-preview-table tbody .ae-sticky-state').allTextContents().then(values=>values.slice(3,10)),['已过滤','已选定','已过滤','需确认','待检查','已忽略','尚未处理'],'persisted decisions must not be hidden as missing download records')
    await firstRow.locator('.ae-release-filename').waitFor()
    const namingCalls=s.calls.filter(c=>c.endpoint.endsWith('/naming/preview')).length
    await s.page.getByRole('combobox',{name:'显示',exact:true}).click()
    await s.page.getByRole('option',{name:'有下载记录',exact:true}).click()
    assert.equal(await s.page.locator('.ae-preview-table tbody tr').count(),4,'skip/filter decisions are excluded from download records')
    await s.page.getByRole('combobox',{name:'显示',exact:true}).click()
    await s.page.getByRole('option',{name:'全部发布',exact:true}).click()
    assert.equal(await s.page.locator('.ae-preview-boundary').count(),0)
    assert.equal(s.calls.filter(c=>c.endpoint.endsWith('/naming/preview')).length,namingCalls,'display filters do not request naming again')
    await firstRow.locator('.ae-release-filename').waitFor()
    assert.match(await firstRow.locator('.ae-release-filename').innerText(),/S02E01/)
    assert.equal(await s.page.locator('.ae-release-meta').count(),0,'default rows omit secondary metadata')
    const rssCalls=s.calls.filter(c=>/\/previews(?:\/|$)/.test(c.endpoint)).length
    s.state.failNamingPreview=true
    await s.page.getByRole('button',{name:'重新预览',exact:true}).click()
    await s.page.getByRole('button',{name:'重试命名',exact:true}).waitFor()
    assert.ok(await s.page.locator('.ae-release-original').count()>0,'naming failure preserves original rows')
    const rssAfterRefresh=s.calls.filter(c=>/\/previews(?:\/|$)/.test(c.endpoint)).length
    assert.ok(rssAfterRefresh>=rssCalls)
    s.state.failNamingPreview=false; s.state.namingRowError=true; s.state.namingRoot=true
    await s.page.getByRole('button',{name:'重试命名',exact:true}).click()
    await s.page.locator('.ae-release-renamed').nth(1).locator('.ae-release-filename').waitFor()
    assert.match(await firstRow.locator('.ae-rename-pending').innerText(),/240/,'single invalid name does not hide other rows')
    assert.equal(s.calls.filter(c=>/\/previews(?:\/|$)/.test(c.endpoint)).length,rssAfterRefresh,'naming retry does not read RSS again')
    s.state.namingRowError=false
    await s.page.getByRole('button',{name:'重新预览',exact:true}).click()
    await firstRow.locator('.ae-release-filename').waitFor()
    assert.match(await firstRow.locator('.ae-release-filename').getAttribute('title'),/^\/动漫整理\/.*S02E01/,'zero-directory template still exposes the full destination')
    passed.push('shared template preview: one batch, local filters, isolated row errors, retry without RSS or drive reads, and root destination')

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
    await s.minimum('3')
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
    await mobile.page.locator('.ae-advanced > summary').click()
    await mobile.page.locator('dialog[open] .anime-floating-body').evaluate(el=>{el.scrollTop=0})
    await mobile.page.screenshot({path:'test-results/anime-config-simple-mobile.png'})
    await mobile.page.evaluate(()=>document.documentElement.classList.add('dark'))
    await mobile.page.screenshot({path:'test-results/anime-config-simple-mobile-dark.png'})
    await mobile.page.setViewportSize({width:1440,height:1000})
    await mobile.page.evaluate(()=>document.documentElement.classList.remove('dark'))
    await mobile.page.screenshot({path:'test-results/anime-config-simple-desktop.png'})
    await mobile.page.setViewportSize({width:390,height:844})
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
    assert.equal(await mobile.page.getByLabel('集数偏移',{exact:true}).evaluate(el=>getComputedStyle(el).backgroundColor),'rgb(255, 255, 255)')
    await mobile.page.evaluate(()=>document.documentElement.classList.add('dark'))
    assert.equal(await mobile.page.locator('.ae-target-season > .ae-label').evaluate(el=>getComputedStyle(el).color),'rgb(226, 232, 240)')
    assert.equal(await mobile.page.getByLabel('集数偏移',{exact:true}).evaluate(el=>getComputedStyle(el).backgroundColor),'rgb(32, 40, 50)')
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
    const zero = await session()
    zero.state.rowCount=67;zero.state.sampleEpisode=0
    zero.state.previewTransform=(item,i)=>i===0?({...item,release:{...item.release,title:'[Group] Mushoku Tensei S2 [00][1080p]',parse:{...item.release.parse,episode:0,rawEpisode:'00'}}}):item
    await zero.go();await zero.source()
    await zero.minimum('0')
    await zero.page.getByLabel('集数偏移',{exact:true}).fill('0')
    await zero.page.getByRole('button',{name:'预览',exact:true}).click()
    await zero.page.locator('.ae-preview-table').waitFor()
    assert.equal(await zero.page.locator('.ae-preview-table tbody tr').count(),67)
    await zero.page.locator('.ae-release-filename').first().waitFor()
    assert.match(await zero.page.locator('.ae-release-filename').first().innerText(),/S02E00/,'00 names normally without TMDB episode zero')
    assert.equal(await zero.page.getByRole('combobox',{name:'显示',exact:true}).innerText(),'全部发布')
    await zero.page.getByRole('button',{name:'返回配置',exact:true}).click()
    const readsBeforeReopen=zero.calls.length
    await zero.page.getByRole('button',{name:'预览',exact:true}).click()
    await zero.page.locator('.ae-preview-table').waitFor()
    assert.equal(zero.calls.filter(call=>!call.endpoint.endsWith('/release-states')).length,readsBeforeReopen-zero.calls.slice(0,readsBeforeReopen).filter(call=>call.endpoint.endsWith('/release-states')).length,'reopening refreshes only local execution states, reusing RSS and naming')
    await zero.page.getByRole('button',{name:'返回配置',exact:true}).click()
    await zero.page.getByRole('button',{name:'保存',exact:true}).click()
    await wait(()=>zero.calls.some(c=>c.key&&c.endpoint.endsWith('/subscriptions')))
    const zeroSave=zero.calls.find(c=>c.key&&c.endpoint.endsWith('/subscriptions')).body.config
    assert.equal(zeroSave.sources[0].rules.minimumEpisode,0)
    assert.equal('chapterFirstEpisode' in zeroSave.sources[0].rules,false)
    assert.deepEqual(zeroSave.sources[0].binding.scope,{kind:'feed'})
    assert.equal(zeroSave.naming.sources[0].segments[0].first,0)
    assert.equal(zeroSave.naming.sources[0].segments[0].targetBase,0)
    passed.push('full 67-row preview, episode 00 naming and persistence, no load/page controls, reopening uses cached snapshot')
    const numbering=await session()
    await numbering.go();await numbering.source()
    await numbering.page.getByLabel('集数偏移',{exact:true}).fill('-24')
    numbering.state.previewTransform=(item,i)=> {
      const kinds=['batch','unknown','special','composite'],raw=['01-11','','12.5','59 / 09']
      if(i>=kinds.length)return item
      return {...item,selectable:false,decision:'needs_confirmation',reasonCode:'EPISODE_NUMBER_UNRESOLVED',release:{...item.release,parse:{...item.release.parse,kind:kinds[i],episode:null,rawEpisode:raw[i]}}}
    }
    await numbering.page.getByRole('button',{name:'预览',exact:true}).click()
    await numbering.page.locator('.ae-preview-table').waitFor()
    const expectedNotes=['合集资源，暂不支持自动整理','未能识别单集编号','特殊集资源，暂不支持自动整理','编号含歧义，需确认集号']
    for(const [i,note] of expectedNotes.entries()) {
      const row=numbering.page.locator('.ae-preview-table tbody tr').nth(i)
      assert.equal(await row.locator('.ae-rename-pending').innerText(),note)
      assert.equal(await row.locator('.ae-sticky-rule .ae-status').getAttribute('title'),note)
      assert.equal(await row.locator('.ae-release-filename').count(),0,'unresolved rows never fabricate a filename')
    }
    await numbering.page.locator('.ae-preview-table tbody tr').nth(4).locator('.ae-release-filename').waitFor()
    assert.match(await numbering.page.locator('.ae-preview-table tbody tr').nth(4).locator('.ae-release-filename').innerText(),/S02E03/,'recognized episodes still use the configured offset')
    await numbering.page.getByRole('button',{name:'详细信息',exact:true}).click()
    assert.deepEqual(await numbering.page.locator('.ae-release-reason').allTextContents().then(values=>values.slice(0,4)),expectedNotes)
    await numbering.page.getByRole('button',{name:'返回配置',exact:true}).click()
    assert.equal(await numbering.page.getByLabel('集数偏移',{exact:true}).inputValue(),'-24')
    assert.equal(numbering.writes().length,0,'explaining a parse issue cannot create downloads or save a subscription')
    passed.push('preview distinguishes packs, unknown, special and ambiguous numbering without losing ordinary naming or editing state')
    await s.go('/anime/subscriptions/saved-subscription')
    await s.minimum('9')
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
    const managed = await session()
    managed.state.saved = structuredClone(s.state.saved)
    managed.state.namingTarget = structuredClone(s.state.namingTarget)
    managed.state.saved.sources = [managed.state.saved.sources.find(source => source.role === 'main')]
    managed.state.saved.naming.sources = managed.state.saved.naming.sources.filter(mapping => mapping.sourceRef === managed.state.saved.sources[0].id)
    managed.state.saved.status = 'archived'
    managed.state.saved.permittedActions = ['pause', 'resume', 'archive', 'remove'].map(name => ({ name, allowed: true }))
    await managed.go('/anime/subscriptions/saved-subscription')
    const originalSource = managed.state.saved.sources[0].id
    assert.ok(await managed.page.getByRole('button', { name: '继续订阅', exact: true }).isEnabled(), 'archiving must not lock lifecycle actions')
    assert.ok(await managed.page.getByRole('button', { name: '删除订阅', exact: true }).isEnabled())
    await managed.page.screenshot({ path: 'test-results/anime-archived-manage-desktop.png' })
    await managed.page.getByLabel('总集数', { exact: true }).fill('13')
    await managed.page.getByRole('button', { name: '保存', exact: true }).click()
    await wait(() => managed.page.locator('.ac-identity').count().then(n => !n))
    assert.equal(managed.state.saved.status, 'archived', 'ordinary archived edits stay archived')
    await managed.go('/anime/subscriptions/saved-subscription')
    await managed.page.getByRole('tab', { name: /备用 RSS/ }).click()
    const writesBeforeCandidate = managed.writes().length
    await managed.page.getByRole('button', { name: '添加备用', exact: true }).click()
    await managed.page.getByRole('dialog', { name: '选择蜜柑来源', exact: true }).waitFor()
    assert.equal(await managed.page.locator('.ac-backup-row').count(), 0, 'opening backup selection never creates an empty row')
    const candidateSites = managed.page.getByRole('group', { name: '选择 RSS 站点', exact: true })
    await candidateSites.getByRole('button', { name: 'AniBT', exact: true }).click()
    await managed.page.getByRole('dialog', { name: '选择AniBT来源', exact: true }).waitFor()
    await candidateSites.getByRole('button', { name: '蜜柑', exact: true }).click()
    await managed.page.getByRole('button', { name: /从零开始 第三季 字幕资源/ }).click()
    await managed.page.getByRole('button', { name: /字幕组乙/ }).click()
    await managed.page.getByRole('radio', { name: /^1080p 简 内嵌/ }).check()
    assert.equal(await managed.page.locator('.ac-backup-row').count(), 0, 'choosing a group and template is still provisional')
    await managed.page.getByRole('button', { name: '关闭来源选择器', exact: true }).click()
    assert.equal(await managed.page.locator('.ac-backup-row').count(), 0)
    assert.equal(await managed.page.getByRole('button', { name: '继续订阅', exact: true }).isEnabled(), true, 'cancelling does not dirty the subscription')
    assert.equal(await managed.page.getByRole('button', { name: '添加备用', exact: true }).evaluate(el => document.activeElement === el), true)
    assert.equal(managed.writes().length, writesBeforeCandidate, 'cancelling sends no save or lifecycle request')
    await managed.page.getByRole('button', { name: '添加备用', exact: true }).click()
    await managed.source('20')
    assert.equal(await managed.page.locator('.ac-backup-row').count(), 1, 'Use This Source installs exactly one configured row')
    const backupSwitch = managed.page.getByRole('switch', { name: '启用此备用 RSS', exact: true })
    assert.equal(await backupSwitch.isChecked(), true)
    const writesBeforeSwitch = managed.writes().length
    await managed.page.locator('.ae-source-switch label').click()
    assert.equal(await backupSwitch.isChecked(), false)
    assert.match(await managed.page.locator('.ac-backup-row').innerText(), /停用/)
    assert.equal(managed.writes().length, writesBeforeSwitch, 'header switch only changes the unsaved form')
    await backupSwitch.focus()
    await managed.page.keyboard.press('Space')
    assert.equal(await backupSwitch.isChecked(), true, 'native switch supports keyboard control')
    await managed.page.screenshot({ path: 'test-results/anime-source-switch-desktop.png' })
    await managed.page.evaluate(() => document.documentElement.classList.add('dark'))
    assert.equal(await managed.page.locator('.ae-source-switch input + span').evaluate(el => getComputedStyle(el).backgroundColor), 'rgb(37, 99, 235)', 'dark switch keeps the project blue')
    await managed.page.setViewportSize({ width: 320, height: 700 })
    const switchTarget = await managed.page.locator('.ae-source-switch label').boundingBox()
    assert.ok(switchTarget.width >= 44 && switchTarget.height >= 44)
    await managed.page.screenshot({ path: 'test-results/anime-source-switch-mobile-dark.png' })
    await managed.page.setViewportSize({ width: 1440, height: 1080 })
    await managed.page.evaluate(() => document.documentElement.classList.remove('dark'))
    await managed.page.locator('.ac-backup-row').click()
    await managed.page.screenshot({ path: 'test-results/anime-source-brand-backup-desktop.png' })
    await managed.page.setViewportSize({ width: 320, height: 700 })
    await managed.page.screenshot({ path: 'test-results/anime-source-brand-backup-mobile.png' })
    await managed.page.setViewportSize({ width: 1440, height: 1080 })
    passed.push('Add Backup opens source selection directly and only installs the confirmed source; cancel preserves count, clean state, focus and lifecycle')
    passed.push('backup header reuses native setting switch with keyboard control, blue dark theme, touch target and no immediate write')
    await managed.page.getByRole('button', { name: '保存并继续订阅', exact: true }).click()
    await wait(() => managed.page.locator('.ac-identity').count().then(n => !n))
    assert.equal(managed.state.saved.status, 'enabled')
    assert.equal(managed.state.saved.sources[0].id, originalSource, 'adding a group keeps original source identity')
    assert.equal(managed.state.saved.sources.length, 2)
    await managed.go('/anime/subscriptions/saved-subscription')
    await managed.page.setViewportSize({ width: 390, height: 844 })
    await managed.page.getByRole('button', { name: '删除订阅', exact: true }).click()
    await managed.page.getByRole('button', { name: '取消删除', exact: true }).click()
    assert.equal(managed.calls.filter(call => call.endpoint.endsWith('/remove')).length, 0)
    await managed.page.getByRole('button', { name: '删除订阅', exact: true }).click()
    await managed.page.screenshot({ path: 'test-results/anime-delete-mobile.png' })
    assert.equal(await managed.page.getByLabel('总集数', { exact: true }).isEnabled(), false, 'delete confirmation freezes configuration')
    managed.state.actionMode = 'busy'
    await managed.page.getByRole('button', { name: '确认删除', exact: true }).click()
    await managed.page.getByText('任务正在处理，请稍后重试删除', { exact: true }).waitFor()
    assert.ok(managed.state.saved, 'busy deletion preserves subscription')
    managed.state.actionMode = 'unknown'
    await managed.page.getByRole('button', { name: '确认删除', exact: true }).click()
    await managed.page.getByRole('button', { name: '核对原操作' }).waitFor()
    const firstDelete = managed.calls.filter(call => call.endpoint.endsWith('/remove')).at(-1)
    await managed.page.getByRole('button', { name: '核对原操作' }).click()
    await wait(() => managed.page.locator('.ac-identity').count().then(n => !n))
    assert.equal(managed.calls.filter(call => call.endpoint.endsWith('/remove')).at(-1).key, firstDelete.key)
    assert.equal(managed.state.saved, undefined)
    assert.equal(await managed.page.getByRole('button', { name: '已移除', exact: true }).count(), 0)
    assert.equal(managed.calls.filter(call => /p115|\/115\//.test(call.endpoint)).length, 0)
    passed.push('archived edits, new group continuation, local delete cancellation/busy/lost receipt and no removed filter')
    const redo=await session()
    redo.state.saved=structuredClone(s.state.saved); redo.state.saved.status='archived'
    await redo.go('/anime/subscriptions/saved-subscription')
    await redo.page.getByRole('button',{name:'预览',exact:true}).click()
    const choices=redo.page.locator('.ae-preview-table tbody input[type=checkbox]')
    await choices.first().waitFor()
    assert.ok(await choices.first().isEnabled(),'completed resource can be redownloaded from an archived subscription')
    assert.ok(await choices.nth(1).isDisabled(),'downloading resource is not selectable')
    await choices.first().check()
    await redo.page.getByRole('combobox',{name:'显示',exact:true}).click()
    await redo.page.getByRole('option',{name:'有下载记录',exact:true}).click()
    assert.equal(await redo.page.locator('.ae-preview-table tbody input:checked').count(),0,'changing filters clears hidden selections')
    await redo.page.getByRole('checkbox',{name:'选择当前显示的可重新下载资源',exact:true}).check()
    assert.equal(await redo.page.locator('.ae-preview-table tbody input:checked').count(),2,'select all includes only eligible visible resources')
    await redo.page.screenshot({path:'test-results/anime-redownload-selected.png'})
    let releaseRedo
    redo.state.redownloadMode='all'; redo.state.redownloadGate=new Promise(resolve=>{releaseRedo=resolve})
    await redo.page.getByRole('button',{name:'重新下载（2）',exact:true}).click()
    await redo.page.getByRole('button',{name:'安排中…',exact:true}).waitFor()
    assert.ok(await redo.page.getByRole('button',{name:'返回配置',exact:true}).isDisabled(),'cannot close while scheduling is in flight')
    releaseRedo(); redo.state.redownloadGate=undefined
    await redo.page.getByText('未能安排重新下载，2 条未提交',{exact:true}).waitFor()
    assert.equal(await redo.page.locator('.ae-preview-table tbody input:checked').count(),2,'fully rejected batch preserves the selection')
    redo.state.redownloadMode='partial'
    await redo.page.getByRole('button',{name:'重新下载（2）',exact:true}).click()
    await redo.page.getByText('已安排 1 条重新下载，1 条未提交',{exact:true}).waitFor()
    assert.equal(await redo.page.locator('.ae-preview-table tbody input:checked').count(),1,'only rejected resources stay selected')
    assert.equal(await redo.page.locator('.ae-sticky-state').filter({hasText:'待提交'}).count(),1,'accepted resource updates in place')
    assert.equal(redo.state.saved.status,'archived','one-shot redownload does not resume RSS')
    const firstRedo=redo.calls.find(call=>call.endpoint.endsWith('/redownload'))
    assert.equal(firstRedo.body.items.length,2)
    redo.state.redownloadMode='unknown'
    await redo.page.getByRole('button',{name:'重新下载（1）',exact:true}).click()
    await redo.page.getByRole('button',{name:'核对原请求',exact:true}).waitFor()
    const unknownRedo=redo.calls.filter(call=>call.endpoint.endsWith('/redownload')).at(-1)
    await redo.page.getByRole('button',{name:'返回配置',exact:true}).click()
    await redo.page.getByRole('button',{name:'预览',exact:true}).click()
    await redo.page.getByRole('button',{name:'核对原请求',exact:true}).click()
    await redo.page.getByText('已安排 1 条重新下载',{exact:true}).waitFor()
    const replayRedo=redo.calls.filter(call=>call.endpoint.endsWith('/redownload')).at(-1)
    assert.equal(replayRedo.key,unknownRedo.key,'lost response and reopen preserve the idempotency key')
    assert.deepEqual(replayRedo.body,unknownRedo.body,'replay does not substitute newer state')
    assert.equal(await redo.page.locator('.ae-preview-table tbody input:checked').count(),0)
    assert.equal(redo.calls.filter(call=>/p115|\/115\//.test(call.endpoint)).length,0,'selection and local refresh never query 115')
    passed.push('RSS redownload: archived one-shot, eligible visible selection, partial failure, in-place progress and lost-receipt replay after reopening')
    assert.deepEqual(errors,[])
    console.log(JSON.stringify({passed,errors},null,2))
  } finally { await Promise.all(contexts.map(c=>c.close())); await browser.close() }
})().catch(error=>{console.error(error);process.exitCode=1})
