<script setup lang="ts">
import { computed, ref } from 'vue'
import { IconUpload } from '@tabler/icons-vue'
import P115ActionDialog from './P115ActionDialog.vue'
import { importMoviePilotClassification } from '../utils/classificationDocument'
import type { ClassificationDiagnostic } from '../types/classification'

const props = defineProps<{ disabled?: boolean }>()
const emit = defineEmits<{ close: []; import: [yaml: string] }>()
const source = ref(''), error = ref(''), diagnostic = ref<ClassificationDiagnostic>(), fileInput = ref<HTMLInputElement>(), reading = ref(false)
const busy = computed(() => props.disabled || reading.value)
function clearError() { error.value = ''; diagnostic.value = undefined }
function failure(reason: unknown) {
  error.value = reason instanceof Error ? reason.message : '无法读取 MP YAML'
  diagnostic.value = (reason as { diagnostic?: ClassificationDiagnostic }).diagnostic
}
async function readFile(event: Event) {
  const input = event.target as HTMLInputElement, file = input.files?.[0]
  input.value = ''
  if (!file || busy.value) return
  clearError()
  if (file.size > 256 * 1024) { error.value = 'MP YAML 不能超过 256 KiB'; return }
  reading.value = true
  try { source.value = await file.text() } catch (reason) { failure(reason) }
  finally { reading.value = false }
}
function load() {
  if (busy.value || !source.value.trim()) return
  clearError()
  try { emit('import', importMoviePilotClassification(source.value)) } catch (reason) { failure(reason) }
}
</script>

<template>
  <P115ActionDialog title="导入 MP YAML" size="wide" :busy="busy" @close="emit('close')">
    <p class="text-xs text-slate-500 dark:text-slate-400">将 MP YAML 转换为本项目格式后载入，保存规则后生效。</p>
    <input ref="fileInput" type="file" accept=".yaml,.yml,text/yaml,application/yaml" aria-label="选择 MP YAML 文件" class="sr-only" :disabled="busy" @change="readFile" />
    <button type="button" class="ci-button" :disabled="busy" @click="fileInput?.click()"><IconUpload :size="15" aria-hidden="true" />{{ reading ? '正在读取…' : '选择 YAML 文件' }}</button>
    <label class="ci-label">或粘贴 MP YAML<textarea v-model="source" aria-label="MP YAML 内容" :disabled="busy" spellcheck="false" placeholder="movie:&#10;  动画电影:&#10;    genre_ids: '16'&#10;  其它电影:&#10;tv:&#10;  未分类:" @input="clearError" /></label>
    <div v-if="error" class="ci-error" role="alert"><p>{{ error }}</p><p v-if="diagnostic">{{ diagnostic.field }}，第 {{ diagnostic.line }} 行，第 {{ diagnostic.column }} 列</p></div>
    <template #footer><button type="button" class="ci-button" :disabled="busy" @click="emit('close')">取消</button><button type="button" class="ci-button ci-primary" :disabled="busy || !source.trim()" @click="load">导入到编辑器</button></template>
  </P115ActionDialog>
</template>

<style scoped>
.ci-button { display: inline-flex; align-items: center; justify-content: center; gap: 6px; min-height: 36px; padding: 7px 12px; border-radius: 8px; background: rgb(148 163 184 / .12); font-size: 12px; color: rgb(var(--color-slate-700)); }.ci-primary { background: #2563eb; color: #fff; }.ci-button:disabled { opacity: .45; cursor: default; }
.ci-label { display: grid; gap: 8px; font-size: 12px; color: rgb(var(--color-slate-600)); }.ci-label textarea { width: 100%; min-height: 280px; max-height: 45dvh; resize: vertical; padding: 12px; border: 1px solid rgb(148 163 184 / .3); border-radius: 8px; background: rgb(var(--color-slate-50)); font: 13px/1.7 ui-monospace, SFMono-Regular, Consolas, monospace; color: rgb(var(--color-slate-800)); tab-size: 2; }.ci-label textarea:focus-visible, .ci-button:focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; }.ci-error { color: #b91c1c; font-size: 12px; overflow-wrap: anywhere; }
.dark .ci-button:not(.ci-primary), .dark .ci-label { color: #cbd5e1; }.dark .ci-label textarea { background: rgb(var(--color-slate-950)); color: #e2e8f0; border-color: #41526a; }.dark .ci-error { color: #fca5a5; }
@media(max-width: 639px) { .ci-button { min-height: 42px; }.ci-label textarea { min-height: 220px; font-size: 14px; } }
</style>
