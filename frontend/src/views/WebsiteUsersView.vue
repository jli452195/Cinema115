<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import ManagementLayout from '../components/ManagementLayout.vue'
import WebsiteInvitesPanel from '../components/WebsiteInvitesPanel.vue'
import MemberPlaybackAdmin from '../components/MemberPlaybackAdmin.vue'
import WebsiteUserCleanup from '../components/WebsiteUserCleanup.vue'
import type { WebsiteUser } from '../composables/useWebsiteAuth'
import { memberRequest, type MemberPlayback } from '../types/memberPlayback'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ logout: [] }>()
const users = ref<WebsiteUser[]>([])
const playbackMembers = ref<Record<string, MemberPlayback>>({})
const tab = ref<'users' | 'invites' | 'playback'>(useRoute().query.tab === 'playback' ? 'playback' : 'users')
const username = ref('')
const password = ref('')
const resetUser = ref<WebsiteUser>()
const resetPassword = ref('')
const error = ref('')
const notice = ref('')
const busy = ref(false)
const cleanup = ref<InstanceType<typeof WebsiteUserCleanup>>()
const loading = ref(false)
const offset = ref(0)
const hasMore = ref(false)
const lifecycle = new AbortController()
async function request(path: string, body?: object) {
  const response = await fetch(`/api/admin/users${path}`, { method: body ? 'POST' : 'GET', headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' }, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]), body: body ? JSON.stringify(body) : undefined })
  const data = await response.json().catch(() => null)
  if (!response.ok) throw new Error(data?.error || '用户操作失败')
  return data
}
async function load(nextOffset = offset.value) {
  loading.value = true
  try {
    const data = await request(`?offset=${nextOffset}`)
    users.value = data.users; hasMore.value = data.hasMore; offset.value = nextOffset
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '用户列表加载失败' }
  finally { loading.value = false }
}
async function mutate(path: string, body: object, message: string) {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    await request(path, body)
    password.value = ''; resetPassword.value = ''; resetUser.value = undefined
    if (!path) username.value = ''
    notice.value = message
    await load(path ? offset.value : 0)
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '用户操作失败' }
  finally { busy.value = false }
}
onMounted(() => {
  void load()
  void memberRequest(props.token, lifecycle.signal, '/users').then(data => {
    playbackMembers.value = Object.fromEntries((data.members as MemberPlayback[]).map(m => [m.userId, m]))
  }).catch(() => { /* Website account management remains available independently. */ })
})
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout title="用户管理" description="创建独立网站账号，让用户浏览电影、剧集与入库状态。" @logout="emit('logout')">
    <div role="group" aria-label="用户管理分类" class="mb-5 flex gap-2">
      <button v-for="entry in [{ id: 'users' as const, label: '网站用户' }, { id: 'invites' as const, label: '注册邀请' }, { id: 'playback' as const, label: '自助注册设置' }]" :key="entry.id" :aria-pressed="tab === entry.id" class="rounded-lg px-4 py-2 text-sm" :class="tab === entry.id ? 'bg-blue-50 text-blue-700 dark:bg-blue-400/10 dark:text-blue-300' : 'text-slate-500 dark:text-slate-400'" @click="tab = entry.id">{{ entry.label }}</button>
    </div>
    <WebsiteInvitesPanel v-if="tab === 'invites'" :token="token" />
    <MemberPlaybackAdmin v-else-if="tab === 'playback'" :token="token" />
    <div v-else class="space-y-6">
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
      <p v-if="notice" role="status" class="rounded-xl bg-emerald-50 p-4 text-sm text-emerald-700">{{ notice }}</p>
      <form class="space-y-4 rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-6" @submit.prevent="mutate('', { username, password }, '账号已创建，请将账号和密码交给用户。')">
        <h2 class="font-semibold text-slate-900 dark:text-slate-100">创建普通用户</h2>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="text-sm text-slate-600 dark:text-slate-400 dark:text-slate-500">用户名<input v-model="username" required minlength="3" maxlength="32" pattern="[A-Za-z0-9][A-Za-z0-9_.\-]{2,31}" autocomplete="off" class="mt-2 w-full rounded-lg border border-slate-200 dark:border-slate-700 p-3" placeholder="3–32 位字母、数字、下划线、点或短横线" /></label>
          <label class="text-sm text-slate-600 dark:text-slate-400 dark:text-slate-500">初始密码<input v-model="password" required minlength="8" maxlength="128" type="password" autocomplete="new-password" class="mt-2 w-full rounded-lg border border-slate-200 dark:border-slate-700 p-3" placeholder="至少 8 位" /></label>
        </div>
        <button :disabled="busy" class="rounded-lg bg-slate-900 px-5 py-2.5 text-sm text-white disabled:opacity-50">{{ busy ? '处理中…' : '创建用户' }}</button>
      </form>
      <section class="rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-6">
        <div class="mb-4 flex items-center justify-between"><h2 class="font-semibold text-slate-900 dark:text-slate-100">网站用户</h2><button :disabled="loading || busy" class="text-sm text-blue-600 disabled:opacity-50" @click="load()">刷新列表</button></div>
        <p class="mb-4 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">普通用户无法访问管理功能。重置密码或修改启用状态后，该用户需要重新登录。</p>
        <p v-if="loading" role="status" class="py-4 text-sm text-slate-400 dark:text-slate-500">正在加载…</p>
        <p v-else-if="!users.length" class="py-6 text-sm text-slate-400 dark:text-slate-500">暂无用户，可以在上方创建。</p>
        <div v-for="user in users" :key="user.id" role="group" :aria-label="user.username + ' 的网站账号'" class="flex flex-wrap items-center justify-between gap-4 border-t border-slate-100 dark:border-slate-800 py-4">
          <div class="min-w-0"><p class="break-all font-medium text-slate-800 dark:text-slate-200">{{ user.username }} <span class="ml-2 text-xs" :class="user.disabled ? 'text-slate-400 dark:text-slate-500' : 'text-emerald-600'">{{ user.cleanupState ? '删除待完成' : user.disabled ? '已停用' : '已启用' }}</span></p><p class="mt-1 text-xs text-slate-400 dark:text-slate-500">创建于 {{ new Date(user.createdAt).toLocaleString() }}</p></div>
          <div class="flex flex-wrap gap-4 text-sm"><RouterLink :to="{ path: '/emby/users', query: { websiteUser: user.id } }" class="text-blue-600 dark:text-blue-300">{{ playbackMembers[user.id]?.embyId ? 'Emby：' + playbackMembers[user.id].embyName : '查看播放授权' }}</RouterLink><template v-if="!user.cleanupState"><button :disabled="busy" class="text-blue-600 disabled:opacity-50" @click="resetUser = user; resetPassword = ''">重置密码</button><button :disabled="busy" :class="user.disabled ? 'text-emerald-600' : 'text-red-600'" @click="mutate(`/${user.id}`, { disabled: !user.disabled }, user.disabled ? '用户已启用，请重新登录。' : '用户已停用，原登录凭证已失效。')">{{ user.disabled ? '启用' : '停用' }}</button></template><button :disabled="busy" class="text-red-600 disabled:opacity-40 dark:text-red-300" @click="resetUser = undefined; cleanup?.open(user.id)">{{ user.cleanupState ? '重试删除' : '删除用户' }}</button></div>
        </div>
        <div v-if="offset || hasMore" class="mt-4 flex justify-end gap-4 text-sm"><button :disabled="!offset || loading || busy" class="disabled:opacity-30" @click="load(offset - 50)">上一页</button><button :disabled="!hasMore || loading || busy" class="disabled:opacity-30" @click="load(offset + 50)">下一页</button></div>
      </section>
      <WebsiteUserCleanup ref="cleanup" :token="token" @busy="busy = $event" @changed="load()" />
      <form v-if="resetUser" class="space-y-4 rounded-2xl border border-blue-200 bg-white dark:bg-slate-900 p-6" @submit.prevent="mutate(`/${resetUser.id}`, { password: resetPassword }, '密码已重置，原登录凭证已失效。')">
        <h2 class="font-semibold">重置 {{ resetUser.username }} 的密码</h2>
        <label class="block text-sm text-slate-600 dark:text-slate-400 dark:text-slate-500">新密码<input v-model="resetPassword" required minlength="8" maxlength="128" type="password" autocomplete="new-password" class="mt-2 w-full rounded-lg border border-slate-200 dark:border-slate-700 p-3" /></label>
        <div class="flex gap-4"><button :disabled="busy" class="rounded-lg bg-blue-600 px-5 py-2.5 text-sm text-white disabled:opacity-50">保存新密码</button><button type="button" :disabled="busy" class="text-sm text-slate-500 dark:text-slate-400 dark:text-slate-500" @click="resetUser = undefined; resetPassword = ''">取消</button></div>
      </form>
    </div>
  </ManagementLayout>
</template>
