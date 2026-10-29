<script setup lang="ts">
import { useId } from 'vue'
import P115SidePanel from './P115SidePanel.vue'

defineProps<{ desktop: boolean; title: string; contentKey?: string; description?: string; busy?: boolean }>()
defineEmits<{ close: [] }>()
const titleID = useId()
</script>

<template>
  <aside v-if="desktop" class="editor-panel" :aria-labelledby="titleID">
    <header><span v-if="$slots.icon" class="editor-panel-icon"><slot name="icon" /></span><div><h2 :id="titleID">{{ title }}</h2><p v-if="description">{{ description }}</p></div></header>
    <div class="editor-panel-body"><slot /></div>
  </aside>
  <P115SidePanel v-else :title="title" :content-key="contentKey" :description="description || ''" :busy="busy" narrow @close="$emit('close')">
    <template v-if="$slots.actions" #actions><slot name="actions" /></template>
    <slot />
    <template v-if="$slots.footer" #footer><slot name="footer" /></template>
  </P115SidePanel>
</template>

<style scoped>
.editor-panel { position: sticky; top: var(--management-content-top); min-width: 0; max-height: calc(100dvh - var(--management-content-top) - 16px); display: flex; flex-direction: column; border-radius: 12px; background: #f0f2f5; box-shadow: 0 1px 4px rgb(15 23 42 / .04); overflow: hidden; }
.editor-panel > header { display: flex; align-items: center; gap: 9px; padding: 18px 20px 0; flex-shrink: 0; }
.editor-panel > header > div { min-width: 0; }
.editor-panel-icon { display: inline-flex; flex-shrink: 0; color: rgb(var(--color-slate-500)); }
.editor-panel h2 { font-size: 15px; font-weight: 600; overflow-wrap: anywhere; color: rgb(var(--color-slate-900)); }
.editor-panel header p { margin-top: 4px; font-size: 12px; line-height: 1.7; color: rgb(var(--color-slate-500)); }
.editor-panel-body { min-height: 0; padding: 18px 20px; overflow-y: auto; overscroll-behavior: contain; scrollbar-gutter: stable; }
.dark .editor-panel { background: #202833; box-shadow: none; }
.dark .editor-panel h2 { color: rgb(var(--color-slate-100)); }
.dark .editor-panel header p { color: rgb(var(--color-slate-400)); }
</style>
