<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import TabbedWorkspace from '../components/TabbedWorkspace.vue'

defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()

const sections = [
  { name: 'media-organize', label: '整理规则' },
  { name: 'media-organize-history', label: '整理记录' },
  { name: 'naming-settings', label: '命名模板' },
  { name: 'classification-settings', label: '分类规则' },
  { name: 'recognition-settings', label: '识别词表' },
]

const route = useRoute()
const historyLocation = ref('/media/organize/history')
const tabs = computed(() => sections.map(section => ({
  ...section,
  to: route.name === section.name ? route.fullPath
    : section.name === 'media-organize-history' ? historyLocation.value
      : { name: section.name },
})))
watch(() => route.fullPath, value => {
  if (route.name === 'media-organize-history') historyLocation.value = value
}, { immediate: true })
</script>

<template>
  <TabbedWorkspace title="自动整理" description="管理目录监控、整理记录、命名模板、分类规则与识别词表。" label="整理功能" :tabs="tabs" @logout="$emit('logout')">
    <template #icon="{ tab }">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">
        <template v-if="tab.name === 'media-organize'">
          <path d="M2 9V5a2 2 0 0 1 2-2h3.9a2 2 0 0 1 1.69.9l.81 1.2a2 2 0 0 0 1.67.9H20a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-1" />
          <path d="M2 13h10m-3 3 3-3-3-3" />
        </template>
        <template v-else-if="tab.name === 'media-organize-history'">
          <path d="M16 22h2a2 2 0 0 0 2-2V7l-5-5H6a2 2 0 0 0-2 2v3" />
          <path d="M14 2v4a2 2 0 0 0 2 2h4" />
          <circle cx="8" cy="16" r="6" />
          <path d="M9.5 17.5 8 16.25V14" />
        </template>
        <template v-else-if="tab.name === 'naming-settings'">
          <path d="M8 3H7a2 2 0 0 0-2 2v5a2 2 0 0 1-2 2 2 2 0 0 1 2 2v5c0 1.1.9 2 2 2h1" />
          <path d="M16 21h1a2 2 0 0 0 2-2v-5c0-1.1.9-2 2-2a2 2 0 0 1-2-2V5a2 2 0 0 0-2-2h-1" />
        </template>
        <template v-else-if="tab.name === 'classification-settings'">
          <path d="m15 5 6.3 6.3a2.4 2.4 0 0 1 0 3.4L17 19" />
          <path d="M9.586 5.586A2 2 0 0 0 8.172 5H3a1 1 0 0 0-1 1v5.172a2 2 0 0 0 .586 1.414L8.29 18.29a2.426 2.426 0 0 0 3.42 0l3.58-3.58a2.426 2.426 0 0 0 0-3.42z" />
          <circle cx="6.5" cy="9.5" r=".5" fill="currentColor" />
        </template>
        <template v-else-if="tab.name === 'recognition-settings'">
          <path d="M3 7V5a2 2 0 0 1 2-2h2m10 0h2a2 2 0 0 1 2 2v2m0 10v2a2 2 0 0 1-2 2h-2M7 21H5a2 2 0 0 1-2-2v-2M7 8h8m-8 4h10m-10 4h6" />
        </template>
      </svg>
    </template>
    <template #default="{ component }"><component :is="component" :token="token" /></template>
  </TabbedWorkspace>
</template>
