<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
const props = defineProps<{ token: string; userId: string; name: string; imageTag?: string }>()
const element = ref<HTMLElement>(), image = ref('')
let observer: IntersectionObserver | undefined, controller: AbortController | undefined
let visible = false
function clear() { controller?.abort(); if (image.value) URL.revokeObjectURL(image.value); image.value = '' }
async function load() {
  clear()
  if (!visible || !props.imageTag) return
  const request = new AbortController(); controller = request
  try {
    const response = await fetch(`/api/admin/emby/playback-cache/users/${encodeURIComponent(props.userId)}/avatar`, { headers: { Authorization: `Bearer ${props.token}` }, signal: AbortSignal.any([request.signal, AbortSignal.timeout(12000)]) })
    if (!response.ok) return
    const blob = await response.blob()
    if (!request.signal.aborted) image.value = URL.createObjectURL(blob)
  } catch { /* Missing or unavailable avatars use the initial. */ }
}
watch(() => [props.userId, props.imageTag], load)
onMounted(() => {
  observer = new IntersectionObserver(entries => { if (entries.some(entry => entry.isIntersecting)) { visible = true; observer?.disconnect(); void load() } })
  if (element.value) observer.observe(element.value)
})
onBeforeUnmount(() => { observer?.disconnect(); clear() })
</script>
<template>
  <span ref="element" class="flex h-10 w-10 shrink-0 items-center justify-center overflow-hidden rounded-full bg-blue-50 text-base font-semibold text-blue-600 dark:bg-blue-900/30 dark:text-blue-400">
    <img v-if="image" :src="image" :alt="name + ' 的头像'" class="h-full w-full object-cover" @error="clear" />
    <span v-else aria-hidden="true">{{ Array.from(name)[0]?.toUpperCase() || '?' }}</span>
  </span>
</template>
