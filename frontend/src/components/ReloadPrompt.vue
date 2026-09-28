<script setup lang="ts">
import { useRegisterSW } from 'virtual:pwa-register/vue'

const {
  offlineReady,
  needRefresh,
  updateServiceWorker,
} = useRegisterSW()

function close() {
  offlineReady.value = false
  needRefresh.value = false
}
</script>

<template>
  <div v-if="offlineReady || needRefresh" class="fixed bottom-4 right-4 z-50 flex flex-col gap-2 rounded-xl border border-slate-200 bg-white p-4 shadow-xl dark:border-slate-700 dark:bg-slate-800" role="alert">
    <div class="mb-2 text-sm text-slate-800 dark:text-slate-200">
      <span v-if="offlineReady">应用已缓存，支持离线访问</span>
      <span v-else>有新版本更新可用！</span>
    </div>
    <div class="flex gap-2">
      <button v-if="needRefresh" @click="updateServiceWorker()" class="rounded-lg bg-amber-500 px-4 py-2 text-sm font-medium text-white hover:bg-amber-600 focus:outline-none focus:ring-2 focus:ring-amber-500 focus:ring-offset-2 dark:focus:ring-offset-slate-900">
        点击重新加载
      </button>
      <button @click="close" class="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-700 hover:bg-slate-50 focus:outline-none focus:ring-2 focus:ring-slate-500 focus:ring-offset-2 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700 dark:focus:ring-offset-slate-900">
        关闭
      </button>
    </div>
  </div>
</template>
