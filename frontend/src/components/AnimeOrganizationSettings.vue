<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import P115DirectoryDialog from './P115DirectoryDialog.vue'
import AnimeOrganizationPreview from './AnimeOrganizationPreview.vue'
import { useAnimeSubscriptionActions } from '../composables/useAnimeSubscriptionActions'
import { animeConfigMessage, animeConfigRequest } from '../lib/anime-config-api'
import type { AnimeSettingsView, AnimeSubscriptionView } from '../types/animeSubscription'
const props = defineProps<{ token: string; view: AnimeSubscriptionView; locked: boolean }>()
const emit = defineEmits<{ changed: [] }>()
const opened = ref(false), directoryOpen = ref(false), settings = ref<AnimeSettingsView>(), loading = ref(false), readError = ref('')
const controller = new AbortController()
async function readSettings() {
  if (loading.value) return
  loading.value = true; readError.value = ''
  try { settings.value = await animeConfigRequest<AnimeSettingsView>(props.token, 'settings', { signal: controller.signal }) }
  catch (reason) { if (!controller.signal.aborted) readError.value = animeConfigMessage(reason) }
  finally { loading.value = false }
}
const directory = useAnimeSubscriptionActions(props.token, props.view.id, async () => { directoryOpen.value = false; await readSettings(); emit('changed') }, 'organization-directory')
const busy = computed(() => props.locked || directory.locked.value || loading.value)
function saveDirectory(cid: string) {
  if (directory.pending.value) { void directory.confirmPending(); return }
  if (!settings.value || busy.value) return
  void directory.execute('settings/organize-directory', { expectedRevision: settings.value.settings.revision, cid }, '保存整理目录', 'PATCH')
}
function toggle(event: Event) { opened.value = (event.target as HTMLDetailsElement).open; if (opened.value && !settings.value) void readSettings() }
onBeforeUnmount(() => controller.abort())
</script>
<template>
  <section class="as-detail-section as-organization" aria-label="整理计划" :aria-busy="loading || directory.loading.value">
    <details @toggle="toggle"><summary>整理计划<span class="as-muted">标准命名整理</span></summary>
      <div v-if="opened" class="as-organization-body">
        <p class="as-muted">下载完成后按标准命名，将视频与已配对字幕从目录1移动到目录2。附件和空目录保留，旧任务沿用原方案。</p>
        <p v-if="!view.naming" class="as-muted">请先在订阅配置中关联 TMDB，并设置来源季集对应。</p>
        <dl v-if="settings" class="as-source-fields"><div><dt>下载目录1</dt><dd>{{ settings.settings.downloadPath || '尚未配置' }}</dd></div><div><dt>整理目录2</dt><dd>{{ settings.settings.organizePath || '尚未配置' }}<button v-if="settings.primaryAccount && settings.readiness.ready" type="button" class="as-text-button" :disabled="busy" @click="directoryOpen = true">选择整理目录</button></dd></div></dl>
        <p v-if="settings && !settings.readiness.ready" class="as-muted">主账号或下载目录已变化，请先重新配置下载目录。</p>
        <p v-if="readError" class="as-muted" role="status">{{ readError }}</p><button v-if="readError" type="button" class="as-text-button" :disabled="loading" @click="readSettings">重读目录设置</button>
        <div class="as-operation-feedback" role="status"><p>{{ directory.error.value || (directory.notice.value ? '整理目录已保存' : '') }}</p><button v-if="directory.pending.value && !directory.loading.value" type="button" class="as-text-button" @click="directory.confirmPending">核对原目录保存</button></div>
        <AnimeOrganizationPreview :key="`${view.revision}:${settings?.settings.revision}`" :token="token" :path="`subscriptions/${view.id}/organization-preview`" :disabled="busy" />
      </div>
    </details>
    <P115DirectoryDialog v-if="directoryOpen && settings?.primaryAccount" :token="token" :account-id="settings.primaryAccount.id" title="动漫整理目录2" description="与下载目录1分开；所有订阅共用，旧任务保留原目标。" :allow-create="false" :saving="directory.loading.value" :save-error="directory.error.value" :confirm-label="directory.pending.value ? '核对原目录保存' : '保存整理目录'" @close="directoryOpen = false" @select="saveDirectory($event.id)" />
  </section>
</template>
