<script setup lang="ts">
import { computed, ref, watchPostEffect } from 'vue'
import { IconPlus, IconTrash, IconChevronDown } from '@tabler/icons-vue'
import { sourceNaming } from '../composables/useAnimeNaming'
import { projectAnimeNaming } from '../utils/animeNamingPreview'
import type { SourceForm } from '../utils/animeSourceForm'
import type { AnimeNamingTarget } from '../types/animeSubscription'
const props = defineProps<{ source: SourceForm; target?: AnimeNamingTarget; disabled: boolean }>()
const advanced = ref<HTMLDetailsElement>()
watchPostEffect(() => { if (props.source.ruleError?.field.startsWith('episode') && advanced.value) advanced.value.open = true })
const id = computed(() => `mapping-${props.source.ref}`)
const config = computed(() => { try { return { value: props.target ? sourceNaming({ ...props.source, mappingOverrides: props.source.mappingOverrides.filter(o => o.rawNumber.trim()) }, props.target) : null, error: '' } } catch (error) { return { value: null, error: (error as Error).message } } })
const examples = computed(() => {
  const mapping = config.value.value, target = props.target
  if (!mapping || !target) return []
  const base = Number(props.source.firstEpisode || '1')
  const numbers = [...new Set([base, base + 1])].filter(n => n >= 0 && n <= 99999)
  const samples = [...numbers.map(n => ({ kind: 'episode', raw: String(n), n })), ...mapping.overrides.filter(o => o.rawKind !== 'episode' || !numbers.includes(Number(o.rawNumber))).map(o => ({ kind: o.rawKind, raw: o.rawNumber, n: Number(o.rawNumber) }))]
  return samples.map(sample => {
    const { n, raw, kind } = sample, key = `${kind}:${raw}`, sourceLabel = kind === 'episode' ? `发布第 ${raw} 话` : `${kind === 'special' ? '特殊集' : '复合编号'} ${raw}`
    const result = projectAnimeNaming(target, mapping, { kind, rawEpisode: raw, episode: kind === 'episode' ? n : null }, props.source.groupName)
    if (result.status === 'ignored') return { key, sourceLabel, label: '不整理', path: '' }
    if (result.status === 'unmapped') return { key, sourceLabel, label: '未设置对应，保留原名', path: '' }
    const code = `S${String(result.season).padStart(2, '0')}E${String(result.episode).padStart(2, '0')}`
    return { key, sourceLabel, label: [code, result.episodeName].filter(Boolean).join(' · '), path: result.pathStem || '' }
  })
})
function addSegment() {
  if (!props.target) return
  const previous = props.source.mappingSegments.at(-1), end = previous?.last ?? Number(props.source.mappingLast || props.source.firstEpisode || 1)
  const first = Math.min(99999, end + 1)
  props.source.mappingSegments.push({ first, last: null, sourceBase: first, targetSeason: props.target.season, targetBase: 1 })
}
function addOverride() { if (props.target) props.source.mappingOverrides.push({ rawKind: 'special', rawNumber: '', action: 'map', targetSeason: props.target.season, targetEpisode: 1 }) }
</script>
<template>
  <section class="ae-source-naming" aria-label="集号与命名预览">
    <div v-if="target" class="ae-field ae-offset"><label :for="`${id}-offset`" class="ae-label">集数偏移</label><input :id="`${id}-offset`" v-model="source.mappingOffset" inputmode="text" :disabled="disabled" :aria-invalid="!!config.error" :aria-describedby="`${id}-examples`" placeholder="0" /></div>
    <p v-if="config.error" class="ae-error" role="status">{{ config.error }}</p>
    <div :id="`${id}-examples`" class="ae-mapping-examples" aria-live="polite"><p v-for="example in examples.slice(0, 2)" :key="example.key">{{ example.sourceLabel }}对应 {{ example.label }}</p></div>
    <details ref="advanced" class="ae-advanced"><summary>自定义<IconChevronDown :size="15" /></summary><fieldset :disabled="disabled"><slot name="custom" /><template v-if="target"><div class="ae-field"><label :for="`${id}-last`" class="ae-label">普通偏移到第几话结束（可留空）</label><input :id="`${id}-last`" v-model="source.mappingLast" inputmode="numeric" placeholder="留空持续沿用" /></div>
      <div v-for="(segment, index) in source.mappingSegments" :key="index" class="ae-extra-mapping"><h4>分段 {{ index + 2 }}</h4><div class="ae-pair"><label class="ae-field">范围起点<input v-model.number="segment.first" type="number" min="0" max="99999" /></label><label class="ae-field">范围终点<input v-model.number="segment.last" type="number" min="0" max="99999" @input="segment.last = ($event.target as HTMLInputElement).value === '' ? null : segment.last" /></label><label class="ae-field">发布基准<input v-model.number="segment.sourceBase" type="number" min="0" max="99999" /></label><label class="ae-field">目标季<select v-model.number="segment.targetSeason"><option v-for="season in target.seasons" :key="season.number" :value="season.number">第 {{ season.number }} 季</option></select></label><label class="ae-field">目标基准集<input v-model.number="segment.targetBase" type="number" min="0" max="99999" /></label></div><button type="button" class="ae-link" @click="source.mappingSegments.splice(index, 1)"><IconTrash :size="15" />删除此分段</button></div>
      <button type="button" class="ae-link" :disabled="source.mappingSegments.length >= 11" @click="addSegment"><IconPlus :size="15" />添加分段</button><p class="ae-note">跨季时先限定上一段终点；各段范围和目标不能重叠。</p>
      <div v-for="(override, index) in source.mappingOverrides" :key="index" class="ae-extra-mapping"><h4>单集例外 {{ index + 1 }}</h4><div class="ae-pair"><label class="ae-field">原始类型<select v-model="override.rawKind"><option value="special">SP／特殊集</option><option value="episode">普通集</option><option value="composite">复合编号</option></select></label><label class="ae-field">原始编号<input v-model="override.rawNumber" maxlength="64" placeholder="与解析出的原始编号一致，如 12.5" /></label><label class="ae-field">处理方式<select v-model="override.action"><option value="map">指定目标</option><option value="ignore">不整理</option></select></label><template v-if="override.action === 'map'"><label class="ae-field">例外目标季<select v-model.number="override.targetSeason"><option v-for="season in target.seasons" :key="season.number" :value="season.number">第 {{ season.number }} 季</option></select></label><label class="ae-field">例外目标集<input v-model.number="override.targetEpisode" type="number" min="0" max="99999" /></label></template></div><button type="button" class="ae-link" @click="source.mappingOverrides.splice(index, 1)"><IconTrash :size="15" />删除此例外</button></div>
      <button type="button" class="ae-link" :disabled="source.mappingOverrides.length >= 100" @click="addOverride"><IconPlus :size="15" />添加单集例外</button><p class="ae-note">SP 与小数集不套用普通偏移；只有明确指定后才参与命名。</p>
    </template></fieldset></details>
  </section>
</template>
