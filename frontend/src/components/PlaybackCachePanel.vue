<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { embyRequest } from '../types/emby'
import type { P115Account } from '../types/p115Accounts'
const props = defineProps<{ token: string; accounts: P115Account[] }>()
interface Entry { key: string; accountId: string; uid: string; directoryId: string; directoryName: string; status: string; message: string; lastUsed: string; file: { name: string; size: number; id: string; sha1: string } }
interface Material { id: string; name: string; size: number; sha1: string; verifiedAt: string; ranges: { start: number; end: number; sha1: string }[] }
const entries = ref<Entry[]>([])
const materials = ref<Material[]>([])
const forgetMaterial = ref('')
const busy = ref(false)
const forgetKey = ref('')
const error = ref('')
const notice = ref('')
const lifecycle = new AbortController()
function accept(data: { entries: Entry[]; materials?: Material[] }) { entries.value = data.entries; materials.value = data.materials || [] }
function sizeLabel(size: number) { return `${(size / 1024 / 1024).toLocaleString(undefined, { maximumFractionDigits: 2 })} MB` }
function copyCount(m: Material) { return entries.value.filter(e => e.status === 'ready' && e.file.sha1.toUpperCase() === m.sha1.toUpperCase() && e.file.size === m.size).length }
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
    notice.value = path === '/forget-material' ? '已移除文件资料及校验片段哈希，账号副本记录和云端文件保留。' : '已移除本地副本记录，文件资料和云端文件保留。'
    forgetKey.value = ''
    forgetMaterial.value = ''
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '操作失败' }
  finally { busy.value = false }
}
function stateLabel(s: string) { return ({ ready: '可复用', uncertain: '需核查', creating: '创建目录中', uploading: '秒传中', deleting: '清理中' } as Record<string, string>)[s] || s }
onMounted(load)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <section class="rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-5 shadow-sm md:p-7">
    <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold text-slate-900 dark:text-slate-100">网盘文件复用记录</h2><button :disabled="busy" type="button" class="text-sm text-blue-600 disabled:opacity-40" @click="load">刷新配置与记录</button></div>
    <p class="mt-2 text-sm leading-6 text-slate-500 dark:text-slate-400">优先复用绑定账号中的文件；需要秒传时使用文件资料，缺少校验片段时依次尝试已有小号副本和主账号。资料不保存 Cookie，播放目标始终是用户绑定账号。移除本地资料或记录不会删除网盘文件。</p>
    <p v-if="error" role="alert" class="mt-4 rounded-lg bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
    <p v-if="notice" role="status" class="mt-4 rounded-lg bg-blue-50 p-3 text-sm text-blue-700">{{ notice }}</p>
    <div class="mt-6 border-t border-slate-100 dark:border-slate-800 pt-5">
      <h3 class="text-sm font-semibold text-slate-800 dark:text-slate-200">文件秒传资料（{{ materials.length }}）</h3>
      <p class="mt-2 text-xs leading-5 text-slate-500 dark:text-slate-400">资料跨账号复用。校验范围相同时可直接使用已验证的片段哈希；115 要求新的范围时，仍需有可读取的文件来源。</p>
      <p v-if="!materials.length" class="mt-3 text-sm text-slate-400 dark:text-slate-500">进入秒传流程后自动积累文件资料。</p>
      <article v-for="material in materials" :key="material.id" class="mt-3 rounded-xl border border-slate-200 dark:border-slate-700 p-4 text-sm">
        <p class="break-all font-medium text-slate-800 dark:text-slate-200">{{ material.name }}</p>
        <p class="mt-2 text-xs text-slate-500 dark:text-slate-400">{{ sizeLabel(material.size) }} · 已记录副本 {{ copyCount(material) }} · 已验证校验片段 {{ material.ranges.length }}</p>
        <p class="mt-2 break-all text-xs text-slate-500 dark:text-slate-400">SHA1：{{ material.sha1 }}</p>
        <details v-if="material.ranges.length" class="mt-2 text-xs text-slate-500 dark:text-slate-400"><summary class="cursor-pointer">查看校验片段</summary><p v-for="range in material.ranges" :key="range.start + ':' + range.end" class="mt-2 break-all">字节 {{ range.start }}–{{ range.end }} · {{ range.sha1 }}</p></details>
        <button v-if="forgetMaterial !== material.id" type="button" :disabled="busy" class="mt-3 text-xs text-slate-500 dark:text-slate-400 underline" @click="forgetMaterial = material.id">移除文件资料</button>
        <div v-else class="mt-3 rounded-lg bg-amber-50 p-3 text-xs text-amber-800 dark:bg-amber-500/10 dark:text-amber-300"><p>移除后，再次秒传可能需要重新读取主账号文件。账号副本记录和云端文件保留。</p><button type="button" :disabled="busy" class="mt-2 font-medium underline" @click="action('/forget-material', { id: material.id })">确认移除资料</button><button type="button" class="ml-4" @click="forgetMaterial = ''">取消</button></div>
      </article>
    </div>
    <div class="mt-6 border-t border-slate-100 dark:border-slate-800 pt-5">
      <div class="flex flex-wrap items-center justify-between gap-3"><h3 class="text-sm font-semibold text-slate-800 dark:text-slate-200">账号文件副本（{{ entries.length }}）</h3></div>
      <p v-if="!entries.length" class="mt-3 text-sm text-slate-400 dark:text-slate-500">复用网盘文件或秒传后会显示记录；分享直取不会建立此类记录。</p>
      <article v-for="entry in entries" :key="entry.key" class="mt-3 rounded-xl border border-slate-200 dark:border-slate-700 p-4 text-sm">
        <p class="break-all font-medium text-slate-800 dark:text-slate-200">{{ entry.file.name }} <span class="text-xs" :class="entry.status === 'ready' ? 'text-emerald-600' : 'text-amber-700'">{{ stateLabel(entry.status) }}</span></p>
        <p class="mt-2 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">账号：{{ accounts.find(a => a.id === entry.accountId)?.name || '原账号已删除' }} · 目录 {{ entry.directoryId || '待确认' }}</p>
        <p class="mt-2 break-all text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">SHA1：{{ entry.file.sha1 }}</p>
        <p class="mt-2 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">最后取链：{{ new Date(entry.lastUsed).toLocaleString() }}</p>
        <p v-if="entry.message" class="mt-2 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ entry.message }}</p>
        <button v-if="forgetKey !== entry.key" type="button" :disabled="busy" class="mt-3 text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500 underline" @click="forgetKey = entry.key">核查后移除本地记录</button>
        <div v-else class="mt-3 rounded-lg bg-amber-50 p-3 text-xs text-amber-800"><p>仅移除本地记录，不删除云端文件。下次播放先按 SHA1 查找；存在未确认上传时，请先核查网盘。</p><button type="button" :disabled="busy" class="mt-2 font-medium underline" @click="action('/forget', { key: entry.key })">确认仅移除记录</button><button type="button" class="ml-4" @click="forgetKey = ''">取消</button></div>
      </article>
    </div>
  </section>
</template>
