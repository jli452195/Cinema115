<script setup lang="ts">
import { ref } from 'vue'
defineProps<{ url?: string; requestHeaders?: string; responseHeaders?: string; lookupHeaders?: string; lookup?: string; steps?: string; status?: number; durationMs?: number; method?: string; path?: string }>()
const copied = ref('')
async function copy(value: string, label: string) {
  try {
    if (navigator.clipboard && window.isSecureContext) await navigator.clipboard.writeText(value)
    else {
      const box = document.createElement('textarea'); box.value = value
      box.style.position = 'fixed'; box.style.opacity = '0'; document.body.appendChild(box)
      try { box.select(); if (!document.execCommand('copy')) throw new Error('copy') } finally { box.remove() }
    }
    copied.value = label + '已复制'
  } catch { copied.value = '复制失败，请选中文本手动复制' }
}
</script>
<template>
  <div class="mt-3 min-w-0 space-y-3 rounded-xl bg-slate-50 dark:bg-slate-950 p-3 text-xs">
    <p v-if="method || status" class="break-all text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ method }} {{ path }} · HTTP {{ status }} · {{ durationMs }} ms</p>
    <div v-for="part in [{ label: '完整 CDN 直链', value: url }, { label: '播放器 → 反代请求头', value: requestHeaders }, { label: '上游取链请求头', value: lookupHeaders }, { label: '反代 → 播放器响应头', value: responseHeaders }, { label: '解析步骤', value: steps }]" :key="part.label">
      <template v-if="part.value">
        <div class="flex items-center justify-between gap-2"><strong class="text-slate-600 dark:text-slate-400 dark:text-slate-500">{{ part.label }}</strong><button class="shrink-0 text-blue-600" @click="copy(part.value, part.label)">复制{{ part.label }}</button></div>
        <pre class="mt-1 max-h-80 overflow-auto whitespace-pre-wrap break-all rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-2 leading-5 [overflow-wrap:anywhere]">{{ part.value }}</pre>
      </template>
    </div>
    <p v-if="lookup" class="text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ lookup }}</p>
    <p role="status" class="text-blue-600">{{ copied }}</p>
  </div>
</template>
