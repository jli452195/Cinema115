<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { FOLLOW_PAGE_SIZE } from '../types/follows'
import FollowCard from '../components/FollowCard.vue'
import FollowDialog from '../components/FollowDialog.vue'
import WorkbenchHeader from '../components/WorkbenchHeader.vue'
import type { Follow } from '../types/follows'
import SettingSwitch from '../components/SettingSwitch.vue'
import SelectMenu from '../components/SelectMenu.vue'

interface AdminFollow extends Follow { subscriberCount: number; createdAt: string; updatedAt: string; revision: number }
const props = defineProps<{ token: string }>()
const emit = defineEmits<{ logout: [] }>()
const route = useRoute(), router = useRouter()
const items = ref<AdminFollow[]>([])
const kind = ref(''), status = ref(''), query = ref('')
const counts = ref<Record<string, number>>({})
const offset = ref(0), hasMore = ref(false), loading = ref(false), busy = ref(false)
const error = ref(''), notice = ref('')
const settingsOpen = ref(false), baseUrl = ref(''), notificationsConfigured = ref(false)
const autoEnabled = ref(false)
const editing = ref<AdminFollow>(), editStatus = ref('pending'), editNote = ref('')
const editError = ref('')
const lifecycle = new AbortController()
let reader: AbortController | undefined
const labels: Record<string, string> = { pending: '待处理', processing: '处理中', closed: '已关闭', completed: '已完成' }
const kindOptions = [{ value: '', label: '全部类型' }, { value: 'movie', label: '电影' }, { value: 'tv', label: '剧集' }]
const statusOptions = [{ value: 'pending', label: '待处理' }, { value: 'processing', label: '处理中' }, { value: 'closed', label: '已关闭' }]
async function request(path: string, body?: object, signal = lifecycle.signal) {
  const response = await fetch(`/api/admin/follows${path}`, { method: body ? 'POST' : 'GET', headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined, signal: AbortSignal.any([signal, AbortSignal.timeout(15000)]) })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(data?.error || '追更管理暂时不可用')
  return data
}
async function load() {
  if (lifecycle.signal.aborted) return
  reader?.abort(); const current = new AbortController(); reader = current
  loading.value = true
  try {
    const data = await request(`?${new URLSearchParams({ kind: kind.value, status: status.value, q: query.value, offset: String(offset.value) })}`, undefined, current.signal)
    if (current.signal.aborted) return
    items.value = data.items; hasMore.value = data.hasMore
    counts.value = data.counts || {}
    if (!items.value.length && offset.value > 0) void filter(Math.max(0, offset.value - FOLLOW_PAGE_SIZE))
  } catch (e) { if (!current.signal.aborted) error.value = e instanceof Error ? e.message : '列表读取失败' }
  finally { if (!current.signal.aborted) loading.value = false }
}
function filter(nextOffset = 0) {
  return router.push({ path: '/follows/manage', query: { ...(kind.value ? { kind: kind.value } : {}), ...(status.value ? { status: status.value } : {}), ...(query.value.trim() ? { q: query.value.trim() } : {}), ...(nextOffset ? { offset: String(nextOffset) } : {}) } })
}
function edit(item: AdminFollow) { editing.value = item; editStatus.value = ['processing', 'closed'].includes(item.status) ? item.status : 'pending'; editNote.value = item.note || ''; error.value = ''; editError.value = ''; notice.value = '' }
async function save() {
  const item = editing.value
  if (!item || busy.value) return
  busy.value = true; error.value = ''; editError.value = ''; notice.value = ''
  try {
    const savedStatus = editStatus.value
    await request(`/${item.kind}/${item.tmdbId}`, { status: editStatus.value, note: editNote.value, revision: item.revision })
    if (lifecycle.signal.aborted) return
    editing.value = undefined; notice.value = savedStatus === 'closed' ? '已关闭资源搜索，并通知当前订阅用户。' : '处理状态与说明已保存，订阅用户可以查看。'; await load()
  } catch (e) { if (!lifecycle.signal.aborted) editError.value = e instanceof Error ? e.message : '保存失败' }
  finally { busy.value = false }
}
async function saveSettings() {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try { await request('/settings', { baseUrl: baseUrl.value, autoEnabled: autoEnabled.value }); notice.value = '追更设置已保存。' }
  catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '保存失败' }
  finally { busy.value = false }
}
function useCurrentAddress() { baseUrl.value = window.location.origin }
watch(() => [props.token, route.query.kind, route.query.status, route.query.q, route.query.offset], () => {
  kind.value = ['movie', 'tv'].includes(String(route.query.kind)) ? String(route.query.kind) : ''
  status.value = ['pending', 'processing', 'closed', 'completed'].includes(String(route.query.status)) ? String(route.query.status) : 'pending'
  query.value = typeof route.query.q === 'string' ? route.query.q.slice(0, 100) : ''
  const value = Number(route.query.offset || 0); offset.value = Number.isInteger(value) && value >= 0 && value <= 1000000 ? value : 0
  editing.value = undefined; error.value = ''; void load()
}, { immediate: true })
onMounted(async () => {
  try {
    const data = await request('/settings')
    baseUrl.value = data.baseUrl; notificationsConfigured.value = data.notificationsConfigured
    autoEnabled.value = data.autoEnabled ?? false
  }
  catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '通知配置读取失败' }
})
onBeforeUnmount(() => { lifecycle.abort(); reader?.abort() })
</script>

<template>
  <div class="min-h-screen bg-slate-50">
    <WorkbenchHeader @logout="emit('logout')" />
    <FollowDialog v-if="editing" title="处理追更申请" :busy="busy" @close="editing = undefined">
      <form class="space-y-4" @submit.prevent="save">
        <p class="break-words text-sm font-semibold text-slate-900">{{ editing.title }}</p>
        <p v-if="editing.status === 'completed'" class="text-sm text-emerald-700">已由 Emby 入库检查自动完成，可更新处理说明。</p>
        <label v-else class="block text-sm text-slate-600">处理状态<SelectMenu v-model="editStatus" :options="statusOptions" :disabled="busy" label="处理状态" class="mt-2 block w-full" /></label>
        <label class="block text-sm text-slate-600">处理说明（用户可见）<textarea v-model="editNote" :disabled="busy" :required="editStatus === 'closed'" maxlength="500" rows="4" class="mt-2 w-full rounded-lg border border-slate-200 p-3" :placeholder="editStatus === 'closed' ? '请填写关闭原因，例如：暂未找到可用资源' : '例如：正在查找资源，或暂未找到合适版本'" /></label>
        <p v-if="editStatus === 'closed'" class="text-xs leading-5 text-amber-700">关闭后停止后续资源搜索并通知订阅用户；已经提交的 STRM 生成可能仍会完成，Emby 入库检查会继续。</p>
        <p v-else class="text-xs text-slate-400">生成 STRM 不等于入库完成，入库以 Emby 索引为准。</p>
        <p v-if="editError" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-600">{{ editError }}</p>
        <div class="flex justify-end gap-4"><button type="button" :disabled="busy" class="px-3 py-2 text-sm text-slate-500 disabled:opacity-40" @click="editing = undefined">取消</button><button :disabled="busy" class="rounded-lg bg-slate-900 px-4 py-2 text-sm text-white disabled:opacity-40">{{ busy ? '保存中…' : '保存处理' }}</button></div>
      </form>
    </FollowDialog>
    <main class="mx-auto max-w-[1600px] space-y-5 px-4 py-10 sm:px-8">
      <div class="flex flex-wrap items-center justify-between gap-4"><div><h1 class="text-2xl font-bold text-slate-900">追更管理</h1><p class="mt-2 text-sm text-slate-500">按影视合并用户申请，搜索资源并跟进处理。</p></div><button class="text-sm text-blue-600" @click="settingsOpen = !settingsOpen">通知设置</button></div>
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
      <p v-if="notice" role="status" class="rounded-xl bg-emerald-50 p-4 text-sm text-emerald-700">{{ notice }}</p>
      <section v-if="settingsOpen" class="space-y-4 rounded-2xl border border-slate-200 bg-white p-5">
        <p class="text-sm" :class="notificationsConfigured ? 'text-emerald-700' : 'text-amber-700'">{{ notificationsConfigured ? '追更申请通知已配置' : '追更申请通知尚未配置' }}<RouterLink to="/telegram/bot" class="ml-3 text-blue-600">配置 Telegram Bot</RouterLink></p>
        <p class="text-xs leading-6 text-slate-500">使用 Bot 配置中的独立通知聊天 ID。同一影视在仍有人追更时仅发送一次申请通知；发送失败会自动重试。</p>
        <form class="space-y-5" @submit.prevent="saveSettings">
          <SettingSwitch v-model="autoEnabled" label="自动搜索并生成资源" description="同一影视由第一位订阅者创建一次后台任务；匹配资源生成后保持处理中，等待 Emby 确认入库。" />
          <p class="text-xs leading-5 text-slate-500">自动任务使用「115分享STRM」明确保存的账号、输出目录、播放地址及转存设置，并进入与网页和 Telegram 相同的后台批次队列。<RouterLink to="/115/share" class="ml-2 text-blue-600">打开 115分享STRM</RouterLink></p>
          <label class="block text-sm text-slate-600">网站访问地址<input v-model="baseUrl" type="url" maxlength="2048" placeholder="例如 http://192.168.0.24:5173" class="mt-2 w-full rounded-lg border border-slate-200 p-3" /></label><p class="text-xs text-slate-400">用于 Telegram 通知中的管理页链接。请填写管理员可访问的网站根地址，留空时只发送片名与编号。</p><div class="flex flex-wrap gap-4"><button type="button" class="text-sm text-blue-600" @click="useCurrentAddress">使用当前访问地址</button><button :disabled="busy" class="rounded-lg bg-slate-900 px-4 py-2 text-sm text-white disabled:opacity-50">保存追更设置</button></div>
        </form>
      </section>
      <nav aria-label="处理状态筛选" class="flex flex-wrap gap-2">
        <button v-for="tab in ['pending', 'processing', 'closed', 'completed']" :key="tab" type="button" :aria-pressed="status === tab" :disabled="busy" class="rounded-lg px-4 py-2.5 text-sm disabled:opacity-50" :class="status === tab ? 'bg-slate-900 text-white' : 'border border-slate-200 bg-white text-slate-600 hover:bg-slate-100'" @click="status = tab; filter()">{{ labels[tab] }} <span v-if="counts[tab] !== undefined" class="ml-2 text-xs opacity-70">{{ counts[tab] }}</span></button>
      </nav>
      <form class="flex flex-wrap gap-3" @submit.prevent="filter()">
        <SelectMenu v-model="kind" :options="kindOptions" label="影视类型" class="w-[7.75rem]" @change="filter()" />
        <input v-model="query" aria-label="搜索追更申请" maxlength="100" placeholder="片名或 TMDB ID" class="min-w-0 flex-1 rounded-lg border border-slate-200 bg-white p-2.5 text-sm" /><button class="rounded-lg bg-slate-900 px-4 py-2 text-sm text-white">搜索</button><button type="button" :disabled="loading || busy" class="px-2 text-sm text-blue-600 disabled:opacity-50" @click="editing = undefined; error = ''; load()">刷新</button>
      </form>
      <p v-if="loading" role="status" class="py-8 text-center text-sm text-slate-400">正在读取申请…</p>
      <p v-else-if="!items.length && !error" class="rounded-2xl border border-slate-200 bg-white py-14 text-center text-sm text-slate-400">暂无符合条件的有效追更申请</p>
      <div v-else class="grid grid-cols-1 items-stretch gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4" data-follow-grid>
        <FollowCard v-for="item in items" :key="`${item.kind}:${item.tmdbId}`" :item="item" admin :subscriber-count="item.subscriberCount" :busy="busy" @edit="edit(item)" />
      </div>
      <div v-if="offset || hasMore" class="flex justify-center gap-6 text-sm"><button :disabled="!offset || loading || busy" class="disabled:opacity-30" @click="filter(Math.max(0, offset - FOLLOW_PAGE_SIZE))">上一页</button><span class="text-slate-400">第 {{ Math.floor(offset / FOLLOW_PAGE_SIZE) + 1 }} 页</span><button :disabled="!hasMore || loading || busy" class="disabled:opacity-30" @click="filter(offset + FOLLOW_PAGE_SIZE)">下一页</button></div>
    </main>
  </div>
</template>
