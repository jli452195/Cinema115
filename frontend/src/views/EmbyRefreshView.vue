<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import { IconDeviceFloppy, IconServer, IconSettings } from '@tabler/icons-vue'
import { embyRequest, type EmbyConfig } from '../types/emby'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const config = ref<EmbyConfig>()
const enabled = ref(false), mappings = ref(''), loading = ref(true), saving = ref(false)
const error = ref(''), notice = ref('')
const lifecycle = new AbortController()
const dirty = computed(() => !!config.value && (enabled.value !== config.value.refreshAfterOrganize || mappings.value.trim() !== config.value.refreshPathMappings))

function reset() {
  enabled.value = !!config.value?.refreshAfterOrganize
  mappings.value = config.value?.refreshPathMappings || ''
  error.value = ''; notice.value = ''
}
async function load() {
  loading.value = true; error.value = ''
  try {
    const data: EmbyConfig = await embyRequest(props.token, 'config', lifecycle.signal)
    if (!lifecycle.signal.aborted) { config.value = data; reset() }
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取刷新设置失败' }
  finally { loading.value = false }
}
async function save() {
  if (saving.value || !dirty.value || !config.value?.configured) return
  saving.value = true; error.value = ''; notice.value = ''
  try {
    const data: EmbyConfig = await embyRequest(props.token, 'refresh-config', lifecycle.signal, { refreshAfterOrganize: enabled.value, refreshPathMappings: mappings.value.trim() })
    if (!lifecycle.signal.aborted) { config.value = data; reset(); notice.value = '刷新设置已保存。' }
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存刷新设置失败' }
  finally { saving.value = false }
}
onMounted(load)
onBeforeUnmount(() => lifecycle.abort())
</script>

<template>
  <ManagementLayout class="refresh-page" title="Emby 刷新" description="整理完成后自动通知 Emby，管理文件路径的对应关系。" :parent="{ label: 'Emby', to: '/emby' }" @logout="$emit('logout')">
    <template #actions>
      <button type="button" class="refresh-button" :disabled="loading || saving || !dirty" @click="reset">撤销修改</button>
      <button type="submit" form="emby-refresh-form" class="refresh-button refresh-primary" :disabled="loading || saving || !dirty || !config?.configured"><IconDeviceFloppy :size="16" aria-hidden="true" />{{ saving ? '正在保存' : '保存设置' }}</button>
    </template>
    <p v-if="error" role="alert" class="refresh-error mb-4">{{ error }}<button v-if="!config" type="button" class="ml-3 underline" :disabled="loading" @click="load">重试</button></p>
    <p v-if="loading" role="status" class="refresh-help mb-4">正在读取刷新设置…</p>
    <form v-if="config" id="emby-refresh-form" class="refresh-card min-w-0 rounded-xl border border-line-default bg-surface-card p-4 shadow-sm sm:p-6" :aria-busy="saving" @submit.prevent="save">
      <h2 class="refresh-section-title">自动刷新</h2>
      <div class="refresh-server">
        <IconServer :size="24" class="shrink-0 text-content-secondary" aria-hidden="true" />
        <div class="min-w-0 flex-1"><p class="refresh-server-name">{{ config.configured ? config.serverName || 'Emby' : '尚未配置服务器' }}</p><p class="refresh-help">{{ config.configured ? config.url : '请先配置 Emby 连接' }}</p></div>
        <RouterLink to="/emby/library" class="refresh-link"><IconSettings :size="16" aria-hidden="true" />连接设置</RouterLink>
      </div>
      <fieldset :disabled="saving || !config.configured" class="refresh-fields">
        <legend class="sr-only">Emby 刷新设置</legend>
        <SettingSwitch v-model="enabled" label="整理后通知 Emby" description="视频整理成功后自动合并通知，保存后生效。通知失败不影响整理结果。" />
        <div class="refresh-mapping">
          <label for="refresh-mappings" class="refresh-field-title">路径替换</label><span class="refresh-help ml-2 font-normal">可选</span>
          <textarea id="refresh-mappings" v-model="mappings" :disabled="!enabled" rows="3" maxlength="16384" spellcheck="false" class="refresh-path-input" placeholder="/已整理 => /媒体库" aria-describedby="mapping-help" />
          <p id="mapping-help" class="refresh-help mt-2">每行一条“整理路径 =&gt; Emby 路径”，按顺序匹配目录前缀。两端路径一致可留空。</p>
        </div>
      </fieldset>
      <p role="status" class="refresh-help refresh-feedback">{{ saving ? '正在保存…' : dirty ? '有未保存的修改，保存后生效' : notice || '当前配置已保存' }}</p>
    </form>
  </ManagementLayout>
</template>

<style scoped>
.refresh-section-title { margin-bottom: 16px; color: rgb(var(--content-primary)); font-size: 16px; font-weight: 600; }
.refresh-server { display: flex; align-items: center; gap: 12px; padding: 12px 16px; margin-bottom: 24px; border: 1px solid rgb(var(--line-default)); border-radius: 8px; background: rgb(var(--surface-page)); }
.refresh-server-name { color: rgb(var(--content-primary)); font-size: 14px; font-weight: 500; overflow-wrap: anywhere; }
.refresh-fields { display: grid; min-width: 0; gap: 24px; }
.refresh-field-title { color: rgb(var(--content-primary)); font-size: 14px; font-weight: 500; }
.refresh-path-input { display: block; width: 100%; min-width: 0; margin-top: 8px; padding: 10px 12px; border: 1px solid rgb(var(--line-control)); border-radius: 8px; background: rgb(var(--surface-page)); color: rgb(var(--content-primary)); font: 14px/1.8 ui-monospace, SFMono-Regular, Consolas, monospace; resize: vertical; }
.refresh-path-input::placeholder { color: rgb(var(--content-secondary)); opacity: 1; }
.refresh-path-input:disabled { cursor: not-allowed; opacity: .5; }
.refresh-help { color: rgb(var(--content-secondary)); font-size: 12px; line-height: 1.8; overflow-wrap: anywhere; }
.refresh-feedback { min-height: 22px; margin-top: 24px; }
.refresh-link { flex-shrink: 0; display: inline-flex; align-items: center; gap: 6px; min-height: 40px; color: rgb(var(--action-link)); font-size: 13px; }
.refresh-button { display: inline-flex; align-items: center; justify-content: center; gap: 8px; min-height: 40px; padding: 8px 16px; border: 1px solid rgb(var(--line-control)); border-radius: 8px; background: rgb(var(--surface-card)); color: rgb(var(--content-primary)); font-size: 14px; font-weight: 500; }
.refresh-primary { border-color: rgb(var(--action-primary)); background: rgb(var(--action-primary)); color: rgb(var(--action-on-primary)); }
.refresh-button:disabled { opacity: .5; cursor: not-allowed; }
.refresh-button:focus-visible, .refresh-link:focus-visible, .refresh-path-input:focus { outline: 2px solid rgb(var(--focus)); outline-offset: 2px; }
.refresh-error { padding: 12px 16px; border-radius: 8px; color: rgb(var(--status-danger)); background: rgb(var(--status-danger-surface)); font-size: 13px; line-height: 1.8; overflow-wrap: anywhere; }
@media (hover: hover) { .refresh-button:hover:not(:disabled) { background: rgb(var(--surface-hover)); } .refresh-primary:hover:not(:disabled) { background: rgb(var(--action-primary-hover)); } .refresh-link:hover { text-decoration: underline; } }
@media (max-width: 639px) {
  .refresh-page :deep(.management-heading) { grid-template-columns: minmax(0, 1fr); }
  .refresh-page :deep(.management-heading > div) { grid-row: 3; flex-wrap: wrap; margin-top: 12px; }
  .refresh-page :deep(.management-heading > p) { grid-column: 1; }
  .refresh-path-input { font-size: 16px; }
  .refresh-button { min-height: 44px; }
}
</style>
