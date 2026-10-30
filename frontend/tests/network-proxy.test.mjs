import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import ts from 'typescript'

const require = createRequire(import.meta.url)
function load(file, resolve = require) {
  const code = ts.transpileModule(readFileSync(new URL(file, import.meta.url), 'utf8'), { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
  const module = { exports: {} }
  new Function('require', 'module', 'exports', code)(resolve, module, module.exports)
  return module.exports
}
const { networkProxyRequest } = load('../src/lib/network-proxy-api.ts')
const { animeImageURL } = load('../src/composables/useAnimeDiscovery.ts', id => id === './usePortalAuth' ? { portalUser: require('vue').ref(), portalToken: require('vue').ref('') } : require(id))
const input = { enabled: true, url: 'socks5h://proxy.example:1080', username: 'operator', services: ['tmdb'] }

test('settings and manual tests use authenticated same-origin APIs and keep password omission distinct from clearing', async t => {
  const calls = []
  t.mock.method(globalThis, 'fetch', async (url, init) => { calls.push({ url, ...init }); return Response.json({ enabled: true }) })
  const signal = new AbortController().signal
  await networkProxyRequest('test-token', signal)
  await networkProxyRequest('test-token', signal, input)
  await networkProxyRequest('test-token', signal, { ...input, password: '', service: 'tmdb' }, true)
  assert.equal(calls[0].url, '/api/admin/network-proxy')
  assert.equal(calls[0].method, 'GET'); assert.equal(calls[0].body, undefined)
  assert.equal(calls[1].headers.Authorization, 'Bearer test-token')
  assert.equal(calls[1].cache, 'no-store')
  assert.equal('password' in JSON.parse(calls[1].body), false)
  assert.equal(calls[2].url, '/api/admin/network-proxy/test')
  assert.deepEqual(JSON.parse(calls[2].body), { ...input, password: '', service: 'tmdb' })
})

test('expired authentication and non-JSON gateway failures produce readable errors', async t => {
  const replies = [Response.json({ error: 'unauthorized' }, { status: 401 }), new Response('<html>gateway</html>', { status: 502 }), new Response('invalid JSON')]
  t.mock.method(globalThis, 'fetch', async () => replies.shift())
  const signal = new AbortController().signal
  await assert.rejects(networkProxyRequest('test-token', signal), /登录已过期/)
  await assert.rejects(networkProxyRequest('test-token', signal), /HTTP 502/)
  await assert.rejects(networkProxyRequest('test-token', signal), /有效数据/)
})

test('a cancelled test never accepts a late successful response', async t => {
  let release
  t.mock.method(globalThis, 'fetch', () => new Promise(resolve => { release = resolve }))
  const controller = new AbortController()
  const pending = networkProxyRequest('test-token', controller.signal, { ...input, service: 'tmdb' }, true)
  controller.abort(new DOMException('cancelled', 'AbortError'))
  release(Response.json({ reachable: true }))
  await assert.rejects(pending, { name: 'AbortError' })
})

test('anime covers accept only the four NAS image routes and never fall back to upstream HTTPS', () => {
  for (const value of ['/api/bangumi/image/m-06-8c/278826_test.jpg', '/api/bangumi/image/l-AB-cD/cover.webp', '/api/mikan/image/202610/cover.jpg', '/api/anibt/image/anime/' + 'a'.repeat(64) + '.webp', '/api/garden/image/565701/poster.jpeg']) assert.equal(animeImageURL(value), value)
  for (const value of ['', 'https://lain.bgm.tv/pic/cover/m/06/8c/278826_test.jpg', 'https://mikanani.me/images/test.jpg', 'https://example.com/cover.jpg', '//example.com/cover.jpg', '/api/bangumi/image/x-06-8c/cover.jpg', '/api/bangumi/image/m-06-8c/../cover.jpg', '/api/bangumi/image/m-06-8c/cover.jpg?url=https://example.com']) assert.equal(animeImageURL(value), '')
})
