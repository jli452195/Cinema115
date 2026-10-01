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
function loadModule(file) {
  const source = readFileSync(file, 'utf8')
  const code = file.endsWith('.vue') ? compileScript(parse(source, { filename: file }).descriptor, { id: 'folder-test' }).content : source
  const compiled = ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
  const module = { exports: {} }
  const resolve = id => id.endsWith('.vue') ? { default: {} } : id.startsWith('.') ? loadModule(path.resolve(path.dirname(file), id + '.ts')) : require(id)
  new Function('require', 'module', 'exports', compiled)(resolve, module, module.exports)
  return module.exports
}
const View = loadModule(fileURLToPath(new URL('../src/views/FolderMonitorView.vue', import.meta.url))).default
View.render = () => null
const renderer = createRenderer({ createElement: () => ({}), createText: () => ({}), createComment: () => ({}), insert() {}, remove() {}, setText() {}, setElementText() {}, patchProp() {}, parentNode: () => null, nextSibling: () => null })
const tick = () => new Promise(resolve => setImmediate(resolve))
async function harness(t) {
  const originalFetch = globalThis.fetch, originalDocument = globalThis.document
  globalThis.document = { title: '' }
  let row = { id: 'directory', accountId: 'main', sourceCID: '10', sourcePath: '/incoming', stage: 'waiting', running: false, queued: false, hasBatch: false, lastError: '', enabled: false }
  let failure = false, hold = false, resolveHeld
  const requests = []
  globalThis.fetch = async (url, init = {}) => {
    const endpoint = new URL(url, 'http://test').pathname.split('/p115/')[1]
    const method = init.method || 'GET', body = init.body ? JSON.parse(init.body) : undefined
    requests.push({ endpoint, method, body })
    if (endpoint === 'accounts') return Response.json({ accounts: [{ id: 'main', primary: true, configured: true }] })
    if (endpoint === 'share/automation-config') return Response.json({ settings: { accountId: 'main', generation: { outputDir: 'library', recursive: true, minSizeMB: 10, videoExtensions: 'mkv' } } })
    if (endpoint === 'folder-monitors') {
      const data = { monitors: [structuredClone(row)] }
      if (hold) { hold = false; await new Promise(resolve => { resolveHeld = resolve }) }
      return Response.json(data)
    }
    if (failure) return Response.json({ error: '保存失败' }, { status: 409 })
    if (endpoint.endsWith('/schedule')) row = { ...row, ...body }
    else if (endpoint.endsWith('/share')) { row = { ...row, queued: true }; return Response.json({ accepted: true, taskId: 'run' }) }
    else if (endpoint === 'folder-monitors/directory' && method === 'PUT') row = { ...row, ...body, enabled: false }
    else throw new Error(`Unexpected request: ${method} ${endpoint}`)
    return Response.json(row)
  }
  const app = renderer.createApp(View, { token: 'test' }), vm = app.mount({}), state = vm.$.setupState
  t.after(() => { app.unmount(); globalThis.fetch = originalFetch; globalThis.document = originalDocument })
  for (let i = 0; i < 30 && state.loading; i++) await tick()
  assert.equal(state.loading, false)
  return { state, requests, fail: value => { failure = value }, hold: () => { hold = true }, release: () => resolveHeld(), set: data => { row = { ...row, ...data } } }
}

test('selecting a directory saves immediately; failure retains previous binding', async t => {
  const { state: s, requests, fail } = await harness(t)
  await s.openSource()
  assert.equal(s.directoryAccountId, 'main')
  fail(true)
  await s.selectSource({ id: '20', path: '/new' })
  assert.equal(s.monitor.sourceCID, '10')
  assert.match(s.actionError, /保存失败/)
  fail(false)
  await s.selectSource({ id: '20', path: '/new' })
  assert.equal(s.monitor.sourceCID, '20')
  assert.equal(requests.filter(r => r.method === 'PUT').length, 2)
  assert.equal(requests.some(r => r.endpoint.endsWith('/share')), false)
  await s.selectSource({ id: '0', path: '/' })
  assert.equal(s.monitor.sourceCID, '20')
})

test('schedule failure leaves dialog open; save uses selected interval', async t => {
  const { state: s, requests, fail } = await harness(t)
  s.openSchedule(); s.interval = '1800'; fail(true)
  await s.saveSchedule(true)
  assert.equal(s.scheduleDialog, true)
  assert.equal(s.monitor.enabled, false)
  fail(false)
  await s.saveSchedule(true)
  assert.equal(s.scheduleDialog, false)
  assert.equal(s.monitor.enabled, true)
  assert.deepEqual(requests.at(-1).body, { enabled: true, intervalSeconds: 1800 })
})

test('late poll cannot overwrite a saved schedule', async t => {
  const h = await harness(t), s = h.state
  h.hold()
  const polling = s.refresh()
  await tick()
  await s.saveSchedule(true)
  h.release(); await polling
  assert.equal(s.monitor.enabled, true)
})

test('submitted task disables directory edits but allows stopping the schedule', async t => {
  const h = await harness(t), s = h.state
  await s.saveSchedule(true)
  await s.start()
  assert.equal(s.active, true)
  assert.equal(s.editable, false)
  assert.equal(s.canSchedule, false)
  await s.saveSchedule(false)
  assert.equal(s.monitor.enabled, false)
  assert.equal(s.monitor.queued, true)
  assert.equal(h.requests.filter(r => r.endpoint.endsWith('/share')).length, 1)
})
