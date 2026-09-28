<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import { notificationRequest, refreshWebsiteUnread, websiteUnread, type WebsiteNotification } from '../composables/useWebsiteNotifications'

defineProps<{ token: string }>()
const emit = defineEmits<{ logout: [] }>()
const router = useRouter()
const items = ref<WebsiteNotification[]>([])
const loading = ref(false), busy = ref(false), hasMore = ref(false), error = ref('')
const controller = new AbortController()
async function load(more = false) {
  if (loading.value || controller.signal.aborted) return
  loading.value = true; error.value = ''
  try {
    const before = more ? items.value.at(-1)?.id || 0 : 0
    const data = await notificationRequest(`?before=${before}`, controller.signal)
    if (controller.signal.aborted) return
    items.value = more ? [...items.value, ...data.items] : data.items; hasMore.value = data.hasMore
  } catch (e) { if (!controller.signal.aborted) error.value = e instanceof Error ? e.message : '读取失败' }
  finally { loading.value = false }
}
async function read(id: number, all = false) {
  if (busy.value || !id) return
  busy.value = true; error.value = ''
  try {
    await notificationRequest('/read', controller.signal, { id, all })
    if (controller.signal.aborted) return
    for (const item of items.value) if (all ? item.id <= id : item.id === id) item.readAt ||= Math.floor(Date.now() / 1000)
    await refreshWebsiteUnread(controller.signal)
  } catch (e) { if (!controller.signal.aborted) error.value = e instanceof Error ? e.message : '标记失败' }
  finally { busy.value = false }
}
onMounted(() => { void load() })
async function open(item: WebsiteNotification) {
  await read(item.id)
  if (!controller.signal.aborted) await router.push(`/${item.kind}/${item.tmdbId}`)
}
onBeforeUnmount(() => controller.abort())
</script>

<template>
  <div class="min-h-screen bg-slate-50 dark:bg-slate-950">
    <WorkbenchHeader @logout="emit('logout')" />
    <main class="workbench-content mx-auto max-w-3xl space-y-5 px-4 pb-10 sm:px-8">
      <div class="flex flex-wrap items-center justify-between gap-4"><div><h1 class="text-2xl font-bold text-slate-900 dark:text-slate-100">站内通知</h1><p class="mt-2 text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500">追更入库、追平及后续变化会保存在这里。</p></div><div class="flex gap-4 text-sm"><button :disabled="loading || busy" class="text-blue-600 disabled:opacity-40" @click="load()">刷新</button><button :disabled="busy || loading || !websiteUnread || !items.length" class="text-slate-600 dark:text-slate-400 dark:text-slate-500 disabled:opacity-40" @click="read(items[0]!.id, true)">全部已读</button></div></div>
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
      <p v-if="!items.length" class="py-12 text-center text-sm text-slate-400 dark:text-slate-500">{{ loading ? '正在加载通知…' : error ? '请刷新重试' : '暂无通知' }}</p>
      <article v-for="item in items" :key="item.id" class="rounded-xl border bg-white dark:bg-slate-900 p-5" :class="item.readAt ? 'border-slate-200 dark:border-slate-700' : 'border-amber-200'">
        <div class="flex items-start gap-3"><span v-if="!item.readAt" aria-label="未读" class="mt-2 h-2 w-2 shrink-0 rounded-full bg-amber-500"></span><div class="min-w-0 flex-1"><RouterLink :to="`/${item.kind}/${item.tmdbId}`" class="break-words font-semibold text-slate-900 dark:text-slate-100 hover:text-amber-700" @click.prevent="open(item)">{{ item.title }}</RouterLink><p class="mt-2 text-sm leading-6 text-slate-600 dark:text-slate-400 dark:text-slate-500">{{ item.message }}</p><div class="mt-3 flex flex-wrap items-center justify-between gap-3 text-xs text-slate-400 dark:text-slate-500"><time>{{ new Date(item.createdAt * 1000).toLocaleString() }}</time><button v-if="!item.readAt" :disabled="busy" class="text-blue-600 disabled:opacity-40" @click="read(item.id)">标为已读</button><span v-else>已读</span></div></div></div>
      </article>
      <button v-if="hasMore" :disabled="loading" class="w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 py-3 text-sm text-slate-600 dark:text-slate-400 dark:text-slate-500 disabled:opacity-40" @click="load(true)">{{ loading ? '加载中…' : '加载更多' }}</button>
    </main>
  </div>
</template>
