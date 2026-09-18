<script setup lang="ts">
import { ref, watch } from 'vue'
import SettingSwitch from './SettingSwitch.vue'
import P115AccountSelect from './P115AccountSelect.vue'
import P115DirectoryDialog from './P115DirectoryDialog.vue'
import { telegramRequest } from '../lib/telegram-api'
import type { TelegramBotConfig, TelegramBotIdentity } from '../types/telegramBots'
import type { P115Account } from '../types/p115Accounts'

const props = defineProps<{ bot: TelegramBotConfig; token: string; accounts: P115Account[] }>()
const emit = defineEmits<{ changed: [] }>()
const name = ref(props.bot.name)
const botToken = ref('')
const allowedChatIDs = ref(props.bot.allowedChatIDs)
const notificationChatID = ref(props.bot.notificationChatID)
const enabled = ref(props.bot.enabled)
const cloudDownloadEnabled = ref(props.bot.cloudDownload?.enabled ?? false)
const cloudAccountId = ref(props.bot.cloudDownload?.accountId ?? '')
const cloudTargetCID = ref(props.bot.cloudDownload?.targetCID ?? '0')
const cloudTargetPath = ref(props.bot.cloudDownload?.targetPath ?? '/')
const directoryDialogOpen = ref(false)
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
    await telegramRequest(props.token, `bots/${props.bot.id}`, { method: 'PUT', body: JSON.stringify({ name: name.value, token: botToken.value.trim(), allowedChatIDs: allowedChatIDs.value, notificationChatID: notificationChatID.value, enabled: enabled.value, cloudDownload: { enabled: cloudDownloadEnabled.value, accountId: cloudAccountId.value, targetCID: cloudTargetCID.value, targetPath: cloudTargetPath.value }, revision: props.bot.revision }) })
    botToken.value = ''; showToken.value = false; tested.value = undefined; emit('changed')
  } catch (e) { error.value = e instanceof Error ? e.message : '保存失败' }
  finally { busy.value = '' }
}

function openDirectory() {
  error.value = ''
  if (!props.accounts.some(account => account.id === cloudAccountId.value && account.configured)) { error.value = '请先选择已登录的 115 账号'; return }
  directoryDialogOpen.value = true
}

watch(cloudAccountId, (value, previous) => {
  if (value === previous) return
  cloudTargetCID.value = '0'
  cloudTargetPath.value = '/'
})

function selectDirectory(value: { id: string; path: string }) {
  cloudTargetCID.value = value.id
  cloudTargetPath.value = value.path
  directoryDialogOpen.value = false
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
        <section class="rounded-xl border border-slate-200 bg-slate-50/70 p-4">
          <SettingSwitch v-model="cloudDownloadEnabled" label="启用磁力转存" description="允许白名单用户在私聊中发送 magnet 或 ED2K，并提交到此 Bot 明确绑定的 115 账号和目录。" />
          <div v-if="cloudDownloadEnabled" class="mt-4 space-y-4 border-t border-slate-200 pt-4">
            <div><P115AccountSelect :id="`bot-cloud-account-${bot.id}`" v-model="cloudAccountId" :accounts="accounts" /><p class="mt-2 text-xs leading-5 text-slate-400">账号被删除或清空凭据后，Bot 会停止提交，不会切换到其他账号。</p></div>
            <div><label class="text-sm font-medium text-slate-700">下载目录</label><div class="mt-2 flex min-w-0 items-center gap-3"><div class="min-w-0 flex-1 truncate rounded-xl border border-slate-200 bg-white p-3 text-sm text-slate-700">{{ cloudTargetPath || '尚未选择' }}</div><button type="button" class="shrink-0 rounded-xl border border-slate-200 bg-white px-4 py-3 text-sm text-blue-600" @click="openDirectory">选择目录</button></div></div>
            <p class="text-xs leading-5 text-slate-500">直接发送 magnet / ED2K 或使用 <code>/cloud &lt;链接&gt;</code> 即可。Bot 会自动移除完全匹配且已经结束的旧任务记录后重新提交，始终保留网盘文件；运行中或状态不明的任务不会删除。Bot 只回复提交结果，不跟踪下载进度。</p>
          </div>
        </section>
      </fieldset>
      <p v-if="bot.lastError" class="rounded-lg bg-amber-50 p-3 text-xs text-amber-800">{{ bot.lastError }}</p>
      <p v-if="notice" class="rounded-lg bg-emerald-50 p-3 text-sm text-emerald-700">{{ notice }}</p>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
      <div class="flex flex-wrap gap-3 border-t border-slate-100 pt-4"><button type="submit" :disabled="!!busy" class="rounded-lg bg-slate-900 px-4 py-2 text-sm text-white disabled:opacity-40">{{ busy === 'save' ? '保存中…' : '保存' }}</button><button type="button" :disabled="!!busy || (!botToken.trim() && !bot.configured)" class="rounded-lg border border-slate-200 px-4 py-2 text-sm text-slate-600 disabled:opacity-40" @click="test">{{ busy === 'test' ? '测试中…' : '测试连接' }}</button><button type="button" :disabled="!!busy || !bot.configured" class="px-2 py-2 text-sm text-amber-700 disabled:opacity-40" @click="clearToken">清空 Token</button><button type="button" :disabled="!!busy" class="ml-auto px-2 py-2 text-sm text-red-600 disabled:opacity-40" @click="remove">删除配置</button></div>
    </form>
    <P115DirectoryDialog v-if="directoryDialogOpen" :token="token" :account-id="cloudAccountId" @close="directoryDialogOpen = false" @select="selectDirectory" />
  </article>
</template>
