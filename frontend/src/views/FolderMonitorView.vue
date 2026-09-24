<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import DirectoryShareButton from '../components/DirectoryShareButton.vue'
import { p115Request } from '../lib/p115-api'
import { directoryShareActive, type DirectoryShareConfig } from '../composables/useDirectoryShare'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const lifecycle = new AbortController()
const monitor = ref<DirectoryShareConfig>()
const accountId = ref('')
const directoryAccountId = ref('')
const sourceCID = ref('')
const sourcePath = ref('')
const sourceDialog = ref(false)
const loading = ref(true)
const saving = ref(false)
const error = ref('')
const notice = ref('')
const buttonRevision = ref(0)
const active = computed(() => directoryShareActive(monitor.value))
const editable = computed(() => !active.value && !monitor.value?.hasBatch)
const dirty = computed(() => sourceCID.value !== (monitor.value?.sourceCID || '') || directoryAccountId.value !== (monitor.value?.accountId || ''))
const bindingChanged = computed(() => !!directoryAccountId.value && directoryAccountId.value !== accountId.value)
function request<T>(path: string, init: RequestInit = {}) {
  return p115Request<T>(props.token, path, { ...init, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(20000)]) })
}
function setMonitor(value?: DirectoryShareConfig) {
  monitor.value = value
  sourceCID.value = value?.sourceCID || ''; sourcePath.value = value?.sourcePath || ''
  directoryAccountId.value = value?.accountId || ''
}
function updateStatus(value?: DirectoryShareConfig) {
  if (!saving.value) monitor.value = value
}
async function refreshSettings() {
  const settings = await request<{ settings: { accountId: string } }>('share/automation-config')
  accountId.value = settings.settings.accountId || ''
}
async function openSource() {
  error.value = ''
  try {
    await refreshSettings()
    if (!accountId.value) throw new Error('请先在 115 分享 STRM 中选择已登录账号')
    sourceDialog.value = true
  } catch (reason) { error.value = reason instanceof Error ? reason.message : '读取分享设置失败' }
}
function selectSource(value: { id: string; path: string }) {
  sourceDialog.value = false
  if (value.id === '0') { error.value = '请选择非根目录'; return }
  sourceCID.value = value.id; sourcePath.value = value.path; directoryAccountId.value = accountId.value
}
async function save() {
  if (!sourceCID.value || !directoryAccountId.value) { error.value = '请选择分享目录'; return }
  saving.value = true; error.value = ''; notice.value = ''
  try {
    const id = monitor.value?.id
    const value = await request<DirectoryShareConfig>(id ? `folder-monitors/${id}` : 'folder-monitors', {
      method: id ? 'PUT' : 'POST', body: JSON.stringify({ accountId: directoryAccountId.value, sourceCID: sourceCID.value, sourcePath: sourcePath.value }),
    })
    setMonitor(value); buttonRevision.value++
    notice.value = '目录已保存，点击分享按钮才会执行。'
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '操作失败' }
  finally { saving.value = false }
}
async function remove() {
  if (!monitor.value || !editable.value || !window.confirm('删除目录分享配置？')) return
  saving.value = true; error.value = ''; notice.value = ''
  try {
    await request(`folder-monitors/${monitor.value.id}`, { method: 'DELETE' })
    setMonitor(); buttonRevision.value++; notice.value = '目录分享配置已删除。'
  } catch (reason) { error.value = reason instanceof Error ? reason.message : '操作失败' }
  finally { saving.value = false }
}
onMounted(async () => {
  document.title = '115 目录分享 · Cinema115'
  try {
    const [data] = await Promise.all([request<{ monitors: DirectoryShareConfig[] }>('folder-monitors'), refreshSettings()])
    if (data.monitors.length === 1) setMonitor(data.monitors[0])
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '操作失败' }
  finally { loading.value = false }
})
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout title="115 目录分享" description="整理完成后，手动分享指定目录并生成 STRM。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <section class="space-y-6 rounded-2xl border border-slate-200 bg-white p-5 shadow-sm sm:p-6" :aria-busy="loading">
      <div>
        <h2 class="font-semibold text-slate-900">分享目录</h2>
        <p class="mt-2 text-xs leading-6 text-slate-500">账号、输出目录和生成规则使用「115 分享 STRM」已保存的设置。每次提交时固定设置，已有任务继续使用原账号。</p>
        <RouterLink to="/115/share" class="text-xs text-blue-600 hover:underline">查看分享 STRM 设置 ↗</RouterLink>
      </div>
      <div>
        <p class="text-sm font-medium text-slate-700">指定目录</p>
        <div class="mt-2 flex items-center gap-3 rounded-xl border border-slate-200 bg-slate-50 px-4 py-3">
          <p class="min-w-0 flex-1 break-all text-sm text-slate-600">{{ sourcePath || '请选择需要分享的目录' }}</p>
          <button type="button" :disabled="loading || saving || !editable" class="shrink-0 rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs text-slate-700 disabled:opacity-40" @click="openSource">选择目录</button>
        </div>
        <p v-if="bindingChanged && !monitor?.hasBatch" class="mt-2 text-xs text-amber-700">分享账号已变化，请重新选择目录并保存。</p>
        <p v-if="!editable" class="mt-2 text-xs text-slate-500">本批次尚未结束，完成或恢复当前任务后可修改目录。</p>
      </div>
      <div class="flex gap-3">
        <button type="button" :disabled="loading || saving || !editable || !sourceCID || bindingChanged" class="rounded-xl border border-slate-200 px-5 py-2.5 text-sm text-slate-700 disabled:opacity-40" @click="save">{{ saving ? '保存中…' : '保存目录' }}</button>
        <button v-if="monitor" type="button" :disabled="loading || saving || !editable" class="rounded-xl px-3 py-2.5 text-sm text-red-600 disabled:opacity-40" @click="remove">删除配置</button>
      </div>
      <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
      <p v-if="notice" role="status" class="rounded-xl bg-emerald-50 p-3 text-sm text-emerald-700">{{ notice }}</p>
      <div class="border-t border-slate-100 pt-5">
        <DirectoryShareButton v-if="!loading" :key="buttonRevision" :token="token" :configuration-id="monitor?.id" :disabled="dirty || saving" disabled-reason="请先保存目录设置" @updated="updateStatus" />
      </div>
      <details class="text-xs leading-6 text-slate-500">
        <summary class="cursor-pointer">目录内容如何处理</summary>
        <p class="mt-2">点击后检查目录，固定本批范围并搬入临时目录，创建长期分享后交给分享 STRM 任务生成。后来进入的内容留待下次点击；成功后临时目录移入回收站，失败时保留供重试。</p>
      </details>
      <div v-if="monitor?.hasBatch || monitor?.shareUrl" class="space-y-2 rounded-xl bg-slate-50 p-4 text-xs leading-6 text-slate-500">
        <p v-if="monitor.batchDirectoryName" class="break-all">临时目录：{{ monitor.batchDirectoryName }} · 已搬入 {{ monitor.batchMoved || 0 }} 项</p>
        <p v-if="monitor.generationTaskId" class="break-all">分享 STRM 任务：{{ monitor.generationTaskId }} <RouterLink to="/115/share" class="text-blue-600 hover:underline">查看分享任务日志</RouterLink></p>
        <a v-if="monitor.shareUrl" :href="monitor.shareUrl" target="_blank" rel="noopener noreferrer" class="text-blue-600 hover:underline">查看本次分享 ↗</a>
      </div>
    </section>
    <P115DirectoryDialog v-if="sourceDialog" :token="token" :account-id="accountId" title="选择分享目录" @close="sourceDialog = false" @select="selectSource" />
  </ManagementLayout>
</template>
