import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import ts from 'typescript'

const require = createRequire(import.meta.url)
const sonner = await import('vue-sonner')
const modules = new Map()
function load(file) {
  if (modules.has(file)) return modules.get(file).exports
  const module = { exports: {} }
  modules.set(file, module)
  const code = ts.transpileModule(readFileSync(file, 'utf8'), { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
  new Function('require', 'module', 'exports', code)(id => id.startsWith('.') ? load(path.resolve(path.dirname(file), id + '.ts')) : id === 'vue-sonner' ? sonner : require(id), module, module.exports)
  return module.exports
}
const source = name => load(fileURLToPath(new URL(`../src/${name}.ts`, import.meta.url)))
const { useCloudDownloadAction } = source('composables/useCloudDownloadAction')
const { useAnimeResourceActions } = source('composables/useAnimeResourceActions')
const { useMediaResourceActions } = source('composables/useMediaResourceActions')
const { useCloudDownloadBatch } = source('composables/useCloudDownloadBatch')
const { magnetKey } = source('lib/cloud-download-batch')
const magnet = 'magnet:?xt=urn:btih:' + 'a'.repeat(40)
const alias = 'magnet:?xt=urn:btih:' + 'A'.repeat(40) + '&dn=另一个标题'
const config = { accountId: 'fixed-main', targetCID: '99', targetPath: '/fixed' }
const deferred = () => {
  let resolve
  const promise = new Promise(yes => { resolve = yes })
  return { promise, resolve }
}
const flush = () => new Promise(resolve => setImmediate(resolve))
const mockFetch = (t, handler) => t.mock.method(globalThis, 'fetch', handler)

test('同磁力只提交一次，提交期间改配置不改变本次目标或回执', async t => {
  let settings = { ...config }, reads = 0
  const receipt = deferred(), posts = []
  mockFetch(t, async (url, init) => {
    if (url.endsWith('/config')) { reads++; return Response.json(settings) }
    posts.push({ accountId: new URL(url, 'http://test').searchParams.get('accountId'), ...JSON.parse(init.body) })
    return posts.length === 1 ? receipt.promise : Response.json({ accepted: true })
  })
  const action = useCloudDownloadAction('test', new AbortController().signal)
  const first = action.submit(magnet)
  await flush()
  assert.equal(action.busy.value, true)
  assert.equal(action.pending.value[magnetKey(magnet)], true)
  await action.submit(alias)
  assert.equal(posts.length, 1)
  settings = { accountId: 'new-main', targetCID: '100', targetPath: '/new' }
  receipt.resolve(Response.json({ accepted: true }))
  await first
  assert.equal(reads, 1)
  assert.deepEqual(posts[0], { accountId: config.accountId, targetCID: config.targetCID, url: magnet })
  assert.match(action.statuses.value[magnetKey(magnet)].message, /\/fixed/)
  assert.equal(action.busy.value, false)
  await action.submit(alias)
  assert.equal(posts.length, 1)
  await action.submit(magnet.replace('a'.repeat(40), 'b'.repeat(40)))
  assert.equal(reads, 2)
  assert.equal(posts[1].accountId, 'new-main')
  assert.equal(posts[1].targetCID, '100')
})

for (const failure of ['structured', 'network']) {
  test(`${failure} 未知回执阻止原磁力和别名自动重发`, async t => {
    let reads = 0, posts = 0
    mockFetch(t, async url => {
      if (url.endsWith('/config')) { reads++; return Response.json(config) }
      posts++
      if (failure === 'network') throw new TypeError('连接中断')
      return Response.json({ error: '115 接收结果未知，请核对', outcomeUncertain: true }, { status: 504 })
    })
    const action = useCloudDownloadAction('test', new AbortController().signal)
    const status = await action.submit(magnet)
    assert.equal(status.uncertain, true)
    assert.equal(action.busy.value, false)
    await action.submit(magnet); await action.submit(alias)
    assert.equal(reads, 1)
    assert.equal(posts, 1)
  })
}

test('配置缺失不提交，可在配置修复后重新读取目标', async t => {
  let configured = false, posts = 0
  mockFetch(t, async url => {
    if (url.endsWith('/config')) return Response.json({ ...config, accountId: configured ? config.accountId : '' })
    posts++; return Response.json({ accepted: true })
  })
  const action = useCloudDownloadAction('test', new AbortController().signal)
  const status = await action.submit(magnet)
  assert.equal(status.needsConfig, true)
  assert.equal(status.uncertain, false)
  assert.equal(posts, 0)
  configured = true
  assert.equal((await action.submit(magnet)).success, true)
  assert.equal(posts, 1)
})

test('卸载前取消、读取配置中取消以及已提交后取消都不写迟到成功', async t => {
  for (const phase of ['before', 'config', 'submitted']) {
    await t.test(phase, async t => {
      const lifecycle = new AbortController(), waiting = deferred()
      let reads = 0, posts = 0
      mockFetch(t, async url => {
        if (url.endsWith('/config')) {
          reads++
          return phase === 'config' ? waiting.promise : Response.json(config)
        }
        posts++; return waiting.promise
      })
      const action = useCloudDownloadAction('test', lifecycle.signal)
      if (phase === 'before') lifecycle.abort()
      const request = action.submit(magnet)
      await flush()
      lifecycle.abort()
      waiting.resolve(Response.json(phase === 'config' ? config : { accepted: true }))
      assert.equal(await request, undefined)
      assert.equal(posts, phase === 'submitted' ? 1 : 0)
      assert.equal(reads, phase === 'before' ? 0 : 1)
      assert.equal(action.busy.value, false)
      assert.equal(action.statuses.value[magnetKey(magnet)], undefined)
      await action.submit(alias)
      assert.equal(posts, phase === 'submitted' ? 1 : 0)
    })
  }
})

test('动漫单条未知结果同步批量状态并移除选择，核对后才允许再选', async t => {
  let uncertain = true, posts = 0
  mockFetch(t, async url => {
    if (url.endsWith('/config')) return Response.json(config)
    posts++
    return uncertain ? Response.json({ error: '结果未知', outcomeUncertain: true }, { status: 504 }) : Response.json({ accepted: true })
  })
  const lifecycle = new AbortController()
  const batch = useCloudDownloadBatch('test', lifecycle.signal)
  const item = { id: 'one', title: '动漫', magnet }
  const actions = useAnimeResourceActions({ token: 'test', title: '动漫', searching: () => false, filtered: () => [item], groups: () => [] }, lifecycle.signal, batch)
  batch.toggle(item)
  await actions.submit(item)
  const key = magnetKey(magnet)
  assert.equal(batch.count.value, 0)
  assert.equal(batch.statuses.value[key].uncertain, true)
  assert.equal(actions.canSelect(item), false)
  await actions.submit({ ...item, magnet: alias })
  assert.equal(posts, 1)
  actions.allowReselectAfterCheck()
  batch.toggle(item)
  assert.equal(batch.count.value, 1)
  uncertain = false
  await actions.submit(item)
  assert.equal(batch.count.value, 0)
  assert.equal(actions.submission(item).success, true)
  assert.equal(actions.canSelect(item), false)
  assert.equal(posts, 2)
})

test('媒体搜索复用同磁力状态，结构化未知回执也会拦截整理和再次下载', async t => {
  let posts = 0
  mockFetch(t, async url => {
    if (url.endsWith('/config')) return Response.json(config)
    posts++; return Response.json({ error: '后台结果未知', outcomeUncertain: true }, { status: 504 })
  })
  const lifecycle = new AbortController()
  const actions = useMediaResourceActions({ token: 'test', source: 'gying', kind: 'tv', tmdbId: '1' }, lifecycle.signal, () => false)
  const item = { id: 'one' }, link = { type: 'magnet', category: 'torrent', url: magnet }
  await actions.cloudDownload(item, link)
  const key = magnetKey(magnet)
  assert.equal(actions.cloudStatuses.value[key].uncertain, true)
  assert.equal(!!actions.downloadBlocked(item, link), true)
  assert.equal(actions.cloudSubmitting.value[key], undefined)
  await actions.cloudDownload({ id: 'other' }, { ...link, url: alias })
  assert.equal(posts, 1)
})
