<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useId } from 'vue'
import { RouterLink } from 'vue-router'
import { memberRequest } from '../types/memberPlayback'
import type { DynamicPoolSettings } from '../types/dynamicPool'
import P115SidePanel from './P115SidePanel.vue'
import P115ActionDialog from './P115ActionDialog.vue'
import P115Icon from './P115Icon.vue'

const props = defineProps<{ token: string; settings: DynamicPoolSettings }>()
const emit = defineEmits<{ close: []; saved: [settings: DynamicPoolSettings] }>()
// A mounted drawer owns its draft. Polling changes the parent's snapshot, never this form.
const baseline = JSON.stringify(props.settings), draft = ref({ ...props.settings })
const dirty = computed(() => JSON.stringify(draft.value) !== baseline)
const busy = ref(false), discard = ref(false), error = ref('')
const formID = useId(), controller = new AbortController()
const form = ref<HTMLFormElement>()
type FieldKey = Exclude<keyof DynamicPoolSettings, 'notificationBotId'>
const groups: { title: string; icon: 'pool' | 'clock' | 'settings'; fields: { key: FieldKey; label: string; min: number; max: number; hint: string }[] }[] = [
  { title: '容量与保留', icon: 'pool' as const, fields: [
    { key: 'maxSessions' as const, label: '每 UID 名额上限', min: 1, max: 20, hint: '1–20，同 UID 配置共享' },
    { key: 'leaseMinutes' as const, label: '无停止通知时保守保留（分钟）', min: 5, max: 1440, hint: '5–1440 分钟' },
  ] },
  { title: '重试与冷却', icon: 'clock' as const, fields: [
    { key: 'maxAttempts' as const, label: '最多尝试账号数', min: 1, max: 3, hint: '1–3 个，故障时有界切换' },
    { key: 'budgetSeconds' as const, label: '取链总预算（秒）', min: 5, max: 90, hint: '5–90 秒，包含重试' },
    { key: 'cooldownSeconds' as const, label: '故障冷却基准（秒）', min: 5, max: 3600, hint: '5–3600 秒；首次网络故障最多等待约 12 秒' },
  ] },
  { title: 'API 频率与通知', icon: 'settings' as const, fields: [
    { key: 'uidIntervalMs' as const, label: '每 UID API 请求间隔（毫秒）', min: 100, max: 10000, hint: '100–10000 毫秒' },
    { key: 'globalIntervalMs' as const, label: '全局 API 请求间隔（毫秒）', min: 10, max: 1000, hint: '10–1000，不大于每 UID 间隔' },
  ] },
]
function close() { if (!busy.value) { if (dirty.value) discard.value = true; else emit('close') } }
async function discardChanges() { discard.value = false; await nextTick(); emit('close') }
async function save() {
  if (busy.value || !dirty.value) return
  error.value = ''
  if (!form.value?.reportValidity()) return
  for (const field of groups.flatMap(g => g.fields)) {
    const value = draft.value[field.key]
    if (!Number.isInteger(value) || value < field.min || value > field.max) { error.value = `${field.label}须为 ${field.min}–${field.max} 的整数`; (form.value.elements.namedItem(field.key) as HTMLInputElement | null)?.focus(); return }
  }
  if (draft.value.globalIntervalMs > draft.value.uidIntervalMs) { error.value = '全局 API 请求间隔不能大于每 UID 间隔'; (form.value.elements.namedItem('globalIntervalMs') as HTMLInputElement | null)?.focus(); return }
  busy.value = true
  try {
    const data = await memberRequest(props.token, AbortSignal.any([controller.signal, AbortSignal.timeout(15000)]), '/pool', { ...draft.value }) as { settings: DynamicPoolSettings }
    if (!controller.signal.aborted) emit('saved', data.settings)
  } catch (e) { if (!controller.signal.aborted) error.value = e instanceof Error ? e.message : '保存失败，请重试' }
  finally { busy.value = false }
}
onBeforeUnmount(() => controller.abort())
</script>
<template>
  <P115SidePanel title="调度设置" description="配置 UID 容量、请求预算与通知。" :busy="busy" @close="close">
    <form :id="formID" ref="form" class="pool-settings-form" @submit.prevent="save">
      <fieldset v-for="(group, index) in groups" :key="group.title" :disabled="busy" :class="index ? 'mt-6 border-t border-slate-200 pt-5 dark:border-slate-700' : ''">
        <h3 class="mb-4 flex items-center gap-2 text-sm font-semibold text-slate-900 dark:text-slate-100"><P115Icon :name="group.icon" class="text-slate-500" />{{ group.title }}</h3>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <label v-for="field in group.fields" :key="field.key" class="block min-w-0 text-xs leading-5">{{ field.label }}
            <input v-model.number="draft[field.key]" :name="field.key" :aria-label="field.label" type="number" required step="1" :min="field.min" :max="field.max" :aria-describedby="formID + '-' + field.key" class="mt-1.5 block h-10 w-full rounded-lg border border-slate-200 bg-transparent px-3 text-sm dark:border-slate-600" />
            <small :id="formID + '-' + field.key" class="mt-1 block text-[11px] text-slate-500 dark:text-slate-400">{{ field.hint }}</small>
          </label>
          <div v-if="index === 2" class="col-span-full rounded-lg border border-slate-200 px-3 py-2.5 dark:border-slate-700">
            <p class="text-xs font-medium text-slate-700 dark:text-slate-300">池健康通知由 Bot 功能模块管理</p>
            <p class="mt-1 text-[11px] leading-5 text-slate-500 dark:text-slate-400">Bot 分配与启停不在此表单中修改。<RouterLink to="/telegram/bot" class="text-blue-600 dark:text-blue-400">打开 Bot 配置</RouterLink></p>
          </div>
        </div>
        <p v-if="index === 0" class="mt-3 text-xs leading-5 text-slate-500 dark:text-slate-400">降低上限不会主动结束已有会话；当前占用达到上限时限制新的名额分配。</p>
      </fieldset>
      <p v-if="error" role="alert" class="mt-4 rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
    </form>
    <template #footer>
      <span class="mr-auto text-xs text-slate-500 dark:text-slate-400">{{ dirty ? '有未保存修改' : '没有未保存修改' }}</span>
      <button type="button" :disabled="busy" class="rounded-lg border border-slate-200 px-3 py-2 text-sm disabled:opacity-40 dark:border-slate-600" @click="close">取消</button>
      <button :form="formID" type="submit" :disabled="busy || !dirty" class="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white disabled:opacity-40">{{ busy ? '保存中…' : '保存池设置' }}</button>
    </template>
  </P115SidePanel>
  <P115ActionDialog v-if="discard" title="放弃未保存的修改？" @close="discard = false">
    <p>关闭后将保留上一次保存的设置。</p>
    <template #footer><button class="px-3 py-2 text-sm" @click="discard = false">继续编辑</button><button class="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white" @click="discardChanges">放弃修改</button></template>
  </P115ActionDialog>
</template>
