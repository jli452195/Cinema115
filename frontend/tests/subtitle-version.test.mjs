import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import ts from 'typescript'
import { parse, compileScript } from '@vue/compiler-sfc'

const require = createRequire(import.meta.url)
const { computed, createRenderer, ref } = require('vue')
const codeCache = new Map()
function moduleLoader(mocks = {}) {
  const modules = new Map()
  return function load(file) {
    if (modules.has(file)) return modules.get(file)
    if (!codeCache.has(file)) {
      const source = readFileSync(file, 'utf8')
      const code = file.endsWith('.vue') ? compileScript(parse(source, { filename: file }).descriptor, { id: 'subtitle-version-test' }).content : source
      codeCache.set(file, ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText)
    }
    const module = { exports: {} }
    const resolve = id => mocks[id] || (id.endsWith('.vue') ? { default: {} } : id.startsWith('.') ? load(path.resolve(path.dirname(file), `${id}.ts`)) : require(id))
    new Function('require', 'module', 'exports', codeCache.get(file))(resolve, module, module.exports)
    modules.set(file, module.exports)
    return module.exports
  }
}
const sourceFile = name => fileURLToPath(new URL(`../src/${name}`, import.meta.url))
const { subtitleTargetName } = moduleLoader()(sourceFile('utils/subtitleLibrary.ts'))
const renderer = createRenderer({ createElement: () => ({}), createText: () => ({}), createComment: () => ({}), insert() {}, remove() {}, setText() {}, setElementText() {}, patchProp() {}, parentNode: () => null, nextSibling: () => null })
const tick = () => new Promise(resolve => setImmediate(resolve))
const hash = index => (index ? 'b' : 'a').repeat(40)
const library = (videoId, version, overrides = {}) => ({ versionId: `library-${version}`, path: `${version}/01.ass`, videoId, language: 'zh-CN', version, file: { name: `${version}/01.ASS`, path: `${version}/01.ass`, sha: `${version}-sha`, size: 3, revision: 'r1' }, ...overrides })
const local = (videoId, version, overrides = {}) => ({ localId: 'c'.repeat(32), videoId, language: 'en', version, file: { name: 'local/01.vtt', data: new Uint8Array([1, 2, 3]), sha256: 'd'.repeat(64) }, ...overrides })

async function harness(t, { batch = false, seasons = [1], rename = false } = {}) {
  const originalFetch = globalThis.fetch, originalWindow = globalThis.window, originalDocument = globalThis.document
  const requests = [], jobs = new Map()
  let app, state, sequence = 0
  t.after(() => {
    try {
      app?.unmount()
    } finally {
      globalThis.fetch = originalFetch
      globalThis.window = originalWindow
      globalThis.document = originalDocument
    }
  })
  globalThis.window = Object.assign(new EventTarget(), { matchMedia: () => ({ matches: true }) })
  globalThis.document = new EventTarget()
  globalThis.fetch = async (url, init = {}) => {
    const endpoint = String(url).split('/p115/')[1]
    const body = init.body instanceof FormData ? init.body : init.body ? JSON.parse(init.body) : undefined
    requests.push({ endpoint, body, headers: init.headers })
    if (endpoint === 'cloud-download/config') return Response.json({ accountId: 'main', targetCID: 'incoming', targetPath: '/incoming' })
    if (endpoint === 'download-organize/config') return Response.json({ accountId: 'main', targetCID: 'library', targetPath: '/library' })
    if (endpoint === 'download-organize/share-config') return Response.json({ ready: true })
    if (endpoint.endsWith('/preview')) {
      const resources = body.resources || [body]
      const planned = resources.map(resource => {
        const resourceHash = resource.magnet.split(':').pop()
        return { id: `preview-${++sequence}`, naming: { revision: 1 }, download: { hash: resourceHash }, items: resource.files.map(file => {
          const id = batch ? `${resourceHash}:${file.id}` : file.id
          const result = state.results[id]
          const stem = rename ? `Official.${result.stem}` : result.stem
          return { file: { id: file.id }, directory: result.directory, stem, name: `${stem}.mkv` }
        }) }
      })
      const result = batch ? { id: `batch-${sequence}`, jobs: planned } : planned[0]
      jobs.set(result.id, result)
      return Response.json(result)
    }
    if (endpoint.endsWith('/attachments')) return new Response(null, { status: 204 })
    if (endpoint.endsWith('/jobs')) return Response.json(jobs.get(body.previewId))
    throw new Error(`意外请求：${endpoint}`)
  }
  // 执行真实组件、资源归属和 HTTP 序列化，只替换不属于本次验证的命名服务与浏览器布局。
  const load = moduleLoader({
    '@tabler/icons-vue': {}, 'vue-router': {},
    '../composables/useStickySurface': { useStickySurface: () => ref(false) },
    '../composables/useDownloadPipelineToast': { trackDownloadPipeline() {} },
    '../composables/useMediaNamingPreview': { useMediaNamingPreview: (_token, source) => {
      const results = computed(() => Object.fromEntries((source()?.files || []).map(file => {
        const stem = `Show.S${String(file.season).padStart(2, '0')}E${String(file.episode).padStart(2, '0')}`
        return [file.id, { directory: `Show/Season ${file.season}`, stem, name: `${stem}.mkv` }]
      })))
      return { results, value: ref({ naming: { revision: 1 } }), errors: ref({}), ready: ref(true), pending: ref(false), error: ref(''), refresh() {} }
    } },
  })
  const Dialog = load(sourceFile('components/DownloadOrganizeDialog.vue')).default
  Dialog.render = () => null
  const entries = Array.from({ length: batch ? 2 : 1 }, (_, index) => {
    const magnet = `magnet:?xt=urn:btih:${hash(index)}`
    return { resource: { id: String(index), title: 'Show', magnet }, source: { source: 'mikan', providerId: String(index), magnet }, files: { manifestId: `manifest-${index}`, files: seasons.map(season => ({ id: `video-${season}`, name: `Raw.S0${season}E01.mkv`, titleOnly: true, directory: false, size: 100, parentId: 'root', sourcePath: `/Raw/season-${season}/01.mkv`, mediaType: 'video', season, episode: index + 1 })) } }
  })
  app = renderer.createApp(Dialog, { token: 'test-token', entries, batch, tmdbId: 1, kind: 'tv', title: 'Show', date: '2026', seasons: seasons.map(number => ({ number, name: '', episodeCount: 1 })) })
  const vm = app.mount({})
  state = vm.$.setupState
  for (let i = 0; i < 30 && state.loading; i++) await tick()
  assert.equal(state.loading, false)
  assert.equal(state.accountId, 'main')
  return { state, requests }
}

test('字幕命名按用户确认版本、语言、扩展名输出，空版本保留原格式', () => {
  for (const [version, language, expected] of [
    ['HKG', 'zh-CN', 'Show.S01E01.HKG.zh-CN.ass'],
    ['KTXP', 'zh-CN', 'Show.S01E01.KTXP.zh-CN.ass'],
    ['HKG', '', 'Show.S01E01.HKG.ass'],
    ['', 'zh-TW', 'Show.S01E01.zh-TW.ass'],
    ['', '', 'Show.S01E01.ass'],
  ]) assert.equal(subtitleTargetName('Show.S01E01.mkv', '字幕目录/01.ASS', language, version), expected)
  for (const ext of ['IDX', 'SUB']) assert.equal(subtitleTargetName('Show.mkv', `01.${ext}`, 'zh-CN', 'KTXP'), `Show.KTXP.zh-CN.${ext.toLowerCase()}`)
})

test('单资源真实请求同时传递字幕库和本地版本，保留主账号、摘要与附件安全边界', async t => {
  const { state: s, requests } = await harness(t)
  s.openSubtitles()
  s.selectSubtitles([library('video-1', 'HKG'), library('video-1', 'KTXP')], [local('video-1', 'KTXP')])
  assert.deepEqual(s.localConflicts, {})
  await s.start()
  assert.equal(s.error, '')
  const preview = requests.find(request => request.endpoint === 'download-organize/preview')
  assert.equal(preview.body.accountId, 'main')
  assert.equal(preview.body.targetCID, 'library')
  assert.equal(preview.body.manifestId, 'manifest-0')
  assert.equal(preview.headers.Authorization, 'Bearer test-token')
  assert.deepEqual(preview.body.subtitles, [
    { language: 'zh-CN', version: 'HKG', videoId: 'video-1', versionId: 'library-HKG', path: 'HKG/01.ass', sha: 'HKG-sha' },
    { language: 'zh-CN', version: 'KTXP', videoId: 'video-1', versionId: 'library-KTXP', path: 'KTXP/01.ass', sha: 'KTXP-sha' },
    { language: 'en', version: 'KTXP', videoId: 'video-1', localId: 'c'.repeat(32), originalName: 'local/01.vtt', sha256: 'd'.repeat(64), size: 3 },
  ])
  const upload = requests.find(request => request.endpoint.endsWith('/attachments'))
  assert.equal(upload.body.get('previewId'), 'preview-1')
  assert.equal(upload.body.get('c'.repeat(32)).size, 3)
  assert.deepEqual(requests.find(request => request.endpoint === 'download-organize/jobs').body, { previewId: 'preview-1' })
  assert.deepEqual(s.selectedExternal.map(s.externalName), ['Show.S01E01.HKG.zh-CN.ass', 'Show.S01E01.KTXP.zh-CN.ass', 'Show.S01E01.KTXP.en.vtt'])
})

test('只修改字幕库版本会改变快照并重新预览，正式命名回显继续带版本', async t => {
  const { state: s, requests } = await harness(t, { rename: true })
  s.openSubtitles()
  s.selectSubtitles([library('video-1', 'HKG')], [])
  const before = s.snapshotKey
  await s.start()
  assert.match(s.notice, /最终命名有变化/)
  assert.equal(s.externalName(s.selectedLibrary[0]), 'Official.Show.S01E01.HKG.zh-CN.ass')
  s.externalSubtitles[0].version = 'KTXP'
  assert.notEqual(s.snapshotKey, before)
  assert.equal(s.externalName(s.selectedLibrary[0]), 'Show.S01E01.KTXP.zh-CN.ass')
  await s.start()
  const previews = requests.filter(request => request.endpoint.endsWith('/preview'))
  assert.equal(previews.length, 2)
  assert.deepEqual(previews.map(request => request.body.subtitles[0].version), ['HKG', 'KTXP'])
  assert.equal(s.externalName(s.selectedLibrary[0]), 'Official.Show.S01E01.KTXP.zh-CN.ass')
  assert.equal(requests.some(request => request.endpoint.endsWith('/jobs')), false)
})

test('版本参与跨来源冲突检测，重复版本阻止提交，改回不同版本即解除', async t => {
  const { state: s, requests } = await harness(t)
  s.openSubtitles()
  s.selectSubtitles([library('video-1', 'HKG')], [local('video-1', 'hkg', { language: 'zh-CN', file: { name: '01.ass', data: new Uint8Array([1]), sha256: 'd'.repeat(64) } })])
  assert.match(s.localConflicts['video-1'], /同名/)
  assert.equal(s.submitDisabled, true)
  await s.start()
  assert.equal(requests.some(request => request.endpoint.endsWith('/preview')), false)
  s.localSubtitles[0].version = 'KTXP'
  assert.deepEqual(s.localConflicts, {})
  assert.equal(s.submitDisabled, false)
  s.externalSubtitles.push(library('video-1', 'HKG', { versionId: 'another-library' }))
  assert.match(s.localConflicts['video-1'], /同名/)
})

test('分季修改及重新打开保留双方版本，未选视频的字幕不进入载荷', async t => {
  const { state: s } = await harness(t, { seasons: [1, 2] })
  s.openSubtitles()
  s.selectSubtitles([library('video-1', 'HKG'), library('video-2', 'KTXP')], [local('video-2', 'HKG')])
  s.openSubtitles(1)
  s.selectSubtitles([library('video-1', 'KTXP')], [])
  s.openSubtitles(2)
  assert.deepEqual(s.scopedLibrary.map(sub => sub.version), ['KTXP'])
  assert.deepEqual(s.scopedLocal.map(sub => sub.version), ['HKG'])
  assert.equal(s.externalSubtitles.find(sub => sub.videoId === 'video-1').version, 'KTXP')
  s.rows.find(row => row.id === 'video-2').selected = false
  const payload = JSON.parse(JSON.stringify(s.resourceSelections()))
  assert.deepEqual(payload[0].subtitles.map(sub => [sub.videoId, sub.version]), [['video-1', 'KTXP']])
})

test('批量请求按资源归属还原视频 ID 并显式传递空版本', async t => {
  const { state: s, requests } = await harness(t, { batch: true })
  s.openSubtitles()
  s.selectSubtitles([library(`${hash(0)}:video-1`, 'HKG'), library(`${hash(1)}:video-1`, 'KTXP', { language: 'zh-TW' })], [local(`${hash(1)}:video-1`, '')])
  assert.deepEqual(s.localConflicts, {})
  await s.start()
  assert.equal(s.error, '')
  const { resources, accountId } = requests.find(request => request.endpoint === 'download-organize/batch/preview').body
  assert.equal(accountId, 'main')
  assert.deepEqual(resources.map(resource => resource.subtitles.map(sub => [sub.videoId, sub.version])), [[['video-1', 'HKG']], [['video-1', 'KTXP'], ['video-1', '']]])
  assert.deepEqual(requests.find(request => request.endpoint === 'download-organize/batch/jobs').body, { previewId: 'batch-2' })
})
