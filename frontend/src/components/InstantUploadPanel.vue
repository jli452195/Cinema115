<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { P115Account } from '../types/p115Accounts'
import { embyRequest } from '../types/emby'

const props = defineProps<{ token: string; accounts: P115Account[]; source?: { accountId: string } }>()
interface Config { userId: string; sourceAccountId: string; accountId: string; targetCid: string; targetPath: string }
interface Binding { userId: string; userName: string; accountId: string; source?: string }
const bindings = ref<Binding[]>([])
interface Record { id: string; sourceAccountId: string; accountId: string; targetCid: string; status: string; message: string; name: string; size: number; sha1: string; fileId?: string; pickCode?: string; challenged: boolean; startedAt: string; durationMs: number }
interface Status { config: Config; records: Record[]; running: boolean }
const config = ref<Config>({ userId: '', sourceAccountId: '', accountId: '', targetCid: '', targetPath: '' })
const saved = ref<Config>()
const records = ref<Record[]>([])
const running = ref(false)
const busy = ref(false)
const loaded = ref(false)
const strm = ref('')
const error = ref('')
const notice = ref('')
const requestId = ref('')
const lifecycle = new AbortController()
const dirty = computed(() => JSON.stringify(config.value) !== JSON.stringify(saved.value))
const blocked = computed(() => busy.value || running.value || !loaded.value)
const target = computed(() => props.accounts.find(a => !a.primary && a.id === bindings.value.find(b => b.userId === config.value.userId)?.accountId))
const dynamic = computed(() => bindings.value.some(b => b.userId === config.value.userId && b.source === 'assigned'))
const ready = computed(() => !!props.accounts.find(a => a.primary && a.configured) && bindings.value.some(b => b.userId === config.value.userId))
watch(strm, () => { requestId.value = '' })
function acceptStatus(data: Status, replaceConfig: boolean) {
  records.value = data.records; running.value = data.running; saved.value = { ...data.config }
  if (replaceConfig) config.value = { ...data.config }
  loaded.value = true
}
async function load(replaceConfig = false) {
  busy.value = true; error.value = ''
  try {
    const [status, users]: [Status, { embyUrl: string; users: { Id: string; Policy?: { IsDisabled?: boolean } }[]; members: { embyId: string; embyName: string; source: string; accountId: string; playbackOnly?: boolean; suspended?: boolean; personalDrive?: { connected: boolean } }[] }] = await Promise.all([
      embyRequest(props.token, 'instant-upload', lifecycle.signal),
      embyRequest(props.token, 'playback-cache/users', lifecycle.signal),
    ])
    bindings.value = (users.members || []).filter(m => m.source === 'assigned' && !m.playbackOnly && !m.suspended && users.users.some(u => u.Id === m.embyId && !u.Policy?.IsDisabled)).map(m => ({ userId: m.embyId, userName: m.embyName, source: m.source, accountId: '' }))
    acceptStatus(status, replaceConfig || !loaded.value)
  }
  catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取秒传配置失败' }
  finally { busy.value = false }
}
async function save() {
  busy.value = true; error.value = ''; notice.value = ''
  try {
    acceptStatus(await embyRequest(props.token, 'instant-upload', lifecycle.signal, { userId: config.value.userId }), true)
    requestId.value = ''; notice.value = '秒传配置已保存。'
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存失败' }
  finally { busy.value = false }
}
async function run() {
  if (blocked.value || dirty.value || !ready.value || !strm.value.trim()) return
  busy.value = true; error.value = ''; notice.value = ''
  if (!requestId.value) requestId.value = Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2, '0')).join('')
  try {
    const result: Record = await embyRequest(props.token, 'instant-upload/run', lifecycle.signal, { id: requestId.value, strm: strm.value.trim() })
    records.value = [result, ...records.value.filter(item => item.id !== result.id)].slice(0, 20)
    notice.value = result.message
    // Keep the ID until the sample or saved config changes, so retrying cannot repeat a write.
  } catch (reason) {
    if (!lifecycle.signal.aborted) error.value = `${reason instanceof Error ? reason.message : '验证请求失败'}。如请求已发出，请刷新记录并核查目标目录。`
  } finally { busy.value = false }
}
function statusLabel(value: string) { return ({ success: '秒传成功', unsupported: '需要普通上传', uncertain: '结果待确认', failed: '验证失败', running: '验证中' } as { [key: string]: string })[value] || value }
function accountName(id: string) { return props.accounts.find(account => account.id === id)?.name || (id ? '个人网盘或账号已删除' : '网盘尚未就绪') }
onMounted(() => load(true))
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <section class="rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-5 shadow-sm md:p-7">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <h2 class="font-semibold text-slate-900 dark:text-slate-100">单文件秒传验证</h2>
      <button type="button" :disabled="busy" class="text-sm text-blue-600 disabled:opacity-40" @click="load()">刷新记录</button>
    </div>
    <p class="mt-2 text-sm leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">将分享 STRM 对应的视频秒传到目标用户的播放网盘。池用户在任务启动时选择小号并固定目录，写入开始后不自动换号。结果保留供你检查。</p>
    <p v-if="error" role="alert" class="mt-4 rounded-lg bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
    <p v-if="notice" role="status" class="mt-4 rounded-lg bg-blue-50 p-3 text-sm text-blue-800">{{ notice }}</p>
    <form class="mt-5" @submit.prevent="save">
      <fieldset :disabled="blocked" class="space-y-5 disabled:opacity-50">
        <div class="grid gap-5 sm:grid-cols-2">
          <label class="text-sm text-slate-700 dark:text-slate-300">目标用户<select v-model="config.userId" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3" @change="requestId = ''"><option value="" disabled>请选择目标用户</option><option v-for="binding in bindings" :key="binding.userId" :value="binding.userId">{{ binding.userName }} · {{ binding.source === 'assigned' ? '动态小号池' : accountName(binding.accountId) }}</option></select></label>
          <p class="text-sm leading-7 text-slate-500 dark:text-slate-400">目标网盘：{{ dynamic ? '启动时自动选择' : target?.name || '请选择用户' }}<br />秒传目录：{{ dynamic ? '使用所选小号的已验证目录' : target?.playbackParentCid || '请先配置播放目录' }}</p>
        </div>
        <div class="flex flex-wrap gap-3">
          <a href="/emby/users" class="text-sm text-blue-600">管理用户来源</a>
          <button type="submit" :disabled="!ready" class="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white disabled:opacity-40">保存秒传配置</button>
        </div>
      </fieldset>
    </form>
    <div class="mt-6 border-t border-slate-100 dark:border-slate-800 pt-5">
      <label for="instant-strm" class="text-sm text-slate-700 dark:text-slate-300">单个 STRM 文件中的播放地址</label>
      <textarea id="instant-strm" v-model="strm" :disabled="blocked" rows="3" spellcheck="false" autocomplete="off" placeholder="打开一个 .strm 文件，粘贴其中的完整地址" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 text-sm disabled:opacity-50" />
      <p class="mt-2 text-xs leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">验证只尝试秒传；如 115 要求普通上传，会停止。校验最多读取 8 MB 片段。这里生成的验证文件会保留，不纳入上方播放缓存的定期清理。</p>
      <p v-if="dirty && loaded" class="mt-2 text-xs text-amber-700">请先保存上面的账号与目录配置。</p>
      <button type="button" :disabled="blocked || dirty || !ready || !strm.trim()" class="mt-3 rounded-lg bg-blue-600 px-4 py-2.5 text-sm font-medium text-white disabled:opacity-40" @click="run">{{ busy || running ? '正在处理…' : '开始单文件秒传' }}</button>
    </div>
    <div v-if="records.length" class="mt-6 space-y-3" aria-live="polite">
      <h3 class="text-sm font-semibold text-slate-800 dark:text-slate-200">最近验证记录</h3>
      <article v-for="record in records" :key="record.id" class="rounded-xl border border-slate-200 dark:border-slate-700 p-4 text-sm">
        <div class="flex flex-wrap items-center justify-between gap-2"><span class="font-medium" :class="record.status === 'success' ? 'text-emerald-700' : record.status === 'failed' ? 'text-red-600' : 'text-amber-700'">{{ statusLabel(record.status) }}</span><time class="text-xs text-slate-400 dark:text-slate-500">{{ new Date(record.startedAt).toLocaleString() }}</time></div>
        <p v-if="record.name" class="mt-2 break-all text-slate-800 dark:text-slate-200">{{ record.name }}</p>
        <p class="mt-2 text-slate-600 dark:text-slate-400 dark:text-slate-500">{{ record.message }}</p>
        <p class="mt-2 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">目标：{{ accountName(record.accountId) }} · 目录 {{ record.targetCid }} · 耗时 {{ (record.durationMs / 1000).toFixed(2) }} 秒<span v-if="record.challenged"> · 已完成片段校验</span></p>
        <p v-if="record.fileId || record.pickCode" class="mt-2 break-all text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500"><span v-if="record.fileId">目标文件 ID：{{ record.fileId }} </span><span v-if="record.pickCode">PickCode：{{ record.pickCode }}</span></p>
      </article>
    </div>
  </section>
</template>
