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
    <!-- 顶部标题栏：紧凑排布 + 状态徽章 -->
    <header class="ant-heading">
      <div class="ant-heading-main">
        <div class="ant-heading-icon">
          <IconBell :size="20" aria-hidden="true" />
        </div>
        <div>
          <h2>Telegram 通知</h2>
          <p>把追番进度送到聊天，按自己的喜好调整消息。</p>
        </div>
      </div>
      <div v-if="settings && config" class="ant-heading-status">
        <span class="ant-status-pill" :class="{ 'is-dirty': dirty }">
          <span class="ant-status-dot" aria-hidden="true"></span>
          {{ dirty ? '有未保存的修改' : '当前配置已保存' }}
        </span>
      </div>
    </header>

    <p v-if="loading && !config" class="ant-muted ant-loading-state" role="status">
      正在读取通知配置…
    </p>

    <div v-if="readError" class="ant-feedback ant-feedback--banner" role="alert">
      <p>{{ readError }}</p>
      <button type="button" class="ae-button" :disabled="loading || saving" @click="load">
        重新读取通知设置
      </button>
    </div>

    <form v-if="settings && config" @submit.prevent="save">
      <fieldset :disabled="saving || loading" class="ant-form">
        <!-- 第一层：推送目标 & 通知图片 双栏并排卡片 -->
        <div class="ant-top-grid">
          <!-- 左卡：推送 Bot -->
          <div class="ant-card ant-destination">
            <div class="ant-card-header">
              <label class="ant-label" for="anime-notification-bot">
                <IconBrandTelegram :size="16" aria-hidden="true" />
                推送 Bot
              </label>
              <RouterLink to="/telegram/bot" class="ant-text-link">管理 Bot</RouterLink>
            </div>
            <SelectMenu
              id="anime-notification-bot"
              v-model="settings.botId"
              label="推送 Bot"
              class="mt-1.5 mb-1.5 w-full"
              :options="[
                { value: '', label: '关闭动漫订阅通知' },
                ...(settings.botId && !selectedBot
                  ? [{ value: settings.botId, label: '原配置已删除', disabled: true }]
                  : []),
                ...bots.map(bot => ({
                  value: bot.id,
                  label:
                    bot.name +
                    (!bot.configured
                      ? '（未配置 Token）'
                      : !bot.enabled
                        ? '（已停用）'
                        : !bot.notificationChatID
                          ? '（缺少通知聊天）'
                          : ''),
                })),
              ]"
            />
            <p class="ant-muted ant-card-hint">{{ botHint }}</p>
          </div>

          <!-- 右卡：通知图片策略 -->
          <div class="ant-card ant-image-settings">
            <div class="ant-card-header">
              <h3 class="ant-label">
                <IconPhoto :size="16" aria-hidden="true" />
                通知配图模式
              </h3>
              <span class="ant-badge">上限 1024 字同发</span>
            </div>
            <label class="sr-only" for="anime-notification-image-mode">通知图片</label>
            <SelectMenu
              id="anime-notification-image-mode"
              v-model="settings.imageMode"
              label="通知图片"
              class="mt-1.5 mb-1.5 w-full"
              :options="[
                { value: 'cover', label: '自动附带动漫封面' },
                { value: 'custom', label: '使用自定义图片 URL' },
                { value: 'none', label: '仅发送纯文字' },
              ]"
            />
            <div v-if="settings.imageMode === 'custom'" class="ant-custom-url">
              <label class="sr-only" for="anime-notification-image-url">图片 URL</label>
              <input
                id="anime-notification-image-url"
                v-model="settings.imageUrl"
                type="url"
                maxlength="2048"
                placeholder="https://example.com/anime-banner.jpg (需公网可访问)"
                :required="settings.imageMode === 'custom'"
              />
            </div>
            <p class="ant-muted ant-card-hint">
              {{
                settings.imageMode === 'cover'
                  ? '优先使用订阅动漫封面，无封面时自动退回纯文字。'
                  : settings.imageMode === 'custom'
                    ? '请填写 Telegram 服务器可访问的公开 HTTP/HTTPS 图片直链。'
                    : '不附带任何图片，仅以纯文字消息推送模板内容。'
              }}
              文案超 1024 字符时图片与长文将分两条发送。
            </p>
          </div>
        </div>

        <!-- 第二层：通知事件触发器（紧凑三列 + 折叠说明） -->
        <div class="ant-events-section">
          <div class="ant-events-header">
            <h3 class="ant-label">触发通知事件</h3>
            <details class="ant-policy-details">
              <summary>防骚扰与异常复核机制</summary>
              <p>
                默认仅推送「整理完成」和「异常提醒」，避免每集重复打扰。初始化与等待结果不报警；临时错误连续失败至少 3 次且持续 5 分钟才提醒，需人工处理的已确认失败及时提醒。发送前会自动复核，已恢复的异常不再发送。仅对保存后新发生的事件生效。
              </p>
            </details>
          </div>

          <div class="ant-events">
            <label
              v-for="event in events"
              :key="event.key"
              class="ant-event"
              :class="{ 'is-selected': settings[event.key] }"
            >
              <input v-model="settings[event.key]" type="checkbox" />
              <span class="ant-event-body">
                <strong>{{ event.title }}</strong>
                <small>{{ event.description }}</small>
              </span>
              <span class="ant-event-check" aria-hidden="true">
                <IconCheck v-if="settings[event.key]" :size="14" />
              </span>
            </label>
          </div>
        </div>

        <!-- 第三层：消息模板与实时预览双栏工作台 -->
        <div class="ant-workbench">
          <div class="ant-composer">
            <!-- 左侧：模板编辑器（含前置变量工具栏） -->
            <div class="ant-editor-pane">
              <div class="ant-pane-header">
                <div class="ant-pane-title">
                  <h3 class="ant-label">
                    <IconCode :size="16" aria-hidden="true" />
                    消息模板
                  </h3>
                  <span class="ant-muted">支持 Telegram 符号、Emoji 与 HTML 格式。</span>
                </div>
                <button
                  type="button"
                  class="ant-text-button"
                  @click="template = config.defaultTemplate"
                >
                  <IconRefresh :size="14" aria-hidden="true" />
                  恢复默认
                </button>
              </div>

              <div class="ant-editor-box">
                <!-- 变量快捷插入栏前置，紧贴输入框上方 -->
                <div class="ant-variables">
                  <span class="ant-variables-label">插入变量：</span>
                  <div class="ant-variables-list">
                    <button
                      v-for="variable in config.variables"
                      :key="variable.name"
                      type="button"
                      :title="`点击插入 ${variable.label}`"
                      @click="insertVariable(variable.name)"
                    >
                      <code>{{ '{' + variable.name + '}' }}</code>
                      <span>{{ variable.label }}</span>
                    </button>
                  </div>
                </div>

                <label class="sr-only" for="anime-notification-template">通知消息模板</label>
                <textarea
                  id="anime-notification-template"
                  ref="editor"
                  v-model="template"
                  rows="14"
                  :maxlength="templateLimit"
                  spellcheck="false"
                  aria-describedby="anime-template-help"
                />

                <!-- 编辑器底部状态与格式标签速查 -->
                <div id="anime-template-help" class="ant-editor-statusbar">
                  <div class="ant-tags-guide">
                    <span>可用标签：</span>
                    <code>&lt;b&gt;粗体&lt;/b&gt;</code>
                    <code>&lt;i&gt;斜体&lt;/i&gt;</code>
                    <code>&lt;u&gt;下划线&lt;/u&gt;</code>
                    <code>&lt;s&gt;删除线&lt;/s&gt;</code>
                    <code>&lt;code&gt;行内代码&lt;/code&gt;</code>
                    <code>&lt;pre&gt;代码块&lt;/pre&gt;</code>
                    <code>&lt;blockquote&gt;引用&lt;/blockquote&gt;</code>
                  </div>
                  <span
                    class="ant-char-count"
                    :class="{ 'is-near-limit': template.length > templateLimit * 0.9 }"
                  >
                    {{ template.length }} / {{ templateLimit }}
                  </span>
                </div>
              </div>
            </div>

            <!-- 右侧：Telegram 实时仿真预览窗 -->
            <div class="ant-preview-pane">
              <div class="ant-pane-header">
                <span class="ant-label">
                  <IconEye :size="16" aria-hidden="true" />
                  消息预览
                </span>
                <div class="ant-preview-SimControl">
                  <SelectMenu
                    v-model="previewKind"
                    label="预览通知事件"
                    class="min-w-[120px] max-w-40"
                    :options="previewKinds"
                  />
                </div>
              </div>

              <div class="ant-chat">
                <div class="ant-chat-header">
                  <div class="ant-chat-avatar" aria-hidden="true">
                    <IconBrandTelegram :size="16" />
                  </div>
                  <div class="ant-chat-meta">
                    <strong>Cinema115</strong>
                    <span>示例数据预览，不会发送 Telegram 消息。</span>
                  </div>
                  <small class="ant-chat-badge">BOT</small>
                </div>

                <div class="ant-chat-viewport">
                  <template v-if="previewHTML">
                    <div class="ant-message-card">
                      <img
                        v-if="previewPhoto && !photoFailed"
                        class="ant-photo"
                        :src="previewPhoto"
                        alt="通知图片预览"
                        referrerpolicy="no-referrer"
                        @error="photoFailed = true"
                      />
                      <p v-if="previewPhoto && photoFailed" class="ant-muted ant-photo-failed">
                        图片暂时无法预览，请检查图片地址或网络。
                      </p>
                      <div
                        class="ant-bubble"
                        :class="{ 'has-photo': !!previewPhoto && !photoFailed }"
                        v-html="previewHTML"
                      ></div>
                    </div>
                  </template>
                  <div
                    v-else
                    class="ant-chat-empty"
                    :role="previewError ? 'alert' : 'status'"
                  >
                    <p :class="{ 'ant-error': !!previewError }">
                      {{ previewError || (previewing ? '正在渲染预览…' : '编辑模板后查看效果') }}
                    </p>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </fieldset>

      <!-- 底部吸附操作栏：首屏直观、滚动常驻、反馈就近 -->
      <footer class="ant-sticky-bar">
        <!-- 错误与反馈提示区：支持长错误展示与冲突重读按钮 -->
        <div
          v-if="error || notice"
          class="ant-bar-feedback"
          :class="{ 'is-error': !!error, 'is-notice': !!notice }"
          role="status"
          aria-live="polite"
        >
          <div class="ant-bar-feedback-text">
            <p v-if="error" class="ant-error">{{ error }} 输入已保留，可调整后重试。</p>
            <p v-else-if="notice" class="ant-notice">{{ notice }}</p>
          </div>
          <button
            v-if="error"
            type="button"
            class="ae-button ant-bar-retry-btn"
            :disabled="loading || saving"
            @click="readLatestRevision"
          >
            读取最新版本，保留输入
          </button>
        </div>

        <div class="ant-bar-main">
          <div class="ant-bar-status">
            <span class="ant-status-pill" :class="{ 'is-dirty': dirty }">
              <span class="ant-status-dot" aria-hidden="true"></span>
              {{ dirty ? '有未保存的修改' : '当前配置已保存' }}
            </span>
            <span v-if="previewError" class="ant-bar-warning">
              模板预览异常，请修正后再保存
            </span>
          </div>

          <div class="ant-bar-actions">
            <button
              type="submit"
              class="ae-button ae-primary ant-save-btn"
              :disabled="loading || saving || !dirty || !!previewError || previewing"
            >
              {{ saving ? '保存中…' : '保存通知设置' }}
            </button>
          </div>
        </div>
      </footer>
    </form>

    <P115ActionDialog v-if="discard" title="放弃未保存的通知修改？" @close="decideLeave(false)">
      <p>通知设置尚未保存，离开后将丢失本次修改。</p>
      <template #footer>
        <button type="button" class="ae-button" @click="decideLeave(false)">继续编辑</button>
        <button type="button" class="ae-button ae-primary" @click="decideLeave(true)">
          放弃修改并离开
        </button>
      </template>
    </P115ActionDialog>
  </div>
</template>

<style scoped>
/* 顶部标题栏 */
.ant-heading {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
  padding-bottom: 18px;
  margin-bottom: 20px;
  border-bottom: 1px solid rgb(148 163 184 / 0.14);
}

.ant-heading-main {
  display: flex;
  gap: 12px;
  align-items: center;
}

.ant-heading-icon {
  display: grid;
  place-items: center;
  flex: none;
  width: 40px;
  height: 40px;
  border-radius: 11px;
  color: var(--ae-blue);
  background: rgb(59 130 246 / 0.1);
  border: 1px solid rgb(59 130 246 / 0.16);
}

.ant-heading h2 {
  font-size: 16px;
  font-weight: 600;
  line-height: 1.3;
}

.ant-heading p,
.ant-muted {
  font-size: 12px;
  color: var(--ae-muted);
  line-height: 1.65;
}

.ant-heading p {
  margin-top: 3px;
}

.ant-heading-status {
  flex: none;
}

.ant-status-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 11px;
  border-radius: 99px;
  font-size: 11px;
  font-weight: 500;
  color: var(--ae-muted);
  background: rgb(148 163 184 / 0.08);
  border: 1px solid rgb(148 163 184 / 0.16);
}

.ant-status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #10b981;
}

.ant-status-pill.is-dirty {
  color: var(--ae-blue);
  background: rgb(59 130 246 / 0.08);
  border-color: rgb(59 130 246 / 0.25);
}

.ant-status-pill.is-dirty .ant-status-dot {
  background: var(--ae-blue);
}

.ant-loading-state {
  padding: 24px 0;
}

/* 表单主网格：大幅收紧纵向间距 */
.ant-form {
  display: grid;
  gap: 18px;
  min-width: 0;
}

/* 第一层：Bot 与图片设置双列并排 */
.ant-top-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
  align-items: stretch;
}

.ant-card {
  display: flex;
  flex-direction: column;
  min-width: 0;
  padding: 14px 16px;
  border-radius: 13px;
  background: rgb(148 163 184 / 0.04);
  border: 1px solid rgb(148 163 184 / 0.16);
}

.ant-card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  margin-bottom: 4px;
}

.ant-card-hint {
  margin-top: auto;
  padding-top: 6px;
  font-size: 11px;
  line-height: 1.55;
}

.ant-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
  font-size: 12.5px;
  color: var(--ae-ink);
}

.ant-badge {
  font-size: 10px;
  padding: 2px 7px;
  border-radius: 6px;
  color: var(--ae-muted);
  background: rgb(148 163 184 / 0.1);
}

.ant-text-link,
.ant-text-button {
  color: var(--ae-blue);
  font-size: 11.5px;
  font-weight: 500;
}

.ant-text-button {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-height: 32px;
  padding: 4px 10px;
  border-radius: 8px;
  background: rgb(59 130 246 / 0.07);
  transition: background-color 0.15s;
}

@media (hover: hover) {
  .ant-text-button:hover:not(:disabled) {
    background: rgb(59 130 246 / 0.14);
  }
}

.ant-custom-url {
  margin: 6px 0 2px;
}

.ant-image-settings input {
  width: 100%;
  min-height: 38px;
  padding: 7px 11px;
  font-size: 12px;
  border: 1px solid rgb(148 163 184 / 0.26);
  border-radius: 9px;
  background: var(--ae-surface);
  color: var(--ae-ink);
}

/* 第二层：通知事件与折叠说明 */
.ant-events-section {
  padding: 14px 16px;
  border-radius: 13px;
  background: rgb(148 163 184 / 0.04);
  border: 1px solid rgb(148 163 184 / 0.16);
}

.ant-events-header {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: baseline;
  gap: 8px 16px;
  margin-bottom: 10px;
}

.ant-policy-details {
  font-size: 11.5px;
  color: var(--ae-muted);
}

.ant-policy-details summary {
  cursor: pointer;
  color: var(--ae-blue);
  font-weight: 500;
  user-select: none;
  list-style-position: inside;
}

.ant-policy-details p {
  margin-top: 8px;
  padding: 10px 12px;
  border-radius: 9px;
  background: var(--ae-surface);
  border: 1px solid rgb(148 163 184 / 0.16);
  font-size: 11px;
  line-height: 1.7;
  color: var(--ae-muted);
}

.ant-events {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
}

.ant-event {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid rgb(148 163 184 / 0.2);
  border-radius: 10px;
  background: var(--ae-surface);
  cursor: pointer;
  min-width: 0;
  transition: border-color 0.15s, background-color 0.15s;
}

.ant-event input {
  accent-color: #2563eb;
  flex: none;
  width: 15px;
  height: 15px;
  margin: 0;
}

.ant-event-body {
  flex: 1;
  min-width: 0;
}

.ant-event strong {
  display: block;
  font-size: 12.5px;
  font-weight: 600;
  line-height: 1.3;
}

.ant-event small {
  display: block;
  margin-top: 2px;
  font-size: 11px;
  line-height: 1.45;
  color: var(--ae-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.ant-event-check {
  display: grid;
  place-items: center;
  flex: none;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  color: var(--ae-blue);
  opacity: 0;
  transition: opacity 0.15s;
}

.ant-event.is-selected {
  background: rgb(59 130 246 / 0.06);
  border-color: rgb(59 130 246 / 0.42);
}

.ant-event.is-selected .ant-event-check {
  opacity: 1;
  background: rgb(59 130 246 / 0.12);
}

/* 第三层：模板编辑 + 实时预览工作台 */
.ant-workbench {
  min-width: 0;
}

.ant-composer {
  display: grid;
  grid-template-columns: minmax(0, 1.35fr) minmax(360px, 1fr);
  gap: 20px;
  align-items: start;
  min-width: 0;
}

.ant-editor-pane,
.ant-preview-pane {
  min-width: 0;
}

.ant-preview-pane {
  position: sticky;
  top: calc(var(--workbench-header-height, 64px) + 20px);
}

.ant-pane-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  min-height: 36px;
  margin-bottom: 10px;
}

.ant-pane-title {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 6px 12px;
}

/* IDE 风格一体化编辑器容器 */
.ant-editor-box {
  display: flex;
  flex-direction: column;
  border-radius: 13px;
  border: 1px solid rgb(148 163 184 / 0.26);
  background: var(--ae-surface);
  overflow: hidden;
  transition: border-color 0.15s, box-shadow 0.15s;
}

.ant-editor-box:focus-within {
  border-color: #60a5fa;
  box-shadow: 0 0 0 3px rgb(59 130 246 / 0.12);
}

/* 前置变量工具栏 */

@media (hover: hover) {
  .ant-variables button:hover:not(:disabled) {
    border-color: rgb(59 130 246 / 0.45);
    background: rgb(59 130 246 / 0.06);
  }
}

.ant-variables button:active:not(:disabled) {
  transform: translateY(1px);
}

.ant-variables code {
  color: var(--ae-blue);
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 11px;
  font-weight: 600;
}

.ant-variables button span {
  color: var(--ae-muted);
  font-size: 10.5px;
}

.ant-editor-box textarea {
  width: 100%;
  resize: vertical;
  min-height: 310px;
  padding: 14px 16px;
  border: 0;
  background: transparent;
  color: var(--ae-ink);
  font: 12.5px/1.75 ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

.ant-editor-box textarea:focus-visible {
  outline: none;
}

/* 编辑器底部状态栏与标签提示 */
.ant-editor-statusbar {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: center;
  gap: 8px 14px;
  padding: 8px 12px;
  background: rgb(148 163 184 / 0.05);
  border-top: 1px solid rgb(148 163 184 / 0.14);
  font-size: 11px;
  color: var(--ae-muted);
}

.ant-tags-guide {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 6px;
}

.ant-tags-guide code {
  padding: 1px 5px;
  border-radius: 4px;
  background: rgb(148 163 184 / 0.1);
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 10.5px;
  color: var(--ae-ink);
}

.ant-char-count {
  margin-left: auto;
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

.ant-char-count.is-near-limit {
  color: #d97706;
}

/* 右侧：Telegram 聊天仿真窗口 */
.ant-chat {
  display: flex;
  flex-direction: column;
  border-radius: 13px;
  overflow: hidden;
  border: 1px solid rgb(59 130 246 / 0.18);
  background:
    radial-gradient(circle at 100% 0%, rgb(59 130 246 / 0.08), transparent 55%),
    rgb(15 23 42 / 0.03);
}

:global(.dark) .ant-chat {
  background:
    radial-gradient(circle at 100% 0%, rgb(59 130 246 / 0.12), transparent 60%),
    rgb(15 23 42 / 0.45);
  border-color: rgb(148 163 184 / 0.16);
}

.ant-chat-header {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 14px;
  background: rgb(59 130 246 / 0.06);
  border-bottom: 1px solid rgb(59 130 246 / 0.1);
}

.ant-chat-avatar {
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border-radius: 50%;
  background: var(--ae-blue);
  color: #fff;
  flex: none;
}

:global(.dark) .ant-chat-avatar {
  color: #0f172a;
}

.ant-chat-meta {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.ant-chat-meta strong {
  font-size: 12px;
  font-weight: 600;
  color: var(--ae-ink);
  line-height: 1.25;
}

.ant-chat-meta span {
  font-size: 10.5px;
  color: var(--ae-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.ant-chat-badge {
  margin-left: auto;
  padding: 2px 6px;
  border-radius: 5px;
  background: rgb(59 130 246 / 0.12);
  color: var(--ae-blue);
  font-size: 9px;
  letter-spacing: 0.06em;
  font-weight: 700;
}

.ant-chat-viewport {
  padding: 16px 14px;
  min-height: 368px;
  display: flex;
  flex-direction: column;
  justify-content: flex-start;
}

/* 限制气泡最大宽度，还原真实 Telegram 消息比例 */
.ant-message-card {
  width: 100%;
  max-width: 430px;
}

.ant-photo-failed {
  padding: 10px 12px;
  border-radius: 10px;
  background: rgb(148 163 184 / 0.1);
  margin-bottom: 8px;
  font-size: 11px;
}

.ant-bubble {
  border: 1px solid rgb(148 163 184 / 0.16);
  border-radius: 4px 13px 13px 13px;
  padding: 12px 14px;
  background: var(--ae-surface);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: 12.5px;
  line-height: 1.75;
  box-shadow: 0 2px 8px rgb(15 23 42 / 0.04);
}

.ant-bubble.has-photo {
  border-radius: 0 0 13px 13px;
}

.ant-bubble :deep(blockquote) {
  display: block;
  border-left: 3px solid #60a5fa;
  padding: 5px 10px;
  border-radius: 4px;
  background: rgb(59 130 246 / 0.07);
  margin: 6px 0;
}

.ant-bubble :deep(code),
.ant-bubble :deep(pre) {
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 11.5px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.ant-bubble :deep(code) {
  background: rgb(148 163 184 / 0.12);
  border-radius: 4px;
  padding: 1px 4px;
}

.ant-bubble :deep(pre) {
  padding: 8px 10px;
  border-radius: 6px;
  background: rgb(148 163 184 / 0.1);
  margin: 6px 0;
}

.ant-chat-empty {
  margin: auto 0;
  text-align: center;
  padding: 28px 12px;
  color: var(--ae-muted);
  font-size: 12px;
}

/* 顶部配置读取错误横幅 */
.ant-feedback--banner {
  padding: 12px 16px;
  border-radius: 12px;
  background: rgb(220 38 38 / 0.06);
  border: 1px solid rgb(220 38 38 / 0.2);
  margin-bottom: 16px;
  font-size: 12px;
  line-height: 1.7;
}

.ant-feedback--banner button {
  margin-top: 8px;
}

.ant-error {
  color: #dc2626;
  font-weight: 500;
}

.ant-notice {
  color: #059669;
  font-weight: 500;
}

/* 底部吸附操作栏：首屏直观、滚动常驻、反馈就近 */
.ant-sticky-bar {
  position: sticky;
  bottom: 0;
  z-index: 20;
  margin-top: 24px;
  padding: 12px 16px;
  padding-bottom: max(12px, env(safe-area-inset-bottom, 0px));
  border-top: 1px solid rgb(148 163 184 / 0.2);
  background: var(--ae-surface);
  background: color-mix(in srgb, var(--ae-surface) 92%, transparent);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  box-shadow: 0 -4px 16px rgb(15 23 42 / 0.06);
  border-radius: 12px 12px 0 0;
}

:global(.dark) .ant-sticky-bar {
  border-top-color: rgb(148 163 184 / 0.16);
  box-shadow: 0 -4px 20px rgb(0 0 0 / 0.4);
}

.ant-bar-feedback {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 12px;
  padding: 8px 12px;
  border-radius: 9px;
  margin-bottom: 10px;
  font-size: 11.5px;
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.ant-bar-feedback.is-error {
  background: rgb(220 38 38 / 0.08);
  border: 1px solid rgb(220 38 38 / 0.24);
}

.ant-bar-feedback.is-notice {
  background: rgb(16 185 129 / 0.08);
  border: 1px solid rgb(16 185 129 / 0.24);
}

.ant-bar-feedback-text {
  flex: 1;
  min-width: 0;
  max-height: 120px;
  overflow-y: auto;
}

.ant-bar-feedback-text p {
  margin: 0;
}

.ant-bar-retry-btn {
  flex: none;
  font-size: 11.5px;
  min-height: 30px;
  padding: 4px 10px;
  border-radius: 7px;
  white-space: nowrap;
}

.ant-bar-main {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  min-width: 0;
}

.ant-bar-status {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 12px;
  min-width: 0;
}

.ant-bar-warning {
  font-size: 11.5px;
  color: #dc2626;
  font-weight: 500;
}

:global(.dark) .ant-bar-warning {
  color: #fca5a5;
}

.ant-bar-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: none;
}

.ant-save-btn {
  min-height: 36px;
  padding: 6px 18px;
  font-weight: 600;
  font-size: 13px;
  white-space: nowrap;
}

.ant-settings :focus-visible {
  outline: 2px solid #60a5fa;
  outline-offset: 2px;
}

.ant-settings :disabled {
  cursor: default;
  opacity: 0.55;
}

:global(.dark) .ant-error {
  color: #fca5a5;
}

:global(.dark) .ant-notice {
  color: #6ee7b7;
}

/* 按通知面板真实宽度响应 */

.ant-settings {
  container: ant-panel / inline-size;
  width: 100%;
  min-width: 0;
  color: var(--ae-ink);
  font-size: 13px;
}

/* 防止卡片头部与下拉菜单挤压溢出 */

.ant-card-header,
.ant-pane-header {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: center;
  gap: 8px 12px;
  min-width: 0;
}

.ant-preview-SimControl {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  max-width: 100%;
}

/* 变量快捷插入栏 */

.ant-variables {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding: 10px 12px;
  background: rgb(148 163 184 / 0.06);
  border-bottom: 1px solid rgb(148 163 184 / 0.16);
}

.ant-variables-label {
  flex: none;
  font-size: 11px;
  font-weight: 500;
  color: var(--ae-muted);
}

.ant-variables-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.ant-variables button {
  display: inline-flex;
  gap: 5px;
  align-items: center;
  min-height: 32px;
  padding: 4px 10px;
  border: 1px solid rgb(148 163 184 / 0.24);
  border-radius: 7px;
  background: var(--ae-surface);
  font-size: 11.5px;
  color: var(--ae-ink);
  cursor: pointer;
  transition: border-color 0.14s, background-color 0.14s, transform 0.1s;
}

.ant-variables button:focus-visible {
  outline: 2px solid #60a5fa;
  outline-offset: 2px;
}

/* 竖版封面完整显示 */

.ant-photo {
  display: block;
  width: 100%;
  max-height: 280px;
  object-fit: contain;
  border: 1px solid rgb(148 163 184 / 0.18);
  border-bottom: 0;
  border-radius: 12px 12px 0 0;
  background: rgb(15 23 42 / 0.04);
}

:global(.dark) .ant-photo {
  background: rgb(15 23 42 / 0.5);
}

/* 当面板实宽 <= 980px 时：编辑与预览改为 1:1 等宽 */

@container ant-panel (max-width: 980px) {
  .ant-composer {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    gap: 16px;
  }
}

/* 当面板实宽 <= 820px 时（如 1024~1280px 屏幕带主侧栏+设置侧栏）：
   编辑器与预览提前解耦为上下单列，避免 300px 窄列挤压溢出 */

@container ant-panel (max-width: 820px) {
  .ant-composer {
    grid-template-columns: minmax(0, 1fr);
  }
  .ant-preview-pane {
    position: static;
  }
  .ant-chat-viewport {
    min-height: 240px;
  }
  .ant-message-card {
    max-width: 460px;
  }
}

/* 当面板实宽 <= 680px 时：顶部双卡（Bot + 配图）与 3 个通知事件转为单列 */

@container ant-panel (max-width: 680px) {
  .ant-top-grid {
    grid-template-columns: minmax(0, 1fr);
    gap: 12px;
  }
  .ant-events {
    grid-template-columns: minmax(0, 1fr);
    gap: 8px;
  }
  .ant-event small {
    white-space: normal;
  }
}

/* 当面板实宽 <= 520px 时（窄屏/手机）：头部、输入框与吸附操作栏全宽适配 */

@container ant-panel (max-width: 520px) {
  .ant-heading {
    flex-direction: column;
    gap: 10px;
    margin-bottom: 16px;
  }
  .ant-editor-box textarea {
    min-height: 240px;
    padding: 12px;
  }
  .ant-message-card {
    max-width: 100%;
  }
  .ant-bar-feedback {
    flex-direction: column;
    align-items: stretch;
  }
  .ant-bar-retry-btn {
    width: 100%;
  }
  .ant-bar-main {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
  }
  .ant-bar-status {
    justify-content: space-between;
  }
  .ant-save-btn {
    width: 100%;
    min-height: 42px;
  }
}

/* 触屏设备加大点击热区 */

@media (pointer: coarse) {
  .ant-variables button {
    min-height: 38px;
    padding: 6px 12px;
  }
  .ant-text-button {
    min-height: 38px;
    padding: 6px 12px;
  }
  .ant-event {
    min-height: 48px;
  }
  .ant-save-btn {
    min-height: 44px;
  }
  .ant-bar-retry-btn {
    min-height: 38px;
  }
}

/* 减少动效模式 */

@media (prefers-reduced-motion: reduce) {
  .ant-text-button,
  .ant-event,
  .ant-event-check,
  .ant-editor-box,
  .ant-variables button {
    transition: none;
  }
  .ant-variables button:active:not(:disabled) {
    transform: none;
  }
}
</style>
