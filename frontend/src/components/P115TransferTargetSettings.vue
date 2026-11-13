<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { IconFolder, IconPencil, IconPlus, IconTrash } from '@tabler/icons-vue'
import P115ActionDialog from './P115ActionDialog.vue'
import P115DirectoryDialog from './P115DirectoryDialog.vue'
import { p115Request } from '../lib/p115-api'

interface TransferTarget {
  id: string
  name: string
  accountId: string
  targetCID: string
  targetPath: string
}

const props = defineProps<{
  token: string
  endpoint: 'cloud-download/profiles' | 'share-transfer/profiles'
  accountId: string
  accountReady: boolean
  title: string
  description: string
  emptyText: string
}>()

const lifecycle = new AbortController()
const targets = ref<TransferTarget[]>([])
const loading = ref(true)
const busy = ref(false)
const error = ref('')
const notice = ref('')
const draft = ref<TransferTarget>()
const deleting = ref<TransferTarget>()
const directoryOpen = ref(false)
const listError = computed(() => loading.value ? '' : error.value)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await p115Request<{ profiles: TransferTarget[] }>(props.token, props.endpoint, { signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    targets.value = data.profiles || []
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取转存目录失败'
  } finally {
    loading.value = false
  }
}

function edit(target?: TransferTarget) {
  if (busy.value) return
  error.value = ''
  notice.value = ''
  const value = target ? { ...target } : { id: '', name: '', accountId: props.accountId, targetCID: '', targetPath: '' }
  if (target && target.accountId !== props.accountId) {
    value.accountId = props.accountId
    value.targetCID = ''
    value.targetPath = ''
  }
  draft.value = value
}

function chooseDirectory(value: { id: string; path: string }) {
  if (draft.value) {
    draft.value.accountId = props.accountId
    draft.value.targetCID = value.id
    draft.value.targetPath = value.path
  }
  directoryOpen.value = false
}

async function save() {
  const value = draft.value
  if (!value || busy.value) return
  if (!props.accountReady) { error.value = '请先登录 115 主账号'; return }
  if (!value.name.trim()) { error.value = '请填写配置名称'; return }
  if (!value.targetCID || !value.targetPath) { error.value = '请选择接收目录'; return }
  busy.value = true
  error.value = ''
  try {
    const result = await p115Request<{ profile: TransferTarget }>(props.token, props.endpoint + (value.id ? '/' + encodeURIComponent(value.id) : ''), {
      method: value.id ? 'PUT' : 'POST',
      body: JSON.stringify({ name: value.name.trim(), accountId: props.accountId, targetCID: value.targetCID, targetPath: value.targetPath }),
      signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]),
    })
    if (value.id) targets.value = targets.value.map(item => item.id === result.profile.id ? result.profile : item)
    else targets.value = [...targets.value, result.profile]
    draft.value = undefined
    notice.value = value.id ? '接收目录已更新' : '接收目录已创建'
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存接收目录失败'
  } finally {
    busy.value = false
  }
}

async function remove() {
  const value = deleting.value
  if (!value || busy.value) return
  busy.value = true
  error.value = ''
  try {
    await p115Request<void>(props.token, props.endpoint + '/' + encodeURIComponent(value.id), { method: 'DELETE', signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    targets.value = targets.value.filter(item => item.id !== value.id)
    deleting.value = undefined
    notice.value = '接收目录已删除'
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '删除接收目录失败'
  } finally {
    busy.value = false
  }
}

onMounted(load)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <section class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-700 dark:bg-slate-900 sm:p-6" :aria-label="title">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0"><h2 class="text-base font-semibold text-slate-900 dark:text-slate-100">{{ title }}</h2><p class="mt-1 text-sm leading-6 text-slate-500 dark:text-slate-400">{{ description }}</p></div>
      <button type="button" class="inline-flex h-9 shrink-0 items-center gap-1.5 rounded-lg bg-blue-600 px-3 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40" :disabled="busy || !accountReady" @click="edit()"><IconPlus :size="16" aria-hidden="true" />新增目录</button>
    </header>
    <p v-if="listError && !draft && !deleting" role="alert" class="mt-4 rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ listError }}</p>
    <p v-else-if="notice" role="status" class="mt-4 rounded-lg bg-emerald-50 p-3 text-sm text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300">{{ notice }}</p>
    <div v-if="loading" class="mt-4 h-16 animate-pulse rounded-lg bg-slate-100 dark:bg-slate-800" aria-label="正在读取目录配置" />
    <div v-else-if="targets.length" class="mt-4 divide-y divide-slate-100 rounded-lg border border-slate-200 dark:divide-slate-800 dark:border-slate-700">
      <article v-for="target in targets" :key="target.id" class="flex min-w-0 flex-wrap items-center gap-3 p-3 sm:p-4">
        <span class="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-blue-50 text-blue-700 dark:bg-blue-950/60 dark:text-blue-300"><IconFolder :size="18" aria-hidden="true" /></span>
        <div class="min-w-0 flex-1"><h3 class="truncate text-sm font-semibold text-slate-800 dark:text-slate-100">{{ target.name }}</h3><p class="mt-1 truncate text-xs text-slate-500 dark:text-slate-400" :title="target.targetPath">{{ target.targetPath }} <span class="font-mono">({{ target.targetCID }})</span></p></div>
        <span v-if="target.accountId !== accountId" class="text-xs font-medium text-amber-700 dark:text-amber-300">主账号变更，需重选目录</span>
        <div class="flex shrink-0 items-center gap-1"><button type="button" class="grid h-9 w-9 place-items-center rounded-lg text-slate-500 hover:bg-slate-100 hover:text-blue-700 dark:text-slate-400 dark:hover:bg-slate-800 dark:hover:text-blue-300" :aria-label="'编辑 ' + target.name" title="编辑" :disabled="busy" @click="edit(target)"><IconPencil :size="16" aria-hidden="true" /></button><button type="button" class="grid h-9 w-9 place-items-center rounded-lg text-slate-500 hover:bg-red-50 hover:text-red-700 dark:text-slate-400 dark:hover:bg-red-950/30 dark:hover:text-red-300" :aria-label="'删除 ' + target.name" title="删除" :disabled="busy" @click="deleting = target; error = ''"><IconTrash :size="16" aria-hidden="true" /></button></div>
      </article>
    </div>
    <div v-else class="mt-4 rounded-lg border border-dashed border-slate-300 px-4 py-7 text-center dark:border-slate-700"><p class="text-sm font-medium text-slate-700 dark:text-slate-200">{{ emptyText }}</p><p class="mt-1 text-xs text-slate-500 dark:text-slate-400">{{ endpoint === 'share-transfer/profiles' ? '创建后可在网页搜索结果中选择，也可分配给 Telegram Bot。' : '创建后可在 Telegram Bot 页面将目录分配给 Bot。' }}</p></div>

    <P115ActionDialog v-if="draft" :title="draft.id ? '编辑接收目录' : '新增接收目录'" :busy="busy" @close="draft = undefined">
      <label class="block text-sm font-medium text-slate-700 dark:text-slate-300">配置名称<input v-model="draft.name" maxlength="50" required placeholder="例如：动画、电影" class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 text-sm dark:border-slate-700 dark:bg-slate-950" /></label>
      <div><label class="text-sm font-medium text-slate-700 dark:text-slate-300">接收目录</label><div class="mt-2 flex min-w-0 items-center gap-3"><div class="min-w-0 flex-1 truncate rounded-lg border border-slate-200 bg-white p-3 text-sm text-slate-700 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-300">{{ draft.targetPath || '尚未选择' }}<span v-if="draft.targetCID" class="ml-1 font-mono text-xs text-slate-400">({{ draft.targetCID }})</span></div><button type="button" class="shrink-0 rounded-lg border border-slate-200 px-3 py-2.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-40 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800" :disabled="busy || !accountReady" @click="directoryOpen = true">选择目录</button></div></div>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
      <template #footer><button type="button" class="rounded-lg border border-slate-200 px-3 py-2 text-sm dark:border-slate-700" :disabled="busy" @click="draft = undefined">取消</button><button type="button" class="rounded-lg bg-blue-600 px-3 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40" :disabled="busy || !draft.name.trim() || !draft.targetCID" @click="save">{{ busy ? '保存中…' : '保存目录' }}</button></template>
    </P115ActionDialog>
    <P115DirectoryDialog v-if="directoryOpen && accountReady" :token="token" :account-id="accountId" @close="directoryOpen = false" @select="chooseDirectory" />
    <P115ActionDialog v-if="deleting" title="删除接收目录？" :busy="busy" @close="deleting = undefined"><p>确定删除“{{ deleting.name }}”？仅删除接收目录配置，不会删除网盘中的文件。</p><p v-if="error" role="alert" class="mt-3 rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p><template #footer><button type="button" class="rounded-lg border border-slate-200 px-3 py-2 text-sm dark:border-slate-700" :disabled="busy" @click="deleting = undefined">取消</button><button type="button" class="rounded-lg bg-red-600 px-3 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-40" :disabled="busy" @click="remove">{{ busy ? '删除中…' : '删除目录' }}</button></template></P115ActionDialog>
  </section>
</template>
