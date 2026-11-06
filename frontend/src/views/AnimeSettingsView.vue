<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { IconArrowLeft, IconBell, IconBookmark, IconFolder, IconRss } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import AnimeStorageSettingsDialog from '../components/AnimeStorageSettingsDialog.vue'
import AnimeNotificationSettings from '../components/AnimeNotificationSettings.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import { AnimeConfigError, animeConfigMessage, animeConfigRequest, animeRequestKey } from '../lib/anime-config-api'
import { portalUser } from '../composables/usePortalAuth'
import { animeReturnPath, invalidateAnimeSubscriptions } from '../composables/useAnimeSubscriptionBrowser'
import '../styles/animeSubscriptionEditor.css'

interface RSSSettings { revision: number; pollMinutes: number; autoArchive: boolean; inactiveArchiveDays: number }
const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const route = useRoute(), router = useRouter()
const tabOrder = ['rss', 'storage', 'notifications'] as const
type TabKey = typeof tabOrder[number]
const tab = computed<TabKey>(() => route.query.tab === 'notifications' ? 'notifications' : route.query.tab === 'storage' ? 'storage' : 'rss')
const notificationsVisited = ref(tab.value === 'notifications'), notificationBlocked = ref(false), notificationDirty = ref(false)
const tabsNavRef = ref<HTMLElement>()
let tabsResizeObserver: ResizeObserver | undefined

function scrollTabIntoView(tabKey: TabKey | string, smooth = true) {
  const container = tabsNavRef.value
  if (!container) return
  const btn = container.querySelector<HTMLElement>(`[data-tab="${tabKey}"]`)
  if (!btn) return

  const containerRect = container.getBoundingClientRect()
  const btnRect = btn.getBoundingClientRect()
  if (containerRect.width === 0 || btnRect.width === 0) return

  const padding = 6
  let delta = 0
  if (btnRect.left < containerRect.left + padding) {
    delta = btnRect.left - (containerRect.left + padding)
  } else if (btnRect.right > containerRect.right - padding) {
    delta = btnRect.right - (containerRect.right - padding)
  }

  if (Math.abs(delta) < 1) return

  const prefersReduced = typeof window !== 'undefined' &&
    window.matchMedia?.('(prefers-reduced-motion: reduce)')?.matches === true
  const behavior: ScrollBehavior = smooth && !prefersReduced ? 'smooth' : 'auto'

  if (typeof container.scrollBy === 'function') {
    container.scrollBy({ left: delta, behavior })
  } else {
    container.scrollLeft += delta
  }
}

watch(tab, value => {
  if (value === 'notifications') notificationsVisited.value = true
  void nextTick(() => scrollTabIntoView(value))
})

onMounted(() => {
  void nextTick(() => scrollTabIntoView(tab.value, false))
  if (typeof ResizeObserver !== 'undefined' && tabsNavRef.value) {
    tabsResizeObserver = new ResizeObserver(() => {
      scrollTabIntoView(tab.value, false)
    })
    tabsResizeObserver.observe(tabsNavRef.value)
  }
})

const returnTo = computed(() => animeReturnPath(route.query.returnTo) || '/anime')
const data = ref<RSSSettings>(), minutes = ref('15'), archive = ref(true), inactive = ref(60)
const loading = ref(false), saving = ref(false), error = ref(''), notice = ref(''), baseline = ref(''), directoryBlocked = ref(false)
const pending = ref<{ key: string; body: RSSSettings }>(), discard = ref(false)
const discardReload = ref(false)
let controller = new AbortController(), generation = 0, leaveDecision: ((value: boolean) => void) | undefined
const signature = () => JSON.stringify([minutes.value, archive.value, inactive.value])
const dirty = computed(() => !!baseline.value && signature() !== baseline.value)
const locked = computed(() => loading.value || saving.value || !!pending.value)
const valid = computed(() => /^\d+$/.test(minutes.value) && Number(minutes.value) >= 10 && Number(minutes.value) <= 1440)
const hasUnsaved = computed(() => dirty.value || notificationDirty.value)
const unsavedSummary = computed(() => {
  if (dirty.value && notificationDirty.value) return 'RSS 设置与 Telegram 通知有未保存修改'
  if (dirty.value) return 'RSS 设置有未保存修改'
  if (notificationDirty.value) return 'Telegram 通知有未保存修改'
  return ''
})

function chooseTab(value: TabKey) {
  if (directoryBlocked.value || notificationBlocked.value || saving.value || pending.value) { notice.value = '保存结果尚未确认，请先核对原请求。'; return }
  void router.replace({ query: { ...route.query, tab: value === 'rss' ? undefined : value } })
}

function onTabsKeydown(event: KeyboardEvent) {
  if (event.key !== 'ArrowRight' && event.key !== 'ArrowLeft' && event.key !== 'Home' && event.key !== 'End') {
    return
  }

  const container = tabsNavRef.value
  if (!container) return

  const focusedBtn = (event.target as HTMLElement | null)?.closest<HTMLButtonElement>('button[data-tab]')
  const focusedKey = (focusedBtn?.getAttribute('data-tab') as TabKey | null) ?? tab.value
  const currentIndex = tabOrder.indexOf(focusedKey)
  if (currentIndex === -1) return

  let targetIndex = -1
  if (event.key === 'ArrowRight') {
    event.preventDefault()
    targetIndex = (currentIndex + 1) % tabOrder.length
  } else if (event.key === 'ArrowLeft') {
    event.preventDefault()
    targetIndex = (currentIndex - 1 + tabOrder.length) % tabOrder.length
  } else if (event.key === 'Home') {
    event.preventDefault()
    targetIndex = 0
  } else if (event.key === 'End') {
    event.preventDefault()
    targetIndex = tabOrder.length - 1
  }

  if (targetIndex < 0) return

  const nextTab = tabOrder[targetIndex]
  const targetBtn = container.querySelector<HTMLButtonElement>(`button[data-tab="${nextTab}"]`)
  const isBlocked = directoryBlocked.value || notificationBlocked.value || saving.value || !!pending.value

  if (nextTab !== tab.value) {
    if (isBlocked) {
      notice.value = '保存结果尚未确认，请先核对原请求。'
      return
    }
    chooseTab(nextTab)
  }

  targetBtn?.focus({ preventScroll: true })
  scrollTabIntoView(nextTab)
}
async function read() {
  const current = ++generation
  controller.abort(); controller = new AbortController()
  loading.value = true; error.value = ''; notice.value = ''; pending.value = undefined
  try {
    const result = await animeConfigRequest<RSSSettings>(props.token, 'rss-settings', { signal: controller.signal })
    if (current !== generation) return
    data.value = result; minutes.value = String(result.pollMinutes); archive.value = result.autoArchive; inactive.value = result.inactiveArchiveDays; baseline.value = signature()
  } catch (reason) { if (current === generation) error.value = animeConfigMessage(reason) }
  finally { if (current === generation) loading.value = false }
}
async function save() {
  if (locked.value || !data.value || !dirty.value || !valid.value) return
  pending.value = { key: animeRequestKey(), body: { revision: data.value.revision, pollMinutes: Number(minutes.value), autoArchive: archive.value, inactiveArchiveDays: inactive.value } }
  await confirmPending()
}
async function confirmPending() {
  const request = pending.value
  if (!request || saving.value) return
  const current = generation
  saving.value = true; error.value = ''; notice.value = ''
  try {
    const result = await animeConfigRequest<RSSSettings>(props.token, 'rss-settings', { method: 'PATCH', ...request, signal: controller.signal })
    if (current !== generation) return
    if (!Number.isSafeInteger(result.revision) || result.revision !== request.body.revision + 1 || result.pollMinutes !== request.body.pollMinutes || result.autoArchive !== request.body.autoArchive || result.inactiveArchiveDays !== request.body.inactiveArchiveDays) throw new AnimeConfigError('保存回执尚未确认，请核对原请求', '', 0, {}, true)
    data.value = result; pending.value = undefined; baseline.value = signature(); notice.value = 'RSS 设置已保存'; invalidateAnimeSubscriptions()
  } catch (reason) {
    if (current !== generation) return
    error.value = animeConfigMessage(reason)
    if (!(reason instanceof AnimeConfigError && reason.uncertain)) pending.value = undefined
  } finally { if (current === generation) saving.value = false }
}
function reload() {
  if (locked.value) return
  if (!dirty.value) { void read(); return }
  discardReload.value = true; discard.value = true
}
function decideLeave(value: boolean) {
  discard.value = false
  if (discardReload.value) { discardReload.value = false; if (value) void read(); return }
  leaveDecision?.(value); leaveDecision = undefined
}
onBeforeRouteUpdate(() => {
  if (saving.value || pending.value || directoryBlocked.value || notificationBlocked.value) { notice.value = '保存结果尚未确认，请先核对原请求。'; return false }
  return true
})
onBeforeRouteLeave(() => {
  if (saving.value || pending.value || directoryBlocked.value || notificationBlocked.value) { notice.value = '保存结果尚未确认，请先核对原请求。'; return false }
  if (!dirty.value) return true
  discardReload.value = false; discard.value = true
  return new Promise<boolean>(resolve => { leaveDecision?.(false); leaveDecision = resolve })
})
function unload(event: BeforeUnloadEvent) { if (dirty.value || saving.value || pending.value || directoryBlocked.value) event.preventDefault() }
window.addEventListener('beforeunload', unload)
watch([() => portalUser.value?.id, () => portalUser.value?.role], () => { data.value = undefined; baseline.value = ''; pending.value = undefined; void read() }, { immediate: true })
onBeforeUnmount(() => {
  tabsResizeObserver?.disconnect()
  tabsResizeObserver = undefined
  generation++
  controller.abort()
  window.removeEventListener('beforeunload', unload)
  leaveDecision?.(false)
})
</script>

<template>
  <ManagementLayout class="ars-layout" title="动漫订阅设置" description="统一管理 RSS 检查、自动归档、下载整理目录与 Telegram 通知。" @logout="$emit('logout')">
    <template #actions><RouterLink :to="returnTo" class="ars-link"><IconArrowLeft :size="17" aria-hidden="true" />返回选番</RouterLink><RouterLink to="/anime/subscriptions" class="ars-link"><IconBookmark :size="17" aria-hidden="true" />我的订阅</RouterLink></template>
    <div class="anime-editor ars-settings">
      <nav ref="tabsNavRef" class="ars-tabs" aria-label="动漫设置分类" @keydown="onTabsKeydown">
        <button
          type="button"
          data-tab="rss"
          :aria-current="tab === 'rss' ? 'page' : undefined"
          :disabled="directoryBlocked || notificationBlocked || !!pending || saving"
          @click="chooseTab('rss')"
        >
          <IconRss :size="18" aria-hidden="true" />
          <span>RSS 设置</span>
          <span v-if="dirty" class="ars-dot" aria-hidden="true" title="RSS 设置有未保存修改"></span>
        </button>
        <button
          type="button"
          data-tab="storage"
          :aria-current="tab === 'storage' ? 'page' : undefined"
          :disabled="directoryBlocked || notificationBlocked || !!pending || saving"
          @click="chooseTab('storage')"
        >
          <IconFolder :size="18" aria-hidden="true" />
          <span>下载与整理</span>
        </button>
        <button
          type="button"
          data-tab="notifications"
          :aria-current="tab === 'notifications' ? 'page' : undefined"
          :disabled="directoryBlocked || notificationBlocked || !!pending || saving"
          @click="chooseTab('notifications')"
        >
          <IconBell :size="18" aria-hidden="true" />
          <span>Telegram 通知</span>
          <span v-if="notificationDirty" class="ars-dot" aria-hidden="true" title="Telegram 通知有未保存修改"></span>
        </button>
        <div v-if="hasUnsaved" class="ars-tabs-unsaved" role="status" :title="unsavedSummary">
          <span class="ars-dot ars-dot--amber" aria-hidden="true"></span>
          <span>有未保存修改</span>
        </div>
      </nav>
      <section v-show="tab === 'rss'" class="ars-panel" aria-labelledby="rss-settings-heading" :aria-busy="loading || saving">
        <header class="ars-heading"><h2 id="rss-settings-heading">RSS 设置</h2><p>所有订阅共用，修改后保存生效。</p></header>
        <p v-if="loading && !data" role="status" class="ae-note">正在读取设置…</p>
        <form v-if="data" class="ars-form" @submit.prevent="save">
          <div class="ars-form-layout">
            <div class="ars-form-main">
              <div class="ars-row"><div><label for="anime-rss-minutes">更新检查间隔</label><p id="anime-rss-minutes-help">每个启用来源按此间隔检查，避免频繁请求源站。</p></div><div class="ars-number"><input id="anime-rss-minutes" v-model="minutes" type="text" inputmode="numeric" required :disabled="locked" :aria-invalid="!valid" aria-describedby="anime-rss-minutes-help" /><span>分钟</span></div></div>
              <p v-if="!valid" role="status" class="ae-note">检查间隔请填写 10 至 1440 分钟的整数。</p>
              <div class="ars-row"><div><label for="anime-archive-complete">齐集整理后自动归档</label><p>所有启用字幕组的目标集数都整理成功后，停止 RSS 并保留记录。</p></div><input id="anime-archive-complete" v-model="archive" class="ars-switch" type="checkbox" role="switch" :disabled="locked" /></div>
              <div class="ars-row"><div><h3 id="anime-archive-inactive">长期停更归档</h3><p>所有启用来源持续没有新匹配资源时，归档为长期停更。</p></div><fieldset class="ars-options" aria-labelledby="anime-archive-inactive" :disabled="locked"><label v-for="option in [{ value: 60, label: '60 天' }, { value: 90, label: '90 天' }, { value: 0, label: '关闭' }]" :key="option.value" :class="{ selected: inactive === option.value }"><input v-model="inactive" type="radio" name="anime-inactive-days" :value="option.value" />{{ option.label }}</label><label v-if="![0, 60, 90].includes(inactive)" class="selected"><input v-model="inactive" type="radio" name="anime-inactive-days" :value="inactive" />{{ inactive }} 天</label></fieldset></div>
            </div>
            <aside class="ars-notes" aria-label="RSS 归档规则说明">
              <h3 class="ars-notes-title">归档规则说明</h3>
              <p>14 天无新匹配资源时显示提示；30 天后每日检查一次。</p>
              <p>RSS 读取失败不作为停更依据。有未完成或待确认任务时，继续处理。</p>
              <p>篇章总集数与发布编号起点可在单部订阅中修正。</p>
            </aside>
          </div>
          <footer class="ars-footer"><span class="ae-note">{{ dirty ? '有未保存的修改' : '当前配置已保存' }}</span><button type="submit" class="ae-button ae-primary" :disabled="locked || !dirty || !valid">{{ saving ? '保存中…' : '保存 RSS 设置' }}</button></footer>
        </form>
        <div class="ars-feedback" role="status" aria-live="polite"><p v-if="error">{{ error }}</p><p v-else-if="notice">{{ notice }}</p><button v-if="pending" type="button" class="ae-button" :disabled="saving" @click="confirmPending">核对原保存请求</button><button v-else-if="error" type="button" class="ae-button" :disabled="loading || saving" @click="reload">重新读取设置</button><p v-if="error && dirty && !pending" class="ae-note">输入已保留。可调整后重试，或重新读取最新设置。</p></div>
      </section>
      <section v-if="tab === 'storage'" class="ars-panel" aria-labelledby="storage-settings-heading">
        <div class="ars-panel-constrained">
          <header class="ars-heading"><h2 id="storage-settings-heading">下载与整理</h2><p>先下载到暂存目录，完成命名并移入整理目录后，清理任务临时目录。</p></header>
          <AnimeStorageSettingsDialog :token="token" inline @blocked="directoryBlocked = $event" />
        </div>
      </section>
      <section v-if="notificationsVisited" v-show="tab === 'notifications'" class="ars-panel ars-panel--wide" aria-label="Telegram 通知设置"><AnimeNotificationSettings :token="token" @blocked="notificationBlocked = $event" @dirty="notificationDirty = $event" /></section>
      <p v-if="tab !== 'rss' && notice" class="ae-note" role="status">{{ notice }}</p>
    </div>
    <P115ActionDialog v-if="discard" title="放弃未保存的 RSS 修改？" @close="decideLeave(false)"><p class="ars-discard">RSS 设置尚未保存。已单独保存的目录不会受影响。</p><template #footer><div class="anime-editor ars-discard-actions"><button type="button" class="ae-button" @click="decideLeave(false)">继续编辑</button><button type="button" class="ae-button ae-primary" @click="decideLeave(true)">{{ discardReload ? '放弃修改并重新读取' : '放弃修改并离开' }}</button></div></template></P115ActionDialog>
  </ManagementLayout>
</template>

<style scoped>
.ars-settings {
  --ae-blue: #2563eb;
  display: flex;
  flex-direction: column;
  gap: 18px;
  width: 100%;
  max-width: 100%;
  margin: 0;
  align-items: stretch;
}

.ars-tabs {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 6px;
  border-radius: 14px;
  background: var(--ae-surface);
  border: 1px solid rgb(148 163 184 / 0.12);
  box-shadow: 0 2px 12px rgb(15 23 42 / 0.03);
  overflow-x: auto;
  overflow-y: hidden;
  white-space: nowrap;
  flex-wrap: nowrap;
  scrollbar-width: none;
  scroll-snap-type: x proximity;
  -webkit-overflow-scrolling: touch;
}

.ars-tabs::-webkit-scrollbar {
  display: none;
}

.ars-tabs button {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex: 0 0 auto;
  min-height: 40px;
  padding: 8px 18px;
  border-radius: 10px;
  text-align: left;
  font-size: 13px;
  font-weight: 500;
  color: var(--ae-muted);
  scroll-snap-align: start;
  transition: background-color 0.15s, color 0.15s, box-shadow 0.15s;
}

@media (hover: hover) {
  .ars-tabs button:not([aria-current]):not(:disabled):hover {
    background: rgb(148 163 184 / 0.08);
    color: var(--ae-ink);
  }
}

.ars-tabs button[aria-current] {
  background: rgb(59 130 246 / 0.1);
  color: var(--ae-blue);
  font-weight: 600;
}

.dark .ars-tabs button[aria-current] {
  background: rgb(59 130 246 / 0.16);
  color: var(--ae-blue);
}

.ars-tabs button:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.ars-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #f59e0b;
  flex: none;
  box-shadow: 0 0 0 2px rgb(245 158 11 / 0.2);
}

.ars-dot--amber {
  background: #f59e0b;
}

.ars-tabs-unsaved {
  margin-left: auto;
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  border-radius: 8px;
  font-size: 12px;
  font-weight: 500;
  color: #b45309;
  background: rgb(245 158 11 / 0.1);
  white-space: nowrap;
}

.dark .ars-tabs-unsaved {
  color: #fbbf24;
  background: rgb(245 158 11 / 0.16);
}

.ars-panel {
  min-width: 0;
  width: 100%;
  max-width: 100%;
  padding: 26px 30px;
  border-radius: 18px;
  background: var(--ae-surface);
  border: 1px solid rgb(148 163 184 / 0.12);
  box-shadow: 0 4px 22px rgb(15 23 42 / 0.04);
}

/* 通知工作台释放全宽，吃满 1600px 主容器全部空间 */
.ars-panel--wide {
  max-width: 100%;
  padding: 24px 28px;
}

.ars-panel-constrained {
  width: 100%;
  max-width: 920px;
}

.ars-heading {
  margin-bottom: 8px;
}

.ars-heading h2 {
  font-size: 17px;
  font-weight: 600;
  color: var(--ae-ink);
}

.ars-heading p {
  margin-top: 7px;
  font-size: 12px;
  line-height: 1.7;
  color: var(--ae-muted);
}

.ars-form {
  container-type: inline-size;
  min-width: 0;
  width: 100%;
}

.ars-form-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 20px;
}

.ars-form-main {
  min-width: 0;
  max-width: 860px;
}

@container (min-width: 880px) {
  .ars-form-layout {
    grid-template-columns: minmax(0, 1fr) 320px;
    gap: 36px;
    align-items: start;
  }
  .ars-form-main {
    max-width: none;
  }
  .ars-notes {
    margin-top: 14px;
    position: sticky;
    top: calc(var(--workbench-header-height, 64px) + 20px);
  }
}

.ars-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  padding: 22px 0;
}

.ars-row > div:first-child {
  min-width: 0;
}

.ars-row label,
.ars-row h3 {
  font-size: 13px;
  font-weight: 500;
  color: var(--ae-ink);
}

.ars-row p {
  margin-top: 6px;
  font-size: 12px;
  line-height: 1.7;
  color: var(--ae-muted);
  max-width: 440px;
}

.ars-number {
  display: flex;
  flex: none;
  align-items: center;
  gap: 9px;
  font-size: 12px;
  color: var(--ae-muted);
}

.anime-editor .ars-number input {
  width: 82px;
  min-height: 44px;
  padding: 8px 10px;
  border-radius: 9px;
  color: var(--ae-ink);
  background: rgb(148 163 184 / 0.08);
  border: 1px solid rgb(148 163 184 / 0.18);
}

.ars-switch {
  appearance: none;
  flex: none;
  width: 38px;
  height: 22px;
  border-radius: 99px;
  background: rgb(148 163 184 / 0.4);
  cursor: pointer;
  position: relative;
  transition: background 0.16s;
}

.ars-switch::after {
  content: '';
  position: absolute;
  width: 16px;
  height: 16px;
  left: 3px;
  top: 3px;
  border-radius: 50%;
  background: #fff;
  box-shadow: 0 1px 3px rgb(15 23 42 / 0.14);
  transition: transform 0.16s;
}

.ars-switch:checked {
  background: #2563eb;
}

.ars-switch:checked::after {
  transform: translateX(16px);
}

.ars-switch:disabled {
  opacity: 0.5;
  cursor: default;
}

.ars-options {
  display: flex;
  flex: none;
  gap: 4px;
  padding: 3px;
  border-radius: 10px;
  background: rgb(148 163 184 / 0.07);
}

.ars-options label {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 40px;
  padding: 6px 12px;
  font-size: 12px;
  border-radius: 8px;
  cursor: pointer;
  white-space: nowrap;
}

.ars-options input[type='radio'] {
  position: absolute;
  width: 1px;
  height: 1px;
  min-height: 0;
  padding: 0;
  border: 0;
  opacity: 0;
}

.ars-options .selected {
  background: rgb(59 130 246 / 0.14);
  color: var(--ae-blue);
  font-weight: 600;
  box-shadow: 0 1px 3px rgb(15 23 42 / 0.05);
}

.ars-options label:has(:focus-visible),
.ars-settings :focus-visible,
.ars-link:focus-visible {
  outline: 2px solid #60a5fa;
  outline-offset: 3px;
}

.ars-options:disabled {
  opacity: 0.55;
}

.ars-notes {
  margin-top: 8px;
  padding: 16px 18px;
  border-radius: 14px;
  background: rgb(148 163 184 / 0.06);
  border: 1px solid rgb(148 163 184 / 0.1);
  color: var(--ae-muted);
  font-size: 12px;
  line-height: 1.8;
}

.ars-notes-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--ae-ink);
  margin-bottom: 8px;
}

.ars-notes p + p {
  margin-top: 6px;
}

.ars-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 14px;
  margin-top: 24px;
  padding-top: 18px;
  border-top: 1px solid rgb(148 163 184 / 0.08);
}

.ars-feedback {
  margin-top: 12px;
  color: var(--ae-muted);
  font-size: 12px;
  line-height: 1.8;
  overflow-wrap: anywhere;
}

.ars-feedback:empty {
  display: none;
}

.ars-feedback button {
  margin-top: 8px;
}

.ars-link {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  min-height: 42px;
  padding: 7px 10px;
  font-size: 12px;
  color: rgb(var(--color-slate-600));
}

.ars-discard {
  font-size: 13px;
  line-height: 1.8;
  color: rgb(var(--color-slate-600));
}

.ars-discard-actions {
  display: flex;
  justify-content: end;
  flex-wrap: wrap;
  gap: 10px;
}

.dark .ars-settings {
  --ae-blue: #93c5fd;
}

@media (max-width: 639px) {
  .ars-layout :deep(.management-heading) {
    grid-template-columns: minmax(0, 1fr);
  }
  .ars-layout :deep(.management-heading > *) {
    grid-column: 1 / -1;
  }
  .ars-tabs {
    gap: 6px;
    padding: 5px;
  }
  .ars-tabs button {
    padding: 6px 12px;
    gap: 6px;
    font-size: 12.5px;
    min-height: 38px;
  }
  .ars-tabs button svg {
    width: 16px;
    height: 16px;
  }
  .ars-tabs-unsaved {
    padding: 4px 8px;
    font-size: 11.5px;
  }
  .ars-panel,
  .ars-panel--wide {
    padding: 18px 16px;
    border-radius: 14px;
  }
  .ars-row {
    flex-wrap: wrap;
    gap: 12px;
    padding: 18px 0;
  }
  .ars-row > div:first-child {
    flex: 1;
    min-width: 160px;
  }
  .ars-options {
    flex-basis: 100%;
  }
  .ars-options label {
    flex: 1;
    min-height: 44px;
  }
  .ars-footer {
    flex-wrap: wrap;
  }
}

@media (prefers-reduced-motion: reduce) {
  .ars-tabs {
    scroll-behavior: auto !important;
  }
  .ars-switch,
  .ars-switch::after,
  .ars-tabs button {
    transition: none;
  }
}
</style>
