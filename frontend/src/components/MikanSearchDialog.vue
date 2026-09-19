<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { p115Request } from '../lib/p115-api'

const props = defineProps<{ token: string; title: string; originalTitle: string }>()
const emit = defineEmits<{ close: [] }>()
interface Item { id: string; title: string; size: string; updatedAt: string; magnet: string; url: string; torrentUrl: string; resolution: string; groupId?: string }
interface Group { id: string; name: string }
interface Bangumi { id: string; title: string; coverUrl: string; url: string }
interface Expansion { groupId: string; take: number }
interface Result { items: Item[]; groups: Group[]; bangumis?: Bangumi[]; bangumi?: Bangumi; more?: Expansion[]; query: string; group: string; cached: boolean; fetchedAt: string; expiresAt: string; attempts: { query: string; count: number; cached: boolean }[] }
const dialog = ref<HTMLDialogElement>()
const query = ref(props.title || props.originalTitle)
const group = ref('')
const groups = ref<Group[]>([])
const result = ref<Result>()
const busy = ref(false)
const error = ref('')
const filter = ref('')
const resolution = ref('全部')
const visible = ref(50)
const copied = ref('')
const copyError = ref('')
const selectedBangumi = ref<Bangumi>()
const brokenCovers = ref<Record<string, boolean>>({})
const searchSnapshot = ref<{ result: Result; query: string; group: string; groups: Group[]; filter: string; resolution: string; visible: number }>()
const bangumis = computed(() => searchSnapshot.value?.result.bangumis || result.value?.bangumis || [])
const expansions = computed(() => (result.value?.more || []).filter(item => !group.value || item.groupId === group.value))
const lifecycle = new AbortController()
const previousFocus = document.activeElement as HTMLElement | null
const previousOverflow = document.body.style.overflow
const submissions = ref<Record<string, { busy?: boolean; success?: boolean; message?: string; needsConfig?: boolean }>>({})
const resolutions = computed(() => ['全部', ...['2160P', '1080P', '720P', '480P', '未知'].filter(value => result.value?.items.some(item => item.resolution === value))])
const filtered = computed(() => {
  const words = filter.value.toLocaleLowerCase().trim().split(/\s+/).filter(Boolean)
  return (result.value?.items || []).filter(item => (!selectedBangumi.value || !group.value || item.groupId === group.value) && (resolution.value === '全部' || item.resolution === resolution.value) && words.every(word => item.title.toLocaleLowerCase().includes(word)))
})
const siteURL = computed(() => {
  if (selectedBangumi.value) return selectedBangumi.value.url
  const params = new URLSearchParams({ searchstr: result.value?.query || query.value })
  if (result.value?.group) params.set('subgroupid', result.value.group)
  return `https://mikanani.me/Home/Search?${params}`
})
watch([filter, resolution], () => { visible.value = 50 }, { flush: 'sync' })

async function search(refresh = false, selectedGroup = '') {
  if (busy.value || !query.value.trim()) return
  busy.value = true
  error.value = ''
  selectedBangumi.value = undefined
  searchSnapshot.value = undefined
  result.value = undefined
  visible.value = 50
  resolution.value = '全部'
  group.value = selectedGroup
  if (!selectedGroup) groups.value = []
  try {
    const q = query.value.trim()
    // 只有默认中文标题搜索才自动使用原名兜底，手动改词和字幕组筛选保留用户意图。
    const fallback = q === props.title.trim() && !selectedGroup ? props.originalTitle : ''
    const response = await fetch('/api/admin/mikan/search', {
      method: 'POST', cache: 'no-store',
      headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ query: q, fallback, group: selectedGroup, refresh }),
      signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(75000)]),
    })
    const data = await response.json().catch(() => null)
    if (!response.ok || !data) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : response.status === 403 ? '仅管理员可使用蜜柑搜索' : data?.error || `蜜柑搜索失败（HTTP ${response.status}）`)
    result.value = data as Result
    query.value = data.query
    if (!selectedGroup) groups.value = data.groups
  } catch (e) {
    if (!lifecycle.signal.aborted) error.value = e instanceof Error && e.name === 'TimeoutError' ? '蜜柑搜索超时，请稍后重试' : e instanceof Error ? e.message : '蜜柑搜索失败，请稍后重试'
  } finally { busy.value = false }
}
function selectGroup(event: Event) {
  const value = (event.target as HTMLSelectElement).value
  if (selectedBangumi.value) { group.value = value; visible.value = 50 }
  else void search(false, value)
}
function useTitle(value: string) { query.value = value; void search() }
function refreshCurrent() {
  if (selectedBangumi.value) void loadBangumi(selectedBangumi.value, true)
  else void search(true, group.value)
}
function backToSearch() {
  if (busy.value || !searchSnapshot.value) return
  const saved = searchSnapshot.value
  result.value = saved.result
  query.value = saved.query
  group.value = saved.group
  groups.value = saved.groups
  filter.value = saved.filter
  resolution.value = saved.resolution
  visible.value = saved.visible
  selectedBangumi.value = undefined
  searchSnapshot.value = undefined
  error.value = ''
}
async function loadBangumi(card: Bangumi, refresh = false, expansion?: Expansion) {
  if (busy.value) return
  if (!selectedBangumi.value && result.value) {
    // 进入番剧前保留搜索结果及筛选，返回时无需重新搜索。
    searchSnapshot.value = { result: result.value, query: query.value, group: group.value, groups: groups.value, filter: filter.value, resolution: resolution.value, visible: visible.value }
  }
  if (!expansion && selectedBangumi.value?.id !== card.id) {
    result.value = undefined
    group.value = ''
    groups.value = []
    filter.value = ''
    resolution.value = '全部'
    visible.value = 50
  }
  selectedBangumi.value = card
  busy.value = true
  error.value = ''
  try {
    const response = await fetch('/api/admin/mikan/bangumi', {
      method: 'POST', cache: 'no-store',
      headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: card.id, group: expansion?.groupId || '', take: expansion?.take || 0, refresh }),
      signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(75000)]),
    })
    const data = await response.json().catch(() => null)
    if (!response.ok || !data) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : response.status === 403 ? '仅管理员可使用蜜柑搜索' : data?.error || `蜜柑番剧读取失败（HTTP ${response.status}）`)
    const next = data as Result
    if (expansion && result.value) {
      // 原站返回该字幕组的累积列表；替换该组并按磁力去重，保留其他组及其加载进度。
      const rows = [...result.value.items.filter(item => item.groupId !== expansion.groupId), ...next.items]
      result.value = { ...result.value, items: [...new Map(rows.map(item => [item.id, item])).values()].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)), more: [...(result.value.more || []).filter(item => item.groupId !== expansion.groupId), ...(next.more || [])] }
    } else {
      result.value = next
      groups.value = next.groups
      group.value = ''
      resolution.value = '全部'
      visible.value = 50
    }
  } catch (e) {
    if (!lifecycle.signal.aborted) error.value = e instanceof Error && e.name === 'TimeoutError' ? '蜜柑番剧读取超时，可重试或返回搜索结果' : e instanceof Error ? e.message : '蜜柑番剧读取失败，请稍后重试'
  } finally { busy.value = false }
}
function time(value: string) { return new Date(value).toLocaleString('zh-CN', { hour12: false }) }
async function copy(item: Item) {
  copyError.value = ''
  try { await navigator.clipboard.writeText(item.magnet); copied.value = item.id }
  catch { copied.value = ''; copyError.value = item.id }
}
async function submit(item: Item) {
  if (submissions.value[item.id]?.busy || submissions.value[item.id]?.success) return
  submissions.value[item.id] = { busy: true }
  let submitting = false
  try {
    // 复用云下载功能明确绑定的账号和目录，不选择默认账号，也不改写配置。
    const config = await p115Request<{ accountId: string; targetCID: string; targetPath: string }>(props.token, 'cloud-download/config', {
      signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]),
    })
    if (!config.accountId) throw new Error('请先在“115 云下载”选择账号并保存配置')
    submitting = true
    const response = await p115Request<{ accepted: boolean }>(props.token, `cloud-download/tasks?accountId=${encodeURIComponent(config.accountId)}`, {
      method: 'POST', body: JSON.stringify({ url: item.magnet, targetCID: config.targetCID || '0' }),
      signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(35000)]),
    })
    if (!response.accepted) throw new Error('115 未确认接收任务，请前往云下载页面核对')
    submissions.value[item.id] = { success: true, message: `已提交到 ${config.targetPath || '115 根目录'}，不代表下载已完成` }
  } catch (e) {
    if (lifecycle.signal.aborted) return
    const message = e instanceof Error ? e.message : '提交失败'
    const uncertain = submitting && (e instanceof TypeError || (e instanceof Error && ['TimeoutError', 'AbortError'].includes(e.name)))
    submissions.value[item.id] = { message: uncertain ? '提交结果尚未确认，请先到 115 云下载核对，避免重复提交' : message, needsConfig: /请先|尚未登录|账号.*不存在|配置|凭据/.test(message) }
  }
}
onMounted(() => {
  document.body.style.overflow = 'hidden'
  dialog.value?.showModal()
  void search()
})
onBeforeUnmount(() => {
  lifecycle.abort()
  dialog.value?.close()
  document.body.style.overflow = previousOverflow
  previousFocus?.focus()
})
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" aria-labelledby="mikan-search-title" class="m-auto max-h-[90dvh] w-[calc(100%-2rem)] max-w-5xl overflow-hidden rounded-2xl border border-slate-200 bg-white p-0 shadow-2xl backdrop:bg-slate-950/40" @cancel.prevent="emit('close')">
      <div class="flex max-h-[90dvh] flex-col">
        <header class="flex shrink-0 items-start justify-between gap-3 border-b border-slate-100 p-5">
          <div class="min-w-0"><h2 id="mikan-search-title" class="text-lg font-semibold text-slate-900">蜜柑搜索</h2><p class="mt-1 break-words text-sm text-slate-500">{{ title }}</p></div>
          <button autofocus type="button" aria-label="关闭蜜柑搜索" class="rounded-lg px-2 py-1 text-xl text-slate-500 hover:bg-slate-100" @click="emit('close')">×</button>
        </header>
        <div class="min-h-0 overflow-y-auto p-5">
          <form class="flex flex-wrap gap-2" @submit.prevent="search()">
            <input v-model="query" aria-label="蜜柑搜索词" maxlength="200" :disabled="busy" class="min-w-0 flex-1 rounded-lg border border-slate-200 px-3 py-2 text-sm" placeholder="输入片名、原名或其他译名" />
            <button :disabled="busy || !query.trim()" class="rounded-lg bg-amber-500 px-4 py-2 text-sm font-medium text-white disabled:opacity-40">{{ busy ? '搜索中…' : '搜索' }}</button>
            <button type="button" :disabled="busy || !query.trim()" class="rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-600 disabled:opacity-40" @click="refreshCurrent">强制刷新</button>
          </form>
          <div class="mt-2 flex flex-wrap gap-4 text-xs text-slate-500">
            <button :disabled="busy" class="text-blue-600 disabled:opacity-40" @click="useTitle(title)">用中文标题</button>
            <button v-if="originalTitle && originalTitle !== title" :disabled="busy" class="text-blue-600 disabled:opacity-40" @click="useTitle(originalTitle)">用原名</button>
            <a :href="siteURL" target="_blank" rel="noopener noreferrer" class="text-blue-600">{{ selectedBangumi ? '打开蜜柑番剧原页 ↗' : '打开蜜柑搜索 ↗' }}</a>
          </div>
          <section v-if="bangumis.length" class="mt-4" aria-label="蜜柑相关番剧">
            <h3 class="text-sm font-medium text-slate-700">相关番剧 <span class="ml-1 text-xs font-normal text-slate-500">点击查看对应资源，不自动匹配 TMDB 季度</span></h3>
            <ul class="mt-3 flex gap-3 overflow-x-auto pb-2">
              <li v-for="card in bangumis" :key="card.id" class="w-32 shrink-0">
                <button type="button" :disabled="busy" :aria-pressed="selectedBangumi?.id === card.id" :aria-label="`查看番剧：${card.title}`" class="h-full w-full overflow-hidden rounded-xl border text-left transition disabled:opacity-50" :class="selectedBangumi?.id === card.id ? 'border-amber-500 bg-amber-50 ring-1 ring-amber-500' : 'border-slate-200 bg-slate-50 hover:border-amber-400'" @click="loadBangumi(card)">
                  <img v-if="card.coverUrl && !brokenCovers[card.id]" :src="card.coverUrl" alt="" loading="lazy" referrerpolicy="no-referrer" class="aspect-[5/7] w-full object-cover" @error="brokenCovers[card.id] = true" />
                  <div v-else class="flex aspect-[5/7] items-center justify-center bg-slate-100 text-xs text-slate-400">暂无封面</div>
                  <p class="break-words p-2 text-xs leading-5 text-slate-700">{{ card.title }}</p>
                </button>
              </li>
            </ul>
          </section>
          <div v-if="selectedBangumi" class="mt-4 flex flex-wrap items-center justify-between gap-2 rounded-xl bg-amber-50 p-3">
            <p class="min-w-0 break-words text-sm font-medium text-amber-900">番剧资源：{{ selectedBangumi.title }}</p>
            <button :disabled="busy" type="button" class="text-sm text-blue-600 disabled:opacity-40" @click="backToSearch">返回搜索结果</button>
          </div>
          <p v-if="busy" role="status" class="mt-4 rounded-xl bg-amber-50 p-4 text-sm text-amber-800">{{ selectedBangumi ? '正在读取番剧资源…按字幕组加载；关闭浮窗可取消等待。' : '正在搜索蜜柑…中文标题没有结果时尝试原名；关闭浮窗可取消等待。' }}</p>
          <p v-if="error" role="alert" class="mt-4 rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
          <template v-if="result">
            <section class="mt-4 rounded-xl bg-slate-50 p-3 text-xs leading-6 text-slate-500" aria-label="蜜柑搜索过程">
              <p v-for="(attempt, index) in result.attempts" :key="index" class="break-words">{{ index ? '原名兜底' : '关键词' }}：{{ attempt.query }} · {{ attempt.count }} 条{{ attempt.cached ? ' · 复用缓存/请求结果' : '' }}</p>
              <p>{{ selectedBangumi ? '首屏' : '' }}取得时间：{{ time(result.fetchedAt) }} · 缓存有效至 {{ time(result.expiresAt) }}</p>
              <p>保留蜜柑返回的全部版本；标题中的季集、分辨率仅供参考，不自动匹配 TMDB 季集。</p>
            </section>
            <div class="mt-4 flex flex-wrap gap-3">
              <select :value="group" aria-label="蜜柑字幕组" :disabled="busy" class="min-w-0 max-w-full rounded-lg border border-slate-200 p-2 text-sm" @change="selectGroup"><option value="">全部字幕组</option><option v-for="g in groups" :key="g.id" :value="g.id">{{ g.name }}</option></select>
              <select v-model="resolution" aria-label="蜜柑分辨率" class="rounded-lg border border-slate-200 p-2 text-sm"><option v-for="r in resolutions" :key="r" :value="r">{{ r === '全部' ? '全部分辨率' : r }}</option></select>
              <input v-model="filter" aria-label="筛选蜜柑结果" placeholder="结果内筛选：合集 / 第二季 / 简繁…" class="min-w-0 flex-1 basis-52 rounded-lg border border-slate-200 p-2 text-sm" />
            </div>
            <p class="my-3 text-xs text-slate-500">匹配 {{ filtered.length }} 条 / {{ selectedBangumi ? '已加载' : '本次取得' }} {{ result.items.length }} 条 · 更新时间从新到旧{{ result.more?.length ? ' · 仍有字幕组资源未加载，筛选仅针对已加载内容' : '' }}</p>
            <p v-if="!result.items.length" class="rounded-xl bg-slate-50 p-8 text-center text-sm text-slate-500">{{ selectedBangumi ? '该番剧当前没有可读取的资源，可返回搜索结果或访问原站。' : result.bangumis?.length ? '关键词未找到磁力资源，可点击上方相关番剧继续查看。' : '蜜柑未找到资源，可修改片名、尝试其他译名或访问原站。' }}</p>
            <p v-else-if="!filtered.length" class="p-8 text-center text-sm text-slate-500">没有符合筛选条件的资源，请调整关键词或分辨率。</p>
            <ul class="space-y-3" aria-label="蜜柑资源列表">
              <li v-for="item in filtered.slice(0, visible)" :key="item.id" class="rounded-xl border border-slate-200 p-4">
                <p class="break-words text-sm font-medium leading-6 text-slate-800 [overflow-wrap:anywhere]">{{ item.title }}</p>
                <div class="mt-2 flex flex-wrap gap-3 text-xs text-slate-500"><span>{{ item.size }}</span><span>{{ item.updatedAt }}（原站时间）</span><span class="rounded bg-slate-100 px-2">{{ item.resolution }}</span></div>
                <div class="mt-3 flex flex-wrap gap-4 text-xs">
                  <button class="text-blue-600" @click="copy(item)">{{ copied === item.id ? '已复制' : '复制磁力' }}</button>
                  <button :disabled="submissions[item.id]?.busy || submissions[item.id]?.success" class="font-medium text-orange-600 disabled:opacity-50" @click="submit(item)">{{ submissions[item.id]?.busy ? '提交中…' : submissions[item.id]?.success ? '已提交' : '115 转存' }}</button>
                  <a :href="item.url" target="_blank" rel="noopener noreferrer" class="text-blue-600">查看原帖 ↗</a>
                  <a v-if="item.torrentUrl" :href="item.torrentUrl" target="_blank" rel="noopener noreferrer" class="text-slate-500">下载种子 ↗</a>
                </div>
                <div v-if="copyError === item.id" class="mt-3 text-xs text-amber-700"><p>浏览器未允许复制，请手动复制下方磁力：</p><textarea :value="item.magnet" readonly aria-label="手动复制磁力" class="mt-2 w-full rounded border border-slate-200 p-2" @focus="($event.target as HTMLTextAreaElement).select()" /></div>
                <p v-if="submissions[item.id]?.message" role="status" class="mt-3 break-words text-xs leading-6" :class="submissions[item.id]?.success ? 'text-emerald-700' : 'text-red-600'">{{ submissions[item.id]?.message }}<RouterLink v-if="submissions[item.id]?.needsConfig" to="/115/cloud-download" class="ml-2 text-blue-600" @click="emit('close')">前往配置</RouterLink></p>
              </li>
            </ul>
            <button v-if="filtered.length > visible" class="mt-4 w-full rounded-lg bg-slate-50 p-3 text-sm text-blue-600" @click="visible += 50">加载更多（剩余 {{ filtered.length - visible }} 条）</button>
            <section v-if="selectedBangumi && expansions.length" aria-label="加载更多番剧资源" class="mt-4 rounded-xl border border-amber-100 bg-amber-50/50 p-3">
              <p class="text-xs leading-5 text-slate-500">以下字幕组在蜜柑还有更多资源，点击后继续读取，不会自动转存。</p>
              <div class="mt-2 flex flex-wrap gap-2">
                <button v-for="more in expansions" :key="more.groupId" type="button" :disabled="busy || more.take > 10000" class="rounded-lg border border-amber-200 bg-white px-3 py-2 text-xs text-amber-800 disabled:opacity-40" @click="loadBangumi(selectedBangumi!, false, more)">{{ more.take > 10000 ? '数量较大，请在原站继续查看：' : '加载更多：' }}{{ groups.find(g => g.id === more.groupId)?.name || more.groupId }}</button>
              </div>
            </section>
          </template>
        </div>
        <footer class="shrink-0 border-t border-slate-100 px-5 py-3 text-xs leading-5 text-slate-500">115 转存使用「115 云下载」配置的账号和目录；提交后可在云下载页面查看任务。<RouterLink :to="{ path: '/logs', query: { module: '蜜柑', level: 'ALL' } }" class="ml-2 text-blue-600" @click="emit('close')">查看日志</RouterLink></footer>
      </div>
    </dialog>
  </Teleport>
</template>
