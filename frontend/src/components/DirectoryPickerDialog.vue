<script setup lang="ts">
import { IconArrowUp, IconChevronRight, IconFolder, IconRefresh, IconServer } from '@tabler/icons-vue'
import FloatingDialog from './AnimeFloatingDialog.vue'

interface DirectoryEntry { id: string; name: string; path?: string; ariaLabel?: string }

const props = withDefaults(defineProps<{
  title: string
  description?: string
  entries: DirectoryEntry[]
  currentPath: string
  canGoUp: boolean
  loading?: boolean
  error?: string
  saving?: boolean
  saveError?: string
  canSelect?: boolean
  confirmLabel?: string
  allowCreate?: boolean
  createLabel?: string
  pathEditable?: boolean
  pathInput?: string
  pathPlaceholder?: string
  pathHint?: string
  emptyText?: string
  loadingText?: string
  notice?: string
}>(), {
  canSelect: true,
  confirmLabel: '选择此目录',
  createLabel: '在此新建 Cinema115 并使用',
  emptyText: '当前目录没有子文件夹，可直接选择此目录。',
  loadingText: '正在读取目录…',
})
const emit = defineEmits<{
  close: []
  open: [id: string]
  up: []
  root: []
  refresh: []
  navigate: [path: string]
  'update:pathInput': [value: string]
  select: []
  create: []
}>()

function navigate() {
  if (props.pathEditable && !props.loading && !props.saving) emit('navigate', props.pathInput || '')
}
</script>

<template>
  <FloatingDialog :title="title" close-label="关闭目录选择器" :close-disabled="saving" class="directory-picker-dialog" @close="emit('close')">
    <template #header>
      <p v-if="description" class="dp-help dp-description">{{ description }}</p>
      <form class="dp-path" @submit.prevent="navigate">
        <label>
          <span>目录路径</span>
          <input
            :value="pathEditable ? pathInput : currentPath || '根目录'"
            :readonly="!pathEditable"
            :disabled="saving"
            :placeholder="pathPlaceholder"
            aria-label="目录路径"
            autocomplete="off"
            spellcheck="false"
            :title="currentPath"
            @input="pathEditable && emit('update:pathInput', ($event.target as HTMLInputElement).value)"
          />
        </label>
        <button v-if="pathEditable" type="submit" class="dp-button" :disabled="loading || saving">前往</button>
      </form>
      <p v-if="pathHint" class="dp-help dp-path-hint">{{ pathHint }}</p>
      <nav class="dp-navigation" aria-label="目录导航">
        <button type="button" class="dp-button" :disabled="loading || saving || !canGoUp" @click="emit('up')"><IconArrowUp :size="16" aria-hidden="true" />返回上级</button>
        <button type="button" class="dp-button" :disabled="loading || saving" @click="emit('root')"><IconServer :size="16" aria-hidden="true" />根目录</button>
        <button type="button" class="dp-button dp-refresh" aria-label="刷新目录" :disabled="loading || saving" @click="emit('refresh')"><IconRefresh :size="17" aria-hidden="true" /></button>
      </nav>
    </template>
    <div class="dp-content" :aria-busy="loading">
      <p v-if="loading" role="status" class="dp-empty">{{ loadingText }}</p>
      <div v-else-if="error" class="dp-empty">
        <p role="alert" class="dp-error">{{ error }}</p>
        <button type="button" class="dp-button" :disabled="saving" @click="emit('refresh')">重新读取</button>
      </div>
      <template v-else>
        <p v-if="!entries.length" class="dp-empty">{{ emptyText }}</p>
        <div v-else class="dp-list">
          <button v-for="entry in entries" :key="entry.id" type="button" class="dp-entry" :title="entry.path || entry.name" :aria-label="entry.ariaLabel || entry.name" :disabled="saving" @click="emit('open', entry.id)">
            <IconFolder :size="21" class="dp-folder" aria-hidden="true" /><span>{{ entry.name }}</span><IconChevronRight :size="17" aria-hidden="true" />
          </button>
        </div>
        <p v-if="notice" role="status" class="dp-help dp-notice">{{ notice }}</p>
      </template>
    </div>
    <template #footer>
      <p class="dp-current" :title="currentPath">{{ currentPath || '请选择目录' }}</p>
      <p v-if="saveError" role="alert" class="dp-error dp-save-error">{{ saveError }}</p>
      <div class="dp-actions">
        <button type="button" class="dp-button" :disabled="saving" @click="emit('close')">取消</button>
        <button v-if="allowCreate" type="button" class="dp-button dp-create" :disabled="saving || loading || !!error" @click="emit('create')">{{ createLabel }}</button>
        <button type="button" class="dp-button dp-primary" :disabled="saving || loading || !!error || !canSelect" @click="emit('select')">{{ saving ? '保存中…' : confirmLabel }}</button>
      </div>
    </template>
  </FloatingDialog>
</template>

<style scoped>
:global(dialog.directory-picker-dialog.anime-floating) {
  width: min(640px, calc(100vw - 32px));
  height: min(660px, calc(100dvh - 48px));
}
:global(dialog.directory-picker-dialog .anime-floating-body) { scrollbar-gutter: stable; }
.dp-help { font-size: 12px; line-height: 1.7; color: var(--floating-surface-muted); overflow-wrap: anywhere; }
.dp-description { margin-bottom: 12px; }
.dp-path { display: flex; align-items: flex-end; gap: 8px; }
.dp-path label { display: grid; flex: 1; min-width: 0; gap: 6px; font-size: 12px; color: var(--floating-surface-muted); }
.dp-path input { width: 100%; min-width: 0; min-height: 36px; padding: 7px 10px; border: 1px solid rgb(148 163 184 / .25); border-radius: 9px; background: rgb(148 163 184 / .06); color: var(--floating-surface-text); font-size: 13px; outline: none; text-overflow: ellipsis; }
.dp-path input:focus-visible { border-color: #3b82f6; outline: 2px solid rgb(59 130 246 / .2); outline-offset: 1px; }
.dp-path input:disabled { opacity: .5; }
.dp-path-hint { margin-top: 6px; }
.dp-button { display: inline-flex; flex: none; align-items: center; justify-content: center; gap: 5px; min-height: 36px; padding: 7px 12px; border-radius: 9px; background: rgb(148 163 184 / .1); color: var(--floating-surface-text); font-size: 12px; line-height: 1.6; }
.dp-button:not(:disabled):hover { background: rgb(148 163 184 / .18); }
.dp-button:disabled { opacity: .4; cursor: default; }
.dp-navigation { display: flex; gap: 8px; margin-top: 12px; }
.dp-navigation .dp-button { white-space: nowrap; }
.dp-refresh { margin-left: auto; padding-inline: 10px; }
.dp-primary { background: #2563eb; color: #fff; }
.dp-primary:not(:disabled):hover { background: #1d4ed8; }
.dp-content { min-width: 0; }
.dp-empty { display: flex; min-height: 160px; flex-direction: column; align-items: center; justify-content: center; gap: 16px; padding: 18px 4px; text-align: center; font-size: 13px; line-height: 1.7; color: var(--floating-surface-muted); }
.dp-error { color: #dc2626; font-size: 13px; line-height: 1.7; overflow-wrap: anywhere; }
.dp-list { display: grid; gap: 6px; }
.dp-entry { display: flex; align-items: center; gap: 12px; min-width: 0; min-height: 48px; padding: 10px 12px; border: 1px solid rgb(148 163 184 / .17); border-radius: 10px; text-align: left; color: var(--floating-surface-text); }
.dp-entry:not(:disabled):hover { background: rgb(59 130 246 / .08); border-color: rgb(59 130 246 / .35); }
.dp-entry:disabled { opacity: .5; cursor: default; }
.dp-button:focus-visible, .dp-entry:focus-visible { outline: 2px solid #2563eb; outline-offset: 2px; }
.dp-entry svg { flex: none; color: var(--floating-surface-muted); }
.dp-entry .dp-folder { color: #3b82f6; }
.dp-entry span { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; }
.dp-notice { padding: 14px 2px 0; }
.dp-current { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-family: ui-monospace, monospace; font-size: 12px; line-height: 1.8; color: var(--floating-surface-muted); }
.dp-save-error { margin-top: 6px; }
.dp-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 8px; margin-top: 10px; }
.dp-actions .dp-button { min-width: 0; overflow-wrap: anywhere; }
:global(.dark .directory-picker-dialog .dp-primary) { background: #3769b5; }
:global(.dark .directory-picker-dialog .dp-primary:not(:disabled):hover) { background: #4479ca; }
:global(.dark .directory-picker-dialog .dp-error) { color: #fca5a5; }
@media (max-width: 639px) {
  :global(dialog.directory-picker-dialog.anime-floating) { width: calc(100vw - 16px); height: min(660px, calc(100dvh - 24px)); }
  .dp-button, .dp-path input { min-height: 44px; }
  .dp-path input { font-size: 16px; }
  .dp-entry { min-height: 52px; }
  .dp-actions .dp-create { order: -1; width: 100%; }
}
</style>
