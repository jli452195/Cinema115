<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { IconCheck, IconDownload } from '@tabler/icons-vue'
import SelectMenu from './SelectMenu.vue'
import { useToast } from '../composables/useToast'
import type { ShareTransferController } from '../composables/useShareTransfer'
import type { ShareTransferLink, ShareTransferResource, ShareTransferTarget } from '../lib/share-transfer'
const props = defineProps<{ transfer: ShareTransferController; resource: ShareTransferResource; disabled?: boolean }>()
const toast = useToast()
const resolving = ref(false), choices = ref<ShareTransferLink[]>([]), choice = ref('')
const submitting = ref(false)
const snapshot = ref<ShareTransferTarget>()
const activeID = ref('')
const status = computed(() => activeID.value ? props.transfer.status(activeID.value, snapshot.value) : undefined)
const sameTarget = computed(() => snapshot.value?.id === props.transfer.target.value?.id && snapshot.value?.targetCID === props.transfer.target.value?.targetCID && snapshot.value?.accountId === props.transfer.target.value?.accountId)
const blocked = computed(() => props.disabled || resolving.value || submitting.value || !props.transfer.target.value || choices.value.length <= 1 && sameTarget.value && !!status.value && status.value.state !== 'failed')
const labels: Record<string, string> = { pending: '转存中…', success: '已转存', uncertain: '待核对', failed: '重试转存' }
const label = computed(() => resolving.value ? '获取链接…' : submitting.value ? '转存中…' : choices.value.length > 1 || !sameTarget.value ? '115 转存' : labels[status.value?.state || ''] || '115 转存')
const completed = computed(() => sameTarget.value && status.value?.state === 'success')
const hint = computed(() => (sameTarget.value || submitting.value) && status.value ? status.value.message : props.transfer.target.value ? '转存到：' + props.transfer.target.value.targetPath : '请先选择转存目录')
watch(() => props.transfer.target.value, () => { choices.value = []; choice.value = '' })
async function start() {
  if (blocked.value) return
  snapshot.value = { ...props.transfer.target.value! }
  resolving.value = true; choices.value = []; choice.value = ''
  try {
    const links = await props.resource.load()
    resolving.value = false
    if (!links.length) throw new Error('该资源没有可转存的 115 分享链接')
    if (links.length === 1) await execute(links[0]!, snapshot.value)
    else choices.value = links
  } catch (reason) { toast.error(reason instanceof Error ? reason.message : '获取分享链接失败', { title: '转存失败' }) }
  finally { resolving.value = false }
}
async function choose(value: string) {
  if (submitting.value) return
  choice.value = value
  const link = choices.value[Number(value)]
  if (!link || !props.transfer.target.value) return
  await execute(link, { ...props.transfer.target.value })
}
async function execute(link: ShareTransferLink, selected: ShareTransferTarget) {
  snapshot.value = selected
  activeID.value = props.transfer.linkID(link)
  submitting.value = true
  try { await props.transfer.submit(link, selected) }
  catch (reason) { toast.error(reason instanceof Error ? reason.message : '转存失败', { title: '转存失败' }) }
  finally { submitting.value = false }
}
</script>

<template>
  <div class="transfer-action">
    <span class="transfer-control" :title="hint"><button type="button" class="resource-action-button resource-secondary-button" :class="{ 'is-complete': completed }" :disabled="blocked" :aria-busy="resolving || submitting" @click="start"><component :is="completed ? IconCheck : IconDownload" :size="16" aria-hidden="true" /><span class="transfer-label" aria-live="polite">{{ label }}</span></button></span>
    <SelectMenu v-if="choices.length" :model-value="choice" :disabled="submitting" :options="choices.map((link, i) => ({ value: String(i), label: '分享 ' + (i + 1) + ' · ' + link.url }))" label="选择要转存的分享" placeholder="选择要转存的分享" density="compact" @update:model-value="choose" />
  </div>
</template>

<style scoped>
.transfer-action { display: inline-flex; flex-direction: column; align-items: stretch; gap: 6px; max-width: 240px; min-width: 0; }
.transfer-control { display: inline-flex; }
.transfer-control button { width: 100%; white-space: nowrap; }
.transfer-label { min-width: 5em; text-align: center; }
</style>
