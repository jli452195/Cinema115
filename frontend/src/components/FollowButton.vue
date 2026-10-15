<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { followRequest, type FollowState } from '../types/follows'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string }>()
const state = ref<FollowState>()
const error = ref('')
const busy = ref(false)
const loading = ref(true)
const coolingDown = ref(false)
let cooldownTimer: ReturnType<typeof setTimeout> | undefined
let controller: AbortController | undefined
const available = computed(() => props.kind === 'movie' && state.value?.libraryState === 'present')
async function load() {
  controller?.abort(); const current = new AbortController(); controller = current
  clearTimeout(cooldownTimer); coolingDown.value = false
  state.value = undefined; error.value = ''; loading.value = true; busy.value = false
  try { const data = await followRequest(props.token, `/${props.kind}/${props.tmdbId}`, current.signal); if (!current.signal.aborted) state.value = data }
  catch (e) { if (!current.signal.aborted) error.value = e instanceof Error ? e.message : '追更状态读取失败' }
  finally { if (!current.signal.aborted) loading.value = false }
}
async function change(cancel = false) {
  const current = controller
  if (!current || current.signal.aborted || busy.value || coolingDown.value || loading.value || !state.value || (!cancel && available.value)) return
  busy.value = true; error.value = ''
  // 提交时固定影视编号，切换路由不能把响应写入另一部影视。
  const path = `/${props.kind}/${props.tmdbId}`
  try {
    const data = await followRequest(props.token, `${path}${cancel ? '/cancel' : ''}`, current.signal, 'POST')
    if (current.signal.aborted) return
    state.value = cancel ? { ...state.value, subscribed: false, request: null } : data
  } catch (e) {
    if (!current.signal.aborted) {
      error.value = e instanceof Error ? e.message : '追更操作失败'
      // 超时可能发生在提交之后，读取服务器状态，避免误认为没有保存。
      try { const data = await followRequest(props.token, path, current.signal); if (!current.signal.aborted) state.value = data } catch { /* 保留原错误供用户重试。 */ }
    }
  } finally {
    if (!current.signal.aborted) {
      busy.value = false
      coolingDown.value = true
      cooldownTimer = setTimeout(() => { coolingDown.value = false }, 1000)
    }
  }
}
watch(() => [props.token, props.kind, props.tmdbId], () => { void load() }, { immediate: true })
onBeforeUnmount(() => { controller?.abort(); clearTimeout(cooldownTimer) })
</script>

<template>
  <div class="space-y-2">
    <div class="flex flex-wrap items-center gap-3">
      <button
        type="button"
        :disabled="loading || busy || coolingDown || !state || (available && !state.subscribed)"
        :aria-label="state?.subscribed ? '取消追更' : undefined"
        class="follow-button group inline-flex items-center justify-center gap-2 px-5 py-2 text-sm font-medium rounded-full transition-colors duration-200 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-500 disabled:opacity-60 disabled:cursor-not-allowed"
        :data-state="state?.subscribed ? 'subscribed' : available ? 'available' : 'idle'"
        :class="[
          state?.subscribed
            ? 'bg-rose-50 text-rose-600 hover:bg-rose-100 hover:text-rose-700 dark:bg-rose-500/10 dark:text-rose-400 dark:hover:bg-rose-500/20'
            : available
              ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-400/10 dark:text-emerald-300'
              : 'bg-slate-100 text-slate-700 hover:bg-slate-200 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700 shadow-sm'
        ]"
        @click="state?.subscribed ? change(true) : change()"
      >
        <svg v-if="loading || busy" aria-hidden="true" class="animate-spin h-4 w-4" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
        </svg>

        <svg v-else-if="!state?.subscribed && !available" aria-hidden="true" class="h-4 w-4 transition-transform duration-200 group-active:scale-75" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4.318 6.318a4.5 4.5 0 000 6.364L12 20.364l7.682-7.682a4.5 4.5 0 00-6.364-6.364L12 7.636l-1.318-1.318a4.5 4.5 0 00-6.364 0z" />
        </svg>

        <svg v-else aria-hidden="true" class="h-4 w-4 transition-transform duration-200 group-active:scale-75" :class="{ 'group-hover:hidden group-focus-visible:hidden': state?.subscribed }" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor">
          <path fill-rule="evenodd" d="M3.172 5.172a4 4 0 015.656 0L10 6.343l1.172-1.171a4 4 0 115.656 5.656L10 17.657l-6.828-6.829a4 4 0 010-5.656z" clip-rule="evenodd" />
        </svg>

        <svg v-if="state?.subscribed && !loading && !busy" aria-hidden="true" class="h-4 w-4 hidden group-hover:block group-focus-visible:block" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4.318 6.318a4.5 4.5 0 000 6.364L12 20.364l7.682-7.682a4.5 4.5 0 00-6.364-6.364L12 7.636l-1.318-1.318a4.5 4.5 0 00-6.364 0z" />
          <line x1="4" y1="4" x2="20" y2="20" stroke-linecap="round" stroke-linejoin="round" stroke-width="2" />
        </svg>

        <span>
          <template v-if="loading">读取中</template>
          <template v-else-if="busy">处理中</template>
          <template v-else-if="state?.subscribed">
            <span class="group-hover:hidden group-focus-visible:hidden">已追更</span>
            <span class="hidden group-hover:inline group-focus-visible:inline">取消追更</span>
          </template>
          <template v-else>{{ available ? '已入库' : '追更' }}</template>
        </span>
      </button>
    </div>
    <p v-if="error" role="alert" class="text-xs text-red-600">{{ error }}<button v-if="!state" :disabled="loading" class="ml-2 underline" @click="load">重试</button></p>
  </div>
</template>

<style scoped>
@media (prefers-reduced-motion: reduce) {
  .animate-spin { animation: none; }
  .transition-colors, .transition-transform { transition: none; }
}
</style>
