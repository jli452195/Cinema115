import { test } from 'node:test'
import assert from 'node:assert/strict'
import { taskToastFixture } from './helpers/task-toast.mjs'

for (const close of ['onDismiss', 'onAutoClose', 'dismiss']) {
  test(`${close} 收起后阶段更新静默，终态使用新 ID 且只提醒一次`, () => {
    const env = taskToastFixture()
    const task = env.createTaskToast('正在下载', '作品 A')
    const progress = env.notices[0]
    assert.equal(progress.type, 'default')
    assert.equal(progress.duration, 5000)
    assert.equal(typeof progress.icon, 'function')
    if (close === 'dismiss') task.dismiss()
    else progress[close]()
    task.update('分享等待审核', '作品 A · 26 个视频')
    assert.equal(env.history.length, 1)
    task.finish('success', '整理完成')
    const result = env.notices[0]
    assert.notEqual(result.id, progress.id)
    assert.equal(result.duration, 6000)
    assert.equal(result.description, '作品 A · 26 个视频')
    progress.onDismiss()
    task.update('旧进度'); task.finish('error', '旧结果')
    assert.equal(env.notices.length, 1)
    assert.equal(env.history.length, 2)
    assert.equal(result.type, 'success')
  })
}

test('可见卡片完成也重新计时，旧关闭回调不影响其他任务或重试', () => {
  const env = taskToastFixture()
  const old = env.createTaskToast('准备中', '旧任务')
  const progress = env.notices[0]
  old.update('正在整理')
  assert.equal(env.notices[0].id, progress.id)
  old.finish('error', '整理失败', '请核对记录')
  const current = env.createTaskToast('正在下载', '新任务')
  progress.onAutoClose()
  current.update('正在整理')
  assert.deepEqual(env.notices.map(value => value.title), ['整理失败', '正在整理'])
  assert.equal(env.notices[0].description, '旧任务 · 请核对记录')
  assert.equal(env.notices[0].icon, undefined)
  assert.equal(env.notices[0].duration, 6000)
})
