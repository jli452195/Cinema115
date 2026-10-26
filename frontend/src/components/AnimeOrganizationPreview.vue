<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { IconArrowDown } from '@tabler/icons-vue'
import { animeConfigMessage, animeConfigRequest } from '../lib/anime-config-api'
import type { AnimeOrganizationPreview } from '../types/animeSubscription'
const props = defineProps<{ token: string; path: string; disabled?: boolean }>()
const result = ref<AnimeOrganizationPreview>(), loading = ref(false), error = ref('')
const controller = new AbortController()
async function read() {
  if (loading.value || props.disabled) return
  loading.value = true; error.value = ''
  try { result.value = await animeConfigRequest<AnimeOrganizationPreview>(props.token, props.path, { signal: controller.signal }) }
  catch (reason) { if (!controller.signal.aborted) error.value = animeConfigMessage(reason) }
  finally { loading.value = false }
}
const labels: Record<string, string> = { temporary_recycled: '未整理，已随临时目录回收', temporary_cleanup_uncertain: '未整理，临时目录清理待确认', existing_target: '已有同名文件，已跳过', moved: '已移动', execution_conflict: '目录或来源名称冲突，等待处理', batch_not_selected: '不整理，随临时目录清理', batch_file_unresolved: '未找到唯一对应的合集文件', mapped: '可按计划整理', subtitle_matched: '字幕已配对', unmapped: '编号尚未映射', ignored: '按规则保留', subtitle_unmatched: '字幕未找到唯一对应视频', video_needs_review: '先确认对应视频', attachment_preserved: '附件保留原处', duplicate_target: '目标路径重复，不覆盖', name_too_long: '名称过长，需要调整', naming_invalid: '命名不符合模板，请调整' }
onBeforeUnmount(() => controller.abort())
</script>
<template>
  <div class="as-organization-preview" :aria-busy="loading">
    <button type="button" class="as-text-button" :disabled="loading || disabled" @click="read">{{ loading ? '正在生成预览…' : result ? '重新读取整理预览' : '查看整理预览' }}</button>
    <p v-if="error" class="as-muted" role="status">{{ error }}{{ result ? '，上次预览已保留。' : '' }}</p>
    <div v-if="result" class="as-plan-items">
      <p class="as-muted">依据任务保存的文件清单与命名方案，不查询文件当前位置。</p>
      <p class="as-muted">命名计划与执行结果取自本次读取的本地记录。执行时已有同名文件跳过，其余继续，不覆盖文件。</p>
      <p v-if="!result.items.length" class="as-muted">{{ result.mode === 'original' ? '尚未保存整理计划，请先补齐 TMDB 命名目标。' : '还没有可用的文件清单。' }}</p>
      <article v-for="item in result.items" :key="item.id" class="as-plan-item" :data-plan-status="item.status">
        <span class="as-plan-status">{{ item.error || labels[item.reason] || '需要确认' }}</span>
        <p><small>任务保存的原路径</small>{{ item.originalPath }}</p>
        <template v-if="item.targetPath"><IconArrowDown :size="14" aria-hidden="true" /><p><small>目标路径</small>{{ item.targetPath }}</p></template>
      </article>
    </div>
  </div>
</template>
