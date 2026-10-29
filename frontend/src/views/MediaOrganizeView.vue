<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { IconPlus, IconCloud, IconServer, IconFolder, IconFolderCheck, IconFolderExclamation, IconClock, IconAdjustmentsHorizontal, IconPlayerPause, IconPlayerPlay, IconEdit, IconTrash, IconX } from '@tabler/icons-vue'
import FloatingDialog from '../components/AnimeFloatingDialog.vue'
import SettingSwitch from '../components/SettingSwitch.vue'
import SelectMenu from '../components/SelectMenu.vue'
import P115DirectoryDialog from '../components/P115DirectoryDialog.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import { mediaOrganizeRequest, type MediaRule, type MediaState } from '../lib/media-organize-api'
import { p115Request } from '../lib/p115-api'

const props = defineProps<{ token: string }>()
const rules = ref<MediaRule[]>([]), active = ref(false), activeRules = ref<string[]>([]), editor = ref<MediaRule>()
const busy = ref(''), error = ref(''), dialogError = ref(''), notice = ref(''), loading = ref(true), refreshing = ref(false)
const directory = ref<'source' | 'target' | 'failure'>(), deleting = ref<{ id: string; name: string }>()
const leaveDialog = ref(false), editorSnapshot = ref('')
const unsaved = computed(() => !!editor.value && ruleSignature(editor.value) !== editorSnapshot.value)
let leaveDecision: ((value: boolean) => void) | undefined
const lifecycle = new AbortController()
let timer: number | undefined, stateGeneration = 0
const storageOptions = [{ value: 'local', label: 'NAS / 本地挂载' }, { value: '115', label: '115 主账号' }]
function ruleSignature(rule: MediaRule) { return JSON.stringify([rule.name, rule.storage, rule.source, rule.target, rule.failure || '', rule.sourceId || '', rule.targetId || '', rule.failureId || '', rule.accountId || '', rule.classify, rule.enabled, rule.intervalMinutes]) }
async function request<T>(path = '', init: RequestInit = {}) { return mediaOrganizeRequest<T>(props.token, path, { ...init, signal: lifecycle.signal }) }
async function refresh() {
  if (refreshing.value || document.hidden || lifecycle.signal.aborted) return
  refreshing.value = true
  const version = stateGeneration
  try {
    const state = await request<MediaState>()
    if (version !== stateGeneration) return
    rules.value = state.rules; active.value = state.active; activeRules.value = state.activeRules; error.value = ''
  } catch (reason) { if (version === stateGeneration && !lifecycle.signal.aborted) error.value = reason instanceof Error ? reason.message : '读取失败' }
  finally { loading.value = false; refreshing.value = false; if (version !== stateGeneration) await refresh() }
}
async function act(name: string, fn: () => Promise<void>, dialog = false) {
  if (busy.value) return
  busy.value = name; (dialog ? dialogError : error).value = ''; notice.value = ''
  try { await fn() } catch (reason) { if (!lifecycle.signal.aborted) (dialog ? dialogError : error).value = reason instanceof Error ? reason.message : '操作失败' }
  finally { busy.value = ''; poll() }
}
function edit(rule?: MediaRule) {
  editor.value = rule ? { ...rule } : { id: '', name: '', storage: 'local', source: '', target: '', classify: true, enabled: false, intervalMinutes: 5 }
  editorSnapshot.value = ruleSignature(editor.value); dialogError.value = ''
}
function storageChanged() { if (editor.value) Object.assign(editor.value, { source: '', target: '', failure: '', sourceId: '', targetId: '', failureId: '', accountId: '' }) }
function clearFailure() { if (editor.value) { editor.value.failure = ''; editor.value.failureId = '' } }
async function chooseDirectory(which: 'source' | 'target' | 'failure') {
  await act('directory', async () => {
    const data = await p115Request<{ accounts: { id: string; primary: boolean }[] }>(props.token, 'accounts', { signal: lifecycle.signal })
    const primary = data.accounts.find(a => a.primary)
    if (!primary) throw new Error('请先在 115 助手配置主账号')
    if (editor.value) { if (editor.value.accountId && editor.value.accountId !== primary.id) storageChanged(); editor.value.accountId = primary.id }
    directory.value = which
  }, true)
}
function picked(value: { id: string; path: string }) { if (editor.value && directory.value) { editor.value[directory.value] = value.path; editor.value[directory.value === 'source' ? 'sourceId' : directory.value === 'target' ? 'targetId' : 'failureId'] = value.id }; directory.value = undefined }
async function save() { await act('save', async () => { await request('/rules', { method: 'POST', body: JSON.stringify(editor.value) }); stateGeneration++; editor.value = undefined; notice.value = '整理配置已保存'; await refresh() }, true) }
async function toggleAutomatic(rule: MediaRule) {
  await act(`toggle-${rule.id}`, async () => {
    const saved = await request<MediaRule>('/rules', { method: 'POST', body: JSON.stringify({ ...rule, enabled: !rule.enabled }) })
    stateGeneration++; rules.value = rules.value.map(item => item.id === saved.id ? saved : item)
    notice.value = saved.enabled ? `已启用${saved.name}的目录监控` : `已暂停${saved.name}的目录监控${active.value ? '，正在执行的任务会继续完成' : ''}`
  })
}
async function runNow(rule: MediaRule) {
  await act(rule.id, async () => {
    await request(`/rules/${rule.id}/run`, { method: 'POST' }); stateGeneration++
    notice.value = '已提交，可在整理记录中查看进度与结果'; await refresh()
  })
}
async function remove() { await act('delete', async () => { if (!deleting.value) return; await request(`/rules/${deleting.value.id}`, { method: 'DELETE' }); stateGeneration++; deleting.value = undefined; await refresh() }, true) }
function poll() { window.clearTimeout(timer); if (lifecycle.signal.aborted || document.hidden || !active.value && !rules.value.some(r => r.enabled)) return; timer = window.setTimeout(async () => { await refresh(); poll() }, active.value ? 5000 : 60000) }
async function visible() { if (!document.hidden) await refresh(); poll() }
function decideLeave(value: boolean) { leaveDialog.value = false; leaveDecision?.(value); leaveDecision = undefined }
function beforeUnload(event: BeforeUnloadEvent) { if (busy.value || unsaved.value) { event.preventDefault(); event.returnValue = '' } }
onBeforeRouteLeave(() => {
  leaveDecision?.(false); leaveDecision = undefined
  if (busy.value) { error.value = '正在处理当前操作，请完成后再切换页面'; if (editor.value || deleting.value) dialogError.value = error.value; return false }
  if (!unsaved.value) { leaveDialog.value = false; return true }
  leaveDialog.value = true; return new Promise<boolean>(resolve => { leaveDecision = resolve })
})
onMounted(async () => { window.addEventListener('beforeunload', beforeUnload); document.addEventListener('visibilitychange', visible); await refresh(); poll() })
onBeforeUnmount(() => { lifecycle.abort(); window.clearTimeout(timer); window.removeEventListener('beforeunload', beforeUnload); document.removeEventListener('visibilitychange', visible); decideLeave(false) })
</script>

<template>
    <div class="mo-page">
      <p v-if="error" role="alert" class="mo-error">{{ error }}<button @click="refresh()">重新加载</button></p>
      <p v-if="notice" role="status" class="mo-muted">{{ notice }}</p>
      <section class="mo-rules" aria-label="整理规则">
        <header class="mo-section"><h2>整理规则</h2><button class="mo-button mo-primary" @click="edit()"><IconPlus :size="17" aria-hidden="true" />新增规则</button></header>
        <p v-if="loading" class="mo-empty">正在读取配置…</p>
        <div v-else-if="!rules.length" class="mo-empty"><IconFolder :size="30" /><p>添加来源与目标目录，立即运行或启用监控。</p><button class="mo-button" @click="edit()">添加整理规则</button></div>
        <div v-else class="mo-rule-grid">
          <article v-for="rule in rules" :key="rule.id" class="mo-rule-card ambient-glow" :aria-label="`${rule.name}整理规则`">
            <header class="mo-rule-heading">
              <span class="mo-rule-symbol" aria-hidden="true"><component :is="rule.storage === '115' ? IconCloud : IconServer" :size="21" /></span>
              <div class="mo-rule-name"><h3 :title="rule.name">{{ rule.name }}</h3><span>{{ rule.storage === 'local' ? '本地挂载' : '115 主账号' }}</span></div>
              <div class="mo-rule-tools"><button class="mo-icon" :disabled="!!busy || activeRules.includes(rule.id)" :aria-label="`编辑${rule.name}`" @click="edit(rule)"><IconEdit :size="17" /></button><button class="mo-icon mo-rule-delete" :disabled="!!busy || activeRules.includes(rule.id)" :aria-label="`删除${rule.name}`" @click="deleting = { id: rule.id, name: rule.name }"><IconTrash :size="17" /></button></div>
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
              <button class="mo-button mo-rule-scan" :disabled="!!busy || activeRules.includes(rule.id)" @click="runNow(rule)"><IconPlayerPlay :size="16" aria-hidden="true" />{{ activeRules.includes(rule.id) ? '运行中…' : '立即运行' }}</button>
            </footer>
          </article>
        </div>
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
        <SettingSwitch v-model="editor.classify" label="按作品信息分类" description="根据作品信息及当前分类规则，在目标目录下生成分类子目录。" />
        <SettingSwitch v-model="editor.enabled" label="启用监控" description="按设置间隔扫描并整理媒体及附件，删除无关文件（含压缩包）、未归属字幕和空文件夹；立即运行同样执行清理。" />
        <label v-if="editor.enabled">扫描间隔（分钟）<input v-model.number="editor.intervalMinutes" type="number" min="1" max="1440" required /></label>
        <p class="mo-muted">STRM 正文保持原样。已有同名文件不会覆盖，命名沿用全局电影和剧集模板。</p>
      </form>
      <template #footer><div class="mo-footer"><button class="mo-button" :disabled="!!busy" @click="editor = undefined">取消</button><button form="media-rule-form" type="submit" class="mo-button mo-primary" :disabled="!!busy">{{ busy === 'save' ? '保存中…' : '保存规则' }}</button></div></template>
    </FloatingDialog>
    <P115DirectoryDialog v-if="directory && editor?.accountId" :token="token" :account-id="editor.accountId" :title="directory === 'source' ? '选择来源目录' : directory === 'target' ? '选择目标目录' : '选择整理失败目录'" :allow-root="false" @close="directory = undefined" @select="picked" />
    <FloatingDialog v-if="deleting" title="删除整理规则" close-label="取消删除规则" :close-disabled="!!busy" @close="deleting = undefined"><p class="py-4 text-sm">确认删除{{ deleting.name }}？同时删除该规则的整理记录；实际文件保持不变。</p><p v-if="dialogError" role="alert" class="mo-error">{{ dialogError }}</p><template #footer><div class="mo-footer"><button class="mo-button" :disabled="!!busy" @click="deleting = undefined">取消</button><button class="mo-button mo-primary" :disabled="!!busy" @click="remove">删除规则</button></div></template></FloatingDialog>
    <P115ActionDialog v-if="leaveDialog" title="放弃未保存的整理修改？" @close="decideLeave(false)"><p>整理规则尚未保存，离开后将丢弃这些修改。后台整理任务会继续运行。</p><template #footer><button type="button" class="mo-button" @click="decideLeave(false)">继续编辑</button><button type="button" class="mo-button mo-primary" @click="decideLeave(true)">放弃修改并离开</button></template></P115ActionDialog>
</template>

<style scoped src="../styles/media-organize.css"></style>
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

.mo-form{display:grid;gap:18px;padding:8px 0}.mo-form label{display:grid;gap:7px;font-size:12px;font-weight:500}.mo-form input{width:100%;min-width:0;min-height:36px;padding:7px 10px;border:1px solid rgb(148 163 184/.23);border-radius:9px;background:rgb(var(--color-slate-50));color:rgb(var(--color-slate-900));font-size:13px;outline:none}.mo-form input:focus{border-color:#3b82f6}.mo-input-action{display:flex;gap:6px}.mo-input-action input{flex:1}.mo-form :deep(input[role="switch"]:checked + span){background:#2563eb}:global(.dark .mo-form input){background:#1e293b;color:#e2e8f0;border-color:rgb(148 163 184/.3)}:global(.dark .mo-form){color:#e2e8f0}
</style>
