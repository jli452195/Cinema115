import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import ts from 'typescript'

const require = createRequire(import.meta.url)
const vue = require('vue')
const target = (id, accountId = 'main') => ({ id, accountId, name: id, targetCID: id, targetPath: '/' + id })
const first = target('animation'), second = target('movie')
const link = id => ({ url: 'https://115cdn.com/s/' + id })
const flush = () => new Promise(resolve => setImmediate(resolve))
const compile = source => ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText

function setup() {
  const memory = new Map(), calls = [], mounts = [], notices = []
  const toast = Object.fromEntries(['success', 'error', 'warning'].map(type => [type, (message, options) => notices.push({ type, message, options })]))
  const toastModule = { useToast: () => toast }
  globalThis.localStorage = { getItem: key => memory.get(key) ?? null, setItem: (key, value) => memory.set(key, value) }
  const api = {
    data: { accountId: 'main', profiles: [first] },
    shareTransferTargets: async () => api.data,
    transferShare: async (_token, snapshot, source) => { calls.push({ snapshot, source }); return { receipt: { targetPath: snapshot.targetPath } } },
  }
  const exports = {}
  const normalizeExports = {}
  new Function('exports', compile(readFileSync(new URL('../src/utils/shareLink.ts', import.meta.url), 'utf8')))(normalizeExports)
  new Function('require', 'exports', compile(readFileSync(new URL('../src/composables/useShareTransfer.ts', import.meta.url), 'utf8')))(name => name === './useToast' ? toastModule : name === '../lib/share-transfer' ? api : name === '../utils/shareLink' ? normalizeExports : require(name), exports)
  const renderer = vue.createRenderer({ createComment: () => ({}), insert() {}, remove() {}, parentNode() {}, nextSibling() {} })
  function mount() {
    const controller = new AbortController()
    let transfer
    const app = renderer.createApp({ setup() { transfer = exports.useShareTransfer('mock', controller.signal); return () => null } })
    app.mount({})
    mounts.push(() => { controller.abort(); app.unmount() })
    return transfer
  }
  function button(transfer, load) {
    const source = readFileSync(new URL('../src/components/ShareTransferButton.vue', import.meta.url), 'utf8').split('<script setup lang="ts">')[1].split('</script>')[0]
    const result = {}, props = { transfer, resource: { load } }
    new Function('require', 'exports', 'defineProps', compile(source + '\nexport { start, choose, choices, label, status, blocked, completed, hint };'))(name => name === '../composables/useToast' ? toastModule : name === './SelectMenu.vue' ? {} : require(name), result, () => props)
    return result
  }
  return { api, calls, notices, memory, mount, button, close: () => mounts.forEach(close => close()) }
}

test('单目录自动选中并记忆，多目录跨弹窗复用；删除或换号后重新选择', async () => {
  const env = setup()
  try {
    const one = env.mount(); await flush()
    assert.equal(one.target.value.id, first.id)
    env.api.data.profiles = [first, second]
    const two = env.mount(); await flush()
    assert.equal(two.target.value.id, first.id)
    two.select(second.id)
    const reopened = env.mount(); await flush()
    assert.equal(reopened.target.value.id, second.id)
    env.api.data.profiles = [first, target('other')]
    await reopened.load()
    assert.equal(reopened.target.value, undefined)
    env.api.data = { accountId: 'changed', profiles: [target('a', 'changed'), target('b', 'changed')] }
    await reopened.load()
    assert.equal(reopened.target.value, undefined)
    env.api.data.profiles = [target('a', 'changed')]
    await reopened.load()
    assert.equal(reopened.target.value.accountId, 'changed')
  } finally { env.close() }
})

test('首次有多个目录时必须先选目录，点击按钮不会直接发送', async () => {
  const env = setup()
  try {
    env.api.data.profiles = [first, second]
    const transfer = env.mount(); await flush()
    assert.equal(env.notices.length, 0)
    const button = env.button(transfer, async () => [link('one')])
    assert.equal(button.blocked.value, true)
    await button.start()
    assert.equal(env.calls.length, 0)
    transfer.select(first.id)
    await button.start()
    assert.equal(env.calls.length, 1)
    assert.equal(env.notices.length, 1)
    assert.equal(env.notices[0].type, 'success')
    assert.match(env.notices[0].message, /animation/)
    assert.equal(button.completed.value, true)
    const reopened = env.mount(); await flush()
    assert.equal(env.notices.length, 1)
    await reopened.submit(link('one'), first)
    assert.equal(env.notices.length, 1)
    assert.equal(button.label.value, '已转存')
    await button.start()
    assert.equal(env.calls.length, 1)
  } finally { env.close() }
})

test('等待取链时切换目录仍使用点击时的快照，下一次使用新目录', async () => {
  const env = setup()
  try {
    env.api.data.profiles = [first, second]
    const transfer = env.mount(); await flush(); transfer.select(first.id)
    let resolve
    const button = env.button(transfer, () => new Promise(done => { resolve = done }))
    const pending = button.start()
    transfer.select(second.id); await vue.nextTick()
    resolve([link('one')]); await pending
    assert.equal(env.calls[0].snapshot.id, first.id)
    assert.match(button.status.value.message, /animation/)
    assert.equal(button.blocked.value, false)
  } finally { env.close() }
})

test('多分享先明确选择，每个链接可分别转存，同一链接成功后不重复提交', async () => {
  const env = setup()
  try {
    const transfer = env.mount(); await flush()
    const button = env.button(transfer, async () => [link('one'), link('two')])
    await button.start()
    assert.equal(env.calls.length, 0)
    assert.equal(button.choices.value.length, 2)
    await button.choose('0'); await button.choose('0'); await button.choose('1')
    assert.deepEqual(env.calls.map(call => call.source.url), [link('one').url, link('two').url])
    transfer.select(first.id)
    await transfer.submit(link('two'), { ...first })
    assert.equal(env.calls.length, 2)
  } finally { env.close() }
})

test('取链完成后显示转存中并锁定当前提交，提取码两种表示共享成功回执', async () => {
  const env = setup()
  try {
    const transfer = env.mount(); await flush()
    let resolve
    env.api.transferShare = async (_token, snapshot, source) => { env.calls.push({ snapshot, source }); return new Promise(done => { resolve = done }) }
    const button = env.button(transfer, async () => [{ ...link('one'), password: 'abcd' }])
    const pending = button.start(); await flush()
    assert.equal(button.label.value, '转存中…')
    assert.equal(button.blocked.value, true)
    resolve({ receipt: { targetPath: first.targetPath } }); await pending
    await transfer.submit({ url: link('one').url + '?password=abcd' }, first)
    assert.equal(env.calls.length, 1)
  } finally { env.close() }
})

test('明确失败允许重试，不确定结果保留待核对并阻止重复转存', async () => {
  const env = setup()
  try {
    const transfer = env.mount(); await flush()
    let attempts = 0
    env.api.transferShare = async () => { attempts++; throw Object.assign(new Error('读取分享失败'), { status: 502, responseData: { outcomeUncertain: false } }) }
    await transfer.submit(link('one'), first); await transfer.submit(link('one'), first)
    assert.equal(attempts, 2)
    assert.deepEqual(env.notices.map(item => item.type), ['error', 'error'])
    assert.equal(transfer.status(transfer.linkID(link('one'))).state, 'failed')
    env.api.transferShare = async () => { attempts++; throw Object.assign(new Error('网络中断'), { status: 502, responseData: { outcomeUncertain: true } }) }
    await transfer.submit(link('two'), first); await transfer.submit(link('two'), first)
    assert.equal(attempts, 3)
    assert.equal(env.notices.at(-1).type, 'warning')
    assert.match(env.notices.at(-1).message, /animation/)
    assert.equal(transfer.status(transfer.linkID(link('two'))).state, 'uncertain')
    const reopened = env.mount(); await flush()
    await reopened.submit(link('two'), first)
    assert.equal(attempts, 3)
    assert.equal(env.notices.length, 3)
  } finally { env.close() }
})

test('取链失败使用错误 Toast，不提交转存；成功后不保留旧错误', async () => {
  const env = setup()
  try {
    const transfer = env.mount(); await flush()
    let failed = true
    const button = env.button(transfer, async () => {
      if (failed) throw new Error('获取链接失败')
      return [link('one')]
    })
    await button.start()
    assert.equal(env.calls.length, 0)
    assert.equal(env.notices[0].type, 'error')
    assert.equal(button.blocked.value, false)
    failed = false
    await button.start()
    assert.equal(button.label.value, '已转存')
    assert.equal(env.notices.at(-1).type, 'success')
  } finally { env.close() }
})
