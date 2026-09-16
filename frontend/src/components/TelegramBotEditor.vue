<script setup lang="ts">
import { ref } from 'vue'
import SettingSwitch from './SettingSwitch.vue'
import { telegramRequest } from '../lib/telegram-api'
import type { TelegramBotConfig, TelegramBotIdentity } from '../types/telegramBots'

const props = defineProps<{ bot: TelegramBotConfig; token: string }>()
const emit = defineEmits<{ changed: [] }>()
const name = ref(props.bot.name)
const botToken = ref('')
const allowedChatIDs = ref(props.bot.allowedChatIDs)
const notificationChatID = ref(props.bot.notificationChatID)
const enabled = ref(props.bot.enabled)
const showToken = ref(false)
const busy = ref('')
const error = ref('')
const notice = ref('')
const tested = ref<TelegramBotIdentity>()

async function test() {
  if (busy.value) return
  busy.value = 'test'; error.value = ''; notice.value = ''
  try {
    const identity = await telegramRequest<TelegramBotIdentity>(props.token, `bots/${props.bot.id}/test`, { method: 'POST', body: JSON.stringify({ token: botToken.value.trim() }) })
    tested.value = identity
    notice.value = identity.webhookConfigured ? '身份验证成功，但此 Bot 已配置 Webhook，启用前需要先停用 Webhook。' : '身份验证成功。'
  } catch (e) { error.value = e instanceof Error ? e.message : '连接测试失败' }
  finally { busy.value = '' }
}

async function save() {
  if (busy.value) return
  busy.value = 'save'; error.value = ''; notice.value = ''
  try {
    await telegramRequest(props.token, `bots/${props.bot.id}`, { method: 'PUT', body: JSON.stringify({ name: name.value, token: botToken.value.trim(), allowedChatIDs: allowedChatIDs.value, notificationChatID: notificationChatID.value, enabled: enabled.value, revision: props.bot.revision }) })
    botToken.value = ''; showToken.value = false; tested.value = undefined; emit('changed')
  } catch (e) { error.value = e instanceof Error ? e.message : '保存失败' }
  finally { busy.value = '' }
}

async function clearToken() {
  if (busy.value || !window.confirm(`清空“${props.bot.name}”的 Token？引用它的功能不会改用其他 Bot。`)) return
  busy.value = 'clear'; error.value = ''
  try { await telegramRequest(props.token, `bots/${props.bot.id}/token`, { method: 'DELETE', body: JSON.stringify({ revision: props.bot.revision }) }); emit('changed') }
  catch (e) { error.value = e instanceof Error ? e.message : '清空失败' }
  finally { busy.value = '' }
}

async function remove() {
  if (busy.value || !window.confirm(`删除“${props.bot.name}”？引用它的功能会要求重新选择。`)) return
  busy.value = 'delete'; error.value = ''
  try { await telegramRequest(props.token, `bots/${props.bot.id}`, { method: 'DELETE', body: JSON.stringify({ revision: props.bot.revision }) }); emit('changed') }
  catch (e) { error.value = e instanceof Error ? e.message : '删除失败' }
  finally { busy.value = '' }
}
</script>

<template>
  <article class="rounded-lg border border-slate-200 bg-white p-5 shadow-sm">
    <div class="flex flex-wrap items-start justify-between gap-3 border-b border-slate-100 pb-4">
      <div><h2 class="font-semibold text-slate-900">{{ bot.name }}</h2><p class="mt-1 text-xs text-slate-400">{{ bot.identity?.username ? `@${bot.identity.username}` : '尚未验证身份' }}</p></div>
      <span class="rounded px-2 py-1 text-xs" :class="bot.connected ? 'bg-emerald-50 text-emerald-700' : bot.lastError ? 'bg-red-50 text-red-700' : bot.enabled ? 'bg-blue-50 text-blue-700' : 'bg-slate-100 text-slate-500'">{{ bot.connected ? '接收中' : bot.lastError ? '连接异常' : bot.enabled ? (bot.running ? '连接中' : '已启用') : '已停用' }}</span>
    </div>
    <form class="mt-5 space-y-5" @submit.prevent="save">
      <fieldset :disabled="!!busy" class="space-y-5 disabled:opacity-60">
        <label class="block text-sm text-slate-700">配置名称<input v-model="name" required maxlength="50" class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3" /></label>
        <label class="block text-sm text-slate-700">Bot Token<span class="relative mt-2 flex"><input v-model="botToken" :type="showToken ? 'text' : 'password'" autocomplete="new-password" class="w-full rounded-lg border border-slate-200 bg-slate-50 p-3 pr-16" :placeholder="bot.configured ? '已保存，留空保留原值' : '123456789:AA…'" /><button type="button" class="absolute inset-y-0 right-0 px-4 text-xs text-slate-500" @click="showToken = !showToken">{{ showToken ? '隐藏' : '显示' }}</button></span></label>
        <label class="block text-sm text-slate-700">允许的聊天 ID<textarea v-model="allowedChatIDs" rows="3" class="mt-2 w-full resize-y rounded-lg border border-slate-200 bg-slate-50 p-3" placeholder="123456789&#10;-1001234567890" /></label>
        <label class="block text-sm text-slate-700">独立通知聊天 ID<input v-model="notificationChatID" class="mt-2 w-full rounded-lg border border-slate-200 bg-slate-50 p-3" placeholder="单个数字 Chat ID" /></label>
        <SettingSwitch v-model="enabled" label="启用 Bot 配置" description="启用后，绑定此配置的功能可以接收消息和发送通知。" />
      </fieldset>
      <p v-if="bot.lastError" class="rounded-lg bg-amber-50 p-3 text-xs text-amber-800">{{ bot.lastError }}</p>
      <p v-if="notice" class="rounded-lg bg-emerald-50 p-3 text-sm text-emerald-700">{{ notice }}</p>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
      <div class="flex flex-wrap gap-3 border-t border-slate-100 pt-4"><button type="submit" :disabled="!!busy" class="rounded-lg bg-slate-900 px-4 py-2 text-sm text-white disabled:opacity-40">{{ busy === 'save' ? '保存中…' : '保存' }}</button><button type="button" :disabled="!!busy || (!botToken.trim() && !bot.configured)" class="rounded-lg border border-slate-200 px-4 py-2 text-sm text-slate-600 disabled:opacity-40" @click="test">{{ busy === 'test' ? '测试中…' : '测试连接' }}</button><button type="button" :disabled="!!busy || !bot.configured" class="px-2 py-2 text-sm text-amber-700 disabled:opacity-40" @click="clearToken">清空 Token</button><button type="button" :disabled="!!busy" class="ml-auto px-2 py-2 text-sm text-red-600 disabled:opacity-40" @click="remove">删除配置</button></div>
    </form>
  </article>
</template>
