<script setup lang="ts">
import { computed, ref, watchPostEffect } from 'vue'
import { IconChevronDown } from '@tabler/icons-vue'
import { sourceNaming } from '../composables/useAnimeNaming'
import { projectAnimeNaming } from '../utils/animeNamingPreview'
import type { SourceForm } from '../utils/animeSourceForm'
import type { AnimeNamingTarget } from '../types/animeSubscription'
const props = defineProps<{ source: SourceForm; target?: AnimeNamingTarget; disabled: boolean }>()
const advanced = ref<HTMLDetailsElement>()
watchPostEffect(() => { if (props.source.ruleError?.field.startsWith('episode') && advanced.value) advanced.value.open = true })
const id = computed(() => `mapping-${props.source.ref}`)
const config = computed(() => { try { return { value: props.target ? sourceNaming(props.source, props.target) : null, error: '' } } catch (error) { return { value: null, error: (error as Error).message } } })
const examples = computed(() => {
  const mapping = config.value.value, target = props.target
  if (!mapping || !target) return []
  const base = Number(props.source.firstEpisode || '1')
  return [base, base + 1].filter(n => Number.isInteger(n) && n >= 0 && n <= 99999).map(n => {
    const key = String(n), sourceLabel = `发布第 ${n} 话`
    const result = projectAnimeNaming(target, mapping, { kind: 'episode', rawEpisode: key, episode: n }, props.source.groupName)
    if (result.status !== 'mapped') return { key, sourceLabel, label: '无法按当前偏移命名' }
    const code = `S${String(result.season).padStart(2, '0')}E${String(result.episode).padStart(2, '0')}`
    return { key, sourceLabel, label: [code, result.episodeName].filter(Boolean).join(' · ') }
  })
})
</script>
<template>
  <section class="ae-source-naming" aria-label="集号与命名预览">
    <div v-if="target" class="ae-field ae-offset"><label :for="`${id}-offset`" class="ae-label">集数偏移</label><input :id="`${id}-offset`" v-model="source.mappingOffset" inputmode="text" :disabled="disabled" :aria-invalid="!!config.error" :aria-describedby="`${id}-examples`" placeholder="0" /></div>
    <p v-if="config.error" class="ae-error" role="status">{{ config.error }}</p>
    <div :id="`${id}-examples`" class="ae-mapping-examples" aria-live="polite"><p v-for="example in examples" :key="example.key">{{ example.sourceLabel }}对应 {{ example.label }}</p></div>
    <details ref="advanced" class="ae-advanced"><summary>自定义<IconChevronDown :size="15" /></summary><fieldset :disabled="disabled"><slot name="custom" /></fieldset></details>
  </section>
</template>
