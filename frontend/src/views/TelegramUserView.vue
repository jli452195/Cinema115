<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import { telegramUserRequest, type TelegramUserStatus } from '../types/telegramUser'
const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const status = ref<TelegramUserStatus>()
const apiId = ref<number>()
const apiHash = ref('')
const bot = ref('')
const phone = ref('')
const code = ref('')
const password = ref('')
const busy = ref('load')
const error = ref('')
const notice = ref('')
const confirmDisconnect = ref(false)
const lifecycle = new AbortController()
async function act(action: string) {
  if (busy.value) return
  busy.value = action; error.value = ''; notice.value = ''
  try {
    const body = action === 'config' ? { apiId: apiId.value, apiHash: apiHash.value.trim(), bot: bot.value.trim() } : action === 'send-code' ? { phone: phone.value.trim() } : action === 'sign-in' ? { code: code.value.trim(), password: password.value } : {}
    const value = await telegramUserRequest<TelegramUserStatus>(props.token, action, lifecycle.signal, body)
    if (lifecycle.signal.aborted) return
    status.value = value; apiId.value = value.apiId; bot.value = value.bot
    if (action === 'config') { apiHash.value = ''; notice.value = 'Telegram API 与资源 Bot 配置已加密保存。' }
    else if (action === 'send-code') notice.value = value.authorized ? '当前账号已登录。' : '验证码已请求，请检查 Telegram 官方客户端或短信。'
    else if (action === 'sign-in') notice.value = value.stage === 'password' ? '账号启用了二步验证，请输入密码完成登录。' : 'Telegram 账号登录成功。'
    else if (action === 'disconnect') { notice.value = '已退出本项目的 Telegram 登录会话。'; confirmDisconnect.value = false }
    else notice.value = value.authorized ? 'Telegram 连接正常，可以在影视详情中搜索资源。' : '尚未登录，请使用手机号完成登录。'
    if (value.authorized) { phone.value = ''; code.value = ''; password.value = '' }
  } catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '操作失败' }
  finally { code.value = ''; password.value = ''; busy.value = '' }
}
async function load() {
  busy.value = 'load'; error.value = ''
  try { status.value = await telegramUserRequest<TelegramUserStatus>(props.token, 'config', lifecycle.signal); apiId.value = status.value.apiId || undefined; bot.value = status.value.bot }
  catch (e) { if (!lifecycle.signal.aborted) error.value = e instanceof Error ? e.message : '读取配置失败' }
  finally { busy.value = '' }
}
onMounted(load)
onBeforeUnmount(() => { lifecycle.abort(); apiHash.value = ''; code.value = ''; password.value = ''; phone.value = '' })
</script>
<template>
  <ManagementLayout title="Telegram 搜索配置" description="使用 Telegram 用户账号向指定资源 Bot 查询影视资源。" :parent="{ label: '资源配置', to: '/resources' }" @logout="$emit('logout')">
    <template #actions><button type="submit" form="telegram-api-form" :disabled="!!busy || !status" class="rounded-lg bg-blue-600 px-4 py-2.5 text-sm text-white disabled:opacity-40">{{ busy === 'config' ? '保存中…' : '保存配置' }}</button></template>
    <p v-if="error" role="alert" class="mb-5 rounded-xl bg-red-50 p-4 text-sm text-red-700">{{ error }}<button v-if="!status" class="ml-3 underline" @click="load">重新加载</button></p>
    <p v-if="notice" role="status" class="mb-5 rounded-xl bg-emerald-50 p-4 text-sm text-emerald-700">{{ notice }}</p>
    <form id="telegram-api-form" class="rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-5 sm:p-7" @submit.prevent="act('config')"><h2 class="font-semibold text-slate-900 dark:text-slate-100">Telegram API 与资源 Bot</h2><fieldset :disabled="!!busy || !status" class="mt-5 grid min-w-0 gap-5 sm:grid-cols-2"><label class="text-sm text-slate-700 dark:text-slate-300">API ID<input v-model.number="apiId" type="number" min="1" step="1" required class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 outline-none focus:border-blue-500"/></label><label class="text-sm text-slate-700 dark:text-slate-300">API Hash<input v-model="apiHash" type="password" autocomplete="new-password" spellcheck="false" :required="!status?.configured" :placeholder="status?.configured ? '已保存，留空保留原值' : '填写自己的 API Hash'" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 outline-none focus:border-blue-500"/></label><label class="text-sm text-slate-700 dark:text-slate-300 sm:col-span-2">资源 Bot 用户名<input v-model="bot" type="text" autocomplete="off" spellcheck="false" required maxlength="33" pattern="@?[A-Za-z][A-Za-z0-9_]{1,28}[Bb][Oo][Tt]" placeholder="填写资源 Bot 用户名" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 outline-none focus:border-blue-500"/><span class="mt-2 block text-xs leading-5 text-slate-500 dark:text-slate-400 dark:text-slate-500">支持填写 <code>@bot_username</code> 或 <code>bot_username</code>；保存后网页搜索和订阅搜索统一使用此 Bot。</span></label></fieldset><p class="mt-4 text-xs leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">API 凭据从 <a href="https://my.telegram.org/apps" target="_blank" rel="noopener noreferrer" class="text-blue-600">my.telegram.org</a> 申请。配置与会话加密保存到服务器，不通过 .env 配置。</p></form>
    <section class="mt-5 rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-5 sm:p-7"><div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold text-slate-900 dark:text-slate-100">Telegram 用户登录</h2><span class="rounded-full px-3 py-1 text-xs" :class="status?.authorized ? 'bg-emerald-50 text-emerald-700' : 'bg-slate-100 dark:bg-slate-800 text-slate-500 dark:text-slate-400 dark:text-slate-500'">{{ status?.authorized ? '已登录' : status?.hasSession ? '会话已保存，待检查' : '未登录' }}</span></div>
      <div v-if="status?.hasSession" class="mt-5 flex flex-wrap items-center gap-3"><span class="text-sm text-slate-600 dark:text-slate-400 dark:text-slate-500">{{ status.name }} {{ status.username ? `@${status.username}` : '' }}</span><button :disabled="!!busy" class="rounded-lg border border-slate-200 dark:border-slate-700 px-4 py-2 text-sm disabled:opacity-40" @click="act('check')">{{ busy === 'check' ? '检查中…' : '检查连接 / 恢复会话' }}</button><button :disabled="!!busy" class="text-xs text-red-500 disabled:opacity-40" @click="confirmDisconnect = true">退出账号</button></div>
      <div v-if="confirmDisconnect" class="mt-4 flex flex-wrap items-center gap-3 rounded-xl bg-red-50 p-4 text-sm text-red-700"><span>退出本项目会话后，需要重新登录才能搜索。</span><button :disabled="!!busy" class="underline" @click="act('disconnect')">确认退出</button><button @click="confirmDisconnect = false">取消</button></div>
      <fieldset v-if="!status?.authorized" :disabled="!!busy || !status?.configured" class="mt-5 min-w-0 space-y-5 disabled:opacity-50">
        <form class="flex flex-wrap items-end gap-3" @submit.prevent="act('send-code')"><label class="min-w-0 flex-1 text-sm text-slate-700 dark:text-slate-300">手机号<input v-model="phone" type="tel" autocomplete="tel" required placeholder="+8613800000000" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 outline-none focus:border-blue-500"/></label><button class="rounded-lg bg-slate-900 px-4 py-3 text-sm text-white">{{ busy === 'send-code' ? '发送中…' : '获取验证码' }}</button></form>
        <form v-if="status?.stage === 'code' || status?.stage === 'password'" class="flex flex-wrap items-end gap-3" @submit.prevent="act('sign-in')"><label v-if="status.stage === 'code'" class="min-w-0 flex-1 text-sm text-slate-700 dark:text-slate-300">登录验证码<input v-model="code" required inputmode="numeric" autocomplete="one-time-code" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 outline-none focus:border-blue-500"/></label><label v-else class="min-w-0 flex-1 text-sm text-slate-700 dark:text-slate-300">二步验证密码<input v-model="password" required type="password" autocomplete="off" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 outline-none focus:border-blue-500"/></label><button class="rounded-lg bg-blue-600 px-4 py-3 text-sm text-white">{{ busy === 'sign-in' ? '登录中…' : '确认登录' }}</button></form>
      </fieldset>
      <p class="mt-5 text-xs leading-6 text-slate-500 dark:text-slate-400 dark:text-slate-500">使用已在所配置的资源 Bot 完成认证的账号。验证码和二步验证密码仅用于当前登录，不保存。服务重启后点击检查连接，或直接在影视详情中搜索即可恢复会话。</p>
    </section>
  </ManagementLayout>
</template>
