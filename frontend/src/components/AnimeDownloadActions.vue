<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useAnimeSubscriptionActions } from '../composables/useAnimeSubscriptionActions'
import type { AnimeDownloadJob } from '../types/animeSubscription'
const props = defineProps<{ token: string; subscriptionId: string; job: AnimeDownloadJob; locked: boolean }>()
const emit = defineEmits<{ changed: [] }>()
const { loading, locked: busy, error, notice, pending, execute, confirmPending } = useAnimeSubscriptionActions(props.token, props.subscriptionId, async () => { emit('changed') }, `job:${props.job.id}`)
const feedback = ref<HTMLElement>()
const retryLabel = computed(() => props.job.organizationPlanned && props.job.fileState === 'verified' ? props.job.blockCode === 'ORGANIZE_TARGET_CONFLICT' ? '继续整理' : '重新整理' : props.job.downloadState === 'queued' ? '继续准备下载' : '重试读取')
async function keepActionFocus(action: () => Promise<void>) {
  const trigger = document.activeElement
  await action()
  await nextTick()
  if (trigger && !trigger.isConnected && document.activeElement === document.body) feedback.value?.focus({ preventScroll: true })
}
function act(action: 'reconcile' | 'retry') {
  void keepActionFocus(() => execute(`jobs/${encodeURIComponent(props.job.id)}/${action}`, { expectedRevision: props.job.revision }, action === 'reconcile' ? '安排核对' : props.job.organizationPlanned && props.job.fileState === 'verified' ? `安排${retryLabel.value}` : '安排重试'))
}
</script>
<template>
  <div v-if="job.permittedActions?.reconcile || job.permittedActions?.retry || pending || error || notice">
    <div class="as-actions"><button v-if="job.permittedActions?.reconcile" type="button" class="as-action" :disabled="locked || busy" @click="act('reconcile')">{{ job.organizationPlanned && job.fileState === 'verified' ? '核对整理结果' : '核对原任务' }}</button><button v-if="job.permittedActions?.retry" type="button" class="as-action" :disabled="locked || busy" @click="act('retry')">{{ retryLabel }}</button></div>
    <div ref="feedback" class="as-operation-feedback" role="status" tabindex="-1"><p v-if="error">{{ error }}</p><p v-else-if="loading">正在安排…</p><p v-else-if="notice">{{ notice }}，后台会沿用原任务处理。</p><template v-if="pending && !loading"><p>安排结果待确认，已保留原请求。</p><button type="button" class="as-text-button" @click="keepActionFocus(confirmPending)">核对原安排</button></template><button v-if="error && !pending" type="button" class="as-text-button" @click="emit('changed')">刷新本地记录</button></div>
  </div>
</template>
