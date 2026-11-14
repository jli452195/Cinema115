import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import ts from 'typescript'

const require = createRequire(import.meta.url), vue = require('vue')
const compiled = ts.transpileModule(readFileSync(new URL('../src/composables/useFollowPendingCount.ts', import.meta.url), 'utf8'), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
}).outputText
const flush = async () => { await vue.nextTick(); await new Promise(resolve => setImmediate(resolve)) }
function setup(t) {
  const originals = ['window', 'document'].map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)])
  const win = new EventTarget(), doc = new EventTarget()
  doc.hidden = false
  globalThis.window = win; globalThis.document = doc
  const mounted = [], unmounted = [], calls = []
  const auth = { portalToken: vue.ref('admin-a'), isPortalAdmin: vue.ref(true), portalAuthError: vue.ref('') }
  const exports = {}
  new Function('require', 'exports', compiled)(name => {
    if (name === 'vue') return { ...vue, onMounted: fn => mounted.push(fn), onBeforeUnmount: fn => unmounted.push(fn) }
    if (name === './usePortalAuth') return auth
    if (name === '../lib/follow-counts') return { fetchFollowPendingCount: (token, signal) => new Promise((resolve, reject) => calls.push({ token, signal, resolve, reject })) }
    throw new Error(name)
  }, exports)
  exports.useFollowPendingCountSync()
  mounted.forEach(fn => fn())
  t.after(() => {
    unmounted.forEach(fn => fn())
    for (const [key, value] of originals) {
      if (value) Object.defineProperty(globalThis, key, value)
      else delete globalThis[key]
    }
  })
  return { ...exports, auth, calls, win, doc }
}

test('操作后的强制刷新串行排在旧查询之后，临时失败保留，401清空并停用', async t => {
  const env = setup(t)
  const refresh = env.refreshFollowPendingCount(true)
  assert.equal(env.calls.length, 1)
  env.calls[0].resolve(3); await flush()
  assert.equal(env.calls.length, 2)
  assert.equal(env.followPendingCount.value, undefined, '操作前的旧结果不能覆盖最新状态')
  env.calls[1].resolve(2); await refresh
  assert.equal(env.followPendingCount.value, 2)
  const failed = env.refreshFollowPendingCount(true)
  env.calls[2].reject(new Error('网络异常')); await failed
  assert.equal(env.followPendingCount.value, 2)
  const unauthorized = env.refreshFollowPendingCount(true)
  env.calls[3].reject(Object.assign(new Error('登录失效'), { status: 401 })); await unauthorized
  assert.equal(env.followPendingCount.value, undefined)
  await env.refreshFollowPendingCount(true)
  assert.equal(env.calls.length, 4)
})

test('换号取消请求并丢弃旧响应，普通用户不查询，切回管理员恢复', async t => {
  const env = setup(t)
  env.auth.portalToken.value = 'admin-b'
  await flush()
  assert.equal(env.calls[0].signal.aborted, true)
  assert.equal(env.calls[1].token, 'admin-b')
  env.calls[0].resolve(99); await flush()
  assert.equal(env.followPendingCount.value, undefined)
  env.calls[1].resolve(4); await flush()
  assert.equal(env.followPendingCount.value, 4)
  env.auth.isPortalAdmin.value = false; await flush()
  assert.equal(env.followPendingCount.value, undefined)
  await env.refreshFollowPendingCount(true)
  assert.equal(env.calls.length, 2)
  env.auth.isPortalAdmin.value = true; await flush()
  assert.equal(env.calls.length, 3)
  env.calls[2].resolve(0); await flush()
  assert.equal(env.followPendingCount.value, 0)
})

test('前台每分钟查询、聚焦节流、后台暂停且恢复只启动一条查询', async t => {
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'], now: 1000000 })
  const env = setup(t)
  env.calls[0].resolve(3); await flush()
  env.win.dispatchEvent(new Event('focus'))
  assert.equal(env.calls.length, 1)
  t.mock.timers.tick(60000); await flush()
  assert.equal(env.calls.length, 2)
  env.calls[1].resolve(4); await flush()
  env.doc.hidden = true; env.doc.dispatchEvent(new Event('visibilitychange'))
  t.mock.timers.tick(120000); await flush()
  assert.equal(env.calls.length, 2)
  env.doc.hidden = false; env.doc.dispatchEvent(new Event('visibilitychange'))
  env.win.dispatchEvent(new Event('focus'))
  assert.equal(env.calls.length, 3)
  env.calls[2].resolve(5); await flush()
  assert.equal(env.followPendingCount.value, 5)
})
