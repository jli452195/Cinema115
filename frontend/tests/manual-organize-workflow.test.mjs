import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import ts from 'typescript'
import { parse, compileScript } from '@vue/compiler-sfc'

// Exercise the actual view setup with Vue's renderer and mocked HTTP responses.
// No browser session or live 115 endpoint is used.
const require = createRequire(import.meta.url)
const { createRenderer, nextTick } = require('vue')
const filename = fileURLToPath(new URL('../src/views/ManualOrganizeView.vue', import.meta.url))
function loadModule(file) {
  const source = readFileSync(file, 'utf8')
  const code = file.endsWith('.vue')
    ? compileScript(parse(source, { filename: file }).descriptor, { id: 'workflow-test' }).content
    : source
  const compiled = ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
  const module = { exports: {} }
  const resolve = id => {
    if (id.endsWith('.vue')) return { default: {} }
    if (id.startsWith('.')) return loadModule(path.resolve(path.dirname(file), id + '.ts'))
    return require(id)
  }
  new Function('require', 'module', 'exports', compiled)(resolve, module, module.exports)
  return module.exports
}
const View = loadModule(filename).default
View.render = () => null
const renderer = createRenderer({
  createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
  insert() {}, remove() {}, setText() {}, setElementText() {}, patchProp() {},
  parentNode: () => null, nextSibling: () => null,
})
const config = { accountId: 'main', sourceCID: '10', sourcePath: '/incoming', targetCID: '20', targetPath: '/library' }
const entry = { id: '30', name: 'Example.S02', directory: true, size: 0 }
const video = (id, episode) => ({ id, parentId: '30', name: `Example.S02E${episode}.mkv`, sourcePath: `/incoming/Example.S02/Example.S02E${episode}.mkv`, directory: false, mediaType: 'video', season: 2, episode, suffix: '', size: 100 })
const media = { id: 100, title: 'Example', kind: 'tv', date: '2024-01-01', seasons: [{ number: 1, name: '第 1 季', episodeCount: 12 }, { number: 2, name: '第 2 季', episodeCount: 12 }] }

async function harness(t) {
  const requests = [], jobs = []
  let conflict = false, historyFailure = false, scanFailure = false
  const originalFetch = globalThis.fetch
  globalThis.fetch = async (url, init = {}) => {
    const pathname = new URL(url, 'http://test').pathname
    const body = init.body ? JSON.parse(init.body) : undefined
    requests.push({ pathname, method: init.method || 'GET', body })
    let data
    if (pathname.endsWith('/accounts')) data = { accounts: [{ id: 'main', primary: true, configured: true }] }
    else if (pathname.endsWith('/config')) data = init.method === 'PUT' ? { ok: true } : { ...config }
    else if (pathname.endsWith('/files')) data = { files: [{ ...entry }], count: 1 }
    else if (pathname.endsWith('/scan')) {
      if (scanFailure) return Response.json({ error: '扫描失败' }, { status: 502 })
      data = { id: 'scan-1', files: [video('32', 2), video('31', 1)] }
    } else if (pathname.endsWith('/detail/tv/100')) data = structuredClone(media)
    else if (pathname.endsWith('/preview') && !pathname.includes('/cleanup/')) {
      data = { id: 'preview-1', title: 'Example', status: 'preview', config, createdAt: '2026-10-01T00:00:00Z', items: body.files.map(file => ({ file: { ...video(file.id, file.episode), ...file }, targetPath: `/library/${file.id}.mkv`, conflict: conflict && file.id === '31' ? '同名冲突' : '', status: 'pending' })) }
    } else if (pathname.endsWith('/cleanup/preview')) {
      data = { id: 'cleanup-1', accountId: 'main', parentId: '10', parentPath: '/incoming', mode: body.mode, status: 'preview', items: [{ ...entry, parentId: '10', path: '/incoming/Example.S02', status: 'pending' }] }
    } else if (pathname.endsWith('/cleanup') && init.method === 'POST') {
      data = { id: 'cleanup-1', accountId: 'main', status: 'completed', items: [{ ...entry, status: 'done' }] }
    } else if (pathname.endsWith('/jobs') && init.method === 'POST') {
      data = { id: 'job-1', title: 'Example', config, status: 'queued', items: [], createdAt: '2026-10-01T00:00:00Z' }
      jobs.push(data)
    } else if (/\/(jobs|cleanup|subtitles)$/.test(pathname)) {
      if (historyFailure) return Response.json({ error: '记录读取失败' }, { status: 502 })
      data = { jobs: pathname.endsWith('/jobs') ? jobs : [] }
    } else throw new Error(`Unexpected request: ${pathname}`)
    return Response.json(data)
  }
  const app = renderer.createApp(View, { token: 'test' })
  const vm = app.mount({})
  const state = vm.$.setupState
  t.after(() => { app.unmount(); globalThis.fetch = originalFetch })
  for (let i = 0; i < 40 && (state.busy || !state.historyReady); i++) await new Promise(resolve => setImmediate(resolve))
  assert.equal(state.busy, '')
  assert.equal(state.historyReady, true)
  return { state, requests, conflict: value => { conflict = value }, historyFailure: value => { historyFailure = value }, scanFailure: value => { scanFailure = value } }
}

test('step navigation keeps edits and does not allow unavailable steps', async t => {
  const { state: s, requests } = await harness(t)
  assert.equal(s.configDirty, false)
  assert.equal(requests.some(r => r.method !== 'GET'), false)
  await s.scrollToStep(4)
  assert.equal(s.activeStep, 1)
  await s.scan(entry)
  assert.equal(s.activeStep, 2)
  assert.deepEqual(s.rows.map(row => row.id), ['31', '32'])
  await s.choose(media)
  assert.equal(s.activeStep, 3)
  s.batchSeason = 2
  s.startEpisode = 5
  s.numberEpisodes()
  s.rows[0].suffix = '4K'
  await s.scrollToStep(1)
  await s.scrollToStep(2)
  await s.scrollToStep(3)
  assert.deepEqual(s.rows.map(row => [row.season, row.episode]), [[2, 5], [2, 6]])
  assert.equal(s.rows[0].suffix, '4K')
  assert.equal(s.media.id, 100)
  assert.equal(requests.filter(r => r.method === 'PUT').length, 0)
})

test('editing invalidates preview, conflicts prevent execution, valid submission resets workflow', async t => {
  const h = await harness(t), s = h.state
  await s.scan(entry)
  await s.choose(media)
  await s.makePreview()
  assert.equal(s.activeStep, 4)
  await s.scrollToStep(3)
  assert.equal(s.preview.id, 'preview-1')
  s.rows[0].suffix = '1080p'
  assert.equal(s.preview, undefined)
  await s.scrollToStep(4)
  assert.equal(s.activeStep, 3)
  h.conflict(true)
  await s.makePreview()
  s.onlyConflicts = true
  assert.equal(s.previewItems.length, 1)
  assert.equal(s.previewVideos, 2)
  await s.execute()
  assert.equal(h.requests.filter(r => r.pathname.endsWith('/jobs') && r.method === 'POST').length, 0)
  h.conflict(false)
  await s.makePreview()
  assert.equal(s.onlyConflicts, false)
  await s.execute()
  assert.equal(s.activeStep, 1)
  assert.equal(s.scanId, '')
  assert.equal(s.preview, undefined)
  assert.equal(s.jobs[0].status, 'queued')
  assert.equal(h.requests.filter(r => r.pathname.endsWith('/jobs') && r.method === 'POST').length, 1)
})

test('changing directories discards the previous scan and saves the new scope before scanning', async t => {
  const { state: s, requests } = await harness(t)
  await s.scan(entry)
  await s.choose(media)
  await s.makePreview()
  s.config.targetCID = '99'
  s.config.targetPath = '/new-library'
  assert.equal(s.activeStep, 1)
  assert.equal(s.scanId, '')
  assert.equal(s.preview, undefined)
  assert.equal(s.media, undefined)
  assert.equal(s.configDirty, true)
  await s.scan(entry)
  assert.equal(s.configDirty, false)
  const saved = requests.find(r => r.method === 'PUT')
  assert.equal(saved.body.targetCID, '99')
  assert.equal(saved.body.accountId, 'main')
  assert(requests.indexOf(saved) < requests.findLastIndex(r => r.pathname.endsWith('/scan')))
})

test('failed history refresh blocks execution without losing preview', async t => {
  const h = await harness(t), s = h.state
  await s.scan(entry)
  await s.choose(media)
  await s.makePreview()
  h.historyFailure(true)
  await s.refreshHistory()
  await s.execute()
  assert.match(s.historyError, /记录读取失败/)
  assert.equal(s.preview.id, 'preview-1')
  assert.equal(h.requests.some(r => r.pathname.endsWith('/jobs') && r.method === 'POST'), false)
})

test('cleanup requires a preview and clears stale organize selection after execution', async t => {
  const { state: s, requests } = await harness(t)
  await s.scan(entry)
  await s.choose(media)
  s.cleanupOpen = true
  s.deleteIDs = ['30']
  await s.previewCleanup('selected')
  assert.equal(s.cleanupPlan.id, 'cleanup-1')
  assert.equal(requests.some(r => r.pathname.endsWith('/cleanup') && r.method === 'POST'), false)
  await s.executeCleanup()
  assert.equal(s.activeStep, 1)
  assert.equal(s.scanId, '')
  assert.equal(s.cleanupPlan, undefined)
  assert.deepEqual(s.deleteIDs, [])
  assert.equal(s.cleanupOpen, true)
})

test('scan failures remain recoverable at the first step', async t => {
  const h = await harness(t), s = h.state
  await s.scan(entry)
  await s.choose(media)
  h.scanFailure(true)
  await s.work('扫描所选内容', () => s.scan(entry))
  await nextTick()
  assert.equal(s.busy, '')
  assert.equal(s.activeStep, 1)
  assert.equal(s.scanId, '')
  assert.match(s.error, /扫描失败/)
})
