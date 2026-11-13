<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import OrganizeTaskHistory from '../components/OrganizeTaskHistory.vue'
import { p115Request } from '../lib/p115-api'
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
const lifecycle = new AbortController()
const active = computed(() => pipelines.value.some(pipeline => !pipeline.finished) || jobs.value.some(job => ['queued', 'submitting', 'downloading', 'running'].includes(job.status)))
let timer: ReturnType<typeof setTimeout> | undefined
let revision = 0
let accountsLoaded = false
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
      accountsLoaded ? undefined : request<{ accounts: P115Account[] }>('accounts', { signal: read.signal }),
    ])
    if (lifecycle.signal.aborted || read.signal.aborted || current !== revision) return false
    if (accountData) { accounts.value = accountData.accounts; accountsLoaded = true }
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
function visibilityChanged() {
  if (document.hidden) { clearTimeout(timer); historyRead?.abort() }
  else if (!resuming.value && !retrying.value) void refresh()
}
onMounted(() => {
  document.addEventListener('visibilitychange', visibilityChanged)
  void refresh()
})
onBeforeUnmount(() => { lifecycle.abort(); clearTimeout(timer); document.removeEventListener('visibilitychange', visibilityChanged) })
</script>

<template>
  <ManagementLayout title="下载整理" description="每次提交保留一条记录，统一查看下载、整理与 STRM 进度；失败时只继续对应阶段。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <OrganizeTaskHistory :jobs="jobs" :pipelines="pipelines" :accounts="accounts" :ready="ready" :error="error" :refreshing="refreshing" :refreshed-at="refreshedAt" :resuming="resuming" :resume-error="resumeError" :retrying="retrying" :retry-error="retryError" @refresh="refresh" @resume="resume" @retry-share="retryShare" />
  </ManagementLayout>
</template>
