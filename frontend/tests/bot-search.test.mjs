import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

const source = await readFile(new URL('../src/types/telegramUser.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const { telegramUserSearchStream, telegramUserRequest, TelegramSearchError } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
const result = { text: '资源列表', items: [], links: ['https://115cdn.com/s/test'], empty: false }
const event = (name, value) => `event:${name}\ndata:${JSON.stringify(value)}\n\n`

test('stream reports queue, partial results and completion across fragmented UTF-8', async t => {
  const events = event('status', { stage: 'queued', position: 2 }) + event('status', { stage: 'collecting' }) + event('result', { complete: false, result }) + event('result', { complete: true, result })
  const bytes = new TextEncoder().encode(events)
  let canceled = false
  t.mock.method(globalThis, 'fetch', async () => new Response(new ReadableStream({
    start(controller) { for (let i = 0; i < bytes.length; i += 7) controller.enqueue(bytes.slice(i, i + 7)) },
    cancel() { canceled = true },
  })))
  const progress = [], updates = []
  await telegramUserSearchStream('test', new AbortController().signal, {}, value => updates.push(value), value => progress.push(value))
  assert.deepEqual(progress, [{ stage: 'queued', position: 2 }, { stage: 'collecting' }])
  assert.equal(updates.length, 2)
  assert.equal(updates[0].result.text, '资源列表')
  assert.equal(updates[1].complete, true)
  assert.equal(canceled, true, 'complete event releases reader even if server socket stays open')
})

test('cooldown error preserves deadline and partial results', async t => {
  const retryAt = new Date(Date.now() + 60000).toISOString()
  const partial = { ...result, partial: true, warning: '后续回复未完整收集' }
  t.mock.method(globalThis, 'fetch', async () => new Response(event('result', { complete: false, result: partial }) + event('error', { code: 'partial_result', error: '已保留部分结果', retryAt })))
  const updates = []
  await assert.rejects(telegramUserSearchStream('test', new AbortController().signal, {}, value => updates.push(value)), error => {
    assert.ok(error instanceof TelegramSearchError)
    assert.equal(error.code, 'partial_result')
    assert.equal(error.retryAt, retryAt)
    return true
  })
  assert.equal(updates[0].result.partial, true)
})

test('stream EOF without complete is not silently reported as success', async t => {
  t.mock.method(globalThis, 'fetch', async () => new Response(event('result', { complete: false, result })))
  await assert.rejects(telegramUserSearchStream('test', new AbortController().signal, {}, () => {}), error => error.code === 'stream_interrupted')
})

test('select uses the same structured cooldown error', async t => {
  const retryAt = new Date(Date.now() + 60000).toISOString()
  t.mock.method(globalThis, 'fetch', async () => Response.json({ error: '等待迟到回复', code: 'cooldown', retryAt }, { status: 409 }))
  await assert.rejects(telegramUserRequest('test', 'select', new AbortController().signal, { id: 'a' }), error => error.code === 'cooldown' && error.retryAt === retryAt)
})
