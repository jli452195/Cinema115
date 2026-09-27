<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import SettingSwitch from './SettingSwitch.vue'
import { embyRequest } from '../types/emby'
import type { P115Account } from '../types/p115Accounts'
const props = defineProps<{ token: string; accounts: P115Account[] }>()
interface Config { cleanupEnabled: boolean; retentionHours: number }
interface Entry { key: string; accountId: string; uid: string; directoryId: string; directoryName: string; status: string; message: string; lastUsed: string; file: { name: string; size: number; id: string } }
const config = ref<Config>({ cleanupEnabled: false, retentionHours: 72 })
const entries = ref<Entry[]>([])
const busy = ref(false)
const loaded = ref(false)
const forgetKey = ref('')
const error = ref('')
const notice = ref('')
const lifecycle = new AbortController()
function accept(data: { config: Config; entries: Entry[] }) { config.value = { cleanupEnabled: data.config.cleanupEnabled, retentionHours: data.config.retentionHours }; entries.value = data.entries; loaded.value = true }
async function load() {
  busy.value = true; error.value = ''
  try { accept(await embyRequest(props.token, 'playback-cache', lifecycle.signal)) }
  catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取缓存配置失败' }
  finally { busy.value = false }
}
async function action(path: string, body: object) {
  busy.value = true; error.value = ''; notice.value = ''
  try {
    accept(await embyRequest(props.token, `playback-cache${path}`, lifecycle.signal, body))
    notice.value = path === '/cleanup' ? '过期缓存检查完成，详细结果见 Emby 302 日志。' : path === '/forget' ? '已移除本地记录，云端文件保留。下次播放会重新建立缓存。' : '秒传文件清理策略已保存。'
    forgetKey.value = ''
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '操作失败' }
  finally { busy.value = false }
}
function stateLabel(s: string) { return ({ ready: '可复用', uncertain: '需核查', creating: '创建目录中', uploading: '秒传中', deleting: '清理中' } as Record<string, string>)[s] || s }
onMounted(load)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <section class="rounded-2xl border border-slate-200 bg-white p-5 shadow-sm md:p-7">
    <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold text-slate-900">秒传文件缓存</h2><button :disabled="busy" type="button" class="text-sm text-blue-600 disabled:opacity-40" @click="load">刷新配置与记录</button></div>
    <p class="mt-2 text-sm leading-6 text-slate-500">这里只管理程序主动秒传并登记的网盘文件。用户绑定在 Emby 用户管理中设置，缓存父目录在 115 助手中设置。</p>
    <p v-if="error" role="alert" class="mt-4 rounded-lg bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
    <p v-if="notice" role="status" class="mt-4 rounded-lg bg-blue-50 p-3 text-sm text-blue-700">{{ notice }}</p>
    <form class="mt-5" @submit.prevent="action('/policy', config)">
      <fieldset :disabled="busy || !loaded" class="space-y-5 disabled:opacity-50">
        <SettingSwitch v-model="config.cleanupEnabled" label="定期清理秒传文件" description="每小时检查一次，将过期的登记视频移入 115 回收站。空缓存目录保留。" />
        <label class="block text-sm text-slate-700">最后取链后保留小时数<input v-model.number="config.retentionHours" type="number" min="24" max="720" required class="ml-3 w-24 rounded-lg border border-slate-200 p-2" /></label>
        <p class="text-xs leading-6 text-slate-500">每次播放取链会续期，默认 72 小时。302 后视频由客户端直接下载，服务无法持续检测播放状态；长时间暂停后可能需要重新打开视频。缓存存入程序新建的专用子目录，已有文件和单文件验证文件不会纳入清理。</p>
        <button type="submit" class="rounded-lg bg-blue-600 px-4 py-2.5 text-sm font-medium text-white">保存清理策略</button>
      </fieldset>
    </form>
    <div class="mt-6 border-t border-slate-100 pt-5">
      <div class="flex flex-wrap items-center justify-between gap-3"><h3 class="text-sm font-semibold text-slate-800">已登记缓存（{{ entries.length }}）</h3><button type="button" :disabled="busy || !loaded" class="text-sm text-blue-600 disabled:opacity-40" @click="action('/cleanup', {})">按已保存策略检查过期缓存</button></div>
      <p v-if="!entries.length" class="mt-3 text-sm text-slate-400">分享受限并秒传后会显示登记文件。分享直取不会建立秒传文件记录。</p>
      <article v-for="entry in entries" :key="entry.key" class="mt-3 rounded-xl border border-slate-200 p-4 text-sm">
        <p class="break-all font-medium text-slate-800">{{ entry.file.name }} <span class="text-xs" :class="entry.status === 'ready' ? 'text-emerald-600' : 'text-amber-700'">{{ stateLabel(entry.status) }}</span></p>
        <p class="mt-2 text-xs text-slate-500">账号：{{ accounts.find(a => a.id === entry.accountId)?.name || '原账号已删除' }} · 目录 {{ entry.directoryId || '待确认' }}</p>
        <p class="mt-2 text-xs text-slate-500">最后取链：{{ new Date(entry.lastUsed).toLocaleString() }}</p>
        <p v-if="entry.message" class="mt-2 text-xs text-slate-500">{{ entry.message }}</p>
        <button v-if="forgetKey !== entry.key" type="button" :disabled="busy" class="mt-3 text-xs text-slate-500 underline" @click="forgetKey = entry.key">核查后移除本地记录</button>
        <div v-else class="mt-3 rounded-lg bg-amber-50 p-3 text-xs text-amber-800"><p>云端文件会保留，下次播放会创建新缓存。请确认已核查此目录。</p><button type="button" :disabled="busy" class="mt-2 font-medium underline" @click="action('/forget', { key: entry.key })">确认仅移除记录</button><button type="button" class="ml-4" @click="forgetKey = ''">取消</button></div>
      </article>
    </div>
  </section>
</template>
