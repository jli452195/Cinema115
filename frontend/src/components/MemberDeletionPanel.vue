<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { memberRequest, type MemberPlayback } from '../types/memberPlayback'
const props = defineProps<{ token: string; members: MemberPlayback[]; disabled?: boolean }>()
const emit = defineEmits<{ changed: []; busy: [value: boolean] }>()
interface Target { key: string; userId: string; embyId: string; name: string; ownerId?: string; completed: boolean; remoteDeleted: boolean }
interface Job { id: string; name: string; fingerprint: string; targets: Target[]; stage: string; message: string; createdAt: number }
interface Preview { key: string; name: string; fingerprint: string; targets: Target[]; children: number; personalDrives?: number; sessions?: number }
const pending = ref<Target[]>([]), jobs = ref<Job[]>([]), preview = ref<Preview>()
const selected = ref(''), confirmation = ref(''), cascade = ref(false), busy = ref(false)
const error = ref(''), notice = ref('')
const lifecycle = new AbortController()
const dialog = ref<HTMLDialogElement>(), opened = ref(false), titleID = useId()
let returnFocus: HTMLElement | null = null
let previousOverflow = ''
async function openDialog() {
 if (!opened.value) { returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null; previousOverflow = document.body.style.overflow; document.body.style.overflow = 'hidden'; opened.value = true }
 await nextTick()
 if (!lifecycle.signal.aborted && !dialog.value?.open) dialog.value?.showModal()
}
function closeDialog() {
 if (busy.value) return
 dialog.value?.close(); opened.value = false; preview.value = undefined
 document.body.style.overflow = previousOverflow; returnFocus?.focus()
}
defineExpose({ inspect })
function setBusy(value: boolean) { busy.value = value; emit('busy', value) }
async function read() {
 const data = await memberRequest(props.token, lifecycle.signal, '/deletions')
 pending.value = data.pending; jobs.value = data.jobs
}
async function refresh() {
 if (busy.value) return
 setBusy(true); error.value = ''
 try { await read() } catch (e) { error.value = e instanceof Error ? e.message : '读取删除任务失败' }
 finally { setBusy(false) }
}
async function inspect(key: string) {
 if (busy.value || props.disabled || !key) return
 setBusy(true); error.value = ''; notice.value = ''; preview.value = undefined; confirmation.value = ''; cascade.value = false
 await openDialog()
 try { preview.value = await memberRequest(props.token, lifecycle.signal, '/deletions/preview', { key }) }
 catch (e) { error.value = e instanceof Error ? e.message : '无法核验删除目标' }
 finally { setBusy(false) }
}
function resume(job: Job) {
 if (busy.value || props.disabled) return
 preview.value = { key: job.id, name: job.name, fingerprint: job.fingerprint, targets: job.targets, children: Math.max(0, job.targets.length - 1) }
 confirmation.value = ''; cascade.value = false; error.value = ''; notice.value = '继续原删除任务，已完成的步骤不会重复执行。'
 void openDialog()
}
async function remove() {
 const target = preview.value
 if (!target || busy.value || props.disabled || confirmation.value !== target.name || target.children > 0 && !cascade.value) return
 setBusy(true); error.value = ''; notice.value = ''
 let completed = false
 try {
  const data = await memberRequest(props.token, lifecycle.signal, '/deletions', { key: target.key, fingerprint: target.fingerprint, confirmName: confirmation.value, includeChildren: cascade.value })
  notice.value = data.job.message
  completed = data.job.stage === 'completed'
  preview.value = undefined; confirmation.value = ''; selected.value = ''
  await read()
 } catch (e) {
  error.value = e instanceof Error ? e.message : '删除结果未确认，请刷新删除任务后继续'
  try { await read() } catch { /* Retain the confirmation and original request for recovery. */ }
 } finally { setBusy(false); if (completed) closeDialog(); emit('changed') }
}
// Initial task loading must not block the parent page's initial user load.
onMounted(async () => {
 busy.value = true
 try { await read() } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取删除任务失败' }
 finally { busy.value = false }
})
onBeforeUnmount(() => { lifecycle.abort(); if (opened.value) { dialog.value?.close(); document.body.style.overflow = previousOverflow }; emit('busy', false) })
</script>
<template>
 <section class="mb-6 mt-6 space-y-4 rounded-2xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900" aria-label="删除 Emby 用户">
  <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold">删除用户与未完成账号清理</h2><button :disabled="busy || disabled" class="text-sm text-blue-600 disabled:opacity-40" @click="refresh">刷新删除任务</button></div>
  <p class="text-sm leading-6 text-slate-500">永久删除 Emby 账号及其观看记录，清理网站身份、个人网盘凭据、播放占用及子账号名额。不会删除 115 文件、管理员网盘或共享池账号；已签发的播放直链可能仍有效。</p>
  <div class="flex flex-wrap gap-3"><select v-model="selected" :disabled="busy || disabled" aria-label="选择删除用户" class="min-w-0 flex-1 rounded-lg border bg-transparent p-2 text-sm"><option value="">选择要删除的用户</option><option v-for="m in members.filter(m => !m.administrator)" :key="m.userId" :value="m.userId">{{ m.embyName }}{{ m.playbackOnly ? '（子账号）' : '' }}</option></select><button :disabled="busy || disabled || !selected" class="rounded-lg border px-3 py-2 text-sm disabled:opacity-40" @click="inspect(selected)">预览删除影响</button></div>
  <p v-if="disabled && !busy" class="text-xs text-slate-500">请先完成或放弃其他用户设置的修改。</p>
  <p v-if="error && !opened" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
  <p v-if="notice && !opened" role="status" class="text-sm text-slate-600 dark:text-slate-300">{{ notice }}</p>
  <details v-if="pending.length" open class="space-y-3"><summary class="cursor-pointer text-sm font-medium">未完成创建（{{ pending.length }}）</summary><div v-for="t in pending" :key="t.key" class="flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3 text-sm"><span class="break-all">{{ t.name }}：{{ t.embyId ? '已确定 Emby 身份' : '外部身份待核查' }}</span><button :disabled="busy || disabled" class="text-red-700 disabled:opacity-40" @click="inspect(t.key)">核查并预览清理</button></div></details>
  <details v-if="jobs.length" open class="space-y-3"><summary class="cursor-pointer text-sm font-medium">删除任务（{{ jobs.length }}）</summary><article v-for="job in [...jobs].reverse()" :key="job.id" class="space-y-2 rounded-lg border p-3 text-sm"><div class="flex flex-wrap justify-between gap-3"><span>{{ job.name }}：{{ job.stage === 'completed' ? '已完成' : '待继续清理' }}</span><button v-if="job.stage !== 'completed'" :disabled="busy || disabled" class="text-blue-600 disabled:opacity-40" @click="resume(job)">继续原任务</button></div><p class="text-xs text-slate-500">{{ job.message }}</p></article></details>
 </section>

 <Teleport to="body">
  <dialog v-if="opened" ref="dialog" :aria-labelledby="titleID" class="deletion-dialog" @cancel.prevent="closeDialog()" @click="event => { if (event.target === dialog) closeDialog() }">
   <div class="space-y-4 p-5 sm:p-6">
    <div class="flex items-center justify-between gap-3"><h2 :id="titleID" class="text-lg font-semibold">删除用户</h2><button type="button" :disabled="busy" aria-label="关闭删除确认" class="rounded-lg p-2 disabled:opacity-40" @click="closeDialog()"><svg aria-hidden="true" class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m6 6 12 12M6 18 18 6" /></svg></button></div>
    <p class="text-sm text-slate-500">永久删除 Emby 账号及观看记录，清理网站关联和个人凭据。不删除 115 文件及管理员网盘；已签发的直链可能仍有效。</p>
    <p v-if="busy && !preview" role="status" class="text-sm">正在核验删除影响…</p>
    <p v-if="error" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ error }}</p>
    <p v-if="notice" role="status" class="text-sm">{{ notice }}</p>
  <form v-if="preview" class="space-y-3 rounded-xl border border-red-200 p-4" @submit.prevent="remove">
   <h3 class="font-medium">确认永久删除 {{ preview.name }}</h3>
   <p class="text-sm">共 {{ preview.targets.length }} 个目标<span v-if="preview.personalDrives !== undefined">，{{ preview.personalDrives }} 个个人网盘配置，{{ preview.sessions }} 个播放占用</span>。操作开始后无法撤销。</p>
   <ul class="space-y-1 text-xs text-slate-500"><li v-for="t in preview.targets" :key="t.key" class="break-all">{{ t.name }}：{{ t.embyId || '尚未发起外部创建' }}{{ t.completed ? '（已清理）' : '' }}</li></ul>
   <label v-if="preview.children" class="flex items-start gap-2 text-sm"><input v-model="cascade" :disabled="busy" type="checkbox" />连同 {{ preview.children }} 个子账号一起永久删除</label>
   <label class="block text-sm">输入用户名 {{ preview.name }} 确认<input v-model="confirmation" :disabled="busy" required autocomplete="off" class="mt-2 block w-full rounded-lg border bg-transparent p-2" /></label>
   <div class="flex gap-3"><button :disabled="busy || disabled || confirmation !== preview.name || preview.children > 0 && !cascade" class="rounded-lg bg-red-700 px-3 py-2 text-sm text-white disabled:opacity-40">{{ busy ? '正在确认删除结果…' : '确认永久删除 / 继续清理' }}</button><button type="button" :disabled="busy" class="text-sm" @click="closeDialog()">取消</button></div>
  </form>

   </div>
  </dialog>
 </Teleport>
</template>

<style scoped>
.deletion-dialog { width: min(560px, calc(100vw - 24px)); max-height: calc(100dvh - 32px); margin: auto; padding: 0; border: 0; border-radius: 16px; overflow-y: auto; background: white; color: rgb(var(--color-slate-800)); }
.deletion-dialog::backdrop { background: #0f172a70; }
.dark .deletion-dialog { background: rgb(var(--color-slate-900)); color: rgb(var(--color-slate-200)); color-scheme: dark; }
</style>
