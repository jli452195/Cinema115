<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { memberRequest, type MemberPlayback } from '../types/memberPlayback'
const props = defineProps<{ token: string; members: MemberPlayback[] }>()
type Session = { key: string; userId: string; item: string; generation: number; issued: boolean; updatedAt: number; expiresAt: number }
const sessions = ref<Session[]>([]), error = ref(''), busy = ref(false), selected = ref<Session>()
const controller = new AbortController()
let timer: ReturnType<typeof setTimeout> | undefined
async function load() {
 if (busy.value) return
 busy.value = true
 try { const data = await memberRequest(props.token, controller.signal, '/playback-sessions'); sessions.value = data.sessions || []; error.value = '' }
 catch(e) { if (!controller.signal.aborted) error.value = e instanceof Error ? e.message : '读取播放占用失败' }
 finally { busy.value = false }
}
async function poll() { if (!document.hidden) await load(); if (!controller.signal.aborted) timer = setTimeout(poll, 10000) }
async function release() {
 const session = selected.value
 if (!session || busy.value) return
 busy.value = true
 try { const data = await memberRequest(props.token, controller.signal, `/playback-sessions/${encodeURIComponent(session.key)}/release`, { generation: session.generation }); sessions.value = data.sessions || []; selected.value = undefined; error.value = '' }
 catch(e) { if (!controller.signal.aborted) error.value = e instanceof Error ? e.message : '释放失败' }
 finally { busy.value = false }
}
onMounted(poll)
onBeforeUnmount(() => { controller.abort(); clearTimeout(timer) })
</script>
<template>
 <details class="mb-4 rounded-xl border border-slate-200 p-4 dark:border-slate-700">
  <summary class="cursor-pointer font-medium">用户播放占用（{{ sessions.length }}）</summary>
  <p class="my-3 text-sm text-slate-500">同一 Emby 用户可以同时发起多个播放会话。停止后释放对应会话，小号池仍按实际 UID 限制容量，并可能保留直链残余占用。</p>
  <p class="mb-3 text-sm text-slate-500">同设备同播放器的新播放可在旧会话静默后接替，优先沿用原小号。缺少设备信息时需通过反代重新登录；多会话无法唯一匹配时独立计数。</p>
  <p v-if="error" role="alert" class="my-2 text-sm text-red-600">{{ error }}</p>
  <p v-if="!sessions.length" class="text-sm text-slate-500">{{ busy ? '读取中' : '暂无播放占用' }}</p>
  <div v-for="s in sessions" :key="s.key" class="mt-3 flex flex-wrap items-center justify-between gap-3 border-t border-slate-200 pt-3 text-sm dark:border-slate-700">
   <div class="min-w-0 break-words"><p>{{ members.find(m => m.userId === s.userId)?.embyName || 'Emby 用户' }}：条目 {{ s.item }}，{{ s.issued ? '已签发直链' : '准备中' }}</p>
    <p class="mt-1 text-xs text-slate-500">最近活动 {{ new Date(s.updatedAt).toLocaleString() }}；预计到期 {{ new Date(s.expiresAt).toLocaleString() }}</p></div>
   <button :disabled="busy" class="text-blue-600 disabled:opacity-40" @click="selected = s">释放播放占用</button>
  </div>
  <div v-if="selected" role="alert" class="mt-4 rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">
   <p>释放后拒绝旧会话的新取链，但不能立即中断已签发的 CDN 直链。确认释放条目 {{ selected.item }} 的占用？</p>
   <div class="mt-2 flex gap-4"><button :disabled="busy" @click="release">确认释放</button><button :disabled="busy" @click="selected = undefined">取消</button></div>
  </div>
 </details>
</template>
