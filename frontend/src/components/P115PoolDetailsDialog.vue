<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useId } from 'vue'
import type { P115Account } from '../types/p115Accounts'

const props = defineProps<{ account: P115Account; mode: 'users' | 'rename'; busy: boolean; error: string }>()
const emit = defineEmits<{ close: []; save: [name: string] }>()
const dialog = ref<HTMLDialogElement>(), name = ref(props.account.name)
const titleID = useId()
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
const previousOverflow = document.body.style.overflow
function close() { if (!props.busy) emit('close') }
onMounted(() => { document.body.style.overflow = 'hidden'; dialog.value?.showModal() })
onBeforeUnmount(() => {
  dialog.value?.close(); document.body.style.overflow = previousOverflow
  if (previousFocus?.isConnected) previousFocus.focus()
})
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" :aria-labelledby="titleID" class="m-auto max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-md overflow-y-auto rounded-2xl border-0 bg-white p-0 text-slate-700 shadow-2xl backdrop:bg-slate-950/50 dark:bg-slate-900 dark:text-slate-200" @cancel.prevent="close" @click="event => { if (event.target === dialog) close() }">
      <header class="flex items-center gap-4 border-b border-slate-100 px-5 py-4 dark:border-slate-800">
        <div class="min-w-0 flex-1"><h2 :id="titleID" class="font-semibold">{{ mode === 'users' ? '使用此小号的用户' : '修改名称' }}</h2><p class="mt-1 truncate text-xs text-slate-500" :title="account.name">{{ account.name }}{{ mode === 'users' ? ' · ' + (account.userCount || 0) + ' 人' : '' }}</p></div>
        <button type="button" aria-label="关闭账号详情" :disabled="busy" class="rounded-lg px-2 py-1 text-2xl text-slate-500 disabled:opacity-40" @click="close">×</button>
      </header>
      <div v-if="mode === 'users'" class="max-h-[60dvh] overflow-y-auto p-5">
        <ul v-if="account.users?.length" class="divide-y divide-slate-100 text-sm dark:divide-slate-800"><li v-for="(user, index) in account.users" :key="index" class="break-all py-3 first:pt-0 last:pb-0">{{ user }}</li></ul>
        <p v-else class="py-5 text-center text-sm text-slate-500">尚未分配用户</p>
      </div>
      <form v-else class="space-y-4 p-5" @submit.prevent="emit('save', name.trim())">
        <label class="block text-sm">名称<input v-model="name" autofocus required maxlength="50" :disabled="busy" class="mt-2 block w-full rounded-lg border border-slate-200 bg-transparent px-3 py-2 dark:border-slate-700" /></label>
        <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
        <div class="flex justify-end gap-3"><button type="button" :disabled="busy" class="px-4 py-2 text-sm" @click="close">取消</button><button :disabled="busy || !name.trim()" class="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white disabled:opacity-40">{{ busy ? '保存中…' : '保存名称' }}</button></div>
      </form>
    </dialog>
  </Teleport>
</template>
