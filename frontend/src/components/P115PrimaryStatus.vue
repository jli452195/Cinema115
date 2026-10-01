<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import type { P115Account } from '../types/p115Accounts'
const props = defineProps<{ accounts: P115Account[] }>()
const primary = computed(() => props.accounts.find(account => account.primary))
</script>

<template>
  <p v-if="!primary?.configured" role="alert" class="rounded-lg bg-amber-50 px-4 py-3 text-sm leading-6 text-amber-800 dark:bg-amber-950/30 dark:text-amber-300">
    {{ primary ? '115 主账号尚未登录，请先保存登录凭据。' : '尚未配置 115 主账号。' }}
    <RouterLink to="/helper115" class="ml-2 text-blue-600 dark:text-amber-500 hover:underline">前往配置</RouterLink>
  </p>
</template>
