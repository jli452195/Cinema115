<script setup lang="ts">
import { useRouter } from 'vue-router'
import { IconArrowLeft } from '@tabler/icons-vue'

const props = defineProps<{ fallback: string; target?: string; iconOnly?: boolean }>()
const router = useRouter()

function goBack() {
  const previous = window.history.state?.back
  if (props.target) {
    const destination = router.resolve(props.target).fullPath
    if (previous === destination) router.back()
    else void router.replace(destination)
    return
  }
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
    <IconArrowLeft :size="20" stroke="1.8" aria-hidden="true" />
    <span v-if="!iconOnly">返回上一页</span>
  </button>
</template>
