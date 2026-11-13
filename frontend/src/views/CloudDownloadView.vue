<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { IconCloudDownload, IconMagnet, IconShare } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115PrimaryStatus from '../components/P115PrimaryStatus.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import P115TransferTargetSettings from '../components/P115TransferTargetSettings.vue'
import { p115Request } from '../lib/p115-api'
import { cloudDownloadNotice, submitCloudDownload } from '../lib/cloud-download'
import type { P115Account } from '../types/p115Accounts'

type TransferTab = 'magnet' | 'share'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const lifecycle = new AbortController()
const route = useRoute()
const activeTab = ref<TransferTab>(route.query.tab === 'share' ? 'share' : 'magnet')
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
const configuredPrimary = computed(() => accounts.value.find(account => account.primary && account.configured))
const currentName = computed(() => targetCID.value === '0' ? '115 根目录' : targetPath.value)
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

async function selectDirectory(value: { id: string; path: string }) {
  directoryDialogOpen.value = false
  targetCID.value = value.id
  targetPath.value = value.path
  await saveConfig()
}

async function submit() {
  if (!canSubmit.value) return
  submitting.value = true
  error.value = ''
  notice.value = ''
  const snapshot = { accountId: accountId.value, targetCID: targetCID.value, targetPath: targetPath.value }
  const rawURL = url.value.trim()
  try {
    await p115Request(props.token, 'cloud-download/config', { method: 'PUT', body: JSON.stringify(snapshot), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    const receipt = await submitCloudDownload(props.token, snapshot.accountId, rawURL, snapshot.targetCID, lifecycle.signal)
    notice.value = cloudDownloadNotice(receipt, snapshot.targetPath)
    if (url.value.trim() === rawURL) url.value = ''
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '提交云下载失败'
  } finally {
    submitting.value = false
  }
}

async function saveConfig() {
  if (!primaryAccount.value?.configured) {
    error.value = '请先在 115 助手中登录主账号'
    return
  }
  applyingConfig.value = true
  error.value = ''
  try {
    await p115Request(props.token, 'cloud-download/config', { method: 'PUT', body: JSON.stringify({ accountId: accountId.value, targetCID: targetCID.value, targetPath: targetPath.value }), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    notice.value = '下载目录已保存。'
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存云下载配置失败'
  } finally {
    applyingConfig.value = false
  }
}

async function refresh() {
  error.value = ''
  try {
    await loadAccounts()
    await loadConfig()
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取 115 配置失败'
  }
}

watch(accountId, () => {
  if (applyingConfig.value) return
  targetCID.value = ''
  targetPath.value = '请选择下载目录'
}, { flush: 'sync' })
onMounted(refresh)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout title="115 云转存" description="管理磁力下载和 Telegram 分享链接的接收目录。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <div class="max-w-4xl">
      <div class="border-b border-slate-200 dark:border-slate-700" role="tablist" aria-label="115 转存类型">
        <button id="magnet-tab" type="button" role="tab" :aria-selected="activeTab === 'magnet'" aria-controls="magnet-panel" class="mr-5 inline-flex items-center gap-2 border-b-2 px-1 py-3 text-sm font-medium transition" :class="activeTab === 'magnet' ? 'border-blue-600 text-blue-700 dark:border-blue-400 dark:text-blue-300' : 'border-transparent text-slate-500 hover:text-slate-800 dark:text-slate-400 dark:hover:text-slate-200'" @click="activeTab = 'magnet'; error = ''; notice = ''"><IconMagnet :size="17" aria-hidden="true" />磁力/ED2K 转存</button>
        <button id="share-tab" type="button" role="tab" :aria-selected="activeTab === 'share'" aria-controls="share-panel" class="inline-flex items-center gap-2 border-b-2 px-1 py-3 text-sm font-medium transition" :class="activeTab === 'share' ? 'border-blue-600 text-blue-700 dark:border-blue-400 dark:text-blue-300' : 'border-transparent text-slate-500 hover:text-slate-800 dark:text-slate-400 dark:hover:text-slate-200'" @click="activeTab = 'share'; error = ''; notice = ''"><IconShare :size="17" aria-hidden="true" />115 链接转存</button>
      </div>

      <section v-if="activeTab === 'magnet'" id="magnet-panel" role="tabpanel" aria-labelledby="magnet-tab" class="space-y-5 pt-5">
        <section class="rounded-xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-700 dark:bg-slate-900 sm:p-6">
          <div class="flex items-start gap-3"><span class="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-blue-50 text-blue-700 dark:bg-blue-950/60 dark:text-blue-300"><IconCloudDownload :size="19" aria-hidden="true" /></span><div><h2 class="text-base font-semibold text-slate-900 dark:text-slate-100">手动提交磁力或 ED2K</h2><p class="mt-1 text-sm leading-6 text-slate-500 dark:text-slate-400">相同的已结束任务会删除任务记录后重新提交，网盘文件保留；运行中的任务不会删除。</p></div></div>
          <P115PrimaryStatus v-if="accountsLoaded" :accounts="accounts" class="mt-4" />
          <label class="mt-4 block text-sm font-medium text-slate-700 dark:text-slate-300">磁力或 ED2K 链接<textarea v-model="url" rows="3" maxlength="8192" placeholder="magnet:?xt=urn:btih:… 或 ed2k://|file|…" class="mt-2 block w-full resize-y rounded-lg border border-slate-200 bg-white p-3 text-sm text-slate-700 outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-100 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-200" /></label>
          <div class="mt-4 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-slate-200 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-950"><div class="min-w-0"><p class="text-sm font-medium text-slate-700 dark:text-slate-300">下载目录</p><p class="mt-1 truncate text-xs text-slate-500 dark:text-slate-400">{{ currentName }}（{{ targetCID || '未选择' }}）</p></div><button type="button" :disabled="!accountId || applyingConfig" class="shrink-0 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-100 disabled:opacity-40 dark:border-slate-600 dark:bg-slate-900 dark:text-slate-200 dark:hover:bg-slate-800" @click="openDirectoryDialog">选择目录</button></div>
          <p v-if="error" role="alert" class="mt-4 rounded-lg bg-red-50 p-3 text-sm leading-6 text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
          <p v-if="notice" role="status" class="mt-4 rounded-lg bg-emerald-50 p-3 text-sm leading-6 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300">{{ notice }}</p>
          <div class="mt-4 flex justify-end"><button type="button" :disabled="!canSubmit" class="inline-flex min-h-10 items-center gap-2 rounded-lg bg-blue-600 px-4 text-sm font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-40" @click="submit"><IconCloudDownload :size="17" aria-hidden="true" />{{ submitting ? '正在提交…' : '开始云下载' }}</button></div>
        </section>
        <P115TransferTargetSettings :token="token" endpoint="cloud-download/profiles" :account-id="configuredPrimary?.id || ''" :account-ready="!!configuredPrimary" title="Bot 磁力/ED2K 转存目录" description="每个目录是一个独立 Bot 功能。一个 Bot 可分配多个目录，私聊时由发送者选择。" empty-text="还没有 Bot 磁力/ED2K 转存目录" />
      </section>

      <section v-else id="share-panel" role="tabpanel" aria-labelledby="share-tab" class="space-y-5 pt-5">
        <section class="rounded-xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900 sm:p-6">
          <div class="flex items-start gap-3"><span class="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200"><IconShare :size="18" aria-hidden="true" /></span><div><h2 class="text-base font-semibold text-slate-900 dark:text-slate-100">115 分享链接接收目录</h2><p class="mt-1 text-sm leading-6 text-slate-500 dark:text-slate-400">网页资源搜索与 Telegram 共用转存目录，直接保存分享内容，不生成 STRM。网页只有一个有效目录时自动使用；多个目录选择一次后继续复用。</p></div></div>
          <P115PrimaryStatus v-if="accountsLoaded" :accounts="accounts" class="mt-4" />
        </section>
        <P115TransferTargetSettings :token="token" endpoint="share-transfer/profiles" :account-id="configuredPrimary?.id || ''" :account-ready="!!configuredPrimary" title="115 链接转存目录" description="创建后即可在网页搜索结果中选择；也可在 Telegram Bot 配置页将目录分配给 Bot，无需绑定 Bot 即可用于网页。" empty-text="还没有 115 链接转存目录" />
      </section>
    </div>
    <P115DirectoryDialog v-if="directoryDialogOpen" :token="token" :account-id="accountId" @close="directoryDialogOpen = false" @select="selectDirectory" />
  </ManagementLayout>
</template>
