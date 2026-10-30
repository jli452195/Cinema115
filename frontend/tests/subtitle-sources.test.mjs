import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import ts from 'typescript'

const require = createRequire(import.meta.url)
const loaded = new Map()
function compile(source, resolver = require) {
  const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
  const module = { exports: {} }
  new Function('require', 'module', 'exports', compiled)(resolver, module, module.exports)
  return module.exports
}
loaded.set('./subtitleImport', compile(await readFile(new URL('../src/utils/subtitleImport.ts', import.meta.url), 'utf8'), name => name === './subtitleArchiveClient' ? { readSubtitleArchive: (bytes, kind, limits, signal) => loaded.get('./subtitleImport').readSubtitleZip(bytes, signal, limits) } : require(name)))
const { readSubtitleResponse } = compile(await readFile(new URL('../src/utils/subtitleSources.ts', import.meta.url), 'utf8'), name => loaded.get(name) || require(name))

test('download streams enforce exact size, errors, progress and cancellation', async () => {
  const signal = new AbortController().signal
  const progress = []
  assert.deepEqual(await readSubtitleResponse(new Response(new Uint8Array([1, 2, 3])), 3, signal, n => progress.push(n)), new Uint8Array([1, 2, 3]))
  assert.deepEqual(progress, [3])
  await assert.rejects(readSubtitleResponse(new Response('extra'), 3, signal, () => {}), /超过/)
  await assert.rejects(readSubtitleResponse(new Response('x'), 3, signal, () => {}), /不完整/)
  await assert.rejects(readSubtitleResponse(new Response('abc', { headers: { 'Content-Length': '9' } }), 3, signal, () => {}), /大小/)
  await assert.rejects(readSubtitleResponse(new Response('{"error":"来源已过期"}', { status: 409 }), 3, signal, () => {}), /来源已过期/)
  const controller = new AbortController()
  let cancelled = false
  const pending = readSubtitleResponse(new Response(new ReadableStream({ cancel() { cancelled = true } })), 3, controller.signal, () => {})
  controller.abort()
  await assert.rejects(pending, { name: 'AbortError' })
  assert.equal(cancelled, true)
})
