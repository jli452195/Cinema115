<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import { followLabel, followRequest, type Follow } from '../types/follows'

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
const offset = ref(0)
const failedImages = ref(new Set<string>())
let controller: AbortController | undefined
const lifecycle = new AbortController()
async function load() {
  if (lifecycle.signal.aborted) return
  controller?.abort(); const current = new AbortController(); controller = current
  loading.value = true; error.value = ''
  try {
    const query = new URLSearchParams({ kind: kind.value, offset: String(offset.value) })
    const data = await followRequest(props.token, `?${query}`, current.signal)
    if (current.signal.aborted) return
    items.value = data.items; hasMore.value = data.hasMore
    if (!items.value.length && offset.value > 0) void navigate(kind.value, offset.value - 20)
  } catch (e) { if (!current.signal.aborted) error.value = e instanceof Error ? e.message : '追更列表加载失败' }
  finally { if (!current.signal.aborted) loading.value = false }
}
function navigate(value: string, next = 0) { return router.push({ path: '/follows', query: { ...(value ? { kind: value } : {}), ...(next ? { offset: String(next) } : {}) } }) }
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
watch(() => [props.token, route.query.kind, route.query.offset], () => {
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
    <main class="mx-auto max-w-5xl space-y-6 px-4 py-10 sm:px-8">
      <div class="flex flex-wrap items-center justify-between gap-4"><div><h1 class="text-2xl font-bold text-slate-900">我的追更</h1><p class="mt-2 text-sm text-slate-500">查看你申请的电影和剧集。剧集默认追更所有正片季，不含特别篇。</p></div><button :disabled="loading || !!busy" class="text-sm text-blue-600 disabled:opacity-50" @click="load">刷新</button></div>
      <nav aria-label="追更类型" class="flex gap-2"><button v-for="tab in [{ value: '', label: '全部' }, { value: 'movie', label: '电影' }, { value: 'tv', label: '剧集' }]" :key="tab.value" :aria-pressed="kind === tab.value" class="rounded-lg px-4 py-2 text-sm" :class="kind === tab.value ? 'bg-slate-900 text-white' : 'bg-white text-slate-600 hover:bg-slate-100'" @click="navigate(tab.value)">{{ tab.label }}</button></nav>
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}<button class="ml-3 underline" @click="load">重试</button></p>
      <p v-if="notice" role="status" class="text-sm text-emerald-700">{{ notice }}</p>
      <p v-if="loading" role="status" class="py-10 text-center text-sm text-slate-400">正在加载追更…</p>
      <div v-else-if="!items.length && !error" class="rounded-2xl border border-slate-200 bg-white px-6 py-14 text-center"><p class="text-slate-500">暂无{{ kind === 'movie' ? '电影' : kind === 'tv' ? '剧集' : '' }}追更</p><RouterLink to="/" class="mt-4 inline-block text-sm text-blue-600">去发现影视</RouterLink></div>
      <div v-else class="space-y-4">
        <article v-for="item in items" :key="`${item.kind}:${item.tmdbId}`" class="flex gap-4 rounded-2xl border border-slate-200 bg-white p-4 sm:gap-6 sm:p-5">
          <RouterLink :to="`/${item.kind}/${item.tmdbId}`" :aria-label="`查看${item.title}`" class="h-32 w-20 shrink-0 overflow-hidden rounded-lg bg-slate-100 sm:h-36 sm:w-24"><img v-if="item.posterUrl && !failedImages.has(item.posterUrl)" :src="item.posterUrl" :alt="item.title" loading="lazy" referrerpolicy="no-referrer" class="h-full w-full object-cover" @error="failedImages.add(item.posterUrl)" /><span v-else class="flex h-full items-center justify-center text-xs text-slate-400">暂无海报</span></RouterLink>
          <div class="flex min-w-0 flex-1 flex-col items-start gap-2"><RouterLink :to="`/${item.kind}/${item.tmdbId}`" class="break-words text-base font-semibold text-slate-900 hover:text-amber-600">{{ item.title }}</RouterLink><p class="text-xs text-slate-400">{{ item.kind === 'movie' ? '电影' : '剧集' }} · {{ item.date?.slice(0, 4) || '年份待定' }}</p><span class="rounded-md px-2 py-1 text-xs" :class="followLabel(item) === '已入库' || item.status === 'completed' ? 'bg-emerald-50 text-emerald-700' : 'bg-amber-50 text-amber-700'">{{ followLabel(item) }}</span><p class="text-xs text-slate-400">申请于 {{ new Date(item.subscribedAt).toLocaleDateString() }}</p><p v-if="item.note" class="whitespace-pre-wrap break-words text-xs leading-5 text-slate-600">{{ item.note }}</p><button :disabled="!!busy" class="mt-auto text-xs text-slate-500 hover:text-red-600 disabled:opacity-50" @click="cancel(item)">{{ busy === `${item.kind}:${item.tmdbId}` ? '取消中…' : '取消追更' }}</button></div>
        </article>
      </div>
      <div v-if="offset || hasMore" class="flex justify-center gap-6 text-sm"><button :disabled="!offset || loading || !!busy" class="disabled:opacity-30" @click="navigate(kind, offset - 20)">上一页</button><span class="text-slate-400">第 {{ Math.floor(offset / 20) + 1 }} 页</span><button :disabled="!hasMore || loading || !!busy" class="disabled:opacity-30" @click="navigate(kind, offset + 20)">下一页</button></div>
    </main>
  </div>
</template>
