import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import ts from 'typescript'

const source = readFileSync(new URL('../src/composables/useToast.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const require = createRequire(import.meta.url)
function setup(t) {
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'], now: 0 })
  const exports = {}
  new Function('require', 'exports', compiled)(require, exports)
  const toast = exports.useToast()
  t.after(toast.clear)
  return { toast, second: exports.useToast(), tick: milliseconds => t.mock.timers.tick(milliseconds) }
}

test('all components share one bounded queue and duplicates refresh without adding cards', t => {
  const { toast, second, tick } = setup(t)
  const first = toast.success('  保存成功  ')
  assert.equal(second, toast)
  tick(2000)
  assert.equal(second.success('保存成功'), first)
  assert.equal(toast.messages.value.length, 1)
  tick(2999)
  assert.equal(toast.messages.value.length, 1)
  tick(1)
  assert.equal(toast.messages.value.length, 0)
  const oldest = toast.info('一', 0)
  toast.info('二', 0); toast.info('三', 0); toast.info('四', 0)
  assert.deepEqual(toast.messages.value.map(item => item.message), ['二', '三', '四'])
  toast.resume(oldest); toast.dismiss(oldest)
  assert.equal(toast.messages.value.length, 3)
})

test('hover and keyboard focus independently pause, then resume only the remaining time', t => {
  const { toast, tick } = setup(t)
  const id = toast.info('正在阅读', 3000)
  tick(1200); toast.pause(id, 'hover')
  tick(5000); toast.pause(id, 'focus'); toast.pause(id, 'focus')
  toast.resume(id, 'hover'); tick(5000)
  assert.equal(toast.messages.value.length, 1)
  toast.resume(id, 'focus'); tick(1000)
  toast.pause(id, 'focus'); tick(10000); toast.resume(id, 'focus')
  tick(799); assert.equal(toast.messages.value.length, 1)
  tick(1); assert.equal(toast.messages.value.length, 0)
})

test('repeated resume events do not restart an active countdown', t => {
  const { toast, tick } = setup(t)
  const id = toast.warning('提醒', 1000)
  tick(500); toast.resume(id); toast.resume(id, 'focus')
  tick(500); assert.equal(toast.messages.value.length, 0)
})

test('repeating a paused message keeps it paused and resets its future reading time', t => {
  const { toast, tick } = setup(t)
  const id = toast.error('连接失败', { title: 'Telegram', duration: 1000 })
  tick(400); toast.pause(id, 'focus')
  assert.equal(toast.error('连接失败', { title: 'Telegram', duration: 2000 }), id)
  tick(3000); assert.equal(toast.messages.value.length, 1)
  toast.resume(id, 'focus'); tick(1999)
  assert.equal(toast.messages.value[0].title, 'Telegram')
  tick(1); assert.equal(toast.messages.value.length, 0)
})

test('persistent notices, clearing, and manual dismissal release their countdowns', t => {
  const { toast, tick } = setup(t)
  const persistent = toast.info('持续提醒', { duration: 0 })
  tick(100000); toast.pause(persistent); toast.resume(persistent); tick(100000)
  assert.equal(toast.messages.value.length, 1)
  toast.dismiss(persistent)
  const dismissed = toast.success('已关闭', 1000)
  toast.dismiss(dismissed); toast.error('清理', 1000); toast.clear()
  const current = toast.info('新通知', 3000)
  tick(1000); assert.equal(toast.messages.value[0].id, current)
  tick(2000); assert.equal(toast.messages.value.length, 0)
})

test('empty messages are ignored and ordinary errors retain the longer default duration', t => {
  const { toast, tick } = setup(t)
  assert.equal(toast.info('   '), undefined)
  toast.error('错误')
  tick(3000); assert.equal(toast.messages.value.length, 1)
  tick(2000); assert.equal(toast.messages.value.length, 0)
})
