import { readFileSync } from 'node:fs'
import ts from 'typescript'

const compiled = ts.transpileModule(readFileSync(new URL('../../src/composables/useTaskToast.ts', import.meta.url), 'utf8'), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
}).outputText

// 执行真实任务通知控制器，只替换通知渲染依赖。
export function taskToastFixture(notices = []) {
  let next = 0
  const dismissed = [], history = []
  const show = (type, title, options) => {
    const id = options.id ?? ++next
    const notice = { type, title, message: title, ...options, id }
    const current = notices.find(value => value.id === id)
    if (current) Object.assign(current, notice)
    else notices.push(notice)
    history.push({ ...notice })
    return id
  }
  const toast = (title, options) => show('default', title, options)
  for (const type of ['success', 'error', 'warning', 'info']) toast[type] = (title, options) => show(type, title, options)
  toast.dismiss = id => {
    dismissed.push(id)
    const index = notices.findIndex(value => value.id === id)
    if (index >= 0) notices.splice(index, 1)
  }
  const exports = {}
  new Function('require', 'exports', compiled)(name => {
    if (name === 'vue-sonner') return { toast }
    if (name === 'vue') return { h: (...args) => args }
    if (name === '@tabler/icons-vue') return { IconLoader2: {} }
    throw new Error(`未模拟依赖 ${name}`)
  }, exports)
  return { ...exports, notices, dismissed, history }
}
