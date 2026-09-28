<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import type { P115Account } from '../types/p115Accounts'
const props = defineProps<{ accounts: P115Account[] }>()
const primary = computed(() => props.accounts.find(account => account.primary))
</script>

<template>
  <p class="text-xs leading-6 text-slate-500 dark:text-slate-400">
    {{ primary?.configured ? `使用主账号 · ${primary.name}` : '请先配置并登录 115 主账号' }}
    <RouterLink to="/helper115" class="ml-2 text-blue-600 dark:text-amber-500 hover:underline">管理主账号</RouterLink>
  </p>
</template>
