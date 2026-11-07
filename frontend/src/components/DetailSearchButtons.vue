<script setup lang="ts">
import SearchPill from './SearchPill.vue'
import type { DetailSearchSource, DetailSearchSourceID } from '../utils/detailSearchSources'

withDefaults(defineProps<{
  sources: readonly DetailSearchSource[]
  interactive?: boolean
}>(), {
  interactive: true,
})

const emit = defineEmits<{
  search: [id: DetailSearchSourceID]
}>()
</script>

<template>
  <div class="detail-search-buttons flex flex-wrap items-center gap-3">
    <SearchPill
      v-for="source in sources"
      :key="source.id"
      :data-search-source="source.id"
      :aria-label="source.label"
      :disabled="!interactive"
      :tabindex="interactive ? undefined : -1"
      :class="{ 'is-preview': !interactive }"
      @click="interactive && emit('search', source.id)"
    >
      <img
        :src="source.logo"
        alt=""
        class="h-4 w-4 shrink-0 object-contain"
        :class="{ 'rounded-[4px]': source.id === 're0' }"
        draggable="false"
      />
      <span class="flex items-center gap-0.5">
        <span class="font-semibold" :class="{ 'tracking-wide': source.id === 're0' }">{{ source.name }}</span>
        <span class="detail-muted">搜索</span>
      </span>
    </SearchPill>
  </div>
</template>

<style scoped>
.is-preview {
  cursor: default !important;
  pointer-events: none !important;
  transform: none !important;
  box-shadow: 0 1px 2px rgb(0 0 0 / 5%) !important;
}
.detail-muted {
  color: var(--md-sys-color-on-surface-variant, rgb(var(--content-secondary, 100 116 139)));
}
</style>
