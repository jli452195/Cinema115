<script setup lang="ts">
import { computed, ref, onBeforeUnmount, onMounted } from 'vue'
import { IconAdjustmentsHorizontal, IconRss, IconTrash } from '@tabler/icons-vue'
import type { AnimeSourceProvider } from '../types/animeCatalog'
import { newAnimeSource } from '../utils/animeSourceForm'
import AnimeSourcePicker from './AnimeSourcePicker.vue'
import AnimeProviderBrand from './AnimeProviderBrand.vue'
import SettingSwitch from './SettingSwitch.vue'
import AnimeSourceNaming from './AnimeSourceNaming.vue'
import type { AnimeNamingTarget } from '../types/animeSubscription'
import { animeProviderName, animeOriginLabel, animeBindingConfirmed, type SourceForm } from '../utils/animeSourceForm'
const props = defineProps<{ source: SourceForm; chapterTitle: string; disabled: boolean; previewBusy: boolean; now: number; namingTarget?: AnimeNamingTarget; selectionOnly?: boolean }>()
const emit = defineEmits<{ search: [source: SourceForm]; choose: [source: SourceForm, id: string, title: string]; group: [source: SourceForm]; remove: []; open: [source: SourceForm]; sample: [source: SourceForm]; cancel: [source: SourceForm]; apply: [source: SourceForm]; childOpen: [open: boolean]; close: [] }>()
const editing = ref<SourceForm>()
const confirmRemoval = ref(false)
const prefix = computed(() => `anime-source-${props.source.ref}`)
const confirmed = computed(() => animeBindingConfirmed(props.source))
const sourceIssue = computed(() => props.source.health !== 'unavailable' ? '' : props.source.errorCode === 'DOWNLOAD_TARGET_CHANGED' ? 'RSS 暂停读取，请在目录设置中重新核对主账号与目录。' : 'RSS 暂时无法读取，请重新选择或核对该来源。')
function close() { emit('childOpen', false); if (editing.value) emit('cancel', editing.value); editing.value = undefined }
function closePicker() { close(); emit('close') }
function open(provider = props.source.provider) {
  close()
  const draft: SourceForm = provider === props.source.provider
    ? JSON.parse(JSON.stringify(props.source))
    : { ...newAnimeSource(props.source.role, props.chapterTitle, provider), ref: props.source.ref, runWhenEnabled: props.source.runWhenEnabled }
  draft.lockedItem = false; draft.lockedGroup = false
  editing.value = draft
  emit('childOpen', true)
  emit('open', editing.value)
}
function selectSite(id: string) { if (id === 'mikan' || id === 'anibt' || id === 'garden') open(id as AnimeSourceProvider) }
function apply() {
  const draft = editing.value
  editing.value = undefined
  emit('childOpen', false)
  if (draft) emit('apply', draft)
}
onMounted(() => { if (props.selectionOnly) open() })
onBeforeUnmount(close)
</script>

<template>
  <div v-if="!selectionOnly" class="anime-source-form">
    <div class="anime-editor-section-heading">
      <div><h2>{{ source.role === 'main' ? '主 RSS' : '备用 RSS' }}</h2><p>各来源独立运行，不同字幕组分别保留，选定版本后不再自动换版。</p></div>
      <div class="ae-source-tools"><SettingSwitch v-if="source.role === 'standby'" v-model="source.runWhenEnabled" label="启用此备用 RSS" :title="source.runWhenEnabled ? '备用 RSS 已启用' : '备用 RSS 已停用'" :disabled="disabled || previewBusy" class="ae-source-switch" /><button type="button" class="ae-icon" :disabled="disabled || previewBusy" :aria-label="source.role === 'main' ? '重置主 RSS 来源' : '移除备用 RSS 来源'" @click="confirmRemoval = true"><IconTrash :size="18" /></button></div>
    </div>
    <div v-if="confirmRemoval" class="ae-inline-notice"><p>{{ source.role === 'main' ? '重新选择主 RSS 来源？' : '移除此备用来源？' }}保存后清除该来源及其独有的下载、整理记录，保留 115 文件。</p><button type="button" class="ae-button" :disabled="disabled || previewBusy" @click="confirmRemoval = false; $emit('remove')">确认</button><button type="button" class="ae-button" @click="confirmRemoval = false">取消</button></div>
    <div v-if="source.groupId" class="ae-source-summary">
      <div class="ae-summary-title"><IconRss :size="18" aria-hidden="true" /><div><h3>{{ source.groupName || '字幕组待核对' }}</h3><p class="ae-note">{{ animeProviderName(source.provider) }} · {{ animeOriginLabel(source) }}{{ animeOriginLabel(source) ? ' · ' : '' }}{{ source.itemTitle }}</p></div><div class="ae-source-actions"><AnimeProviderBrand :provider="source.provider" compact class="ae-source-brand" /><button type="button" class="ae-button" :disabled="disabled || previewBusy" @click="open()"><IconAdjustmentsHorizontal :size="15" />更换来源</button></div></div>
      <p v-if="!confirmed" class="ae-note">查看最近发布并选择规格后，即可生成匹配预览。</p>
      <p v-if="sourceIssue" class="ae-error" role="status">{{ sourceIssue }}</p>
    </div>
    <div v-else class="ae-choose-source"><IconRss :size="28" aria-hidden="true" /><div><h3>选择资源与字幕组</h3><p>先看最近发布，再选择想保留的规格。</p></div><button type="button" class="ae-button ae-primary" :disabled="disabled || previewBusy" @click="open()">选择资源</button></div>
    <fieldset v-if="source.groupId" :disabled="disabled || previewBusy" class="ae-fields ae-source-options">
      <legend class="sr-only">补番与过滤规则</legend>
      <div class="ae-pair ae-patterns"><div class="ae-field"><label :for="`${prefix}-match`" class="ae-label">匹配正则（全部满足）</label><textarea :id="`${prefix}-match`" v-model="source.match" :aria-invalid="source.ruleError?.field === 'match' || undefined" :aria-describedby="source.ruleError?.field === 'match' ? `${prefix}-rule-error` : undefined" @input="source.ruleError = undefined" rows="2" spellcheck="false" placeholder="每行一条，留空接收全部规格" /></div><div class="ae-field"><label :for="`${prefix}-exclude`" class="ae-label">排除正则（任一命中）</label><textarea :id="`${prefix}-exclude`" v-model="source.exclude" :aria-invalid="source.ruleError?.field === 'exclude' || undefined" :aria-describedby="source.ruleError?.field === 'exclude' ? `${prefix}-rule-error` : undefined" @input="source.ruleError = undefined" rows="2" spellcheck="false" placeholder="例如：(?i)720p" /></div></div>
      <p class="ae-note">每行一条；匹配全部满足，排除任一命中。忽略大小写可加 (?i)。</p>
      <AnimeSourceNaming :source="source" :target="namingTarget" :disabled="disabled || previewBusy">
        <template #custom>
          <div class="ae-field ae-minimum"><label :for="`${prefix}-minimum`" class="ae-label">从第几话开始下载（含本话）</label><input :id="`${prefix}-minimum`" v-model="source.minimum" inputmode="numeric" /></div>
          <div class="ae-pair ae-parser-fields"><div class="ae-field"><label :for="`${prefix}-episode-pattern`" class="ae-label">集号正则</label><input :id="`${prefix}-episode-pattern`" v-model="source.episodePattern" :aria-invalid="source.ruleError?.field === 'episodePattern' || undefined" @input="source.ruleError = undefined" spellcheck="false" placeholder="留空使用默认解析" /></div><div class="ae-field"><label :for="`${prefix}-episode-group`" class="ae-label">捕获组序号</label><input :id="`${prefix}-episode-group`" v-model="source.episodeGroup" @input="source.ruleError = undefined" inputmode="numeric" :disabled="!source.episodePattern.trim()" /></div></div>
        </template>
      </AnimeSourceNaming>
      <p v-if="source.ruleError" :id="`${prefix}-rule-error`" class="ae-error" role="alert">{{ source.ruleError.message }}</p>
    </fieldset>
  </div>
  <AnimeSourcePicker v-if="editing" :key="editing.provider" :source="editing" :chapter-title="chapterTitle" :disabled="disabled || previewBusy" :now="now" @close="closePicker" @provider="selectSite" @search="$emit('search', editing)" @choose="(id, title) => editing && $emit('choose', editing, id, title)" @group="$emit('group', editing)" @sample="$emit('sample', editing)" @confirm="apply" />
</template>
