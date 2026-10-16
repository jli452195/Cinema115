<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import type { EmbyMissingNotice } from '../types/emby'

const props = defineProps<{ notices: EmbyMissingNotice[] }>()
const id = useId(), trigger = ref<HTMLButtonElement>(), panel = ref<HTMLElement>(), closeButton = ref<HTMLButtonElement>()
const open = ref(false), position = ref<Record<string, string>>({})
const label = computed(() => {
  const notices = props.notices
  if (notices.some(item => item.code === 'sync_running')) return '媒体库同步中'
  const pending = notices.find(item => item.code === 'events_pending')
  if (pending) return `通知处理中 ${pending.count}`
  const metadata = notices.find(item => item.code === 'metadata_incomplete')
  const episodes = notices.find(item => item.code === 'episodes_incomplete')
  if (metadata && episodes) return '资料待补充'
  if (metadata) return `资料待补充 ${metadata.count}`
  if (episodes) return `分集信息不全 ${episodes.count}`
  return notices.some(item => item.code === 'connection_unconfirmed') ? '连接待确认' : '索引说明'
})
function close(restoreFocus = false) {
  panel.value?.hidePopover()
  if (restoreFocus) trigger.value?.focus({ preventScroll: true })
}
function place() {
  if (!trigger.value || !panel.value) return
  const anchor = trigger.value.getBoundingClientRect(), margin = 12, viewport = window.visualViewport
  const leftEdge = (viewport?.offsetLeft || 0) + margin, topEdge = (viewport?.offsetTop || 0) + margin
  const rightEdge = (viewport?.offsetLeft || 0) + Math.min(document.documentElement.clientWidth, viewport?.width || innerWidth) - margin
  const bottomEdge = (viewport?.offsetTop || 0) + (viewport?.height || innerHeight) - margin
  const width = Math.max(0, Math.min(360, rightEdge - leftEdge)), maxHeight = Math.max(0, bottomEdge - topEdge)
  // Measure at the target width before choosing above/below, including after resize.
  panel.value.style.width = width + 'px'
  panel.value.style.maxHeight = maxHeight + 'px'
  const height = panel.value.offsetHeight
  const top = anchor.bottom + 8 + height <= bottomEdge ? anchor.bottom + 8 : Math.max(topEdge, anchor.top - height - 8)
  position.value = { left: Math.max(leftEdge, Math.min(anchor.right - width, rightEdge - width)) + 'px', top: Math.min(top, bottomEdge - height) + 'px', width: width + 'px', maxHeight: maxHeight + 'px' }
}
async function toggle() {
  if (open.value) { close(); return }
  panel.value?.showPopover()
  await nextTick()
  place()
  closeButton.value?.focus({ preventScroll: true })
}
function changed(event: Event) { open.value = (event as ToggleEvent).newState === 'open' }
function onFocusOut(event: FocusEvent) {
  if (event.relatedTarget instanceof Node && (panel.value?.contains(event.relatedTarget) || event.relatedTarget === trigger.value)) return
  close()
}
function onScroll(event: Event) {
  if (event.target instanceof Node && panel.value?.contains(event.target)) return
  close()
}
function onResize() { if (open.value) place() }
watch(() => props.notices, () => close())
onMounted(() => { window.addEventListener('resize', onResize); window.addEventListener('scroll', onScroll, true); window.visualViewport?.addEventListener('resize', onResize) })
onBeforeUnmount(() => { close(); window.removeEventListener('resize', onResize); window.removeEventListener('scroll', onScroll, true); window.visualViewport?.removeEventListener('resize', onResize) })
</script>

<template>
  <button ref="trigger" type="button" :aria-controls="id" aria-haspopup="dialog" :aria-expanded="open" class="inline-flex min-h-11 items-center gap-1.5 rounded-lg px-2 text-xs text-slate-500 transition-colors hover:bg-slate-100 hover:text-slate-800 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-slate-400 dark:text-slate-400 dark:hover:bg-slate-800 dark:hover:text-slate-200" @click="toggle">
    <svg aria-hidden="true" class="h-3.5 w-3.5 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="12" cy="12" r="9"/><path d="M12 11v6m0-10v.1"/></svg>
    {{ label }}
  </button>
  <div :id="id" ref="panel" popover="auto" role="dialog" :aria-labelledby="id + '-title'" class="missing-status-panel border border-slate-200 bg-white text-slate-700 shadow-xl dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300" :style="position" @toggle="changed" @keydown.esc.prevent.stop="close(true)" @focusout="onFocusOut">
    <div class="mb-2 flex items-center justify-between gap-3">
      <h2 :id="id + '-title'" class="text-sm font-semibold text-slate-900 dark:text-slate-100">索引状态</h2>
      <button ref="closeButton" type="button" aria-label="关闭索引状态" class="flex h-11 w-11 shrink-0 items-center justify-center rounded-lg text-slate-500 hover:bg-slate-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-slate-400 dark:hover:bg-slate-800" @click="close(true)"><svg aria-hidden="true" class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="m6 6 12 12M6 18 18 6"/></svg></button>
    </div>
    <div class="space-y-3 text-sm leading-6"><p v-for="notice in notices" :key="notice.code">{{ notice.message }}</p></div>
    <p class="mt-3 text-xs leading-5 text-slate-500 dark:text-slate-400">缺集按 Emby 已入库分集计算，无 TMDB 编号的剧集按各自 Emby 条目检测。</p>
    <RouterLink to="/emby/library" class="mt-2 inline-flex min-h-11 items-center text-sm text-blue-600 dark:text-blue-400">查看媒体库同步</RouterLink>
  </div>
</template>

<style scoped>
.missing-status-panel { position: fixed; inset: auto; width: 360px; max-width: calc(100vw - 24px); margin: 0; padding: 12px 16px; overflow: auto; border-radius: 16px; }
</style>
