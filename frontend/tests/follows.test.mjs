import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
import ts from 'typescript'
import { parse, compileScript } from '@vue/compiler-sfc'
import { createSSRApp } from 'vue'
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
