<script setup lang="ts">
import type { P115Account } from '../types/p115Accounts'

// 各 115 功能共用显式选择器；空值仅是占位，不自动选首项或其他账号。
defineProps<{ modelValue: string; accounts: P115Account[]; id: string; allowSource?: boolean; sourceLabel?: string; label?: string; compact?: boolean; disableAccounts?: boolean }>()
defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <label :for="id" class="text-sm font-medium text-slate-700">{{ label || '使用 115 配置' }}</label>
  <select :id="id" :value="modelValue" :required="!allowSource" class="mt-2 w-full min-w-0 rounded-xl border border-slate-200 bg-slate-50 text-sm text-slate-700 outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-100" :class="compact ? 'px-3 py-2' : 'p-3'" @change="$emit('update:modelValue', ($event.target as HTMLSelectElement).value)">
    <option value="" :disabled="!allowSource">{{ allowSource ? sourceLabel || '使用 302 源账号' : '请选择 115 账号配置' }}</option>
    <option v-if="modelValue && !accounts.some(account => account.id === modelValue)" :value="modelValue" disabled>原配置已删除，请重新选择</option>
    <option v-for="account in accounts" :key="account.id" :value="account.id" :disabled="disableAccounts || !account.configured">{{ account.name }}{{ account.configured ? '' : '（未登录）' }}</option>
  </select>
</template>
