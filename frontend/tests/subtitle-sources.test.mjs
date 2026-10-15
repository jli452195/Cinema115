import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import ts from 'typescript'
import { zipSync, strToU8 } from 'fflate'

const require = createRequire(import.meta.url)
const loaded = new Map()
function compile(source, resolver = require) {
  const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
  const module = { exports: {} }
  new Function('require', 'module', 'exports', compiled)(resolver, module, module.exports)
  return module.exports
}
loaded.set('./subtitleImport', compile(await readFile(new URL('../src/utils/subtitleImport.ts', import.meta.url), 'utf8')))
loaded.set('../lib/p115-api', compile(await readFile(new URL('../src/lib/p115-api.ts', import.meta.url), 'utf8')))
const { readSubtitleResponse, readDriveSubtitles, mergeSubtitleFiles } = compile(await readFile(new URL('../src/utils/subtitleSources.ts', import.meta.url), 'utf8'), name => loaded.get(name) || require(name))

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

test('cloud input uses authenticated snapshots, parses ZIP and releases on success or failure', async t => {
  const bytes = zipSync({ 'S01E01.srt': strToU8('subtitle'), 'font.ttf': strToU8('font') })
  const source = { id: '42', parentId: '10', name: 'subs.zip', size: bytes.length, directory: false, sourcePath: '/字幕/subs.zip' }
  for (const broken of [false, true]) {
    const calls = [], progress = []
    t.mock.method(globalThis, 'fetch', async (url, init) => {
      calls.push({ url, init })
      assert.equal(init.headers.Authorization, 'Bearer admin')
      if (init.method === 'POST') return Response.json({ id: 'snapshot', files: [source] })
      if (init.method === 'DELETE') return new Response(null, { status: 204 })
      return new Response(broken ? bytes.subarray(0, 2) : bytes)
    })
    const action = readDriveSubtitles('admin', 'main', 'job', [source], new AbortController().signal, p => progress.push(p))
    if (broken) await assert.rejects(action, /subs.zip.*不完整/)
    else {
      const result = await action
      assert.equal(result.files[0].name, 'S01E01.srt')
      assert.equal(result.files[0].sourceLabel, '/字幕/subs.zip（压缩包）')
      assert.match(result.files[0].sourceKey, /^archive:/)
      assert.equal(result.ignored, 1)
      assert.deepEqual([...new Set(progress.map(p => p.phase))], ['prepare', 'download', 'extract'])
    }
    assert.equal(calls.length, 3)
    assert.match(calls[1].url, /snapshot\/files\/42\?accountId=main/)
    assert.equal(calls[2].init.method, 'DELETE')
    t.mock.restoreAll()
  }
})

test('append preserves edited row identity, deduplicates within sources and checks combined budget atomically', () => {
  const old = { name: '01.srt', data: new Uint8Array([1]), sha256: 'a', videoId: 'manual', language: 'en', manual: true }
  const result = mergeSubtitleFiles([old], [old, { ...old, name: '02.srt' }], true)
  assert.equal(result.kept[0], old)
  assert.equal(result.kept[0].videoId, 'manual')
  assert.equal(result.duplicates, 1)
  assert.equal(result.added.length, 1)
  assert.equal(mergeSubtitleFiles([old], [{ ...old, sourceKey: 'other-directory' }], true).added.length, 1)
  assert.equal(mergeSubtitleFiles([old], [old], false).kept.length, 0)
  const full = Array.from({ length: 100 }, (_, index) => ({ ...old, name: `${index}.srt` }))
  assert.throws(() => mergeSubtitleFiles(full, [{ ...old, name: 'new.srt' }], true), /100/)
  assert.equal(full.length, 100)
})
