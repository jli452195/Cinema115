import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import ts from 'typescript'
import { libarchiveWasm } from 'libarchive-wasm'

const require = createRequire(import.meta.url)
const source = await readFile(new URL('../src/utils/subtitleArchive.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const module = { exports: {} }
new Function('require', 'module', 'exports', compiled)(require, module, module.exports)
const { extractSubtitleArchive } = module.exports
const wasm = await libarchiveWasm({ wasmBinary: await readFile(new URL('../node_modules/libarchive-wasm/dist/libarchive.wasm', import.meta.url)) })
const fixture = async name => new Uint8Array(await readFile(new URL(`./fixtures/subtitle-archives/${name}`, import.meta.url)))
const limits = { bytes: 64 * 1024 * 1024, files: 100 }

test('RAR4 and RAR5 compressed files are decoded, hashed and keep nested names', async () => {
  for (const version of [4, 5]) {
    const result = extractSubtitleArchive(await fixture(`normal-v${version}.rar`), 'rar', wasm, limits)
    assert.equal(result.files.length, 1)
    assert.equal(result.files[0].name, 'example/readm.ass')
    assert.equal(result.files[0].data.length, 144)
    assert.match(result.files[0].sha256, /^[a-f0-9]{64}$/)
    assert(result.ignored > 0)
  }
})

test('solid 7z supports Unicode names and ignores attachments', async () => {
  const result = extractSubtitleArchive(await fixture('normal.7z'), '7z', wasm, limits)
  assert.equal(result.files.length, 1)
  assert.equal(result.files[0].name, 'episode01.chs.srt')
  assert.match(new TextDecoder().decode(result.files[0].data), /subtitle/)
  assert.equal(result.ignored, 1)
})

test('7z encrypted headers and encrypted data are both refused', async () => {
  for (const file of ['encrypted-header.7z', 'encrypted-data.7z']) {
    const bytes = await fixture(file)
    assert.throws(() => extractSubtitleArchive(bytes, '7z', wasm, limits), /加密/)
  }
})

test('corrupt archives, file/count budgets and incomplete streams are rejected', async () => {
  const normal = await fixture('normal.7z')
  assert.throws(() => extractSubtitleArchive(normal, '7z', wasm, { bytes: 1, files: 100 }), /16 MiB/)
  assert.throws(() => extractSubtitleArchive(normal, '7z', wasm, { bytes: limits.bytes, files: 0 }), /100/)
  assert.throws(() => extractSubtitleArchive(normal.subarray(0, 20), '7z', wasm, limits), /损坏/)
  assert.throws(() => extractSubtitleArchive(new Uint8Array(40), 'rar', wasm, limits), /损坏/)
  const rar = await fixture('normal-v4.rar')
  assert.throws(() => extractSubtitleArchive(rar.subarray(0, 12), 'rar', wasm, limits), /损坏/)
})
