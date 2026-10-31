<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { useRoute } from 'vue-router'
import { portalToken, portalUser } from '../composables/usePortalAuth'
import { IconArrowLeft, IconChevronDown, IconExternalLink, IconPhotoOff, IconX } from '@tabler/icons-vue'
import type { AnimeCardItem, AnimeSubjectResult } from '../types/anime'
import { animeCooldownUntil, animeDetailState, animeFailure, animeImageURL, cachedAnime, rememberAnimeDetail, requestAnime } from '../composables/useAnimeDiscovery'

const props = defineProps<{ token: string; item: AnimeCardItem }>()
const emit = defineEmits<{ close: []; navigate: [item: AnimeCardItem] }>()
const route = useRoute()
const remembered = animeDetailState(props.item.id)
const ownerToken = props.token, ownerID = portalUser.value?.id
const dialog = ref<HTMLDialogElement>(), body = ref<HTMLElement>(), closeButton = ref<HTMLButtonElement>()
const headingID = useId(), relationsID = useId()
const current = ref(remembered?.current || props.item), history = ref<AnimeCardItem[]>(remembered?.history || [])
const details = ref<AnimeSubjectResult>(), related = ref<AnimeSubjectResult>()
const loading = ref(false), error = ref(''), relationsLoading = ref(false), relationsError = ref('')
const relationsOpen = ref(remembered?.relationsOpen || false), relationsLimit = ref(remembered?.relationsLimit || 12), synopsisOpen = ref(remembered?.synopsisOpen || false), imageFailed = ref(false)
const subject = computed(() => details.value?.subject || current.value)
const stale = computed(() => details.value && (details.value.metadata.stale || details.value.metadata.expiresAt <= Date.now()))
const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
const previousOverflow = document.body.style.overflow
let subjectController = new AbortController(), relationsController = new AbortController()
let disposed = false, backdropDown = false

async function loadSubject(force = false) {
  if (!props.token || loading.value) return
  const id = current.value.id
  subjectController.abort(); const active = new AbortController(); subjectController = active
  const key = `subject:${id}`
  const cached = cachedAnime<AnimeSubjectResult>(key)
  if (cached) details.value = cached.data
  if (cached?.fresh && !force) return
  loading.value = true; error.value = ''
  try {
    const result = await requestAnime<AnimeSubjectResult>(key, `subjects/${encodeURIComponent(id)}`, props.token, active.signal)
    if (!disposed && active === subjectController) details.value = result
  } catch (e) { if (!disposed && !active.signal.aborted) error.value = animeFailure(e) }
  finally { if (!disposed && active === subjectController) loading.value = false }
}
async function loadRelations(force = false) {
  if (!props.token || relationsLoading.value) return
  relationsOpen.value = true
  relationsController.abort(); const active = new AbortController(); relationsController = active
  const id = current.value.id, key = `relations:${id}`
  const cached = cachedAnime<AnimeSubjectResult>(key)
  if (cached) related.value = cached.data
  if (cached?.fresh && !force) return
  relationsLoading.value = true; relationsError.value = ''
  try {
    const result = await requestAnime<AnimeSubjectResult>(key, `subjects/${encodeURIComponent(id)}?includeRelations=true`, props.token, active.signal)
    if (!disposed && active === relationsController) related.value = result
  } catch (e) { if (!disposed && !active.signal.aborted) relationsError.value = animeFailure(e) }
  finally { if (!disposed && active === relationsController) relationsLoading.value = false }
}
async function select(item: AnimeCardItem, back = false) {
  if (item.id === current.value.id) return
  if (!back) { history.value.push(current.value); if (history.value.length > 32) history.value.shift() }
  subjectController.abort(); relationsController.abort()
  current.value = item; details.value = undefined; related.value = undefined
  error.value = ''; relationsError.value = ''; loading.value = false; relationsLoading.value = false
  relationsOpen.value = false; relationsLimit.value = 12; synopsisOpen.value = false; imageFailed.value = false
  emit('navigate', item)
  void loadSubject()
  await nextTick()
  body.value?.scrollTo({ top: 0 }); closeButton.value?.focus({ preventScroll: true })
}
function goBack() { const item = history.value.pop(); if (item) void select(item, true) }
function backdropClick(event: MouseEvent) {
  if (backdropDown && event.target === dialog.value) emit('close')
  backdropDown = false
}
function trapFocus(event: KeyboardEvent) {
  if (event.key !== 'Tab') return
  const elements = Array.from(dialog.value?.querySelectorAll<HTMLElement>('button:not(:disabled), a[href], [tabindex="0"]') || []).filter(element => element.getClientRects().length)
  const first = elements[0], last = elements.at(-1)
  if (!first || !last) { event.preventDefault(); return }
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
}
onMounted(() => {
  dialog.value?.showModal(); document.body.style.overflow = 'hidden'
  closeButton.value?.focus({ preventScroll: true })
  void loadSubject()
  if (relationsOpen.value) void loadRelations()
  void nextTick(() => body.value?.scrollTo({ top: remembered?.top || 0, behavior: 'instant' }))
})
onBeforeUnmount(() => {
  if (ownerToken === portalToken.value && ownerID === portalUser.value?.id && portalUser.value?.role === 'admin') rememberAnimeDetail({ current: current.value, history: history.value, relationsOpen: relationsOpen.value, relationsLimit: relationsLimit.value, synopsisOpen: synopsisOpen.value, top: body.value?.scrollTop || 0 })
  disposed = true; subjectController.abort(); relationsController.abort(); dialog.value?.close()
  document.body.style.overflow = previousOverflow
  if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true })
})
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" class="anime-detail floating-surface" :aria-labelledby="headingID" @cancel.prevent="emit('close')" @keydown="trapFocus" @pointerdown="backdropDown = $event.target === dialog" @click="backdropClick">
      <header class="anime-detail-toolbar">
        <button v-if="history.length" type="button" class="anime-quiet-button" aria-label="返回上一篇章" @click="goBack"><IconArrowLeft :size="18" aria-hidden="true" /><span>上一篇章</span></button>
        <span v-else class="text-xs text-slate-500 dark:text-slate-400">Bangumi 篇章资料</span>
        <button ref="closeButton" type="button" class="anime-icon-button" aria-label="关闭篇章详情" @click="emit('close')"><IconX :size="20" aria-hidden="true" /></button>
      </header>
      <div ref="body" class="anime-detail-body">
        <div class="anime-detail-heading">
          <div class="anime-detail-poster">
            <img v-if="animeImageURL(subject.coverUrl) && !imageFailed" :src="animeImageURL(subject.coverUrl)" :alt="subject.title" referrerpolicy="no-referrer" @error="imageFailed = true" />
            <IconPhotoOff v-else :size="26" aria-hidden="true" />
          </div>
          <div class="min-w-0">
            <h2 :id="headingID" class="text-xl font-semibold leading-7 text-slate-900 dark:text-slate-100">{{ subject.title }}</h2>
            <p v-if="subject.originalTitle && subject.originalTitle !== subject.title" class="mt-2 break-words text-xs leading-5 text-slate-500 dark:text-slate-400">{{ subject.originalTitle }}</p>
            <p class="mt-3 text-xs leading-6 text-slate-600 dark:text-slate-300">{{ subject.date ? `${subject.date} 首播` : '首播日期未收录' }}<span v-if="details?.subject.platform" class="ml-3">{{ details.subject.platform }}</span></p>
            <p class="text-xs leading-6 text-slate-500 dark:text-slate-400">Bangumi {{ subject.id }}<span v-if="details?.subject.totalEpisodes != null" class="ml-3">资料收录 {{ details.subject.totalEpisodes }} 话</span></p>
            <a :href="`https://bgm.tv/subject/${encodeURIComponent(subject.id)}`" target="_blank" rel="noopener noreferrer" class="anime-source-link">在 Bangumi 查看<IconExternalLink :size="14" aria-hidden="true" /><span class="sr-only">（新窗口）</span></a>
          </div>
        </div>
        <div class="anime-detail-status" role="status">
          <span v-if="loading">正在读取篇章资料…</span>
          <span v-else-if="error">{{ error }}</span>
          <span v-else-if="stale">当前为缓存资料，更新暂不可用。</span>
          <button v-if="error || stale" type="button" class="anime-text-button" :disabled="loading || !!animeCooldownUntil" @click="loadSubject(true)">重试资料</button>
        </div>
        <section class="anime-detail-section" aria-label="篇章简介" :aria-busy="loading">
          <h3>篇章简介</h3>
          <p v-if="details" class="anime-synopsis" :class="{ 'line-clamp-4': !synopsisOpen }">{{ details.subject.synopsis || 'Bangumi 暂未收录简介。' }}</p>
          <p v-else class="text-sm leading-7 text-slate-500 dark:text-slate-400">{{ loading ? '正在读取简介…' : '篇章资料尚未读取成功。' }}</p>
          <button v-if="(details?.subject.synopsis.length || 0) > 180" type="button" class="anime-text-button mt-2" :aria-expanded="synopsisOpen" @click="synopsisOpen = !synopsisOpen">{{ synopsisOpen ? '收起简介' : '展开简介' }}</button>
        </section>
        <section class="anime-detail-section">
          <button type="button" class="anime-relations-toggle" :aria-expanded="relationsOpen" :aria-controls="relationsID" @click="relationsOpen ? relationsOpen = false : loadRelations()">
            <span><span class="font-semibold">关联篇章</span><span class="ml-2 text-xs text-slate-500 dark:text-slate-400">前传、续作与其他关联</span></span>
            <IconChevronDown :size="18" aria-hidden="true" :class="{ 'rotate-180': relationsOpen }" />
          </button>
          <div v-if="relationsOpen" :id="relationsID" :aria-busy="relationsLoading" class="mt-3">
            <p v-if="relationsLoading" role="status" class="anime-detail-status">正在读取关联篇章…</p>
            <p v-if="relationsError" role="status" class="anime-detail-status">{{ relationsError }} <button type="button" class="anime-text-button" :disabled="relationsLoading || !!animeCooldownUntil" @click="loadRelations(true)">重试关联</button></p>
            <p v-if="related?.relations?.stale" class="anime-detail-status">关联篇章暂时显示缓存资料。</p>
            <div v-if="related?.relations?.items.length" class="anime-relations-list">
              <button v-for="item in related.relations.items.slice(0, relationsLimit)" :key="`${item.relation}:${item.id}`" type="button" :disabled="item.id === current.id" @click="select(item)">
                <span class="text-xs text-blue-600 dark:text-blue-400">{{ item.relation || '关联' }}</span>
                <span class="min-w-0 break-words text-sm font-medium">{{ item.title }}</span>
                <span class="text-xs text-slate-500 dark:text-slate-400">{{ item.date?.slice(0, 4) || '年份未收录' }}</span>
              </button>
            </div>
            <p v-else-if="related && !relationsLoading" class="anime-detail-status">Bangumi 暂未收录关联篇章。</p>
            <button v-if="(related?.relations?.items.length || 0) > relationsLimit" type="button" class="anime-text-button mt-3" @click="relationsLimit += 12">显示更多关联</button>
          </div>
        </section>
        <p class="anime-detail-footnote">以上为放送及篇章资料，不代表字幕组已发布或资源可下载。</p>
        <RouterLink :to="{ path: '/anime/new', query: { bangumiId: current.id, returnTo: route.fullPath } }" class="anime-configure-link">选择资源并配置订阅</RouterLink>
      </div>
    </dialog>
  </Teleport>
</template>

<style scoped>
.anime-detail { width: min(720px, calc(100vw - 32px)); max-height: min(860px, calc(100dvh - 48px)); padding: 0; margin: auto; border-radius: 22px; color: var(--floating-surface-text); }
.anime-detail[open] { display: flex; flex-direction: column; }
.anime-detail-toolbar { display: flex; flex: none; min-height: 60px; align-items: center; justify-content: space-between; gap: 12px; padding: 10px 18px 6px 24px; }
.anime-detail-body { min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: 6px 26px 26px; }
.anime-detail-heading { display: grid; grid-template-columns: 106px minmax(0, 1fr); align-items: start; gap: 22px; }
.anime-detail-poster { display: flex; width: 100%; aspect-ratio: 2 / 3; align-items: center; justify-content: center; overflow: hidden; border-radius: 12px; background: rgb(148 163 184 / .12); color: var(--floating-surface-muted); }
.anime-detail-poster img { width: 100%; height: 100%; object-fit: cover; }
.anime-detail-heading h2 { overflow-wrap: anywhere; }
.anime-source-link { display: inline-flex; min-height: 44px; align-items: center; gap: 6px; font-size: 12px; color: #2563eb; }
.dark .anime-source-link { color: #93c5fd; }
.anime-icon-button { display: inline-flex; width: 44px; height: 44px; align-items: center; justify-content: center; border-radius: 12px; }
.anime-icon-button:hover, .anime-quiet-button:hover { background: rgb(148 163 184 / .12); }
.anime-quiet-button { display: inline-flex; min-height: 44px; align-items: center; gap: 8px; border-radius: 10px; padding: 0 6px; font-size: 12px; }
.anime-detail-section { margin-top: 24px; }
.anime-detail-section h3 { margin-bottom: 10px; font-size: 14px; font-weight: 600; }
.anime-synopsis { white-space: pre-line; overflow-wrap: anywhere; font-size: 14px; line-height: 1.9; color: var(--floating-surface-muted); }
.anime-detail-status { display: flex; min-height: 28px; flex-wrap: wrap; align-items: center; gap: 8px; margin-top: 8px; font-size: 12px; line-height: 22px; color: var(--floating-surface-muted); }
.anime-text-button { display: inline-flex; min-height: 36px; align-items: center; color: #2563eb; font-size: 12px; }
.dark .anime-text-button { color: #93c5fd; }
.anime-text-button:disabled { opacity: .5; cursor: wait; }
.anime-relations-toggle { display: flex; width: 100%; min-height: 44px; align-items: center; justify-content: space-between; gap: 8px; text-align: left; font-size: 14px; }
.anime-relations-list { display: grid; gap: 6px; }
.anime-relations-list button { display: grid; grid-template-columns: 58px minmax(0, 1fr) auto; min-height: 52px; align-items: center; gap: 10px; padding: 10px 12px; border-radius: 12px; text-align: left; background: rgb(148 163 184 / .07); }
.anime-relations-list button:hover { background: rgb(96 165 250 / .12); }
.anime-relations-list button:disabled { opacity: .5; }
.anime-detail-footnote { margin-top: 30px; font-size: 12px; line-height: 22px; color: var(--floating-surface-muted); }
.anime-configure-link { display: flex; min-height: 44px; align-items: center; justify-content: center; padding: 0 18px; margin-top: 16px; border-radius: 12px; background: #2563eb; color: #fff; font-size: 13px; font-weight: 500; }
.anime-configure-link:hover { background: #1d4ed8; }
.anime-detail button:focus-visible, .anime-detail a:focus-visible { outline: 2px solid #60a5fa; outline-offset: 2px; }
@media (max-width: 639px) { .anime-detail { width: calc(100vw - 16px); max-height: calc(100dvh - 24px); border-radius: 18px; } .anime-detail-toolbar { padding-inline: 16px 10px; } .anime-detail-body { padding: 8px 18px max(22px, env(safe-area-inset-bottom)); } .anime-detail-heading { grid-template-columns: 76px minmax(0, 1fr); gap: 14px; } .anime-detail-heading h2 { font-size: 18px; } .anime-relations-list button { grid-template-columns: 48px minmax(0, 1fr); } .anime-relations-list button > :last-child { grid-column: 2; } }
</style>
