import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
import ts from 'typescript'

const dataModule = source => `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`
const compile = source => ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const { observeNotificationVisibility } = await import(dataModule(compile(await readFile(new URL('../src/utils/notificationVisibility.ts', import.meta.url), 'utf8'))))

test('可见满一秒才已读；快速滚过、失焦、后台及卸载取消计时', t => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const original = Object.fromEntries(['document', 'window', 'IntersectionObserver'].map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]))
  t.after(() => {
    for (const [key, descriptor] of Object.entries(original)) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor)
      else delete globalThis[key]
    }
  })
  const doc = new EventTarget(), win = new EventTarget()
  let focused = true, callback, disconnected = false, seen = 0
  doc.hidden = false; doc.hasFocus = () => focused
  globalThis.document = doc; globalThis.window = win
  globalThis.IntersectionObserver = class {
    constructor(fn) { callback = fn }
    observe() {}
    disconnect() { disconnected = true }
  }
  const element = {}, stop = observeNotificationVisibility(element, () => seen++)
  const visibility = ratio => callback([{ target: element, isIntersecting: ratio > 0, intersectionRatio: ratio }])
  visibility(0.4); t.mock.timers.tick(2000); assert.equal(seen, 0)
  visibility(1); t.mock.timers.tick(900); visibility(0); t.mock.timers.tick(2000); assert.equal(seen, 0)
  visibility(1); t.mock.timers.tick(900); focused = false; win.dispatchEvent(new Event('blur'))
  t.mock.timers.tick(2000); assert.equal(seen, 0)
  focused = true; win.dispatchEvent(new Event('focus')); t.mock.timers.tick(900)
  doc.hidden = true; doc.dispatchEvent(new Event('visibilitychange')); t.mock.timers.tick(2000); assert.equal(seen, 0)
  doc.hidden = false; doc.dispatchEvent(new Event('visibilitychange'))
  t.mock.timers.tick(999); assert.equal(seen, 0)
  t.mock.timers.tick(1); assert.equal(seen, 1)
  visibility(0); visibility(1); t.mock.timers.tick(2000); assert.equal(seen, 1)
  stop(); assert.equal(disconnected, true)
  const stopOther = observeNotificationVisibility(element, () => seen++)
  visibility(1); stopOther(); t.mock.timers.tick(2000); assert.equal(seen, 1)
})

const require = createRequire(import.meta.url)
const vueURL = pathToFileURL(require.resolve('vue/dist/vue.runtime.esm-bundler.js')).href
const authURL = dataModule(`import { ref } from '${vueURL}'; export const portalToken = ref('test-token')`)
const notificationsSource = compile(await readFile(new URL('../src/composables/useWebsiteNotifications.ts', import.meta.url), 'utf8'))
  .replaceAll(/from ["']vue["']/g, `from '${vueURL}'`)
  .replaceAll(/from ["'].\/usePortalAuth["']/g, `from '${authURL}'`)
const { notificationRequest, readWebsiteNotifications, websiteUnread } = await import(dataModule(notificationsSource))
const { portalToken } = await import(authURL)
const response = data => ({ ok: true, json: async () => data })
const flush = () => new Promise(resolve => setImmediate(resolve))

test('已读后忽略旧轮询计数，排队串行提交，换号后忽略旧响应', async t => {
  const calls = []
  t.mock.method(globalThis, 'fetch', (url, options) => new Promise(resolve => calls.push({ url, options, resolve })))
  const signal = new AbortController().signal
  const oldPoll = notificationRequest('/unread', signal)
  const read = readWebsiteNotifications({ ids: [1, 2] }, signal)
  await flush()
  calls[1].resolve(response({ ok: true, unread: 1 }))
  await read
  assert.equal(websiteUnread.value, 1)
  calls[0].resolve(response({ unread: 3 })); await oldPoll
  assert.equal(websiteUnread.value, 1, '旧响应使角标反弹')
  const first = readWebsiteNotifications({ ids: [3] }, signal)
  const second = readWebsiteNotifications({ ids: [4] }, signal)
  await flush(); assert.equal(calls.length, 3)
  calls[2].resolve(response({ ok: true, unread: 0 })); await first
  await flush(); assert.equal(calls.length, 4)
  calls[3].resolve(response({ ok: true, unread: 0 })); await second
  const pending = notificationRequest('/unread', signal)
  portalToken.value = 'another-user'
  calls[4].resolve(response({ unread: 99 }))
  await assert.rejects(pending, /登录状态已改变/)
  assert.equal(websiteUnread.value, 0)
})
