<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115AccountSelect from '../components/P115AccountSelect.vue'
import EmbyUserAvatar from '../components/EmbyUserAvatar.vue'
import type { P115Account } from '../types/p115Accounts'
import { p115Request } from '../lib/p115-api'
import { embyRequest } from '../types/emby'
const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
interface Binding { userId: string; userName: string; accountId: string }
interface User { Id: string; Name: string; PrimaryImageTag?: string; Policy?: { IsDisabled?: boolean; IsAdministrator?: boolean } }
interface Draft { accountId: string; original: string; busy: boolean; error: string; notice: string }
const config = ref({ embyUrl: '', bindings: [] as Binding[] })
const users = ref<User[]>([]), accounts = ref<P115Account[]>([]), drafts = ref<Record<string, Draft>>({})
const busy = ref(false), loaded = ref(false), usersLoaded = ref(false), error = ref(''), sourceAccountId = ref(''), proxyURL = ref('')
const query = ref(''), filter = ref('all'), confirmRefresh = ref(false), copied = ref('')
const lifecycle = new AbortController()
const sourceName = computed(() => accounts.value.find(a => a.id === sourceAccountId.value)?.name || '未配置或已删除')
const serverChanged = computed(() => !!config.value.bindings.length && config.value.embyUrl !== proxyURL.value)
const cards = computed(() => {
  const list = users.value.map(user => ({ ...user, missing: false }))
  const ids = new Set(list.map(user => user.Id))
  for (const b of config.value.bindings) if (!ids.has(b.userId)) list.push({ Id: b.userId, Name: b.userName || b.userId, missing: usersLoaded.value })
  return list
})
const filtered = computed(() => cards.value.filter(user => {
  const bound = !!savedAccount(user.Id)
  if (filter.value === 'bound' && !bound || filter.value === 'source' && bound || filter.value === 'disabled' && !user.Policy?.IsDisabled) return false
  return `${user.Name} ${user.Id} ${accountName(savedAccount(user.Id))}`.toLowerCase().includes(query.value.trim().toLowerCase())
}))
const saving = computed(() => Object.values(drafts.value).some(d => d.busy))
const unsaved = computed(() => Object.values(drafts.value).some(dirty))
function savedAccount(id: string) { return config.value.bindings.find(b => b.userId === id)?.accountId || '' }
function accountName(id: string) { return id ? accounts.value.find(a => a.id === id)?.name || '原账号已删除' : sourceName.value }
function accountIssue(id: string) { const a = accounts.value.find(a => a.id === id); return !a ? '账号已删除，请重新选择' : !a.configured ? '账号未登录，请在 115 助手更新凭据' : '' }
function directoryCID(id: string) { return accounts.value.find(a => a.id === id)?.playbackParentCid || '' }
function directory(id: string) { const cid = directoryCID(id); return cid === '0' ? '根目录（0）' : cid || '未设置（分享直取不受影响）' }
function dirty(d: Draft) { return d.accountId !== d.original }
function makeDraft(id: string): Draft { const accountId = savedAccount(id); return { accountId, original: accountId, busy: false, error: '', notice: '' } }
function resetDrafts() { drafts.value = Object.fromEntries(cards.value.map(user => [user.Id, makeDraft(user.Id)])) }
async function refresh(discard = false) {
  if (busy.value || saving.value) return
  if (unsaved.value && !discard) { confirmRefresh.value = true; return }
  confirmRefresh.value = false; busy.value = true; error.value = ''
  try {
    const [status, accountList, proxy] = await Promise.all([
      embyRequest(props.token, 'playback-cache', lifecycle.signal),
      p115Request<{ accounts: P115Account[] }>(props.token, 'accounts', { signal: lifecycle.signal }),
      embyRequest(props.token, 'proxy', lifecycle.signal),
    ])
    config.value = { embyUrl: status.config.embyUrl, bindings: status.config.bindings }
    accounts.value = accountList.accounts; sourceAccountId.value = proxy.config.accountId; proxyURL.value = proxy.config.url
    loaded.value = true
    try {
      const data = await embyRequest(props.token, 'playback-cache/users', lifecycle.signal)
      if (data.embyUrl !== proxyURL.value) throw new Error('Emby 服务配置已变化，请重新刷新。')
      if (serverChanged.value) { users.value = []; usersLoaded.value = false; throw new Error('Emby 服务已变化，请先移除原服务绑定，再刷新读取新服务用户。') }
      users.value = data.users; usersLoaded.value = true
    } catch (e) {
      usersLoaded.value = false
      if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取 Emby 用户失败，已保留绑定'
    }
    resetDrafts()
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取用户配置失败' }
  finally { busy.value = false }
}
async function save(user: User) {
  const d = drafts.value[user.Id]
  if (!d || d.busy || busy.value) return
  d.busy = true; d.error = ''; d.notice = ''
  try {
    const data = await embyRequest(props.token, `playback-cache/bindings/${encodeURIComponent(user.Id)}`, lifecycle.signal, {
      embyUrl: !d.accountId && config.value.bindings.length ? config.value.embyUrl : proxyURL.value,
      mode: d.accountId ? 'personal' : 'source', accountId: d.accountId, expectedAccountId: d.original,
    })
    // Apply only this card; concurrent responses must not reset other cards or their drafts.
    const binding = (data.config.bindings as Binding[]).find(b => b.userId === user.Id)
    config.value.bindings = config.value.bindings.filter(b => b.userId !== user.Id)
    if (binding) config.value.bindings.push(binding)
    config.value.embyUrl = data.config.embyUrl
    d.original = binding?.accountId || ''; d.notice = '已保存，新播放请求生效。'
  } catch (e) { if (!lifecycle.signal.aborted) d.error = e instanceof Error ? e.message : '保存失败' }
  finally { d.busy = false }
}
async function copy(value: string) {
  try {
    if (navigator.clipboard && window.isSecureContext) await navigator.clipboard.writeText(value)
    else {
      const box = document.createElement('textarea'); box.value = value; box.style.position = 'fixed'; box.style.opacity = '0'; document.body.appendChild(box)
      try { box.select(); if (!document.execCommand('copy')) throw new Error('copy') } finally { box.remove() }
    }
    copied.value = value
  } catch { copied.value = ''; error.value = '复制失败，请选中文本手动复制。' }
}
onMounted(() => refresh())
onBeforeUnmount(() => lifecycle.abort())
</script>
<template>
  <ManagementLayout title="Emby 用户管理" description="查看每位用户的播放账号，独立管理个人 115 绑定。" :parent="{ label: 'Emby', to: '/emby' }" @logout="$emit('logout')">
    <template #actions><RouterLink to="/helper115" class="rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-4 py-2.5 text-sm text-slate-700 dark:text-slate-300">115 账号与缓存目录</RouterLink></template>
    <section class="mb-6 space-y-4 rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-5 shadow-sm">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div><h2 class="font-semibold text-slate-900 dark:text-slate-100">Emby 用户 <span class="ml-1 text-slate-400 dark:text-slate-500">{{ cards.length }}</span></h2><p class="mt-2 text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500">302 源账号：<span class="font-medium text-slate-700 dark:text-slate-300">{{ sourceName }}</span> <RouterLink to="/emby/proxy" class="ml-2 text-blue-600">前往设置</RouterLink></p></div>
        <button :disabled="busy || saving" class="rounded-lg border border-slate-200 dark:border-slate-700 px-4 py-2 text-sm text-slate-600 dark:text-slate-400 dark:text-slate-500 disabled:opacity-40" @click="refresh()">{{ busy ? '读取中…' : '刷新 Emby 用户' }}</button>
      </div>
      <p class="text-xs leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">个人账号优先读取分享，分享受限时由 302 源账号辅助秒传。未绑定用户使用 302 源账号；已绑定账号失效时停止播放。</p>
      <div class="flex flex-wrap gap-3">
        <input v-model="query" aria-label="搜索 Emby 用户" placeholder="搜索用户名、用户 ID 或播放账号" class="min-w-0 flex-1 basis-64 rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 px-4 py-2.5 text-sm" />
        <select v-model="filter" aria-label="筛选 Emby 用户" class="rounded-xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-3 py-2.5 text-sm"><option value="all">全部用户</option><option value="bound">已绑定个人账号</option><option value="source">使用 302 源账号</option><option value="disabled">Emby 已停用</option></select>
      </div>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
      <div v-if="confirmRefresh" class="flex flex-wrap items-center gap-2 rounded-lg bg-amber-50 p-3 text-sm text-amber-800"><span>有未保存的修改，刷新将丢弃这些修改。</span><button class="font-medium underline" @click="refresh(true)">丢弃修改并刷新</button><button @click="confirmRefresh = false">继续编辑</button></div>
    </section>
    <div class="grid items-stretch gap-4 md:grid-cols-2 xl:grid-cols-3">
      <article v-for="user in filtered" :key="user.Id" :aria-label="user.Name + ' 的播放配置'" class="flex min-w-0 flex-col rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 shadow-sm">
        <header class="flex flex-wrap items-start justify-between gap-2 border-b border-slate-100 dark:border-slate-800 p-4">
          <div class="flex min-w-0 items-center gap-3">
            <EmbyUserAvatar :token="token" :user-id="user.Id" :name="user.Name" :image-tag="serverChanged ? undefined : user.PrimaryImageTag" />
            <div class="min-w-0"><h3 class="break-all font-semibold text-slate-900 dark:text-slate-100">{{ user.Name }}</h3><div class="mt-2 flex flex-wrap gap-1.5 text-xs"><span class="rounded-md px-2 py-1" :class="user.Policy?.IsAdministrator ? 'bg-violet-50 text-violet-700' : 'bg-slate-100 dark:bg-slate-800 text-slate-500 dark:text-slate-400 dark:text-slate-500'">{{ user.Policy ? user.Policy.IsAdministrator ? '管理员' : '普通用户' : '权限待读取' }}</span><span v-if="user.Policy?.IsDisabled || user.missing || serverChanged" class="rounded-md bg-red-50 px-2 py-1 text-red-600">{{ serverChanged ? '原服务绑定' : user.missing ? '用户不存在' : 'Emby 已停用' }}</span></div></div>
          </div>
          <div class="max-w-full rounded-lg px-2.5 py-1.5 text-xs" :class="savedAccount(user.Id) ? 'bg-blue-50 text-blue-700' : 'bg-slate-50 dark:bg-slate-950 text-slate-600 dark:text-slate-400 dark:text-slate-500'"><p>{{ savedAccount(user.Id) ? '已绑定个人账号' : '使用 302 源账号' }}</p><p class="mt-1 break-all font-semibold">{{ accountName(savedAccount(user.Id)) }}</p></div>
        </header>
        <form v-if="drafts[user.Id]" class="flex flex-1 flex-col" @submit.prevent="save(user)">
          <fieldset :disabled="busy || drafts[user.Id].busy" class="flex flex-1 flex-col p-4 disabled:opacity-60">
            <P115AccountSelect :id="'account-' + user.Id" v-model="drafts[user.Id].accountId" :accounts="accounts" :disable-accounts="user.missing || user.Policy?.IsDisabled || serverChanged || !usersLoaded" allow-source compact label="115 账号" :source-label="'使用 302 源账号 · ' + sourceName" />
            <p v-if="drafts[user.Id].accountId && accountIssue(drafts[user.Id].accountId)" class="mt-2 text-xs text-red-600">{{ accountIssue(drafts[user.Id].accountId) }}</p>
            <div v-if="drafts[user.Id].accountId" class="mt-3 text-xs leading-5 text-slate-500 dark:text-slate-400 dark:text-slate-500">
              <p class="break-all">缓存目录：{{ directory(drafts[user.Id].accountId) }}</p>
              <div class="mt-1 flex flex-wrap gap-3"><RouterLink to="/helper115" class="text-blue-600">设置目录</RouterLink><button v-if="directoryCID(drafts[user.Id].accountId)" type="button" class="text-blue-600" @click="copy(directoryCID(drafts[user.Id].accountId))">{{ copied === directoryCID(drafts[user.Id].accountId) ? '已复制' : '复制目录 ID' }}</button></div>
            </div>
            <p v-else-if="accountIssue(sourceAccountId)" class="mt-3 text-xs text-red-600">请在 302 设置中检查源账号配置</p>
            <p v-if="user.missing || serverChanged" class="mt-2 text-xs leading-5 text-amber-700">选择源账号并保存，可移除原绑定。</p>
            <p v-if="drafts[user.Id].error" role="alert" class="mt-2 text-xs text-red-600">{{ drafts[user.Id].error }}</p>
            <div class="mt-auto flex items-center justify-between gap-2 pt-3"><span class="text-xs" :class="dirty(drafts[user.Id]) ? 'text-amber-600' : drafts[user.Id].notice ? 'text-emerald-600' : 'text-slate-400 dark:text-slate-500'" role="status">{{ dirty(drafts[user.Id]) ? '未保存' : drafts[user.Id].notice ? '保存成功' : '已保存' }}</span><button type="submit" :disabled="!dirty(drafts[user.Id]) || !!drafts[user.Id].accountId && (!usersLoaded || user.missing || user.Policy?.IsDisabled || serverChanged)" class="rounded-lg bg-blue-600 px-3 py-1.5 text-xs font-medium text-white disabled:opacity-40">{{ drafts[user.Id].busy ? '保存中…' : '保存' }}</button></div>
          </fieldset>
        </form>
        <footer class="flex items-start gap-2 rounded-b-2xl border-t border-slate-100 dark:border-slate-800 bg-slate-50 dark:bg-slate-950/60 px-4 py-2.5 text-xs text-slate-400 dark:text-slate-500"><p class="min-w-0 flex-1 break-all">Emby ID：{{ user.Id }}</p><button type="button" :aria-label="'复制 ' + user.Name + ' 的 Emby ID'" class="shrink-0 text-blue-600" @click="copy(user.Id)">{{ copied === user.Id ? '已复制' : '复制' }}</button></footer>
      </article>
    </div>
    <p v-if="!filtered.length" class="py-12 text-center text-sm text-slate-400 dark:text-slate-500">{{ busy && !loaded ? '正在读取用户…' : query || filter !== 'all' ? '没有符合条件的用户' : '尚未读取到用户，请检查 Emby 配置后刷新。' }}</p>
  </ManagementLayout>
</template>
