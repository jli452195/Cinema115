<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { IconArrowLeft, IconCheck, IconChevronDown, IconChevronRight, IconExternalLink, IconRefresh, IconSearch } from '@tabler/icons-vue'
import AnimeFloatingDialog from './AnimeFloatingDialog.vue'
import { animeProviderName, animeOriginLabel, animeSourceURL, animeBindingConfirmed, confirmAnimeSource, animeSpecKey, animeSpecLabel, type SourceForm } from '../utils/animeSourceForm'
import type { AnimeSpec } from '../types/animeSubscription'
const props = defineProps<{ source: SourceForm; chapterTitle: string; disabled: boolean; now: number }>()
const emit = defineEmits<{ close: []; search: []; choose: [id: string, title: string]; group: []; sample: []; confirm: [] }>()
const step = ref<'items' | 'groups' | 'rules'>(props.source.groupId ? 'rules' : props.source.itemId ? 'groups' : 'items')
const heading = ref<HTMLElement>(), body = ref<HTMLElement>(), error = ref(''), errorField = ref('')
const shell = ref<InstanceType<typeof AnimeFloatingDialog>>()
const positions = { items: 0, groups: 0, rules: 0 }
let top = 0
const prefix = computed(() => `source-picker-${props.source.ref}`)
const partyLabel = computed(() => props.source.provider === 'garden' ? '发布方' : '字幕组')
const sample = computed(() => props.source.sample)
const profiles = computed(() => {
  const options = [...(sample.value?.profiles || [])]
  for (const spec of props.source.specProfiles) if (!options.some(option => animeSpecKey(option.spec) === animeSpecKey(spec))) options.push({ key: animeSpecKey(spec), spec, label: animeSpecLabel(spec), count: 0 })
  return options
})
async function go(value: typeof step.value) { positions[step.value] = top; step.value = value; error.value = ''; await nextTick(); shell.value?.scrollTo(positions[value]); heading.value?.focus({ preventScroll: true }) }
watch(() => props.source.entry, entry => { if (entry && step.value === 'items') void go('groups') })
function choose(id: string, title: string) { if (props.disabled || props.source.searchBusy) return; if (props.source.entry?.id === id) void go('groups'); else emit('choose', id, title) }
function group(id: string) {
  if (props.disabled || props.source.searchBusy) return
  if (props.source.groupId !== id) { props.source.groupId = id; emit('group') }
  else emit('sample')
  void go('rules')
}
function selected(spec: AnimeSpec) { return props.source.specMode === 'profiles' && props.source.specProfiles.some(profile => animeSpecKey(profile) === animeSpecKey(spec)) }
function toggle(spec: AnimeSpec) {
  const source = props.source, key = animeSpecKey(spec), wasSelected = selected(spec)
  if (!wasSelected && source.specProfiles.length >= 12) { error.value = '每个来源最多选择 12 种规格'; return }
  source.specMode = 'profiles'; source.resolution = 'any'
  source.specProfiles = wasSelected ? source.specProfiles.filter(profile => animeSpecKey(profile) !== key) : [...source.specProfiles, JSON.parse(JSON.stringify(spec))]
  error.value = ''
}
function allSpecifications() { const all = props.source.specMode !== 'any'; props.source.specMode = all ? 'any' : ''; props.source.specProfiles = []; props.source.resolution = all ? 'any' : ''; error.value = '' }
async function confirm() {
  if (props.disabled || props.source.searchBusy || props.source.sampleBusy && !sample.value) return
  error.value = ''
  let target = ''
  if (!props.source.specMode && !props.source.resolution || props.source.specMode === 'profiles' && !props.source.specProfiles.length) { error.value = '请选择至少一种规格，或明确选择全部规格'; target = '[data-spec-option]' }
  else {
    try {
      confirmAnimeSource(props.source)
    } catch (reason) {
      error.value = reason instanceof Error ? reason.message : '请检查来源规则'
      target = '[data-spec-option]'
    }
  }
  errorField.value = target
  if (error.value) { await nextTick(); const input = body.value?.querySelector<HTMLElement>(target); input?.focus(); input?.scrollIntoView({ block: 'center', behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth' }); return }
  emit('confirm')
}
</script>

<template>
  <AnimeFloatingDialog ref="shell" :title="`选择${animeProviderName(source.provider)}来源`" close-label="关闭来源选择器" @close="$emit('close')" @scroll="top = $event">
    <div ref="body" class="anime-editor ae-picker">
      <nav class="ae-picker-nav" aria-label="来源选择步骤"><button v-if="step !== 'items' && !(step === 'rules' && source.lockedGroup)" type="button" class="ae-icon" :aria-label="step === 'rules' ? `返回${partyLabel}` : '返回资源条目'" :disabled="disabled" @click="go(step === 'rules' ? 'groups' : 'items')"><IconArrowLeft :size="18" /></button><div><p class="ae-note">{{ chapterTitle }}</p><h3 ref="heading" tabindex="-1">{{ step === 'items' ? '选择资源条目' : step === 'groups' ? (source.provider === 'garden' ? '选择发布方' : '选择字幕组') : '选择资源规格' }}</h3></div><span class="ae-note">{{ step === 'items' ? '1' : step === 'groups' ? '2' : '3' }} / 3</span></nav>
      <p v-if="source.searchError" class="ae-error" role="status">{{ source.searchError }}</p><p v-if="source.cooldown > now" class="ae-note">来源冷却中，请 {{ Math.ceil((source.cooldown - now) / 1000) }} 秒后重试。</p>
      <fieldset :disabled="disabled" :aria-busy="source.searchBusy" class="ae-picker-content">
        <legend class="sr-only">{{ step === 'items' ? `${animeProviderName(source.provider)}候选` : step === 'groups' ? `${partyLabel}列表` : '来源规格' }}</legend>
        <template v-if="step === 'items'">
          <label :for="`${prefix}-search`" class="ae-label">搜索{{ animeProviderName(source.provider) }}资源</label><form class="ae-search" @submit.prevent="!source.searchBusy && $emit('search')"><input :id="`${prefix}-search`" v-model="source.keyword" type="search" :maxlength="source.provider === 'anibt' ? 120 : 128" placeholder="输入番剧名称" autocomplete="off" /><button type="submit" class="ae-button ae-primary" :disabled="source.searchBusy || source.cooldown > now"><IconSearch :size="17" />搜索</button></form>
          <p class="ae-note ae-picker-help">请选择与当前篇章对应的条目，搜索结果不会自动绑定。</p>
          <p v-if="source.searchBusy" class="ae-note" role="status">正在读取候选条目…</p>
          <p v-if="source.search" class="ae-note">{{ source.searchError ? '上次成功搜索' : '搜索结果' }}：{{ source.searchedKeyword }}</p>
          <div v-if="source.search" class="ae-picker-list"><button v-for="item in source.search.items" :key="item.id" type="button" :aria-disabled="source.searchBusy" :disabled="source.cooldown > now || source.lockedItem && source.itemId !== item.id" @click="choose(item.id, item.title)"><span><strong>{{ item.title }}</strong><small>{{ animeProviderName(source.provider) }} {{ item.id }}</small></span><IconChevronRight :size="18" aria-hidden="true" /></button></div>
          <p v-if="source.search && !source.search.items.length" class="ae-source-empty">没有找到资源，可以换个名称搜索，或先保存草稿。</p><p v-if="source.search?.truncated" class="ae-note">结果较多，请缩小搜索范围。</p>
        </template>
        <template v-else-if="step === 'groups'">
          <p class="ae-picker-selected">{{ source.itemTitle }}<small>{{ animeProviderName(source.provider) }} {{ source.itemId }}</small></p>
          <p class="ae-note">{{ source.provider === 'garden' ? '选择字幕组或发布者，同时固定它的原发布站。' : '展开一个字幕组，查看它的最近发布和可选规格。' }}</p><p v-if="source.entry?.groupsPartial" class="ae-note">当前发布方来自最近 200 条资源，不代表全部历史。</p>
          <p v-if="source.searchBusy" class="ae-note" role="status">正在读取{{ partyLabel }}…</p>
          <div class="ae-picker-list"><button v-for="item in source.entry?.groups" :key="item.id" type="button" :aria-disabled="source.searchBusy" :disabled="source.lockedGroup && source.groupId !== item.id" @click="group(item.id)"><span><strong>{{ item.name }}</strong><small><template v-if="item.originName">{{ item.originName }} · {{ item.kind === 'publisher' ? '发布者' : '字幕组' }} · </template>{{ source.groupId === item.id ? `已选${partyLabel}` : '查看最近发布' }}</small></span><IconChevronRight :size="18" aria-hidden="true" /></button></div>
          <p v-if="source.entry && !source.entry.groups.length" class="ae-source-empty">暂未读取到{{ partyLabel }}，可稍后重试或保存草稿。</p>
          <button v-if="!source.entry && !source.searchBusy" type="button" class="ae-button" :disabled="source.cooldown > now" @click="$emit('choose', source.itemId, source.itemTitle)">重试读取{{ partyLabel }}</button>
        </template>
        <template v-else>
          <p class="ae-picker-selected">{{ source.groupName }}<small>{{ animeOriginLabel(source) }}{{ animeOriginLabel(source) ? ' · ' : '' }}{{ source.itemTitle }} · {{ animeProviderName(source.provider) }} {{ source.itemId }}</small></p>
          <section class="ae-recent" aria-label="最近发布" :aria-busy="source.sampleBusy"><div class="ae-recent-heading"><h4>最近发布</h4><button type="button" class="ae-link" :disabled="source.sampleBusy || source.sampleCooldown > now" @click="$emit('sample')"><IconRefresh :size="14" :class="{ 'animate-spin': source.sampleBusy }" />{{ source.sampleBusy ? '读取中' : source.sampleError ? '重试读取' : '刷新样本' }}</button></div>
            <p v-if="source.sampleError" role="status" class="ae-error">{{ source.sampleError }}{{ sample ? '，保留上次成功样本。' : '' }}</p><p v-else-if="source.sampleBusy && !sample" role="status" class="ae-note">正在读取此{{ partyLabel }}的近期资源…</p>
            <template v-if="sample"><ul class="ae-recent-list"><li v-for="item in sample.releases.slice(0, 1)" :key="item.postKey"><p>{{ item.title }}</p><small>{{ item.parse.episode != null ? `发布第 ${item.parse.episode} 话` : '编号待核对' }}<span v-if="item.sizeRaw"> · {{ item.sizeRaw }}</span></small></li></ul><details v-if="sample.releases.length > 1" class="ae-advanced"><summary>其余 {{ sample.releases.length - 1 }} 条样本<IconChevronDown :size="15" /></summary><ul class="ae-recent-list"><li v-for="item in sample.releases.slice(1)" :key="item.postKey"><p>{{ item.title }}</p><small>{{ item.sizeRaw }}</small></li></ul></details><p v-if="!sample.releases.length" class="ae-note">来源读取成功，当前没有发布。可明确选择全部规格，或先保存草稿。</p><p class="ae-note ae-sample-boundary">{{ sample.releases.length }} 条近期样本，仅代表已读窗口。规格依据标题声明。<span v-if="sample.observedAt">读取于 {{ new Date(sample.observedAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) }}。</span></p></template>
          </section>
          <section class="ae-specifications" aria-label="资源规格"><h4>保留哪些规格<small v-if="!source.specMode && !source.resolution" class="ae-section-state">请选择</small></h4><p class="ae-note">{{ !sample && source.sampleError ? '未能读取样本，可选择全部规格继续配置，或稍后重试。' : '可多选，同集不同版本分别保留。' }}</p><p v-if="source.sampleBusy && !sample" class="ae-note" role="status">正在分析近期发布规格…</p><div class="ae-spec-grid" :aria-busy="source.sampleBusy && !sample"><label v-for="option in profiles" :key="option.key" class="ae-spec-option" :class="{ selected: selected(option.spec) }"><input data-spec-option type="checkbox" :disabled="source.sampleBusy && !sample" :checked="selected(option.spec)" @change="toggle(option.spec)" /><span><strong>{{ option.label }}</strong><small>{{ option.count ? `当前样本 ${option.count} 条` : '未在当前样本出现，保留原规则' }}</small></span></label><label class="ae-spec-option" :class="{ selected: source.specMode === 'any' }"><input data-spec-option type="checkbox" :disabled="source.sampleBusy && !sample" :checked="source.specMode === 'any'" @change="allSpecifications" /><span><strong>全部规格</strong><small>接收该组所有规格，可能包含同集多个版本</small></span></label></div><p v-if="error && errorField === '[data-spec-option]'" role="alert" class="ae-error">{{ error }}</p><p v-if="sample?.profileReviewCount" class="ae-note">{{ sample.profileReviewCount }} 条标题同时列出多种规格，未作为单独选项；可在匹配预览中核对。</p><p v-if="sample?.profilesTruncated" class="ae-note">样本规格较多，仅展示部分组合；不会自动扩大所选规则。</p><p v-if="!source.specMode && source.resolution" class="ae-note">当前保留原画质规则：{{ source.resolution === 'any' ? '不限画质' : source.resolution }}。可在上方选择更具体的规格。</p></section>

        </template>
      </fieldset>
      <p v-if="source.sampleCooldown > now" class="ae-note">样本读取冷却中，请 {{ Math.ceil((source.sampleCooldown - now) / 1000) }} 秒后重试。</p>
    </div>
    <template v-if="step === 'rules'" #footer>
      <footer class="anime-editor ae-picker-dock"><p class="ae-note">选择所需规格，返回配置页查看季集与命名预览。</p><div><button type="button" class="ae-button ae-primary" :disabled="disabled || source.searchBusy || source.sampleBusy && !sample" @click="confirm"><IconCheck :size="17" />{{ animeBindingConfirmed(source) ? '更新来源规则' : '使用此来源' }}</button><a :href="animeSourceURL(source)" class="ae-link" target="_blank" rel="noopener noreferrer">查看来源<IconExternalLink :size="13" /><span class="sr-only">（新窗口）</span></a></div></footer>
    </template>
  </AnimeFloatingDialog>
</template>
