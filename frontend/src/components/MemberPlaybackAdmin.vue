<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
const props = defineProps<{ token: string }>()
interface Settings { enabled: boolean; libraryIds: string[]; allowDownload: boolean; publicUrl: string; revision: number }
interface Member { userId: string; embyName: string; embyId: string; stage: string; message: string; adminBlocked: boolean; directoryStarted: boolean; directoryId: string; appliedRevision: number; syncError: string; source: string; assignedAccountId: string; personalDrive?: { uid: string; connected: boolean; directoryId: string } }
const settings = ref<Settings>({ enabled: false, libraryIds: [], allowDownload: false, publicUrl: '', revision: 0 }), members = ref<Member[]>([])
const libraries = ref<{ ItemId: string; Name: string; CollectionType: string }[]>([])
const libraryLoading = ref(false), libraryLoaded = ref(false), savedRevision = ref(0), loaded = ref(false)
const missingLibraries = computed(() => libraryLoaded.value ? settings.value.libraryIds.filter(id => !libraries.value.some(library => library.ItemId === id)) : [])
const counts = computed(() => {
  const provisioned = members.value.filter(member => member.embyId)
  return { failed: provisioned.filter(member => member.syncError).length, pending: provisioned.filter(member => !member.syncError && member.appliedRevision !== savedRevision.value).length, synced: provisioned.filter(member => !member.syncError && member.appliedRevision === savedRevision.value).length }
})
const error = ref(''), notice = ref(''), busy = ref(false)
const lifecycle = new AbortController()
async function request(path: string, body?: object) {
  const response = await fetch(`/api/admin/${path}`, { method: body ? 'POST' : 'GET', headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(95000)]) })
  const data = await response.json().catch(() => null)
  if (!response.ok) throw new Error(data?.error || '配置操作失败')
  return data
}
let statusTimer: ReturnType<typeof setTimeout> | undefined
async function load(initial = false) {
  try {
    const data = await request('member-playback')
    if (initial) { settings.value = data.settings; loaded.value = true }
    members.value = data.members; savedRevision.value = data.settings.revision
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取失败' }
}
async function loadLibraries() {
  if (libraryLoading.value) return
  libraryLoading.value = true; error.value = ''
  try { libraries.value = (await request('member-playback/libraries')).libraries || []; libraryLoaded.value = true }
  catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '媒体库读取失败' }
  finally { libraryLoading.value = false }
}
async function pollStatus() {
  if (lifecycle.signal.aborted) return
  if (!busy.value) await load()
  if (!lifecycle.signal.aborted) statusTimer = setTimeout(pollStatus, 5000)
}
async function mutate(id = '', body: object = settings.value) {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try {
    const data = await request('member-playback' + (id ? `/${id}` : ''), body)
    if (!id) settings.value = data.settings
    members.value = data.members; savedRevision.value = data.settings.revision
    notice.value = id === 'sync' ? '已安排重新同步，可在下方查看结果' : id ? '操作已保存' : '设置已保存，正在同步已有个人用户；同步完成前暂停播放'
  }
  catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '操作失败' }
  finally { busy.value = false }
}
onMounted(async () => { await Promise.all([load(true), loadLibraries()]); if (!lifecycle.signal.aborted) statusTimer = setTimeout(pollStatus, 5000) })
onBeforeUnmount(() => { lifecycle.abort(); clearTimeout(statusTimer) })
</script>
<template>
  <div class="space-y-5">
    <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm text-red-700 dark:bg-red-400/10 dark:text-red-300">{{ error }}</p>
    <p v-if="notice" role="status" class="text-sm text-emerald-700 dark:text-emerald-300">{{ notice }}</p>
    <form class="space-y-4 rounded-2xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900" @submit.prevent="mutate()">
      <h2 class="font-semibold text-slate-900 dark:text-slate-100">自助注册与播放设置</h2>
      <p class="text-sm leading-6 text-slate-500 dark:text-slate-400">直接选择个人用户可访问的 Emby 媒体库，设置同时用于新用户和已有个人用户。仅支持原文件直链播放，音视频转码、重新封装、删除及管理权限始终禁用。</p>
      <label class="flex gap-2 text-sm text-slate-700 dark:text-slate-300"><input v-model="settings.enabled" type="checkbox" />允许普通网站用户自助开通播放</label>
      <fieldset class="space-y-3" :disabled="busy || !loaded">
        <legend class="text-sm text-slate-700 dark:text-slate-300">允许访问的媒体库</legend>
        <div class="flex flex-wrap gap-4 text-sm">
          <button type="button" :disabled="libraryLoading" class="text-blue-600 disabled:opacity-40 dark:text-blue-300" @click="loadLibraries">{{ libraryLoading ? '正在读取…' : '刷新媒体库' }}</button>
          <button type="button" :disabled="!libraryLoaded" class="text-blue-600 disabled:opacity-40 dark:text-blue-300" @click="settings.libraryIds = libraries.map(library => library.ItemId)">选择当前全部</button>
          <button type="button" class="text-slate-500 dark:text-slate-400" @click="settings.libraryIds = []">清空选择</button>
        </div>
        <div class="grid max-h-64 gap-3 overflow-y-auto rounded-xl border border-slate-200 p-3 sm:grid-cols-2 dark:border-slate-700">
          <label v-for="library in libraries" :key="library.ItemId" class="flex items-start gap-2 text-sm text-slate-700 dark:text-slate-200"><input v-model="settings.libraryIds" type="checkbox" :value="library.ItemId" class="mt-1" /><span>{{ library.Name }}<small class="ml-2 text-slate-500 dark:text-slate-400">{{ library.CollectionType }}</small></span></label>
          <p v-if="libraryLoaded && !libraries.length" class="text-sm text-slate-500 dark:text-slate-400">Emby 尚未返回媒体库，请添加媒体库后刷新。</p>
        </div>
        <p class="text-xs text-slate-500 dark:text-slate-400">已选 {{ settings.libraryIds.length }} 个。新增媒体库不会自动开放；开启自助播放时至少选择一个。</p>
        <p v-if="missingLibraries.length" class="text-sm text-amber-700 dark:text-amber-300">有 {{ missingLibraries.length }} 个已选媒体库不在当前服务中。<button type="button" class="underline" @click="settings.libraryIds = settings.libraryIds.filter(id => !missingLibraries.includes(id))">移除失效选择</button></p>
      </fieldset>
      <label class="flex gap-2 text-sm text-slate-700 dark:text-slate-300"><input v-model="settings.allowDownload" type="checkbox" />允许下载原文件（默认关闭）</label>
      <p class="text-xs text-slate-500 dark:text-slate-400">下载使用用户当前授权的播放账号，并校验媒体库和条目权限；不提供转码下载。</p>
      <label class="block text-sm text-slate-600 dark:text-slate-300">Emby 对外反代地址<input v-model="settings.publicUrl" type="url" :required="settings.enabled" placeholder="https://emby.example.com" class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-950" /></label>
      <p class="text-xs leading-6 text-slate-500 dark:text-slate-400">此地址必须指向 Cinema115 播放反代。Emby 源站应只允许反代访问，避免客户端绕过个人绑定检查。已发出的 CDN 链接及正在传输的内容无法立即撤回。</p>
      <button :disabled="busy || !loaded || (settings.enabled && (!settings.libraryIds.length || !!missingLibraries.length))" class="rounded-lg bg-blue-600 px-4 py-2.5 text-sm text-white disabled:opacity-40">保存设置</button>
    </form>
    <section class="rounded-2xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900">
      <div class="flex flex-wrap justify-between gap-3"><h2 class="font-semibold text-slate-900 dark:text-slate-100">全局权限同步</h2><div class="flex gap-4"><button class="text-sm text-blue-600 dark:text-blue-300" :disabled="busy" @click="load()">刷新状态</button><button class="text-sm text-blue-600 dark:text-blue-300" :disabled="busy || !loaded" @click="mutate('sync', {})">重试同步</button></div></div>
      <p class="mt-3 text-xs text-slate-500 dark:text-slate-400" role="status">权限同步：已同步 {{ counts.synced }} · 待同步 {{ counts.pending }} · 失败 {{ counts.failed }}</p>
      <RouterLink to="/emby/users" class="mt-4 inline-block text-sm text-blue-600 dark:text-blue-300">管理 Emby 用户与播放授权</RouterLink>
    </section>
  </div>
</template>
