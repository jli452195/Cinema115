<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { IconFolder, IconPencil, IconPlus, IconTrash } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115PrimaryStatus from '../components/P115PrimaryStatus.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import { p115Request } from '../lib/p115-api'
import { cloudDownloadNotice, submitCloudDownload } from '../lib/cloud-download'
import type { P115Account } from '../types/p115Accounts'

interface CloudDownloadProfile {
  id: string
  name: string
  accountId: string
  targetCID: string
  targetPath: string
}

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const lifecycle = new AbortController()
const accounts = ref<P115Account[]>([])
const profiles = ref<CloudDownloadProfile[]>([])
const accountsLoaded = ref(false)
const accountId = ref('')
const url = ref('')
const targetCID = ref('0')
const targetPath = ref('/')
const directoryDialogOpen = ref(false)
const submitting = ref(false)
const error = ref('')
const notice = ref('')
const applyingConfig = ref(false)
const profileDraft = ref<CloudDownloadProfile>()
const deletingProfile = ref<CloudDownloadProfile>()
const profileSaving = ref(false)
const profileError = ref('')
const profileNotice = ref('')
const profileDirectoryDialogOpen = ref(false)

const primaryAccount = computed(() => accounts.value.find(account => account.primary && account.id === accountId.value))
const configuredPrimary = computed(() => accounts.value.find(account => account.primary && account.configured))
const currentName = computed(() => targetPath.value === '/' ? '115 根目录' : targetPath.value)
const canSubmit = computed(() => !!primaryAccount.value?.configured && !!targetCID.value && !!url.value.trim() && !submitting.value && !applyingConfig.value)

async function loadAccounts() {
  const data = await p115Request<{ accounts: P115Account[] }>(props.token, 'accounts', { signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
  accounts.value = data.accounts
  accountsLoaded.value = true
}

async function loadConfig() {
  const config = await p115Request<{ accountId: string; targetCID: string; targetPath: string }>(props.token, 'cloud-download/config', { signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
  applyingConfig.value = true
  accountId.value = config.accountId || ''
  targetCID.value = config.targetCID || ''
  targetPath.value = config.targetPath || '请选择下载目录'
  if (targetCID.value === '0') targetPath.value = '/'
  applyingConfig.value = false
}

async function loadProfiles() {
  const data = await p115Request<{ profiles: CloudDownloadProfile[] }>(props.token, 'cloud-download/profiles', { signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
  profiles.value = data.profiles || []
}

function editProfile(profile?: CloudDownloadProfile) {
  if (profileSaving.value) return
  profileError.value = ''
  profileNotice.value = ''
  profileDraft.value = profile ? { ...profile } : { id: '', name: '', accountId: configuredPrimary.value?.id || '', targetCID: '', targetPath: '' }
}

function openProfileDirectory() {
  if (!configuredPrimary.value) {
    profileError.value = '请先在 115 助手中登录主账号'
    return
  }
  if (profileDraft.value) profileDraft.value.accountId = configuredPrimary.value.id
  profileError.value = ''
  profileDirectoryDialogOpen.value = true
}

function selectProfileDirectory(value: { id: string; path: string }) {
  if (profileDraft.value) {
    profileDraft.value.targetCID = value.id
    profileDraft.value.targetPath = value.path
  }
  profileDirectoryDialogOpen.value = false
}

async function saveProfile() {
  const draft = profileDraft.value
  if (!draft || profileSaving.value) return
  const account = configuredPrimary.value
  if (!account) { profileError.value = '请先在 115 助手中登录主账号'; return }
  if (!draft.name.trim()) { profileError.value = '请填写配置名称'; return }
  if (!draft.targetCID || !draft.targetPath) { profileError.value = '请选择转存目录'; return }
  profileSaving.value = true
  profileError.value = ''
  try {
    const result = await p115Request<{ profile: CloudDownloadProfile }>(props.token, 'cloud-download/profiles' + (draft.id ? '/' + encodeURIComponent(draft.id) : ''), {
      method: draft.id ? 'PUT' : 'POST',
      body: JSON.stringify({ name: draft.name.trim(), accountId: account.id, targetCID: draft.targetCID, targetPath: draft.targetPath }),
      signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]),
    })
    if (draft.id) profiles.value = profiles.value.map(profile => profile.id === result.profile.id ? result.profile : profile)
    else profiles.value = [...profiles.value, result.profile]
    profileDraft.value = undefined
    profileNotice.value = draft.id ? '转存配置已更新' : '转存配置已创建'
  } catch (reason) {
    if (!lifecycle.signal.aborted) profileError.value = reason instanceof Error ? reason.message : '保存转存配置失败'
  } finally { profileSaving.value = false }
}

async function removeProfile() {
  const profile = deletingProfile.value
  if (!profile || profileSaving.value) return
  profileSaving.value = true
  profileError.value = ''
  try {
    await p115Request<void>(props.token, 'cloud-download/profiles/' + encodeURIComponent(profile.id), { method: 'DELETE', signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    profiles.value = profiles.value.filter(item => item.id !== profile.id)
    deletingProfile.value = undefined
    profileNotice.value = '转存配置已删除'
  } catch (reason) {
    if (!lifecycle.signal.aborted) profileError.value = reason instanceof Error ? reason.message : '删除转存配置失败'
  } finally { profileSaving.value = false }
}

function openDirectoryDialog() {
  if (!primaryAccount.value?.configured) {
    error.value = '请先在 115 助手中登录主账号'
    return
  }
  error.value = ''
  directoryDialogOpen.value = true
}

async function selectDirectory(value: { id: string; path: string; name: string }) {
  directoryDialogOpen.value = false
  targetCID.value = value.id
  targetPath.value = value.path
  await saveConfig()
}

async function submit() {
  if (!canSubmit.value) return
  submitting.value = true; error.value = ''; notice.value = ''
  // 提交开始后固定账号、链接和目录，保存配置期间的界面改动不改变本次任务。
  const snapshot = { accountId: accountId.value, targetCID: targetCID.value, targetPath: targetPath.value }
  const rawURL = url.value.trim()
  try {
    await p115Request(props.token, 'cloud-download/config', { method: 'PUT', body: JSON.stringify(snapshot), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    const receipt = await submitCloudDownload(props.token, snapshot.accountId, rawURL, snapshot.targetCID, lifecycle.signal)
    notice.value = cloudDownloadNotice(receipt, snapshot.targetPath)
    if (url.value.trim() === rawURL) url.value = ''
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '提交云下载失败'
  } finally { submitting.value = false }
}

async function saveConfig() {
  if (!primaryAccount.value?.configured) {
    error.value = '请先在 115 助手中登录主账号'
    return
  }
  applyingConfig.value = true; error.value = ''
  try {
    await p115Request(props.token, 'cloud-download/config', { method: 'PUT', body: JSON.stringify({ accountId: accountId.value, targetCID: targetCID.value, targetPath: targetPath.value }), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    notice.value = '下载目录已保存。'
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存云下载配置失败'
  } finally { applyingConfig.value = false }
}

async function refresh() {
  error.value = ''
  try { await loadAccounts(); await Promise.all([loadConfig(), loadProfiles()]) }
  catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取 115 配置失败' }
}

watch(accountId, () => {
  if (applyingConfig.value) return
  targetCID.value = ''; targetPath.value = '请选择下载目录'
}, { flush: 'sync' })
onMounted(async () => { await refresh() })
onBeforeUnmount(() => { lifecycle.abort() })
</script>

<template>
  <ManagementLayout title="磁力转存" description="将磁力或 ED2K 链接转存到指定目录。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <div class="max-w-3xl">
      <section class="rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-5 shadow-sm sm:p-6">
        <h2 class="text-base font-semibold text-slate-900 dark:text-slate-100">磁力转存</h2>
        <p class="mt-1 text-sm leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">提交磁力或 ED2K 到指定目录。相同的已结束任务会自动删除记录后重新提交，网盘文件保留；运行中的任务不会删除。</p>
        <P115PrimaryStatus v-if="accountsLoaded" :accounts="accounts" class="mt-5" />
        <label class="mt-5 block text-sm font-medium text-slate-700 dark:text-slate-300">磁力或 ED2K 链接<textarea v-model="url" rows="4" maxlength="8192" placeholder="magnet:?xt=urn:btih:… 或 ed2k://|file|…" class="mt-2 block w-full resize-y rounded-xl border border-slate-200 dark:border-slate-700 p-3 text-sm text-slate-700 dark:text-slate-300 outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-100" /></label>
        <div class="mt-5 flex items-center justify-between gap-4 rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-4"><div class="min-w-0"><p class="text-sm font-medium text-slate-700 dark:text-slate-300">下载目录</p><p class="mt-1 truncate text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ currentName }}（{{ targetCID }}）</p></div><button type="button" :disabled="!accountId || applyingConfig" class="shrink-0 rounded-lg border border-blue-200 bg-blue-50 px-3 py-2 text-xs text-blue-700 disabled:opacity-40" @click="openDirectoryDialog">选择目录</button></div>
        <p v-if="error" role="alert" class="mt-4 rounded-xl bg-red-50 p-3 text-sm leading-6 text-red-700">{{ error }}</p>
        <p v-if="notice" role="status" class="mt-4 rounded-xl bg-emerald-50 p-3 text-sm leading-6 text-emerald-700">{{ notice }}</p>
        <button type="button" :disabled="!canSubmit" class="mt-5 w-full rounded-xl bg-blue-600 px-4 py-3 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-40" @click="submit">{{ submitting ? '正在转存…' : '开始磁力转存' }}</button>
      </section>
      <section class="mt-6 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-700 dark:bg-slate-900 sm:p-6" aria-labelledby="named-targets-title">
        <header class="flex flex-wrap items-start justify-between gap-3">
          <div class="min-w-0"><h2 id="named-targets-title" class="text-base font-semibold text-slate-900 dark:text-slate-100">转存配置</h2><p class="mt-1 text-sm leading-6 text-slate-500 dark:text-slate-400">为 Telegram 磁力转存功能准备命名目录。Bot 页面可将每个配置分配为独立功能。</p></div>
          <button type="button" class="inline-flex h-9 shrink-0 items-center gap-1.5 rounded-xl bg-blue-600 px-3 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40" :disabled="profileSaving" @click="editProfile()"><IconPlus :size="16" aria-hidden="true" />新增配置</button>
        </header>
        <p v-if="profileError && !profileDraft && !deletingProfile" role="alert" class="mt-4 rounded-xl bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ profileError }}</p>
        <p v-if="profileNotice" role="status" class="mt-4 rounded-xl bg-emerald-50 p-3 text-sm text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300">{{ profileNotice }}</p>
        <div v-if="profiles.length" class="mt-4 divide-y divide-slate-100 rounded-xl border border-slate-200 dark:divide-slate-800 dark:border-slate-700">
          <article v-for="profile in profiles" :key="profile.id" class="flex min-w-0 flex-wrap items-center gap-3 p-3 sm:p-4">
            <span class="grid h-10 w-10 shrink-0 place-items-center rounded-xl bg-blue-50 text-blue-700 dark:bg-blue-950/60 dark:text-blue-300"><IconFolder :size="19" aria-hidden="true" /></span>
            <div class="min-w-0 flex-1"><h3 class="truncate text-sm font-semibold text-slate-800 dark:text-slate-100">{{ profile.name }}</h3><p class="mt-1 truncate text-xs text-slate-500 dark:text-slate-400" :title="profile.targetPath">{{ profile.targetPath }} <span class="font-mono">({{ profile.targetCID }})</span></p></div>
            <div class="flex shrink-0 items-center gap-1"><button type="button" class="grid h-9 w-9 place-items-center rounded-lg text-slate-500 hover:bg-slate-100 hover:text-blue-700 dark:text-slate-400 dark:hover:bg-slate-800 dark:hover:text-blue-300" :aria-label="'编辑' + profile.name" title="编辑" :disabled="profileSaving" @click="editProfile(profile)"><IconPencil :size="16" aria-hidden="true" /></button><button type="button" class="grid h-9 w-9 place-items-center rounded-lg text-slate-500 hover:bg-red-50 hover:text-red-700 dark:text-slate-400 dark:hover:bg-red-950/30 dark:hover:text-red-300" :aria-label="'删除' + profile.name" title="删除" :disabled="profileSaving" @click="deletingProfile = profile; profileError = ''"><IconTrash :size="16" aria-hidden="true" /></button></div>
          </article>
        </div>
        <div v-else class="mt-4 rounded-xl border border-dashed border-slate-300 px-4 py-8 text-center dark:border-slate-700"><p class="text-sm font-medium text-slate-700 dark:text-slate-200">还没有命名转存配置</p><p class="mt-1 text-xs text-slate-500 dark:text-slate-400">新增配置并选择目录后，可在 Telegram Bot 页面分配给 Bot。</p></div>
      </section>
    </div>
    <P115DirectoryDialog v-if="directoryDialogOpen" :token="token" :account-id="accountId" @close="directoryDialogOpen = false" @select="selectDirectory" />
    <P115ActionDialog v-if="profileDraft" :title="profileDraft.id ? '编辑转存配置' : '新增转存配置'" :busy="profileSaving" @close="profileDraft = undefined">
      <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">配置名称<input v-model="profileDraft.name" maxlength="50" required placeholder="例如：动画、电影" class="mt-2 w-full rounded-xl border border-slate-200 bg-slate-50 p-3 text-sm dark:border-slate-700 dark:bg-slate-950" /></label>
      <div><label class="text-sm font-medium text-slate-700 dark:text-slate-300">转存目录</label><div class="mt-2 flex min-w-0 items-center gap-3"><div class="min-w-0 flex-1 truncate rounded-xl border border-slate-200 bg-white p-3 text-sm text-slate-700 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-300">{{ profileDraft.targetPath || '尚未选择' }}<span v-if="profileDraft.targetCID" class="ml-1 font-mono text-xs text-slate-400">({{ profileDraft.targetCID }})</span></div><button type="button" class="shrink-0 rounded-xl border border-blue-200 bg-blue-50 px-3 py-3 text-sm text-blue-700 hover:bg-blue-100 disabled:opacity-40 dark:border-blue-900 dark:bg-blue-950/50 dark:text-blue-300" :disabled="profileSaving || !configuredPrimary" @click="openProfileDirectory">选择目录</button></div></div>
      <p v-if="profileError" role="alert" class="rounded-xl bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ profileError }}</p>
      <template #footer><button type="button" class="rounded-xl border border-slate-200 px-3 py-2 text-sm dark:border-slate-700" :disabled="profileSaving" @click="profileDraft = undefined">取消</button><button type="button" class="rounded-xl bg-blue-600 px-3 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40" :disabled="profileSaving || !profileDraft.name.trim() || !profileDraft.targetCID" @click="saveProfile">{{ profileSaving ? '保存中…' : '保存配置' }}</button></template>
    </P115ActionDialog>
    <P115DirectoryDialog v-if="profileDirectoryDialogOpen && configuredPrimary" :token="token" :account-id="configuredPrimary.id" @close="profileDirectoryDialogOpen = false" @select="selectProfileDirectory" />
    <P115ActionDialog v-if="deletingProfile" title="删除转存配置？" :busy="profileSaving" @close="deletingProfile = undefined"><p>确定删除“{{ deletingProfile.name }}”？分配给 Bot 的引用也会失效，Bot 不会自动改用其他目录。</p><p v-if="profileError" role="alert" class="mt-3 rounded-xl bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ profileError }}</p><template #footer><button type="button" class="rounded-xl border border-slate-200 px-3 py-2 text-sm dark:border-slate-700" :disabled="profileSaving" @click="deletingProfile = undefined">取消</button><button type="button" class="rounded-xl bg-red-600 px-3 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-40" :disabled="profileSaving" @click="removeProfile">{{ profileSaving ? '删除中…' : '删除配置' }}</button></template></P115ActionDialog>
  </ManagementLayout>
</template>
