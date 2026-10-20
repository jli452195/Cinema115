<script setup lang="ts">
import { computed, onBeforeUnmount, ref, toRef, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { IconPlus, IconFolder, IconPhotoOff, IconCheck, IconArrowLeft, IconSettings } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import AnimeSourceForm from '../components/AnimeSourceForm.vue'
import AnimeNamingTarget from '../components/AnimeNamingTarget.vue'
import AnimeChapterEpisodes from '../components/AnimeChapterEpisodes.vue'
import AnimeReleasePreview from '../components/AnimeReleasePreview.vue'
import AnimeStorageSettingsDialog from '../components/AnimeStorageSettingsDialog.vue'
import { animeImageURL } from '../composables/useAnimeDiscovery'
import { useAnimeSubscriptionEditor } from '../composables/useAnimeSubscriptionEditor'
import { animeSourceReady } from '../utils/animeSourceForm'
import { animeReturnPath } from '../composables/useAnimeSubscriptionBrowser'
import '../styles/animeSubscriptionEditor.css'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const route = useRoute(), router = useRouter()
const subscriptionID = computed(() => String(route.params.id || ''))
const bangumiID = computed(() => typeof route.query.bangumiId === 'string' ? route.query.bangumiId : '')
const seed = computed(() => {
  const initial = window.history.state?.animeSource
  if (initial?.bangumiId === bangumiID.value) return { provider: String(initial.provider || ''), itemId: String(initial.itemId || '') }
  return { provider: typeof route.query.source === 'string' ? route.query.source : '', itemId: typeof route.query.itemId === 'string' ? route.query.itemId : '' }
})
const editor = useAnimeSubscriptionEditor(toRef(props, 'token'), subscriptionID, bangumiID, seed)
const { sources, activeRef, active, subject, settings, local, loading, loadError, feedback, settingsError, previewBusy, saveBusy, settingsBusy, pendingSave, receipt, duplicateID, conflict, now, budget, locked, dirty, startIssue, needsPreview, selectedCount } = editor
const imageFailed = ref(false), settingsOpen = ref(false)
const returnPath = computed(() => animeReturnPath(route.query.returnTo || window.history.state?.animeReturnTo) || (subscriptionID.value ? `/anime/subscriptions/${encodeURIComponent(subscriptionID.value)}` : '/anime'))
const returnLabel = computed(() => returnPath.value.startsWith('/anime/subscriptions') ? '返回订阅' : '返回选番')
watch(() => subject.value?.coverUrl, () => { imageFailed.value = false })
function canLeave() {
  if (saveBusy.value || settingsBusy.value || pendingSave.value) { window.alert('订阅或设置提交结果尚未确认，请先核对原请求结果。'); return false }
  return !dirty.value || window.confirm('还有未提交的订阅配置，确定离开？')
}
function unload(event: BeforeUnloadEvent) { if (dirty.value || pendingSave.value || saveBusy.value || settingsBusy.value) event.preventDefault() }
window.addEventListener('beforeunload', unload)
onBeforeUnmount(() => window.removeEventListener('beforeunload', unload))
onBeforeRouteLeave(canLeave)
onBeforeRouteUpdate(canLeave)
function openSaved(id: string) { void router.push(savedLocation(id)) }
function savedLocation(id: string) {
  const back = router.resolve(returnPath.value)
  return { path: `/anime/subscriptions/${encodeURIComponent(id)}`, query: back.path.startsWith('/anime/subscriptions') ? { status: back.query.status, q: back.query.q, page: back.query.page } : {} }
}
async function reloadConfiguration() {
  if (dirty.value && !window.confirm('重新读取将替换当前未提交输入，确定继续？')) return
  await editor.initialize()
}
</script>

<template>
  <ManagementLayout :title="local ? '配置动漫订阅' : '开始动漫订阅'" description="选择字幕组与规格，核对资源后直接开始订阅，下载完成后自动整理。" :parent="{ label: '动漫订阅', to: returnPath }" @logout="$emit('logout')">
    <div class="anime-editor">
      <p v-if="loading" role="status" class="ae-loading">正在读取篇章与配置…</p>
      <div v-else-if="loadError" class="ae-load-error" role="status"><p>{{ loadError }}</p><button type="button" class="ae-button" @click="editor.initialize">重新读取</button><RouterLink :to="returnPath" class="ae-link">{{ returnLabel }}</RouterLink></div>
      <template v-else-if="subject">
        <section class="ae-chapter" aria-label="当前篇章">
          <div class="ae-chapter-poster"><img v-if="animeImageURL(subject.coverUrl) && !imageFailed" :src="animeImageURL(subject.coverUrl)" :alt="subject.title" referrerpolicy="no-referrer" @error="imageFailed = true" /><IconPhotoOff v-else :size="24" aria-hidden="true" /></div>
          <div class="ae-chapter-identity"><p class="ae-eyebrow">{{ subject.id ? '当前 Bangumi 篇章' : '当前订阅番剧' }}</p><h2>{{ subject.title }}</h2><p class="ae-note">{{ subject.id ? subject.date || '首播日期未收录' : '未关联 Bangumi，可直接配置来源与 TMDB 命名' }}<span v-if="subject.id"> · Bangumi {{ subject.id }}</span><span v-if="local"> · 配置版本 {{ local.revision }}</span></p></div>
          <AnimeChapterEpisodes v-if="subject.id" :token="token" :bangumi-id="subject.id" :target="editor.naming.target.value" :target-busy="editor.naming.busy.value || editor.naming.searching.value" />
          <RouterLink :to="returnPath" class="ae-button"><IconArrowLeft :size="16" />{{ returnLabel }}</RouterLink>
        </section>

        <section v-if="receipt" class="ae-receipt" role="status"><IconCheck :size="26" aria-hidden="true" /><div><h2>已开始订阅</h2><p>已按本页确认的资源选择开始处理，后续发布将按规则检查并下载整理。</p><div class="ae-receipt-actions"><RouterLink :to="savedLocation(receipt.id)" class="ae-button ae-primary">查看订阅</RouterLink><RouterLink :to="returnPath" class="ae-button">{{ returnLabel }}</RouterLink></div></div></section>

        <template v-else>
          <AnimeNamingTarget :naming="editor.naming" :disabled="locked" />
          <section class="ae-storage-summary" aria-label="共用存储目录">
            <IconFolder :size="20" aria-hidden="true" />
            <div class="ae-storage-paths"><span class="ae-note">共用目录</span><p><span>下载</span>{{ settings?.settings.downloadPath || '尚未配置' }}</p><p><span>整理</span>{{ settings?.settings.organizePath || '尚未配置' }}</p><small v-if="settings && !settings.readiness.ready">{{ settings.readiness.issues.includes('ACCOUNT_SNAPSHOT_CHANGED') ? '主账号已变化，请重新选择下载目录。' : '开始订阅前，请在设置中补齐下载与整理目录。' }}</small><small v-if="settingsError" role="status">{{ settingsError }}</small></div>
            <button type="button" class="ae-button" :disabled="locked || !!previewBusy" aria-label="动漫存储设置" @click="settingsOpen = true"><IconSettings :size="16" aria-hidden="true" />设置</button>
          </section>
          <div class="ae-editor-grid">
            <section class="ae-workspace" aria-label="RSS 来源配置">
              <div class="ae-source-tabs" role="group" aria-label="切换 RSS 来源"><button v-for="(source, index) in sources" :key="source.ref" type="button" :disabled="locked || !!previewBusy" :aria-pressed="activeRef === source.ref" @click="activeRef = source.ref"><span>{{ source.role === 'main' ? '主 RSS' : `备用 ${index}` }}</span><span v-if="source.groupName" class="ae-tab-group">{{ source.groupName }}</span><span class="ae-tab-state" :class="{ ready: animeSourceReady(source) }">{{ animeSourceReady(source) ? '已配置' : '待配置' }}</span></button><button type="button" class="ae-add-source" :disabled="locked || !!previewBusy || sources.length >= 5" @click="editor.addSource"><IconPlus :size="16" />添加备用</button></div>
              <AnimeSourceForm v-if="active" :key="active.ref" :source="active" :naming-target="editor.naming.target.value" :chapter-title="subject.title" :disabled="locked" :preview-busy="!!previewBusy" :now="now" @search="editor.search" @choose="editor.chooseEntry" @group="editor.groupChanged" @remove="editor.removeSource(active)" @open="editor.openSource" @sample="source => editor.readSample(source, true)" @cancel="editor.cancelSourceEdit" @apply="candidate => editor.applySource(active!, candidate)" />
            </section>


          </div>

          <AnimeReleasePreview v-if="active" :key="`preview-${active.ref}`" :source="active" :valid="editor.validPreview(active)" :busy="!!previewBusy" :disabled="locked" :now="now" :remaining="budget.remaining" :settings-available="!!settings" :naming-target="editor.naming.target.value" :naming-busy="editor.naming.busy.value" :organize-path="settings?.settings.organizePath" @generate="editor.runPreview(active, 'generate')" @load="editor.runPreview(active, 'load')" @page="editor.runPreview(active, 'page', { page: $event })" @select="(id, checked) => editor.selectRelease(active!, id, checked)" @dismiss="editor.dismissPending(active, $event)" @clear-recovery="editor.dismissRecovery(active)" />

          <div v-if="feedback" class="ae-save-feedback" role="status"><p>{{ feedback }}</p><button v-if="pendingSave" type="button" class="ae-button ae-primary" :disabled="saveBusy" @click="editor.retrySave">{{ saveBusy ? '核对订阅结果…' : '核对原订阅请求' }}</button><button v-if="duplicateID" type="button" class="ae-link" @click="openSaved(duplicateID)">打开已有订阅</button><button v-if="conflict && local" type="button" class="ae-link" :disabled="locked || !!previewBusy" @click="reloadConfiguration">重新读取配置</button><button v-if="conflict" type="button" class="ae-link" :disabled="locked || !!previewBusy" @click="editor.reloadSettings">重新读取目录设置</button></div>
          <footer class="ae-save-bar"><p id="anime-start-hint" aria-live="polite">{{ pendingSave ? '提交结果待确认，输入已保留，请核对原请求。' : startIssue || (needsPreview ? '先生成匹配预览，核对后即可开始订阅。' : `已选 ${selectedCount} 条资源${selectedCount ? '，开始后将下载并自动整理。' : '，开始后等待后续更新。'}`) }}</p><div><button type="button" class="ae-button ae-primary" aria-describedby="anime-start-hint" :disabled="locked || editor.naming.busy.value || editor.naming.searching.value || !!previewBusy || sources.some(source => source.searchBusy) || !!startIssue" @click="editor.start">{{ saveBusy ? '正在开始订阅…' : previewBusy ? '正在生成预览…' : needsPreview ? '预览并核对' : '开始订阅' }}</button></div></footer>
        </template>
      </template>
    </div>
    <AnimeStorageSettingsDialog v-if="settingsOpen" :token="token" @close="settingsOpen = false" @loaded="settings = $event; settingsError = ''" @changed="settings = $event; settingsError = ''" />
  </ManagementLayout>
</template>
