<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { IconFolder } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import OrganizeTaskHistory from '../components/OrganizeTaskHistory.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import P115PrimaryStatus from '../components/P115PrimaryStatus.vue'
import { p115Request } from '../lib/p115-api'
import { loadCloudDownloadConfig } from '../lib/cloud-download'
import { retryDownloadPipelineShares } from '../lib/download-pipeline'
import { trackDownloadPipeline } from '../composables/useDownloadPipelineToast'
import type { DownloadPipeline, OrganizeJob } from '../types/organize'
import type { P115Account } from '../types/p115Accounts'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const jobs = ref<OrganizeJob[]>([]), accounts = ref<P115Account[]>([])
const pipelines = ref<DownloadPipeline[]>([])
const ready = ref(false), refreshing = ref(false), error = ref(''), refreshedAt = ref('')
const resuming = ref(''), resumeError = ref<{ id: string; message: string }>()
const retrying = ref(''), retryError = ref<{ id: string; message: string }>()
const stagingAccountId = ref(''), stagingCID = ref(''), stagingPath = ref('')
const stagingLoading = ref(true), stagingSaving = ref(false), stagingError = ref(''), stagingNotice = ref('')
const directoryAccountId = ref('')
const lifecycle = new AbortController()
const active = computed(() => pipelines.value.some(pipeline => !pipeline.finished) || jobs.value.some(job => ['queued', 'submitting', 'downloading', 'running'].includes(job.status)))
const configuredPrimary = computed(() => accounts.value.find(account => account.primary && account.configured))
const stagingReady = computed(() => !!stagingCID.value && stagingAccountId.value === configuredPrimary.value?.id)
const stagingDisplayPath = computed(() => !stagingReady.value ? '尚未设置' : stagingCID.value === '0' ? '115 根目录' : stagingPath.value)
let timer: ReturnType<typeof setTimeout> | undefined
let revision = 0
const accountsLoaded = ref(false)
let historyRead: AbortController | undefined
const request = <T,>(path: string, init: RequestInit = {}) => p115Request<T>(props.token, path, { ...init, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000), ...(init.signal ? [init.signal] : [])]) })
const message = (reason: unknown) => reason instanceof Error ? reason.message : '读取任务失败，请重试'

async function refresh(): Promise<boolean> {
  if (lifecycle.signal.aborted) return false
  const current = ++revision
  historyRead?.abort()
  const read = historyRead = new AbortController()
  clearTimeout(timer)
  refreshing.value = true
  try {
    const [data, accountData] = await Promise.all([
      request<{ jobs: OrganizeJob[]; pipelines: DownloadPipeline[] }>('download-organize/jobs', { signal: read.signal }),
      accountsLoaded.value ? undefined : request<{ accounts: P115Account[] }>('accounts', { signal: read.signal }),
    ])
    if (lifecycle.signal.aborted || read.signal.aborted || current !== revision) return false
    if (accountData) { accounts.value = accountData.accounts; accountsLoaded.value = true }
    jobs.value = data.jobs
    pipelines.value = data.pipelines || []
    error.value = ''; ready.value = true; refreshedAt.value = new Date().toISOString()
    return true
  } catch (reason) { if (!lifecycle.signal.aborted && !read.signal.aborted && current === revision) error.value = message(reason) }
  finally { if (current === revision) { refreshing.value = false; schedule() } }
  return false
}
function schedule() {
  clearTimeout(timer)
  if (!lifecycle.signal.aborted && !document.hidden && !resuming.value && !retrying.value) timer = setTimeout(refresh, active.value ? 2000 : 10000)
}
async function resume(job: OrganizeJob) {
  if (resuming.value || retrying.value || !ready.value || error.value || !job.resumable) return
  resuming.value = job.id; resumeError.value = undefined
  clearTimeout(timer)
  historyRead?.abort()
  let uncertain = false
  try {
    const result = await request<OrganizeJob>('download-organize/resume', { method: 'POST', body: JSON.stringify({ jobId: job.id }) })
    jobs.value = jobs.value.map(old => old.id === result.id ? result : old)
    trackPipeline(pipelines.value.find(pipeline => pipeline.jobIds.includes(job.id)))
  } catch (reason) {
    if (!lifecycle.signal.aborted) {
      const status = (reason as { status?: number }).status
      uncertain = !status || status >= 500
      resumeError.value = { id: job.id, message: uncertain ? '恢复结果暂未确认，请刷新记录确认任务状态后重试；不会自动重新提交离线。' : message(reason) }
    }
  } finally {
    const refreshed = await refresh()
    if (uncertain && refreshed && jobs.value.some(current => current.id === job.id && ['queued', 'submitting', 'downloading', 'running', 'completed'].includes(current.status) && (current.status !== job.status || current.updatedAt !== job.updatedAt))) {
      resumeError.value = undefined
      trackPipeline(pipelines.value.find(pipeline => pipeline.jobIds.includes(job.id)))
    }
    resuming.value = ''
    schedule()
  }
}
async function retryShare(id: string) {
  if (retrying.value || resuming.value || !ready.value || error.value || !pipelines.value.some(pipeline => pipeline.shares.some(share => share.id === id && share.canRetry))) return
  retrying.value = id; retryError.value = undefined
  clearTimeout(timer)
  historyRead?.abort()
  let uncertain = false
  try {
    await retryDownloadPipelineShares(props.token, [id], AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]))
    trackPipeline(pipelines.value.find(pipeline => pipeline.shares.some(share => share.id === id)))
  }
  catch (reason) {
    if (!lifecycle.signal.aborted) {
      const status = (reason as { status?: number }).status
      uncertain = !status || status >= 500
      retryError.value = { id, message: uncertain ? '重试结果暂未确认，请刷新记录核对；不会自动重复提交。' : message(reason) }
    }
  }
  finally {
    const refreshed = await refresh()
    if (uncertain && refreshed && pipelines.value.some(pipeline => pipeline.shares.some(share => share.id === id && ['retry_queued', 'running', 'success'].includes(share.status)))) {
      retryError.value = undefined
      trackPipeline(pipelines.value.find(pipeline => pipeline.shares.some(share => share.id === id)))
    }
    retrying.value = ''; schedule()
  }
}
function trackPipeline(pipeline?: DownloadPipeline) {
  if (pipeline && !lifecycle.signal.aborted) trackDownloadPipeline(props.token, pipeline.id, `${pipeline.title} · ${pipeline.total} 个资源`, true)
}
async function loadStagingConfig() {
  stagingError.value = ''
  try {
    const config = await loadCloudDownloadConfig(props.token, lifecycle.signal)
    if (lifecycle.signal.aborted) return
    stagingAccountId.value = config.accountId || ''
    stagingCID.value = config.targetCID || ''
    stagingPath.value = config.targetCID === '0' ? '/' : config.targetPath || ''
  } catch (reason) {
    if (!lifecycle.signal.aborted) stagingError.value = reason instanceof Error ? reason.message : '读取整理下载暂存目录失败'
  } finally {
    stagingLoading.value = false
  }
}

async function saveStagingDirectory(value: { id: string; path: string }) {
  const accountId = directoryAccountId.value
  directoryAccountId.value = ''
  if (!accountId || stagingSaving.value) return
  stagingSaving.value = true
  stagingError.value = ''
  stagingNotice.value = ''
  try {
    await request('cloud-download/config', {
      method: 'PUT',
      body: JSON.stringify({ accountId, targetCID: value.id, targetPath: value.path }),
    })
    stagingAccountId.value = accountId
    stagingCID.value = value.id
    stagingPath.value = value.id === '0' ? '/' : value.path
    stagingNotice.value = '整理下载暂存目录已保存。'
  } catch (reason) {
    if (!lifecycle.signal.aborted) stagingError.value = reason instanceof Error ? reason.message : '保存整理下载暂存目录失败'
  } finally {
    stagingSaving.value = false
  }
}

function visibilityChanged() {
  if (document.hidden) { clearTimeout(timer); historyRead?.abort() }
  else if (!resuming.value && !retrying.value) void refresh()
}
onMounted(() => {
  document.addEventListener('visibilitychange', visibilityChanged)
  void refresh()
  void loadStagingConfig()
})
onBeforeUnmount(() => { lifecycle.abort(); clearTimeout(timer); document.removeEventListener('visibilitychange', visibilityChanged) })
</script>

<template>
  <ManagementLayout title="下载整理" description="每次提交保留一条记录，统一查看下载、整理与 STRM 进度；失败时只继续对应阶段。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <section class="mb-5 rounded-xl border border-slate-200 bg-white p-4 shadow-sm dark:border-slate-700 dark:bg-slate-900 sm:p-5" aria-label="整理下载暂存目录">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex min-w-0 items-start gap-3">
          <span class="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-blue-50 text-blue-700 dark:bg-blue-950/60 dark:text-blue-300"><IconFolder :size="18" aria-hidden="true" /></span>
          <div class="min-w-0">
            <h2 class="text-sm font-semibold text-slate-900 dark:text-slate-100">整理下载暂存目录</h2>
            <p class="mt-0.5 text-xs leading-5 text-slate-500 dark:text-slate-400">下载整理和分享 STRM 的临时文件夹会建在这里。</p>
            <p v-if="!stagingLoading && !stagingError" class="mt-1 truncate text-xs text-slate-700 dark:text-slate-200" :title="stagingDisplayPath">{{ stagingDisplayPath }}</p>
          </div>
        </div>
        <button type="button" :disabled="!configuredPrimary || stagingLoading || stagingSaving" class="shrink-0 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm font-medium text-slate-700 hover:bg-slate-100 disabled:opacity-40 dark:border-slate-600 dark:bg-slate-900 dark:text-slate-200 dark:hover:bg-slate-800" @click="directoryAccountId = configuredPrimary?.id || ''">{{ stagingSaving ? '保存中…' : '选择目录' }}</button>
      </div>
      <P115PrimaryStatus v-if="accountsLoaded" :accounts="accounts" class="mt-3" />
      <p v-if="stagingError" role="alert" class="mt-3 rounded-lg bg-red-50 p-2.5 text-xs leading-5 text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ stagingError }}</p>
      <p v-else-if="stagingNotice" role="status" class="mt-3 rounded-lg bg-emerald-50 p-2.5 text-xs leading-5 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300">{{ stagingNotice }}</p>
    </section>
    <OrganizeTaskHistory :jobs="jobs" :pipelines="pipelines" :accounts="accounts" :ready="ready" :error="error" :refreshing="refreshing" :refreshed-at="refreshedAt" :resuming="resuming" :resume-error="resumeError" :retrying="retrying" :retry-error="retryError" @refresh="refresh" @resume="resume" @retry-share="retryShare" />
    <P115DirectoryDialog v-if="directoryAccountId" :token="token" :account-id="directoryAccountId" title="选择整理下载暂存目录" @close="directoryAccountId = ''" @select="saveStagingDirectory" />
  </ManagementLayout>
</template>
