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
const target = { profileId: 'download', accountId: 'fixed-main', targetCID: '99', targetPath: '/fixed' }
const selection = { snapshot: () => target }
function mock(t, handler) { const original = globalThis.fetch; globalThis.fetch = handler; t.after(() => { globalThis.fetch = original }) }

test('normalizes hex/base32 magnets and rejects unrelated URLs', () => {
  assert.equal(magnetKey('magnet:?xt=urn:btih:ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'), '00443214c74254b635cf84653a56d7c675be77df')
  assert.equal(magnetKey('https://example.com/?xt=urn:btih:' + 'a'.repeat(40)), '')
  assert.equal(magnetKey('magnet:?xt=urn:btih:' + 'A'.repeat(40) + '&dn=简体'), magnetKey('magnet:?xt=urn:btih:' + 'a'.repeat(40) + '&dn=繁体'))
  assert.equal(magnetKey('magnet:?xt=urn:btih:' + 'a'.repeat(40) + '&dn=中文 [HEVC] 文件'), 'a'.repeat(40))
})

test('selection deduplicates resources and includes rows beyond one upstream batch', () => {
  const b = useCloudDownloadBatch('test', new AbortController().signal, selection)
  b.toggle(resource(1)); b.toggle({ ...resource(1), id: 'same-magnet' })
  assert.equal(b.count.value, 0)
  b.selectAll(Array.from({ length: 110 }, (_, n) => resource(n + 1)))
  assert.equal(b.count.value, 110)
  b.selectAll([resource(1), resource(2)])
  assert.equal(b.count.value, 108)
  b.clear(); assert.equal(b.count.value, 0)
})

test('partial batch receipts remove accepted/uncertain rows and preserve definite failures', async t => {
  let posts = 0
  mock(t, async (url, init) => {
    assert.equal(url, '/api/admin/p115/cloud-download/tasks/batch')
    posts++
    const body = JSON.parse(init.body)
    assert.deepEqual(body, { profileId: 'download', accountId: 'fixed-main', targetCID: '99', urls: [1, 2, 3].map(index => resource(index).magnet) })
    return response([event('ready'), event('start'), event('start', 1), event('start', 2), event('result', 0, { accepted: true }), event('result', 1, { error: '拒绝' }), event('result', 2, { error: '中断', outcomeUncertain: true }), event('done', 0, { stopped: true })])
  })
  const b = useCloudDownloadBatch('test', new AbortController().signal, selection)
  b.selectAll([resource(1), resource(2), resource(3)])
  await Promise.all([b.submit(), b.submit()])
  assert.equal(posts, 1); assert.equal(b.accepted.value, 1); assert.equal(b.failed.value, 1); assert.equal(b.uncertain.value, 1)
  assert.deepEqual([...b.selected.value.values()].map(r => r.id), ['2'])
  assert.equal(b.eligible(resource(3)), false)
  b.allowReselectAfterCheck(); assert.equal(b.eligible(resource(3)), true)
})

test('truncated/buffered streams never allow unacknowledged links to retry automatically', async t => {
  mock(t, async () => response([event('ready'), event('start'), event('start', 1), event('result', 0, { accepted: true })]))
  const b = useCloudDownloadBatch('test', new AbortController().signal, selection)
  b.selectAll([resource(1), resource(2), resource(3)]); await b.submit()
  assert.equal(b.accepted.value, 1); assert.equal(b.uncertain.value, 2); assert.equal(b.count.value, 0)
  assert.match(b.notice.value, /已提交 1 条/)
  assert.equal(b.running.value, false)
})

test('explicit stopped receipt preserves resources the server has not started', async t => {
  mock(t, async () => response([event('ready'), event('start'), event('result', 0, { outcomeUncertain: true, error: '中断' }), event('done', 0, { stopped: true })]))
  const b = useCloudDownloadBatch('test', new AbortController().signal, selection)
  b.selectAll([resource(1), resource(2), resource(3)]); await b.submit()
  assert.equal(b.uncertain.value, 1); assert.equal(b.count.value, 2)
  assert.deepEqual([...b.selected.value.values()].map(r => r.id), ['2', '3'])
})

test('未选择目录或目录过期时保留批量选择，明确拒绝也保持可重试', async t => {
  let posts = 0, selected, changeMessage = ''
  mock(t, async () => {
    posts++
    return posts === 1
      ? Response.json({ code: 'cloud_target_changed', error: '目录已变更' }, { status: 409 })
      : Response.json({ error: 'invalid' }, { status: 400 })
  })
  const b = useCloudDownloadBatch('test', new AbortController().signal, {
    snapshot: () => selected,
    targets: { value: [] },
    reconfirmMessage: { value: '' },
    missingTargetMessage: () => '请先新增下载目录',
    handleTargetChanged: (_snapshot, message) => { selected = undefined; changeMessage = message },
  })
  b.toggle(resource(1)); await b.submit()
  assert.equal(posts, 0); assert.equal(b.count.value, 1); assert.equal(b.needsConfig.value, true)
  selected = target
  await b.submit()
  assert.equal(changeMessage, '目录已变更')
  assert.equal(b.count.value, 1)
  assert.equal(b.uncertain.value, 0)
  selected = { ...target, targetCID: '100' }
  await b.submit(); assert.equal(b.count.value, 1); assert.equal(b.uncertain.value, 0)
})

test('gateway rejection without a receipt is uncertain rather than safe to retry', async t => {
  mock(t, async () => new Response('gateway timeout', { status: 504 }))
  const b = useCloudDownloadBatch('test', new AbortController().signal, selection)
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

test('545 条逐个回执跨数据块完整读取，不受其他页面的选择条数策略限制', async () => {
  const total = 545, events = [event('ready', 0, {}, total)]
  for (let index = 0; index < total; index++) events.push(event('start', index, {}, total), event('result', index, { accepted: true }, total))
  events.push(event('done', 0, {}, total))
  const bytes = new TextEncoder().encode(events.map(value => JSON.stringify(value)).join('\n') + '\n')
  const stream = new ReadableStream({ start(controller) {
    for (let offset = 0; offset < bytes.length; offset += 137) controller.enqueue(bytes.slice(offset, offset + 137))
    controller.close()
  } })
  const received = []
  await readCloudBatch(new Response(stream, { headers: { 'Content-Type': 'application/x-ndjson' } }), value => received.push(value))
  assert.equal(received.length, 1092)
  assert.equal(received.every(value => value.total === 545), true)
  assert.deepEqual(received.filter(value => value.type === 'start').map(value => value.index), Array.from({ length: total }, (_, index) => index))
  assert.equal(received.filter(value => value.type === 'result' && value.accepted).length, total)
  assert.equal(received.at(-1).type, 'done')
})

test('总数仍须为正安全整数，非法事件类型及非整数文件编号继续拒绝', async () => {
  for (const total of [0, -1, 1.5, Number.MAX_SAFE_INTEGER + 1, '545', null]) {
    let received = 0
    await assert.rejects(readCloudBatch(response([event('ready', 0, {}, total)]), () => received++), /回执格式异常/)
    assert.equal(received, 0)
  }
  for (const value of [event('other', 0, {}, 545), event('start', 1.5, {}, 545), event('result', '1', {}, 545)]) {
    await assert.rejects(readCloudBatch(response([value]), () => {}), /回执格式异常/)
  }
  await readCloudBatch(response([event('done', 0, {}, Number.MAX_SAFE_INTEGER)]), () => {})
})

test('413 结构化错误明确标记接收前拒绝，网关超时保持接收不确定', async () => {
  await assert.rejects(readCloudBatch(Response.json({ error: '请求体过大' }, { status: 413 }), () => assert.fail('拒绝时不得分发文件回执')), error => {
    assert.equal(error.beforeStart, true)
    assert.match(error.message, /请求体过大/)
    return true
  })
  await assert.rejects(readCloudBatch(new Response('gateway timeout', { status: 504 }), () => {}), error => {
    assert.equal(error.beforeStart, false)
    return true
  })
})

test('stop aborts network work and marks unknown receipts for review', async t => {
  let entered
  const started = new Promise(resolve => { entered = resolve })
  mock(t, async (_url, init) => {
    entered()
    return new Promise((_, reject) => init.signal.addEventListener('abort', () => reject(init.signal.reason), { once: true }))
  })
  const b = useCloudDownloadBatch('test', new AbortController().signal, selection)
  b.toggle(resource(1)); const task = b.submit(); await started; b.stop(); await task
  assert.equal(b.running.value, false); assert.equal(b.uncertain.value, 1); assert.equal(b.count.value, 0)
  assert.match(b.notice.value, /已提交 0 条/)
})
