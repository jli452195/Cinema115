<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { IconFolder } from '@tabler/icons-vue'
import P115ActionDialog from './P115ActionDialog.vue'
import P115DirectoryDialog from './P115DirectoryDialog.vue'
import { useAnimeStorageSettings } from '../composables/useAnimeStorageSettings'
import type { AnimeSettingsView } from '../types/animeSubscription'

const props = defineProps<{ token: string }>()
const emit = defineEmits<{ close: []; loaded: [settings: AnimeSettingsView]; changed: [settings: AnimeSettingsView] }>()
const storage = useAnimeStorageSettings(props.token, settings => emit('changed', settings))
const { settings, loading, readError, error, notice, pending, saving, locked } = storage
const directoryKind = ref<'download' | 'organize'>('download'), directoryOpen = ref(false), feedback = ref<HTMLElement>()
const previousOverflow = document.body.style.overflow
const unavailable = computed(() => settings.value?.readiness.issues.includes('ACCOUNT_SNAPSHOT_CHANGED'))
watch(settings, value => { if (value) emit('loaded', value) })
function openDirectory(kind: 'download' | 'organize') {
  if (locked.value || !settings.value?.primaryAccount) return
  directoryKind.value = kind; directoryOpen.value = true
}
async function saveDirectory(cid: string) {
  await storage.saveDirectory(directoryKind.value, cid)
  if (!error.value || pending.value) {
    directoryOpen.value = false
    await nextTick()
    if (pending.value) feedback.value?.focus({ preventScroll: true })
  }
}
async function confirmPending() {
  const trigger = document.activeElement
  await storage.confirmPending(); await nextTick()
  if (trigger instanceof HTMLElement && !trigger.isConnected && document.activeElement === document.body) feedback.value?.focus({ preventScroll: true })
}
onMounted(() => { void storage.readSettings() })
// Both native dialogs restore their own lock. A route change can unmount the
// outer dialog first, so restore the original value after both children finish.
onUnmounted(() => { document.body.style.overflow = previousOverflow })
</script>

<template>
  <P115ActionDialog title="动漫存储设置" :busy="saving" @close="emit('close')">
    <div class="anime-storage-settings" :aria-busy="loading || saving">
      <p class="storage-note">所有订阅共用，选择目录后立即保存。</p>
      <p v-if="loading && !settings" role="status" class="storage-note">正在读取目录设置…</p>
      <template v-if="settings">
        <p v-if="settings.primaryAccount" class="storage-account">115 主账号<span>{{ settings.primaryAccount.name }}</span></p>
        <p v-else class="storage-note">请先在 115 助手配置主账号。<RouterLink to="/helper115" class="storage-link">配置主账号</RouterLink></p>
        <section v-for="kind in (['download', 'organize'] as const)" :key="kind" class="storage-directory" :aria-label="kind === 'download' ? '下载目录1' : '整理目录2'">
          <div class="storage-heading"><IconFolder :size="18" aria-hidden="true" /><h3>{{ kind === 'download' ? '下载目录1' : '整理目录2' }}</h3><button type="button" class="storage-button" :disabled="locked || !settings.primaryAccount || kind === 'organize' && !settings.readiness.ready" @click="openDirectory(kind)">{{ kind === 'download' ? '选择下载目录' : '选择整理目录' }}</button></div>
          <p class="storage-path">{{ (kind === 'download' ? settings.settings.downloadPath : settings.settings.organizePath) || '尚未配置' }}</p>
          <p class="storage-note">{{ kind === 'download' ? '每条资源保存到独立子目录。' : '标准命名后，将视频与配对字幕移动至此。' }}</p>
        </section>
        <p v-if="settings.primaryAccount && !settings.readiness.ready" class="storage-note">{{ unavailable ? '主账号已变化，请重新选择下载目录。' : '先选择下载目录，再配置整理目录。' }}</p>
      </template>
      <div v-if="readError" class="storage-feedback" role="status"><p>{{ readError }}</p><button type="button" class="storage-link" :disabled="loading || saving" @click="storage.readSettings">重新读取设置</button></div>
      <div ref="feedback" class="storage-feedback" role="status" aria-live="polite" tabindex="-1">
        <p v-if="error">{{ error }}</p><p v-else-if="notice">{{ notice }}</p>
        <template v-if="pending"><p>待核对{{ pending.kind === 'download' ? '下载' : '整理' }}目录保存结果，请先核对原请求。</p><button type="button" class="storage-button" :disabled="saving" @click="confirmPending">{{ saving ? '核对中…' : '核对原目录保存' }}</button></template>
      </div>
      <p class="storage-note storage-footnote">修改只影响之后新建的任务，已有任务沿用原目录。</p>
    </div>
  </P115ActionDialog>
  <P115DirectoryDialog v-if="directoryOpen && settings?.primaryAccount" :token="token" :account-id="settings.primaryAccount.id" :title="directoryKind === 'organize' ? '动漫整理目录2' : '动漫订阅下载目录'" description="使用主账号；下载目录与整理目录需分开。" :allow-create="false" :saving="saving" :save-error="error" :confirm-label="directoryKind === 'organize' ? '保存整理目录' : '保存此目录'" @close="directoryOpen = false" @select="saveDirectory($event.id)" />
</template>

<style scoped>
.anime-storage-settings { display: grid; gap: 14px; }
.storage-note { color: rgb(var(--color-slate-500)); font-size: 12px; line-height: 1.65; }
.storage-account { display: flex; flex-wrap: wrap; gap: 6px 12px; font-size: 12px; color: rgb(var(--color-slate-500)); }
.storage-account span { color: rgb(var(--color-slate-700)); overflow-wrap: anywhere; }
.storage-directory { min-width: 0; padding: 14px; border-radius: 12px; background: rgb(148 163 184 / .07); }
.storage-heading { display: flex; align-items: center; gap: 7px; color: rgb(var(--color-slate-500)); }
.storage-heading h3 { flex: 1; min-width: 0; color: rgb(var(--color-slate-700)); font-size: 13px; font-weight: 600; }
.storage-button, .storage-link { color: #2563eb; font-size: 12px; }
.storage-button { min-height: 40px; padding: 6px 9px; border-radius: 9px; background: rgb(59 130 246 / .08); }
.storage-button:disabled { opacity: .45; cursor: not-allowed; }
.storage-link { min-height: 40px; display: inline-flex; align-items: center; padding-inline: 4px; }
.storage-path { margin-block: 4px 6px; font-size: 13px; overflow-wrap: anywhere; color: rgb(var(--color-slate-700)); }
.storage-feedback { min-width: 0; font-size: 12px; color: rgb(var(--color-slate-600)); overflow-wrap: anywhere; }
.storage-feedback:empty { display: none; }
.storage-feedback:focus-visible, .storage-button:focus-visible, .storage-link:focus-visible { outline: 2px solid #60a5fa; outline-offset: 3px; }
.storage-footnote { font-size: 11px; }
.dark .storage-account span, .dark .storage-heading h3, .dark .storage-path { color: #e2e8f0; }
.dark .storage-button, .dark .storage-link { color: #93c5fd; }
.dark .storage-feedback { color: #cbd5e1; }
@media (max-width: 380px) { .storage-directory { padding: 10px; } .storage-heading { flex-wrap: wrap; } .storage-heading .storage-button { margin-left: auto; } }
</style>
