<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ title: string }>()
// 首字取自片名或姓名，保留完整的 Unicode 字符。
const initial = computed(() => Array.from(props.title.trim().replace(/^[「《【（(\s]+/u, ''))[0] || '影')
</script>

<template>
  <div class="poster-placeholder" aria-hidden="true"><span>{{ initial }}</span></div>
</template>

<style scoped>
.poster-placeholder { display: flex; width: 100%; height: 100%; align-items: center; justify-content: center; background: linear-gradient(145deg, #f1f5f9, #e2e8f0); user-select: none; }
.poster-placeholder span { color: rgb(100 116 139 / .22); font-size: 3.5rem; font-weight: 700; line-height: 1; }
.dark .poster-placeholder { background: linear-gradient(145deg, #1e293b, #0f172a); }
.dark .poster-placeholder span { color: rgb(148 163 184 / .16); }
</style>
