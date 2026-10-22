<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { IconAdjustmentsHorizontal, IconSearch, IconRefresh, IconChevronDown } from '@tabler/icons-vue'
import SelectMenu from './SelectMenu.vue'
import type { useAnimeNaming } from '../composables/useAnimeNaming'
const props = defineProps<{ naming: ReturnType<typeof useAnimeNaming>; disabled: boolean }>()
const manualID = ref('')
const advancedOpen = ref(false)
const targetID = `anime-naming-${useId()}`
const seasonOptions = computed(() => props.naming.target.value?.seasons.map(season => ({ value: String(season.number), label: `${season.name || `第 ${season.number} 季`} · ${season.episodeCount} 集${season.date ? ` · ${season.date}` : ''}` })) || [])
const groupOptions = computed(() => {
  const target = props.naming.target.value
  const items = props.naming.groups.value.map(group => ({ value: group.id, label: group.name }))
  if (target?.groupId && !items.some(group => group.value === target.groupId)) items.unshift({ value: target.groupId, label: target.groupName })
  return [{ value: '', label: 'TMDB 默认季集' }, ...items]
})
function loadID() { if (/^[1-9]\d*$/.test(manualID.value.trim())) void props.naming.load(manualID.value.trim()) }
function toggleAdvanced() {
  advancedOpen.value = !advancedOpen.value
  if (advancedOpen.value) void props.naming.readGroups()
}
watch(() => props.naming.target.value?.tmdbId, () => { advancedOpen.value = false })
</script>
<template>
  <section class="ae-naming-target" aria-label="TMDB 命名目标" :aria-busy="naming.busy.value || naming.searching.value">
    <div class="ae-naming-heading">
      <div class="ae-target-identity"><span class="ae-label">整理目标</span><h3 v-if="naming.target.value">{{ naming.target.value.title }} <small v-if="naming.target.value.year">({{ naming.target.value.year }})</small></h3><p v-else class="ae-note">{{ naming.searching.value || naming.busy.value ? '正在匹配 TMDB 作品与目标季…' : '关联 TMDB 后，按对应季集标准命名整理。' }}</p></div>
      <button type="button" class="ae-button ae-target-change" :disabled="disabled || naming.busy.value" :aria-expanded="naming.picker.value" @click="naming.picker.value = !naming.picker.value"><IconAdjustmentsHorizontal :size="16" aria-hidden="true" />{{ naming.target.value ? '更换目标' : '关联 TMDB' }}</button>
    </div>
    <div class="ae-target-controls">
      <div v-if="naming.target.value" class="ae-target-season"><span class="ae-label">目标季</span><SelectMenu class="ae-target-season-menu" :model-value="String(naming.target.value.season)" :options="seasonOptions" label="目标季" variant="anime" :disabled="disabled || naming.busy.value" @change="naming.load(naming.target.value!.tmdbId, Number($event), naming.target.value!.groupId)" /></div>
      <slot name="parameters" />
      <button v-if="naming.target.value" :id="`${targetID}-options-button`" type="button" class="ae-button ae-target-options" :disabled="disabled || naming.busy.value" :aria-expanded="advancedOpen" :aria-controls="`${targetID}-options`" @click="toggleAdvanced"><span class="ae-target-group-name">{{ naming.target.value.groupId ? naming.target.value.groupName : '默认编排' }}</span><span>更多</span><IconChevronDown :size="15" :class="{ 'is-open': advancedOpen }" aria-hidden="true" /></button>
    </div>
    <div v-if="naming.target.value" v-show="advancedOpen" :id="`${targetID}-options`" class="ae-target-advanced" role="region" :aria-labelledby="`${targetID}-options-button`">
      <div class="ae-target-arrangement"><div class="ae-field"><span class="ae-label">剧集编排</span><SelectMenu class="ae-target-group-menu" :model-value="naming.target.value.groupId" :options="groupOptions" label="剧集编排" variant="anime" :disabled="disabled || naming.busy.value" @change="naming.load(naming.target.value!.tmdbId, undefined, $event)" /></div><button type="button" class="ae-link" :disabled="disabled || naming.busy.value" @click="naming.load(naming.target.value!.tmdbId, naming.target.value!.season, naming.target.value!.groupId)"><IconRefresh :size="14" aria-hidden="true" />重新读取目标资料</button></div>
      <p class="ae-note">请核对目标季与下方实际编号；此编排仅用于本订阅的命名整理。</p>
    </div>
    <div v-if="naming.picker.value" class="ae-target-picker">
      <form class="ae-search" @submit.prevent="naming.search()"><input v-model="naming.keyword.value" aria-label="搜索 TMDB 作品" maxlength="128" placeholder="输入作品名称" :disabled="disabled" /><button type="submit" class="ae-button" :disabled="disabled || naming.searching.value"><IconSearch :size="16" aria-hidden="true" />搜索</button></form>
      <div class="ae-target-candidates"><button v-for="item in naming.candidates.value" :key="item.id" type="button" :disabled="disabled || naming.busy.value" @click="naming.load(String(item.id))"><strong>{{ item.title }}</strong><small>{{ item.date?.slice(0, 4) || '年份待定' }} · TMDB {{ item.id }}</small></button></div>
      <p v-if="!naming.searching.value && !naming.candidates.value.length" class="ae-note">可搜索作品名称，或直接填写已知 TMDB ID。</p>
      <form class="ae-search" @submit.prevent="loadID"><input v-model="manualID" inputmode="numeric" aria-label="TMDB 作品 ID" placeholder="TMDB 作品 ID" :disabled="disabled" /><button type="submit" class="ae-button" :disabled="disabled || naming.busy.value || !/^[1-9]\d*$/.test(manualID.trim())">使用此 ID</button></form>
    </div>
    <p v-if="naming.error.value" class="ae-error" role="status">{{ naming.error.value }}<span v-if="naming.target.value">，当前目标已保留。</span></p>
  </section>
</template>
