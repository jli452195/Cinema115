<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'

const props = defineProps<{ active: boolean; count: number; total: number }>()
const emit = defineEmits<{ load: [] }>()
const anchor = ref<HTMLElement>()
const supported = typeof IntersectionObserver !== 'undefined'
let observer: IntersectionObserver | undefined

// 每次追加后重新观察底部位置；收起季或离开页面时停止，避免隐藏列表继续加载。
watch(() => [anchor.value, props.active, props.count, props.total], () => {
  observer?.disconnect()
  observer = undefined
  if (!supported || !anchor.value || !props.active || props.count >= props.total) return
  const current = new IntersectionObserver(entries => {
    if (observer !== current || !props.active || !entries.some(entry => entry.isIntersecting)) return
    current.disconnect()
    observer = undefined
    emit('load')
  }, { rootMargin: '0px 0px 320px 0px' })
  observer = current
  current.observe(anchor.value)
}, { flush: 'post', immediate: true })

onBeforeUnmount(() => { observer?.disconnect(); observer = undefined })
</script>

<template>
  <div ref="anchor" class="py-5 text-center text-xs text-slate-400" data-infinite-load-more>
    <p v-if="count < total">已显示 {{ count }} / {{ total }} 集<span v-if="supported"> · 下滑继续加载</span></p>
    <p v-else>已显示全部 {{ total }} 集</p>
    <button v-if="!supported && count < total" type="button" class="mt-3 rounded-lg border border-slate-200 px-5 py-2 text-blue-600" @click="emit('load')">加载更多</button>
  </div>
</template>
