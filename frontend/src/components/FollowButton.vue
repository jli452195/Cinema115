<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { followLabel, followRequest, type FollowState } from '../types/follows'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string }>()
const state = ref<FollowState>()
const error = ref('')
const busy = ref(false)
const loading = ref(true)
const notice = ref('')
let controller: AbortController | undefined
const available = computed(() => props.kind === 'movie' && state.value?.libraryState === 'present')
const label = computed(() => busy.value ? '处理中…' : loading.value ? '读取追更状态…' : available.value ? '已入库' : state.value?.subscribed ? '已追更' : '追更')
async function load() {
  controller?.abort(); const current = new AbortController(); controller = current
  state.value = undefined; error.value = ''; notice.value = ''; loading.value = true; busy.value = false
  try { const data = await followRequest(props.token, `/${props.kind}/${props.tmdbId}`, current.signal); if (!current.signal.aborted) state.value = data }
  catch (e) { if (!current.signal.aborted) error.value = e instanceof Error ? e.message : '追更状态读取失败' }
  finally { if (!current.signal.aborted) loading.value = false }
}
async function change(cancel = false) {
  const current = controller
  if (!current || current.signal.aborted || busy.value || loading.value || !state.value) return
  busy.value = true; error.value = ''; notice.value = ''
  // 提交时固定影视编号，切换路由不能把响应写入另一部影视。
  const path = `/${props.kind}/${props.tmdbId}`
  try {
    const data = await followRequest(props.token, `${path}${cancel ? '/cancel' : ''}`, current.signal, 'POST')
    if (current.signal.aborted) return
    state.value = cancel ? { ...state.value, subscribed: false, request: null } : data
    notice.value = cancel ? '已取消你的追更' : '追更申请已保存，可在「我的追更」查看'
  } catch (e) {
    if (!current.signal.aborted) {
      error.value = e instanceof Error ? e.message : '追更操作失败'
      // 超时可能发生在提交之后，读取服务器状态，避免误认为没有保存。
      try { const data = await followRequest(props.token, path, current.signal); if (!current.signal.aborted) state.value = data } catch { /* 保留原错误供用户重试。 */ }
    }
  } finally { if (!current.signal.aborted) busy.value = false }
}
watch(() => [props.token, props.kind, props.tmdbId], () => { void load() }, { immediate: true })
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <div class="space-y-2">
    <div class="flex flex-wrap items-center gap-3">
      <button type="button" :disabled="loading || busy || !state || available || state.subscribed" class="rounded-lg border px-4 py-2 text-xs font-medium disabled:cursor-default" :class="state?.subscribed || available ? 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-400/30 dark:bg-emerald-400/10 dark:text-emerald-300' : 'border-amber-300 bg-amber-50 text-amber-800 hover:bg-amber-100 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-500 dark:hover:bg-amber-500/20 disabled:opacity-50'" @click="change()">{{ label }}</button>
      <button v-if="state?.subscribed" type="button" :disabled="busy" class="text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500 hover:text-red-600 disabled:opacity-50" @click="change(true)">取消追更</button>
      <RouterLink v-if="state?.subscribed" to="/follows" class="text-xs text-blue-600">我的追更</RouterLink>
    </div>
    <p v-if="state?.subscribed && state.request" class="max-w-xl whitespace-pre-wrap break-words text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ followLabel(state.request) }}<span v-if="state.request.note"> · {{ state.request.note }}</span></p>
    <p v-if="notice" role="status" class="text-xs text-emerald-700">{{ notice }}</p>
    <p v-if="error" role="alert" class="text-xs text-red-600">{{ error }}<button v-if="!state" :disabled="loading" class="ml-2 underline" @click="load">重试</button></p>
  </div>
</template>
