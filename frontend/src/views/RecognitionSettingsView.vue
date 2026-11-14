<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { IconAlertTriangle, IconArrowRight, IconCheck, IconChevronRight, IconDeviceFloppy, IconDownload, IconFileText, IconHelpCircle, IconPlayerPlay, IconRefresh, IconTrash, IconUpload } from '@tabler/icons-vue'
import SegmentedControl from '../components/SegmentedControl.vue'
import P115ActionDialog from '../components/P115ActionDialog.vue'
import TextSourceEditor from '../components/TextSourceEditor.vue'
import { useRecognitionSettings } from '../composables/useRecognitionSettings'
import type { RecognitionKind } from '../types/recognition'

const props = defineProps<{ token: string }>()
const {
  kind, settings, drafts, loading, saving, importing, error, notice, diagnostic, dialog, dirty, locked, fileNotActive,
  samples, previewing, previewError, results, items, reread, activateFile, save, runPreview, importFile, exportFile, decide,
} = useRecognitionSettings(() => props.token)
const source = ref<InstanceType<typeof TextSourceEditor>>(), fileInput = ref<HTMLInputElement>(), showHelp = ref(false)
const current = computed({ get: () => drafts[kind.value], set: value => { drafts[kind.value] = value } })
const filename = computed(() => kind.value === 'groups' ? 'release-groups.txt' : 'platforms.txt')
const kindLabel = (value: RecognitionKind) => value === 'groups' ? '字幕组与发布组' : '来源平台'
const currentCount = computed(() => current.value.split(/\r?\n/).filter(line => line.trim() && !line.trim().startsWith('#')).length)
const tabs = computed(() => (['groups', 'platforms'] as const).map(value => ({ value, label: value === 'groups' ? '字幕组与发布组' : '来源平台', title: settings.value && drafts[value] !== settings.value.record[value] ? '有未保存的修改' : undefined })))
const dialogTitle = computed(() => dialog.value === 'clear' ? '清空当前词表？' : dialog.value === 'read' ? '重新读取词表文件？' : dialog.value === 'import' ? '将 TXT 载入当前词表？' : '放弃未保存的修改？')
async function locate(target: RecognitionKind, line: number, column = 1) { kind.value = target; await nextTick(); source.value?.locate(line, column) }
async function selectFile(event: Event) {
  const input = event.target as HTMLInputElement, file = input.files?.[0]
  input.value = ''
  if (file) await importFile(file)
}
function fillExamples() {
  samples.value = '[喵萌奶茶屋&LoliHouse] 示例动画 - 01 [1080p].mkv\n[樱都字幕组] 示例动画 - 02 [1080p].mkv\nExample.S01E01.Netflix.WEB-DL.1080p-NTb.mkv\n示例剧集.S01E02.爱奇艺.WEB-DL.1080p.mkv'
}
</script>

<template>
  <div class="recognition-settings">
    <header class="rs-toolbar">
      <div class="rs-switches"><SegmentedControl v-model="kind" label="识别词表类型" density="compact" :options="tabs" :disabled="locked || !settings" /><span v-if="dirty" class="rs-dirty">未保存草稿</span></div>
      <button class="rs-button rs-primary" type="button" :disabled="locked || !dirty" @click="save"><IconDeviceFloppy :size="16" aria-hidden="true" />{{ saving ? '保存中…' : '保存并生效' }}</button>
    </header>

    <div v-if="loading" class="rs-status" role="status">正在读取识别词表…</div>
    <div v-if="error" class="rs-alert rs-error" role="alert"><IconAlertTriangle :size="17" aria-hidden="true" /><div><p>{{ error }}</p><p v-if="settings">当前草稿已保留，可修改后重试，或导出备份后重新读取文件。</p></div></div>
    <div v-if="settings?.record.loadError" class="rs-alert rs-warning" role="alert"><IconAlertTriangle :size="17" aria-hidden="true" /><div><p>词表文件未能加载：{{ settings.record.loadError }}</p><p>请修正后保存。{{ settings.record.activeRevision ? '当前继续使用最后一次有效词表。' : '词表尚未生效。' }}</p></div></div>
    <div v-else-if="fileNotActive" class="rs-alert rs-warning"><span>文件内容与当前生效词表不同</span><button type="button" class="rs-button" :disabled="locked || dirty" @click="activateFile">应用文件修改</button></div>

    <div v-if="settings" class="rs-workspace">
      <section class="rs-panel rs-editor-panel" aria-label="识别词表编辑">
        <header class="rs-panel-header"><div class="rs-file"><IconFileText :size="16" aria-hidden="true" /><code>{{ filename }}</code><span>{{ currentCount }} 条规则</span></div><button class="rs-link" type="button" :aria-expanded="showHelp" aria-controls="recognition-syntax" @click="showHelp = !showHelp"><IconHelpCircle :size="15" aria-hidden="true" />语法说明</button></header>
        <div class="rs-editor-tools">
          <button class="rs-button rs-quiet" type="button" :disabled="locked" @click="fileInput?.click()"><IconUpload :size="15" aria-hidden="true" />{{ importing ? '读取中…' : '导入 TXT' }}</button>
          <button class="rs-button rs-quiet" type="button" :disabled="loading || importing" @click="exportFile"><IconDownload :size="15" aria-hidden="true" />导出 TXT</button>
          <button class="rs-button rs-quiet" type="button" :disabled="loading || saving || importing" @click="reread"><IconRefresh :size="15" aria-hidden="true" />重新读取</button>
          <button class="rs-button rs-quiet" type="button" :disabled="locked || !current" @click="dialog = 'clear'"><IconTrash :size="15" aria-hidden="true" />清空词表</button>
        </div>
        <div v-if="showHelp" id="recognition-syntax" class="rs-help">
          <p>每行一条正则，默认保留命中原文：<code>(?:桜|樱)都字幕组</code></p>
          <p>需要映射时写：<code>桜都字幕组 ~= (?:桜|樱)都字幕[组組]</code></p>
          <p>空行及 <code>#</code> 开头的注释会被忽略。联合组分别识别，同片段映射优先；不同映射发生冲突时不选用。</p>
          <p>使用 Go 正则语法，默认忽略大小写，可用 <code>(?-i:...)</code> 区分大小写；不支持前后查找及反向引用。</p>
        </div>
        <TextSourceEditor :key="kind" ref="source" v-model="current" :label="`${kindLabel(kind)}词表内容`" :disabled="locked" :error-line="diagnostic?.kind === kind ? diagnostic.line : undefined" describedby="recognition-editor-help" />
        <button v-if="diagnostic" class="rs-diagnostic" type="button" @click="locate(diagnostic.kind, diagnostic.line, diagnostic.column)"><IconAlertTriangle :size="16" aria-hidden="true" /><span>{{ kindLabel(diagnostic.kind) }}第 {{ diagnostic.line }} 行：{{ diagnostic.message }}</span><span class="rs-locate">定位</span></button>
        <footer id="recognition-editor-help" class="rs-editor-footer"><p v-if="!currentCount">当前词表为空，此类信息不会被识别。</p>UTF-8 TXT，每个文件最多 256 KiB。保存会同时更新两个词表。</footer>
      </section>

      <section class="rs-panel rs-tester" aria-label="草稿识别测试" :aria-busy="previewing">
        <header class="rs-panel-header"><div><h2>草稿识别测试</h2><p>使用当前草稿，无需保存</p></div><button class="rs-link" type="button" :disabled="loading" @click="fillExamples">填入示例</button></header>
        <div class="rs-test-input">
          <label for="recognition-samples">文件名或发布标题</label>
          <textarea id="recognition-samples" v-model="samples" aria-label="测试文件名" rows="4" placeholder="每行一个文件名，试试包含联合字幕组或 NF、爱奇艺的名称" spellcheck="false" autocapitalize="off"></textarea>
          <div class="rs-test-actions"><span>{{ items.length }} / 100 行</span><div><button class="rs-link" type="button" :disabled="!samples" @click="samples = ''">清空</button><button class="rs-button" type="button" :disabled="loading || previewing || !items.length || items.length > 100" @click="runPreview"><IconPlayerPlay :size="14" aria-hidden="true" />{{ previewing ? '测试中…' : '测试识别' }}</button></div></div>
        </div>
        <div class="rs-results" aria-live="polite">
          <p v-if="previewing" class="rs-result-status" role="status">正在使用当前草稿识别…</p>
          <div v-else-if="previewError" class="rs-result-error" role="alert"><p>{{ previewError }}</p><button v-if="diagnostic" class="rs-link" type="button" @click="locate(diagnostic.kind, diagnostic.line, diagnostic.column)">定位{{ kindLabel(diagnostic.kind) }}第 {{ diagnostic.line }} 行</button></div>
          <div v-else-if="!results.length" class="rs-empty"><IconFileText :size="27" aria-hidden="true" /><p>粘贴文件名，查看识别出的字幕组和平台</p><span>修改词表后会自动重新测试，命中行可直接定位。</span></div>
          <article v-for="(item, index) in results" :key="index" class="rs-result">
            <p class="rs-result-name">{{ item.name }}</p>
            <div class="rs-tags"><span v-for="group in item.releaseGroups" :key="`g-${group}`" class="rs-tag rs-group">{{ group }}</span><span v-if="!item.releaseGroups.length" class="rs-missing">未识别发布组</span><span v-for="platform in item.platforms" :key="`p-${platform}`" class="rs-tag rs-platform">{{ platform }}</span><span v-if="!item.platforms.length" class="rs-missing">未识别平台</span></div>
            <details v-if="item.matches.length" class="rs-match-details"><summary><IconChevronRight class="rs-chevron" :size="14" aria-hidden="true" />{{ item.matches.length }} 处命中，查看原文与规则行</summary><ul><li v-for="(match, matchIndex) in item.matches" :key="matchIndex"><button class="rs-link" type="button" @click="locate(match.kind, match.line)">{{ match.kind === 'groups' ? '发布组' : '平台' }}第 {{ match.line }} 行</button><code>{{ match.raw }}</code><template v-if="match.value !== match.raw"><IconArrowRight :size="13" aria-label="映射为" /><code>{{ match.value }}</code></template></li></ul></details>
            <div v-for="(conflict, conflictIndex) in item.conflicts" :key="conflictIndex" class="rs-conflict"><IconAlertTriangle :size="15" aria-hidden="true" /><div><p>“{{ conflict.raw }}”存在映射冲突，未采用该片段。</p><div><button v-for="line in conflict.lines" :key="line" class="rs-link" type="button" @click="locate(conflict.kind, line)">{{ conflict.kind === 'groups' ? '发布组' : '平台' }}第 {{ line }} 行</button></div><p v-if="conflict.values?.length">目标名称：{{ conflict.values.join('、') }}</p></div></div>
          </article>
        </div>
      </section>
    </div>

    <div v-if="!settings && !loading" class="rs-status"><button class="rs-button" type="button" @click="reread"><IconRefresh :size="15" aria-hidden="true" />重新读取词表</button></div>
    <p v-if="notice" class="rs-notice" role="status"><IconCheck :size="16" aria-hidden="true" />{{ notice }}</p>
    <p class="rs-scope">用于普通文件整理与搜索下载整理；RSS 沿用来源字幕组。已确认的整理计划不受词表修改影响。</p>
    <details class="rs-references">
      <summary><IconChevronRight class="rs-chevron" :size="14" aria-hidden="true" />词表参考</summary>
      <div>
        <p>可参考以下项目补充组名与平台写法，整理为本页支持的 UTF-8 TXT 后手动导入。</p>
        <ul>
          <li><a href="https://github.com/jxxghp/MoviePilot/blob/HEAD/app/domain/meta/releasegroup.py" target="_blank" rel="noopener noreferrer">MoviePilot 发布组规则</a><span>组名参考，原格式为 Python</span></li>
          <li><a href="https://github.com/TRaSH-Guides/Guides/tree/master/docs/json/sonarr/cf" target="_blank" rel="noopener noreferrer">TRaSH-Guides 公开规则</a><span>发布组与平台写法参考，原格式为 JSON</span></li>
        </ul>
        <p>这些资料不能直接作为本页 TXT 导入，正则也需符合 Go 语法。导入只载入草稿，测试后保存生效。</p>
      </div>
    </details>
    <input ref="fileInput" type="file" accept=".txt,text/plain" hidden aria-label="导入识别词表 TXT" @change="selectFile" />
  </div>

  <P115ActionDialog v-if="dialog" :title="dialogTitle" @close="decide(false)">
    <p v-if="dialog === 'clear'">仅清空当前“{{ kindLabel(kind) }}”词表的草稿，另一个词表的草稿会保留。保存后才会生效。</p>
    <p v-else-if="dialog === 'import'">TXT 将替换对应词表的当前草稿，另一个词表保持不变。保存后才会生效。</p>
    <p v-else-if="dialog === 'read'">重新读取会替换两个词表的本地草稿。如需保留修改，请先取消并导出 TXT。</p>
    <p v-else>两个词表的修改尚未保存，离开后会丢失。</p>
    <template #footer><button class="rs-button" type="button" @click="decide(false)">{{ dialog === 'leave' ? '继续编辑' : '取消' }}</button><button class="rs-button rs-primary" type="button" @click="decide(true)">{{ dialog === 'clear' ? '清空词表' : dialog === 'read' ? '重新读取' : dialog === 'import' ? '载入草稿' : '放弃并离开' }}</button></template>
  </P115ActionDialog>
</template>

<style scoped>
.rs-match-details summary { display:flex; align-items:center; gap:3px; list-style:none; }
.rs-match-details summary::-webkit-details-marker { display:none; }
.rs-chevron { flex-shrink:0; }
.rs-match-details[open] .rs-chevron { transform:rotate(90deg); }
.rs-references { font-size:12px; line-height:1.8; color:var(--rs-muted); }
.rs-references summary { display:flex; align-items:center; gap:4px; width:fit-content; min-height:36px; cursor:pointer; list-style:none; color:#2563eb; }
.rs-references summary::-webkit-details-marker { display:none; }
.rs-references[open] .rs-chevron { transform:rotate(90deg); }
.rs-references>div { display:grid; gap:8px; padding:8px 12px; border-left:2px solid var(--rs-border); overflow-wrap:anywhere; }
.rs-references ul { display:grid; gap:6px; }
.rs-references li { display:flex; align-items:baseline; gap:10px; flex-wrap:wrap; }
.rs-references a { color:#2563eb; text-decoration:underline; text-underline-offset:3px; }
.rs-references summary:focus-visible,.rs-references a:focus-visible { outline:2px solid #93c5fd; outline-offset:2px; }
.dark .rs-references summary,.dark .rs-references a { color:#93c5fd; }
.recognition-settings { --rs-border:rgb(var(--color-slate-200) / .8); --rs-muted:rgb(var(--color-slate-500)); --rs-ink:rgb(var(--color-slate-800)); display:grid; gap:16px; color:var(--rs-ink); }
.rs-toolbar,.rs-switches,.rs-editor-tools,.rs-test-actions,.rs-test-actions>div { display:flex; align-items:center; gap:10px; }.rs-toolbar { justify-content:space-between; flex-wrap:wrap; min-height:40px; }.rs-switches { min-width:0; flex-wrap:wrap; }.rs-dirty { color:#a16207; font-size:11px; white-space:nowrap; }
.rs-button,.rs-link { display:inline-flex; justify-content:center; align-items:center; gap:6px; font-size:12px; line-height:1.5; }.rs-button { min-height:36px; padding:7px 11px; border-radius:8px; border:1px solid var(--rs-border); color:rgb(var(--color-slate-700)); background:rgb(var(--color-slate-50) / .5); white-space:nowrap; }.rs-primary { background:#2563eb; border-color:#2563eb; color:white; }.rs-quiet { border-color:transparent; background:transparent; padding:6px 8px; }.rs-link { min-height:32px; color:#2563eb; text-align:left; }.rs-button:disabled,.rs-link:disabled { opacity:.45; cursor:default; }.rs-button:focus-visible,.rs-link:focus-visible,.rs-diagnostic:focus-visible { outline:2px solid #93c5fd; outline-offset:2px; }
.rs-workspace { display:grid; grid-template-columns:minmax(0,1.15fr) minmax(0,.85fr); gap:16px; align-items:start; }.rs-panel { min-width:0; overflow:hidden; border:1px solid var(--rs-border); border-radius:12px; background:rgb(255 255 255 / .75); }.rs-panel-header { display:flex; justify-content:space-between; align-items:center; gap:10px; min-height:56px; padding:10px 14px; border-bottom:1px solid var(--rs-border); background:rgb(var(--color-slate-50) / .5); }.rs-panel-header h2 { font-size:13px; font-weight:600; }.rs-panel-header p { margin-top:3px; font-size:11px; color:var(--rs-muted); }.rs-file { min-width:0; display:flex; flex-wrap:wrap; align-items:center; gap:7px; }.rs-file code { font-size:12px; }.rs-file>span { font-size:11px; color:var(--rs-muted); }.rs-editor-tools { flex-wrap:wrap; justify-content:flex-end; gap:2px; padding:5px 7px; border-bottom:1px solid var(--rs-border); }
.rs-help { display:grid; gap:7px; padding:12px 14px; font-size:12px; line-height:1.8; color:rgb(var(--color-slate-600)); background:rgb(37 99 235 / .035); border-bottom:1px solid var(--rs-border); overflow-wrap:anywhere; }.rs-help code { font-family:ui-monospace,Consolas,monospace; color:var(--rs-ink); }.rs-editor-footer,.rs-scope { font-size:11px; line-height:1.8; color:var(--rs-muted); }.rs-editor-footer { padding:9px 14px; border-top:1px solid var(--rs-border); }.rs-scope { margin-top:2px; }
.rs-diagnostic { display:flex; align-items:flex-start; gap:7px; width:100%; padding:10px 13px; color:#b91c1c; background:rgb(220 38 38 / .06); border-top:1px solid rgb(220 38 38 / .2); text-align:left; font-size:12px; overflow-wrap:anywhere; }.rs-diagnostic svg { flex-shrink:0; margin-top:2px; }.rs-locate { margin-left:auto; flex-shrink:0; text-decoration:underline; }
.rs-test-input { display:grid; gap:7px; padding:13px; border-bottom:1px solid var(--rs-border); }.rs-test-input label { font-size:12px; font-weight:500; }.rs-test-input textarea { width:100%; min-height:122px; resize:vertical; border:1px solid var(--rs-border); border-radius:8px; padding:9px 10px; background:rgb(var(--color-slate-50) / .45); color:inherit; font-family:ui-monospace,SFMono-Regular,Consolas,monospace; font-size:12px; line-height:1.85; }.rs-test-input textarea:focus { outline:1px solid #2563eb; border-color:#2563eb; }.rs-test-input textarea::placeholder { color:rgb(var(--color-slate-400)); }.rs-test-actions { justify-content:space-between; font-size:11px; color:var(--rs-muted); }
.rs-results { display:grid; gap:10px; padding:13px; min-height:200px; max-height:440px; overflow:auto; align-content:start; }.rs-result { min-width:0; padding:11px; border:1px solid var(--rs-border); border-radius:9px; background:rgb(var(--color-slate-50) / .35); }.rs-result-name { color:rgb(var(--color-slate-600)); font:11px/1.8 ui-monospace,SFMono-Regular,Consolas,monospace; overflow-wrap:anywhere; }.rs-tags { display:flex; align-items:center; flex-wrap:wrap; gap:5px; margin-top:9px; }.rs-tag { padding:3px 7px; border-radius:5px; font-size:11px; line-height:1.5; overflow-wrap:anywhere; max-width:100%; }.rs-group { background:rgb(37 99 235 / .08); color:#1d4ed8; }.rs-platform { background:rgb(var(--color-slate-200) / .5); color:rgb(var(--color-slate-700)); }.rs-missing { font-size:11px; color:var(--rs-muted); margin-right:5px; }.rs-match-details { margin-top:9px; color:var(--rs-muted); font-size:11px; }.rs-match-details summary { cursor:pointer; min-height:28px; padding-block:5px; }.rs-match-details ul { display:grid; gap:4px; margin-top:4px; }.rs-match-details li { display:flex; align-items:center; gap:6px; flex-wrap:wrap; }.rs-match-details code { overflow-wrap:anywhere; color:rgb(var(--color-slate-600)); }.rs-match-details .rs-link { font-size:11px; min-height:28px; }.rs-conflict { display:flex; align-items:flex-start; gap:6px; margin-top:9px; padding:8px; border-radius:6px; font-size:11px; line-height:1.8; color:#a16207; background:rgb(245 158 11 / .08); overflow-wrap:anywhere; }.rs-conflict>svg { flex-shrink:0; margin-top:3px; }.rs-conflict .rs-link { font-size:11px; margin-right:8px; }
.rs-empty { min-height:176px; display:flex; flex-direction:column; align-items:center; justify-content:center; gap:9px; text-align:center; color:rgb(var(--color-slate-400)); font-size:12px; line-height:1.8; padding:16px; }.rs-empty>span { font-size:11px; }.rs-result-status,.rs-status { font-size:12px; line-height:1.8; color:var(--rs-muted); }.rs-result-status { padding:16px; }.rs-result-error { font-size:12px; line-height:1.8; color:#b91c1c; padding:8px; }
.rs-alert { display:flex; align-items:flex-start; gap:9px; padding:11px 13px; border:1px solid; border-radius:9px; font-size:12px; line-height:1.8; overflow-wrap:anywhere; }.rs-alert>svg { margin-top:2px; flex-shrink:0; }.rs-error { color:#b91c1c; background:rgb(220 38 38 / .04); border-color:rgb(220 38 38 / .18); }.rs-warning { color:#a16207; background:rgb(245 158 11 / .05); border-color:rgb(245 158 11 / .2); }.rs-warning>.rs-button { margin-left:auto; }.rs-notice { display:flex; align-items:center; gap:7px; color:var(--rs-muted); font-size:12px; }
.dark .recognition-settings { --rs-border:rgb(var(--color-slate-700) / .65); --rs-muted:rgb(var(--color-slate-400)); --rs-ink:rgb(var(--color-slate-200)); }.dark .rs-panel { background:rgb(var(--color-slate-900) / .6); }.dark .rs-panel-header,.dark .rs-result,.dark .rs-test-input textarea { background:rgb(var(--color-slate-950) / .25); }.dark .rs-button { background:rgb(var(--color-slate-800) / .5); color:rgb(var(--color-slate-200)); }.dark .rs-primary { background:#2563eb; color:#fff; }.dark .rs-quiet { background:transparent; }.dark .rs-link { color:#93c5fd; }.dark .rs-help,.dark .rs-result-name,.dark .rs-match-details code { color:rgb(var(--color-slate-400)); }.dark .rs-group { background:rgb(147 197 253 / .12); color:#93c5fd; }.dark .rs-platform { background:rgb(var(--color-slate-800)); color:rgb(var(--color-slate-300)); }.dark .rs-warning,.dark .rs-dirty,.dark .rs-conflict { color:#fcd34d; }.dark .rs-error,.dark .rs-diagnostic,.dark .rs-result-error { color:#fca5a5; }
@media(hover:hover) { .rs-button:not(:disabled):hover { filter:brightness(.96); }.rs-quiet:not(:disabled):hover { background:rgb(var(--color-slate-200) / .4); }.rs-link:not(:disabled):hover { text-decoration:underline; } }
@media(max-width:1023px) { .rs-workspace { grid-template-columns:minmax(0,1fr); }.rs-results { max-height:none; }.rs-tester { min-height:0; } }
@media(max-width:639px) { .rs-toolbar { gap:10px; }.rs-switches { width:100%; justify-content:space-between; }.rs-toolbar>.rs-button { width:100%; min-height:42px; }.rs-dirty { font-size:10px; }.rs-panel-header { padding-inline:11px; gap:7px; }.rs-file { gap:6px; }.rs-file code { font-size:11px; }.rs-file>span { font-size:10px; }.rs-editor-tools { display:grid; grid-template-columns:repeat(4,minmax(0,1fr)); padding-inline:3px; }.rs-editor-tools .rs-button { font-size:11px; min-height:42px; padding-inline:3px; gap:4px; }.rs-editor-tools .rs-button svg { width:13px; }.rs-test-input textarea { font-size:16px; }.rs-link { min-height:36px; }.rs-match-details .rs-link { min-height:36px; }.rs-alert { flex-wrap:wrap; }.rs-scope { font-size:11px; } }
</style>
