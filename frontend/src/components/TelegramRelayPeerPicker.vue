<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { IconBroadcast, IconRobot, IconSearch, IconUsers } from '@tabler/icons-vue'
import FloatingDialog from './AnimeFloatingDialog.vue'
import TelegramAvatar from './TelegramAvatar.vue'
import { telegramRelayRequest, type TelegramRelayPeer, type TelegramRelayPeerPage } from '../types/telegramRelay'

const props = defineProps<{ token: string; role: 'source' | 'target'; excludedId?: string }>()
const emit = defineEmits<{ close: []; select: [peer: TelegramRelayPeer, selectionToken: string] }>()
const peers = ref<TelegramRelayPeer[]>([]), selectionTokens = ref(new Map<string, string>()), cursor = ref(''), query = ref(''), error = ref(''), loading = ref(false)
const lifecycle = new AbortController()
const available = computed(() => peers.value.filter(peer => peer[props.role] && peer.id !== props.excludedId && `${peer.title} ${peer.username || ''}`.toLocaleLowerCase().includes(query.value.trim().toLocaleLowerCase())))
const kinds = { channel: '频道', group: '群组', bot: '机器人' }
async function load(reset = false) {
  if (loading.value) return
  loading.value = true; error.value = ''
  try {
    const page = await telegramRelayRequest<TelegramRelayPeerPage>(props.token, '/peers', lifecycle.signal, { cursor: reset ? '' : cursor.value })
    if (lifecycle.signal.aborted) return
    const merged = new Map((reset ? [] : peers.value).map(peer => [peer.id, peer]))
    const tokens = new Map(reset ? [] : selectionTokens.value)
    for (const peer of page.peers) { merged.set(peer.id, peer); tokens.set(peer.id, page.selectionToken) }
    peers.value = [...merged.values()]; selectionTokens.value = tokens; cursor.value = page.nextCursor
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取会话失败' }
  finally { if (!lifecycle.signal.aborted) loading.value = false }
}
onMounted(() => load(true))
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <FloatingDialog :title="role === 'source' ? '选择来源频道' : '选择转发目标'" close-label="关闭会话选择" size="config" class="relay-peer-picker" @close="emit('close')">
    <template #header><p class="rp-help">{{ role === 'source' ? '选择当前 Telegram 账号已加入的一个频道。' : '选择机器人、可发言的群组或可发布消息的频道。' }}</p><label class="rp-filter"><IconSearch :size="17" aria-hidden="true" /><input v-model="query" type="search" aria-label="筛选已加载的会话" placeholder="搜索已加载的名称或用户名" /></label></template>
    <p v-if="error" role="alert" class="rp-error">{{ error }}<button type="button" :disabled="loading" @click="load(true)">重新读取</button></p>
    <div class="rp-options"><button v-for="peer in available" :key="peer.id" type="button" class="rp-option" :disabled="!selectionTokens.get(peer.id)" @click="emit('select', peer, selectionTokens.get(peer.id) || '')"><TelegramAvatar :token="token" :peer-id="peer.id" :avatar-id="peer.avatarId" class="rp-icon"><IconBroadcast v-if="peer.kind === 'channel'" :size="19" aria-hidden="true" /><IconRobot v-else-if="peer.kind === 'bot'" :size="19" aria-hidden="true" /><IconUsers v-else :size="19" aria-hidden="true" /></TelegramAvatar><span><strong>{{ peer.title }}</strong><small>{{ kinds[peer.kind] }}<template v-if="peer.username"> · @{{ peer.username }}</template></small></span></button></div>
    <p v-if="loading" role="status" class="rp-empty">正在读取 Telegram 会话…</p><p v-else-if="!available.length && !error" class="rp-empty">{{ query ? '已加载的会话中没有匹配项' : cursor ? '本页没有可选项，可继续加载' : '没有符合条件的会话，请先在 Telegram 中加入频道或打开目标聊天' }}</p>
    <button v-if="cursor" type="button" class="rp-more" :disabled="loading" @click="load()">加载更多会话</button>
  </FloatingDialog>
</template>

<style scoped>
:global(dialog.relay-peer-picker) { width: min(620px, calc(100vw - 24px)); height: min(640px, calc(100dvh - 36px)); }
.rp-help { @apply text-slate-500 dark:text-slate-400; margin-bottom: 14px; font-size: 12px; line-height: 1.6; }.rp-filter { @apply bg-slate-100 dark:bg-slate-800 text-slate-500 dark:text-slate-400; display: flex; align-items: center; gap: 8px; padding: 0 12px; border-radius: 9px; }.rp-filter input { width: 100%; min-width: 0; padding: 10px 0; background: transparent; color: var(--floating-surface-text); font-size: 13px; outline: none; }.rp-options { display: grid; gap: 5px; }.rp-option { display: flex; align-items: center; gap: 12px; min-width: 0; width: 100%; padding: 12px 8px; border-radius: 10px; text-align: left; }.rp-option:hover { background: rgb(37 99 235 / .1); }.rp-option > span:last-child { display: grid; gap: 4px; min-width: 0; overflow-wrap: anywhere; }.rp-option strong { font-size: 13px; font-weight: 600; }.rp-option small { @apply text-slate-500 dark:text-slate-400; font-size: 11px; }.rp-icon { @apply bg-blue-50 dark:bg-blue-500/15 text-blue-600 dark:text-blue-300; display: grid; place-items: center; width: 36px; height: 36px; flex: none; border-radius: 50%; }.rp-empty { @apply text-slate-500 dark:text-slate-400; padding: 28px 8px; text-align: center; font-size: 13px; line-height: 1.7; }.rp-error { @apply bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300; padding: 12px; border-radius: 9px; font-size: 13px; line-height: 1.6; }.rp-error button { display: block; margin-top: 8px; text-decoration: underline; }.rp-more { @apply bg-slate-100 dark:bg-slate-800; display: block; margin: 14px auto 0; padding: 9px 14px; border-radius: 9px; font-size: 12px; }.rp-more:disabled { opacity: .5; }.rp-filter:focus-within, .rp-option:focus-visible, .rp-more:focus-visible { outline: 2px solid #3b82f6; outline-offset: 2px; }
@media(max-width: 639px) { .rp-filter input { font-size: 16px; }.rp-option { min-height: 62px; } }
</style>
