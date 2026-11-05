<script setup lang="ts">
import { computed, ref } from 'vue'
import { IconBroadcast, IconEdit, IconRobot, IconUsers } from '@tabler/icons-vue'
import FloatingDialog from '../AnimeFloatingDialog.vue'
import SettingSwitch from '../SettingSwitch.vue'
import TelegramAvatar from '../TelegramAvatar.vue'
import TelegramRelayPeerPicker from '../TelegramRelayPeerPicker.vue'
import type { TelegramRelayInput, TelegramRelayPeer } from '../../types/telegramRelay'

const props = defineProps<{
  token: string
  input: TelegramRelayInput
  peers: ReadonlyMap<string, TelegramRelayPeer>
  loading: boolean
  busy: boolean
  error: string
}>()
const emit = defineEmits<{ close: []; save: [input: TelegramRelayInput]; select: [peer: TelegramRelayPeer]; change: [] }>()
const draft = ref<TelegramRelayInput>({ ...props.input, linkTypes: [...props.input.linkTypes] })
const picker = ref<'source' | 'target'>()
const canSave = computed(() => !!draft.value.sourceId && !!draft.value.targetId && draft.value.sourceId !== draft.value.targetId && !!draft.value.linkTypes.length && !props.busy)

function choose(peer: TelegramRelayPeer, selectionToken: string) {
  if (!picker.value) return
  emit('select', peer)
  if (picker.value === 'source') {
    draft.value.sourceId = peer.id
    draft.value.sourceTitle = peer.title
    draft.value.sourceSelectionToken = selectionToken
    if (!draft.value.name) draft.value.name = peer.title
  } else {
    draft.value.targetId = peer.id
    draft.value.targetTitle = peer.title
    draft.value.targetSelectionToken = selectionToken
  }
  picker.value = undefined
  emit('change')
}
</script>

<template>
  <FloatingDialog :title="draft.id ? '编辑转发规则' : '添加转发规则'" close-label="关闭规则编辑" :close-disabled="busy" class="relay-editor" @close="emit('close')">
    <form id="relay-rule-form" class="relay-form" :inert="busy" :aria-busy="busy" @submit.prevent="canSave && emit('save', draft)">
      <label>规则名称<input v-model="draft.name" maxlength="100" placeholder="选择来源后自动填写" /></label>
      <div class="relay-pair">
        <div>
          <span>来源频道</span>
          <button type="button" class="relay-select" :disabled="loading || busy" @click="picker = 'source'">
            <TelegramAvatar :token="token" :peer-id="draft.sourceId" :avatar-id="peers.get(draft.sourceId)?.avatarId" :size="32" class="relay-selection-avatar"><IconBroadcast :size="18" aria-hidden="true" /></TelegramAvatar>
            <span>{{ draft.sourceTitle || '选择来源频道' }}</span><IconEdit :size="15" aria-hidden="true" />
          </button>
        </div>
        <div>
          <span>转发目标</span>
          <button type="button" class="relay-select" :disabled="loading || busy" @click="picker = 'target'">
            <TelegramAvatar :token="token" :peer-id="draft.targetId" :avatar-id="peers.get(draft.targetId)?.avatarId" :size="32" class="relay-selection-avatar">
              <IconRobot v-if="draft.targetId.startsWith('user:')" :size="18" aria-hidden="true" /><IconUsers v-else-if="draft.targetId.startsWith('chat:')" :size="18" aria-hidden="true" /><IconBroadcast v-else :size="18" aria-hidden="true" />
            </TelegramAvatar>
            <span>{{ draft.targetTitle || '选择机器人、频道或群组' }}</span><IconEdit :size="15" aria-hidden="true" />
          </button>
        </div>
      </div>
      <fieldset class="relay-types">
        <legend>识别链接</legend>
        <label><input v-model="draft.linkTypes" type="checkbox" value="115" />115 分享</label>
        <label><input v-model="draft.linkTypes" type="checkbox" value="ed2k" />ed2k</label>
      </fieldset>
      <details class="relay-filters">
        <summary>关键词筛选（可选）</summary>
        <label>包含条件<input v-model="draft.includePattern" maxlength="1000" spellcheck="false" placeholder="正则表达式，留空不限制" /></label>
        <label>排除条件<input v-model="draft.excludePattern" maxlength="1000" spellcheck="false" placeholder="正则表达式，匹配后不转发" /></label>
      </details>
      <SettingSwitch v-model="draft.enabled" label="启用这条规则" :disabled="loading || busy" />
      <p v-if="draft.sourceId && draft.sourceId === draft.targetId" role="alert" class="relay-error">来源和目标不能是同一个频道。</p>
      <p v-if="!draft.linkTypes.length" role="alert" class="relay-error">至少选择一种链接。</p>
      <p v-if="error" role="alert" class="relay-error">{{ error }}</p>
      <p class="relay-help">消息投递不代表目标机器人已下载或转存成功。不会主动发送测试消息。</p>
    </form>
    <template #footer>
      <div class="relay-dialog-actions">
        <button type="button" class="relay-button" :disabled="loading || busy" @click="emit('close')">取消</button>
        <button form="relay-rule-form" type="submit" class="relay-button relay-primary" :disabled="!canSave">{{ busy ? '保存中…' : '保存规则' }}</button>
      </div>
    </template>
  </FloatingDialog>
  <TelegramRelayPeerPicker v-if="picker" :token="token" :role="picker" :excluded-id="picker === 'source' ? draft.targetId : draft.sourceId" @close="picker = undefined" @select="choose" />
</template>

<style scoped src="./relayControls.css" />

<style scoped>
.relay-help { @apply text-slate-500 dark:text-slate-400; font-size: 12px; line-height: 1.7; }
.relay-form { display: grid; gap: 18px; padding-block: 8px; }
.relay-form > label, .relay-filters label { display: grid; gap: 7px; font-size: 12px; }
.relay-form input:not([type=checkbox]) { @apply bg-slate-50 dark:bg-slate-950 border-slate-200 dark:border-slate-700; min-width: 0; width: 100%; min-height: 38px; border-width: 1px; border-style: solid; border-radius: 8px; padding: 8px 11px; outline: none; color: var(--floating-surface-text); font-size: 13px; }
.relay-form input:focus { border-color: #3b82f6; }
.relay-pair { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.relay-pair > div { display: grid; gap: 8px; min-width: 0; font-size: 12px; }
.relay-select { @apply bg-slate-100 dark:bg-slate-800; display: flex; align-items: center; gap: 8px; min-width: 0; min-height: 54px; padding: 10px; border-radius: 10px; text-align: left; }
.relay-select > span { min-width: 0; flex: 1; overflow-wrap: anywhere; font-size: 13px; }
.relay-select > svg { flex-shrink: 0; color: #3b82f6; }
.relay-types { display: flex; gap: 20px; font-size: 13px; }
.relay-types legend { margin-bottom: 10px; font-size: 12px; }
.relay-types label { display: flex; align-items: center; gap: 7px; }
.relay-types input { width: 16px; height: 16px; accent-color: #2563eb; }
.relay-filters { @apply border-slate-200 dark:border-slate-700; padding-top: 12px; border-top-width: 1px; border-top-style: solid; }
.relay-filters summary { cursor: pointer; font-size: 12px; }
.relay-filters[open] { display: grid; gap: 12px; }
.relay-filters label { margin-top: 12px; }
.relay-dialog-actions { display: flex; justify-content: flex-end; gap: 8px; }
.relay-select:focus-visible { outline: 2px solid #3b82f6; outline-offset: 2px; }
:global(dialog.relay-editor) { width: min(660px, calc(100vw - 24px)); }
@media (max-width: 639px) {
  .relay-pair { grid-template-columns: 1fr; }
  .relay-form input:not([type=checkbox]) { font-size: 16px; }
}
.relay-select > .relay-selection-avatar { @apply bg-blue-50 dark:bg-blue-500/15 text-blue-600 dark:text-blue-300; flex: 0 0 32px; border-radius: 50%; }
</style>
