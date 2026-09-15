<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ openDashboard: []; back: []; home: []; openP115: []; openLogs: []; logout: [] }>()
interface Identity { id: number; first_name: string; username: string; webhookConfigured: boolean }
interface Config { configured: boolean; enabled: boolean; allowedChatIDs: string; notificationChatID: string; identity: Identity; source: string; running: boolean; connected: boolean; lastError: string }
const config = ref<Config>()
const botToken = ref('')
const chatIDs = ref('')
const notificationChatID = ref('')
const enabled = ref(false)
const showToken = ref(false)
const loading = ref(true)
const busy = ref<'test' | 'save' | ''>('')
const error = ref('')
const notice = ref('')
const tested = ref<Identity>()
const pollingError = ref('')
const lifecycle = new AbortController()
let timer: number | undefined
let mounted = true
const status = computed(() => {
  if (!config.value?.configured) return '未配置'
  if (!config.value.enabled) return '已停用'
  if (config.value.lastError) return '连接异常'
  if (config.value.connected) return '接收中'
  if (config.value.running) return '连接中'
  return '等待启动'
})
const identity = computed(() => tested.value ?? config.value?.identity)

async function request(path: string, body?: object) {
  const response = await fetch(`/api/admin/telegram/${path}`, {
    method: body === undefined ? 'GET' : 'POST',
    signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(20000)]),
    headers: { Authorization: `Bearer ${props.token}`, 'Content-Type': 'application/json' }, body: JSON.stringify(body),
  })
  const data = await response.json().catch(() => null)
  if (!response.ok || !data) throw new Error(response.status === 401 ? '管理员登录已过期，请重新登录' : data?.error ?? `请求失败（HTTP ${response.status}）`)
  return data
}

async function refresh(initial = false) {
  const initialize = initial || !config.value
  try {
    const data: Config = await request('config')
    if (!mounted) return
    config.value = data; pollingError.value = ''
    if (initialize) { enabled.value = data.enabled; chatIDs.value = data.allowedChatIDs; notificationChatID.value = data.notificationChatID ?? '' }
  } catch (e) { if (mounted) pollingError.value = e instanceof Error ? e.message : '读取状态失败' }
  finally { if (mounted) loading.value = false }
}

async function test() {
  if (busy.value) return
  busy.value = 'test'; error.value = ''; notice.value = ''; tested.value = undefined
  try {
    const data: Identity = await request('test', { token: botToken.value.trim() })
    if (!mounted) return
    tested.value = data
    notice.value = data.webhookConfigured ? 'Bot 身份验证成功，但已配置 Webhook。启用接收前需先在原服务中停用 Webhook。' : '连接测试成功，已读取 Bot 身份。本次测试没有发送消息。'
  } catch (e) { if (mounted) error.value = e instanceof Error ? e.message : '连接测试失败' }
  finally { busy.value = '' }
}

async function save() {
  if (busy.value) return
  busy.value = 'save'; error.value = ''; notice.value = ''
  try {
    const data: Config = await request('config', { token: botToken.value.trim(), allowedChatIDs: chatIDs.value, notificationChatID: notificationChatID.value, enabled: enabled.value })
    if (!mounted) return
    config.value = data; chatIDs.value = data.allowedChatIDs; notificationChatID.value = data.notificationChatID; botToken.value = ''; showToken.value = false; tested.value = undefined
    notice.value = data.enabled ? '配置已加密保存，接收器正在应用新配置，无需重启后端。' : '配置已加密保存，消息接收已停用。'
    notice.value += data.notificationChatID ? ' STRM 成功及追更申请通知已启用。' : ' 独立通知已关闭。'
  } catch (e) { if (mounted) error.value = e instanceof Error ? e.message : '保存配置失败' }
  finally { busy.value = '' }
}

watch(botToken, () => { tested.value = undefined; notice.value = '' }, { flush: 'sync' })
onMounted(async () => {
  await refresh(true)
  const poll = async () => { if (!mounted) return; if (!busy.value) await refresh(); if (mounted) timer = window.setTimeout(poll, 5000) }
  if (mounted) timer = window.setTimeout(poll, 5000)
})
onBeforeUnmount(() => { mounted = false; lifecycle.abort(); if (timer) window.clearTimeout(timer) })
</script>

<template>
  <ManagementLayout title="Bot 配置" description="接收分享链接，并将 STRM 成功及追更申请通知发送到指定聊天。" :parent="{ label: 'Telegram 配置', to: '/telegram' }" @logout="emit('logout')">
    <template #actions><button type="submit" form="telegram-config-form" :disabled="loading || !!busy || !config" class="shrink-0 rounded-lg bg-blue-600 px-4 py-2.5 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40">{{ busy === 'save' ? '正在保存…' : '保存配置' }}</button></template>
        <section class="rounded-2xl border border-slate-200 bg-white shadow-sm">
          <div class="flex flex-wrap items-center justify-between gap-4 border-b border-slate-100 px-5 py-5 md:px-7"><div class="flex items-center gap-3"><span class="flex h-10 w-10 items-center justify-center rounded-xl bg-blue-50 text-sm font-semibold text-blue-600">01</span><div><h2 class="font-semibold text-slate-900">Bot 配置</h2><p class="mt-1 text-xs text-slate-400">Telegram Bot API</p></div></div><span class="rounded-full px-3 py-1 text-xs font-medium" :class="config?.connected ? 'bg-emerald-50 text-emerald-700' : config?.lastError ? 'bg-red-50 text-red-700' : 'bg-slate-100 text-slate-500'">{{ loading ? '读取中' : status }}</span></div>
          <form id="telegram-config-form" class="space-y-6 p-5 md:p-7" @submit.prevent="save">
            <fieldset :disabled="loading || !!busy || !config" class="min-w-0 space-y-6 disabled:opacity-60">
              <div><label for="tg-token" class="text-sm font-medium text-slate-700">Bot Token</label><div class="relative mt-2"><input id="tg-token" v-model="botToken" :type="showToken ? 'text' : 'password'" autocomplete="new-password" spellcheck="false" class="w-full rounded-xl border border-slate-200 bg-slate-50 py-3 pl-3 pr-16 text-sm outline-none focus:border-blue-500" :placeholder="config?.configured ? '已保存 Token，留空保留；输入新值即可更换' : '123456789:AA…'" /><button type="button" class="absolute inset-y-0 right-0 px-4 text-xs text-slate-500 hover:text-blue-600" :aria-label="showToken ? '隐藏 Token' : '显示 Token'" @click="showToken = !showToken">{{ showToken ? '隐藏' : '显示' }}</button></div><p class="mt-2 text-xs leading-6 text-slate-500">在 Telegram 中通过 <a href="https://t.me/BotFather" target="_blank" rel="noopener noreferrer" class="text-blue-600 hover:underline">@BotFather</a> 创建 Bot，复制其提供的 Token。</p></div>
              <div><label for="tg-chats" class="text-sm font-medium text-slate-700">允许的聊天 ID</label><textarea id="tg-chats" v-model="chatIDs" rows="3" class="mt-2 block w-full resize-y rounded-xl border border-slate-200 bg-slate-50 p-3 text-sm outline-none focus:border-blue-500" placeholder="123456789&#10;-1001234567890"></textarea><p class="mt-2 text-xs leading-6 text-slate-500">填写数字 Chat ID，每行一个或用逗号分隔。私聊通常为正数，群组通常为负数；Bot 只处理并回复这些聊天。</p></div>
              <div><label for="tg-notify-chat" class="text-sm font-medium text-slate-700">独立通知聊天 ID</label><input id="tg-notify-chat" v-model="notificationChatID" type="text" inputmode="text" autocomplete="off" class="mt-2 block w-full rounded-xl border border-slate-200 bg-slate-50 p-3 text-sm outline-none focus:border-blue-500" placeholder="填写单个聊天 ID，留空关闭通知" /><p class="mt-2 text-xs leading-6 text-slate-500">网页、Bot 搜索和后台审核重试生成成功，以及用户发起新的追更申请时，统一通知此聊天。请先与 Bot 发起会话，或将 Bot 加入目标群并允许发言。Telegram 内提交的生成任务继续更新原聊天回执，不重复通知。此配置独立于消息接收开关；留空关闭独立通知。追更通知的网站跳转地址在「追更管理 → 通知设置」中填写。</p></div>
              <label class="flex cursor-pointer items-center justify-between gap-4 rounded-xl border border-slate-200 p-4"><span><span class="block text-sm font-medium text-slate-700">启用消息接收</span><span class="mt-1 block text-xs leading-5 text-slate-500">保存后生效，按「115分享STRM」中保存的生成选项处理链接。</span></span><input v-model="enabled" type="checkbox" role="switch" aria-label="启用消息接收" class="peer sr-only"/><span class="relative h-6 w-11 shrink-0 rounded-full bg-slate-300 transition after:absolute after:left-0.5 after:top-0.5 after:h-5 after:w-5 after:rounded-full after:bg-white after:transition peer-checked:bg-blue-600 peer-checked:after:translate-x-5 peer-focus-visible:ring-2 peer-focus-visible:ring-blue-400"></span></label>
              <div class="flex flex-wrap gap-3 border-t border-slate-100 pt-6"><button type="button" class="rounded-lg border border-slate-200 px-5 py-3 text-sm text-slate-600 hover:bg-slate-50 disabled:opacity-50" :disabled="!botToken.trim() && !config?.configured" @click="test">{{ busy === 'test' ? '正在测试…' : '测试连接' }}</button></div>
            </fieldset>
            <p v-if="notice" role="status" class="rounded-xl bg-emerald-50 p-4 text-sm leading-6 text-emerald-800">{{ notice }}</p>
            <p v-if="error" role="alert" class="rounded-xl bg-red-50 p-4 text-sm leading-6 text-red-700">{{ error }}</p>
          </form>
        </section>
        <section class="mt-5 rounded-2xl border border-slate-200 bg-white p-5 md:p-7"><div class="flex items-center justify-between"><h2 class="text-sm font-semibold text-slate-800">连接信息</h2><button type="button" :disabled="loading || !!busy" class="text-xs text-blue-600 disabled:opacity-40" @click="refresh()">刷新状态</button></div><dl class="mt-5 grid gap-5 text-sm sm:grid-cols-2"><div><dt class="text-xs text-slate-400">机器人</dt><dd class="mt-1.5 text-slate-700">{{ identity?.username ? `@${identity.username}` : '尚未验证' }}<span v-if="identity?.first_name" class="ml-2 text-xs text-slate-400">{{ identity.first_name }}</span></dd></div><div><dt class="text-xs text-slate-400">配置来源</dt><dd class="mt-1.5 text-slate-700">{{ config?.source === 'web' ? '网页配置 · 已加密保存' : '尚未配置' }}</dd></div></dl><p v-if="config?.lastError || pollingError" role="alert" class="mt-4 rounded-lg bg-amber-50 p-3 text-sm leading-6 text-amber-800">{{ pollingError || config?.lastError }}</p><p class="mt-5 text-xs leading-6 text-slate-400">首次私聊请先在 Telegram 打开 Bot 并点击 Start。接收器使用长轮询，同一个 Bot 请只交给一个服务接收消息。</p></section>
  </ManagementLayout>
</template>
