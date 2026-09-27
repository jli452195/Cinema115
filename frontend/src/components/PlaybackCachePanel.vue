<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { embyRequest } from '../types/emby'
import type { P115Account } from '../types/p115Accounts'
const props = defineProps<{ token: string; accounts: P115Account[] }>()
interface Entry { key: string; accountId: string; uid: string; directoryId: string; directoryName: string; status: string; message: string; lastUsed: string; file: { name: string; size: number; id: string; sha1: string } }
const entries = ref<Entry[]>([])
const busy = ref(false)
const forgetKey = ref('')
const error = ref('')
const notice = ref('')
const lifecycle = new AbortController()
function accept(data: { entries: Entry[] }) { entries.value = data.entries }
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
    notice.value = '已移除本地记录，云端文件保留。下次播放会按 SHA1 重新查找。'
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
    <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold text-slate-900">网盘文件复用记录</h2><button :disabled="busy" type="button" class="text-sm text-blue-600 disabled:opacity-40" @click="load">刷新配置与记录</button></div>
    <p class="mt-2 text-sm leading-6 text-slate-500">按 SHA1 复用朋友网盘中的文件，未找到时秒传到配置目录。程序不删除网盘文件，请在 115 中手动管理。用户绑定在 Emby 用户管理中设置，秒传保存目录在 115 助手中设置。</p>
    <p v-if="error" role="alert" class="mt-4 rounded-lg bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
    <p v-if="notice" role="status" class="mt-4 rounded-lg bg-blue-50 p-3 text-sm text-blue-700">{{ notice }}</p>
    <div class="mt-6 border-t border-slate-100 pt-5">
      <div class="flex flex-wrap items-center justify-between gap-3"><h3 class="text-sm font-semibold text-slate-800">文件记录（{{ entries.length }}）</h3></div>
      <p v-if="!entries.length" class="mt-3 text-sm text-slate-400">复用网盘文件或秒传后会显示记录；分享直取不会建立此类记录。</p>
      <article v-for="entry in entries" :key="entry.key" class="mt-3 rounded-xl border border-slate-200 p-4 text-sm">
        <p class="break-all font-medium text-slate-800">{{ entry.file.name }} <span class="text-xs" :class="entry.status === 'ready' ? 'text-emerald-600' : 'text-amber-700'">{{ stateLabel(entry.status) }}</span></p>
        <p class="mt-2 text-xs text-slate-500">账号：{{ accounts.find(a => a.id === entry.accountId)?.name || '原账号已删除' }} · 目录 {{ entry.directoryId || '待确认' }}</p>
        <p class="mt-2 break-all text-xs text-slate-500">SHA1：{{ entry.file.sha1 }}</p>
        <p class="mt-2 text-xs text-slate-500">最后取链：{{ new Date(entry.lastUsed).toLocaleString() }}</p>
        <p v-if="entry.message" class="mt-2 text-xs text-slate-500">{{ entry.message }}</p>
        <button v-if="forgetKey !== entry.key" type="button" :disabled="busy" class="mt-3 text-xs text-slate-500 underline" @click="forgetKey = entry.key">核查后移除本地记录</button>
        <div v-else class="mt-3 rounded-lg bg-amber-50 p-3 text-xs text-amber-800"><p>仅移除本地记录，不删除云端文件。下次播放先按 SHA1 查找；存在未确认上传时，请先核查网盘。</p><button type="button" :disabled="busy" class="mt-2 font-medium underline" @click="action('/forget', { key: entry.key })">确认仅移除记录</button><button type="button" class="ml-4" @click="forgetKey = ''">取消</button></div>
      </article>
    </div>
  </section>
</template>
