<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { IconCheck, IconPlus, IconSearch } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import FloatingDialog from '../components/AnimeFloatingDialog.vue'
import telegramIcon from '../assets/telegram/telegram.svg'
import { telegramUserRequest, TelegramSearchError, type TelegramChannel, type TelegramChannelConfig, type TelegramChannelPage } from '../types/telegramUser'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const config = ref<TelegramChannelConfig>()
const loading = ref(true), loadError = ref(''), notice = ref('')
const opened = ref(false), busy = ref(''), error = ref(''), query = ref('')
const channels = ref<TelegramChannel[]>([]), selected = ref<string[]>([])
const selectionToken = ref(''), cursor = ref('')
const retry = ref(''), expired = ref(false)
const lifecycle = new AbortController()
let picker: AbortController | undefined
const available = computed(() => channels.value.filter(channel => `${channel.title} ${channel.username || ''}`.toLocaleLowerCase().includes(query.value.trim().toLocaleLowerCase())))
const request = <T,>(path: string, body?: object, signal = lifecycle.signal) => telegramUserRequest<T>(props.token, path, signal, body)
async function load() {
  loading.value = true; loadError.value = ''
  try { config.value = await request<TelegramChannelConfig>('channels') }
  catch (e) { if (!lifecycle.signal.aborted) loadError.value = e instanceof Error ? e.message : '读取配置失败' }
  finally { loading.value = false }
}
async function readChannels(reset = false) {
  if (busy.value || !picker) return
  const signal = picker.signal
  busy.value = 'list'; error.value = ''; retry.value = ''
  try {
    const page = await request<TelegramChannelPage>('channels/list', { cursor: reset ? '' : cursor.value }, signal)
    if (signal.aborted) return
    if (reset && page.config) { config.value = page.config; selected.value = page.config.channels.map(channel => channel.id) }
    const merged = new Map((reset ? config.value?.channels || [] : channels.value).map(channel => [channel.id, channel]))
    for (const channel of page.channels) merged.set(channel.id, channel)
    channels.value = [...merged.values()]
    selectionToken.value = page.selectionToken; cursor.value = page.nextCursor
    expired.value = false
  } catch (e) {
    if (!signal.aborted) {
      error.value = e instanceof Error ? e.message : '读取频道失败'
      expired.value = e instanceof TelegramSearchError && e.code === 'channel_selection_expired'
      retry.value = expired.value || reset ? 'restart' : 'read'
    }
  }
  finally { if (!signal.aborted) busy.value = '' }
}
function open() {
  picker?.abort(); picker = new AbortController()
  opened.value = true; busy.value = ''; error.value = ''; notice.value = ''; query.value = ''; retry.value = ''; expired.value = false
  selected.value = config.value?.channels.map(channel => channel.id) || []
  channels.value = [...config.value?.channels || []]; selectionToken.value = ''; cursor.value = ''
  void readChannels(true)
}
function close() {
  if (busy.value === 'save') return
  picker?.abort(); opened.value = false; busy.value = ''
}
async function save() {
  if (busy.value || expired.value || !selectionToken.value || !picker) return
  const signal = picker.signal
  busy.value = 'save'; error.value = ''; retry.value = ''
  try {
    const value = await request<TelegramChannelConfig>('channels', { selectionToken: selectionToken.value, channelIds: selected.value }, signal)
    if (signal.aborted) return
    config.value = value; opened.value = false
    notice.value = value.channels.length ? `已保存 ${value.channels.length} 个搜索频道。` : '已清空搜索频道。'
  } catch (e) {
    if (!signal.aborted) {
      error.value = e instanceof Error ? e.message : '保存失败'
      expired.value = e instanceof TelegramSearchError && e.code === 'channel_selection_expired'
      if (expired.value) retry.value = 'restart'
    }
  }
  finally { if (!signal.aborted) busy.value = '' }
}
onMounted(load)
onBeforeUnmount(() => { lifecycle.abort(); picker?.abort() })
</script>

<template>
  <ManagementLayout title="TG 频道配置" description="选择已加入的频道，作为详情页聚合搜索的资源来源。" :parent="{ label: '资源配置', to: '/resources' }" @logout="$emit('logout')">
    <div class="channel-settings">
      <p v-if="loadError" role="alert" class="channel-error">{{ loadError }} <button class="channel-button" @click="load">重新加载</button></p>
      <p v-if="notice" role="status" class="channel-notice">{{ notice }}</p>
      <section class="channel-panel channel-account" aria-label="Telegram 账号">
        <img :src="telegramIcon" width="36" height="36" alt="" />
        <div class="channel-account-text"><h2>Telegram 用户账号</h2><p v-if="loading">读取中…</p><p v-else-if="config?.account.hasSession">{{ config.account.name || '已保存登录会话' }}<span v-if="config.account.username"> · @{{ config.account.username }}</span></p><p v-else>请先配置 API 并登录 Telegram</p></div>
        <RouterLink to="/telegram/user" class="channel-button">管理登录</RouterLink>
      </section>
      <section class="channel-panel" aria-labelledby="saved-channels-title">
        <div class="channel-section-heading"><div><h2 id="saved-channels-title">搜索频道</h2><p>共用上方账号，可选择多个频道。</p></div><button class="channel-button channel-primary" :disabled="loading || !config?.account.hasSession" @click="open"><IconPlus :size="16" aria-hidden="true" />选择频道</button></div>
        <ul v-if="config?.channels.length" class="channel-list"><li v-for="channel in config.channels" :key="channel.id" class="channel-saved"><span class="channel-avatar"><IconCheck :size="18" aria-hidden="true" /></span><div class="channel-text"><strong>{{ channel.title }}</strong><small>{{ channel.username ? `@${channel.username}` : '私有频道' }}</small></div></li></ul>
        <div v-else class="channel-empty"><p>{{ loading ? '读取中…' : '尚未选择频道' }}</p><small v-if="!loading">登录后，选择账号已加入的资源频道。</small></div>
      </section>
    </div>
    <FloatingDialog v-if="opened" title="选择 Telegram 频道" close-label="关闭频道选择" size="config" :close-disabled="busy === 'save'" class="channel-picker" @close="close">
      <template #header><p class="channel-picker-description">从已加入的频道中选择，包含归档频道。</p><label class="channel-filter"><IconSearch :size="18" aria-hidden="true"/><input v-model="query" type="search" aria-label="筛选已加载的频道" placeholder="筛选已加载的频道" /></label></template>
      <p v-if="error" role="alert" class="channel-error">{{ error }} <button v-if="retry" :disabled="!!busy" class="channel-button" @click="readChannels(retry === 'restart')">{{ retry === 'restart' ? '重新加载频道' : '重试读取' }}</button></p>
      <fieldset :disabled="busy === 'save' || !selectionToken || expired" class="channel-options"><legend class="sr-only">可选频道</legend><label v-for="channel in available" :key="channel.id" class="channel-option" :class="{ 'is-selected': selected.includes(channel.id) }"><input v-model="selected" type="checkbox" :value="channel.id" :disabled="!selected.includes(channel.id) && selected.length >= 100" /><span class="channel-text"><strong>{{ channel.title }}</strong><small>{{ channel.username ? `@${channel.username}` : '私有频道' }}</small></span></label></fieldset>
      <div v-if="busy === 'list'" role="status" class="channel-empty">正在读取频道…</div>
      <div v-else-if="!available.length" class="channel-empty">{{ query ? '已加载的频道中没有匹配结果' : cursor ? '这一批没有找到频道，可继续加载' : error ? '频道读取未完成' : '账号中未找到可选频道' }}</div>
      <button v-if="cursor && !expired" :disabled="!!busy" class="channel-button channel-more" @click="readChannels()">加载更多频道</button>
      <template #footer><div class="channel-picker-footer"><span aria-live="polite">已选 {{ selected.length }} 个频道<span v-if="selected.length >= 100">，最多 100 个</span></span><div><button class="channel-button" :disabled="busy === 'save'" @click="close">取消</button><button class="channel-button channel-primary" :disabled="!!busy || !selectionToken || expired" @click="save">{{ busy === 'save' ? '保存中…' : '保存频道' }}</button></div></div></template>
    </FloatingDialog>
  </ManagementLayout>
</template>

<style scoped>
:global(dialog.channel-picker) { width: min(680px, calc(100vw - 32px)); height: min(600px, calc(100dvh - 48px)); }
.channel-settings { @apply text-slate-800 dark:text-slate-200; max-width: 860px; display: grid; gap: 18px; }
.channel-panel { @apply bg-white dark:bg-slate-900; padding: 20px; border-radius: 16px; box-shadow: 0 2px 14px rgb(15 23 42 / .04); }
.channel-account, .channel-section-heading { display: flex; align-items: center; gap: 14px; }
.channel-account-text { flex: 1; min-width: 0; overflow-wrap: anywhere; }
.channel-account h2, .channel-section-heading h2 { font-size: 14px; font-weight: 600; }
.channel-account p, .channel-section-heading p, .channel-picker-description { @apply text-slate-500 dark:text-slate-400; margin-top: 4px; font-size: 12px; line-height: 1.6; }
.channel-section-heading { justify-content: space-between; flex-wrap: wrap; }
.channel-button { display: inline-flex; flex-shrink: 0; align-items: center; justify-content: center; gap: 6px; min-height: 36px; padding: 7px 12px; border-radius: 9px; background: rgb(148 163 184 / .10); font-size: 12px; font-weight: 500; color: inherit; }
.channel-button:hover:not(:disabled) { background: rgb(148 163 184 / .18); }
.channel-button.channel-primary { background: #2563eb; color: #fff; }
.channel-button.channel-primary:hover:not(:disabled) { background: #1d4ed8; }
.channel-button:disabled { opacity: .45; cursor: not-allowed; }
.channel-button:focus-visible, .channel-filter:focus-within { outline: 2px solid #3b82f6; outline-offset: 2px; }
.channel-list { margin-top: 14px; display: grid; gap: 6px; }
.channel-saved { display: flex; gap: 12px; align-items: center; padding: 12px 4px; }
.channel-avatar { @apply bg-blue-50 text-blue-600 dark:bg-blue-500/15 dark:text-blue-300; display: grid; place-items: center; width: 34px; height: 34px; flex-shrink: 0; border-radius: 10px; }
.channel-text { min-width: 0; display: grid; gap: 3px; overflow-wrap: anywhere; }
.channel-text strong { font-size: 13px; line-height: 1.5; font-weight: 500; }
.channel-text small { @apply text-slate-500 dark:text-slate-400; font-size: 11px; }
.channel-empty { @apply text-slate-500 dark:text-slate-400; padding: 32px 12px; text-align: center; font-size: 13px; }
.channel-empty small { display: block; margin-top: 8px; font-size: 12px; }
.channel-error, .channel-notice { padding: 12px 14px; border-radius: 10px; font-size: 13px; line-height: 1.6; }
.channel-error { @apply bg-red-50 text-red-700 dark:bg-red-500/10 dark:text-red-300; }
.channel-notice { @apply bg-blue-50 text-blue-700 dark:bg-blue-500/15 dark:text-blue-300; }
.channel-picker-description { margin: 0 0 12px; }
.channel-filter { @apply text-slate-500 dark:text-slate-400; display: flex; gap: 10px; align-items: center; padding: 0 12px; min-height: 38px; border-radius: 10px; background: rgb(148 163 184 / .10); }
.channel-filter input { width: 100%; min-width: 0; padding: 8px 0; background: transparent; outline: none; font-size: 13px; color: var(--floating-surface-text); }
.channel-options { min-width: 0; display: grid; gap: 6px; }
.channel-option { display: flex; gap: 12px; align-items: center; min-height: 60px; padding: 10px 12px; border-radius: 10px; cursor: pointer; }
.channel-option:hover { background: rgb(148 163 184 / .10); }
.channel-option.is-selected { background: rgb(37 99 235 / .12); }
.channel-option input { width: 17px; height: 17px; flex-shrink: 0; accent-color: #2563eb; }
.channel-more { display: flex; margin: 16px auto 0; }
.channel-picker-footer, .channel-picker-footer > div { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.channel-picker-footer > span { @apply text-slate-500 dark:text-slate-400; font-size: 12px; }
@media (max-width: 639px) { .channel-panel { padding: 16px; } .channel-account { gap: 10px; flex-wrap: wrap; } .channel-button { min-height: 44px; } .channel-filter { min-height: 44px; } .channel-filter input { font-size: 16px; } }
@media (max-width: 639px) { :global(dialog.channel-picker) { width: calc(100vw - 16px); height: min(640px, calc(100dvh - 24px)); } }
</style>
