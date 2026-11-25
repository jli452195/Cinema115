import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as vue from 'vue'

function loadModule(relativePath, dependencies = {}) {
  const module = { exports: {} }
  const code = ts.transpileModule(readFileSync(new URL(relativePath, import.meta.url), 'utf8'), {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
  }).outputText
  new Function('module', 'exports', 'require', code)(module, module.exports, name => {
    assert.ok(name in dependencies, `未模拟的依赖 ${name}`)
    return dependencies[name]
  })
  return module.exports
}
const sources = loadModule('../src/utils/telegramSources.ts')
const files = loadModule('../src/utils/telegramFiles.ts')
const { telegramShareCards, telegramShareIdentities } = loadModule('../src/utils/telegramShares.ts', { './shareLink': loadModule('../src/utils/shareLink.ts') })
const { TelegramSearchError } = loadModule('../src/types/telegramUser.ts')
const canonical = sources.canonicalTelegramSourceURL
const page = (sourceId = 'source') => ({ sourceId, pageTitle: '测试作品', groups: [{ id: 'version', title: '1080p WEB-DL', links: [{ type: 'ed2k', url: 'ed2k://|file|Show.S01E01.mkv|1000|0123456789abcdef0123456789abcdef|/' }] }], cached: true })
const resource = (id, url = `https://telegra.ph/${id}`) => ({
  id, title: '相同片名 TMDB 123', text: '', links: [], sourceUrl: `https://t.me/test/${id}`,
  postedAt: '2026-11-17T01:00:00Z', channel: { id: 'channel', title: '测试频道' },
  sources: [{ id: `source-${id}`, type: 'telegraph', url, label: '文件清单' }],
})
async function flush() {
  for (let step = 0; step < 5; step++) { await Promise.resolve(); await vue.nextTick() }
}
function harness(initial = [], settings = {}) {
  const resources = vue.ref(initial), enabled = vue.ref(settings.enabled ?? true)
  const lifecycle = new AbortController(), requests = [], expired = []
  const composable = loadModule('../src/composables/useTelegramChannelSources.ts', {
    vue, '../utils/telegramSources': sources, '../utils/telegramFiles': files,
    '../types/telegramUser': {
      TelegramSearchError,
      telegramUserRequest: (token, path, signal, body) => new Promise((resolve, reject) => {
        requests.push({ token, path, signal, body, resolve, reject })
      }),
    },
  })
  const state = composable.useTelegramChannelSources({
    token: 'test-token', signal: lifecycle.signal, kind: 'tv', tmdbId: '123',
    resources: () => resources.value, enabled: () => enabled.value, onExpired: error => expired.push(error),
  })
  return { state, resources, enabled, lifecycle, requests, expired }
}

test('清单仅按规范化官方 URL 合并，跨页保留真实引用，不按相同片名或 TMDB 合并', () => {
  const a = resource('a', 'http://Telegra.ph/Page-01?utm=x#files')
  const b = resource('b', 'https://telegra.ph/Page-01?utm=y')
  const c = resource('c', 'https://telegra.ph/Other-01')
  const candidates = sources.telegramSourceCandidates([a, a, b, c])
  assert.deepEqual(candidates.map(candidate => [candidate.url, candidate.references.length]), [
    ['https://telegra.ph/Page-01', 2], ['https://telegra.ph/Other-01', 1],
  ])
  assert.equal(candidates[0].item, a)
  assert.deepEqual(candidates[0].references.map(reference => reference.item.id), ['a', 'b'])
  assert.equal(canonical('https://telegra.ph/%50age-01#part'), canonical(a.sources[0].url))
  assert.equal(canonical('https://example.com/Page-01'), undefined)
  assert.equal(canonical('https://telegra.ph@evil.test/Page-01'), undefined)
  assert.equal(canonical('https://telegra.ph/a/b'), undefined)
  assert.equal(canonical('not a url'), undefined)
})

test('115 直链与清单中的相同分享合并，保留原 STRM 身份、版本和原帖来源', () => {
  const a = resource('a', 'https://telegra.ph/Drama-01')
  const b = resource('b', 'http://telegra.ph/Drama-01#copy')
  const direct = 'https://115.com/s/shared?password=abcd'
  a.links = [{ type: '115', url: direct }]
  const candidates = sources.telegramSourceCandidates([a, b])
  const cards = telegramShareCards([a, b], candidates, {
    [candidates[0].url]: { ...page(), shareGroups: [
      { id: 'bluray', title: '蓝光版', links: [
        { type: '115', url: 'https://115cdn.com/s/shared?password=abcd&', metadata: { specs: ['1080p'] } },
        { type: '115', url: 'https://115cdn.com/s/other?password=efgh' },
        { type: '115', url: 'https://115cdn.com.bad.test/s/wrong' },
        { type: 'ed2k', url: page().groups[0].links[0].url },
      ] },
    ] },
  })
  assert.equal(cards.length, 2)
  assert.equal(telegramShareIdentities(cards[0].links[0], 'tv', '123')[0], `tg-channel/a/${direct}`)
  assert.equal(cards[0].links[0].url, direct)
  assert.deepEqual(cards[0].links[0].versions, ['蓝光版'])
  assert.deepEqual(cards[0].links[0].sources.map(source => source.url), [a.sourceUrl, candidates[0].url, b.sourceUrl])
  assert.deepEqual(cards[0].links[0].metadata.specs, ['1080p'])
  assert.equal(cards[1].title, '蓝光版')
  assert.equal(cards[1].links.length, 1)
})

test('有明确提取码的清单分享补足无密码直链，保留不同明确密码', () => {
  const a = resource('a')
  const bare = 'https://115.com/s/shared'
  a.links = [{ type: '115', url: bare }]
  const candidates = sources.telegramSourceCandidates([a])
  const url = candidates[0].url
  const linkFor = password => ({ type: '115', url: `https://115cdn.com/s/shared?password=${password}` })
  const first = linkFor('abcd'), second = linkFor('efgh')
  const group = { id: 'file', title: '剧集分享', links: [first] }
  const resolved = { ...page(), groups: [], shareGroups: [group] }
  const one = telegramShareCards([a], candidates, { [url]: resolved }).flatMap(card => card.links)
  assert.equal(one.length, 1)
  assert.equal(one[0].url, first.url)
  assert.deepEqual(telegramShareIdentities(one[0], 'tv', '123'), [`tg-channel/a/${bare}`, `tg-channel/tv/123/telegraph/${first.url}`])
  const many = telegramShareCards([a], candidates, { [url]: { ...resolved, shareGroups: [{ ...group, links: [first, second] }] } }).flatMap(card => card.links)
  assert.deepEqual(many.map(item => item.url), [first.url, second.url])
  assert.equal(new Set(many.map(item => item.id)).size, 2)
  assert.deepEqual(many.map(item => telegramShareIdentities(item, 'tv', '123')), [[`tg-channel/tv/123/telegraph/${first.url}`], [`tg-channel/tv/123/telegraph/${second.url}`]])
})

test('分页补入同一 Telegraph 分享的直链时，任务恢复优先原帖键并保留同作品的清单键', () => {
  const source = resource('source')
  const candidates = sources.telegramSourceCandidates([source])
  const canonical = 'https://115cdn.com/s/shared?password=abcd'
  const first = 'https://115.com/s/shared?password=abcd', second = 'https://anxia.com/s/shared?password=abcd'
  const pages = { [candidates[0].url]: { ...page(), shareGroups: [{ id: 'share', title: '蓝光', links: [{ type: '115', url: canonical }] }] } }
  const [before] = telegramShareCards([source], candidates, pages).flatMap(card => card.links)
  const a = { ...resource('a'), links: [{ type: '115', url: first }] }
  const b = { ...resource('b'), links: [{ type: '115', url: second }] }
  const [after] = telegramShareCards([source, a, b], candidates, pages).flatMap(card => card.links)
  const [telegraph] = telegramShareIdentities(before, 'tv', '123')
  assert.equal(after.id, before.id)
  assert.deepEqual(telegramShareIdentities(after, 'tv', '123'), [`tg-channel/a/${first}`, `tg-channel/b/${second}`, telegraph])
  const [whileLoading] = telegramShareCards([source, a, b], candidates, {}).flatMap(card => card.links)
  assert.deepEqual(telegramShareIdentities(whileLoading, 'tv', '123'), telegramShareIdentities(after, 'tv', '123'))
  assert.notEqual(telegramShareIdentities(after, 'movie', '123').at(-1), telegraph)
})

test('纯分享清单使用同一读取结果，不会进入 ED2K 文件列表', async t => {
  const a = resource('a')
  const context = harness([a])
  t.after(() => context.lifecycle.abort())
  await flush()
  const link = { type: '115', url: 'https://115cdn.com/s/shared?password=abcd' }
  context.requests[0].resolve({ ...page(), groups: [], shareGroups: [{ id: 'share', title: '4K 版本', links: [link] }] })
  await flush()
  assert.equal(context.state.progress.value.loaded, 1)
  assert.equal(files.telegramFiles(context.state.resolvedItems.value).length, 0)
  assert.equal(telegramShareCards(context.resources.value, context.state.candidates.value, context.state.pages.value)[0].links[0].url, link.url)
  assert.equal(context.requests.length, 1)
})

test('自动泵维持两路并发，先完成一路立即补下一页，同页跨帖只请求一次', async t => {
  const context = harness([resource('a'), resource('b'), resource('c'), resource('d'), resource('alias', 'http://telegra.ph/a?copy=1')])
  t.after(() => context.lifecycle.abort())
  await flush()
  assert.equal(context.requests.length, 2)
  assert.deepEqual(context.state.progress.value, { total: 4, loaded: 0, failed: 0, loading: 2, pending: 2, references: 5 })
  assert.deepEqual(context.requests[0].body, { kind: 'tv', tmdbId: '123', resourceId: 'a', sourceId: 'source-a' })
  assert.equal(context.requests[0].path, 'channels/resolve')
  assert.equal(context.requests[0].token, 'test-token')
  assert.equal('url' in context.requests[0].body, false)
  context.requests[0].resolve(page())
  await flush()
  assert.equal(context.requests.length, 3, '第二路未完成时也应补足空闲并发')
  assert.equal(context.state.progress.value.loading, 2)
  context.requests[2].resolve(page())
  await flush()
  assert.equal(context.requests.length, 4)
  context.requests[1].resolve(page()); context.requests[3].resolve(page())
  await flush()
  assert.deepEqual(context.state.progress.value, { total: 4, loaded: 4, failed: 0, loading: 0, pending: 0, references: 5 })
  await context.state.resolve(context.state.candidates.value[0])
  assert.equal(context.requests.length, 4)
})

test('enabled 与暂停只停止新自动读取，进行中的请求可完成，恢复后继续补位', async t => {
  const context = harness([resource('a'), resource('b'), resource('c'), resource('d')], { enabled: false })
  t.after(() => context.lifecycle.abort())
  await flush()
  assert.equal(context.requests.length, 0)
  context.enabled.value = true
  await flush()
  assert.equal(context.requests.length, 2)
  context.state.pause()
  context.requests[0].resolve(page()); context.requests[1].resolve(page())
  await flush()
  assert.equal(context.requests.length, 2)
  assert.equal(context.state.progress.value.loaded, 2)
  context.state.resume()
  context.enabled.value = false
  await flush()
  assert.equal(context.requests.length, 2)
  context.enabled.value = true
  await flush()
  assert.equal(context.requests.length, 4)
})

test('失败不会自动重试或因分页重复读取，显式重试仍受两路并发限制', async t => {
  const context = harness([resource('a'), resource('b'), resource('c')])
  t.after(() => context.lifecycle.abort())
  await flush()
  context.requests[0].reject(new Error('页面暂不可用'))
  context.requests[1].resolve(page())
  await flush()
  assert.equal(context.requests.length, 3)
  context.requests[2].reject(new Error('解析失败'))
  await flush()
  context.resources.value = [...context.resources.value, resource('alias', 'http://telegra.ph/a#copy')]
  await flush()
  assert.equal(context.requests.length, 3)
  assert.equal(context.state.progress.value.failed, 2)
  context.state.pause(); context.enabled.value = false
  const retry = context.state.retryFailed()
  await flush()
  assert.equal(context.requests.length, 3, '暂停或自动开关关闭时，显式队列也不得开始')
  context.state.resume()
  await flush()
  assert.equal(context.requests.length, 3, '切换到其他标签后不能消费重试队列')
  context.enabled.value = true
  await flush()
  assert.equal(context.requests.length, 5)
  assert.equal(context.state.progress.value.loading, 2)
  context.requests[3].resolve(page()); context.requests[4].resolve(page())
  await retry
  assert.equal(context.state.progress.value.loaded, 3)
  assert.equal(context.state.progress.value.failed, 0)
})

test('手动读取同一页共享请求，暂停时队列等待恢复且不突破两路并发', async t => {
  const context = harness([resource('a'), resource('b'), resource('c')])
  t.after(() => context.lifecycle.abort())
  context.state.pause()
  const pending = context.state.candidates.value.map(context.state.resolve)
  const duplicate = context.state.resolve(context.state.candidates.value[0])
  assert.equal(duplicate, pending[0])
  await flush()
  assert.equal(context.requests.length, 0)
  context.state.resume()
  await flush()
  assert.equal(context.requests.length, 2)
  context.requests[1].resolve(page())
  await flush()
  assert.equal(context.requests.length, 3)
  context.requests[0].resolve(page()); context.requests[2].resolve(page())
  await Promise.all(pending)
})

test('进行中的显式批次暂停后仅允许已开始请求完成，后续队列等待恢复', async t => {
  const context = harness([resource('a'), resource('b'), resource('c'), resource('d')])
  t.after(() => context.lifecycle.abort())
  context.state.pause()
  const pending = context.state.candidates.value.map(context.state.resolve)
  context.state.resume()
  await flush()
  assert.equal(context.requests.length, 2)
  context.state.pause()
  context.requests[0].resolve(page()); context.requests[1].resolve(page())
  await flush()
  assert.equal(context.requests.length, 2, '完成一路不得绕过暂停继续消费显式队列')
  assert.equal(context.state.progress.value.loaded, 2)
  context.state.resume()
  await flush()
  assert.equal(context.requests.length, 4)
  context.requests[2].resolve(page()); context.requests[3].resolve(page())
  await Promise.all(pending)
})

test('分页新增同页来源无需重读，大清单每个版本仅展开一次，保留全部原帖来源', async t => {
  const context = harness([resource('first', 'http://telegra.ph/Large-01?x=1')])
  t.after(() => context.lifecycle.abort())
  await flush()
  const large = page()
  large.groups[0].links = Array.from({ length: 420 }, () => large.groups[0].links[0])
  large.groups.push({ id: 'version2', title: '2160p WEB-DL', links: large.groups[0].links })
  context.requests[0].resolve(large)
  await flush()
  context.resources.value = [context.resources.value[0], ...Array.from({ length: 19 }, (_, index) => resource(`copy-${index}`, `https://telegra.ph/Large-01?copy=${index}#files`))]
  await flush()
  assert.equal(context.requests.length, 1)
  assert.equal(context.state.candidates.value.length, 1)
  assert.equal(context.state.progress.value.references, 20)
  assert.equal(context.state.resolvedItems.value.length, 2)
  assert.equal(context.state.resolvedItems.value[0].links.length, 420)
  assert.equal(context.state.resolvedItems.value[0].fileSources.length, 20)
  assert.deepEqual(context.state.resolvedItems.value[0].fileSources.map(source => source.url), context.resources.value.map(item => item.sourceUrl))
  const unique = files.telegramFiles(context.state.resolvedItems.value)
  assert.equal(unique.length, 1)
  assert.equal(unique[0].sources.length, 20)
})

test('reset 取消旧请求且隔离相同 URL 的迟到回执，不抢先读取替换前旧列表', async t => {
  const context = harness([resource('old', 'https://telegra.ph/Same-01'), resource('old-pending')])
  t.after(() => context.lifecycle.abort())
  await flush()
  const old = context.requests[0]
  context.state.pause()
  context.state.reset()
  context.resources.value = [resource('new', 'http://telegra.ph/Same-01?new=1')]
  await flush()
  assert.equal(old.signal.aborted, true)
  assert.equal(context.requests.length, 3)
  assert.equal(context.requests[2].body.resourceId, 'new')
  assert.equal(context.state.paused.value, false)
  old.resolve({ ...page(), pageTitle: '旧回执' })
  context.requests[1].reject(new Error('旧错误'))
  await flush()
  assert.equal(context.state.pages.value['https://telegra.ph/Same-01'], undefined)
  assert.equal(context.state.statuses.value['https://telegra.ph/Same-01'].busy, true)
  context.requests[2].resolve({ ...page(), pageTitle: '新回执' })
  await flush()
  assert.equal(context.state.pages.value['https://telegra.ph/Same-01'].pageTitle, '新回执')
})

test('搜索开始 cancelPending 隔离旧回执并保留已加载页和失败，搜索结束后恢复待处理页', async t => {
  const context = harness([resource('loaded'), resource('failed'), resource('pending'), resource('queued')])
  t.after(() => context.lifecycle.abort())
  await flush()
  context.requests[0].resolve(page())
  context.requests[1].reject(new Error('清单不可用'))
  await flush()
  assert.equal(context.requests.length, 4)
  const oldPending = context.requests[2], oldQueued = context.requests[3]
  context.enabled.value = false
  context.state.cancelPending()
  assert.equal(oldPending.signal.aborted, true)
  assert.equal(oldQueued.signal.aborted, true)
  assert.equal(context.state.progress.value.loaded, 1)
  assert.equal(context.state.progress.value.failed, 1)
  assert.equal(context.state.progress.value.loading, 0)
  assert.equal(context.state.progress.value.pending, 2)
  oldPending.reject(new TelegramSearchError('旧上下文过期', 'channel_search_expired'))
  oldQueued.resolve({ ...page(), pageTitle: '旧回执' })
  await flush()
  assert.equal(context.expired.length, 0, '旧请求不得在新搜索等待期间污染过期状态')
  assert.equal(context.state.pages.value['https://telegra.ph/queued'], undefined)
  assert.equal(context.requests.length, 4, '搜索尚未结束不得恢复读取')
  assert.equal(context.state.statuses.value['https://telegra.ph/failed'].error, '清单不可用')
  context.enabled.value = true
  await flush()
  assert.equal(context.requests.length, 6)
  assert.deepEqual(context.requests.slice(4).map(request => request.body.resourceId), ['pending', 'queued'])
  context.requests[4].resolve(page()); context.requests[5].resolve(page())
  await flush()
  assert.equal(context.state.progress.value.loaded, 3)
  assert.equal(context.state.progress.value.failed, 1)
})

test('生命周期取消后回执、错误回调及后续自动调度均不得迟到更新', async () => {
  const context = harness([resource('a'), resource('b'), resource('c')])
  await flush()
  context.lifecycle.abort()
  const beforePages = JSON.stringify(context.state.pages.value), beforeStatuses = JSON.stringify(context.state.statuses.value)
  context.requests[0].resolve(page())
  context.requests[1].reject(new TelegramSearchError('缓存过期', 'channel_search_expired'))
  await flush()
  assert.equal(context.requests.every(request => request.signal.aborted), true)
  assert.equal(JSON.stringify(context.state.pages.value), beforePages)
  assert.equal(JSON.stringify(context.state.statuses.value), beforeStatuses)
  assert.equal(context.expired.length, 0)
  assert.equal(context.requests.length, 2)
})

test('搜索上下文过期只回调当前请求，重新挂载重新 resolve 以复用后端缓存恢复文件', async t => {
  const first = harness([resource('a')])
  t.after(() => first.lifecycle.abort())
  await flush()
  first.requests[0].reject(new TelegramSearchError('缓存过期', 'channel_search_expired'))
  await flush()
  assert.equal(first.expired.length, 1)
  assert.equal(first.state.progress.value.failed, 1)
  first.lifecycle.abort()
  const second = harness([resource('a')])
  t.after(() => second.lifecycle.abort())
  await flush()
  assert.equal(second.requests.length, 1, '重新挂载不能只剩空清单占位')
  second.requests[0].resolve(page())
  await flush()
  assert.equal(second.state.pages.value['https://telegra.ph/a'].cached, true)
  assert.equal(second.state.resolvedItems.value.length, 1)
  assert.equal(second.state.progress.value.loaded, 1)
})
