<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { nanShareRequest, type RE0Resource, type RE0SearchResult, type RE0ShareOptions, type RE0Submission, type RE0Transfer, type RE0TransferOptions } from '../types/nanshare'

const props = defineProps<{ token: string; kind: 'movie' | 'tv'; tmdbId: string; title: string }>()
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement>()
const selectionPanel = ref<HTMLElement>()
const lifecycle = new AbortController()
const previousFocus = document.activeElement as HTMLElement | null
const previousOverflow = document.body.style.overflow
const result = ref<RE0SearchResult>()
const error = ref('')
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
function label(item: RE0Resource) {
  const state = result.value?.submissions[item.slug]?.state
  if (selected.value?.slug === item.slug && (phase.value === 'options' || phase.value === 'submit')) return '提交中…'
  if (pendingRequests.value[item.slug]) return '重试获取提交结果'
  return state === 'accepted' ? '再次生成 STRM' : state === 'submitting' ? '提交中…' : state === 'unknown' ? '核对后重新提交' : state === 'failed' ? '重新提交' : '分享 STRM'
}
async function search(refresh = false) {
  if (phase.value) return
  phase.value = 'search'; error.value = ''; refreshNotice.value = ''; selected.value = undefined; options.value = undefined; transferSelected.value = undefined
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
  selected.value = item; options.value = undefined; mode.value = ''; folderIndex.value = ''; error.value = ''; phase.value = 'options'
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
  await nextTick(); selectionPanel.value?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
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
  phase.value = 'submit'; error.value = ''; refreshNotice.value = ''
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
  } finally { phase.value = '' }
}
function transferLabel(item: RE0Resource) {
  if (transferSelected.value?.slug === item.slug && (phase.value === 'transfer' || phase.value === 'transfer-options')) return '转存中…'
  if (transferPending.value[item.slug]) return '获取转存结果'
  const state = result.value?.transfers?.[item.slug]?.state
  return ({ accepted: '已转存', already_saved: '已转存过', unknown: '结果待核查', partial: '部分完成', submitting: '转存中…' } as Record<string, string>)[state || ''] || '一键转存'
}
function transferBlocked(item: RE0Resource) { return !!phase.value || ['accepted', 'already_saved', 'unknown', 'partial', 'submitting'].includes(result.value?.transfers?.[item.slug]?.state || '') }
async function prepareTransfer(item: RE0Resource) {
  if (transferBlocked(item) || !result.value) return
  selected.value = undefined; transferSelected.value = item; error.value = ''; transferOptions.value = undefined; transferFolder.value = ''
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
  if (!lifecycle.signal.aborted && transferOptions.value?.folders.length === 1) await submitTransfer()
}
async function submitTransfer() {
  if (phase.value || !result.value || !transferSelected.value || !transferOptions.value || !transferFolder.value) return
  const requestId = Array.from(crypto.getRandomValues(new Uint8Array(16)), value => value.toString(16).padStart(2, '0')).join('')
  await sendTransfer({ requestId, searchId: result.value.id, slug: transferSelected.value.slug, accountId: transferOptions.value.accountId, folderId: transferFolder.value })
}
async function sendTransfer(input: TransferInput) {
  if (phase.value || !result.value) return
  transferPending.value[input.slug] = input
  phase.value = 'transfer'; error.value = ''
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
  } finally { phase.value = ''; transferSelected.value = undefined }
}
function close() { if (!writing.value) emit('close') }
onMounted(() => { document.body.style.overflow = 'hidden'; dialog.value?.showModal(); void search() })
onBeforeUnmount(() => { lifecycle.abort(); dialog.value?.close(); document.body.style.overflow = previousOverflow; previousFocus?.focus() })
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" aria-labelledby="re0-title" class="m-auto max-h-[90dvh] w-[calc(100%-1.5rem)] max-w-4xl overflow-hidden rounded-2xl border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-200 p-0 shadow-2xl backdrop:bg-slate-950/40" @cancel.prevent="close">
      <div class="flex max-h-[90dvh] flex-col">
        <header class="flex shrink-0 items-start justify-between gap-4 border-b border-slate-100 dark:border-slate-800 p-5"><div class="min-w-0"><h2 id="re0-title" class="text-lg font-semibold text-slate-900 dark:text-slate-100">RE0 搜索</h2><p class="mt-1 break-words text-sm text-slate-500 dark:text-slate-400">{{ title }} · 115 资源</p></div><button autofocus type="button" aria-label="关闭 RE0 搜索" :disabled="writing" class="rounded-lg px-2 py-1 text-xl text-slate-500 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-slate-800 dark:bg-slate-800 disabled:opacity-30" @click="close">×</button></header>
        <div class="min-h-0 overflow-y-auto p-4 sm:p-5">
          <div class="mb-4 flex flex-wrap items-center gap-3"><span class="rounded-lg bg-blue-50 px-3 py-2 text-xs font-medium text-blue-700 dark:bg-blue-400/10 dark:text-blue-300">RE0 · 115 <template v-if="result">({{ result.items.length }})</template></span><button type="button" :disabled="!!phase" class="text-xs text-blue-600 dark:text-blue-300 hover:text-blue-500 dark:hover:text-blue-200 disabled:opacity-40" @click="search(true)">刷新搜索</button><RouterLink to="/nanshare" class="ml-auto text-xs text-slate-500 dark:text-slate-400 hover:text-blue-600 dark:hover:text-slate-200" @click="emit('close')">登录配置</RouterLink></div>
          <p v-if="error" role="alert" class="mb-4 break-words rounded-xl bg-red-50 p-4 text-sm leading-6 text-red-700 dark:bg-red-400/10 dark:text-red-300">{{ error }}</p>
          <p v-if="refreshNotice" role="status" class="mb-4 rounded-xl bg-amber-50 p-4 text-sm text-amber-700 dark:bg-amber-400/10 dark:text-amber-200">{{ refreshNotice }}</p>
          <p v-if="phase === 'search'" role="status" class="mb-4 rounded-xl bg-blue-50 p-4 text-sm text-blue-700 dark:bg-blue-400/10 dark:text-blue-300">正在搜索 RE0 资源…</p>
          <p v-if="writing" role="status" class="mb-4 rounded-xl bg-blue-50 p-4 text-sm text-blue-700 dark:bg-blue-500/10 dark:text-blue-300">{{ phase === 'transfer' ? '正在通过 NanShare 转存，请等待转存结果…' : '正在提交给 NanShare，请等待提交结果…' }}</p>
          <section v-if="transferSelected && transferOptions && transferOptions.folders.length > 1 && !writing" aria-label="一键转存目录" class="mb-5 rounded-xl border border-blue-200 bg-blue-50/50 p-4 dark:border-blue-500/30 dark:bg-blue-500/10">
            <h3 class="text-sm font-semibold text-slate-900 dark:text-slate-100">转存 · {{ name(transferSelected) }}</h3>
            <p class="mt-2 text-xs text-slate-600 dark:text-slate-400">{{ points(transferSelected) }} · 使用 NanShare 的 115 转存助手配置，积分限制由 NanShare 校验。</p>
            <form class="mt-3 flex flex-wrap gap-3" @submit.prevent="submitTransfer"><select v-model="transferFolder" aria-label="转存目录" required class="min-w-0 flex-1 rounded-lg border border-slate-200 bg-white p-2.5 text-sm text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200"><option disabled value="">请选择转存目录</option><option v-for="folder in transferOptions.folders" :key="folder.id" :value="folder.id">{{ folder.name || folder.path || folder.id }}</option></select><button type="submit" :disabled="!transferFolder || !!phase" class="rounded-lg bg-blue-600 px-4 py-2.5 text-sm text-white disabled:opacity-40">开始转存</button><button type="button" class="text-sm text-slate-500 dark:text-slate-400" @click="transferSelected = undefined">取消</button></form>
          </section>
          <section v-if="selected && (needsSelection || needsRetryConfirmation) && phase !== 'submit'" ref="selectionPanel" aria-label="分享 STRM 配置" class="mb-5 rounded-xl border border-emerald-200 bg-emerald-50/50 p-4 dark:border-emerald-400/20 dark:bg-emerald-400/5">
            <h3 class="break-words text-sm font-semibold text-slate-900 dark:text-slate-100">分享 STRM · {{ name(selected) }}</h3>
            <p class="mt-2 text-xs leading-6 text-slate-600 dark:text-slate-400">{{ points(selected) }}{{ !selected.is_unlocked && (selected.unlock_points === null || selected.unlock_points > 0) ? '，提交后由 NanShare 按 RE0 规则解锁。' : '。' }}文件生成到 NanShare 的输出目录。</p>
            <p v-if="phase === 'options'" role="status" class="mt-3 text-sm text-slate-500 dark:text-slate-400">读取分享 STRM 配置…</p>
            <label v-if="needsRetryConfirmation" class="mt-3 flex items-start gap-2 text-sm text-amber-800 dark:text-amber-200"><input v-model="retryConfirmed" type="checkbox" class="mt-1"/>我已核对 NanShare 任务，仍需重新提交</label>
            <form v-if="options" class="mt-4" @submit.prevent="submit"><fieldset :disabled="!!phase" class="grid min-w-0 gap-4 sm:grid-cols-2">
              <label class="min-w-0 text-xs text-slate-600 dark:text-slate-400">生成模式<select v-model="mode" aria-label="生成模式" required class="mt-2 w-full rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-2.5 text-sm"><option disabled value="">请选择模式</option><option v-for="value in options.modes" :key="value" :value="value">{{ value === 'reshare' ? '转存再分享' : '普通分享' }}</option></select></label>
              <label class="min-w-0 text-xs text-slate-600 dark:text-slate-400 sm:col-span-2">输出目录<select v-model="folderIndex" aria-label="输出目录" required class="mt-2 w-full rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-2.5 text-sm"><option disabled value="">请选择输出目录</option><option v-for="folder in options.folders" :key="folder.index" :value="String(folder.index)">{{ folder.name || '输出目录' }} · {{ folder.path }}</option></select></label>
              <div class="flex gap-4 sm:col-span-2"><button type="submit" :disabled="!canSubmit" class="rounded-lg bg-emerald-600 px-4 py-2.5 text-sm text-white disabled:opacity-40">提交分享 STRM</button><button type="button" class="text-sm text-slate-500 dark:text-slate-400" @click="selected = undefined">取消</button></div>
            </fieldset></form>
          </section>
          <label v-if="result?.items.length" class="mb-4 block text-xs text-slate-500 dark:text-slate-400">筛选当前结果<input v-model="query" type="search" placeholder="版本名称、清晰度、发布者" class="mt-2 w-full rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950 p-3 text-sm text-slate-900 dark:text-slate-200 placeholder:text-slate-400 dark:placeholder:text-slate-500 outline-none focus:border-blue-500 dark:focus:border-blue-400"/></label>
          <p v-if="result && !result.items.length && phase !== 'search'" role="status" class="py-12 text-center text-sm text-slate-500 dark:text-slate-400">RE0 没有找到该影视的 115 资源。</p>
          <p v-else-if="result?.items.length && !filtered.length" class="py-10 text-center text-sm text-slate-500 dark:text-slate-400">没有符合筛选条件的资源。</p>
          <ul v-if="filtered.length" aria-label="RE0 资源列表" class="space-y-3"><li v-for="item in filtered" :key="item.slug" class="rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-950/40 p-4">
            <div class="flex flex-wrap items-center gap-2 text-[11px]"><span class="rounded-full bg-blue-50 px-2 py-1 text-blue-700 dark:bg-blue-400/10 dark:text-blue-300">115</span><span data-unlock-status class="inline-flex items-center gap-1 rounded-full px-2 py-1" :class="item.is_unlocked ? 'bg-blue-50 text-blue-600 dark:bg-blue-400/10 dark:text-blue-300' : item.unlock_points === 0 ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-400/10 dark:text-emerald-300' : 'bg-amber-50 text-amber-700 dark:bg-amber-400/10 dark:text-amber-200'"><svg aria-hidden="true" class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="5" y="10" width="14" height="11" rx="2"/><path v-if="item.is_unlocked || item.unlock_points === 0" d="M8 10V6a4 4 0 0 1 8 0"/><path v-else d="M8 10V6a4 4 0 0 1 8 0v4"/><path v-if="item.is_unlocked" d="m9 15 2 2 4-4"/><path v-else d="M12 14v3"/></svg>{{ points(item) }}</span><span v-if="item.is_official" class="text-amber-600 dark:text-amber-200">官组</span><span v-for="resolution in item.video_resolution || []" :key="resolution" class="rounded border border-slate-300 dark:border-slate-600 px-2 py-0.5 text-slate-600 dark:text-slate-400">{{ resolution }}</span><span v-if="item.validate_status === 'invalid'" class="text-red-600 dark:text-red-300">链接可能失效</span></div>
            <div class="mt-3 flex flex-wrap items-start justify-between gap-3"><h3 class="min-w-0 flex-1 basis-48 break-words text-sm font-semibold leading-6 text-slate-900 dark:text-slate-100">{{ name(item) }}</h3><div class="flex flex-wrap gap-2"><button type="button" :disabled="transferBlocked(item)" class="shrink-0 rounded-lg bg-blue-50 px-3 py-2 text-xs font-medium text-blue-700 dark:bg-blue-400/10 dark:text-blue-300 hover:bg-blue-100 disabled:opacity-50 dark:bg-blue-500/10 dark:text-blue-300 dark:hover:bg-blue-500/20" @click="prepareTransfer(item)">{{ transferLabel(item) }}</button><button type="button" :disabled="!!phase || result?.submissions[item.slug]?.state === 'submitting'" class="shrink-0 rounded-lg bg-emerald-50 px-3 py-2 text-xs font-medium text-emerald-700 hover:bg-emerald-100 disabled:opacity-50 dark:bg-emerald-500/10 dark:text-emerald-300 dark:hover:bg-emerald-500/20" @click="prepare(item)">{{ label(item) }}</button></div></div>
            <div class="mt-3 flex flex-wrap gap-x-3 gap-y-2 break-words text-xs text-slate-500 dark:text-slate-400"><span v-if="item.share_size" class="text-emerald-700 dark:text-emerald-300">{{ item.share_size }}</span><span v-if="item.resource_sources?.length">{{ item.resource_sources.join(' / ') }}</span><span v-if="item.publisher">{{ item.publisher }}</span><span v-if="item.publish_time">{{ item.publish_time }}</span><span v-if="item.unlocked_users_count">{{ item.unlocked_users_count }} 人解锁</span><span v-if="item.subtitle_language?.length || item.subtitle_type?.length" class="text-amber-700 dark:text-amber-200">{{ [...(item.subtitle_language || []), ...(item.subtitle_type || [])].join(' / ') }}</span></div>
            <p v-if="result?.submissions[item.slug]" role="status" class="mt-3 break-words text-xs leading-6" :class="result.submissions[item.slug]?.state === 'accepted' ? 'text-emerald-700 dark:text-emerald-300' : 'text-amber-700 dark:text-amber-200'">{{ result.submissions[item.slug]?.message }}</p>
            <p v-if="result?.transfers?.[item.slug]" role="status" class="mt-2 break-words text-xs leading-6" :class="result.transfers[item.slug]?.state === 'accepted' ? 'text-emerald-700 dark:text-emerald-300' : 'text-amber-700 dark:text-amber-300'">{{ result.transfers[item.slug]?.message }}</p>
          </li></ul>
        </div>
      </div>
    </dialog>
  </Teleport>
</template>
