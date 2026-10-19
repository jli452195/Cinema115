<script setup lang="ts">
import { useAnimeSubscriptionActions } from '../composables/useAnimeSubscriptionActions'
import type { AnimeDownloadJob } from '../types/animeSubscription'
const props = defineProps<{ token: string; subscriptionId: string; job: AnimeDownloadJob; locked: boolean }>()
const emit = defineEmits<{ changed: [] }>()
const { loading, locked: busy, error, notice, pending, execute, confirmPending } = useAnimeSubscriptionActions(props.token, props.subscriptionId, async () => { emit('changed') }, `job:${props.job.id}`)
function act(action: 'reconcile' | 'retry') {
  void execute(`jobs/${encodeURIComponent(props.job.id)}/${action}`, { expectedRevision: props.job.revision }, action === 'reconcile' ? '安排核对' : '安排重试')
}
</script>
<template>
  <div v-if="job.permittedActions?.reconcile || job.permittedActions?.retry || pending || error || notice">
    <div class="as-actions"><button v-if="job.permittedActions?.reconcile" type="button" class="as-action" :disabled="locked || busy" @click="act('reconcile')">{{ job.organizationPlanned && job.fileState === 'verified' ? '核对整理结果' : '核对原任务' }}</button><button v-if="job.permittedActions?.retry" type="button" class="as-action" :disabled="locked || busy" @click="act('retry')">{{ job.downloadState === 'queued' ? '继续准备下载' : '重试读取' }}</button></div>
    <div class="as-operation-feedback" role="status"><p v-if="error">{{ error }}</p><p v-else-if="loading">正在安排…</p><p v-else-if="notice">{{ notice }}，后台会沿用原任务处理。</p><template v-if="pending && !loading"><p>安排结果待确认，已保留原请求。</p><button type="button" class="as-text-button" @click="confirmPending">核对原安排</button></template><button v-if="error && !pending" type="button" class="as-text-button" @click="emit('changed')">刷新本地记录</button></div>
  </div>
</template>
