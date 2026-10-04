<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch, type ObjectDirective } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import { notificationRequest, readWebsiteNotifications, useWebsiteNotificationSync, websiteUnread, type NotificationRead, type WebsiteNotification } from '../composables/useWebsiteNotifications'
import { observeNotificationVisibility } from '../utils/notificationVisibility'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ logout: [] }>()
const router = useRouter()
const items = ref<WebsiteNotification[]>([])
const loading = ref(false), writes = ref(0), hasMore = ref(false), error = ref('')
const busy = computed(() => writes.value > 0)
let controller = new AbortController()
const pending = new Set<number>(), locallyRead = new Set<number>()
let batchTimer: ReturnType<typeof setTimeout> | undefined
let automaticBusy = false
const observers = new Map<Element, () => void>()
const vAutoRead: ObjectDirective<HTMLElement, WebsiteNotification> = {
  mounted(element, { value }) {
    if (!value.readAt) observers.set(element, observeNotificationVisibility(element, () => {
      pending.add(value.id)
      scheduleBatch()
    }))
  },
  updated(element, { value }) {
    if (value.readAt) { observers.get(element)?.(); observers.delete(element) }
  },
  unmounted(element) { observers.get(element)?.(); observers.delete(element) },
}
function scheduleBatch(delay = 250) {
  if (batchTimer || automaticBusy || !pending.size || controller.signal.aborted || document.hidden || !document.hasFocus()) return
  batchTimer = setTimeout(() => { batchTimer = undefined; void flushBatch() }, delay)
}
async function flushBatch() {
  if (automaticBusy || !pending.size || document.hidden || !document.hasFocus()) return
  automaticBusy = true
  const current = controller
  const success = await read({ ids: [...pending].slice(0, 100) })
  if (current !== controller || current.signal.aborted) return
  automaticBusy = false
  scheduleBatch(success ? 250 : 5000)
}
function resume() { scheduleBatch(); void load() }
useWebsiteNotificationSync(resume)
async function load(more = false) {
  if (loading.value || controller.signal.aborted) return
  const current = controller
  loading.value = true; error.value = ''
  try {
    const before = more ? items.value.at(-1)?.id || 0 : 0
    const data = await notificationRequest(`?before=${before}`, current.signal)
    if (current.signal.aborted) return
    const received = (data.items as WebsiteNotification[]).map(item => ({ ...item, readAt: item.readAt || (locallyRead.has(item.id) ? Math.floor(Date.now() / 1000) : 0) }))
    items.value = more ? [...items.value, ...received] : received; hasMore.value = data.hasMore
  } catch (e) { if (!current.signal.aborted) error.value = e instanceof Error ? e.message : '读取失败' }
  finally { if (!current.signal.aborted) loading.value = false }
}
async function read(body: NotificationRead) {
  const current = controller
  if (current.signal.aborted) return false
  writes.value++; error.value = ''
  try {
    await readWebsiteNotifications(body, current.signal)
    if (current.signal.aborted) return false
    const matches = (id: number) => 'ids' in body ? body.ids.includes(id) : body.all ? id <= body.id : id === body.id
    for (const id of pending) if (matches(id)) { locallyRead.add(id); pending.delete(id) }
    for (const item of items.value) if (matches(item.id)) {
      item.readAt ||= Math.floor(Date.now() / 1000)
      locallyRead.add(item.id)
    }
    return true
  } catch (e) {
    if (!current.signal.aborted) error.value = e instanceof Error ? e.message : '标记失败'
    return false
  } finally { if (!current.signal.aborted) writes.value-- }
}
onMounted(() => { void load() })
async function open(item: WebsiteNotification) {
  const current = controller
  if (!item.readAt) await read({ ids: [item.id] })
  if (!current.signal.aborted) await router.push(`/${item.kind}/${item.tmdbId}`)
}
function cleanup() {
  controller.abort(); clearTimeout(batchTimer); batchTimer = undefined
  for (const stop of observers.values()) stop()
  observers.clear(); pending.clear(); locallyRead.clear()
}
watch(() => props.token, () => {
  cleanup(); controller = new AbortController()
  items.value = []; loading.value = false; writes.value = 0; automaticBusy = false
  void load()
})
onBeforeUnmount(cleanup)
</script>

<template>
  <div class="min-h-screen bg-slate-50 dark:bg-slate-950">
    <WorkbenchHeader @logout="emit('logout')" />
    <main class="workbench-content mx-auto max-w-3xl space-y-5 px-4 pb-10 sm:px-8">
      <div class="flex flex-wrap items-center justify-between gap-4"><div><h1 class="text-2xl font-bold text-slate-900 dark:text-slate-100">站内通知</h1><p class="mt-2 text-sm text-slate-500 dark:text-slate-400">追更入库、追平及后续变化会保存在这里。查看后自动标为已读。</p></div><div class="flex gap-4 text-sm"><button :disabled="loading || busy" class="text-blue-600 disabled:opacity-40" @click="load()">刷新</button><button :disabled="busy || loading || !websiteUnread || !items.length" class="text-slate-600 dark:text-slate-400 disabled:opacity-40" @click="read({ id: items[0]!.id, all: true })">全部已读</button></div></div>
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
      <p v-if="!items.length" class="py-12 text-center text-sm text-slate-400 dark:text-slate-500">{{ loading ? '正在加载通知…' : error ? '请刷新重试' : '暂无通知' }}</p>
      <article v-for="item in items" :key="item.id" v-auto-read="item" class="rounded-xl border bg-white dark:bg-slate-900 p-5" :class="item.readAt ? 'border-slate-200 dark:border-slate-700' : 'border-amber-200'">
        <div class="flex items-start gap-3"><span class="mt-2 h-2 w-2 shrink-0 rounded-full" :class="item.readAt ? 'bg-transparent' : 'bg-amber-500'" :aria-label="item.readAt ? undefined : '未读'"></span><div class="min-w-0 flex-1"><RouterLink :to="`/${item.kind}/${item.tmdbId}`" class="break-words font-semibold text-slate-900 dark:text-slate-100 hover:text-amber-700" @click.prevent="open(item)">{{ item.title }}</RouterLink><p class="mt-2 text-sm leading-6 text-slate-600 dark:text-slate-400">{{ item.message }}</p><div class="mt-3 flex flex-wrap items-center justify-between gap-3 text-xs text-slate-400 dark:text-slate-500"><time>{{ new Date(item.createdAt * 1000).toLocaleString() }}</time><button v-if="!item.readAt" :disabled="busy" class="text-blue-600 disabled:opacity-40" @click="read({ ids: [item.id] })">标为已读</button><span v-else>已读</span></div></div></div>
      </article>
      <button v-if="hasMore" :disabled="loading" class="w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 py-3 text-sm text-slate-600 dark:text-slate-400 dark:text-slate-500 disabled:opacity-40" @click="load(true)">{{ loading ? '加载中…' : '加载更多' }}</button>
    </main>
  </div>
</template>
