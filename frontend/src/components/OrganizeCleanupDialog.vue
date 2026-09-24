<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import type { OrganizeCleanup } from '../types/manualOrganize'

defineProps<{ plan: OrganizeCleanup; accountName: string; busy: boolean; blocked: boolean; error: string }>()
const emit = defineEmits<{ close: []; confirm: [] }>()
const dialog = ref<HTMLDialogElement>()
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : undefined
const previousOverflow = document.body.style.overflow
onMounted(() => { document.body.style.overflow = 'hidden'; dialog.value?.showModal() })
onBeforeUnmount(() => {
  dialog.value?.close()
  document.body.style.overflow = previousOverflow
  if (previousFocus?.isConnected) previousFocus.focus()
})
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" aria-labelledby="cleanup-title" class="m-auto max-h-[calc(100dvh-2rem)] w-[calc(100%-2rem)] max-w-xl overflow-y-auto rounded-2xl bg-white p-5 text-slate-700 shadow-2xl backdrop:bg-slate-950/50" @cancel.prevent="!busy && emit('close')">
      <h2 id="cleanup-title" class="font-semibold text-slate-900">{{ plan.mode === 'empty' ? '确认清理空目录' : '确认删除所选条目' }}</h2>
      <p class="mt-3 break-all text-sm">账号：{{ accountName }} · {{ plan.parentPath }}</p>
      <p class="mt-2 text-sm text-red-700">将 {{ plan.items.length }} 个{{ plan.mode === 'empty' ? '空文件夹' : '条目' }}移入 115 回收站，可在 115 中恢复。</p>
      <p v-if="plan.mode === 'selected' && plan.items.some(item => item.directory)" class="mt-2 text-sm font-medium text-red-700">所选文件夹及其内部全部文件、子文件夹将一起删除。</p>
      <p v-if="plan.mode === 'empty'" class="mt-2 text-xs leading-5 text-slate-500">只清理空文件夹，从最内层开始。执行时已有内容的目录会保留，当前目录本身也会保留。</p>
      <ul class="my-4 max-h-72 space-y-2 overflow-y-auto rounded-xl bg-slate-50 p-3 text-sm">
        <li v-for="item in plan.items" :key="item.id" class="break-all"><span class="mr-2 text-xs text-slate-500">{{ item.directory ? '目录' : '文件' }}</span>{{ item.path }}</li>
      </ul>
      <p v-if="error" role="alert" class="my-3 text-sm text-red-700">{{ error }}</p>
      <p v-if="blocked" class="my-3 text-sm text-amber-700">请等待整理或清理任务完成后重新预览。</p>
      <p v-if="busy" role="status" class="my-3 text-sm text-slate-500">正在移入回收站并核实结果…</p>
      <div class="flex justify-end gap-3">
        <button type="button" :disabled="busy" class="rounded-lg border border-slate-200 px-4 py-2 text-sm disabled:opacity-40" autofocus @click="emit('close')">取消</button>
        <button type="button" :disabled="busy || blocked || !plan.items.length" class="rounded-lg bg-red-600 px-4 py-2 text-sm text-white hover:bg-red-700 disabled:opacity-40" @click="emit('confirm')">确认移入回收站</button>
      </div>
    </dialog>
  </Teleport>
</template>
