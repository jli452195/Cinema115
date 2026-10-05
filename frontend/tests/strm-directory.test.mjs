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
  globalThis.window = { location: { origin: 'http://test' }, setTimeout, clearTimeout, setInterval, clearInterval }
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
  let saved = { accountId: 'main', parentCID: '0', generation: { outputDir: '', baseUrl: 'http://test', videoExtensions: 'mkv', metadataExtensions: 'srt', minSizeMB: 0, recursive: true } }
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
    assert.equal(s.activeTab, 'basic')
    assert.equal(s.outputDir, '')
    assert.equal(s.taskOutputDir, '')
    assert.deepEqual([...s.outputDirectories], [])
    s.addDirectory('待整理')
    s.openDirectoryDialog('待整理')
    s.closeDirectoryDialog()
    assert.deepEqual([...s.outputDirectories], ['待整理'])
    s.openDirectoryDialog('待整理')
    s.addDirectory('电影')
    assert.deepEqual([...s.outputDirectories], ['电影'])
    assert.equal(s.outputDir, '电影')
    s.addDirectory('剧集')
    s.openDirectoryDialog('电影')
    s.addDirectory('剧集')
    assert.deepEqual([...s.outputDirectories], ['剧集'])
    assert.equal(s.outputDir, '剧集')
    s.removeDirectory('剧集')
    mounted.app.unmount()
    mounted = mount('views/ShareView.vue', { token: 'test' })
    await tick()
    assert.deepEqual([...mounted.state.outputDirectories], [])
    assert.equal(mounted.state.outputDir, '')
    const empty = mounted.state
    empty.sharePath = '/电影/2026'
    empty.addDirectory('.')
    await empty.saveAutomation()
    assert.equal(writes.length, 1)
    mounted.app.unmount()
    mounted = mount('views/ShareView.vue', { token: 'test' })
    await tick()
    const reloaded = mounted.state
    assert.equal(reloaded.outputDir, '.')
    assert.deepEqual([...reloaded.outputDirectories], ['.'])
    assert.equal(reloaded.sharePath, '/电影/2026')
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

test('share settings preserve explicit root on server reload', async t => {
  let saved = { accountId: 'main', generation: { outputDir: '待整理', baseUrl: 'http://test', videoExtensions: 'mkv' } }
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
    assert.deepEqual([...s.outputDirectories], ['待整理'])
    s.sharePath = '/电影/2026'
    s.addDirectory('.')
    assert.equal(s.outputDir, '.')
    assert.equal(s.resolvedDirectory('.'), '/strm')
    assert.equal(s.directoryName('.'), 'STRM 根目录')
    await s.saveAutomation()
    assert.equal(writes.length, 1)
    assert.equal(writes[0].generation.outputDir, '.')
    assert.equal(writes[0].generation.sharePath, '/电影/2026')
    assert.equal(writes[0].generation.recursive, true)
    mounted.app.unmount()
    mounted = mount('views/ShareView.vue', { token: 'test' })
    await tick()
    const reloaded = mounted.state
    assert.equal(reloaded.outputDir, '.')
    assert.equal(reloaded.sharePath, '/电影/2026')
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

test('editing a directory keeps failed saves retryable and persists the replacement', async t => {
  let failSave = true
  let saved = { accountId: 'main', generation: { outputDir: 'inbox', baseUrl: 'http://test', videoExtensions: 'mkv' } }
  environment(t, async (url, init = {}) => {
    if (url.endsWith('/accounts')) return Response.json({ accounts: [{ id: 'main', primary: true, configured: true }] })
    if (url.endsWith('/bots')) return Response.json({ bots: [] })
    if (url.endsWith('/generation-config')) return Response.json({ baseUrl: 'http://test', videoExtensions: 'mkv' })
    if (url.endsWith('/automation-config')) {
      if (init.method === 'POST') {
        if (failSave) return Response.json({ error: 'save failed' }, { status: 500 })
        saved = JSON.parse(init.body)
      }
      return Response.json({ settings: saved })
    }
    throw new Error(`Unexpected request ${url}`)
  })
  let mounted = mount('views/ShareView.vue', { token: 'test' })
  try {
    await tick()
    const s = mounted.state
    s.openDirectoryDialog('inbox')
    s.addDirectory('movies')
    assert.equal(s.settingsDirty, true)
    await s.saveAutomation()
    assert.match(s.error, /save failed/)
    assert.equal(saved.generation.outputDir, 'inbox')
    assert.equal(s.settingsDirty, true)
    failSave = false
    await s.saveAutomation()
    assert.equal(saved.generation.outputDir, 'movies')
    assert.equal(s.taskOutputDir, 'movies')
    assert.equal(s.settingsDirty, false)
    mounted.app.unmount()
    mounted = mount('views/ShareView.vue', { token: 'test' })
    await tick()
    assert.equal(mounted.state.outputDir, 'movies')
    assert.deepEqual([...mounted.state.outputDirectories], ['movies'])
  } finally { mounted.app.unmount() }
})
test('115 picker selects root and child paths, blocks loading and failed listing', async t => {
  let fail = false
  environment(t, async url => {
    assert.equal(new URL(url, 'http://test').searchParams.get('accountId'), 'main')
    return fail ? Response.json({ error: '目录读取失败' }, { status: 500 }) : Response.json({ directories: [], hasMore: false })
  })
  const selected = []
  const { app, state: s } = mount('components/P115DirectoryDialog.vue', { token: 'test', accountId: 'main', allowRoot: true, onSelect: value => selected.push(value) })
  try {
    s.select()
    assert.equal(selected.length, 0)
    await tick()
    s.select()
    assert.equal(selected[0].id, '0')
    assert.equal(selected[0].path, '/')
    await s.openDirectory({ id: '12', name: '电影' })
    s.select()
    assert.equal(selected[1].id, '12')
    assert.equal(selected[1].path, '/电影')
    fail = true
    await s.openDirectory({ id: '13', name: '无法读取' })
    s.select()
    assert.equal(selected.length, 2)
  } finally { app.unmount() }
})

test('manual generation parses pasted text, uses saved rules and ignores unsaved collection settings', async t => {
  const submitted = []
  environment(t, async (url, init = {}) => {
    if (url.endsWith('/accounts')) return Response.json({ accounts: [{ id: 'main', primary: true, configured: true }] })
    if (url.endsWith('/bots')) return Response.json({ bots: [] })
    if (url.endsWith('/generation-config')) return Response.json({ baseUrl: 'http://test', videoExtensions: 'mkv' })
    if (url.endsWith('/automation-config')) return Response.json({ settings: { accountId: 'main', parentCID: '0', reshareEnabled: true, generation: { outputDir: 'strm', baseUrl: 'http://test', videoExtensions: 'mkv', recursive: true } } })
    if (url.endsWith('/generation-runs')) {
      submitted.push(JSON.parse(init.body))
      return Response.json({ id: 'run', status: 'queued', items: [] })
    }
    if (url.endsWith('/generation-runs/run')) return Response.json({ id: 'run', status: 'success', items: [{ result: { count: 1 } }] })
    throw new Error(`Unexpected request ${url}`)
  })
  const { app, state: s } = mount('views/ShareView.vue', { token: 'test', tab: 'generate' })
  try {
    await tick()
    assert.equal(s.activeTab, 'generate')
    assert.equal(s.generationMode, 'reshare')
    s.tgBatchWaitSeconds = 30
    s.videoExtensions = 'mp4'
    s.sharePath = '/未保存路径'
    s.addDirectory('.')
    assert.equal(s.outputDir, 'strm')
    assert.equal(s.taskOutputDir, '.')
    s.shareURL = '分享给你 https://115cdn.com/s/example 提取码：aB12'
    s.parseShareText()
    assert.equal(s.shareURL, 'https://115cdn.com/s/example')
    assert.equal(s.password, 'aB12')
    s.password = 'new1'
    await s.generate()
    assert.equal(submitted.length, 1)
    assert.equal(submitted[0].collectionScope, undefined)
    assert.equal(submitted[0].inputs[0].mode, 'reshare')
    assert.equal(submitted[0].inputs[0].videoExtensions, 'mkv')
    assert.equal(submitted[0].inputs[0].sharePath, '/')
    assert.equal(submitted[0].inputs[0].outputDir, '.')
    assert.equal(submitted[0].inputs[0].password, 'new1')
    assert.equal(s.settingsDirty, true)
    s.shareURL = 'https://115cdn.com/s/next'
    s.parseShareText()
    assert.equal(s.password, '')
    s.shareURL = 'https://115cdn.com/s/next?password=zX90'
    s.parseShareText()
    assert.equal(s.password, 'zX90')
    s.shareURL = 'https://115cdn.com/s/one https://115cdn.com/s/two'
    await s.generate()
    assert.equal(submitted.length, 1)
  } finally { app.unmount() }
})
