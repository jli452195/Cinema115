<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { IconPlus, IconCloud, IconFolder, IconFolderCheck, IconEdit, IconTrash, IconRefresh, IconSearch, IconLoader2, IconSettings, IconCode, IconChevronRight, IconClock } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import STRMDirectoryDialog from '../components/STRMDirectoryDialog.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import SelectMenu from '../components/SelectMenu.vue'
import { useDriveSTRMState } from '../composables/useDriveSTRMState'
import { useDriveSTRMEditor } from '../composables/useDriveSTRMEditor'
import type { Mapping } from '../types/driveSTRM'
import { active, pendingMetadata, downloadingMetadata, date, intervalLabel } from '../utils/driveSTRM'
import { strmDirectoryPath } from '../utils/strmDirectory'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const remote = useDriveSTRMState(() => props.token)
const {
  state, settingsDraft, busy, error, dialogError, notice, loading,
  settingsDirty, needsSettingsSave, canStart, hasSchedule, startHint, strmPreview,
  changedAccount, eventConsumerErrors, attachmentMessage, refresh, saveSettings, resetSettings,
  eventLabel, scheduleLabel, run, toggle, stop, status,
} = remote
const drafts = useDriveSTRMEditor(remote)
const {
  editor, deleting, sourceDialog, outputDialog, editorDirty, editorRunning,
  directoryReadOnly, editorSaveLabel, canSaveEditor, closeEditor, selectSource, pickedSource, requestDelete,
} = drafts
const addButton = ref<HTMLButtonElement>()
let editorTrigger: HTMLElement | null = null
const unsaved = computed(() => editorDirty.value || settingsDirty.value)
function edit(mapping?: Mapping) {
  const trigger = document.activeElement instanceof HTMLElement ? document.activeElement : null
  if (drafts.edit(mapping)) editorTrigger = trigger
}
async function save() {
  const trigger = editorTrigger
  await drafts.save()
  if (!editor.value) { await nextTick(); if (trigger?.isConnected) trigger.focus({ preventScroll: true }) }
}
async function remove() {
  await drafts.remove()
  if (!deleting.value) { await nextTick(); addButton.value?.focus() }
}
const scanSchedules = [
  { enabled: 'reconcileEnabled', interval: 'reconcileIntervalHours', name: '增量核对', intervals: [6, 12, 24, 72, 168], help: '比对目录树与补齐缺失，减少资料请求' },
  { enabled: 'fullScanEnabled', interval: 'fullScanIntervalHours', name: '全量扫描', intervals: [24, 72, 168, 336, 720], help: '读取全部文件资料，核验同路径替换' },
] as const
function beforeUnload(e: BeforeUnloadEvent) { if (unsaved.value || busy.value) { e.preventDefault(); e.returnValue = '' } }
onBeforeRouteLeave(() => {
  if (busy.value) { (editor.value || deleting.value ? dialogError : error).value = '正在处理当前操作，请完成后再切换页面'; return false }
  return !unsaved.value || window.confirm('放弃未保存的修改？')
})
onMounted(() => { document.title = '115网盘STRM · Cinema115'; window.addEventListener('beforeunload', beforeUnload) })
onBeforeUnmount(() => { window.removeEventListener('beforeunload', beforeUnload) })
</script>

<template>
  <ManagementLayout title="115网盘STRM" description="从主网盘生成本地 STRM，保留原目录层级。" :parent="{ label: '115 网盘', to: '/115' }" @logout="$emit('logout')">
    <p v-if="error" role="alert" class="ds-alert">{{ error }} <button type="button" class="underline" :disabled="!!busy" @click="refresh">重新加载</button></p>
    <p v-if="notice" role="status" class="ds-notice">{{ notice }}</p>
    <p v-if="state && !state.accountReady" class="ds-alert">请先在 <RouterLink to="/helper115" class="underline">115 助手</RouterLink> 配置并登录主账号，基础设置可先保存。</p>
    <p v-if="state?.events?.lastError" role="status" class="ds-alert">事件检查暂不可用：{{ state.events.lastError }}。仍可完整扫描补齐。</p>
    <p v-for="item in eventConsumerErrors" :key="item.name" role="status" class="ds-alert">{{ item.label }}事件处理暂缓：{{ item.message }}。后台将自动重试，生活事件采集独立运行。</p>
    <p v-if="attachmentMessage" role="status" class="ds-alert">{{ attachmentMessage }}</p>
    <div v-if="loading" class="ds-empty" role="status">正在读取配置…</div>
    <template v-else-if="state && settingsDraft">
      <form class="ds-settings" aria-label="基础设置" @submit.prevent="saveSettings">
        <div class="ds-section-heading">
          <h2><IconSettings :size="19" aria-hidden="true" />基础设置</h2>
          <div class="ds-save-actions">
            <button v-if="settingsDirty" type="button" class="ds-text-button" :disabled="!!busy" @click="resetSettings">还原修改</button>
            <button type="submit" class="ds-button ds-primary" :disabled="!!busy || !needsSettingsSave">{{ busy === 'settings' ? '保存中…' : needsSettingsSave ? '保存设置' : '已保存' }}</button>
          </div>
        </div>
        <p class="ds-help">所有目录共用；图片策略用于后续任务，降低并发或延长取链间隔也会约束待补附件。</p>
        <fieldset :disabled="!!busy" class="ds-settings-grid">
          <div><label class="ds-outlined"><span>STRM 播放地址</span><input v-model="settingsDraft.baseUrl" type="url" required placeholder="http://NAS_IP:8080" aria-describedby="ds-base-help" /></label><p id="ds-base-help" class="ds-help">填写播放器或 Emby 能访问的 Cinema115 地址。</p></div>
          <div><label class="ds-outlined"><span>视频后缀</span><input v-model="settingsDraft.videoExtensions" required placeholder="mp4,mkv,avi" aria-describedby="ds-video-help" /></label><p id="ds-video-help" class="ds-help">逗号分隔，为匹配的视频生成 STRM。</p></div>
          <div><label class="ds-outlined"><span>元数据后缀</span><input v-model="settingsDraft.metadataExtensions" placeholder="srt,ass,ssa" aria-describedby="ds-metadata-help" /></label><p id="ds-metadata-help" class="ds-help">按原名下载字幕、NFO 或图片，留空不下载；单个最多 32 MB。</p></div>
          <div><label class="ds-outlined"><span>最小文件大小（MB）</span><input v-model.number="settingsDraft.minSizeMB" type="number" required min="0" max="1073741824" step="1" aria-describedby="ds-size-help" /></label><p id="ds-size-help" class="ds-help">0 表示不限制，仅过滤视频，不影响元数据。</p></div>
          <div class="ds-download-controls">
            <div class="ds-select-field ds-image-mode"><span class="ds-field-label">图片下载策略</span><SelectMenu v-model="settingsDraft.metadataImageMode" label="图片下载策略" :options="[{ value: 'cdn', label: 'CDN 预览图优先' }, { value: 'original', label: '原文件' }]" :disabled="!!busy" aria-describedby="ds-image-help" class="w-full" /></div>
            <label class="ds-outlined ds-rate-input"><span>附件下载并发</span><input v-model.number="settingsDraft.metadataConcurrency" type="number" required min="1" max="8" step="1" aria-describedby="ds-rate-help" /></label>
            <label class="ds-outlined ds-rate-input"><span>取链批次间隔（秒）</span><input v-model.number="settingsDraft.metadataBatchIntervalSeconds" type="number" required min="0" max="60" step="1" aria-describedby="ds-rate-help" /></label>
            <div class="ds-download-help"><p id="ds-image-help" class="ds-help">CDN 预览图可能经过压缩；有可用图片地址时直接下载，缺少地址时合并获取原文件链接。</p><p id="ds-rate-help" class="ds-help">每批最多合并 32 个附件取链；0 秒不设批次间隔。未变化文件跳过，403 或 429 后自动暂停附件下载，冷却后逐步恢复。</p></div>
          </div>
        </fieldset>
        <fieldset :disabled="!!busy" class="ds-event-setting">
          <div><span class="ds-event-title">生活事件</span><p id="ds-event-help" class="ds-help">所有监控目录共享一次读取，按变更更新。</p></div>
          <div class="ds-select-field"><span class="ds-field-label">事件检查间隔</span><SelectMenu v-model="settingsDraft.eventIntervalSeconds" label="事件检查间隔" :options="[{ value: 0, label: '关闭' }, { value: 20, label: '每 20 秒' }, { value: 60, label: '每 1 分钟' }, { value: 300, label: '每 5 分钟' }, { value: 900, label: '每 15 分钟' }, { value: 1800, label: '每 30 分钟' }]" :disabled="!!busy" aria-describedby="ds-event-help" class="w-full" /></div>
        </fieldset>
        <details class="ds-preview"><summary><IconChevronRight class="ds-chevron" :size="14" aria-hidden="true" /><IconCode :size="16" aria-hidden="true" />STRM 内容预览</summary><code>{{ strmPreview }}</code><p class="ds-help">示例字段，生成时替换为实际文件信息；支持播放器直接播放，也支持通过 Cinema115 的 Emby 反代播放。</p></details>
      </form>
      <section class="ds-directories" aria-label="监控目录">
        <div class="ds-section-heading">
          <h2><IconFolder :size="19" aria-hidden="true" />监控目录<span class="ds-section-count">{{ state.mappings.length }}</span></h2>
          <span :title="startHint"><button ref="addButton" type="button" class="ds-button ds-check" :disabled="!!busy || !canStart" @click="edit()"><IconPlus :size="17" aria-hidden="true" />添加目录</button></span>
        </div>
        <p v-if="startHint" class="ds-help ds-start-hint">{{ startHint }}，再添加或调整目录、运行扫描。</p>
        <p v-else-if="!hasSchedule" class="ds-help ds-start-hint">自动周期均已关闭；首次生成、手动操作及已接受的任务仍会执行。</p>
        <div v-if="!state.mappings.length" class="ds-empty"><IconFolder :size="22" aria-hidden="true" /><p>尚未添加目录，选择主网盘来源与本地输出目录即可开始。</p></div>
        <div v-else class="ds-grid">
          <article v-for="m in state.mappings" :key="m.id" class="ds-card" :aria-label="m.name">
            <header class="ds-heading"><span class="ds-symbol"><IconCloud :size="21" aria-hidden="true" /></span><div class="ds-name"><h3 :title="m.name">{{ m.name }}</h3><span class="ds-status" aria-live="polite" :class="{ running: active(m) || downloadingMetadata(m), attention: !!m.lastError || !!m.metadataFailed }"><IconLoader2 v-if="active(m) || downloadingMetadata(m)" class="ds-spin" :size="12" aria-hidden="true" />{{ status(m) }}</span></div><button :id="'ds-edit-' + m.id" type="button" class="ds-icon" :disabled="!!busy" title="编辑目录与自动任务" :aria-label="'编辑' + m.name" @click="edit(m)"><IconEdit :size="17" aria-hidden="true" /></button><button type="button" class="ds-icon" :disabled="!!busy || active(m)" :aria-label="'删除' + m.name" @click="requestDelete(m)"><IconTrash :size="17" aria-hidden="true" /></button></header>
            <dl class="ds-paths"><div><dt><IconCloud :size="15" aria-hidden="true" />来源</dt><dd :title="m.sourcePath">{{ m.sourcePath || m.sourceCID }}</dd></div><div><dt><IconFolderCheck :size="15" aria-hidden="true" />输出</dt><dd :title="strmDirectoryPath(m.outputDir)">{{ strmDirectoryPath(m.outputDir) }}</dd></div></dl>
            <div class="ds-card-schedule">
              <div class="ds-schedule-summary"><span class="ds-schedule-title">自动任务</span></div>
              <p class="ds-schedule-description" :aria-label="m.name + '自动任务摘要'"><span class="ds-chip-event">{{ eventLabel() }}</span><span class="ds-chip-increment">增量 {{ scheduleLabel(m, 'reconcile') }}</span><span class="ds-chip-full">全量 {{ scheduleLabel(m, 'fullScan') }}</span></p>
            </div>
            <div class="ds-counts"><span>本次视频 <b>{{ m.stats.videos }}</b></span><span>写入 {{ m.stats.written }}</span><span>未变 {{ m.stats.unchanged }}</span><span v-if="m.stats.metadataWritten">附件 {{ m.stats.metadataWritten }}</span><span v-if="m.metadataPending">待补附件 {{ m.metadataPending }}</span><span v-if="m.metadataFailed" class="ds-retrying">待重试 {{ m.metadataFailed }}</span><span v-if="m.stats.failed" class="ds-failed">失败 {{ m.stats.failed }}</span></div>
            <p v-if="changedAccount(m)" class="ds-failed ds-message">主账号已变化，请重新选择来源目录。</p><p v-else-if="m.lastError" class="ds-failed ds-message">{{ m.lastError }}</p>
            <div class="ds-meta"><span>最近扫描 {{ date(m.lastScanAt) }}</span><div><button v-if="active(m) || pendingMetadata(m)" type="button" :disabled="!!busy" :aria-label="'停止' + m.name + (active(m) ? '本次扫描' : '附件补齐')" @click="stop(m)">停止本次</button><button type="button" :disabled="!!busy || !m.enabled && (changedAccount(m) || !canStart)" :title="!m.enabled ? startHint : undefined" :aria-label="(m.enabled ? '暂停' : '开启') + m.name + '监控'" @click="toggle(m, !m.enabled)">{{ m.enabled ? '暂停监控' : '开启监控' }}</button></div></div>
            <footer class="ds-actions"><button type="button" class="ds-button ds-check" :disabled="!!busy || active(m) || changedAccount(m) || !canStart || !m.enabled" :title="startHint || (!m.enabled ? '开启监控后检查事件更新；也可直接完整扫描' : undefined)" @click="run(m, false)"><IconRefresh :size="16" aria-hidden="true" />{{ active(m) ? '检查中…' : '检查更新' }}</button><button type="button" class="ds-button" :disabled="!!busy || active(m) || changedAccount(m) || !canStart" :title="startHint" @click="run(m, true)"><IconSearch :size="16" aria-hidden="true" />完整扫描补齐</button></footer>
          </article>
        </div>
        <details class="ds-notes"><summary>同步说明</summary><p>生活事件按全局间隔共享读取，增量核对和全量扫描在每个目录卡片中独立设置；目录暂停后不参与自动任务。修改设置不额外安排扫描，已经接受的任务会继续；STRM 先生成，附件在后台补齐，失败附件单独重试。停止本次会清除未完成的附件待办，已生成文件保留；已有 STRM 可手动完整扫描更新。同一文件改名或移动后，新输出生成成功才清理旧输出及空目录，不保留手动修改；云端删除不自动删除本地文件。目录树导出 TXT 由系统清理，网盘源文件保留。</p></details>
      </section>
    </template>
    <P115ActionDialog v-if="editor" :title="editor.id ? '编辑目录' : '新增目录'" size="wide" layout="contained" :busy="!!busy" @close="closeEditor">
      <form id="drive-strm-form" class="ds-editor-form" :aria-label="(editor.name || '新目录') + '设置'" novalidate @submit.prevent="save">
        <p v-if="dialogError" role="alert" class="ds-editor-error">{{ dialogError }}</p>
        <p v-if="changedAccount(editor)" role="alert" class="ds-editor-error">主账号已变化，请重新选择来源目录。</p>
        <p v-else-if="directoryReadOnly" class="ds-editor-alert">{{ editorRunning ? '此目录正在执行任务，目录信息只读；自动任务可修改并于下个周期生效。' : startHint + '。当前可单独调整自动任务。' }}</p>

        <!-- 目录基本信息 -->
        <section aria-labelledby="ds-directory-heading" class="ds-editor-section">
          <div class="ds-editor-section-header">
            <h3 id="ds-directory-heading" class="ds-editor-section-heading">
              <IconFolder :size="16" aria-hidden="true" />目录信息
            </h3>
            <span v-if="editor.id && !directoryReadOnly" class="ds-editor-subtle-hint">变更路径将重新扫描</span>
          </div>

          <!-- 桌面端目录名称与自动监控同行 -->
          <div class="ds-editor-row ds-name-monitor-row">
            <div class="ds-field ds-name-field">
              <label for="ds-dir-name" class="ds-field-label">名称</label>
              <input
                id="ds-dir-name"
                v-model="editor.name"
                required
                maxlength="60"
                placeholder="例如：动漫媒体库"
                :disabled="!!busy || directoryReadOnly"
                class="ds-input"
              />
            </div>
            <div class="ds-field ds-monitor-field">
              <span class="ds-field-label">监控设置</span>
              <div class="ds-monitor-box">
                <SettingSwitch
                  v-model="editor.enabled"
                  label="自动监控"
                  :disabled="!!busy || directoryReadOnly"
                />
              </div>
            </div>
          </div>

          <!-- 桌面端来源与本地输出双列紧凑排布 -->
          <div class="ds-editor-row ds-paths-row">
            <div class="ds-field ds-source-field">
              <span id="ds-source-label" class="ds-field-label">主网盘来源</span>
              <button
                type="button"
                class="ds-directory-picker"
                :disabled="!!busy || directoryReadOnly"
                aria-labelledby="ds-source-label ds-source-value"
                @click="selectSource"
              >
                <IconCloud :size="16" class="ds-field-icon" aria-hidden="true" />
                <span id="ds-source-value" class="ds-directory-text" :title="editor.sourcePath || '选择云端目录'">
                  {{ editor.sourcePath || '选择云端目录' }}
                </span>
                <span class="ds-picker-action">选择</span>
              </button>
              <p class="ds-field-hint" title="主账号网盘目录">主账号网盘目录</p>
            </div>
            <div class="ds-field ds-output-field">
              <label for="ds-output" class="ds-field-label">本地输出</label>
              <div class="ds-output-group">
                <input
                  id="ds-output"
                  v-model="editor.outputDir"
                  required
                  placeholder="例如：动漫"
                  aria-describedby="ds-output-help"
                  :disabled="!!busy || directoryReadOnly"
                  class="ds-input"
                />
                <button
                  type="button"
                  class="ds-button ds-picker-btn"
                  :disabled="!!busy || directoryReadOnly"
                  @click="outputDialog = true"
                >
                  选择
                </button>
              </div>
              <p id="ds-output-help" class="ds-field-hint" :title="strmDirectoryPath(editor.outputDir)">
                {{ strmDirectoryPath(editor.outputDir) }}
              </p>
            </div>
          </div>
        </section>

        <!-- 自动任务设置（双列增量与全量） -->
        <section aria-labelledby="ds-schedule-heading" class="ds-editor-section ds-schedule-section">
          <div class="ds-editor-section-header">
            <h3 id="ds-schedule-heading" class="ds-editor-section-heading">
              <IconClock :size="16" aria-hidden="true" />自动任务
            </h3>
            <span class="ds-editor-subtle-hint">独立周期执行</span>
          </div>

          <div class="ds-schedule-grid">
            <div
              v-for="schedule in scanSchedules"
              :key="schedule.enabled"
              class="ds-schedule-card"
            >
              <div class="ds-schedule-card-header">
                <SettingSwitch
                  v-model="editor.schedule[schedule.enabled]"
                  :label="'自动' + schedule.name"
                  :disabled="!!busy"
                  class="ds-schedule-toggle"
                />
                <div class="ds-schedule-interval">
                  <label :for="'ds-interval-' + schedule.enabled" class="ds-sr">{{ schedule.name }}周期</label>
                  <SelectMenu
                    :id="'ds-interval-' + schedule.enabled"
                    v-model="editor.schedule[schedule.interval]"
                    :label="schedule.name + '周期'"
                    :options="schedule.intervals.map(hours => ({ value: hours, label: intervalLabel(hours) }))"
                    :disabled="!!busy || !editor.schedule[schedule.enabled]"
                    class="w-full"
                  />
                </div>
              </div>
              <p class="ds-schedule-card-hint">{{ schedule.help }}</p>
            </div>
          </div>

          <p class="ds-editor-bottom-note">生活事件按全局设置共享；{{ !editor.enabled ? '此目录已暂停监控，恢复后生效。' : '保存自动任务不触发重新扫描，当前任务继续完成。' }}</p>
          <p v-if="settingsDirty" class="ds-editor-bottom-note ds-dirty-warn">基础设置有未保存的修改，不会影响此目录。</p>
        </section>
      </form>
      <template #footer>
        <span class="ds-editor-footer-note">{{ editorDirty ? '有未保存的修改' : '各目录独立配置' }}</span>
        <button type="button" class="ds-button" :disabled="!!busy" @click="closeEditor">取消</button>
        <button type="submit" form="drive-strm-form" class="ds-button ds-primary" :disabled="!!busy || !canSaveEditor">{{ busy === 'save' ? '保存中…' : editorSaveLabel }}</button>
      </template>
    </P115ActionDialog>
    <P115DirectoryDialog v-if="sourceDialog && editor" :token="token" :account-id="editor.accountId" :allow-root="false" title="选择主网盘来源目录" @close="sourceDialog = false" @select="pickedSource" />
    <STRMDirectoryDialog v-if="outputDialog" :token="token" title="选择本地输出目录" @close="outputDialog = false" @select="path => { if (editor) editor.outputDir = path; outputDialog = false }" />
    <P115ActionDialog v-if="deleting" title="删除目录配置" :busy="!!busy" @close="deleting = undefined"><p>删除「{{ deleting.name }}」的配置与索引？已生成的本地文件和网盘文件都会保留。</p><p v-if="dialogError" role="alert" class="ds-failed">{{ dialogError }}</p><template #footer><button type="button" class="ds-button" :disabled="!!busy" @click="deleting = undefined">取消</button><button type="button" class="ds-button ds-danger" :disabled="!!busy" @click="remove">{{ busy ? '删除中…' : '删除配置' }}</button></template></P115ActionDialog>
  </ManagementLayout>
</template>

<style scoped>
.ds-settings { @apply rounded-2xl bg-white dark:bg-slate-900; padding:20px; }
.ds-section-heading { display:flex; align-items:center; justify-content:space-between; gap:12px; }
.ds-section-heading h2 { @apply text-slate-800 dark:text-slate-100; display:flex; align-items:center; gap:8px; min-width:0; font-size:15px; font-weight:600; }
.ds-section-heading h2>svg { @apply text-blue-500; flex-shrink:0; }
.ds-section-count { @apply bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400; padding:1px 7px; border-radius:6px; font-size:12px; font-variant-numeric:tabular-nums; }
.ds-save-actions { display:flex; align-items:center; gap:12px; flex-shrink:0; }
.ds-help { @apply text-slate-500 dark:text-slate-400; font-size:12px; line-height:1.7; margin-top:7px; }
.ds-settings-grid { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:18px 20px; margin-top:22px; min-width:0; }
.ds-download-controls { @apply bg-slate-50 dark:bg-slate-800/50; grid-column:1/-1; display:flex; flex-wrap:wrap; align-items:center; gap:14px 18px; border-radius:10px; padding:13px 14px; }.ds-rate-input { width:160px; flex-shrink:0; }.ds-image-mode { width:200px; flex-shrink:0; }.ds-download-help { flex-basis:100%; min-width:0; }.ds-download-controls .ds-help { margin:0; }.ds-download-help .ds-help+.ds-help { margin-top:6px; }
.ds-event-setting { display:flex; align-items:center; gap:20px; margin-top:22px; min-width:0; }.ds-event-setting>div { flex:1; min-width:0; }.ds-event-title { @apply text-slate-700 dark:text-slate-200; font-size:13px; font-weight:500; }.ds-event-setting .ds-help { margin-top:3px; }.ds-event-setting>.ds-select-field { width:220px; flex:0 0 220px; }
.ds-select-field { display:flex; flex-direction:column; gap:4px; min-width:0; }
.ds-outlined { @apply border border-slate-300 dark:border-slate-600; position:relative; display:block; min-width:0; border-radius:8px; transition:border-color .15s; }
.ds-outlined:focus-within { @apply border-blue-500; box-shadow:0 0 0 1px #3b82f6; }
.ds-outlined>span { @apply bg-white text-slate-600 dark:bg-slate-900 dark:text-slate-300; position:absolute; top:-9px; left:9px; padding:0 5px; font-size:12px; line-height:17px; }
.ds-outlined>input { @apply text-slate-800 dark:text-slate-100; display:block; width:100%; min-width:0; height:40px; padding:0 13px; border:0; border-radius:inherit; background:transparent; font-size:13px; outline:none; }
.ds-outlined>input:focus-visible { outline:none; }
.ds-outlined:has(>input:disabled) { @apply border-slate-200 bg-slate-50 dark:border-slate-700 dark:bg-slate-800/50; }.ds-outlined>input:disabled { @apply text-slate-400 dark:text-slate-500; cursor:not-allowed; }
.ds-preview { margin-top:14px; }.ds-preview summary { @apply text-slate-600 dark:text-slate-300; display:flex; align-items:center; gap:6px; width:fit-content; min-height:32px; cursor:pointer; list-style:none; font-size:12px; }.ds-preview summary::-webkit-details-marker { display:none; }.ds-chevron { transition:transform .15s; }.ds-preview[open] .ds-chevron { transform:rotate(90deg); }.ds-preview code { @apply rounded-lg bg-slate-50 text-slate-700 dark:bg-slate-950 dark:text-slate-300; display:block; padding:10px 12px; font-size:12px; line-height:1.8; overflow-wrap:anywhere; }
.ds-directories { margin-top:24px; }.ds-directories>.ds-section-heading { margin-bottom:14px; }.ds-start-hint { margin:-4px 0 12px; }
.ds-grid { display:grid; grid-template-columns:repeat(auto-fill,minmax(min(100%,340px),1fr)); align-items:start; gap:14px; }
.ds-card { @apply rounded-2xl bg-white p-4 text-slate-700 dark:bg-slate-900 dark:text-slate-200; min-width:0; box-shadow:0 4px 16px -10px rgb(0 0 0 / .15),0 1px 3px rgb(0 0 0 / .035); transition:box-shadow .2s; }.ds-card:hover { box-shadow:0 9px 26px -14px rgb(0 0 0 / .2),0 2px 6px rgb(0 0 0 / .04); }
.ds-heading { display:flex; align-items:center; gap:8px; margin-bottom:16px; }.ds-symbol { @apply bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300; display:grid; place-items:center; width:36px; height:36px; border-radius:11px; flex-shrink:0; }.ds-name { flex:1; min-width:0; }.ds-name h3 { font-size:15px; font-weight:600; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
.ds-status { @apply text-slate-500 dark:text-slate-400; display:inline-flex; gap:5px; align-items:center; font-size:12px; margin-top:3px; }.ds-status.running { @apply text-blue-600 dark:text-blue-400; }.ds-status.attention { @apply text-amber-700 dark:text-amber-400; }
.ds-icon { @apply text-slate-400 hover:bg-slate-100 hover:text-blue-600 dark:hover:bg-slate-800; display:grid; place-items:center; width:32px; height:36px; border-radius:8px; flex-shrink:0; }
.ds-paths { display:grid; gap:8px; font-size:13px; }.ds-paths>div { display:flex; align-items:baseline; gap:12px; }.ds-paths dt { @apply text-slate-500 dark:text-slate-400; display:flex; align-items:center; gap:6px; flex-shrink:0; }.ds-paths dd { min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.ds-card-schedule { margin-top:15px; }.ds-schedule-summary { display:flex; align-items:center; gap:8px; }.ds-schedule-title { @apply text-slate-500 dark:text-slate-400; font-size:11px; }.ds-schedule-description { display:flex; flex-wrap:wrap; gap:6px; margin-top:6px; font-size:11px; line-height:1.7; }.ds-schedule-description>span { @apply bg-slate-50 text-slate-500 dark:bg-slate-800/70 dark:text-slate-400; border-radius:6px; padding:2px 7px; }

/* 目录编辑弹窗 - 紧凑双列与 HeroUI 风格中性控件 */
.ds-editor-form { margin:0; display:flex; flex-direction:column; gap:12px; }
.ds-editor-error { @apply rounded-lg bg-red-50 text-red-600 dark:bg-red-950/40 dark:text-red-400; padding:8px 12px; font-size:12px; line-height:1.5; overflow-wrap:anywhere; margin:0; }
.ds-editor-alert { @apply rounded-lg bg-amber-50 text-amber-800 dark:bg-amber-950/40 dark:text-amber-300; padding:8px 12px; font-size:12px; line-height:1.5; overflow-wrap:anywhere; margin:0; }
.ds-editor-section { display:flex; flex-direction:column; gap:8px; }
.ds-editor-section-header { display:flex; align-items:center; justify-content:space-between; gap:8px; }
.ds-editor-section-heading { @apply text-slate-800 dark:text-slate-100; display:flex; align-items:center; gap:6px; font-size:13px; font-weight:600; }
.ds-editor-section-heading>svg { @apply text-slate-400 dark:text-slate-500; }
.ds-editor-subtle-hint { @apply text-slate-400 dark:text-slate-500; font-size:11px; }

/* 字段与栅格排版 */
.ds-editor-row { display:flex; gap:12px; }
.ds-field { display:flex; flex-direction:column; gap:4px; min-width:0; }
.ds-field-label { @apply text-slate-600 dark:text-slate-300; font-size:12px; font-weight:500; }
.ds-field-hint { @apply text-slate-400 dark:text-slate-500; font-size:11px; line-height:1.4; margin:2px 0 0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }

/* 桌面端名称与自动监控同行 */
.ds-name-monitor-row { align-items:flex-end; }
.ds-name-field { flex:1; }
.ds-monitor-field { width:190px; flex-shrink:0; }
.ds-monitor-box { @apply border border-slate-200 bg-slate-50/80 dark:border-slate-800 dark:bg-slate-800/40; display:flex; align-items:center; height:36px; padding:0 10px; border-radius:8px; }
.ds-monitor-box :deep(label) { width:100%; display:flex; flex-direction:row-reverse; align-items:center; justify-content:space-between; gap:8px; cursor:pointer; }
.ds-monitor-box :deep(span.text-sm) { font-size:12px; font-weight:500; }
.ds-monitor-box:has(input:disabled) { @apply border-slate-200 bg-slate-50 opacity-60 dark:border-slate-800 dark:bg-slate-900/50; cursor:not-allowed; }
.ds-monitor-box:has(input:disabled) :deep(label) { cursor:not-allowed; }

/* 输入框通用 */
.ds-input { @apply border border-slate-200 bg-white text-slate-800 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-100; height:36px; padding:0 10px; border-radius:8px; font-size:13px; width:100%; min-width:0; box-sizing:border-box; transition:border-color .15s,box-shadow .15s; }
.ds-input:focus { @apply border-blue-500; outline:none; box-shadow:0 0 0 1px #3b82f6; }
.ds-input:disabled { @apply border-slate-200 bg-slate-50 text-slate-400 dark:border-slate-800 dark:bg-slate-900/50 dark:text-slate-500; cursor:not-allowed; }

/* 桌面端来源与输出双列紧凑 */
.ds-paths-row { display:grid; grid-template-columns:1fr 1fr; gap:12px; }
.ds-directory-picker { @apply border border-slate-200 bg-slate-50/80 hover:bg-slate-100 text-slate-700 dark:border-slate-700 dark:bg-slate-800/40 dark:hover:bg-slate-800 dark:text-slate-200; display:flex; align-items:center; gap:6px; width:100%; height:36px; padding:0 10px; border-radius:8px; font-size:13px; text-align:left; box-sizing:border-box; min-width:0; cursor:pointer; transition:background-color .15s,border-color .15s; }
.ds-directory-picker:disabled { @apply cursor-not-allowed opacity-50 bg-slate-50 dark:bg-slate-900; }
.ds-directory-text { flex:1; min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; font-size:12px; }
.ds-field-icon { @apply text-slate-400 dark:text-slate-500; flex-shrink:0; }
.ds-picker-action { @apply text-blue-600 dark:text-blue-400; font-size:12px; font-weight:500; flex-shrink:0; }
.ds-output-group { display:flex; gap:6px; width:100%; min-width:0; }
.ds-output-group .ds-input { flex:1; }
.ds-picker-btn { flex-shrink:0; height:36px; min-height:36px; padding:0 12px; font-size:12px; }

/* 自动任务双列卡片 */
.ds-schedule-section { @apply border-t border-slate-100 dark:border-slate-800/80; margin-top:6px; padding-top:10px; gap:8px; }
.ds-schedule-grid { display:grid; grid-template-columns:1fr 1fr; gap:12px; }
.ds-schedule-card { @apply border border-slate-200/90 bg-slate-50/60 dark:border-slate-800 dark:bg-slate-800/30; border-radius:10px; padding:10px 12px; display:flex; flex-direction:column; justify-content:space-between; min-width:0; gap:5px; }
.ds-schedule-card-header { display:flex; align-items:center; justify-content:space-between; gap:8px; min-width:0; }
.ds-schedule-toggle { flex:1; min-width:0; }
.ds-schedule-toggle :deep(label) { display:flex; align-items:center; gap:6px; cursor:pointer; }
.ds-schedule-toggle :deep(span.text-sm) { font-size:12px; font-weight:500; }
.ds-schedule-interval { flex-shrink:0; }
.ds-schedule-card-hint { @apply text-slate-500 dark:text-slate-400; font-size:11px; line-height:1.4; margin:0; }

/* 底部状态提示文案 */
.ds-editor-bottom-note { @apply text-slate-400 dark:text-slate-500; font-size:11px; line-height:1.4; margin:0; }
.ds-dirty-warn { @apply text-amber-600 dark:text-amber-400; }
.ds-editor-footer-note { @apply text-slate-400 dark:text-slate-500; align-self:center; margin-right:auto; font-size:12px; }

/* 开关蓝色高亮与无障碍焦点圈 */
.ds-editor-form :deep(input[role='switch']:checked + span) { background-color:#2563eb; }
.ds-editor-form :deep(input[role='switch']:focus-visible + span) { --tw-ring-color:#60a5fa; }

.ds-counts { @apply bg-slate-50 dark:bg-slate-800/60; display:flex; flex-wrap:wrap; gap:5px 13px; border-radius:9px; padding:9px 10px; font-size:12px; margin-top:14px; }.ds-counts b { font-size:14px; font-variant-numeric:tabular-nums; }
.ds-retrying { @apply text-amber-700 dark:text-amber-400; }
.ds-meta { @apply text-slate-500 dark:text-slate-400; display:flex; flex-wrap:wrap; justify-content:space-between; align-items:center; gap:5px; font-size:12px; margin:10px 0; }.ds-meta button { @apply text-blue-600 dark:text-blue-400; padding:5px 0 5px 8px; }.ds-message { font-size:12px; line-height:1.6; margin-top:8px; overflow-wrap:anywhere; }.ds-failed { @apply text-red-600 dark:text-red-400; }
.ds-actions { display:grid; grid-template-columns:1fr 1fr; gap:8px; }.ds-button { @apply bg-slate-100 text-slate-700 hover:bg-slate-200 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700; display:inline-flex; align-items:center; justify-content:center; gap:6px; border-radius:9px; min-height:36px; padding:7px 11px; font-size:13px; font-weight:500; }.ds-primary { @apply bg-blue-600 text-white hover:bg-blue-700 dark:bg-blue-600 dark:text-white dark:hover:bg-blue-500; }.ds-check { @apply bg-blue-50 text-blue-700 hover:bg-blue-100 dark:bg-blue-950/60 dark:text-blue-300 dark:hover:bg-blue-900/60; }.ds-danger { @apply bg-red-600 text-white hover:bg-red-700 dark:bg-red-600 dark:text-white; }.ds-text-button { @apply text-blue-600 dark:text-blue-400; font-size:12px; padding:8px 0; }button:disabled { cursor:not-allowed; opacity:.45; }button:focus-visible,input:focus-visible,summary:focus-visible { outline:2px solid #3b82f6; outline-offset:3px; }
.ds-alert { @apply rounded-xl bg-amber-50 text-amber-800 dark:bg-amber-950/30 dark:text-amber-300; padding:12px; margin-bottom:12px; font-size:13px; overflow-wrap:anywhere; }.ds-notice { @apply text-blue-700 dark:text-blue-300; margin-bottom:12px; font-size:13px; }.ds-empty { @apply rounded-xl bg-white text-slate-500 dark:bg-slate-900 dark:text-slate-400; display:flex; align-items:center; gap:10px; padding:18px; font-size:13px; line-height:1.7; }.ds-empty>svg { flex-shrink:0; }
.ds-notes { @apply text-slate-500 dark:text-slate-400; font-size:12px; line-height:1.8; margin-top:12px; }.ds-notes summary { width:fit-content; min-height:32px; cursor:pointer; }.ds-notes p { margin-top:4px; max-width:900px; }
.ds-sr { position:absolute; width:1px; height:1px; overflow:hidden; clip-path:inset(50%); }.ds-spin { animation:ds-spin 1s linear infinite; }@keyframes ds-spin { to { transform:rotate(360deg) } }
@media(max-width:639px) { .ds-settings { padding:16px; }.ds-settings-grid { grid-template-columns:minmax(0,1fr); gap:20px; }.ds-download-controls { justify-content:space-between; gap:14px 10px; padding:14px 12px; }.ds-download-controls .ds-help { flex-basis:100%; }.ds-rate-input { width:118px; }.ds-button { min-height:44px; padding-inline:8px; white-space:nowrap; }.ds-save-actions { gap:10px; }.ds-icon { width:40px; height:40px; }.ds-meta button { min-height:40px; padding-inline:8px; }.ds-text-button,.ds-preview summary,.ds-notes summary { min-height:44px; }.ds-outlined>input { height:44px; font-size:16px; } }
@media(max-width:639px) {
  .ds-rate-input,.ds-image-mode { width:100%; }.ds-event-setting { align-items:flex-start; gap:16px; flex-direction:column; }.ds-event-setting>.ds-select-field { width:100%; flex:none; }
  .ds-editor-row.ds-name-monitor-row { flex-direction:column; align-items:stretch; gap:8px; }
  .ds-monitor-field { width:100%; }
  .ds-monitor-box { height:44px; min-height:44px; padding:0 12px; }
  .ds-editor-row.ds-paths-row { grid-template-columns:1fr; gap:8px; }
  .ds-directory-picker { height:44px; min-height:44px; }
  .ds-input { height:44px; min-height:44px; font-size:16px; }
  .ds-picker-btn { height:44px; min-height:44px; padding:0 14px; font-size:13px; }
  .ds-schedule-grid { grid-template-columns:1fr; gap:8px; }
  .ds-schedule-card { padding:10px 12px; }
  .ds-schedule-card-header { flex-wrap:wrap; gap:8px; }
  .ds-schedule-toggle :deep(label) { min-height:44px; display:flex; align-items:center; }
  .ds-editor-footer-note { display:none; }
}
@media(max-width:359px) {
  .ds-actions { grid-template-columns:1fr; }.ds-settings>.ds-section-heading { flex-wrap:wrap; }.ds-save-actions { margin-left:auto; }
  .ds-schedule-card-header { flex-direction:column; align-items:stretch; gap:6px; }
  .ds-schedule-interval { width:100%; }
}
@media(prefers-reduced-motion:reduce) { .ds-spin { animation:none; }.ds-chevron,.ds-outlined,.ds-card { transition:none; } }
</style>
