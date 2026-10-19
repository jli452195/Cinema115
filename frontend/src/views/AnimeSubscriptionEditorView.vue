<script setup lang="ts">
import { computed, onBeforeUnmount, ref, toRef, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { IconPlus, IconFolder, IconPhotoOff, IconCheck, IconArrowLeft } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import AnimeSourceForm from '../components/AnimeSourceForm.vue'
import AnimeNamingTarget from '../components/AnimeNamingTarget.vue'
import AnimeChapterEpisodes from '../components/AnimeChapterEpisodes.vue'
import AnimeReleasePreview from '../components/AnimeReleasePreview.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
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
const { sources, activeRef, active, subject, settings, local, loading, loadError, feedback, settingsError, previewBusy, saveBusy, settingsBusy, pendingSave, pendingSettings, receipt, duplicateID, conflict, directoryOpen, now, budget, locked, dirty, pausedReady } = editor
const imageFailed = ref(false), settingsExpanded = ref(false)
const returnPath = computed(() => animeReturnPath(route.query.returnTo || window.history.state?.animeReturnTo) || (subscriptionID.value ? `/anime/subscriptions/${encodeURIComponent(subscriptionID.value)}` : '/anime'))
const returnLabel = computed(() => returnPath.value.startsWith('/anime/subscriptions') ? '返回订阅' : '返回选番')
watch(() => subject.value?.coverUrl, () => { imageFailed.value = false })
function canLeave() {
  if (saveBusy.value || settingsBusy.value || pendingSave.value || pendingSettings.value) { window.alert('保存结果尚未确认，请先重试原请求核对结果。'); return false }
  return !dirty.value || window.confirm('还有未保存的订阅配置，确定离开？')
}
function unload(event: BeforeUnloadEvent) { if (dirty.value || pendingSave.value || pendingSettings.value || saveBusy.value || settingsBusy.value) event.preventDefault() }
window.addEventListener('beforeunload', unload)
onBeforeUnmount(() => window.removeEventListener('beforeunload', unload))
onBeforeRouteLeave(canLeave)
onBeforeRouteUpdate(canLeave)
function openSaved(id: string) { void router.push({ path: `/anime/subscriptions/${encodeURIComponent(id)}/edit`, query: { returnTo: returnPath.value } }) }
function savedLocation(id: string) {
  const back = router.resolve(returnPath.value)
  return { path: `/anime/subscriptions/${encodeURIComponent(id)}`, query: back.path.startsWith('/anime/subscriptions') ? { status: back.query.status, q: back.query.q, page: back.query.page } : {} }
}
async function reloadConfiguration() {
  if (dirty.value && !window.confirm('重新读取将替换当前未保存输入，确定继续？')) return
  await editor.initialize()
}
</script>

<template>
  <ManagementLayout :title="local ? '编辑动漫订阅' : '配置动漫订阅'" description="选择字幕组与规格，预览匹配结果。当前仅保存配置，下载尚未开放。" :parent="{ label: '动漫订阅', to: returnPath }" @logout="$emit('logout')">
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

        <section v-if="receipt" class="ae-receipt" role="status"><IconCheck :size="26" aria-hidden="true" /><div><h2>{{ receipt.status === 'paused' ? '已保存为暂停配置' : '草稿已保存' }}</h2><p>配置版本 {{ receipt.revision }}。未创建下载任务，历史选择需在之后执行前重新预览。</p><div class="ae-receipt-actions"><button type="button" class="ae-button ae-primary" @click="openSaved(receipt.id)">继续编辑</button><RouterLink :to="savedLocation(receipt.id)" class="ae-button">查看订阅</RouterLink><RouterLink :to="returnPath" class="ae-button">{{ returnLabel }}</RouterLink></div></div></section>

        <template v-else>
          <AnimeNamingTarget :naming="editor.naming" :disabled="locked" />
          <section class="ae-download-summary" aria-label="下载存放设置">
            <IconFolder :size="20" aria-hidden="true" />
            <div class="ae-download-path"><span class="ae-label">下载位置<span class="ae-note"> · 所有订阅共用</span></span><p>{{ settings?.settings.downloadPath || '尚未配置下载目录' }}</p><small v-if="settings && !settings.readiness.ready">{{ settings.readiness.issues.includes('ACCOUNT_SNAPSHOT_CHANGED') ? '主账号已变化，请重新选择并确认目录。' : '补齐主账号与目录后可保存为暂停；不影响查看资源或保存草稿。' }}</small></div>
            <button v-if="settings?.primaryAccount" type="button" class="ae-button" :disabled="locked || !!previewBusy" @click="directoryOpen = true">{{ settings.settings.downloadCid ? '更改目录' : '选择目录' }}</button>
            <RouterLink v-else-if="settings" to="/helper115" class="ae-button">配置主账号</RouterLink>
            <button type="button" class="ae-link" :aria-expanded="settingsExpanded" @click="settingsExpanded = !settingsExpanded">目录选项</button>
            <div v-if="settingsExpanded || settingsError || pendingSettings" class="ae-download-options"><p v-if="settings?.primaryAccount" class="ae-note">使用主账号 {{ settings.primaryAccount.name }}，每条发布保存到独立子目录。</p><p v-if="settingsError" role="status" class="ae-error">{{ settingsError }}</p><button type="button" class="ae-link" :disabled="locked || !!previewBusy" @click="editor.reloadSettings">重读设置</button><button v-if="pendingSettings && !directoryOpen" type="button" class="ae-button ae-primary" :disabled="settingsBusy" @click="editor.saveDirectory()">重试原目录保存请求</button></div>
          </section>
          <div class="ae-editor-grid">
            <section class="ae-workspace" aria-label="RSS 来源配置">
              <div class="ae-source-tabs" role="group" aria-label="切换 RSS 来源"><button v-for="(source, index) in sources" :key="source.ref" type="button" :aria-pressed="activeRef === source.ref" @click="activeRef = source.ref"><span>{{ source.role === 'main' ? '主 RSS' : `备用 ${index}` }}</span><span v-if="source.groupName" class="ae-tab-group">{{ source.groupName }}</span><span class="ae-tab-state" :class="{ ready: animeSourceReady(source) }">{{ animeSourceReady(source) ? '已配置' : '草稿' }}</span></button><button type="button" class="ae-add-source" :disabled="locked || !!previewBusy || sources.length >= 5" @click="editor.addSource"><IconPlus :size="16" />添加备用</button></div>
              <AnimeSourceForm v-if="active" :key="active.ref" :source="active" :naming-target="editor.naming.target.value" :chapter-title="subject.title" :disabled="locked" :preview-busy="!!previewBusy" :now="now" @search="editor.search" @choose="editor.chooseEntry" @group="editor.groupChanged" @remove="editor.removeSource(active)" @open="editor.openSource" @sample="source => editor.readSample(source, true)" @cancel="editor.cancelSourceEdit" @apply="candidate => editor.applySource(active!, candidate)" />
            </section>


          </div>

          <AnimeReleasePreview v-if="active" :key="`preview-${active.ref}`" :source="active" :valid="editor.validPreview(active)" :busy="!!previewBusy" :disabled="locked" :now="now" :remaining="budget.remaining" :settings-available="!!settings" @generate="editor.runPreview(active, 'generate')" @load="editor.runPreview(active, 'load')" @page="editor.runPreview(active, 'page', { page: $event })" @select="(id, checked) => editor.selectRelease(active!, id, checked)" @dismiss="editor.dismissPending(active, $event)" @clear-recovery="editor.dismissRecovery(active)" />

          <div v-if="feedback" class="ae-save-feedback" role="status"><p>{{ feedback }}</p><button v-if="pendingSave" type="button" class="ae-button ae-primary" :disabled="saveBusy" @click="editor.retrySave">{{ saveBusy ? '核对保存结果…' : '重试原保存请求' }}</button><button v-if="duplicateID" type="button" class="ae-link" @click="openSaved(duplicateID)">打开已有配置</button><button v-if="conflict && local" type="button" class="ae-link" :disabled="locked || !!previewBusy" @click="reloadConfiguration">重新读取配置</button><button v-if="conflict" type="button" class="ae-link" :disabled="locked || !!previewBusy" @click="editor.reloadSettings">重新读取目录设置</button></div>
          <footer class="ae-save-bar"><p>{{ pendingSave || pendingSettings ? '保存结果待确认，输入已锁定保留。' : dirty ? '有未保存的修改' : '仅保存配置，不会开始下载' }}<span v-if="!pausedReady">主 RSS 与下载目录完整后可保存为暂停。</span></p><div><button type="button" class="ae-button" :disabled="locked || editor.naming.busy.value || editor.naming.searching.value || !!previewBusy || sources.some(source => source.searchBusy) || !settings" @click="editor.save('draft')">{{ saveBusy ? '保存中…' : '保存草稿' }}</button><button type="button" class="ae-button ae-primary" :disabled="locked || editor.naming.busy.value || editor.naming.searching.value || !!previewBusy || sources.some(source => source.searchBusy) || !pausedReady" @click="editor.save('paused')">保存为暂停</button></div></footer>
        </template>
      </template>
    </div>
    <P115DirectoryDialog v-if="directoryOpen && settings?.primaryAccount" :token="token" :account-id="settings.primaryAccount.id" title="动漫订阅下载目录" description="固定使用主账号，为所有动漫订阅保存下载目录。" :allow-create="false" :saving="settingsBusy" :save-error="settingsError" :confirm-label="pendingSettings ? '重试原保存请求' : '保存此目录'" @close="() => { if (!pendingSettings) directoryOpen = false }" @select="editor.saveDirectory($event.id)" />
  </ManagementLayout>
</template>
