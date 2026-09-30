<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import ManagementLayout from '../components/ManagementLayout.vue'
import MemberPlaybackEditor from '../components/MemberPlaybackEditor.vue'
import { memberRequest, type MemberPlayback, type MemberSettings } from '../types/memberPlayback'
import type { P115Account } from '../types/p115Accounts'
const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const members = ref<MemberPlayback[]>([]), accounts = ref<P115Account[]>([])
const settings = ref<MemberSettings>({ enabled: false, revision: 0, libraryIds: [], allowDownload: false })
const error = ref(''), query = ref(''), filter = ref('all'), group = ref('all'), loading = ref(false)
const dirtyUsers = ref(new Set<string>()), busyUsers = ref(new Set<string>())
const generation = ref(0), confirmDiscard = ref(false)
const busy = computed(() => loading.value || busyUsers.value.size > 0)
const lifecycle = new AbortController()
const visible = computed(() => new Set(members.value.filter(m => {
 const account = accounts.value.find(a => a.id === m.assignedAccountId)?.name || ''
 const matches = `${m.embyName} ${m.embyId} ${account}`.toLowerCase().includes(query.value.trim().toLowerCase())
 const state = filter.value === 'all' || m.source === filter.value || filter.value === 'pending' && (m.stage !== 'ready' || m.adminBlocked || m.disabled || !!m.syncError || m.appliedRevision !== settings.value.revision)
 const grouping = group.value === 'all' || (group.value === 'excluded') === !!(m.administrator || m.skipLibrarySync)
 return matches && state && grouping
}).map(m => m.userId)))
function mark(set: Set<string>, id: string, value: boolean) { if (value) set.add(id); else set.delete(id) }
async function load(discard = false) {
 if (busy.value) return
 if (dirtyUsers.value.size && !discard) { confirmDiscard.value = true; return }
 loading.value = true; error.value = ''
 try {
  const [data, pool] = await Promise.all([memberRequest(props.token, lifecycle.signal, '/users'), memberRequest(props.token, lifecycle.signal, '/accounts')])
  members.value = [...data.members].sort((a: MemberPlayback, b: MemberPlayback) => a.embyName.localeCompare(b.embyName, 'zh-CN', { numeric: true }) || a.userId.localeCompare(b.userId))
  settings.value = data.settings; accounts.value = pool.accounts
  dirtyUsers.value.clear(); generation.value++; confirmDiscard.value = false
 } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取失败' }
 finally { loading.value = false }
}
function updated(member: MemberPlayback, next: MemberSettings) {
 members.value = members.value.map(m => m.userId === member.userId ? member : m)
 settings.value = next
 accounts.value = accounts.value.map(a => ({ ...a, userCount: members.value.filter(m => m.source === 'assigned' && m.assignedAccountId === a.id).length }))
}
onBeforeRouteLeave(() => !dirtyUsers.value.size || window.confirm('有未保存的播放账号修改，离开后将丢弃。是否离开？'))
onMounted(() => load())
onBeforeUnmount(() => lifecycle.abort())
</script>
<template>
 <ManagementLayout title="Emby 用户管理" description="查看用户播放账号，管理播放权限。" :parent="{ label: 'Emby', to: '/emby' }" @logout="$emit('logout')">
  <template #actions><div class="flex flex-wrap justify-end gap-2"><RouterLink to="/115/pool" class="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300">共享小号池</RouterLink><RouterLink to="/users" class="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300">邀请与设置</RouterLink></div></template>
  <section class="mb-6 space-y-4 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-700 dark:bg-slate-900" aria-label="用户筛选">
   <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold text-slate-900 dark:text-slate-100">Emby 用户 <span class="ml-2 font-normal text-slate-400">{{ members.length }}</span></h2><button :disabled="busy" class="rounded-lg border border-slate-200 px-3 py-2 text-sm text-slate-600 disabled:opacity-40 dark:border-slate-700 dark:text-slate-300" @click="load()">{{ loading ? '正在刷新…' : '刷新 Emby 用户' }}</button></div>
   <div class="grid grid-cols-2 gap-3 md:grid-cols-[minmax(0,1fr)_auto_auto]"><input v-model="query" aria-label="搜索 Emby 用户" placeholder="搜索用户名、用户 ID 或播放账号" class="col-span-2 min-w-0 rounded-xl border border-slate-200 bg-slate-50 px-4 py-3 text-sm text-slate-700 md:col-span-1 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-200" /><select v-model="filter" aria-label="筛选用户" class="min-w-0 rounded-xl border border-slate-200 bg-white px-3 py-3 text-sm text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200"><option value="all">全部用户</option><option value="primary">主网盘</option><option value="assigned">共享小号</option><option value="personal">个人网盘</option><option value="pending">待开通 / 已暂停</option></select><select v-model="group" aria-label="媒体库同步分组" class="min-w-0 rounded-xl border border-slate-200 bg-white px-3 py-3 text-sm text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200"><option value="all">全部分组</option><option value="default">默认组</option><option value="excluded">不同步组</option></select></div>
   <p v-if="dirtyUsers.size" class="text-xs text-amber-700 dark:text-amber-300">{{ dirtyUsers.size }} 位用户有未保存的修改</p>
  </section>
  <p v-if="error" role="alert" class="mb-4 rounded-lg bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
  <div v-if="confirmDiscard" class="mb-4 flex flex-wrap items-center gap-3 rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">刷新会丢弃未保存的修改。<button class="underline" :disabled="busy" @click="load(true)">放弃修改并刷新</button><button @click="confirmDiscard = false">继续编辑</button></div>
  <div class="grid items-start gap-4 md:grid-cols-2 xl:grid-cols-3">
   <MemberPlaybackEditor v-for="m in members" v-show="visible.has(m.userId)" :key="generation + ':' + m.userId" :token="token" :member="m" :settings="settings" :accounts="accounts" :disabled="busy" @updated="updated" @dirty="mark(dirtyUsers, m.userId, $event)" @busy="mark(busyUsers, m.userId, $event)" />
  </div>
  <p v-if="!visible.size" class="py-12 text-center text-sm text-slate-500">{{ loading ? '正在读取用户…' : members.length ? '没有符合筛选条件的用户' : '暂无 Emby 用户' }}</p>
 </ManagementLayout>
</template>
<style scoped>
@media (max-width: 639px) {
 :deep(.management-heading) { grid-template-columns: minmax(0, 1fr); }
 :deep(.management-heading > div) { grid-row: 3; }
 :deep(.management-heading > p) { grid-column: 1; }
}
</style>
