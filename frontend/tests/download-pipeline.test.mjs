import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import { taskToastFixture } from './helpers/task-toast.mjs'

const moduleURL = source => `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`
const flush = () => new Promise(resolve => setImmediate(resolve))
const pipeline = (id, status, finished = false) => ({ id, status, finished, jobIds: [id], message: `${status}进度`, shares: [] })

async function setup() {
  const state = { calls: [], notices: [], timers: new Map(), next: 0, response: [], request: undefined }
  const fixture = taskToastFixture(state.notices)
  state.createTaskToast = fixture.createTaskToast
  globalThis.__downloadPipelineTest = state
  const mocks = moduleURL(`const state = globalThis.__downloadPipelineTest;
    export const fetchDownloadPipelines = async (token, ids) => {
      state.calls.push({ token, ids });
      return state.request ? state.request(token, ids) : state.response;
    };
    export const createTaskToast = state.createTaskToast;
    export const setTimeout = callback => { const id = ++state.next; state.timers.set(id, callback); return id; };
    export const clearTimeout = id => state.timers.delete(id);
    // ${Math.random()}`)
  let source = ts.transpileModule(await readFile(new URL('../src/composables/useDownloadPipelineToast.ts', import.meta.url), 'utf8'), {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext },
  }).outputText.replace("from './useTaskToast'", `from '${mocks}'`).replace("from '../lib/download-pipeline'", `from '${mocks}'`)
  source = `import { setTimeout, clearTimeout } from '${mocks}';\n${source}`
  const { trackDownloadPipeline } = await import(moduleURL(source))
  const tick = async () => {
    const [id, callback] = state.timers.entries().next().value || []
    assert.ok(callback, '存在下一次聚合轮询')
    state.timers.delete(id); callback(); await flush()
  }
  return { state, fixture, track: trackDownloadPipeline, tick }
}

test('下载、整理、STRM 交接不提前成功，收起后审核静默且完成仍通知', async () => {
  const { state, track, tick } = await setup()
  state.response = [pipeline('batch', 'downloading')]
  track('token', 'batch', '测试动画 · 2 项资源')
  await flush()
  const progress = state.notices[0]
  progress.onDismiss()
  for (const status of ['organizing', 'generating', 'review', 'generating']) {
    state.response = [pipeline('batch', status)]
    await tick()
    assert.ok(!state.notices.some(notice => notice.type === 'success'))
  }
  assert.equal(state.notices.length, 1)
  assert.equal(state.notices[0].title, '正在下载', '收起后的审核和其他阶段不能重新弹出')
  state.response = [pipeline('batch', 'success', true)]
  await tick()
  assert.equal(state.notices.at(-1).type, 'success')
  assert.notEqual(state.notices[0].id, progress.id)
  assert.equal(state.notices[0].duration, 6000)
  assert.ok(state.notices.every(notice => notice.description.includes('测试动画')))
  assert.equal(state.timers.size, 0)
})

test('同时提交批次合并查询，重复跟踪不重复通知或轮询', async () => {
  const { state, fixture, track, tick } = await setup()
  state.response = [pipeline('a', 'downloading'), pipeline('b', 'generating')]
  track('token', 'a', 'A'); track('token', 'a', 'A'); track('token', 'b', 'B')
  await flush()
  await tick()
  assert.deepEqual(state.calls.at(-1).ids, ['a', 'b'])
  assert.equal(fixture.history.filter(notice => notice.title === '已加入后台下载整理').length, 2)
  assert.equal(state.timers.size, 1)
  state.response = [pipeline('a', 'partial', true), pipeline('b', 'interrupted', true)]
  await tick()
  assert.equal(state.notices.at(-2).type, 'warning')
  assert.equal(state.notices.at(-1).type, 'warning')
  assert.equal(state.timers.size, 0)
})

test('新增任务清除旧定时器，等待请求完成后只保留一轮查询', async () => {
  const { state, track } = await setup()
  state.response = [pipeline('a', 'downloading')]
  track('token', 'a', 'A'); await flush()
  track('token', 'b', 'B'); await flush()
  assert.equal(state.timers.size, 1)
})

test('短暂查询失败可恢复，连续缺失记录提示核对而不宣告执行失败', async () => {
  const { state, track, tick } = await setup()
  state.request = async () => { throw new Error('网络中断') }
  track('token', 'a', 'A'); await flush()
  state.request = undefined; state.response = [pipeline('a', 'generating')]
  await tick()
  state.response = []
  for (let i = 0; i < 5; i++) await tick()
  assert.equal(state.notices.at(-1).type, 'warning')
  assert.match(state.notices.at(-1).description, /后台任务继续执行/)
  assert.equal(state.timers.size, 0)
  assert.ok(!state.notices.some(notice => notice.type === 'error' || notice.type === 'success'))
})

test('鉴权失效停止查询并提示重新登录', async () => {
  const { state, track } = await setup()
  state.request = async () => { throw Object.assign(new Error('登录失效'), { status: 401 }) }
  track('token', 'a', 'A'); await flush()
  assert.match(state.notices.at(-1).description, /重新登录/)
  assert.equal(state.timers.size, 0)
})

test('手动恢复产生新一轮跟踪，旧轮询的失败结果不能覆盖重试', async () => {
  const { state, track, tick } = await setup()
  let complete
  state.request = () => new Promise(resolve => { complete = resolve })
  track('token', 'a', 'A'); await flush()
  track('token', 'a', 'A 重试', true)
  complete([pipeline('a', 'failed', true)]); await flush()
  assert.ok(!state.notices.some(notice => notice.type === 'error'))
  state.request = undefined; state.response = [pipeline('a', 'generating')]
  await tick()
  assert.match(state.notices.at(-1).description, /A 重试/)
  state.response = [pipeline('a', 'success', true)]
  await tick()
  assert.equal(state.notices.at(-1).type, 'success')
  assert.equal(state.timers.size, 0)
})

test('重试接口逐条核验接受结果，不能把 HTTP 200 当作全成功', async () => {
  const state = { calls: [], response: { results: [{ id: 'a', accepted: true }, { id: 'b', accepted: false, reason: '原账号已变化' }] } }
  globalThis.__downloadPipelineAPI = state
  const mock = moduleURL(`const state = globalThis.__downloadPipelineAPI; export async function p115Request(...args) { state.calls.push(args); return state.response; }`)
  const source = ts.transpileModule(await readFile(new URL('../src/lib/download-pipeline.ts', import.meta.url), 'utf8'), {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext },
  }).outputText.replace("from './p115-api'", `from '${mock}'`)
  const { retryDownloadPipelineShares } = await import(moduleURL(source))
  await assert.rejects(retryDownloadPipelineShares('token', ['a', 'a', 'b']), /原账号已变化/)
  assert.deepEqual(JSON.parse(state.calls[0][2].body).ids, ['a', 'b'])
  await retryDownloadPipelineShares('token', ['a'])
  state.response = { results: [] }
  await assert.rejects(retryDownloadPipelineShares('token', ['missing']), /未确认重试结果/)
})

test('一个记录移出历史不影响其余进度，大量批次按接口上限分组', async () => {
  const calls = []
  globalThis.__downloadPipelineRead = async (_token, path) => {
    const ids = new URL(path, 'http://test/').searchParams.get('ids').split(',')
    calls.push(ids)
    if (ids.includes('missing')) throw Object.assign(new Error('已移出历史'), { status: 409 })
    return { pipelines: ids.map(id => pipeline(id, 'downloading')) }
  }
  const mock = moduleURL('export const p115Request = globalThis.__downloadPipelineRead;')
  const source = ts.transpileModule(await readFile(new URL('../src/lib/download-pipeline.ts', import.meta.url), 'utf8'), {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext },
  }).outputText.replace("from './p115-api'", `from '${mock}'`)
  const { fetchDownloadPipelines } = await import(moduleURL(source))
  const ids = Array.from({ length: 101 }, (_, i) => String(i))
  const result = await fetchDownloadPipelines('token', ['missing', ...ids])
  assert.deepEqual(result.map(item => item.id), ids)
  assert.ok(calls.every(ids => ids.length <= 100))
})
