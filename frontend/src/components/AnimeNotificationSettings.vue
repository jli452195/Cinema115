<script setup lang="ts">
import SelectMenu from './SelectMenu.vue'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { IconBell, IconBrandTelegram, IconCheck, IconCode, IconEye, IconPhoto, IconRefresh } from '@tabler/icons-vue'
import P115ActionDialog from './P115ActionDialog.vue'
import { telegramRequest } from '../lib/telegram-api'
import type { TelegramBotConfig } from '../types/telegramBots'
import type { AnimeNotificationConfig, AnimeNotificationKind, AnimeNotificationSettings } from '../types/animeNotifications'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ blocked: [value: boolean]; dirty: [value: boolean] }>()
const config = ref<AnimeNotificationConfig>(), settings = ref<AnimeNotificationSettings>(), bots = ref<TelegramBotConfig[]>([])
const template = ref(''), baseline = ref(''), loading = ref(false), saving = ref(false), readError = ref(''), error = ref(''), notice = ref('')
const previewKind = ref<AnimeNotificationKind>('organized'), previewHTML = ref(''), previewError = ref(''), previewing = ref(false)
const previewPhoto = ref(''), photoFailed = ref(false)
const editor = ref<HTMLTextAreaElement>(), discard = ref(false)
const lifecycle = new AbortController()
let previewController: AbortController | undefined, previewTimer: ReturnType<typeof setTimeout> | undefined, previewGeneration = 0
let leaveDecision: ((value: boolean) => void) | undefined
const signature = () => JSON.stringify([settings.value?.botId, settings.value?.downloadComplete, settings.value?.organized, settings.value?.failed, settings.value?.imageMode, settings.value?.imageUrl, template.value])
const dirty = computed(() => !!baseline.value && signature() !== baseline.value)
const templateLimit = computed(() => config.value?.maxTemplateLength || 2800)
const selectedBot = computed(() => bots.value.find(bot => bot.id === settings.value?.botId))
const botHint = computed(() => {
  if (!settings.value?.botId) return '选择 Bot 后，新发生的订阅事件会推送到该 Bot 的通知聊天。'
  if (!selectedBot.value) return '原 Bot 配置已删除，请重新选择。'
  if (!selectedBot.value.configured) return '此 Bot 尚未填写 Token，请先到 Telegram Bot 页面配置。'
  if (!selectedBot.value.enabled) return '此 Bot 已停用，启用后才能发送通知。'
  if (!selectedBot.value.notificationChatID) return '此 Bot 尚未配置通知聊天 ID。'
  return `通知聊天 ID：${selectedBot.value.notificationChatID}`
})
const events: { key: 'downloadComplete' | 'organized' | 'failed'; title: string; description: string }[] = [
  { key: 'downloadComplete', title: '下载完成', description: '115 离线下载完成时提醒' },
  { key: 'organized', title: '整理完成', description: '命名并移入整理目录后提醒' },
  { key: 'failed', title: '异常提醒', description: '确认失败或持续异常后提醒' },
]
const previewKinds: { value: AnimeNotificationKind; label: string }[] = [{ value: 'organized', label: '整理完成' }, { value: 'downloaded', label: '下载完成' }, { value: 'failed', label: '异常提醒' }]

function checkCapabilities(result: AnimeNotificationConfig) {
  if (!result.settings?.imageMode || typeof result.preview?.photoUrl !== 'string') throw new Error('当前后台仍为旧版本，图片通知尚未生效。请更新并重启后端，再重新读取设置。')
}

// 预览只保留通知模板允许的格式标签，移除属性和浏览器可执行内容。
function safePreview(html: string) {
  const parsed = new DOMParser().parseFromString(html, 'text/html')
  const allowed = new Set(['B', 'I', 'U', 'S', 'CODE', 'PRE', 'BLOCKQUOTE'])
  function clean(parent: Node) {
    for (const node of Array.from(parent.childNodes)) {
      if (node.nodeType === Node.TEXT_NODE) continue
      if (!(node instanceof HTMLElement) || ['SCRIPT', 'STYLE', 'IFRAME', 'OBJECT'].includes(node.tagName)) { node.remove(); continue }
      clean(node)
      if (!allowed.has(node.tagName)) { node.replaceWith(...Array.from(node.childNodes)); continue }
      for (const attribute of Array.from(node.attributes)) node.removeAttribute(attribute.name)
    }
  }
  clean(parsed.body)
  return parsed.body.innerHTML
}

async function load() {
  if (loading.value || saving.value) return
  loading.value = true; readError.value = ''; error.value = ''; notice.value = ''
  try {
    const [result, botResult] = await Promise.all([
      telegramRequest<AnimeNotificationConfig>(props.token, 'anime-notifications', { signal: lifecycle.signal }),
      telegramRequest<{ bots: TelegramBotConfig[] }>(props.token, 'bots', { signal: lifecycle.signal }),
    ])
    if (lifecycle.signal.aborted) return
    checkCapabilities(result)
    config.value = result; settings.value = { ...result.settings, imageMode: result.settings.imageMode || 'cover', imageUrl: result.settings.imageUrl || '' }; bots.value = botResult.bots
    template.value = result.settings.template || result.defaultTemplate; baseline.value = signature()
  } catch (reason) { if (!lifecycle.signal.aborted) readError.value = reason instanceof Error ? reason.message : '通知配置读取失败' }
  finally { loading.value = false }
}

async function save() {
  if (!settings.value || !config.value || saving.value || !dirty.value) return
  saving.value = true; error.value = ''; notice.value = ''
  try {
    const body = { ...settings.value, template: template.value === config.value.defaultTemplate ? '' : template.value }
    const result = await telegramRequest<AnimeNotificationConfig>(props.token, 'anime-notifications', { method: 'PUT', body: JSON.stringify(body), signal: lifecycle.signal })
    if (lifecycle.signal.aborted) return
    checkCapabilities(result)
    config.value = result; settings.value = { ...result.settings, imageMode: result.settings.imageMode || 'cover', imageUrl: result.settings.imageUrl || '' }; template.value = result.settings.template || result.defaultTemplate
    baseline.value = signature(); notice.value = '通知设置已保存'
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '保存失败，输入已保留' }
  finally { saving.value = false }
}

async function readLatestRevision() {
  if (!settings.value || loading.value || saving.value) return
  loading.value = true; error.value = ''; notice.value = ''
  try {
    const result = await telegramRequest<AnimeNotificationConfig>(props.token, 'anime-notifications', { signal: lifecycle.signal })
    if (lifecycle.signal.aborted || !settings.value) return
    checkCapabilities(result)
    config.value = result; settings.value.revision = result.settings.revision
    notice.value = '已读取最新版本，本次输入已保留。请核对后保存。'
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取失败，输入已保留' }
  finally { loading.value = false }
}

async function preview() {
  const current = ++previewGeneration
  previewController?.abort(); previewController = new AbortController()
  previewing.value = true; previewError.value = ''; previewHTML.value = ''; previewPhoto.value = ''; photoFailed.value = false
  try {
    const result = await telegramRequest<{ html: string; photoUrl: string }>(props.token, 'anime-notifications/preview', {
      method: 'POST', body: JSON.stringify({ template: template.value, event: previewKind.value, imageMode: settings.value?.imageMode || 'cover', imageUrl: settings.value?.imageUrl || '' }),
      signal: AbortSignal.any([lifecycle.signal, previewController.signal]),
    })
    if (current === previewGeneration && !lifecycle.signal.aborted) {
      if (typeof result.photoUrl !== 'string') throw new Error('后台尚未提供图片预览，请更新并重启后端。')
      previewHTML.value = safePreview(result.html); previewPhoto.value = result.photoUrl
    }
  } catch (reason) { if (current === previewGeneration && !lifecycle.signal.aborted) previewError.value = reason instanceof Error ? reason.message : '模板无法预览' }
  finally { if (current === previewGeneration) previewing.value = false }
}

async function insertVariable(name: string) {
  const field = editor.value
  const start = field?.selectionStart ?? template.value.length, end = field?.selectionEnd ?? start
  const variable = `{${name}}`
  template.value = template.value.slice(0, start) + variable + template.value.slice(end)
  await nextTick(); field?.focus(); field?.setSelectionRange(start + variable.length, start + variable.length)
}
function decideLeave(value: boolean) { discard.value = false; leaveDecision?.(value); leaveDecision = undefined }
function unload(event: BeforeUnloadEvent) { if (dirty.value || saving.value) event.preventDefault() }
onBeforeRouteLeave(() => {
  if (saving.value) return false
  if (!dirty.value) return true
  discard.value = true
  return new Promise<boolean>(resolve => { leaveDecision?.(false); leaveDecision = resolve })
})
watch([template, previewKind, () => settings.value?.imageMode, () => settings.value?.imageUrl], () => {
  clearTimeout(previewTimer); previewController?.abort(); previewGeneration++; previewHTML.value = ''; previewError.value = ''; previewPhoto.value = ''
  if (config.value) { previewing.value = true; previewTimer = setTimeout(() => { void preview() }, 350) }
})
watch(dirty, value => emit('dirty', value), { immediate: true })
watch(saving, value => emit('blocked', value), { immediate: true })
window.addEventListener('beforeunload', unload)
void load()
onBeforeUnmount(() => { lifecycle.abort(); previewController?.abort(); clearTimeout(previewTimer); window.removeEventListener('beforeunload', unload); leaveDecision?.(false); emit('blocked', false); emit('dirty', false) })
</script>

<template>
  <div class="ant-settings" :aria-busy="loading || saving">
    <header class="ant-heading"><div class="ant-heading-icon"><IconBell :size="20" aria-hidden="true" /></div><div><h2>Telegram 通知</h2><p>把追番进度送到聊天，按自己的喜好调整消息。</p></div></header>
    <p v-if="loading && !config" class="ant-muted" role="status">正在读取通知配置…</p>
    <div v-if="readError" class="ant-feedback" role="alert"><p>{{ readError }}</p><button type="button" class="ae-button" :disabled="loading || saving" @click="load">重新读取通知设置</button></div>
    <form v-if="settings && config" @submit.prevent="save">
      <fieldset :disabled="saving || loading" class="ant-form">
        <div class="ant-destination"><div class="ant-label-row"><label for="anime-notification-bot">推送 Bot</label><RouterLink to="/telegram/bot" class="ant-text-link">管理 Bot</RouterLink></div><SelectMenu id="anime-notification-bot" v-model="settings.botId" label="推送 Bot" class="mt-2 mb-1 w-full" :options="[{ value: '', label: '关闭动漫订阅通知' }, ...(settings.botId && !selectedBot ? [{ value: settings.botId, label: '原配置已删除', disabled: true }] : []), ...bots.map(bot => ({ value: bot.id, label: (bot.name) + (!bot.configured ? '（未配置 Token）' : !bot.enabled ? '（已停用）' : !bot.notificationChatID ? '（缺少通知聊天）' : '') }))]" /><p class="ant-muted">{{ botHint }}</p></div>
        <div class="ant-image-settings"><h3 class="ant-label"><IconPhoto :size="17" aria-hidden="true" />通知图片</h3><label class="sr-only" for="anime-notification-image-mode">通知图片</label><SelectMenu id="anime-notification-image-mode" v-model="settings.imageMode" label="通知图片" class="mt-2 mb-1 w-full" :options="[{ value: 'cover', label: '自动附带动漫封面' }, { value: 'custom', label: '使用自定义图片 URL' }, { value: 'none', label: '仅发送文字' }]" /><template v-if="settings.imageMode === 'custom'"><label class="ant-label" for="anime-notification-image-url">图片 URL</label><input id="anime-notification-image-url" v-model="settings.imageUrl" type="url" maxlength="2048" placeholder="https://example.com/anime.jpg" :required="settings.imageMode === 'custom'" /></template><p class="ant-muted">{{ settings.imageMode === 'cover' ? '自动使用订阅的动漫封面，没有可用封面时发送文字。' : settings.imageMode === 'custom' ? '填写 Telegram 可访问的公开 HTTP 或 HTTPS 图片地址，不能使用本机或内网地址。' : '以纯文字消息发送模板内容。' }} 图片显示在文字上方；说明超过 1024 字符时，图片与完整文字分开发送。</p></div><div><h3 class="ant-label">通知事件</h3><div class="ant-events"><label v-for="event in events" :key="event.key" class="ant-event" :class="{ 'is-selected': settings[event.key] }"><input v-model="settings[event.key]" type="checkbox" /><span><strong>{{ event.title }}</strong><small>{{ event.description }}</small></span><IconCheck v-if="settings[event.key]" :size="15" aria-hidden="true" /></label></div><p class="ant-muted">默认只推送整理完成和异常，避免每集重复提醒。初始化与等待结果不报警；临时错误连续失败至少 3 次且持续 5 分钟才提醒，需人工处理的已确认失败及时提醒。发送前会复核，已恢复的异常不再发送。仅通知保存后新发生的事件。</p></div>
        <div class="ant-template-header"><div><h3 class="ant-label"><IconCode :size="17" aria-hidden="true" />消息模板</h3><p class="ant-muted">支持 Telegram 符号、Emoji 与 HTML 格式。</p></div><button type="button" class="ant-text-button" @click="template = config.defaultTemplate"><IconRefresh :size="14" aria-hidden="true" />恢复默认</button></div>
        <div class="ant-composer"><div class="ant-editor"><label class="sr-only" for="anime-notification-template">通知消息模板</label><textarea id="anime-notification-template" ref="editor" v-model="template" rows="17" :maxlength="templateLimit" spellcheck="false" aria-describedby="anime-template-help" /><p id="anime-template-help" class="ant-muted">模板 {{ template.length }} / {{ templateLimit }} 字符。格式标签：&lt;b&gt;粗体&lt;/b&gt;、&lt;i&gt;斜体&lt;/i&gt;、&lt;u&gt;下划线&lt;/u&gt;、&lt;s&gt;删除线&lt;/s&gt;、&lt;code&gt;代码&lt;/code&gt;、&lt;pre&gt;代码块&lt;/pre&gt;、&lt;blockquote&gt;引用&lt;/blockquote&gt;。</p></div><div class="ant-preview"><div class="ant-preview-header"><span><IconEye :size="15" aria-hidden="true" />消息预览</span><SelectMenu v-model="previewKind" label="预览通知事件" class="min-w-0 max-w-40" :options="previewKinds" /></div><div class="ant-chat"><div class="ant-chat-brand"><IconBrandTelegram :size="17" aria-hidden="true" /><span>Cinema115</span><small>BOT</small></div><template v-if="previewHTML"><img v-if="previewPhoto && !photoFailed" class="ant-photo" :src="previewPhoto" alt="通知图片预览" referrerpolicy="no-referrer" @error="photoFailed = true" /><p v-if="previewPhoto && photoFailed" class="ant-muted ant-photo-failed">图片暂时无法预览，请检查图片地址或网络。</p><div class="ant-bubble" :class="{ 'has-photo': !!previewPhoto && !photoFailed }" v-html="previewHTML"></div></template><p v-else class="ant-muted" :role="previewError ? 'alert' : 'status'">{{ previewError || (previewing ? '正在生成预览…' : '编辑模板后查看效果') }}</p></div><p class="ant-muted">示例数据预览，不会发送 Telegram 消息。</p></div></div>
        <div class="ant-variables"><span class="ant-muted">点击插入变量</span><div><button v-for="variable in config.variables" :key="variable.name" type="button" :title="variable.label" @click="insertVariable(variable.name)"><code>{{ '{' + variable.name + '}' }}</code><span>{{ variable.label }}</span></button></div></div>
      </fieldset>
      <div class="ant-feedback" role="status" aria-live="polite"><p v-if="error" class="ant-error">{{ error }} 输入已保留，可调整后重试。</p><p v-else-if="notice">{{ notice }}</p><button v-if="error" type="button" class="ae-button" :disabled="loading || saving" @click="readLatestRevision">读取最新版本，保留输入</button></div>
      <footer class="ant-footer"><span class="ant-muted">{{ dirty ? '有未保存的修改' : '当前配置已保存' }}</span><button type="submit" class="ae-button ae-primary" :disabled="loading || saving || !dirty || !!previewError || previewing">{{ saving ? '保存中…' : '保存通知设置' }}</button></footer>
    </form>
    <P115ActionDialog v-if="discard" title="放弃未保存的通知修改？" @close="decideLeave(false)"><p>通知设置尚未保存，离开后将丢失本次修改。</p><template #footer><button type="button" class="ae-button" @click="decideLeave(false)">继续编辑</button><button type="button" class="ae-button ae-primary" @click="decideLeave(true)">放弃修改并离开</button></template></P115ActionDialog>
  </div>
</template>

<style scoped>
.ant-settings { color: var(--ae-ink); font-size: 13px; }
.ant-heading { display: flex; gap: 12px; align-items: center; margin-bottom: 26px; }
.ant-heading-icon { display: grid; place-items: center; flex: none; width: 42px; height: 42px; border-radius: 13px; color: var(--ae-blue); background: rgb(59 130 246 / .09); }
.ant-heading h2 { font-size: 17px; font-weight: 600; }
.ant-heading p, .ant-muted { font-size: 11px; color: var(--ae-muted); line-height: 1.8; }
.ant-heading p { margin-top: 5px; }
.ant-form { display: grid; gap: 22px; min-width: 0; }
.ant-label-row, .ant-template-header, .ant-footer { display: flex; justify-content: space-between; align-items: center; gap: 12px; }
.ant-label, .ant-label-row label { display: flex; align-items: center; gap: 7px; font-weight: 600; font-size: 12px; }
.ant-text-link, .ant-text-button { color: var(--ae-blue); font-size: 11px; }
.ant-text-button { display: inline-flex; align-items: center; gap: 5px; min-height: 36px; flex: none; }
.ant-image-settings input { margin: 9px 0 5px; width: 100%; min-height: 42px; padding: 8px 10px; border: 1px solid rgb(148 163 184 / .25); border-radius: 9px; background: var(--ae-surface); color: var(--ae-ink); }
.ant-image-settings input { font-size: 12px; }
.ant-image-settings > .ant-label:not(:first-child) { margin-top: 10px; }
.ant-events { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; margin: 10px 0 7px; }
.ant-event { display: flex; align-items: center; gap: 8px; padding: 12px 10px; border: 1px solid rgb(148 163 184 / .22); border-radius: 10px; cursor: pointer; min-width: 0; }
.ant-event input { accent-color: #2563eb; flex: none; width: 14px; height: 14px; }
.ant-event > span { flex: 1; min-width: 0; }
.ant-event strong { display: block; font-size: 12px; font-weight: 500; }
.ant-event small { display: block; margin-top: 4px; font-size: 10px; line-height: 1.7; color: var(--ae-muted); }
.ant-event > svg { display: none; }
.ant-event.is-selected { background: rgb(59 130 246 / .06); border-color: rgb(59 130 246 / .4); }
.ant-template-header { margin-bottom: -10px; }
.ant-template-header .ant-muted { margin-top: 5px; }
.ant-composer { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 14px; min-width: 0; }
.ant-editor, .ant-preview { min-width: 0; }
.ant-editor textarea { width: 100%; resize: vertical; min-height: 330px; padding: 14px 12px; border: 1px solid rgb(148 163 184 / .25); border-radius: 11px; background: rgb(148 163 184 / .04); color: var(--ae-ink); font: 11px/1.8 ui-monospace, SFMono-Regular, Consolas, monospace; }
.ant-editor > p { margin-top: 7px; overflow-wrap: anywhere; }
.ant-preview-header { display: flex; align-items: center; justify-content: space-between; gap: 5px; margin-bottom: 9px; }
.ant-preview-header > span { display: flex; align-items: center; gap: 5px; font-size: 11px; font-weight: 500; }
.ant-chat { padding: 14px 12px; min-height: 290px; border-radius: 12px; background: rgb(59 130 246 / .05); border: 1px solid rgb(59 130 246 / .1); }
.ant-chat-brand { display: flex; gap: 6px; align-items: center; margin-bottom: 12px; color: var(--ae-blue); font-size: 11px; font-weight: 600; }
.ant-chat-brand small { margin-left: auto; font-size: 8px; letter-spacing: .08em; font-weight: 500; }
.ant-bubble { border: 1px solid rgb(148 163 184 / .14); border-radius: 3px 12px 12px 12px; padding: 14px 12px; background: var(--ae-surface); white-space: pre-wrap; overflow-wrap: anywhere; font-size: 11px; line-height: 1.9; box-shadow: 0 2px 5px rgb(15 23 42 / .03); }
.ant-photo { display: block; width: 100%; max-height: 310px; object-fit: contain; border: 1px solid rgb(148 163 184 / .14); border-bottom: 0; border-radius: 12px 12px 0 0; background: rgb(148 163 184 / .07); }
.ant-bubble.has-photo { border-radius: 0 0 12px 12px; }
.ant-photo-failed { padding: 10px; border-radius: 8px; background: rgb(148 163 184 / .07); margin-bottom: 8px; }
.ant-bubble :deep(blockquote) { display: block; border-left: 3px solid #60a5fa; padding: 5px 9px; border-radius: 4px; background: rgb(59 130 246 / .06); margin: 5px 0; }
.ant-bubble :deep(code), .ant-bubble :deep(pre) { font-family: ui-monospace, Consolas, monospace; font-size: 10px; white-space: pre-wrap; overflow-wrap: anywhere; }
.ant-bubble :deep(code) { background: rgb(148 163 184 / .1); border-radius: 3px; padding: 1px 3px; }
.ant-preview > p { margin-top: 8px; }
.ant-variables > div { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 7px; }
.ant-variables button { display: inline-flex; gap: 6px; align-items: center; min-height: 30px; padding: 4px 8px; border: 1px solid rgb(148 163 184 / .18); border-radius: 6px; font-size: 10px; color: var(--ae-muted); }
.ant-variables code { color: var(--ae-blue); font-size: 10px; }
.ant-feedback { font-size: 12px; line-height: 1.8; color: var(--ae-muted); margin-top: 16px; overflow-wrap: anywhere; }
.ant-feedback:empty { display: none; }
.ant-feedback button { margin-top: 10px; }
.ant-error { color: #dc2626; }
.ant-footer { margin-top: 24px; padding-top: 18px; border-top: 1px solid rgb(148 163 184 / .12); }
.ant-settings :focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; }
.ant-settings :disabled { cursor: default; opacity: .55; }
@media(max-width: 639px) { .ant-composer { grid-template-columns: minmax(0, 1fr); } .ant-events { grid-template-columns: minmax(0, 1fr); } .ant-event { padding: 11px 12px; } .ant-event small { margin-top: 2px; } .ant-event.is-selected > svg { display: block; color: var(--ae-blue); flex: none; } .ant-heading { margin-bottom: 22px; } .ant-heading p { font-size: 10px; } .ant-editor textarea { min-height: 260px; } .ant-footer { flex-wrap: wrap; } }
:global(.dark) .ant-error { color: #fca5a5; }
</style>
