<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
interface Preview {
  userId: string; username: string; embyId: string; embyName: string; adopted: boolean; source: string
  drives: { id: string; uid: string; playbackParentCid?: string }[]
  mode: string; state: string; blockedReason: string; fingerprint: string
}
const props = defineProps<{ token: string }>()
const emit = defineEmits<{ changed: []; busy: [value: boolean] }>()
const dialog = ref<HTMLDialogElement>(), preview = ref<Preview>(), records = ref<Preview[]>([])
const busy = ref(false), error = ref(''), historyError = ref(''), name = ref(''), mode = ref('none')
const lifecycle = new AbortController()
async function request(path: string, body?: object) {
  const response = await fetch(`/api/admin/users${path}`, { method: body ? 'POST' : 'GET', headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(95000)]) })
  const data = await response.json()
  if (!response.ok) throw new Error(data.error || '清理操作失败')
  return data
}
async function history() {
  historyError.value = ''
  try { const data = await request('/cleanup-records'); if (!Array.isArray(data.records)) throw new Error('invalid'); records.value = data.records }
  catch { historyError.value = '清理记录读取失败，请重试' }
}
async function open(id: string) {
  if (busy.value) return
  preview.value = undefined; error.value = ''; name.value = ''
  await nextTick(); dialog.value?.showModal()
  busy.value = true; emit('busy', true)
  try { preview.value = await request(`/${encodeURIComponent(id)}/cleanup`); mode.value = preview.value!.mode }
  catch (e) { error.value = e instanceof Error ? e.message : '预览失败' }
  finally { busy.value = false; emit('busy', false) }
}
function close() { if (!busy.value) dialog.value?.close() }
async function remove() {
  const p = preview.value
  if (!p || busy.value || name.value !== p.username || p.blockedReason) return
  busy.value = true; emit('busy', true); error.value = ''
  try {
    await request(`/${encodeURIComponent(p.userId)}/cleanup`, { username: name.value, mode: mode.value, fingerprint: p.fingerprint })
    dialog.value?.close(); await history()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '删除未完成，请重试'
    // Refresh the durable choice/fingerprint after partial completion or a lost response.
    try { preview.value = await request(`/${encodeURIComponent(p.userId)}/cleanup`); mode.value = preview.value!.mode }
    catch { preview.value = undefined; error.value += '；请关闭后重新读取预览' }
  } finally { busy.value = false; emit('busy', false); emit('changed') }
}
defineExpose({ open })
onMounted(history)
onBeforeUnmount(() => lifecycle.abort())
</script>
<template>
  <details class="rounded-xl border border-slate-200 bg-white p-4 text-sm dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300">
    <summary class="cursor-pointer">用户清理记录（{{ records.length }}）</summary>
    <p class="mt-3 text-xs leading-6 text-slate-500 dark:text-slate-400">保留原身份、处理方式及网盘归属，避免重新注册时继承旧权限。个人网盘 UID 和目录保留，登录凭据已清除；网盘文件未删除。</p>
    <p v-if="historyError" role="alert" class="mt-3 text-red-600 dark:text-red-300">{{ historyError }}</p>
    <button :disabled="busy" class="mt-3 text-blue-600 dark:text-blue-300" @click="history">刷新清理记录</button>
    <div class="mt-3 max-h-80 overflow-y-auto">
      <div v-for="record in records" :key="record.userId" class="space-y-1 border-t border-slate-100 py-3 dark:border-slate-800">
        <p class="break-all">{{ record.username }} · 网站账号已删除</p>
        <p v-if="record.embyId" class="text-xs">Emby：{{ record.embyName }} · {{ record.mode === 'keep' ? '保留且锁定' : '已确认不存在' }}</p>
        <p v-for="drive in record.drives" :key="drive.id" class="break-all text-xs">个人网盘 UID：{{ drive.uid || '无' }} · 目录：{{ drive.playbackParentCid || '无' }}</p>
        <p class="break-all text-xs text-slate-500 dark:text-slate-400">原网站 ID：{{ record.userId }}</p>
      </div>
    </div>
  </details>
  <dialog ref="dialog" aria-labelledby="cleanup-title" class="m-auto max-h-[90dvh] w-[calc(100%-2rem)] max-w-lg overflow-y-auto rounded-2xl border border-slate-200 bg-white p-6 text-slate-900 backdrop:bg-black/40 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100" @cancel.prevent="close">
    <h2 id="cleanup-title" class="text-lg font-semibold">删除网站用户</h2>
    <p v-if="error" role="alert" class="mt-4 text-sm text-red-600 dark:text-red-300">{{ error }}</p>
    <form v-if="preview && preview.state !== 'deleted'" class="mt-4 space-y-4 text-sm" @submit.prevent="remove">
      <fieldset :disabled="busy" class="space-y-4 disabled:opacity-60">
        <p>网站账号：{{ preview.username }}</p>
        <template v-if="preview.embyId">
          <p>Emby：{{ preview.embyName }}（{{ preview.adopted ? '关联的已有账号' : '网站创建的账号' }}）</p>
          <p class="break-all text-xs text-slate-500 dark:text-slate-400">Emby ID：{{ preview.embyId }}</p>
          <label class="block">Emby 处理方式<select v-model="mode" :disabled="preview.state === 'deleting'" class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-950"><option value="delete">一并删除 Emby 账号及其用户数据</option><option value="keep">保留 Emby 并锁定登录和播放</option></select></label>
        </template>
        <p v-else>未关联 Emby 账号。</p>
        <p v-if="preview.source">播放来源：{{ preview.source === 'primary' ? '主账号' : preview.source === 'assigned' ? '管理员小号' : '本人网盘' }}</p>
        <p v-for="drive in preview.drives" :key="drive.id" class="break-all">个人网盘 UID：{{ drive.uid || '无' }} · 目录：{{ drive.playbackParentCid || '无' }}</p>
        <p class="text-xs leading-6 text-slate-500 dark:text-slate-400">删除网站账号、个人订阅和通知，撤销个人网盘凭据。管理员网盘账号及云端文件保留；已用邀请码不能再次使用。保留的 Emby 维持锁定，不自动使用主账号。</p>
        <p v-if="mode === 'delete'" class="text-xs text-red-600 dark:text-red-300">删除 Emby 将失去该账号的观看进度等用户数据，无法撤销。</p>
        <p v-if="preview.state === 'deleting'" class="text-amber-700 dark:text-amber-300">删除待完成，网站账号已锁定。重试会继续原处理方式。</p>
        <p v-if="preview.blockedReason" class="text-amber-700 dark:text-amber-300">{{ preview.blockedReason }}</p>
        <label class="block">输入网站用户名确认<input v-model="name" autocomplete="off" class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3 dark:border-slate-700 dark:bg-slate-950" /></label>
        <button :disabled="name !== preview.username || !!preview.blockedReason" class="rounded-lg bg-red-600 px-4 py-2 text-white disabled:opacity-40">{{ busy ? '处理中…' : preview.state === 'deleting' ? '重试删除' : '确认删除' }}</button>
      </fieldset>
    </form>
    <p v-else-if="preview?.state === 'deleted'" class="mt-4 text-sm">清理已经完成，可关闭查看清理记录。</p>
    <p v-else-if="busy" class="mt-4 text-sm">正在读取关联信息…</p>
    <button :disabled="busy" class="mt-4 text-sm text-slate-500 disabled:opacity-40 dark:text-slate-400" @click="close">关闭</button>
  </dialog>
</template>
