import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import ts from 'typescript'

const require = createRequire(import.meta.url)
const compiled = ts.transpileModule(await readFile(new URL('../src/composables/usePortalAuth.ts', import.meta.url), 'utf8'), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
}).outputText
const admin = { id: 'admin', username: 'admin', role: 'admin' }
const reader = { id: 'reader', username: 'reader', role: 'user' }
function tab(storage = new Map()) {
  const module = { exports: {} }, queue = [], requests = []
  const localStorage = { getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) }
  const fetch = (url, init) => { requests.push(init.headers.Authorization); return queue.shift()(url, init) }
  new Function('require', 'module', 'exports', 'localStorage', 'fetch', 'AbortSignal', compiled)(require, module, module.exports, localStorage, fetch, AbortSignal)
  return { ...module.exports, storage, requests, queue, reply: (user = admin, extra = {}, status = 200) => queue.push(async () => Response.json({ user, ...extra }, { status })) }
}
function deferred() {
  let resolve
  const promise = new Promise(r => { resolve = r })
  return { promise, resolve }
}

test('verified login is cached and concurrent probes share one request', async () => {
  const a = tab(), response = deferred()
  a.setPortalToken('original'); a.queue.push(() => response.promise)
  const first = a.ensurePortalSession(true), second = a.ensurePortalSession(true)
  assert.equal(a.requests.length, 1)
  response.resolve(Response.json({ user: admin }))
  assert.equal((await first).id, admin.id); assert.equal((await second).id, admin.id)
  await a.ensurePortalSession()
  assert.equal(a.requests.length, 1)
})

for (const status of [200, 401]) test(`late ${status} cannot erase or replace a newer login from another tab`, async () => {
  const a = tab(), b = tab(a.storage), response = deferred()
  a.setPortalToken('old'); a.queue.push(() => response.promise)
  const pending = a.ensurePortalSession(true)
  b.setPortalToken('new'); a.reply(reader)
  response.resolve(Response.json({ user: admin, token: 'stale-renewal' }, { status }))
  assert.equal((await pending).id, reader.id)
  assert.equal(a.storage.get('admin_token'), 'new')
  assert.equal(a.portalToken.value, 'new')
  assert.deepEqual(a.requests, ['Bearer old', 'Bearer new'])
})

test('conditional invalidation preserves a newer shared token without trusting it before verification', () => {
  const a = tab(), b = tab(a.storage)
  a.setPortalToken('old'); b.setPortalToken('new')
  a.clearPortalSession('old')
  assert.equal(a.portalToken.value, 'old')
  assert.equal(a.storage.get('admin_token'), 'new')
})

test('external renewal is verified before use and preserves same-user mounted identity', async () => {
  const a = tab(), b = tab(a.storage), response = deferred()
  a.setPortalToken('old'); a.reply(); await a.ensurePortalSession(true)
  const verified = a.portalUser.value
  b.setPortalToken('new'); a.queue.push(() => response.promise)
  const pending = a.ensurePortalSession(true)
  assert.equal(a.portalUser.value, verified)
  assert.equal(a.portalToken.value, 'old')
  response.resolve(Response.json({ user: admin }))
  await pending
  assert.equal(a.portalToken.value, 'new')
  assert.equal(a.portalUser.value.id, verified.id)
})

for (const external of [false, true]) test(`logout prevents in-flight renewal resurrection (${external ? 'other' : 'same'} tab)`, async () => {
  const a = tab(), b = tab(a.storage), response = deferred()
  a.setPortalToken('old'); a.queue.push(() => response.promise)
  const pending = a.ensurePortalSession(true)
  ;(external ? b : a).clearPortalSession()
  response.resolve(Response.json({ user: admin, token: 'renewed' }))
  assert.equal(await pending, undefined)
  assert.equal(a.portalToken.value, '')
  assert.equal(a.portalUser.value, undefined)
  assert.equal(a.storage.has('admin_token'), false)
})

test('logout while reading response body also prevents restoring a session', async () => {
  const a = tab(), body = deferred(), reading = deferred()
  a.setPortalToken('old')
  a.queue.push(async () => ({ status: 200, ok: true, json: () => { reading.resolve(); return body.promise } }))
  const pending = a.ensurePortalSession(true)
  await reading.promise
  a.storage.clear()
  body.resolve({ user: admin, token: 'renewed' })
  assert.equal(await pending, undefined)
  assert.equal(a.portalToken.value, '')
})

test('new-token verification does not wait for or get cleared by an old request', async () => {
  const a = tab(), old = deferred(), fresh = deferred()
  a.setPortalToken('old'); a.queue.push(() => old.promise)
  const first = a.ensurePortalSession(true)
  a.setPortalToken('new'); a.queue.push(() => fresh.promise)
  const second = a.ensurePortalSession(true)
  old.resolve(Response.json({ user: admin }))
  await Promise.resolve()
  const third = a.ensurePortalSession(true)
  assert.equal(a.requests.length, 2)
  fresh.resolve(Response.json({ user: reader }))
  for (const p of [first, second, third]) assert.equal((await p).id, reader.id)
})

test('renewal persists the verified identity and does not clear anime caches', async () => {
  const a = tab()
  a.setPortalToken('old'); a.reply()
  await a.ensurePortalSession(true)
  const source = await readFile(new URL('../src/composables/useAnimeDiscovery.ts', import.meta.url), 'utf8')
  const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
  const module = { exports: {} }
  new Function('require', 'module', 'exports', code)(name => name === './usePortalAuth' ? a : require(name), module, module.exports)
  module.exports.rememberAnimeScroll('calendar', 250)
  a.reply(admin, { token: 'renewed' })
  await a.ensurePortalSession(true)
  assert.equal(a.storage.get('admin_token'), 'renewed')
  assert.equal(a.portalUser.value.id, admin.id)
  assert.equal(module.exports.animeScroll('calendar'), 250)
  a.clearPortalSession()
  assert.equal(module.exports.animeScroll('calendar'), 0)
})

test('transient background failures preserve verified UI, but genuine 401 clears it', async () => {
  const a = tab()
  a.setPortalToken('valid'); a.reply(); await a.ensurePortalSession(true)
  for (const status of [503, 429, 408]) {
    a.reply(undefined, {}, status)
    await a.ensurePortalSession(true, { background: true })
    assert.equal(a.portalUser.value.id, admin.id)
    assert.equal(a.portalAuthError.value, '')
    assert.equal(a.storage.get('admin_token'), 'valid')
  }
  a.queue.push(async () => { throw new DOMException('timeout', 'TimeoutError') })
  await a.ensurePortalSession(true, { background: true })
  assert.equal(a.portalAuthError.value, '')
  a.reply(undefined, {}, 401); await a.ensurePortalSession(true)
  assert.equal(a.portalUser.value, undefined)
  assert.equal(a.storage.has('admin_token'), false)
})
