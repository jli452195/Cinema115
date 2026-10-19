<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useId } from 'vue'
import { useRoute } from 'vue-router'
import { IconExternalLink } from '@tabler/icons-vue'
import AnimeFloatingDialog from './AnimeFloatingDialog.vue'
import { animeConfigMessage, animeConfigRequest } from '../lib/anime-config-api'
import type { MikanEntry } from '../types/animeSubscription'
import type { MikanCatalogItem } from '../types/animeCatalog'
import { animeEditorEntry } from '../utils/animeEditorEntry'
const props = defineProps<{ token: string; item: MikanCatalogItem }>()
defineEmits<{ close: [] }>()
const route = useRoute(), entry = ref<MikanEntry>(), loading = ref(false), error = ref(''), bangumiID = ref<string | null>(null)
const manualAssociation = ref(false)
async function editAssociation() { manualAssociation.value = true; await nextTick(); document.getElementById('mikan-bangumi-id')?.focus() }
let controller: AbortController | undefined, associationInitialized = false
const choiceName = useId()
const bangumiLinks = computed(() => [...new Set((entry.value?.bangumiIds || []).map(String).filter(id => /^[1-9]\d{0,11}$/.test(id)))])
const validID = computed(() => !bangumiID.value || /^[1-9]\d{0,11}$/.test(bangumiID.value))
const target = computed(() => animeEditorEntry(bangumiID.value || '', 'mikan', props.item.id, route.fullPath))
async function load() {
  controller?.abort(); const active = new AbortController(); controller = active; loading.value = true; error.value = ''
  try {
    const result = await animeConfigRequest<{ entry: MikanEntry }>(props.token, `sources/mikan/${props.item.id}`, { signal: active.signal })
    if (!active.signal.aborted) {
      entry.value = result.entry
      const links = bangumiLinks.value
      if (!associationInitialized && !bangumiID.value && links.length === 1) bangumiID.value = links[0]!
      associationInitialized = true
    }
  }
  catch (reason) { if (!active.signal.aborted) error.value = animeConfigMessage(reason) }
  finally { if (controller === active) loading.value = false }
}
void load()
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <AnimeFloatingDialog title="蜜柑订阅来源" close-label="关闭蜜柑条目" @close="$emit('close')">
    <div class="mikan-entry-dialog">
      <h3>{{ item.title }}</h3><p class="anime-context">蜜柑 {{ item.id }}<span v-if="item.updatedDate"> · 资源更新 {{ item.updatedDate }}</span></p>
      <a :href="`https://mikanani.me/Home/Bangumi/${item.id}`" target="_blank" rel="noopener noreferrer" class="mikan-entry-link">查看蜜柑原站<IconExternalLink :size="14" aria-hidden="true" /><span class="sr-only">（新窗口）</span></a>
      <p v-if="loading" role="status" class="anime-context">正在读取此条目的字幕组…</p>
      <p v-if="error" role="status" class="anime-context">{{ error }} <button type="button" class="mikan-entry-link" @click="load">重试</button></p>
      <template v-if="entry">
        <p class="mikan-entry-label">可用字幕组</p><div class="mikan-entry-groups"><span v-for="group in entry.groups" :key="group.id">{{ group.name }}</span><p v-if="!entry.groups.length" class="anime-context">当前没有发布组，可继续保存草稿。</p></div>
        <details class="mikan-entry-association">
          <summary>Bangumi 篇章<span class="mikan-entry-association-state">{{ bangumiID ? `已关联 ${bangumiID}` : bangumiID === null && bangumiLinks.length > 1 ? '多个篇章待选择' : '暂未关联' }}</span></summary>
          <p class="anime-context">默认使用原站关联的篇章，可修改或取消；TMDB 命名单独配置。</p>
          <fieldset v-if="bangumiLinks.length" class="mikan-entry-hints">
            <legend class="sr-only">选择 Bangumi 篇章</legend>
            <div v-for="id in bangumiLinks" :key="id" class="mikan-entry-hint-row"><label><input v-model="bangumiID" type="radio" :name="choiceName" :value="id" />Bangumi {{ id }}</label><a :href="`https://bgm.tv/subject/${id}`" target="_blank" rel="noopener noreferrer" :aria-label="`在新窗口核对 Bangumi ${id}`"><IconExternalLink :size="14" aria-hidden="true" /></a></div>
            <label><input v-model="bangumiID" type="radio" :name="choiceName" value="" />暂不关联</label>
          </fieldset>
          <button v-if="bangumiLinks.length && !manualAssociation" type="button" class="mikan-entry-link" @click="editAssociation">手动填写其他篇章</button>
          <template v-if="manualAssociation || !bangumiLinks.length"><label class="mikan-entry-label" for="mikan-bangumi-id">Bangumi 篇章 ID</label><input id="mikan-bangumi-id" v-model.trim="bangumiID" class="mikan-entry-input" inputmode="numeric" maxlength="12" placeholder="可留空" :aria-invalid="!validID" :aria-describedby="!validID ? 'mikan-bangumi-error' : undefined" /><p v-if="!validID" id="mikan-bangumi-error" role="alert" class="anime-context">请输入有效的数字 ID，或留空。</p></template>
        </details>
      </template>
    </div>
    <template #footer><RouterLink v-if="entry && validID" :to="target" class="anime-search-submit mikan-entry-continue">选择字幕组并配置订阅</RouterLink><p v-else-if="entry && !validID" role="alert" class="anime-context">请修正 Bangumi ID，或留空后继续。</p><p v-else class="anime-context">读取条目后继续选择字幕组与规格。</p></template>
  </AnimeFloatingDialog>
</template>

<style scoped>
.mikan-entry-dialog h3 { font-size: 20px; font-weight: 600; line-height: 1.6; overflow-wrap: anywhere; }
.mikan-entry-link { display: inline-flex; min-height: 40px; align-items: center; gap: 6px; color: #3b82f6; font-size: 12px; }
.mikan-entry-label { display: block; font-size: 13px; font-weight: 500; margin-top: 18px; }
.mikan-entry-groups { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 10px; }
.mikan-entry-groups span { padding: 7px 10px; font-size: 12px; border-radius: 9px; background: rgb(148 163 184 / .1); }
.mikan-entry-association { margin-top: 24px; color: var(--floating-surface-muted); font-size: 12px; }
.mikan-entry-association summary { cursor: pointer; padding-block: 12px; }
.mikan-entry-association-state { margin-left: 10px; font-size: 11px; }
.mikan-entry-hint-row { display: flex; align-items: center; gap: 8px; }
.mikan-entry-hints { display: grid; gap: 4px; margin-top: 12px; }
.mikan-entry-hints label { display: flex; min-height: 40px; align-items: center; gap: 8px; }
.mikan-entry-hints a { display: inline-flex; padding: 10px; }
.mikan-entry-input { display: block; width: 100%; margin-top: 8px; min-height: 44px; padding: 0 12px; background: rgb(148 163 184 / .08); border-radius: 10px; }
.mikan-entry-continue { display: flex; align-items: center; justify-content: center; text-align: center; }
.mikan-entry-input:focus-visible, .mikan-entry-association summary:focus-visible, a:focus-visible { outline: 2px solid #60a5fa; outline-offset: 3px; }
</style>
