import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import ts from 'typescript'
import { parse, compileScript } from '@vue/compiler-sfc'

const require = createRequire(import.meta.url)
const { createRenderer } = require('vue')
function load(file) {
  const source = readFileSync(file, 'utf8')
  const code = file.endsWith('.vue') ? compileScript(parse(source, { filename: file }).descriptor, { id: 'directory-test' }).content : source
  const compiled = ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
  const module = { exports: {} }
  const resolve = id => id.endsWith('.vue') ? { default: {} } : id.startsWith('.') ? load(path.resolve(path.dirname(file), id + '.ts')) : require(id)
  new Function('require', 'module', 'exports', compiled)(resolve, module, module.exports)
  return module.exports
}
const file = name => fileURLToPath(new URL(`../src/${name}`, import.meta.url))
const renderer = createRenderer({ createElement: () => ({}), createText: () => ({}), createComment: () => ({}), insert() {}, remove() {}, setText() {}, setElementText() {}, patchProp() {}, parentNode: () => null, nextSibling: () => null })
const tick = () => new Promise(resolve => setImmediate(resolve))
function environment(t, fetch) {
  const originals = new Map(['fetch', 'document', 'window', 'HTMLElement', 'localStorage'].map(key => [key, globalThis[key]]))
  globalThis.fetch = fetch
  globalThis.HTMLElement = class {}
  globalThis.document = { body: { style: { overflow: '' } }, activeElement: null }
  globalThis.window = { location: { origin: 'http://test' } }
  const storage = new Map()
  globalThis.localStorage = { getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value) }
  t.after(() => { for (const [key, value] of originals) globalThis[key] = value })
}
function mount(name, props) {
  const View = load(file(name)).default
  View.render = () => null
  const app = renderer.createApp(View, props)
  const vm = app.mount({})
  return { app, state: vm.$.setupState }
}

test('directory dialog selects loaded root and children, blocks errors and loading', async t => {
  let fail = false
  environment(t, async url => {
    if (fail) return Response.json({ error: '无法读取' }, { status: 500 })
    const folder = new URL(url, 'http://test').searchParams.get('path') || ''
    return Response.json({ path: folder, displayPath: folder ? `/strm/${folder}` : '/strm', directories: [], truncated: false })
  })
  const selected = []
  const { app, state: s } = mount('components/STRMDirectoryDialog.vue', { token: 'test', onSelect: value => selected.push(value) })
  try {
    s.select()
    assert.deepEqual(selected, [])
    await tick()
    s.select()
    assert.deepEqual(selected, ['.'])
    await s.openDirectory('电影')
    s.select()
    assert.deepEqual(selected, ['.', '电影'])
    s.goUp(); await tick(); s.select()
    assert.equal(selected.at(-1), '.')
    fail = true
    await s.openDirectory('bad')
    s.select()
    assert.equal(selected.length, 3)
  } finally { app.unmount() }
})

test('share settings save and reload explicit root; removing choices never selects root implicitly', async t => {
  let saved = { accountId: 'main', parentCID: '0', generation: { outputDir: 'strm', baseUrl: 'http://test', videoExtensions: 'mkv', metadataExtensions: 'srt', minSizeMB: 0, recursive: true } }
  const writes = []
  environment(t, async (url, init = {}) => {
    if (url.endsWith('/accounts')) return Response.json({ accounts: [{ id: 'main', primary: true, configured: true }] })
    if (url.endsWith('/bots')) return Response.json({ bots: [] })
    if (url.endsWith('/generation-config')) return Response.json({ baseUrl: 'http://test', videoExtensions: 'mkv' })
    if (url.endsWith('/automation-config')) {
      if (init.method === 'POST') { saved = JSON.parse(init.body); writes.push(saved) }
      return Response.json({ settings: saved })
    }
    throw new Error(`Unexpected request ${url}`)
  })
  let mounted = mount('views/ShareView.vue', { token: 'test' })
  try {
    await tick()
    const s = mounted.state
    assert.equal(s.initializing, false)
    s.addDirectory('.')
    assert.equal(s.outputDir, '.')
    assert.equal(s.resolvedDirectory('.'), '/strm')
    assert.equal(s.directoryName('.'), 'STRM 根目录')
    await s.saveAutomation()
    assert.equal(writes.length, 1)
    assert.equal(writes[0].generation.outputDir, '.')
    mounted.app.unmount()
    mounted = mount('views/ShareView.vue', { token: 'test' })
    await tick()
    const reloaded = mounted.state
    assert.equal(reloaded.outputDir, '.')
    assert.equal(reloaded.outputDirectories.includes('.'), true)
    assert.equal(reloaded.settings().outputDir, '.')
    for (const directory of [...reloaded.outputDirectories]) reloaded.removeDirectory(directory)
    assert.equal(reloaded.outputDir, '')
    await reloaded.saveAutomation()
    assert.equal(writes.length, 1)
    assert.match(reloaded.error, /请选择 STRM/)
    await reloaded.generate()
    assert.equal(reloaded.generating, false)
    reloaded.addDirectory('../outside')
    assert.equal(reloaded.outputDir, '')
  } finally { mounted.app.unmount() }
})

test('root representation is distinct from missing selection and unsafe paths', () => {
  const { validSTRMDirectory: valid, strmDirectoryPath: display } = load(file('utils/strmDirectory.ts'))
  for (const value of ['.', '电影', '电影/2026']) assert.equal(valid(value), true)
  for (const value of ['', undefined, null, '/', '/strm', '..', './电影', '电影/../其他', '电影//其他', 'D:\\strm']) assert.equal(valid(value), false)
  assert.equal(display('.'), '/strm')
  assert.equal(display(''), '未选择')
})
