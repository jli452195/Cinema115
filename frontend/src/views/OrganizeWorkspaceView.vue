<script setup lang="ts">
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'

defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const route = useRoute(), historyLocation = ref('/media/organize/history')
const navigation = ref<HTMLElement>()
let resizeObserver: ResizeObserver | undefined
function revealCurrent() {
  const container = navigation.value, active = container?.querySelector<HTMLElement>('[aria-current="page"]')
  if (!container || !active) return
  const bounds = container.getBoundingClientRect(), item = active.getBoundingClientRect()
  if (item.right > bounds.right) container.scrollLeft += item.right - bounds.right
  else if (item.left < bounds.left) container.scrollLeft += item.left - bounds.left
}
watch(() => route.name, async () => { await nextTick(); revealCurrent() })
onMounted(() => { resizeObserver = new ResizeObserver(revealCurrent); if (navigation.value) resizeObserver.observe(navigation.value) })
onBeforeUnmount(() => resizeObserver?.disconnect())
watch(() => route.fullPath, value => { if (route.name === 'media-organize-history') historyLocation.value = value }, { immediate: true })
const sections = [
  { name: 'media-organize', label: '整理规则' },
  { name: 'media-organize-history', label: '整理记录' },
  { name: 'naming-settings', label: '命名模板' },
  { name: 'classification-settings', label: '分类规则' },
  { name: 'recognition-settings', label: '识别词表' },
]
</script>

<template>
  <ManagementLayout title="自动整理" description="管理目录监控、整理记录、命名模板、分类规则与识别词表。" @logout="$emit('logout')">
    <nav ref="navigation" class="organize-sections" aria-label="整理功能">
      <RouterLink v-for="section in sections" :key="section.name" :to="route.name === section.name ? route.fullPath : section.name === 'media-organize-history' ? historyLocation : { name: section.name }" :aria-current="route.name === section.name ? 'page' : undefined">{{ section.label }}</RouterLink>
    </nav>
    <RouterView v-slot="{ Component }"><component :is="Component" :token="token" /></RouterView>
  </ManagementLayout>
</template>

<style scoped>
.organize-sections { display: flex; align-items: center; gap: 26px; margin-bottom: 12px; overflow-x: auto; scrollbar-width: none; }
.organize-sections a { position: relative; display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0; min-height: 44px; padding: 0 3px 9px; white-space: nowrap; font-size: 14px; color: rgb(var(--color-slate-500)); }
.organize-sections a[aria-current=page] { color: #2563eb; font-weight: 600; }
.organize-sections a[aria-current=page]::after { content: ''; position: absolute; bottom: 0; left: 3px; right: 3px; height: 2px; border-radius: 2px; background: currentColor; }
.organize-sections a:focus-visible { outline: 2px solid #78a2db; outline-offset: -2px; border-radius: 4px; }
.dark .organize-sections a[aria-current=page] { color: #93c5fd; }
@media (hover: hover) { .organize-sections a:hover { color: #2563eb; }.dark .organize-sections a:hover { color: #93c5fd; } }
@media (max-width: 639px) { .organize-sections { gap: 22px; margin-bottom: 10px; }.organize-sections a { font-size: 13px; } }
</style>
