<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115PrimaryStatus from '../components/P115PrimaryStatus.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import { p115Request } from '../lib/p115-api'
import { cloudDownloadNotice, submitCloudDownload } from '../lib/cloud-download'
import type { P115Account } from '../types/p115Accounts'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const lifecycle = new AbortController()
const accounts = ref<P115Account[]>([])
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

const primaryAccount = computed(() => accounts.value.find(account => account.primary && account.id === accountId.value))
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
  try { await loadAccounts(); await loadConfig() }
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
    </div>
    <P115DirectoryDialog v-if="directoryDialogOpen" :token="token" :account-id="accountId" @close="directoryDialogOpen = false" @select="selectDirectory" />
  </ManagementLayout>
</template>
