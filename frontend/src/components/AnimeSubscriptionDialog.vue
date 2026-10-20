<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, reactive, ref, toRef, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { IconPlus, IconFolder, IconPhotoOff, IconSettings, IconChevronDown, IconTrash } from '@tabler/icons-vue'
import AnimeFloatingDialog from './AnimeFloatingDialog.vue'
import AnimeSourceForm from './AnimeSourceForm.vue'
import AnimeNamingTarget from './AnimeNamingTarget.vue'
import AnimeChapterEpisodes from './AnimeChapterEpisodes.vue'
import AnimeReleasePreview from './AnimeReleasePreview.vue'
import SelectMenu from './SelectMenu.vue'
import AnimeDownloadJobs from './AnimeDownloadJobs.vue'
import AnimeStorageSettingsDialog from './AnimeStorageSettingsDialog.vue'
import { useAnimeSubscriptionEditor } from '../composables/useAnimeSubscriptionEditor'
import { useAnimeSubscriptionActions } from '../composables/useAnimeSubscriptionActions'
import { animeImageURL } from '../composables/useAnimeDiscovery'
import { animeProviderName, animeSourceSpecs } from '../utils/animeSourceForm'
import { subscriptionStatus } from '../utils/animeSubscriptionPresentation'
import '../styles/animeSubscriptionEditor.css'
import '../styles/animeSubscriptions.css'

const props = withDefaults(defineProps<{ token: string; id?: string; bangumiId?: string; seed?: { provider: string; itemId: string } }>(), { id: '', bangumiId: '' })
const emit = defineEmits<{ close: []; saved: [id: string]; open: [id: string] }>()
const editor = useAnimeSubscriptionEditor(toRef(props, 'token'), toRef(props, 'id'), toRef(props, 'bangumiId'), computed(() => props.seed || { provider: '', itemId: '' }))
const { sources, activeRef, active, subject, settings, local, loading, loadError, feedback, settingsError, previewBusy, saveBusy, pendingSave, receipt, duplicateID, conflict, now, budget, locked, dirty, startIssue } = editor
const shell = ref<InstanceType<typeof AnimeFloatingDialog>>()
const tab = ref<'basic' | 'backup' | 'jobs'>('basic'), previewOpen = ref(false), settingsOpen = ref(false), childOpen = ref(false), imageFailed = ref(false)
const discard = ref(false), confirmRemove = ref(false), jobsVersion = ref(0)
const reloadConfirm = ref(false)
const episodesOpen = ref(false)
const previewRef = ref(''), previewButton = ref<HTMLButtonElement>()
const previewStates = reactive<Record<string, { filter: string; left: number; top: number }>>({})
function previewState(ref: string) { return previewStates[ref] ||= { filter: 'all', left: 0, top: 0 } }
const previewSource = computed(() => sources.value.find(s => s.ref === previewRef.value) || active.value || sources.value[0])
const previewSourceOptions = computed(() => sources.value.map(source => ({ value: source.ref, label: `${source.role === 'main' ? '主 RSS' : '备用 RSS'} · ${source.groupName || animeProviderName(source.provider)}` })))
const main = computed(() => sources.value.find(s => s.role === 'main'))
const backups = computed(() => sources.value.filter(s => s.role === 'standby'))
const readonly = computed(() => !!local.value && ['archived', 'removed'].includes(local.value.status))
const actions = useAnimeSubscriptionActions(props.token, props.id, async () => { confirmRemove.value = false; jobsVersion.value++; await editor.initialize() })
const actionLocked = computed(() => locked.value || dirty.value || actions.locked.value)
let routeDecision: ((leave: boolean) => void) | undefined
let leaving = false
const scroll = new Map<string, number>()
let top = 0
function close() {
  if (childOpen.value || previewOpen.value || settingsOpen.value) return
  if (confirmRemove.value) { confirmRemove.value = false; return }
  if (discard.value) { cancelDiscard(); return }
  if (pendingSave.value || saveBusy.value || actions.pending.value || actions.loading.value) { feedback.value = '提交结果尚未确认，请先核对原请求。'; return }
  if (dirty.value) { discard.value = true; return }
  emit('close')
}
function guard() {
  if (leaving) return true
  if (pendingSave.value || saveBusy.value || actions.pending.value || actions.loading.value) { feedback.value = '提交结果尚未确认，请先核对原请求。'; return false }
  if (!dirty.value) return true
  discard.value = true
  return new Promise<boolean>(resolve => { routeDecision?.(false); routeDecision = resolve })
}
function cancelDiscard() { discard.value = false; reloadConfirm.value = false; routeDecision?.(false); routeDecision = undefined }
function discardChanges() {
  discard.value = false; leaving = true
  if (reloadConfirm.value) { leaving = false; reloadConfirm.value = false; void editor.initialize(); return }
  if (routeDecision) { routeDecision(true); routeDecision = undefined } else emit('close')
}
function unload(event: BeforeUnloadEvent) { if (dirty.value || pendingSave.value || saveBusy.value) event.preventDefault() }
window.addEventListener('beforeunload', unload)
onBeforeRouteLeave(guard)
onBeforeRouteUpdate((to, from) => to.fullPath === from.fullPath ? true : guard())
onBeforeUnmount(() => { window.removeEventListener('beforeunload', unload); routeDecision?.(false) })
watch(receipt, value => { if (value) { leaving = true; emit('saved', value.id) } })
watch(tab, async (value, previous) => { scroll.set(previous, top); await nextTick(); shell.value?.scrollTo(scroll.get(value) || 0) })
watch(() => subject.value?.coverUrl, () => { imageFailed.value = false })
function addBackup() { editor.addSource() }
function chooseBackup(id: string) { activeRef.value = activeRef.value === id ? '' : id }
function control(action: string, label: string) { if (local.value) void actions.execute(`subscriptions/${local.value.id}/${action}`, { expectedRevision: local.value.revision }, label) }
const allowed = (action: string) => !!local.value?.permittedActions?.some(a => a.name === action && a.allowed)
async function openPreview() {
  if (childOpen.value || settingsOpen.value || locked.value) return
  previewRef.value = tab.value === 'backup' && active.value?.role === 'standby' ? activeRef.value : main.value?.ref || ''
  previewOpen.value = true
  await nextTick()
  const source = previewSource.value
  if (source && !editor.validPreview(source) && !previewBusy.value) await editor.runPreview(source, 'generate')
}
function closePreview() { previewOpen.value = false; void nextTick(() => previewButton.value?.focus({ preventScroll: true })) }
watch(previewRef, async () => { if (previewOpen.value && previewSource.value && !editor.validPreview(previewSource.value)) await editor.runPreview(previewSource.value, 'generate') })
async function reload() { if (dirty.value) { reloadConfirm.value = true; discard.value = true; shell.value?.scrollTo(0); return } await editor.initialize() }
</script>

<template>
  <AnimeFloatingDialog ref="shell" :title="id ? '修改订阅' : '新增订阅'" close-label="关闭订阅配置" size="config" @close="close" @scroll="top = $event">
    <template #header>
      <div v-if="subject" class="ac-identity">
        <div class="ac-poster"><img v-if="animeImageURL(subject.coverUrl) && !imageFailed" :src="animeImageURL(subject.coverUrl)" alt="" referrerpolicy="no-referrer" @error="imageFailed = true" /><IconPhotoOff v-else :size="22" aria-hidden="true" /></div>
        <div><h3>{{ subject.title }}</h3><p><span v-if="subject.id">Bangumi {{ subject.id }}</span><span v-if="editor.naming.target.value">TMDB {{ editor.naming.target.value.tmdbId }}</span><span>{{ local ? subscriptionStatus(local.status) : '保存后启用' }}</span></p></div>
      </div>
      <div class="ac-tabs" role="tablist" aria-label="订阅配置">
        <button id="anime-tab-basic" type="button" role="tab" :aria-selected="tab === 'basic'" aria-controls="anime-panel-basic" @click="tab = 'basic'">基本</button>
        <button id="anime-tab-backup" type="button" role="tab" :aria-selected="tab === 'backup'" aria-controls="anime-panel-backup" @click="tab = 'backup'">备用 RSS <small>{{ backups.length }}</small></button>
        <button v-if="id" id="anime-tab-jobs" type="button" role="tab" :aria-selected="tab === 'jobs'" aria-controls="anime-panel-jobs" @click="tab = 'jobs'">下载记录</button>
      </div>
    </template>
    <div class="anime-editor ac-editor">
      <div v-if="discard" class="ac-confirm" role="alert"><p>{{ reloadConfirm ? '放弃未保存的修改，重新读取最新配置？' : '有尚未保存的修改，是否放弃？' }}</p><div><button type="button" class="ae-button" @click="cancelDiscard">继续编辑</button><button type="button" class="ae-button ae-danger" @click="discardChanges">放弃修改</button></div></div>
      <p v-if="loading" role="status" class="ae-loading">正在读取订阅配置…</p>
      <div v-else-if="loadError" class="ae-load-error" role="status"><p>{{ loadError }}</p><button type="button" class="ae-button" @click="editor.initialize">重新读取</button></div>
      <template v-else-if="subject">
        <div v-show="tab === 'basic'" id="anime-panel-basic" role="tabpanel" aria-labelledby="anime-tab-basic">
          <AnimeNamingTarget :naming="editor.naming" :disabled="locked" />
          <details v-if="subject.id" class="ae-advanced ac-episodes" @toggle="episodesOpen = ($event.target as HTMLDetailsElement).open"><summary>篇章集号<IconChevronDown :size="15" /></summary><AnimeChapterEpisodes v-if="episodesOpen" :token="token" :bangumi-id="subject.id" :target="editor.naming.target.value" :target-busy="editor.naming.busy.value || editor.naming.searching.value" /></details>
          <AnimeSourceForm v-if="main" :source="main" :naming-target="editor.naming.target.value" :chapter-title="subject.title" :disabled="locked" :preview-busy="!!previewBusy" :now="now" @search="editor.search" @choose="editor.chooseEntry" @group="editor.groupChanged" @remove="editor.removeSource(main)" @open="editor.openSource" @sample="source => editor.readSample(source, true)" @cancel="editor.cancelSourceEdit" @apply="editor.applySource(main, $event)" @child-open="childOpen = $event" />
          <section class="ac-storage"><IconFolder :size="18" aria-hidden="true" /><div><p><span>下载</span>{{ settings?.settings.downloadPath || '尚未配置' }}</p><p><span>整理</span>{{ settings?.settings.organizePath || '尚未配置' }}</p><small v-if="settingsError">{{ settingsError }}</small></div><button type="button" class="ae-button" aria-label="动漫存储设置" :disabled="locked || childOpen || !!previewBusy" @click="settingsOpen = true"><IconSettings :size="16" />设置</button></section>
        </div>
        <div v-show="tab === 'backup'" id="anime-panel-backup" role="tabpanel" aria-labelledby="anime-tab-backup">
          <div class="ac-backup-heading"><p class="ae-note">每个来源使用自己的编号映射，独立检查和下载。</p><button type="button" class="ae-button" :disabled="locked || !!previewBusy || sources.length >= 5" @click="addBackup"><IconPlus :size="16" />添加备用</button></div>
          <p v-if="!backups.length" class="ac-empty">暂无备用 RSS，可添加其他字幕组的版本。</p>
          <section v-for="source in backups" :key="source.ref" class="ac-backup">
            <button type="button" class="ac-backup-row" :aria-expanded="activeRef === source.ref" :disabled="childOpen" @click="chooseBackup(source.ref)"><span><strong>{{ source.groupName || '选择备用来源' }}</strong><small>{{ animeProviderName(source.provider) }} · {{ animeSourceSpecs(source) }} · {{ source.runWhenEnabled ? '启用' : '停用' }}</small></span><span v-if="editor.naming.target.value" class="ac-map">发布 {{ source.mappingSource }} 对应 S{{ String(source.mappingSeason || editor.naming.target.value.season).padStart(2, '0') }}E{{ String(source.mappingTarget).padStart(2, '0') }}</span><IconChevronDown :size="16" :class="{ 'rotate-180': activeRef === source.ref }" /></button>
            <AnimeSourceForm v-if="activeRef === source.ref" :source="source" :naming-target="editor.naming.target.value" :chapter-title="subject.title" :disabled="locked" :preview-busy="!!previewBusy" :now="now" @search="editor.search" @choose="editor.chooseEntry" @group="editor.groupChanged" @remove="editor.removeSource(source)" @open="editor.openSource" @sample="value => editor.readSample(value, true)" @cancel="editor.cancelSourceEdit" @apply="editor.applySource(source, $event)" @child-open="childOpen = $event" />
          </section>
        </div>
        <div v-if="tab === 'jobs' && local" id="anime-panel-jobs" role="tabpanel" aria-labelledby="anime-tab-jobs">
          <div class="as-actions">
            <button v-if="allowed('pause') && local.status === 'enabled'" type="button" class="ae-button" :disabled="actionLocked" @click="control('pause', '暂停订阅')">暂停订阅</button>
            <button v-if="allowed('resume')" type="button" class="ae-button" :disabled="actionLocked" @click="control('resume', '恢复订阅')">恢复订阅</button>
            <button v-if="allowed('archive') && local.status !== 'archived'" type="button" class="ae-button" :disabled="actionLocked" @click="control('archive', '归档订阅')">归档</button>
            <button v-if="allowed('unarchive') && local.status === 'archived'" type="button" class="ae-button" :disabled="actionLocked" @click="control('unarchive', '取消归档')">取消归档</button>
            <button v-if="allowed('remove') && local.status !== 'removed'" type="button" class="ae-button" :disabled="actionLocked" @click="confirmRemove = !confirmRemove"><IconTrash :size="15" />移除</button>
          </div>
          <p v-if="dirty" class="ae-note">有未保存的配置，保存后可操作订阅状态。</p>
          <div v-if="confirmRemove" class="ac-confirm"><p>停止后续订阅并保留记录，不删除网盘文件。</p><button type="button" class="ae-button" :disabled="actionLocked" @click="control('remove', '移除订阅')">确认移除</button><button type="button" class="ae-button" @click="confirmRemove = false">取消</button></div>
          <p v-if="actions.error.value || actions.notice.value" role="status" class="ae-note">{{ actions.error.value || actions.notice.value }}</p>
          <button v-if="actions.pending.value" type="button" class="ae-button" :disabled="actions.loading.value" @click="actions.confirmPending">核对原操作</button>
          <AnimeDownloadJobs :key="local.id" :token="token" :id="local.id" :locked="actions.locked.value || saveBusy" :version="jobsVersion" />
        </div>
      </template>
    </div>
    <template #footer>
      <div v-if="feedback" class="ac-feedback" role="status"><p>{{ feedback }}</p><button v-if="pendingSave" type="button" class="ae-button" :disabled="saveBusy" @click="editor.retrySave">核对原保存请求</button><button v-if="duplicateID" type="button" class="ae-button" @click="emit('open', duplicateID)">打开已有订阅</button><button v-if="conflict" type="button" class="ae-button" :disabled="saveBusy" @click="reload">重载最新配置</button></div>
      <div class="ac-footer"><p id="anime-save-hint">{{ readonly ? '已归档或移除的订阅保留执行记录。' : startIssue || (local?.status === 'paused' ? '仅保存配置，订阅保持暂停。' : '保存配置后，后台独立检查、下载并整理。') }}</p><div><button type="button" class="ae-button" :disabled="saveBusy || !!pendingSave" @click="close">取消</button><button ref="previewButton" type="button" class="ae-button" :disabled="locked || childOpen || !!previewBusy || !subject" @click="openPreview">预览</button><button type="button" class="ae-button ae-primary" aria-describedby="anime-save-hint" :disabled="locked || actions.locked.value || childOpen || !!previewBusy || !!startIssue || editor.naming.busy.value || editor.naming.searching.value" @click="editor.save">{{ saveBusy ? '保存中…' : '保存' }}</button></div></div>
    </template>
  </AnimeFloatingDialog>
  <AnimeFloatingDialog v-if="previewOpen" title="匹配与重命名预览" close-label="关闭预览，返回配置" size="preview" @close="closePreview">
    <div class="anime-editor ac-preview">
      <AnimeReleasePreview v-if="previewSource" :key="previewSource.ref" :source="previewSource" :state="previewState(previewSource.ref)" :valid="editor.validPreview(previewSource)" :busy="!!previewBusy" :disabled="locked" :now="now" :remaining="budget.remaining" :settings-available="!!settings" :naming-target="editor.naming.target.value" :naming-busy="editor.naming.busy.value" :organize-path="settings?.settings.organizePath" @generate="editor.runPreview(previewSource, 'generate')" @load="editor.runPreview(previewSource, 'load')" @page="editor.runPreview(previewSource, 'page', { page: $event })"><template #source><SelectMenu v-model="previewRef" :options="previewSourceOptions" :disabled="!!previewBusy" label="来源" variant="anime" class="ac-preview-source" /></template></AnimeReleasePreview>
    </div><template #footer><div class="ac-preview-close"><span class="ae-note">预览只查看匹配和命名，不创建下载任务。</span><button type="button" class="ae-button" @click="closePreview">返回配置</button></div></template>
  </AnimeFloatingDialog>
  <AnimeStorageSettingsDialog v-if="settingsOpen" :token="token" @close="settingsOpen = false" @loaded="settings = $event; settingsError = ''" @changed="settings = $event; settingsError = ''" />
</template>

<style scoped>
.ac-identity { display: flex; align-items: center; gap: 14px; margin-bottom: 18px; }
.ac-poster { flex: none; width: 46px; height: 64px; display: grid; place-items: center; overflow: hidden; border-radius: 8px; background: rgb(148 163 184 / .12); }
.ac-poster img { width: 100%; height: 100%; object-fit: cover; }
.ac-identity h3 { font-size: 17px; font-weight: 600; line-height: 1.4; }
.ac-identity p { display: flex; flex-wrap: wrap; gap: 5px 12px; margin-top: 6px; font-size: 11px; color: var(--floating-surface-muted); }
.ac-tabs { display: flex; gap: 6px; }
.ac-tabs button { padding: 9px 18px; border-radius: 10px; font-size: 13px; color: var(--floating-surface-muted); }
.ac-tabs button[aria-selected=true] { color: #0f766e; background: rgb(20 184 166 / .10); font-weight: 600; }
:global(.dark) .ac-tabs button[aria-selected=true] { color: #5eead4; }
.ac-tabs small { margin-left: 4px; opacity: .75; }
.ac-editor { width: 100%; max-width: none; margin: 0; }
.ac-editor :deep(.ae-naming-target) { margin-top: 0; padding: 0 0 18px; background: none; border: 0; }
.ac-editor :deep(.ae-naming-heading h3 small) { display: block; margin: 5px 0 0; }
.ac-editor :deep(.ae-source-summary), .ac-editor :deep(.ae-choose-source) { background: rgb(148 163 184 / .06); }
.ac-episodes { margin: 0 0 22px; }
.ac-storage { display: flex; align-items: center; gap: 12px; margin-top: 24px; color: var(--floating-surface-muted); }
.ac-storage > div { flex: 1; min-width: 0; font-size: 11px; }
.ac-storage p { overflow-wrap: anywhere; line-height: 1.8; }
.ac-storage span { margin-right: 10px; opacity: .75; }
.ac-backup-heading, .ac-backup-row, .ac-footer, .ac-preview-close { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.ac-backup-heading { margin: 5px 0 16px; }
.ac-backup { margin-bottom: 12px; border-radius: 14px; background: rgb(148 163 184 / .05); }
.ac-backup-row { width: 100%; text-align: left; padding: 15px; }
.ac-backup-row > span:first-child { flex: 1; min-width: 0; }
.ac-backup-row strong { display: block; font-size: 13px; font-weight: 500; }
.ac-backup-row small { display: block; font-size: 11px; color: var(--floating-surface-muted); margin-top: 4px; }
.ac-backup :deep(.anime-source-form) { padding: 3px 15px 18px; }
.ac-map { font-size: 11px; color: var(--floating-surface-muted); }
.ac-empty { padding: 48px 16px; text-align: center; font-size: 12px; color: var(--floating-surface-muted); }
.ac-footer > p { font-size: 11px; line-height: 1.6; color: var(--floating-surface-muted); max-width: 320px; }
.ac-footer > div { display: flex; flex: none; gap: 8px; }
.ac-confirm { position: sticky; top: 0; z-index: 2; padding: 16px; margin: 0 0 18px; border-radius: 12px; color: var(--ae-ink); background: var(--ae-surface); box-shadow: 0 4px 18px rgb(15 23 42 / .10); font-size: 13px; line-height: 1.7; }
.ac-confirm .ae-danger { color: #b91c1c; background: #fef2f2; }
:global(.dark) .ac-confirm .ae-danger { color: #fecaca; background: #4b2f38; }
.ac-confirm > div { display: flex; justify-content: flex-end; gap: 8px; margin-top: 12px; }
.ac-feedback { font-size: 12px; color: var(--floating-surface-muted); margin-bottom: 12px; }
.ac-feedback button { margin: 8px 6px 0 0; }
.ac-preview { display: flex; flex: 1; min-height: 0; width: 100%; max-width: none; margin: 0; }
.ac-preview :deep(.ae-preview) { display: flex; flex: 1; flex-direction: column; min-height: 0; margin: 0; padding: 0; border: 0; background: none; box-shadow: none; }
.ac-preview :deep(.ae-preview > *) { flex-shrink: 0; }
.ac-preview :deep(.ae-release-list) { flex: 1; min-height: 0; max-height: none; }
.ac-preview-source { flex: 0 1 310px; min-width: 0; font-size: 12px; }
@media(max-width: 639px) { .ac-footer { align-items: stretch; flex-direction: column; } .ac-footer > p { max-width: none; } .ac-footer > div { justify-content: flex-end; } .ac-tabs button { padding-inline: 12px; } .ac-map { display: none; } .ac-backup-heading { align-items: flex-start; } .ac-backup-heading .ae-button { flex: none; } .ac-preview-close { align-items: flex-start; } }
</style>
