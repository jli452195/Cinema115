<script lang="ts">
let activeDownloads = 0
const waiting: (() => void)[] = []
async function downloadAvatar(signal: AbortSignal, load: () => Promise<void>) {
  let acquired = false
  await new Promise<void>(resolve => {
    const ready = () => { signal.removeEventListener('abort', cancel); acquired = true; activeDownloads++; resolve() }
    const cancel = () => { const index = waiting.indexOf(ready); if (index >= 0) waiting.splice(index, 1); resolve() }
    if (signal.aborted) resolve()
    else if (activeDownloads < 2) ready()
    else { waiting.push(ready); signal.addEventListener('abort', cancel, { once: true }) }
  })
  try { if (acquired && !signal.aborted) await load() }
  finally { if (acquired) { activeDownloads--; waiting.shift()?.() } }
}
</script>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = withDefaults(defineProps<{ token: string; peerId: string; avatarId?: string; size?: number }>(), { size: 36 })
const element = ref<HTMLElement>(), image = ref('')
let observer: IntersectionObserver | undefined, controller: AbortController | undefined
let visible = false
function clear() { controller?.abort(); if (image.value) URL.revokeObjectURL(image.value); image.value = '' }
async function load() {
  clear()
  if (!visible || !props.avatarId || !props.token) return
  const request = new AbortController(); controller = request
  const path = `/api/admin/telegram-relay/avatars/${encodeURIComponent(props.peerId)}?photo=${encodeURIComponent(props.avatarId)}`
  const token = props.token
  await downloadAvatar(request.signal, async () => {
    try {
      const response = await fetch(path, { headers: { Authorization: `Bearer ${token}` }, cache: 'no-store', signal: AbortSignal.any([request.signal, AbortSignal.timeout(12000)]) })
      if (!response.ok) return
      const blob = await response.blob()
      if (!request.signal.aborted && blob.size <= (128 << 10) && ['image/jpeg', 'image/png', 'image/webp'].includes(blob.type)) image.value = URL.createObjectURL(blob)
    } catch { /* Unavailable avatars keep the existing SVG fallback. */ }
  })
}
watch(() => [props.peerId, props.avatarId, props.token], load)
onMounted(() => {
  if (typeof IntersectionObserver === 'undefined') { visible = true; void load(); return }
  observer = new IntersectionObserver(entries => {
    if (entries.some(entry => entry.isIntersecting)) { visible = true; observer?.disconnect(); void load() }
  })
  if (element.value) observer.observe(element.value)
})
onBeforeUnmount(() => { observer?.disconnect(); clear() })
</script>

<template>
  <span ref="element" class="telegram-avatar" :style="{ width: `${size}px`, height: `${size}px` }" aria-hidden="true">
    <img v-if="image" :src="image" :width="size" :height="size" alt="" decoding="async" @error="clear" />
    <slot v-else />
  </span>
</template>

<style scoped>
.telegram-avatar { display: grid; place-items: center; flex-shrink: 0; overflow: hidden; border-radius: 50%; }
.telegram-avatar img { width: 100%; height: 100%; object-fit: cover; }
</style>
