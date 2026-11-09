<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { IconCopy, IconDeviceFloppy, IconServer, IconSettings } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import { embyRequest, type EmbyConfig } from '../types/emby'
import { copyText } from '../utils/clipboard'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const config = ref<EmbyConfig>()
const enabled = ref(false), loading = ref(true), saving = ref(false), copied = ref(false)
const error = ref(''), notice = ref('')
const lifecycle = new AbortController()
const dirty = computed(() => !!config.value && enabled.value !== !!config.value.deepDeleteEnabled)
const webhook = computed(() => config.value?.webhookBaseUrl && config.value.webhookPath ? config.value.webhookBaseUrl.replace(/\/$/, '') + config.value.webhookPath : '')
const webhookDisplay = computed(() => webhook.value ? webhook.value.replace(/([?&]token=)[^&]*/, '$1******') : '')

function reset() {
  enabled.value = !!config.value?.deepDeleteEnabled
  error.value = ''; notice.value = ''
}
async function load() {
  loading.value = true; error.value = ''
  try {
    const data: EmbyConfig = await embyRequest(props.token, 'config', lifecycle.signal)
    if (!lifecycle.signal.aborted) { config.value = data; reset() }
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取删除联动设置失败' }
  finally { loading.value = false }
}
async function save() {
  if (saving.value || !dirty.value || !config.value?.configured) return
  saving.value = true; error.value = ''; notice.value = ''
  try {
    const data: EmbyConfig = await embyRequest(props.token, 'deep-delete-config', lifecycle.signal, { enabled: enabled.value })
    if (!lifecycle.signal.aborted) { config.value = data; reset(); notice.value = '删除联动设置已保存。' }
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存删除联动设置失败' }
  finally { saving.value = false }
}
async function copyWebhook() {
  if (!webhook.value) return
  error.value = ''
  try { await copyText(webhook.value); copied.value = true }
  catch (reason) { error.value = reason instanceof Error ? reason.message : '复制通知地址失败' }
}
onMounted(load)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout class="delete-page" title="删除联动" description="接收神医助手的主动删除通知，将对应的主网盘视频移入 115 回收站。" :parent="{ label: 'Emby', to: '/emby' }" @logout="$emit('logout')">
    <template #actions>
      <RouterLink to="/logs?module=Emby&level=ALL" class="delete-link">查看日志</RouterLink>
      <button type="button" class="delete-button" :disabled="loading || saving || !dirty" @click="reset">撤销修改</button>
      <button type="submit" form="emby-deep-delete-form" class="delete-button delete-primary" :disabled="loading || saving || !dirty || !config?.configured"><IconDeviceFloppy :size="16" aria-hidden="true" />{{ saving ? '正在保存' : '保存设置' }}</button>
    </template>
    <p v-if="error" role="alert" class="delete-error mb-4">{{ error }}<button v-if="!config" type="button" class="ml-3 underline" :disabled="loading" @click="load">重试</button></p>
    <p v-if="loading" role="status" class="delete-help mb-4">正在读取删除联动设置…</p>
    <form v-if="config" id="emby-deep-delete-form" class="min-w-0 rounded-xl border border-line-default bg-surface-card p-4 shadow-sm sm:p-6" :aria-busy="saving" @submit.prevent="save">
      <h2 class="mb-4 text-base font-semibold text-content-primary">主动删除联动</h2>
      <div class="delete-server">
        <IconServer :size="24" class="shrink-0 text-content-secondary" aria-hidden="true" />
        <div class="min-w-0 flex-1"><p class="break-words text-sm font-medium text-content-primary">{{ config.configured ? config.serverName || 'Emby' : '尚未配置服务器' }}</p><p class="delete-help">{{ config.configured ? config.url : '请先配置 Emby 连接' }}</p></div>
        <RouterLink to="/emby/library" class="delete-link"><IconSettings :size="16" aria-hidden="true" />连接设置</RouterLink>
      </div>
      <SettingSwitch v-model="enabled" label="启用删除联动" description="仅处理神医助手的主动删除通知，保存后生效。" :disabled="saving || !config.configured" />
      <div class="mt-6">
        <label for="deep-delete-webhook" class="text-sm font-medium text-content-primary">Webhook 地址</label>
        <div class="mt-2 flex min-w-0 items-center gap-2">
          <input id="deep-delete-webhook" :value="webhookDisplay" readonly spellcheck="false" class="delete-input" placeholder="请先在连接设置中保存通知地址" aria-describedby="deep-delete-webhook-help" />
          <button type="button" class="delete-button shrink-0" :disabled="!webhook" @click="copyWebhook"><IconCopy :size="16" aria-hidden="true" />{{ copied ? '已复制' : '复制地址' }}</button>
        </div>
        <p id="deep-delete-webhook-help" class="delete-help mt-2">复用媒体库同步的通知地址。复制内容包含通知密钥，可直接填入 Emby 的 Webhook 通知。</p>
      </div>
      <ol class="delete-help mt-5 list-decimal space-y-1 pl-4">
        <li>在神医助手中启用深度删除通知，并在 Emby 的 Webhook 通知中选择“媒体深度删除”（deep.delete）。</li>
        <li>只删除与本项目目录 STRM 记录完整匹配的主网盘视频，字幕、图片、目录和其他副本均保留。</li>
        <li>使用本地记录定位并合并同目录删除请求；无法匹配的通知会跳过，结果可在日志中查看。</li>
      </ol>
      <p role="status" class="delete-help mt-6 min-h-[22px]">{{ saving ? '正在保存…' : dirty ? '有未保存的修改，保存后生效' : notice || '当前配置已保存' }}</p>
    </form>
  </ManagementLayout>
</template>

<style scoped>
.delete-server { display: flex; align-items: center; gap: 12px; padding: 12px 16px; margin-bottom: 24px; border: 1px solid rgb(var(--line-default)); border-radius: 8px; background: rgb(var(--surface-page)); }
.delete-help { color: rgb(var(--content-secondary)); font-size: 12px; line-height: 1.8; overflow-wrap: anywhere; }
.delete-link { flex-shrink: 0; display: inline-flex; align-items: center; gap: 6px; min-height: 40px; color: rgb(var(--action-link)); font-size: 13px; }
.delete-button { display: inline-flex; align-items: center; justify-content: center; gap: 8px; min-height: 40px; padding: 8px 16px; border: 1px solid rgb(var(--line-control)); border-radius: 8px; background: rgb(var(--surface-card)); color: rgb(var(--content-primary)); font-size: 14px; font-weight: 500; }
.delete-primary { border-color: rgb(var(--action-primary)); background: rgb(var(--action-primary)); color: rgb(var(--action-on-primary)); }
.delete-button:disabled { opacity: .5; cursor: not-allowed; }
.delete-input { width: 0; flex: 1; min-width: 0; min-height: 40px; padding: 9px 12px; border: 1px solid rgb(var(--line-control)); border-radius: 8px; background: rgb(var(--surface-page)); color: rgb(var(--content-primary)); font: 13px/1.5 ui-monospace, SFMono-Regular, Consolas, monospace; }
.delete-input::placeholder { color: rgb(var(--content-secondary)); }
.delete-button:focus-visible, .delete-link:focus-visible, .delete-input:focus { outline: 2px solid rgb(var(--focus)); outline-offset: 2px; }
.delete-error { padding: 12px 16px; border-radius: 8px; color: rgb(var(--status-danger)); background: rgb(var(--status-danger-surface)); font-size: 13px; line-height: 1.8; overflow-wrap: anywhere; }
@media (hover: hover) { .delete-button:hover:not(:disabled) { background: rgb(var(--surface-hover)); } .delete-primary:hover:not(:disabled) { background: rgb(var(--action-primary-hover)); } .delete-link:hover { text-decoration: underline; } }
@media (max-width: 639px) {
  .delete-page :deep(.management-heading) { grid-template-columns: minmax(0, 1fr); }
  .delete-page :deep(.management-heading > div) { grid-row: 3; flex-wrap: wrap; margin-top: 12px; }
  .delete-page :deep(.management-heading > p) { grid-column: 1; }
  .delete-input { font-size: 16px; }
  .delete-button { min-height: 44px; }
}
</style>
