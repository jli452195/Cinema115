<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { IconArrowRight, IconBroadcast, IconEdit, IconPlus, IconRefresh, IconRobot, IconTrash, IconUsers } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import FloatingDialog from '../components/AnimeFloatingDialog.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import TelegramRelayPeerPicker from '../components/TelegramRelayPeerPicker.vue'
import TelegramAvatar from '../components/TelegramAvatar.vue'
import { telegramRelayRequest, type TelegramRelayInput, type TelegramRelayPeer, type TelegramRelayRule, type TelegramRelaySettings } from '../types/telegramRelay'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const settings = ref<TelegramRelaySettings>(), loading = ref(true), error = ref(''), notice = ref(''), busy = ref('')
const editing = ref<TelegramRelayInput>(), editError = ref(''), picker = ref<'source' | 'target'>(), deleting = ref<TelegramRelayRule>()
const peerAvatars = ref(new Map<string, TelegramRelayPeer>())
const lifecycle = new AbortController()
const request = <T,>(path = '', body?: object, method?: string) => telegramRelayRequest<T>(props.token, path, lifecycle.signal, body, method)
const canSave = computed(() => !!editing.value?.sourceId && !!editing.value?.targetId && editing.value.sourceId !== editing.value.targetId && !!editing.value.linkTypes.length && !busy.value)
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
function choose(peer: TelegramRelayPeer, selectionToken: string) {
  if (!editing.value || !picker.value) return
  peerAvatars.value.set(peer.id, peer)
  if (picker.value === 'source') { editing.value.sourceId = peer.id; editing.value.sourceTitle = peer.title; editing.value.sourceSelectionToken = selectionToken; if (!editing.value.name) editing.value.name = peer.title }
  else { editing.value.targetId = peer.id; editing.value.targetTitle = peer.title; editing.value.targetSelectionToken = selectionToken }
  picker.value = undefined; editError.value = ''
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
    <FloatingDialog v-if="editing" :title="editing.id ? '编辑转发规则' : '添加转发规则'" close-label="关闭规则编辑" :close-disabled="!!busy" class="relay-editor" @close="editing = undefined"><form id="relay-rule-form" class="relay-form" :inert="!!busy" :aria-busy="!!busy" @submit.prevent="canSave && save(editing!, true)"><label>规则名称<input v-model="editing.name" maxlength="100" placeholder="选择来源后自动填写" /></label><div class="relay-pair"><div><span>来源频道</span><button type="button" class="relay-select" :disabled="loading || !!busy" @click="picker = 'source'"><TelegramAvatar :token="token" :peer-id="editing.sourceId" :avatar-id="peerAvatars.get(editing.sourceId)?.avatarId" :size="32" class="relay-selection-avatar"><IconBroadcast :size="18" aria-hidden="true" /></TelegramAvatar><span>{{ editing.sourceTitle || '选择来源频道' }}</span><IconEdit :size="15" aria-hidden="true" /></button></div><div><span>转发目标</span><button type="button" class="relay-select" :disabled="loading || !!busy" @click="picker = 'target'"><TelegramAvatar :token="token" :peer-id="editing.targetId" :avatar-id="peerAvatars.get(editing.targetId)?.avatarId" :size="32" class="relay-selection-avatar"><IconRobot v-if="editing.targetId.startsWith('user:')" :size="18" aria-hidden="true" /><IconUsers v-else-if="editing.targetId.startsWith('chat:')" :size="18" aria-hidden="true" /><IconBroadcast v-else :size="18" aria-hidden="true" /></TelegramAvatar><span>{{ editing.targetTitle || '选择机器人、频道或群组' }}</span><IconEdit :size="15" aria-hidden="true" /></button></div></div><fieldset class="relay-types"><legend>识别链接</legend><label><input v-model="editing.linkTypes" type="checkbox" value="115" />115 分享</label><label><input v-model="editing.linkTypes" type="checkbox" value="ed2k" />ed2k</label></fieldset><details class="relay-filters"><summary>关键词筛选（可选）</summary><label>包含条件<input v-model="editing.includePattern" maxlength="1000" spellcheck="false" placeholder="正则表达式，留空不限制" /></label><label>排除条件<input v-model="editing.excludePattern" maxlength="1000" spellcheck="false" placeholder="正则表达式，匹配后不转发" /></label></details><SettingSwitch v-model="editing.enabled" label="启用这条规则" :disabled="loading || !!busy" /><p v-if="editing.sourceId && editing.sourceId === editing.targetId" role="alert" class="relay-error">来源和目标不能是同一个频道。</p><p v-if="!editing.linkTypes.length" role="alert" class="relay-error">至少选择一种链接。</p><p v-if="editError" role="alert" class="relay-error">{{ editError }}</p><p class="relay-help">消息投递不代表目标机器人已下载或转存成功。不会主动发送测试消息。</p></form><template #footer><div class="relay-dialog-actions"><button type="button" class="relay-button" :disabled="loading || !!busy" @click="editing = undefined">取消</button><button form="relay-rule-form" type="submit" class="relay-button relay-primary" :disabled="!canSave">{{ busy ? '保存中…' : '保存规则' }}</button></div></template></FloatingDialog>
    <TelegramRelayPeerPicker v-if="picker && editing" :token="token" :role="picker" :excluded-id="picker === 'source' ? editing.targetId : editing.sourceId" @close="picker = undefined" @select="choose" />
    <P115ActionDialog v-if="deleting" title="删除转发规则？" :busy="!!busy" @close="deleting = undefined"><p>删除“{{ deleting.name || deleting.sourceTitle }}”规则及未发送消息。</p><p v-if="error" role="alert" class="relay-error">{{ error }}</p><template #footer><button class="relay-button" :disabled="loading || !!busy" @click="deleting = undefined">取消</button><button class="relay-button relay-delete" :disabled="loading || !!busy" @click="remove">删除规则</button></template></P115ActionDialog>
  </ManagementLayout>
</template>

<style scoped>
.relay-page { @apply text-slate-800 dark:text-slate-200; display: grid; gap: 16px; max-width: 1120px; }.relay-toolbar, .relay-account, .relay-actions, .relay-card-heading, .relay-card-footer, .relay-card-actions { display: flex; align-items: center; gap: 10px; }.relay-toolbar { justify-content: space-between; flex-wrap: wrap; }.relay-account { gap: 12px; }.relay-account strong { font-size: 14px; }.relay-account p { display: flex; gap: 10px; margin-top: 4px; font-size: 12px; }.relay-account a { @apply text-blue-600 dark:text-blue-300; }.relay-symbol { @apply bg-blue-50 dark:bg-blue-500/15 text-blue-600 dark:text-blue-300; display: grid; place-items: center; width: 40px; height: 40px; border-radius: 50%; }.relay-state { @apply text-slate-500 dark:text-slate-400; }.relay-state.connected { @apply text-green-700 dark:text-green-300; }.relay-button { @apply bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-200; display: inline-flex; align-items: center; justify-content: center; gap: 6px; min-height: 36px; padding: 8px 12px; border-radius: 9px; font-size: 12px; font-weight: 500; }.relay-button:hover:not(:disabled) { background: rgb(148 163 184 / .22); }.relay-button.relay-primary { background: #2563eb; color: #fff; }.relay-button.relay-primary:hover:not(:disabled) { background: #1d4ed8; }.relay-button:disabled, .relay-icon:disabled { opacity: .45; cursor: default; }.relay-description, .relay-help { @apply text-slate-500 dark:text-slate-400; font-size: 12px; line-height: 1.7; }.relay-description { max-width: 850px; }.relay-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); align-items: start; gap: 16px; }.relay-card { @apply bg-white dark:bg-slate-900 border-slate-200 dark:border-slate-700; min-width: 0; padding: 18px 20px 12px; border-width: 1px; border-style: solid; border-radius: 15px; box-shadow: 0 3px 14px rgb(15 23 42 / .025); }.relay-card-heading { justify-content: space-between; gap: 12px; }.relay-card-heading h2 { min-width: 0; overflow-wrap: anywhere; font-size: 14px; font-weight: 600; }.relay-card :deep(.relay-switch label > span:last-child) { display: none; }.relay-card.is-disabled .relay-route, .relay-card.is-disabled .relay-tags { opacity: .65; }.relay-route { display: grid; grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr); align-items: center; gap: 12px; margin-top: 18px; }.relay-route > div { display: grid; gap: 6px; min-width: 0; }.relay-route span { @apply text-slate-500 dark:text-slate-400; font-size: 11px; }.relay-route strong { overflow-wrap: anywhere; font-size: 13px; font-weight: 500; line-height: 1.6; }.relay-route > svg { @apply text-slate-400 dark:text-slate-500; }.relay-tags { display: flex; gap: 6px; flex-wrap: wrap; }.relay-tags > span { @apply bg-blue-50 dark:bg-blue-500/15 text-blue-700 dark:text-blue-300; padding: 3px 7px; border-radius: 6px; font-size: 11px; }.relay-card-footer { @apply border-slate-100 dark:border-slate-800; margin-top: 13px; padding-top: 9px; justify-content: space-between; border-top-width: 1px; border-top-style: solid; }.relay-icon { @apply text-slate-500 dark:text-slate-400; display: grid; place-items: center; width: 32px; height: 32px; border-radius: 8px; }.relay-icon:hover { background: rgb(148 163 184 / .13); }.relay-danger { @apply text-red-600 dark:text-red-300; }.relay-empty { @apply text-slate-500 dark:text-slate-400; display: grid; justify-items: center; gap: 10px; padding: 44px 15px; font-size: 13px; text-align: center; }.relay-empty strong { @apply text-slate-700 dark:text-slate-300; font-weight: 500; }.relay-error, .relay-warning, .relay-notice { padding: 11px 13px; border-radius: 9px; font-size: 12px; line-height: 1.7; overflow-wrap: anywhere; }.relay-error { @apply bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300; }.relay-warning { @apply bg-amber-50 dark:bg-amber-500/10 text-amber-800 dark:text-amber-300; }.relay-notice { @apply bg-blue-50 dark:bg-blue-500/10 text-blue-700 dark:text-blue-300; }.relay-form { display: grid; gap: 18px; padding-block: 8px; }.relay-form > label, .relay-filters label { display: grid; gap: 7px; font-size: 12px; }.relay-form input:not([type=checkbox]) { @apply bg-slate-50 dark:bg-slate-950 border-slate-200 dark:border-slate-700; min-width: 0; width: 100%; min-height: 38px; border-width: 1px; border-style: solid; border-radius: 8px; padding: 8px 11px; outline: none; color: var(--floating-surface-text); font-size: 13px; }.relay-form input:focus { border-color: #3b82f6; }.relay-pair { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }.relay-pair > div { display: grid; gap: 8px; min-width: 0; font-size: 12px; }.relay-select { @apply bg-slate-100 dark:bg-slate-800; display: flex; align-items: center; gap: 8px; min-width: 0; min-height: 54px; padding: 10px; border-radius: 10px; text-align: left; }.relay-select > span { min-width: 0; flex: 1; overflow-wrap: anywhere; font-size: 13px; }.relay-select > svg { flex-shrink: 0; color: #3b82f6; }.relay-types { display: flex; gap: 20px; font-size: 13px; }.relay-types legend { margin-bottom: 10px; font-size: 12px; }.relay-types label { display: flex; align-items: center; gap: 7px; }.relay-types input { width: 16px; height: 16px; accent-color: #2563eb; }.relay-filters { @apply border-slate-200 dark:border-slate-700; padding-top: 12px; border-top-width: 1px; border-top-style: solid; }.relay-filters summary { cursor: pointer; font-size: 12px; }.relay-filters[open] { display: grid; gap: 12px; }.relay-filters label { margin-top: 12px; }.relay-dialog-actions { display: flex; justify-content: flex-end; gap: 8px; }.relay-delete { @apply bg-red-600 text-white; }.relay-button:focus-visible, .relay-select:focus-visible, .relay-icon:focus-visible { outline: 2px solid #3b82f6; outline-offset: 2px; }
.relay-page :deep([role=switch]:checked + span), .relay-form :deep([role=switch]:checked + span) { background: #2563eb; }
:global(dialog.relay-editor) { width: min(660px, calc(100vw - 24px)); }
@media(max-width: 760px) { .relay-grid { grid-template-columns: 1fr; } }@media(max-width: 639px) { .relay-toolbar { align-items: flex-start; }.relay-toolbar > .relay-actions { width: 100%; justify-content: flex-end; }.relay-card { padding: 16px 16px 10px; }.relay-button { min-height: 42px; }.relay-pair { grid-template-columns: 1fr; }.relay-form input:not([type=checkbox]) { font-size: 16px; } }
.relay-select > .relay-selection-avatar { @apply bg-blue-50 dark:bg-blue-500/15 text-blue-600 dark:text-blue-300; flex: 0 0 32px; border-radius: 50%; }
.relay-route > .relay-peer { display: flex; align-items: center; gap: 9px; }.relay-peer-copy { display: grid; gap: 4px; min-width: 0; }.relay-route .relay-peer-avatar { @apply bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400; flex: 0 0 32px; }
</style>
