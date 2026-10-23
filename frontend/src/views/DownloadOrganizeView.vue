<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import OrganizeTaskHistory from '../components/OrganizeTaskHistory.vue'
import { p115Request } from '../lib/p115-api'
import type { OrganizeJob } from '../types/manualOrganize'
import type { P115Account } from '../types/p115Accounts'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const jobs = ref<OrganizeJob[]>([]), accounts = ref<P115Account[]>([])
const ready = ref(false), refreshing = ref(false), error = ref(''), refreshedAt = ref('')
const resuming = ref(''), resumeError = ref<{ id: string; message: string }>()
const lifecycle = new AbortController()
const active = computed(() => jobs.value.some(job => ['queued', 'submitting', 'downloading', 'running'].includes(job.status)))
let timer: ReturnType<typeof setTimeout> | undefined
let revision = 0
let accountsLoaded = false
let historyRead: AbortController | undefined
const request = <T,>(path: string, init: RequestInit = {}) => p115Request<T>(props.token, path, { ...init, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000), ...(init.signal ? [init.signal] : [])]) })
const message = (reason: unknown) => reason instanceof Error ? reason.message : '读取任务失败，请重试'

async function refresh() {
  if (lifecycle.signal.aborted) return
  const current = ++revision
  historyRead?.abort()
  const read = historyRead = new AbortController()
  clearTimeout(timer)
  refreshing.value = true
  try {
    const [data, accountData] = await Promise.all([
      request<{ jobs: OrganizeJob[] }>('garden-organize/jobs', { signal: read.signal }),
      accountsLoaded ? undefined : request<{ accounts: P115Account[] }>('accounts', { signal: read.signal }),
    ])
    if (lifecycle.signal.aborted || read.signal.aborted || current !== revision) return
    if (accountData) { accounts.value = accountData.accounts; accountsLoaded = true }
    jobs.value = data.jobs
    error.value = ''; ready.value = true; refreshedAt.value = new Date().toISOString()
  } catch (reason) { if (!lifecycle.signal.aborted && !read.signal.aborted && current === revision) error.value = message(reason) }
  finally { if (current === revision) { refreshing.value = false; schedule() } }
}
function schedule() {
  clearTimeout(timer)
  if (!lifecycle.signal.aborted && !document.hidden && !resuming.value) timer = setTimeout(refresh, active.value ? 2000 : 10000)
}
async function resume(job: OrganizeJob) {
  if (resuming.value || !ready.value || error.value || !job.resumable) return
  resuming.value = job.id; resumeError.value = undefined
  clearTimeout(timer)
  historyRead?.abort()
  let uncertain = false
  try {
    const result = await request<OrganizeJob>('garden-organize/resume', { method: 'POST', body: JSON.stringify({ jobId: job.id }) })
    jobs.value = jobs.value.map(old => old.id === result.id ? result : old)
  } catch (reason) {
    if (!lifecycle.signal.aborted) {
      const status = (reason as { status?: number }).status
      uncertain = !status || status >= 500
      resumeError.value = { id: job.id, message: uncertain ? '恢复结果暂未确认，请刷新记录确认任务状态后重试；不会自动重新提交离线。' : message(reason) }
    }
  } finally {
    await refresh()
    if (uncertain && jobs.value.some(current => current.id === job.id && ['queued', 'submitting', 'downloading', 'running', 'completed'].includes(current.status))) resumeError.value = undefined
    resuming.value = ''
    schedule()
  }
}
function visibilityChanged() {
  if (document.hidden) { clearTimeout(timer); historyRead?.abort() }
  else if (!resuming.value) void refresh()
}
onMounted(() => {
  document.addEventListener('visibilitychange', visibilityChanged)
  void refresh()
})
onBeforeUnmount(() => { lifecycle.abort(); clearTimeout(timer); document.removeEventListener('visibilitychange', visibilityChanged) })
</script>

<template>
  <ManagementLayout title="下载整理" description="详情页发起的下载整理任务。继续任务会保留原定目录、命名与已完成进度。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <OrganizeTaskHistory :jobs="jobs" :cleanup-jobs="[]" :subtitle-jobs="[]" :accounts="accounts" tab="organize" organize-only :ready="ready" :error="error" :refreshing="refreshing" :refreshed-at="refreshedAt" :busy="false" :resuming="resuming" :resume-error="resumeError" @refresh="refresh" @resume="resume" />
  </ManagementLayout>
</template>
