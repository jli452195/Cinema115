<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import TelegramBotEditor from '../components/TelegramBotEditor.vue'
import { telegramRequest } from '../lib/telegram-api'
import { p115Request } from '../lib/p115-api'
import type { TelegramBotConfig } from '../types/telegramBots'
import type { P115Account } from '../types/p115Accounts'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ logout: [] }>()
const bots = ref<TelegramBotConfig[]>([])
const accounts = ref<P115Account[]>([])
const loading = ref(true)
const adding = ref(false)
const error = ref('')
const lifecycle = new AbortController()
let timer: number | undefined
let sequence = 0

async function refresh() {
  const current = ++sequence
  try {
    const data = await telegramRequest<{ bots: TelegramBotConfig[] }>(props.token, 'bots', { signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    if (current === sequence && !lifecycle.signal.aborted) { bots.value = data.bots; error.value = '' }
  } catch (e) { if (current === sequence && !lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取 Bot 配置失败' }
  finally { if (current === sequence) loading.value = false }
}

async function loadAccounts() {
  try {
    const data = await p115Request<{ accounts: P115Account[] }>(props.token, 'accounts', { signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    if (!lifecycle.signal.aborted) accounts.value = data.accounts
  } catch (e) {
    if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取 115 账号失败'
  }
}

async function add() {
  if (adding.value) return
  adding.value = true; error.value = ''
  try {
    await telegramRequest(props.token, 'bots', { method: 'POST', body: JSON.stringify({ name: `Bot ${bots.value.length + 1}` }), signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(15000)]) })
    await refresh()
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '添加失败' }
  finally { adding.value = false }
}

onMounted(async () => {
  await Promise.all([refresh(), loadAccounts()])
  const poll = async () => { if (!lifecycle.signal.aborted && !adding.value) await refresh(); if (!lifecycle.signal.aborted) timer = window.setTimeout(poll, 5000) }
  if (!lifecycle.signal.aborted) timer = window.setTimeout(poll, 5000)
})
onBeforeUnmount(() => { lifecycle.abort(); if (timer) window.clearTimeout(timer) })
</script>

<template>
  <ManagementLayout title="Bot 配置" description="管理 Telegram Bot 凭据；具体功能分别选择要使用的 Bot。" :parent="{ label: 'Telegram 配置', to: '/telegram' }" @logout="emit('logout')">
    <div class="mb-6 flex flex-wrap items-center gap-3 border-b border-slate-200 pb-6">
      <button type="button" :disabled="loading || adding || bots.length >= 20" class="rounded-lg bg-blue-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40" @click="add">{{ adding ? '添加中…' : '＋ 添加配置' }}</button>
      <button type="button" :disabled="loading" class="rounded-lg border border-slate-200 px-4 py-2.5 text-sm text-slate-600 disabled:opacity-40" @click="refresh">刷新配置</button>
      <span class="text-xs text-slate-500">{{ bots.length }} 个 Bot 配置</span>
      <p class="w-full text-xs leading-6 text-slate-400">功能保存明确的 Bot 配置编号。修改、停用或删除配置不会让功能改用其他 Bot；同一个 Bot 只有一个消息接收器。</p>
    </div>
    <p v-if="error" role="alert" class="mb-5 rounded-lg bg-red-50 p-4 text-sm text-red-700">{{ error }}</p>
    <p v-if="loading" role="status" class="py-10 text-sm text-slate-500">正在读取 Bot 配置…</p>
    <p v-else-if="!bots.length && !error" class="py-10 text-sm text-slate-500">暂无 Bot，请添加配置，再到具体功能中选择使用。</p>
    <div class="grid items-start gap-5 xl:grid-cols-2"><TelegramBotEditor v-for="bot in bots" :key="`${bot.id}:${bot.revision}`" :bot="bot" :token="token" :accounts="accounts" @changed="refresh" /></div>
  </ManagementLayout>
</template>
