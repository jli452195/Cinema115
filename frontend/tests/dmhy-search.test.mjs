import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import ts from 'typescript'
const require = createRequire(import.meta.url)
function loadModule(file, imports = {}) {
  const module = { exports: {} }
  const code = ts.transpileModule(readFileSync(new URL(`../src/${file}.ts`, import.meta.url), 'utf8'), { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
  new Function('module', 'exports', 'require', code)(module, module.exports, name => imports[name] || require(name))
  return module.exports
}
const batchUtils = loadModule('lib/cloud-download-batch')
const { searchDmhyResources, readDmhyMagnet } = loadModule('lib/dmhy-search', { './cloud-download-batch': batchUtils })
const specs = loadModule('utils/resourceSpecFilters')
const { ref } = require('vue')
const row = (id, categoryScope = 'film') => ({ id, categoryScope, title: `资源 ${id}`, resolution: '1080P', magnet: '', url: `https://share.dmhy.org/topics/view/${id}_.html` })
const input = { query: '幽游白书', fallback: 'Yu Yu Hakusho', group: '', page: 1, refresh: false }
const result = (request, items = [row('1')]) => ({ query: request.query, items, groups: [], page: request.page, hasMore: request.page === 1, attempts: [] })

test('DMHY API sends independent request and retains all scopes, including unknown and no-magnet rows', async t => {
  const calls = []
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    calls.push({ url, options })
    return Response.json({ ...result(input, [row('1'), row('2', 'supplement'), row('3', 'other'), row('4', 'future-category')]), specs: { 1: { resolution: '1080p' } } })
  })
  const data = await searchDmhyResources('admin', input, new AbortController().signal)
  assert.equal(calls[0].url, '/api/admin/dmhy/search')
  assert.deepEqual(JSON.parse(calls[0].options.body), input)
  assert.equal(calls[0].options.headers.Authorization, 'Bearer admin')
  assert.deepEqual(data.items.map(item => item.categoryScope), ['film', 'supplement', 'other', 'supplement'])
  assert.equal(data.items[0].resolution, '1080P')
  assert.equal(data.items[3].resolution, '未知')
  assert.equal(data.items[0].magnet, '')
})

test('DMHY API rejects authorization failures and malformed pagination', async t => {
  for (const [status, expected] of [[401, /登录已过期/], [403, /仅管理员/], [502, /来源暂时失败/]]) {
    t.mock.method(globalThis, 'fetch', async () => Response.json({ error: '来源暂时失败' }, { status }))
    await assert.rejects(searchDmhyResources('admin', input, new AbortController().signal), expected)
    t.mock.restoreAll()
  }
  t.mock.method(globalThis, 'fetch', async () => Response.json({ ...result(input), page: 2 }))
  await assert.rejects(searchDmhyResources('admin', input, new AbortController().signal), /结果无法读取/)
})

test('DMHY reads only a requested post magnet and rejects wrong identities', async t => {
  const calls = [], magnet = 'magnet:?xt=urn:btih:' + 'a'.repeat(40)
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    calls.push({ url, options })
    return Response.json({ id: '160765', magnet: magnet.toUpperCase().replace('MAGNET', 'magnet').replace('XT=URN:BTIH:', 'xt=urn:btih:') })
  })
  assert.equal(await readDmhyMagnet('admin', '160765', new AbortController().signal), magnet)
  assert.equal(calls[0].url, '/api/admin/dmhy/magnet')
  assert.deepEqual(JSON.parse(calls[0].options.body), { id: '160765' })
  assert.equal(calls[0].options.headers.Authorization, 'Bearer admin')
  await assert.rejects(readDmhyMagnet('admin', 'https://evil.test', new AbortController().signal), /编号无效/)
  assert.equal(calls.length, 1)
  for (const data of [{ id: '1', magnet }, { id: '160765', magnet: '' }, { id: '160765', magnet: magnet + '&xt=urn:btih:' + 'b'.repeat(40) }]) {
    t.mock.method(globalThis, 'fetch', async () => Response.json(data))
    await assert.rejects(readDmhyMagnet('admin', '160765', new AbortController().signal), /无法核验/)
  }
})

test('DMHY local scope filters never discard loaded rows; append deduplicates and retains scopes and filters', async () => {
  const calls = [], positions = [], fail = { value: false }
  const { useAnimeResourceSearch } = loadModule('composables/useAnimeResourceSearch', {
    '../lib/garden-search': { searchGardenResources: () => { throw new Error('不应调用 Garden') } },
    '../lib/dmhy-search': { searchDmhyResources: async (_, request) => {
      calls.push(request)
      if (fail.value) throw new Error('模拟来源失败')
      return { ...result(request, request.page === 1 ? [row('1'), row('2', 'supplement'), row('3', 'other')] : [row('1'), row('4')]), groups: [{ id: '7', name: '字幕组' }] }
    } },
    '../lib/anime-resource-api': {}, '../utils/resourceSpecFilters': specs,
  })
  const state = useAnimeResourceSearch({ token: 'admin', title: input.query, originalTitle: input.fallback, provider: 'dmhy' }, new AbortController().signal, { batchMode: ref(false), blocked: () => false, resetResultPosition: async () => {}, appendedResults: async previous => positions.push(previous) })
  await state.search()
  assert.equal(state.categoryScope.value, 'film')
  assert.deepEqual(state.filtered.value.map(item => item.id), ['1'])
  assert.deepEqual(state.categoryOptions.value.map(option => option.count), [1, 1, 3])
  assert.equal(calls[0].fallback, input.fallback)
  state.categoryScope.value = 'supplement'
  assert.deepEqual(state.filtered.value.map(item => item.id), ['2'])
  state.categoryScope.value = 'all'
  assert.equal(state.filtered.value.length, 3)
  state.filter.value = '资源 4'
  assert.equal(state.filtered.value.length, 0)
  assert.equal(state.result.value.hasMore, true)
  await state.searchGarden(false, '', 2)
  assert.equal(calls[1].page, 2)
  assert.equal(calls[1].fallback, '')
  assert.deepEqual(state.result.value.items.map(item => item.id), ['1', '2', '3', '4'])
  assert.deepEqual(state.filtered.value.map(item => item.id), ['4'])
  assert.equal(state.categoryScope.value, 'all')
  assert.deepEqual(positions, [0])
  fail.value = true
  await state.searchGarden(false, '7')
  assert.equal(state.group.value, '')
  assert.equal(state.result.value.items.length, 4)
  assert.match(state.error.value, /模拟来源失败/)
  fail.value = false
  state.query.value = '手动别名'
  await state.searchGarden(true, '7')
  assert.deepEqual(calls.at(-1), { query: '手动别名', fallback: '', group: '7', page: 1, refresh: true })
  assert.match(state.siteURL.value, /team_id=7/)
  const previous = state.result.value
  state.updateMagnet('1', 'magnet:?xt=urn:btih:' + 'a'.repeat(40))
  assert.notEqual(state.result.value, previous)
  assert.notEqual(state.result.value.items[0], previous.items[0])
  assert.equal(state.result.value.items[0].magnet, 'magnet:?xt=urn:btih:' + 'a'.repeat(40))
  assert.equal(previous.items[0].magnet, '')
  await state.searchGarden(false, '7', 2)
  assert.equal(state.result.value.items[0].magnet, 'magnet:?xt=urn:btih:' + 'a'.repeat(40))
  assert.deepEqual(state.result.value.items.map(item => item.id), ['1', '2', '3', '4'])
  await state.searchGarden(true, '7')
  assert.equal(state.result.value.items[0].magnet, '')
})

test('DMHY manual magnet resolution shares preparation lock, skips conflicts and ignores cancelled results', async () => {
  let release, calls = 0
  const updates = [], lifecycle = new AbortController(), item = { ...row('5'), magnetIssue: 'missing' }
  const { useAnimeResourceActions } = loadModule('composables/useAnimeResourceActions', {
    '../lib/cloud-download-batch': batchUtils,
    '../lib/dmhy-search': { readDmhyMagnet: async () => { calls++; return new Promise(resolve => { release = resolve }) } },
    '../lib/anime-resource-api': { loadAnimeOrganizeFiles: () => { throw new Error('不应读取种子') } },
    './useCloudDownloadAction': { useCloudDownloadAction: () => ({ busy: ref(false), submit: () => { throw new Error('不应提交115') } }) },
  })
  const batch = { running: ref(false), selected: ref(new Map()), count: ref(0), statuses: ref({}), eligible: row => !!batchUtils.magnetKey(row.magnet) }
  const options = { token: 'admin', provider: 'dmhy', searching: () => false, filtered: () => [item], groups: () => [], updateMagnet: (...values) => updates.push(values) }
  const action = useAnimeResourceActions(options, lifecycle.signal, batch)
  const event = { currentTarget: { isConnected: true, closest: () => undefined } }
  assert.equal(action.canReadMagnet({ ...item, magnetIssue: 'conflict' }), false)
  const pending = action.readMagnet(item, event)
  assert.equal(action.busy.value, true)
  assert.equal(action.readingMagnet.value, '5')
  await action.readMagnet(item, event)
  assert.equal(calls, 1)
  release('magnet:?xt=urn:btih:' + 'a'.repeat(40)); await pending
  assert.deepEqual(updates, [['5', 'magnet:?xt=urn:btih:' + 'a'.repeat(40)]])
  assert.equal(action.busy.value, false)
  const cancelled = action.readMagnet({ ...item, id: '6', magnetIssue: 'invalid' }, event)
  lifecycle.abort(); release('magnet:?xt=urn:btih:' + 'b'.repeat(40)); await cancelled
  assert.equal(updates.length, 1)
  assert.equal(Object.keys(action.prepareNotices.value).length, 0)
})
