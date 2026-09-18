<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115AccountSelect from '../components/P115AccountSelect.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import { p115Request } from '../lib/p115-api'
import type { P115Account } from '../types/p115Accounts'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const lifecycle = new AbortController()
const accounts = ref<P115Account[]>([])
const accountId = ref('')
const url = ref('')
const targetCID = ref('0')
const targetPath = ref('/')
const directoryDialogOpen = ref(false)
const submitting = ref(false)
const error = ref('')
const notice = ref('')
const applyingConfig = ref(false)

const selectedAccount = computed(() => accounts.value.find(account => account.id === accountId.value))
const currentName = computed(() => targetPath.value === '/' ? '115 根目录' : targetPath.value)
const canSubmit = computed(() => !!accountId.value && !!selectedAccount.value?.configured && !!url.value.trim() && !submitting.value)

async function loadAccounts() {
  const data = await p115Request<{ accounts: P115Account[] }>(props.token, 'accounts', { signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
  accounts.value = data.accounts
  if (accountId.value && !accounts.value.some(account => account.id === accountId.value)) accountId.value = ''
}

async function loadConfig() {
  const config = await p115Request<{ accountId: string; targetCID: string; targetPath: string }>(props.token, 'cloud-download/config', { signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
  applyingConfig.value = true
  accountId.value = config.accountId || ''
  targetCID.value = config.targetCID || '0'
  targetPath.value = config.targetPath || '/'
  if (targetCID.value === '0') targetPath.value = '/'
  applyingConfig.value = false
}

function openDirectoryDialog() {
  if (!accountId.value || !selectedAccount.value?.configured) {
    error.value = '请先选择已登录的 115 账号'
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
  try {
    await p115Request(props.token, 'cloud-download/config', { method: 'PUT', body: JSON.stringify({ accountId: accountId.value, targetCID: targetCID.value, targetPath: targetPath.value }), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    await p115Request(props.token, 'cloud-download/tasks?accountId=' + encodeURIComponent(accountId.value), { method: 'POST', body: JSON.stringify({ url: url.value.trim(), targetCID: targetCID.value }), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(35000)]) })
    notice.value = '磁力已提交到 115 云下载。'
    url.value = ''
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '提交云下载失败'
  } finally { submitting.value = false }
}

async function saveConfig() {
  if (!accountId.value || !selectedAccount.value?.configured) {
    error.value = '请先选择已登录的 115 账号'
    return
  }
  applyingConfig.value = true; error.value = ''
  try {
    await p115Request(props.token, 'cloud-download/config', { method: 'PUT', body: JSON.stringify({ accountId: accountId.value, targetCID: targetCID.value, targetPath: targetPath.value }), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    notice.value = '云下载账号和目录已保存。'
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
  targetCID.value = '0'; targetPath.value = '/'
}, { flush: 'sync' })
onMounted(async () => { await refresh() })
onBeforeUnmount(() => { lifecycle.abort() })
</script>

<template>
  <ManagementLayout title="磁力转存" description="将磁力链接转存到指定的 115 账号和目录。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <div class="max-w-3xl">
      <section class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm sm:p-6">
        <h2 class="text-base font-semibold text-slate-900">磁力转存</h2>
        <p class="mt-1 text-sm leading-6 text-slate-500">选择账号和目录后，提交 magnet 磁力链接到 115。</p>
        <div class="mt-5"><P115AccountSelect id="cloud-download-account" v-model="accountId" :accounts="accounts" /></div>
        <label class="mt-5 block text-sm font-medium text-slate-700">磁力或 ED2K 链接<textarea v-model="url" rows="4" maxlength="8192" placeholder="magnet:?xt=urn:btih:… 或 ed2k://|file|…" class="mt-2 block w-full resize-y rounded-xl border border-slate-200 p-3 text-sm text-slate-700 outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-100" /></label>
        <div class="mt-5 flex items-center justify-between gap-4 rounded-xl border border-slate-200 bg-slate-50 p-4"><div class="min-w-0"><p class="text-sm font-medium text-slate-700">下载目录</p><p class="mt-1 truncate text-xs text-slate-500">{{ currentName }}（{{ targetCID }}）</p></div><button type="button" :disabled="!accountId || applyingConfig" class="shrink-0 rounded-lg border border-blue-200 bg-blue-50 px-3 py-2 text-xs text-blue-700 disabled:opacity-40" @click="openDirectoryDialog">选择目录</button></div>
        <p v-if="error" role="alert" class="mt-4 rounded-xl bg-red-50 p-3 text-sm leading-6 text-red-700">{{ error }}</p>
        <p v-if="notice" role="status" class="mt-4 rounded-xl bg-emerald-50 p-3 text-sm leading-6 text-emerald-700">{{ notice }}</p>
        <button type="button" :disabled="!canSubmit" class="mt-5 w-full rounded-xl bg-blue-600 px-4 py-3 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-40" @click="submit">{{ submitting ? '正在转存…' : '开始磁力转存' }}</button>
      </section>
    </div>
    <P115DirectoryDialog v-if="directoryDialogOpen" :token="token" :account-id="accountId" @close="directoryDialogOpen = false" @select="selectDirectory" />
  </ManagementLayout>
</template>
