<script setup lang="ts">
import type { P115Account } from '../types/p115Accounts'

// 各 115 功能共用显式选择器；空值仅是占位，不自动选首项或其他账号。
defineProps<{ modelValue: string; accounts: P115Account[]; id: string }>()
defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <label :for="id" class="text-sm font-medium text-slate-700">使用 115 配置</label>
  <select :id="id" :value="modelValue" required class="mt-2 w-full min-w-0 rounded-xl border border-slate-200 bg-slate-50 p-3 text-sm text-slate-700 outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-100" @change="$emit('update:modelValue', ($event.target as HTMLSelectElement).value)">
    <option value="" disabled>请选择 115 账号配置</option>
    <option v-if="modelValue && !accounts.some(account => account.id === modelValue)" :value="modelValue" disabled>原配置已删除，请重新选择</option>
    <option v-for="account in accounts" :key="account.id" :value="account.id" :disabled="!account.configured">{{ account.name }}{{ account.configured ? '' : '（未登录）' }}</option>
  </select>
</template>
