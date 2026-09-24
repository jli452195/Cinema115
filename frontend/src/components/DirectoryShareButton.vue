<script setup lang="ts">
import { watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useDirectoryShare, type DirectoryShareConfig } from '../composables/useDirectoryShare'

const props = defineProps<{ token: string; configurationId?: string; disabled?: boolean; disabledReason?: string }>()
const emit = defineEmits<{ updated: [value: DirectoryShareConfig | undefined] }>()
const { monitor, accountName, loading, error, notice, unavailable, label, stageText, refresh, submit } = useDirectoryShare(props.token, () => props.configurationId)
watch(monitor, value => emit('updated', value))
</script>

<template>
  <div class="space-y-2 text-sm">
    <div class="flex flex-wrap items-center gap-3">
      <button type="button" :disabled="disabled || !!unavailable" :title="disabled ? disabledReason : unavailable" class="rounded-xl bg-blue-600 px-5 py-2.5 font-medium text-white hover:bg-blue-700 focus-visible:outline-blue-500 disabled:opacity-40" @click="submit">{{ label }}</button>
      <RouterLink to="/115/folder-monitor" class="text-xs text-blue-600 hover:underline">目录设置与详情</RouterLink>
      <RouterLink v-if="monitor?.logTaskID" :to="{ path: '/logs', query: { module: 'STRM', level: 'ALL', taskID: monitor.logTaskID } }" class="text-xs text-blue-600 hover:underline">本次日志</RouterLink>
    </div>
    <p v-if="monitor" class="break-all text-xs leading-6 text-slate-500">目标：{{ monitor.sourcePath || monitor.sourceCID }} · 账号：{{ accountName }}。处理该目录当前整批内容。</p>
    <p v-if="disabled && disabledReason" class="text-xs text-amber-700">{{ disabledReason }}</p>
    <p v-else-if="!loading && unavailable" class="text-xs text-amber-700">{{ unavailable }}</p>
    <p v-if="monitor" role="status" class="text-xs text-slate-500">{{ stageText }}</p>
    <p v-if="notice" role="status" class="text-xs text-emerald-700">{{ notice }}</p>
    <p v-if="error" role="alert" class="text-xs text-red-600">{{ error }} <button type="button" class="underline" @click="refresh">刷新状态</button></p>
  </div>
</template>
