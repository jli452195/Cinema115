<script setup lang="ts">
import { useId } from 'vue'
import P115SidePanel from './P115SidePanel.vue'

defineProps<{ desktop: boolean; title: string; description: string; busy?: boolean }>()
defineEmits<{ close: [] }>()
const titleID = useId()
</script>

<template>
  <aside v-if="desktop" class="editor-panel" :aria-labelledby="titleID">
    <header><span v-if="$slots.icon" class="editor-panel-icon"><slot name="icon" /></span><div><h2 :id="titleID">{{ title }}</h2><p>{{ description }}</p></div></header>
    <div class="editor-panel-body"><slot /></div>
  </aside>
  <P115SidePanel v-else :title="title" :description="description" :busy="busy" narrow @close="$emit('close')">
    <template v-if="$slots.actions" #actions><slot name="actions" /></template>
    <slot />
    <template v-if="$slots.footer" #footer><slot name="footer" /></template>
  </P115SidePanel>
</template>

<style scoped>
.editor-panel { position: sticky; top: calc(var(--workbench-header-height) + 16px); min-width: 0; max-height: calc(100dvh - var(--workbench-header-height) - 32px); display: flex; flex-direction: column; border-radius: 14px; background: #eef2f8; box-shadow: 0 3px 14px rgb(30 58 100 / .07), inset 0 0 0 1px rgb(148 163 184 / .14); overflow: hidden; }
.editor-panel > header { display: flex; align-items: center; gap: 12px; padding: 17px 20px; flex-shrink: 0; background: #e2eaf6; }
.editor-panel-icon { display: grid; place-items: center; width: 38px; height: 38px; flex-shrink: 0; border-radius: 10px; background: #2563eb; color: #fff; box-shadow: 0 2px 6px rgb(37 99 235 / .16); }
.editor-panel h2 { font-size: 15px; font-weight: 600; color: rgb(var(--color-slate-900)); }
.editor-panel header p { margin-top: 4px; font-size: 12px; line-height: 1.7; color: rgb(var(--color-slate-500)); }
.editor-panel-body { min-height: 0; padding: 18px 20px; overflow-y: auto; overscroll-behavior: contain; scrollbar-gutter: stable; }
.dark .editor-panel { background: rgb(var(--color-slate-900)); box-shadow: 0 3px 14px rgb(0 0 0 / .15), inset 0 0 0 1px rgb(148 163 184 / .15); }
.dark .editor-panel > header { background: #29374d; }.dark .editor-panel-icon { background: #315caa; }
.dark .editor-panel h2 { color: rgb(var(--color-slate-100)); }
.dark .editor-panel header p { color: rgb(var(--color-slate-400)); }
</style>
