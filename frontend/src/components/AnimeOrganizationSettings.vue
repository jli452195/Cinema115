<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import AnimeStorageSettingsDialog from './AnimeStorageSettingsDialog.vue'
import AnimeOrganizationPreview from './AnimeOrganizationPreview.vue'
import { animeConfigMessage, animeConfigRequest } from '../lib/anime-config-api'
import type { AnimeSettingsView, AnimeSubscriptionView } from '../types/animeSubscription'
const props = defineProps<{ token: string; view: AnimeSubscriptionView; locked: boolean }>()
const emit = defineEmits<{ changed: [] }>()
const opened = ref(false), settingsOpen = ref(false), settings = ref<AnimeSettingsView>(), loading = ref(false), readError = ref('')
const controller = new AbortController()
async function readSettings() {
  if (loading.value) return
  loading.value = true; readError.value = ''
  try { settings.value = await animeConfigRequest<AnimeSettingsView>(props.token, 'settings', { signal: controller.signal }) }
  catch (reason) { if (!controller.signal.aborted) readError.value = animeConfigMessage(reason) }
  finally { loading.value = false }
}
const busy = computed(() => props.locked || loading.value)
function settingsChanged(value: AnimeSettingsView) { settings.value = value; emit('changed') }
function toggle(event: Event) { opened.value = (event.target as HTMLDetailsElement).open; if (opened.value && !settings.value) void readSettings() }
onBeforeUnmount(() => controller.abort())
</script>
<template>
  <section class="as-detail-section as-organization" aria-label="整理计划" :aria-busy="loading">
    <details @toggle="toggle"><summary>整理计划<span class="as-muted">标准命名整理</span></summary>
      <div v-if="opened" class="as-organization-body">
        <p class="as-muted">下载完成后按标准命名，将视频与已配对字幕从目录1移动到目录2。附件和空目录保留，旧任务沿用原方案。</p>
        <p v-if="!view.naming" class="as-muted">请先在订阅配置中关联 TMDB，并设置来源季集对应。</p>
        <dl v-if="settings" class="as-source-fields"><div><dt>下载目录1</dt><dd>{{ settings.settings.downloadPath || '尚未配置' }}</dd></div><div><dt>整理目录2</dt><dd>{{ settings.settings.organizePath || '尚未配置' }}</dd></div></dl>
        <button type="button" class="as-text-button" :disabled="busy" aria-label="动漫存储设置" @click="settingsOpen = true">存储设置</button>
        <p v-if="settings && !settings.readiness.ready" class="as-muted">主账号或下载目录已变化，请先重新配置下载目录。</p>
        <p v-if="readError" class="as-muted" role="status">{{ readError }}</p><button v-if="readError" type="button" class="as-text-button" :disabled="loading" @click="readSettings">重读目录设置</button>

        <AnimeOrganizationPreview :key="`${view.revision}:${settings?.settings.revision}`" :token="token" :path="`subscriptions/${view.id}/organization-preview`" :disabled="busy" />
      </div>
    </details>
    <AnimeStorageSettingsDialog v-if="settingsOpen" :token="token" @close="settingsOpen = false" @loaded="settings = $event; readError = ''" @changed="settingsChanged" />
  </section>
</template>
