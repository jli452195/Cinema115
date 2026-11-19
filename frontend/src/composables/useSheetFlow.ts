import { computed, inject, nextTick, onBeforeUnmount, provide, shallowReactive, shallowRef, type InjectionKey } from 'vue'
import type { DismissDecision, DismissReason } from './useMobileSheet'

interface SheetPage {
  id: string
  headingID: string
  getBody: () => HTMLElement | undefined
  getContainer: () => HTMLElement | undefined
  isBlocked: () => boolean
  beforeDismiss?: (reason: DismissReason) => DismissDecision | Promise<DismissDecision>
  onPop: () => void
}

interface RegisteredPage extends SheetPage {
  top: number
  focus: HTMLElement | null
}

interface SheetFlowOptions {
  rootId: string
  rootHeadingID: string
  getRootBody: () => HTMLElement | undefined
  getRootContainer: () => HTMLElement | undefined
  isRootBlocked: () => boolean
  beforeDismissRoot?: (reason: DismissReason) => DismissDecision | Promise<DismissDecision>
  beforeCloseAll?: () => DismissDecision | Promise<DismissDecision>
  getExpanded: () => boolean
  toggleExpanded: () => void
  // 全局关闭直接交给根宿主处理，不逐层触发子页返回。
  onCloseHost: () => void
}

function textField(element: HTMLElement) {
  return element instanceof HTMLTextAreaElement || element instanceof HTMLInputElement && !['checkbox', 'radio', 'button', 'submit', 'reset', 'file'].includes(element.type) || element.isContentEditable
}

function focusPage(page: RegisteredPage, remembered = false) {
  const container = page.getContainer()
  if (!container?.isConnected || !container.getClientRects().length) return
  const target = remembered ? page.focus : null
  // 触屏返回不主动唤起输入法，焦点仍限制在当前活动页内。
  if (target?.isConnected && container.contains(target) && target.getClientRects().length && !target.matches(':disabled') && !target.closest('[inert]') && !(window.matchMedia('(pointer: coarse)').matches && textField(target))) {
    target.focus({ preventScroll: true })
  } else {
    const fallback = container.querySelector<HTMLElement>('[data-sheet-back]:not(:disabled), [data-sheet-close]:not(:disabled)') || container
    fallback.focus({ preventScroll: true })
  }
}

export function provideSheetFlow(options: SheetFlowOptions) {
  const stage = shallowRef<HTMLElement>()
  const root: RegisteredPage = {
    id: options.rootId,
    headingID: options.rootHeadingID,
    getBody: options.getRootBody,
    getContainer: options.getRootContainer,
    isBlocked: options.isRootBlocked,
    beforeDismiss: options.beforeDismissRoot,
    onPop() {},
    top: 0,
    focus: null,
  }
  // 每个宿主各自持有页面，结束搜索会话即释放，不跨资源保留附件或表单。
  const pages = shallowReactive<RegisteredPage[]>([root])
  const current = computed(() => pages.at(-1)!)
  const depth = computed(() => pages.length)
  const headingID = computed(() => current.value.headingID)
  const blocked = computed(() => pages.some(page => page.isBlocked()))
  const expanded = computed(options.getExpanded)
  let disposed = false, pendingBack = false, navigationReadyAt = 0

  function snapshot(page: RegisteredPage) {
    page.top = page.getBody()?.scrollTop || 0
    const element = document.activeElement
    page.focus = element instanceof HTMLElement && page.getContainer()?.contains(element) ? element : null
  }

  function restore(page: RegisteredPage) {
    void nextTick(() => {
      if (disposed || current.value !== page) return
      const body = page.getBody()
      if (body) body.scrollTop = page.top
      focusPage(page, true)
    })
  }

  function registerPage(registration: SheetPage) {
    if (disposed) return () => {}
    snapshot(current.value)
    const page: RegisteredPage = { ...registration, top: 0, focus: null }
    pages.push(page)
    void nextTick(() => { if (!disposed && current.value === page) focusPage(page) })
    let removed = false
    return () => {
      if (removed || disposed) return
      removed = true
      const index = pages.indexOf(page)
      if (index < 1) return
      const wasCurrent = current.value === page
      pages.splice(index, 1)
      if (wasCurrent) {
        // 返回按钮在相同位置，忽略同一次快速双击落到上一页的第二下。
        navigationReadyAt = performance.now() + 300
        restore(current.value)
      }
    }
  }

  async function requestDismiss(reason: DismissReason = 'button'): Promise<DismissDecision> {
    if (disposed || pendingBack || performance.now() < navigationReadyAt || blocked.value) return 'blocked'
    const page = current.value
    pendingBack = true
    try {
      const decision = await page.beforeDismiss?.(reason) ?? 'dismiss'
      // 页内异步编辑器可能已切页或开始提交，不能再操作此前的栈顶。
      if (disposed || current.value !== page || blocked.value) return 'blocked'
      if (decision !== 'dismiss') {
        if (decision === 'handled') navigationReadyAt = performance.now() + 300
        return decision
      }
      if (pages.length === 1) return 'dismiss'
      page.onPop()
      await nextTick()
      return 'handled'
    } catch { return 'blocked' }
    finally { pendingBack = false }
  }

  function popToRoot() {
    if (disposed || pages.length === 1) return
    const children = pages.splice(1).reverse()
    navigationReadyAt = performance.now() + 300
    for (const page of children) page.onPop()
    restore(root)
  }

  async function requestCloseAll(): Promise<DismissDecision> {
    if (disposed || pendingBack || performance.now() < navigationReadyAt || blocked.value) return 'blocked'
    pendingBack = true
    try {
      const decision = await (options.beforeCloseAll?.() ?? 'dismiss')
      if (disposed || blocked.value) return 'blocked'
      if (decision !== 'dismiss') {
        if (decision === 'handled') navigationReadyAt = performance.now() + 300
        return decision
      }
    } catch {
      return 'blocked'
    } finally {
      pendingBack = false
    }
    options.onCloseHost()
    return 'dismiss'
  }

  const flow = {
    stage, depth, headingID, blocked, expanded,
    isActive: (id: string) => current.value.id === id,
    registerPage,
    requestDismiss,
    requestCloseAll,
    popToRoot,
    toggleExpanded: options.toggleExpanded,
  }
  provide(sheetFlowKey, flow)
  onBeforeUnmount(() => { disposed = true; pages.splice(1); stage.value = undefined })
  return flow
}

export type SheetFlow = ReturnType<typeof provideSheetFlow>
const sheetFlowKey: InjectionKey<SheetFlow> = Symbol('sheet-flow')

export function useSheetFlow() { return inject(sheetFlowKey, null) }
