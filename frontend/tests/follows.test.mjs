import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
import ts from 'typescript'
import { parse, compileScript } from '@vue/compiler-sfc'
import { createRenderer, createSSRApp, nextTick } from 'vue'
import { renderToString } from '@vue/server-renderer'

const require = createRequire(import.meta.url)
const dataModule = source => `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`
const compile = source => ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const followsURL = dataModule(compile(await readFile(new URL('../src/types/follows.ts', import.meta.url), 'utf8')))
const { isFollowCaughtUp, followLabel } = await import(followsURL)
const item = {
  kind: 'tv', tmdbId: 282326, title: '兰香如故', posterUrl: '', date: '2026-09-23',
  status: 'processing', effectiveStatus: 'caught_up', subscribedAt: '2026-09-23', note: '', libraryState: 'present',
  progress: { state: 'tracking', aired: 28, owned: 28, checkedAt: 1 },
}

test('已播28集全部入库、进度仍为tracking：使用后端已追平分类', () => {
  assert.equal(isFollowCaughtUp(item), true)
  assert.equal(followLabel(item), '已追平 · 继续追更')
  assert.equal(isFollowCaughtUp({ ...item, effectiveStatus: undefined }), true)
})

test('后端分类优先，前端不能把处理中或待处理重新归为已追平', () => {
  for (const effectiveStatus of ['pending', 'processing', 'closed', 'completed']) {
    assert.equal(isFollowCaughtUp({ ...item, effectiveStatus }), false)
  }
  for (const progress of [
    { ...item.progress, aired: 29 },
    { ...item.progress, aired: 0, owned: 0 },
    { ...item.progress, error: '等待复核' },
  ]) {
    assert.equal(isFollowCaughtUp({ ...item, effectiveStatus: undefined, progress }), false)
  }
})

const vueURL = pathToFileURL(require.resolve('vue/dist/vue.runtime.esm-bundler.js')).href
const stubURL = dataModule(`import { h } from '${vueURL}'; export default { setup: (_, { slots }) => () => h('span', slots.default?.()) }`)
const routerURL = dataModule(`export { default as RouterLink } from '${stubURL}'`)
const imageURL = dataModule('export const tmdbImageURL = value => value')
const source = await readFile(new URL('../src/components/FollowCard.vue', import.meta.url), 'utf8')
const { descriptor } = parse(source)
const compiled = compile(compileScript(descriptor, { id: 'follow-card-test', inlineTemplate: true }).content)
  .replaceAll(/from ["']vue["']/g, `from '${vueURL}'`)
  .replaceAll(/from ["']vue-router["']/g, `from '${routerURL}'`)
  .replaceAll(/from ["']\.\.\/types\/follows["']/g, `from '${followsURL}'`)
  .replaceAll(/from ["']\.\/FollowDialog.vue["']/g, `from '${stubURL}'`)
  .replaceAll(/from ["']\.\.\/utils\/tmdbImage["']/g, `from '${imageURL}'`)
const { default: FollowCard } = await import(dataModule(compiled))

test('个人和管理卡片都显示已追平并继续追更，不提示后续日期待定', async () => {
  for (const admin of [false, true]) {
    const html = await renderToString(createSSRApp(FollowCard, { item, admin }))
    assert.match(html, /已追平/)
    assert.match(html, /已入库 28\/28 集 · 继续追更/)
    assert.doesNotMatch(html, /待追齐|后续播出日期待定|处理中/)
  }
})

test('管理徽标与分组一致，异常复核保留为进度说明', async () => {
  const html = await renderToString(createSSRApp(FollowCard, {
    item: { ...item, effectiveStatus: 'pending', status: 'pending', progress: { ...item.progress, error: '等待复核' } }, admin: true,
  }))
  assert.match(html, /待处理/)
  assert.match(html, /等待自动复核入库信息/)
  assert.doesNotMatch(html, /已追平/)
})

const buttonSource = await readFile(new URL('../src/components/FollowButton.vue', import.meta.url), 'utf8')
const iconsURL = dataModule(`export { default as IconChevronDown, default as IconClock, default as IconHeart, default as IconHeartFilled, default as IconLoader2, default as IconRefresh, default as IconX } from '${stubURL}'`)
const buttonCompiled = compile(compileScript(parse(buttonSource).descriptor, { id: 'follow-button-test', inlineTemplate: true }).content)
  .replaceAll(/from ["']vue["']/g, `from '${vueURL}'`)
  .replaceAll(/from ["']vue-router["']/g, `from '${routerURL}'`)
  .replaceAll(/from ["']\.\.\/types\/follows["']/g, `from '${followsURL}'`)
  .replaceAll(/from ["']@tabler\/icons-vue["']/g, `from '${iconsURL}'`)
const { default: FollowButton } = await import(dataModule(buttonCompiled))

// 用 Vue 实际渲染按钮并触发点击，只替换 DOM 与网络边界。
const node = (type, text = '') => ({ type, text, props: {}, children: [], parent: null, hidePopover() {} })
const renderer = createRenderer({
  createElement: type => node(type), createText: text => node('text', text), createComment: text => node('comment', text),
  setText: (target, text) => { target.text = text },
  setElementText: (target, text) => { target.text = text; target.children = [] },
  patchProp: (target, key, previous, value) => { target.props[key] = value },
  insert(target, parent, anchor = null) {
    if (target.parent) target.parent.children.splice(target.parent.children.indexOf(target), 1)
    const index = anchor ? parent.children.indexOf(anchor) : -1
    parent.children.splice(index < 0 ? parent.children.length : index, 0, target)
    target.parent = parent
  },
  remove(target) { target.parent.children.splice(target.parent.children.indexOf(target), 1); target.parent = null },
  parentNode: target => target.parent,
  nextSibling: target => target.parent?.children[target.parent.children.indexOf(target) + 1] || null,
})
const findButton = target => target.type === 'button' && (target.props['data-state'] || target.props.class?.includes('follow-button')) ? target : target.children.map(findButton).find(Boolean)
const findAvailable = target => target.props['data-state'] === 'available' ? target : target.children.map(findAvailable).find(Boolean)
const flush = async () => { await new Promise(resolve => setImmediate(resolve)); await nextTick() }

async function mountFollowButton(t, kind, initialState, props = {}) {
  const requests = []
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    requests.push({ url, method: options.method })
    const data = options.method === 'POST'
      ? url.endsWith('/cancel') ? { ok: true } : { ...initialState, subscribed: true }
      : initialState
    return { ok: true, json: async () => data }
  })
  const root = node('root')
  const app = renderer.createApp(FollowButton, { token: 'test-token', kind, tmdbId: '94664', ...props })
  app.mount(root)
  t.after(() => app.unmount())
  await flush()
  return { button: () => findButton(root), available: () => findAvailable(root), requests }
}

test('电影已入库统一为静态状态，不论是否有个人订阅', async t => {
  for (const subscribed of [false, true]) {
    await t.test(String(subscribed), async t => {
      const { button, available, requests } = await mountFollowButton(t, 'movie', { subscribed, request: null, libraryState: 'present' })
      assert.equal(button(), undefined)
      assert.equal(available().type, 'span')
      assert.equal(requests.length, 1)
    })
  }
})

test('电影按钮服从详情页可靠入库状态，旧申请结果不能覆盖', async t => {
  const { button, available } = await mountFollowButton(t, 'movie', { subscribed: true, request: { status: 'completed' }, libraryState: 'present' }, { libraryState: 'absent' })
  assert.equal(available(), undefined)
  assert.equal(button().props['data-state'], 'requested')
  assert.equal(button().props['aria-label'], '已申请，查看申请详情')
  assert.equal(button().props['aria-haspopup'], 'dialog')
})

test('电影详情页已入库优先于旧的未入库申请状态', async t => {
  const { available, button } = await mountFollowButton(t, 'movie', { subscribed: true, request: { status: 'processing' }, libraryState: 'absent' }, { libraryState: 'present' })
  assert.equal(button(), undefined)
  assert.equal(available().type, 'span')
})

test('电影入库未知时不能作为未入库发起申请', async t => {
  const { button, available, requests } = await mountFollowButton(t, 'movie', { subscribed: false, request: null, libraryState: 'absent' }, { libraryState: 'unknown' })
  assert.equal(button(), undefined)
  assert.equal(available(), undefined)
  assert.equal(requests.length, 1)
})

test('电影关闭申请仍可查看原因，不误报处理中或开放重新申请', async t => {
  const { button } = await mountFollowButton(t, 'movie', { subscribed: true, request: { status: 'closed', note: '暂无可用资源' }, libraryState: 'absent' })
  assert.equal(button().props['aria-label'], '申请已关闭，查看申请详情')
  assert.equal(button().props['aria-haspopup'], 'dialog')
})

test('已入库剧集仍可追更，同一按钮可取消追更', async t => {
  const { button, requests } = await mountFollowButton(t, 'tv', { subscribed: false, request: null, libraryState: 'present' })
  assert.equal(button().props.disabled, false)
  await button().props.onClick()
  await flush()
  assert.deepEqual(requests.at(-1), { url: '/api/me/follows/tv/94664', method: 'POST' })
  assert.equal(button().props['aria-label'], '取消追更')
  assert.equal(button().props.disabled, true)
  await button().props.onClick()
  assert.equal(requests.length, 2, '冷却期间重复点击不得发送取消请求')
  await new Promise(resolve => setTimeout(resolve, 1050))
  await flush()
  assert.equal(button().props.disabled, false)
  await button().props.onClick()
  await flush()
  assert.deepEqual(requests.at(-1), { url: '/api/me/follows/tv/94664/cancel', method: 'POST' })
  assert.equal(button().props.disabled, true)
})
