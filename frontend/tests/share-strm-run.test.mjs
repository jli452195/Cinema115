import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const vueURL = pathToFileURL(require.resolve('vue/dist/vue.runtime.esm-bundler.js')).href
const dataModule = source => `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`
const deferred = () => {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

async function setup(options = {}) {
  const memory = new Map()
  globalThis.localStorage = { getItem: key => memory.get(key) ?? null, setItem: (key, value) => memory.set(key, value) }
  let sequence = 0
  const submissions = [], followers = [], cancellations = [], notices = [], navigation = []
  const toast = Object.fromEntries(['success', 'error', 'warning'].map(type => [type, (message, options) => notices.push({ type, message, options })]))
  toast.promise = (promise, options) => {
    const notice = { type: 'loading', message: options.loading }
    notices.push(notice)
    promise.then(value => Object.assign(notice, { type: 'success', ...options.success(value) }), error => Object.assign(notice, { type: 'error', ...options.error(error) }))
  }
  const mock = {
    toast,
    router: { push: path => { navigation.push(path) } },
    ShareRunError: class extends Error {},
    newShareRunID: () => (++sequence).toString(16).padStart(32, '0'),
    savedSTRMSettings: async () => ({ outputDir: 'strm', baseUrl: 'http://nas', recursive: true }),
    submitShareRun: async (_token, id, inputs, scope) => {
      submissions.push({ id, inputs, scope })
      return { id, status: 'queued', readyAt: new Date(Date.now() + 5000).toISOString(), items: [{ taskId: id, status: 'queued', message: '' }], selectedTaskIds: [id] }
    },
    waitForShareRun: (_token, id, signal, progress) => {
      const pending = deferred()
      signal.addEventListener('abort', () => pending.reject(new Error('aborted')), { once: true })
      followers.push({ id, progress, ...pending })
      return pending.promise
    },
    cancelShareRun: async (_token, id) => { cancellations.push(id) },
  }
  globalThis.__shareRunTest = mock
  const mockURL = dataModule(`const mock = globalThis.__shareRunTest;
    export const useToast = () => mock.toast;
    export const useRouter = () => mock.router;
    export const ShareRunError = mock.ShareRunError;
    ${['cancelShareRun', 'newShareRunID', 'savedSTRMSettings', 'submitShareRun', 'waitForShareRun'].map(name => `export const ${name} = (...args) => mock.${name}(...args);`).join('\n')}
    // ${Math.random()}`)
  const normalizeURL = dataModule('export const normalize115Share = value => value')
  const source = await readFile(new URL('../src/composables/useShareSTRMRun.ts', import.meta.url), 'utf8')
  let compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
  compiled = compiled.replace("from 'vue'", `from '${vueURL}'`).replace("from '../utils/shareLink'", `from '${normalizeURL}'`).replace("from '../utils/shareGeneration'", `from '${mockURL}'`)
    .replace("from './useToast'", `from '${mockURL}'`).replace("from 'vue-router'", `from '${mockURL}'`)
  const { useShareSTRMRun } = await import(dataModule(compiled + `\n// ${Date.now()}-${Math.random()}`))
  const lifecycle = new AbortController()
  const runs = useShareSTRMRun('test', lifecycle.signal, { scope: 'bot/tv/test', ...options })
  return { runs, lifecycle, submissions, followers, cancellations, mock, mockURL, notices, navigation }
}

const resource = (id, load = async () => [{ url: `https://115cdn.com/s/${id}` }]) => ({ id, identity: `bot/${id}`, load })
const flush = () => new Promise(resolve => setImmediate(resolve))

test('搜索未结束可先选择，取链串行，已提交任务不锁其他资源', async () => {
  const search = deferred(), firstLoad = deferred(), entered = deferred()
  const env = await setup({ beforeLoad: () => search.promise })
  const order = []
  const first = resource('a', async () => { order.push('a'); entered.resolve(); await firstLoad.promise; return [{ url: 'https://115cdn.com/s/a' }] })
  const second = resource('b', async () => { order.push('b'); return [{ url: 'https://115cdn.com/s/b' }] })
  const p1 = env.runs.submit(first), p2 = env.runs.submit(second)
  await env.runs.submit(first)
  assert.equal(env.runs.pending.value.a, true)
  assert.equal(env.runs.pending.value.b, true)
  assert.equal(env.submissions.length, 0)
  search.resolve(); await entered.promise
  assert.deepEqual(order, ['a'])
  firstLoad.resolve(); await Promise.all([p1, p2])
  assert.deepEqual(order, ['a', 'b'])
  assert.equal(env.submissions.length, 2)
  assert.equal(env.followers.length, 2)
  assert.equal(env.runs.preparing.value, 0)
  assert.equal(env.runs.busy.value, true)
  assert.equal(env.submissions[0].scope, env.submissions[1].scope)
  assert.deepEqual(env.notices.map(notice => notice.message), ['已提交后台', '已提交后台'])
  env.lifecycle.abort(); await flush()
  assert.equal(env.cancellations.length, 0)
})

test('合并批次按来源分别显示结果，不串用其他资源的失败', async () => {
  const env = await setup()
  let next = 0
  env.mock.submitShareRun = async () => ({ id: 'c'.repeat(32), status: 'queued', selectedTaskIds: [++next === 1 ? 'one' : 'two'], items: [] })
  await env.runs.submit(resource('a')); await env.runs.submit(resource('b'))
  const one = env.followers[0], two = env.followers[1]
  const view = { id: 'batch', status: 'failed', items: [
    { taskId: 'one', status: 'success', result: { count: 1, directory: 'strm' } },
    { taskId: 'two', status: 'failed', error: '来源二失败' },
  ] }
  one.progress(view); two.progress(view); one.resolve(view); two.resolve(view)
  await flush()
  assert.equal(env.runs.statuses.value.a.success, true)
  assert.equal(env.runs.statuses.value.b.success, false)
  assert.match(env.runs.statuses.value.b.message, /来源二失败/)
  assert.doesNotMatch(env.runs.statuses.value.a.message, /来源二失败/)
  assert.equal(env.runs.busy.value, false)
  env.lifecycle.abort()
})

test('关闭页面取消尚未取链的选择，不主动取消后台任务', async () => {
  const search = deferred()
  const env = await setup({ beforeLoad: () => search.promise })
  const pending = env.runs.submit(resource('a'))
  env.lifecycle.abort(); search.resolve(); await pending
  assert.equal(env.submissions.length, 0)
  assert.equal(env.cancellations.length, 0)
  assert.equal(env.runs.preparing.value, 0)
  assert.deepEqual(env.notices.map(notice => [notice.type, notice.message]), [['info', '已取消准备']])
})

test('多个资源恢复时并行轮询，停止同批只请求一次', async () => {
  const env = await setup()
  const batchID = 'd'.repeat(32)
  let index = 0
  env.mock.submitShareRun = async () => ({ id: batchID, status: 'queued', selectedTaskIds: [String(++index)], items: [] })
  const items = [resource('a'), resource('b')]
  await env.runs.submit(items[0]); await env.runs.submit(items[1])
  env.lifecycle.abort(); await flush()
  const newController = new AbortController()
  // 复用同一持久化存储，模拟关闭弹窗后重新打开。
  const { useShareSTRMRun } = await import(dataModule(ts.transpileModule(
    (await readFile(new URL('../src/composables/useShareSTRMRun.ts', import.meta.url), 'utf8')),
    { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } },
  ).outputText.replace("from 'vue'", `from '${vueURL}'`)
    .replace("from './useToast'", `from '${env.mockURL}'`).replace("from 'vue-router'", `from '${env.mockURL}'`)
    .replace("from '../utils/shareLink'", `from '${dataModule('export const normalize115Share = value => value')}'`)
    .replace("from '../utils/shareGeneration'", `from '${dataModule('export const { cancelShareRun, newShareRunID, savedSTRMSettings, ShareRunError, submitShareRun, waitForShareRun } = globalThis.__shareRunTest; // restore')}'`)))
  const restored = useShareSTRMRun('test', newController.signal)
  await restored.restore(items)
  assert.equal(env.followers.length, 4)
  assert.equal(restored.pending.value.a, true)
  assert.equal(restored.pending.value.b, true)
  assert.equal(env.notices.length, 2, '恢复历史任务不重复弹出提交提示')
  await restored.stop()
  assert.deepEqual(env.cancellations, [batchID])
  newController.abort(); await flush()
})

test('Promise Toast 从准备更新到后台接收，实际生成结束后才通知完成', async () => {
  const loading = deferred(), env = await setup()
  const pending = env.runs.submit(resource('a', () => loading.promise))
  assert.equal(env.notices[0].type, 'loading')
  assert.equal(env.submissions.length, 0)
  loading.resolve([{ url: 'https://115cdn.com/s/a' }]); await pending
  assert.equal(env.notices.length, 1)
  assert.equal(env.notices[0].message, '已提交后台')
  assert.equal(env.runs.statuses.value.a.success, false)
  env.notices[0].action.onClick()
  assert.deepEqual(env.navigation, ['/115/share/logs'])
  const run = { status: 'success', items: [{ taskId: env.submissions[0].id, status: 'success', result: { count: 2 } }] }
  env.followers[0].progress(run); env.followers[0].resolve(run); await flush()
  assert.equal(env.notices[1].options.title, 'STRM 生成完成')
  assert.equal(env.notices[1].type, 'success')
  env.lifecycle.abort()
})

test('配置和取链失败更新为错误通知，准备队列可继续提交后续资源', async () => {
  const env = await setup()
  env.mock.savedSTRMSettings = async () => { throw new Error('请先配置 STRM') }
  await env.runs.submit(resource('config'))
  assert.equal(env.notices[0].type, 'error')
  assert.match(env.notices[0].description, /请先配置 STRM/)
  env.mock.savedSTRMSettings = async () => ({})
  await env.runs.submit(resource('link', async () => { throw new Error('取链失败') }))
  assert.equal(env.notices[1].type, 'error')
  assert.match(env.notices[1].description, /取链失败/)
  await env.runs.submit(resource('ok'))
  assert.equal(env.submissions.length, 1)
  assert.equal(env.notices[2].message, '已提交后台')
  env.lifecycle.abort(); await flush()
})

test('提交发出后关闭弹窗仍显示真实回执，不误报取消准备', async () => {
  const response = deferred(), entered = deferred(), env = await setup()
  env.mock.submitShareRun = async () => { entered.resolve(); return response.promise }
  const pending = env.runs.submit(resource('a'))
  await entered.promise; env.lifecycle.abort()
  response.resolve({ id: 'e'.repeat(32), status: 'queued', items: [] }); await pending
  assert.equal(env.notices[0].message, '已提交后台')
  assert.equal(env.followers.length, 0)
  assert.equal(env.runs.busy.value, false)
})

test('提交网络异常提示核对日志，不误报提交成功', async () => {
  const env = await setup()
  env.mock.submitShareRun = async () => { throw new Error('连接中断') }
  await env.runs.submit(resource('a'))
  assert.equal(env.notices[0].type, 'error')
  assert.match(env.notices[0].description, /若未收到确认/)
  assert.equal(env.runs.pending.value.a, undefined)
  env.lifecycle.abort(); await flush()
  assert.equal(env.notices.length, 1)
})

test('轮询中断提示进度不可用，恢复已完成任务不重复通知', async () => {
  const env = await setup()
  await env.runs.submit(resource('a'))
  env.followers[0].reject(new Error('网络中断')); await flush()
  assert.equal(env.notices[1].type, 'warning')
  assert.match(env.notices[1].message, /后台任务不会.*取消/)
  await env.runs.restore([resource('a')])
  const run = { status: 'success', items: [{ taskId: env.submissions[0].id, status: 'success', result: { count: 1 } }] }
  env.followers[1].progress(run); env.followers[1].resolve(run); await flush()
  assert.equal(env.runs.statuses.value.a.success, true)
  assert.equal(env.notices.length, 2)
  env.lifecycle.abort()
})
