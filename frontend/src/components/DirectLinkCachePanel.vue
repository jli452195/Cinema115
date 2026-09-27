<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import PlaybackDiagnostics from './PlaybackDiagnostics.vue'
const props = defineProps<{ token: string; minutes: number }>()
interface Entry { id: string; user: string; mediaSource: string; account: string; mode: string; cdn: string; expiresAt: string; hits: number; url?: string; createdAt?: string; traceId?: string; requestHeaders?: string; lookupHeaders?: string }
const entries = ref<Entry[]>([])
const hits = ref(0), misses = ref(0), now = ref(Date.now())
const search = ref(''), category = ref('all'), error = ref('')
const busy = ref(false)
const lifecycle = new AbortController()
let timer: ReturnType<typeof setInterval> | undefined
const remaining = (e: Entry) => Math.max(0, Math.ceil((Date.parse(e.expiresAt) - now.value) / 1000))
const active = computed(() => entries.value.filter(e => remaining(e) > 0))
const instant = computed(() => active.value.filter(e => e.mode.includes('秒传')).length)
const drive = computed(() => active.value.filter(e => e.mode === '源账号网盘直取').length)
const visible = computed(() => active.value.filter(e =>
  (category.value === 'all' || category.value === 'drive' && e.mode === '源账号网盘直取' || category.value === 'instant' && e.mode.includes('秒传') || category.value === 'direct' && !e.mode.includes('秒传') && e.mode !== '源账号网盘直取') &&
  [e.user, e.mediaSource, e.account, e.mode, e.cdn, e.url, e.traceId, e.requestHeaders].join(' ').toLowerCase().includes(search.value.trim().toLowerCase())))
async function request(method = 'GET', id = '') {
  if (busy.value) return
  busy.value = true; error.value = ''
  try {
    const response = await fetch('/api/admin/emby/proxy/direct-links' + (id ? '/' + encodeURIComponent(id) : ''), {
      method, headers: { Authorization: 'Bearer ' + props.token },
      signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]),
    })
    const data = await response.json()
    if (!response.ok) throw new Error(data.error || '读取直链缓存失败')
    entries.value = data.entries; hits.value = data.hits; misses.value = data.misses
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '直链缓存操作失败' }
  finally { busy.value = false }
}
onMounted(() => {
  void request()
  timer = setInterval(() => { now.value = Date.now(); if (Math.floor(now.value / 1000) % 5 === 0) void request() }, 1000)
})
onBeforeUnmount(() => { lifecycle.abort(); if (timer) clearInterval(timer) })
</script>

<template>
  <section class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm md:p-7">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h2 class="font-semibold text-slate-900">直链缓存管理</h2>
      <div class="flex gap-4 text-sm"><button :disabled="busy" class="text-blue-600 disabled:opacity-40" @click="request()">刷新</button><button :disabled="busy || !active.length" class="text-red-600 disabled:opacity-40" @click="request('DELETE')">清空直链缓存</button></div>
    </div>
    <p class="mt-2 text-sm leading-6 text-slate-500">{{ minutes ? '当前缓存 ' + minutes + ' 分钟' : '直链缓存已关闭' }}。只缓存临时播放地址，重启或保存反代配置后清空。移除后下次播放重新取链，不删除朋友网盘中的文件。</p>
    <div class="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-4">
      <button v-for="item in [{ key: 'all', label: '全部', count: active.length }, { key: 'direct', label: '分享直取', count: active.length - instant - drive }, { key: 'instant', label: '秒传播放', count: instant }, { key: 'drive', label: '网盘直取', count: drive }]" :key="item.key" class="rounded-xl border p-3 text-left" :class="category === item.key ? 'border-blue-300 bg-blue-50' : 'border-slate-200 bg-slate-50'" @click="category = item.key"><span class="text-sm text-slate-600">{{ item.label }}</span><strong class="mt-1 block text-xl">{{ item.count }}</strong></button>
    </div>
    <p class="mt-3 text-xs text-slate-500">命中 {{ hits }} 次 · 发起取链 {{ misses }} 次（本次缓存实例统计）</p>
    <input v-model="search" aria-label="搜索直链缓存" placeholder="搜索用户、媒体源、账号、播放方式或 CDN 域名" class="mt-4 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 text-sm" />
    <p v-if="error" role="alert" class="mt-3 text-sm text-red-600">{{ error }}</p>
    <div v-for="entry in visible" :key="entry.id" class="mt-3 rounded-xl border border-slate-200 p-4 text-sm">
      <div class="flex flex-wrap items-center justify-between gap-2"><div class="flex flex-wrap gap-2"><span class="rounded-full bg-emerald-50 px-2 py-1 text-xs text-emerald-700">剩余 {{ remaining(entry) }} 秒</span><span class="rounded-full bg-blue-50 px-2 py-1 text-xs text-blue-700">{{ entry.mode }}</span><span class="py-1 text-xs text-slate-400">命中 {{ entry.hits }} 次</span></div><button :disabled="busy" class="text-red-600 disabled:opacity-40" @click="request('DELETE', entry.id)">移除</button></div>
      <p class="mt-3 break-all text-slate-700">Emby 用户：{{ entry.user }}</p>
      <p class="mt-1 break-all text-slate-600">媒体源：{{ entry.mediaSource }}</p>
      <p class="mt-1 break-all text-slate-600">取链账号：{{ entry.account }}</p>
      <p class="mt-1 break-all text-xs text-slate-400">CDN：{{ entry.cdn }}</p>
      <p v-if="entry.createdAt" class="mt-1 break-all text-xs text-slate-500">建链：{{ new Date(entry.createdAt).toLocaleString() }} · 到期：{{ new Date(entry.expiresAt).toLocaleString() }}</p>
      <RouterLink v-if="entry.traceId" class="mt-1 block text-xs text-blue-600" :to="{ path: '/logs', query: { module: 'Emby 302', level: 'ALL', taskID: entry.traceId } }">建链请求 {{ entry.traceId }} · 查看日志</RouterLink>
      <details v-if="entry.url" class="mt-3 min-w-0"><summary class="cursor-pointer text-blue-600">查看完整直链和建链请求头</summary>
        <PlaybackDiagnostics :url="entry.url" :request-headers="entry.requestHeaders" :lookup-headers="entry.lookupHeaders" lookup="这里保存首次建链时的请求头，缓存命中不会更新；115 Cookie 与登录令牌已隐藏。CDN 请求由播放器发送，实际请求头不可见。" />
      </details>
    </div>
    <p v-if="!visible.length" class="py-6 text-center text-sm text-slate-400">{{ active.length ? '没有匹配的缓存' : '暂无有效直链缓存，播放后会自动建立' }}</p>
  </section>
</template>
