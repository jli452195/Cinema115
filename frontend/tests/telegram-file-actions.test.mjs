import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed, ref } from 'vue'

const code = ts.transpileModule(readFileSync(new URL('../src/composables/useTelegramFileActions.ts', import.meta.url), 'utf8'), { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const batchModule = { exports: {} }
const batchCode = ts.transpileModule(readFileSync(new URL('../src/lib/cloud-download-batch.ts', import.meta.url), 'utf8'), { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
new Function('module', 'exports', batchCode)(batchModule, batchModule.exports)
const { readCloudBatch } = batchModule.exports
const files = [{ id: 'file-a', url: 'ed2k://a' }, { id: 'file-b', url: 'ed2k://b' }]
const manyFiles = (total = 545) => Array.from({ length: total }, (_, index) => ({ id: `file-${String(index).padStart(4, '0')}`, url: `ed2k://|file|Show.S01E${String(index + 1).padStart(3, '0')}.mkv|1000|${index.toString(16).padStart(32, '0')}|/` }))

function harness(options = {}) {
  const module = { exports: {} }, statuses = ref({}), writes = [], imports = []
  let configReads = 0, requestNumber = 0
  const state = { primaryAccountId: 'main', accountReady: true, settingsSaved: true, settings: { baseUrl: 'http://nas' }, targets: [{ id: 'tv', name: '剧集', outputDir: 'ed2k/剧集' }, { id: 'movie', name: '电影', outputDir: 'ed2k/电影' }] }
  const dependencies = {
    vue: { computed, ref },
    './useCloudDownloadAction': { useCloudDownloadAction: () => ({ statuses, busy: computed(() => Object.values(statuses.value).some(status => status.busy)), submit: async () => { throw new Error('此测试不走单文件接口') } }) },
    '../lib/cloud-download': { loadCloudDownloadConfig: async () => { configReads++; return { accountId: 'main', targetCID: '123', targetPath: '/Downloads' } }, cloudDownloadNotice: () => '已提交' },
    '../lib/cloud-download-batch': { readCloudBatch: async (response, onEvent) => options.receipts ? options.receipts(onEvent, writes.at(-1).body.urls.length) : readCloudBatch(response, onEvent) },
    '../lib/ed2k-strm-api': { createED2KSTRMAPI: () => ({ state: async () => state, imports: async input => { imports.push(input); if (options.imports) return options.imports(input, imports.length) } }) },
    '../utils/ed2kSTRM': { ed2kRequestID: () => `request-${++requestNumber}` },
  }
  const fetch = async (url, init) => {
    const body = JSON.parse(init.body)
    writes.push({ url, body })
    if (options.fetch) return options.fetch(url, init)
    const total = body.urls.length, events = [{ type: 'ready', total }]
    for (let index = 0; index < total; index++) events.push({ type: 'start', index, total }, { type: 'result', index, total, accepted: true })
    events.push({ type: 'done', total })
    return new Response(events.map(event => JSON.stringify(event)).join('\n') + '\n', { headers: { 'Content-Type': 'application/x-ndjson' } })
  }
  new Function('module', 'exports', 'require', 'fetch', code)(module, module.exports, name => {
    assert.ok(name in dependencies, `未模拟的依赖 ${name}`)
    return dependencies[name]
  }, fetch)
  const lifecycle = new AbortController()
  const actions = module.exports.useTelegramFileActions('mock-token', lifecycle.signal)
  return { actions, statuses, writes, imports, state, lifecycle, configReads: () => configReads }
}

test('批量云下载一次读取目录并一次提交整批，按文件保存成功回执', async () => {
  const context = harness()
  await context.actions.download(files)
  assert.equal(context.configReads(), 1)
  assert.deepEqual(context.writes, [{ url: '/api/admin/p115/cloud-download/tasks/batch?accountId=main', body: { urls: ['ed2k://a', 'ed2k://b'], targetCID: '123' } }])
  assert.equal(context.actions.cloudStatus(files[0]).success, true)
  assert.equal(context.actions.cloudStatus(files[1]).success, true)
  assert.equal(context.actions.cloudEligible(files[0]), false)
  assert.equal(context.actions.busy.value, false)
})

test('流中断保留已确认成功，未获回执的文件阻止直接重试', async () => {
  const context = harness({ receipts: (onEvent, total) => {
    onEvent({ type: 'start', index: 0, total })
    onEvent({ type: 'result', index: 0, total, accepted: true })
    onEvent({ type: 'start', index: 1, total })
    throw new Error('连接中断')
  } })
  await context.actions.download(files)
  assert.equal(context.actions.cloudStatus(files[0]).success, true)
  assert.equal(context.actions.cloudStatus(files[1]).uncertain, true)
  assert.equal(context.actions.cloudEligible(files[1]), false)
  assert.equal(context.actions.busy.value, false)
})

test('明确接收前拒绝保留可重试状态，错误回执总数不伪装完成', async () => {
  const rejected = harness({ receipts: () => { throw Object.assign(new Error('目录已失效'), { beforeStart: true }) } })
  await rejected.actions.download(files)
  assert.equal(rejected.actions.cloudEligible(files[0]), true)
  assert.equal(rejected.actions.cloudStatus(files[0]), undefined)
  const malformed = harness({ receipts: onEvent => onEvent({ type: 'ready', total: 1 }) })
  await malformed.actions.download(files)
  assert.equal(malformed.actions.cloudStatus(files[0]).uncertain, true)
  assert.match(malformed.actions.notice.value, /回执数量异常/)
})

test('STRM 多目录不静默选择，重试沿用标识，切换目标后可独立生成', async () => {
  const context = harness({ imports: (_input, count) => { if (count === 1) throw Object.assign(new Error('目录暂不可用'), { status: 409, responseData: { error: '目录暂不可用' } }) } })
  await context.actions.generate(files)
  assert.equal(context.imports.length, 0)
  assert.match(context.actions.notice.value, /请选择/)
  context.actions.targetID.value = 'tv'
  await context.actions.generate(files)
  await context.actions.generate(files)
  assert.equal(context.imports.length, 2)
  assert.equal(context.imports[0].requestId, context.imports[1].requestId)
  assert.equal(context.actions.strmStatus(files[0]).success, true)
  context.actions.targetID.value = 'movie'
  assert.equal(context.actions.strmEligible(files[0]), true)
  await context.actions.generate(files)
  assert.equal(context.imports[2].targetId, 'movie')
  assert.notEqual(context.imports[2].requestId, context.imports[1].requestId)
})

test('STRM 回执丢失后只重放完整原批次，原账号或目录变化时拒绝重放', async () => {
  const context = harness({ imports: (_input, count) => { if (count === 1) throw new TypeError('网络中断') } })
  context.actions.targetID.value = 'tv'
  await context.actions.generate(files)
  assert.equal(context.actions.strmStatus(files[0]).uncertain, true)
  await context.actions.generate([files[0]])
  assert.equal(context.imports.length, 1, '不确定批次不允许拆出新请求')
  await context.actions.retrySTRM()
  assert.equal(context.imports.length, 2)
  assert.deepEqual(context.imports[1], context.imports[0], '重放相同目标、请求编号与完整文件集合')
  const changed = harness({ imports: () => { throw new TypeError('网络中断') } })
  changed.actions.targetID.value = 'tv'
  await changed.actions.generate(files)
  changed.state.primaryAccountId = 'other'
  await changed.actions.retrySTRM()
  assert.equal(changed.imports.length, 1)
  assert.match(changed.actions.notice.value, /主账号或输出目录已变化/)
  const moved = harness({ imports: () => { throw new TypeError('网络中断') } })
  moved.actions.targetID.value = 'tv'
  await moved.actions.generate(files)
  moved.state.targets[0].outputDir = 'ed2k/另一目录'
  await moved.actions.retrySTRM()
  assert.equal(moved.imports.length, 1, '同一目录编号修改路径后也不得重放')
  assert.match(moved.actions.notice.value, /输出目录已变化/)
  moved.state.targets[0].outputDir = 'ed2k/剧集'
  moved.state.settings.baseUrl = 'http://other-nas'
  await moved.actions.retrySTRM()
  assert.equal(moved.imports.length, 1, '播放地址变化后不得重放')
  assert.match(moved.actions.notice.value, /播放地址已变化/)
})

test('545 个云下载文件整包提交一次并逐条取得成功回执，不做前端拆批', async () => {
  const context = harness(), rows = manyFiles()
  await context.actions.download(rows)
  assert.equal(context.configReads(), 1)
  assert.equal(context.writes.length, 1)
  assert.equal(context.writes[0].body.targetCID, '123')
  assert.deepEqual(context.writes[0].body.urls, rows.map(file => file.url))
  assert.equal(Object.keys(context.statuses.value).length, 545)
  for (const file of rows) {
    assert.equal(context.actions.cloudStatus(file).success, true)
    assert.equal(context.actions.cloudEligible(file), false)
  }
  assert.match(context.actions.notice.value, /已提交 545 \/ 545 个文件/)
  assert.equal(context.actions.busy.value, false)
})

test('545 个 STRM 文件一次完整提交，成功后不重复生成', async () => {
  const context = harness(), rows = manyFiles().reverse()
  context.actions.targetID.value = 'tv'
  await context.actions.generate(rows)
  assert.equal(context.imports.length, 1)
  assert.equal(context.imports[0].targetId, 'tv')
  assert.deepEqual(context.imports[0].links, [...rows].sort((a, b) => a.id.localeCompare(b.id)).map(file => file.url))
  for (const file of rows) assert.equal(context.actions.strmStatus(file).success, true)
  assert.match(context.actions.notice.value, /已接收 545 个文件/)
  await context.actions.generate(rows)
  assert.equal(context.imports.length, 1)
  assert.equal(context.actions.busy.value, false)
})

test('545 个 STRM 文件回执不确定时只重放同编号完整原包，不能改成子集请求', async () => {
  const context = harness({ imports: (_input, count) => { if (count === 1) throw new TypeError('连接中断') } }), rows = manyFiles()
  context.actions.targetID.value = 'tv'
  await context.actions.generate(rows)
  assert.equal(context.imports.length, 1)
  assert.equal(context.actions.pendingSTRM.value.files.length, 545)
  for (const file of rows) assert.equal(context.actions.strmStatus(file).uncertain, true)
  await context.actions.generate([rows[0]])
  await context.actions.generate(rows.slice(1))
  assert.equal(context.imports.length, 1)
  await context.actions.retrySTRM()
  assert.equal(context.imports.length, 2)
  assert.deepEqual(context.imports[1], context.imports[0])
  assert.equal(context.imports[1].links.length, 545)
  assert.equal(context.actions.pendingSTRM.value, undefined)
  for (const file of rows) assert.equal(context.actions.strmStatus(file).success, true)
})

test('请求体超限被明确拒绝时云下载和 STRM 均不进入接收不确定状态', async () => {
  const rows = manyFiles()
  const cloud = harness({ fetch: async () => Response.json({ error: '请求体过大，请减少内容' }, { status: 413 }) })
  await cloud.actions.download(rows)
  assert.equal(cloud.writes.length, 1)
  assert.equal(cloud.writes[0].body.urls.length, 545)
  assert.equal(Object.keys(cloud.statuses.value).length, 0)
  assert.equal(cloud.actions.cloudEligible(rows[0]), true)
  assert.match(cloud.actions.notice.value, /请求体过大/)
  const strm = harness({ imports: () => { throw Object.assign(new Error('请求体过大，请减少内容'), { status: 413, responseData: { error: '请求体过大，请减少内容' } }) } })
  strm.actions.targetID.value = 'tv'
  await strm.actions.generate(rows)
  assert.equal(strm.imports.length, 1)
  assert.equal(strm.actions.pendingSTRM.value, undefined)
  for (const file of rows) {
    assert.equal(strm.actions.strmStatus(file).uncertain, false)
    assert.equal(strm.actions.strmEligible(file), true)
  }
  assert.match(strm.actions.notice.value, /请求体过大/)
})

test('大批量云下载忙时拒绝重复提交，取消保留未确认状态，已取消生命周期不再提交', async () => {
  let enter
  const started = new Promise(resolve => { enter = resolve })
  const context = harness({ fetch: async (_url, init) => {
    enter()
    return new Promise((_, reject) => init.signal.addEventListener('abort', () => reject(init.signal.reason), { once: true }))
  } }), rows = manyFiles()
  const pending = context.actions.download(rows)
  await started
  await context.actions.download(rows)
  assert.equal(context.writes.length, 1)
  context.actions.stop()
  await pending
  for (const file of rows) assert.equal(context.actions.cloudStatus(file).uncertain, true)
  assert.equal(context.actions.busy.value, false)
  assert.match(context.actions.notice.value, /已停止后续提交/)
  context.lifecycle.abort()
  await context.actions.download(rows)
  await context.actions.generate(rows)
  assert.equal(context.writes.length, 1)
  assert.equal(context.imports.length, 0)
})
