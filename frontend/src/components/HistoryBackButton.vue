<script setup lang="ts">
import { useRouter } from 'vue-router'

const props = defineProps<{ fallback: string; iconOnly?: boolean }>()
const router = useRouter()

function goBack() {
  const previous = window.history.state?.back
  if (typeof previous === 'string' && previous) {
    try {
      const target = new URL(previous, window.location.href)
      const current = new URL(window.location.href)
      if (target.origin === current.origin && target.href !== current.href) {
        router.back()
        return
      }
    } catch { /* 损坏或非网址的历史状态使用固定回退页。 */ }
  }
  void router.push(props.fallback)
}
</script>

<template>
  <button type="button" aria-label="返回上一页" title="返回上一页" class="inline-flex shrink-0 items-center justify-center gap-2 rounded-lg transition focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-amber-500" :class="iconOnly ? 'h-10 w-10' : 'py-2 hover:text-amber-600'" @click="goBack">
    <svg class="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m12 5-7 7 7 7M5 12h14" /></svg>
    <span v-if="!iconOnly">返回上一页</span>
  </button>
</template>
