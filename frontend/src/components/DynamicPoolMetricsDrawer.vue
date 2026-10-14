<script setup lang="ts">
import { computed } from 'vue'
import type { DynamicPoolSnapshot } from '../types/dynamicPool'
import { poolHealthLabel } from '../types/dynamicPool'
import P115SidePanel from './P115SidePanel.vue'
const props = defineProps<{ snapshot: DynamicPoolSnapshot; refreshError: string; updatedAt: number }>()
defineEmits<{ close: [] }>()
const ratio = (n: number | undefined, total: number) => total ? `${((n || 0) / total * 100).toFixed(1)}%（${n || 0}/${total}）` : '暂无样本'
const groups = computed(() => {
  const m = props.snapshot.metrics, a = props.snapshot.api
  return [
    { title: '取链与容量事件', note: '失败包含业务准入拒绝，不等同于 115 上游故障。', rows: [
      ['成功 / 失败', `${m.succeeded} / ${m.failed}`], ['p50 / p95', m.samples ? `${m.p50Ms} / ${m.p95Ms} ms` : '暂无样本'],
      ['尝试切换次数', m.switches], ['故障换 UID 请求比例', ratio(m.switchedRequests, m.requests)],
      ['设备冲突 / 容量拒绝 / 容量转交', `${m.deviceConflicts || 0} / ${m.capacityDenials || 0} / ${m.capacityTransfers || 0}`], ['运行期单 UID 峰值占用', m.peakUidSessions],
    ] },
    { title: '解析与副本复用', note: '复用率分母为成功副本路径，包含直链缓存命中。', rows: [
      ['成功解析', m.resolved || 0], ['副本路径', m.copyResolutions || 0], ['已有副本复用率', ratio(m.copyReuses, m.copyResolutions)], ['直链缓存命中', m.directLinkHits || 0], ['源盘 API 放行', a.sourceRequests || 0],
    ] },
    { title: '115 API 预算', rows: [['放行请求', a.requests], ['收到 429', a.throttled], ['队列拒绝', a.rejected], ['当前等待', a.waiting], ['累计等待', `${a.waitMs} ms`]] },
    ...(a.stages ? [{ title: 'API 阶段', note: '统计为预算放行次数，不包含 CDN 读取。', rows: [
      ['凭据检查', a.stages.credential_check || 0], ['文件查询', a.stages.file_lookup || 0], ['取链', a.stages.direct_link || 0], ['秒传提交', a.stages.instant_upload || 0], ['目录写入', a.stages.directory_write || 0], ['其他元数据', (a.stages.metadata || 0) + (a.stages.other || 0)],
    ] }] : []),
    ...(a.origins ? [{ title: 'API 来源', rows: [['源盘', a.origins.source || 0], ['校验副本', a.origins.donor || 0], ['目标', a.origins.target || 0], ['后台探测', a.origins.probe || 0]] }] : []),
  ]
})
const events = computed(() => [...(props.snapshot.events || [])].reverse().slice(0, 10))
</script>
<template>
  <P115SidePanel title="运行详情" description="查看本次运行指标和近期健康事件。" @close="$emit('close')">
    <p v-if="refreshError" role="alert" class="mb-4 rounded-lg bg-amber-50 p-3 text-xs leading-5 text-amber-800 dark:bg-amber-950/30 dark:text-amber-300">状态刷新失败，显示上次成功更新的数据：{{ refreshError }}</p>
    <div class="mb-6 rounded-lg bg-slate-50 p-3 text-xs leading-6 text-slate-500 dark:bg-slate-800/60 dark:text-slate-400">指标从服务本次启动开始累计，重启后重新统计。耗时保留最近最多 256 个完成请求，包含失败和重试，不代表播放器起播时间。<p v-if="snapshot.metrics.startedAt">本次运行开始：{{ new Date(snapshot.metrics.startedAt).toLocaleString() }}</p></div>
    <section v-for="group in groups" :key="group.title" class="mb-6">
      <h3 class="mb-2 text-sm font-semibold text-slate-900 dark:text-slate-100">{{ group.title }}</h3>
      <dl class="text-xs"><div v-for="[label, value] in group.rows" :key="label" class="flex items-baseline justify-between gap-4 border-b border-slate-100 py-2.5 dark:border-slate-800"><dt class="text-slate-500 dark:text-slate-400">{{ label }}</dt><dd class="shrink-0 text-right tabular-nums">{{ value }}</dd></div></dl>
      <p v-if="group.note" class="mt-2 text-xs leading-5 text-slate-500 dark:text-slate-400">{{ group.note }}</p>
    </section>
    <section aria-label="最近健康事件"><h3 class="mb-2 text-sm font-semibold text-slate-900 dark:text-slate-100">最近健康事件</h3><p v-if="!events.length" class="text-xs text-slate-500 dark:text-slate-400">暂无健康状态变化事件</p><ol v-else><li v-for="event in events" :key="event.id" class="border-b border-slate-100 py-3 text-xs dark:border-slate-800"><time class="text-slate-500 dark:text-slate-400">{{ new Date(event.at).toLocaleString() }}</time><p class="mt-1">{{ snapshot.accounts.find(a => a.id === event.accountId)?.name || '已移除的小号' }}：{{ poolHealthLabel(event.state) }}</p></li></ol></section>
    <template #footer><span class="mr-auto text-xs text-slate-500 dark:text-slate-400">上次更新 {{ new Date(updatedAt).toLocaleTimeString() }}</span><button class="rounded-lg border border-slate-200 px-4 py-2 text-sm dark:border-slate-600" @click="$emit('close')">关闭</button></template>
  </P115SidePanel>
</template>
