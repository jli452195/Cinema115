<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { nanShareRequest, type RE0Resource, type RE0SearchResult, type RE0ShareOptions, type RE0Submission, type RE0Transfer, type RE0TransferOptions } from '../types/nanshare'
import re0Logo from '../assets/re0/logo-re0-square.webp'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string; title: string; themeColors?: Record<string, string> }>()
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement>()
const viewportStyle = ref<Record<string, string>>({})
const dialogStyle = computed(() => ({ ...props.themeColors, ...viewportStyle.value }))
const lifecycle = new AbortController()
const previousFocus = document.activeElement as HTMLElement | null
const previousOverflow = document.body.style.overflow
const result = ref<RE0SearchResult>()
const error = ref('')
const errorSlug = ref('')
const phase = ref<'' | 'search' | 'options' | 'submit' | 'transfer-options' | 'transfer'>('')
const transferSelected = ref<RE0Resource>()
const transferOptions = ref<RE0TransferOptions>()
const transferFolder = ref('')
type TransferInput = { requestId: string; searchId: string; slug: string; accountId: string; folderId: string }
const transferPending = ref<Record<string, TransferInput>>({})
const writing = computed(() => phase.value === 'submit' || phase.value === 'transfer')
const selected = ref<RE0Resource>()
const options = ref<RE0ShareOptions>()
const mode = ref('')
const folderIndex = ref('')
const query = ref('')
const refreshNotice = ref('')
const retryConfirmed = ref(false)
type SubmitInput = { requestId: string; searchId: string; slug: string; version: string; accountId: string; mode: string; folderIndex: number }
const pendingRequests = ref<Record<string, SubmitInput>>({})
const needsRetryConfirmation = computed(() => !!selected.value && result.value?.submissions[selected.value.slug]?.state === 'unknown')
const filtered = computed(() => (result.value?.items || []).filter(item => `${item.name} ${item.remark} ${(item.video_resolution || []).join(' ')} ${item.publisher}`.toLowerCase().includes(query.value.trim().toLowerCase())))
const canSubmit = computed(() => !!selected.value && !!options.value?.accountId && !!mode.value && folderIndex.value !== '' && !phase.value && (!needsRetryConfirmation.value || retryConfirmed.value))
const needsSelection = computed(() => !!options.value && (options.value.modes.length > 1 || options.value.folders.length > 1))
function name(item: RE0Resource) { return item.remark || item.name || item.title || '未命名资源' }
function points(item: RE0Resource) { return item.is_unlocked ? '已解锁' : item.unlock_points == null ? '积分未知' : item.unlock_points > 0 ? `${item.unlock_points} 积分` : '免费' }
function subtitles(item: RE0Resource) { return [...(item.subtitle_language || []), ...(item.subtitle_type || [])].join(' / ') }
async function revealSelectionPanel() {
  await nextTick()
  const panel = dialog.value?.querySelector<HTMLElement>('.re0-selection')
  panel?.scrollIntoView({ block: 'nearest', behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth' })
  panel?.querySelector<HTMLElement>('select, input, button')?.focus({ preventScroll: true })
}
async function focusResourceAction(slug: string, action: 'transfer' | 'share') {
  await nextTick()
  const resource = Array.from(dialog.value?.querySelectorAll<HTMLElement>('[data-resource-slug]') || []).find(element => element.dataset.resourceSlug === slug)
  const button = resource?.querySelector<HTMLButtonElement>(`[data-re0-action="${action}"]`)
  const target = button && !button.disabled ? button : resource || dialog.value?.querySelector<HTMLElement>('input[type="search"]')
  target?.focus({ preventScroll: true })
}
function cancelSelection(slug: string, action: 'transfer' | 'share') {
  if (phase.value) return
  if (action === 'transfer') transferSelected.value = undefined
  else selected.value = undefined
  void focusResourceAction(slug, action)
}
function clearQuery() {
  query.value = ''
  dialog.value?.querySelector<HTMLInputElement>('input[type="search"]')?.focus({ preventScroll: true })
}
function label(item: RE0Resource) {
  const state = result.value?.submissions[item.slug]?.state
  if (selected.value?.slug === item.slug && phase.value === 'options') return '读取配置…'
  if (selected.value?.slug === item.slug && phase.value === 'submit') return '提交中…'
  if (pendingRequests.value[item.slug]) return '重试获取提交结果'
  return state === 'accepted' ? '再次生成 STRM' : state === 'submitting' ? '提交中…' : state === 'unknown' ? '核对后重新提交' : state === 'failed' ? '重新提交' : '分享 STRM'
}
async function search(refresh = false) {
  if (phase.value) return
  phase.value = 'search'; error.value = ''; errorSlug.value = ''; refreshNotice.value = ''; selected.value = undefined; options.value = undefined; transferSelected.value = undefined
  try {
    const next = await nanShareRequest<RE0SearchResult>(props.token, 'search', lifecycle.signal, 'POST', { kind: props.kind, tmdbId: Number(props.tmdbId), refresh })
    if (!lifecycle.signal.aborted) result.value = next
  } catch (reason) { if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : 'RE0 搜索失败' }
  finally { phase.value = '' }
}
async function prepare(item: RE0Resource) {
  if (phase.value || !result.value || result.value.submissions[item.slug]?.state === 'submitting') return
  transferSelected.value = undefined
  const pending = pendingRequests.value[item.slug]
  if (pending) { selected.value = item; await send(pending); return }
  retryConfirmed.value = false
  selected.value = item; options.value = undefined; mode.value = ''; folderIndex.value = ''; error.value = ''; errorSlug.value = item.slug; phase.value = 'options'
  try {
    const next = await nanShareRequest<RE0ShareOptions>(props.token, `share-options/${encodeURIComponent(result.value.id)}`, lifecycle.signal)
    if (lifecycle.signal.aborted) return
    if (!next.accountId || !next.modes.length || !next.folders.length) throw new Error('请先在 NanShare 配置分享 STRM 的账号、生成模式和输出目录')
    options.value = next
    // Match NanShare: reuse the feature's saved account binding and skip the
    // dialog when exactly one mode and one destination are available.
    mode.value = next.modes[0]!
    folderIndex.value = String(next.folders[0]!.index)
  } catch (reason) {
    selected.value = undefined
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取分享 STRM 配置失败'
  }
  finally { phase.value = '' }
  if (lifecycle.signal.aborted || !options.value) return
  if (!needsSelection.value && !needsRetryConfirmation.value) { await submit(); return }
  await revealSelectionPanel()
}
async function submit() {
  if (!canSubmit.value || !result.value || !selected.value || !options.value) return
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  const requestId = Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('')
  await send({ requestId, searchId: result.value.id, slug: selected.value.slug, version: options.value.version, accountId: options.value.accountId, mode: mode.value, folderIndex: Number(folderIndex.value) })
}
async function send(input: SubmitInput) {
  if (!result.value || phase.value) return
  const slug = input.slug
  pendingRequests.value[slug] = input
  phase.value = 'submit'; error.value = ''; errorSlug.value = slug; refreshNotice.value = ''
  try {
    const receipt = await nanShareRequest<RE0Submission>(props.token, 'submit', lifecycle.signal, 'POST', input)
    if (lifecycle.signal.aborted) return
    result.value.submissions[slug] = receipt; selected.value = undefined; delete pendingRequests.value[slug]
    if (receipt.state === 'accepted') {
      phase.value = 'search'
      try {
        const updated = await nanShareRequest<RE0SearchResult>(props.token, 'search', lifecycle.signal, 'POST', { kind: props.kind, tmdbId: Number(props.tmdbId), refresh: true })
        if (!lifecycle.signal.aborted) result.value = updated
      } catch {
        if (!lifecycle.signal.aborted) refreshNotice.value = '任务已提交，解锁状态暂未刷新，请稍后点击“刷新搜索”。'
      }
    }
  } catch (reason) {
    if (!lifecycle.signal.aborted) {
      error.value = reason instanceof Error ? reason.message : '提交失败'
      // Restore the server receipt before allowing another submission after a disconnect.
      try {
        result.value = await nanShareRequest<RE0SearchResult>(props.token, 'search', lifecycle.signal, 'POST', { kind: props.kind, tmdbId: Number(props.tmdbId) })
        const recovered = result.value.submissions[slug]
        const status = reason instanceof Error && 'status' in reason ? Number(reason.status) : 0
        if (recovered?.requestId === input.requestId || (status >= 400 && status < 500)) delete pendingRequests.value[slug]
      }
      catch { error.value += '；请先在 NanShare 核对任务，再重新打开搜索' }
      selected.value = undefined
    }
  } finally { phase.value = ''; void focusResourceAction(slug, 'share') }
}
function transferLabel(item: RE0Resource) {
  if (transferSelected.value?.slug === item.slug && phase.value === 'transfer-options') return '读取配置…'
  if (transferSelected.value?.slug === item.slug && phase.value === 'transfer') return '转存中…'
  if (transferPending.value[item.slug]) return '获取转存结果'
  const state = result.value?.transfers?.[item.slug]?.state
  return ({ accepted: '已转存', already_saved: '已转存过', unknown: '结果待核查', partial: '部分完成', submitting: '转存中…' } as Record<string, string>)[state || ''] || '一键转存'
}
function transferBlocked(item: RE0Resource) { return !!phase.value || ['accepted', 'already_saved', 'unknown', 'partial', 'submitting'].includes(result.value?.transfers?.[item.slug]?.state || '') }
async function prepareTransfer(item: RE0Resource) {
  if (transferBlocked(item) || !result.value) return
  selected.value = undefined; transferSelected.value = item; error.value = ''; errorSlug.value = item.slug; transferOptions.value = undefined; transferFolder.value = ''
  const pending = transferPending.value[item.slug]
  if (pending) { await sendTransfer(pending); return }
  phase.value = 'transfer-options'
  try {
    const options = await nanShareRequest<RE0TransferOptions>(props.token, `transfer-options/${encodeURIComponent(result.value.id)}`, lifecycle.signal)
    if (!options.accountId || !options.folders.length) throw new Error('请先在 NanShare 配置 115 转存助手的账号和目录')
    transferOptions.value = options
    if (options.folders.length === 1) transferFolder.value = options.folders[0]!.id
  } catch (reason) {
    transferSelected.value = undefined
    if (!lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取转存配置失败'
  } finally { phase.value = '' }
  if (lifecycle.signal.aborted || !transferOptions.value) return
  if (transferOptions.value.folders.length === 1) await submitTransfer()
  else await revealSelectionPanel()
}
async function submitTransfer() {
  if (phase.value || !result.value || !transferSelected.value || !transferOptions.value || !transferFolder.value) return
  const requestId = Array.from(crypto.getRandomValues(new Uint8Array(16)), value => value.toString(16).padStart(2, '0')).join('')
  await sendTransfer({ requestId, searchId: result.value.id, slug: transferSelected.value.slug, accountId: transferOptions.value.accountId, folderId: transferFolder.value })
}
async function sendTransfer(input: TransferInput) {
  if (phase.value || !result.value) return
  transferPending.value[input.slug] = input
  phase.value = 'transfer'; error.value = ''; errorSlug.value = input.slug
  try {
    const receipt = await nanShareRequest<RE0Transfer>(props.token, 'transfer', lifecycle.signal, 'POST', input)
    if (!lifecycle.signal.aborted) {
      result.value.transfers = { ...result.value.transfers, [input.slug]: receipt }
      delete transferPending.value[input.slug]
    }
  } catch (reason) {
    if (!lifecycle.signal.aborted) {
      error.value = reason instanceof Error ? reason.message : '转存请求失败'
      const status = reason instanceof Error && 'status' in reason ? Number(reason.status) : 0
      if (status >= 400 && status < 500) delete transferPending.value[input.slug]
      else error.value += '；可点击“获取转存结果”查询原请求，请勿重复提交。'
    }
  } finally { phase.value = ''; transferSelected.value = undefined; void focusResourceAction(input.slug, 'transfer') }
}
function close() { if (!writing.value) emit('close') }
function syncVisualViewport() {
  const viewport = window.visualViewport
  viewportStyle.value = {
    '--re0-visible-height': `${viewport?.height ?? window.innerHeight}px`,
    '--re0-visible-top': `${viewport?.offsetTop ?? 0}px`,
    '--re0-visible-bottom': `${viewport ? Math.max(0, window.innerHeight - viewport.height - viewport.offsetTop) : 0}px`,
  }
}
onMounted(() => {
  syncVisualViewport()
  window.visualViewport?.addEventListener('resize', syncVisualViewport)
  window.visualViewport?.addEventListener('scroll', syncVisualViewport)
  document.body.style.overflow = 'hidden'; dialog.value?.showModal(); void search()
})
onBeforeUnmount(() => {
  window.visualViewport?.removeEventListener('resize', syncVisualViewport)
  window.visualViewport?.removeEventListener('scroll', syncVisualViewport)
  lifecycle.abort(); dialog.value?.close(); document.body.style.overflow = previousOverflow; previousFocus?.focus()
})
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" :style="dialogStyle" aria-labelledby="re0-title" aria-describedby="re0-work-title" class="re0-dialog media-search-dialog" @cancel.prevent="close">
      <div class="re0-layout">
        <header class="re0-header">
          <div class="re0-brand"><img :src="re0Logo" alt="" width="40" height="40" /></div>
          <div class="re0-heading">
            <h2 id="re0-title">RE0 资源搜索</h2>
            <p id="re0-work-title" :title="title">{{ title }}</p>
          </div>
          <button autofocus type="button" aria-label="关闭 RE0 搜索" :disabled="writing" class="re0-icon-button" @click="close">
            <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="m6 6 12 12M18 6 6 18" /></svg>
          </button>
        </header>

        <div class="re0-toolbar">
          <label class="re0-search-field">
            <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 4 4" /></svg>
            <span class="sr-only">筛选当前结果</span>
            <input v-model="query" type="search" placeholder="筛选版本、清晰度或发布者" :disabled="!result?.items.length || !!phase" />
          </label>
          <div class="re0-tools">
            <button type="button" :disabled="!!phase" class="re0-tool-button" @click="search(true)">
              <svg aria-hidden="true" :class="{ 're0-spinning': phase === 'search' }" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 7v5h-5M4 17v-5h5M6.2 6.2A8 8 0 0 1 20 12M4 12a8 8 0 0 0 13.8 5.8" /></svg>
              刷新搜索
            </button>
            <RouterLink v-if="!writing" to="/nanshare" class="re0-tool-button" @click="emit('close')">
              <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M4 7h16M4 17h16" /><circle cx="9" cy="7" r="3" fill="var(--re0-container)" /><circle cx="15" cy="17" r="3" fill="var(--re0-container)" /></svg>
              登录配置
            </RouterLink>
            <button v-else type="button" disabled class="re0-tool-button">登录配置</button>
          </div>
        </div>

        <div class="re0-body" :aria-busy="phase === 'search'">
          <div class="re0-results-heading">
            <h3>115 资源</h3>
            <span v-if="result" role="status">{{ query.trim() ? '显示 ' + filtered.length + ' / ' + result.items.length + ' 个版本' : result.items.length + ' 个版本' }}</span>
          </div>
          <p v-if="error && !filtered.some(item => item.slug === errorSlug)" role="alert" class="re0-notice re0-error">{{ error }}</p>
          <p v-if="refreshNotice" role="status" class="re0-notice re0-warning">{{ refreshNotice }}</p>
          <p v-if="phase === 'search'" role="status" class="re0-notice re0-info">{{ result ? '正在刷新 RE0 资源…' : '正在搜索 RE0 资源…' }}</p>
          <div v-if="phase === 'search' && !result" aria-hidden="true" class="re0-skeleton">
            <div v-for="index in 2" :key="index"><span /><span /><span /></div>
          </div>
          <div v-if="result && !result.items.length && phase !== 'search'" role="status" class="re0-empty">
            <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v10H3Z" /><path d="M9 13h6" /></svg>
            <p>RE0 暂未收录该影视的 115 资源</p>
            <span>可以稍后刷新搜索</span>
          </div>
          <div v-else-if="result?.items.length && !filtered.length" role="status" class="re0-empty">
            <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 4 4M8 10.5h5" /></svg>
            <p>没有符合筛选条件的资源</p>
            <button type="button" class="re0-text-button" @click="clearQuery">清除筛选</button>
          </div>

          <ul v-if="filtered.length" aria-label="RE0 资源列表" class="re0-resource-list">
            <li v-for="item in filtered" :key="item.slug" :data-resource-slug="item.slug" tabindex="-1" class="re0-resource" :class="{ 're0-resource-active': selected?.slug === item.slug || transferSelected?.slug === item.slug }">
              <div class="re0-resource-main">
                <div class="re0-resource-info">
                  <div class="re0-version-heading">
                    <h3>{{ name(item) }}</h3>
                    <span data-unlock-status class="re0-badge" :class="item.is_unlocked ? 're0-info' : item.unlock_points === 0 ? 're0-success' : 're0-warning'">
                      <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="5" y="10" width="14" height="11" rx="2" /><path v-if="item.is_unlocked || item.unlock_points === 0" d="M8 10V6a4 4 0 0 1 8 0" /><path v-else d="M8 10V6a4 4 0 0 1 8 0v4" /><path v-if="item.is_unlocked" d="m9 15 2 2 4-4" /><path v-else d="M12 14v3" /></svg>
                      {{ points(item) }}
                    </span>
                    <span v-if="item.is_official" class="re0-badge re0-info">官组</span>
                    <span v-if="item.validate_status === 'invalid'" class="re0-badge re0-error">链接可能失效</span>
                  </div>
                  <dl class="re0-metrics">
                    <div v-if="item.video_resolution?.length"><dt>清晰度</dt><dd>{{ item.video_resolution.join(' / ') }}</dd></div>
                    <div v-if="item.share_size"><dt>大小</dt><dd>{{ item.share_size }}</dd></div>
                    <div v-if="item.resource_sources?.length"><dt>来源</dt><dd>{{ item.resource_sources.join(' / ') }}</dd></div>
                    <div v-if="subtitles(item)"><dt>字幕</dt><dd>{{ subtitles(item) }}</dd></div>
                  </dl>
                  <div v-if="item.publisher || item.publish_time || item.unlocked_users_count" class="re0-resource-meta">
                    <span v-if="item.publisher">发布者 {{ item.publisher }}</span>
                    <time v-if="item.publish_time">{{ item.publish_time }}</time>
                    <span v-if="item.unlocked_users_count">{{ item.unlocked_users_count }} 人解锁</span>
                  </div>
                </div>
                <div class="re0-actions">
                  <button type="button" data-re0-action="transfer" :disabled="transferBlocked(item)" class="re0-button re0-primary-button" @click="prepareTransfer(item)">
                    <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v10H3Z" /><path d="M12 10v6m-3-3 3 3 3-3" /></svg>
                    {{ transferLabel(item) }}
                  </button>
                  <button type="button" data-re0-action="share" :disabled="!!phase || result?.submissions[item.slug]?.state === 'submitting'" class="re0-button re0-outlined-button" @click="prepare(item)">
                    <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9Zm0 0v6h6" /><path d="m10 12 5 3-5 3Z" /></svg>
                    {{ label(item) }}
                  </button>
                </div>
              </div>
              <p v-if="error && errorSlug === item.slug" role="alert" class="re0-notice re0-error">{{ error }}</p>
              <p v-if="(phase === 'options' && selected?.slug === item.slug) || (phase === 'transfer-options' && transferSelected?.slug === item.slug)" role="status" class="re0-notice re0-info">{{ phase === 'options' ? '读取分享 STRM 配置…' : '读取转存配置…' }}</p>
              <p v-if="writing && (selected?.slug === item.slug || transferSelected?.slug === item.slug)" role="status" class="re0-notice re0-info">{{ phase === 'transfer' ? '正在通过 NanShare 转存，请等待转存结果…' : '正在提交给 NanShare，请等待提交结果…' }}</p>
              <p v-if="result?.submissions[item.slug]" role="status" class="re0-notice" :class="result.submissions[item.slug]?.state === 'accepted' ? 're0-success' : result.submissions[item.slug]?.state === 'failed' ? 're0-error' : 're0-warning'">{{ result.submissions[item.slug]?.message }}</p>
              <p v-if="result?.transfers?.[item.slug]" role="status" class="re0-notice" :class="['accepted', 'already_saved'].includes(result.transfers[item.slug]!.state) ? 're0-success' : result.transfers[item.slug]?.state === 'failed' ? 're0-error' : 're0-warning'">{{ result.transfers[item.slug]?.message }}</p>

              <section v-if="transferSelected?.slug === item.slug && transferOptions && transferOptions.folders.length > 1 && !writing" aria-label="一键转存目录" class="re0-selection">
                <h4>选择转存目录</h4>
                <p>使用 NanShare 的 115 转存助手配置，积分限制由 NanShare 校验。</p>
                <form @submit.prevent="submitTransfer">
                  <label class="re0-field">转存目录
                    <select v-model="transferFolder" required :disabled="!!phase">
                      <option disabled value="">请选择转存目录</option>
                      <option v-for="folder in transferOptions.folders" :key="folder.id" :value="folder.id">{{ folder.name || folder.path || folder.id }}</option>
                    </select>
                  </label>
                  <div class="re0-form-actions">
                    <button type="submit" :disabled="!transferFolder || !!phase" class="re0-button re0-primary-button">开始转存</button>
                    <button type="button" :disabled="!!phase" class="re0-text-button" @click="cancelSelection(item.slug, 'transfer')">取消</button>
                  </div>
                </form>
              </section>

              <section v-if="selected?.slug === item.slug && options && (needsSelection || needsRetryConfirmation) && phase !== 'submit'" aria-label="分享 STRM 配置" class="re0-selection">
                <h4>分享 STRM 配置</h4>
                <p>{{ points(item) }}{{ !item.is_unlocked && (item.unlock_points === null || item.unlock_points > 0) ? '，提交后由 NanShare 按 RE0 规则解锁。' : '。' }}文件生成到 NanShare 的输出目录。</p>
                <label v-if="needsRetryConfirmation" class="re0-retry-confirmation"><input v-model="retryConfirmed" type="checkbox" :disabled="!!phase" />我已核对 NanShare 任务，仍需重新提交</label>
                <form v-if="options" @submit.prevent="submit">
                  <fieldset :disabled="!!phase">
                    <label class="re0-field">生成模式
                      <select v-model="mode" required>
                        <option disabled value="">请选择模式</option>
                        <option v-for="value in options.modes" :key="value" :value="value">{{ value === 'reshare' ? '转存再分享' : '普通分享' }}</option>
                      </select>
                    </label>
                    <label class="re0-field">输出目录
                      <select v-model="folderIndex" required>
                        <option disabled value="">请选择输出目录</option>
                        <option v-for="folder in options.folders" :key="folder.index" :value="String(folder.index)">{{ folder.name || '输出目录' }} · {{ folder.path }}</option>
                      </select>
                    </label>
                    <div class="re0-form-actions">
                      <button type="submit" :disabled="!canSubmit" class="re0-button re0-primary-button">提交分享 STRM</button>
                      <button type="button" class="re0-text-button" @click="cancelSelection(item.slug, 'share')">取消</button>
                    </div>
                  </fieldset>
                </form>
              </section>
            </li>
          </ul>
        </div>
        <footer class="search-log-footer"><RouterLink v-if="!writing" :to="{ path: '/logs', query: { module: '搜索', level: 'ALL' } }" @click.capture="writing && $event.preventDefault()" @click="close">查看日志</RouterLink><span v-else>正在提交任务，请等待结果。</span></footer>
      </div>
    </dialog>
  </Teleport>
</template>

<style scoped>
.re0-dialog {
  --re0-max-height: 90dvh;
  --re0-surface: var(--search-dialog-surface);
  --re0-container: var(--search-dialog-container);
  --re0-card: var(--search-dialog-card);
  --re0-text: var(--search-dialog-text);
  --re0-muted: var(--search-dialog-muted);
  --re0-outline: var(--search-dialog-outline);
  --re0-primary: var(--search-dialog-primary);
  --re0-tonal: var(--search-dialog-tonal);
  --re0-on-tonal: var(--search-dialog-on-tonal);
  --re0-success: #246044;
  --re0-success-bg: #e6f2e9;
  --re0-warning: #765500;
  --re0-warning-bg: #fff1cd;
  --re0-error: #a12b2b;
  --re0-error-bg: #ffe9e7;
  margin: auto;
  padding: 0;
  width: min(960px, calc(100% - 48px));
  max-width: none;
  max-height: var(--re0-max-height);
  border-radius: 28px;
  color: var(--re0-text);
  color-scheme: light;
  overflow: hidden;
}
.dark .re0-dialog {
  --re0-surface: var(--search-dialog-surface);
  --re0-container: var(--search-dialog-container);
  --re0-card: var(--search-dialog-card);
  --re0-text: var(--search-dialog-text);
  --re0-muted: var(--search-dialog-muted);
  --re0-outline: var(--search-dialog-outline);
  --re0-primary: var(--search-dialog-primary);
  --re0-tonal: var(--search-dialog-tonal);
  --re0-on-tonal: var(--search-dialog-on-tonal);
  --re0-success: #a5d8b8;
  --re0-success-bg: #223d31;
  --re0-warning: #eed085;
  --re0-warning-bg: #42371e;
  --re0-error: #ffb4ab;
  --re0-error-bg: #502c2b;
  color-scheme: dark;
}
.re0-layout { display: flex; flex-direction: column; max-height: var(--re0-max-height); }
.re0-header { display: flex; flex-shrink: 0; align-items: center; gap: 14px; padding: 24px 24px 16px; }
.re0-brand { display: grid; place-items: center; flex-shrink: 0; width: 48px; height: 48px; border-radius: 14px; background: var(--re0-card); }
.re0-brand img { border-radius: 10px; }
.re0-heading { flex: 1; min-width: 0; }
.re0-heading h2 { margin: 0; font-size: 20px; line-height: 28px; font-weight: 650; }
.re0-heading p { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; overflow-wrap: anywhere; margin: 4px 0 0; color: var(--re0-muted); font-size: 13px; line-height: 20px; }
.re0-icon-button { display: grid; place-items: center; flex-shrink: 0; width: 48px; height: 48px; border: 0; border-radius: 50%; background: transparent; color: var(--re0-muted); }
.re0-icon-button svg { width: 24px; height: 24px; }
.re0-toolbar { display: flex; flex-shrink: 0; align-items: center; gap: 12px; padding: 0 24px 20px; }
.re0-search-field { display: flex; align-items: center; flex: 1; min-width: 0; gap: 10px; min-height: 48px; padding: 0 14px; border: 1px solid transparent; border-radius: 14px; background: var(--re0-card); color: var(--re0-muted); }
.re0-search-field:focus-within { border-color: var(--re0-primary); }
.re0-search-field svg { width: 20px; height: 20px; flex-shrink: 0; }
.re0-search-field input { width: 100%; min-width: 0; height: 46px; border: 0; padding: 0; outline: none; background: transparent; color: var(--re0-text); font-size: 13px; }
.re0-search-field input::placeholder { color: var(--re0-muted); opacity: .8; }
.re0-tools { display: flex; gap: 4px; flex-shrink: 0; }
.re0-tool-button { display: inline-flex; align-items: center; justify-content: center; gap: 7px; min-height: 48px; padding: 0 12px; border: 0; border-radius: 24px; background: transparent; color: var(--re0-muted); font-size: 12px; font-weight: 550; text-decoration: none; white-space: nowrap; }
.re0-tool-button svg { width: 18px; height: 18px; }
.re0-body { min-height: 0; padding: 0 24px 24px; overflow-y: auto; overscroll-behavior: contain; scrollbar-gutter: stable; }
.re0-results-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin: 0 0 12px; }
.re0-results-heading h3 { margin: 0; font-size: 13px; line-height: 20px; font-weight: 600; }
.re0-results-heading span { color: var(--re0-muted); font-size: 12px; }
.re0-resource-list { display: grid; gap: 12px; margin: 0; padding: 0; list-style: none; }
.re0-resource { min-width: 0; padding: 20px; border: 1px solid var(--search-dialog-card-border); border-radius: 20px; background: var(--re0-card); }
.re0-resource-active { border-color: var(--re0-primary); }
.re0-resource-main { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center; gap: 20px; }
.re0-resource-info { min-width: 0; }
.re0-version-heading { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.re0-version-heading h3 { margin: 0; font-size: 15px; line-height: 24px; font-weight: 650; overflow-wrap: anywhere; }
.re0-badge { display: inline-flex; flex-shrink: 0; align-items: center; gap: 4px; padding: 3px 8px; border-radius: 8px; font-size: 11px; line-height: 18px; font-weight: 550; }
.re0-badge svg { width: 14px; height: 14px; }
.re0-metrics { display: flex; flex-wrap: wrap; gap: 12px 22px; margin: 14px 0 0; }
.re0-metrics div { min-width: 0; }
.re0-metrics dt { color: var(--re0-muted); font-size: 11px; line-height: 16px; }
.re0-metrics dd { margin: 3px 0 0; font-size: 12px; line-height: 20px; font-weight: 550; overflow-wrap: anywhere; }
.re0-actions { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; }
.re0-resource-meta { display: flex; flex-wrap: wrap; gap: 4px 18px; margin-top: 16px; color: var(--re0-muted); font-size: 11px; line-height: 18px; overflow-wrap: anywhere; }
.re0-info { color: var(--re0-on-tonal); background: var(--re0-tonal); }
.re0-notice.re0-info { border: 1px solid color-mix(in srgb, var(--re0-outline) 45%, transparent); background: var(--re0-card); color: var(--re0-text); }
.re0-success { color: var(--re0-success); background: var(--re0-success-bg); }
.re0-warning { color: var(--re0-warning); background: var(--re0-warning-bg); }
.re0-error { color: var(--re0-error); background: var(--re0-error-bg); }
.re0-notice { margin: 12px 0; padding: 12px 14px; border-radius: 12px; font-size: 12px; line-height: 22px; overflow-wrap: anywhere; }
.re0-resource .re0-notice:last-child { margin-bottom: 0; }
.re0-selection { margin-top: 18px; padding-top: 18px; border-top: 1px solid var(--re0-outline); }
.re0-selection h4 { margin: 0; font-size: 14px; line-height: 22px; font-weight: 600; }
.re0-selection p { margin: 6px 0 0; color: var(--re0-muted); font-size: 12px; line-height: 22px; }
.re0-selection form { margin-top: 16px; }
.re0-selection fieldset { display: grid; gap: 16px; min-width: 0; margin: 0; padding: 0; border: 0; }
.re0-field { display: flex; flex-direction: column; gap: 8px; min-width: 0; color: var(--re0-muted); font-size: 12px; }
.re0-field select { width: 100%; min-width: 0; min-height: 48px; padding: 10px 12px; border: 1px solid var(--re0-outline); border-radius: 12px; background: var(--re0-surface); color: var(--re0-text); font-size: 13px; }
.re0-form-actions { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; margin-top: 16px; }
.re0-selection fieldset .re0-form-actions { margin-top: 0; }
.re0-text-button { min-height: 48px; padding: 10px 16px; border: 0; border-radius: 24px; background: transparent; color: var(--re0-primary); font-size: 12px; line-height: 20px; font-weight: 600; }
.re0-retry-confirmation { display: flex; align-items: center; gap: 10px; min-height: 48px; margin-top: 12px; padding: 10px 12px; border-radius: 12px; background: var(--re0-warning-bg); color: var(--re0-warning); font-size: 12px; line-height: 20px; }
.re0-retry-confirmation input { flex-shrink: 0; width: 18px; height: 18px; accent-color: var(--re0-primary); }
.re0-empty { display: flex; flex-direction: column; align-items: center; gap: 8px; padding: 48px 16px; color: var(--re0-muted); text-align: center; }
.re0-empty svg { width: 36px; height: 36px; margin-bottom: 8px; }
.re0-empty p { margin: 0; font-size: 14px; }
.re0-empty span { font-size: 12px; }
.re0-skeleton { display: grid; gap: 12px; }
.re0-skeleton div { display: grid; gap: 14px; padding: 24px; border-radius: 20px; background: var(--re0-card); }
.re0-skeleton span { height: 12px; width: 60%; border-radius: 6px; background: color-mix(in srgb, var(--re0-outline) 45%, transparent); }
.re0-skeleton span:first-child { height: 18px; width: 35%; }
.re0-skeleton span:last-child { width: 45%; }
.re0-dialog button { cursor: pointer; }
.re0-dialog button:disabled { cursor: default; opacity: .45; }
.re0-dialog input:disabled { opacity: .6; }
.re0-dialog button:focus-visible, .re0-dialog a:focus-visible, .re0-dialog select:focus-visible, .re0-dialog input[type="checkbox"]:focus-visible, .re0-resource:focus-visible { outline: 2px solid var(--re0-primary); outline-offset: 3px; }
.re0-spinning { animation: re0-spin 1s linear infinite; }
@keyframes re0-spin { to { transform: rotate(360deg); } }
@media (hover: hover) {
  .re0-icon-button:hover:not(:disabled), .re0-tool-button:hover:not(:disabled), .re0-text-button:hover:not(:disabled) { background: color-mix(in srgb, var(--re0-primary) 8%, transparent); }
}
@media (max-width: 799px) {
  .re0-resource-main { grid-template-columns: minmax(0, 1fr); gap: 16px; }
}
@media (max-width: 639px) {
  .re0-dialog { --re0-max-height: calc(min(100dvh, var(--re0-visible-height, 100dvh)) - env(safe-area-inset-top) - env(safe-area-inset-bottom) - 24px); inset-block: calc(var(--re0-visible-top, 0px) + env(safe-area-inset-top)) calc(var(--re0-visible-bottom, 0px) + env(safe-area-inset-bottom)); width: calc(100% - 24px); border-radius: 24px; }
  .re0-header { gap: 10px; padding: 16px 16px 12px; }
  .re0-heading h2 { font-size: 18px; line-height: 26px; }
  .re0-toolbar { flex-wrap: wrap; gap: 4px; padding: 0 16px 12px; }
  .re0-search-field { flex-basis: 100%; }
  .re0-search-field input { font-size: 16px; }
  .re0-tools { justify-content: flex-end; width: 100%; }
  .re0-body { padding: 0 16px 16px; scrollbar-gutter: auto; }
  .re0-resource { padding: 16px; border-radius: 18px; }
  .re0-actions { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .re0-metrics { gap: 10px 20px; }
  .re0-field select { font-size: 16px; }
}
@media (max-width: 359px) {
  .re0-actions { grid-template-columns: minmax(0, 1fr); }
}
@media (prefers-reduced-motion: reduce) {
  .re0-spinning { animation: none; }
}
</style>
