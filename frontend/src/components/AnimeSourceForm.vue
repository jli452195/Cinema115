<script setup lang="ts">
import { computed, ref, onBeforeUnmount } from 'vue'
import { IconAdjustmentsHorizontal, IconRss, IconTrash, IconChevronDown } from '@tabler/icons-vue'
import AnimeProviderButton from './AnimeProviderButton.vue'
import { animeProviders, type AnimeSourceProvider } from '../types/animeCatalog'
import { newAnimeSource } from '../utils/animeSourceForm'
import AnimeSourcePicker from './AnimeSourcePicker.vue'
import AnimeSourceNaming from './AnimeSourceNaming.vue'
import type { AnimeNamingTarget } from '../types/animeSubscription'
import { animeProviderName, animeOriginLabel, animeBindingConfirmed, animeSourceSpecs, type SourceForm } from '../utils/animeSourceForm'
const props = defineProps<{ source: SourceForm; chapterTitle: string; disabled: boolean; previewBusy: boolean; now: number; namingTarget?: AnimeNamingTarget }>()
const emit = defineEmits<{ search: [source: SourceForm]; choose: [source: SourceForm, id: string, title: string]; group: [source: SourceForm]; remove: []; open: [source: SourceForm]; sample: [source: SourceForm]; cancel: [source: SourceForm]; apply: [source: SourceForm]; childOpen: [open: boolean] }>()
const editing = ref<SourceForm>()
const confirmRemoval = ref(false)
const prefix = computed(() => `anime-source-${props.source.ref}`)
const confirmed = computed(() => animeBindingConfirmed(props.source))
const sourceIssue = computed(() => props.source.health !== 'unavailable' ? '' : props.source.errorCode === 'DOWNLOAD_TARGET_CHANGED' ? 'RSS 暂停读取，请在目录设置中重新核对主账号与目录。' : 'RSS 暂时无法读取，请重新选择或核对该来源。')
const range = computed(() => props.source.scope === 'range' ? `发布第 ${props.source.first || '未填'} 至 ${props.source.last || '未填'} 话` : props.source.scope === 'confirmed_chapter' ? '整个来源属于当前篇章' : '尚未选择来源范围')
function close() { emit('childOpen', false); if (editing.value) emit('cancel', editing.value); editing.value = undefined }
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
onBeforeUnmount(close)
</script>

<template>
  <div class="anime-source-form">
    <div class="anime-editor-section-heading">
      <div><h2>{{ source.role === 'main' ? '主 RSS' : '备用 RSS' }}</h2><p>各来源独立运行，不同字幕组分别保留，选定版本后不再自动换版。</p></div>
      <button type="button" class="ae-icon" :disabled="disabled || previewBusy" :aria-label="source.role === 'main' ? '重置主 RSS 来源' : '移除备用 RSS 来源'" @click="confirmRemoval = true"><IconTrash :size="18" /></button>
    </div>
    <div v-if="confirmRemoval" class="ae-inline-notice"><p>{{ source.role === 'main' ? '重新选择主 RSS 来源？' : '移除此备用来源？' }}已有执行记录会保留。</p><button type="button" class="ae-button" :disabled="disabled || previewBusy" @click="confirmRemoval = false; $emit('remove')">确认</button><button type="button" class="ae-button" @click="confirmRemoval = false">取消</button></div>
    <div class="ae-source-sites"><span class="ae-note">{{ source.groupId ? '换个来源' : '选择资源站点' }}</span><div role="group" aria-label="选择 RSS 站点"><AnimeProviderButton v-for="site in animeProviders" :key="site.id" :provider="site.id" :name="site.name" :available="site.available && !disabled && !previewBusy" :selected="!!source.groupId && source.provider === site.id" compact @select="selectSite(site.id)" /></div></div>
    <div v-if="source.groupId" class="ae-source-summary">
      <div class="ae-summary-title"><IconRss :size="20" aria-hidden="true" /><div><h3>{{ source.groupName || '字幕组待核对' }}</h3><p class="ae-note">{{ animeOriginLabel(source) }}{{ animeOriginLabel(source) ? ' · ' : '' }}{{ source.itemTitle }} · {{ animeProviderName(source.provider) }} {{ source.itemId }}</p></div><button type="button" class="ae-button" :disabled="disabled || previewBusy" @click="open()"><IconAdjustmentsHorizontal :size="16" />修改来源规则</button></div>
      <dl class="ae-source-facts"><div><dt>匹配规则</dt><dd>{{ animeSourceSpecs(source) }}</dd></div><div><dt>来源</dt><dd>{{ confirmed ? '已选定' : '待选定' }}<span v-if="source.scope === 'range'"> · {{ range }}</span></dd></div></dl>
      <p v-if="!confirmed" class="ae-note">查看最近发布并选择规格后，即可生成匹配预览。</p>
      <p v-if="sourceIssue" class="ae-error" role="status">{{ sourceIssue }}</p>
    </div>
    <div v-else class="ae-choose-source"><IconRss :size="28" aria-hidden="true" /><div><h3>选择资源与字幕组</h3><p>先看最近发布，再选择想保留的规格。</p></div><button type="button" class="ae-button ae-primary" :disabled="disabled || previewBusy" @click="open()">选择资源</button></div>
    <AnimeSourceNaming v-if="source.groupId && namingTarget" :source="source" :target="namingTarget" :disabled="disabled || previewBusy" />
    <p v-else-if="source.groupId" class="ae-original-note">未关联 TMDB，下载保留发布原名。</p>
    <fieldset v-if="source.groupId" :disabled="disabled || previewBusy" class="ae-fields ae-source-options">
      <legend class="sr-only">补番与过滤规则</legend>
      <div class="ae-field"><label :for="`${prefix}-minimum`" class="ae-label">从第几话开始下载（含本话）</label><input :id="`${prefix}-minimum`" v-model="source.minimum" inputmode="numeric" placeholder="字幕组标题中的编号" /><p class="ae-note">按字幕组发布编号填写，后台自动补齐并持续检查更新。</p></div>
      <details class="ae-advanced"><summary>高级下载规则<IconChevronDown :size="15" /></summary><label class="ae-check"><input type="checkbox" :checked="source.scope === 'range'" @change="source.scope = ($event.target as HTMLInputElement).checked ? 'range' : 'feed'; source.correspondence = false; source.identityNumbering = false" /><span>限定发布编号范围</span></label><div v-if="source.scope === 'range'" class="ae-pair"><label class="ae-field">范围起点<input v-model="source.first" inputmode="numeric" placeholder="如 25" /></label><label class="ae-field">范围终点<input v-model="source.last" inputmode="numeric" placeholder="如 36" /></label></div></details>
      <div class="ae-pair ae-patterns"><div class="ae-field"><label :for="`${prefix}-match`" class="ae-label">匹配正则（全部满足）</label><textarea :id="`${prefix}-match`" v-model="source.match" :aria-invalid="source.ruleError?.field === 'match' || undefined" :aria-describedby="source.ruleError?.field === 'match' ? `${prefix}-rule-error` : undefined" @input="source.ruleError = undefined" rows="3" spellcheck="false" placeholder="每行一条，留空接收全部规格" /></div><div class="ae-field"><label :for="`${prefix}-exclude`" class="ae-label">排除正则（任一命中）</label><textarea :id="`${prefix}-exclude`" v-model="source.exclude" :aria-invalid="source.ruleError?.field === 'exclude' || undefined" :aria-describedby="source.ruleError?.field === 'exclude' ? `${prefix}-rule-error` : undefined" @input="source.ruleError = undefined" rows="3" spellcheck="false" placeholder="例如：(?i)720p" /></div></div>
      <p class="ae-note">每行一条，不按逗号分隔。使用 Go/RE2 正则，默认区分大小写；忽略大小写可加 (?i)。</p>
      <details class="ae-advanced" :open="source.ruleError?.field.startsWith('episode')"><summary>自定义集号提取<IconChevronDown :size="15" /></summary><div class="ae-pair"><div class="ae-field"><label :for="`${prefix}-episode-pattern`" class="ae-label">集号正则</label><input :id="`${prefix}-episode-pattern`" v-model="source.episodePattern" :aria-invalid="source.ruleError?.field === 'episodePattern' || undefined" :aria-describedby="source.ruleError?.field === 'episodePattern' ? `${prefix}-rule-error` : undefined" @input="source.ruleError = undefined" spellcheck="false" placeholder="留空使用默认解析" /></div><div class="ae-field"><label :for="`${prefix}-episode-group`" class="ae-label">捕获组序号</label><input :id="`${prefix}-episode-group`" v-model="source.episodeGroup" :aria-invalid="source.ruleError?.field === 'episodeGroup' || undefined" :aria-describedby="source.ruleError?.field === 'episodeGroup' ? `${prefix}-rule-error` : undefined" @input="source.ruleError = undefined" inputmode="numeric" :disabled="!source.episodePattern.trim()" /></div></div><p class="ae-note">例如 -\s*(\d+)，集号取捕获组 1。自定义规则未匹配或出现多个集号时，仅跳过该条资源。</p></details>
      <p v-if="source.ruleError" :id="`${prefix}-rule-error`" class="ae-error" role="alert">{{ source.ruleError.message }}</p>
      <label v-if="source.role === 'standby'" class="ae-check"><input v-model="source.runWhenEnabled" type="checkbox" /><span>启用此备用 RSS</span></label>
    </fieldset>

    <AnimeSourcePicker v-if="editing" :source="editing" :chapter-title="chapterTitle" :disabled="disabled || previewBusy" :now="now" @close="close" @search="$emit('search', editing)" @choose="(id, title) => editing && $emit('choose', editing, id, title)" @group="$emit('group', editing)" @sample="$emit('sample', editing)" @confirm="apply" />
  </div>
</template>
