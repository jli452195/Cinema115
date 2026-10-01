<script setup lang="ts">
import { watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useDirectoryShare, type DirectoryShareConfig } from '../composables/useDirectoryShare'

const props = defineProps<{ token: string; configurationId?: string; disabled?: boolean; disabledReason?: string }>()
const emit = defineEmits<{ updated: [value: DirectoryShareConfig | undefined] }>()
const { monitor, accountId, loading, error, notice, unavailable, label, stageText, refresh, submit, canEnd, endBatch } = useDirectoryShare(props.token, () => props.configurationId)
watch(monitor, value => emit('updated', value))
</script>

<template>
  <div class="space-y-2 text-sm">
    <div class="flex flex-wrap items-center gap-3">
      <button type="button" :disabled="disabled || !!unavailable" :title="disabled ? disabledReason : unavailable" class="rounded-xl bg-blue-600 px-5 py-2.5 font-medium text-white hover:bg-blue-700 focus-visible:outline-blue-500 disabled:opacity-40" @click="submit">{{ label }}</button>
      <RouterLink to="/115/folder-monitor" class="text-xs text-blue-600 hover:underline">目录设置与详情</RouterLink>
      <button v-if="canEnd" type="button" class="rounded-lg border border-red-200 px-3 py-2 text-xs text-red-600 hover:bg-red-50" @click="endBatch">结束本次任务</button>
      <RouterLink v-if="monitor?.logTaskID" :to="{ path: '/logs', query: { module: 'STRM', level: 'ALL', taskID: monitor.logTaskID } }" class="text-xs text-blue-600 hover:underline">本次日志</RouterLink>
    </div>
    <p v-if="monitor" class="break-all text-xs leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">目标：{{ monitor.sourcePath || monitor.sourceCID }}。处理该目录当前整批内容。</p>
    <p v-if="monitor?.hasBatch && monitor.accountId !== accountId" class="break-all text-xs text-amber-700">本批次属于更换前的账号（{{ monitor.accountId }}），请核对原文件位置。</p>
    <p v-if="disabled && disabledReason" class="text-xs text-amber-700">{{ disabledReason }}</p>
    <p v-else-if="!loading && unavailable" class="text-xs text-amber-700">{{ unavailable }}</p>
    <p v-if="monitor" role="status" class="text-xs text-slate-500 dark:text-slate-400 dark:text-slate-500">{{ stageText }}</p>
    <p v-if="monitor?.hasBatch && monitor.lastError" class="text-xs leading-6 text-amber-700">继续会沿用旧分享和旧生成规则。若文件已删除或需要更换规则，请结束本次任务，再重新整理和分享。</p>
    <p v-if="notice" role="status" class="text-xs text-emerald-700">{{ notice }}</p>
    <p v-if="error" role="alert" class="text-xs text-red-600">{{ error }} <button type="button" class="underline" @click="refresh">刷新状态</button></p>
  </div>
</template>
