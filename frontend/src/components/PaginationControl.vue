<script setup lang="ts">
import { computed, ref, watch } from 'vue'

const props = defineProps<{ page: number; pages: number; disabled: boolean; label: string }>()
const emit = defineEmits<{ change: [page: number] }>()
const input = ref(String(props.page))
const inputWidth = computed(() => `${Math.max(3, String(props.pages).length + 2)}ch`)
watch(() => props.page, value => { input.value = String(value) })
watch(() => props.disabled, () => { input.value = String(props.page) })
function reset() { input.value = String(props.page) }
function submit() {
  const value = input.value.trim()
  reset()
  if (props.disabled || !/^\d+$/.test(value)) return
  const page = Math.min(props.pages, Number(value))
  if (!Number.isSafeInteger(page) || page < 1 || page === props.page) return
  emit('change', page)
}
</script>

<template>
  <nav :aria-label="label" class="flex items-center justify-center gap-2 py-2 text-sm text-slate-500 dark:text-slate-400">
    <form class="flex items-center gap-2" @submit.prevent="submit">
      <span aria-hidden="true">第</span>
      <input v-model="input" type="text" inputmode="numeric" enterkeyhint="go" maxlength="8" :aria-label="`${label}，当前页码，共 ${pages} 页，回车跳转`" :disabled="disabled" :style="{ width: inputWidth }" class="h-11 min-w-16 rounded-2xl border border-slate-200 bg-white/70 px-2 text-center font-medium tabular-nums text-slate-900 outline-none transition-colors focus:border-slate-400 focus:ring-2 focus:ring-slate-400/15 disabled:opacity-50 dark:border-slate-700 dark:bg-slate-900/70 dark:text-slate-100" @blur="reset" @keydown.esc.prevent="reset" />
      <span class="whitespace-nowrap tabular-nums" aria-hidden="true">/ {{ pages.toLocaleString() }} 页</span>
      <span class="sr-only">共 {{ pages }} 页</span>
    </form>
    <div class="ml-1 flex items-center gap-1">
      <button type="button" aria-label="上一页" :disabled="disabled || page <= 1" class="flex h-11 w-11 items-center justify-center rounded-xl transition-colors hover:bg-slate-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-slate-400 disabled:opacity-30 disabled:hover:bg-transparent dark:hover:bg-slate-800" @click="emit('change', page - 1)"><svg aria-hidden="true" class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m14 7-5 5 5 5"/></svg></button>
      <button type="button" aria-label="下一页" :disabled="disabled || page >= pages" class="flex h-11 w-11 items-center justify-center rounded-xl transition-colors hover:bg-slate-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-slate-400 disabled:opacity-30 disabled:hover:bg-transparent dark:hover:bg-slate-800" @click="emit('change', page + 1)"><svg aria-hidden="true" class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m10 7 5 5-5 5"/></svg></button>
    </div>
  </nav>
</template>
