<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { IconPlus, IconCloud, IconServer, IconFolder, IconFolderCheck, IconFolderExclamation, IconClock, IconAdjustmentsHorizontal, IconPlayerPause, IconPlayerPlay, IconRefresh, IconEdit, IconTrash, IconSearch, IconArrowRight, IconChevronRight, IconX } from '@tabler/icons-vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import FloatingDialog from '../components/AnimeFloatingDialog.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import SelectMenu from '../components/SelectMenu.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import PaginationControl from '../components/PaginationControl.vue'
import { mediaOrganizeRequest, type MediaRule, type MediaRun, type MediaGroup, type MediaState, type MediaHistory, type MediaHistoryItem, type MediaEntry } from '../lib/media-organize-api'
import { p115Request } from '../lib/p115-api'

const props = defineProps<{ token: string }>()
defineEmits<{ logout: [] }>()
const rules = ref<MediaRule[]>([]), active = ref(false), editor = ref<MediaRule>(), current = ref<MediaRun>()
const history = ref<MediaHistory>({ items: [], page: 1, pages: 1, total: 0 })
const busy = ref(''), error = ref(''), dialogError = ref(''), notice = ref(''), loading = ref(true), dirty = ref(false)
const directory = ref<'source' | 'target' | 'failure'>(), query = ref(''), searchGroup = ref(''), candidates = ref<{ id: number; kind: string; title: string; date: string }[]>([])
const deleting = ref<{ id: string; name: string }>()
const lifecycle = new AbortController()
let timer: number | undefined, generation = 0, stateGeneration = 0
const refreshing = ref(false)
const running = (status: string) => ['queued', 'scanning', 'running'].includes(status)
const labels: Record<string, string> = { ready: '可整理', needs_match: '待匹配', invalid: '待修正', conflict: '冲突', error: '失败', done: '已完成', unmatched: '未关联', retained: '已保留' }
const readyCount = computed(() => current.value?.readyCount || 0)
const failureCount = computed(() => current.value?.failureCount || 0)
const groupedEntries = computed(() => {
  const groups = new Map<string, NonNullable<MediaRun['entries']>>()
  for (const entry of current.value?.entries || []) { if (!groups.has(entry.group)) groups.set(entry.group, []); groups.get(entry.group)!.push(entry) }
  return groups
})
const storageOptions = [{ value: 'local', label: 'NAS / 本地挂载' }, { value: '115', label: '115 主账号' }]
async function request<T>(path = '', init: RequestInit = {}) { return mediaOrganizeRequest<T>(props.token, path, { ...init, signal: lifecycle.signal }) }
async function refresh(page = history.value.page) {
  if (refreshing.value || document.hidden || lifecycle.signal.aborted) return
  refreshing.value = true
  const version = stateGeneration
  try {
    const state = await request<MediaState>(`?page=${page}`)
    if (version !== stateGeneration) return
    rules.value = state.rules; active.value = state.active; history.value = state.history
    if (current.value && !dirty.value && running(current.value.status)) {
      const id = current.value.id, version = generation
      const run = await request<MediaRun>(`/runs/${id}`)
      if (current.value?.id === id && version === generation && !dirty.value) {
        if (!running(run.status) && !run.entries?.length && run.status === 'completed') { closeRun(); notice.value = run.message || '没有可处理文件' }
        else current.value = run
      }
    }
    error.value = ''
  } catch (reason) { if (version === stateGeneration && !lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取失败' }
  finally {
    loading.value = false; refreshing.value = false
    if (version !== stateGeneration) await refresh(page)
  }
}
async function act(name: string, fn: () => Promise<void>, dialog = false) {
  if (busy.value) return
  busy.value = name; (dialog ? dialogError : error).value = ''; notice.value = ''
  try { await fn() } catch (reason) { if (!lifecycle.signal.aborted) (dialog ? dialogError : error).value = reason instanceof Error ? reason.message : '操作失败' }
  finally { busy.value = ''; poll() }
}
function edit(rule?: MediaRule) {
  editor.value = rule ? { ...rule } : { id: '', name: '', storage: 'local', source: '', target: '', classify: true, enabled: false, intervalMinutes: 5 }
  dialogError.value = ''
}
function storageChanged() { if (editor.value) Object.assign(editor.value, { source: '', target: '', failure: '', sourceId: '', targetId: '', failureId: '', accountId: '' }) }
function clearFailure() { if (editor.value) { editor.value.failure = ''; editor.value.failureId = '' } }
async function chooseDirectory(which: 'source' | 'target' | 'failure') {
  await act('directory', async () => {
    const data = await p115Request<{ accounts: { id: string; primary: boolean }[] }>(props.token, 'accounts', { signal: lifecycle.signal })
    const primary = data.accounts.find(a => a.primary)
    if (!primary) throw new Error('请先在 115 助手配置主账号')
    if (editor.value) {
      if (editor.value.accountId && editor.value.accountId !== primary.id) storageChanged()
      editor.value.accountId = primary.id
    }
    directory.value = which
  }, true)
}
function picked(value: { id: string; path: string }) { if (editor.value && directory.value) { editor.value[directory.value] = value.path; editor.value[directory.value === 'source' ? 'sourceId' : directory.value === 'target' ? 'targetId' : 'failureId'] = value.id }; directory.value = undefined }
async function save() { await act('save', async () => { await request('/rules', { method: 'POST', body: JSON.stringify(editor.value) }); stateGeneration++; editor.value = undefined; notice.value = '整理配置已保存'; await refresh() }, true) }
async function toggleAutomatic(rule: MediaRule) {
  await act(`toggle-${rule.id}`, async () => {
    const saved = await request<MediaRule>('/rules', { method: 'POST', body: JSON.stringify({ ...rule, enabled: !rule.enabled }) })
    stateGeneration++
    rules.value = rules.value.map(item => item.id === saved.id ? saved : item)
    notice.value = saved.enabled ? `已启用${saved.name}的目录监控` : `已暂停${saved.name}的目录监控${active.value ? '，正在执行的任务会继续完成' : ''}`
  })
}
async function scan(rule: MediaRule) { await act(rule.id, async () => { const run = await request<MediaRun>(`/rules/${rule.id}/scan`, { method: 'POST' }); current.value = run; generation++; dirty.value = false; dialogError.value = ''; await refresh() }) }
async function openRun(id: string) { await act(id, async () => { current.value = await request<MediaRun>(`/runs/${id}`); generation++; dirty.value = false; dialogError.value = ''; searchGroup.value = ''; candidates.value = [] }) }
function closeRun() { current.value = undefined; generation++; searchGroup.value = ''; dirty.value = false; candidates.value = [] }
async function preview() {
  await act('preview', async () => {
    const run = current.value; if (!run) return
    current.value = await request<MediaRun>(`/runs/${run.id}/preview`, { method: 'POST', body: JSON.stringify({ groups: run.groups?.filter(g => g.tmdbId).map(g => ({ id: g.id, kind: g.kind, tmdbId: g.tmdbId })), files: run.entries?.filter(e => e.file.mediaType === 'video' && e.status !== 'done').map(e => ({ id: e.file.id, season: e.file.season, episode: e.file.episode })) }) })
    dirty.value = false; await refresh()
  }, true)
}
async function apply() { await act('apply', async () => { if (current.value) current.value = await request<MediaRun>(`/runs/${current.value.id}/apply`, { method: 'POST' }); await refresh() }, true) }
function match(group: MediaGroup) { searchGroup.value = group.id; query.value = group.identity.name; candidates.value = []; dialogError.value = '' }
async function search() {
  const version = generation, group = searchGroup.value
  await act('search', async () => {
    const response = await fetch(`/api/admin/tmdb/browse?kind=multi&query=${encodeURIComponent(query.value.trim())}&page=1`, { headers: { Authorization: `Bearer ${props.token}` }, signal: AbortSignal.any([lifecycle.signal, AbortSignal.timeout(25000)]) })
    const data = await response.json(); if (!response.ok) throw new Error(data.error || '搜索失败'); if (version === generation && group === searchGroup.value) candidates.value = (data.items || []).filter((item: { kind: string }) => ['movie', 'tv'].includes(item.kind))
  }, true)
}
function pick(candidate: { id: number; kind: string; title: string }) { const group = current.value?.groups?.find(g => g.id === searchGroup.value); if (group) { group.tmdbId = candidate.id; group.kind = candidate.kind; group.title = candidate.title; dirty.value = true }; searchGroup.value = ''; candidates.value = [] }
async function remove() { await act('delete', async () => { if (!deleting.value) return; await request(`/rules/${deleting.value.id}`, { method: 'DELETE' }); stateGeneration++; deleting.value = undefined; await refresh() }, true) }
function episodeLabel(item: MediaHistoryItem) { return item.kind === 'tv' && item.mediaType === 'video' ? `S${String(item.season).padStart(2, '0')}E${String(item.episode).padStart(2, '0')}` : '' }
function sourcePath(entry: MediaEntry) { return entry.failure?.state === 'stored' ? `${current.value?.rule.failure}/${entry.file.sourcePath}` : entry.file.sourcePath }
function failureNotice(entry: MediaEntry) {
  if (entry.failure?.state === 'stored') return '已移入失败目录；在此修正匹配并更新预览后，可直接继续整理。'
  if (!entry.failure?.message) return ''
  return entry.failure.state === 'moving' ? `移入结果尚未确认，继续整理将核对文件位置：${entry.failure.message}` : `未能移入失败目录，文件保留原处：${entry.failure.message}`
}
function poll() { window.clearTimeout(timer); if (lifecycle.signal.aborted || document.hidden) return; const processing = active.value || current.value && running(current.value.status); if (!processing && !rules.value.some(r => r.enabled)) return; timer = window.setTimeout(async () => { await refresh(); poll() }, processing ? 2500 : 60000) }
async function visible() { if (!document.hidden) await refresh(); poll() }
onMounted(async () => { document.addEventListener('visibilitychange', visible); await refresh(); poll() })
onBeforeUnmount(() => { lifecycle.abort(); window.clearTimeout(timer); document.removeEventListener('visibilitychange', visible) })
</script>

<template>
  <ManagementLayout title="自动整理" description="识别作品、分类并使用公共命名模板整理。支持 NAS 挂载目录、STRM、视频和 115 主账号。" @logout="$emit('logout')">
    <template #actions><button class="mo-button mo-primary" @click="edit()"><IconPlus :size="17" />新增规则</button></template>
    <div class="mo-page">
      <p v-if="error" role="alert" class="mo-error">{{ error }}<button @click="refresh()">重新加载</button></p>
      <p v-if="notice" role="status" class="mo-muted">{{ notice }}</p>
      <section class="mo-rules" aria-label="整理规则">
        <header class="mo-section"><h2>整理规则</h2><RouterLink to="/settings/naming">命名模板<IconArrowRight :size="15" /></RouterLink></header>
        <p v-if="loading" class="mo-empty">正在读取配置…</p>
        <div v-else-if="!rules.length" class="mo-empty"><IconFolder :size="30" /><p>添加一个来源目录，先扫描预览，再启用监控。</p><button class="mo-button" @click="edit()">添加整理规则</button></div>
        <div v-else class="mo-rule-grid">
          <article v-for="rule in rules" :key="rule.id" class="mo-rule-card ambient-glow" :aria-label="`${rule.name}整理规则`">
            <header class="mo-rule-heading">
              <span class="mo-rule-symbol" aria-hidden="true"><component :is="rule.storage === '115' ? IconCloud : IconServer" :size="21" /></span>
              <div class="mo-rule-name"><h3 :title="rule.name">{{ rule.name }}</h3><span>{{ rule.storage === 'local' ? '本地挂载' : '115 主账号' }}</span></div>
              <div class="mo-rule-tools"><button class="mo-icon" :disabled="!!busy" :aria-label="`编辑${rule.name}`" @click="edit(rule)"><IconEdit :size="17" /></button><button class="mo-icon mo-rule-delete" :disabled="!!busy" :aria-label="`删除${rule.name}`" @click="deleting = { id: rule.id, name: rule.name }"><IconTrash :size="17" /></button></div>
            </header>
            <div class="mo-rule-schedule" :class="{ 'is-enabled': rule.enabled }"><component :is="rule.enabled ? IconClock : IconPlayerPause" :size="14" aria-hidden="true" /><span>{{ rule.enabled ? `定时监控 · 每 ${rule.intervalMinutes} 分钟` : '监控已暂停' }}</span></div>
            <dl class="mo-rule-details">
              <div class="mo-rule-path"><dt><IconFolder class="mo-rule-source-icon" :size="16" aria-hidden="true" />来源</dt><dd><code :title="rule.source">{{ rule.source }}</code></dd></div>
              <div class="mo-rule-path"><dt><IconFolderCheck class="mo-rule-target-icon" :size="16" aria-hidden="true" />目标</dt><dd><code :title="rule.target">{{ rule.target }}</code></dd></div>
              <div class="mo-rule-path"><dt><IconFolderExclamation class="mo-rule-failure-icon" :size="16" aria-hidden="true" />失败目录</dt><dd><code v-if="rule.failure" :title="rule.failure">{{ rule.failure }}</code><span v-else class="mo-rule-unset">未设置，识别失败的视频留在来源</span></dd></div>
              <div><dt><IconAdjustmentsHorizontal class="mo-rule-policy-icon" :size="16" aria-hidden="true" />整理策略</dt><dd>{{ rule.classify ? '按类型分类' : '使用目标目录' }}<span class="mo-rule-policy">不覆盖同名文件</span></dd></div>
              <div><dt><IconTrash class="mo-rule-cleanup-icon" :size="16" aria-hidden="true" />自动清理</dt><dd>无关文件、未归属字幕及空目录</dd></div>
            </dl>
            <footer class="mo-rule-actions">
              <button class="mo-button mo-rule-toggle" :class="{ 'is-start': !rule.enabled }" :disabled="!!busy" :aria-label="`${rule.enabled ? '暂停' : '启用'}${rule.name}的目录监控`" @click="toggleAutomatic(rule)"><component :is="rule.enabled ? IconPlayerPause : IconPlayerPlay" :size="16" aria-hidden="true" />{{ busy === `toggle-${rule.id}` ? '保存中…' : rule.enabled ? '暂停监控' : '启用监控' }}</button>
              <button class="mo-button mo-rule-scan" :disabled="!!busy" @click="scan(rule)"><IconSearch :size="16" aria-hidden="true" />扫描预览</button>
            </footer>
          </article>
        </div>
      </section>
      <section v-if="history.total" class="mo-results" aria-label="整理记录">
        <header class="mo-section"><div class="mo-title"><h2>整理记录</h2><span>{{ history.total }} 项记录</span></div><button class="mo-icon" aria-label="刷新记录" :disabled="refreshing" @click="refresh()"><IconRefresh :size="17" /></button></header>
        <div class="mo-history-grid">
          <button v-for="item in history.items" :key="`${item.runId}:${item.fileId}`" class="mo-result" :disabled="!!busy" :aria-label="`查看${item.name}的整理记录`" @click="openRun(item.runId)">
            <span class="mo-result-heading"><strong><span class="mo-result-name" :title="item.title || item.name">{{ item.title || item.name }}</span><span v-if="episodeLabel(item)" class="mo-episode">{{ episodeLabel(item) }}</span></strong><IconChevronRight :size="16" aria-hidden="true" /></span>
            <span class="mo-result-meta"><span class="mo-chip mo-status-chip" :class="item.status === 'done' ? 'mo-success' : 'mo-failure'">{{ item.status === 'done' ? item.mediaType === 'cleanup' ? '已删除' : '成功' : '失败' }}</span><span class="mo-chip mo-kind">{{ item.directory ? '空文件夹' : item.mediaType === 'cleanup' ? '无关文件' : item.mediaType === 'attachment' ? '媒体附件' : item.mediaType === 'subtitle' ? '字幕' : item.kind === 'movie' ? '电影' : '剧集' }}</span><span v-if="!item.directory" class="mo-chip mo-format">{{ item.name.split('.').pop()?.toUpperCase() }}</span></span>
            <span class="mo-result-path"><span>原名</span><code :title="item.source">{{ item.name }}</code></span>
            <span v-if="item.target" class="mo-result-path"><span>目标</span><code :title="item.target">{{ item.target.split('/').pop() }}</code></span>
            <span v-if="item.message" class="mo-result-message" :class="item.status === 'done' ? 'mo-muted' : 'mo-red'" :title="item.message">{{ item.message }}</span>
            <time :datetime="item.at">{{ new Date(item.at).toLocaleString() }}</time>
          </button>
        </div>
        <PaginationControl v-if="history.pages > 1" :page="history.page" :pages="history.pages" :disabled="refreshing || !!busy" label="整理记录分页" @change="refresh" />
      </section>
    </div>
    <FloatingDialog v-if="editor" title="整理规则" close-label="关闭整理规则" :close-disabled="!!busy" @close="editor = undefined">
      <form id="media-rule-form" class="mo-form" @submit.prevent="save">
        <p v-if="dialogError" role="alert" class="mo-error">{{ dialogError }}</p>
        <label>规则名称<input v-model="editor.name" required maxlength="80" placeholder="例如：NAS 下载目录" /></label>
        <label>存储位置<SelectMenu v-model="editor.storage" :options="storageOptions" label="存储位置" @change="storageChanged" /></label>
        <label>来源目录<div class="mo-input-action"><input v-model="editor.source" required :readonly="editor.storage === '115'" :placeholder="editor.storage === 'local' ? '/media/inbox' : '选择 115 来源目录'" /><button v-if="editor.storage === '115'" type="button" class="mo-icon" aria-label="选择来源目录" @click="chooseDirectory('source')"><IconFolder :size="19" /></button></div></label>
        <label>目标目录<div class="mo-input-action"><input v-model="editor.target" required :readonly="editor.storage === '115'" :placeholder="editor.storage === 'local' ? '/media/library' : '选择 115 目标目录'" /><button v-if="editor.storage === '115'" type="button" class="mo-icon" aria-label="选择目标目录" @click="chooseDirectory('target')"><IconFolder :size="19" /></button></div></label>
        <label>整理失败目录（可选）<div class="mo-input-action"><input v-model="editor.failure" aria-label="整理失败目录" :readonly="editor.storage === '115'" placeholder="留空则保留在来源目录" /><button v-if="editor.failure" type="button" class="mo-icon" aria-label="清空整理失败目录" @click="clearFailure"><IconX :size="17" /></button><button v-if="editor.storage === '115'" type="button" class="mo-icon" aria-label="选择整理失败目录" @click="chooseDirectory('failure')"><IconFolder :size="19" /></button></div><span class="mo-muted">执行时将无法匹配、季集不明或冲突文件移入，保留原名与目录层级；网络故障保留原处。</span></label>
        <p class="mo-muted">{{ editor.storage === 'local' ? '填写容器内已挂载且可写的目录。来源与目标不能重叠。新文件稳定 30 秒后处理。' : '使用 115 主账号，来源与目标不能重叠。更换主账号后需重新选择目录。' }}</p>
        <SettingSwitch v-model="editor.classify" label="按作品信息分类" description="电影按动画、华语、其他分类；剧集按动漫、纪录、综艺和地区分类。" />
        <SettingSwitch v-model="editor.enabled" label="启用监控" description="按设置间隔扫描并整理媒体及附件，删除无关文件（含压缩包）、未归属字幕和空文件夹；手动整理不执行清理。" />
        <label v-if="editor.enabled">扫描间隔（分钟）<input v-model.number="editor.intervalMinutes" type="number" min="1" max="1440" required /></label>
        <p class="mo-muted">STRM 正文保持原样。已有同名文件不会覆盖，命名沿用全局电影和剧集模板。</p>
      </form>
      <template #footer><div class="mo-footer"><button class="mo-button" :disabled="!!busy" @click="editor = undefined">取消</button><button form="media-rule-form" type="submit" class="mo-button mo-primary" :disabled="!!busy">{{ busy === 'save' ? '保存中…' : '保存规则' }}</button></div></template>
    </FloatingDialog>
    <P115DirectoryDialog v-if="directory && editor?.accountId" :token="token" :account-id="editor.accountId" :title="directory === 'source' ? '选择来源目录' : directory === 'target' ? '选择目标目录' : '选择整理失败目录'" :allow-root="false" @close="directory = undefined" @select="picked" />
    <FloatingDialog v-if="current" class="mo-preview-dialog" title="整理预览与记录" close-label="关闭整理预览" size="organize" :close-disabled="!!busy" @close="closeRun">
      <template #header><div class="mo-title"><h3>{{ current.rule.name }}</h3><span>{{ labels[current.status] }}</span></div><p class="mo-muted">{{ current.message }}</p><p v-if="dialogError" role="alert" class="mo-error">{{ dialogError }}</p></template>
      <div class="mo-preview"><p v-if="running(current.status)" class="mo-empty" role="status">{{ labels[current.status] }}，关闭弹窗后任务继续。</p>
        <section v-for="group in current.groups" :key="group.id" class="mo-work"><header><div><h3>{{ group.title || group.identity.name || '未识别作品' }}</h3><p class="mo-muted">{{ group.tmdbId ? `${group.kind === 'movie' ? '电影' : '剧集'} · TMDB ${group.tmdbId}` : group.message }}<span v-if="group.category" class="mo-meta">{{ group.category }}</span></p></div><button class="mo-button" :disabled="!!busy || running(current.status) || groupedEntries.get(group.id)?.some(e => e.status === 'done')" @click="match(group)">{{ group.tmdbId ? '更换作品' : '匹配作品' }}</button></header>
          <form v-if="searchGroup === group.id" class="mo-search" @submit.prevent="search"><div class="mo-input-action"><input v-model="query" aria-label="搜索 TMDB 作品" required placeholder="片名" /><button class="mo-icon" type="submit" :disabled="!!busy" aria-label="搜索作品"><IconSearch :size="18" /></button></div><div class="mo-candidates"><button v-for="candidate in candidates" :key="`${candidate.kind}:${candidate.id}`" type="button" @click="pick(candidate)"><strong>{{ candidate.title }}</strong><span>{{ candidate.kind === 'movie' ? '电影' : '剧集' }} · {{ candidate.date?.slice(0, 4) }} · {{ candidate.id }}</span></button><p v-if="!busy && candidates.length === 0" class="mo-muted">输入片名并搜索，选择正确作品。</p></div></form>
          <article v-for="entry in groupedEntries.get(group.id)" :key="entry.file.id" class="mo-file"><div class="mo-file-path"><code>{{ sourcePath(entry) }}</code><code v-if="entry.relative" class="mo-target">{{ entry.relative }}</code><p v-if="entry.message" :class="entry.status === 'error' || entry.status === 'conflict' ? 'mo-red' : 'mo-muted'">{{ entry.message }}</p><p v-if="failureNotice(entry)" class="mo-muted">{{ failureNotice(entry) }}</p></div><div class="mo-file-controls"><template v-if="group.kind === 'tv' && entry.file.mediaType === 'video'"><label>季<input v-model.number="entry.file.season" type="number" min="0" max="1000" :disabled="!!busy || entry.status === 'done' || running(current.status)" @input="dirty = true" /></label><label>集<input v-model.number="entry.file.episode" type="number" min="1" max="10000" :disabled="!!busy || entry.status === 'done' || running(current.status)" @input="dirty = true" /></label></template><span class="mo-status" :class="{ 'mo-red': ['conflict', 'error'].includes(entry.status), 'mo-blue': entry.status === 'ready' }">{{ labels[entry.status] }}</span></div></article>
        </section><div v-for="entry in groupedEntries.get('')" :key="entry.file.id" class="mo-file"><div class="mo-file-path"><code>{{ sourcePath(entry) }}</code><p class="mo-muted">{{ entry.message }}</p><p v-if="failureNotice(entry)" class="mo-muted">{{ failureNotice(entry) }}</p></div></div><p v-if="!running(current.status) && !current.entries?.length" class="mo-empty">没有可处理文件。</p>
      </div>
      <template #footer><div class="mo-footer"><span class="mo-muted">{{ dirty ? '修改后请更新预览' : `${readyCount} 项可整理${failureCount ? `，${failureCount} 项失败后自动移入失败目录` : ''}` }}</span><button v-if="!running(current.status)" class="mo-button" :disabled="!!busy" @click="preview">更新预览</button><button v-if="!running(current.status)" class="mo-button" :class="{ 'mo-primary': readyCount > 0 }" :disabled="!!busy || dirty || !(readyCount || current.status !== 'preview' && failureCount)" @click="apply">{{ current.status === 'preview' ? '确认整理并移动' : '继续整理' }}</button></div></template>
    </FloatingDialog>
    <FloatingDialog v-if="deleting" title="移除记录" close-label="取消移除" :close-disabled="!!busy" @close="deleting = undefined"><p class="py-4 text-sm">确认移除{{ deleting.name }}？文件不会被删除。</p><p v-if="dialogError" role="alert" class="mo-error">{{ dialogError }}</p><template #footer><div class="mo-footer"><button class="mo-button" @click="deleting = undefined">取消</button><button class="mo-button mo-primary" :disabled="!!busy" @click="remove">确认移除</button></div></template></FloatingDialog>
  </ManagementLayout>
</template>

<style scoped>
.mo-rules{min-width:0}.mo-rule-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(min(100%,320px),1fr));gap:16px}
.mo-rule-card{display:flex;min-width:0;flex-direction:column;padding:18px;border:1px solid rgb(148 163 184/.16);border-radius:16px;background:#fff;box-shadow:0 2px 8px rgb(15 23 42/.035);transition:box-shadow .2s;--ambient-glow-strength:.55}
.mo-rule-symbol{display:flex;align-items:center;justify-content:center;flex:none;width:38px;height:38px;border-radius:11px;background:#dbeafe;color:#1d4ed8}
.mo-rule-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:10px}.mo-rule-name{min-width:0;flex:1}.mo-rule-name h3{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:16px;font-weight:600;line-height:24px;color:#0f172a}.mo-rule-name>span{display:block;margin-top:2px;font-size:11px;line-height:18px;color:#64748b}
.mo-rule-tools{display:flex;flex:none;gap:2px;margin:-5px -5px 0 0}.mo-rule-tools .mo-icon{width:32px;min-height:32px;color:#64748b}
.mo-rule-schedule{display:inline-flex;align-items:center;align-self:flex-start;gap:5px;margin-top:10px;padding:3px 8px;border-radius:6px;background:#f1f5f9;color:#475569;font-size:12px;line-height:18px}.mo-rule-schedule.is-enabled{background:#2563eb;color:#fff}
.mo-rule-details{display:grid;gap:9px;margin-top:18px}.mo-rule-details>div{display:grid;grid-template-columns:80px minmax(0,1fr);gap:8px;align-items:baseline}.mo-rule-details dt{display:flex;align-items:center;gap:6px;white-space:nowrap;font-size:12px;line-height:21px;color:#64748b}.mo-rule-details dt svg{flex:none;color:#6688b5}.mo-rule-details dd{min-width:0;font-size:13px;line-height:21px;color:#334155}.mo-rule-path{min-height:42px}.mo-rule-path code{display:-webkit-box;overflow:hidden;-webkit-box-orient:vertical;-webkit-line-clamp:2;overflow-wrap:anywhere;font-size:13px;line-height:21px;color:#475569}.mo-rule-policy{display:inline-block;margin-left:8px;color:#64748b;font-size:12px}.mo-rule-unset{font-size:12px;color:#64748b}
.mo-rule-details .mo-rule-source-icon{color:#2563eb}.mo-rule-details .mo-rule-target-icon{color:#7c3aed}.mo-rule-details .mo-rule-failure-icon{color:#b45309}.mo-rule-details .mo-rule-policy-icon{color:#4f46e5}.mo-rule-details .mo-rule-cleanup-icon{color:#64748b}
:global(.dark .mo-rule-details .mo-rule-source-icon){color:#60a5fa}:global(.dark .mo-rule-details .mo-rule-target-icon){color:#c4b5fd}:global(.dark .mo-rule-details .mo-rule-failure-icon){color:#fbbf24}:global(.dark .mo-rule-details .mo-rule-policy-icon){color:#a5b4fc}:global(.dark .mo-rule-details .mo-rule-cleanup-icon){color:#94a3b8}
.mo-rule-actions{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px;margin-top:auto;padding-top:18px}.mo-rule-actions .mo-button{min-height:40px;font-size:13px}.mo-rule-actions .mo-rule-toggle{background:#f1f5f9;color:#334155}.mo-rule-actions .mo-rule-toggle:hover{background:#e2e8f0}.mo-rule-actions .mo-rule-toggle.is-start{background:#2563eb;color:#fff}.mo-rule-actions .mo-rule-toggle.is-start:hover{background:#1d4ed8}.mo-rule-actions .mo-rule-scan{background:#dbeafe;color:#1d4ed8}.mo-rule-actions .mo-rule-scan:hover{background:#bfdbfe}
:global(.dark .mo-rule-card){background:#1e293b;border-color:rgb(148 163 184/.12);box-shadow:0 2px 8px rgb(0 0 0/.12)}:global(.dark .mo-rule-symbol){background:#1e3a5f;color:#93c5fd}:global(.dark .mo-rule-name h3){color:#f1f5f9}:global(.dark .mo-rule-name>span),:global(.dark .mo-rule-tools .mo-icon),:global(.dark .mo-rule-details dt),:global(.dark .mo-rule-policy),:global(.dark .mo-rule-unset){color:#94a3b8}:global(.dark .mo-rule-details dd),:global(.dark .mo-rule-path code){color:#cbd5e1}:global(.dark .mo-rule-details dt svg){color:#86a5ce}:global(.dark .mo-rule-schedule){background:#334155;color:#cbd5e1}:global(.dark .mo-rule-schedule.is-enabled){background:#1d4ed8;color:#fff}:global(.dark .mo-rule-actions .mo-rule-toggle){background:#334155;color:#e2e8f0}:global(.dark .mo-rule-actions .mo-rule-toggle:hover){background:#475569}:global(.dark .mo-rule-actions .mo-rule-toggle.is-start){background:#3769b5;color:#fff}:global(.dark .mo-rule-actions .mo-rule-toggle.is-start:hover){background:#4479ca}:global(.dark .mo-rule-actions .mo-rule-scan){background:#1e3a5f;color:#bfdbfe}:global(.dark .mo-rule-actions .mo-rule-scan:hover){background:#254972}
@media(hover:hover) and (pointer:fine){.mo-rule-card:hover{box-shadow:0 8px 24px -10px rgb(37 99 235/.2)}.mo-rule-tools .mo-rule-delete:hover{color:#dc2626}:global(.dark .mo-rule-card:hover){box-shadow:0 8px 24px -10px rgb(0 0 0/.35)}:global(.dark .mo-rule-tools .mo-rule-delete:hover){color:#fca5a5}}
@media(max-width:639px){.mo-rule-grid{gap:12px}.mo-rule-card{padding:16px}.mo-rule-tools .mo-icon{width:40px;min-height:40px}.mo-rule-actions .mo-button{min-height:44px}}
@media(prefers-reduced-motion:reduce){.mo-rule-card{transition:none}}
.mo-results{min-width:0}.mo-history-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,340px),1fr));gap:12px}
.mo-result{display:flex;flex-direction:column;min-width:0;gap:7px;padding:14px;text-align:left;border-radius:14px;background:#fff;box-shadow:0 2px 8px rgb(15 23 42/.035);transition:background-color .16s,box-shadow .16s}.mo-result:hover{background:#eff6ff;box-shadow:0 4px 16px rgb(37 99 235/.1)}
.mo-result-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;font-size:13px;line-height:1.6}.mo-result-heading strong{display:flex;align-items:center;flex:1;min-width:0;gap:8px;font-weight:600}.mo-result-heading svg{flex:none;color:rgb(var(--color-slate-400))}.mo-result-name,.mo-result-path code,.mo-result-message{display:block;min-width:0;overflow:hidden;white-space:nowrap;text-overflow:ellipsis}.mo-episode{flex:none;border-radius:5px;padding:2px 6px;background:#2563eb;color:#fff;font-size:11px;font-weight:500;line-height:1.5;font-variant-numeric:tabular-nums}
.mo-result-meta{display:flex;align-items:center;gap:6px;font-size:11px}.mo-chip{display:inline-flex;align-items:center;gap:4px;border-radius:5px;padding:2px 7px;line-height:1.5;font-weight:500}.mo-status-chip{font-size:12px;font-weight:600}.mo-success{color:#fff;background:#15803d}.mo-failure{color:#fff;background:#b91c1c}.mo-kind{color:#1d4ed8;background:#bfdbfe}.mo-format{color:#475569;background:#e2e8f0}
.mo-result-path{display:grid;grid-template-columns:36px minmax(0,1fr);gap:8px;align-items:baseline;font-size:11px;line-height:1.7;color:rgb(var(--color-slate-500))}.mo-result-path code{color:rgb(var(--color-slate-600))}.mo-result-message{font-size:12px;line-height:1.7}.mo-result time{margin-top:auto;padding-top:2px;font-size:11px;color:rgb(var(--color-slate-500))}
:global(.dark .mo-result){background:#1e293b}:global(.dark .mo-result:hover){background:#24334d}:global(.dark .mo-success){color:#dcfce7;background:#166534}:global(.dark .mo-failure){color:#fee2e2;background:#991b1b}:global(.dark .mo-kind){color:#bfdbfe;background:#1e40af}:global(.dark .mo-episode){color:#fff;background:#2563eb}:global(.dark .mo-format){color:#cbd5e1;background:rgb(100 116 139/.25)}:global(.dark .mo-result-path code){color:#cbd5e1}
@media(max-width:639px){.mo-history-grid{gap:10px}.mo-result{padding:14px 12px}.mo-result-path{gap:6px}}
.mo-page{display:grid;gap:22px}.mo-section{display:flex;align-items:center;justify-content:space-between;min-height:54px;gap:12px}.mo-section h2,.mo-title h3{font-size:14px;font-weight:600}.mo-work h3{font-size:16px;font-weight:600}.mo-section a{display:flex;align-items:center;gap:6px;font-size:12px;color:#2563eb}.mo-title{display:flex;align-items:center;gap:10px;flex-wrap:wrap}.mo-title>span{font-size:11px;color:rgb(var(--color-slate-500));border-radius:6px;padding:2px 6px;background:rgb(148 163 184/.1)}.mo-muted{font-size:12px;color:rgb(var(--color-slate-500));line-height:1.7}.mo-meta{margin-left:12px}.mo-footer{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.mo-button,.mo-icon{display:inline-flex;justify-content:center;align-items:center;gap:6px;min-height:36px;border-radius:9px;background:rgb(148 163 184/.1);font-size:12px;padding:6px 12px;white-space:nowrap}.mo-icon{width:36px;padding:0;background:transparent}.mo-button:hover,.mo-icon:hover{background:rgb(148 163 184/.18)}.mo-primary{background:#2563eb;color:white}.mo-primary:hover{background:#1d4ed8}button:disabled{opacity:.45;cursor:default}.mo-empty{display:flex;flex-direction:column;align-items:center;gap:15px;text-align:center;font-size:13px;color:rgb(var(--color-slate-500));padding:32px 10px}.mo-form{display:grid;gap:18px;padding:8px 0}.mo-form label{display:grid;gap:7px;font-size:12px;font-weight:500}.mo-form input,.mo-search input,.mo-file-controls input{width:100%;min-width:0;min-height:36px;padding:7px 10px;border:1px solid rgb(148 163 184/.23);border-radius:9px;background:rgb(var(--color-slate-50));color:rgb(var(--color-slate-900));font-size:13px;outline:none}.mo-form input:focus,.mo-search input:focus,.mo-file-controls input:focus{border-color:#3b82f6}.mo-input-action{display:flex;gap:6px}.mo-input-action input{flex:1}.mo-footer{justify-content:flex-end}.mo-footer>.mo-muted{margin-right:auto}.mo-error{font-size:13px;line-height:1.6;color:#dc2626;padding:10px 0}.mo-error button{margin-left:12px;text-decoration:underline}.mo-preview{display:grid;gap:18px}.mo-work{padding-top:16px}.mo-work>header{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:10px}.mo-work+.mo-work{border-top:1px solid rgb(148 163 184/.18)}.mo-file{display:flex;align-items:center;gap:14px;padding:11px 0}.mo-file+.mo-file{border-top:1px solid rgb(148 163 184/.09)}.mo-file-path{flex:1;min-width:0;display:grid;gap:6px}.mo-file-path code{font-size:13px;overflow-wrap:anywhere;line-height:1.75;color:rgb(var(--color-slate-600))}.mo-file-path code.mo-target{color:#2563eb}.mo-file-controls{display:flex;align-items:center;gap:7px;flex-shrink:0}.mo-file-controls label{display:grid;gap:3px;font-size:12px;color:rgb(var(--color-slate-500))}.mo-file-controls input{width:72px;font-size:14px;min-height:36px;padding:6px 8px}.mo-status{width:56px;text-align:right;font-size:12px;color:rgb(var(--color-slate-500))}.mo-search{padding:10px 0}.mo-candidates{display:grid;gap:4px;margin-top:8px;max-height:220px;overflow:auto}.mo-candidates button{display:flex;justify-content:space-between;gap:10px;text-align:left;padding:10px;border-radius:8px;background:rgb(148 163 184/.08);font-size:12px}.mo-candidates span{font-size:11px;color:rgb(var(--color-slate-500))}.mo-blue{color:#2563eb!important}.mo-red{color:#dc2626!important}:global(.dark .mo-primary){background:#3769b5}:global(.dark .mo-blue),:global(.dark .mo-file-path code.mo-target),:global(.dark .mo-section a){color:#93c5fd!important}:global(.dark .mo-red),:global(.dark .mo-error){color:#fca5a5!important}button:focus-visible,a:focus-visible{outline:2px solid #3b82f6;outline-offset:2px}@media(max-width:639px){.mo-file{align-items:flex-start;flex-direction:column;gap:8px}.mo-file-controls{align-self:flex-end}.mo-button,.mo-icon{min-height:44px}.mo-footer{gap:8px}.mo-footer>.mo-muted{flex:1 0 100%}.mo-candidates button{flex-direction:column;gap:4px}}
:global(.mo-preview-dialog.anime-floating.anime-floating-organize){height:fit-content;min-height:min(360px,calc(100dvh - 48px))}
.mo-form :deep(input[role="switch"]:checked + span){background:#2563eb}
:global(.dark .mo-form input),:global(.dark .mo-search input),:global(.dark .mo-file-controls input){background:#1e293b;color:#e2e8f0;border-color:rgb(148 163 184/.3)}
:global(.dark .mo-muted),:global(.dark .mo-title>span),:global(.dark .mo-status),:global(.dark .mo-candidates span){color:#94a3b8}
:global(.dark .mo-file-path code),:global(.dark .mo-file-controls label){color:#cbd5e1}
:global(.mo-preview-dialog .anime-floating-body){flex:0 1 auto}
:global(.mo-preview-dialog .mo-title h3){font-size:16px}
:global(.mo-preview-dialog .mo-title>span){font-size:12px}
:global(.mo-preview-dialog .mo-muted),:global(.mo-preview-dialog .mo-button){font-size:13px}
.mo-file-path p{font-size:13px;line-height:1.6}
@media(max-width:639px){.mo-file-controls input{font-size:16px;min-height:44px;width:78px}}
:global(.dark .mo-page),:global(.dark .mo-form),:global(.dark .mo-preview){color:#e2e8f0}
</style>
