<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { IconArrowRight, IconBroadcast, IconEdit, IconPlus, IconRefresh, IconRobot, IconTrash, IconUsers } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import TelegramRelayRuleEditor from '../components/telegram/TelegramRelayRuleEditor.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import TelegramAvatar from '../components/TelegramAvatar.vue'
import { telegramRelayRequest } from '../lib/telegram-relay-api'
import type { TelegramRelayInput, TelegramRelayPeer, TelegramRelayRule, TelegramRelaySettings } from '../types/telegramRelay'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const settings = ref<TelegramRelaySettings>(), loading = ref(true), error = ref(''), notice = ref(''), busy = ref('')
const editing = ref<TelegramRelayInput>(), editError = ref(''), deleting = ref<TelegramRelayRule>()
const peerAvatars = ref(new Map<string, TelegramRelayPeer>())
const lifecycle = new AbortController()
const request = <T,>(path = '', body?: object, method?: string) => telegramRelayRequest<T>(props.token, path, lifecycle.signal, body, method)
const statusText = computed(() => settings.value?.status.connected ? '监听已连接' : settings.value?.account.hasSession ? '监听未连接' : '尚未登录')
function message(reason: unknown) { return reason instanceof Error ? reason.message : '操作失败，请重试' }
async function load() {
  if (busy.value) return
  loading.value = true; error.value = ''
  try { const value = await request<TelegramRelaySettings>(); if (!lifecycle.signal.aborted) { settings.value = value; peerAvatars.value = new Map((value.peers || []).map(peer => [peer.id, peer])) } }
  catch (reason) { if (!lifecycle.signal.aborted) error.value = message(reason) }
  finally { if (!lifecycle.signal.aborted) loading.value = false }
}
function edit(rule?: TelegramRelayRule) {
  if (loading.value || busy.value) return
  editing.value = rule ? { ...rule, linkTypes: [...rule.linkTypes], includePattern: rule.includePattern || '', excludePattern: rule.excludePattern || '' } : { id: '', name: '', sourceId: '', sourceTitle: '', targetId: '', targetTitle: '', enabled: true, linkTypes: ['115', 'ed2k'], includePattern: '', excludePattern: '' }
  editError.value = ''; notice.value = ''
}
function keep(rule: TelegramRelayRule) {
  if (!settings.value) return
  const exists = settings.value.rules.some(item => item.id === rule.id)
  settings.value.rules = exists ? settings.value.rules.map(item => item.id === rule.id ? rule : item) : [...settings.value.rules, rule]
}
async function save(input: TelegramRelayInput, close = false) {
  if (busy.value || loading.value) return false
  busy.value = input.id || 'new'; error.value = ''; editError.value = ''; notice.value = ''
  try {
    const { rule } = await request<{ rule: TelegramRelayRule }>('/rules', {
      id: input.id, name: input.name, sourceId: input.sourceId, targetId: input.targetId,
      enabled: input.enabled, linkTypes: input.linkTypes, includePattern: input.includePattern, excludePattern: input.excludePattern,
      sourceSelectionToken: input.sourceSelectionToken, targetSelectionToken: input.targetSelectionToken,
    })
    if (lifecycle.signal.aborted) return false
    keep(rule); if (close) editing.value = undefined
    notice.value = rule.enabled ? '转发规则已保存并启用。' : '转发规则已保存并停用。'
    return true
  } catch (reason) { if (!lifecycle.signal.aborted) { if (close) editError.value = message(reason); else error.value = message(reason) }; return false }
  finally { if (!lifecycle.signal.aborted) busy.value = '' }
}
async function toggle(rule: TelegramRelayRule, enabled: boolean) {
  const previous = rule.enabled
  rule.enabled = enabled
  if (!await save(rule)) rule.enabled = previous
}
async function remove() {
  const rule = deleting.value
  if (!rule || busy.value) return
  busy.value = rule.id; error.value = ''; notice.value = ''
  try { await request(`/rules/${encodeURIComponent(rule.id)}`, undefined, 'DELETE'); if (!lifecycle.signal.aborted && settings.value) { settings.value.rules = settings.value.rules.filter(item => item.id !== rule.id); deleting.value = undefined; notice.value = '转发规则及未发送消息已删除。' } }
  catch (reason) { if (!lifecycle.signal.aborted) error.value = message(reason) }
  finally { if (!lifecycle.signal.aborted) busy.value = '' }
}
onMounted(load)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout title="TG 频道转发" description="一个来源频道对应一个目标，独立筛选和启停。" :parent="{ label: 'Telegram 配置', to: '/telegram' }" @logout="$emit('logout')">
    <div class="relay-page">
      <header class="relay-toolbar"><div class="relay-account"><TelegramAvatar :token="token" peer-id="self" :avatar-id="settings?.account.avatarId" :size="40" class="relay-symbol"><IconBroadcast :size="21" aria-hidden="true" /></TelegramAvatar><div><strong>{{ settings?.account.name || 'Telegram 用户账号' }}</strong><p><span class="relay-state" :class="{ connected: settings?.status.connected }">{{ loading ? '读取中…' : statusText }}</span><RouterLink to="/telegram/user">管理登录</RouterLink></p></div></div><div class="relay-actions"><button type="button" class="relay-button" :disabled="loading || !!busy" @click="load"><IconRefresh :size="16" aria-hidden="true" />刷新</button><button type="button" class="relay-button relay-primary" :disabled="loading || !!busy || !settings?.account.hasSession" @click="edit()"><IconPlus :size="16" aria-hidden="true" />添加转发</button></div></header>
      <p v-if="error" role="alert" class="relay-error">{{ error }}</p><p v-if="notice" role="status" class="relay-notice">{{ notice }}</p><p v-if="settings?.status.error" class="relay-warning">{{ settings.status.error }}</p>
      <p class="relay-description">保留消息正文，将按钮中的 115 / ed2k 地址展开到卡片内。首次启用从当前消息位置开始；暂停不投递，恢复后继续处理可用的新消息。</p>
      <div v-if="loading && !settings" role="status" class="relay-empty">正在读取转发规则…</div><div v-else-if="!settings?.rules.length" class="relay-empty"><IconBroadcast :size="28" aria-hidden="true" /><strong>还没有转发规则</strong><p>选择一个来源频道和一个转发目标后保存。</p></div>
      <div v-else class="relay-grid">
        <article v-for="rule in settings.rules" :key="rule.id" class="relay-card" :class="{ 'is-disabled': !rule.enabled }">
          <div class="relay-card-heading"><h2>{{ rule.name || rule.sourceTitle }}</h2><SettingSwitch :model-value="rule.enabled" :label="`启用${rule.name || rule.sourceTitle}`" :disabled="loading || !!busy" class="relay-switch" @update:model-value="value => toggle(rule, value)" /></div>
          <div class="relay-route">
            <div class="relay-peer"><TelegramAvatar :token="token" :peer-id="rule.sourceId" :avatar-id="peerAvatars.get(rule.sourceId)?.avatarId" :size="32" class="relay-peer-avatar"><IconBroadcast :size="18" aria-hidden="true" /></TelegramAvatar><div class="relay-peer-copy"><span>来源频道</span><strong>{{ rule.sourceTitle }}</strong></div></div>
            <IconArrowRight :size="17" aria-hidden="true" />
            <div class="relay-peer"><TelegramAvatar :token="token" :peer-id="rule.targetId" :avatar-id="peerAvatars.get(rule.targetId)?.avatarId" :size="32" class="relay-peer-avatar"><IconRobot v-if="rule.targetId.startsWith('user:')" :size="18" aria-hidden="true" /><IconUsers v-else-if="rule.targetId.startsWith('chat:')" :size="18" aria-hidden="true" /><IconBroadcast v-else :size="18" aria-hidden="true" /></TelegramAvatar><div class="relay-peer-copy"><span>转发目标</span><strong>{{ rule.targetTitle }}</strong></div></div>
          </div>
          <footer class="relay-card-footer">
            <div class="relay-tags"><span v-for="kind in rule.linkTypes" :key="kind">{{ kind === '115' ? '115 分享' : 'ed2k' }}</span></div>
            <div class="relay-card-actions"><button type="button" class="relay-icon" :aria-label="`编辑${rule.name || rule.sourceTitle}`" :disabled="loading || !!busy" @click="edit(rule)"><IconEdit :size="17" aria-hidden="true" /></button><button type="button" class="relay-icon relay-danger" :aria-label="`删除${rule.name || rule.sourceTitle}`" :disabled="loading || !!busy" @click="deleting = rule; error = ''"><IconTrash :size="17" aria-hidden="true" /></button></div>
          </footer>
        </article>
      </div>
    </div>
    <TelegramRelayRuleEditor v-if="editing" :token="token" :input="editing" :peers="peerAvatars" :loading="loading" :busy="!!busy" :error="editError" @close="editing = undefined" @save="save($event, true)" @select="peer => peerAvatars.set(peer.id, peer)" @change="editError = ''" />
    <P115ActionDialog v-if="deleting" title="删除转发规则？" :busy="!!busy" @close="deleting = undefined"><p>删除“{{ deleting.name || deleting.sourceTitle }}”规则及未发送消息。</p><p v-if="error" role="alert" class="relay-error">{{ error }}</p><template #footer><button class="relay-button" :disabled="loading || !!busy" @click="deleting = undefined">取消</button><button class="relay-button relay-delete" :disabled="loading || !!busy" @click="remove">删除规则</button></template></P115ActionDialog>
  </ManagementLayout>
</template>

<style scoped src="../components/telegram/relayControls.css" />

<style scoped>
.relay-page { @apply text-slate-800 dark:text-slate-200; display: grid; gap: 16px; max-width: 1120px; }
.relay-toolbar, .relay-account, .relay-actions, .relay-card-heading, .relay-card-footer, .relay-card-actions { display: flex; align-items: center; gap: 10px; }
.relay-toolbar { justify-content: space-between; flex-wrap: wrap; }
.relay-account { gap: 12px; }
.relay-account strong { font-size: 14px; }
.relay-account p { display: flex; gap: 10px; margin-top: 4px; font-size: 12px; }
.relay-account a { @apply text-blue-600 dark:text-blue-300; }
.relay-symbol { @apply bg-blue-50 dark:bg-blue-500/15 text-blue-600 dark:text-blue-300; display: grid; place-items: center; width: 40px; height: 40px; border-radius: 50%; }
.relay-state { @apply text-slate-500 dark:text-slate-400; }
.relay-state.connected { @apply text-green-700 dark:text-green-300; }
.relay-icon:disabled { opacity: .45; cursor: default; }
.relay-description { @apply text-slate-500 dark:text-slate-400; max-width: 850px; font-size: 12px; line-height: 1.7; }
.relay-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); align-items: start; gap: 16px; }
.relay-card { @apply bg-white dark:bg-slate-900 border-slate-200 dark:border-slate-700; min-width: 0; padding: 18px 20px 12px; border-width: 1px; border-style: solid; border-radius: 15px; box-shadow: 0 3px 14px rgb(15 23 42 / .025); }
.relay-card-heading { justify-content: space-between; gap: 12px; }
.relay-card-heading h2 { min-width: 0; overflow-wrap: anywhere; font-size: 14px; font-weight: 600; }
.relay-card :deep(.relay-switch label > span:last-child) { display: none; }
.relay-card.is-disabled .relay-route, .relay-card.is-disabled .relay-tags { opacity: .65; }
.relay-route { display: grid; grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr); align-items: center; gap: 12px; margin-top: 18px; }
.relay-route > div { display: grid; gap: 6px; min-width: 0; }
.relay-route span { @apply text-slate-500 dark:text-slate-400; font-size: 11px; }
.relay-route strong { overflow-wrap: anywhere; font-size: 13px; font-weight: 500; line-height: 1.6; }
.relay-route > svg { @apply text-slate-400 dark:text-slate-500; }
.relay-tags { display: flex; gap: 6px; flex-wrap: wrap; }
.relay-tags > span { @apply bg-blue-50 dark:bg-blue-500/15 text-blue-700 dark:text-blue-300; padding: 3px 7px; border-radius: 6px; font-size: 11px; }
.relay-card-footer { @apply border-slate-100 dark:border-slate-800; margin-top: 13px; padding-top: 9px; justify-content: space-between; border-top-width: 1px; border-top-style: solid; }
.relay-icon { @apply text-slate-500 dark:text-slate-400; display: grid; place-items: center; width: 32px; height: 32px; border-radius: 8px; }
.relay-icon:hover { background: rgb(148 163 184 / .13); }
.relay-danger { @apply text-red-600 dark:text-red-300; }
.relay-empty { @apply text-slate-500 dark:text-slate-400; display: grid; justify-items: center; gap: 10px; padding: 44px 15px; font-size: 13px; text-align: center; }
.relay-empty strong { @apply text-slate-700 dark:text-slate-300; font-weight: 500; }
.relay-warning, .relay-notice { padding: 11px 13px; border-radius: 9px; font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; }
.relay-warning { @apply bg-amber-50 dark:bg-amber-500/10 text-amber-800 dark:text-amber-300; }
.relay-notice { @apply bg-blue-50 dark:bg-blue-500/10 text-blue-700 dark:text-blue-300; }
.relay-delete { @apply bg-red-600 text-white; }
.relay-icon:focus-visible { outline: 2px solid #3b82f6; outline-offset: 2px; }
@media (max-width: 760px) {
  .relay-grid { grid-template-columns: 1fr; }
}
@media (max-width: 639px) {
  .relay-toolbar { align-items: flex-start; }
  .relay-toolbar > .relay-actions { width: 100%; justify-content: flex-end; }
  .relay-card { padding: 16px 16px 10px; }
}
.relay-route > .relay-peer { display: flex; align-items: center; gap: 9px; }
.relay-peer-copy { display: grid; gap: 4px; min-width: 0; }
.relay-route .relay-peer-avatar { @apply bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400; flex: 0 0 32px; }
</style>
