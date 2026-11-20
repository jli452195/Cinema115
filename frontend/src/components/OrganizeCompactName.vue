<script setup lang="ts">
import type { OrganizeCompactName } from '../utils/organizeSelection'

defineProps<{ name: OrganizeCompactName }>()
</script>

<template>
  <span class="compact-name-line">
    <span v-if="name.prefixOmitted" class="compact-name-difference"><span class="compact-name-omitted" aria-hidden="true">…</span>{{ name.head }}</span>
    <template v-else>
      <span v-if="name.head" class="compact-name-head" :class="{ 'is-marker-head': name.tailStartsWithMarker }">{{ name.head }}</span>
      <span v-if="name.tail" class="compact-name-tail" :class="{ 'is-end-aligned': !name.tailStartsWithMarker }">
        <span v-if="!name.tailStartsWithMarker" class="compact-name-tail-inner">{{ name.tail }}</span>
        <template v-else>{{ name.tail }}</template>
      </span>
    </template>
    <span v-if="name.extension" class="compact-name-extension">{{ name.extension }}</span>
  </span>
</template>

<style scoped>
.compact-name-line { display: flex; width: 100%; min-width: 0; align-items: center; }
.compact-name-omitted { margin-right: 1px; color: rgb(var(--content-secondary)); }
.compact-name-difference { flex: 0 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
/* 分段兜底：无标记时均分收缩并右对齐尾段保留真实结尾；有显式集号或特典标记时优先收缩开头并保留尾段起始标识。 */
.compact-name-head, .compact-name-tail { flex: 0 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: pre; }
.compact-name-head.is-marker-head { flex-shrink: 3; }
.compact-name-tail.is-end-aligned { display: inline-flex; justify-content: flex-end; text-overflow: clip; }
.compact-name-tail-inner { flex: 0 0 auto; white-space: pre; }
.compact-name-extension { flex: 0 0 auto; white-space: nowrap; }
</style>
