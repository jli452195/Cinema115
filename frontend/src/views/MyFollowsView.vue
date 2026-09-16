<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import FollowCard from '../components/FollowCard.vue'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import { FOLLOW_PAGE_SIZE, followRequest, type Follow } from '../types/follows'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ logout: [] }>()
const route = useRoute(), router = useRouter()
const items = ref<Follow[]>([])
const loading = ref(false)
const busy = ref('')
const error = ref('')
const notice = ref('')
const hasMore = ref(false)
const kind = ref('')
const status = ref('ongoing')
const counts = ref<Record<string, number>>({})
const offset = ref(0)
let controller: AbortController | undefined
const lifecycle = new AbortController()
async function load() {
  if (lifecycle.signal.aborted) return
  controller?.abort(); const current = new AbortController(); controller = current
  loading.value = true; error.value = ''
  try {
    const query = new URLSearchParams({ kind: kind.value, status: status.value, offset: String(offset.value) })
    const data = await followRequest(props.token, `?${query}`, current.signal)
    if (current.signal.aborted) return
    items.value = data.items; hasMore.value = data.hasMore
    counts.value = data.counts || {}
    if (!items.value.length && offset.value > 0) void navigate(kind.value, Math.max(0, offset.value - FOLLOW_PAGE_SIZE))
  } catch (e) { if (!current.signal.aborted) error.value = e instanceof Error ? e.message : '追更列表加载失败' }
  finally { if (!current.signal.aborted) loading.value = false }
}
function navigate(value: string, next = 0, nextStatus = status.value) { return router.push({ path: '/follows', query: { ...(value ? { kind: value } : {}), ...(nextStatus !== 'ongoing' ? { status: nextStatus } : {}), ...(next ? { offset: String(next) } : {}) } }) }
async function cancel(item: Follow) {
  if (busy.value) return
  busy.value = `${item.kind}:${item.tmdbId}`; error.value = ''; notice.value = ''
  try {
    await followRequest(props.token, `/${item.kind}/${item.tmdbId}/cancel`, lifecycle.signal, 'POST')
    if (lifecycle.signal.aborted) return
    notice.value = `已取消「${item.title}」的追更`
    await load()
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '取消失败' }
  finally { busy.value = '' }
}
watch(() => [props.token, route.query.kind, route.query.status, route.query.offset], () => {
  status.value = ['closed', 'completed'].includes(String(route.query.status)) ? String(route.query.status) : 'ongoing'
  kind.value = ['movie', 'tv'].includes(String(route.query.kind)) ? String(route.query.kind) : ''
  const value = Number(route.query.offset || 0)
  offset.value = Number.isInteger(value) && value >= 0 && value <= 1000000 ? value : 0
  items.value = []; notice.value = ''; void load()
}, { immediate: true })
onBeforeUnmount(() => { controller?.abort(); lifecycle.abort() })
</script>

<template>
  <div class="min-h-screen bg-slate-50">
    <WorkbenchHeader @logout="emit('logout')" />
    <main class="mx-auto max-w-[1600px] space-y-6 px-4 py-10 sm:px-8">
      <div class="flex flex-wrap items-center justify-between gap-4"><div><h1 class="text-2xl font-bold text-slate-900">我的追更</h1><p class="mt-2 text-sm text-slate-500">查看你申请的电影和剧集。剧集默认追更所有正片季，不含特别篇。</p></div><button :disabled="loading || !!busy" class="text-sm text-blue-600 disabled:opacity-50" @click="load">刷新</button></div>
      <nav aria-label="追更状态" class="flex flex-wrap gap-2">
        <button v-for="tab in [{ value: 'ongoing', label: '追更中' }, { value: 'closed', label: '已关闭' }, { value: 'completed', label: '已完成' }]" :key="tab.value" type="button" :aria-pressed="status === tab.value" :disabled="!!busy" class="rounded-lg px-4 py-2.5 text-sm disabled:opacity-50" :class="status === tab.value ? 'bg-slate-900 text-white' : 'border border-slate-200 bg-white text-slate-600 hover:bg-slate-100'" @click="navigate(kind, 0, tab.value)">{{ tab.label }} <span v-if="counts[tab.value] !== undefined" class="ml-2 text-xs opacity-70">{{ counts[tab.value] }}</span></button>
      </nav>
      <nav aria-label="追更类型" class="flex gap-2"><button v-for="tab in [{ value: '', label: '全部' }, { value: 'movie', label: '电影' }, { value: 'tv', label: '剧集' }]" :key="tab.value" :aria-pressed="kind === tab.value" class="rounded-lg px-4 py-2 text-sm" :class="kind === tab.value ? 'bg-slate-900 text-white' : 'bg-white text-slate-600 hover:bg-slate-100'" @click="navigate(tab.value)">{{ tab.label }}</button></nav>
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}<button class="ml-3 underline" @click="load">重试</button></p>
      <p v-if="notice" role="status" class="text-sm text-emerald-700">{{ notice }}</p>
      <p v-if="loading" role="status" class="py-10 text-center text-sm text-slate-400">正在加载追更…</p>
      <div v-else-if="!items.length && !error" class="rounded-2xl border border-slate-200 bg-white px-6 py-14 text-center"><p class="text-slate-500">暂无{{ status === 'completed' ? '已完成的' : status === 'closed' ? '已关闭的' : '进行中的' }}{{ kind === 'movie' ? '电影' : kind === 'tv' ? '剧集' : '' }}追更</p><RouterLink to="/" class="mt-4 inline-block text-sm text-blue-600">去发现影视</RouterLink></div>
      <div v-else class="grid grid-cols-1 items-stretch gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4" data-follow-grid>
        <FollowCard v-for="item in items" :key="`${item.kind}:${item.tmdbId}`" :item="item" :busy="!!busy" :cancelling="busy === `${item.kind}:${item.tmdbId}`" @cancel="cancel(item)" />
      </div>
      <div v-if="offset || hasMore" class="flex justify-center gap-6 text-sm"><button :disabled="!offset || loading || !!busy" class="disabled:opacity-30" @click="navigate(kind, Math.max(0, offset - FOLLOW_PAGE_SIZE))">上一页</button><span class="text-slate-400">第 {{ Math.floor(offset / FOLLOW_PAGE_SIZE) + 1 }} 页</span><button :disabled="!hasMore || loading || !!busy" class="disabled:opacity-30" @click="navigate(kind, offset + FOLLOW_PAGE_SIZE)">下一页</button></div>
    </main>
  </div>
</template>
