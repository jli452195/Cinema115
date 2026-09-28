<script setup lang="ts">
import type { TelegramBotConfig } from '../types/telegramBots'

defineProps<{ modelValue: string; bots: TelegramBotConfig[]; id: string; required?: boolean }>()
defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <label :for="id" class="text-sm font-medium text-slate-700 dark:text-slate-300">使用 Telegram Bot</label>
  <select :id="id" :value="modelValue" :required="required" class="mt-2 w-full min-w-0 rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 text-sm text-slate-700 dark:text-slate-300 outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-100" @change="$emit('update:modelValue', ($event.target as HTMLSelectElement).value)">
    <option value="" disabled>请选择 Telegram Bot 配置</option>
    <option v-if="modelValue && !bots.some(bot => bot.id === modelValue)" :value="modelValue" disabled>原配置已删除，请重新选择</option>
    <option v-for="bot in bots" :key="bot.id" :value="bot.id" :disabled="!bot.configured || !bot.enabled || !bot.notificationChatID">{{ bot.name }}{{ !bot.configured ? '（未配置）' : !bot.enabled ? '（已停用）' : !bot.notificationChatID ? '（未配置通知聊天）' : '' }}</option>
  </select>
</template>
