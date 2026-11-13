import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const source = readFileSync(new URL('../src/composables/useToast.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText

function setup() {
  const calls = [], dismissed = [], exports = {}
  let counter = 0
  const sonner = { dismiss: id => dismissed.push(id), promise: (promise, options) => { calls.push({ promise, options }); return 'promise-id' } }
  for (const type of ['success', 'error', 'warning', 'info']) {
    sonner[type] = (title, options) => {
      const id = options.id ?? ++counter
      calls.push({ type, title, ...options, id })
      return id
    }
  }
  new Function('require', 'exports', compiled)(name => {
    assert.equal(name, 'vue-sonner')
    return { toast: sonner }
  }, exports)
  return { toast: exports.useToast(), second: exports.useToast(), calls, dismissed }
}

test('共享入口把四种类型、标题和正文交给 vue-sonner', () => {
  const { toast, second, calls } = setup()
  assert.equal(second, toast)
  for (const type of ['success', 'error', 'warning', 'info']) toast[type]('  通知正文  ', { title: '  配置结果  ' })
  assert.deepEqual(calls.map(({ type, title, description, duration }) => ({ type, title, description, duration })), [
    { type: 'success', title: '配置结果', description: '通知正文', duration: 3000 },
    { type: 'error', title: '配置结果', description: '通知正文', duration: 5000 },
    { type: 'warning', title: '配置结果', description: '通知正文', duration: 3000 },
    { type: 'info', title: '配置结果', description: '通知正文', duration: 3000 },
  ])
})

test('Promise 通知复用 vue-sonner 原生能力并保留返回值', () => {
  const { toast, calls } = setup()
  const promise = Promise.resolve('accepted')
  const options = { loading: '正在提交', success: '已提交', error: '提交失败' }
  assert.equal(toast.promise(promise, options), 'promise-id')
  assert.equal(calls[0].promise, promise)
  assert.equal(calls[0].options, options)
})

test('旧数字时长和零时长持续提示保持兼容，空消息不显示', () => {
  const { toast, calls } = setup()
  assert.equal(toast.info('   '), undefined)
  assert.equal(calls.length, 0)
  toast.success('已保存', 1200)
  toast.info('保持显示', { duration: 0 })
  toast.warning('负数按持续显示处理', -1)
  toast.error('默认错误时长', { title: '', duration: NaN })
  assert.deepEqual(calls.map(({ title, duration }) => [title, duration]), [
    ['操作成功', 1200], ['提示', Infinity], ['请注意', Infinity], ['操作失败', 5000],
  ])
  assert.ok(calls.every(call => call.action === undefined))
})

test('操作按钮保留标签和原始点击事件，正常关闭时释放去重记录', () => {
  const { toast, calls, dismissed } = setup()
  const received = []
  const action = { label: '查看任务', onClick: event => received.push(event) }
  const first = toast.success('已创建下载整理任务', { action })
  assert.equal(calls.at(-1).action.label, action.label)
  const event = new Event('click', { cancelable: true })
  calls.at(-1).action.onClick(event)
  assert.deepEqual(received, [event])
  assert.equal(received[0], event)
  assert.notEqual(toast.success('已创建下载整理任务', { action }), first)
  assert.deepEqual(dismissed, [])
})

test('点击事件预先取消或业务回调阻止默认行为时保留去重记录', () => {
  for (const preventBeforeCallback of [true, false]) {
    const { toast, calls, dismissed } = setup()
    const event = new Event('click', { cancelable: true })
    if (preventBeforeCallback) event.preventDefault()
    const action = {
      label: '查看任务',
      onClick: received => {
        assert.equal(received, event)
        if (!preventBeforeCallback) received.preventDefault()
      },
    }
    const first = toast.success('任务已提交', { action })
    calls.at(-1).action.onClick(event)
    assert.equal(event.defaultPrevented, true)
    assert.notEqual(toast.success('任务已提交', { action }), first)
    assert.deepEqual(dismissed, [first])
  }
})

test('业务点击回调抛错时透传异常并保留去重记录', () => {
  const { toast, calls, dismissed } = setup()
  const error = new Error('导航失败')
  const action = { label: '查看任务', onClick: () => { throw error } }
  const first = toast.success('任务已提交', { action })
  assert.throws(() => calls.at(-1).action.onClick(new Event('click')), thrown => thrown === error)
  assert.notEqual(toast.success('任务已提交', { action }), first)
  assert.deepEqual(dismissed, [first])
})

test('旧操作按钮的点击回调不能释放替代通知的去重记录', () => {
  const { toast, calls, dismissed } = setup()
  let clicked = 0
  const action = { label: '查看任务', onClick: () => { clicked++ } }
  const first = toast.success('任务已提交', { action })
  const oldClick = calls.at(-1).action.onClick
  const current = toast.success('任务已提交', { action })
  oldClick(new Event('click'))
  assert.equal(clicked, 1)
  assert.notEqual(toast.success('任务已提交', { action }), current)
  assert.deepEqual(dismissed, [first, current])
})

test('相同正文的不同操作按钮分别显示', () => {
  const { toast, dismissed } = setup()
  const onClick = () => {}
  const withoutAction = toast.success('任务已提交')
  const organize = toast.success('任务已提交', { action: { label: '查看整理任务', onClick } })
  const cloud = toast.success('任务已提交', { action: { label: '查看云下载', onClick } })
  assert.notEqual(organize, withoutAction)
  assert.notEqual(cloud, organize)
  assert.deepEqual(dismissed, [])
})

test('业务键区分同文案同按钮的不同目标，只替换同一目标的重复通知', () => {
  const { toast, calls, dismissed } = setup()
  const targets = []
  const firstAction = { label: '查看任务', onClick: () => targets.push('first') }
  const secondAction = { label: '查看任务', onClick: () => targets.push('second') }
  const first = toast.success('任务已提交', { key: 'organize:first', action: firstAction })
  const second = toast.success('任务已提交', { key: 'organize:second', action: secondAction })
  assert.notEqual(first, second)
  assert.deepEqual(dismissed, [])
  const replacement = toast.success('任务已提交', { key: 'organize:first', action: firstAction })
  assert.notEqual(replacement, first)
  assert.deepEqual(dismissed, [first])
  calls.find(call => call.id === second).action.onClick(new Event('click'))
  calls.at(-1).action.onClick(new Event('click'))
  assert.deepEqual(targets, ['second', 'first'])
})

test('相同通知替换旧卡并重新计时，不同标题和类型分别显示', () => {
  const { toast, calls, dismissed } = setup()
  const previous = toast.success('保存成功')
  const current = toast.success(' 保存成功 ', { duration: 4000 })
  assert.notEqual(current, previous)
  assert.deepEqual(dismissed, [previous])
  assert.equal(calls.at(-1).duration, 4000)
  assert.notEqual(toast.success('保存成功', { title: '网盘设置' }), current)
  assert.notEqual(toast.info('保存成功'), current)
  assert.deepEqual(dismissed, [previous])
})

test('持续通知改为自动关闭时使用新卡片和新时长', () => {
  const { toast, calls, dismissed } = setup()
  const persistent = toast.info('操作进度', { duration: 0 })
  assert.equal(calls.at(-1).duration, Infinity)
  const timed = toast.info('操作进度', { duration: 200 })
  assert.notEqual(timed, persistent)
  assert.equal(calls.at(-1).duration, 200)
  assert.deepEqual(dismissed, [persistent])
  const persistentAgain = toast.info('操作进度', { duration: 0 })
  assert.notEqual(persistentAgain, timed)
  assert.equal(calls.at(-1).duration, Infinity)
  assert.deepEqual(dismissed, [persistent, timed])
})

test('被替换卡片的旧退出回调不能释放当前通知', () => {
  const { toast, calls, dismissed } = setup()
  const previous = toast.success('相同内容')
  const oldCallback = calls.at(-1).onAutoClose
  const current = toast.success('相同内容')
  oldCallback()
  const next = toast.success('相同内容')
  assert.notEqual(next, current)
  assert.deepEqual(dismissed, [previous, current])
})

test('手动关闭和清空委托组件处理，新通知不受旧退出回调影响', () => {
  const { toast, calls, dismissed } = setup()
  const old = toast.success('相同内容')
  const oldCallback = calls.at(-1).onDismiss
  toast.dismiss(old)
  const current = toast.success('相同内容')
  oldCallback()
  const replacement = toast.success('相同内容')
  assert.notEqual(replacement, current)
  assert.deepEqual(dismissed, [old, current])
  toast.clear()
  const afterClear = toast.success('相同内容')
  assert.notEqual(afterClear, replacement)
  assert.deepEqual(dismissed, [old, current, undefined])
})

test('滑动或关闭按钮释放去重记录，不影响同内容其他类型', () => {
  const { toast, calls, dismissed } = setup()
  const success = toast.show('操作结果', 'success')
  const release = calls.at(-1).onDismiss
  const error = toast.error('操作结果')
  release()
  assert.notEqual(toast.success('操作结果'), success)
  assert.deepEqual(dismissed, [])
  assert.notEqual(toast.error('操作结果'), error)
  assert.deepEqual(dismissed, [error])
})
