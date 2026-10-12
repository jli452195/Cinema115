import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import ts from 'typescript'

const require = createRequire(import.meta.url)
function load(file) {
  const module = { exports: {} }
  const code = ts.transpileModule(readFileSync(file, 'utf8'), { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
  new Function('require', 'module', 'exports', code)(id => id.startsWith('.') ? load(path.resolve(path.dirname(file), id + '.ts')) : require(id), module, module.exports)
  return module.exports
}
const { useCloudDownloadBatch } = load(fileURLToPath(new URL('../src/composables/useCloudDownloadBatch.ts', import.meta.url)))
const { magnetKey, readCloudBatch } = load(fileURLToPath(new URL('../src/lib/cloud-download-batch.ts', import.meta.url)))
const resource = n => ({ id: String(n), title: '资源' + n, magnet: 'magnet:?xt=urn:btih:' + n.toString(16).padStart(40, '0') })
const event = (type, index = 0, extra = {}, total = 3) => ({ type, index, total, ...extra })
const response = events => new Response(events.map(e => JSON.stringify(e)).join('\n') + '\n', { headers: { 'Content-Type': 'application/x-ndjson' } })
const config = { accountId: 'fixed-main', targetCID: '99', targetPath: '/fixed' }
function mock(t, handler) { const original = globalThis.fetch; globalThis.fetch = handler; t.after(() => { globalThis.fetch = original }) }

test('normalizes hex/base32 magnets and rejects unrelated URLs', () => {
  assert.equal(magnetKey('magnet:?xt=urn:btih:ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'), '00443214c74254b635cf84653a56d7c675be77df')
  assert.equal(magnetKey('https://example.com/?xt=urn:btih:' + 'a'.repeat(40)), '')
  assert.equal(magnetKey('magnet:?xt=urn:btih:' + 'A'.repeat(40) + '&dn=简体'), magnetKey('magnet:?xt=urn:btih:' + 'a'.repeat(40) + '&dn=繁体'))
  assert.equal(magnetKey('magnet:?xt=urn:btih:' + 'a'.repeat(40) + '&dn=中文 [HEVC] 文件'), 'a'.repeat(40))
})

test('selection deduplicates resources, includes hidden rows and caps batches', () => {
  const b = useCloudDownloadBatch('test', new AbortController().signal)
  b.toggle(resource(1)); b.toggle({ ...resource(1), id: 'same-magnet' })
  assert.equal(b.count.value, 0)
  b.selectAll(Array.from({ length: 110 }, (_, n) => resource(n + 1)))
  assert.equal(b.count.value, 100)
  b.selectAll([resource(1), resource(2)])
  assert.equal(b.count.value, 98)
  b.clear(); assert.equal(b.count.value, 0)
})

test('partial batch receipts remove accepted/uncertain rows and preserve definite failures', async t => {
  let posts = 0
  mock(t, async (url, init) => {
    if (url.endsWith('/config')) return Response.json(config)
    posts++
    const body = JSON.parse(init.body)
    assert.equal(body.targetCID, '99'); assert.equal(new URL(url, 'http://test').searchParams.get('accountId'), 'fixed-main')
    return response([event('ready'), event('start'), event('result', 0, { accepted: true }), event('start', 1), event('result', 1, { error: '拒绝' }), event('start', 2), event('result', 2, { error: '中断', outcomeUncertain: true }), event('done', 0, { stopped: true })])
  })
  const b = useCloudDownloadBatch('test', new AbortController().signal)
  b.selectAll([resource(1), resource(2), resource(3)])
  await Promise.all([b.submit(), b.submit()])
  assert.equal(posts, 1); assert.equal(b.accepted.value, 1); assert.equal(b.failed.value, 1); assert.equal(b.uncertain.value, 1)
  assert.deepEqual([...b.selected.value.values()].map(r => r.id), ['2'])
  assert.equal(b.eligible(resource(3)), false)
  b.allowReselectAfterCheck(); assert.equal(b.eligible(resource(3)), true)
})

test('truncated/buffered streams never allow unacknowledged links to retry automatically', async t => {
  mock(t, async url => url.endsWith('/config') ? Response.json(config) : response([event('ready'), event('start'), event('result', 0, { accepted: true })]))
  const b = useCloudDownloadBatch('test', new AbortController().signal)
  b.selectAll([resource(1), resource(2), resource(3)]); await b.submit()
  assert.equal(b.accepted.value, 1); assert.equal(b.uncertain.value, 2); assert.equal(b.count.value, 0)
  assert.equal(b.running.value, false)
})

test('explicit stopped receipt preserves resources the server has not started', async t => {
  mock(t, async url => url.endsWith('/config') ? Response.json(config) : response([event('ready'), event('start'), event('result', 0, { outcomeUncertain: true, error: '中断' }), event('done', 0, { stopped: true })]))
  const b = useCloudDownloadBatch('test', new AbortController().signal)
  b.selectAll([resource(1), resource(2), resource(3)]); await b.submit()
  assert.equal(b.uncertain.value, 1); assert.equal(b.count.value, 2)
  assert.deepEqual([...b.selected.value.values()].map(r => r.id), ['2', '3'])
})

test('invalid config or validation failure preserves selection without POST or uncertain status', async t => {
  let posts = 0
  mock(t, async url => { if (url.endsWith('/config')) return Response.json({ ...config, accountId: '' }); posts++; return Response.json({ error: 'invalid' }, { status: 400 }) })
  const b = useCloudDownloadBatch('test', new AbortController().signal)
  b.toggle(resource(1)); await b.submit()
  assert.equal(posts, 0); assert.equal(b.count.value, 1); assert.equal(b.needsConfig.value, true)
  globalThis.fetch = async url => url.endsWith('/config') ? Response.json(config) : Response.json({ error: 'invalid' }, { status: 400 })
  await b.submit(); assert.equal(b.count.value, 1); assert.equal(b.uncertain.value, 0)
})

test('gateway rejection without a receipt is uncertain rather than safe to retry', async t => {
  mock(t, async url => url.endsWith('/config') ? Response.json(config) : new Response('gateway timeout', { status: 504 }))
  const b = useCloudDownloadBatch('test', new AbortController().signal)
  b.toggle(resource(1)); await b.submit()
  assert.equal(b.count.value, 0); assert.equal(b.uncheckedCount.value, 1)
})

test('handles split UTF-8 chunks and rejects a stream without the final receipt', async () => {
  const bytes = new TextEncoder().encode(JSON.stringify({type: 'done', total: 1, error: '中文提示'}) + '\n')
  const stream = new ReadableStream({ start(c) { for (let i = 0; i < bytes.length; i += 3) c.enqueue(bytes.slice(i, i + 3)); c.close() } })
  let count = 0
  await readCloudBatch(new Response(stream, { headers: { 'Content-Type': 'application/x-ndjson' } }), () => count++)
  assert.equal(count, 1)
  await assert.rejects(readCloudBatch(response([event('ready')]), () => {}), /连接中断/)
})

test('stop aborts network work and marks unknown receipts for review', async t => {
  let entered
  const started = new Promise(resolve => { entered = resolve })
  mock(t, async (url, init) => {
    if (url.endsWith('/config')) return Response.json(config)
    entered()
    return new Promise((_, reject) => init.signal.addEventListener('abort', () => reject(init.signal.reason), { once: true }))
  })
  const b = useCloudDownloadBatch('test', new AbortController().signal)
  b.toggle(resource(1)); const task = b.submit(); await started; b.stop(); await task
  assert.equal(b.running.value, false); assert.equal(b.uncertain.value, 1); assert.equal(b.count.value, 0)
})
