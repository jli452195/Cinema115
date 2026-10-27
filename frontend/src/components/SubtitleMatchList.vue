<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { subtitleLanguages, subtitleTargetName } from '../utils/subtitleLibrary'
import type { SubtitleMatchRow } from '../utils/subtitleImport'

const props = defineProps<{ rows: SubtitleMatchRow[]; videos: { id: string; name: string }[]; targetNames: Record<string, string>; editableVersion?: boolean; disabled?: boolean; initialLanguage?: string }>()
const language = ref(props.rows.some(row => row.language === props.initialLanguage) ? props.initialLanguage! : 'all')
const visible = computed(() => props.rows.filter(row => language.value === 'all' || row.language === language.value))
const availableLanguages = computed(() => subtitleLanguages.filter(lang => props.rows.some(row => row.language === lang.value)))
watch(availableLanguages, options => { if (language.value !== 'all' && !options.some(option => option.value === language.value)) language.value = 'all' })
function sync(row: SubtitleMatchRow) { if (row.pairKey) for (const other of props.rows) if (other !== row && other.pairKey === row.pairKey) Object.assign(other, { videoId: row.videoId, language: row.language, version: row.version, selected: row.selected }) }
function selectVisible() { for (const row of visible.value) { row.selected = !!row.videoId; sync(row) } }
</script>

<template>
  <div class="subtitle-match">
    <div class="match-filters"><label>语言 <select v-model="language" aria-label="筛选字幕语言" :disabled="disabled"><option value="all">全部</option><option v-for="lang in availableLanguages" :key="lang.value" :value="lang.value">{{ lang.label }}</option></select></label><button type="button" :disabled="disabled" @click="selectVisible">选择当前已匹配项</button><button type="button" :disabled="disabled" @click="rows.forEach(row => row.selected = false)">清空选择</button></div>
    <ul class="match-files"><li v-for="row in visible" :key="row.key">
      <label class="match-name"><input v-model="row.selected" type="checkbox" :disabled="disabled" :aria-label="`选择字幕 ${row.file.name}`" @change="sync(row)" /><span>{{ row.file.name }}</span></label>
      <div class="match-mapping"><select v-model="row.videoId" :aria-label="`${row.file.name} 对应视频`" :disabled="disabled" @change="sync(row)"><option value="">请选择对应视频</option><option v-for="video in videos" :key="video.id" :value="video.id">{{ video.name }}</option></select><select v-model="row.language" :aria-label="`${row.file.name} 语言`" :disabled="disabled" @change="sync(row)"><option v-for="lang in subtitleLanguages" :key="lang.value" :value="lang.value">{{ lang.label }}</option></select><input v-if="editableVersion" v-model="row.version" :aria-label="`${row.file.name} 版本`" :disabled="disabled" maxlength="30" placeholder="版本（可选）" @input="sync(row)" /></div>
      <p v-if="row.videoId" class="match-target">整理后：{{ subtitleTargetName(targetNames[row.videoId] || videos.find(v => v.id === row.videoId)?.name || '', row.file.name, row.language, row.version) }}</p><p v-else class="match-target">{{ row.reason }}</p>
    </li></ul>
  </div>
</template>

<style scoped>
.subtitle-match { min-width:0;font-size:13px; }
.match-filters,.match-mapping { display:flex;align-items:center;gap:8px;flex-wrap:wrap; }
.match-filters { margin:12px 0; }
select,input:not([type="checkbox"]) { min-height:36px;padding:6px 10px;border:1px solid rgb(148 163 184 / .25);border-radius:9px;background:var(--floating-surface-container,#fff);color:inherit; }
button { min-height:36px;border-radius:9px;padding:6px 10px;background:rgb(148 163 184 / .1); }
button:disabled { opacity:.45; }
.match-files { list-style:none;margin:0;padding:0; }
.match-files li { padding:12px 0;border-bottom:1px solid rgb(148 163 184 / .14); }
.match-name { display:flex;align-items:flex-start;gap:8px;overflow-wrap:anywhere; }
.match-name span { min-width:0; }
.match-name input { flex:none;margin-top:3px;accent-color:#2563eb; }
.match-mapping { margin:8px 0 0 23px; }
.match-mapping select:first-child { flex:1;min-width:0; }
.match-mapping input { width:130px;min-width:0; }
.match-target { margin:6px 0 0 23px;font-size:12px;color:var(--floating-surface-muted,#64748b);overflow-wrap:anywhere; }
@media(max-width:639px) { select,input:not([type="checkbox"]),button { min-height:44px;font-size:16px; } .match-mapping select:first-child { flex-basis:100%;width:100%; } .match-mapping input { flex:1;width:110px; } }
</style>
