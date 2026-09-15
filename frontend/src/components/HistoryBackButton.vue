<script setup lang="ts">
import { useRouter } from 'vue-router'

const props = defineProps<{ fallback: string }>()
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
  <button type="button" class="inline-flex items-center gap-2 rounded-lg py-2 transition hover:text-amber-600" @click="goBack"><span aria-hidden="true">←</span> 返回上一页</button>
</template>
