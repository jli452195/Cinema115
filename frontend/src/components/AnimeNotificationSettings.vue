<script setup lang="ts">
import SelectMenu from './SelectMenu.vue'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { IconBrandTelegram, IconCheck, IconCode, IconEye, IconPhoto, IconRefresh } from '@tabler/icons-vue'
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
const photoLoading = ref(false), previewResolved = ref(false)
// 首次响应前预留配图空间，后续刷新保留上一版已完成的消息。
const showPreviewPhoto = computed(() => previewResolved.value ? !!previewPhoto.value : settings.value?.imageMode !== 'none')
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
  if (!settings.value || !config.value || loading.value || saving.value || !dirty.value || previewing.value || previewError.value) return
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
  previewing.value = true; previewError.value = ''
  try {
    const result = await telegramRequest<{ html: string; photoUrl: string }>(props.token, 'anime-notifications/preview', {
      method: 'POST', body: JSON.stringify({ template: template.value, event: previewKind.value, imageMode: settings.value?.imageMode || 'cover', imageUrl: settings.value?.imageUrl || '' }),
      signal: AbortSignal.any([lifecycle.signal, previewController.signal]),
    })
    if (current === previewGeneration && !lifecycle.signal.aborted) {
      if (typeof result.photoUrl !== 'string') throw new Error('后台尚未提供图片预览，请更新并重启后端。')
      if (result.photoUrl !== previewPhoto.value) {
        photoFailed.value = false
        photoLoading.value = !!result.photoUrl
      }
      previewHTML.value = safePreview(result.html); previewPhoto.value = result.photoUrl
      previewResolved.value = true
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
  clearTimeout(previewTimer); previewController?.abort(); previewGeneration++; previewError.value = ''
  // 输入立即更新，只对预览请求做防抖，避免光标延迟和预览反复清空。
  if (config.value && !lifecycle.signal.aborted) { previewing.value = true; previewTimer = setTimeout(() => { void preview() }, 150) }
})
watch(dirty, value => emit('dirty', value), { immediate: true })
watch(saving, value => emit('blocked', value), { immediate: true })
window.addEventListener('beforeunload', unload)
void load()
onBeforeUnmount(() => { lifecycle.abort(); previewController?.abort(); clearTimeout(previewTimer); window.removeEventListener('beforeunload', unload); leaveDecision?.(false); emit('blocked', false); emit('dirty', false) })
</script>

<template>
  <div class="ant-settings" :aria-busy="loading || saving">
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
      <div class="ant-workspace">
        <!-- 左侧：统一配置表单卡片 (约 60%) -->
        <div class="ant-config-card">
          <fieldset :disabled="saving || loading" class="ant-form-fields">
            <!-- 分组 1：推送 Bot 与通知配图模式 -->
            <div class="ant-section">
              <div class="ant-section-grid">
                <!-- 推送 Bot -->
                <div class="ant-field-group">
                  <div class="ant-field-header">
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
                  <p class="ant-muted ant-field-hint">{{ botHint }}</p>
                </div>

                <!-- 通知配图模式 -->
                <div class="ant-field-group">
                  <div class="ant-field-header">
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
                  <p class="ant-muted ant-field-hint">
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
            </div>

            <!-- 分组 2：触发通知事件 -->
            <div class="ant-section ant-section--divided">
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

            <!-- 分组 3：消息模板与变量 -->
            <div class="ant-section ant-section--divided ant-editor-pane">
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
                  rows="13"
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
          </fieldset>

          <!-- 反馈提示横幅：移至 fieldset 外，避免保存失败后重试按钮被整组禁用 -->
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

          <!-- 分组 4：保存与状态（对应 .ars-footer 风格） -->
          <footer class="ant-footer">
            <div class="ant-footer-status">
              <span class="ant-status-pill" :class="{ 'is-dirty': dirty }">
                <span class="ant-status-dot" aria-hidden="true"></span>
                {{ dirty ? '有未保存的修改' : '当前配置已保存' }}
              </span>
              <span v-if="previewError" class="ant-footer-warning">
                模板预览异常，请修正后再保存
              </span>
            </div>

            <div class="ant-footer-actions">
              <button
                type="submit"
                class="ae-button ae-primary ant-save-btn"
                :disabled="loading || saving || !dirty || !!previewError || previewing"
              >
                {{ saving ? '保存中…' : '保存通知设置' }}
              </button>
            </div>
          </footer>
        </div>

        <!-- 右侧：Telegram 消息模拟独立卡片 (约 40%) -->
        <aside class="ant-preview-pane">
          <!-- 内层容器：在右侧网格范围内居中缩小，常规最大宽度约 400px，高度较矮时收窄至 360px -->
          <div class="ant-preview-content">
            <!-- 顶部轻量操作栏 -->
            <div class="ant-preview-header">
              <div class="ant-preview-title">
                <IconEye :size="16" aria-hidden="true" />
                <span>消息预览</span>
              </div>
              <div class="ant-preview-control">
                <SelectMenu
                  v-model="previewKind"
                  label="预览通知事件"
                  class="min-w-[120px] max-w-40"
                  :options="previewKinds"
                />
              </div>
            </div>

            <!-- 状态行：保留 40px 最低高度，长错误自然换行，不设内部滚动 -->
            <div class="ant-preview-status" role="status" aria-live="polite">
              <p v-if="previewError" class="ant-preview-status--error">
                {{ previewError }}
              </p>
              <p v-else-if="previewing" class="ant-preview-status--loading">
                正在更新预览…
              </p>
              <p v-else class="ant-preview-status--hint">
                示例数据预览，不会发送 Telegram 消息。
              </p>
            </div>

            <!-- 只保留图文消息本体，完整展示封面与正文 -->
            <div class="ant-sim-card">
              <!-- 固定图片占位，骨架与图片同级挂载，完整展示竖版海报 -->
              <div v-if="showPreviewPhoto" class="ant-photo-frame">
                <img
                  v-if="previewPhoto && !photoFailed"
                  :key="previewPhoto"
                  class="ant-photo"
                  :src="previewPhoto"
                  alt="通知图片预览"
                  referrerpolicy="no-referrer"
                  @load="photoLoading = false"
                  @error="photoFailed = true; photoLoading = false"
                />
                <div v-if="photoFailed" class="ant-photo-failed" role="alert">
                  <p>图片暂时无法预览，请检查图片地址或网络。</p>
                </div>
                <div
                  v-if="photoLoading || !previewPhoto"
                  class="ant-photo-skeleton"
                  aria-hidden="true"
                ></div>
              </div>

              <!-- 正文随内容自然展开，整张消息跟随页面滚动 -->
              <div class="ant-preview-body">
                <div v-if="previewHTML" class="ant-bubble" v-html="previewHTML"></div>
                <div v-else class="ant-bubble ant-bubble-placeholder">
                  <div class="ant-bubble-skeleton" aria-hidden="true">
                    <span class="ant-skeleton-line" style="width: 45%"></span>
                    <span class="ant-skeleton-line" style="width: 90%"></span>
                    <span class="ant-skeleton-line" style="width: 70%"></span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </aside>
      </div>
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
.ant-settings {
  width: 100%;
  min-width: 0;
  color: var(--ae-ink);
  font-size: 13px;
}

.ant-muted {
  font-size: 12px;
  color: var(--ae-muted);
  line-height: 1.65;
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

/* 独立双栏工作台布局：>=1024px 左约60% 右约40%，<1024px 上下堆叠 */
.ant-workspace {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 24px;
  align-items: start;
  min-width: 0;
}

@media (min-width: 1024px) {
  .ant-workspace {
    grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
    gap: 24px;
  }
}

/* 左侧：唯一统一表单卡片，支持命名容器查询实现卡片宽度自适应 */
.ant-config-card {
  container-type: inline-size;
  container-name: config-card;
  min-width: 0;
  padding: 24px 26px;
  border-radius: 18px;
  background: var(--ae-surface);
  border: 1px solid rgb(148 163 184 / 0.14);
  box-shadow: 0 4px 20px rgb(15 23 42 / 0.04);
}

.dark .ant-config-card {
  box-shadow: 0 4px 20px rgb(0 0 0 / 0.25);
  border-color: rgb(148 163 184 / 0.14);
}

.ant-form-fields {
  display: flex;
  flex-direction: column;
  min-width: 0;
  border: 0;
  padding: 0;
  margin: 0;
}

/* 内部组间细分隔线与间距 */
.ant-section {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.ant-section--divided {
  margin-top: 22px;
  padding-top: 22px;
  border-top: 1px solid rgb(148 163 184 / 0.12);
}

/* 推送 Bot 与配图模式并排（移动端或窄卡单列） */
.ant-section-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 18px;
}

@media (max-width: 640px) {
  .ant-section-grid {
    grid-template-columns: minmax(0, 1fr);
    gap: 16px;
  }
}

.ant-field-group {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.ant-field-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  margin-bottom: 4px;
  min-height: 24px;
}

.ant-field-hint {
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

.ant-custom-url input {
  width: 100%;
  min-height: 38px;
  padding: 7px 11px;
  font-size: 12px;
  border: 1px solid rgb(148 163 184 / 0.26);
  border-radius: 9px;
  background: var(--ae-surface);
  color: var(--ae-ink);
}

/* 通知事件触发器 */
.ant-events-header {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: baseline;
  gap: 8px 16px;
  margin-bottom: 12px;
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
  background: rgb(148 163 184 / 0.06);
  border: 1px solid rgb(148 163 184 / 0.14);
  font-size: 11px;
  line-height: 1.7;
  color: var(--ae-muted);
}

.ant-events {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
}

@media (max-width: 680px) {
  .ant-events {
    grid-template-columns: minmax(0, 1fr);
    gap: 8px;
  }
}

.ant-event {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid rgb(148 163 184 / 0.2);
  border-radius: 10px;
  background: rgb(148 163 184 / 0.04);
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

@media (max-width: 680px) {
  .ant-event small {
    white-space: normal;
  }
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

/* 消息模板编辑器 */
.ant-editor-pane {
  min-width: 0;
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

.ant-editor-box {
  display: flex;
  flex-direction: column;
  border-radius: 13px;
  border: 1px solid rgb(148 163 184 / 0.24);
  background: var(--ae-surface);
  overflow: hidden;
  transition: border-color 0.15s, box-shadow 0.15s;
}

.ant-editor-box:focus-within {
  border-color: #60a5fa;
  box-shadow: 0 0 0 3px rgb(59 130 246 / 0.12);
}

.ant-variables {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding: 10px 12px;
  background: rgb(148 163 184 / 0.06);
  border-bottom: 1px solid rgb(148 163 184 / 0.16);
  min-width: 0;
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
  min-width: 0;
  flex: 1 1 auto;
}

.ant-variables button {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 4px 6px;
  align-items: center;
  min-height: 32px;
  max-width: 100%;
  padding: 4px 10px;
  border: 1px solid rgb(148 163 184 / 0.24);
  border-radius: 7px;
  background: var(--ae-surface);
  font-size: 11.5px;
  color: var(--ae-ink);
  cursor: pointer;
  white-space: normal;
  text-align: left;
  overflow-wrap: anywhere;
  word-break: break-word;
  transition: border-color 0.14s, background-color 0.14s, transform 0.1s;
}

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
  word-break: break-all;
}

.ant-variables button span {
  color: var(--ae-muted);
  font-size: 10.5px;
  word-break: break-word;
}

.ant-editor-box textarea {
  width: 100%;
  resize: vertical;
  min-height: 280px;
  padding: 14px 16px;
  border: 0;
  background: transparent;
  color: var(--ae-ink);
  font: 12.5px/1.75 ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

.ant-editor-box textarea:focus-visible {
  outline: none;
}

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

/* 反馈横幅提示 */
.ant-bar-feedback {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 12px;
  padding: 10px 14px;
  border-radius: 10px;
  margin-top: 16px;
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

/* 左侧卡片底部：与 RSS .ars-footer 风格一致 */
.ant-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 14px;
  margin-top: 24px;
  padding-top: 18px;
  border-top: 1px solid rgb(148 163 184 / 0.12);
}

.ant-footer-status {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 12px;
  min-width: 0;
}

.ant-footer-warning {
  font-size: 11.5px;
  color: #dc2626;
  font-weight: 500;
}

.dark .ant-footer-warning {
  color: #fca5a5;
}

.ant-footer-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: none;
}

.ant-save-btn {
  min-height: 38px;
  padding: 6px 20px;
  font-weight: 600;
  font-size: 13px;
  white-space: nowrap;
}

/* 左侧卡片基于自身可用宽度的自适应布局（避免 1024/1280px 带侧栏时挤压溢出） */
@container config-card (max-width: 500px) {
  .ant-section-grid {
    grid-template-columns: minmax(0, 1fr);
    gap: 16px;
  }

  .ant-events {
    grid-template-columns: minmax(0, 1fr);
    gap: 8px;
  }

  .ant-event small {
    white-space: normal;
  }

  .ant-pane-header {
    flex-wrap: wrap;
    align-items: flex-start;
    gap: 8px 12px;
  }

  .ant-footer {
    flex-direction: column;
    align-items: stretch;
    gap: 12px;
  }

  .ant-footer-status {
    justify-content: space-between;
  }

  .ant-footer-actions {
    width: 100%;
  }

  .ant-save-btn {
    width: 100%;
    min-height: 42px;
  }

  .ant-bar-feedback {
    flex-direction: column;
    align-items: stretch;
  }

  .ant-bar-retry-btn {
    width: 100%;
  }
}

/* ================== 右侧：仿真预览 ================== */
.ant-preview-pane {
  min-width: 0;
}

@media (min-width: 1024px) {
  .ant-preview-pane {
    position: sticky;
    top: calc(var(--workbench-header-height, 64px) + 16px);
    align-self: start;
  }
}

/* 预览内层容器：居中缩小，常规最大宽度约 400px，高度较矮时收窄至 360px */
.ant-preview-content {
  width: 100%;
  max-width: 400px;
  margin-inline: auto;
}

@media (max-height: 850px) {
  .ant-preview-content {
    max-width: 360px;
  }
}

/* 顶部轻量操作栏 */
.ant-preview-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  min-height: 36px;
  margin-bottom: 6px;
}

.ant-preview-title {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
  font-size: 13px;
  color: var(--ae-ink);
}

.ant-preview-control {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
}

/* 状态行：保留 40px 最低高度稳定布局，长错误自然换行，不设内部滚动或截断 */
.ant-preview-status {
  box-sizing: border-box;
  min-height: 40px;
  margin-bottom: 10px;
  font-size: 11.5px;
  line-height: 1.45;
  display: flex;
  align-items: center;
}

.ant-preview-status p {
  margin: auto 0;
  width: 100%;
  overflow-wrap: anywhere;
  word-break: break-word;
  white-space: normal;
}

.ant-preview-status--hint {
  color: var(--ae-muted);
}

.ant-preview-status--loading {
  color: var(--ae-blue);
}

.ant-preview-status--error {
  color: #dc2626;
  font-weight: 500;
}

.dark .ant-preview-status--error {
  color: #fca5a5;
}

/* 消息气泡卡片：白色正文气泡，深色主题中性色，轻量原生圆角 */
.ant-sim-card {
  width: 100%;
  border-radius: 12px;
  border: 1px solid rgb(148 163 184 / 0.18);
  background: #ffffff;
  box-shadow: 0 1px 4px rgb(15 23 42 / 0.06);
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.dark .ant-sim-card {
  background: rgb(var(--surface-card));
  border-color: rgb(var(--line-default));
  box-shadow: 0 2px 8px rgb(0 0 0 / 0.25);
}

/* 媒体区域：固定 16/9 占位比例槽，淡灰背景 */
.ant-photo-frame {
  position: relative;
  width: 100%;
  aspect-ratio: 16 / 9;
  background: #e2e8f0;
  overflow: hidden;
  border-bottom: 1px solid rgb(148 163 184 / 0.12);
  flex: none;
}

.dark .ant-photo-frame {
  background: rgb(var(--surface-page));
  border-bottom-color: rgb(var(--line-default));
}

/* 竖版海报居中 contain 完整显示，两侧留白，禁止 cover 裁切 */
.ant-photo {
  position: absolute;
  inset: 0;
  display: block;
  width: 100%;
  height: 100%;
  object-fit: contain;
  object-position: center;
}

.ant-photo-skeleton {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  z-index: 1;
  pointer-events: none;
  background: linear-gradient(
    90deg,
    rgb(148 163 184 / 0.08) 25%,
    rgb(148 163 184 / 0.16) 50%,
    rgb(148 163 184 / 0.08) 75%
  );
  background-size: 200% 100%;
  animation: ant-skeleton-pulse 1.6s ease-in-out infinite;
}

@keyframes ant-skeleton-pulse {
  0% {
    background-position: 200% 0;
  }
  100% {
    background-position: -200% 0;
  }
}

.ant-photo-failed {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  width: 100%;
  height: 100%;
  padding: 16px;
  text-align: center;
  font-size: 11px;
  color: var(--ae-muted);
  background: rgb(148 163 184 / 0.06);
  z-index: 2;
}

.ant-photo-failed p {
  margin: 0;
}

/* 正文预览区：完全随内容自然撑开，取消所有高度限制与内部滚动 */
.ant-preview-body {
  width: 100%;
  min-height: 0;
  padding: 10px 12px;
}

.ant-bubble {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  word-break: break-word;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ae-ink);
}

.ant-bubble :deep(blockquote) {
  display: block;
  border-left: 3px solid #60a5fa;
  padding: 4px 8px;
  border-radius: 4px;
  background: rgb(59 130 246 / 0.07);
  margin: 5px 0;
  font-size: 11.5px;
  line-height: 1.5;
}

.ant-bubble :deep(code),
.ant-bubble :deep(pre) {
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 11px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  word-break: break-word;
}

.ant-bubble :deep(code) {
  background: rgb(148 163 184 / 0.12);
  border-radius: 4px;
  padding: 1px 4px;
}

.ant-bubble :deep(pre) {
  padding: 6px 8px;
  border-radius: 6px;
  background: rgb(148 163 184 / 0.1);
  margin: 5px 0;
}

/* 初始无文本骨架屏 */
.ant-bubble-skeleton {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 4px 0;
}

.ant-skeleton-line {
  display: block;
  height: 11px;
  border-radius: 5px;
  background: rgb(148 163 184 / 0.12);
  animation: ant-skeleton-pulse 1.6s ease-in-out infinite;
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

.ant-settings :focus-visible {
  outline: 2px solid #60a5fa;
  outline-offset: 2px;
}

.ant-settings :disabled {
  cursor: default;
  opacity: 0.55;
}

.dark .ant-error {
  color: #fca5a5;
}

.dark .ant-notice {
  color: #6ee7b7;
}

/* 窄屏响应式处理 (手机 320px ~ 640px) */
@media (max-width: 640px) {
  .ant-config-card {
    padding: 16px 14px;
    border-radius: 14px;
  }
  .ant-editor-box textarea {
    min-height: 220px;
    padding: 12px;
  }
  .ant-sim-card {
    max-width: 100%;
  }
  .ant-footer {
    flex-direction: column;
    align-items: stretch;
    gap: 12px;
  }
  .ant-footer-status {
    justify-content: space-between;
  }
  .ant-save-btn {
    width: 100%;
    min-height: 42px;
  }
  .ant-bar-feedback {
    flex-direction: column;
    align-items: stretch;
  }
  .ant-bar-retry-btn {
    width: 100%;
  }
}

/* 触屏热区适配 */
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

/* 减少动效模式支持 */
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
  .ant-photo-skeleton,
  .ant-skeleton-line {
    animation: none;
  }
}
</style>
