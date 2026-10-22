<script setup lang="ts">
import { computed, ref, watchPostEffect } from 'vue'
import { IconChevronDown } from '@tabler/icons-vue'
import { sourceNaming } from '../composables/useAnimeNaming'
import type { SourceForm } from '../utils/animeSourceForm'
import type { AnimeNamingTarget } from '../types/animeSubscription'
const props = defineProps<{ source: SourceForm; target?: AnimeNamingTarget; disabled: boolean }>()
const advanced = ref<HTMLDetailsElement>()
watchPostEffect(() => { if (props.source.ruleError?.field.startsWith('episode') && advanced.value) advanced.value.open = true })
const id = computed(() => `mapping-${props.source.ref}`)
const error = computed(() => { try { if (props.target) sourceNaming(props.source, props.target); return '' } catch (reason) { return (reason as Error).message } })
</script>
<template>
  <section class="ae-source-naming" aria-label="集号与命名预览">
    <div v-if="target" class="ae-field ae-offset"><label :for="`${id}-offset`" class="ae-label">集数偏移</label><input :id="`${id}-offset`" v-model="source.mappingOffset" inputmode="text" :disabled="disabled" :aria-invalid="!!error" :aria-describedby="error ? `${id}-error` : undefined" placeholder="0" /></div>
    <p v-if="error" :id="`${id}-error`" class="ae-error" role="status">{{ error }}</p>
    <details ref="advanced" class="ae-advanced"><summary>自定义<IconChevronDown :size="15" /></summary><fieldset :disabled="disabled"><slot name="custom" /></fieldset></details>
  </section>
</template>
