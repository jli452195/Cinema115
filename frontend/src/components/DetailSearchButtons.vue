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
  <div class="detail-search-buttons">
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
.detail-search-buttons { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 12px; }
.search-pill.is-preview {
  cursor: default;
  pointer-events: none;
  transform: none;
  opacity: 1;
}
.detail-muted {
  color: var(--glass-muted);
}
</style>
