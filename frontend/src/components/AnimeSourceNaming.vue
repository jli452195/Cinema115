<script setup lang="ts">
import { computed } from 'vue'
import { IconPlus, IconTrash, IconChevronDown } from '@tabler/icons-vue'
import { sourceNaming } from '../composables/useAnimeNaming'
import { projectAnimeNaming } from '../utils/animeNamingPreview'
import type { SourceForm } from '../utils/animeSourceForm'
import type { AnimeNamingTarget } from '../types/animeSubscription'
const props = defineProps<{ source: SourceForm; target: AnimeNamingTarget; disabled: boolean }>()
const id = computed(() => `mapping-${props.source.ref}`)
const config = computed(() => { try { return { value: sourceNaming(props.source, props.target), error: '' } } catch (error) { return { value: null, error: (error as Error).message } } })
const examples = computed(() => {
  const mapping = config.value.value
  if (!mapping) return []
  const base = Number(props.source.mappingSource), minimum = Number(props.source.minimum)
  const numbers = props.source.mappingSource ? [...new Set([base, base + 1, minimum > base ? minimum : base])].filter(n => n > 0 && n <= 99999) : []
  const samples = [...numbers.map(n => ({ kind: 'episode', raw: String(n), n })), ...mapping.overrides.filter(o => o.rawKind !== 'episode' || !numbers.includes(Number(o.rawNumber))).map(o => ({ kind: o.rawKind, raw: o.rawNumber, n: Number(o.rawNumber) }))]
  return samples.map(sample => {
    const { n, raw, kind } = sample, key = `${kind}:${raw}`, sourceLabel = kind === 'episode' ? `发布第 ${raw} 话` : `${kind === 'special' ? '特殊集' : '复合编号'} ${raw}`
    const result = projectAnimeNaming(props.target, mapping, { kind, rawEpisode: raw, episode: kind === 'episode' ? n : null }, props.source.groupName)
    if (result.status === 'ignored') return { key, sourceLabel, label: '不整理', path: '' }
    if (result.status === 'unmapped') return { key, sourceLabel, label: '未设置对应，保留原名', path: '' }
    const code = `S${String(result.season).padStart(2, '0')}E${String(result.episode).padStart(2, '0')}`
    return { key, sourceLabel, label: `${code} · ${result.status === 'mapped' ? result.episodeName : '目标资料待核对'}`, path: result.pathStem || '' }
  })
})
function manual() { props.source.mappingManual = true }
function addSegment() {
  manual()
  const previous = props.source.mappingSegments.at(-1), end = previous?.last ?? Number(props.source.mappingLast || props.source.mappingSource || 1)
  const first = Math.min(99999, end + 1)
  props.source.mappingSegments.push({ first, last: null, sourceBase: first, targetSeason: props.target.season, targetBase: 1 })
}
function addOverride() { manual(); props.source.mappingOverrides.push({ rawKind: 'special', rawNumber: '', action: 'map', targetSeason: props.target.season, targetEpisode: 1 }) }
</script>
<template>
  <section class="ae-source-naming" aria-label="集号与命名预览">
    <div class="ae-naming-heading"><h3>集号重命名基准</h3><span class="ae-note">{{ source.mappingManual ? '手动设置' : '系统建议，请核对' }}</span></div>
    <fieldset :disabled="disabled" class="ae-mapping-anchor">
      <legend class="sr-only">发布编号对应目标季集</legend>
      <div class="ae-field"><label :for="`${id}-source`" class="ae-label">发布第几话</label><input :id="`${id}-source`" v-model="source.mappingSource" inputmode="numeric" placeholder="如 25" @input="manual" /></div>
      <div class="ae-field"><label :for="`${id}-season`" class="ae-label">对应目标季</label><select :id="`${id}-season`" :value="source.mappingSeason || target.season" @change="source.mappingSeason = ($event.target as HTMLSelectElement).value; manual()"><option v-for="season in target.seasons" :key="season.number" :value="season.number">第 {{ season.number }} 季</option></select></div>
      <div class="ae-field"><label :for="`${id}-target`" class="ae-label">对应第几集</label><input :id="`${id}-target`" v-model="source.mappingTarget" inputmode="numeric" placeholder="如 1" @input="manual" /></div>
    </fieldset>
    <p v-if="!source.mappingSource" class="ae-note">当前发布可能采用连续编号，请填写一组对应；未配置时保留原名。</p>
    <p v-if="source.mappingSeason && Number(source.mappingSeason) !== target.season" class="ae-note">此来源沿用手动设置的第 {{ source.mappingSeason }} 季；更换推荐季不会覆盖手动映射。</p>
    <p v-if="config.error" class="ae-error" role="status">{{ config.error }}</p>
    <div v-if="examples.length" class="ae-mapping-examples" aria-live="polite"><p v-for="example in examples" :key="example.key">{{ example.sourceLabel }}对应 {{ example.label }}</p><p v-if="examples.find(e => e.path)?.path" class="ae-path-preview">{{ examples.find(e => e.path)?.path }}</p><small>预览不含扩展名；开始订阅后按此方案下载并整理。</small></div>
    <details class="ae-advanced"><summary>分段与特殊集<IconChevronDown :size="15" /></summary><fieldset :disabled="disabled"><div class="ae-field"><label :for="`${id}-last`" class="ae-label">当前映射到发布第几话结束（可留空）</label><input :id="`${id}-last`" v-model="source.mappingLast" inputmode="numeric" placeholder="留空表示持续沿用此对应" @input="manual" /></div>
      <div v-for="(segment, index) in source.mappingSegments" :key="index" class="ae-extra-mapping"><h4>分段 {{ index + 2 }}</h4><div class="ae-pair"><label class="ae-field">范围起点<input v-model.number="segment.first" type="number" min="1" max="99999" @input="manual" /></label><label class="ae-field">范围终点<input v-model.number="segment.last" type="number" min="1" max="99999" @input="segment.last = segment.last || null; manual()" /></label><label class="ae-field">发布基准<input v-model.number="segment.sourceBase" type="number" min="1" max="99999" @input="manual" /></label><label class="ae-field">目标季<select v-model.number="segment.targetSeason" @change="manual"><option v-for="season in target.seasons" :key="season.number" :value="season.number">第 {{ season.number }} 季</option></select></label><label class="ae-field">目标基准集<input v-model.number="segment.targetBase" type="number" min="1" max="99999" @input="manual" /></label></div><button type="button" class="ae-link" @click="source.mappingSegments.splice(index, 1); manual()"><IconTrash :size="15" />删除此分段</button></div>
      <button type="button" class="ae-link" :disabled="source.mappingSegments.length >= 11" @click="addSegment"><IconPlus :size="15" />添加分段</button><p class="ae-note">跨季时先限定上一段终点；各段范围和目标不能重叠。</p>
      <div v-for="(override, index) in source.mappingOverrides" :key="index" class="ae-extra-mapping"><h4>单集例外 {{ index + 1 }}</h4><div class="ae-pair"><label class="ae-field">原始类型<select v-model="override.rawKind" @change="manual"><option value="special">SP／特殊集</option><option value="episode">普通集</option><option value="composite">复合编号</option></select></label><label class="ae-field">原始编号<input v-model="override.rawNumber" maxlength="64" placeholder="与解析出的原始编号一致，如 12.5" @input="manual" /></label><label class="ae-field">处理方式<select v-model="override.action" @change="manual"><option value="map">指定目标</option><option value="ignore">不整理</option></select></label><template v-if="override.action === 'map'"><label class="ae-field">例外目标季<select v-model.number="override.targetSeason" @change="manual"><option v-for="season in target.seasons" :key="season.number" :value="season.number">第 {{ season.number }} 季</option></select></label><label class="ae-field">例外目标集<input v-model.number="override.targetEpisode" type="number" min="1" max="99999" @input="manual" /></label></template></div><button type="button" class="ae-link" @click="source.mappingOverrides.splice(index, 1); manual()"><IconTrash :size="15" />删除此例外</button></div>
      <button type="button" class="ae-link" :disabled="source.mappingOverrides.length >= 100" @click="addOverride"><IconPlus :size="15" />添加单集例外</button><p class="ae-note">SP 与小数集不套用普通偏移；只有明确指定后才参与命名。</p>
    </fieldset></details>
  </section>
</template>
